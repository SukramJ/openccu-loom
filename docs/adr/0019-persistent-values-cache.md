# ADR 0019 — Persistent VALUES cache with wire-DP lifecycle

- **Status**: accepted
- **Date**: 2026-05-23
- **Related**:
  [ADR 0002 — multi-CCU first class](./0002-multi-ccu-first-class.md),
  [ADR 0017 — logging and diagnostics](./0017-logging-and-diagnostics.md),
  [Caching Architecture](../caching.md) — full overview of every cache layer + boot-time radio cost,
  earlier MASTER-values persistence (`internal/store/sqlite/master_values.go`)

## Decision

Wire-side VALUES paramset data points (status, sensor readings,
maintenance flags) are persisted across daemon restarts in a new
SQLite table `values_cache`. Each data point grows a small lifecycle
state machine — `unobserved → cache → live → stale → live` — that
the REST surface, MQTT bridge and SPA consume so the operator sees
what is fresh, what comes from disk, and what froze when the
connection dropped.

## Context

The daemon used to lose every wire VALUES on restart. Until the
first `fetch_all_device_data` round returned, the SPA showed empty
tiles, MQTT topics held nothing (retained traffic excepted) and the
Matter bridge reported every attribute as unobserved. Cold boot
delivered "nothing" for 5–30 s; CCU + daemon restart concurrently
delivered "nothing" for a minute or more.

MASTER paramsets had already been moved to a persistent cache (see
`master_values.go` and the wizard from 2026-05-22) because the
duty-cycle research showed there is no funk-free way to refresh
them at runtime. VALUES persistence has a different motivation:
not duty-cycle, but UI / bridge surface continuity. The two caches
therefore stay separate tables with different lifecycle and
different refresh policies.

The lifecycle expectations were collected via the Wizard on
2026-05-23. Operator answers drove every decision in this ADR.

## Design

### Scope

- Wire-DP VALUES paramset + event-only parameters (UNREACH, RSSI,
  LOW_BAT etc.).
- Calculated DPs and custom DPs are *not* persisted — they are
  deterministically derived from their wire sources during the
  restore pass.
- Inbox devices (`ReadyConfig == false`) are not persisted; their
  data points do not exist in the daemon's model registry yet.
- `nil` values are not persisted — a restored data point should
  never resurrect the equivalent of a "we saw `null` last time"
  observation.

### Storage layout

Migration `016_values_cache.sql` defines the single table
`values_cache`:

```
central_name         TEXT
interface_id         TEXT
channel_address      TEXT
parameter_name       TEXT
value_json           TEXT        — JSON-encoded wire value
value_type           TEXT        — bool | int | float | string | null
last_seen_at         INTEGER     — epoch ms; every push, incl. cyclic
last_changed_at      INTEGER     — epoch ms; only on value change
cache_schema_version INTEGER     — Migration discriminator (currently 1)
PRIMARY KEY (central_name, interface_id, channel_address, parameter_name)
```

Multi-CCU partitioning follows ADR 0002: `central_name` is in the
primary key. Two centrals wired into the same daemon hold
independent rows for the same channel address.

The schema-version column lets a future migration detect rows whose
encoding the current code can no longer interpret. The restore pass
filters by version; rows of the wrong version are dropped on the
next GC and refilled on the next live event.

### Lifecycle on the wire data point

`generic.DataPoint` gains a `source` field of type
`hmenum.ValueSource`. The four states map to operator-facing
semantics:

| Source        | Meaning                                                    |
| ------------- | ---------------------------------------------------------- |
| `unobserved`  | Never seen any value — neither from disk nor from the wire. |
| `cache`       | Restored from `values_cache`; no live confirmation yet.     |
| `live`        | Last set by a push event or `fetch_all_device_data`; connection healthy. |
| `stale`       | Last `live` value frozen because the connection dropped.   |

Transitions:

- **unobserved → cache** by `DataPoint.RestoreCachedValue(value, lastSeen, lastChanged)` during the restore pass.
- **cache → live** and **live → live** by the existing `OnEvent`
  path; `live` is the steady state during healthy operation.
- **live → stale** when a `ConnectionLostEvent` for the interface
  fires. `WireValueSourceLifecycle` walks every wire DP of the
  affected interface and calls `MarkStale`. The value is preserved;
  only the source token flips.
- **stale → live** when `RecoveryCompletedEvent` for the interface
  fires. `MarkLive` runs symmetric to `MarkStale`.

Both transitions trigger the data point's
`confirmedUpdateCallbacks` so the Matter Subscribe stream and any
other subscriber sees the freshness flip without needing a separate
event channel. MQTT additionally republishes via the new
`hmevent.DataPointSourceChangedEvent` on the central bus — the
event-bridge synthesises a `DataPointValueChangedEvent` with
`OldValue == NoneValue` so the value-diff dedup downstream treats
the transition as a fresh emission.

### Persistence triggers

Two writers:

1. **Periodic flusher** (default 60 s, configurable). Walks every
   central, every channel, every wire DP whose source is `live` or
   `stale` and pushes the snapshot into the store in a single
   `SaveBatch` transaction. `cache` and `unobserved` rows are
   skipped — re-persisting a cache value with the same timestamps
   round-trips nothing useful.
2. **Graceful shutdown** flush. The closer returned by
   `WireValuesCacheFlusher` blocks until the final flush completes
   so the cache survives `kill -TERM` without losing the last
   interval's worth of updates.

There is no "flush on every change" path. The 60 s window is the
trade between SQLite write load on a 1000-DP installation and crash
data loss.

### Restore at boot

The pipeline order changes to:

```
ListDevices → Ingest (Devices/Channels) → hydrateDataPoints →
  custom DPs → suppress-undefined → calculated DPs →
  refineWeekProfiles → restoreValuesFromCache → seedValues → init()
```

The restore pass:

1. Iterates every channel of every device on the current interface.
2. Reads cached rows in one SELECT per channel.
3. For each cached parameter, looks up the wire DP via
   `Channel.Parameter(name)`. Unknown parameters are silently
   skipped (the GC will drop the dead row).
4. Calls `RestoreCachedValue` if the data point implements it,
   falling back to `OnWireValue` otherwise. Restored DPs land at
   source `cache`.

`fetch_all_device_data` runs immediately after and overwrites every
value the CCU returns; data points the CCU does not include
(sleeping battery devices, etc.) keep their cached source until a
real push event arrives.

### Conflict resolution

`fetch_all_device_data` always wins. Cache is purely the gap-filler
for the period between boot and the first live data round; it never
overrides a live value.

### Cleanup

Three paths:

- **DeleteDevice on device removal.** Same trigger as the existing
  master-values cleanup. Prefix-safe (`"DEVICE"` never deletes
  `"DEVICE2:0"`).
- **Dead-row GC** scheduled daily plus once at daemon start. Walks
  the cache, builds an `alive` set from every wire DP that survived
  the most recent hydration, and deletes the rest. Defensive: a
  `nil` alive set is a no-op so a hydration bug cannot wipe the
  cache by accident.
- **Operator-driven reset** via REST: `POST /api/v1/admin/values-cache/reset`
  (global) and `POST /api/v1/devices/{addr}/values-cache/reset`
  (per device). Both return 204 on success.

### Reporting

REST `DataPointSummary` adds four fields:

- `source`: the lifecycle token.
- `last_seen_at`: ISO-8601 timestamp, advances on cyclic info.
- `last_changed_at`: ISO-8601, advances only on actual value change.
- `value_age_seconds`: pre-computed integer for the SPA, so the
  browser does not parse the timestamp on every render.

OpenAPI documents the schema; the SPA `DataPointSummary` type
mirrors it. `ChannelStatusBadge` renders a clock-glyph plus italic
text with a `title=` tooltip for any DP whose source is `cache` or
`stale` — same visual language as the existing observed/unobserved
distinction.

Telemetry: seven gauges register against `health.Tracker`:

```
values_cache.restored_rows      values_cache.cast_failures
values_cache.gc_rows_deleted    values_cache.flush_batches
values_cache.flushed_entries    values_cache.row_count
values_cache.value_json_bytes
```

The Prometheus `/metrics` and the `/diagnostics` dump pick them up
without extra wiring.

### Configuration

The feature is opt-out. Operator can override the defaults in
`config.yaml`:

```yaml
persistence:
  values_cache:
    enabled: true
    flush_interval: 60s
    disabled_centrals: []
```

This block is reserved but not yet read by the code — the current
phase ships with hard-coded defaults. The config wiring is part of
the follow-up alongside the MQTT republish path.

## Alternatives considered

**Persistent value cache also for MASTER.** Already covered by the
separate `master_values` table (ADR not formalised). VALUES and
MASTER have different refresh strategies — keeping them in one
table would force one policy.

**Periodic refresh against the CCU after recovery.** Tried (Phase 3
of the original 2026-05-22 implementation) and reverted after a
live test showed it pushed the CCU duty-cycle over the legal limit.
The HMIPServer-bytecode analysis later showed that
`getParamset(MASTER)` itself is duty-cycle-neutral on warm CCU, but
the bulk read still adds CPU load and offers no clear benefit when
the per-DP push events already carry the truth.

**Source token on the WS protocol, not on REST.** A WS-only token
would require every consumer to track the lifecycle out of band.
The REST surface is the authoritative DP description; making the
source a REST field keeps the API single-sourced.

**Flush on every change.** Cleanest crash semantics, but a 1000-DP
installation with 30 events/s would write hundreds of inserts/min
to SQLite. The 60 s batch is a 60×-ish reduction in write load
with bounded data-loss in exchange.

## Consequences

### Positive

- The SPA renders its known state immediately on boot — no more
  blank tiles during the first `fetch_all_device_data` round.
- MQTT retained-publishes carry the last-known value across daemon
  restarts (Phase D part 2 will add the explicit republish on
  source change for non-retained consumers).
- Matter Subscribe streams see the wire DP populated before
  `init()` returns; Apple Home / Google Home / Alexa stay
  responsive across daemon restarts.
- Operators get a clear `source: stale` signal when the CCU is
  unreachable; "is this value actually fresh" is no longer
  guesswork.

### Negative

- One additional SQLite table + flusher goroutine per daemon
  process. The shared WAL DB handle keeps write contention low,
  but a wedged SQLite write would now affect both
  `master_values` and `values_cache`.
- `fetch_all_device_data` and the restore pass both apply values
  to every wire DP — the second write is redundant for parameters
  the CCU returns. Measured cost: small (the OnEvent path
  short-circuits on `valuesClose`), but real.
- Two timestamps (`last_seen_at` + `last_changed_at`) per DP plus
  the source field grow `DataPointSummary` by ~80 bytes on the
  wire. Browser-side JSON parsing on huge devices (~30 DPs per
  channel × 50 channels) still well under a millisecond.

### Implementation status

All follow-ups originally flagged on this ADR have shipped:

- **MQTT republish-on-source-change** — landed via
  `hmevent.DataPointSourceChangedEvent` (event-catalogue count
  bumped to 36). `EventBridge.onSourceChanged` subscribes the event
  and synthesises a `DataPointValueChangedEvent` with
  `OldValue == NoneValue`, so the value-diff dedup downstream
  treats the transition as a fresh emission and the existing
  Bridge.PublishState path covers it — HA-Discovery `force_update`
  was already in place.
- **`persistence.values_cache.*` config-keys** — `Config.Persistence`
  with a nested `ValuesCache` sub-tree (`enabled` as `*bool` so the
  YAML decoder distinguishes "not set" from "explicitly false",
  `flush_interval`, `disabled_centrals`). `wireValuesCacheStore`
  honours the master switch, the daemon's flusher reads the
  overridden interval, and a per-central filter closure
  (`WireDeps.ValuesCacheCentralFilter`) excludes named centrals
  from the cache without touching the rest of the wiring.
- **Integration tests against `godevccu`** —
  `tests/integration/values_cache_e2e_test.go` exercises the
  restore → fetch_all → push round-trip, the
  ConnectionLost → Stale → RecoveryCompleted → Live lifecycle with
  bus-event assertions, the fetch-all overrides cache path, and
  the dead-row GC.

Future work — all originally flagged items have shipped:

- **Per-central dirty counter** — landed as `adapter.dirtyTracker`.
  `WireValuesCacheFlusher` subscribes each central's EventBus for
  `DataPointValueChangedEvent` + `DataPointSourceChangedEvent` and
  the periodic tick `SwapClean`s the per-central flag before
  walking. Quiet centrals are skipped entirely. The shutdown flush
  bypasses the tracker so the final write still covers everyone.
  Hot-path cost: one atomic store after a read-locked map lookup
  per event.
