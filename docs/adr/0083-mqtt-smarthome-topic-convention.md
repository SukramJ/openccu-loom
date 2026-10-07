# ADR 0083 — One topic convention for all six projects: mqtt-smarthome 2.0

- **Status**: accepted (2026-10-06)
- **Supersedes (in part)**: [ADR 0011](./0011-mqtt-topic-and-payload-architecture.md)
  §Topic hierarchy, §Payload schemas and the retain rule for `bridge/status`;
  [ADR 0070](./0070-shared-ha-discovery-model-module.md) — the *topic-schema*
  half of the "A clean break" decision bullet, and the sentence of the
  2026-09-12 amendment that `topic.Layout` exists "precisely so each consumer
  keeps its own schema"; the topic shapes (not the mechanism) of
  [ADR 0052](./0052-daemon-level-alarm-mqtt-topics.md),
  [ADR 0059](./0059-security-safety-mqtt-plane.md) and
  [ADR 0009](./0009-service-method-command-topics.md)
- **Leaves in force**: [ADR 0068](./0068-unique-id-stability-per-plane.md) entirely;
  ADR 0070's amendment "this daemon keeps its `unique_id`" and its amendment
  "the hub model keeps its topics" (finished strings via `MQTTAddressable`);
  ADR 0052's and 0059's reasons for daemon-level trees without `<central>`;
  ADR 0009's scalar-to-argument mapping for service methods
- **Related**: [ADR 0081](./0081-deployment-self-description.md) (the supervisor
  predicate), [MQTT topic schema](../mqtt-topic-schema.md)

## Context

Six projects publish their own MQTT trees: this daemon and the five bridges
`go-mtec2mqtt`, `go-zendure2mqtt`, `go-homeconnect2mqtt`, `go-daikin2mqtt` and
`go-unifi2mqtt`. ADR 0070 extracted the shared Home Assistant discovery model
into `go-hamqtt` and originally called for harmonised topic schemas. That
half was never executed: the closing section of ADR 0070 lists nine phases,
none of which moved a state topic, and the 2026-09-12 amendment made the
opposite explicit. Each project therefore still has its own grammar.
All facts below were read from `origin/main` of each repository on 2026-10-06;
file paths are relative to the repository named in the row.

| Project | Status topic | Payload | Bools | Bridge status |
|---|---|---|---|---|
| openccu-loom | `<base>/<central>/<iface>/<addr>/<ch>/values/<param>` (`internal/north/mqtt/topics.go`, `internal/model/naming/pathdata.go`) | JSON `{value, available, modified_at, refreshed_at}`, float seconds (`internal/payload/wrapper.go`) | JSON | `<base>/bridge/status` + `bridge/health` (`topics.go:48-57`) |
| mtec | `<root>/<serial>/<group>/<key>/state` (`internal/hass/hamqtt.go`) | plain, floats `%.3f` (`MQTT_FLOAT_FORMAT`), enums as localised labels (`internal/coordinator/process.go:68,245`) | `1`/`0` | `<root>/bridge/status` (`internal/hass/discovery.go:61`) |
| zendure | `<root>/<sn>/<group>/<key>/state` (`internal/process/topic.go`) | plain, enums as localised labels (`internal/process/process.go:153`) | `1`/`0` | `<root>/bridge/status` (`internal/harender/harender.go`) |
| homeconnect | `<root>/<device name>/<BSH feature path>/state` (`internal/layout/layout.go`) | plain, enums as localised labels (`internal/bridge/publish.go`, `i18n.EnumLabel`) | `true`/`false` | `<root>/status` |
| daikin | `<root>/<uuid>/<embeddedId>/<key>/state` (`internal/layout/layout.go`) | plain, enums as localised labels (`internal/coordinator/coordinator.go`, `Entry.LocalizedLabel`) | `true`/`false`, power `on`/`off` | `<root>/bridge/status` |
| unifi | `<root>/<site>/device/<mac>/<key>` (no suffix; `internal/coordinator/topics.go`) | plain, English tokens | `ON`/`OFF` | `<root>/bridge/status` + `bridge/info` + `bridge/error` |

Defaults of the root today: loom `openccu-loom`; mtec none (`MQTT_TOPIC` is
required, `internal/config/validate.go:96`; examples use `MTEC`); zendure
`zendure2mqtt` (`internal/config/config.go:23`); homeconnect `homeconnect`
(`internal/config/defaults.go:10`); daikin `daikin` (`internal/config/config.go:80`);
unifi `unifi` (`internal/config/defaults.go:22`). Command subscriptions run at QoS 1 in loom,
mtec, homeconnect and unifi, and at QoS 0 in zendure (a raw
`Subscribe`, no `CommandRouter`) and daikin (`internal/coordinator/plane.go:69`).

A consumer that wants "every adapter on the broker" has to learn six
grammars, five payload styles and four boolean spellings. The mqtt-smarthome
specification 2.0 (2026-09-16) is a published convention that answers exactly
that question, and it is also what hobbyquaker's Smart Home Engine ("she")
reads. The decision to adopt it, for all six projects in one wave, is taken;
this ADR records how.

## Decision

### Conformance

- Sections **3, 4, 5 and 6** of the spec are implemented in full — the spec's
  own conformance bar (§2).
- **Section 7** (maintenance topics) is implemented in all six, built once in
  `go-hamqtt`.
- **Section 8** is adopted for how entities reference topics and availability;
  the discovery topic form and identities stay as they are (below).
- **Sections 9–11** (CLI/env, device discovery, logging) are not adopted.
  These are Go daemons with config files, shipped as containers and add-ons.

### Grammar

```
<name>/<function>/<item...>     function ∈ connected | status | set | info | meta | maintenance | ha
```

The function moves from the topic suffix (`…/state`, `…/set`) to the second
level. `get` stays reserved and unimplemented. `meta` is a function of this
project family (§3 allows it): the retained descriptor companion, today
loom's `…/config`, payload unchanged. Item paths are identical under
`status`, `set` and `meta`. `ha` is the family's second one, added by
amendment item 7: a Home Assistant-native document that Home Assistant
parses without a template, and nothing else — its single tenant is loom's
JSON-schema light.

`<name>` is the existing config key (`MQTT_TOPIC` in the bridges,
`north.mqtt.topic_base` in loom), with new defaults `mtec`, `zendure`,
`homeconnect`, `daikin`, `unifi`; loom stays `openccu-loom`. An explicitly
configured value is kept verbatim. Two details follow from the code:

- loom accepts a multi-level base (`home/loom`, `topics.go:316-321`) and unifi
  sanitises its root to one segment (`sanitiseSegment`). Both keep their
  behaviour; a loom base containing `/` runs outside §3 (`<name>` MUST NOT
  contain `/`), is invisible to a `+/info` scan, and the daemon logs that once
  at start. `+` and `#` stay refused (`command_subscriber.go:778`).
- mtec gains a default where it had none, so existing installations — all of
  which configured a value — keep their root.

**The name is the only thing that keeps two instances apart**, and nothing
checks it. Two cases, both the operator's to resolve by configuring a
different name; each bridge's README says so next to the option:

- Two instances of the same project on one broker (two inverters, two UniFi
  consoles) with the same `<name>` write the same topics and overwrite each
  other's `connected` and `info`. A daemon cannot tell a second instance's
  retained `connected` from the one its own previous run left, so it does not
  try to detect this.
- The defaults `unifi` and `homeconnect` are also the default instance names
  of hobbyquaker's Node.js adapters `unifi2mqtt` and `homeconnect2mqtt`
  (`--name` default in unifi2mqtt's README; `defaults: {name: 'homeconnect'}`
  in homeconnect2mqtt's `config.js`). Running one of those beside the Go
  bridge of the same name on one broker needs one of the two renamed. The
  defaults deliberately carry no `go-` prefix (decided 2026-10-06): the short
  word is what the spec asks for, and the overlap needs both adapters for the
  same hardware on the same broker.

### Naming

- Segments and keys a project **coins** are `snake_case`: mtec `now-base` →
  `now_base` (and `now-grid`, `now-inverter`, `now-backup`, `now-battery`,
  `now-pv`, `internal/registers/register.go:39-48`).
- Identifiers **owned by the upstream protocol** stay verbatim, made topic-safe
  with `topic.Safe` (replaces `/ + #`, space, tab, CR/LF, NUL with `_`):
  Homematic parameter names (`ACTUAL_TEMPERATURE`), addresses, wire interface
  ids, Home Connect feature keys (`BSH/Common/Setting/PowerState`), ONECTA
  embedded ids (`climateControl`), serial numbers, MACs. This is a deliberate
  reading of §3's SHOULD: a renamed upstream identifier is a second vocabulary
  a consumer must map back.
- The device segment is a stable hardware identifier. homeconnect today puts
  the operator's device name into the topic unsanitised — spaces, `/` and
  umlauts pass `validateDeviceName` (`internal/profile/devices.go`). It moves
  to the appliance's `haId`, which the profile archive carries
  (`internal/profile/archive.go:58`, validated by `ValidHaID`) but which is not
  yet in `DeviceConfig`; plumbing it there is part of homeconnect's work.

### `connected`, per-device `online`, `info`

- `<name>/connected`: plain `0`/`1`/`2`, retained. `0` by LWT and on graceful
  stop; `1` broker up, upstream unusable (inverter, cloud, controller; for loom:
  no central reachable); `2` operational. Replaces `bridge/status` (homeconnect:
  `<root>/status`) with `online`/`offline`.
- Per-device reachability is `<name>/status/<device>/online` (`true`/`false`).
- `<name>/info`: retained JSON on every broker connect — `name` (the Go project
  name: `go-mtec2mqtt`, `go-zendure2mqtt`, `go-homeconnect2mqtt`,
  `go-daikin2mqtt`, `go-unifi2mqtt`, `openccu-loom`), `version`, `spec: "2.0"`,
  `go` (runtime version, §6's "own key"), `host`, `pid`, `started` (ISO 8601),
  `maintenance` (the actual setting), plus project fields. It must **not** be
  `unifi2mqtt` or `homeconnect2mqtt`: those are hobbyquaker's npm packages, and
  she would offer their npm versions as an update for our daemons. loom's
  `bridge/health` (`version`, `commit`, `build_date`, `started_at`, `centrals`,
  `cmd/openccu-loom/daemon_helpers.go:70`) and unifi's `bridge/info` fold into
  it; `started_at` becomes `started`, the redundant `status` field goes.

### Status payload

Every status item of every instance uses the object form; no plain opt-out.

```json
{"val": 21.6, "ts": 1730385720123, "lc": 1730385720123, "hm": {"available": true}}
```

- `val` is a JSON boolean, number, string, or the structured value. mtec's
  formatted float strings go; rounding precision stays a project concern.
  Units never appear in payloads, only in discovery.
- `ts` is the time of the observation that caused the publish, `lc` the time
  `val` last changed; integer milliseconds.
- Project fields go under one key. loom uses `"hm"`, holding `available` and
  `additional_information`; `refreshed_at` → `ts`, `modified_at` → `lc`
  (float seconds → integer ms). A loom document whose primary field is a
  `state` (the security plane) puts that field in `val`, the rest under `hm`;
  a document without a primary value (device `info`, `diagnostics`, `update`,
  message aggregates, `last_alarm`) is `val` whole. `ON`/`OFF` payloads become
  `true`/`false`.
- **Enums** carry the stable token — the raw code or English token — following
  loom's rule that tokens are authoritative and labels live in discovery
  (`options` + `value_template`/`command_template`, `docs/mqtt-topic-schema.md`
  §`/config` companion). mtec, zendure, homeconnect and daikin stop publishing
  localised labels as state. daikin's power `on`/`off` becomes a boolean.
- **Publish rule**: on value change and on every broker (re)connect, never on
  every poll. `go-hamqtt`'s dedup compares `val` only (today it compares whole
  payload bytes, `publisher/state.go:379-435`), and the reconnect replay
  re-sends the cached object with its original `ts`. unifi's forced periodic
  republish (`internal/coordinator/publish.go:70-80`) is removed.
- **Events** — loom's channel `event`/`impulse`/`device_error`, alarm and
  security events, `system/status`, unifi's `bridge/error`, `go-hamqtt`'s
  pulse topics — are status items, **not retained**, QoS 0, same object form
  with the event type in `val` (loom's other fields under `hm`). loom's legacy
  `…/event/<type>` pulse is dropped.
- Known limitation: after a process restart `lc` is the first observation
  unless the adapter reads its own retained value back first (optional, later).

### `set`

- Same item path as status. A plain value or `{"val": …}` is accepted; other
  JSON objects are structured parameters (ADR 0009's service-method bodies
  unchanged). The §5.3 conversions apply: `true/false, 1/0, on/off, yes/no`
  case-insensitively; numbers rounded and clamped; enums by token,
  case-insensitively — a project MAY keep accepting labels.
- Parameter and method levels below the item are allowed. loom's
  `custom/<kind>/set/<method>` becomes `set/…/custom/<kind>/<method>`. Program
  `trigger`, unifi `cmd/restart`, `cmd/power_cycle`, `cmd/authorize`, daikin
  `refresh` are action items with no status; any non-empty payload fires them.
- Retained `set` messages and empty payloads are ignored in all six. This
  changes unifi's `cmd/authorize`, which accepts an empty payload today: it now
  needs `{}` or `{"minutes": n}`.
- No echo of the request as status. A rejected or failed `set` is logged at
  `warn` with topic and payload (§3.3); loom redacts the alarm `code` field
  before logging.
- QoS: status at QoS 0; `set` subscribed at **QoS 1** in all six (zendure and
  daikin move up). A subscription QoS is an upper bound — a consumer that
  follows §4 and publishes at QoS 0 still gets QoS 0. The spec discourages
  QoS 1 because a duplicate `set` repeats a hardware action; we keep it because
  a lost write on a flaky link is the failure operators actually report, most
  `set` items here are levels (a duplicate is harmless), and for the rest —
  action items, service methods such as `open` — the consumer controls the
  duplicate risk by publishing at QoS 0.

### Maintenance (§7)

Built once in `go-hamqtt`, on by default, disabled by one key per project
(`MQTT_MAINTENANCE=false`; loom `north.mqtt.maintenance.enabled: false`):

| Topic | Effect |
|---|---|
| `<name>/maintenance/set/loglevel` | `error`/`warn`/`info`/`debug` onto slog levels, not persisted; loom sets the root level of its `hmlog.LevelRegistry` (`cmd/openccu-loom/daemon_logging.go:84`), per-path overrides untouched |
| `<name>/maintenance/set/restart` | graceful shutdown, `connected` → `0`, exit 0 — only where a supervisor restarts the process; otherwise refused and logged at `warn` |
| `<name>/maintenance/stats` | retained, every `MQTT_STATS_INTERVAL` (default 60 s, `0` = off): `rss` (Linux `/proc`; omitted elsewhere), `heapUsed`, `heapTotal` (`runtime/metrics`), `cpu` (share of one core), `uptime`, `ts`; no `eventLoopLag` |

**Restart gating in loom.** The REST restart (`POST /system/restart`) is
mounted only when `detectSupervisedRestart()` is true
(`cmd/openccu-loom/daemon_helpers.go:398`, `daemon_rest_mount.go:443,563`):
`OPENCCU_LOOM_SUPERVISOR=1`, systemd as parent, Kubernetes, or `/.dockerenv`.
The MQTT restart uses that same predicate, the same once-per-30-s latch and
SIGTERM path (`internal/north/rest/handlers/system_admin.go:138-215`), and an
audit entry. It does **not** use `deployment.Kind`
(`internal/deployment/deployment.go`): ADR 0081 says the kind names where the
daemon runs, and that `OPENCCU_LOOM_SUPERVISOR` alone means "something
restarts the process" — a `standalone` daemon may or may not be supervised.
The bridges have no such predicate today; `go-hamqtt` takes a `Supervised`
answer from the consumer, and a bridge that cannot answer refuses.

**Security** (§7): anyone who may publish on the broker can restart an
instance or raise its log level. On REST the restart is admin-gated; on MQTT
the only gate is the broker's ACLs (an adapter needs `<name>/#` and the
discovery prefix). This is the same posture ADR 0052 recorded for
`alarm/<zone>/set`; operators on an unsecured broker disable maintenance.

### Home Assistant discovery

- `state_topic` → `<name>/status/<item>`, `command_topic` → `<name>/set/<item>`,
  `value_template` reads `value_json.val`; platforms with on/off payloads use
  `{{ value_json.val | lower }}` with `true`/`false` payloads, because Jinja
  renders a JSON boolean as `True`.
- Availability: `<name>/connected` (available at ≥ 2, via
  `{{ 'online' if value | int(0) >= 2 else 'offline' }}`) plus the device's
  `…/online` item, `availability_mode: all`. Each entry carries only `topic`,
  `payload_available`, `payload_not_available`, `value_template`. loom's
  "Daemon connection" sensor reads `connected ≥ 1` and keeps no availability.
- **`unique_id`, node ids, device identifiers and the discovery topic form are
  not changed.** ADR 0068 stays in force; Home Assistant re-points existing
  entities to the new topics because its registry keys on `unique_id`. Known
  gap against §8: unifi publishes per-entity configs (step 6 declined, ADR 0070
  closing) and loom keeps bundles off by default; both stay so.
- zendure needs one guard: its `unique_id` and device identifier embed the
  topic root (`hass.UniqueID(r.Root, …)`, `unitID := r.Root + "_" + dev.SN`,
  `internal/harender/harender.go:167,309`). Its identity root is pinned to
  the old effective value — the configured `MQTT_TOPIC`, or `zendure2mqtt`
  when unset — so the new default moves topics, not identities. mtec's
  `unique_id` uses the constant prefix `MTEC_` and the register key
  (`internal/hass/discovery.go:31`) and is unaffected by `now_base`.

### Where it is built

| Piece | Exists today (`go-hamqtt` v0.35.0) | Missing |
|---|---|---|
| `topic.Layout` for this grammar | interface, consumer-implementable (`topic/topic.go:28-51`); only `Default` (`<root>/…/<bucket>/<path>`) | a `topic.SmartHome` layout; `Availability` returns `…/online`, `Bridge` returns `<name>/connected` |
| `{val, ts, lc}` encoding | closed enum `EnvelopeEncoding`/`RawEncoding` (`discovery/render.go:96-117`), `Envelope{value, available}` (`publisher/state.go:45-56`) | a third encoding + its `value_template`; `val`-only dedup; `lc` tracking |
| `connected` availability | `PayloadOnline`/`PayloadOffline` constants used by `Runtime.Will`, `AnnounceOnline`, `AvailabilityPublisher` (`render.go:129-132`, `birth.go:69-81`) | `0/1/2` will and transitions, `value_template` on bridge entries |
| `info` publisher | none | new |
| `set` normaliser | `CommandRouter` hands raw bytes, drops retained (`command.go:1140`), does not filter empty | unwrap `{"val"}`, §5.3 conversions, empty-payload drop |
| maintenance | none | new (§7 table above) |
| pulses | `PulseLayout` (`topic/pulse.go`), QoS 0, not retained | layout answers with status items |

All of it is additive (a minor release). loom rewrites `TopicBuilder`
(`internal/north/mqtt/topics.go`) and the `naming.MQTT*` shapes
(`internal/model/naming/pathdata.go`), keeps `MQTTAddressable`, and rewrites
`docs/mqtt-topic-schema.md` with the implementation. `go-mqtt` needs nothing.

### Reserved first-level items

Literal first-level items share a level with a configured name: loom's
`alarm`, `security`, `system` and the new `bridge` sit beside `<central>`;
daikin's `scheduler` beside device UUIDs; unifi's `bridge` beside `<site>`.
**Today nothing prevents the collision**: `validateCentralNames`
(`internal/config/config.go:2508`) checks distinctness after `TopicSafe` and
`hmtypes.ValidateCentralName` checks the character set, but a central named
`alarm` is accepted and shares `<base>/alarm/…` with the alarm plane. Below
`<central>`, `hub`, `system` and `devices` cannot collide because wire
interface ids are `<central>-<interface>`. This ADR adds the guard: loom
refuses a central, and unifi a `SITE`, whose topic-safe form is `alarm`,
`security`, `system` or `bridge`, or any function name (`connected`,
`status`, `set`, `get`, `info`, `meta`, `maintenance`, and loom's `ha` —
amendment item 7) — the latter so the migration sweep below can never
mistake an old topic for a new one.

## Target, by example

| Project | Before | After |
|---|---|---|
| loom | `openccu-loom/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/1/values/ACTUAL_TEMPERATURE` | `openccu-loom/status/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/1/values/ACTUAL_TEMPERATURE` |
| mtec | `MTEC/MT1234567890/now-base/grid_power/state` | `MTEC/status/MT1234567890/now_base/grid_power` |
| zendure | `zendure2mqtt/SF2400AC0012345/now/electric_level/state` | `zendure/status/SF2400AC0012345/now/electric_level` |
| homeconnect | `homeconnect/Geschirrspüler/BSH/Common/Setting/PowerState/state` | `homeconnect/status/<haId>/BSH/Common/Setting/PowerState` |
| daikin | `daikin/<uuid>/climateControl/operation_mode/state` | `daikin/status/<uuid>/climateControl/operation_mode` |
| unifi | `unifi/default/device/aabbccddeeff/cpu_utilization` | `unifi/status/default/device/aabbccddeeff/cpu_utilization` |

### loom — every row of `docs/mqtt-topic-schema.md`

`<n>` = `<name>`; `…` = `<central>/<iface>/<addr>`; `F` = function.
Unless marked, status is retained and set is subscribed at QoS 1.

| Today | After |
|---|---|
| `<base>/<central>/<iface>/<addr>/<ch>/values/<param>` (also `master`, `calculated`) | `<n>/status/…/<ch>/values/<param>` |
| `…/<ch>/custom/<kind>` | `<n>/status/…/<ch>/custom/<kind>` (the light also `<n>/ha/…/<ch>/custom/light`, amendment item 7) |
| `…/<ch>/event`, `…/<ch>/impulse`, `…/<ch>/device_error` (not retained) | `<n>/status/…/<ch>/event` etc. (not retained) |
| `…/<addr>/availability` | `<n>/status/…/online` |
| `…/<addr>/info`, `…/diagnostics`, `…/update` | `<n>/status/…/info`, `…/diagnostics`, `…/update` |
| `…/<ch>/values/<param>/config` (also `master`, `calculated`) | `<n>/meta/…/<ch>/values/<param>` |
| `…/<ch>/custom/<kind>/config` | `<n>/meta/…/<ch>/custom/<kind>` |
| `…/<ch>/event/<type>` (legacy pulse) | dropped |
| `…/<ch>/combined/<kind>` | `<n>/status/…/<ch>/combined/<kind>` |
| `…/<ch>/week_profile/state` | `<n>/status/…/<ch>/week_profile` |
| `…/<ch>/schedule/state` (active-entry count) | `<n>/status/…/<ch>/schedule/active_entries` |
| `…/<ch>/schedule/attrs` | `<n>/status/…/<ch>/schedule/attributes` |
| `…/<ch>/schedule/<key>/state` | `<n>/status/…/<ch>/schedule/switch/<key>` |
| `<base>/alarm/<zone>/state` | `<n>/status/alarm/<zone>/panel` |
| `<base>/alarm/<zone>/availability` | `<n>/status/alarm/<zone>/online` |
| `<base>/alarm/<zone>/event` (not retained) | `<n>/status/alarm/<zone>/event` (not retained) |
| `…/<ch>/values/<param>/set` (also `master`) | `<n>/set/…/<ch>/values/<param>` |
| `…/<ch>/custom/<kind>/set/<method>` | `<n>/set/…/<ch>/custom/<kind>/<method>` |
| `…/<ch>/combined/<kind>/set` | `<n>/set/…/<ch>/combined/<kind>` |
| `…/<ch>/week_profile/set` | `<n>/set/…/<ch>/week_profile` |
| `…/<ch>/schedule/<key>/set` | `<n>/set/…/<ch>/schedule/switch/<key>` |
| `<base>/<central>/devices/<addr>/cdps/<cdp>/<op>/invoke` | `<n>/set/<central>/devices/<addr>/cdps/<cdp>/<op>` (action) |
| `<base>/<central>/hub/install_mode/<iface>/set` | `<n>/set/<central>/hub/install_mode/<iface>` |
| `<base>/system/addon_update/set` | `<n>/set/system/addon_update` |
| `<base>/alarm/<zone>/set` | `<n>/set/alarm/<zone>/panel` |
| `homeassistant/<component>/<node_id>/<object_id>/config` | unchanged |
| `<base>/bridge/status` | `<n>/connected` (`0`/`1`/`2`) |
| `<base>/bridge/health` | `<n>/info` |
| `<base>/<central>/hub/status` | `<n>/status/<central>/online` |
| `<base>/<central>/hub/sysvars/<sv>/state` / `…/set` | `<n>/status/<central>/hub/sysvars/<sv>` / `<n>/set/<central>/hub/sysvars/<sv>` |
| `<base>/<central>/hub/programs/<id>/state` / `…/set` | `<n>/status/<central>/hub/programs/<id>/active` / `<n>/set/…/programs/<id>/active` |
| `<base>/<central>/hub/programs/<id>/trigger` | `<n>/set/<central>/hub/programs/<id>/trigger` (action) |
| `<base>/<central>/hub/programs/<id>/execute_available` | `<n>/status/<central>/hub/programs/<id>/execute_available` |
| `<base>/<central>/hub/connectivity/<iface>` | `<n>/status/<central>/hub/connectivity/<iface>` |
| `<base>/<central>/hub/install_mode/<iface>` | `<n>/status/<central>/hub/install_mode/<iface>` |
| `<base>/<central>/hub/update` | `<n>/status/<central>/hub/update` |
| `<base>/<central>/hub/{alarm_messages,service_messages,inbox}` | `<n>/status/<central>/hub/{alarm_messages,service_messages,inbox}` |
| `<base>/<central>/system/{health_score,latency,last_event_age}` | `<n>/status/<central>/system/{health_score,latency,last_event_age}` |
| `<base>/<central>/system/status` (event) | `<n>/status/<central>/system/status` (not retained) |
| `<base>/system/addon_update/state` | `<n>/status/system/addon_update` |
| `<base>/security/state` | `<n>/status/security/severity` |
| `<base>/security/{alarm,problem,health,last_alarm,last_fault}` | `<n>/status/security/{alarm,problem,health,last_alarm,last_fault}` |
| `<base>/security/class/<class>`, `…/zone/<slug>` | `<n>/status/security/class/<class>`, `…/zone/<slug>` |
| `<base>/security/{event,fault}` (not retained) | `<n>/status/security/{event,fault}` (not retained) |
| `<base>/security/availability` | `<n>/status/security/online` |
| reserved `<base>/<central>/hub/info`, `…/hub/diagnostics` | reserved `<n>/status/<central>/hub/info`, `…/hub/diagnostics` |
| unwired `…/<addr>/update/set` | unwired `<n>/set/…/update` |

Renamed leaves, and why: `panel` (the `alarm_control_panel` item; state
tokens out, `ARM_*` tokens in, one path), `active` (program activation; its
siblings `trigger` and `execute_available` are other items), `active_entries`
and `attributes` (two items that were both named by a `state` suffix),
`switch/<key>` (so no schedule key can shadow `active_entries`), `severity`
(the folded value the security `state` field carried).

### Bridges

| Project | Today | After |
|---|---|---|
| mtec | `<r>/<serial>/<group>/<key>/state` / `…/set`; `<r>/bridge/status` | `<n>/status/<serial>/<group>/<key>` / `<n>/set/…`; `<n>/connected`; new `<n>/status/<serial>/online` |
| zendure | `<r>/<sn>/<group>/<key>/state`, `<r>/<sn>/battery/<pack>/<key>/state`, `…/set`; `<r>/bridge/status` | `<n>/status/<sn>/<group>/<key>`, `<n>/status/<sn>/battery/<pack>/<key>`, `<n>/set/…`; `<n>/connected`; new `<n>/status/<sn>/online` |
| homeconnect | `<r>/<dev>/<feature>/state` / `…/set`; `<r>/<dev>/availability`; `<r>/<dev>/connection_state`; `<r>/<dev>/_control/{start,stop}_program/set`; `<r>/<dev>/_uid/<i>/state`; `<r>/status` | `<n>/status/<haId>/<feature>` / `<n>/set/…`; `<n>/status/<haId>/online`; `<n>/status/<haId>/connection_state`; `<n>/set/<haId>/_control/{start,stop}_program`; `<n>/status/<haId>/_uid/<i>`; `<n>/connected` |
| daikin | `<r>/<uuid>/<emb>/<key>/state` / `…/set`; `…/<emb>/climate/attributes`; `<r>/scheduler/<id>/enabled/{state,set}`; `…/<emb>/refresh/set`; `<r>/bridge/status` | `<n>/status/<uuid>/<emb>/<key>` / `<n>/set/…`; `<n>/status/<uuid>/<emb>/climate/attributes`; `<n>/{status,set}/scheduler/<id>/enabled`; `<n>/set/<uuid>/<emb>/refresh`; `<n>/connected`; new `<n>/status/<uuid>/online` |
| unifi | `<r>/<site>/…/<key>`; `<r>/<site>/device/<mac>/cmd/{restart,locate/set}`; `…/port/<p>/cmd/power_cycle`; `…/client/<mac>/{blocked/set,cmd/authorize}`; `…/wlan/<id>/enabled/set`; `<r>/bridge/{status,info,error}` | `<n>/status/<site>/…/<key>`; `<n>/set/<site>/device/<mac>/cmd/{restart,locate}`; `<n>/set/…/port/<p>/cmd/power_cycle`; `<n>/set/<site>/client/<mac>/{blocked,cmd/authorize}`; `<n>/set/<site>/wlan/<id>/enabled`; `<n>/connected`, `<n>/info`, `<n>/status/bridge/error` (not retained); new `<n>/status/<site>/device/<mac>/online` |

unifi's `state` leaves (`device/<mac>/state`, `client/<mac>/state`,
`health/wan/state`) are item names, not function suffixes — unifi's layout
has none — and stay. daikin's Faikin firmware topics (`state/<host>`,
`command/<host>/<suffix>`, `internal/faikin/faikin.go`) are dictated by the
firmware, live outside `<name>/` and are unaffected.

## Migration

A clean break per project (§12): a major release (for a 0.x project, its next
breaking minor), no compatibility switch, old and new topics side by side in
each README.

**The retained sweep.** On every start of the new major, each adapter clears
what its old layout left, the way orphaned discovery configs are retracted
today (loom: `RunRetainCleanupOnce`, `internal/north/mqtt/retain_cleanup.go:391`,
which already evicts the retired `channels/` shape):

1. Subscribe for a bounded window, retained messages only, to `<old-root>/#`
   — the configured value, or the old default when unset (`zendure2mqtt`; for
   the others the old root equals `<name>`).
2. A topic whose second level is a function name is **new** and never
   touched. The reserved-name guard above makes this test sound for loom
   centrals and unifi sites; serials, UUIDs and MACs cannot spell a function
   name. homeconnect's old device segment is an operator-chosen name with no
   such guard: a device named `status`, `set`, `info` or `meta` leaves its old
   topics retained (stale, never wrongly deleted), and the README says so. One
   further exception: homeconnect's old bare `<root>/status` (exactly two
   levels; the new layout never publishes `<name>/status` itself) is cleared
   by exact match.
3. Every other topic is cleared (empty retained payload) only if it matches
   an old shape for an identifier **this instance owns** — a device id it
   knows, a configured central, the `bridge/*`, `alarm/*`, `security/*` and
   `system/addon_update/*` trees of its own root. Never a prefix match: ADR
   0070's homeconnect review measured a prefix rule deleting 510 live
   components of a sibling instance.
4. The same read-back of `<name>/status/#` serves §3.2's steady-state rule
   (clear items the first complete picture no longer contains).

The sweep stays for the life of the major (idempotent; it also cleans after a
rollback and re-upgrade) and is removed in the next one.

**Order of the wave.** `go-hamqtt` (additive minor) → the five bridges
(each: switch layout, sweep, maintenance, `info`) → loom with its in-repo
consumers. Before the wave ships, one bridge is verified against a running
she instance (inventory row, restart, log level, stats, wipe).

**Who reads loom's raw topics.** Nobody else in this project family, on the
evidence of `origin/main`: `openccu-loom-client` is a REST/WebSocket client
with no MQTT library (`openccu_loom_client/transport/ws.py`; `MQTT_RAW` in
`capabilities.py:57-58` is a capability name only); `node-red-contrib-openccu-loom`
uses `undici` and `ws` (`lib/client.js:3,28`); `homematicip_local`'s MQTT
consumer (`custom_components/homematicip_local/mqtt.py:24-80`) subscribes to
CCU-Jack-style `device/status/…` paths, not loom's. Inside this repository:
`docs/user/multi-ccu.md` (topic and `mosquitto_sub` examples),
`docs/user-guide.md:227`, `script/clean-mqtt-discovery.sh`,
`cmd/openccu-loom/daemon_north.go:551` (LWT), and the e2e suite. Operators'
own Node-RED flows, dashboards and scripts are the real blast radius.

## Compatibility with she

What the decisions are designed to satisfy, from reading she's Services
documentation and its payload parser, services inventory and HA-discovery
analysis — not tested behaviour; the acceptance step above tests it.

| she feature | Needs | Status after this ADR |
|---|---|---|
| Scripts and the bus | `parsePayload`: a JSON object with `val` is taken as-is | compatible |
| Services, MQTT tier: inventory, restart, log level, Mem/CPU, wipe of retained topics | `<name>/connected`, `<name>/info`, `maintenance/set/{loglevel,restart}`, `maintenance/stats` | compatible |
| Discovery analysis | per-entity and device form both read | compatible |
| npm "update available" badge | `info.name` is an npm package | not applicable (Go project names, see `info`) |
| Host and Catalog tiers | npm package on mqtt-interfaces-core, `<adapter>@.service` with `/etc/<adapter>/%i.env`, `--config-schema`, `--install`, `--discover` (§9–10) | not compatible, out of scope |

## Alternatives considered

**Additive only** — publish `<name>/connected`, `<name>/info` and
`<name>/maintenance/…` beside today's topics and leave state topics, command
topics and payloads alone. Checked against she's sources, it would satisfy
she's Services MQTT tier without breaking anything:

- she's scripts subscribe to arbitrary topics and already work against
  today's layouts: its payload parser maps a plain payload to `val`, and reads
  loom's envelope as an object without `val`, i.e. as `val.value`.
- she uses HA discovery only as an analysis and clean-up view (grouping
  devices, orphan detection, wiping retained configs). It does not resolve
  entities or evaluate `value_template`, so discovery cannot stand in for the
  topic convention there.
- The inventory needs only `connected` and `info`; restart, log level and
  stats need `maintenance/`. The wipe clears `<name>/info`, `<name>/connected`,
  `<name>/status/…` and `<name>/maintenance/…`, plus discovery configs whose
  every referenced topic lies under `<name>/`.

**Rejected** (decided 2026-10-06), because the goal is one topic, command and
payload structure across the six projects, and this delivers none of it: six
layouts and four boolean spellings remain, there is no device-side `ts`/`lc`,
and she's wipe would miss every state topic, which lives outside
`<name>/status/`.

## Why

- **One grammar is what a consumer needs, and a published one beats ours.**
  Each of the six could be made consistent with the other five by fiat; only a
  convention someone else documents gives a non-project consumer (she, a
  dashboard, a script) one rule to learn.
- **`{val, ts, lc}` everywhere** because `ts`/`lc` are the one thing a retained
  plain value cannot carry, and loom already pays for both (`modified_at`,
  `refreshed_at`). One form for all instances means consumers never sniff.
- **Tokens, not labels**, because a label changes with `LANGUAGE`; a token is
  what a write has to carry back. loom settled this for itself; the bridges
  inherit it.
- **`unique_id` untouched**, because ADR 0068's measurement is unchanged: Home
  Assistant has no migration for it, and a topic move needs none.

## Consequences

- **Who breaks**: every raw-topic consumer of all six — operators' flows,
  dashboards, Telegraf and `mosquitto_sub` scripts. Home Assistant users do
  not: discovery re-points entities, identities stay. A dashboard reading
  `value_json.value` breaks; one reading the HA entity does not.
- **Lost**: plain payloads for trivially scripted consumers (a shell script
  doing `mosquitto_sub | read v` must now parse JSON); localised enum labels on
  the wire; mtec's fixed-decimal strings; unifi's periodic refresh of every
  retained topic; loom's legacy `event/<type>` pulse; empty-payload
  `cmd/authorize`; a `+/info`-visible loom whose base has a `/`.
- **New config refusals**: loom centrals and unifi sites named like a reserved
  item or function no longer start. Unlikely, but a hard failure on upgrade.
- **New attack surface**: maintenance restart and log level over MQTT, gated
  by broker ACLs only; on by default per §7.
- **Work, by repository**:
  - `go-hamqtt`: the seven pieces in the table above; minor release.
  - mtec: layout, `snake_case` groups, JSON numbers, tokens instead of labels,
    `1/0` → booleans, a default root, device `online`.
  - zendure: same, plus moving to `CommandRouter` (QoS 1, retained drop) and
    pinning the identity root.
  - homeconnect: `haId` into `DeviceConfig` and the topic, tokens instead of
    labels (the reverse mapping in `internal/bridge/command.go` stays for
    `set`), `<root>/status` → `connected`.
  - daikin: tokens, power as boolean, command QoS 1, device `online`.
  - unifi: `ON/OFF` → booleans, `bridge/*` fold, drop periodic republish,
    device `online`, `SITE` guard.
  - loom: `TopicBuilder` and `naming` shapes, payload envelope, every
    discovery template reading `value_json.value` (17 matching non-test lines
    in `internal/`), the HA
    availability lists, the reserved-name guard, the sweep, maintenance with
    the restart gate, and the schema document.
- **Contract tests rewritten in loom**: `tests/contract/mqtt_topic_schema_producer_test.go`,
  `mqtt_topic_schema_doctest_test.go`, `mqtt_topology_test.go`,
  `discovery_roundtrip_test.go`, `lock_target_level_payload_parity_test.go`,
  `security_mqtt_plane_test.go`; the round-trip and one-spelling tests in
  `internal/north/mqtt/` (`hub_topics_roundtrip_test.go`,
  `alarm_topics_roundtrip_test.go`, `security_topics_roundtrip_test.go`,
  `hub_one_topic_spelling_test.go`); seven `tests/e2e/` files; the golden
  `internal/central/adapter/testdata/state_golden_per_dp.json`. Each bridge rewrites its layout and
  publish tests.
- `docs/mqtt-topic-schema.md` and the user docs change with the implementation,
  not with this ADR.

## Amendment (2026-10-06) — findings from the implementation

This ADR was written before the code. The implementation in openccu-loom,
go-hamqtt v0.36.0 and the five bridges found the following; each is a fact
the decision text above does not say, or says differently.

1. **`maintenance/set/restart` fires on an empty payload.** The decision
   says empty `set` payloads are ignored in all six projects. The one
   exception is the restart: spec §7 gives its payload as "any", and she
   publishes it with an empty payload (`she-services-api.js`), so treating
   it like a `set` would make she's Restart button do nothing. go-hamqtt's
   `publisher.Instance` routes it without the `set` normalisation; a
   retained restart is still dropped, and the restart only happens behind
   the supervisor predicate.
2. **The sweep does not subscribe `<old-root>/#` where that overlaps live
   command routes** (Migration, step 1). go-mtec2mqtt found that a filter
   overlapping the daemon's own `set` subscriptions delivers a `set`
   arriving during the window twice on a shared connection. loom's sweep
   subscribes one filter per old tree — `<base>/<central>/#` for each
   configured central and `<base>/{bridge,alarm,security,system}/#` — none
   of which can match a `<base>/set/…` route, and it rides a connection of
   its own besides.
3. **she manages only instance names matching `[A-Za-z0-9_.-]+`, and its
   wipe does not clear `<name>/meta/…` or `<name>/ha/…`.** A loom base
   outside that set (a multi-level `home/loom` included) is not offered by
   she at all; on any base, she's wipe leaves the `meta` companions and the
   light's `ha` twins retained.
4. **The bridges' enum tokens are their English catalog tokens where the
   raw code is numeric** (mtec, zendure): a numeric register code is not a
   token a consumer could read, so the English catalog spelling stands in
   for it — still independent of `LANGUAGE`, which is the property the
   Enums rule exists for.
5. **go-hamqtt gained `publisher.DetectSupervised`**, loom's
   `detectSupervisedRestart` shared for the bridges. The bridges' Home
   Assistant add-ons set their `*_SUPERVISED=0`, so the MQTT restart is
   refused there (a container marker alone is a known false positive).
   loom keeps its own predicate, because the REST restart route is mounted
   behind the same function and the two surfaces must not disagree.
6. **zendure and mtec also keep a multi-level name** with a start-up
   warning, as loom does (Grammar).

Found in loom:

7. **The `status` tree is status objects without exception; the
   JSON-schema light reads a twin under `ha`** (decided 2026-10-06,
   replacing the first implementation, which kept `…/custom/light` in Home
   Assistant's shape). Home Assistant's light platform in `schema: json`
   parses the state topic's document natively — `state`, `brightness`,
   `color`, `color_temp_kelvin`, `effect`, `color_mode` at the top level —
   and takes no value template (`light/schema_json.py`, `_state_received`),
   so it cannot read a status object's `val`; converting the lights to the
   template schema would lose the colour-mode declarations (the HmIP-LSC's
   simultaneous `hs`/`color_temp`) the JSON schema carries. So
   `<name>/status/…/<ch>/custom/light` is a status object whose `val` is
   that document, like every other aggregate, and the same document is
   published bare and retained on `<name>/ha/…/<ch>/custom/light`, which
   the light entity's `state_topic` names; its `command_topic` stays under
   `set`. `ha` becomes a topic function of this project family, for a Home
   Assistant-native document that cannot be templated and nothing else;
   the light is its single tenant. The reason is that a consumer of the
   `status` tree meets one payload form; the cost is that each light is
   published twice. One publish writes both from one document, device
   removal and the orphan sweep evict both, and a reconnect republishes
   both. A central named `ha` is refused like one named after any other
   function, and the migration sweep treats `<name>/ha/…` as new. go-hamqtt
   v0.36.0's `topic.IsFunction` does not know `ha`, so loom checks it
   locally (`naming.IsFunction`); go-hamqtt should learn it.
8. **Two loom shapes were not rows of the table.** The undocumented alarm
   item `<base>/alarm/<zone>/triggered-motion` (the latched-detector count)
   became `<base>/status/alarm/<zone>/triggered_motion`, `snake_case` per
   Naming. The bucket-less command shape `…/<ch>/<PARAM>/set`, kept for
   pre-bucket automations, was dropped: under the grammar it would share a
   length with the week-profile item, and nothing in this repository
   produced it.
9. **The command route set changed shape, not size.** The schedule switch
   (`…/schedule/switch/<key>`, eight levels below the base) has a route of
   its own again, out of reach of the seven-level data-point catch-all; the
   custom-DP operation item (`<central>/devices/<addr>/cdps/<name>/<op>`)
   has the catch-all's length and is dispatched from inside it, like the
   combined item. The set stays pairwise disjoint, so the router never
   switches into attributed mode.
10. **`lc` is tracked per topic by the bridge, not taken from
    `modified_at`.** The domain stamps `modified_at` and `refreshed_at` with
    the same event time on every publish, so `modified_at` does not say
    when the value last changed. `ts` is the event time; `lc` moves only
    when `val` does, which is the spec's definition. The Known limitation
    stands: after a restart `lc` is the first observation.
11. **The `online` items publish at QoS 1**, as loom's availability markers
    always have (finding F5 of the ADR 0070 runtime measurement: a lost
    transition is never repaired by a later publish); every other status
    item stays at the state QoS, 0 by default.
12. **Per-data-point self-availability survives under `hm`.** go-hamqtt's
    standard context drops the envelope-flag availability level under the
    status-object encoding, because the convention has no `available`
    field. loom keeps the flag under `hm` and renders the entry as
    `{{ value_json.hm.available | lower }}`. loom's planes keep their own
    layouts (none is a `SmartHomeLayout`, which would also switch the
    default value template of entities that name their own) and pass the
    resolved availability list through one rewrite into the `connected ≥ 2`
    / boolean-`online` vocabulary.
13. **Event and update entities need a template that rebuilds JSON.** Home
    Assistant's event platform and update platform both parse the
    post-template payload as JSON; the event entities render
    `{{ dict(value_json.hm or {}, event_type=value_json.val) | tojson }}`, the
    update entities `{{ value_json.val | tojson }}`. `system/status` puts
    the component that changed in `val`; a Security & Safety event its verb.
14. **`<base>/info` carries a live field.** go-hamqtt's
    `InstanceConfig.Extra` is fixed at construction, while loom's
    `centrals` must name a CCU adopted at runtime; the field is a value that
    resolves when the document is rendered.
15. **The sweep's notion of an owned device is the configured central.** A
    device the daemon knows may not be loaded when the sweep runs at boot
    (a CCU still starting), so the exact old shapes are matched below every
    configured central rather than per known device address; a sibling
    daemon's centrals are never subscribed.
16. **The WebSocket daemon status keeps `online`/`offline`.** Its doc
    claimed the MQTT plane retains the same words on `bridge/status`; it now
    names the "Daemon connection" entity, whose template renders the
    `connected` level into those words. No REST or WebSocket behaviour
    changed.
17. **Self-reporting entities are the exception to `connected ≥ 2`.**
    Level 2 means a central is reachable, so an entity whose state is that
    reachability, or this daemon itself, is gated at level 1 — otherwise it
    is unavailable exactly when an operator needs to read it. Of the
    entities whose only availability was `bridge/status` before this ADR,
    two are of that kind and take `≥ 1`: the per-interface connectivity
    sensors and the add-on update entity (the daemon's own release, which
    must stay installable through a CCU outage). The "Daemon connection"
    sensor reads `connected` itself and has no availability. Every other
    entity keeps `≥ 2`.
18. **`dict()` raises on a missing `hm`.** jinja2 3.1.6, which Home
    Assistant renders with, raises for `dict()` of an undefined or `null`
    argument, so the event template reads `value_json.hm or {}`, and a
    status object without project fields omits `hm` rather than writing
    `null`.
19. **`text` entities wrap their value.** Under the plain `set` form an
    empty string is an ignored empty payload and a string opening with `{`
    or `[` is malformed JSON; before this ADR both reached the CCU (the
    empty one as a null value). The `text` entities — a writable string
    parameter, an editable string system variable — therefore send
    `{{ {"val": value} | tojson }}`. The display's `notify` entity already
    sent a JSON object and is unaffected.

20. **An unobserved data point is `{"val": null, …, "hm": {"available":
    false}}`.** Before this ADR it was `{"value": null, "available":
    false}`; the fields moved into the status object, and the decision
    stands for the same reason: the per-parameter availability entry reads
    `hm.available` from the state topic, so publishing nothing would leave
    the entity unavailable with no body to explain it. `val` stays present
    as `null` rather than being omitted. Every per-parameter value template
    guards on `value_json.val is not none` and renders an empty string, which
    Home Assistant 2026.10 ignores instead of storing (`number.py:177`,
    `select.py:119`, `binary_sensor.py:186`, `lock.py:192`, `sensor.py:322`
    for numeric sensors, and the empty-payload guards in `cover.py`,
    `valve.py` and `climate.py`); no `| lower` template sees the `null`,
    which would otherwise render `none`.

## Revisit when

- Command acknowledgement or error reporting over MQTT is wanted — the spec
  has none, and all six only log.
- A consumer needs plain payloads (the §9 `--json-payloads` opt-out).
- `get` (§3.4) gets an implementation upstream or a consumer that needs it.
- Device-based discovery for unifi (needs a per-console identity, ADR 0070
  closing) and loom (bundles on by default).
- `lc` across restarts matters: read back the retained value before the first
  publish.
- she's Host and Catalog tiers (§9–10: `--config-schema`, `--install`,
  env-file layout, npm distribution) — not a fit for Go binaries shipped as
  containers and add-ons with config files.

## References

- mqtt-smarthome specification 2.0, <https://github.com/mqtt-smarthome/mqtt-smarthome/blob/master/SPEC.md>
- Smart Home Engine, <https://github.com/hobbyquaker/she>
- [ADR 0011](./0011-mqtt-topic-and-payload-architecture.md),
  [ADR 0068](./0068-unique-id-stability-per-plane.md),
  [ADR 0070](./0070-shared-ha-discovery-model-module.md),
  [ADR 0081](./0081-deployment-self-description.md)
- `go-hamqtt` v0.35.0: `topic/topic.go`, `topic/pulse.go`, `discovery/render.go`,
  `publisher/state.go`, `publisher/command.go`, `publisher/birth.go`
- loom: `internal/north/mqtt/{topics,bridge,command_subscriber,retain_cleanup}.go`,
  `internal/model/naming/pathdata.go`, `internal/payload/wrapper.go`,
  `internal/config/config.go`, `cmd/openccu-loom/daemon_helpers.go`
