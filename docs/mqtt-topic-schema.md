# MQTT Topic Schema

Reference for OpenCCU-Loom's MQTT topic layout. Aimed at external
consumers (Node-RED flows, custom dashboards, Telegraf scrapers) that
subscribe to Homematic MQTT topics.

**Source of truth for topic names:** `internal/north/mqtt/topics.go`
(all Go methods delegate to `internal/model/naming/`). The canonical
function signatures are cited below.

**Related decisions:** [ADR 0002 — Multi-CCU First Class](./adr/0002-multi-ccu-first-class.md),
[ADR 0011 — MQTT Topic & Payload Architecture](./adr/0011-mqtt-topic-and-payload-architecture.md).

---

## Multi-CCU namespacing

OpenCCU-Loom is **multi-CCU from v1.0** (ADR 0002): one daemon can
bridge several CCUs simultaneously. Every topic carries a `<central>`
segment immediately below `<base>` so all CCUs share a single broker
namespace without collision. A single-CCU deployment is the
degenerate case with one entry under that segment.

---

## Schema

> Notation: `<base>` is the configured `mqtt.topic_base` (default
> `openccu-loom`). `<central>` is the CCU name from the daemon config
> (e.g. `GoOtto`). `<iface>` is the interface ID (e.g. `HmIP-RF`).
> `<addr>` is the device address (e.g. `000C9709AEF157`). `<ch>` is
> the channel number. `<param>` is the wire-parameter name.

### State topics

| Topic class | Topic |
|---|---|
| Per-DP VALUES state | `<base>/<central>/<iface>/<addr>/<ch>/values/<param>` |
| Per-DP MASTER state | `<base>/<central>/<iface>/<addr>/<ch>/master/<param>` |
| Custom-DP derived state | `<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>` |
| Device availability | `<base>/<central>/<iface>/<addr>/availability` |
| Device info snapshot | `<base>/<central>/<iface>/<addr>/info` |
| Device diagnostics | `<base>/<central>/<iface>/<addr>/diagnostics` |

Go builder methods: `TopicBuilder.ParameterState`, `TopicBuilder.SlotState`,
`TopicBuilder.DeviceAvailability`, `TopicBuilder.DeviceInfo`,
`TopicBuilder.DeviceDiagnostics`.

### Command (set) topics

| Topic class | Topic |
|---|---|
| Write single parameter (VALUES) | `<base>/<central>/<iface>/<addr>/<ch>/values/<param>/set` |
| Write MASTER parameter | `<base>/<central>/<iface>/<addr>/<ch>/master/<param>/set` |
| Custom-DP service method | `<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>/set/<method>` |

Go builder methods: `TopicBuilder.ParameterCommand`, `TopicBuilder.SlotCommand`,
`TopicBuilder.CustomDPServiceMethod`.

### HA Discovery

| Topic class | Topic |
|---|---|
| Discovery config | `homeassistant/<component>/<node_id>/<object_id>/config` |

Go builder method: `TopicBuilder.DiscoveryConfig`.

### Bridge / hub status

| Topic class | Topic |
|---|---|
| Bridge online/offline (LWT) | `<base>/bridge/status` |
| Bridge health (build + boot metadata) | `<base>/bridge/health` |
| CCU connection status | `<base>/<central>/hub/status` |
| CCU info snapshot | `<base>/<central>/hub/info` |
| System-variable state | `<base>/<central>/hub/sysvars/<name>/state` |
| System-variable set | `<base>/<central>/hub/sysvars/<name>/set` |
| Program trigger | `<base>/<central>/hub/programs/<id>/trigger` |
| Interface connectivity | `<base>/<central>/connectivity/<iface>` |
| System status event | `<base>/<central>/system/status` |

Go builder methods: `TopicBuilder.BridgeStatus`, `TopicBuilder.BridgeHealth`,
`TopicBuilder.HubStatus`, `TopicBuilder.HubInfo`,
`TopicBuilder.HubSysvar`, `TopicBuilder.HubSysvarCommand`,
`TopicBuilder.HubProgramTrigger`, `TopicBuilder.Connectivity`,
`TopicBuilder.SystemStatus`.

---

## Concrete examples

Assume `base=openccu-loom`, `central=GoOtto`, device `HmIP-BWTH`
at address `000C9709AEF157`, channel 1.

| Use case | Topic |
|---|---|
| Actual temperature (read) | `openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/1/values/ACTUAL_TEMPERATURE` |
| Set-point temperature (write) | `openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/1/values/SET_POINT_TEMPERATURE/set` |
| Climate service method (set mode) | `openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/1/custom/climate/set/set_mode` |
| CCU online status | `openccu-loom/GoOtto/hub/status` |
| System variable | `openccu-loom/GoOtto/hub/sysvars/Presence/state` |

---

## Subscription wildcards

The `<central>` segment means a two-level wildcard (`+/+`) is needed
to subscribe to all VALUES events across all CCUs:

```
openccu-loom/+/+/+/+/values/+
```

To scope to a single CCU, replace the first `+` with the central name:

```
openccu-loom/GoOtto/+/+/+/values/+
```

---

## Payload shape

OpenCCU-Loom publishes per-DP state as a slim JSON envelope:

```json
{
  "value": 21.6,
  "available": true,
  "modified_at": 1730385720.123,
  "refreshed_at": 1730385720.123
}
```

HA Jinja templates can read individual fields via `value_json.value`,
`value_json.available`, etc.

#### Optional `additional_information`

Data points that expose enriched model metadata carry it under an optional
`additional_information` object. It is **omitted entirely** for plain scalar
DPs (so the common-case payload is byte-identical to the example above) and
present only when the DP provides it. The current producer is the calculated
operating-voltage sensor, whose metadata describes the device battery:

```json
{
  "value": 2.9,
  "available": true,
  "modified_at": 1730385720.123,
  "refreshed_at": 1730385720.123,
  "additional_information": {
    "Battery Type": "LR03",
    "Battery Qty": 2,
    "Low Battery Limit": "2.2V",
    "Low Battery Limit Default": "2.2V",
    "Voltage max": "3.0V"
  }
}
```

The merge is strictly additive — every other field keeps its shape and
position. (Exposing the same metadata on the REST datapoint DTO and on the
hub service-/alarm-message aggregates is a planned follow-up.)

### `/config` companion (descriptor)

Descriptor metadata (`unit`, `type`, `min`, `max`, `default`,
`value_list`, `source` for calculated DPs) lives on the retained
`/config` companion topic next to each state topic — `…/values/
ACTUAL_TEMPERATURE/config`, `…/master/SET_POINT_TEMPERATURE/config`,
etc. The descriptor payload is published once per DP (diff-gated; no
re-publish when the descriptor bytes are unchanged) so state events
remain lean.

For per-parameter wire DPs:
```json
{ "unit": "°C", "type": "FLOAT", "paramset": "VALUES", "min": -10, "max": 50 }
```

For custom-DP aggregates the shape is domain-specific (climate emits
`hvac_modes` / `preset_modes` / `min_temp` etc., cover emits
`supports_tilt` / `inverted_control`, …). Each Custom-DP type owns its
typed descriptor in `internal/payload/descriptor.go`.

---

## Retain and QoS policy

OpenCCU-Loom retains all state, availability, info, diagnostics,
config, and discovery topics. Event topics (`/event`, pulse topics)
are non-retained QoS 0. Command (`/set`, `/trigger`, `/invoke`) topics
are non-retained and subscribed at QoS 1 (at-least-once) by default —
configurable via `QoSProfile.Commands` — so an inbound write is not
silently dropped on a flaky broker connection.
