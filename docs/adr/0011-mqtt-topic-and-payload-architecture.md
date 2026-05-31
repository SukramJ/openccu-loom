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

## Status notes

The declarative source surface (`TopicSlot`, `Bucket`, `HAEntity`,
`Slotted`, `DiscoveryDynamic`) and the per-DP / per-channel topic
hierarchy are in place. Residual cleanup items — `classifyComponent`
per-parameter fallback, `service_method_routing.go` deletion, the
boot-time retain-cleanup migrator for legacy topics, and the "no
custom-DP type name in `internal/north/mqtt/` outside test fixtures"
contract invariant — land as ordinary refactors when the
surrounding files are touched.

## Reference: comparable implementations

- `aiohomematic2mqtt` 2026.4.0 — per-DP `device/status/<addr>/<ch>/<param>/state`
  with JSON wrapper; treats `CLIMATE` etc. as a synthetic per-channel
  custom-DP topic.
- `homematicip_local` (Home Assistant integration) — does NOT publish
  via MQTT; it consumes aiohomematic's Python objects directly. Its
  device-info shape (manufacturer, model, model_id, sw_version,
  configuration_url, suggested_area, via_device) is the visual
  reference for the device card we want to mirror.
