# MQTT Topic Schema

Reference for OpenCCU-Loom's MQTT topic layout. Aimed at external
consumers (Node-RED flows, custom dashboards, Telegraf scrapers) that
subscribe to Homematic MQTT topics.

**Source of truth for topic names:** `internal/north/mqtt/topics.go`
(all Go methods delegate to `internal/model/naming/`). The canonical
function signatures are cited below.

**Related decisions:** [ADR 0002 — Multi-CCU First Class](./adr/0002-multi-ccu-first-class.md),
[ADR 0011 — MQTT Topic & Payload Architecture](./adr/0011-mqtt-topic-and-payload-architecture.md),
[ADR 0052 — Daemon-Level Alarm MQTT Topics](./adr/0052-daemon-level-alarm-mqtt-topics.md).

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
>
> Every name-derived segment (`<central>`, `<name>`, `<key>`, …) is
> escaped for MQTT: a space, `+`, `#` and `/` each become `_`, so a CCU
> configured as `Wohn Zimmer` appears as `Wohn_Zimmer`. Case is
> preserved. The daemon resolves the escaped segment back to the
> configured name on inbound commands, so two configured names must not
> collapse onto the same segment — the daemon rejects such a
> configuration at start-up.
> `<addr>` is the device address (e.g. `000C9709AEF157`). `<ch>` is
> the channel number. `<param>` is the wire-parameter name. `<zone>`
> is an alarm-zone id, or the reserved pseudo-zone id `master`.

> The channel press-event topic carries a JSON envelope
> `{"event_type": "<type>", "available": true, "modified_at": "…"}`.
> `<type>` is the lower-cased press parameter (`press_short`,
> `press_long`, …) — except on the curated doorbell models
> (`HM-Sen-DB-PCB`, `HmIP-DBB`, `HmIP-DSD-PCB`, shared via the
> upstream data package's `device_semantics` extract), where the
> short press fires as Home Assistant's standard **`ring`** event
> type, matching the announced `event_types` of the discovered
> doorbell entity.

### State topics

| Topic class | Topic |
|---|---|
| Per-DP VALUES state | `<base>/<central>/<iface>/<addr>/<ch>/values/<param>` |
| Per-DP MASTER state | `<base>/<central>/<iface>/<addr>/<ch>/master/<param>` |
| Per-DP CALCULATED state | `<base>/<central>/<iface>/<addr>/<ch>/calculated/<param>` |
| Custom-DP derived state | `<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>` |
| Channel press event (not retained) | `<base>/<central>/<iface>/<addr>/<ch>/event` |
| Device availability | `<base>/<central>/<iface>/<addr>/availability` |
| Device info snapshot | `<base>/<central>/<iface>/<addr>/info` |
| Device diagnostics | `<base>/<central>/<iface>/<addr>/diagnostics` |
| Alarm zone state † | `<base>/alarm/<zone>/state` |
| Alarm zone availability † | `<base>/alarm/<zone>/availability` |
| Alarm zone event † (not retained) | `<base>/alarm/<zone>/event` |

Go builder methods: `TopicBuilder.ParameterState`, `TopicBuilder.SlotState`,
`TopicBuilder.DeviceAvailability`, `TopicBuilder.DeviceInfo`,
`TopicBuilder.DeviceDiagnostics`.

† No `<central>` segment — see [Alarm topics](#alarm-topics-daemon-level-no-central)
below.

### Command (set) topics

| Topic class | Topic |
|---|---|
| Write single parameter (VALUES) | `<base>/<central>/<iface>/<addr>/<ch>/values/<param>/set` |
| Write MASTER parameter | `<base>/<central>/<iface>/<addr>/<ch>/master/<param>/set` |
| Custom-DP service method | `<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>/set/<method>` |
| Alarm zone command † | `<base>/alarm/<zone>/set` |

Go builder methods: `TopicBuilder.ParameterCommand`, `TopicBuilder.SlotCommand`,
`TopicBuilder.CustomDPServiceMethod`.

† No `<central>` segment — see [Alarm topics](#alarm-topics-daemon-level-no-central)
below.

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
| Program state (active flag, retained) | `<base>/<central>/hub/programs/<id>/state` |
| Program activation set | `<base>/<central>/hub/programs/<id>/set` |
| Program trigger (run once) | `<base>/<central>/hub/programs/<id>/trigger` |
| Program execute availability | `<base>/<central>/hub/programs/<id>/execute_available` |
| Interface connectivity | `<base>/<central>/hub/connectivity/<iface>` |
| System status event | `<base>/<central>/system/status` |

The program `set` and `trigger` topics are **command topics** — the
daemon subscribes to them and never publishes there; only `state` and
`execute_available` carry daemon-published (retained) content. A
`trigger` message with a **non-empty** payload runs the program once
(HA's discovery button publishes `true`); an empty payload is ignored —
that is the shape of a retained-message eviction, not a command.

Go builder methods: `TopicBuilder.BridgeStatus`, `TopicBuilder.BridgeHealth`,
`TopicBuilder.HubStatus`, `TopicBuilder.HubInfo`, `TopicBuilder.SystemStatus`.
The sysvar/program/connectivity topics are built by `internal/model/naming`
free functions rather than `TopicBuilder` methods: `naming.MQTTHubSysvarState`,
`naming.MQTTHubSysvarCommand`, `naming.MQTTHubProgramTrigger`,
`naming.MQTTHubConnectivity`.

### Alarm topics (daemon-level, no `<central>`)

Alarm zones (`notes/concepts/alarm-concept.md` §14) are daemon-level objects: an
zone's sensors and outputs are `(central_name, DataPointKey)` pairs and
routinely span more than one configured CCU, so a zone has no single
owning central to place in the `<central>` segment. The alarm subtree
therefore omits it — a **deliberate extension** of the "every topic
carries `<central>`" rule from
[ADR 0011](./adr/0011-mqtt-topic-and-payload-architecture.md),
precedented only by the read-only `<base>/bridge/status` /
`<base>/bridge/health` pair above. See
[ADR 0052 — Daemon-Level Alarm MQTT Topics](./adr/0052-daemon-level-alarm-mqtt-topics.md)
for the rationale.

> **Interim security note:** until the alarm-codes feature ships, the
> `code` field of the JSON command form is accepted but not validated —
> broker authentication and topic ACLs are the only gate on
> `ARM_*`/`DISARM`/`SILENCE`. Restrict write access to `<base>/alarm/#`
> if the broker is not fully trusted.

`<zone>` is either a configured alarm-zone id or the reserved
pseudo-zone id `master`. The `master` topics are published only when
2 or more zones are configured and aggregate every real zone: any
`triggered` wins, else any `pending`, else any `arming`, else
all-`disarmed`, else the shared mode token when every armed zone
agrees, otherwise `armed_away` for a mixed set. Master **arm** is
best-effort — each zone arms independently and a failure surfaces as
a per-zone `FAILED_TO_ARM` detail rather than failing the whole
request (`notes/concepts/alarm-concept.md` §18 item 5, "matches G5"); master
**disarm** disarms every zone unconditionally.

#### `<base>/alarm/<zone>/state` (retained)

A bare HA `alarm_control_panel` state token, not JSON:
`disarmed`, `arming`, `pending`, `triggered`, `armed_home`,
`armed_away`, `armed_night`, `armed_vacation`, `armed_custom_bypass`.
Mapped from the engine's `(AlarmZoneState, AlarmMode)` pair: bare
`disarmed`/`arming`/`pending`/`triggered` states map to the
like-named token regardless of mode; an `armed` state maps by mode —
`perimeter`→`armed_home`, `full`→`armed_away`, `night`→`armed_night`,
`vacation`→`armed_vacation`, `custom`→`armed_custom_bypass`.

#### `<base>/alarm/<zone>/availability` (retained)

`online` / `offline`, driven by `AlarmHealthChangedEvent` and the
alarm-service lifecycle (offline while the engine is stopped or the
daemon is shutting down).

#### `<base>/alarm/<zone>/event` (JSON, not retained, QoS 0)

Follows the general event-topic policy below. Payload shape:

```json
{
  "type": "TRIGGER",
  "zone_id": "eg",
  "zone_name": "Erdgeschoss",
  "changed_by": "",
  "mode": "full",
  "open_sensors": ["..."],
  "delay_s": 30
}
```

`type` vocabulary for this slice: `TRIGGER`, `SILENCED`,
`FAILED_TO_ARM`, `DISARMED`, `ARMED`, `NOTIFICATION`. `open_sensors`
and `delay_s` are present only where meaningful (e.g. `FAILED_TO_ARM`
carries `open_sensors`; a `pending`→`triggered` transition may carry
`delay_s`). `INVALID_CODE` and `DURESS` extend this vocabulary once
per-zone codes ship (`notes/concepts/alarm-concept.md` §13.3, §15 item 6).

`NOTIFICATION` (0.43.1) is published once per enrolled notification
output at incident-fire time — for every mode the output is enrolled
in, including silent policies, and never cancelled by silence. It
carries an additional `output` field: the enrolled output's display
name, or its id when unnamed.

```json
{
  "type": "NOTIFICATION",
  "zone_id": "eg",
  "zone_name": "Erdgeschoss",
  "output": "Doorbell"
}
```

Delivery is per-output and opt-out: each notification output has its
own `notify_mqtt` / `notify_webhook` flags (both default on) — a
`false` value on `notify_mqtt` skips this MQTT entry for that output,
independent of the webhook plane's own `notify_webhook` flag. The
`alarm.notification` WebSocket broadcast (topic `alarm.panel`) is
unconditional and always fires alongside, regardless of either flag.

#### `<base>/alarm/<zone>/set` (command, not retained, QoS 1)

Two accepted payload forms:

1. **Bare HA token** (plain string): `ARM_HOME`, `ARM_AWAY`,
   `ARM_NIGHT`, `ARM_VACATION`, `ARM_CUSTOM_BYPASS`, `DISARM` —
   mapped through the inverse of the state table above.
2. **JSON form**: `{"action": "ARM_HOME", "code": "1234"}` — `action`
   accepts the same HA tokens; `code` is accepted but ignored until
   per-zone PIN policy ships.

**Loom extension**: `{"action": "SILENCE"}` mutes an active
siren/output on a `triggered` zone without disarming it. This is not
part of HA's own `alarm_control_panel` command vocabulary — it is
documented here as a raw-plane-only extension
(`notes/concepts/alarm-concept.md` §13.3).

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

An `ENUM` parameter additionally carries its value list twice — the raw
CCU tokens in `value_list` and their localised display strings in
`value_labels`, index-aligned:

```json
{
  "type": "ENUM",
  "paramset": "VALUES",
  "value_list": ["AUTO_MODE", "MANU_MODE", "PARTY_MODE", "BOOST_MODE"],
  "value_labels": ["Automatik", "Manuell", "Urlaub", "Boost"]
}
```

The tokens stay authoritative: a write has to carry one of them back to
the CCU. The labels exist because a consumer that renders values — Home
Assistant above all — has no translation table of its own, so it would
otherwise show `auto_mode` verbatim. Home Assistant discovery therefore
publishes the labels as an entity's `options` and maps them back to the
token in the `command_template`. A parameter the translation archive has
no value table for is humanised instead (`AUTO_MODE` → `Auto Mode`), the
same string the REST and UI surfaces show, so a value reads identically
wherever an operator meets it. Labels are omitted when they would be
ambiguous (a duplicate or empty label), and the raw tokens are then
published unchanged.

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


## Security & Safety plane (daemon-level)

The third daemon-level tree beside `bridge/` and `alarm/` — see
[ADR 0059](./adr/0059-security-safety-mqtt-plane.md). Like the alarm
plane it carries no `<central>` segment: a hazard class aggregates
across every configured CCU.

| Topic | Retained | Payload |
|---|---|---|
| `<base>/security/state` | yes | JSON; `state` is the folded severity (`ok`/`info`/`warning`/`alarm`/`critical`), plus per-class and per-zone facets |
| `<base>/security/alarm` | yes | JSON; `state` is `ON` while any hazard class is active, with `sources[]` and `by_class{}` |
| `<base>/security/problem` | yes | JSON; `state` is `ON` while any fault stands, with the fault list |
| `<base>/security/health` | yes | `ON` while the alarm engine reports itself unhealthy |
| `<base>/security/class/<class>` | yes | JSON; `state` is `ON`/`OFF` per hazard or fault class |
| `<base>/security/zone/<slug>` | yes | JSON; `state` is the count of active sources, with `by_class{}` |
| `<base>/security/last_alarm` | yes | The last hazard report: `subject`, `message`, `i18n_key`, `args`, `sources[]` |
| `<base>/security/last_fault` | yes | The last fault report, same shape |
| `<base>/security/event` | **no** | One hazard report per occurrence, QoS 0 |
| `<base>/security/fault` | **no** | One fault report per occurrence, QoS 0 — same payload shape as `event` |
| `<base>/security/availability` | yes | `online` / `offline` |

The two event topics are deliberately **not** retained and publish at
QoS 0: a consumer ignores retained payloads on an event topic, and a
re-delivered alarm event re-fires every automation subscribed to it. The
`last_alarm` / `last_fault` topics exist precisely because of that — a
consumer that restarts has no way to replay an event.

Each event topic has exactly **one** producer, and both carry the same
rendered-report shape. `<base>/security/fault` briefly had two: the ledger
transition wrote `fault_id` and `open_count` without any text, the rendered
report wrote `subject` and `message` without an id, and a consumer parses one
payload shape per topic — so every automation reading either field got it on
half the messages. The ledger facts live in the retained `problem` attributes
instead, which carry the full standing list with ids, count and
acknowledgement flags.

Every topic the discovery declares is a topic the plane writes.
`TestSecurityPlaneTopicsRoundTrip` compares the two sets, because they once
disagreed — discovery derived the state topic from the flat entity key
(`security/class_smoke`) while the publisher wrote the nested one
(`security/class/smoke`) — and each half passed its own tests while every
class and zone entity stayed unavailable forever.

Retained topics for a class that lost its last source, or a zone that was
deleted, are evacuated together with their discovery config. The orphan sweep
that removes stale retained configs waits until the plane has declared
itself: before that it cannot tell an orphan from an entity that has not been
published yet, and would delete the plane's own discovery at every start.

Discovery uses node id `security` and the device card
`openccu-loom_security`, deliberately separate from `openccu-loom_alarm`
so the two publishers cannot make each other's card name flap.
