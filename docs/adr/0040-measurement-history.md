# ADR 0040 — Embedded SQLite measurement history (default) with an opt-in push exporter

- **Status**: proposed
- **Date**: 2026-06-17
- **Related**:
  [ADR 0002 — multi-CCU first class](./0002-multi-ccu-first-class.md),
  [ADR 0011 — MQTT topic and payload architecture](./0011-mqtt-topic-and-payload-architecture.md),
  [ADR 0019 — persistent VALUES cache](./0019-persistent-values-cache.md),
  [ADR 0027 — encrypt config secrets at rest](./0027-encrypt-config-secrets-at-rest.md),
  [ADR 0037 — pluggable span exporter](./0037-otlp-span-exporter.md),
  [Caching Architecture](../caching.md)

## Context

OpenCCU-Loom is a stateless bridge: it emits value changes (event bus,
MQTT) and persists only what it needs to recover the *last-known* value
across restarts (`values_cache`, ADR 0019). There is no historical
time-series of measurement values anywhere in the daemon — no timestamped
value table, no recorder, no time-series dependency. A Home Assistant
user does not miss this: HA's own recorder captures the history. A user
who runs OpenCCU-Loom **without** HA — the explicit non-HA audience from
the project overview — has no built-in way to answer "what was the living
room temperature over the last week?" and must stand up an external stack
(MQTT → Telegraf → InfluxDB → Grafana) just to draw a line chart.

That is the gap this ADR closes. The driving use case is **charts in the
Svelte SPA over the short-to-medium term** (days to a few weeks), not
long-term archival. The design must respect the project's hard
constraints: single static binary, `CGO_ENABLED=0`, no GPL/AGPL/LGPL/MPL
dependency, multi-CCU from day one, and the standing "stop and discuss
before adding a heavy dependency" policy.

The realistic workload is small. A few CCUs, a few hundred devices,
change-based numeric sensor pushes — on the order of tens of millions of
rows per year, not billions. This does **not** require a purpose-built
TSDB; for reference, HA's default recorder is itself SQLite.

## Decision

Two layers, in priority order:

1. **Default surface — an embedded SQLite measurement recorder.** A new
   opt-in recorder subscribes to `hmevent.DataPointValueChangedEvent`,
   keeps numeric measurements in a dedicated `history.db`, applies a
   retention window, and exposes a REST history endpoint the SPA charts
   directly. Zero extra services — the non-HA "I just want graphs" user
   is served by the binary alone.

2. **Opt-in power path — a pluggable push exporter.** For users who
   already run Grafana/InfluxDB/VictoriaMetrics, a `MeasurementExporter`
   seam (modelled on ADR 0037's `SpanExporter`) ships with a lean
   InfluxDB line-protocol/HTTP implementation, default off, no client
   library. The recorder is the single capture point; SQLite and the
   exporter are two sinks on the same buffered stream.

Both layers are disabled by default. The whole feature is one DB-tier
config section (`persistence.history`), therefore editable through the
SPA exactly like `north.mqtt` and `persistence.values_cache`.

## Design

### Scope and filter

- Source event: `hmevent.DataPointValueChangedEvent`
  (`pkg/hmevent/catalogue.go`). It already carries the timestamp
  (`Base.ts`), the `DataPointKey` (`InterfaceID`, `ChannelAddress`,
  `ParamsetKey`, `Parameter`) and the new value — everything a row needs.
  The sample timestamp is **the wire-reception time carried by the
  event, never the boot wall-clock** (see provenance guard below).
- Only `ParamsetKey == VALUES` parameters whose value is **numeric-
  coercible** are recorded (temperature, humidity, power, RSSI, …).
  Booleans may optionally be stored as `0`/`1`; strings and enums are
  excluded by default. This keeps the table narrow and the row volume
  proportional to *sensor* traffic, not to every event on the bus.
- An allow/deny list of parameter-name globs (default allow-numeric)
  gives the operator cardinality control without code changes.
- Calculated and custom DPs are **not** recorded — like ADR 0019, they
  are derived from their wire sources, so storing them would double-count.
- Multi-CCU: `central_name` is part of the primary key (ADR 0002).

### Provenance guard — no boot-time pseudo-values

After a restart a data point can briefly report a value that is **not a
real measurement**: the zero-value default of a freshly created DP before
any wire observation, or a value replayed from `values_cache` during the
restore pass. Recording either would inject a fake spike into the chart —
worse, the replayed cache value would be stamped at boot time, not at the
time the measurement actually occurred. This must not happen.

The discriminator is **provenance, not magnitude.** A real `0` (power
draw of a switched-off socket) is a valid measurement and must be kept;
the bogus values are distinguished by *where they came from*, which ADR
0019 already models on the wire DP as `hmenum.ValueSource`
(`unobserved → cache → live → stale`). The recorder therefore admits a
sample only when **all** of these hold:

- The DP's `ValueSource` is **`live`** at the moment of the event — i.e.
  the value was confirmed by a genuine push or `fetch_all_device_data`.
  `unobserved` (never seen), `cache` (restored from disk) and `stale`
  (frozen on connection loss) are all rejected.
- The value is **non-nil / non-`None`** (consistent with ADR 0019, which
  never persists `nil`).
- The event is a genuine wire reception, **not** the synthetic
  source-change emission the `values_cache` machinery republishes on a
  `cache`/`stale` → `live` flip (ADR 0019 synthesises a
  `DataPointValueChangedEvent` with `OldValue == NoneValue` for the MQTT
  republish path; that carries the *frozen* value, not a fresh reading,
  and must be filtered out).
- **No filter on the numeric value itself** — explicitly *not*
  "drop zeros".

A consequence worth calling out for implementation: ADR 0019's restore
pass calls `RestoreCachedValue` for DPs that implement it, but falls back
to `OnWireValue` for those that do not — and that fallback path emits the
ordinary value events at boot time. Gating on `ValueSource == live`
(which the restore sets to `cache`, and only `fetch_all_device_data`
promotes to `live`) rejects those boot-time emissions regardless of which
event path produced them. The exact emission paths must be confirmed
against `OnWireValue` / `RestoreCachedValue` during slice 2, and the
guard is locked by a contract test (below).

### Storage — a separate `history.db`

The history lives in its own SQLite file under `data_dir`, with its own
WAL and its own goose migration series — **not** in the main config/
session DB. ADR 0019 already flagged the shared-handle risk ("a wedged
SQLite write would now affect both tables"); an append-heavy history
stream is exactly the writer that should not contend with config and
session writes. Separation also makes "wipe my history" a file-level
operation and keeps backups of the small config DB cheap.

Narrow schema:

```
measurements(
  central_name    TEXT,
  interface_id    TEXT,
  channel_address TEXT,
  parameter       TEXT,
  ts              INTEGER,   -- epoch ms
  value           REAL,
  PRIMARY KEY (central_name, interface_id, channel_address, parameter, ts)
) WITHOUT ROWID;
```

The primary key *is* the query index: every chart query is "one DP, time
range", i.e. a prefix scan on `(dp_key…, ts)`. `WITHOUT ROWID` keeps the
row compact and avoids a second b-tree.

### Recorder — buffered, non-blocking

The recorder mirrors the `values_cache` flusher (ADR 0019) and the ADR
0037 exporter contract: the event-bus handler **must not block** on disk
I/O. It pushes each accepted sample onto a bounded buffer; a background
goroutine batches by size/interval and writes one `SaveBatch`
transaction per flush. On overflow the policy is drop-oldest — a stalled
disk degrades history granularity, it never stalls the bus or OOMs the
daemon. A graceful-shutdown flush drains the buffer so the last interval
survives `kill -TERM`.

### Retention and downsampling

Retention runs on the existing `internal/scheduler`: a periodic
`DELETE FROM measurements WHERE ts < cutoff` plus `PRAGMA
incremental_vacuum`. Default raw retention is 30 days (configurable),
which covers the SPA short/mid-term goal.

**Downsampling is explicitly out of the MVP.** Because the goal is
days-to-weeks, raw retention is sufficient; rollup tables (5-min/hourly
avg/min/max) are a later, additive step behind their own config keys if a
user wants longer windows without raw-row cost. The schema above does not
preclude it.

### REST + SPA

Spec-driven (`assets/openapi.yaml` first), a read endpoint:

```
GET /api/v1/history?central=…&dp=<interface/address/parameter>
                   &from=…&to=…&buckets=N
```

The server does the bucketing — it aggregates the range into at most `N`
buckets (avg/min/max per bucket) so the browser receives a bounded
payload regardless of how many raw rows back it. The SPA renders the
result as a line chart on the existing data-point views. This keeps the
"100k raw points" problem on the server where a single SQL aggregate
handles it.

### Exporter seam (opt-in)

Following ADR 0037 verbatim in spirit:

```go
type MeasurementExporter interface {
    // Export is handed each accepted sample. Implementations MUST NOT
    // block — buffer and return.
    Export(Sample)
    Shutdown(ctx context.Context) error
}
```

The recorder fans each accepted sample to the SQLite sink and, when
configured, to the registered exporter. The shipped implementation speaks
**InfluxDB line protocol over HTTP** (`POST <endpoint>/api/v2/write`) with
the standard library only — no `influxdb-client-go`, the same lean-binary
reasoning ADR 0037 applied to OTLP/JSON. The seam keeps Prometheus
`remote_write` and OTLP-metrics as future implementations without a
dependency commitment now. Export credentials (the Influx token) are a
secret and are handled per ADR 0027 (encrypted at rest / `env_file`
reference), never stored as plaintext in `config_sections`.

### Configuration (DB-tier, SPA-editable)

`persistence.history` sits beside the existing `persistence.values_cache`
sub-tree, so it is seeded into `config_sections` and editable in the SPA:

```yaml
persistence:
  history:
    enabled: false           # opt-in
    retention: 720h          # 30 days raw
    flush_interval: 5s
    include: ["TEMPERATURE", "HUMIDITY", "*POWER*", "ACTUAL_*"]
    exclude: []
    disabled_centrals: []
    export:
      enabled: false
      kind: influxdb          # influxdb | (later) prometheus_remote_write | otlp
      endpoint: ""
      org: ""
      bucket: ""
      token_env: ""           # name of env var holding the token (ADR 0027)
```

Following ADR 0019's `*bool` convention, `enabled` is a pointer so the
decoder distinguishes "unset" from "explicitly false".

### Telemetry and tests

- Health/`/metrics` gauges analogous to ADR 0019:
  `history.rows_written`, `history.batches`, `history.buffer_dropped`,
  `history.row_count`, `history.retention_deleted`,
  `history.export_failures`.
- Contract tests: history DB is a *separate* file from the config DB;
  only numeric `VALUES` parameters are persisted; **values with source
  `cache` / `unobserved` / `stale` and synthetic source-flip emissions
  are never recorded, while a genuine `live` `0` is** (the provenance
  guard); retention cutoff deletes only rows older than the window;
  multi-CCU rows for the same channel address on two centrals stay
  independent.
- Integration test against `godevccu`: restart with a populated
  `values_cache`, assert the restore pass produces **no** history rows
  and the first real `fetch_all_device_data` value is the earliest row;
  push → record → bucketed read-back; retention sweep; export round-trip
  to a stub HTTP sink.

## Alternatives considered

- **Embedded pure-Go TSDB library** (`nakabonne/tstorage` MIT, or the
  Prometheus / VictoriaMetrics tsdb packages, Apache-2.0). Rejected as
  the default: it adds a **second** storage engine beside SQLite — a
  second on-disk format to back up, migrate and reason about — for a
  workload SQLite handles comfortably. `tstorage` is lightly maintained;
  the Prometheus/VM engines are awkward to embed in-process and bring a
  large surface. The query layer would be bespoke either way, whereas
  SQL aggregates give the SPA bucketing for free.

- **External InfluxDB as the only path** (no embedded store). Rejected as
  the default: it forces the exact "stand up a separate service" burden
  on the non-HA user this feature exists to serve. Kept as the *opt-in*
  export target instead.

- **Datapoint values as Prometheus gauges on `/metrics`.** Rejected as
  the primary mechanism: the pull model samples at scrape interval and
  misses transient changes between scrapes, and device-address labels
  blow up `/metrics` cardinality. A clean complement for users who
  already scrape, but not a faithful history and not a chart source.

- **"Emit only", status quo.** Rejected as sufficient: it is precisely
  the gap. It remains available (MQTT raw plane is unchanged) as the
  third, zero-cost path for users with their own pipeline.

- **One shared DB with the config/session store.** Rejected per ADR
  0019's own negative-consequences note: an append-heavy history writer
  should not share a WAL handle with config/session writes.

- **Synchronous write in the event handler.** Rejected: disk I/O on the
  bus publisher goroutine. Bounded-buffer + background-batch keeps the
  handler non-blocking, same as ADR 0019's flusher and ADR 0037's
  exporter.

## Consequences

### Positive

- A non-HA operator gets measurement charts in the SPA with **no** extra
  service — the binary is self-contained, which matches the audience.
- No new dependency, no CGo, no copyleft: pure-Go SQLite already in tree,
  exporter is stdlib HTTP + line protocol.
- The recorder is one capture point with two sinks, so the opt-in
  external path (Grafana/Influx) reuses the same filter and buffer.
- Multi-CCU-correct by construction (`central_name` in the PK).

### Negative

- A second SQLite file + a recorder goroutine + a scheduler retention
  job per daemon. The separate handle bounds the blast radius, but it is
  more moving parts than "emit only".
- SQLite is not a compressed columnar TSDB; very long retention with many
  high-frequency sensors would grow the file and eventually want the
  downsampling rollups deferred here. Acceptable for the stated
  short/mid-term goal; revisit if long-term archival becomes a goal.
- Server-side bucketing adds query CPU on wide ranges; bounded by the
  `buckets=N` cap and the prefix-scan PK.

### Implementation plan (slices)

1. Migration + `history.db` store + access struct.
2. Recorder (filter, bounded buffer, batch flush, shutdown flush) +
   config wiring + retention scheduler job.
3. `assets/openapi.yaml` history endpoint + handler + SPA chart.
4. `MeasurementExporter` seam + InfluxDB line-protocol implementation +
   `persistence.history.export` config.

`SPECIFICATION.md` gains the new daemon surface; `CHANGELOG.md` gets a
user-visible entry when slice 1 lands.
