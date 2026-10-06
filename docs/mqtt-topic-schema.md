# MQTT Topic Schema

Reference for OpenCCU-Loom's MQTT topic layout. Aimed at external
consumers (Node-RED flows, custom dashboards, Telegraf scrapers, the Smart
Home Engine "she") that subscribe to Homematic MQTT topics.

Since ADR 0083 the layout follows the
[mqtt-smarthome 2.0 convention](https://github.com/mqtt-smarthome/mqtt-smarthome/blob/master/SPEC.md):

```
<name>/<function>/<item...>     function ∈ connected | status | set | info | meta | maintenance | ha
```

`meta` and `ha` are this project family's own functions (§3 allows them).
`ha` exists for one reason only: to carry a Home Assistant-native document
that Home Assistant parses without a template, so that the `status` tree
keeps one payload form. Its single tenant is the JSON-schema light's state
(see [Payload shape](#payload-shape)). Nothing a value template can read
belongs there.

The same grammar is spoken by the five sibling bridges (`go-mtec2mqtt`,
`go-zendure2mqtt`, `go-homeconnect2mqtt`, `go-daikin2mqtt`,
`go-unifi2mqtt`), so a consumer learns one rule for all six. Upgrading from
the previous layout is a breaking change for raw-topic consumers — see
[Migration](#migration-from-the-pre-adr-0083-layout) at the end.

**Source of truth for topic names:** `internal/north/mqtt/topics.go`
(most methods delegate to `internal/model/naming/`; the combined-DP and
schedule shapes are composed in `topics.go` itself, and a few hub shapes
have no `TopicBuilder` wrapper and are cited as `naming.MQTT*` functions
below). The canonical function signatures are cited with each table.

Both directions are checked by
`tests/contract/mqtt_topic_schema_producer_test.go`: every shape documented
here has to be classified and produced, **and** every topic-shape producer
in those two files has to have a row here. A builder added without a row —
or a row without a producer — fails the contract suite.

**Related decisions:** [ADR 0083 — mqtt-smarthome 2.0 topic convention](./adr/0083-mqtt-smarthome-topic-convention.md),
[ADR 0002 — Multi-CCU First Class](./adr/0002-multi-ccu-first-class.md),
[ADR 0011 — MQTT Topic & Payload Architecture](./adr/0011-mqtt-topic-and-payload-architecture.md),
[ADR 0052 — Daemon-Level Alarm MQTT Topics](./adr/0052-daemon-level-alarm-mqtt-topics.md),
[ADR 0068 — `unique_id` stability](./adr/0068-unique-id-stability-per-plane.md).

---

## Instance name and multi-CCU namespacing

`<name>` is the configured `north.mqtt.topic_base` (default `openccu-loom`),
kept verbatim. The convention asks for a single topic level; a base that
spans several levels (`home/loom`) is still accepted and works, but runs
outside the convention — a tool scanning `+/info` for instances cannot see
it — and the daemon logs that once at start (`mqtt.topic_base.non_conformant`).

OpenCCU-Loom is **multi-CCU from v1.0** (ADR 0002): one daemon can bridge
several CCUs. Every per-CCU item carries a `<central>` segment as its
**first item level**, directly below the function, so all CCUs share one
broker namespace without collision. A single-CCU deployment is the
degenerate case with one entry under that segment.

### Reserved first-level items

The literal items `alarm`, `security` and `system` (and `bridge`, the old
daemon tree the migration sweep still reads) share the first item level
with `<central>`. The daemon therefore **refuses at config validation** a
central whose topic-safe name is `alarm`, `security`, `system` or `bridge`,
or one of the function names `connected`, `status`, `set`, `get`, `info`,
`meta`, `maintenance`, `ha` — the latter so the migration sweep can never mistake
an old topic for a new one. Below `<central>`, `hub`, `system` and `devices`
cannot collide with an interface, because wire interface ids are
`<central>-<interface>`.

---

## Schema

> Notation: `<name>` is the configured `north.mqtt.topic_base` (default
> `openccu-loom`). `<central>` is the CCU name from the daemon config
> (e.g. `GoOtto`). `<iface>` is the **wire** interface id — the CCU
> spells it `<central>-<interface>`, so the `HmIP-RF` interface of the
> `GoOtto` central is `GoOtto-HmIP-RF`. The bare interface token never
> appears in this segment.
>
> Every name-derived segment (`<central>`, `<sysvar>`, `<key>`, …) is
> escaped for MQTT: a space, `+`, `#` and `/` each become `_`, so a CCU
> configured as `Wohn Zimmer` appears as `Wohn_Zimmer`. Case is
> preserved. The daemon resolves the escaped segment back to the
> configured name on inbound commands, so two configured names must not
> collapse onto the same segment — the daemon rejects such a
> configuration at start-up.
> `<addr>` is the device address (e.g. `000C9709AEF157`). `<ch>` is
> the channel number. `<param>` is the wire-parameter name. `<zone>`
> is an alarm-zone id, or the reserved pseudo-zone id `master`.

Item paths are identical under `status`, `set` and `meta`: the `set` of a
status item, and its descriptor companion, live on the same path under a
different function.

### Instance topics

| Topic class | Topic |
|---|---|
| Connection level (retained, Last Will) | `<name>/connected` |
| Instance introspection (retained) | `<name>/info` |
| Process statistics (retained) | `<name>/maintenance/stats` |
| Log-level command | `<name>/maintenance/set/loglevel` |
| Restart command | `<name>/maintenance/set/restart` |

Go builder methods: `TopicBuilder.Connected`, `TopicBuilder.Info`,
`TopicBuilder.Maintenance`.

`<name>/connected` is a plain integer, retained: `0` by Last Will and on a
graceful stop, `1` while the daemon is connected to the broker and **no**
central is reachable, `2` while **at least one** central's reachability
gate (`<name>/status/<central>/online`, below) reads reachable. It replaces
the former `bridge/status` online/offline marker.

`<name>/info` is a retained JSON document published on every broker
(re)connect by go-hamqtt's `publisher.Instance`: `name` (`openccu-loom`),
`version`, `spec` (`"2.0"`), `go` (the Go runtime version), `host`, `pid`,
`started` (ISO 8601), `maintenance` (whether the maintenance topics are
enabled), plus this daemon's own fields `commit`, `build_date` and
`centrals` (the centrals the daemon serves right now, resolved on every
publish). It replaces the former `bridge/health` document.

The maintenance topics are described in [Maintenance](#maintenance).

### State topics

| Topic class | Topic |
|---|---|
| Per-DP VALUES state | `<name>/status/<central>/<iface>/<addr>/<ch>/values/<param>` |
| Per-DP MASTER state | `<name>/status/<central>/<iface>/<addr>/<ch>/master/<param>` |
| Per-DP CALCULATED state | `<name>/status/<central>/<iface>/<addr>/<ch>/calculated/<param>` |
| Custom-DP derived state | `<name>/status/<central>/<iface>/<addr>/<ch>/custom/<kind>` |
| Light state, Home Assistant's own document | `<name>/ha/<central>/<iface>/<addr>/<ch>/custom/light` |
| Channel press event (not retained) | `<name>/status/<central>/<iface>/<addr>/<ch>/event` |
| Channel impulse event (not retained) | `<name>/status/<central>/<iface>/<addr>/<ch>/impulse` |
| Channel device-error event (not retained) | `<name>/status/<central>/<iface>/<addr>/<ch>/device_error` |
| Device reachability | `<name>/status/<central>/<iface>/<addr>/online` |
| Device info snapshot | `<name>/status/<central>/<iface>/<addr>/info` |
| Device diagnostics | `<name>/status/<central>/<iface>/<addr>/diagnostics` |
| Device firmware-update state | `<name>/status/<central>/<iface>/<addr>/update` |
| Per-DP descriptor companion | `<name>/meta/<central>/<iface>/<addr>/<ch>/values/<param>` |
| Custom-DP descriptor companion | `<name>/meta/<central>/<iface>/<addr>/<ch>/custom/<kind>` |
| Combined-DP state | `<name>/status/<central>/<iface>/<addr>/<ch>/combined/<kind>` |
| Week-profile select state | `<name>/status/<central>/<iface>/<addr>/<ch>/week_profile` |
| Schedule sensor (active-entry count) | `<name>/status/<central>/<iface>/<addr>/<ch>/schedule/active_entries` |
| Schedule sensor attributes | `<name>/status/<central>/<iface>/<addr>/<ch>/schedule/attributes` |
| Schedule channel switch state | `<name>/status/<central>/<iface>/<addr>/<ch>/schedule/switch/<key>` |
| Alarm zone panel state † | `<name>/status/alarm/<zone>/panel` |
| Alarm zone reachability † | `<name>/status/alarm/<zone>/online` |
| Alarm zone event † (not retained) | `<name>/status/alarm/<zone>/event` |
| Alarm zone latched motion detectors † | `<name>/status/alarm/<zone>/triggered_motion` |

Go builder methods: `TopicBuilder.ParameterState`, `TopicBuilder.SlotState`,
`TopicBuilder.SlotHAState`,
`TopicBuilder.ChannelEvent`, `TopicBuilder.ChannelImpulse`,
`TopicBuilder.ChannelDeviceError`, `TopicBuilder.DeviceAvailability`,
`TopicBuilder.DeviceInfo`, `TopicBuilder.DeviceDiagnostics`,
`TopicBuilder.DeviceUpdateState`, `TopicBuilder.ParameterConfig`,
`TopicBuilder.SlotConfig`, `TopicBuilder.CombinedState`,
`TopicBuilder.WeekProfileState`, `TopicBuilder.ScheduleEntityState`,
`TopicBuilder.ScheduleEntityAttrs`, `TopicBuilder.ScheduleSwitchState`.

The `meta` companion row above is written for the VALUES bucket; the MASTER
and CALCULATED buckets have their own (`<name>/meta/…/master/<param>`,
`<name>/meta/…/calculated/<param>`), and so does the custom-DP aggregate.
See [§`meta` companion](#meta-companion-descriptor) for the payload.

The three channel event items are siblings rather than one item with a kind
segment: `…/event` was already a leaf, and nesting under it would deliver
impulses to every `…/event` subscriber. Each kind gets its own Home
Assistant `event` entity, whose announced `event_types` list is exactly
that kind's parameters — a pulse whose type is not announced is dropped by
Home Assistant without a trace, so the lists must not be merged. The event
type is the lower-cased press parameter (`press_short`, `press_long`, …) —
except on the curated doorbell models (`HM-Sen-DB-PCB`, `HmIP-DBB`,
`HmIP-DSD-PCB`, shared via the upstream data package's `device_semantics`
extract), where the short press fires as Home Assistant's standard
**`ring`** event type, matching the announced `event_types` of the
discovered doorbell entity.

The former per-event-type pulse `…/<ch>/event/<type>` is **gone**: under
the convention the event type is the status object's `val`, not a topic
level of its own.

`<name>/status/<central>/<iface>/<addr>/update` carries the HA `update`
entity's state (installed version, latest version, in-progress flag). See
[Unwired command spellings](#unwired-command-spellings) for the
`set/…/update` shape, which the entity does not declare and nothing
subscribes.

† No `<central>` segment — see [Alarm topics](#alarm-topics-daemon-level-no-central)
below.

### Command (set) topics

| Topic class | Topic |
|---|---|
| Write single parameter (VALUES) | `<name>/set/<central>/<iface>/<addr>/<ch>/values/<param>` |
| Write MASTER parameter | `<name>/set/<central>/<iface>/<addr>/<ch>/master/<param>` |
| Custom-DP service method | `<name>/set/<central>/<iface>/<addr>/<ch>/custom/<kind>/<method>` |
| Combined-DP write | `<name>/set/<central>/<iface>/<addr>/<ch>/combined/<kind>` |
| Week-profile select | `<name>/set/<central>/<iface>/<addr>/<ch>/week_profile` |
| Schedule channel switch | `<name>/set/<central>/<iface>/<addr>/<ch>/schedule/switch/<key>` |
| Custom-DP operation (action) | `<name>/set/<central>/devices/<addr>/cdps/<cdp>/<op>` |
| Per-interface install mode | `<name>/set/<central>/hub/install_mode/<iface>` |
| Add-on self-update install † | `<name>/set/system/addon_update` |
| Alarm zone panel command † | `<name>/set/alarm/<zone>/panel` |

Go builder methods: `TopicBuilder.ParameterCommand`,
`TopicBuilder.CustomDPServiceMethod`, `TopicBuilder.CombinedCommand`,
`TopicBuilder.WeekProfileCommand`, `TopicBuilder.ScheduleSwitchCommand`,
`TopicBuilder.CustomDPInvoke`, `TopicBuilder.AddonUpdateCommand`;
`naming.MQTTHubInstallModeCommand`.

The daemon consumes every row above through `+`-wildcard filters below
`<name>/set/` (`CommandSubscriber.routes`), not through a per-topic builder
call — so a builder here can have zero production callers while its shape is
honoured on the wire. `TopicBuilder.ParameterCommand` is exactly that case.
The filter set is pairwise disjoint: the data-point filter
`<name>/set/+/+/+/+/+/+` also carries the combined-DP item and the
custom-DP operation item, which have the same length and are dispatched from
inside its handler.

† No `<central>` segment. The alarm rows are explained under
[Alarm topics](#alarm-topics-daemon-level-no-central) below; the add-on
self-update is daemon-level for the same class of reason — the self-updater
is a property of the daemon process, not of any one CCU (ADR 0057).

#### `set` payloads

- A plain value or `{"val": …}` is accepted for every item. Other JSON
  objects (and arrays) are **structured parameters**: the service-method
  bodies of ADR 0009, the alarm panel's `{"action", "code"}` form and the
  custom-DP operation body `{"params": {…}, "priority": "…"}` arrive
  unchanged. Booleans are read as `true`/`false`, `1`/`0`, `on`/`off`,
  `yes`/`no`, case-insensitively.
- **Empty payloads are ignored**, and so are **retained** messages: an
  empty message is what clearing a retained topic looks like on a live
  subscription, and a retained command would replay on every reconnect. An
  action item (program `trigger`, a custom-DP operation, the add-on install)
  fires on any non-empty payload; a zero-argument service method or
  operation takes `{}`.
- The one exception is `<name>/maintenance/set/restart`, which fires on
  **any** non-retained payload, the empty one included — the Smart Home
  Engine publishes it empty.
- A rejected or failed `set` is logged at `warn` with its topic and payload.
  The alarm panel's `code` field is redacted before it is logged.
- There is no echo and no acknowledgement on MQTT: the next status item is
  the answer.

#### Unwired command spellings

One `set` shape has a canonical spelling in the topic builder and no wire
behaviour at either end: nothing publishes it and no subscription filter
matches it. **Do not publish to it** — the message is accepted by the broker
and read by nobody.

| Unwired shape | Builder | Subscriber |
|---|---|---|
| `<name>/set/<central>/<iface>/<addr>/update` | `TopicBuilder.DeviceUpdateCommand` | none |

The HA `update` entity declares no `command_topic`: flashing device firmware
from an unconfirmed — possibly retained and replayed — broker payload is
unsafe. The builder is kept so the spelling has one home if the command path
is ever wired, and so the shape stays pinned rather than drifting.

### HA Discovery

| Topic class | Topic |
|---|---|
| Discovery config | `homeassistant/<component>/<node_id>/<object_id>/config` |

Go builder method: `TopicBuilder.DiscoveryConfig`.

The discovery topic form, every `unique_id`, every node id and every device
identifier are **unchanged** by ADR 0083 (ADR 0068 stays in force): Home
Assistant keys its entity registry on `unique_id` and re-points an existing
entity to its new state and command topics on the next discovery publish.

`homeassistant/` is Home Assistant's own tree, shared by every integration
and every daemon on the broker, so the `<node_id>` segment is what keeps one
daemon's retained configs apart from another's. It is composed:

```
<node_id> = [<base-slug>_]<scope>
```

- `<base-slug>_` is `north.mqtt.topic_base` slugged, and is present **only
  when the base is not the default** `openccu-loom`. A daemon that never set
  a base writes the node ids it always wrote.
- `<scope>` is `<central-slug>_<address>` for a device entity,
  `<central-slug>_<kind>` for a hub entity, and the bare literal `alarm`,
  `security` or `daemon` for the three daemon-level planes, which belong to
  the process rather than to any one CCU.

The base scope is built by `TopicBuilder.DiscoveryNodeScope`, which renders
part of this segment and no topic of its own.

What the discovery payloads read:

- `state_topic` is `<name>/status/<item>`, `command_topic` is
  `<name>/set/<item>`, and every `value_template` reads the status object's
  `val` — `{{ value_json.val }}`, `{{ value_json.val | lower }}` on the
  platforms whose state is compared against `true`/`false` payloads
  (Jinja renders a JSON boolean as `True`), `{{ value_json.val.<field> }}`
  for a field of a document. Event entities render the event document Home
  Assistant parses from the status object:
  `{{ dict(value_json.hm or {}, event_type=value_json.val) | tojson }}`.
- **Availability**, `availability_mode: all`: `<name>/connected`, available
  at level 2 — `{{ 'online' if value | int(0) >= 2 else 'offline' }}` — plus
  the entity's own reachability item: the device's, the central's, the alarm
  zone's or the security plane's `online` item, or a program's
  `execute_available`, each read with `{{ value_json.val | lower }}` against
  `true`/`false`. A per-parameter entity also lists its own status item,
  read with `{{ value_json.hm.available | lower }}`. Each list entry carries
  only `topic`, `payload_available`, `payload_not_available` and
  `value_template`.
- Level 2 means a central is reachable, so the entities that report on the
  daemon or on that reachability are gated at level 1 instead (`>= 1`):
  the per-interface connectivity sensors and the add-on update entity. The
  "Daemon connection" sensor reads `<name>/connected` at level ≥ 1 itself
  and carries no availability block of its own.
- `text` entities send `{{ {"val": value} | tojson }}`. The plain `set`
  form cannot carry an empty string (an empty payload is ignored) or one
  starting with `{` or `[` (read as JSON); wrapped, both arrive verbatim.

### Bridge / hub status

| Topic class | Topic |
|---|---|
| CCU reachability gate (retained) | `<name>/status/<central>/online` |
| System-variable state | `<name>/status/<central>/hub/sysvars/<sysvar>` |
| System-variable set | `<name>/set/<central>/hub/sysvars/<sysvar>` |
| Program active flag (retained) | `<name>/status/<central>/hub/programs/<id>/active` |
| Program activation set | `<name>/set/<central>/hub/programs/<id>/active` |
| Program trigger (run once, action) | `<name>/set/<central>/hub/programs/<id>/trigger` |
| Program execute availability | `<name>/status/<central>/hub/programs/<id>/execute_available` |
| Interface connectivity | `<name>/status/<central>/hub/connectivity/<iface>` |
| Per-interface install-mode countdown | `<name>/status/<central>/hub/install_mode/<iface>` |
| CCU firmware-update state | `<name>/status/<central>/hub/update` |
| Alarm-message aggregate | `<name>/status/<central>/hub/alarm_messages` |
| Service-message aggregate | `<name>/status/<central>/hub/service_messages` |
| Inbox aggregate | `<name>/status/<central>/hub/inbox` |
| System health score | `<name>/status/<central>/system/health_score` |
| Aggregated connection latency | `<name>/status/<central>/system/latency` |
| Last-event age (seconds) | `<name>/status/<central>/system/last_event_age` |
| System status event (not retained) | `<name>/status/<central>/system/status` |
| Add-on self-update state (daemon-level) | `<name>/status/system/addon_update` |

`<name>/status/<central>/online` is a boolean status item and the
**per-CCU availability gate**: every CCU-scoped hub entity lists it in its
discovery `availability` block *alongside* `<name>/connected`. Home
Assistant's `availability_mode: "all"` is a conjunction over that list, so
such an entity is available only while the daemon runs **and** its CCU is
on the bus. Without the gate, a CCU that went unreachable while the daemon
stayed up would leave every one of its sysvars, programs, system scores and
message aggregates "available" in Home Assistant, showing whatever they last
reported, indefinitely. The gates also drive `<name>/connected`: `2` while
any of them reads `true`, `1` otherwise.

The value is a **conjunction of two signals**: `true` while at least one
interface is reachable **and** ReGa answers. The first half is a disjunction
over the CCU's interface states — one interface down is a per-interface
fault, reported by the per-interface connectivity binary sensor above, and
does not mean the CCU is gone, while a CCU that is gone takes every interface
with it. The second half is a 30 s poll of the CCU's own
`/ise/checkrega.cgi`: the entities this item gates are ReGa-scoped, so a
ReGaHss that dies or hangs while `rfd`/`HMIPServer` keep serving freezes
every sysvar, program and system score while every interface still reads
reachable. An answer that is not `OK` flips the gate at once; three
consecutive probes that do not complete at all are needed before silence
counts as down; a CCU that answers 401/403/404 (the CGI behind auth, or
absent on that firmware) latches the probe off and folds as if the signal did
not exist. A flip is debounced: a level has to hold for 15 s before it is
written, so a flapping interface produces no retained traffic and no strobing
entities.

Two hub entities deliberately do **not** list it: the per-interface
connectivity binary sensors, whose state is the fold's own input, and the
"Daemon connection" sensor. Gating either on the answer it reports would make
it unavailable in exactly the situation it exists for.

On a graceful stop the daemon writes `false` to every per-CCU gate before it
writes `<name>/connected` = `0`. The gate carries **no Last Will of its own**
— MQTT allows one will per connection and the daemon's is spent on
`<name>/connected` — and needs none: the will's `0` already makes every gated
entity unavailable, because the availability list is a conjunction. A stale
retained `true` left by a killed daemon is a false statement on a readable
topic, not a ghost entity, and the next connect repairs it before that CCU's
discovery configs are republished.

The program `active` and `trigger` items under `set` are **command items** —
the daemon subscribes to them and never publishes there; the status items
`active` and `execute_available` carry the daemon-published (retained)
content. A `trigger` message with a **non-empty** payload runs the program
once (HA's discovery button publishes `true`); an empty payload is ignored.

The three `system/*` metric items are central-wide, retained scalars — one
health score, one aggregated connection latency, one last-event age in
seconds — and are the items the reserved `hub/diagnostics` shape below points
consumers at. They sit under `system/`, not `hub/`, alongside `system/status`.

The three `hub/` message aggregates (`alarm_messages`, `service_messages`,
`inbox`) carry the CCU's own message lists as their `val`, each backing one
Home Assistant sensor with a count state and the list in its attributes.

#### Feature-gated hub entities

Four hub entities are **declared only while the central offers the
feature behind them**: the alarm-message aggregate (`hub.alarm_messages`),
the service-message aggregate (`hub.service_messages`), the inbox
aggregate (`hub.inbox`), and the CCU firmware-update entity
(`hub.system_update`). A CCU offers all four, so nothing changes there.
On an openccu-lite central the alarm-message aggregate and the inbox are
never published — the box has neither — and the service-message
aggregate and the update entity are published only while the token
carries `system:read`. When a central's feature set changes at runtime (a token
re-paired, a scope revoked), the affected entity's discovery config is
retracted through the same retained-empty-payload path an ordinary
device removal uses — the daemon never leaves a stale discovery config
pointing at a feature that stopped being offered. See
[Connecting an openccu-lite system](admin/openccu-lite.md) for what
gates each feature.

`<name>/status/system/addon_update` is daemon-level and carries **no
`<central>` segment**: the CCU add-on self-updater (ADR 0057) is a property
of the daemon process itself, not of any one CCU. Its `set` companion is in
the command table above.

Go builder methods: `TopicBuilder.SystemStatus`, `TopicBuilder.HubStatus`,
`TopicBuilder.HubSystemHealthScore`, `TopicBuilder.HubConnectionLatency`,
`TopicBuilder.HubLastEventAge`, `TopicBuilder.HubUpdate`,
`TopicBuilder.AddonUpdateState`.
The sysvar/program/connectivity/install-mode/aggregate items are built by
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
| `<name>/status/<central>/hub/info` | `TopicBuilder.HubInfo` | none |
| `<name>/status/<central>/hub/diagnostics` | `TopicBuilder.HubDiagnostics` | none |

`hub/info` was promised by this document — as "CCU info snapshot" — from its
first revision until 2026-09-12, when the promise was withdrawn rather than
kept; see [ADR 0011's amendment of 2026-09-12](./adr/0011-mqtt-topic-and-payload-architecture.md).
The CCU reachability gate that was withdrawn with it was implemented on
2026-09-13 and is the published `<central>/online` item above.
`hub/diagnostics` was never documented as published. The builders are kept so
the shapes stay pinned and cannot drift if one of them does gain a publisher.

Where the same information already reaches a consumer:

- **CCU identity and firmware** — the fields `hub/info` would have
  carried (`model`, `sw_version`, `serial_number`, `configuration_url`)
  are in the HA discovery *device block* of every hub entity, built by
  `hubDeviceBlock` in `internal/north/mqtt/hub_discovery.go`.
- **Per-interface reachability** —
  `<name>/status/<central>/hub/connectivity/<iface>` above, one retained
  entity per CCU interface.
- **Daemon reachability** — `<name>/connected`.
- **CCU radio and load diagnostics** — per-device data points
  (`DUTY_CYCLE`, `CARRIER_SENSE_LEVEL`, …) on their own channel items,
  plus the central-wide metric items `system/health_score`,
  `system/latency` and `system/last_event_age`.

### Alarm topics (daemon-level, no `<central>`)

Alarm zones (`notes/concepts/alarm-concept.md` §14) are daemon-level objects: a
zone's sensors and outputs are `(central_name, DataPointKey)` pairs and
routinely span more than one configured CCU, so a zone has no single
owning central to place in the `<central>` segment. The alarm items
therefore put the literal `alarm` where `<central>` would sit — see
[ADR 0052 — Daemon-Level Alarm MQTT Topics](./adr/0052-daemon-level-alarm-mqtt-topics.md)
for the rationale, and [Reserved first-level items](#reserved-first-level-items)
for why no central may be named `alarm`.

> **Security note:** broker authentication and topic ACLs are the only
> gate on who may publish to `<name>/set/alarm/…` — per-zone codes, where
> configured, are validated by the daemon, but anyone who can publish can
> try. Restrict write access to `<name>/set/alarm/#` if the broker is not
> fully trusted.

`<zone>` is either a configured alarm-zone id or the reserved
pseudo-zone id `master`. The `master` items are published only when
2 or more zones are configured and aggregate every real zone: any
`triggered` wins, else any `pending`, else any `arming`, else
all-`disarmed`, else the shared mode token when every armed zone
agrees, otherwise `armed_away` for a mixed set. Master **arm** is
best-effort — each zone arms independently and a failure surfaces as
a per-zone `FAILED_TO_ARM` detail rather than failing the whole
request (`notes/concepts/alarm-concept.md` §18 item 5, "matches G5"); master
**disarm** disarms every zone unconditionally.

The panel is one item, `panel`: the `alarm_control_panel` state token is its
status, the `ARM_*` command its `set`.

#### `<name>/status/alarm/<zone>/panel` (retained)

A status object whose `val` is a Home Assistant `alarm_control_panel` state
token: `disarmed`, `arming`, `pending`, `triggered`, `armed_home`,
`armed_away`, `armed_night`, `armed_vacation`, `armed_custom_bypass`.
Mapped from the engine's `(AlarmZoneState, AlarmMode)` pair: bare
`disarmed`/`arming`/`pending`/`triggered` states map to the
like-named token regardless of mode; an `armed` state maps by mode —
`perimeter`→`armed_home`, `full`→`armed_away`, `night`→`armed_night`,
`vacation`→`armed_vacation`, `custom`→`armed_custom_bypass`.

#### `<name>/status/alarm/<zone>/online` (retained)

A boolean status item, driven by `AlarmHealthChangedEvent` and the
alarm-service lifecycle (`false` while the engine is stopped or the
daemon is shutting down).

#### `<name>/status/alarm/<zone>/triggered_motion` (retained)

The number of latched motion detectors of the zone (every zone for
`master`) as `val`. Its "clear latched motion detectors" button publishes
`RESET_MOTION` on the panel's `set` item.

#### `<name>/status/alarm/<zone>/event` (not retained, QoS 0)

Follows the general event rule below: the event type is `val`, every other
field is under `hm`.

```json
{
  "val": "TRIGGER",
  "ts": 1730385720123,
  "lc": 1730385720123,
  "hm": {
    "zone_id": "eg",
    "zone_name": "Erdgeschoss",
    "mode": "full",
    "open_sensors": ["..."],
    "delay_s": 30
  }
}
```

Type vocabulary: `TRIGGER`, `SILENCED`, `FAILED_TO_ARM`, `DISARMED`,
`ARMED`, `NOTIFICATION`. `open_sensors` and `delay_s` are present only where
meaningful (e.g. `FAILED_TO_ARM` carries `open_sensors`; a
`pending`→`triggered` transition may carry `delay_s`); `changed_by` and
`sources` likewise. Per-zone codes have shipped, but `INVALID_CODE` and
`DURESS` never join this vocabulary: the alarm engine deliberately does
not route a duress disarm through the MQTT publisher (it fires a
Hidden journal entry and a report instead, gated by
`alarm.duress_visibility`) — see `notes/concepts/alarm-concept.md`
§13.3, §15 item 6.

`NOTIFICATION` is published once per enrolled notification output at
incident-fire time — for every mode the output is enrolled in, including
silent policies, and never cancelled by silence. It carries an additional
`hm.output` field: the enrolled output's display name, or its id when
unnamed.

Delivery is per-output and opt-out: each notification output has its
own `notify_mqtt` / `notify_webhook` flags (both default on) — a
`false` value on `notify_mqtt` skips this MQTT entry for that output,
independent of the webhook plane's own `notify_webhook` flag. The
`alarm.notification` WebSocket broadcast (topic `alarm.panel`) is
unconditional and always fires alongside, regardless of either flag.

#### `<name>/set/alarm/<zone>/panel` (command, QoS 1)

Accepted payloads:

1. **Bare HA token** (plain string, or `{"val": "…"}`): `ARM_HOME`,
   `ARM_AWAY`, `ARM_NIGHT`, `ARM_VACATION`, `ARM_CUSTOM_BYPASS`, `DISARM` —
   mapped through the inverse of the state table above.
2. **JSON form**: `{"action": "ARM_HOME", "code": "1234"}` — `action`
   accepts the same HA tokens; `code` is validated against the zone's code
   policy. It is redacted in every log line.

**Loom extensions**: `SILENCE` mutes an active siren/output on a `triggered`
zone without disarming it, `TRIGGER` raises the panic path, and
`RESET_MOTION` clears the zone's latched motion detectors. None is part of
HA's own `alarm_control_panel` command vocabulary; they are raw-plane
extensions (`notes/concepts/alarm-concept.md` §13.3).

---

## Concrete examples

Assume `name=openccu-loom`, `central=GoOtto`, device `HmIP-BWTH`
at address `000C9709AEF157`, channel 1.

| Use case | Topic |
|---|---|
| Actual temperature (read) | `openccu-loom/status/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/1/values/ACTUAL_TEMPERATURE` |
| Set-point temperature (write) | `openccu-loom/set/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/1/values/SET_POINT_TEMPERATURE` |
| Climate service method (set mode) | `openccu-loom/set/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/1/custom/climate/set_mode` |
| System variable | `openccu-loom/status/GoOtto/hub/sysvars/Presence` |
| Device reachability | `openccu-loom/status/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/online` |
| CCU reachability | `openccu-loom/status/GoOtto/online` |
| Instance level | `openccu-loom/connected` |

---

## Subscription wildcards

The `<central>` segment means a two-level wildcard (`+/+`) is needed
to subscribe to all VALUES items across all CCUs:

```
openccu-loom/status/+/+/+/+/values/+
```

To scope to a single CCU, replace the first `+` with the central name:

```
openccu-loom/status/GoOtto/+/+/+/values/+
```

Every status item of the daemon is below `openccu-loom/status/#`; every
instance on the broker answers `+/info` (a multi-level base is the one
exception, see above).

---

## Payload shape

Every status item — state, reachability, document, event — is an
mqtt-smarthome 2.0 **status object** (one form, no plain opt-out):

```json
{
  "val": 21.6,
  "ts": 1730385720123,
  "lc": 1730385720123,
  "hm": { "available": true }
}
```

- `val` is the value: a JSON boolean, number or string, or a structured
  value. Units never appear in payloads, only in discovery and `meta`.
- `ts` is the time of the observation that caused the publish, `lc` the
  time `val` last changed; integer milliseconds since the epoch. After a
  daemon restart `lc` is the first observation (known limitation).
- `hm` is this daemon's one project-extension key. For a data point it holds
  `available` and, when the data point provides it,
  `additional_information`.
- `ON`/`OFF` payloads are booleans. Enums carry the raw CCU token; labels
  live in discovery and in the `meta` companion.
- Status items are published at the state QoS (0) on change and on every
  broker (re)connect; the `online` reachability items are published at QoS 1,
  on a transition and on every reconnect.

Which value goes in `val`:

| Item | `val` | `hm` |
|---|---|---|
| Per-DP state (`values`/`master`/`calculated`) | the parameter value (`null` before the first observation) | `available`, `additional_information` |
| `online` items, `execute_available`, connectivity, schedule switch, program `active` | boolean | — |
| Device `info`, `diagnostics`, `update`; custom-DP aggregates; message aggregates; hub and add-on `update`; schedule `attributes`; security `last_alarm`/`last_fault` | the whole document | — |
| Alarm panel | the state token | — |
| Security `severity`, `alarm`, `problem`, `class/<class>`, `zone/<slug>` | the primary value (severity token, boolean, source count) | the facets |
| Security `health` | boolean (`true` = the alarm engine reports a problem) | — |
| Channel `event`/`impulse`/`device_error` (not retained) | the event type | `available` (always `true`) |
| Alarm `event` (not retained) | the event type | the rest of the event |
| Security `event`/`fault` (not retained) | the report's verb (`event_type`) | the rest of the rendered report |
| `<central>/system/status` (not retained) | the component that changed | the rest of the event; `ts` is the event's time |

**The `ha` twin of the light.** Every status item is a status object,
the custom-DP light aggregate
`<name>/status/<central>/<iface>/<addr>/<ch>/custom/light` included: its
`val` is the light's state document (`{"state": "ON", "brightness": …}`).
The discovered light uses Home Assistant's `schema: json`, which parses its
state topic's document natively and supports no value template, so it
cannot read `val`. The same document is therefore also published bare and
retained on `<name>/ha/<central>/<iface>/<addr>/<ch>/custom/light`, and the
light entity's `state_topic` points there; its `command_topic` stays under
`set`. Both come from one publish, leave together when the device is
removed, and are republished together on reconnect.

#### Optional `additional_information`

Data points that expose enriched model metadata carry it under
`hm.additional_information`. It is **omitted entirely** for plain scalar
DPs and present only when the DP provides it. The current producer is the
calculated operating-voltage sensor, whose metadata describes the device
battery:

```json
{
  "val": 2.9,
  "ts": 1730385720123,
  "lc": 1730385720123,
  "hm": {
    "available": true,
    "additional_information": {
      "Battery Type": "LR03",
      "Battery Qty": 2,
      "Low Battery Limit": "2.2V",
      "Low Battery Limit Default": "2.2V",
      "Voltage max": "3.0V"
    }
  }
}
```

### `meta` companion (descriptor)

Descriptor metadata (`unit`, `type`, `min`, `max`, `default`,
`value_list`, `source` for calculated DPs) lives on the retained `meta`
item with the same item path as each state item — `<name>/meta/…/values/
ACTUAL_TEMPERATURE`, `<name>/meta/…/master/SET_POINT_TEMPERATURE`, etc. Its
payload is the plain descriptor document, unchanged by ADR 0083. The
descriptor is published once per DP (diff-gated; no re-publish when the
descriptor bytes are unchanged) so state events remain lean.

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

OpenCCU-Loom retains every status item except the events, plus the `meta`
companions, the light's `ha` twin, `<name>/connected`, `<name>/info`, `<name>/maintenance/stats` and
the discovery configs. Event items (`event`, `impulse`, `device_error`, alarm
and security events, `system/status`) are non-retained QoS 0. Command
(`set`) items are non-retained and subscribed at QoS 1 (at-least-once) — a
subscription QoS is an upper bound, so a consumer that publishes a `set` at
QoS 0 still gets QoS 0, which is how a consumer controls the duplicate risk
of an action item.

---

## Maintenance

mqtt-smarthome 2.0 §7, served by go-hamqtt's `publisher.Instance` on the
command plane's router. **On by default**; `north.mqtt.maintenance.enabled:
false` switches all of it off (and `info.maintenance` then reads `false`).

| Topic | Effect |
|---|---|
| `<name>/maintenance/set/loglevel` | `error` / `warn` / `info` / `debug` (any case, plain or `{"val": …}`) sets the **root level** of the daemon's log-level registry; per-path overrides are untouched; not persisted |
| `<name>/maintenance/set/restart` | graceful shutdown (`connected` → `0`, exit 0) for the supervisor to restart — **only** when a supervisor is detected (`OPENCCU_LOOM_SUPERVISOR=1`, systemd as parent, Kubernetes, or `/.dockerenv`, the same predicate the REST restart is mounted behind); otherwise refused and logged at `warn`. Shares the REST restart's 30 s once-only latch and SIGTERM path and writes the same audit entry. Fires on any non-retained payload, the empty one included |
| `<name>/maintenance/stats` | retained, every `north.mqtt.maintenance.stats_interval_seconds` (default 60, `0` = off): `rss`, `heapUsed`, `heapTotal`, `cpu`, `uptime`, `ts` |

Retained maintenance commands are ignored. Any other item under
`maintenance/set/` is logged at `warn` and ignored.

**Security**: anyone who may publish on the broker can restart the daemon
or raise its log level. On REST the restart is admin-gated; on MQTT the only
gate is the broker's ACLs (the daemon needs `<name>/#` and the discovery
prefix). Operators on an unsecured broker disable maintenance.

---

## Security & Safety plane (daemon-level)

The second daemon-level tree beside `alarm/` — see
[ADR 0059](./adr/0059-security-safety-mqtt-plane.md). Like the alarm
plane it carries no `<central>` segment: a hazard class aggregates
across every configured CCU.

| Topic | Retained | Payload |
|---|---|---|
| `<name>/status/security/severity` | yes | `val` is the folded severity (`ok`/`info`/`warning`/`alarm`/`critical`); per-class and per-zone facets under `hm` |
| `<name>/status/security/alarm` | yes | `val` is `true` while any hazard class is active; `sources[]` and `by_class{}` under `hm` |
| `<name>/status/security/problem` | yes | `val` is `true` while any fault stands; the fault list under `hm` |
| `<name>/status/security/health` | yes | `val` is `true` while the alarm engine reports itself unhealthy |
| `<name>/status/security/class/<class>` | yes | `val` is `true`/`false` per hazard or fault class; facets under `hm` |
| `<name>/status/security/zone/<slug>` | yes | `val` is the count of active sources; `by_class{}` under `hm` |
| `<name>/status/security/last_alarm` | yes | `val` is the last hazard report: `subject`, `message`, `i18n_key`, `args`, `sources[]`, `at`, … |
| `<name>/status/security/last_fault` | yes | The last fault report, same shape |
| `<name>/status/security/event` | **no** | One hazard report per occurrence, QoS 0: the verb in `val`, the rest of the report under `hm` |
| `<name>/status/security/fault` | **no** | One fault report per occurrence, QoS 0 — same shape as `event` |
| `<name>/status/security/online` | yes | boolean |

The folded severity's entity keeps its `unique_id` (`loom_security_state`);
only its item is named `severity`, because under the convention a `state`
leaf would read as a function suffix.

The two event items are deliberately **not** retained and publish at
QoS 0: a consumer ignores retained payloads on an event item, and a
re-delivered alarm event re-fires every automation subscribed to it. The
`last_alarm` / `last_fault` items exist precisely because of that — a
consumer that restarts has no way to replay an event.

Each event item has exactly **one** producer, and both carry the same
rendered-report shape. `security/fault` briefly had two: the ledger
transition wrote `fault_id` and `open_count` without any text, the rendered
report wrote `subject` and `message` without an id, and a consumer parses one
payload shape per item — so every automation reading either field got it on
half the messages. The ledger facts live in the retained `problem` facets
instead, which carry the full standing list with ids, count and
acknowledgement flags.

Every item the discovery declares is an item the plane writes.
`TestSecurityPlaneTopicsRoundTrip` compares the two sets, because they once
disagreed — discovery derived the state topic from the flat entity key
(`security/class_smoke`) while the publisher wrote the nested one
(`security/class/smoke`) — and each half passed its own tests while every
class and zone entity stayed unavailable forever.

Retained items for a class that lost its last source, or a zone that was
deleted, are evacuated together with their discovery config. The orphan sweep
that removes stale retained configs waits until the plane has declared
itself: before that it cannot tell an orphan from an entity that has not been
published yet, and would delete the plane's own discovery at every start.

Discovery uses node id `security` and the device card
`openccu-loom_security`, deliberately separate from `openccu-loom_alarm`
so the two publishers cannot make each other's card name flap.

---

## Migration from the pre-ADR-0083 layout

ADR 0083 is a clean break, with no compatibility switch: the function moved
from the topic suffix (`…/state`, `…/set`, `…/config`) to the second level,
and every status payload became a status object.

**Who breaks:** every raw-topic consumer — Node-RED flows, dashboards,
Telegraf and `mosquitto_sub` scripts, and any template reading
`value_json.value`. **Home Assistant users do not:** discovery re-points
existing entities to the new topics, and `unique_id`, node ids, device
identifiers and the discovery topic form are unchanged. A Home Assistant
dashboard that reads an entity keeps working; one that subscribes to a raw
topic does not.

Old and new, side by side (`<base>` is the old prefix, `<name>` the same
configured value):

| Before | After |
|---|---|
| `<base>/<central>/<iface>/<addr>/<ch>/values/<param>` (also `master`, `calculated`) | `<name>/status/<central>/<iface>/<addr>/<ch>/values/<param>` |
| `<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>` | `<name>/status/<central>/<iface>/<addr>/<ch>/custom/<kind>` |
| `<base>/<central>/<iface>/<addr>/<ch>/event` (also `impulse`, `device_error`; not retained) | `<name>/status/<central>/<iface>/<addr>/<ch>/event` |
| `<base>/<central>/<iface>/<addr>/availability` | `<name>/status/<central>/<iface>/<addr>/online` |
| `<base>/<central>/<iface>/<addr>/info` | `<name>/status/<central>/<iface>/<addr>/info` |
| `<base>/<central>/<iface>/<addr>/diagnostics` | `<name>/status/<central>/<iface>/<addr>/diagnostics` |
| `<base>/<central>/<iface>/<addr>/update` | `<name>/status/<central>/<iface>/<addr>/update` |
| `<base>/<central>/<iface>/<addr>/<ch>/values/<param>/config` (also `master`, `calculated`) | `<name>/meta/<central>/<iface>/<addr>/<ch>/values/<param>` |
| `<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>/config` | `<name>/meta/<central>/<iface>/<addr>/<ch>/custom/<kind>` |
| `<base>/<central>/<iface>/<addr>/<ch>/event/<type>` (legacy pulse) | dropped |
| `<base>/<central>/<iface>/<addr>/<ch>/combined/<kind>` | `<name>/status/<central>/<iface>/<addr>/<ch>/combined/<kind>` |
| `<base>/<central>/<iface>/<addr>/<ch>/week_profile/state` | `<name>/status/<central>/<iface>/<addr>/<ch>/week_profile` |
| `<base>/<central>/<iface>/<addr>/<ch>/schedule/state` | `<name>/status/<central>/<iface>/<addr>/<ch>/schedule/active_entries` |
| `<base>/<central>/<iface>/<addr>/<ch>/schedule/attrs` | `<name>/status/<central>/<iface>/<addr>/<ch>/schedule/attributes` |
| `<base>/<central>/<iface>/<addr>/<ch>/schedule/<key>/state` | `<name>/status/<central>/<iface>/<addr>/<ch>/schedule/switch/<key>` |
| `<base>/alarm/<zone>/state` | `<name>/status/alarm/<zone>/panel` |
| `<base>/alarm/<zone>/availability` | `<name>/status/alarm/<zone>/online` |
| `<base>/alarm/<zone>/event` (not retained) | `<name>/status/alarm/<zone>/event` |
| `<base>/alarm/<zone>/triggered-motion` | `<name>/status/alarm/<zone>/triggered_motion` |
| `<base>/<central>/<iface>/<addr>/<ch>/values/<param>/set` (also `master`) | `<name>/set/<central>/<iface>/<addr>/<ch>/values/<param>` |
| `<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>/set/<method>` | `<name>/set/<central>/<iface>/<addr>/<ch>/custom/<kind>/<method>` |
| `<base>/<central>/<iface>/<addr>/<ch>/combined/<kind>/set` | `<name>/set/<central>/<iface>/<addr>/<ch>/combined/<kind>` |
| `<base>/<central>/<iface>/<addr>/<ch>/week_profile/set` | `<name>/set/<central>/<iface>/<addr>/<ch>/week_profile` |
| `<base>/<central>/<iface>/<addr>/<ch>/schedule/<key>/set` | `<name>/set/<central>/<iface>/<addr>/<ch>/schedule/switch/<key>` |
| `<base>/<central>/devices/<addr>/cdps/<cdp>/<op>/invoke` | `<name>/set/<central>/devices/<addr>/cdps/<cdp>/<op>` |
| `<base>/<central>/hub/install_mode/<iface>/set` | `<name>/set/<central>/hub/install_mode/<iface>` |
| `<base>/system/addon_update/set` | `<name>/set/system/addon_update` |
| `<base>/alarm/<zone>/set` | `<name>/set/alarm/<zone>/panel` |
| `homeassistant/<component>/<node_id>/<object_id>/config` | `homeassistant/<component>/<node_id>/<object_id>/config` (unchanged) |
| `<base>/bridge/status` (`online`/`offline`) | `<name>/connected` (`0`/`1`/`2`) |
| `<base>/bridge/health` | `<name>/info` |
| `<base>/<central>/hub/status` | `<name>/status/<central>/online` |
| `<base>/<central>/hub/sysvars/<sysvar>/state` | `<name>/status/<central>/hub/sysvars/<sysvar>` |
| `<base>/<central>/hub/sysvars/<sysvar>/set` | `<name>/set/<central>/hub/sysvars/<sysvar>` |
| `<base>/<central>/hub/programs/<id>/state` | `<name>/status/<central>/hub/programs/<id>/active` |
| `<base>/<central>/hub/programs/<id>/set` | `<name>/set/<central>/hub/programs/<id>/active` |
| `<base>/<central>/hub/programs/<id>/trigger` | `<name>/set/<central>/hub/programs/<id>/trigger` |
| `<base>/<central>/hub/programs/<id>/execute_available` | `<name>/status/<central>/hub/programs/<id>/execute_available` |
| `<base>/<central>/hub/connectivity/<iface>` | `<name>/status/<central>/hub/connectivity/<iface>` |
| `<base>/<central>/hub/install_mode/<iface>` | `<name>/status/<central>/hub/install_mode/<iface>` |
| `<base>/<central>/hub/update` | `<name>/status/<central>/hub/update` |
| `<base>/<central>/hub/alarm_messages` | `<name>/status/<central>/hub/alarm_messages` |
| `<base>/<central>/hub/service_messages` | `<name>/status/<central>/hub/service_messages` |
| `<base>/<central>/hub/inbox` | `<name>/status/<central>/hub/inbox` |
| `<base>/<central>/system/health_score` | `<name>/status/<central>/system/health_score` |
| `<base>/<central>/system/latency` | `<name>/status/<central>/system/latency` |
| `<base>/<central>/system/last_event_age` | `<name>/status/<central>/system/last_event_age` |
| `<base>/<central>/system/status` (event) | `<name>/status/<central>/system/status` |
| `<base>/system/addon_update/state` | `<name>/status/system/addon_update` |
| `<base>/security/state` | `<name>/status/security/severity` |
| `<base>/security/alarm` | `<name>/status/security/alarm` |
| `<base>/security/problem` | `<name>/status/security/problem` |
| `<base>/security/health` | `<name>/status/security/health` |
| `<base>/security/last_alarm` | `<name>/status/security/last_alarm` |
| `<base>/security/last_fault` | `<name>/status/security/last_fault` |
| `<base>/security/class/<class>` | `<name>/status/security/class/<class>` |
| `<base>/security/zone/<slug>` | `<name>/status/security/zone/<slug>` |
| `<base>/security/event` (not retained) | `<name>/status/security/event` |
| `<base>/security/fault` (not retained) | `<name>/status/security/fault` |
| `<base>/security/availability` | `<name>/status/security/online` |
| reserved `<base>/<central>/hub/info` | reserved `<name>/status/<central>/hub/info` |
| reserved `<base>/<central>/hub/diagnostics` | reserved `<name>/status/<central>/hub/diagnostics` |
| unwired `<base>/<central>/<iface>/<addr>/update/set` | unwired `<name>/set/<central>/<iface>/<addr>/update` |

Payloads changed with the topics: `{"value", "available", "modified_at",
"refreshed_at"}` became `{"val", "ts", "lc", "hm": {"available", …}}` with
integer-millisecond timestamps (`refreshed_at` → `ts`, `modified_at` →
`lc`); `online`/`offline` markers and `ON`/`OFF` became booleans; the
security documents' `state` field moved to `val`; the channel events'
`event_type` moved to `val`.

For example, the actual temperature of the thermostat in
[Concrete examples](#concrete-examples):

```
before: openccu-loom/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/1/values/ACTUAL_TEMPERATURE
        {"value":21.6,"available":true,"modified_at":1730385720.123,"refreshed_at":1730385720.123}
after:  openccu-loom/status/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/1/values/ACTUAL_TEMPERATURE
        {"val":21.6,"ts":1730385720123,"lc":1730385720123,"hm":{"available":true}}
```

**The retained sweep.** On every start the daemon clears what the old layout
left retained on the broker:

- It subscribes the **old trees only**, for a short window: one filter per
  configured central (`<name>/<central>/#`) plus `<name>/bridge/#`,
  `<name>/alarm/#`, `<name>/security/#` and `<name>/system/#` — never
  `<name>/#`, which would overlap the daemon's own `set` routes.
- A topic whose first level below the base is a function name (`status`,
  `set`, `meta`, `ha`, …) is new and never touched.
- Every other topic is cleared only if it matches an **exact old shape** for
  an identifier this daemon owns — a configured central, or its own
  `bridge`, `alarm`, `security` and `system/addon_update` trees. Never a
  prefix match: a sibling daemon on the same broker keeps every topic.
- The sweep is idempotent: a second run finds nothing to clear, and a
  rollback followed by a re-upgrade is cleaned again. It stays for the life
  of this major.

**Reserved central names.** A central whose topic-safe name is `alarm`,
`security`, `system`, `bridge`, `connected`, `status`, `set`, `get`, `info`,
`meta` or `maintenance` is now refused at config validation. Unlikely, but a
**hard failure on upgrade**: rename the central before upgrading.
