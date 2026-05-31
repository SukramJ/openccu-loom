# ADR 0007 — Strong Model: `Source` Interface for Read + Write

- **Status**: Accepted
- **Date**: 2026-04-30
- **Extends**: [ADR 0004 — Python decorators ↔ Go cross-cutting](./0004-decorators-vs-cross-cutting.md)
- **Related**: `SPECIFICATION.md` §13–§18, `internal/north/mqtt/`,
  `internal/north/rest/`, `internal/model/`

## Context

ADR 0004 chose Go-idiomatic mechanisms for the cross-cutting effects
that aiohomematic delivers via Python decorators. For the
**read-classification** pair (`@info_property` / `@config_property` /
`@state_property`) it landed on a `payload:"<kind>"` struct-tag plus a
reflection-based enumerator in `internal/payload` (already in tree:
`payload.For` / `payload.ForWith` with the tag form
`payload:"<kind>,alt=<name>"`, exhaustive zero-value filtering, and
embedded-struct recursion).

What ADR 0004 does **not** cover — and what running the model end-to-
end has surfaced as the next bottleneck — are two concrete gaps.

### Gap 1 — partial coverage

Today only `*model/device.Device` exposes `Info()` /
`Config()` / `State()`. None of the other layers do:

| Layer | Today | aiohomematic equivalent |
|---|---|---|
| `internal/model/device.Channel`     | ad-hoc readers (`HasParameter`, `ParameterFloatRange`, `ParameterMultiplier`, `ParameterFloatValue`, `ParameterValueList`) | `Channel(PayloadMixin)` (`model/device.py:954`) |
| `internal/model/generic/*`          | per-adapter DTOs | `BaseDataPoint(PayloadMixin)` (`model/data_point.py:520`) |
| `internal/model/calculated/*`       | per-adapter DTOs | inherits `BaseDataPoint` |
| `internal/model/custom/*`           | typed Go API only | `CustomDataPoint(PayloadMixin)` |
| `internal/model/hub/*`              | 8 specialised `Bridge.Publish<X>` methods | each Hub-DP individually inherits `PayloadMixin` |
| `internal/central.CentralUnit`      | per-adapter health DTOs | `CentralUnit.info_payload` (`interfaces/central.py:303`) |
| `internal/client.InterfaceClient`   | per-adapter health DTOs | `Client.info_payload` (`interfaces/client.py:1249`) |

Consequence: **wire-to-semantic translation lives in the north-bound
adapters**. The MQTT discovery builder
(`internal/north/mqtt/discovery_aggregate.go`, ~700 LOC) hard-codes
SET_POINT_MODE → `"heat"/"auto"` mappings, OFF-threshold floors,
min/max resolution, climate-step defaults, lock-state token
translation, light HSL templating, valve `reports_position`
heuristics. All of this is domain knowledge that aiohomematic carries
*in the model* and exposes through `state_payload`. The Python MQTT
bridge (`aiohomematic2mqtt/platforms/generic_entity.py`) is almost
entirely dispatch — it does not know what a thermostat is.

### Gap 2 — write side has no symmetric mechanism

The read side has its (partial) classification; the write side has
nothing. `@inspector(scope=ServiceScope.EXTERNAL)` in aiohomematic
marks methods that constitute the *external* contract — collected by
`get_service_calls()` (`decorators.py:321`) into
`service_methods` / `service_method_names`
(`model/data_point.py:407,412`). aiohomematic2mqtt subscribes one MQTT
topic per service method (`platforms/generic_entity.py:50,264`):

```python
@property
def _ha_command_topics(self) -> tuple[str, ...]:
    return tuple(self._hm_entity.service_method_names)
```

For a BWTH this yields `set_temperature`, `set_mode`, `set_profile`,
`enable_away_mode_by_calendar`, `enable_away_mode_by_duration`,
`disable_away_mode` — *automatically*, with zero per-device-type code
in the bridge.

`OpenCCU-Loom` has the building blocks (`CommandSink.SetValue`,
`CDPInvocationSink.InvokeCustomDP`) but:

- there is no enumeration like `ServiceMethodNames()` on a Custom-DP,
- the bridge subscribes one wildcard `cdps/<dp>/<op>/invoke` topic
  with the operation token in the path, not one topic per method,
- HA-Discovery's `*_command_topic` fields point at **wire parameters**
  (`SET_POINT_TEMPERATURE`) with inline Jinja `*_command_template`
  doing the semantic-to-wire conversion, instead of pointing at
  **operations** (`set_temperature`),
- the `CDPDispatcher` keeps its own operation table parallel to the
  model API — duplicate maintenance.

### Gap 3 — MQTT topology mirrors gap 1

Today the raw plane publishes one topic per *wire parameter* (`…/<chan>/
ACTUAL_TEMPERATURE`, `…/<chan>/SET_POINT_TEMPERATURE`, `…/<chan>/
SET_POINT_MODE`, `…/<chan>/HUMIDITY`, `…/<chan>/BOOST_MODE`). HA
Discovery aggregates these into a climate entity through six separate
state-topic references plus inline Jinja templates. Subscribers on the
raw plane (Node-RED, loggers) see only isolated wire scalars; there is
no single topic that semantically describes "this BWTH is currently in
`heat` mode at 22.0 °C, profile `boost`".

aiohomematic2mqtt instead publishes **one** state topic per entity
with a semantic JSON object; HA reads the same topic via
`value_template: "{{ value_json.<field> }}"` (`platforms/climate.py:32`).

## Decision

Adopt a **single universal contract for every domain object that
crosses a north-bound boundary**:

```go
// internal/payload/source.go (sketch)
package payload

import (
    "context"

    "github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Source describes any domain object — DataPoint, Channel, Device,
// Hub-DP, CentralUnit, InterfaceClient — that may be reflected onto
// MQTT, REST, WebSocket, or a structured log.
//
// The five methods split read access (Info / Config / State) from
// write access (ServiceMethodNames / Invoke). This mirrors
// aiohomematic's PayloadMixin + service_methods pair.
type Source interface {
    Info()   InfoPayload     // typed; concrete *XInfo struct
    Config() ConfigPayload   // typed; concrete *XConfig struct
    State()  StatePayload    // typed; concrete *XState struct

    ServiceMethodNames() []string
    Invoke(ctx context.Context, name string, params map[string]any,
        priority hmenum.CommandPriority) error
}

// Marker types — each is `any` so the interface stays open to
// future payload kinds, and consumers type-switch on the concrete
// struct to read domain-specific fields with compile-time safety.
type InfoPayload   any
type ConfigPayload any
type StatePayload  any
```

`Source` is implemented at every layer of the model. Adapters consume
only this interface; they do not know what a thermostat is.

### Read-side mechanism

Every implementation returns a typed struct pointer matching its
domain — `*ClimateInfo`, `*LockConfig`, `*BlindState`, etc. The
typed structs live in `internal/payload/{info,descriptor,state}.go`
and mirror the historical map keys via `json:` tags, so the wire
shape is byte-identical to the pre-migration map output.

Embedding mirrors model inheritance: `ColorLightInfo` embeds
`LightInfo` and adds `Kind`; `EffectLightState` embeds
`ColorLightState` and adds `Effect`; `SmokeSirenInfo` embeds
`SirenInfo` and adds the kind discriminator. This keeps the Go
type tree aligned with the wire JSON shape.

Conditional fields use either a pointer type (nil when not observed)
or `omitempty` plus a zero-value sentinel. Both render to an absent
JSON key — the JSON wire output is conditional on observation,
matching the prior map-based behaviour where keys were only added
inside `if observed` blocks.

The earlier `payload.For(v, kind)` reflection sweep using
`payload:"info"` struct tags has been retired — every Source builds
its payload structs explicitly. Reflection still survives as the
[payload.PayloadAsMap] helper that JSON-marshals a typed payload
into the loose map shape some legacy consumers (REST DTOs that
predate the typed migration) still expect.

### Write-side mechanism

Service methods are registered explicitly in the constructor — no
decorators, no naming conventions:

```go
package climate

func New(cfg Config) *Climate {
    c := &Climate{ /* … */ }
    c.RegisterService("set_temperature", c.serviceSetTemperature)
    c.RegisterService("set_mode",        c.serviceSetMode)
    c.RegisterService("set_profile",     c.serviceSetProfile)
    c.RegisterService("enable_away_mode_by_duration",
        c.serviceEnableAwayModeByDuration)
    return c
}
```

Registration lives on a `BaseDataPoint`-embedded helper struct
(sketch):

```go
// internal/payload/registry.go (sketch)
package payload

import (
    "context"
    "fmt"
    "sync"

    "github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ServiceHandler is the uniform shape for an external service method.
// `params` is the JSON-decoded body; the handler validates and coerces
// to its real arguments. `priority` propagates straight to the south-
// bound write so callers retain control.
type ServiceHandler func(ctx context.Context, params map[string]any,
    priority hmenum.CommandPriority) error

// ServiceRegistry is embedded into every Source-bearing struct. It
// holds the deterministic ordered list of names plus the dispatch
// table. Names are returned in registration order so HA-Discovery
// emits stable `*_command_topic` mappings.
type ServiceRegistry struct {
    mu    sync.RWMutex
    names []string
    funcs map[string]ServiceHandler
}

func (r *ServiceRegistry) RegisterService(name string, h ServiceHandler) {
    r.mu.Lock()
    defer r.mu.Unlock()
    if _, dup := r.funcs[name]; dup {
        panic(fmt.Sprintf("payload: duplicate service method %q", name))
    }
    if r.funcs == nil {
        r.funcs = make(map[string]ServiceHandler)
    }
    r.funcs[name] = h
    r.names = append(r.names, name)
}

func (r *ServiceRegistry) ServiceMethodNames() []string {
    r.mu.RLock()
    defer r.mu.RUnlock()
    out := make([]string, len(r.names))
    copy(out, r.names)
    return out
}

func (r *ServiceRegistry) Invoke(ctx context.Context, name string,
    params map[string]any, priority hmenum.CommandPriority) error {
    r.mu.RLock()
    h, ok := r.funcs[name]
    r.mu.RUnlock()
    if !ok {
        return fmt.Errorf("payload: unknown service method %q", name)
    }
    return h(ctx, params, priority)
}
```

Every `Source` implementation embeds `ServiceRegistry`. Methods that
should be **internal** (`load_data_point_value`, `fetchHistory`)
simply are not registered — there is no `ServiceScope` enum to
mirror. Absence is the marker. This is cleaner than the Python
decorator switch.

### Layer-by-layer scope

Mandatory `Source` implementation across:

- `internal/model/device.Device` — already has the read trio; add the
  registry and register `delete`, `factory_reset`, `set_install_mode`
  (currently dispersed in central-adapter handlers).
- `internal/model/device.Channel` — new. Read trio surfaces what is
  today behind ad-hoc readers in `discovery_aggregate.go`. Service
  methods rare (most channel-level writes go through MASTER-paramset
  PUT in the config session, which has its own contract).
- `internal/model/generic/*` — Sensor / BinarySensor / Number / Switch
  / Select / Button / Text. Service methods: `set_value`, `turn_on`,
  `turn_off`, `press`, `set_text`, `select_option` as appropriate.
  This is the bulk of the write surface.
- `internal/model/calculated/*` — read trio mostly; service methods
  rare (`set_offset` on Climate-derived).
- `internal/model/custom/*` — Climate, Cover, Light, Lock, Siren,
  Valve, TextDisplay. Read trio = semantic state (`hvac_mode`,
  `preset_mode`, `current_temperature`, …). Service methods =
  the existing `Set*` typed-Go API, registered.
- `internal/model/hub/*` — Program, Sysvar, Update (firmware),
  AlarmMessages, ServiceMessages, InstallMode, Connectivity, Metrics.
  Service methods: `trigger`, `set_value`, `install`, `dismiss`,
  `enable(seconds)`, `disable`. Replaces the eight specialised
  `Bridge.Publish<X>` methods with one generic Hub-DP publisher that
  uses the trio.
- `internal/central.CentralUnit` — `Info()` (name, model, version,
  serial, url) feeds HA-Discovery `via_device` and the system REST
  endpoint. Service methods: `restart`, `reload_devices`,
  `start_service_messages_check`.
- `internal/client.InterfaceClient` — `Info()` (interface,
  protocol, host, version, connected_since) feeds the per-interface
  health API and MQTT connectivity topic.

### MQTT bridge consequences

The bridge (`internal/north/mqtt/`) reduces to dispatch over `Source`:

1. **Aggregated state topic per `Source`** —
   `<base>/<central>/<iface>/<addr>/<chan>/state` carries
   `dp.State()` as a JSON object. The existing per-parameter
   topics stay (raw plane, parameter-level subscribers), but HA
   Discovery shifts to the aggregated topic with
   `value_template: "{{ value_json.<field> }}"`.
2. **One command topic per service method** —
   `…/<chan>/cdp/<dp_name>/<service_method>/set` (or equivalent
   shape — final scheme decided in the implementation phase, gated by
   ADR 0006 conventions). The bridge subscribes
   `len(dp.ServiceMethodNames())` topics, dispatching by trailing path
   segment to `dp.Invoke(name, params, priority)`. The wildcard
   `cdps/<dp>/<op>/invoke` form remains as a JSON-RPC-style fallback
   for REST-bridge and scripting consumers but is no longer the
   primary HA path.
3. **`discovery_aggregate.go` shrinks dramatically.** Wire→semantic
   translation (SET_POINT_MODE int → `"heat"/"auto"`, OFF-threshold
   floor, lock token mapping, light HSL template, valve
   reports_position heuristic) all moves into the model. The builder
   keeps only: choose HA component, wire the aggregated state topic,
   wire the per-method command topics, set `value_template`s. Estimate:
   ~700 LOC → <200 LOC.
4. **Hub-DP specialisations collapse.**
   `PublishProgram` / `PublishSysvar` / `PublishInstallMode` /
   `PublishAlarmMessages` / `PublishServiceMessages` /
   `PublishConnectivity` become one `publishHubSource(hubDP)` that
   uses the trio.

### REST and WebSocket consequences

- `POST /api/v1/dp/<key>/services/<name>` becomes one generic handler
  that calls `dp.Invoke`. Per-operation handlers in
  `internal/north/rest/handlers/` collapse — the OpenAPI document can
  still expose per-operation paths for clarity, but they all route
  through the same code.
- WebSocket initial-state messages include `service_method_names` so
  the UI can build action menus dynamically rather than carrying a
  per-DP-type widget table.

### Logging / metrics

A thin wrapper around `Invoke` provides what `_emit_service_metrics`
in aiohomematic does for free: per-method latency histogram,
per-method error counter. Naming follows
`openccu_loom_service_call_duration_seconds{method="set_temperature"}`
to match the existing instrumentation convention from ADR 0004.

The `LogContextMixin` half of aiohomematic (`support/mixins.py:21`)
is *not* in scope for this ADR — log context will be considered when
its absence becomes a concrete pain point. The `payload:"…"` tags
already provide enough metadata to add a `LogAttrs()` method later
without restructuring.

## Trade-offs

- **Two paths for read**: explicit methods vs. struct-tag reflection.
  Operators reading a Custom-DP need to know whether the payload comes
  from the method body or from tag sweep. We accept this — Generic-DP
  has 15+ shared fields where tags are clearly better; Custom-DP has
  conditional logic where methods are clearly better. A linter rule
  ("a type that defines `State()` must not also tag fields with
  `payload:\"state\"`") prevents accidental dual-source.
- **Registration in constructors**: forgetting to register a service
  method is silent. Mitigation: a contract test per Custom-DP shape
  asserts the expected method set (analog to aiohomematic's
  `tests/test_service_methods.py`). Adding a method to the Go API
  without registering becomes a visible test failure.
- **Topic shape change**: the aggregated state topic and the per-
  method command topics are observable schema changes. Coordinated
  via the existing `LegacyAlias` plumbing in `BridgeConfig` —
  aggregated topics ship behind a config flag in the first release,
  default in the next. Gives operators time to update Node-RED flows.
- **Reflection cost**: tag-based payload sweep is per-instance.
  Cached per type via `sync.Map` keyed on `reflect.Type`; the hot
  path is a single map lookup plus a slice copy. Benchmarked in
  `tests/bench/payload_test.go` before MQTT cut-over.
- **Panic on duplicate registration**: `RegisterService` panics on
  duplicate names. Constructors are init-time; a panic is the right
  signal for a programming error. Tests catch this trivially.

## Why not simpler shapes

- **Stay parameter-scoped, no aggregated state topic**: keeps today's
  shape but never closes the wire→semantic gap. Custom-DP semantics
  remain trapped in the bridge. Every new device type means more
  inline Jinja in `discovery_aggregate.go`.
- **Reflection-only, no explicit methods**: forces every conditional
  field into a tagged struct field with sentinel zero values. Loses
  the ability to compute fields (`hvac_modes` is a function of the
  device profile, not a stored value).
- **Method-only, no struct tags**: forces ~15 LOC of boilerplate per
  Generic-DP type for fields that are mechanical 1:1 mappings. Tags
  earn their keep here.
- **One global service-method registry indexed by DP type**: avoids
  per-instance `map`. Rejected: it cannot capture per-instance state
  in the closure (e.g. the bound `c.SetTemperature` method already
  carries `c`). The map-per-instance is the simplest correct shape.
- **HA-Discovery to keep pointing at wire-parameter command topics**:
  preserves today's bridge code. Rejected — the wire-mapping logic
  belongs in the model, not in HA-Discovery Jinja. ADR 0007 is
  precisely about pulling it back.

## Consequences

### Positive

- One contract (`Source`) covers MQTT, REST, WebSocket, and structured
  logging — the four north-bound surfaces that today each maintain
  their own DTO shape.
- Wire→semantic translation lives in the model, where the domain
  knowledge already is. The MQTT bridge becomes dispatch, matching
  the role aiohomematic2mqtt plays.
- Adding a new device type means adding a new Custom-DP with its
  trio and registered methods — no bridge edits, no
  `discovery_aggregate.go` edits, no REST-handler edits.
- `service_method_names` is a contractually testable surface;
  parity tests with aiohomematic become possible (same operations
  exposed for the same device type).
- HA-Discovery shrinks to value_template references on a single
  state topic, matching aiohomematic2mqtt's shape and the broker
  resource footprint that comes with it.

### Negative

- Touches every model package. Phased migration — see plan above —
  keeps each step self-contained and shippable.
- Two parallel state-topic plans (per-parameter raw + aggregated)
  exist for at least one minor version. Documented in the operator
  notes; `LegacyAlias` already has the precedent.
- A new `internal/payload` dependency is added to every model package.
  Mitigated by keeping `internal/payload` zero-dep beyond stdlib +
  `pkg/hmenum`.

### Mitigations

- Contract test `tests/contract/source_completeness_test.go`: every
  type listed in the layer-by-layer scope above must implement
  `Source`. New types added to those packages auto-trip the test
  until they comply.
- Benchmark `tests/bench/payload_build_test.go`: the per-type cached
  reflection path must stay below 500 ns/op for a 20-field struct.
  Regressions block release per the existing benchmark gate.
- `golangci-lint` rule (custom analyser, light): a type that defines
  `State()` must not have struct fields tagged
  `payload:"state"`. Prevents accidental dual-source.
- Operator-facing migration note in `CHANGELOG.md` for each step
  that flips a default (steps 3, 9). The aggregated topic stays
  config-gated until the default flip, with explicit upgrade guidance.

## Status notes

- The MQTT-topology direction set out in this ADR (one aggregated
  state topic per channel) is itself superseded by ADR 0011, which
  adopts a per-DP topology. The `Source` contract — read trio plus
  `ServiceMethodNames` / `Invoke` — remains in force.
- A `LogContextMixin` analogue in Go is intentionally deferred until
  log-enrichment becomes a concrete pain point. The `payload:"…"`
  tags already provide enough metadata to add a `LogAttrs()` method
  later without restructuring.
