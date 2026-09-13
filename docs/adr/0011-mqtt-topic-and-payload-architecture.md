# ADR 0011 — MQTT Topic & Payload Architecture (Model-Push, Per-DP Topology)

- **Status**: Accepted
- **Date**: 2026-04-30
- **Supersedes (in part)**: [ADR 0007](./0007-strong-model-source-interface.md) §9 (aggregated state topic),
  [ADR 0008](./0008-aggregated-state-default-flip.md) (aggregated-state flip),
  [ADR 0009](./0009-service-method-command-topics.md) (service-method command topics — extended, not replaced),
  [ADR 0010](./0010-discovery-payload-from-model.md) (discovery payload owner — extended)
- **Related**: `internal/north/mqtt/`, `internal/model/custom/*/payload.go`,
  `internal/central/adapter/eventbridge.go`, `internal/payload/source.go`

## Context

Several iterations of MQTT topology have accumulated layered workarounds:

- ADR 0007 introduced an **aggregated channel state topic** (`<central>/<iface>/<addr>/<ch>/state`)
  carrying every wire-DP a custom-DP knows.
- ADR 0008 made the aggregated topology unconditional and removed the
  per-DP fallback.
- ADR 0010 moved the HA-Discovery payload construction into the model.

Real-world MQTT capture from 2026-04-30 (HmIP-BWTH `000C9709AEF157`,
broker side-by-side with `aiohomematic2mqtt`) surfaced four classes of
problems:

1. **Empty-Jinja-template warnings**: HA's MQTT Climate platform renders
   `value_json.hvac_mode` etc. against the aggregated state JSON. When
   the aggregate is published before all constituent DPs have been
   observed (a real and common boot-time race) the JSON misses fields
   the discovery references — HA logs `Invalid modes mode:` /
   `'dict object' has no attribute 'hvac_mode'` on every retain replay.
   We fixed it by gating the publish behind a new
   `payload.Observable` interface (commit `a7e1f0a`) — that is a
   **workaround**, not a clean architecture.
2. **`available` field collisions**: HA's MQTT Siren schema rejects an
   `available` key inside the state JSON
   (`extra keys not allowed @ data['available']`). We stripped it from
   every custom-DP `StatePayload` (commit `f54141a`) — again, a
   workaround for a topology that conflates entity availability with
   state fields.
3. **Sparse `device` block**: OpenCCU-Loom's discovery emits 5 device
   fields (identifiers, manufacturer, model, name, serial_number)
   while `aiohomematic2mqtt` emits 11 (adds model_id, sw_version,
   suggested_area, via_device, configuration_url, etc.). HA's device
   card is correspondingly poorer.
4. **Static-only capabilities**: HA's MQTT Climate platform takes
   `modes` and `preset_modes` as **static** lists in the discovery
   config. aiohomematic's `profiles` property is mode-conditional —
   when the thermostat flips between AUTO and HEAT, the valid preset
   list changes (week-program slots are only valid in AUTO). The
   current static discovery payload cannot express this; HA's preset
   selector therefore lists invalid options most of the time.

Live snoop comparison of the BWTH between the two daemons:

| | OpenCCU-Loom (today) | aiohomematic2mqtt |
|---|---|---|
| State topology | `<central>/<iface>/<addr>/<ch>/state` (aggregate) | `device/status/<addr>/<ch>/<param>/state` (per-DP) |
| State payload | `{"available":true,"current_temperature":21.6,"target_temperature":20.5,"state_uncertain":false}` | per-DP JSON `{"value":21.6,"available":true,"modified_at":...,"refreshed_at":...}` |
| Climate `min_temp` / `max_temp` | hardcoded 4.5 / 30.5 | reads operator `TEMPERATURE_MINIMUM` / `TEMPERATURE_MAXIMUM` from the channel (14.0 / 23.0 for our test BWTH) |
| `current_humidity_topic` | absent (BWTH humidity invisible in HA Climate card) | present, points at per-DP humidity topic |
| Device block fields | 5 | 11 |
| Climate `preset_modes` | static `["week_program_1..6","boost","away"]` | static `["boost","week_program_1..6"]` (also static — but HA cannot represent "AUTO-only") |

## Decision

Adopt a **per-DP push** topology with a curated **derived-state aggregate**
for synthetic fields, and make discovery **reactive** to capability
changes. Mirrors aiohomematic2mqtt's mature shape with three concrete
improvements: explicit `master/` vs. `values/` separation, `calculated/`
as a first-class URL segment, and `custom/<kind>/` as the home of
service methods + derived-state.

### Guiding principle: declarative model, dumb bridge

**Every fact about a source — what it is, what topics it owns, which
HA component it surfaces as, which fields its discovery references,
which service methods it accepts, which wire-parameters can change
its discovery shape — is declared on the model object itself.** The
bridge is a thin JSON publisher that consults the source's declared
surface and never carries domain rules.

What this rules out — *no* per-parameter classification table in
`internal/north/mqtt/discovery.go`, *no* service-method routing map
in `internal/north/mqtt/service_method_routing.go`, *no* hard-coded
"this domain has these fields" knowledge anywhere in `north/mqtt/`.

What gets owned where:

| Concern | Owner |
|---|---|
| What HA component am I? (sensor / binary_sensor / climate / …) | model (`HAComponent() string`) |
| Which paramset bucket am I in? (values / master / calculated / custom) | model (`TopicSlot()`) |
| Which service methods do I accept? (set_temperature, set_mode, …) | model (`ServiceMethodNames()`, exists) |
| Which wire-params can flip my discovery shape? | model (`DiscoveryTriggers()`) |
| What does my state JSON look like? (fields + types) | model (`StatePayload()`, exists) |
| What is the full HA-Discovery body? (templates, modes, presets, …) | model (`HADiscoveryPayload(ctx)`, ADR 0010) |
| What is the device card? (model_id, sw_version, rooms, …) | model (`InfoPayload()` umfangreich, exists) |
| Should this DP be visible at all? | model (visibility filter, ADR 0005) |
| Topic prefixing: `<base>/<central>/<iface>/<addr>/<ch>/…` | bridge — pure naming convention |
| JSON marshalling, retained flag, QoS, broker dispatch | bridge — pure transport |
| Subscribe → service-method dispatch | bridge — pure transport (looks up the method on the source via `Source.Invoke`) |
| Discovery-payload caching + diff-gated republish | bridge — pure efficiency |

### Source surface (Go interfaces)

All extensions are **optional** — sources without the new methods get
sensible defaults (no discovery, no custom topology) so the migration
is incremental.

```go
package payload

// TopicSlot identifies the source's address in the topic tree.
// The bridge reads it to construct the full topic path; the source
// never sees the broker base, central name, or interface id — those
// are bridge-side prefixes.
type TopicSlot struct {
    Address   string  // CCU device address ("000C9709AEF157")
    Channel   int     // 0..N
    Bucket    Bucket  // values | master | calculated | custom
    Parameter string  // wire-param name OR custom-DP kind ("climate")
}

type Bucket string
const (
    BucketValues     Bucket = "values"
    BucketMaster     Bucket = "master"
    BucketCalculated Bucket = "calculated"
    BucketCustom     Bucket = "custom"
)

// HAEntity is implemented by sources that surface as an HA entity
// via MQTT Discovery. Returns "" to opt out of discovery.
type HAEntity interface {
    HAComponent() string  // "sensor", "binary_sensor", "climate", "lock", "valve", …
}

// Slotted is implemented by sources that own a topic slot under
// their channel. Sources that don't (e.g. abstract aggregates) opt
// out by not implementing this — bridge publishes nothing for them.
type Slotted interface {
    TopicSlot() TopicSlot
}

// DiscoveryDynamic — already specified above. Repeated here for
// completeness of the source-surface listing.
type DiscoveryDynamic interface {
    DiscoveryTriggers() []hmenum.Parameter
}

// HADiscoveryPayloadBuilder — already from ADR 0010. Repeated here
// because it's part of the declarative surface.
type HADiscoveryPayloadBuilder interface {
    HADiscoveryPayload(ctx HADiscoveryContext) (component string, body map[string]any)
}
```

Worked example — Climate fully declares its surface:

```go
// internal/model/custom/climate/topology.go
package climate

import (
    "github.com/SukramJ/openccu-loom/internal/payload"
    "github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func (c *Climate) HAComponent() string { return "climate" }

func (c *Climate) TopicSlot() payload.TopicSlot {
    return payload.TopicSlot{
        Address:   c.Address,
        Channel:   c.ChannelNo(),
        Bucket:    payload.BucketCustom,
        Parameter: "climate",
    }
}

func (c *Climate) DiscoveryTriggers() []hmenum.Parameter {
    return []hmenum.Parameter{
        hmenum.ParameterControlMode,
        hmenum.ParameterHeatingCooling,
        hmenum.ParameterActiveProfile,
    }
}

// ServiceMethodNames is already declared via the ServiceRegistry the
// type embeds; no extra plumbing needed.
//
// HADiscoveryPayload renders the full HA Climate discovery body
// referencing per-DP topics for direct values + the custom-DP
// state topic for derived fields. The bridge supplies the topic
// strings via HADiscoveryContext — model never builds raw paths.
func (c *Climate) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
    // c.Profiles() and c.Modes() are recomputed on every call —
    // reactive discovery picks up the change automatically.
    body = map[string]any{
        "current_temperature_topic":    ctx.WireParameterStateTopic("ACTUAL_TEMPERATURE"),
        "current_temperature_template": "{{ value_json.value }}",
        "current_humidity_topic":       ctx.WireParameterStateTopic("HUMIDITY"),
        "current_humidity_template":    "{{ value_json.value }}",
        "temperature_state_topic":      ctx.WireParameterStateTopic("SET_POINT_TEMPERATURE"),
        "temperature_state_template":   "{{ value_json.value }}",
        "temperature_command_topic":    ctx.ServiceMethodCommandTopic("set_temperature"),
        "mode_state_topic":             ctx.AggregatedStateTopic(),
        "mode_state_template":          "{{ value_json.hvac_mode }}",
        "mode_command_topic":           ctx.ServiceMethodCommandTopic("set_mode"),
        "preset_mode_state_topic":      ctx.AggregatedStateTopic(),
        "preset_mode_value_template":   "{{ value_json.preset_mode }}",
        "preset_mode_command_topic":    ctx.ServiceMethodCommandTopic("set_profile"),
        "action_topic":                 ctx.AggregatedStateTopic(),
        "action_template":              "{{ value_json.action }}",
        "min_temp":                     c.MinTemp(),
        "max_temp":                     c.MaxTemp(),
        "temp_step":                    c.TemperatureStep(),
        "temperature_unit":             "C",
        "modes":                        modesToStrings(c.Modes()),
        "preset_modes":                 profilesToStrings(c.Profiles()),
    }
    return "climate", body
}
```

Bridge consumes this surface generically:

```go
// internal/north/mqtt/dispatch.go (sketch)
func (b *Bridge) PublishSourceState(ctx context.Context, src payload.Source) error {
    slotted, ok := src.(payload.Slotted)
    if !ok { return nil }                       // source opts out
    slot := slotted.TopicSlot()
    topic := b.topics.SlotTopic(b.cfg.CentralName, slot)
    payloadBytes, _ := json.Marshal(src.StatePayload())
    return b.client.Publish(ctx, topic, payloadBytes, b.cfg.QoS.State, true)
}

func (b *Bridge) PublishDiscovery(ctx context.Context, src payload.Source) error {
    haEntity, ok := src.(payload.HAEntity)
    if !ok { return nil }                       // source has no HA discovery
    component := haEntity.HAComponent()
    if component == "" { return nil }
    builder, ok := src.(payload.HADiscoveryPayloadBuilder)
    if !ok { return nil }                       // source opted out of full discovery
    discoveryCtx := b.discoveryContextFor(src)  // wires per-DP topic helpers
    _, body := builder.HADiscoveryPayload(discoveryCtx)
    // ... diff-gated publish ...
}
```

The bridge contains **zero** domain knowledge about climate, lock,
cover, light, etc. — it just dispatches generic interface calls.

### What stays in the bridge

Pure transport concerns. The bridge owns:

- **Topic naming convention** — the literal string templates
  `<base>/<central>/<iface>/<addr>/<ch>/<bucket>/<parameter>/state`
  etc. live in `north/mqtt/topics.go`. The model knows it's slot
  `(BWTH001, ch=1, BucketValues, "ACTUAL_TEMPERATURE")` — the bridge
  stitches that into a full topic path.
- **JSON marshalling** of the maps the source returns.
- **Retain flag, QoS, broker authentication, reconnect logic**.
- **Discovery payload cache** for diff-gated republishing.
- **Subscribe-side dispatch**: subscribe to `<...>/values/<param>/set`
  → call `Source.Invoke(ctx, "set_value", {"value": …}, priority)`;
  subscribe to `<...>/custom/<kind>/set/<method>` → call
  `Source.Invoke(ctx, method, body, priority)`. The mapping is
  generic (URL pattern → interface call), no per-method hardcoded
  routing.

### What this lets us delete

After full migration:

- `internal/north/mqtt/discovery.go: classifyComponent` — every
  source declares its own HA component.
- `internal/north/mqtt/service_method_routing.go` — service-method
  names come from the registered handlers on the source itself, no
  routing table needed.
- The `payload:"info"` tag-driven device-block harvest in
  `deviceDescriptor` — `<addr>/info` topic carries the same data and
  the discovery's `device` field reads from there or copies from
  `Device.InfoPayload()`.
- Any per-domain switch/case branching in the bridge (`switch
  ev.ChannelType { case "CLIMATE": …}`) — there should be none after
  this rolls out.

Bridge LOC target after the migration: **the dispatch loop, topic
naming, JSON marshalling, and broker plumbing** — nothing else.
ADR 0010 already reduced `discovery_aggregate.go` to ~150 LOC; this
ADR continues that direction by removing the rest of the
domain-aware code paths from the bridge.

### Topic hierarchy

```
<base>/                                     openccu-loom/
├── bridge/
│   ├── status                              "online" | "offline"  (LWT, retained)
│   └── health                              JSON: connections, queue depth, dispatch lag
└── <central>/                              GoOtto/
    ├── hub/
    │   ├── status                          "online" | "offline"  (CCU reachable)
    │   ├── info                            JSON: model, sw_version, serial, url, ...
    │   ├── diagnostics                     JSON: duty_cycle_global, carrier_sense, ...
    │   ├── sysvars/<name>/state            per-sysvar JSON
    │   ├── sysvars/<name>/set              (HA → daemon)
    │   └── programs/<name>/trigger         (HA → daemon)
    └── <iface>/<addr>/                     HmIP-RF/000C9709AEF157/
        ├── availability                    "online" | "offline"  (per-device)
        ├── info                            JSON: full device-info shape (see §Device info)
        ├── diagnostics                     JSON: last_seen, rssi_*, low_bat, duty_cycle, ...
        ├── update/state                    JSON: installed_version, latest_version, in_progress
        ├── update/set                      "INSTALL"
        └── channels/<ch>/
            ├── values/<param>/state        per-DP VALUES paramset state
            ├── values/<param>/set          (HA → daemon)
            ├── master/<param>/state        per-DP MASTER paramset state
            ├── master/<param>/set          (HA → daemon, MASTER write)
            ├── calculated/<name>/state     calculated DP state
            └── custom/<kind>/
                ├── state                   derived/synthetic fields only (hvac_mode, preset_mode, action, ...)
                ├── set/<service_method>    (HA → daemon, JSON body with named args)
                └── …
```

Concrete BWTH `000C9709AEF157`:

```
openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/
├── availability
├── info
├── diagnostics
├── update/{state,set}
└── channels/
    ├── 0/                                  (maintenance)
    │   ├── values/{RSSI_DEVICE,RSSI_PEER,LOW_BAT,DUTY_CYCLE,UNREACH,
    │   │           STICKY_UNREACH,CONFIG_PENDING,UPDATE_PENDING,
    │   │           ERROR_CODE,LOCK_TARGET_LEVEL,...}/state
    │   ├── values/LOCK_TARGET_LEVEL/set
    │   ├── master/{GLOBAL_BUTTON_LOCK,...}/state
    │   ├── master/<param>/set
    │   └── custom/lock/                    (button-lock custom DP)
    │       ├── state                       {available,lock_state,is_locked,is_jammed,...}
    │       └── set/{lock,unlock,open}
    ├── 1/                                  (climate)
    │   ├── values/{ACTUAL_TEMPERATURE,SET_POINT_TEMPERATURE,HUMIDITY,
    │   │           CONTROL_MODE,BOOST_MODE,ACTIVE_PROFILE,...}/state
    │   ├── values/{SET_POINT_TEMPERATURE,BOOST_MODE,ACTIVE_PROFILE}/set
    │   ├── master/{TEMPERATURE_MINIMUM,TEMPERATURE_MAXIMUM,
    │   │           WINDOW_OPEN_TEMPERATURE,PARTY_*}/state
    │   ├── master/<param>/set
    │   ├── calculated/{DEW_POINT,DEW_POINT_SPREAD}/state
    │   └── custom/climate/
    │       ├── state                       {available,hvac_mode,preset_mode,action,state_uncertain}
    │       └── set/{set_temperature,set_mode,set_profile,
    │                enable_away_mode_by_calendar,disable_away_mode}
    └── 9/                                  (switching)
        ├── values/STATE/state
        └── values/STATE/set
```

### Payload schemas

**All retained except `set/*` topics.** All JSON.

#### Per-DP `values/<param>/state` and `master/<param>/state`

```json
{
  "value": 21.6,
  "available": true,
  "modified_at": 1730385720.123,
  "refreshed_at": 1730385720.123
}
```

The wire envelope carries only live state. Descriptor metadata —
`unit`, `type`, `min`, `max`, `default`, `value_list`, `source` for
calculated DPs — lives on the retained companion `/config` topic and
is not duplicated on every value event. `modified_recently` /
`refreshed_recently` are not part of the wire shape either —
consumers derive them from `modified_at` / `refreshed_at` plus their
own clock.

#### Calculated `calculated/<name>/state`

Same envelope as the per-DP topic. The constituent parameter names a
calculated DP is derived from (e.g. `["ACTUAL_TEMPERATURE","HUMIDITY"]`
for `DEW_POINT`) live in the companion `/config` payload via
`source`, not in every state event.

```json
{
  "value": 9.4,
  "available": true,
  "modified_at": 1730385720.123,
  "refreshed_at": 1730385720.123
}
```

The companion `/config` topic carries
`{"unit":"°C","source":["ACTUAL_TEMPERATURE","HUMIDITY"]}` once;
state events stay scalar.

#### Custom-DP `custom/<kind>/state`

**Derived/synthetic fields only.** Direct wire values are NOT duplicated
here — HA's discovery references the per-DP topics directly for those.

```json
{
  "available": true,
  "hvac_mode": "heat",
  "preset_mode": "boost",
  "action": "heating",
  "state_uncertain": false
}
```

#### Device `info` (umfangreich, mirrors aiohomematic / homematicip_local)

```json
{
  "address": "000C9709AEF157",
  "interface_id": "HmIP-RF",
  "interface": "HmIP-RF",
  "central": "GoOtto",
  "model": "HmIP-BWTH",
  "model_id": "Homematic IP Wandthermostat mit Feuchtesensor",
  "model_icon": "mdi:thermostat",
  "sub_model": "",
  "name": "Wandthermostat AK",
  "manufacturer": "eQ-3",
  "product_group": "HmIP-RF",
  "sw_version": "3.0.4",
  "hw_version": null,
  "rooms": ["Ankleide"],
  "functions": ["Heizung"],
  "rx_modes": ["BURST", "WAKEUP"],
  "configuration_url": "http://172.18.X.XX",
  "updatable": true,
  "channels": [
    { "channel_no": 0, "type": "MAINTENANCE",                       "paramset_keys": ["VALUES","MASTER"], "custom_dps": ["lock"] },
    { "channel_no": 1, "type": "HEATING_CLIMATECONTROL_TRANSCEIVER","paramset_keys": ["VALUES","MASTER"], "custom_dps": ["climate"] },
    { "channel_no": 9, "type": "SWITCH_VIRTUAL_RECEIVER",            "paramset_keys": ["VALUES","MASTER"], "custom_dps": [] }
  ]
}
```

Single retained snapshot. Every consumer (REST, UI, HA-Discovery
builder, external tools) reads from the same source — no separate
HM-specific fields scattered across multiple topics.

#### Device `diagnostics`

```json
{
  "last_seen": 1730385720.0,
  "rssi_device": -78,
  "rssi_peer": -69,
  "duty_cycle": false,
  "low_bat": false,
  "battery_pct": null,
  "config_pending": false,
  "update_pending": false,
  "unreach": false,
  "sticky_unreach": false
}
```

Aggregated from the maintenance-channel DPs the operator usually
displays in HA's diagnostic-entity panel; the individual DPs continue
to be published under `channels/0/values/...` for granular subscribers.

### HA Discovery — direct topics + derived aggregate

A Climate discovery for our BWTH ch1 references **multiple** state
topics — direct values straight from per-DP topics, derived values
from the curated custom-DP aggregate:

```jsonc
{
  "current_temperature_topic":  ".../channels/1/values/ACTUAL_TEMPERATURE/state",
  "current_temperature_template": "{{ value_json.value }}",
  "current_humidity_topic":     ".../channels/1/values/HUMIDITY/state",
  "current_humidity_template":  "{{ value_json.value }}",
  "temperature_state_topic":    ".../channels/1/values/SET_POINT_TEMPERATURE/state",
  "temperature_state_template": "{{ value_json.value }}",
  "temperature_command_topic":  ".../channels/1/custom/climate/set/set_temperature",

  "mode_state_topic":           ".../channels/1/custom/climate/state",
  "mode_state_template":        "{{ value_json.hvac_mode }}",
  "mode_command_topic":         ".../channels/1/custom/climate/set/set_mode",
  "preset_mode_state_topic":    ".../channels/1/custom/climate/state",
  "preset_mode_value_template": "{{ value_json.preset_mode }}",
  "preset_mode_command_topic":  ".../channels/1/custom/climate/set/set_profile",
  "action_topic":               ".../channels/1/custom/climate/state",
  "action_template":            "{{ value_json.action }}",

  "min_temp": 14.0, "max_temp": 23.0, "temp_step": 0.5, "temperature_unit": "C",
  "modes": ["auto", "heat", "off"],
  "preset_modes": ["boost"],

  "availability": [
    { "topic": "openccu-loom/bridge/status", "payload_available": "online", "payload_not_available": "offline" },
    { "topic": ".../000C9709AEF157/availability", "payload_available": "online", "payload_not_available": "offline" }
  ],
  "availability_mode": "all",

  "device": { "...full device info from `<addr>/info`..." },
  "origin": { "name": "openccu-loom", ... }
}
```

Templates **never render empty** in this topology: each per-DP topic
publishes when (and only when) its DP is observed; the custom-DP
aggregate only contains derived fields the model can compute from the
already-observed wire DPs.

### Reactive discovery (dynamic capabilities)

aiohomematic models a few things as state-dependent that HA only
accepts statically in the discovery config — most prominently
`preset_modes` for HmIP thermostats which only includes week-program
slots when `mode == AUTO`. Pinning the discovery to the worst-case
list (always include all profiles) shows invalid options to the user
most of the time.

Add a new optional capability to custom-DP source types:

```go
package payload

// DiscoveryDynamic is implemented by Sources whose HA-Discovery
// payload depends on observed state — most notably custom-DPs whose
// `modes`/`preset_modes` lists are mode- or capability-conditional.
//
// Bridge subscribes the listed parameters; on every observed change
// it re-renders the discovery payload via HADiscoveryPayloadBuilder
// and re-publishes the retained discovery topic when the rendered
// JSON differs from the cached previous version. HA picks up the
// change automatically (retained discovery → entity reconfiguration).
type DiscoveryDynamic interface {
    // DiscoveryTriggers returns the wire parameters whose value
    // change can flip the discovery shape. Empty slice ↔ static
    // discovery (default).
    DiscoveryTriggers() []hmenum.Parameter
}
```

Climate implementation:

```go
func (c *Climate) DiscoveryTriggers() []hmenum.Parameter {
    return []hmenum.Parameter{
        hmenum.ParameterControlMode,    // mode=AUTO/MANU/AWAY/BOOST → preset_modes shape
        hmenum.ParameterHeatingCooling, // heating vs cooling → modes shape
        hmenum.ParameterActiveProfile,  // active profile → preset_modes ordering
    }
}
```

Bridge integration: `EventBridge.onValueChanged` adds a third dispatch
arm next to per-DP-publish and custom-derived-state-publish. A
`discoveryCache map[uniqueID][]byte` holds the last published JSON;
re-renders that produce the same bytes are no-ops (no broker traffic).

### Service-method command shape

JSON body with named arguments — extends ADR 0009:

```jsonc
// publish to .../channels/1/custom/climate/set/set_temperature
{ "temperature": 21.0 }

// publish to .../channels/1/custom/climate/set/enable_away_mode_by_calendar
{ "start": "2026-05-01T08:00:00", "end": "2026-05-08T17:00:00", "away_temperature": 12.0 }

// trivial set, no args → empty body or single-key object
// publish to .../channels/0/custom/lock/set/lock
{}
```

Bridge subscribes `<...>/custom/<kind>/set/+` per channel + custom-DP,
dispatches the trailing path segment as the service-method name,
unmarshals the JSON body into a `map[string]any`, and calls
`Source.Invoke(ctx, methodName, params, priority)`.

Per-DP wire writes (`values/<param>/set`) keep accepting a bare
scalar — strings, numbers, booleans — so HA's stock entity types
without a JSON body keep working out of the box.

## Consequences

### What gets removed

- `bridge.PublishSourceState` aggregating every wire-DP value into the
  channel-level state topic.
- The channel-aggregated state topic itself (`<...>/<ch>/state`).
  Existing retained value is cleaned up at boot.
- The `payload.Observable` gating interface and the cache in
  `EventBridge.markAvailability` (commit `a7e1f0a`) — strictly an
  artefact of the previous topology.
- The `available` field in custom-DP `StatePayload` methods (already
  removed in `f54141a`; this ADR locks the absence in).
- `state_uncertain` from custom-DP `StatePayload` migrates to the
  custom-DP aggregate; otherwise unchanged.

### What gets added

- Per-DP `values/<param>/state` JSON-wrapper publish path.
- `master/<param>/state` and `master/<param>/set` topics.
- `calculated/<name>/state` topic.
- `custom/<kind>/state` topic restricted to derived fields.
- `custom/<kind>/set/<method>` JSON-body command-topic dispatcher.
- `<addr>/info` umfangreich device-info topic.
- `<addr>/diagnostics` aggregated diagnostics topic.
- `hub/{info,diagnostics}` and `hub/sysvars/<name>/{state,set}` and
  `hub/programs/<name>/trigger` (some of these exist already; this
  ADR pins the canonical shape).
- `payload.DiscoveryDynamic` interface + Bridge re-render dispatch.

### What changes shape

- ADR 0009's service-method topics move from
  `<...>/<ch>/svc/<method>/set` to
  `<...>/<ch>/custom/<kind>/set/<method>` for consistency with the
  read-side custom-DP namespace.
- ADR 0010's `HADiscoveryPayloadBuilder` keeps its responsibilities
  but the context interface (`HADiscoveryContext`) gains methods to
  request per-DP state topics by parameter name.
- ADR 0007's "aggregated state is the canonical read surface" is
  retired; the per-DP topic is canonical and the custom-DP-aggregate
  is curated and minimal.

### Migration

A one-shot retain-cleanup runs at first boot post-deploy: the daemon
loads the broker's existing retained inventory under its `topic_base`
(via mosquitto's `$SYS` / a transient subscribe), publishes empty
payloads to every legacy topic that no longer fits the new shape, and
records the migration in a new state file so the cleanup happens
exactly once.

HA picks up the new discovery topics automatically on republish; old
HA entities orphaned by URL changes are auto-removed because their
discovery topics get cleared by the cleanup.

### Test impact

- `internal/north/mqtt/discovery_*_test.go` is rewritten to assert
  the new multi-topic discovery shape per platform.
- `internal/central/adapter/eventbridge_*_test.go` gains a per-DP
  publish suite plus a reactive-discovery-rerender suite.
- Contract tests under `tests/contract/` add a topic-shape invariant
  asserting the documented hierarchy is respected.
- `payload.Observable` tests are deleted along with the gate.
- Snapshot fixtures under `internal/north/mqtt/discovery_bwth_snapshot_test.go`
  are regenerated; the new fixtures can be diffed against retained
  payloads from a side-by-side `aiohomematic2mqtt` run for
  cross-stack validation.

## Amendment (2026-09-12) — two hub shapes were promised and never published

The topic hierarchy above lists `hub/status`, `hub/info` and
`hub/diagnostics`, and "What gets added" names `hub/{info,diagnostics}`
as topics this ADR pins the canonical shape of.
`docs/mqtt-topic-schema.md` carried the first two as operator-facing
promises — "CCU connection status" and "CCU info snapshot" — from its
first revision. **No daemon build has ever published a byte on any of the
three.** The two documented rows are withdrawn from the schema document,
which now names all three as reserved shapes with no publisher; this
entry records the withdrawal so the rows are not silently gone.

What was measured, before changing anything:

- **Zero production callers of the builders.** `TopicBuilder.HubStatus`,
  `.HubInfo` and `.HubDiagnostics` have no caller in any non-test file.
  Every call site is a test: `HubStatus` 3 and `HubInfo` 3, in
  `internal/north/mqtt/mqtt_test.go`,
  `internal/north/mqtt/bridge_edge_cases_test.go` and
  `tests/contract/mqtt_topic_schema_doctest_test.go`; `HubDiagnostics` 2,
  in the first two of those — it reached no contract pin at all, because
  it was never documented. It has one now, alongside the other two, as a
  reserved shape.
- **Zero production callers of the free functions they delegate to.**
  `naming.MQTTHubStatus`, `MQTTHubInfo` and `MQTTHubDiagnostics` are each
  called from exactly one non-test line, `internal/north/mqtt/topics.go`
  — the builder method itself. Their only other references are
  `internal/model/naming/pathdata_hub_test.go` and one
  `strings.TrimSuffix` in `hub_topics_roundtrip_test.go`, which borrows
  `MQTTHubStatus` to derive a per-CCU prefix.
- **Nothing on the wire, in either direction.** `grep -rn "hub/status"`
  over the repository returned ten hits before this change: two in the
  schema document, six in tests, two in the builder — and none in a
  publish path. `hub/info` returned seven files and `hub/diagnostics`
  four, all of them the builder, its unit test or the doctest. No file
  under `internal/north/mqtt/testdata/` contains any of the three
  literals, so
  no discovery golden references them either — not even as a
  `state_topic` a consumer would subscribe to and never hear from.
- **Every documented sibling is real.** The remaining rows of the
  schema's "Bridge / hub status" table were checked the same way and each
  has a production producer or consumer: `bridge/status` (15 non-test
  call sites outside `topics.go`, the LWT among them), `bridge/health`
  (`bridge.go`'s `AnnounceOnline`), the sysvar, program, connectivity and
  `system/status` topics (all published from `Bridge.Publish*`, driven by
  `internal/central/adapter/hub_mqtt_publisher.go`), and the command
  rows, which the daemon consumes through wildcard subscriptions rather
  than by building each topic — `TopicBuilder.ParameterCommand` has no
  production call site of its own either, and its documented
  `values/<param>/set` and `master/<param>/set` shapes are nonetheless
  honoured, because a `+`-wildcard filter matches them. That is why the
  producer guard below classifies a command row separately instead of
  demanding a call site for it. The two hub rows were the only unkept
  promises in the document.

### Why withdrawal rather than implementation

Withdrawing a documented topic class is a wire-promise change, so ADR
0068's discipline applies: a break on the MQTT plane is permitted and is
documented rather than silent. This one is unusually cheap to take,
because the promise was never kept — there is no retained value at the
old topic to sweep, no discovery payload naming it, and no subscriber
that ever received anything. An operator who followed the document and
subscribed has been receiving nothing since the first release; the
document now tells them why.

The information each shape would have carried already reaches consumers
by another route, enumerated in the schema document's reserved-shapes
section: the `hub/info` fields are in the HA discovery device block that
`hubDeviceBlock` builds, per-interface reachability is
`hub/connectivity/<iface>`, daemon reachability is `bridge/status`, and
the radio/load diagnostics are per-device data points plus the
central-wide `system/health_score`, `system/latency` and
`system/last_event_age` metric topics.

`hub/status` is the one of the three with a real gap behind it, and it is
**reported, not implemented here.** Every CCU-scoped hub entity lists
`<base>/bridge/status` as its availability source, so a CCU that goes
unreachable while the daemon stays up leaves its entities "available"
with stale values. A per-CCU availability rollup is the missing source.
It needs a state machine (which interface states fold into "the CCU is
gone", and the debounce that keeps a reconnect from flapping the whole
CCU's entity set), a retained publish on connect and on change, an
`offline` value in the bridge's own LWT ordering, and the availability
lists of every hub entity re-pointed — which moves discovery goldens.
That is new published traffic, and it gets its own change with its own
note.

### The guard was checking the wrong half

`tests/contract/mqtt_topic_schema_doctest_test.go` pinned
`TopicBuilder.HubStatus` against the "CCU connection status" row of the
schema document and passed for the entire life of the defect. It could
not do otherwise: it compares a builder's output against a documented
string, and a builder nobody calls renders its string perfectly. The
same blindness covered every other row.

The gap is closed by
`tests/contract/mqtt_topic_schema_producer_test.go`, which parses the
schema document's own tables and requires each documented shape to be
classified as published by a named builder (which must then have a
production call site), consumed as a command topic, or reserved (which
must have none). A new row in the document fails the test until someone
classifies it, and a reserved shape that quietly gains a publisher fails
it too. The strongest form of the check — bytes arriving at a broker —
needs a broker and belongs in an integration test; a production call
site of the producing builder is the strongest statement a unit test can
make, and it is the one that was missing.

Two things stay as they are. The builders are **not deleted**: the
doctest and the roundtrip guard pin the shapes through them, and the
dead-code ratchet never saw them anyway — `script/reachability`
classifies package-level members only, so no method is ever classified
(the inventory contains no dotted identifier), and the three
`naming.MQTT*` free functions read as reachable because RTA treats the
methods that call them as live members of a live type. The ratchet's
counts do not move. And `docs/adr/0006-naming-conventions.md`'s builder
inventory is unaffected: it never listed these three.

## Status notes

The declarative source surface (`TopicSlot`, `Bucket`, `HAEntity`,
`Slotted`, `DiscoveryDynamic`) and the per-DP / per-channel topic
hierarchy are in place. Residual cleanup items — `classifyComponent`
per-parameter fallback, `service_method_routing.go` deletion, the
boot-time retain-cleanup migrator for legacy topics, and the "no
custom-DP type name in `internal/north/mqtt/` outside test fixtures"
contract invariant — land as ordinary refactors when the
surrounding files are touched.

## Amendment (2026-09-13) — `hub/status` is published: the per-CCU availability gate

The 2026-09-12 amendment above withdrew `hub/status` from the schema
document as a reserved shape and named, in the same breath, the defect
behind it: *every CCU-scoped hub entity takes availability from
`bridge/status`, so an unreachable CCU leaves its entities "available"
with stale values.* The daemon's Last Will says only whether the DAEMON
is alive. A CCU that drops off the bus — a power cut, a pulled network
cable, a ReGa that stopped answering — leaves every one of its sysvars,
programs, system scores, install-mode entities and message aggregates
looking live in Home Assistant, showing whatever they last reported, for
as long as the daemon stays up. There is no timeout behind it.

This amendment records the fix. `<base>/<central>/hub/status` is a
published topic class as of this date, moved into the schema document's
"Bridge / hub status" table, and
`tests/contract/mqtt_topic_schema_producer_test.go` classifies it
`promisePublished`. `hub/info` and `hub/diagnostics` stay reserved.

### What is on the topic

A retained `online` / `offline` marker per configured CCU, published
through the shared `publisher.AvailabilityPublisher` at **QoS 1** — the
level PR #803 pinned for every availability flip *and* every retraction,
because an availability level is written only on a transition and is
therefore not repaired by a later publish. A lost `offline` here is
precisely the defect this topic exists to fix, left unfixed.

### The fold: reachable means ANY interface, not ALL

The value is a disjunction over the CCU's per-interface reachability
states: `online` while at least one interface is reachable, `offline`
once none is.

The conjunction was the other candidate and is wrong here. One interface
down is a per-interface fault with its own entity — the connectivity
`binary_sensor` this ADR already pins at
`<base>/<central>/hub/connectivity/<iface>` — and the ordinary shapes of
it (a crashed CUxD, an unplugged HmIP-Wired gateway, a BidCoS radio
module the CCU restarts by itself) leave the ReGa logic layer answering
normally. Everything gated by this topic is ReGa-scoped, not
interface-scoped: sysvar values, program state, the system scores, the
message aggregates. Their values are not stale while ReGa is alive, and
greying them out because one radio is down would hide a working CCU
behind an unrelated fault, and would do it on a topic whose whole purpose
is to be trusted. The disjunction reports the CCU gone exactly when it is
gone, because a CCU that goes away takes every interface process with it
and they flip together.

An unobserved tracker folds to `online`. Nothing in the hub plane is
published before the CCU's serial has been read off it, so "no interface
state yet" at that point means the daemon has just demonstrated it can
talk to the CCU. Folding it the other way would publish a retained
`offline` and grey out every hub entity of a healthy CCU on every daemon
start.

### The debounce: a dwell, not a rate limit

A folded level has to hold for **15 seconds** before it is written.

The distinction matters. A rate limit would still write both ends of a
flap, just more slowly. A dwell absorbs the flap entirely: a level that
does not survive the window is never written at all, so an interface
that bounces down and up inside the window puts nothing on the wire and
Home Assistant sees nothing. Without it, a flapping CCU produces a burst
of retained messages AND strobes every CCU-scoped entity between
available and unavailable — worse than either steady state, because an
operator watching the dashboard sees values blink out and return, and
any automation with an `unavailable` trigger fires on every blink.

The dwell is symmetric: recovery is debounced exactly like loss. An
asymmetric gate that published `online` immediately would still strobe,
because a flap alternates — every upward edge would reach the broker and
only the downward ones would be held.

The one write that is never debounced is the FIRST one for a CCU. Home
Assistant holds an entity unavailable until every topic in its
`availability` list has reported a payload it recognises, so the gate's
first retained byte has to exist before the discovery configs that name
it; debouncing it would grey out the whole hub plane for the dwell on
every daemon start, against no previous level to flap with. The seed is
queued ahead of every hub discovery build in `wireOneCentral`, and the
fan-out worker is FIFO, which is what orders the byte before the configs.

### Availability lists: added alongside, never instead

Every CCU-scoped hub entity now lists `<base>/<central>/hub/status` in
its discovery `availability` block **in addition to**
`<base>/bridge/status`. Home Assistant's default `availability_mode:
"all"` is a conjunction over that list, which is exactly the wanted
semantics: available when the daemon is up AND the CCU is on the bus.
The two statements are independent and neither implies the other — a
dead daemon publishes nothing about its CCUs, and a live daemon with a
dead CCU says nothing about itself — so replacing rather than adding
would have traded one blind spot for another.

Two hub entities deliberately do not list it, for one reason stated
twice:

- The per-interface **connectivity** sensors, whose state is the fold's
  own input. Gating them on the fold makes them unavailable in exactly
  the situation they exist to report.
- The **daemon-status** sensor, which already carries no availability
  block at all for the same reason one level up.

### LWT ordering

MQTT permits exactly **one** Last Will per connection, and this daemon's
is spent on `<base>/bridge/status`. `hub/status` therefore *cannot* have
a will of its own; that is a protocol fact, not a design choice.

It needs none. Because the gate is added alongside `bridge/status` and
the list is a conjunction, the will's `bridge/status: offline` alone is
enough to make every CCU-scoped entity unavailable, whatever the per-CCU
gates still say. A retained `online` that outlives a killed daemon is a
false STATEMENT on a topic an operator can read, not a ghost entity.

The daemon repairs the statement wherever it can:

- **Graceful stop.** The broker discards the will of a client that
  disconnects cleanly, so `Bridge.AnnounceOffline` is the only thing that
  ever writes `offline`. It now writes every per-CCU gate FIRST and the
  bridge marker second, so no instant exists in which the daemon has
  declared itself gone while its CCU gates still claim reachability.
- **Ungraceful death.** The will covers the entities; the next connect
  repairs the statement, because the per-CCU seed republishes the current
  fold before that CCU's discovery configs.
- **A CCU removed from the fleet.** `offline` would be a claim about a
  CCU that no longer exists, so `RetractCentral` retracts the topic
  instead, through the same availability publisher that wrote it.

## Reference: comparable implementations

- `aiohomematic2mqtt` 2026.4.0 — per-DP `device/status/<addr>/<ch>/<param>/state`
  with JSON wrapper; treats `CLIMATE` etc. as a synthetic per-channel
  custom-DP topic.
- `homematicip_local` (Home Assistant integration) — does NOT publish
  via MQTT; it consumes aiohomematic's Python objects directly. Its
  device-info shape (manufacturer, model, model_id, sw_version,
  configuration_url, suggested_area, via_device) is the visual
  reference for the device card we want to mirror.

## Amendment (2026-09-13) — the schema document was a strict subset of the wire

`tests/contract/mqtt_topic_schema_producer_test.go`, added the day before
this entry, checks that every shape `docs/mqtt-topic-schema.md` documents is
classified and produced. It checks that direction only, and the inverse gap
was the larger one: **the document described a strict subset of what the
daemon publishes.** Eight topic families had publishers and no documented
row at all —

- `<base>/system/addon_update/{state,set}` (ADR 0057, daemon-level),
- `<base>/<central>/hub/install_mode/<iface>` and its `/set`,
- `<base>/<central>/hub/update`,
- `<base>/<central>/hub/{alarm_messages,service_messages,inbox}`,
- `<base>/<central>/system/{health_score,latency,last_event_age}`,
- `<base>/<central>/<iface>/<addr>/<ch>/week_profile/{state,set}`,
- `<base>/<central>/<iface>/<addr>/<ch>/schedule/{state,attrs,<key>/state,<key>/set}`,
- `<base>/<central>/<iface>/<addr>/<ch>/combined/<kind>` and its `/set`,

plus the device firmware-update state topic, the legacy per-type pulse
topic, the `/config` descriptor companions and the custom-DP `invoke`
shape. An external consumer reading the document had no way to learn any of
them exists. They are documented now; no publish path changed.

The guard gained the missing direction with them.
`TestMQTTTopicProducersAreDocumented` inventories every topic-shape producer
in `internal/north/mqtt/topics.go` and `internal/model/naming/pathdata.go`
by parsing those two files, and fails when a producer has no classification
or an inventoried shape has no row in the document. Adding a topic builder
is now a three-part move — function, inventory entry, documented row — and
doing fewer than three fails.

One rule it deliberately does **not** adopt: "every builder must have a
caller". That rule is wrong here and the older half of the guard already
records why — the daemon consumes commands through `+` wildcards, so
`TopicBuilder.ParameterCommand` has zero production callers while both its
documented `/set` shapes are honoured on the wire. The inventory classifies;
it does not count.

The producer scan's file exclusion narrowed in the same change. It used to
skip `topics.go` and `pathdata.go` whole, which also discarded calls made
from non-producer functions in those files — an ordinary production call
site. It now skips per enclosing function: a producer delegating to a
producer is still not a call site, but `TopicBuilder.systemMetricTopics`
calling `HubSystemHealthScore` counts, because that helper is the
retained-orphan sweep's enumeration of the shape, not a second spelling of
it.
