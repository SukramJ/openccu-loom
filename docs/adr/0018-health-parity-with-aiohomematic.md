# ADR 0018 — Health Tracker Parity with aiohomematic

- **Status**: Accepted
- **Date**: 2026-05-22
- **Related**:
  `internal/health/{tracker,client,availability_test,client_test}.go`,
  `internal/client/observer/health.go`,
  `internal/central/adapter/health_wiring.go`,
  `internal/store/sqlite/health_probe.go`,
  `internal/north/rest/handlers/diagnostics.go`,
  ADR 0017,
  SPECIFICATION §8 Observability.

## Context

ADR 0017 established the daemon's observability backbone (W3C trace,
redaction, level-registry, diagnostics dump, capture). A post-landing
audit against `aiohomematic/central/health.py` surfaced two distinct
gaps in the Health surface:

1. **Tiefe (pro-Interface-Detail).** OpenCCU-Loom's `Tracker.Record(name, …)`
   captured a single binary `Healthy` bit plus a free-text note. The
   Python reference carries a much richer `ConnectionHealth`
   dataclass per client — `last_successful_request`,
   `last_failed_request`, `last_event_received` (each with a
   DST-safe monotonic shadow), `consecutive_failures`,
   `reconnect_attempts`, `in_recovery`, plus a per-client
   `health_score` derived from a 40 % state + 30 % circuit + 30 %
   recent-activity weighting. Without these, a diagnostic dump could
   not tell whether a healthy-looking interface had just survived a
   stream of failures or had genuinely been idle.

2. **Breite (Coverage).** Only two producers fed the tracker: the
   per-interface event-bus subscription (`WireHealth`) and a 60 s
   central heartbeat job. SQLite, MQTT, Matter, JSON-RPC hub session,
   scheduler, REST/WS — none registered themselves. An operator
   asking "is the daemon healthy?" got a per-interface verdict and
   nothing more.

The user's standing memory directive applies: aiohomematic is the
1:1 reference for any reconnect/recovery/health constant.
Hand-rolled values are forbidden.

## Decision

We extended the Health backbone in two co-ordinated waves.

### Wave 6a — Detail-Felder pro Interface (Tiefe)

Introduced `internal/health/client.go::ClientHealth` mirroring
aiohomematic's `ConnectionHealth` field-for-field:

```go
type ClientHealth struct {
    LastSuccessfulRequest time.Time
    LastFailedRequest     time.Time
    LastEventReceived     time.Time
    ConsecutiveFailures   int
    ReconnectAttempts     int
    InRecovery            bool
}
```

Notable choices:

- **Monotonic time** — Go's `time.Now()` already carries a monotonic
  reading on every value it produces. `time.Time.Sub` is DST-safe
  out of the box, so we did NOT replicate aiohomematic's
  `*_monotonic` shadow fields. The same correctness guarantee, fewer
  fields to keep in sync.
- **Implicit registration** — `Tracker.RecordRequest(name, success)`
  creates the `ClientHealth` entry on first touch. No explicit
  `register_client` call, no boot-time enumeration of interfaces.
- **Semantic-fault tolerance** — the `Health` observer that drives
  `RecordRequest` from the transport layer treats non-retryable
  XMLRPC faults (`Unknown Parameter`, validation rejections) as
  successes for health-counting purposes. Same predicate the
  circuit breaker uses in `reliability/circuit.go` so a write-only
  data point poll cannot poison the interface verdict.

New tracker methods (extension only — every existing call site keeps
its semantics):

| Method | Mirrors | Trigger |
|---|---|---|
| `RecordRequest(name, success)` | `record_successful_request` / `record_failed_request` | observer.Health on every RPC |
| `SetRecoveryFlag(name, bool)` | `in_recovery = …` | RecoveryStarted/Completed/Failed events |
| `ResetReconnects(name)` | `reset_reconnect_counter` | ClientStateChanged → Connected |
| `SetPrimaryInterface(name)` | `set_primary_interface` | composition root (not yet wired) |
| `ClientDetail(name)` | snapshot of `ConnectionHealth` | diagnostics dump |
| `ClientScore(name)` | `health_score` (40/30/30) | diagnostics dump + SPA |
| `PrimaryClientHealthy()` | `primary_client_healthy` | JSON-RPC hub fallback |
| `CentralScore(name)` | per-central aggregate | multi-CCU SPA tile |

Score weighting:

- 40 % `clientScoreState(Status)` — Healthy=1.0, Degraded=0.5, else 0.
- 30 % `clientScoreCircuit(lastBreakerNote)` — closed=1.0,
  half-open=0.5, open=0.0. The breaker note is found by scanning the
  per-component sample history backwards for the most recent
  `breaker …` annotation, because `LastSample.Note` may be overwritten
  by an `event-received` sample before the score is read.
- 30 % `clientScoreActivity(ageOfLastEvent)` — staged decay
  (<60 s = 1.0, <300 s = 0.66, <600 s = 0.33, else 0).

### Wave 6b — Coverage (Breite)

Each subsystem becomes a Health producer using one of two patterns:

1. **Probe pattern** for resources without an event surface
   (`internal/store/sqlite/health_probe.go`). A 30 s ticker pings the
   resource; `< 100 ms`= healthy, `< 500 ms` = degraded, else
   unhealthy. Component name is the bare subsystem name (`sqlite`).
2. **Observer pattern** for southbound transports that already carry
   a `TransportObserver` slot. `observer.NewHealth(tracker,
   observer.WithComponentName("hub.<central>"))` is wired into the
   JSON-RPC hub config so hub-only failures show up as
   `hub.ccu-main` independently of the XML-RPC interfaces.

The remaining subsystems (MQTT, Matter, Scheduler, REST/WS) follow
the same pattern and are scheduled as a follow-up wave (Wave 6b-tail).
Their producer files are not yet committed — the SPA already
tolerates their absence (the Client-Health card only renders the
rows that exist).

### Per-Central aggregation

`Tracker.CentralScore(central)` averages over every component whose
name carries `central` as a substring. Wiring already prefixes the
component names with the central name (`ccu-main-HmIP-RF`,
`hub.ccu-main`, …), so the substring rule is enough; no separate
registry is needed.

### Diagnostics Dump + SPA surface

`GET /api/v1/diagnostics` now emits `health.clients[]` (one
`DiagnosticsClient` per interface that has touched the new detail
path) and `health.primary_client_healthy`. The Svelte
`Diagnostics.svelte` view gained a Client-Health card that lists
per-interface score, last successful/failed/event timestamps,
consecutive-failure / reconnect-attempt counters, and an `in_recovery`
badge. The card is hidden when `clients` is empty so a freshly-booted
daemon does not show an empty grid.

## Consequences

**Positive**

- Diagnose dumps now answer "is the JSON-RPC hub still reachable?"
  independently of "is the XML-RPC HmIP-RF interface fine?" — they
  show up as separate health components and separate scores.
- The SPA's Diagnostics view has the per-interface drill-down the
  audit asked for: an operator can see at a glance which interface
  is in recovery, how many consecutive failures it has suffered, and
  when the last successful call landed.
- Multi-CCU setups get per-central scores without code changes on
  the tracker — the substring rule is enough.
- The Health Observer slots cleanly into the existing
  `interfaces.TransportObserver` chain via `observer.NewMulti`,
  next to the Logging Observer from ADR 0017. New observers can be
  added without touching the transport packages.

**Negative**

- `ClientHealth` carries pointers in the tracker's `clients` map. A
  long-running daemon retains one entry per interface ever seen.
  Bounded by the number of configured interfaces (small), so the
  memory footprint is irrelevant in practice; documented here for
  completeness.
- `ClientScore` walks the sample history backwards to find the most
  recent breaker note. With the default 200-sample ring that's
  ≤ 200 string comparisons per score call. Score is read by the
  diagnostics endpoint and the SPA poll, not on the hot path; cost
  is acceptable.

**Risks accepted**

- The substring-based `CentralScore` rule could double-count a
  component that legitimately mentions another central's name (e.g.
  a CUxD interface name that happens to contain another CCU's host).
  Mitigation: callers can switch to an explicit prefix registry later
  if the false-positive surface appears in practice.

## Alternatives considered

- **Emit `RequestSucceededEvent` / `RequestFailedEvent` on the
  internal event bus** and have `WireHealth` subscribe. Rejected
  because the same wiring already had a `TransportObserver` slot,
  and an observer is one less hop than publishing through the bus
  for every RPC. Performance and clarity both improve.
- **Hand-port aiohomematic's `*_monotonic` shadow fields** into
  separate `time.Duration`-since-boot pairs. Rejected — Go's runtime
  already attaches a monotonic reading to every `time.Time`. The
  shadow would be redundant and split the source of truth.
- **Bind health to a fixed list of subsystems (whitelist)**.
  Rejected — implicit registration on first `Record` keeps the
  wiring layer in charge of what shows up, mirroring the current
  Tracker behaviour and matching aiohomematic's lazy `register_client`
  model.

## Follow-ups

- Wave 6b-tail: MQTT, Matter, Scheduler, REST/WS health producers.
  The SPA already tolerates their absence; once added, no SPA
  changes are needed.
- `Tracker.SetPrimaryInterface` is not yet called from any wiring.
  The fallback HmIP-RF substring rule satisfies the
  `PrimaryClientHealthy` query in every current deployment; explicit
  pinning becomes useful only for non-HmIP CCUs.

## References

- aiohomematic central health: `aiohomematic/central/health.py`
- OpenCCU-Loom Tracker: `internal/health/tracker.go`,
  `internal/health/client.go`
- Health observer: `internal/client/observer/health.go`
- Store probe: `internal/store/sqlite/health_probe.go`
- Diagnostics surface: `internal/north/rest/handlers/diagnostics.go`
- SPA view: `assets/ui/src/routes/Diagnostics.svelte`
