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

> The three channel event topics carry the same JSON envelope
> `{"event_type": "<type>", "available": true, "modified_at": "…"}`.
> They are siblings rather than one topic with a kind segment: `…/event`
> was already a leaf, and nesting under it would deliver impulses to every
> `…/event` subscriber. Each kind gets its own Home Assistant `event`
> entity, whose announced `event_types` list is exactly that kind's
> parameters — a pulse whose type is not announced is dropped by Home
> Assistant without a trace, so the lists must not be merged.
>
> `impulse` and `device_error` arrived in daemon 0.69.0. Before that only
> keypress was published here, while all three reached the REST and
> WebSocket planes.
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
| Channel impulse event (not retained) | `<base>/<central>/<iface>/<addr>/<ch>/impulse` |
| Channel device-error event (not retained) | `<base>/<central>/<iface>/<addr>/<ch>/device_error` |
| Device availability | `<base>/<central>/<iface>/<addr>/availability` |
| Device info snapshot | `<base>/<central>/<iface>/<addr>/info` |
| Device diagnostics | `<base>/<central>/<iface>/<addr>/diagnostics` |
| Device firmware-update state | `<base>/<central>/<iface>/<addr>/update` |
| Per-DP descriptor companion | `<base>/<central>/<iface>/<addr>/<ch>/values/<param>/config` |
| Custom-DP descriptor companion | `<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>/config` |
| Channel per-type pulse (legacy, not retained) | `<base>/<central>/<iface>/<addr>/<ch>/event/<type>` |
| Combined-DP state | `<base>/<central>/<iface>/<addr>/<ch>/combined/<kind>` |
| Week-profile select state | `<base>/<central>/<iface>/<addr>/<ch>/week_profile/state` |
| Schedule sensor state | `<base>/<central>/<iface>/<addr>/<ch>/schedule/state` |
| Schedule sensor attributes | `<base>/<central>/<iface>/<addr>/<ch>/schedule/attrs` |
| Schedule channel switch state | `<base>/<central>/<iface>/<addr>/<ch>/schedule/<key>/state` |
| Alarm zone state † | `<base>/alarm/<zone>/state` |
| Alarm zone availability † | `<base>/alarm/<zone>/availability` |
| Alarm zone event † (not retained) | `<base>/alarm/<zone>/event` |

Go builder methods: `TopicBuilder.ParameterState`, `TopicBuilder.SlotState`,
`TopicBuilder.DeviceAvailability`, `TopicBuilder.DeviceInfo`,
`TopicBuilder.DeviceDiagnostics`, `TopicBuilder.DeviceUpdateState`,
`TopicBuilder.ParameterConfig`, `TopicBuilder.SlotConfig`,
`TopicBuilder.DataPointEvent`, `TopicBuilder.CombinedState`,
`TopicBuilder.WeekProfileState`, `TopicBuilder.ScheduleEntityState`,
`TopicBuilder.ScheduleEntityAttrs`, `TopicBuilder.ScheduleSwitchState`.

The `/config` companion row above is written for the VALUES bucket; the
MASTER and CALCULATED buckets take the same `/config` suffix on their own
state topic (`…/master/<param>/config`, `…/calculated/<param>/config`), and
so does the custom-DP aggregate. See [§`/config` companion](#config-companion-descriptor)
for the payload.

`…/event/<type>` is the **legacy** per-event-type pulse topic that predates
the three sibling channel-event topics above it. It is still published, one
message per event type, so a subscriber written against the older shape keeps
working; new consumers should read `…/event`, `…/impulse` and
`…/device_error`, which are what Home Assistant discovery declares.

`<base>/<central>/<iface>/<addr>/update` carries the HA `update` entity's
JSON state (installed version, latest version, in-progress flag). It follows
the device-scope convention of its `/availability`, `/info` and
`/diagnostics` siblings — **no `/state` suffix**, because the topic *is* the
state. See [Unwired command spellings](#unwired-command-spellings) for the
`update/set` shape, which the entity does not declare and nothing subscribes.

† No `<central>` segment — see [Alarm topics](#alarm-topics-daemon-level-no-central)
below.

### Command (set) topics

| Topic class | Topic |
|---|---|
| Write single parameter (VALUES) | `<base>/<central>/<iface>/<addr>/<ch>/values/<param>/set` |
| Write MASTER parameter | `<base>/<central>/<iface>/<addr>/<ch>/master/<param>/set` |
| Custom-DP service method | `<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>/set/<method>` |
| Combined-DP write | `<base>/<central>/<iface>/<addr>/<ch>/combined/<kind>/set` |
| Week-profile select | `<base>/<central>/<iface>/<addr>/<ch>/week_profile/set` |
| Schedule channel switch | `<base>/<central>/<iface>/<addr>/<ch>/schedule/<key>/set` |
| Custom-DP operation invoke | `<base>/<central>/devices/<addr>/cdps/<name>/<op>/invoke` |
| Per-interface install mode | `<base>/<central>/hub/install_mode/<iface>/set` |
| Add-on self-update install † | `<base>/system/addon_update/set` |
| Alarm zone command † | `<base>/alarm/<zone>/set` |

Go builder methods: `TopicBuilder.ParameterCommand`,
`TopicBuilder.CustomDPServiceMethod`, `TopicBuilder.CombinedCommand`,
`TopicBuilder.WeekProfileCommand`, `TopicBuilder.ScheduleSwitchCommand`,
`TopicBuilder.CustomDPInvoke`, `TopicBuilder.AddonUpdateCommand`;
`naming.MQTTHubInstallModeCommand`.

The daemon consumes every row above through `+`-wildcard filters
(`CommandSubscriber.routes`), not through a per-topic builder call — so a
builder here can have zero production callers while its shape is honoured
on the wire. `TopicBuilder.ParameterCommand` is exactly that case.

† No `<central>` segment. The alarm rows are explained under
[Alarm topics](#alarm-topics-daemon-level-no-central) below; the add-on
self-update is daemon-level for the same class of reason — the self-updater
is a property of the daemon process, not of any one CCU (ADR 0057).

#### Unwired command spellings

One `/set` shape has a canonical spelling in the topic builder and no wire
behaviour at either end: nothing publishes it and no subscription filter
matches it. **Do not publish to it** — the message is accepted by the broker
and read by nobody.

| Unwired shape | Builder | Subscriber |
|---|---|---|
| `<base>/<central>/<iface>/<addr>/update/set` | `TopicBuilder.DeviceUpdateCommand` | none |

The HA `update` entity declares no `command_topic`: flashing device firmware
from an unconfirmed — possibly retained and replayed — broker payload is
unsafe. The builder is kept so the spelling has one home if the command path
is ever wired, and so the shape stays pinned rather than drifting.

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
| CCU reachability gate (retained) | `<base>/<central>/hub/status` |
| System-variable state | `<base>/<central>/hub/sysvars/<name>/state` |
| System-variable set | `<base>/<central>/hub/sysvars/<name>/set` |
| Program state (active flag, retained) | `<base>/<central>/hub/programs/<id>/state` |
| Program activation set | `<base>/<central>/hub/programs/<id>/set` |
| Program trigger (run once) | `<base>/<central>/hub/programs/<id>/trigger` |
| Program execute availability | `<base>/<central>/hub/programs/<id>/execute_available` |
| Interface connectivity | `<base>/<central>/hub/connectivity/<iface>` |
| Per-interface install-mode countdown | `<base>/<central>/hub/install_mode/<iface>` |
| CCU firmware-update state | `<base>/<central>/hub/update` |
| Alarm-message aggregate | `<base>/<central>/hub/alarm_messages` |
| Service-message aggregate | `<base>/<central>/hub/service_messages` |
| Inbox aggregate | `<base>/<central>/hub/inbox` |
| System health score | `<base>/<central>/system/health_score` |
| Aggregated connection latency | `<base>/<central>/system/latency` |
| Last-event age (seconds) | `<base>/<central>/system/last_event_age` |
| System status event | `<base>/<central>/system/status` |
| Add-on self-update state (daemon-level) | `<base>/system/addon_update/state` |

`<base>/<central>/hub/status` carries `online` / `offline` and is the
**per-CCU availability gate**: every CCU-scoped hub entity lists it in its
discovery `availability` block *alongside* `<base>/bridge/status`. Home
Assistant's default `availability_mode: "all"` is a conjunction over that
list, so such an entity is available only while the daemon is up **and** its
CCU is on the bus. Before this topic existed, a CCU that went unreachable
while the daemon stayed up left every one of its sysvars, programs, system
scores and message aggregates "available" in Home Assistant, showing whatever
they last reported, indefinitely.

The value is a fold over the CCU's interface states: `online` while **at
least one** interface is reachable, `offline` once none is. One interface
down is a per-interface fault, reported by the per-interface connectivity
binary sensor above, and does not mean the CCU is gone — a CCU that is gone
takes every interface with it. A flip is debounced: a level has to hold for
15 s before it is written, so a flapping interface produces no retained
traffic and no strobing entities.

Two hub entities deliberately do **not** list it: the per-interface
connectivity binary sensors, whose state is the fold's own input, and the
"Daemon connection" sensor below. Gating either on the answer it reports
would make it unavailable in exactly the situation it exists for.

On a graceful stop the daemon writes `offline` to every per-CCU gate before
it writes the bridge marker. It carries **no Last Will of its own** — MQTT
allows one will per connection and the daemon's is spent on
`<base>/bridge/status` — and needs none: the will's `bridge/status: offline`
already makes every gated entity unavailable, because the availability list
is a conjunction. A stale retained `online` left by a killed daemon is a
false statement on a readable topic, not a ghost entity, and the next
connect repairs it before that CCU's discovery configs are republished.

`<base>/bridge/status` is also the state source of the "Daemon connection"
binary sensor published for every central. That entity carries no
availability block of its own: it reports the daemon being gone, so an
availability source pointing at the same topic would render it unavailable
in exactly the situation it exists to report.

The program `set` and `trigger` topics are **command topics** — the
daemon subscribes to them and never publishes there; only `state` and
`execute_available` carry daemon-published (retained) content. A
`trigger` message with a **non-empty** payload runs the program once
(HA's discovery button publishes `true`); an empty payload is ignored —
that is the shape of a retained-message eviction, not a command.

The three `system/*` metric topics are central-wide, retained scalars — one
health score, one aggregated connection latency, one last-event age in
seconds — and are the topics the reserved `hub/diagnostics` shape below
points consumers at. They sit under `system/`, not `hub/`, alongside
`system/status`.

The three `hub/` message aggregates (`alarm_messages`, `service_messages`,
`inbox`) are retained JSON documents of the CCU's own message lists, each
backing one Home Assistant sensor with a count state and the list in its
attributes.

`<base>/system/addon_update/state` is daemon-level and carries **no
`<central>` segment**, like the `bridge/` pair: the CCU add-on self-updater
(ADR 0057) is a property of the daemon process itself, not of any one CCU.
Its `set` companion is in the command table above.

Go builder methods: `TopicBuilder.BridgeStatus`, `TopicBuilder.BridgeHealth`,
`TopicBuilder.SystemStatus`, `TopicBuilder.HubStatus`,
`TopicBuilder.HubSystemHealthScore`, `TopicBuilder.HubConnectionLatency`,
`TopicBuilder.HubLastEventAge`, `TopicBuilder.HubUpdate`,
`TopicBuilder.AddonUpdateState`.
The sysvar/program/connectivity/install-mode/aggregate topics are built by
`internal/model/naming` free functions rather than `TopicBuilder` methods:
`naming.MQTTHubSysvarState`, `naming.MQTTHubSysvarCommand`,
`naming.MQTTHubProgramState`, `naming.MQTTHubProgramSet`,
`naming.MQTTHubProgramTrigger`,
`naming.MQTTHubProgramExecuteAvailability`, `naming.MQTTHubConnectivity`,
`naming.MQTTHubInstallModeForInterface`, `naming.MQTTHubAlarmMessages`,
`naming.MQTTHubServiceMessages`, `naming.MQTTHubInbox`.

#### Reserved `hub/` shapes — nothing publishes here

These two shapes have a topic builder and no publisher. **Do not
subscribe to them**: no daemon build has ever put a byte on either, and
an availability source or a template sensor pointing at one waits
forever.

| Reserved shape | Builder | Production publishers |
|---|---|---|
| `<base>/<central>/hub/info` | `TopicBuilder.HubInfo` | none |
| `<base>/<central>/hub/diagnostics` | `TopicBuilder.HubDiagnostics` | none |

`hub/status` and `hub/info` were promised by this document — as "CCU
connection status" and "CCU info snapshot" — from its first revision
until 2026-09-12, when both promises were withdrawn rather than kept;
see [ADR 0011's amendment of 2026-09-12](./adr/0011-mqtt-topic-and-payload-architecture.md).
`hub/status` was the one of the three the withdrawal named a real gap
behind, and on 2026-09-13 it was implemented and moved into the
published table above — the per-CCU availability gate. `hub/info` and
`hub/diagnostics` stay reserved. `hub/diagnostics` was never documented.
The builders are kept so the shapes stay pinned and cannot drift if one
of them does gain a publisher.

Where the same information already reaches a consumer:

- **CCU identity and firmware** — the fields `hub/info` would have
  carried (`model`, `sw_version`, `serial_number`, `configuration_url`)
  are in the HA discovery *device block* of every hub entity, built by
  `hubDeviceBlock` in `internal/north/mqtt/hub_discovery.go`.
- **Per-interface reachability** —
  `<base>/<central>/hub/connectivity/<iface>` above, one retained
  entity per CCU interface.
- **Daemon reachability** — `<base>/bridge/status`.
- **CCU radio and load diagnostics** — per-device data points
  (`DUTY_CYCLE`, `CARRIER_SENSE_LEVEL`, …) on their own channel topics,
  plus the central-wide metric topics `system/health_score`,
  `system/latency` and `system/last_event_age`.

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
`delay_s`). Per-zone codes have shipped, but `INVALID_CODE` and
`DURESS` never join this vocabulary: the alarm engine deliberately does
not route a duress disarm through the MQTT publisher (it fires a
Hidden journal entry and a report instead, gated by
`alarm.duress_visibility`) — see `notes/concepts/alarm-concept.md`
§13.3, §15 item 6.

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
