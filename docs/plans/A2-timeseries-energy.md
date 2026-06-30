# Implementation plan — A2: Energy view + history rollup

**Status:** prioritised, not started.
**Audience:** a fresh Claude-Opus environment with no access to the
review conversation. Everything needed is below; verify each path
against the tree before editing (line numbers drift).

This plan delivers **two** things the operator asked for together:

1. **Energy/consumption view** — a new SPA route that aggregates
   power/energy over time (per-device breakdown, daily/monthly totals),
   built on the existing history backend + chart components.
2. **History rollup / downsampling** — persistent low-resolution
   aggregate tables + tiered retention, so long-term history stays cheap
   (today there is only a raw table + a retention purge + query-time
   bucketing).

Both rest on the measurement-history subsystem introduced by
[ADR 0040](../adr/0040-measurement-history.md). History is **opt-in**
(`persistence.history.enabled` defaults to false); everything here is
gated the same way.

---

## 1. Current state (verified)

### Backend

| Concern | Location | Notes |
|---|---|---|
| Dedicated DB | `internal/store/sqlite/measurements.go` — `OpenHistory(ctx, dsn)`, `migrateHistory(ctx, db)` | History lives in its own `history.db`, separate migration series, so an append-heavy writer never contends with config writes. |
| Migration series | `internal/store/sqlite/migrations_history/001_measurements.sql` | Single table `measurements(central_name, interface_id, channel_address, parameter, ts INTEGER /*epoch ms*/, value REAL)`, `PRIMARY KEY(central_name, interface_id, channel_address, parameter, ts) WITHOUT ROWID`. |
| Store type | `MeasurementStore` (`NewMeasurementStore(db)`) | Methods: `SaveBatch(ctx, []MeasurementSample)`, `QueryBuckets(ctx, central, iface, channel, parameter, from, to, buckets) ([]MeasurementBucket, error)`, `DeleteOlderThan(ctx, cutoff) (int64, error)`, `DeleteDevice(...)`, `DeleteAll(ctx)`, `Stats(ctx)`, `MetricsSnapshot()`. |
| Row / bucket DTOs | same file | `MeasurementSample{CentralName, InterfaceID, ChannelAddress, Parameter, TS time.Time, Value float64}`; `MeasurementBucket{TS time.Time, Avg, Min, Max float64, Count int64}`. |
| Aggregation | `QueryBuckets` SQL | `SELECT CAST((ts-?)/? AS INTEGER) bucket, AVG(value), MIN(value), MAX(value), COUNT(*) ... GROUP BY bucket`. Query-time only — **no persisted aggregate**. |
| Recorder | `internal/history/recorder.go` — `New(store, Options)`, `Wire(reg *central.Registry) func()` | Subscribes to `hmevent.DataPointValueChangedEvent` (`recorder.go:158`), buffers `MeasurementSample`, and owns its **own goroutine** `loop` (`recorder.go:189`) that periodically `flush`es the buffer and `purge`s via `DeleteOlderThan` (`recorder.go:316`). `numericValue` records only Int/Float values. |
| Recorder options | `Options{EnabledFor, Include, Exclude, FlushInterval, Retention, MaxBuffer, Logger, Exporter}` | Defaults: flush 5 s, retention 720 h (30 d). |
| REST | `internal/north/rest/handlers/history.go` | `HistoryService interface { Query(ctx, HistoryQuery) ([]HistoryBucket, error) }`; `HistoryBucket{TS,Avg,Min,Max,Count}` (JSON `ts/avg/min/max/count`); `HistoryQuery{Central, InterfaceID, ChannelAddress, Parameter, From, To, Buckets}`; buckets default 200, max 2000. |
| Route | `internal/north/rest/router.go:108` (`History` dep) + `:651` (`if d.History != nil { pr.Get("/history", handlers.GetHistory(d.History)) }`) | Nil when the feature is off → handler returns 404, the SPA maps it to `HistoryNotEnabledError`. |
| cmd wiring | `cmd/openccu-loom/history_wiring.go` (`wireHistoryStore`, gated on `cfg.Persistence.History.HistoryFeatureEnabled()`), `cmd/openccu-loom/history_handler_adapter.go` (`newHistoryHandlerAdapter(store) HistoryService` → `QueryBuckets`), `cmd/openccu-loom/daemon_infra.go:44` (`historyStore *sqlite.MeasurementStore`). | |
| Config | `internal/config/config.go` — `PersistenceConfig.History HistoryConfig` (`persistence.history`) | Fields (all `cfg:"expert"`): `Enabled *bool`, `Retention time.Duration`, `FlushInterval`, `Include []string`, `Exclude []string`, `DisabledCentrals []string`, `Export HistoryExportConfig`. Has `HistoryFeatureEnabled()`. |
| Scheduler | `internal/scheduler/scheduler.go` | `Job{Name, Interval time.Duration, Run JobFunc, RunOnStart bool, OnStart, OnComplete}`; `(*Scheduler).Add(Job) error`. Note: the history recorder does **not** use this scheduler — it runs its own loop. |

### Power / energy data points (verified)

Identified purely by **parameter name** (`pkg/hmenum/parameter.go`):

- `ParameterPower = "POWER"` — **instantaneous** load in W.
- `ParameterEnergyCounter = "ENERGY_COUNTER"` — **cumulative monotonic**
  meter in Wh (consumed).
- `ParameterEnergyCounterFeedIn = "ENERGY_COUNTER_FEED_IN"` — cumulative
  meter in Wh (fed back into the grid).

(Related: `pkg/hmenum/field.go` `FieldPower`/`FieldEnergyCounter`,
`pkg/hmenum/quantity.go` `QuantityPower`.) Per-device aggregation walks
the device's channels and filters data points by these parameter names.

### SPA

- `assets/ui/src/lib/api/client.ts` — `HistoryBucket` type +
  `getHistory({central, interface_id, channel, parameter, from, to, buckets})`,
  throws `HistoryNotEnabledError` on 404.
- `assets/ui/src/lib/components/HistoryChart.svelte` — reusable chart
  (already consumes `HistoryBucket[]`).
- `assets/ui/src/lib/control/widgets/Powermeter.svelte` — existing
  power widget.
- `assets/ui/src/App.svelte` — minimal **hash router**: a `route`
  `$derived` switch (`App.svelte:131+`) maps `#/<path>` → a `Route`
  kind; render block below. Default route `#/devices`.
- `assets/ui/src/lib/components/ui/Sidebar.svelte` — nav clusters
  (`overview` / `automation` / `diagnose`), each item `{ href: "#/...",
  label: t("nav.<x>") }`.
- `assets/ui/src/lib/i18n.ts` — `EN` and `DE` catalogues.

---

## 2. Design decisions

### Rollup schema

Add two persisted aggregate tiers alongside the raw table. **Resolutions:**
`raw` (existing) → **hourly** → **daily**. Two new tables, both scoped by
the same multi-CCU key (`central_name, interface_id, channel_address,
parameter`) per ADR 0002:

```
measurements_hourly(central_name, interface_id, channel_address, parameter,
                    bucket_ts INTEGER,  -- epoch ms, truncated to hour start
                    sum REAL, min REAL, max REAL, count INTEGER,
                    first REAL, last REAL,
                    PRIMARY KEY(central_name, interface_id, channel_address, parameter, bucket_ts)
) WITHOUT ROWID;
measurements_daily(... same columns, bucket_ts truncated to UTC day start ...)
```

Why these aggregates:
- `sum`+`count` make `avg` exact and let hourly→daily re-rollup stay
  exact (never average-of-averages).
- `min`/`max` preserve the existing `MeasurementBucket` contract (peaks).
- `first`/`last` are **required for cumulative counters**: a bucket's
  energy consumption = `last - first` (see below). Instantaneous params
  ignore them.

### Power vs. energy (the key distinction)

- **`POWER` (instantaneous, W):** aggregate with `avg` (mean load) and
  `max` (peak). Never summed.
- **`ENERGY_COUNTER` (cumulative, Wh):** consumption over a bucket =
  `last − first`. **Counter-reset edge case:** the meter resets to 0 on
  device re-pair / firmware events, so `last < first` is possible. v1
  rule: if `last < first`, treat the bucket as a reset and use
  `consumption = last` (energy since reset) and set a `reset` flag in the
  response; do **not** emit a negative delta. Document this in the
  handler.
- **`ENERGY_COUNTER_FEED_IN`:** identical delta logic, reported as a
  separate `feed_in_wh` series.

Output unit: store Wh internally; the energy endpoint returns Wh and the
SPA renders kWh (`/1000`).

### Retention tiers

Add two config fields next to the existing `Retention`:

- `Retention` (raw) — keep default 720 h / 30 d.
- `RetentionHourly` — default e.g. 13 months (`13*30*24h`); 0 → default.
- `RetentionDaily` — default 0 = **keep forever** (daily rows are tiny).

Rollup must run **before** raw purge so no raw row is deleted before it
is folded into the hourly tier.

### Where the rollup runs

The recorder already owns a goroutine `loop` that flushes + purges with
the history DB handle in scope. **Decision (diverges from the original
"add a `scheduler.Job`" suggestion, with rationale):** add the rollup
step into `recorder.loop` on an hourly ticker rather than a
`scheduler.Job`. Reason: cohesion (all history-write concerns in one
place, same DB handle, no cross-package wiring) and ordering (rollup
must precede purge — trivial inside the same loop, racy across two
schedulers). If operators later want it visible in the jobs list /
`scheduler.failures` gauge, expose a thin `scheduler.Job` wrapper then;
not needed for v1.

### Energy aggregation API

New endpoint, additive, gated like `/history`:

```
GET /api/v1/energy
  ?central=<name>            (required)
  ?from=<RFC3339>&to=<RFC3339>   (required)
  ?group=hour|day|month      (default day)
  ?device=<address>          (optional; omitted = all energy devices on the central)
```

Response DTO (`EnergyResponse`):

```jsonc
{
  "group": "day",
  "from": "...", "to": "...",
  "devices": [
    {
      "address": "00021BE9957782",
      "name": "Bücherregal",
      "buckets": [
        { "ts": "...", "consumed_wh": 412.0, "feed_in_wh": 0.0,
          "avg_power_w": 18.2, "peak_power_w": 240.0, "reset": false }
      ],
      "total_consumed_wh": 9123.0,
      "total_feed_in_wh": 0.0
    }
  ],
  "total_consumed_wh": 21000.0,
  "total_feed_in_wh": 0.0
}
```

The handler reads from the **daily/hourly rollup** tables (cheap) and
maps device names via the existing device registry adapter. `group=month`
re-aggregates daily rows in SQL.

---

## 3. Implementation steps

### Step 1 — DB migration + store methods (rollup foundation)

1. Create `internal/store/sqlite/migrations_history/002_rollups.sql`
   (goose `-- +goose Up` / `-- +goose Down`) with the two tables above.
   Mirror the comment style of `001_measurements.sql`.
2. In `internal/store/sqlite/measurements.go` add:
   - `RollupHourly(ctx, olderThan time.Time) (int64, error)` — folds raw
     rows with `ts < olderThan` into `measurements_hourly` via
     `INSERT ... ON CONFLICT(...) DO UPDATE` (idempotent re-run safe).
     Compute `bucket_ts = ts - (ts % 3600000)`. Aggregate
     `SUM/MIN/MAX/COUNT` and `first/last` (use
     `value` at `MIN(ts)`/`MAX(ts)` per bucket — a correlated subquery or
     window function; SQLite via modernc supports window functions).
   - `RollupDaily(ctx, olderThan)` — folds `measurements_hourly` rows
     into `measurements_daily` (re-aggregate: `sum=Σsum`, `count=Σcount`,
     `min=MIN(min)`, `max=MAX(max)`, `first=first of earliest bucket`,
     `last=last of latest bucket`). Day boundary = UTC midnight.
   - `DeleteHourlyOlderThan(ctx, cutoff)`, `DeleteDailyOlderThan(ctx, cutoff)`.
   - `QueryEnergy(ctx, central string, deviceAddr string /*""=all*/, from, to time.Time, group string) (...)` — reads the rollup tier matching `group` and returns per-(channel,parameter) buckets the handler folds into per-device totals. Filter `parameter IN ('POWER','ENERGY_COUNTER','ENERGY_COUNTER_FEED_IN')`.
   - Keep all queries scoped by `central_name` (+ optional device prefix
     on `channel_address`).

### Step 2 — Recorder rollup loop

1. In `internal/history/recorder.go`, add a `rollup(ctx)` method that
   calls `RollupHourly(now-1h)` → `RollupDaily(now-1d)` →
   `DeleteHourlyOlderThan(now-RetentionHourly)` →
   `DeleteDailyOlderThan` (if `RetentionDaily>0`). Then the existing raw
   `purge` runs (already present), now guaranteed to run *after* the
   hourly rollup.
2. Add an hourly ticker to `recorder.loop` (`recorder.go:189`) that
   invokes `rollup`. Reuse the existing ticker/loop structure; keep the
   flush ticker as-is.
3. Add `RetentionHourly`/`RetentionDaily` to `Options` and thread them
   from config (Step 4).

### Step 3 — Energy REST endpoint

1. New handler file `internal/north/rest/handlers/energy.go`:
   `EnergyService interface { Energy(ctx, EnergyQuery) (EnergyResponse, error) }`,
   `GetEnergy(svc EnergyService) http.HandlerFunc`. Parse + validate
   query params (mirror `parseHistoryQuery` style; `problem.Write` for
   errors). Apply the counter-reset rule here.
2. Define DTOs (`EnergyQuery`, `EnergyResponse`, `EnergyDevice`,
   `EnergyBucket`) in the handler package (mirroring `HistoryBucket`).
3. Route: add an `Energy handlers.EnergyService` dep field next to
   `History` (`router.go:108`) and register
   `if d.Energy != nil { pr.Get("/energy", handlers.GetEnergy(d.Energy)) }`
   next to the history route (`router.go:651`).
4. cmd: add `cmd/openccu-loom/energy_handler_adapter.go`
   (`newEnergyHandlerAdapter(store, deviceRegistryAdapter) EnergyService`),
   resolving device names via the registry. Wire it in the same place
   `History` is wired (gated on `HistoryFeatureEnabled()`).

### Step 4 — Config

1. Add to `HistoryConfig` (`internal/config/config.go`):
   `RetentionHourly time.Duration` (yaml `retention_hourly`),
   `RetentionDaily time.Duration` (yaml `retention_daily`), both
   `cfg:"expert"`, both `omitempty`.
2. Thread them into `history.Options` in `wireHistoryStore`
   (`cmd/openccu-loom/history_wiring.go`).

### Step 5 — SPA energy view

1. `assets/ui/src/lib/api/client.ts`: add `EnergyResponse`/`EnergyBucket`
   types + `getEnergy({central, from, to, group, device})`; reuse the
   `HistoryNotEnabledError` 404 mapping.
2. New route `assets/ui/src/routes/Energy.svelte`:
   - Central selector + time-range + group (hour/day/month) controls.
   - Reuse `HistoryChart.svelte` for the consumption series; a per-device
     breakdown table (use the shared `DataTable` component) with
     consumed/feed-in totals (Wh→kWh).
   - **Operating concept (mandatory):** `LoadingState` / `EmptyState`
     (no energy devices / feature off) / `ErrorState`; all strings via
     `t(...)`; every colour utility carries a `dark:` variant.
3. Router: add `import Energy` + a `#/energy` → `{ kind: "energy" }`
   branch in `App.svelte` (`:131+`) and a render arm; extend the
   `<title>` switch.
4. Sidebar: add a nav item under the `overview` (or a new `analyse`)
   cluster: `{ href: "#/energy", label: t("nav.energy") }`.
5. i18n: add `nav.energy`, `page.title.energy`, and all
   `energy.*` view strings to **both** `EN` and `DE` in `i18n.ts`.

---

## 4. Config & API contract changes (build-gating tests)

These are hard build gates — skip one and `make test` fails:

1. **Every new `cfg:`-tagged field needs a label AND help in EN + DE.**
   For `retention_hourly` and `retention_daily` add to *both* catalogues
   in `assets/ui/src/lib/i18n.ts`:
   - `config.field.persistence.history.retention_hourly`
   - `config.help.persistence.history.retention_hourly`
   - `config.field.persistence.history.retention_daily`
   - `config.help.persistence.history.retention_daily`
   Enforced by `TestConfigFieldsHaveLabelsAndHelp` (in `tests/contract/`),
   which lists every missing `EN`/`DE` × `field`/`help` entry.
2. **OpenAPI + WS schema digest.** Add `/api/v1/energy` (and the
   `EnergyResponse` schema) to `assets/openapi.yaml` **first**, then run
   `make export-schemas` to refresh the committed digest, **and** bump
   `APIVersion` (the PR-only "api contract guard" fails otherwise). See
   the API-contract checklist in `CLAUDE.md` → Common Tasks.
3. Regenerate the SPA's `types.generated.ts` from the OpenAPI spec via
   the repo's generation step (check `assets/ui/package.json` scripts;
   the `HistoryBucket` type is already generated this way).

---

## 5. Tests

- **Store (`measurements_test.go`, extend the existing file — do NOT
  create `*_rollup_coverageN`):** rollup correctness (raw→hourly→daily
  sums/min/max/first/last exact), idempotent re-run, counter-reset
  handling, multi-CCU isolation (two centrals don't bleed), retention
  deletes only the intended tier.
- **Energy handler (`energy_test.go`):** query validation, per-device
  folding, Wh totals, reset flag, `group=month` re-aggregation, 404 when
  service nil.
- **Integration (optional, `-tags=integration`):** feed synthetic
  `ENERGY_COUNTER` samples through the recorder against godevccu, roll
  up, read `/energy`, assert kWh totals.
- **SPA:** Playwright e2e for `#/energy`
  (`assets/ui/tests/e2e/`) with mocked `/api/v1/energy`, plus
  **light + dark** visual baselines (`npm run e2e:update`); a Vitest
  component test for the breakdown table. Keep both suites green.
- Test-naming rule: name files after the unit under test
  (`measurements_test.go`, `energy_test.go`) — never `*_coverageN` /
  `*_batchN` (blocked by repo convention).

## 6. Project-rule checklist

- [ ] SPDX header on every new `.go` file (`// SPDX-License-Identifier:
      MIT` + copyright line).
- [ ] No CGo; SQLite via `modernc.org/sqlite` only.
- [ ] **Multi-CCU:** every rollup table + query scoped by
      `central_name` (and the rest of the key). No single-central
      assumption.
- [ ] `ctx context.Context` first arg on every new store/IO method.
- [ ] SPA operating concept: shared `LoadingState`/`EmptyState`/
      `ErrorState`, `toastStore` for action results, `t(...)` for all
      strings, `dark:` variants / `--ha-*` tokens.
- [ ] `CHANGELOG.md` entry (user-visible: "energy view + history
      rollup"). On a version bump, also the HA add-on changelog.

## 7. Acceptance criteria

- With `persistence.history.enabled=true`, energy data points recorded;
  after ≥1 h the hourly table populates, after ≥1 d the daily table.
- `GET /api/v1/energy?central=X&group=day` returns per-device kWh that
  reconcile with raw `ENERGY_COUNTER` deltas (within counter resolution).
- Counter resets never produce negative consumption; the `reset` flag is
  set on affected buckets.
- Raw rows older than `Retention` are gone but their energy is preserved
  in the daily tier.
- The `#/energy` SPA route renders the chart + per-device breakdown in
  light and dark, with proper loading/empty/error states; disabled
  feature shows the "history not enabled" empty state.
- `make test`, `make lint`, and `cd assets/ui && npm run e2e` pass.

## 8. Effort & sequencing

**Effort: L** (DB + recorder + REST + SPA + contract gates).
Sequence: Step 1 (DB/store) → Step 2 (recorder rollup) → Step 3 (energy
endpoint) → Step 4 (config) → Step 5 (SPA). Steps 1–2 are independently
testable before any UI work. The energy view (Steps 3+5) can ship before
deep rollup tuning, reading raw via `QueryEnergy` against the raw table
as a fallback when rollup tables are still empty.

## 9. References

- `CLAUDE.md` → **Common Tasks**: "Add a REST endpoint", "Add a new
  database table", "Add a translation key"; **Critical Rules**:
  multi-CCU, pure-Go SQLite; the **API contract change checklist**.
- `CLAUDE.md` → **Testing Guidelines** (contract/integration/e2e
  pillars, test-naming rule).
- [ADR 0040](../adr/0040-measurement-history.md) — embedded history +
  opt-in push exporter (the rollup is the "later additive step" it
  anticipates).
- `docs/caching.md` — history's place among the cache/storage layers.
