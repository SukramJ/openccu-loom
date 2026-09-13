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

## Amendment (2026-09-12) — the `LegacyAlias` precedent it cites did not exist

Two places above lean on `LegacyAlias` as working, shipped plumbing: the
topic-shape-change risk is "coordinated via the existing `LegacyAlias` plumbing
in `BridgeConfig` — aggregated topics ship behind a config flag in the first
release, default in the next", and the two-parallel-state-topic-plans
consequence notes that "`LegacyAlias` already has the precedent".

**There was no precedent.** `BridgeConfig.LegacyAlias` had no config key, no
environment override and no flag; it was the Go zero value on every build, and
the only sites that ever set it were tests. It is now deleted — ADR 0006's
amendment of the same date carries the measurement.

Neither sentence is load-bearing for this ADR's decision, which stands
unchanged: the strong model-source interface does not depend on a
mirror-during-migration mechanism. What changes is the mitigation. A future
topic-shape change that wants a migration window has to build the opt-in it
needs, with a real config key an operator can set and a test that asserts the
key reaches the bridge — and not assume one is already there.

## Amendment (2026-09-13) — four artifacts this ADR names do not exist

The decision stands; its **references** do not. Four identifiers and paths
above name things that are not in this repository, three of them in the
Mitigations and Trade-offs sections, which is where a reader goes to find
out how a risk is actually held down.

- **`tests/bench/payload_test.go`** (§Trade-offs, "Reflection cost … 
  Benchmarked in"). No such file. `tests/bench/` holds
  `aggregated_state_publish_bench_test.go`, `event_bus_bench_test.go`,
  `reliability_bench_test.go` and `snapshot_bench_test.go`. The first is
  the closest thing to the benchmark this line promises.
- **`tests/bench/payload_build_test.go`** (§Mitigations, "the per-type
  cached reflection path must stay below 500 ns/op for a 20-field struct").
  No such file either, and no benchmark anywhere asserts that ceiling — so
  "Regressions block release per the existing benchmark gate" describes a
  gate that does not close on this path.
- **`CDPDispatcher`** (§Gap 1, "keeps its own operation table parallel to
  the model API"). No type of that name exists. The real one is
  `CustomDPDispatcher`, in
  `internal/central/adapter/custom_dp_dispatcher.go`. The gap it describes
  is real; only the name is wrong.
- **The custom `golangci-lint` analyser** (§Trade-offs and §Mitigations, "a
  type that defines `State()` must not have struct fields tagged
  `payload:\"state\"`"). `.golangci.yaml` configures no custom analyser and
  no plugin. The rule did ship, as a **contract test** —
  `tests/contract/source_no_dual_source_test.go` — which checks the same
  property by reflection over the model types. A reader looking for a linter
  rule finds nothing and concludes the protection is missing; it is not, it
  is one directory over.

Two references in the same sections are **correct** and worth stating so,
because the four above cast doubt on the rest:
`tests/contract/source_completeness_test.go` exists and does what §Mitigations
says, and `internal/north/mqtt/discovery_aggregate.go` exists.

Nothing here changes the decision. What it changes is what a reader should
do with a named artifact in this ADR: check it. The 2026-09-12 amendment
above withdrew this ADR's `LegacyAlias` precedent for the same reason — it
was cited as shipped plumbing and was not.

## Amendment (2026-09-13) — the payload-build benchmark now exists, and the 500 ns/op bound is not met

The amendment above reported that `tests/bench/payload_build_test.go` did not
exist, so §Mitigations' "the per-type cached reflection path must stay below
500 ns/op for a 20-field struct — regressions block release per the existing
benchmark gate" bounded nothing. That file now exists, the bound has been
measured for the first time, and the measurement is the news:

**the ADR's own twenty-field workload costs 1 343 ns/op — 2.7x the bound.**

### What the 500 ns/op applies to

§Trade-offs and §Mitigations describe the same operation twice: "tag-based
payload sweep … cached per type via `sync.Map` keyed on `reflect.Type`; the
hot path is a single map lookup plus a slice copy" and "the per-type cached
reflection path … for a 20-field struct". That operation is
`payload.ForWith` — `internal/payload/payload.go`, delegating the cached field
walk to `go-hamqtt/payload`, whose `fieldsOf` is exactly the
`sync.Map`-keyed-on-`(reflect.Type, Kind)` cache the ADR describes. There is no
other cached reflection path in this tree, and `payload.For` — the spelling
§Decision uses when it says the sweep "has been retired" — is not in
`internal/payload` at all; `ForWith` is what survived and what production
calls.

**The ADR is ambiguous about this in one respect, and it is worth naming.**
§Decision says the tag sweep "has been retired — every Source builds its
payload structs explicitly", while §Trade-offs and §Mitigations, further down
the same document, still treat it as the live hot path worth bounding. Both
are partly true: the sweep is no longer how a `Source` builds its own payload,
but it was never removed, and `internal/north/mqtt/discovery.go:1306` and
`discovery_schedule.go:479` still call it per device on every HA-Discovery
build. So the bound has a live subject.

**And there is a fifth phantom artifact, which the amendment above did not
catch.** §Decision offers `payload.PayloadAsMap` as the reflection helper that
survived the retirement — "Reflection still survives as the [payload.
PayloadAsMap] helper that JSON-marshals a typed payload into the loose map
shape some legacy consumers still expect". No such identifier exists anywhere
in this repository. It would have been the wrong subject for the 500 ns/op
bound regardless — the ADR describes it as a JSON round-trip, not a per-type
cached tag walk — but a reader sent to it to understand what reflection is
still doing here finds nothing, which is precisely the failure mode the
amendment above set out to fix.

The benchmark therefore measures `payload.ForWith` twice, because the ADR
describes the workload two ways:

- `BenchmarkPayloadBuildDeviceInfo` — the production call site verbatim:
  `payload.ForWith(dev, payload.KindInfo, payload.Options{UseAltNames: true})`
  over a `*device.Device` built through `device.New`, which also crosses the
  `ExtraProperties` merge the mutex-guarded `name` field forces.
- `BenchmarkPayloadBuildTwentyField` — the ADR's literal workload parameter, a
  20-field tagged struct, through the same production function.

### Measured

Taken on the CI runner (`ubuntu-latest`, AMD EPYC 9V45), minimum of seven runs
at `-benchtime=300ms` — the figures the gate is calibrated against:

| Benchmark | ns/op | B/op | allocs/op | ADR bound | over by |
|---|---|---|---|---|---|
| `BenchmarkPayloadBuildTwentyField` (the ADR's workload) | **1 343** | 2384 | 26 | 500 ns/op | **2.7x** |
| `BenchmarkPayloadBuildDeviceInfo` (the production call site) | **829** | 1464 | 19 | 500 ns/op | **1.7x** |

So the bound is missed on both readings, and missed by more on the reading the
ADR itself states. The allocation counts are the stable part and they are what
makes the verdict safe on any machine: the figures above are from an EPYC
server part, and the slowest target this daemon actually ships to is the
32-bit ARMv7 build `.goreleaser.yaml` produces for the CCU add-on bundle — so
no plausible change of machine closes a gap of this size; a change of machine
widens it. The
per-call cost is one map allocation, then per retained field a `FieldByIndex`,
an `IsZero`, an `Interface()` that boxes the value (an allocation for every
non-pointer field) and a map insert with a string hash. §Trade-offs' picture of
the hot path as "a single map lookup plus a slice copy" describes the *cache*
lookup, not the call: the cache removes the reflection walk, not the boxing and
the map build, and those are where the time is.

**The ADR's number is left standing as written.** It is not quietly relaxed to
whatever the code does — that would turn a design target into a description and
lose the information that there is a gap. Nor was the code tuned to reach it in
the change that first measured it; a measurement and the optimisation it
motivates do not belong in the same commit.

### Where it is now enforced

- `tests/bench/payload_build_test.go` — the two benchmarks above, `//go:build
  bench` like the rest of `tests/bench/`.
- `script/bench_gate.sh` (`make bench-gate`) — the gate. It holds the ceilings,
  so the benchmark file stays a measurement and the policy stays in one
  reviewable place. Armed at 2 700 ns/op and 1 700 ns/op respectively: the
  measured minimum, doubled. The doubling is headroom for runner silicon, not
  for noise — the minimum-of-N below already handles noise — because GitHub's
  hosted pool is not one machine and a leg scheduled on an older part is
  genuinely slower at identical code.
- `.github/workflows/ci.yml`, the `bench` job — a new step after `make bench`.
  `make bench` runs every benchmark and asserts nothing; this step is the part
  that can go red.

**The armed ceilings are a ratchet at today's measured cost, not the ADR's
500 ns/op.** They exist so the path cannot get *worse* while the gap is open.
Lowering one after a real improvement is the point of the ratchet; raising one
needs a reason in the commit message that raises it.

### Why minimum-of-N, and not benchstat

A shared CI runner only ever adds time. Contention, a co-tenant VM, a throttled
core and a GC pause all push a sample up; nothing pushes one below the cost the
code actually has. The minimum across N runs is therefore the sample least
polluted by the runner, and the only summary statistic whose noise is
one-sided in the safe direction: noise can let a regression through a run
(the next run catches it), but it cannot turn the gate red on an unchanged
tree. A mean or a median drifts with runner load and would need padding so
generous that the gate stops meaning anything.

This is not a theoretical preference. On the developer machine the benchmark
was written on, ten runs of the unchanged twenty-field benchmark spanned
4 773 - 5 990 ns/op with the box near-idle and 32 907 - 49 634 ns/op with three
other build jobs on it — a factor of seven between two runs of identical code.
The minimum tracked the quiet figure in both cases. Even on the dedicated CI
runner one sample in seven came in 29 % high (1 343 - 1 735 ns/op) while the
minimum held to within 3 %. `benchstat` was the obvious alternative and is the wrong tool here:
it compares two sets of samples for a *significant difference*, which needs a
stored baseline from comparable hardware, and a gate whose baseline is a
committed file becomes a gate that passes because somebody regenerated the
file — a failure mode this repository has already shipped four times over.

The remaining risk is the opposite one: a CI runner on genuinely slower
silicon than the machine the ceiling was calibrated on. That is why the
ceilings carry explicit headroom over the measured minimum, stated in the
script rather than folded into a global multiplier.

### The gate was verified to fail

A gate that cannot fail is the defect this amendment exists to close, wearing
a stopwatch. So the gate was made to fail on the machine it guards, at the
ceilings it actually carries, before it was trusted.

`payload.ForWith` was given 40 extra allocations per call and pushed as a
throwaway commit. The `bench` job went red naming both benchmarks:

```
BenchmarkPayloadBuildTwentyField: 3139.0 ns/op > ceiling 2700 ns/op
BenchmarkPayloadBuildDeviceInfo:  2444.0 ns/op > ceiling 1700 ns/op
```

66 allocs/op instead of 26, and 59 instead of 19 — a 2.3x regression, well
short of the catastrophic, and the gate caught it. The commit was reverted and
the same gate on the same runner returned 1 343 / 829 ns/op green. That is the
sensitivity the doubled ceiling buys: not "any regression", but any regression
that doubles the cost of a path the daemon runs per device on every
HA-Discovery build.

## Amendment (2026-09-13) — the 500 ns/op bound is withdrawn: it governs 1.2 % of the work it sits in

The amendment above measured `payload.ForWith` for the first time and
reported that §Mitigations' "must stay below 500 ns/op for a 20-field struct"
is missed by 2.7x. It measured a cost. It did not measure whether that cost
matters, and it said as much by deferring the optimisation to a separate
change.

This is that separate change, and it does not optimise anything. The
denominator came back first, and it ends the question:

**`payload.ForWith` is 1.2 % of the discovery build it is part of.** Closing
the 500 ns/op gap completely — not improving it, *eliminating two thirds of the
call* — would buy 0.7 % of one HA-Discovery entity build. The bound is
withdrawn rather than defended, because a performance promise the next reader
will optimise toward is worse than no promise at all.

### The denominator

`BenchmarkDiscoveryBuildPerEntity` (`tests/bench/payload_build_test.go`)
measures `DefaultDiscoveryBuilder.Build` — the exact call
`Bridge.PublishDiscoveryOnly` makes (`internal/north/mqtt/bridge.go:892`), and
the one that contains exactly one `deviceDescriptor` and therefore exactly one
`payload.ForWith`. Around that one call sit topic construction, component
classification, `device_class` and `state_class` resolution, i18n lookups, the
`hadiscovery.RenderComponent` model render and the JSON marshal of the ~1.3 KB
payload that actually goes on the wire.

Measured on the CI runner (`ubuntu-latest`, Intel Xeon Platinum 8573C):

| Benchmark | ns/op | B/op | allocs/op | share of a build |
|---|---|---|---|---|
| `BenchmarkDiscoveryBuildPerEntity` (the whole build) | **95 082** | 41 199 | 811 | 100 % |
| `BenchmarkPayloadBuildDeviceInfo` (the `ForWith` inside it) | **1 174** | 1 464 | 19 | **1.2 %** time, **2.3 %** allocations, 3.6 % bytes |

**The allocation ratio is the load-bearing figure, not the nanoseconds.** 19
allocations out of 811 is a property of the code, not of the machine: it is
identical on every run, on every runner, and on every architecture. The
nanosecond ratio can drift with silicon; the ratio 19/811 cannot. Any argument
that a faster or slower machine changes this verdict has to explain how it
changes that count.

### What it costs at fleet scale

A full build re-enters the builders for every entity. At ~11.7 entities per
device — the repository's own figure, from a live HA instance carrying 958 MQTT
entities across 82 devices (`docs/adr/0070-shared-ha-discovery-model-module.md`)
— and taking 12:

| Fleet | Entities | `ForWith` total | Whole discovery build |
|---|---|---|---|
| 50 devices | 600 | **0.7 ms** | 57 ms |
| 200 devices | 2 400 | **2.8 ms** | 0.23 s |
| 1 000 devices | 12 000 | **14 ms** | 1.1 s |

Fourteen milliseconds, once, on the largest fleet this daemon plausibly sees,
spread across a boot that also has to talk to a CCU over XML-RPC. Meeting the
ADR's bound would have removed eight of those fourteen.

**And the birth-triggered republish — the case that sounded most expensive —
costs nothing at all.** `Bridge.RepublishDiscovery`
(`internal/north/mqtt/bridge.go:755`) replays the already-built retained
payloads the publisher runtime holds; it does not re-enter
`DiscoveryBuilder.Build`. A Home Assistant restart therefore makes **zero**
`ForWith` calls, at any fleet size. The paths that do re-enter the builders are
daemon boot, MQTT (re)connect, config reload, and a single device being added
or renamed — the last of which is one device's worth, some 12 entities, 14 µs.

The one genuinely recurring caller is the value-change path
(`internal/central/adapter/eventbridge.go:1549`), which re-renders the
descriptor per changed data point forever after boot. That is a steady-state
cost, and at 1.2 µs against a 95 µs render it is the same 1.2 %.

### On the hardware that ships

**This was not measured on the target, and cannot be honestly claimed to have
been.** The slowest platform the add-on bundle supports is the CCU3, `armv7l`
(`docs/user-guide.md`), a 32-bit ARM userland on a Cortex-A53-class part at
roughly 1.2 GHz. No such hardware was available; nothing below is a
measurement.

*Method, stated so it can be disagreed with*: the CI part is a Xeon 8573C, a
wide out-of-order core at ~3 GHz. Against an in-order, dual-issue A53 at
1.2 GHz the clock ratio alone is ~2.5x, and the per-clock ratio for
allocation- and map-heavy Go — which is what both benchmarks are — is
plausibly another 4x to 10x once the A53's far smaller caches and 32-bit
pointer-and-64-bit-arithmetic penalties are included. **Central estimate 20x,
plausible range 10x–30x**, uncertainty dominated by the cache term rather than
the clock term. So a 1 000-device boot's discovery build is estimated at
**11 s–34 s** on a CCU3, of which `ForWith` is **0.14 s–0.42 s**.

*Why the estimate does not change the verdict*: both benchmarks are the same
kind of work — Go allocator traffic, map inserts, string hashing — so whatever
factor the A53 applies, it applies to both, and the 1.2 % share survives
unchanged. The uncertainty above is entirely in the absolute figures, which is
why the withdrawal rests on the ratio and on the 19/811 allocation count
instead. What the ARMv7 estimate *does* say is that the **denominator** is
worth attention on that hardware: 11–34 seconds of serial discovery building
on a 1 000-device CCU3 is a real number, and it is not `ForWith`'s.

### What replaces the bound

§Mitigations' sentence is withdrawn, not relaxed. In its place,
`script/bench_gate.sh` arms a ceiling on the operation that actually carries
the cost:

- **`BenchmarkDiscoveryBuildPerEntity` — 190 000 ns/op**, the measured 95 082
  doubled, on the same minimum-of-seven basis and with the same 2x
  runner-silicon headroom as the other two.
- The two `ForWith` ceilings **stay where PR #814 armed them** (2 700 and
  1 700 ns/op). They are no longer a placeholder for an optimisation that is
  coming — they are plain regression protection on a path nobody should now
  spend effort on. Nothing about `payload.ForWith` was changed by this
  amendment, deliberately: the correct response to "this is 1.2 % of the work"
  is to leave it alone, and an untouched hot path is also a payload pinned
  byte-for-byte against `internal/north/mqtt/testdata/`.

A reader who wants the old sentence's intent should read the new ceiling
instead: *the per-entity HA-Discovery build must stay below 190 µs on a CI-class
x86 core*. That one is measured, enforced, and large enough that a regression
in it is something an operator on a CCU3 would actually feel.

### And the fifth phantom artifact, now corrected

The amendment above recorded that §Decision's `payload.PayloadAsMap` — "[…]
Reflection still survives as the [payload.PayloadAsMap] helper that
JSON-marshals a typed payload into the loose map shape some legacy consumers
(REST DTOs that predate the typed migration) still expect" — exists nowhere in
this repository. It records the correction here.

Searched again, and the finding is stronger than "wrong name": there is **no
helper of that shape under any name**. `internal/payload` exports exactly one
function returning a loose map, `ForWith`, and it is a cached struct-tag walk,
not a JSON round-trip. No `AsMap`/`ToMap`-shaped helper exists in `internal/`
or `pkg/`, and the upstream `go-hamqtt/payload` has none either. The sentence
does not misname a real thing the way `CDPDispatcher` misnamed
`CustomDPDispatcher`; it describes a mechanism that was never built.

**So the whole clause is withdrawn.** What survives the retirement §Decision
describes is `payload.ForWith` and nothing else, it is reached from two call
sites (`internal/north/mqtt/discovery.go:1306` and
`discovery_schedule.go:479`), and — per this amendment — it accounts for 1.2 %
of the build those call sites sit in. A reader asking "what is reflection still
doing in this daemon" now has a complete answer in one place, which is what the
phantom sentence prevented.

That is five withdrawn references across three amendments. The standing
instruction from the 2026-09-13 amendment above applies to this document
without exception: **check every artifact this ADR names before relying on it.**
