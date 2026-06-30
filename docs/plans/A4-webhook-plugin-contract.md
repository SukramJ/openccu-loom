# Implementation plan — A4: Bidirectional webhook bridge + northbound plugin contract

**Status**: prioritised, not started.
**Audience**: a fresh Claude-Opus environment with no access to the
review conversation. Everything needed is below; verify each cited
path against the tree before editing (paths were verified at the time
of writing but code moves).

---

## 1. Summary

Deliver three things, in this dependency order:

1. **Northbound plugin contract** — a shared lifecycle interface
   (`Service`) + a `Registry`, so northbound bridges are started/stopped
   uniformly instead of being hand-wired one by one in
   `cmd/openccu-loom/daemon.go`.
2. **Outbound webhook** — a northbound bridge that subscribes to the
   per-central event bus and `POST`s a signed JSON payload to operator-
   configured URLs on datapoint / system / incident events. New config
   section `north.webhook`. **Folds in B4**: a new
   `hmevent.IncidentRecordedEvent` so incidents reach the webhook.
3. **Inbound webhook** — REST endpoints that let external systems POST
   to set a datapoint value or trigger a program, reusing the same
   write/trigger adapters the daemon already exposes.

The webhook bridge is the contract's first consumer; existing bridges
(MQTT, Matter, MCP, REST) are migrated onto the contract opportunistically,
**without changing their behaviour**.

---

## 2. Current state (verified)

### 2.1 Event bus (per-central, generic, typed)

`internal/central/events/bus.go`:

```go
func Subscribe[T hmevent.Event](b *Bus, fn func(T), opts ...HandlerOption) func()  // returns unsubscribe
func Publish[T hmevent.Event](b *Bus, e T)
func WithPriority(p Priority) HandlerOption
```

- One `*events.Bus` **per central**. The supervisor reaches them via the
  central registry: `reg.List()` → each `u.EventBus` (see consumer
  pattern below). There is no global bus.
- `Publish` is re-entrancy-safe (deferred dispatch); a high-water gauge
  (`Bus.DeferredHighWater()`, alert at `events.DeferredHighWaterAlert`)
  guards runaway recursion. Do **not** block inside a handler — hand off
  to a goroutine/queue for slow work (HTTP I/O).

### 2.2 Domain events (`pkg/hmevent/`)

Every event struct embeds `hmevent.Base` (init via `hmevent.NewBase()`)
and implements `Type() EventType`. The relevant existing events
(`pkg/hmevent/catalogue.go`):

```go
type DataPointValueChangedEvent struct {
    Base
    Key      hmtypes.DataPointKey
    OldValue hmtypes.ParamValue
    NewValue hmtypes.ParamValue
}
func (DataPointValueChangedEvent) Type() EventType { return EventTypeDataPointValueChanged }

type SystemStatusChangedEvent struct {
    Base
    CentralName string
    Component   string
    Healthy     bool
    Reason      string
    InterfaceID string
    ErrorCode   int
    FailureReason hmenum.FailureReason
    // …more fields
}
```

`DataPointValueChangedEvent.Key` (`hmtypes.DataPointKey`) carries the
central + interface + channel-address + parameter — this is the
multi-CCU scoping dimension and the basis for outbound filters and the
payload's identity fields.

Event-type constants live in `pkg/hmevent/catalogue.go` (e.g.
`EventTypeSystemStatusChanged EventType = "system.status_changed"`).
**A new event must be registered in the catalogue test**
`tests/contract/event_catalogue_test.go` (it maps every type string).

### 2.3 Existing consumer pattern (the template to copy)

`internal/north/mqtt/system_status_publisher.go` — `Start()` iterates
`reg.List()`, subscribes one handler per central, accumulates the
returned unsubscribe funcs, and `Stop()` calls them all:

```go
for _, u := range p.reg.List() {
    bus := u.EventBus
    if bus == nil { continue }
    centralName := u.Name()
    unsub := events.Subscribe(bus, func(e hmevent.SystemStatusChangedEvent) {
        // build payload, marshal, publish; log+return on error
    })
    p.unsubs = append(p.unsubs, unsub)
}
```

Fan-out for datapoint values is wired in
`internal/central/adapter/eventbridge.go` (and `mqtt/wiring.go` consumes
`DataPointValueChangedEvent`). The webhook outbound consumer follows the
**exact same shape** (subscribe per central, store unsubs, Stop()).

### 2.4 Existing bridge wiring (no shared contract today)

`cmd/openccu-loom/daemon.go` constructs each bridge by hand with bespoke
constructors and `.Start(ctx)` calls (e.g. `adapter.NewEventBridge(...)
.Start(ctx)`, `hubMQTT.Start(ctx)`). There is **no** common
`Bridge`/`Service` interface (verified: grep for
`type .*Bridge interface` / `type Service interface` in
`internal/north` + `pkg` returns nothing). MQTT additionally has a
runtime supervisor (`cmd/openccu-loom/mqtt_supervisor.go`,
`SwapBridge`) for hot-reload.

### 2.5 Incident path (B4 fold-in)

`internal/client/reliability/incident.go`:

```go
type IncidentRecord struct {
    CentralName string
    InterfaceID string
    Type        hmenum.IncidentType
    Severity    hmenum.IncidentSeverity
    Message     string
    Details     string
}
type IncidentRecorder interface {
    RecordIncident(ctx context.Context, inc IncidentRecord) error
}
```

Recorded into SQLite (`internal/store/sqlite/incidents.go::RecordIncident`),
called from `internal/central/adapter/callback_handlers.go:~640` and from
`reliability/incident.go` (circuit/coalesce paths). **It does NOT publish
a bus event today** (verified: no `hmevent.*Incident*` event exists). The
reliability package deliberately stays free of the central bus and uses
func-adapter hooks (`CircuitEventPublisherFunc`) — mirror that for the
incident publish so the package keeps zero bus dependency.

### 2.6 Inbound write/trigger surface (reuse, don't reinvent)

- Value write: `DataPointWriterAdapter.SetValue(ctx, address,
  hmenum.Parameter(param), value, priority)` —
  `internal/central/adapter/devices.go:151`; the MCP `set_datapoint`
  tool calls exactly this (`internal/north/mcp/tools.go`, uses
  `hmenum.CommandPriorityHigh`).
- Program trigger: MCP `trigger_program`
  (`internal/north/mcp/tools.go::registerTriggerProgram`) → hub adapter.
- REST router: `internal/north/rest/router.go::NewRouter(d Deps)` (chi).
  Routes mount under `/api/v1`; a `nil` dep means "route not mounted"
  (404). Auth middleware groups already exist (`pr.With(admin)`, etc.).

### 2.7 Config + hot-reload

- `internal/config/config.go::NorthConfig` bundles
  `REST/UI/MQTT/Matter/MCP/Discovery`. **`NorthMCP` is the ideal template**
  for `NorthWebhook` (a feature-flag struct with `cfg:"basic"` /
  `cfg:"expert"` tags). Secret fields use `cfg:"secret"` (e.g.
  `OIDC.ClientSecret`, `MQTT.Password`).
- `internal/config/restart.go::RestartRequiredDiff` lists the structural
  fields that force a restart. **`north.mqtt` is NOT in that set** — it
  is hot-reloaded via the MQTT supervisor (`SwapBridge`) and the
  `ReloadHandler` (`cmd/openccu-loom/reload.go`,
  `mqtt_supervisor.go`). Webhook should follow this (see §3a).

---

## 3. Design decisions

### 3a. Plugin contract — `Service` interface + `Registry`

New package `internal/north/bridge/` (daemon-internal; not a public wire
protocol, so it does **not** go in `pkg/interfaces` — that is reserved
for cross-package *protocol* contracts per CLAUDE.md):

```go
// internal/north/bridge/service.go
package bridge

// Service is a northbound adapter with a uniform lifecycle. Start must
// be non-blocking (spawn goroutines, return); Stop must be idempotent
// and unblock any background goroutines. Name is a stable identifier
// used in logs, health, and the restart-pending surface.
type Service interface {
    Name() string
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}

// HealthReporter is an optional capability a Service may implement so
// the registry can roll it into /health.
type HealthReporter interface {
    Healthy() (ok bool, detail string)
}

type Registry struct { /* mu, []Service, logger */ }
func (r *Registry) Register(s Service)
func (r *Registry) StartAll(ctx context.Context) error  // start in order; on error, Stop already-started, return
func (r *Registry) StopAll(ctx context.Context)         // reverse order, best-effort, aggregate errors to log
```

**Migration without breaking behaviour**: do *not* rewrite every bridge
at once. (1) Land the interface + registry. (2) Wrap each existing
bridge in a thin `Service` adapter (e.g. `mqttService{sup}` whose
`Start`/`Stop` delegate to the current calls) and register it in
`daemon.go`, replacing the inline `.Start(ctx)` lines one at a time.
Each migration is behaviour-preserving and independently testable. The
webhook bridge implements `Service` natively from day one.

> Scope guard: the contract is intentionally minimal (lifecycle + optional
> health). Do **not** model config-reload in the interface yet — MQTT's
> supervisor stays as-is; generalising hot-swap is out of scope for A4.

### 3b. Outbound webhook (event-bus consumer)

- **Bridge type** `internal/north/webhook/outbound.go` implements
  `bridge.Service`. `Start` iterates `reg.List()` and `events.Subscribe`s
  one handler per central for each subscribed event type
  (`DataPointValueChangedEvent`, `SystemStatusChangedEvent`,
  `IncidentRecordedEvent`); stores unsubs; `Stop` unsubscribes. Mirrors
  `system_status_publisher.go`.
- **Never block the bus**: the handler builds the payload and enqueues it
  onto a bounded buffered channel; a small worker pool drains the channel
  and performs the HTTP POST with retry. A full queue drops oldest +
  increments a `webhook.dropped` metric (mirror the audit-overflow
  pattern). This keeps `Publish` fast and re-entrancy-safe (§2.1).
- **Payload** (versioned JSON):
  ```json
  {
    "schema": "openccu-loom.webhook/v1",
    "event": "datapoint.value_changed",
    "central": "<central_name>",
    "interface": "<interface_id>",
    "address": "<channel address>",
    "parameter": "<NAME>",
    "value": <typed>,
    "previous": <typed>,
    "ts": "<RFC3339>"
  }
  ```
  System-status and incident events carry their own field sets under the
  same envelope (`event` discriminates). Map from the event structs in
  §2.2 / §3d. `central` is mandatory in every payload (multi-CCU).
- **Filters** (config): per-endpoint allowlist by event type, by central,
  and an optional channel/parameter glob on datapoint events (match
  against `DataPointKey`). Default = all events, all centrals.
- **HMAC signing**: HMAC-SHA256 over the raw body with the per-endpoint
  secret, sent as header `X-OpenCCU-Signature: sha256=<hex>` plus
  `X-OpenCCU-Delivery: <uuid>` and `X-OpenCCU-Event: <type>`. The secret
  is a `cfg:"secret"` field (see §5).
- **Retry/delivery**: at-least-once, bounded exponential backoff
  (e.g. 3 attempts, 1s/2s/4s, jittered), per-endpoint timeout
  (default 10s). On final failure log + metric; never crash, never block.
  Out of scope: persistent delivery queue (note it as a future item).

### 3c. Inbound webhook (REST trigger surface)

- New handlers in `internal/north/rest/handlers/webhook_inbound.go`,
  mounted in `router.go` under `/api/v1/webhook/` behind the existing
  auth middleware **and** gated by `cfg.North.Webhook.Inbound.Enabled`
  (the handler returns 404/503 when disabled, so toggling is hot — no
  remount, no restart-required entry).
- Endpoints (reuse existing adapters from §2.6):
  - `POST /api/v1/webhook/value` — body
    `{ "central"?: string, "address": string, "parameter": string,
       "value": <any>, "priority"?: string }` → `Writer.SetValue(...)`.
  - `POST /api/v1/webhook/program` — body
    `{ "central"?: string, "program": string }` → hub program trigger.
- **Auth**: these run inside the normal REST auth chain (token/session).
  Additionally accept an inbound-specific bearer token from
  `cfg.North.Webhook.Inbound.Token` (`cfg:"secret"`) for header-only
  callers (e.g. a doorbell). Document that inbound writes are real
  device writes — same authorization weight as REST `PUT .../value`.
- Multi-CCU: `central` optional only when unambiguous; if multiple
  centrals expose the address, require it and 400 otherwise.

### 3d. New event `hmevent.IncidentRecordedEvent` (B4)

In `pkg/hmevent/catalogue.go`:

```go
const EventTypeIncidentRecorded EventType = "incident.recorded"

type IncidentRecordedEvent struct {
    Base
    CentralName string
    InterfaceID string
    Type        hmenum.IncidentType
    Severity    hmenum.IncidentSeverity
    Message     string
    Details     string
}
func (IncidentRecordedEvent) Type() EventType { return EventTypeIncidentRecorded }
```

**Publish without coupling the reliability package to the bus**: add an
optional publisher hook (func-adapter, mirroring
`CircuitEventPublisherFunc`) that the central-adapter layer wires to
`events.Publish`. Emit *after* the SQLite write succeeds, in the layer
that already holds the bus (`internal/central/adapter/callback_handlers.go`
and wherever the recorder is constructed) — not inside
`store/sqlite/incidents.go`. Register the new type in
`tests/contract/event_catalogue_test.go`.

---

## 4. Implementation steps

1. **Event** — add `EventTypeIncidentRecorded` + `IncidentRecordedEvent`
   to `pkg/hmevent/catalogue.go`; add the mapping in
   `tests/contract/event_catalogue_test.go`.
2. **Incident publish** — add a publisher hook to the incident recorder
   path and wire it to `events.Publish` in
   `internal/central/adapter/` (where the bus is in scope). Emit after a
   successful record.
3. **Plugin contract** — new `internal/north/bridge/` with `Service`,
   `HealthReporter`, `Registry` (+ unit tests).
4. **Config** — add `NorthWebhook` to `internal/config/config.go`
   (`NorthConfig.Webhook`), modelled on `NorthMCP`. Fields: `Enabled`,
   `Endpoints []WebhookEndpoint{URL, Secret cfg:"secret", Events []string,
   Centrals []string, ParameterGlob string, TimeoutMs int}`,
   `Inbound{Enabled bool, Token string cfg:"secret"}`. Defaults in the
   config defaulting path. Decide cfg classes (`basic`/`expert`).
5. **Outbound bridge** — `internal/north/webhook/outbound.go`
   (`bridge.Service`): per-central subscriptions, bounded queue, worker
   pool, HMAC, retry. Payload builders per event type.
6. **Inbound handlers** — `internal/north/rest/handlers/webhook_inbound.go`
   + DTOs; mount in `internal/north/rest/router.go` under
   `/api/v1/webhook/` (nil-dep guard + config-flag guard).
7. **Wiring** — in `cmd/openccu-loom/daemon.go` /
   `daemon_north.go`: construct the `bridge.Registry`, register the
   webhook outbound service (and, incrementally, wrap existing bridges),
   call `Registry.StartAll(ctx)` / `StopAll` on shutdown. Pass the
   inbound deps into the REST `Deps`.
8. **OpenAPI + schemas** — add the two inbound endpoints to
   `assets/openapi.yaml`; run `make export-schemas`; bump REST
   `APIVersion` (see §5).
9. **i18n** — add label + help for every new `cfg:` leaf in EN and DE
   (see §5).

---

## 5. Config & API-contract changes (build-gated — do not skip)

- **Field labels/help**: every new `cfg:`-tagged leaf needs BOTH
  `config.field.<path>` AND `config.help.<path>` in **both** the `EN`
  and `DE` catalogues of `assets/ui/src/lib/i18n.ts`
  (e.g. `config.field.north.webhook.enabled`,
  `config.help.north.webhook.enabled`, …). Missing entries fail
  `TestConfigFieldsHaveLabelsAndHelp` (in `tests/contract/`). For list
  elements, add field/help for the element's leaf paths as the schema
  classifier emits them — run the test and add exactly what it reports
  missing.
- **Secrets round-trip**: `Secret` and `Inbound.Token` are
  `cfg:"secret"`. `GET /api/v1/config` masks them to `***`
  (`maskSecrets`); the PUT path swaps `***` back to the stored value
  (`restoreMaskedSecrets`, `internal/north/rest/handlers/admin_config.go`).
  Extend the masked-secret tests under
  `internal/north/rest/handlers/` to cover the new fields (string secret
  in a list element). **Never persist `***`.**
- **API contract guard**: editing `assets/openapi.yaml` requires
  `make export-schemas` (regenerates the digest) **and** an `APIVersion`
  bump, or the PR-only "api contract guard" fails. Add a `CHANGELOG.md`
  entry and the HA add-on changelog entry on release.

---

## 6. Tests

Name test files after the unit under test (no `*_coverageN` / `*_batchN`
— `TestDocPurity` forbids tracking names; keep comments English, no
audit tags).

- **Outbound** (`internal/north/webhook/outbound_test.go`): event →
  signed POST (verify HMAC against a known secret); event-type / central
  / parameter-glob filtering; retry with a fake `http.RoundTripper`
  (fail twice → succeed); queue-full drop increments the metric; `Stop`
  unsubscribes (no POST after stop). Use a fake bus + fake central
  registry; assert multi-CCU isolation (central A's event never carries
  central B's name).
- **Inbound** (`webhook_inbound_test.go`): auth required (401 without
  token); `value` → `Writer.SetValue` called with the right
  address/param/priority (fake writer); `program` → trigger called;
  ambiguous central → 400; disabled → 404/503.
- **Incident event** (in the central-adapter test for the recorder):
  successful record publishes exactly one `IncidentRecordedEvent` with
  mirrored fields; no publish on record error.
- **Plugin registry** (`internal/north/bridge/registry_test.go`):
  `StartAll` starts in order; a mid-start error stops already-started
  services; `StopAll` is reverse-order and idempotent.
- **Contract test**: the webhook adds a northbound capability — add/adjust
  a capability/contract test if a capability flag is introduced
  (`tests/contract/`). Register the new event type in
  `event_catalogue_test.go` (step 1).

---

## 7. Project-rule checklist (per file / PR)

- [ ] SPDX header on every new `.go` file:
      `// SPDX-License-Identifier: MIT` + `// Copyright (C) 2026 OpenCCU-Loom authors.`
- [ ] `CGO_ENABLED=0` safe — no cgo, no new copyleft deps (HMAC = stdlib
      `crypto/hmac`; HTTP = stdlib).
- [ ] Multi-CCU safe — `central` named in every payload, every inbound
      request, every subscription loop (`reg.List()`).
- [ ] Secret-masking round-trip intact; masked-secret tests extended.
- [ ] Every new `cfg:` leaf has EN+DE label **and** help.
- [ ] No `panic` outside `main`/tests; `ctx context.Context` first param
      on every I/O method; handlers never block the event bus.
- [ ] `Service`/`Registry` interface lives in the consumer-side package
      (`internal/north/bridge`), not `pkg/interfaces` (that is for
      cross-package wire protocols).
- [ ] `make lint && make test` green (run lint repo-wide: `golangci-lint
      run ./...` — cross-package linters flag callers in untouched files).

---

## 8. Acceptance criteria (observable)

1. With one configured endpoint + secret, toggling a real datapoint
   produces exactly one HTTP POST whose `X-OpenCCU-Signature` verifies
   against the secret and whose body matches the v1 schema with the
   correct `central`.
2. A `POST /api/v1/webhook/value` with a valid token flips a real device
   parameter (observable via REST `GET` / MQTT state).
3. A recorded incident (e.g. forced circuit-breaker trip in a test)
   results in an `IncidentRecordedEvent` and a delivered webhook POST.
4. Outbound delivery failures retry then surface via log + metric; the
   daemon stays healthy and the event bus is never blocked.
5. Existing MQTT/Matter/MCP behaviour is unchanged after they are wrapped
   in `Service` (their own test suites stay green).

---

## 9. Effort & sequencing

Recommended order: **contract (3a) → event+incident publish (3d) →
outbound (3b) → inbound (3c)**. Rationale: the contract is small and
unblocks clean registration of the new bridge; the incident event is a
prerequisite for full outbound coverage and is independently shippable;
outbound is the headline value and proves the consumer pattern; inbound
is the most security-sensitive and benefits from landing last on a
stable base. Rough effort: contract **S**, event/incident **S**,
outbound **M**, inbound **M** (auth + multi-CCU disambiguation).

Each of the four sub-steps is independently mergeable and testable —
prefer four PRs over one.

---

## 10. References

- `CLAUDE.md` → *Critical Rules*: "Config secrets: never round-trip the
  `***` mask"; "Every config field needs a label AND a help text in en +
  de"; "Interfaces in the consumer package, except for cross-cutting
  protocols"; "Multi-CCU from day one".
- `CLAUDE.md` → *Common Tasks*: "Add a REST endpoint" (openapi-first,
  router, DTOs, tests); "Add a translation key".
- Memory / `[[api-contract-change-checklist]]`: `make export-schemas` +
  APIVersion bump for any `openapi.yaml`/`wsapi.json` edit.
- Event-bus design: `internal/central/events/bus.go`; re-entrancy +
  `DeferredHighWaterAlert`.
- ADR pointers: ADR 0025/0026 (MCP — the closest precedent for a
  feature-flagged northbound adapter with a write-gate); ADR 0002
  (multi-CCU). Consider a short ADR if the plugin contract changes how
  all bridges are wired (architectural shift).
