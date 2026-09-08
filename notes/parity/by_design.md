# by_design.md — Intentional Architecture Divergences Go ↔ Reference

**As of:** 2026-05-10
**Purpose:** Catalogue of all intentional structural divergences between OpenCCU-Loom (Go) and its two reference implementations:

- **CCU side** — [aiohomematic](https://github.com/SukramJ/aiohomematic) (Python). Sections §1+ below carry the original aiohomematic-vs-Go content.
- **Matter side** — [matter.js](https://github.com/matter-js/matter.js) HEAD. ([home-assistant-matter-bridge](https://github.com/Nabu-Casa/home-assistant-matter-bridge) is a supplementary read for bridge composition patterns but **not** a gold standard — it carries HA-specific shims that do not translate to OpenCCU-Loom.) See section §"Matter / matter.js Divergences" near the end.

The two reference layers do not overlap: CCU wire knowledge stays in aiohomematic, Matter wire knowledge stays in matter.js. See [CLAUDE.md §matter.js as the Matter Gold Standard](../../CLAUDE.md#matterjs-as-the-matter-gold-standard) for the workflow rule.

These items are NOT implementation gaps; they are idiomatic Go solutions for TypeScript / Python constructs **or** documented production-relevant deviations. During a re-audit / repeatability check they must be scored as ✅ (by design), not ❌. This file does NOT age with implementation waves — by-design is stable.

---

## Overview — Pattern Classes

| Python pattern | Go idiom | Number of items |
|---|---|---|
| Multi-inheritance Mixin / Protocol | Interface + Composition | 5 |
| `@property` / `@cached_property` / `DelegatedProperty` | Method on struct | 20 |
| `@inspector` / `@measure_execution_time` Decorator | `observability/instrument.go` + `boundary/execute.go` | 4 |
| `asyncio.Looper` / `asyncio.Task` / `asyncio.ContextVar` | Goroutine + errgroup + `context.Context` | 8 |
| Python DI container (`*_provider`) | Direct constructor parameter | 2 |
| `@lifecycle_hook` / `model_post_init` / `finalize_init` | Pipeline setup in constructor | 6 |
| SQLAlchemy ORM + `delay_save` (async file storage) | `modernc.org/sqlite` + `sql.Tx` | 11 |
| Singleton (`CENTRAL_REGISTRY`) | Struct + constructor per ADR 0002 | 3 |
| `*_descriptor` / declarative class (Python descriptor protocol) | Constructor + explicit wiring | 8 |
| Python NamedTuple | Standalone Go struct | 5 |
| Decorator-based coordinator hooks (`@callback_backend_system`, `@callback_event`) | Explicit method / goroutine | 4 |
| Python classmethod / factory / Fluent Builder | Package-level function / struct literal | 5 |
| Python typed exception hierarchy | Sentinel errors + `errors.Is` | 2 |
| CCU link management (v1.0-scope exclusion) | REST handler + adapter (out of scope) | 3 |
| Hexagonal: coordinator methods on wrong layer | Coordinator layer or adapter layer | 11 |
| **Total** | | **97** |

---

## A1 — Model Core / Generic

### DI Container / Dependency Injection

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M1002 | `_event_bus_provider` | dp.py:219 | `BaseDataPointFields.publisher` (EventPublisher iface) | `internal/model/generic/datapoint.go` | Python DI: full EventBusProvider injected; Go uses direct EventPublisher interface parameter | W4 |
| M1171 | `_context` (DeviceContext) | device.py:230 | Direct constructor parameter | `internal/model/device/device.go:20` | Python DI-specific DeviceContext object; Go uses direct constructor parameters | W4 |

### Coordinator Delegation (G-21)

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M1XXX | `load_data_point_value()` @inspector | data_point.py:1126 | Coordinator delegation via REST/WS handler | `internal/central/coordinators/`, `internal/north/rest/ws/` | Python `@inspector`-decorated direct method call on the DP; Go delegates the CCU-side value fetch to the coordinator (ingest pipeline) and exposes it via REST/WS handler — by design (hexagonal architecture, SPEC §3). No load loop at DP level. | W4 |

### Lifecycle Hooks (`finalize_init`, `on_config_changed`, `model_post_init`)

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M1039 | `finalize_init()` | dp.py:426 | Pipeline setup in constructor | `internal/model/device/ingest.go` | Python post-init hook for STATUS listener registration; Go uses pipeline-driven setup | W4 |
| M1073 | `on_config_changed()` (BaseDataPoint) | dp.py:617 | Coordinator layer (`Device.OnConfigChanged`) | `internal/central/adapter/` | Python lifecycle hook; Go equivalent lives at device level per hexagonal architecture | W4 |
| M1211 | `finalize_init()` (Device) | device.py:646 | Pipeline setup in constructor / ingest pipeline | `internal/central/adapter/` | Python post-init hook: loads value cache + finalizes channels; Go uses pipeline-driven setup | W4 |
| M1225 | `on_config_changed()` (Device) | device.py:792 | Absent by design (coordinator layer) | `internal/central/adapter/` | Python lifecycle hook for config reload; Go equivalent on coordinator layer | W4 |

### Timestamp Handling / Unconfirmed State

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M1006 | `_unconfirmed_modified_at` | dp.py:233 | Collapsed into single set | `internal/model/generic/datapoint.go` | Python tracks unconfirmed timestamps separately; Go collapses confirmed + unconfirmed — by design | W4 |
| M1007 | `_unconfirmed_refreshed_at` | dp.py:234 | Collapsed into single set | `internal/model/generic/datapoint.go` | Same as M1006 | W4 |

### Path/Routing — MQTT topic from DataPointKey instead of PathData

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M1177 | `_ise_id` | device.py:246 | Separate ReGa integration | `internal/central/adapter/hub_wiring.go` | ReGa feature: ISE ID in CCU ReGa scope; Go handles via separate ReGa integration | W4 |

### BD-Identity-RoutingKeyNamespaces — three deliberately distinct unique-id namespaces

OpenCCU-Loom carries **three** unique-id producers and they are
intentionally *not* unified. Conflating them would either orphan
existing Home Assistant entities or break the HA drop-in's
entity-identity stability.

1. **MQTT-Discovery `unique_id`** —
   `PathData.DiscoveryUniqueID` (`internal/model/naming/pathdata.go`)
   emits `<openccu-loom>_<central?>_<address>_<channel>_<suffix>`. It is
   daemon-namespaced (the `openccu-loom` prefix) and **pinned**: HA
   persists this value in its registry, so changing the format orphans
   every MQTT-discovered entity. It is by design *not* the
   `aiohomematic` routing key — an MQTT-discovery user and an
   `aiohomematic`/loom-client user are two separate integrations with
   two separate registries.

2. **WS / REST `unique_id` field** —
   `BaseDataPointFields.UniqueID()` (`internal/model/datapoint/base.go`)
   emits `<central>:<address>:<keyName>`. This is an **opaque
   daemon-internal scoping / routing token**, not the HA registry key.
   External clients must not feed it to HA as a `unique_id`.

3. **Cross-backend HA routing key** — `internal/routingkey`
   (`GenerateUniqueID`, `GenerateChannelUniqueID`, `HubSlug`) mirrors the
   shared routing-key contract bit-for-bit and is locked by a
   golden-fixture test under `tests/contract/`. It carried one declared
   divergence until 2026-08-28 — CUxD addresses were scoped by the central
   here and left bare by the reference — which retired when aiohomematic
   2026.8.7 landed the same rule (SukramJ/aiohomematic#3370). The cases now
   live in the shared golden fixtures. It exists so the Go side
   can reproduce / validate the key that the HA drop-in client rebuilds
   from `address` + `parameter` (+ `entry_id[-10:]` as the HA-owned
   prefix, which the daemon cannot know). The WS / REST payloads expose
   the raw `address`, `parameter`, `category`, and hub `name`
   (= legacy name) the client needs to rebuild it.

`internal/model/device/naming.go:GenerateUniqueID` is a fourth,
**legacy** generator that is currently unused by production code (only
the naming-pipeline golden test references it). Its rules diverge from
the contract (it central-prefixes VCU channels and capitalises the hub
roots), so it must not be used where the HA routing key is expected; new
consumers use `internal/routingkey` instead. See
`docs/external-clients/ha-drop-in-identity-and-scoping.md` for the full
owner split (client injects the HA prefix; daemon supplies scoping).

### CCU Link Management (v1.0 scope exclusion)

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M1209 | `create_central_links()` | device.py:625 | REST handler + adapter (out of scope) | `internal/central/adapter/central_links.go` | CCU link management: standalone scope; not in v1.0 scope | W4 |
| M1244 | `Channel.create_central_link()` | device.py | REST handler + adapter | `internal/central/adapter/central_links.go` | Same as M1209 | W4 |
| M1245 | `Channel.remove_central_link()` | device.py | REST handler + adapter | `internal/central/adapter/central_links.go` | Same as M1209 | W4 |

### Other Path Asymmetries (standalone placement)

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M1001 | `_central_info` | dp.py:218 | `Config.CentralName` / scalar string | `internal/model/generic/datapoint.go` | Scoped differently: Go stores scalar string, not protocol object | W4 |
| M1004 | `_modified_at` | dp.py:227 | Dual-field `BaseDataPointFields.modifiedAt` + `DataPoint[T].modifiedAt` | `internal/model/generic/` | Dual field in Go; `DataPoint[T]` shadows the base | W4 |
| M1005 | `_refreshed_at` | dp.py:228 | Dual-field (same as M1004) | `internal/model/generic/` | Same dual-shadow pattern | W4 |

### BD-Visibility-IgnoredVsNoCreate — hidden params split into Ignored + NoCreate

aiohomematic uses a single `NO_CREATE` usage for every visibility-suppressed
parameter. OpenCCU-Loom **deliberately splits "hidden" into two usages**:

- `NoCreate` — a generic DP structurally consumed by an aggregating parent
  (Custom / Combined / Week-Profile); never a standalone entity.
- `Ignored` — a DP suppressed by the visibility gate's static rules
  (`IGNORED_PARAMETERS`, `HIDDEN_PARAMETERS`, wildcards, channel-operation-mode
  mask) that is **user-toggleable through the un-ignore feature (ADR 0015)** —
  a capability aiohomematic does not have, so it has no need for the distinction.

**Snapshot consequence:** ~2093 generic DPs (e.g. `CONFIG_PENDING`, `UNREACH`,
`STICKY_UNREACH`, `SECTION`, `UPDATE_PENDING`, `ACTIVITY_STATE`, `DIRECTION`,
and several `MASTER.*` params) emit `usage="ignored"` + `forced_usage="ignored"`
where aiohomematic emits `usage="no_create"` (no `forced_usage`).

**Behaviourally equivalent:** `BaseDataPointFields.Visible()`
(`internal/model/datapoint/base.go`) returns `false` for both `Ignored` and
`NoCreate` — neither is surfaced north-bound. The only consumer that
distinguishes them is the un-ignore candidate list
(`QueryFacade.GetUnIgnoreCandidates`, `internal/central/queryfacade.go`), an
OpenCCU-Loom-only feature. Forcing these DPs to `NoCreate` to match
aiohomematic would delete the un-ignore capability — so this is by design, not
a bug.

**Snapshot tolerance:** `script/model_snapshot_diff.py` (`canon_hidden_usage`)
canonicalises the two hidden usages so this divergence does not count as drift,
while a *real* usage drift (e.g. `ignored`↔`data_point`, where only one side is
a hidden usage) still surfaces.

### BD-Visibility-CDPStateGroupStatus — visible group-STATE split into ce_state

The reference model uses a single `CDP_VISIBLE` usage for every visible
constituent of a custom entity's device. OpenCCU-Loom **deliberately splits
`ce_visible` into two usages**:

- `CDP_VISIBLE` — a genuine extra sensor a custom entity exposes alongside its
  primary control (e.g. HmIP-BWTH `HUMIDITY` / `ACTUAL_TEMPERATURE`, a contact
  `STATE`). Distinct information, not a duplicate of the primary.
- `CDP_STATE` — the group-STATE **status transmitter** a custom actor spans off
  its primary channel via the `FieldGroupState` field mapping (e.g. the valve's
  `WATER_SWITCH_TRANSMITTER` STATE, a switch's status channel). Its value merely
  **restates the primary's own projection** (the valve/switch on/off), so it is
  a redundant status channel.

**Why:** the Matter bridge projects one endpoint per physical device by default
(`north.matter.expose_secondary_channels=false`). It drops a custom entity's
non-primary constituents — `ce_secondary` actor channels and the `ce_state`
status transmitter — while KEEPING genuine `ce_visible` extra sensors. A single
`ce_visible` class cannot express that distinction; `ce_state` can, and only
Matter acts on it.

**Behaviourally equivalent for every other surface:** `EnabledByDefault()` /
`Visible()` (`internal/model/datapoint/base.go`, `internal/model/generic/datapoint.go`)
return `true` for both `CDP_VISIBLE` and `CDP_STATE`, and the MQTT discovery gate
(`internal/north/mqtt/discovery.go`) passes both — so HA-Discovery, MQTT and REST
carry the group-STATE channel exactly as before. The marker is set in
`internal/model/custom/materialize.go` (`applyFieldValueToChannel`, keyed on
`hmenum.FieldGroupState`).

**Snapshot tolerance:** `script/model_snapshot_diff.py` (`canon_state_usage`)
canonicalises `ce_state`→`ce_visible` on both sides, so this split does not count
as drift while a real `ce_visible`↔other drift still surfaces.

### BD-Visibility-ScheduleChannelLocks — raw schedule-lock DPs suppressed

For a non-climate schedule device (e.g. HmIP-MIO16-PCB ch49, HmIP-FWI ch13)
**both** stacks build a structured per-target-channel `ScheduleChannelSwitch`
surface (Go: `attachNonClimateWeekProfileToDevice`,
`internal/central/adapter/week_profile_filter.go`; aiohomematic:
`_create_schedule_channel_switches`, `model/week_profile_data_point.py`).

The divergence is in what happens to the **raw** bitfield DPs
`WEEK_PROGRAM_CHANNEL_LOCKS`, `WEEK_PROGRAM_TARGET_CHANNEL_LOCK`,
`WEEK_PROGRAM_TARGET_CHANNEL_LOCKS`:

- **OpenCCU-Loom** suppresses them (`usage=no_create`, not surfaced) via
  `suppressRedundantScheduleDPs` — the channel switches are the canonical
  surface, so exposing the raw bitfield as well would give Home Assistant a
  redundant sensor + select + number entity beside the proper switches.
- **aiohomematic** builds the same switches but **also** leaves the raw DPs
  visible (`usage=data_point`), so those redundant entities appear.

No functionality is lost: the switch surface (present on both stacks) owns the
per-channel enable/disable; OpenCCU-Loom merely hides the redundant raw DPs.
This is a deliberate cleanup, not a missing data point.

**Snapshot tolerance:** `script/model_snapshot_diff.py`
(`is_schedule_lock_suppression`) tolerates exactly the `no_create`↔`data_point`
suppression signature on these three parameters; any other drift on them still
surfaces.

### BD-Schedule-CoarsestTimeBase — the duration encoder picks the coarsest base, not the natural one

`<NN>_WP_DURATION_BASE` / `_FACTOR` (and the RAMP_TIME pair) encode a duration
as a time base plus a factor the firmware caps at 30. Several pairs express the
same duration — 10s is `(SEC_10, 1)`, `(SEC_5, 2)` and `(SEC_1, 10)` alike — so
an encoder needs a tie-break rule.

- **aiohomematic** starts at the *natural* base for the input unit and promotes
  only when the factor would exceed the cap, so "10s" becomes `(SEC_1, 10)` and
  "40min" becomes `(MIN_5, 8)` (`convert_duration_to_base_factor`,
  `_NATURAL_BASE_INDEX`, `model/schedule_models.py`).
- **OpenCCU-Loom** picks the *coarsest* base that divides evenly within the
  cap, so "10s" becomes `(SEC_10, 1)` and "40min" becomes `(MIN_10, 4)`
  (`weekprofile.ParseTimeBaseFactor`).

Both are valid encodings of the identical duration and the CCU accepts either;
the device evaluates base × factor, not the shape of the pair. The rule differs
because OpenCCU-Loom reached this format from the REST side first, and the
coarsest-base rule was already what its schedule editor wrote to every device in
the field. Switching to the natural base would have re-encoded every stored
schedule on its next save for no behavioural gain.

Two properties are enforced instead of the reference's rule, and they are what
the tests pin (`TestDurationBaseFactorRoundTrip`,
`TestDurationEncodingSettlesAfterOnePass`, `internal/model/weekprofile`):

- **Value preservation.** Decoding is exact — the factor is multiplied out in
  its base's own unit, so `(SEC_5, 13)` reads "65s". Rendering by magnitude
  instead ("1min") loses 5 seconds on the next save, because the string is what
  gets re-encoded.
- **One-pass settling.** A schedule opened and saved unedited may have its pair
  normalised once and never moves again.

The reference's other duration rules are ported unchanged: the factor cap of
30, the eight time bases and their 100ms weights, and the rejection of
sub-100ms granularity. One documented addition — the pair `(HOUR_1, 31)` passes
through verbatim although it sits past the cap, because the firmware parks it on
slots with no duration and the lock domain uses it for "until further notice";
the reference rejects "31h" outright, which would make every door-lock schedule
read from a CCU unsavable.

### BD-Visibility-RedundantForcedUsage — Go records a forced_usage that restates the agreed usage

Click-event press parameters (`PRESS_SHORT`, `PRESS_LONG`, `PRESS`) on physical
remotes and push-buttons (HM-RC-*, HM-PB-*, HMW-IO-*, …) are surfaced as
**button data points on BOTH stacks** — `usage=data_point` on each side.

The two stacks reach that identical verdict by different bookkeeping:

- **OpenCCU-Loom** marks it explicitly. `applyClickEventMarks`
  (`internal/central/adapter/device_pipeline.go`) calls
  `SetForcedUsage(DataPointUsageDataPoint)` for a writable press on a device
  that does not suppress generic buttons, so the snapshot emits both
  `usage=data_point` **and** `forced_usage=data_point`.
- **aiohomematic** arrives at `usage=data_point` from the natural
  visibility/usage resolution and never writes `_forced_usage`, so its snapshot
  emits `usage=data_point` with no `forced_usage`.

The observable surface is the same (a button entity); only the Go-internal
`forced_usage` field is populated. This accounts for the bulk of the
generic-DP snapshot residue (~739 DPs across ~70 button/remote devices). It is
the same class of representation-only divergence already catalogued for
[BD-Visibility-IgnoredVsNoCreate](#bd-visibility-ignoredvsnocreate--hidden-params-split-into-ignored--nocreate)
and the `wrapped_dps` / `profile` tolerated fields.

**Snapshot tolerance:** `script/model_snapshot_diff.py`
(`is_redundant_forced_usage`) drops a `forced_usage` drift **only** when both
sides agree on `usage` and the Go `forced_usage` merely restates that same
usage while aiohomematic leaves it unset. A force that actually *changes* the
realised `usage` still surfaces as a `usage` drift. The two characterised cases
where it changes — actuator-local button events
([BD-Visibility-ActuatorLocalButtonEvents](#bd-visibility-actuatorlocalbuttonevents--keypress-events-on-actuator-local-input-channels))
and the HM-Sec-Key/HM-Sec-Win `DIRECTION` / HmIP-SWSD-2
`SMOKE_DETECTOR_ALARM_STATUS` suppression-direction residue — are handled
separately below / counted against the baseline.

### BD-Visibility-ActuatorLocalButtonEvents — keypress events on actuator local-input channels

Some actuators carry **local push-button inputs** on dedicated channels — the
wired blind/dimmer actuators `HMW-LC-Bl1-DR`, `HMW-LC-Bl1-DR-2`,
`HMW-LC-Dim1L-DR` (ch1/ch2) and the HmIP blind actuator with push-button unit
`HB-LC-Bl1PBU-FM` (ch2/ch3). Their `PRESS_SHORT` / `PRESS_LONG` parameters
diverge:

- **OpenCCU-Loom** surfaces them as a **keypress event** (`usage=event`,
  `forced_usage=event`, `enabled_default=true`) via the `suppressGenericButton`
  branch of `applyClickEventMarks`
  (`internal/central/adapter/device_pipeline.go`): the device has a custom-DP
  profile so the generic *button* is withheld, but the local press is kept as
  an event source so automations can react to a wall-button press.
- **aiohomematic** marks the same channels `no_create` — neither button nor
  event.

This is a deliberate, **more-capable** surface (OpenCCU-Loom exposes the local
press; aiohomematic suppresses it), not a missing/extra data point. ~16 DPs
across the four device families above.

**Snapshot tolerance:** `script/model_snapshot_diff.py`
(`is_local_button_event_suppression`) tolerates exactly the
`event`↔`no_create` signature on click-event press parameters; any other usage
combination on a press parameter still surfaces.

### BD-Visibility-VariantModelHiddenParams — DIRECTION / SMOKE_DETECTOR_ALARM_STATUS hidden on variant models

A handful of parameters are surfaced by the reference stack but kept hidden by
OpenCCU-Loom on **variant device models**:

- `DIRECTION` on `HM-Sec-Key-S`, `HM-Sec-Key-O`, `HM-Sec-Key-Generic`,
  `HM-Sec-Win-Generic` (ch1) — `usage=no_create` (Go) vs `usage=data_point`
  (aiohomematic).
- `SMOKE_DETECTOR_ALARM_STATUS` on `HmIP-SWSD-2` (ch1) — `usage=no_create`
  (Go) vs `usage=ce_visible` (aiohomematic, via the siren custom profile).

OpenCCU-Loom treats these parameters as hidden by default and re-promotes them
only for the **base / exact** device model — the built-in per-device un-ignore
(`unIgnoreParametersByDevice`, `internal/store/visibility/rules.go`) is matched
by reverse-prefix (`deviceUnIgnoresByPrefix`, `internal/store/visibility/decider.go`),
so a longer variant model (`HM-Sec-Key-S`) does not inherit the shorter base
model's (`HM-Sec-Key`) un-ignore and stays at the default-hidden state.

This is a **deliberate** scoping choice: the per-device exceptions are pinned to
the exact models they were authored for, rather than fanning out to every
variant. The affected parameters are status/diagnostic surfaces (lock/window
movement direction, smoke-alarm status), not control points — hiding them on
unlisted variants keeps the north-bound surface conservative. ~5 DPs across the
device families above.

**Snapshot tolerance:** `script/model_snapshot_diff.py`
(`is_reference_only_visibility`) tolerates exactly the `no_create`↔(`data_point`
| `ce_visible`) signature on `DIRECTION` / `SMOKE_DETECTOR_ALARM_STATUS`; any
other usage combination on these parameters still surfaces.

### BD-Snapshot-UnnamedChannelName — unnamed channels: null (openccu-loom) vs stringified number (aiohomematic)

Against the name-less pydevccu / godevccu simulators no channel carries a
custom name. aiohomematic reports an unnamed channel's `name` as the channel
number stringified (channel N → `"N"`, `model/support.py` `get_channel_name`
fallback); openccu-loom leaves it `null`. Both encode "no custom name
assigned"; on a real CCU the operator-assigned names populate both stacks
identically. This is a snapshot-representation difference, not a model gap.

**Snapshot tolerance:** `script/model_snapshot_diff.py` (`_is_unnamed_channel`)
canonicalises the `name` field so `null` / `""` ↔ `str(channel_number)` compare
equal. A real assigned name (neither null nor the channel number) still surfaces
as a `channel_fields` drift.

### BD-Snapshot-InterfaceTopology — interface_id / product_group reflect simulator topology, not the model port

`interface_id` and `product_group` record which XML-RPC interface served a
device. Against the two simulators this is a property of the *simulator's*
fixture topology: godevccu and pydevccu organise the same classic BidCos-RF
devices (`263 x`, `ZEL STG RM DWT 10`, `ASH550`, `IS-WDS-TH-OD-S-R3`, …) under
different interface endpoints, so the two stacks report `HmIP-RF` vs
`BidCos-RF` for the same address (69 devices in the current fleet). On a real
CCU a device is received on exactly one interface and both stacks read the same
value, so these fields agree in production — the divergence carries no
model-fidelity meaning. Aligning the godevccu fixtures' interface assignment to
pydevccu would close it at the source; tracked separately.

**Snapshot tolerance:** `script/model_snapshot_diff.py`
(`_TOLERATED_DEVICE_FIELDS`) excludes `interface_id` and `product_group` from
the `device_fields` drift, so it stays sensitive to a genuine `model` /
`firmware` / `version` regression and to channels aiohomematic exposes that
openccu-loom lacks.

---

## A2 — Custom DPs

### Python descriptor protocol → Go struct fields + explicit wiring

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M2029 | `DataPointField[DataPointT]` descriptor | field.py:20 | Plain struct pointer fields | `internal/model/custom/` | Python descriptor protocol for auto-wiring; Go uses simple struct pointer fields + explicit wiring in constructor | W4 |
| M2047 | `RebasedChannelGroupConfig` dataclass | profile.py:100 | Constructor calls with typed struct fields | `internal/model/custom/` | Python dataclass + factory for dynamic channel rebasing; Go uses constructor calls at the call site with typed struct fields | W4 |

### Python structural Protocol → Go interfaces

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M2035 | `TimerCapable` Protocol | mixins.py:48 | Go interface composition | `internal/model/custom/mixins.go` | Python structural Protocol / multiple-inheritance mixin; Go interfaces fulfil the same role without declaration | W4 |
| M2036 | `ValueCapable` Protocol | mixins.py:55 | Go interface composition | `internal/model/custom/mixins.go` | Same as M2035 | W4 |

### Entity description source for aggregated HA components

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M2080 | Entity description Light/Climate/TextDisplay | (not in `homematicip_local/.../entity_helpers/descriptions/`) | Per-profile builder in custom DP | `internal/model/custom/light/light.go::HADiscoveryPayload`, `…/climate/climate.go::HADiscoveryPayload`, `…/textdisplay/text_display.go::HADiscoveryPayload` | Unlike Switch/Cover/Lock/Sensor/etc. (whose `entity_category`/`enabled_default` come from the flat description tables in `homematicip_local/custom_components/homematicip_local/entity_helpers/descriptions/{switches,covers,locks,sensors,binary_sensors,valves,sirens,buttons,selects,numbers}.py`), Light, Climate, and TextDisplay build their discovery payload **per profile** from the custom DP itself. Both stacks compose structurally differently: aiohomematic resolves the fields in `aiohomematic/model/custom/{light,climate,text_display}.py` directly in the custom DP; `homematicip_local` provides no central description map. openccu-loom mirrors this via an `HADiscoveryPayload` method on each custom DP class. **This asymmetry is by design — no source-of-truth drift.** | W11 (2026-05-04, audit clarification for L13) |

### Cover.IsStateChange — no separate mutex needed

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M2W10 | `Cover.IsStateChange` mutex/lock | cover.py | No separate lock | `internal/model/custom/cover/cover.go` | The Python reference uses a per-method lock around `is_state_change` checks on Cover because async coroutines may interleave reads and writes on the shared mutable state. In Go, `Cover.IsStateChange` reads from the embedded generic data points whose own `sync.RWMutex` already protects concurrent access. A second outer mutex around `IsStateChange` would add lock-ordering complexity for no safety benefit — the Go runtime's data-race detector enforces the same invariant more precisely. By design: no separate Cover-level lock needed. | 2026-05-28 |

### Climate.humidity — float64 vs. int wire type

| ID | Python symbol | File:line | Go path | Rationale |
|----|--------------|-------------|---------|------------|
| A2-D01 | `_dp_humidity: DpSensor[int \| None]` | climate.py:172 | `internal/model/custom/climate/climate.go:163` — `*generic.Sensor[float64]` | The CCU sends humidity as an integer percentage (e.g. `65`). Python's `DpSensor[int]` captures this directly. Go uses `generic.Sensor[float64]` because the generic layer coerces numeric wire values to `float64` uniformly; callers receive a float (e.g. `65.0`) which renders identically in JSON / MQTT payloads (no fractional humidity from the CCU). Changing the type to `Sensor[int32]` would require migrating the generic field constructor (`custom.FloatSensorField` → a new integer-sensor variant) and updating all adapter / MQTT / Matter humidity paths. The float representation is a transport detail only — no semantic difference at the device level. Tracked as a type-precision gap; migration is a P2 task requiring a schema-version bump in the MQTT discovery topic. |

### Lock — optimistic state echo after write

| ID | Python symbol | File:line | Go idiom | Go path | Rationale |
|----|--------------|-------------|---------|---------|------------|
| A2-D20 | `CustomDpIpLock.lock/unlock/open` | lock.py:141-154 | Optimistic echo via `observeCommand` | `internal/model/custom/lock/lock.go::observeCommand` | Python writes to `_dp_lock_target_level` only and waits for the CCU push-callback to update `_dp_lock_state`. OpenCCU-Loom additionally calls `observeCommand` immediately after each write to synthesise a tentative `LOCK_STATE` / `STATE` value. This improves responsiveness for MQTT / REST consumers that read state right after issuing a command, before the CCU round-trip completes. The optimistic value is overwritten by the next CCU push, so correctness is preserved. No Python equivalent exists; this is a deliberate Go-side enhancement. |

### BD-CDP-AvailabilityCarrier — availability gates via OR over primary carriers, not `all()` over the readable set

**Reference mechanism (aiohomematic ADR-0025, #3286).** aiohomematic derives
`CustomDataPoint.is_valid` from `all(dp refreshed for dp in _validity_relevant_fields)`
— an **AND** over a per-class, declaratively-pinned set of state-carrying fields.
That set had to be *shrunk* to state carriers (ADR-0025, superseding the earlier
`_validity_irrelevant_data_points` blocklist) because a secondary readback in the
AND — activity/direction readbacks, group readbacks, colours, `HUMIDITY`,
`*_ALARM_SELECTION`, MASTER fields — that the CCU never re-pushes after a reboot
leaves the whole custom data point stuck at `value_state=restored` for hours
(#3255, #3279). The AND is what makes a never-refreshing secondary field fatal.

**openccu-loom divergence.** Each custom data point gates `IsRefreshed()` on an
**OR** over its wire slots (`AggregateView.IsRefreshed` = *any* slot observed),
so a secondary slot can only make availability *easier*, never block it.
openccu-loom is therefore **structurally immune** to the #3255/#3279 stuck-restored
failure class that ADR-0025 exists to close — it does not need the AND-set-shrink
because it never runs the AND. This is reinforced by how channel values actually
arrive: the initial/periodic bulk seed (`fetch_all_device_data.fn`, gated only on
the CCU holding a timestamped value) refreshes *all* timestamped channel params in
one pass, and live device reports arrive as a batched XML-RPC `system.multicall`
that the callback mux splits per-parameter (`internal/client/transport/xmlrpc/mux.go`),
so the primary carriers land together promptly. Switching to AND would import the
exact reboot-fragility (idle sirens never report `*_ALARM_ACTIVE`; `SETPOINT` lags
after a reboot) that OR avoids, for no practical gain.

**Standing guard.** A contract test pins, per custom-DP type, that its availability
gate observes the ADR-0025 **primary state carrier** — a regression guard against a
future refactor dropping the primary carrier from the gate. The OR semantics and
the current (superset) slot lists are intentionally kept; the pin is positive-only
(carrier ⊆ gate), not `only-carrier`. Per-type carrier mapping (openccu-loom type →
carrier param → aiohomematic ADR-0025 class):

| openccu-loom type | primary carrier param | aiohomematic ADR-0025 class · set |
|---|---|---|
| `switch.Switch` | `STATE` | CustomDpSwitch · `{STATE}` |
| `switch.AccessPermission` | `STATE` | CustomDpIpAccessPermission · `{STATE}` |
| `cover.Cover` | `LEVEL` | CustomDpCover/Blind/IpBlind/WindowDrive · `{LEVEL}` |
| `cover.Garage` | `DOOR_STATE` | CustomDpGarage · `{DOOR_STATE}` |
| `light.Light` | `LEVEL` | CustomDp{Dimmer,ColorDimmer,ColorTempDimmer,IpFixedColorLight,IpRGBWLight,IpRGBWColorTempLight,IpDrgDaliLight,SoundPlayerLed} · `{LEVEL}` |
| `valve.Irrigation` | `STATE` | CustomDpIpIrrigationValve · `{STATE}` |
| `valve.Modulating` | `LEVEL` | modulating-valve variant · `{LEVEL}` |
| `lock.Lock` | `LOCK_STATE` (IP) / `STATE` (RF) | CustomDpIpLock `{LOCK_STATE}` · CustomDpRfLock `{STATE}` · CustomDpButtonLock `{BUTTON_LOCK}` |
| `siren.Siren` | `ACOUSTIC_ALARM_ACTIVE` / `OPTICAL_ALARM_ACTIVE` (+ `SMOKE_DETECTOR_ALARM_STATUS`) | CustomDpIpSiren `{ACOUSTIC_ALARM_ACTIVE, OPTICAL_ALARM_ACTIVE}` · CustomDpIpSirenSmoke `{SMOKE_DETECTOR_ALARM_STATUS}` |
| `climate.Climate` | `ACTUAL_TEMPERATURE` + `SETPOINT`/`SET_POINT_TEMPERATURE` | CustomDp{Ip,Rf,SimpleRf}Thermostat · `{TEMPERATURE, SETPOINT(, mode)}` |
| `textdisplay.TextDisplay` | `BURST_LIMIT_WARNING` (its only readable field) | CustomDpTextDisplay · `frozenset()` — **diverges**: aiohomematic treats a text display as always-valid (empty set → `all(())=True`); openccu-loom anchors observation on the `BURST_LIMIT_WARNING` binary sensor, so a display with no readable field stays unobserved until that warning channel reports (`TestTextDisplayIsRefreshedFalseWithoutBurstLimitWarning`). Intentional. |

(aiohomematic's `CustomDpSoundPlayer {DIRECTION}` has no distinct openccu-loom
custom-DP type; sound handling lives inside `siren`.)

---

## A3 — Calc / Combined / Hub

### Python descriptor protocol → explicit wiring

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M3028 | `CalculatedDataPointField` descriptor | field.py:29 | Explicit struct fields + subscribe.go wiring | `internal/model/calculated/subscribe.go` | Python descriptor protocol; Go uses explicit struct fields + subscribe.go wiring; equivalent functionality, different idiom | W4 |
| M3030 | `__get__(instance, owner)` descriptor protocol | field.py:98 | Explicit field access | — | Python descriptor protocol has no Go equivalent; Go uses explicit field access | W4 |
| M3031 | `data_point_type` DelegatedProperty | field.py:143 | Explicit method delegation | — | Python DelegatedProperty via descriptor; Go uses explicit method delegation | W4 |
| M3069 | `CombinedTimerField` descriptor class | field.py:47 | Builder-pattern constructor calls | `internal/model/custom/` | Python declarative descriptor; Go wires combined DPs explicitly in custom channel constructors; equivalent functionality | W4 |
| M3070 | `CombinedHsColorField` descriptor class | field.py:120 | Builder-pattern constructor calls | `internal/model/custom/light/` | Same as M3069 | W4 |
| M3071 | `_is_combined_field` marker | field.py:52 | Go type system (unnecessary) | — | Python introspection marker; Go type system makes this unnecessary | W4 |
| M3072 | `CombinedFieldProtocol` Protocol | field.py:31 | Go interface | — | Python structural Protocol; Go interfaces fulfil the same role | W4 |

### Python NamedTuple → standalone Go structs

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M3089 | `ProgramDpType` NamedTuple | hub.py:101 | `Program` + `ProgramDpButton` structs | `internal/model/hub/` | Python groups (pid, button, switch) in a tuple; Go uses standalone struct types | W4 |
| M3090 | `MetricsDpType` NamedTuple | hub.py:109 | Flat `Metrics` map | `internal/model/hub/metrics.go` | Python has 3-sensor tuple; Go has flat `Metrics` map — equivalent data, different representation | W4 |
| M3091 | `ConnectivityDpType` NamedTuple | hub.py:117 | `Connectivity` struct with interfaceID key | `internal/model/hub/connectivity.go` | Python NamedTuple grouping; Go uses map-based aggregated types | W4 |

### Hub — fetch methods on wrong layer (coordinator vs hub)

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M3097 | `Hub.create_connectivity_dps()` | hub.py:215 | Direct constructor call in coordinator | `internal/central/coordinators/hub.go` | Python `create_*_dps()` factory; Go uses direct constructor calls in coordinator; builder-pattern divergence | W4 |
| M3099 | `Hub.create_metrics_dps()` | hub.py:268 | `NewMetrics()` constructor | `internal/model/hub/metrics.go` | Same as M3097 | W4 |
| M3101 | `Hub.fetch_connectivity_data(scheduled)` | hub.py:331 | `Connectivity.OnState()` in HubCoordinator | `internal/central/coordinators/hub.go` | Hub.fetch_* methods live on Hub in Python; Go moves fetch logic to HubCoordinator per hexagonal architecture (SPEC §3) | W4 |
| M3104 | `Hub.fetch_metrics_data(scheduled)` | hub.py:388 | `Metrics.Observe()` in HubCoordinator | `internal/central/coordinators/hub.go` | Same as M3101 | W4 |

### Further placement asymmetries

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M3008 | `is_relevant_for_model(channel)` classmethod | dp.py:116 | `Is*Relevant(ch, model)` package-level funcs | `internal/model/calculated/relevance.go` | Go externalises Python class-static methods to package-level functions; idiomatic Go | W4 |
| M3036 | `DerivedBinarySensorRegistry.register()` classmethod | dbs.py:73 | Compile-time `derivedBinaryRegistry` slice | `internal/model/calculated/derived_binary.go` | Python supports runtime registration; Go uses static compile-time slices | W4 |
| M3037 | `is_relevant_for_mapping(channel, mapping)` classmethod | dbs.py:127 | `DerivedBinaryMapping.AppliesToChannel(chNo)` | `internal/model/calculated/` | Same as M3008 | W4 |
| M3053 | `paramset_key = COMBINED` | cdp.py:178 | Implicit in UniqueID prefix | `internal/model/generic/` | Go embeds the key in the UniqueID string; no separate paramset_key property needed | W4 |
| M3056 | `dpk` DataPointKey | cdp.py:212 | `UniqueID` string | `internal/model/generic/` | Python NamedTuple DataPointKey; Go uses composite UniqueID string; equivalent information | W4 |
| M3084 | `_convert_value(old_value, new_value)` | hdp.py:231 | `toWire(v)` with typed switch | `internal/model/hub/` | Python singledispatch coercion; Go uses typed switch in toWire | W4 |
| M3085 | `_get_path_data()` HubDataPoint | hdp.py:248 | Routes generated at registration time in north adapters | `internal/north/mqtt/topics.go` | Python PathData for REST/MQTT routing; Go generates routes at registration time in north adapters | W4 |
| M3112 | `publish_connectivity_refreshed()` | hub.py:528 | Direct EventBus publish | `internal/central/coordinators/hub.go` | Python encapsulates EventBus calls; Go publishes directly on EventBus per hexagonal design | W4 |
| M3114 | `publish_metrics_refreshed()` | hub.py:554 | Direct EventBus publish | `internal/central/coordinators/hub.go` | Same as M3112 | W4 |
| M3124 | `InstallModeDpSensor` separate class | im.py:144 | `InstallMode` unified struct | `internal/model/hub/installmode.go` | Python splits button/sensor into separate classes via inheritance; Go uses a single struct with all methods | W4 |
| M3128 | Sensor split (3 named classes) | metrics.py:172 | `MetricKind` consts + `Metrics.Observe` | `internal/model/hub/metrics.go` | Python has 3 named typed objects; Go uses a unified `Metrics` map | W4 |

---

## A4 — Central + Coordinators

### BD-CCU-SchedulerIntervals — periodic refresh cadences diverge from aiohomematic

aiohomematic's scheduler intervals (`const.py`: `sys_scan_interval=30s`,
`periodic_refresh_interval=15s`, `device_firmware_check_interval=6h`,
`system_update_check_interval=4h`, `metrics_refresh_interval=60s`) do not
match OpenCCU-Loom's (`internal/central/jobs.go`: program/sysvar/inbox/
service-message/alarm 5 min, firmware/system-update 60 min, client-data
refresh 5 min, hub-metrics 5 min, firmware-updating poll 30 s).

**Rationale:** OpenCCU-Loom is **push-event-first** — every MVP interface
supports push callbacks (SPECIFICATION.md §5.1; there is no polling-only
data path). The periodic jobs are *reconciliation safety nets* for the
data that push already delivers, not the primary data path, so they run
far less often than aiohomematic's poll-leaning cadences without losing
freshness. The firmware-updating poll (30 s) is intentionally *faster*
than aiohomematic's 5 min because an in-progress firmware transfer is the
one short-lived state a user actively watches. Operators can override any
interval via config. A faithful match would multiply CCU radio load for
no user-visible benefit. (Flagged by the parity audit 2026-05-30 as
C-01/02/03; classified by-design.)

### BD-CCU-RefreshViaSourceLifecycle — equal-value callbacks don't re-publish a value event

aiohomematic publishes a `data_point_updated_event` on *every* CCU
callback, including ones whose value is unchanged, so MQTT republishes
the value on each refresh (keeping HA's `expire_after`/`force_update`
entities alive) and an optimistic write is confirmed by the echo.

OpenCCU-Loom's `EventCoordinator.HandleRawEvent`
(`internal/central/coordinators/event.go`) updates the cache + interface
liveness on an equal-value callback but suppresses the
`DataPointValueChangedEvent`. **Refresh-without-change is handled by a
different mechanism**: the wire-DP source-token lifecycle
(`unobserved → cache → live → stale → live`, ADR 0019) emits a
`DataPointSourceChangedEvent` on every freshness transition, which the MQTT
bridge + SPA republish even though the value did not change
(`internal/central/adapter/eventbridge.go`). Optimistic confirmation runs
through the `<X>_STATUS` → optimistic-tracker route (see
BD-CCU-StatusUncertainViaTracker), not the value event.

**Rationale:** the two designs reach the same observable outcome via
different plumbing; OpenCCU-Loom's avoids the value-event spam aiohomematic
produces (one bus event per unchanged callback). Forcing equal-value
publishes would *double* the source-token republish. (Parity audit
2026-05-30 C-04 — reclassified bug → by-design after tracing ADR 0019.)
Narrow follow-up: a DP reported frequently enough to never go stale and
unchanged for > `expire_after` would not republish; if that edge matters
the fix is a periodic live-DP heartbeat republish, not equal-value events.

### BD-CCU-StatusUncertainViaTracker — `<X>_STATUS` drives the optimistic tracker, not a status_value enum

aiohomematic stores a `_status_value: ParameterStatus` per data point
(`model/data_point.py`) that drives `is_status_valid` / `state_uncertain`
and is published as a distinct status event. OpenCCU-Loom routes a
`<X>_STATUS` CCU echo to the base data point's optimistic tracker to
confirm / mismatch an in-flight write
(`internal/central/adapter/callback_handlers.go`), and `state_uncertain`
is exposed via `DataPoint.StateUncertain()` and aggregated by calculated
sensors (`internal/model/calculated/state_uncertain.go`).

**Rationale:** both stacks expose `state_uncertain`; OpenCCU-Loom derives
it from the optimistic-write lifecycle rather than a separate
ParameterStatus enum. (Parity audit 2026-05-30 C-07 — reclassified.)

`DataPoint.IsStatusValid` keeps the reference semantics — vacuously true with
no status observation, otherwise valid for `NORMAL` or `UNKNOWN` (no
measured-vs-control discriminator). `UNKNOWN` stays valid for every parameter,
so a control actuator such as `LEVEL` reporting `UNKNOWN` during the init-phase
grace period is still a meaningful position (reference #2630 / PR #2634).

**North-bound `available` is gated on the full `IsValid()` chain (reference
parity).** Mirroring the reference's `model/data_point.py is_valid`, the
north-bound `available` flag now reflects refreshed + acceptable `STATUS` +
value type + range:

- **REST / WebSocket** — `DataPoint.State()` reports `available = observed &&
  IsValid()` (`internal/model/generic/payload.go`).
- **MQTT** — the per-DP runtime (`VALUES`) slot-state publish sets `available`
  from `IsValid()` and republishes the base parameter when its paired
  `<X>_STATUS` event arrives out-of-band
  (`internal/central/adapter/eventbridge.go`). Device-level reachability and the
  `MASTER` / `CALCULATED` planes are not gated.

Consequence: an `OVERFLOW`/`UNDERFLOW` status, or an as-yet-unobserved data
point, publishes as unavailable (with a `null` value) rather than as a
confirmed reading. This intentionally reverses the earlier
`{available:true}`-for-unobserved snapshot convention in favour of reference
parity — a reachable device whose data points have not reported yet now shows
each entity as unavailable until the first real value arrives, exactly as the
reference's `is_valid` does (`is_refreshed` is part of the chain).

**`0.0` after a CCU restart (reference #3228) is fixed at the seed source, not
the status gate.** The `#3228` DEBUG log showed the CCU does **not** emit
`STATUS=UNKNOWN` — every `*_STATUS` event is `NORMAL` and the post-reconnect
`getValue` returns `Fault -5`. The real source of the `0.0` was the
`fetch_all_device_data.fn` bulk-load script coercing an **empty**
(not-yet-measured) numeric value to `"0"`; the script now skips empty values
(`internal/client/rega/scripts/fetch_all_device_data.fn`,
guarded by `internal/client/rega/fetch_all_device_data_test.go`), so the data
point stays unset until a real measurement arrives. The status gate is not
expected to suppress that placeholder — by the time a value reaches the gate it
already carries `STATUS=NORMAL`.

### BD-CCU-ValuesBulkParamsetLoad — the VALUES lazy fallback loads the whole paramset via getParamset, not per-parameter getValue

aiohomematic's `_ValueCache._get_values_for_cache` (`model/device.py`) loads a
single `VALUES` parameter with `getValue` and only batches `MASTER` via
`getParamset`. OpenCCU-Loom loads `VALUES` with `getParamset` as well
(`internal/model/device/value_cache.go`, `runLoadValuesParamset`). The bulk
`fetch_all_device_data` seed only ships data points that already carry a
non-zero value (it skips empties — see BD-CCU-StatusUncertainViaTracker), so the
per-parameter fallback runs for every not-yet-measured parameter — except on the
`VirtualDevices` interface, where the fallback is skipped entirely (aggregated
heating-group VALUES have no backing device, so `getValue` returns only the
CCU-internal default `0`/`STATUS=NORMAL`; this mirrors aiohomematic
`_ValueCache._get_values_for_cache`, so it is parity, not a divergence — #3228).
For all other interfaces, fetching the channel's whole `VALUES` paramset in one
call warms every still-unloaded sibling at once instead of issuing one `getValue`
each.

**Rationale:** fewer CCU round-trips on the fallback path, and a value plus its
paired `<X>_STATUS` arrive from one atomic snapshot. Safety is preserved: the
singleflight key stays **per-parameter** (a forced refresh of one parameter
cannot be coalesced away by another's), the explicitly requested parameter is
always applied, and sibling fills are gated on **not-yet-observed** — a bulk
read therefore never clobbers a restored / already-known value (restore-first /
#3228). A not-yet-measured parameter still yields `Fault -5` / an absent key,
which the existing sentinel path handles without writing a placeholder. Tests:
`internal/model/device/value_cache_test.go`
(`TestLoadValueValuesParamsetSiblingGuard`).

### BD-CCU-CentralLinkTeardownZeroesPressLong — deactivating a central link zeroes PRESS_LONG in addition to PRESS_SHORT

aiohomematic's `Channel.remove_central_link` (`model/device.py`) — like its
`create_central_link` — only ever touches a single value-id,
`REPORT_VALUE_USAGE_VALUE_ID = "PRESS_SHORT"` (`const.py`): it calls
`report_value_usage(value_id="PRESS_SHORT", ref_counter=0)` on teardown and
`ref_counter=1` on setup. OpenCCU-Loom mirrors that on the **setup** path
(`CreateCentralLinks` raises only `PRESS_SHORT`), but on the **teardown** path
(`RemoveCentralLinks`, `internal/central/adapter/central_links.go`) it issues a
**second** `reportValueUsage(value_id="PRESS_LONG", ref_counter=0)` per channel.
Both wire calls together mark the channel done; the first error marks it failed.

**Rationale.** The reference zeroes only `PRESS_SHORT`, but the CCU WebUI's own
`removeCentralLink` (OpenCCU firmware patch
`0171-WebUI-Add-HmIPKeyTransceiverCentralLinkConfiguration.patch`) zeroes
`PRESS_SHORT` **and** `PRESS_LONG` — for both HmIP-RF and BidCos-RF — so the
device-internal direct link is reliably torn down; a lingering non-zero
`PRESS_LONG` ref-counter can leave the device forwarding long-press events to
the CCU after the user deactivated the link. OpenCCU-Loom follows the CCU WebUI
here rather than the reference. Activation stays `PRESS_SHORT`-only, matching
both the WebUI's `createCentralLink` and the reference, because a fresh link only
needs the primary counter raised. Tests:
`internal/central/adapter/central_links_test.go`
(`TestCentralLinksRemoveZeroesPressShortAndLong`,
`TestCentralLinksCreateOnlyRaisesPressShort`).

### BD-North-CustomDPCompositionMap — the Custom-DP field→parameter composition map is deliberately not on the wire

External-client ask **K1** (`notes/reference/external-client-asks.md`) proposed exposing,
per Custom-DP, the full **field→parameter composition** — which wire parameter
on which field-channel composes a Cover/Climate/Light CDP — so a client could
retire `aiohomematic.model.custom.DeviceProfileRegistry`. OpenCCU-Loom exposes
the **channel-level** composition (`CustomDPSummary.channels`,
`ChannelSummary.custom_dp_name`, `ChannelSummary.is_custom_dp_primary`) and the
normalised semantic state (`CustomDPSummary.state`, the typed
`payload.StatePayload`), but **not** the finer per-parameter wiring map. This is
a deliberate non-goal.

**Rationale.** Exposing the parameter-level composition would re-surface exactly
the paramset-level wiring the normalised Custom-DP state
(see [BD-CCU-StatusUncertainViaTracker](#bd-ccu-statusuncertainviatracker--x_status-drives-the-optimistic-tracker-not-a-status_value-enum)
and the typed `StatePayload`) is designed to hide — the two pull in opposite
directions. It would also trade one tight coupling for another: instead of the
client depending on the reference registry, it would depend on the daemon's
**internal profile-graph shape** mirrored 1:1 on the wire, so every profile
change becomes a wire-contract change. The need K1 actually serves —
entity-grouping ("which channels belong to this CDP, which is primary") — is
already met by the channel-level fields above, and a client that wants the raw
parameter inventory of a channel can enumerate it through the existing
`GET …/channels/{no}/data-points` endpoint. Dropping the reference registry does
**not** require this map: after the primary-channel marker and the
`ClimateMode` / `ClimateProfile` enums (`pkg/hmenum/climate.go`, exported into
`assets/schemas/enums.json`), every *output* the registry provided is covered.
The per-parameter composition is therefore additional internal detail with no
client on its critical path, and is intentionally withheld.

### asyncio idioms → Go concurrency

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M4002 | `__aenter__` / `__aexit__` | central_unit.py | defer + context cancellation | `internal/central/central.go` | Python asyncio context manager; Go uses defer + context cancellation; idiomatic Go lifecycle management | W4 |
| M4006 | `_has_active_threads` property | central_unit.py | context cancellation + goroutine lifecycle | `internal/central/central.go` | Python asyncio thread-alive check; Go uses context cancellation; not needed | W4 |
| M4011 | `looper` DelegatedProperty | central_unit.py | `CentralUnit.Scheduler` struct field | `internal/scheduler/` | Python asyncio.Looper; Go uses goroutines + errgroup + context; idiomatic Go concurrency | W4 |

### Decorator-based coordinator hooks → explicit calls

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M4114 | `@callback_backend_system` decorator | rpc_server.py | Goroutines for background work | `internal/central/adapter/` | Python: event(), newDevices() etc. run as background tasks via decorator; Go: handlers run in goroutines where needed | W4 |
| M4193 | `list_devices()` @callback_backend_system | device.py | Explicit `event.Publish` in HandleNewDevices | `internal/central/adapter/callback_handlers.go` | Python `@coordinator_method` / decorator-based hooks; Go uses explicit event.Publish calls | W4 |
| M4211 | `@callback_event` decorator | event.py | Explicit call chain in HandleRawEvent | `internal/central/adapter/eventbridge.go` | Python decorator-based pipeline step; Go uses explicit call chain in HandleRawEvent; architectural divergence, not a gap | W4 |
| M4212 | `@loop_check` decorator | event.py | `context.Context` cancellation checks | `internal/central/adapter/` | Python lifecycle decorator validates asyncio loop state; Go uses context.Context cancellation checks; not needed | W4 |
| M4234 | `@callback_backend_system` decorator (decorators.py) | decorators.py | Explicit method calls + goroutines | `internal/central/adapter/` | Python fires system callback events via scheduler after CCU callbacks; Go has no auto-decorator equivalent | W4 |
| M4235 | `@callback_event` decorator (decorators.py) | decorators.py | HandleRawEvent → DataPointValueChangedEvent | `internal/central/adapter/eventbridge.go` | Python encapsulates event() callback; Go goes directly to DataPointValueChangedEvent; architectural divergence | W4 |

### Observability decorators → instrument.go

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M4236 | `@inspector` decorator | decorators.py | `Instrument` func in observability | `internal/observability/instrument.go` | Python diagnostic logging decorator; Go uses `Instrument` function in observability package | W4 |

### Classmethod / factory / builder → package-level func

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M4033 | `create_central()` classmethod | config.py | Package-level `central.New()` | `internal/central/central.go` | Python classmethod factory; Go uses package-level constructor function; idiomatic Go | W4 |
| M4034 | `create_central_url()` method | config.py | Private helper in adapter | `internal/central/adapter/hub_wiring.go:ccuBaseURLFor()` | Python class method; Go uses private helper in adapter layer per hexagonal design | W4 |
| M4040 | `model_post_init` | config.py | Constructor validation `config.Validate()` | `internal/config/config.go` | Python Pydantic @lifecycle_hook / model_post_init; Go uses constructor validation in config.Validate() | W4 |
| M4046 | `build()` / `validate()` | config_builder.py | Struct literal + `Validate()` | `internal/config/config.go` | Python fluent builder; Go uses struct literal + Validate(); idiomatic Go | W4 |
| M4047 | `ValidationError` frozen dataclass | config_builder.py | Error interface + sentinel errors | `pkg/hmerr/` | Python frozen dataclass; Go uses error interface + sentinel errors | W4 |
| M4124 | `create_subscription_group()` factory method | bus.py | `events.SubscriptionGroup{}` constructor | `internal/central/events/` | Python factory method on class; Go uses struct constructor; idiomatic Go | W4 |

### Singleton → Struct + Constructor (ADR 0002)

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M4093 | `CENTRAL_REGISTRY` module-level singleton | registry.py | `CentralRegistry` struct + constructor | `internal/central/registry.go` | Python module-level singleton; Go uses struct + constructor per ADR 0002 multi-CCU design; no global state | W4 |

### Typed exception → sentinel errors

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M4099 | `InvalidCentralStateTransitionError` | state_machine.py | `statemachine.ErrInvalidTransition` | `internal/central/statemachine/` | Python typed exception hierarchy; Go uses sentinel errors + errors.Is | W4 |

### Python class-level introspection → transport layer

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M4105 | `system.listMethods()` introspection | rpc_server.py | `xmlrpc.Mux.RegisterSystemMethods()` | `internal/client/transport/xmlrpc/` | Python class-level introspection; Go implements in transport layer per hexagonal design | W4 |

### ReGa script comment density

| ID | Scripts | Rationale |
|----|---------|-----------|
| A4-R01 | `acknowledge_message.fn`, `get_alarm_messages.fn`, `get_service_messages.fn`, `set_system_variable.fn` | Go ReGa scripts are smaller (380–733 bytes) than the Python originals because they use terse block comments rather than the Python narrative-style inline comments. Functionality is identical. The size difference is comment density only, not logic. Additional scripts present in Go (`create_system_variable.fn`, `update_system_variable.fn`, `set_device_rooms.fn`, `set_device_functions.fn`) have no Python counterpart — they cover CCU features added for the full REST surface. `get_links_for_device.fn` and `get_install_mode.fn` are also Go-only (planned for phase-B link-management and install-mode coordinator). |

### Hexagonal: coordinator methods on adapter layer

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M4177 | `get_link_paramset_description()` | configuration.py | `adapter.paramsets.GetLinkParamsetDescription()` | `internal/central/adapter/link_param_metadata.go` | In adapter layer, not on ConfigurationCoordinator | W5 |
| M4178 | `copy_paramset()` | configuration.py | `north/rest/ws/commands_extended.go:CopyParamsets` | `internal/north/rest/ws/` | On WS command handler; Python on coordinator with `CopyParamsetResult` | W5 |
| M4179 | `put_paramset()` | configuration.py | `adapter.paramsets.PutParamset()` | `internal/central/adapter/paramsets.go` | In adapter layer with validation; not on ConfigurationCoordinator | W5 |
| M4180 | `WeekProfile.get_weekday()` / `set_weekday()` | week_profile.py:1132,1231 | `SchedulesDomain.GetSchedule()` + `schedule.Climate.Profiles[key].Days[day]` + `SchedulesDomain.SetSchedule()` | `internal/central/adapter/schedule_io.go` | Python has convenience methods for single-weekday access; Go: `GetSchedule` returns the full `*schedule.Climate`, caller reads/writes `Profiles[key].Days[day]` directly and calls `SetSchedule` for persistence. Functionally equivalent, no separate API needed — by design (coordinator delegation, G-31). | W6 |

### Coordinator as pure registry — edge cases (wave E 2026-05-05)

Three test-migration findings: Python coordinators carry methods that in
Go are deliberately moved to other layers. In wave E the content pass
marked these as "Skipped/architecture divergence" — formally documented
as by-design here.

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M4237 | `DeviceCoordinator.create_central_links()` / `remove_central_links()` | central/coordinators/device.py | On adapter layer (`adapter/central_links.go`); DeviceCoordinator holds only `DeviceEntry{Address, Model}` | `internal/central/adapter/central_links.go` | Hexagonal: coordinator = pure registry; link logic is cross-cutting (Devices × Channels × Backend) and belongs in the adapter layer | W6/Wave-E |
| M4238 | `HubCoordinator.get_hub_data_points(registered=True/False)` | central/coordinators/hub.py | `GetHubDataPoints()` without parameter; caller filters via `dp.IsRegistered()` post-fetch | `internal/central/coordinators/hub.go` | Composability: filters are the caller's responsibility; the `IsRegistered()` flag lives on every hub DP via `BaseDataPointFields`. No inline filter parameter needed — follows Go's standard-library style (`io.ReadAll` + filter, not `io.ReadAll(filter=...)`) | W6/Wave-E |
| M4239 | `BackgroundScheduler.Job.next_run` timestamp field | central/scheduler.py | `time.Ticker` + interval-based without per-job timestamp bookkeeping | `internal/scheduler/scheduler.go` | Go idiom: timer tick instead of explicit `next_run` bookkeeping. Skip/advance behaviour is implicit via ticker reset, not via timestamp comparison | W6/Wave-E |

---

## A5 — Client + Reliability

### ClientStateMachine — standalone type

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M5049 | `state_machine` DelegatedProperty | interface_client.py | `InterfaceClient` state fields + `SetState`/`WaitForState` | `internal/client/interface_client.go` | Go ClientStateMachine is a standalone type, not a delegate field on InterfaceClient; idiomatic Go | W4 |

### BIN-RPC — no workaround needed

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| — | BIN-RPC CUxD via JSON-RPC | (workaround) | Native BIN-RPC | `internal/client/transport/binrpc/` | aiohomematic uses JSON-RPC workaround for CUxD; openccu-loom has native BIN-RPC (CLAUDE.md, Critical Rules) | Architecture |

### ClientCoordinator as pure registry (wave E 2026-05-05)

In Python, `ClientCoordinator` is an orchestrator with its own caches
(`_clients_started`, `_primary_client` cache), bootstrap methods
(`_create_clients`, `_init_clients`, `_de_init_clients`),
health subscription (`_on_health_record_event`), and TCP readiness probing
(`wait_for_tcp_ready`). In Go, `ClientCoordinator` is strictly a pure
registry — the specialisations live where they belong domain-wise.
In wave E the content pass marked these as "Skipped" — formally
documented as by-design here.

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M5050 | `ClientCoordinator._primary_client` cache + `_clients_started` flag | central/coordinators/client.py | `PrimaryClient()` recomputed from sorted list; `AllClientsActive()` as read proxy (backs `Available()`) | `internal/central/coordinators/client.go` | Avoid caching; deterministic re-computation from the registry list is sufficient (at most N=8 interfaces) | W6/Wave-E |
| M5051 | `ClientCoordinator.poll_clients` filter | central/coordinators/client.py | `DataPoint.NoPushUpdates` flag per-DP via `internal/model/generic/datapoint.go` | (cross-cutting) | Push capability is a DataPoint property, not a client property; modelling it per-DataPoint is closer to wire behaviour and decouples the coordinator from DP details | W6/Wave-E |
| M5052 | `ClientCoordinator.wait_for_tcp_ready()` | central/coordinators/client.py | `ConnectionRecoveryCoordinator.RecoveryStageTCPChecking` as own recovery stage | `internal/central/coordinators/connection_recovery.go` | TCP readiness is a recovery concern, not a client-init concern; a stage-based pipeline with classification is cleaner than a client method call | W6/Wave-E |
| M5053 | `ClientCoordinator._on_health_record_event` | central/coordinators/client.py | Health tracking in `internal/health/tracker.go`; coordinator status is not driven by health events | `internal/health/` | Health subscription on a pure-registry coordinator is a layer violation; health lives in its own package and propagates via dedicated events | W6/Wave-E |

### Removed unused coordinator / connection-health scaffolding (2026-06-28)

These symbols were carried as design scaffolding but never gained a production
caller. With 1:1 aiohomematic parity no longer a primary goal, they were removed
rather than kept as unwired API; the concept each embodied is recorded here so
the knowledge survives the deletion.

- **`internal/health/connection.go` (`Connection` + `ConnectionRegistry`, ~476 LOC).**
  Modelled per-interface connection health as a standalone object: XML-RPC /
  JSON-RPC circuit state, an in-recovery flag, successful/failed request and
  event counters, and `ConnectionSnapshot` exports, held in a registry keyed by
  interface ID. It was superseded by the live health surface in
  `internal/health/tracker.go` (`Component` / `ClientHealth`, fed by the
  per-central heartbeat and the transport observer), which became the single
  source of health truth — leaving `connection.go` an uninstantiated parallel
  model. When richer per-interface connection diagnostics are wanted, extend the
  tracker rather than reviving this object.
- **`ClientCoordinator.PollClients()`** returned the registered client entries
  whose transport was not connected (re-poll candidates). The coordinator stays
  a pure registry (table above); a "which interfaces are down" view is better
  derived from the health tracker than from a coordinator method.
- **`ClientCoordinator.SubscribeToHealthEvents(bus, onEval)`** wired the
  coordinator to health-evaluation events, which contradicted M5053 (health is
  not driven from the pure-registry coordinator); removal aligns the code with
  that decision.
- **`ClientCoordinator.RestartClients(ctx)`** was a stop → cooldown → start
  convenience over the still-present `StopClients` / `StartClients`. Re-add it
  (or an equivalent) when an operator "restart interface" command lands.
  (`AllClientsActive()` / `Available()` were kept — `Available()` backs the
  per-central connection verdict in `central.go`.)
- **`hmevent.DeviceStateChangedEvent`** aggregated a device's high-level flags
  (`Available` / `LowBattery` / `ConfigPending` / `UpdatePending`) into one
  event. It never had a publisher — only a diagnostics-introspection subscriber
  that therefore never fired. There is no aiohomematic counterpart: there a
  device's `available` and maintenance flags are *derived* properties (an
  availability helper reads `UN_REACH` on channel 0; the maintenance flags read
  the other channel-0 maintenance data points), and changes propagate through
  the ordinary data-point update callbacks. openccu-loom mirrors that: consumers
  read `dev.Available()` / the maintenance accessors on demand (MQTT
  `SetAvailableFunc`, Matter `Reachable`, the SPA), and reactive updates ride the
  underlying maintenance data points' `DataPointValueChangedEvent`. The aggregate
  was therefore a redundant *second* propagation path over the same per-DP
  signals — not a second source of truth, but duplicate notification with no
  consumer. Reintroduce it only together with a consumer that genuinely wants a
  single per-device state-change event (so the event and its subscriber land in
  one step).

---

## A6 — Store + Caches

### Python async StorageProtocol / file-based → synchronous SQLite queries

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M6006 | `delay_save(data_func, delay=1.0)` | storage.py:113 | Synchronous SQLite transactions | `internal/store/sqlite/` | Python asyncio debounced write; Go uses synchronous SQLite transactions; write-batching not needed at application layer | W4 |
| M6007 | `flush()` (StorageProtocol) | storage.py:131 | Not needed (synchronous) | `internal/store/sqlite/` | Python async flush on shutdown; Go SQLite is synchronous, no pending write needed | W4 |
| M6008 | `load()→dict\|None` | storage.py:138 | Direct SQLite queries | `internal/store/sqlite/` | Python async StorageProtocol; Go uses direct SQLite queries via modernc.org/sqlite | W4 |
| M6009 | `remove()` | storage.py:147 | Direct `DELETE` | `internal/store/sqlite/` | Same as M6008 | W4 |
| M6010 | `save(data)` | storage.py:150 | Direct `Upsert` | `internal/store/sqlite/` | Same as M6008 | W4 |
| M6011 | `create_storage(key, ...)` factory | storage.py:177 | SQLite `Open()` | `internal/store/sqlite/` | Python file-based storage factory; Go uses SQLite store open pattern; architectural divergence from file-per-key to SQL | W4 |
| M6013 | `delay_save()` (concrete impl) | storage.py:299 | Synchronous SQLite transactions | `internal/store/sqlite/` | Python asyncio debounced write; Go SQLite writes are synchronous via sql.Tx | W4 |
| M6014 | `flush()` (concrete impl) | storage.py:330 | Not needed | `internal/store/sqlite/` | Same as M6007 | W4 |
| M6015 | `load()` (concrete impl) | storage.py:343 | `sqlite/devices.go:Get`, `sqlite/paramsets.go:Get` | `internal/store/sqlite/` | Python file-based storage with asyncio; Go uses modernc.org/sqlite direct row access per ADR | W4 |
| M6017 | `save()` (concrete impl) | storage.py:391 | `sqlite/devices.go:Upsert` | `internal/store/sqlite/` | Python async file save; Go uses synchronous SQLite Upsert | W4 |

### Python NamedTuple → unexported Go struct

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M6188 | `IgnoreCacheKey` / `UnIgnoreCacheKey` NamedTuple | types.py:29 | `ignoreCacheKey` struct (unexported) | `internal/store/` | Python NamedTuple cache key exported; Go uses unexported struct; idiomatic Go encapsulation | W4 |

---

## A7 — Crosscut + Sub-projects

### asyncio.Looper → goroutines + errgroup

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M7064 | `Looper` class | async_support.py | Goroutines + errgroup | `internal/scheduler/` | Python wraps asyncio loop; Go goroutines are the idiomatic equivalent | W4 |
| M7065 | `Looper.async_add_executor_job()` | async_support.py | `go func()` goroutine | `internal/scheduler/` | Python thread-pool executor; Go uses direct goroutine | W4 |
| M7066 | `Looper.block_till_done()` | async_support.py | `errgroup.Wait()` | `internal/scheduler/` | Python asyncio task wait; Go uses `errgroup.Wait()` | W4 |
| M7067 | `Looper.cancel_tasks()` | async_support.py | context cancellation | `internal/scheduler/` | Python asyncio task cancellation; Go uses `context.cancel()` | W4 |
| M7068 | `Looper.create_task()` | async_support.py | `go func()` | `internal/scheduler/` | Python `asyncio.create_task()`; Go uses `go func()`; idiomatic Go | W4 |

### @inspector / @measure_execution_time → observability/instrument.go

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M7019 | `measure_execution_time()` | decorators.py | `Instrument` func | `internal/observability/instrument.go` | Python `@measure_execution_time` decorator; Go uses `internal/observability/instrument.go` + `boundary/execute.go`; idiomatic Go observability | W4 |
| M7020 | `get_service_calls()` | decorators.py | `MetricsAggregator` observer pattern | `internal/metrics/aggregator.go` | Python WeakKeyDictionary service registry; Go uses MetricsAggregator observer pattern; equivalent observability | W4 |
| M7021 | `_emit_service_metrics()` | decorators.py | `metrics/emitter.go` | `internal/metrics/emitter.go` | Python decorator-internal emission; Go uses metrics/emitter.go explicitly | W4 |

### DelegatedProperty → explicit method delegation

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M7024 | `DelegatedProperty[T]` class | property_decorators.py | Explicit method delegation | (throughout the codebase) | Python descriptor-based DelegatedProperty; Go uses explicit method delegation; idiomatic Go | W4 |

### Singleton → Struct + Constructor

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M7054 | `_TranslationStore` lazy-load singleton | ccu_translations.py | `translations.Translations` struct | `internal/ccudata/translations.go` | Python module-level singleton; Go uses struct + constructor; no global state per ADR 0002 | W4 |

### asyncio.ContextVar → context.Value()

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M7035 | `get_request_id()` via ContextVar | context.py | REST-middleware request ID scoping | `internal/north/rest/middleware/` | Python global ContextVar propagation; Go scopes request IDs to REST middleware layer; idiomatic Go context usage | W4 |

### Python convenience facade → coordinator/adapter

| ID | Python symbol | File:line | Go idiom | Go path | Rationale | Marked |
|----|--------------|-------------|---------|---------|------------|---------|
| M7010 | `PRIMARY_CLIENT_CANDIDATE_INTERFACES` const | const.py | Inlined in central coordinator logic | `internal/central/` | Python module-level constant; Go inlines in central coordinator logic; idiomatic Go | W4 |
| M7015 | `HomematicAPI.read_value()` | api.py | Via coordinator adapter path | `internal/central/adapter/` | Python convenience facade; Go routes via coordinator adapter per hexagonal design | W4 |
| M7016 | `HomematicAPI.write_value()` | api.py | Via coordinator adapter path | `internal/central/adapter/` | Same as M7015 | W4 |

### ws-rest-split — homematicip-local-frontend WS commands → REST + event stream

**Anchor:** `ws-rest-split` — referenced from `internal/north/rest/ws/commands_extended.go` and `cmd/openccu-loom/ws_adapters.go`.

The `homematicip_local` HA integration and the accompanying Lit SPA `homematicip-local-frontend` use a WS command protocol for *all* operations (read + write). openccu-loom has adopted this protocol as a parity shape, but runs its own UI as a **Svelte SPA in `assets/ui/`** that communicates exclusively via REST + WS event stream (see `assets/ui/src/lib/api/ws.ts` — WS is consumed only via `{op:"subscribe", topics:["*"]}` as a read-only stream; all write and read calls go through `/api/v1/...`).

Consequence: a number of WS commands from the reference protocol are **not implemented / dormant** in OpenCCU-Loom, because the in-tree consumers (Svelte SPA, REST clients) do not call them.

**Removed** (non-implementable stubs that only returned errors):

| WS command | Python counterpart | OpenCCU-Loom replacement path |
|---|---|---|
| `devices.copy` | `homematicip_local/config/copy_paramset` | REST: `paramset.copy` (once `ParamsetReader` is wired externally) |
| `devices.export` | `homematicip_local/config/export_paramset` | REST: `/devices/{addr}/channels/{no}/config/export` |
| `links.copy` | (none — was OpenCCU-Loom-specific) | deliberately no replacement; CCU offers no atomic bulk-copy operation |

**Dormant** (adapter families exist as interface + handler in `internal/north/rest/ws/commands_extended.go`, but are disabled via `nil` provider in `cmd/openccu-loom/ws_adapters.go`):

| Interface (file) | WS command | Activation path |
|---|---|---|
| `ChangeHistoryQuery` | `change_history.list` | external WS bridge must wire provider |
| `ChangeHistoryClearer` | `change_history.clear` | ditto |
| `CentralInfo` | `central.info`, `central.connectivity`, `central.system_health`, `central.reconcile` | ditto |
| `ThrottleStats` | `ccu.throttle_stats` | ditto |
| `DeviceStatisticsQuery` | `ccu.device_statistics` | ditto |
| `IncidentClearer` | `incidents.list`, `incidents.get`, `incidents.clear` | ditto |
| `ExtendedHub` | `service_messages.disable`, `service_messages.suppressed`, `service_messages.unsuppress` | ditto |
| `ParamsetReader` | `paramset.copy`, `paramset.form_schema` | ditto |

`CacheClearer` (`ccu.cache_clear`) and `FirmwareRefresher` (`firmware.refresh`)
used to sit in that table and no longer belong there: both are wired in
`cmd/openccu-loom/ws_adapters.go` — the first to `cachereset.Service` (ADR 0042),
the second to `adapter.NewFirmwareDomain`. A table naming a live command as
dormant is worse than no table, because it is the document a reader consults
before deciding a command is not worth wiring.

The authoritative list is not this table but
`TestExtendedCommandsStubEveryOptionalProviderCommandWhenUnwired`
(`internal/north/rest/ws/commands_more_apis_test.go`): it dispatches every
command that must answer `not_implemented` while its provider is nil, so a
command that gains a provider — or loses one — fails there first.

**Rationale:** The Svelte SPA has REST counterparts for all operations relevant to it (`/install-mode`, `/devices/{addr}/firmware/update`, `/devices/{addr}/channels/{no}/config/export`, `/devices/{addr}/links`, `/sessions/edit/*`, `/incidents`, `/audit`, `/metrics`). The WS command frames are a second address space that OpenCCU-Loom does not actively maintain. If someone wanted to run the `homematicip-local-frontend` Lit SPA against OpenCCU-Loom, the bridge would need to wire the `nil` providers.

**Marked:** WS API cleanup 2026-05-05.

---

## Removed Unwired Subsystems (2026-07-04 cleanup)

The full-repo wiring audit removed several subsystems that were built
(and tested) but never reached production wiring. The knowledge is
preserved here; the code lives in Git history (branch
`claude/wiring-audit-cleanup`, pre-removal commits).

| Removed | What it was | Why removed / where the live twin is |
|---|---|---|
| `internal/configui` generator half (`generator.go`, `grouping.go`, `labels.go`, `widget.go`, `step.go`, `link_param_metadata.go`, `easymode/` tree) | Port of the aiohomematic-config form-schema pipeline: FormSchema generation, parameter grouping, label resolution, widget classification, easymode use-case pipelines | Superseded by `internal/central/adapter/uischema_adapter.go` (UISchemaAdapter), which assembles the SPA's rendering schema from the device registry + embedded metadata archives and serves both REST and the `paramset.form_schema` WS command. Only the session/export half of configui is live. |
| `internal/north/matter/cluster/core/power_source.go` | PowerSource (0x002F) cluster server, core-package variant | Duplicate of the production `measurement.PowerSourceServer` (mounted via the measurement cluster factory); schema parity for PowerSource is held by the measurement package's parity tests. |
| `internal/central/statemachine/client.go` + `connection_state.go` | Second port of the client state machine (diverged transition table) + a connection-stage tracker | The live machine is `internal/client/state_machine.go`, embedded in InterfaceClient (now the single source of lifecycle truth). The orphan's one improvement — STOPPING reachable from every non-terminal state — was adopted into the live table before removal. |
| `internal/central/events/batch.go` | Event-batching helper on the internal bus | No consumer ever existed; north-bound batching needs are covered by the MQTT coalescer and the WS replay buffer. |
| `internal/central/adapter/schedule_facade.go` | Facade layer over ScheduleQueryAdapter | REST and WS consume `ScheduleQueryAdapter` directly; the facade added a layer without callers. |
| `internal/central/registry/central.go` (CentralRegistry) | Name→Central lookup with `Central any` | Superseded by the typed `internal/central.Registry`, which is what every northbound adapter uses. |
| `NewQueryFacade` 3-arg wrapper | Model-less query-facade constructor | Folded into a single 4-arg `NewQueryFacade`; the model-less variant only served tests. |

Related (removed in the same cleanup, documented in CHANGELOG): the
publisher-less bus-metric funnel (`Emit*` + metric event types +
`SubscribeObserver`), six sourceless `hmevent` types, and the
scheduler-event wrapper that duplicated the job instrumentation in
`internal/central/jobs.go`.

---

## Matter / matter.js Divergences

> **Matter divergences moved.** The Matter wire stack lives in the
> [go-fabric](https://github.com/SukramJ/go-fabric) module, and the entries
> that describe *its* behaviour moved with it, to
> [`notes/parity/by_design.md`](https://github.com/SukramJ/go-fabric/blob/main/notes/parity/by_design.md).
> What stays below is the host half: how this repository's device model
> projects onto Matter — which HomeMatic device becomes which device type,
> which data point becomes which cluster attribute. A code comment here that
> cites a `BD-Matter-…` id and does not find it below will find it there.


### BD-Matter-LeakAsContactSensor — leak measurement class materialises as ContactSensor (0x0015), not WaterLeakDetector (0x0043)

matter.js `packages/model/src/standard/elements/water-leak-detector.element.ts`
defines the dedicated WaterLeakDetector device type (0x0043, a
Matter-1.3-introduced detector type) that a matter.js bridge would advertise
for leak sensors. OpenCCU-Loom (`pkg/interfaces/matter.go`,
`MatterMeasurementClassDeviceType`) instead maps `MatterMeasurementLeak` to
ContactSensor (0x0015, `contact-sensor.element.ts`).

**Rationale (ecosystem ceiling):** Amazon Alexa's bridge support is pinned
below the detector device-type revisions — field evidence shows that a single
endpoint advertising 0x0043 renders the ENTIRE Alexa-side bridge unresponsive,
taking every other bridged device down with it. ContactSensor keeps the
identical wire surface (mandatory BooleanState 0x0045 server per
`contact-sensor.element.ts`), so only the DeviceTypeList entry differs.
Polarity is non-inverted alarm semantics: the model's boolean passes through
`BooleanStateServer` verbatim, so a detected leak reports StateValue=true
(which ContactSensor renders as "closed or contact" per cluster §1.7.5.1,
`boolean-state.resource.ts`) and dry reports StateValue=false ("open or no
contact"). No loom classifier emits the Leak class yet
(`internal/model/generic/matter.go` has no leak/moisture parameter case), so
the flip landed before any moisture parameter was wired — there is no
migration concern. Pinned by `TestLeakClassMapsToContactSensorDeviceType`
(`pkg/interfaces/matter_funcs_test.go`). Revisit only when the major
ecosystems demonstrably accept the detector device types.

### BD-Matter-WindowCovering-EndProductType — garage doors report RollerShade (0), not Unknown (255)

The matter.js EndProductTypeEnum
(`packages/model/src/standard/elements/window-covering-cluster.element.ts:166-192`)
carries no garage value, and neither does TypeEnum. The garage projection
therefore reports the neutral RollerShade (0) on both Type and EndProductType
rather than the semantically "honest" Unknown (255): at least one ecosystem's
automation/routine device picker drops devices that report
EndProductType=Unknown, which would make a bridged garage door
un-automatable. Similarly, curtains report CentralCurtain (16) as the generic
pick among the three curtain geometries (LateralLeftCurtain=14 /
LateralRightCurtain=15 / CentralCurtain=16) because the HM channel does not
report which geometry the actuator drives. Pinned by
`TestParityMatterJS_WindowCoveringTypeAndEndProductType` in
`internal/model/custom/cover/parity_matterjs_test.go`.

### BD-Matter-WindowCovering-InferredTarget — inferred TargetPosition on externally initiated movement

**Where:** `internal/model/custom/cover/matter.go` (`liftTargetRead`),
`cover.go` (`Cover.OnDirection`), `garage.go` (`Garage.OnSection`).

**matter.js behaviour:** `WindowCoveringServer` derives OperationalStatus
FROM target-vs-current (`#handleLiftTargetPositionChanging` →
`#computeOperationalState`,
`packages/node/src/behaviors/window-covering/WindowCoveringServer.ts:215-222, :271-281`),
because on a native Matter device every movement starts with a command that
first writes the target. There is no upstream code path for movement that
begins without a target write.

**OpenCCU-Loom divergence (bridge-domain, deliberate):** a bridged HM cover
can start moving with no Matter command at all — wall button, CCU program —
surfacing only as a DIRECTION / ACTIVITY_STATE (Cover/Blind) or SECTION
(Garage) push. Reporting the stale commanded target then makes controllers
that derive the motion arrow from target-vs-current (Apple Home) render the
wrong or no arrow. The projection therefore inverts matter.js's derivation
and infers the target from the motion: while opening, a commanded target is
kept only if strictly ahead of current (`target < current` in percent100ths,
0 = open), otherwise the reported target is the direction limit 0; closing is
the mirror image (keep `target > current`, else 10000); at rest the commanded
target is reported while in effect, otherwise the read mirrors
CurrentPosition (matter.js startup init `:142`, StopMotion snap `:490`). An
externally reported moving→stopped transition additionally clears the stored
commanded target (the `handleStopMovement` snap semantics,
`WindowCoveringServer.ts:485-493`, applied to a device-initiated stop).
Pinned by `TestCoverInferredTarget_*`,
`TestBlindInferredTarget_LiftInferredTiltCommandedUntilStop`, and
`TestGarageInferredTarget_SectionMovementAndStop` in
`internal/model/custom/cover/matter_target_position_test.go`. The Blind tilt
axis has no motion signal in the model, so no tilt inference applies
(matching the existing OperationalStatus tilt limitation).

### BD-Matter-WindowCovering-SliderDebounce — GoTo*Percentage two-phase slider debounce with accepted-before-written CCU write

**Where:** `internal/model/custom/cover/matter_debounce.go`; the
`MatterInvoke` GoToLiftPercentage / GoToTiltPercentage / StopMotion /
UpOrOpen / DownOrClose cases in `internal/model/custom/cover/matter.go`.

**matter.js behaviour:** `WindowCoveringServer.goToLiftPercentage` /
`goToTiltPercentage` store `TargetPosition*Percent100ths` immediately and
trigger the movement as a detached worker — the invoke returns Success
before the movement completes
(`packages/node/src/behaviors/window-covering/WindowCoveringServer.ts:574-605`,
`:379-383` "this method returns before actual movement completes"). There is
no command coalescing: a native motor consumes every target write.

**Loom divergence:** the bridged CCU write for a GoTo*Percentage command is
debounced per (device, axis). Delay is two-phase: 400 ms for the first
command of a gesture (a quick swipe's first value is an unwanted
intermediate step) and 150 ms while a drag is active (previous command in
the slot less than 600 ms old). Each new command cancels and replaces the
pending write. A command whose destination is within 1 % (100 percent100ths)
of the observed current position is acknowledged without any radio write and
additionally drops a pending intermediate drag value. StopMotion cancels all
pending axis writes; UpOrOpen / DownOrClose cancel the pending writes of the
axes they drive. Consequence: the Matter command returns Success on
acceptance, and a subsequently failing deferred CCU write is only logged
(`cover: deferred window covering position write failed`) — the commanded
target stays reported and the position mismatch surfaces through the normal
CCU value-event echo.

**Why:** Apple Home / Google Home slider drags emit 5-10 GoTo commands in
quick succession. HM cover actuators sit behind a duty-cycle-limited radio;
forwarding every intermediate value as its own LEVEL / LEVEL_COMBINED /
DOOR_COMMAND write makes the motor stutter and burns duty cycle. Loom's
client-side coalescer is singleflight-for-identical-calls and never
coalesces distinct slider values. matter.js needs no such layer because it
drives a native motor, not a radio-constrained bridge target. REST / MQTT /
WS set-position paths are deliberately NOT debounced — only the Matter GoTo
invoke path carries slider-gesture bursts.

**Guards:** `internal/model/custom/cover/matter_debounce_test.go`
(gesture-start vs active-drag delays via fake clock, drag replace, at-target
skip incl. pending-drop, StopMotion cancellation, Subscribe-detach
cancellation, per-axis independence on Blind, real-timer smoke test) and
`TestParityMatterJS_GoToPercentageStoresTargetBeforeDeviceWrite` (target
readable at invoke return, CCU write only after the debounce window).

## Appendix: Where are these items marked in the sources?

The items in this file originate from the following audit sources. These files are historical and do NOT serve as the source of truth for open gaps — for that, the current `parity_audit_v4.md` and `/tmp/parity_v4_a*.md` apply.

| File | PATH-ASYMMETRY by design (items) |
|---|---|
| `notes/parity/v2/a1_model_core_generic.md` | M1001–M1009, M1039, M1073, M1171, M1177, M1209, M1211, M1225, M1244, M1245 (12 explicitly marked) |
| `notes/parity/v2/a2_custom.md` | M2029, M2035, M2036, M2047 (4 explicitly marked) |
| `notes/parity/v2/a3_calc_combined_hub.md` | M3008, M3028, M3030, M3031, M3036, M3037, M3053, M3056, M3069, M3070, M3071, M3072, M3084, M3085, M3089, M3090, M3091, M3097, M3099, M3101, M3104, M3112, M3114, M3124, M3128 (25 explicitly marked) |
| `notes/parity/v2/a4_central.md` | M4002, M4006, M4011, M4033, M4034, M4040, M4046, M4047, M4093, M4099, M4105, M4114, M4124, M4193, M4211, M4212, M4234, M4235, M4236 (19 explicitly marked) |
| `notes/parity/v2/a5_client.md` | M5049 (1 explicitly marked) |
| `notes/parity/v2/a6_store_schemas.md` | M6006–M6011, M6013–M6015, M6017, M6188 (11 explicitly marked) |
| `notes/parity/v2/a7_crosscut_subprojects.md` | M7010, M7015, M7016, M7019, M7020, M7021, M7024, M7035, M7054, M7064–M7068 (14 explicitly marked) |

**Total explicitly marked from v2 sources: 86 items.**
Additionally, the v4 inside-out audit (parity_audit_v4.md) captures 239 🔄 items reflecting the same pattern classes — most of them are A1/A2/A5/A6 placement asymmetries (method on coordinator instead of on DataPoint/Device, adapter instead of coordinator, etc.).

---

## Inside-Out Audit Addenda (2026-05-27)

Items discovered during the inside-out audit run captured in
`parity_audit.md` / `parity_audit_matter.md` / `parity_audit_chip.md`
that qualify as 🔄 by-design rather than implementation gaps.

### A2 — Custom DPs

| Symbol | Loom path | Rationale |
|---|---|---|
| Python `CustomDpWindowDrive` subclass | `internal/model/custom/cover/cover.go` `Cover` + `Config.WindowDrive bool` + `CoverVariant=VariantWindow` | Single struct + variant flag collapses the Python subclass — same behaviour surface, less duplication. |
| `valve.Modulating` (Go-only forward declaration) | `internal/model/custom/valve/valve.go` | No aiohomematic peer; the LEVEL-based modulating valve infrastructure is in place for when a future device profile maps onto it. Until then it remains unregistered (no `init.go` entry). Full rationale: entry `A2-BD03`. |

### A5 — Scheduler

| Field | Go default | Python default | Rationale |
|---|---|---|---|
| `defaultCheckConnectionSlot` | 30 s | 15 s | Go's BIN-RPC push channel covers most stale-callback detection; the slower poll is a safety net, not the primary signal. |
| `defaultConnectivityRefresh` | 2 min | 60 s | Hub connectivity-DP refresh is non-critical for any user-visible signal (HA "available" comes from broker-LWT). Slower default keeps CCU radio traffic minimal. |
| Scheduler job-name labels (`central.*` vs Python `_refresh_*`/`_check_*`) | Go uses `central.*` prefix labels | Python uses private-method names | Diagnostic-only divergence — log greps don't need to align cross-stack. |

### A6 — Sub-Projects

| Item | Loom path | Rationale |
|---|---|---|
| `ConfigChangeLog` placement | `internal/audit/change_log.go` (NOT `internal/configui/`) | Audit-trail concerns live in the audit package across both stacks; `configui` only consumes the recorded entries. Matches Go's package-by-responsibility convention. |
| 6-stage SUBTYPE translation chain | `internal/ccudata/translations.go::DeviceModelLabel` (lines 133-191) | Go strict superset of Python's 2-stage chain. Stages 3-4 (vendor-prefix strip, space-tail drop, iterative `-X` token drop) fix 25 historic SUBTYPE-propagation bugs. Tested via `TestDeviceModelLabelSubtypePropagation`. |

### A4 — Backend Signature Parameters (`markers`, `include_internal`, `max_wait_time`, `poll_interval`)

Python's `CcuBackend` (`aiohomematic/client/backends/ccu.py`) passes `markers` kwargs to `get_all_programs` and `get_all_system_variables`, and uses `include_internal` on some fetch helpers. `create_backup_and_download` takes `max_wait_time` and `poll_interval` as keyword arguments.

Go's `Operations` interface (`internal/client/backends/backend.go`) handles these differently:

- **`markers`** — not passed into `GetAllPrograms` or `GetAllSystemVariables`. The `Operations` interface comment states "Marker-based filtering is the caller's responsibility." Filtering by `DescriptionMarker` is a presentation concern that belongs at the coordinator or adapter layer, not in the raw backend call. This keeps the backend interface stable against future marker changes.
- **`include_internal`** — absorbed into the backend implementation; internal entries are always returned and the coordinator layer filters them based on context.
- **`max_wait_time` / `poll_interval`** — present in the Go `Operations` interface signature for `CreateBackupAndDownload(ctx context.Context, maxWaitTime, pollInterval float64)`. The Go `CcuBackend` implementation ignores these two parameters (uses `_`) because Go's context-based cancellation subsumes the polling responsibility. Callers pass the documented Python defaults (300 s / 5 s) to satisfy the interface; the Go implementation uses `context.WithTimeout` semantics internally.

These are deliberate caller-responsibility divergences, not missing features.

### A1 — Cache TTL Strategy (per-paramset vs. global)

| Item | Go value | Python value | Rationale |
|---|---|---|---|
| VALUES paramset TTL | no-expiry (push-invalidated) | `MAX_CACHE_AGE = 10 s` | Go receives VALUES updates via push events (XML-RPC/BIN-RPC callbacks); no polling needed. A push event is the authoritative invalidation signal; the cache holds the last-known value indefinitely until the next push arrives. Python polls and therefore needs a short global TTL as a stale-read guard. |
| MASTER paramset TTL | 30 min | `MAX_CACHE_AGE = 10 s` | MASTER reads are expensive CCU radio operations (one XML-RPC `getParamset` per channel). 30 min reflects the fact that MASTER changes are operator-driven and rare; shorter TTL would produce unnecessary radio load on large installations. |
| Sentinel paramset TTL | 5 min | `MAX_CACHE_AGE = 10 s` | Sentinel (placeholder) entries are created on cache-miss to prevent hammering the CCU on repeated requests for the same missing key. 5 min is a safety window that balances retry frequency against radio cost. |

These three TTLs are intentional design choices, not an omission of Python's global constant. The Python `MAX_CACHE_AGE` works as a single safety net because Python's aiohomematic polls all paramsets; Go differentiates because push-based VALUES delivery makes an expiry clock redundant for that tier, while keeping MASTER/sentinel TTLs conservative.

Go paths: `internal/store/sqlite/paramsets.go`, `internal/central/coordinators/` paramset-cache policy.

### A4 — Reconnect Initial Backoff (2 s vs. 5 s)

Go uses a 2 s initial backoff (`ReconnectConfig.InitialDelay` default in `internal/client/interface_client_orchestration.go`) versus Python's 5 s (`BASE_RETRY_DELAY` in `aiohomematic/client/const.py`).

Rationale: Go's coalescer and circuit-breaker pre-filter duplicate retry traffic so the first retry attempt carries low risk of stampede. A 2 s initial delay gives faster recovery on transient CCU network blips (common during CCU firmware updates) without meaningful overload risk. The delay is capped at 120 s max (Python caps at 60 s) to allow the same long-term back-off behaviour.

### A5 — EventStalenessThreshold (60 s vs. 300 s)

Go: `EventStalenessThreshold = 60 s` (`internal/health/connection.go`). Python: implicit 300 s window derived from `PING_PONG_MISMATCH_COUNT_TTL = 300 s` in `aiohomematic/client/const.py`.

Rationale: Go's BIN-RPC push channel fires liveness events more frequently than Python's polling paths. A 60 s threshold catches stale connections faster without false positives because the callback server receives at least one keepalive event per 30 s interval on a healthy connection. 300 s in Python reflects that polling-only paths emit events less frequently; shrinking the threshold to 60 s there would produce spurious DISCONNECTED transitions.

Go path: `internal/health/connection.go::EventStalenessThreshold`.

### B — PingPong Cache MaxEntries (1000 vs. 100)

Go: `PingPongConfig.MaxEntries` defaults to 1000 (`internal/client/reliability/pingpong.go:124`). Python: `PING_PONG_CACHE_MAX_SIZE = 100` (`aiohomematic/client/const.py`).

Rationale: Go's `PingPongTracker` uses a single-pass eviction strategy (evicts oldest 20 % entries when the cap is reached) with O(N) scan cost paid only at eviction time, not on every RecordPing/RecordPong call. At 1000 entries the eviction overhead is negligible and the larger window improves anomaly detection across large device fleets (a 400-device installation with frequent ping cycles would saturate a 100-entry window within seconds). Python's in-memory dict has the same asymptotic cost but the smaller cap reflects Python's single-threaded asyncio event-loop budget constraints; Go goroutines are not subject to the same constraint.

### A7 — `textDescriptionsByDevice` — production reader in place

The `textDescriptionsByDevice` map and its exported accessor `LookupTextDisplayByDevice` in `internal/north/mqtt/entity_descriptions.go` are production code used at lines 202 and 437 of the same file (inside `EntityDescriptionFor`). The map is not test-only. Tests in `internal/north/mqtt/text_display_press_event_test.go` exercise the production reader directly. No additional production path is required; the audit note was a false positive.

### L2 (A7) — `/admin/mqtt/reload` path alignment

OpenAPI `assets/openapi.yaml` declares `POST /admin/mqtt/reload`. The chi router registers the handler at `pr.Post("/mqtt/reload", ...)` inside the `/api/v1` group — which resolves to `POST /api/v1/mqtt/reload`, not `/api/v1/admin/mqtt/reload`.

**Resolved**: the router now registers the path as `/admin/mqtt/reload` (matching the OpenAPI spec), so the effective route is `POST /api/v1/admin/mqtt/reload`. The admin middleware (`pr.With(admin)`) is unchanged; only the path segment was corrected.

Go path: `internal/north/rest/router.go`.

---

### A5 — `AllClientsActive` empty-set semantics

`ClientCoordinator.AllClientsActive()` (`internal/central/coordinators/client.go`) returns `false` when no clients are registered. Python `ClientCoordinator.available` (`aiohomematic/central/coordinators/client.py`) returns `True` for an empty set (`all([])` is `True` in Python).

The Go choice is intentional: a central unit with zero clients is not operational and should not be surfaced as "available". An empty set means setup is incomplete or all clients have been removed at runtime; marking the central as unavailable causes `EvaluateCentralState` to transition to `CentralStateFailed`, which triggers the operator dashboard to show a warning. Python's `True`-for-empty behaviour reflects asyncio boot ordering where clients are added asynchronously — Go initialises clients synchronously before state evaluation runs.

Go path: `internal/central/coordinators/client.go::AllClientsActive`.

### A5 — `ReplaceDevice` model-check strictness

`DeviceCoordinator.ReplaceDevice` (`internal/central/coordinators/device.go`) pre-fetches the new device description and rejects the replacement if the type field differs from the old model (e.g. HmIP-PSM → HmIP-PSM-2). Python `device.py::replace_device` skips this check and replaces unconditionally.

The stricter Go behaviour is intentional: a model mismatch between old and new device means the replacement may expose a different set of channels or parameters. Accepting it silently would produce a stale domain model (old-profile data points surviving under the new address) and can cause incorrect MQTT Discovery payloads. Operators who genuinely replace a device with a different model must remove the old entry first, then add the new one — the same sequence Python's coordinator would trigger internally.

Go path: `internal/central/coordinators/device.go::ReplaceDevice`.

### A5 — Dual Registry types (`central.Registry` + `registry.CentralRegistry`)

OpenCCU-Loom has two registry types for centrals:

- `internal/central/central_registry.go::Registry` — the operational registry; holds `*CentralUnit` directly and exposes `HubFor`, `StartAll`, `StopAll`.
- `internal/central/registry/central.go::CentralRegistry` — an opaque anti-coupling layer where `Central any`; used in packages that must reference "a central" without importing `internal/central` (avoids import cycles).

Python has a single `CentralRegistry` singleton (`aiohomematic/central/registry.py`). The split is by design: Go's strict import graph prevents `internal/central` from being imported by packages that `internal/central` itself imports. `registry.CentralRegistry` is the decoupled reference; `central.Registry` is the concrete operational type. Only `central.Registry` supports `HubFor` and lifecycle operations; consumers that need those must receive a `*central.Registry` directly.

Go paths: `internal/central/central_registry.go`, `internal/central/registry/central.go`.

### A5 — Scheduler cadence divergence (comprehensive)

All Go scheduler job intervals diverge from Python defaults. The divergence is intentional and explained by the push-vs-poll architecture difference. The table below records the full set:

| Job | Go default | Python default | Rationale |
|---|---|---|---|
| `check_connection` | 30 s | 15 s | BIN-RPC push covers primary liveness; 30 s is a safety net |
| `refresh_client_data` | 5 min | 15 s | Push events replace polling for VALUES; 5 min is a reconcile guard |
| `refresh_program_data` | 5 min | 30 s | Programs change infrequently; 5 min balances freshness vs. radio cost |
| `refresh_sysvar_data` | 5 min | 30 s | Same rationale as programs |
| `refresh_inbox_data` | 5 min | 30 s | Inbox items arrive via push events on most CCU firmware |
| `refresh_service_messages_data` | 5 min | 30 s | Service messages are low-urgency; 5 min prevents radio congestion |
| `refresh_alarm_messages_data` | 5 min | 30 s | Same rationale as service messages |
| `refresh_system_update_data` | 60 min | 4 h | Go polls more frequently to give operators faster firmware-update visibility; the shorter interval still avoids radio stress |
| `firmware_check` | 60 min | 6 h | Same as system update |
| `firmware_updating_check` | 30 s | 5 min | Active firmware update needs fast polling to detect completion |
| `metrics_refresh` | 5 min | 60 s | Metrics are diagnostic-only; 5 min is sufficient for dashboard staleness |
| `connectivity_refresh` | 2 min | 60 s | Connectivity DP is non-critical; 2 min keeps traffic minimal |

Go path: `internal/central/jobs.go` (default interval constants).

### A5 — `_STATUS`-suffix event subscription architecture

Python `EventCoordinator._add_status_subscription` (`aiohomematic/central/coordinators/event.py`) publishes two events for a status-bearing data point: one for the base parameter name and one specifically for the `_STATUS` suffix. Go inverts this: `HandleRawEventNormalized` (`internal/central/coordinators/event.go`) strips the `_STATUS` suffix and publishes a single event under the base name.

The Go architecture is intentional. Subscribers that need the raw status event can inspect the `RPCParameterReceivedEvent.Parameter` field before the suffix is stripped (the raw name is preserved in the event). Emitting a single normalised event avoids the double-dispatch overhead and reduces bus pressure on large installations with many status data points. The strip logic mirrors the STATUS normalisation in Python's `data_point_event` path, just applied before publication rather than in the subscriber.

Go path: `internal/central/coordinators/event.go::HandleRawEventNormalized`.

### A5 — `ClientCoordinator.SubscribeToHealthEvents` — explicit wiring, not self-subscribing

Python `ClientCoordinator.__init__` self-subscribes to health record events on construction. Go `ClientCoordinator.SubscribeToHealthEvents` (`internal/central/coordinators/client.go`) returns a subscription handle that the caller must invoke explicitly.

The explicit wiring is by design: Go's hexagonal architecture treats the event bus as a constructor-injected dependency; a coordinator that wires itself in its constructor creates a hidden coupling between construction order and bus availability. The caller (central adapter wiring layer) is responsible for calling `SubscribeToHealthEvents` at the correct point in the startup sequence, after the bus is live. This matches the pattern used for all other subscription sites in `internal/central/adapter/`.

Go path: `internal/central/coordinators/client.go::SubscribeToHealthEvents`.

### A6 — `configui.Generate` / `LabelResolver` / `ParameterGrouper` — two parallel pipelines by design

`internal/configui/generator.go::Generate`, `internal/configui/labels.go::LabelResolver`, and `internal/configui/grouping.go::ParameterGrouper` are full ports of the Python `FormSchemaGenerator` / `LabelResolver` / `ParameterGrouper` types from `aiohomematic-config`. They produce `FormParameter` / `FormSection` / `FormSchema` output.

The `UISchemaAdapter` (`internal/central/adapter/uischema_adapter.go`) builds UI schema responses for the REST API using its own inline path rather than calling `configui.Generate`. The adapter was written first and the configui port followed separately; both pipelines converge on the same translation tables and easymode metadata.

The two pipelines coexist intentionally:

- The configui package is the canonical Go port of the Python schema-generation surface. Its output type (`FormSchema`) is the wire format for WS commands `paramset.form_schema` and `links.get_form_schema`.
- The UISchemaAdapter produces `UISchema` (REST DTO), which includes additional fields (`Profile`, `CrossValidations` inline) and is shaped for the SPA rather than for machine clients.

Merging them would require a `FormSchema → UISchema` mapper layer. That is a valid future refactoring but not required for correctness: tests in `internal/configui/` cover the configui path; integration tests via the REST API cover the UISchemaAdapter path. Any drift between the two output shapes is caught by those test suites.

Go paths: `internal/configui/generator.go`, `internal/central/adapter/uischema_adapter.go`.

### A6 — `internal/configui/easymode/{uc2,uc5,uc6,crossvalidation}` Pipeline — not wired in production

The `Pipeline` type in `internal/configui/easymode/usecase.go` and its concrete use-cases (`uc2`, `uc5`, `uc6`, `crossvalidation`) implement the Python `_enrich_easymode` / `_build_subset_groups` pipeline stages from `FormSchemaGenerator`. They were developed as part of an earlier "pipeline architecture" evaluation.

The UISchemaAdapter (`internal/central/adapter/uischema_adapter.go`) produces SubsetGroups, Visibility rules, and CrossValidations directly (lines 127–163) without instantiating the Pipeline. The use-case packages remain in the codebase because:

1. They are fully covered by their own unit tests.
2. They represent the canonical Go port of the Python pipeline stages; removing them loses the provenance mapping.
3. A future refactoring that routes the UISchemaAdapter through `configui.Generate` would pick these use-cases up naturally.

Until that refactoring lands, the packages are intentionally present-but-unwired. No production caller should be added without also routing the UISchemaAdapter through the `Generate` entrypoint.

Go paths: `internal/configui/easymode/usecase.go`, `internal/configui/easymode/uc2/`, `internal/configui/easymode/uc5/`, `internal/configui/easymode/uc6/`, `internal/configui/easymode/crossvalidation/`.

---

### A1 — Climate / Lock / InstallMode: Subclass hierarchy → Tagged-Struct

Python uses distinct subclasses for each Climate variant (`SimpleRfClimate`, `RfClimate`, `IpClimate`), each Lock variant (`RfLock`, `IpLock`, `ButtonLock`), and separates `InstallModeDpSensor` / `InstallModeDpButton` into two concrete types.

Go uses a single tagged struct with a `Kind` discriminator field:

- `climate.Climate` with `Kind` values `KindSimpleRf`, `KindRf`, `KindIP`.
- `lock.Lock` with `Kind` values `KindIP`, `KindRF`, `KindButton`.
- `hub.InstallMode` is a single struct; `Press()` is a method on the struct rather than a separate `InstallModeDpButton` subclass.

The tagged-struct pattern avoids Go's lack of structural subtyping and allows the north-bound adapters to share a single type assertion path instead of a type-switch over a closed set of subclasses. The `Kind` field drives per-variant behaviour inside the methods.

Go paths: `internal/model/custom/climate/climate.go`, `internal/model/custom/lock/lock.go`, `internal/model/hub/install_mode.go`.

---

### A1 — Program / MetricsDpType / ConnectivityDpType: NamedTuple → Struct

Python defines `ProgramDpType(NamedTuple)`, `MetricsDpType(NamedTuple)`, and `ConnectivityDpType(NamedTuple)` as lightweight named-tuple wrappers grouping a button DP and a sensor DP.

Go uses full struct types (`hub.Program`, `hub.MetricHubSensor`, `hub.Connectivity`) with methods rather than plain field-tuples. The struct-with-methods pattern is idiomatic Go, avoids anonymous field access, and allows attaching validation / observer hooks directly to the type.

Go paths: `internal/model/hub/program.go`, `internal/model/hub/metrics.go`, `internal/model/hub/connectivity.go`.

---

### A1 — BaseClimateSensor Generic-Subclass → Shared Helper

Python uses a generic abstract base class `_BaseMetricsSensor[SensorT: float | None]` to share logic across `SystemHealthSensor`, `ConnectionLatencySensor`, and `LastEventAgeSensor`.

Go uses three concrete `MetricHubSensor` wrapper structs (sharing a common `hubSensorBase` embedded helper) rather than a generic abstract base. Go generics would work syntactically but the three sensor types diverge enough in their value types (`float64` vs `time.Duration`) that a single parameterised base gives little benefit over the flat struct composition.

Go path: `internal/model/hub/metrics.go`.

---

### A4-B01 — JsonCcuBackend removed by design

Python `aiohomematic` supports a `JsonCcuBackend` for CCU-Jack JSON-only mode.

OpenCCU-Loom does not include a `JsonCcuBackend`. The JSON-RPC transport is used only as a fallback for specific CCU API calls (e.g. ReGa) where XML-RPC is not available; device-event delivery always uses XML-RPC or BIN-RPC callbacks. JSON-only mode is classified as a non-goal in `SPECIFICATION.md §2.2`.

Go path: `internal/client/backends/`.

---

### A4-B02 — CUxD uses BIN-RPC directly

Python `aiohomematic` routes CUxD through JSON-RPC as a workaround because it lacks a BIN-RPC callback server.

OpenCCU-Loom runs its own BIN-RPC callback server (`internal/central/callback/binrpc/`) and speaks BIN-RPC directly to CUxD. This is a stated CLAUDE.md critical rule and enables real push-event delivery from CUxD devices without polling.

Go path: `internal/client/backends/cuxd_backend.go`, `internal/central/callback/binrpc/`.

---

### A4-B03 — Per-class throttle (Read / Write / Control)

Python uses a single shared `Throttle` instance per interface client.

Go splits the throttle into three independent per-class channels: `ReadThrottle`, `WriteThrottle`, and `ControlThrottle` (`internal/client/reliability/throttle.go`). This prevents high-frequency read traffic (e.g. bulk paramset fetches at boot) from starving urgent write commands, which is important for large fleets where the read burst at reconnect can saturate a single-channel throttle for several seconds.

Go path: `internal/client/reliability/throttle.go`.

---

### A4-B04 — ReGa script set extended

Python `aiohomematic` uses a fixed set of ReGa scripts for read operations. OpenCCU-Loom extends the set with write-side scripts: `create_system_variable`, `update_system_variable`, `set_device_rooms`, and `set_device_functions`. These write paths use ReGa because the equivalent XML-RPC endpoints either do not exist or are unreliable on all supported CCU firmware versions.

Go path: `internal/client/backends/rega/scripts/`.

---

### A4-B05 — Permanent-fault classification via message text

Python classifies circuit-breaker faults by exception type only.

OpenCCU-Loom adds an additional classifier that inspects the error message string for known-permanent fault signatures (e.g. `"AUTH_FAILED"`, `"INVALID_SESSION"`). When a permanent fault is detected the circuit breaker transitions directly to OPEN rather than going through the half-open retry cycle, reducing the reconnect storm on credentials-changed or session-expired scenarios.

Go path: `internal/client/reliability/circuit_breaker.go`.

---

### A4-B06 — RecoveryWaiter wired directly from circuit-breaker state change

Python's `RecoveryWaiter` subscribes to the event bus for circuit-breaker open events.

Go's `RecoveryWaiter` (`internal/client/reliability/recovery_waiter.go`) is notified directly by the circuit breaker via a callback registered at construction time. This avoids an event-bus round-trip on the hot path and makes the recovery sequence synchronous within the circuit-breaker's state machine.

Go path: `internal/client/reliability/recovery_waiter.go`.

---

### A4-B07 — `Operations.initialize()` — state managed differently

Python `Operations.initialize()` is a single entry-point that resets all reliability-stack components (circuit breaker, ping-pong, command tracker) to their initial state. In Go the lifecycle is managed by `InterfaceClient.Start()` and `InterfaceClient.Stop()` (`internal/client/interface_client.go`), which call the per-component reset methods in a defined order as part of the connection lifecycle. There is no single `Initialize` method; the responsibility is distributed across the orchestration layer.

Go path: `internal/client/interface_client.go`.

---

### A4-D01 — CommandTracker max-size constants (2× larger)

| Constant | Python | Go |
|---|---|---|
| `COMMAND_TRACKER_MAX_SIZE` | 500 | 1000 |
| `COMMAND_TRACKER_WARNING_THRESHOLD` | 400 | 800 |
| `LAST_COMMAND_SEND_TRACKER_CLEANUP_THRESHOLD` | 100 | 500 |

Go's constants are deliberately larger because Go's `map` has no GC-driven eviction cost. Python's smaller defaults were chosen to keep asyncio heap pressure low under Python's single-threaded GIL — a constraint that does not apply to Go. On a 400-device fleet with rapid command issuance the larger window allows the tracker to maintain a meaningful history for anomaly detection without premature eviction.

Go path: `internal/client/reliability/command_tracker.go:38-54`.

---

### A4-D02 — PingPong cache MaxEntries (10× larger)

Python `PING_PONG_CACHE_MAX_SIZE = 100`. Go `PingPongConfig.MaxEntries` defaults to 1000.

Same rationale as A4-D01: Go's eviction strategy (20 % oldest-entries scan) is O(N) only at eviction time, not on every RecordPing/RecordPong call. At 1000 entries the larger window improves anomaly detection across large device fleets. Python's 100-entry cap reflects asyncio heap budget constraints; Go goroutines are not subject to the same constraint.

Go path: `internal/client/reliability/pingpong.go:124`.

---

### A4-D03 — Optimistic-Update-Timeout: 60 s vs. 30 s

Python `optimistic_update_timeout = 30 s`. Go's default `OptimisticTimeout` is 60 s (`internal/model/custom/state_change.go`).

The longer Go window accounts for the higher round-trip latency on large installations where the CCU may be several hops away and the XML-RPC callback server shares a busy HTTP listener. Optimistic state is visible in the UI for at most one additional CCU polling cycle (30 s max for most parameters) if the CCU-confirmed echo is delayed. If needed this can be tuned per-deployment via `cfg.Model.OptimisticTimeoutSeconds`.

Go path: `internal/model/custom/state_change.go`.

---

### A3-G5 — MetricHubSensor MQTT publishing — RESOLVED

Python `hub.py:388,510,554` wires `fetch_metrics_data`, `init_metrics`, and `publish_metrics_refreshed` so that SystemHealth, ConnectionLatency, and LastEventAge sensor values are published to MQTT topics.

OpenCCU-Loom's `MetricHubSensor` family (`internal/model/hub/metrics.go`) is fully implemented at the model level, and the MQTT surface is now wired: `wireOneCentral` (`internal/central/adapter/hub_mqtt_publisher.go`, `--- Metrics ---` block) publishes `BuildSystemHealthDiscovery` + `PublishHubSystemHealthScore` and subscribes `Metrics.OnUpdate(MetricSystemHealth, …)`; Connection Latency rides the per-interface `ConnectivityChangedEvent.LatencyMs` path (`PublishHubConnectionLatency`) in the `--- Connectivity ---` block. Coverage: `internal/central/adapter/hub_metric_sensors_test.go`. The REST surface (`GET /api/v1/hub/{central}/metrics`) remains in place alongside it.

**Remaining sub-item (original):** `MetricLastEventAgeSecs` MQTT Discovery/publish path was still separate — `hub_mqtt_publisher.go` published only `MetricSystemHealth` (tracked under `A3-BD-MetricLastEventAge`).

**Status (2026-06): implemented — `MetricLastEventAgeSecs` discovery and publish are wired in `internal/north/mqtt/`: `BuildLastEventAgeDiscovery` in `hub_discovery.go`, `PublishHubLastEventAge` in `bridge.go`, and the subscription in `hub_mqtt_publisher.go`.**

Go paths: `internal/model/hub/metrics.go`, `internal/central/adapter/hub_mqtt_publisher.go`.

---

### A3-G9 — Inbox MQTT publishing — RESOLVED

Python `hub.py:763` fires `SystemEventType.HUB_REFRESHED` when inbox data changes, which the MQTT adapter picks up to publish pending-device notifications.

OpenCCU-Loom now publishes the inbox: `wireOneCentral` (`internal/central/adapter/hub_mqtt_publisher.go`, `--- Inbox ---` block) emits `BuildInboxDiscovery` (`internal/north/mqtt/hub_discovery.go`), performs the initial-state publish when `hubModel.Inbox.Observed()`, and subscribes `hubModel.Inbox.OnUpdate` → `PublishInbox` (`internal/north/mqtt/bridge.go`). Pending devices in the CCU inbox are now signalled via MQTT, not just the SPA / REST API. Coverage: `internal/central/adapter/hub_mqtt_publisher_inbox_test.go`.

Go paths: `internal/model/hub/inbox.go`, `internal/central/adapter/hub_mqtt_publisher.go`.

---

### A5 — `HubCoordinator.SuppressServiceMessage` — suppress-only (no unsuppress via coordinator)

Python `HubCoordinator.suppress_service_message` (`aiohomematic/central/coordinators/hub.py:599-615`) accepts a `suppress: bool` argument allowing both suppress and unsuppress in one call.

Go `HubCoordinator.SuppressServiceMessage` (`internal/central/coordinators/hub.go:297`) always suppresses; there is no `suppress bool` parameter. Unsuppression is handled at the south-bound adapter level directly (`ServiceMessageSuppressor` interface). This is by design: the coordinator layer in Go is intentionally thin — it delegates the RPC call to the wired suppressor without encoding bidirectionality. Callers that need to unsuppress call the adapter directly, which keeps the coordinator free of dual-mode conditionals.

Go path: `internal/central/coordinators/hub.go::SuppressServiceMessage`.

---

### A5 — RPC-server background tasks: goroutine-per-connection vs. asyncio task pool

Python `rpc_server.py:445-458` limits outstanding background tasks via `MAX_RPC_BACKGROUND_TASKS` and logs a warning at the limit. Go's `net/http` server dispatches each request to a new goroutine; no explicit task cap is maintained.

The Go approach is by design: `net/http`'s goroutine-per-connection model provides natural concurrency isolation. A slow handler blocks only its own goroutine, not the event loop. If a future profiling run identifies slow-handler starvation under high CCU callback load, an explicit semaphore-based cap can be added to `internal/central/rpcserver/xmlrpc_server.go`. As of v0.1.0 the risk is low because CCU callback rates are bounded by the CCU's own throttle.

Go path: `internal/central/rpcserver/xmlrpc_server.go`.

---

### A5 — Feature flags: no per-CentralUnit feature-flag evaluation

Python evaluates certain feature flags (e.g. `enable_program_scan`, `enable_sysvar_scan`) per-central at boot. Go collapses this into the daemon config (`cfg.South.*`) which applies uniformly to all centrals.

The simplification is by design: OpenCCU-Loom's multi-CCU model (`ADR-0002`) uses a single daemon config shared across all centrals; per-central overrides are expressed as per-central config blocks in `config.yaml` (see `SPECIFICATION.md §4`). A per-`CentralUnit` feature-flag evaluation layer would duplicate the config model and is deferred to a post-0.1.0 milestone if operators request per-central scanning profiles.

Go path: `internal/central/central.go`, `internal/config/`.

---

### A5 — Status-subscription architecture: explicit subscribe vs. Python auto-decorator

Python marks coordinator methods with `@callback_event` which auto-registers them as status-change subscribers. Go has no decorator equivalent; coordinator status events are published on the internal bus and consumed by explicit `events.Subscribe` calls in `internal/central/adapter/`.

The explicit subscription pattern is by design (hexagonal architecture, SPEC §3). It makes the event-flow graph readable: every subscription appears at its wiring site, not hidden inside a decorator. `internal/central/coordinators/client.go::SubscribeToHealthEvents` is the canonical example.

Go path: `internal/central/coordinators/`, `internal/central/adapter/`.

---

### A5 — `HubCoordinator.HubStatePaths` linear scan vs. O(1) index

Python maintains `_state_path_to_name` as an O(1) index updated on every `add_*_data_point` call. Go `HubCoordinator.HubStatePaths()` iterates linearly over programs + sysvars on every call.

This is acceptable for the v0.1.0 fleet size (typically ≤ 300 programs + sysvars on a real CCU). A reverse-lookup API (`GetProgramDataPointByStatePath`) and an O(1) index are deferred to a performance milestone if profiling shows the linear scan as a hot path.

Go path: `internal/central/coordinators/hub.go::HubStatePaths`.

---

### A5 — `LinkCoordinator.GetLinksForLocale` — locale enrichment lives in the adapter layer

Python `link.py:168-261` enriches link labels with `get_channel_type_translation(locale=locale)` inside the coordinator. The Go port performs the equivalent channel-type-label localization one layer up, in the domain adapter: `LinksDomain.enrichLink` calls `channelTypeLabel(locale, channel)` → `translations.ChannelType(locale, …)` (`internal/central/adapter/links.go:136,141,286`), and the REST DTO `handlers.Link` exposes `sender_channel_type_label` / `receiver_channel_type_label` (`internal/north/rest/handlers/links.go:42,47`). The REST/WS response a caller receives is therefore already localized — this is an architectural split (raw links in the coordinator, localized DTO in the adapter), not a missing feature.

The coordinator-level `LinkCoordinator.GetLinksForLocale` (`internal/central/coordinators/link.go:180`) has no production caller: it only role-filters, and its `locale` parameter is currently unused. The parameter is retained so a future coordinator-level consumer needs no signature change.

Go path: `internal/central/coordinators/link.go::GetLinksForLocale`, `internal/central/adapter/links.go::enrichLink`.

---

### A5 — `Interfaces()` return type: sorted slice vs. Python frozenset

Python `ClientCoordinator.interfaces` returns an unordered `frozenset[Interface]`. Go `ClientCoordinator.Interfaces()` returns a `[]hmenum.Interface` sorted by enum integer value to guarantee deterministic output.

The sorted-slice choice is by design: Go has no frozenset, and a deterministic order is strictly better for CLI output, test assertions, and log readability. The sort order (enum integer) follows the declaration order in `pkg/hmenum/interface.go` which groups interfaces by protocol family.

Go path: `internal/central/coordinators/client.go::Interfaces`.

---

### A1-P2-1 — Per-interface connectivity sensor vs. aggregate Connectivity

Python exposes one `HmConnectionStateSensor` data point per CCU interface (one DP per interface ID). Go exposes a single `Connectivity` aggregate (`internal/model/hub/connectivity.go`) that tracks all interfaces in one struct. North-bound adapters that need per-interface entities call `Connectivity.List()` to iterate over the per-interface reachability map.

The aggregate model is by design: it reduces the number of hub-level objects the coordinator must lifecycle-manage and enables atomic multi-interface state snapshots. Per-interface MQTT topics are published via `MQTTTopicsForInterface` on a single `Connectivity` instance, matching the Python per-DP topic shape without one Go struct per interface.

Go path: `internal/model/hub/connectivity.go`.

---

### A1-P2-2 — No explicit `cleanup_subscriptions`

Python `DataPoint.cleanup_subscriptions` iterates `_subscribers` and clears the set. Go's event subscription model uses a closure returned from `OnUpdate`/`OnConfirmedUpdate` calls — the caller holds an unsubscribe function and calls it on teardown (`defer unsub()`). There is no equivalent bulk-clear method because Go's GC reclaims unused subscriber slots; the `device.NotifyRemoved` path in `internal/model/device/channel.go` additionally unregisters wire-side DPs on device removal.

The unsubscribe-closure pattern is by design (Go idiom: consumers own their subscription lifetime). Bulk cleanup is unnecessary because the subscriber list entries are individually nilled by each closure, and the event-bus compacts nilled entries on the next notification cycle.

Go path: `internal/model/device/channel.go`, `internal/model/generic/datapoint.go`.

---

### A1-P2-15 — InstallMode backend sync loop: asyncio.Task vs. scheduler job

Python implements the InstallMode sync loop as a long-running `asyncio.Task` that sleeps in a loop and calls `_update_install_mode()` on each tick. Go implements it as a periodic scheduler job registered via `internal/scheduler/`. The Go job is externally managed (start/stop follows the daemon lifecycle) rather than being a self-contained coroutine.

The scheduler-job approach is by design: it integrates InstallMode polling into the same job lifecycle that governs all other periodic coordinator tasks (program scan, sysvar scan, firmware check). An asyncio-style self-contained loop would fight Go's goroutine lifecycle management and make graceful shutdown harder to reason about.

Go path: `internal/scheduler/`, `internal/central/coordinators/hub.go`.

---

### A2-10 — `UnconfirmedLastValuesSend` is a count, not a per-channel mapping

Python `BaseDataPoint.unconfirmed_last_values_send` is a `dict[str, Any]` mapping channel addresses to their last-sent unconfirmed value. Go `BaseDP.UnconfirmedLastValuesSend()` returns an `int` counting the number of unconfirmed sends.

The count-only approach is by design for v0.1.0: the primary consumer of the count is the REST API `GET .../pending` which only needs the count to determine whether an optimistic badge should be shown. The address-keyed map is deferred until a use case (multi-channel optimistic rollback, differential re-send on reconnect) requires it.

Go path: `internal/model/custom/mixins.go::BaseDP`.

---

### A2-11 — No `_old_manu_setpoint` tracking in Climate

Python `Climate._manu_temp_changed` stores the previous manual setpoint in `_old_manu_setpoint` and restores it when the mode transitions away from manual. Go's `Climate` has no such field — manual setpoint changes and mode transitions are sent as separate wire writes without restore-on-mode-change logic.

The omission is by design for v0.1.0: the Python restore logic is rarely triggered in practice (it fires when a user switches from manual mode back to auto, which most HA automations never do) and its interaction with the active week-program schedule is non-trivial to reason about. The Go model instead lets the week-program schedule restore its own temperature on the next slot boundary.

Go path: `internal/model/custom/climate/climate.go`.

---

### A2-15 — `GroupState` tracks local membership, not the wire `GROUP_STATE` parameter

Python `Switch.group_state` reads the wire `GROUP_STATE` parameter to derive membership. Go `GroupState` (`internal/model/custom/mixins.go`) is a local map of member addresses populated from the channel's link profile; it does not read the wire `GROUP_STATE` parameter.

The local-map approach is by design: the wire `GROUP_STATE` is a CCU-internal bitmask encoding that changes across firmware versions. The Go model derives group membership from the explicit link-profile associations loaded at startup (the same source the REST API uses for the group-state endpoint), which is more stable and does not require an extra wire read on every state change.

Go path: `internal/model/custom/mixins.go::GroupState`.

---

### A2-16 — Blind `relevant_data_points` override not needed in Go

Python `Blind.relevant_data_points` overrides the base class to exclude `LEVEL_2` from the set of DPs that trigger state-change callbacks. Go does not have an equivalent override because the Go event-subscription model gives each DP its own subscription; `LEVEL_2` subscriptions simply are not registered for Blind, so it does not participate in state aggregation.

This is by design: Go's explicit subscription model makes the "exclude LEVEL_2" logic unnecessary — what is not subscribed cannot fire. Python's `relevant_data_points` is a guard against base-class over-subscription; Go achieves the same result structurally.

Go path: `internal/model/custom/cover/blind.go`.

---

### A2-20 — EffectLight.Subscribe cleanup: Go closures vs. Python explicit cleanup

Python `EffectLight.subscribe` returns cleanup functions from each inner DP subscription and stores them for later explicit teardown. Go's `EffectLight.Subscribe` returns a single unsubscribe closure that calls all stored per-DP unsubscribe functions. The caller holds the single closure and calls it to tear down all subscriptions atomically.

The single-closure model is by design (Go pattern, mirrors other custom DP Subscribe implementations in this codebase). Subscription cleanup is still explicit — it just happens through the returned closure rather than through Python's stored list of cleanup functions.

Go path: `internal/model/custom/light/effect_light.go::Subscribe`.

---

### A2-21 — `has_data_point_key` not needed: `DataPointKey()` available on every DP

Python `BaseDataPoint.has_data_point_key` is a boolean predicate used to guard calls to `DataPointKey()` when the DP might not have a key. Go's `DataPointKey()` is defined on all DP types and returns a zero-value `DataPointKey{}` when the key is unset; callers that need to distinguish the "no key" case check `key.IsZero()` or compare against the zero value directly.

The `IsZero` approach is by design: it is consistent with Go's convention of returning zero values for absent fields, and avoids a separate boolean predicate.

Go path: `pkg/hmtypes/datapoint_key.go::DataPointKey.IsZero`.

---

### A2-22 — `channel_group_addresses` not needed: link-profile addresses available via `GroupState`

Python `BaseDataPoint.channel_group_addresses` returns all addresses participating in the same group. Go does not expose an equivalent field on `BaseDP`; callers that need group addresses call `Switch.GroupState().Members()` which returns the link-profile-derived member set.

The `GroupState.Members()` API is the canonical Go surface for group-address enumeration. `channel_group_addresses` was a convenience accessor on the Python DP; in Go the information is owned by `GroupState` (separation of concerns).

Go path: `internal/model/custom/mixins.go::GroupState.Members`.

---

### A2-24 — `state_uncertain` does not propagate into `IsStateChange`

Python's `DataPoint.is_state_change` checks `state_uncertain` and returns `True` (treat as changed) when the state is uncertain. Go's `IsStateChange` implementations check whether a value has been observed (`ok == false`) and return `true` when the state is unknown — which is semantically equivalent. The implementation diverges only in naming: Go checks the observed flag on the embedded generic DP rather than calling a `StateUncertain()` method.

The two approaches are functionally equivalent: both force a write when the state is unobserved. Go does not need a separate `StateUncertain()` propagation path because the observation flag is always checked inline in each `IsStateChange` implementation.

Go path: `internal/model/custom/state_change.go`, individual `IsStateChange` methods.

---

### A3-G2 — InstallMode per-interface data point: architectural TODO

Python exposes one `HmInstallModeSensor` per interface on the hub. Go's `InstallMode` (`internal/model/hub/install_mode.go`) was originally a single aggregate tracking install mode state without per-interface granularity.

This was a tracked TODO for a post-0.1.0 milestone. Per-interface install mode required the `Connectivity`-style per-interface map pattern applied to `InstallMode`. The REST API originally exposed install-mode state as a single hub-level resource; per-interface splitting required a REST API revision.

**Status (2026-06): implemented — `GET /install-mode/interfaces` and `POST /install-mode/interfaces` are routed at `internal/north/rest/router.go:816-817`, with handlers in `internal/north/rest/handlers/system_hub.go`.**

Go path: `internal/model/hub/install_mode.go`, `internal/north/rest/handlers/system_hub.go`.

---

### A3-G11 — `check_against_pd` validation: CONFIG_PENDING push handles this in Go

Python `check_against_pd` runs a paramset-descriptor validation pass on incoming values before storing them. Go does not have an equivalent validation call in the hot path; incoming values are stored as-received from the CCU and validated lazily when the REST API or north-bound adapter applies a user-supplied patch.

The deferred-validation approach is by design: the CCU is the authoritative source; values pushed via `newValue` callbacks have already passed the CCU's internal validation. For user-originated writes the REST layer applies `parameter.Validate` before forwarding to the CCU, which is the correct place for descriptor-based validation. Inline validation of CCU-pushed values adds latency without benefit.

Go path: `internal/parameter/validate.go`, `internal/central/callback_handlers.go`.

---

### A3-G12 — `Connectivity.Available()` returns false for empty tracker: correct

Python `HmConnectionStateSensor.available` (implicitly derived) returns `True` once any state is set. Go `Connectivity.Available()` returns `false` until `OnState` has been called at least once.

Returning `false` for an empty tracker is correct: an unseen connectivity state is genuinely unknown, not "available". The behaviour is consistent with all other `HubDataPointer` implementations which return `Available()==false` before first observation.

Go path: `internal/model/hub/connectivity.go::Available`.

---

### A4-M02 — Throttle `MaxQueueDepth`: Go uses 4× factor, Python has no cap

Python's `CommandThrottle` has no configurable queue depth cap; the asyncio heap is unbounded by default. Go adds a `MaxQueueDepth` field (recommended value: 4× `MaxInFlight`) as documented in the field comment.

The addition is by design (SPECIFICATION §8.4 backpressure requirement): without a cap, a stalled CCU fills the heap until OOM. The 4× factor is a conservative default that prevents runaway growth while still allowing reasonable burst absorption. Python's no-cap default was acceptable under asyncio's GIL-constrained single-thread execution model, where the heap growth rate is bounded by the event-loop tick rate.

Go path: `internal/client/reliability/throttle.go::ThrottleConfig.MaxQueueDepth`.

---

### A4-M05 — Ping callerID: Go uses InterfaceID only, not a per-DP attribution

Python `PingPong.ping` receives a `callerID` string that can be any attribution token (e.g. a data-point address). Go `PingPong.RecordPing` records latency keyed on the `interfaceID` string; there is no per-DP attribution.

The interface-level attribution is by design: Go's PingPong aggregate is a per-interface health monitor, not a per-DP tracker. Per-DP attribution would require a map-per-DP which is O(#DPs) in size; interface-level attribution is O(#interfaces) and directly answers the question "is interface X healthy?".

Go path: `internal/client/reliability/pingpong.go`.

---

### A4-M08 — `AllCircuitBreakersClosed`: Go has one CB per InterfaceClient

Python checks a list of CBs (`_all_circuit_breakers_closed`). Go `InterfaceClient.AllCircuitBreakersClosed()` checks exactly one `cfg.Circuit` CB because each `InterfaceClient` is bound to one interface and therefore has exactly one circuit breaker.

The single-CB model is by design: Go's `InterfaceClient` maps 1:1 to a `(central, interface)` pair (SPECIFICATION §5). Python's multi-CB list exists because the Python client was originally designed as a multi-interface aggregate; Go split that surface into per-interface clients at the architecture layer (ADR-0002).

Go path: `internal/client/interface_client.go::AllCircuitBreakersClosed`.

---

### A4-N04 — Login backoff: Go max 3 attempts vs. Python max 10

Python `jsonrpc.client.py` retries login with up to 10 attempts using exponential backoff (max 60 s). Go `internal/client/transport/jsonrpc/client.go` uses a max of 3 attempts (`loginMaxFailedAttempts = 3`) with 1 s base and 2× multiplier (max ~4 s).

The lower attempt cap is by design: the Go daemon is a long-running service with a reconnector loop. When the CCU is unreachable, the reconnector restarts the entire `init` sequence (including login) on the next cycle; persisting login retries inside a single `init` call for up to 60 s would hold the reconnect lock and delay other interfaces. Three fast attempts detect transient failures; the reconnector handles persistent failures with its own backoff.

Go path: `internal/client/transport/jsonrpc/client.go::loginMaxFailedAttempts`.

---

### A4-N06 — ClientStateMachine: Go allows additional transitions

Python `ClientStateMachine` enforces a strict transition graph (e.g. `Stopped → Running` only). Go's state machine (`internal/client/state_machine.go`) allows additional transitions (e.g. `Stopped → Created`, `Failed → Stopped`) to support the reconnect-loop lifecycle where the daemon may restart a previously stopped client without allocating a new state machine.

The additional transitions are by design: Go's reconnector pattern requires `Stopped → Created/Initializing` to restart the auth and init sequence without recreating the entire client. Python's strict graph was designed for a single-shot connection; Go's broader graph enables the multi-attempt reconnect pattern required by the daemon's reliability layer.

Go path: `internal/client/state_machine.go`.

---

### A4-J01 — `IsMethodSupported` gating: checked at registration time, not hot path

Python gates every XML-RPC method invocation with `is_method_supported(method)` before the call. Go checks method support at `InterfaceClient` construction time (when the backend is registered) and does not re-check per call in the hot path.

The registration-time check is by design: the set of supported methods for a given CCU backend does not change at runtime. Pre-checking at registration avoids a map lookup on every command dispatch. The `interfaces.MethodChecker` interface is available for callers that need runtime capability queries (e.g. the firmware-update path).

Go path: `internal/client/interface_client.go`, `pkg/interfaces/method_checker.go`.

---

### A4-P01 — CONVERTABLE_PARAMETERS auto-routing — RESOLVED (routed at the call-site)

Python `CommandTracker.add_set_value` (command.py:131–134) automatically routes `COMBINED_PARAMETER` / `LEVEL_COMBINED` to `add_combined_parameter` based on parameter membership in `CONVERTABLE_PARAMETERS`.

The original entry claimed Go "always calls `AddSetValue` without auto-routing" — that is **no longer true** (and was already stale for the optimistic path). The decided design — route at the call-site rather than re-deriving the type inside the tracker — is implemented and wired:

`InterfaceClient.WriteUnconfirmedValue` (`internal/client/interface_client_orchestration.go`) checks `parameter.IsConvertable(parameter)` and, for a string value, calls `CommandTracker().AddCombinedParameter(channelAddress, parameter, s)` (which decomposes the combined wire string into its constituent sub-parameters under `ParamsetKeyValues`); otherwise it falls back to `AddSetValue`. The production north-bound write path reaches it via the daemon optimistic hook `valueWriter.SetCommandTrackerFn(...)` (`cmd/openccu-loom/daemon_wiring.go`), whose closure resolves the `InterfaceClient` via the central registry and calls `WriteUnconfirmedValue`. So a subsequent north-bound read on a constituent DP (e.g. `LEVEL`) returns the optimistic value rather than the opaque combined shorthand.

Keeping the routing at the call-site (rather than inside the tracker) avoids injecting a parameter-lookup function into the tracker and the allocator pressure that would add on every `SetValue`.

**Status (2026-06): RESOLVED.** Behaviour pinned by a regression test on `WriteUnconfirmedValue` and a guard test that the two `ConvertableParameters` sets (`internal/parameter` used at the call-site, `internal/model/value` the model mirror) stay in agreement.

Go path: `internal/client/interface_client_orchestration.go::WriteUnconfirmedValue`, `internal/client/reliability/command_tracker.go`, `internal/client/value_writer.go::SetCommandTrackerFn`, `internal/parameter/converter.go::IsConvertable`, `internal/model/value/converter.go::IsConvertableParameter`.

---

### A4-P02 — Paramset description coalescer: single shared coalescer vs. two dedicated coalescers

Python `InterfaceClient.__init__` creates two separate `RequestCoalescer` instances: `_device_description_coalescer` and `_paramset_description_coalescer` (interface_client.py:156–165). Go uses one shared coalescer; `FetchParamsetDescriptions` calls the backend directly without routing through the coalescer.

The single-coalescer approach is by design: Go's `InterfaceClient` is already bounded to a single `(central, interface)` pair, and paramset fetches are batched at the coordinator level before the client sees them. Adding a second coalescer would halve the coalescing benefit while adding memory pressure. In practice, concurrent paramset fetches are rare on the Go path because the device-creation pipeline batches all paramset fetches before returning. If concurrent fetch storms are observed in production this can be revisited.

Go path: `internal/client/reliability/coalesce.go`, `internal/client/interface_client_orchestration.go`.

---

### A4-P03 — `OnSystemStatusRestored`: explicit call required, no bus subscription

Python `InterfaceClient.__init__` subscribes to `SystemStatusChangedEvent` and reacts to connection-restored transitions by calling `_ping_pong_tracker.clear()` (interface_client.py:181–185). Go exposes `OnSystemStatusRestored` as a method that must be called explicitly by the coordinator; there is no internal bus subscription.

The explicit-call pattern is by design: Go's central coordinator already owns the system-status state machine and is the natural place to call `OnSystemStatusRestored`. Subscribing the client to the bus internally would create a second, harder-to-trace control path. The coordinator's `ConnectionRecoveryCoordinator` handles the restoration sequence and can call `OnSystemStatusRestored` at the right moment.

Go path: `internal/client/interface_client.go::OnSystemStatusRestored`.

---

### A4-P04 — WITHDRAWN: the "6 dead ReGa scripts" divergence never existed

This entry used to claim that six scripts (`get_alarm_messages`,
`get_backend_info`, `get_program_descriptions`, `get_serial`,
`get_service_messages`, `get_system_variable_descriptions`) had no production
callers because Go preferred the JSON-RPC methods `Alarm.getAll`,
`System.getSystemInformation` and `Interface.getServiceMessages`.

Every load-bearing claim in it was wrong, and it is retained here only so the
correction is discoverable from the ID:

- **None of those three JSON-RPC methods exists.** The CCU's authoritative
  method catalogue is `WebUI/www/api/methods.conf` in the firmware; it lists
  183 methods and none of these. There is no CCU JSON-RPC endpoint for alarm
  or service messages at all.
- **All six scripts have production callers**, resolved through
  `internal/central/adapter/hub_wiring.go`. `get_serial` is boot-blocking: a
  central does not come up without it.
- The described fallback ("the CCU falls back to them when the JSON-RPC method
  is not available") is therefore not a mechanism that exists anywhere.

Read this as a caution rather than a divergence: an entry in this catalogue is
only as good as the source it was checked against. For the CCU's JSON-RPC
surface that source is `methods.conf` plus the `.tcl` implementations beside
it; for ReGa method and constant names it is the `ReGaHss` binary's own symbol
table.

Go path: `pkg/hmenum/rega_script.go`, `internal/client/rega/`,
`internal/central/adapter/hub_wiring.go`.

---

### A5-P01 — `DeviceCoordinator.HandleNewDevices` vs `_add_new_devices` 218-LOC pipeline

Python `_add_new_devices` (device.py:862) runs a full pipeline: semaphore guard, `delay_new_device_creation` config gate, paramset fetch, `Cache.SaveAll`, verify-and-heal, `create_devices`, consistency check. Go's `HandleNewDevices` (device.go:249) registers descriptions and emits an event; the paramset fetch, device creation, and consistency check are driven by the adapter wiring layer (`ccu_wiring.go`) that coordinates the full sequence.

The placement is by design: Go's hexagonal architecture places orchestration sequences in the adapter layer, not in the coordinator. The coordinator is a pure registry; multi-step sequences with cross-cutting concerns (cache, client, event bus) live in the adapter. This mirrors the same split used for `start_clients` (M5050), `create_central_links` (M4237), and `fetch_*_data` (M3101).

Go path: `internal/central/coordinators/device.go::HandleNewDevices`, `internal/central/adapter/`.

---

### A5-P02 — PONG token parsing: Go PingPong tracker receives raw value string, not split token

Python `EventCoordinator.data_point_event` (event.py:181) splits the value on `"#"` to extract the PONG token, then passes the token to `ping_pong_tracker.handle_received_pong(token)`. Go's `HandleRawEventNormalized` routes `PONG` events to the tracker without splitting the value; the token is the raw value string.

The approach is sufficient for the health signal: Go's PingPong tracker records RTT keyed on interface ID, not per-token. Token-splitting would only be needed for per-ping RTT metrics. Per-token matching requires a map lookup and string allocation on every PONG event. The overhead is negligible at 15-second ping intervals, but the complexity is not justified until per-interface RTT Prometheus metrics are wired to a consumer.

Go path: `internal/central/coordinators/event.go::HandleRawEventNormalized`, `internal/client/reliability/pingpong.go`.

---

### A5-P03 — `DataPointsCreatedEvent` not emitted: per-device lifecycle events sufficient

Python `EventCoordinator._emit_devices_created_events` (event.py:459) emits both `DeviceLifecycleEvent(CREATED)` per device and a separate `DataPointsCreatedEvent` aggregating all new data-point keys. Go's `EmitDevicesCreatedEvents` (event.go:379) emits one `DeviceCreatedEvent` per device address without a follow-up `DataPointsCreatedEvent`.

The separate `DataPointsCreatedEvent` is deferred by design: Go's north-bound discovery adapters subscribe to `DeviceCreatedEvent` and pull the DP catalogue from the central registry after the event fires. A second event carrying the same data would add no value for current consumers. It can be added when a streaming-DP-discovery surface requires the aggregate without a registry lookup.

Go path: `internal/central/coordinators/event.go::EmitDevicesCreatedEvents`.

---

### A5-P04 — `HubCoordinator.InitHub` does not trigger initial fetch sequence

Python `hub.py:493` runs an inline fetch chain (programs, sysvars, inbox, service messages, alarm messages, install mode, metrics, connectivity). Go's `InitHub` (hub.go:743) calls only `Clear()`.

The placement is by design: initial data hydration is triggered by the adapter wiring layer after `InitHub` returns, via the `Refresh*` hook callbacks registered on `SetRefreshHooks`. This keeps the coordinator as a pure reset operation and allows the adapter to control the hydration sequence (parallelise fetches, respect the `devices_created` gate). Mirrors the same pattern used for `fetch_*_data` (M3101/M3104).

Go path: `internal/central/coordinators/hub.go::InitHub`.

---

### A5-P05 — `HubCoordinator.ConnectivityDPs` connectivity DP factory

Python `hub.py:141` exposes a `ConnectivityDPs` property returning custom data points for CCU-side metrics. The original divergence: Go's `ConnectivityDPs()` returned nil because the per-interface connectivity sensor model was not yet implemented.

**Status (2026-06): implemented — `HubCoordinator.ConnectivityDPs` (`internal/central/coordinators/hub.go::ConnectivityDPs`) now returns `hubModel.ConnectivityDataPoints()`, the per-interface connectivity aggregate registered via `hub.Hub.SetConnectivity`; it returns nil only when no hub model is wired or no connectivity has been registered.** Coverage: `internal/central/coordinators/hub_connectivity_suppress_test.go::TestConnectivityDPsReturnsWiredConnectivity`.

Go path: `internal/central/coordinators/hub.go::ConnectivityDPs`.

---

### A6-P01 — UC5/UC6 easymode output wired in the live REST adapter

Python `_enrich_easymode` (form_schema.py:472–528) runs UC5 (preset chips) and UC6 (subset group membership per parameter) as part of the `FormSchemaGenerator` pipeline. The original divergence: the live REST adapter `UISchemaAdapter.UISchema` did not emit UC5/UC6 output, deferred until the SPA-side type contract (`UISchemaParameter.presets` / `UISchemaParameter.subset_group_id` in `assets/ui/src/lib/api/types.ts`) was matched with rendering tests.

**Status (2026-06): implemented — both use-cases are applied inline in the live adapter.** `UISchemaAdapter.UISchema` (`internal/central/adapter/uischema_adapter.go:479-496`) sets `entry.Preset` / `entry.Presets` and propagates the `AllowCustomValue` flag from the archive preset (UC5), and attaches `entry.SubsetGroupID` from `meta.SubsetGroupIDs` (UC6). The output is the same data the standalone `uc5`/`uc6` packages produce; the adapter applies the logic inline rather than routing through the `easymode.Pipeline` type (see A6 / BD-A6-Pipeline for why the Pipeline abstraction itself stays unwired).

Go path: `internal/central/adapter/uischema_adapter.go::UISchema`.

Go path: `internal/central/adapter/uischema_adapter.go`, `internal/configui/easymode/uc5/`, `internal/configui/easymode/uc6/`.

---

### A1-P2-BD01 — `write_value` return tuple: Go uses callbacks instead

Python `BaseParameterDataPoint.write_value(value, write_at)` returns `(old, new)` so callers (e.g. `DeviceErrorEvent.event`) can decide on the old vs. new pair. Go `DataPoint[T].OnEvent(v T)` fires the update synchronously and delivers `(old, next)` via the `OnUpdate(fn func(old, next T))` callback pattern.

The callback pattern is by design: Go's type-safe `OnUpdate` closures are more composable than a return-tuple in an async context. `DeviceErrorEvent`'s equivalent in Go (`event/event.go::deviceErrorActive`) reads the previous value from its own `lastValue` field, which is populated in the same lock-protected `FireAt` path — functionally equivalent without requiring the caller to thread the return value through. Custom DP composers use `OnUpdate` hooks rather than inspecting the write-return.

Go path: `internal/model/generic/datapoint.go::OnEvent`, `internal/model/event/event.go::deviceErrorActive`.

---

### A1-P2-BD02 — `Group.UniqueID` token uses full Kind string, not short suffix

Python `ChannelEventGroup.unique_id` uses `event_group_{kind.short}_{channel.unique_id}` where `kind.short` strips the `homematic.` prefix (`"keypress"`, `"impulse"`, `"device_error"`). Go's `NewGroupWithCentral` produces `<central>:<channelAddress>:event_group/<full-kind-string>`, e.g. `ccu1:A:1:event_group/homematic.keypress`.

The format difference is by design for a pure-Loom deployment: the Go UniqueID follows the uniform `<central>:<address>:<keyName>` pattern used by every other DP family (see `datapoint/base.go::UniqueID`). The `homematic.` prefix is preserved as part of the Kind token so the string remains self-describing without a lookup table. Users migrating from a parallel HA+aiohomematic setup to a pure-Loom setup will see different entity unique-IDs for event groups; this is acceptable because the two stacks cannot coexist on the same CCU without registry conflicts anyway.

Go path: `internal/model/event/group.go::NewGroupWithCentral`, `internal/model/datapoint/base.go::UniqueID`.

---

### A1-P2-BD03 — `Channel.IsInMultiGroup` is not cached

Python `Channel.is_in_multi_group` is a `hm_property(cached=True)` — computed once and stored. Go `Channel.IsInMultiGroup()` recomputes on every call (reads `groupNumber` under a read-lock, then checks `device.IsInMultiChannelGroup`).

The no-cache approach is by design: `groupNumber` can change after initial construction when the coordinator detects multi-channel group assignments during a late-arriving paramset. Caching would require invalidation logic. The computation is O(1) (a single integer comparison), so the performance impact is negligible. If profiling ever shows it as a hot path, a `sync.Once`-protected cache can be added without API changes.

Go path: `internal/model/device/channel.go::IsInMultiGroup`.

---

### A1-P2-BD04 — `get_channel_group_addresses` not exposed on CustomDataPoint

Python `CustomDataPoint.get_channel_group_addresses` returns the set of channel addresses covered by the channel group (primary + secondary + state + fixed channels). Go does not expose this as a method on the custom DP types; callers that need the address set can iterate `Device.GroupChannels(groupNo)` or read the profile schema directly.

The omission is by design: the channel-group address set is a structural property of the device/profile graph, not a cached data-point field. Exposing it on the DP would duplicate the profile schema's channel-list and require keeping them in sync. North-bound adapters that need the set (e.g. for MQTT multi-channel discovery) query `Device.GroupChannels` directly.

Go path: `internal/model/device/channel_group.go`, `internal/model/custom/profile_schema.go`.

---

### A3-BD01 — Combined Timer `Default()` returns `any`, not typed float/nil

Python `CombinedDpTimerAction.default` delegates to `value_dp.default` and returns `float | None`. Go `Timer.Default()` returns `any` (a `float64` when set, `nil` when unset).

The `any` return is by design: Go's `CombinedDataPoint` interface uses `any` for `Default()` to allow all combined-DP types to share the same interface signature without generics overhead at the interface boundary. Callers that need the typed value use a type switch or the `Timer`-specific API surface.

Go path: `internal/model/combined/timer.go`.

---

### A3-BD02 — Combined writes are not batched via a `CallParameterCollector`

Python `CombinedDpTimerAction.send_value(*, value, collector=None)` and similar combined-DP write methods accept an optional `collector` to batch multiple writes into a single `put_paramset` call. Go `Timer.SetDuration`, `HSColor.Set`, `LevelCombined.SetLevel` execute writes directly via the injected `Writer` without batch support.

The direct-write approach is by design: Go's reliability layer (circuit breaker + throttle + coalescer in `internal/client/`) already coalesces rapid sequential writes to the same channel at the transport layer. Adding a `WriterBatch` abstraction on top of that would duplicate the coalescing logic without meaningful benefit. The REST and WS write paths that need transactional multi-DP writes should use the `Collector` pattern in `internal/model/generic/collector.go`.

Go path: `internal/model/combined/timer.go`, `internal/model/generic/collector.go`.

---

### A3-BD03 — `HubCoordinator.RefreshConnectivity` / `RefreshMetrics` not present

Python `Hub.fetch_connectivity_data` and `Hub.fetch_metrics_data` are called periodically to sync the connectivity and metrics sensors. Go's `HubCoordinator` exposes 7 specific refresh hooks but not Connectivity and Metrics equivalents — those are driven by the `Reconciler`'s `reconcileConnectivity` / `reconcileSystemHealth` jobs instead.

The Reconciler path is by design: it consolidates all health-related pulls into a single scheduled job (`central.reconcile`, default 5 min) with configurable cadence. Adding per-function refresh hooks on the Hub coordinator would create a second scheduling surface that diverges from the Reconciler's backoff and error-handling. The Reconciler's probe-function pattern is more testable and easier to configure than per-aggregate refresh jobs.

**Wiring status (corrected 2026-05-31 re-audit).** The `reconcileConnectivity`
pass needs **both** `Reconciler.Connectivity` (the cache target) **and**
`Reconciler.Connect` (the `Interface.listInterfaces` probe). The hub wiring
seeded the target but originally left the probe nil, so the pass
short-circuited and the connectivity sync described above never actually
fired. The probe is now wired in `WireHub`
(`internal/central/adapter/hub_wiring.go`, alongside `SetConnectivity`) so the
connectivity reconcile runs as documented. The `reconcileSystemHealth` and
`reconcileUnobservedDataPoints` slots (`Reconciler.Health`,
`Reconciler.Unobserved`) remain **intentionally optional and nil by default**:
system-health is a *derived* score (computed from interface/connectivity
state, not a single CCU read), and the unobserved-DP sweep needs a load-safe
whitelist design before it polls the live radio on a schedule. Both are
documented nil-tolerant extension points (`reconciler.go` short-circuits when
nil), not the implemented-but-unwired capability class — they are unbuilt, not
dormant.

Go path: `internal/central/coordinators/reconciler.go`, `internal/central/coordinators/hub.go`, `internal/central/adapter/hub_wiring.go`.

---

### A3-BD04 — Per-Interface connectivity DPs not modelled as individual DataPoint instances

Python models one `HmInterfaceConnectivitySensor` per interface — each is an independent DP instance with its own `unique_id`, `available`, and `state_uncertain`. Go uses a single `hub.Connectivity` aggregate with a `Reachable(interfaceID)` lookup; north-bound adapters synthesise per-interface virtual DPs for HA discovery.

The aggregate approach is by design: a single aggregate is easier to lock correctly in a multi-CCU context and avoids allocating N independent DP instances at startup before the interface list is known. The HA discovery builder (`internal/north/mqtt/hub_discovery.go::BuildConnectivityDiscovery`) already synthesises the per-interface discovery payloads from the aggregate, so the HA user experience is equivalent. The aggregate approach also makes it simpler to compute `AllReachable()` without iterating a dynamic DP map.

Go path: `internal/model/hub/connectivity.go`, `internal/north/mqtt/hub_discovery.go`.

---

### A3-BD05 — `ChannelSwitch.Value` reads from in-memory scheduleEnabled map

Python `ChannelSwitch` reads `int(self._dp_channel_locks.value)` and parses the bitmask at read-time. Go `ChannelSwitch.Value()` reads from the in-memory `ProfileDataPoint.scheduleEnabled` map populated by `SyncScheduleEnabled`.

The in-memory map is by design: Go's approach is single-source-of-truth-oriented — the parsed bitmask is materialised once into a typed map on every `SyncScheduleEnabled` call, avoiding repeated bitmask parsing on every read. The trade-off is that the in-memory state lags the wire DP value by one `SyncScheduleEnabled` cycle; this is acceptable because `SetScheduleEnabled` always triggers a re-sync.

Go path: `internal/model/weekprofile/channel_switch.go`, `internal/model/weekprofile/datapoint.go::SyncScheduleEnabled`.

---

### A3-BD06 — Sysvar LIST index resolved to label before MQTT publish

Python sends the raw integer index for LIST-type sysvars and declares a `value_template` in HA discovery that maps the index to a label. Go `hub_mqtt_publisher.go::sysvarStateForMQTT` resolves the LIST index to its string label before publishing to MQTT.

The server-side resolution is by design: resolving in the publisher means the retained MQTT message is human-readable without an HA template, which simplifies non-HA consumers (dashboards, automations, Node-RED). The round-trip (HA sends label → Loom maps back to index for the CCU write) is handled correctly in the command handler. The risk of a stale value-list breaking the round-trip is mitigated by the fact that value-list changes require a firmware update, after which the daemon restarts and re-fetches the descriptor.

Go path: `internal/central/adapter/hub_mqtt_publisher.go::sysvarStateForMQTT`.

---

### A3-BD07 — MetricConnectionLatMs uses implicit ping-pong-only latency, no explicit pattern filter

Python (`aiohomematic/model/hub/metrics.py:202`) calls `get_aggregated_latency(pattern="ping_pong")` when computing the `HmConnectionLatencySensor` value, explicitly filtering to ping-pong-sourced samples.

Go's `Metrics.Observe(MetricConnectionLatMs, …)` is only ever called from the reconciler and the health-wiring paths, which already aggregate exclusively from `internal/metrics/aggregator.go::AggregatedLatency("ping_pong.rtt")` (see `aggregator.go:250`). No non-ping-pong latency samples can reach `MetricConnectionLatMs` because all callers filter at the call site. The `Metrics` struct is therefore implicitly ping-pong-only; an explicit pattern field would duplicate the filtering logic already present at the aggregation layer.

Go path: `internal/metrics/aggregator.go::AggregatedLatency`, `internal/central/coordinators/reconciler.go::reconcileSystemHealth`.

---

### A3-BD08 — SetScheduleEnabled does not perform a post-write CCU re-read

Python (`week_profile_data_point.py:353-390`) calls `load_data_point_value` after a successful `SetScheduleEnabled` write to re-read the CCU's actual bitmask and confirm the write.

Go's `SetScheduleEnabled` sets a 3-second write-hold window (`writeHoldUntil = now + 3s`) after the wire write. The CCU typically echoes the new bitmask via a `WEEK_PROGRAM_CHANNEL_LOCKS` push event within ~1 s; `SyncScheduleEnabled` receives the event and updates the in-memory state. This push-driven confirmation replaces the synchronous re-read: in a push-callback architecture the CCU is authoritative on the state, so a post-write poll would duplicate the already-incoming push. The write-hold window guards against stale pre-write echoes that arrive between the write and the confirming push.

The practical difference is that Python's re-read is a forced synchronous confirmation; Go waits for the asynchronous push. Both result in eventual consistency with the CCU state; Go's path adds no extra radio load.

Go path: `internal/model/weekprofile/datapoint.go::SetScheduleEnabled`.

---

### A4-BD-HG-PING — HomegearBackend.check_connection uses `ping` instead of `clientServerInitialized`

**Python reference:** `aiohomematic/client/backends/homegear.py:94` — `clientServerInitialized(interface_id)`.

**Go path:** `internal/client/backends/homegear.go:87` — calls `Ping`.

**Rationale:** `clientServerInitialized` is a Homegear-specific method that checks whether a given registered client (callback URL) is still tracked by the server. `ping` is the CCU-compatible method. Go's `HomegearBackend` reuses the CCU-compatible ping path because Homegear's XML-RPC layer accepts `ping` without error in all tested firmware versions (Homegear 0.6+ advertises `ping` in `system.listMethods`). Switching to `clientServerInitialized` would require Homegear-specific method dispatching in the connection-check path; the observable effect of this divergence (a slightly less informative liveness check) is acceptable for the 0.1.0 scope.

---

### A4-BD-HG-LINK — HomegearBackend implements link operations despite Python returning ErrNotImplemented

**Python reference:** `aiohomematic/client/backends/homegear.py` — `HomegearBackend` inherits `BaseBackend.get_links/add_link/remove_link/get_link_peers` which all raise `NotImplementedError`. `HOMEGEAR_CAPABILITIES.linking = False` (`aiohomematic/client/backends/capabilities.py:124`).

**Go path:** `internal/client/backends/homegear.go:218-292` — full link operations via XML-RPC. `CapabilityFor(KindHomegear).LinkOperations = true` (`internal/client/backends/capabilities.go:209`).

**Rationale:** Homegear 0.8+ supports HomeMatic device linking via XML-RPC (`getLinks`, `addLink`, `removeLink`, `getLinkPeers`). Python's `aiohomematic` omits link support for Homegear because the Python integration targets CCU-only deployments; the missing `linking` capability was a scope exclusion, not a protocol limitation. Go's `HomegearBackend` enables linking for operators who use Homegear as a standalone bridge with linked devices. The `LinkOperations=true` capability flag ensures the coordinator exposes the link management REST endpoints when a Homegear backend is active. No CCU behaviour is affected.

---

### A4-BD-CMDTRACKER-DEAD — CommandTracker.GetLastSentValue has no production reader

**Python reference:** `aiohomematic/store/dynamic/command.py::unconfirmed_last_value_send` — the central property read by `data_point.py:952::unconfirmed_last_value`.

**Go path:** `internal/client/reliability/command_tracker.go:163-188` — `GetLastSentValue` exists but has no production caller in `internal/central/` or `internal/north/`.

**Rationale:** Go's unconfirmed-value surface is `BaseDataPointFields.UnconfirmedValueForKey` (`internal/model/datapoint/base.go`), which is the single reader used by all data-point types. `CommandTracker` was ported alongside `InFlightTracker` as the write-side bookkeeping pair; the read-path (`GetLastSentValue`) has no caller because the model layer already covers this via `UnconfirmedValueForKey`. The tracker's write path (`AddSetValue`, `AddPutParamset`) fires productively on every write operation. The unused `GetLastSentValue` method remains as a structural mirror of the Python API; it will be connected if a future feature (e.g. optimistic-update diffing at the transport layer) requires per-command last-sent tracking independently of the model layer.

---

### BD-CONFIGUI-OtherTitle — "Other Settings" section title vs. Python "Settings"

**Python reference:** `aiohomematic-config/aiohomematic_config/grouping.py:233` — fallback section uses `_translate(group_id="other", fallback="Settings")`. Translated: "Settings" (en) / "Sonstige Einstellungen" (de).

**Go path:** `internal/configui/grouping.go:120` — `otherTitle` initialised to `"Other Settings"`; applied at line 328 as the all-bucket label.

**Rationale:** "Other Settings" is more descriptive than bare "Settings" for a catch-all bucket. The divergence is cosmetic — no HA entity or data-flow depends on this string. Chosen for clarity and consistency with English UI text elsewhere in the project.

---

### BD-CONFIGUI-SubsetGroupID — consistent subset_group_id pointer vs. Python inconsistency

**Python reference:** `aiohomematic-config/aiohomematic_config/form_schema.py:509` assigns `form_param.subset_group_id = f"subset_{subset.id}"` (numeric subset ID), but line 462 builds the Group container with `id=f"subset_{subset.member_params[0]}"` (first parameter name). The two sides use different keys.

**Go path:** `internal/central/adapter/uischema_adapter.go:216` uses `subset_<first-member-param>` consistently on both sides.

**Rationale:** Python's inconsistency is a reference bug, not an intended contract. Go's consistent `subset_<first-member-param>` scheme ensures the SPA group lookup always succeeds. Re-introducing the mismatch would break the UC6 subset-selector without benefit.

---

### BD-CONFIGUI-MasterProfileSynthesis — synthesiseMasterProfile is latent infrastructure

**Python reference:** `aiohomematic-config/aiohomematic_config/master_profile_store.py` — `MasterProfileStore._resolve` matches MASTER paramset values against PROFILES_MAP entries from CCU TCL scripts.

**Go path:** `internal/central/adapter/uischema_adapter.go:318` — `synthesiseMasterProfile` converts a `ccudata.MasterProfile` into a `UISchemaProfile` for the SPA ProfileSelector. The embedded easymode archive currently ships zero channels with `master_profile` (the upstream extractor has the code path but no device in the fleet triggers it).

**Rationale:** The function activates automatically once the upstream `openccu-data` extractor emits `master_profile` entries for devices with CCU-side PROFILES_MAP blocks. Removing it now would require re-porting when the upstream data catches up, with no correctness gain in the interim. The runtime cost is zero — the branch is guarded by `cmeta.MasterProfile != nil`.

---

### BD-CCUDATA-SnapshotDate — embedded openccu-data snapshot lags upstream by design

**Python reference:** `openccu-data` changelog version 2026.5.0 (2026-05-10) — translations and easymodes regenerated.

**Go path:** the `SnapshotVersion` constant of the
`github.com/SukramJ/go-openccu-data` module, pinned in `go.mod`.

**Rationale:** The snapshot follows the module version, not every upstream release. Translation updates only affect labels for new OCCU firmware parameters and do not change any data-path logic; the schema is unchanged and the curated overlays are unaffected. Since [ADR 0053](../../docs/adr/0053-go-openccu-data-module.md) the module regenerates itself on an upstream release and arrives here as a dependabot bump, so the lag is now bounded by that bump rather than by a manual refresh.

> **Review this entry.** With regeneration automated, it is no longer clear that this divergence still needs a by-design tolerance. Nothing in the code or the tooling references `BD-CCUDATA-SnapshotDate`. It is kept for now because removing a parity tolerance is not a side effect of a documentation fix.

---

### A5-BD01 — `ClientCoordinator.StartClients` — 8-stage orchestration lives in the adapter, not the coordinator

Python `ClientCoordinator.start_clients` (client.py:244) is an 8-step orchestrator: failure reset, `_create_clients`, `set_primary_interface`, `cache.load_all`, `_init_clients`, `check_and_create_devices_from_cache`, `set_data_cache_initialization_complete`, `hub_coordinator.init_hub`. Go's `StartClients` (client.go:236) iterates the registered `StartFunc` hooks.

The separation is by design: Go's hexagonal architecture routes multi-step sequences with cross-cutting concerns (cache, event bus, hub initialisation) to the adapter wiring layer (`internal/central/adapter/`), not into a coordinator. The coordinator remains a pure registry. The full boot sequence lives in `internal/central/adapter/ccu_wiring.go`, which calls `ClientCoordinator.StartClients`, `Cache.LoadAll`, `DeviceCoordinator.CheckAndCreateDevicesFromCache`, `Cache.SetDataCacheInitializationComplete`, and `HubCoordinator.InitHub` in order. A coordinator that hard-wires this sequence internally would couple it to the construction order of all other coordinators and block parallel initialisation across multiple centrals. Mirrors A5-P01 (`HandleNewDevices` split) and A5-P04 (`InitHub` placement).

Go path: `internal/central/coordinators/client.go::StartClients`, `internal/central/adapter/ccu_wiring.go`.

---

### A5-BD02 — `EventCoordinator.PublishSystemEvent` — no central type-dispatcher; callers use typed Emit methods

Python `EventCoordinator.publish_system_event` (event.py:368) acts as a type-dispatcher: it routes `SystemEventType.DEVICES_CREATED` to `_emit_devices_created_events`, `DEVICES_DELAYED` to `_emit_devices_delayed_event`, etc. Go has a direct `PublishSystemEvent` (event.go:231) that publishes `SystemStatusChangedEvent` unconditionally, plus separate typed emitters (`EmitDevicesCreatedEvents`, `EmitDevicesDelayedEvent`, `EmitDeviceRemovedEvent`, `EmitHubRefreshedEvent`).

The direct-call pattern is by design: Go's type system makes the caller choose the right method at compile time, which is safer than a runtime enum dispatch. A central dispatcher adds an indirection layer that buys nothing in a statically-typed language — the enum variant would be a Go constant that maps 1:1 to the corresponding method call anyway. Callers that currently use `PublishSystemEvent` need the raw `SystemStatusChangedEvent` on the bus; callers that need typed device-lifecycle events call the typed `Emit*` methods directly. No accidental omission risk: the Go compiler rejects a call to the wrong method, whereas a Python string-dispatch with a typo silently does nothing.

Go path: `internal/central/coordinators/event.go::PublishSystemEvent`, `internal/central/coordinators/event.go::EmitDevicesCreatedEvents`.

---

### A5-BD03 — `DefaultRecoveryPipeline` — no startup-failure branch; nil-probe stubs make it safe for cold boot

Python `ConnectionRecoveryCoordinator._execute_recovery_stages` (connection_recovery.py:454) checks `client_exists` and skips `RPC_CHECKING`, `WARMING_UP`, and `STABILITY_CHECK` when the client has never been created. Go's `DefaultRecoveryPipeline` (recovery_stages.go:89) always produces all 8 stages.

The omission is by design for two reasons. First, the production adapter (`internal/central/adapter/ccu_wiring.go:475`) does not use `DefaultRecoveryPipeline` at all — it wires a custom 2-stage pipeline (`reconnect` + `SyncHubData`) via `WithPipelineFor`. `DefaultRecoveryPipeline` is a convenience constructor for test scenarios that need the full 8-stage shape with injected probes. Second, `DefaultRecoveryPipeline` already handles the startup case safely: when `TCPProbe`, `RPCProbe`, and `StabilityProbe` are nil (the default), the corresponding stages are no-op successes (`probeStage(nil)` returns `noopStage()`). A cold-boot recovery run with no probes wired completes immediately without contacting the CCU, which is exactly the desired behaviour. Callers that do supply real probes (integration tests, future diagnostic modes) control whether probes run by setting the fields; they own the startup-versus-recovery distinction at the wiring site.

Go path: `internal/central/coordinators/recovery_stages.go::DefaultRecoveryPipeline`, `internal/central/adapter/ccu_wiring.go`.

---

### A5-BD04 — `CentralUnit.Start` — minimal bootstrap; full orchestration lives in the daemon adapter

Python `CentralUnit.start` (central_unit.py:593) is a 150-line method: IP discovery, XML-RPC server creation, `start_clients` / `start_direct` branching, `_evaluate_central_state`, `health_tracker.sync_central_state`. Go's `CentralUnit.Start` (central.go:411) does exactly three things: transition state-machine to Initializing, start the scheduler, transition to Running.

The minimal implementation is by design: Go's daemon bootstrap (`cmd/openccu-loom/daemon.go`) owns the full start sequence — RPC server bind, per-central client wiring, cache hydration, hub init, health tracker sync. Placing this sequence inside `CentralUnit.Start` would make it untestable in isolation and force a full daemon setup for every coordinator unit test. The scheduler start is in `CentralUnit.Start` because it is the only lifecycle concern that belongs exclusively to `CentralUnit`; everything else is cross-cutting and lives in the adapter. The risk (`EvaluateCentralState` not called from `Start`) is mitigated: the adapter calls `EvaluateCentralState` after `WireCentrals` completes, which is after all clients have been started.

Go path: `internal/central/central.go::Start`, `cmd/openccu-loom/daemon.go`.

---

### A5-BD05 — `CentralUnit.ReadableGenericDataPoints` — placed on domain orchestrator, not on QueryFacade

Python `query_facade.py:375` exposes `get_readable_generic_data_points` as a read-only facade method. Go has `CentralUnit.ReadableGenericDataPoints` (central.go:1051) on the domain orchestrator.

The placement is by design: Go's `QueryFacade` is a thin adapter over the `CentralUnit` API surface (it holds a `*CentralUnit` reference); moving the method there would add a pass-through delegation with no benefit. `ReadableGenericDataPoints` reads from the `DeviceRegistry` which is owned by `CentralUnit`; placing it directly on `CentralUnit` avoids an intermediate hop. The `QueryFacade` surface is intentionally kept narrow — it exists to provide a read-only view for north-bound adapters, not to duplicate every read method. REST handlers that call `ReadableGenericDataPoints` do so via the `CentralUnit` reference already in scope.

Go path: `internal/central/central.go::ReadableGenericDataPoints`, `internal/central/queryfacade.go`.

---

### A5-BD06 — `HubCoordinator.GetProgramDataPoint` / `GetSysvarDataPoint` — single-key lookup; legacy_name and state_path are covered separately

Python `hub.py:414,465` supports three lookup paths per entity: primary identifier (pid/vid), `legacy_name`, and `state_path`. Go's `GetProgramDataPoint(pid)` (hub.go:621) and `GetSysvarDataPoint(name)` (hub.go:634) use the primary identifier only.

The single-key approach is by design for v0.1.0: `GetSysvarDataPoint` takes `name`, which IS the legacy name (the CCU's original `Name` field before slug-normalisation). The `LegacyName()` accessor on `hub.HubDataPoint` (`internal/model/hub/generic_data_point.go:100`) returns `h.Name` — so for sysvars the `name` parameter already covers the legacy-name lookup path. State-path lookup is not needed at the coordinator level: `HubStatePaths()` (hub.go:673) enumerates all state paths, and the `QueryFacade` routes state-path resolution via that list. REST and WS handlers that need state-path resolution use `QueryFacade` — the coordinator does not need to duplicate the dispatch.

Go path: `internal/central/coordinators/hub.go::GetProgramDataPoint`, `internal/central/coordinators/hub.go::GetSysvarDataPoint`, `internal/central/coordinators/hub.go::HubStatePaths`.

---

### A5-BD07 — `DeviceCoordinator.RefreshFirmwareData` — no device_address filter; global refresh only

Python `device.py:682` accepts an optional `device_address` filter to refresh firmware data for a single device. Go's `RefreshFirmwareData` (device.go:916) always refreshes globally.

The global-only approach is by design for v0.1.0: the production call sites (`internal/central/jobs.go` scheduler jobs `firmware_check`, `firmware_delivery_check`, `firmware_updating_check`) always pass no filter — they scan the entire fleet. Firmware state changes are infrequent (at most a few updates per month); fetching the full list on each scheduler tick has negligible overhead compared to the RPC round-trip. A per-device path would only be useful for a REST endpoint that triggers an on-demand single-device firmware refresh; that endpoint is not in the v0.1.0 scope. The method can be extended with an optional address filter when the REST surface requires it, without an API break (an empty-string address defaults to the global path).

Go path: `internal/central/coordinators/device.go::RefreshFirmwareData`.

---

### A5-BD08 — `CacheCoordinator.LoadAll` — no schema version check; goose migrations handle structural changes

Python `cache.py:272` checks `DataOperationResult.VERSION_MISMATCH` and calls `clear_all` when the cached schema version does not match. Go's `LoadAll` (cache.go:318) loads the persisted entries without a schema-version gate.

The omission is by design: OpenCCU-Loom's persistence layer uses goose-managed SQLite migrations (`internal/store/sqlite/migrations/`). When a migration adds or removes columns the migration itself sets the stored data to a valid post-migration state; no application-level version comparison is needed. Python's `VERSION_MISMATCH` guard exists because its cache is a plain pickle file with no migration tooling — any schema change produces an unreadable file. SQLite migrations atomically transform the schema and data together, so a version mismatch cannot survive a successful daemon start. If a future cache entry format changes in a backwards-incompatible way, the correct path is a goose migration that drops and recreates the affected rows, not a runtime version check.

Go path: `internal/central/coordinators/cache.go::LoadAll`, `internal/store/sqlite/migrations/`.

---

### A5-BD09 — `ConnectionState` placed in `statemachine` package, not in `central` package

Python `CentralConnectionState` lives in `aiohomematic/central/connection_state.py` as a sibling of `CentralUnit`. Go's `ConnectionState` (`internal/central/statemachine/connection_state.go`) lives in `internal/central/statemachine/`.

The placement is by design: `ConnectionState` tracks per-interface issue counts that feed directly into the state-machine transition logic in `statemachine/central.go` — `MarkInterfaceDegraded` and `TransitionTo` both consume it. Keeping it in the same package avoids an import cycle (the state-machine would need to import the `central` package to read `ConnectionState`, while `central` already imports `statemachine`). The Python arrangement is flat (everything in `central/`), so the same-package grouping there creates no cycle. In Go's package graph the state-machine package is the correct owner: it is the only consumer, and all public surface (`AddIssue`, `RemoveIssue`, `IssueCount`, etc.) is reachable from `central` via the `*statemachine.Central` reference that `CentralUnit` holds.

---

### A1-BD01 — `AdditionalInformation()` emitted north-bound — PARTIALLY RESOLVED (MQTT per-DP state + REST datapoint DTO)

`ServiceMessages.AdditionalInformation()` (`internal/model/hub/messages.go`), `AlarmMessages.AdditionalInformation()`, and `OperatingVoltageLevelSensor.AdditionalInformation()` (`internal/model/calculated/voltage.go:129`) expose enriched metadata maps. The original concern — that merging them would require a versioned MQTT-schema bump — was waived by the repo owner in favour of a strictly **additive** extension under a single optional `additional_information` key.

**Status (2026-06): per-DP MQTT state RESOLVED.** The per-DP MQTT slot-state envelope now carries an optional `additional_information` map: `payload.PerDPState.AdditionalInformation` (`json:"additional_information,omitempty"`), populated at the publish boundary in `internal/central/adapter/eventbridge.go` via the non-invasive optional-capability seam `dpAdditionalInformation` (a type assertion on `additionalInfoProvider`, so a plain scalar DP contributes nothing and its payload stays byte-identical). The current producer is the operating-voltage sensor's battery metadata. Documented in `docs/mqtt-topic-schema.md`.

**Status (2026-06): REST datapoint DTO RESOLVED.** `handlers.DataPointSummary` (`internal/north/rest/handlers/devices.go`) now carries the optional `additional_information` map, populated in `toDataPointSummary` via the same optional-capability assertion (`dp.(interface{ AdditionalInformation() map[string]any })`). Additive schema extension: `assets/openapi.yaml` `DataPointSummary` gained the property, `make export-schemas` regenerated the digest, and `APIVersion` bumped 2.9.0 → 2.10.0.

**Remaining follow-up (still open):** the hub service-/alarm-message MQTT + REST aggregates — `internal/north/mqtt/bridge.go::PublishServiceMessages`/`PublishAlarmMessages` currently marshal the raw `[]hub.ServiceMessage`/`[]hub.AlarmMessage` structs (not the `AdditionalInformation()` maps), and `handlers.ServiceMessageDTO`/`AlarmMessageDTO` likewise. Wiring the enriched maps there is a separate change (it alters the published `items` shape).

Go path: `internal/payload/wrapper.go::PerDPState`, `internal/central/adapter/eventbridge.go::dpAdditionalInformation`, `internal/model/calculated/voltage.go::AdditionalInformation`, `internal/model/hub/messages.go::AdditionalInformation`.

---

### A1-BD02 — `GetReadableDataPoints` not used by `MasterPoller`

`Device.GetReadableDataPoints(paramsetKey)` (`internal/model/device/aggregate.go:715`) returns the subset of data points that advertise READ in their operations bitmask. The `MasterPoller` (`internal/client/backends/master_poll.go`) does not call this method — it schedules a full `getParamset` fetch for the entire address/paramset pair after a write.

This is by design: a full `getParamset` round-trip is cheaper in practice than constructing N individual `getValue` calls (one per readable DP), because the CCU's XML-RPC server processes the paramset fetch in a single handler. `GetReadableDataPoints` is retained as a public helper for callers that need the filtered list for display or diffing purposes; removing it would reduce the surface available to future REST/WS handlers that want per-parameter read exposure.

Go path: `internal/model/device/aggregate.go::GetReadableDataPoints`, `internal/client/backends/master_poll.go`.

---

### A1-BD03 — `EmptySimpleEntry` has no production caller in v0.1.0

`EmptySimpleEntry(category)` (`internal/model/schedule/simple.go::EmptySimpleEntry`) constructs a minimal but valid `SimpleEntry` suitable as a UI default when a user adds a new schedule slot. The Go helper has no production caller (only its own definition + the schedule package test suite).

This is by design. The original deferral noted the SPA had no "add slot" affordance; that is no longer true — the Svelte schedule editor now exposes an add-slot button (`assets/ui/src/lib/components/schedule/SimpleScheduleEditor.svelte` `addEntry` handler, `schedule.add_slot` label). The SPA constructs the new slot client-side and persists the whole schedule via the existing REST/WS write path, so it never round-trips through the Go `EmptySimpleEntry` factory. The function mirrors the reference implementation's `empty_schedule_entry` property and is retained as the canonical model-side default-entry constructor; it would gain a production caller only if a server-side "add empty slot" endpoint is introduced.

Go path: `internal/model/schedule/simple.go::EmptySimpleEntry`.

---

### A1-BD04 — `LegacyName()` returns empty string on Hub aggregate types

`AlarmMessages.LegacyName()`, `ServiceMessages.LegacyName()`, and `InstallMode.LegacyName()` (hub package) all return `""`. Only `HubDataPoint.LegacyName()` returns a meaningful non-empty string.

This is by design: the `LegacyName()` method exists to satisfy a common interface contract shared by all hub data-point types — `HubDataPoint` needs it to surface the original pre-slug name from the CCU, while the aggregate types (`AlarmMessages`, `ServiceMessages`, `InstallMode`) are Go-synthesised aggregates with no CCU-side pre-slug name to preserve. An empty return is the correct sentinel meaning "no legacy name exists for this type". Returning a hard-coded string (e.g. `"alarm_messages"`) would be semantically wrong — it would imply a CCU-assigned name that was never present on the wire.

Go path: `internal/model/hub/messages.go::LegacyName`, `internal/model/hub/inbox.go::LegacyName`, `internal/model/hub/install_mode.go::LegacyName`.

---

### A2-BD01 — Lock unlock-event ring buffer is Go-only

`Lock` maintains a ring buffer of the 10 most recent unlock events (`internal/model/custom/lock/lock.go:559-582`, capacity `unlockEventCapacity = 10`). The reference implementation has no equivalent structure.

This is a deliberate Go-side extension: the unlock history is consumed by the REST/WS event-log surface and by the Matter `DoorLock` cluster's `LockOperationEvent` stream. Both require a per-device event window that survives the single-event-bus model. No Python counterpart is needed because the reference implementation relies on the HA event bus for history.

Go path: `internal/model/custom/lock/lock.go::unlockEventRing`.

---

### A2-BD03 — Valve modulating data point is Go-only and unregistered

`valve.Modulating` (`internal/model/custom/valve/valve.go`) exposes a
continuous LEVEL (0..1 position) write path for proportional radiator-style
valve actuators — the kind of device an `HmIP-FALMOT-C12` presents. The
aiohomematic reference custom-DP set contains only `CustomDpIpIrrigationValve`
(a switch-based on/off valve) and models no modulating valve at all.

This is by design on two levels:

- **The type exists** because a modulating actuator needs a float-valued
  write path the boolean switch model cannot express; Go adds it as a
  dedicated type rather than coercing it into a switch. It is fully
  exercised by `valve_test.go` (FALMOT-C12-shaped fixtures).
- **The type is unregistered.** `pkg/hmenum/device_profile.go` defines only
  `DeviceProfileIPIrrigationValve`, and `valve/init.go` registers a
  constructor for that profile alone — nothing maps onto `Modulating`.
  Registering it would mean inventing a device→profile mapping that the
  reference stack does not have. Leaving it unregistered is therefore
  parity, not a gap; it is a forward declaration to be wired only if a
  matching profile appears upstream (see `notes/plans/roadmap.md` Phase 4).

Go path: `internal/model/custom/valve/valve.go::Modulating`;
registration: `internal/model/custom/valve/init.go`.

---

### A2-BD04 — `EffectLight.Effects()` prefers `PROGRAM.VALUE_LIST`, falls back to the reference's fixed list

`EffectLight` (`internal/model/custom/light/effect.go`) populates its effects list from the PROGRAM data point's `VALUE_LIST` at construction time and falls back to the reference's fixed seven-element list `("Off", "Slow color change", "Medium color change", "Fast color change", "Campemit", "Waterfall", "TV simulation")` when the descriptor carries none.

The fallback is not optional. `RfDimmer_Color` is registered for exactly one model, HM-LC-RGBW-WM, and its PROGRAM parameter (`VCU3747418:3`, RGBW_AUTOMATIC) is `INTEGER MIN 0 MAX 255` with **no** `VALUE_LIST` — the CCU's own string table labels it "Program number". So on the only device this profile covers, the dynamic source is always empty. Until 0.61.4 the discovery payload papered over that with four substituted labels (`NONE`, `SLOW_COLOR_CHANGE`, `MEDIUM_COLOR_CHANGE`, `FAST_COLOR_CHANGE`) that exist in no `VALUE_LIST` on any device in the fleet and in no CCU string table: Home Assistant rendered an effect picker in which every entry was refused by `SetEffectByLabel`, and `Effect()` reported an empty label forever.

The earlier version of this entry claimed the dynamic approach "automatically reflects firmware-added effects". That premise was false for the one device concerned, and the entry blessed a surface that did not work.

The index written to PROGRAM is the position in the list, so the order of the seven labels is the wire contract, not a presentation choice.

Go path: `internal/model/custom/light/effect.go::EffectLight`, `colorDimmerEffects`.

---

### A2-BD05 — `Climate.ScheduleProfileNos` uses static 1..6 pool

`Climate.numWeekPrograms()` (`internal/model/custom/climate/climate.go:1808-1840`) returns a static count based on the device profile. The reference implementation derives the profile-slot count dynamically from `_dp_active_profile.min` / `_dp_active_profile.max`.

This is by design for v0.1.0: the static mapping covers all known HM-CC-RT-DN and HmIP-eTRV variants (all have exactly 3 or 6 slots). Dynamic derivation requires the `ACTIVE_PROFILE` parameter's `MIN`/`MAX` descriptor to be present and loaded before profile enumeration, which introduces a boot-ordering dependency. The static mapping will be replaced with the dynamic approach in the climate schedule-editor milestone when the ordering constraint can be resolved cleanly.

Go path: `internal/model/custom/climate/climate.go::numWeekPrograms`.

---

### A2-BD06 — `Light.IsStateChange` ON/OFF check does not apply the `len(kwargs)==1` guard

`Light.IsStateChange(turnOn, turnOff, brightness)` (`internal/model/custom/light/light.go:746`) evaluates the `turnOn`/`turnOff` flags independently of whether `brightness` is also set. The reference implementation applies these flags only when the ON/OFF argument is the sole kwarg (`len(kwargs) == 1`).

This is by design: the Go surface decomposes the combined kwargs dict into explicit typed arguments at the call site. A caller that passes both `turnOn=true` and a non-nil `brightness` is asking for both changes, and the state-change check should reflect that. The `len==1` Python guard exists because `kwargs` is a dynamic dict and the ON/OFF short-circuit would otherwise suppress the brightness check — a problem that cannot arise in Go's statically-typed signature. The full-parity `IsStateChangeFull` form additionally checks HSColor, ColorTemp, Effect, OnTime, and RampTime, which covers the multi-kwarg case faithfully.

Go path: `internal/model/custom/light/light.go::IsStateChange`, `internal/model/custom/light/light.go::IsStateChangeFull`.

---

### A2-BD07 — soundfile index range follows the device, not the reference's 189

`ConvertSoundfileIndex` (`internal/model/custom/siren/sound.go`) accepts `1..252`; the reference's `_convert_soundfile_index` (`model/custom/siren.py`) raises above 189.

The HmIP-MP3P — the only device in the fleet carrying `SOUNDFILE` — advertises a 256-entry `VALUE_LIST`: `INTERNAL_SOUNDFILE`, `SOUNDFILE_001` … `SOUNDFILE_252`, `RANDOM_SOUNDFILE`, `OLD_VALUE`, `DO_NOT_CARE`. The reference bound is only ever applied to its integer convenience form; a string soundfile is passed through to the wire untouched, so 190..252 remain reachable there. openccu-loom publishes the whole list as Home Assistant's `available_tones`, which means the numeric round-trip is on the hot path — a 189 cap made the daemon offer 63 tones it then silently dropped, and the player replayed the previously selected file.

Two related decisions in the same place:

- `OLD_VALUE` and `DO_NOT_CARE` are filtered out of `AvailableSoundfiles()`. They are link-profile sentinels ("restore the previous value" / "leave untouched"), not playable files.
- A tone the device does not offer is an error (`ErrUnknownSoundfile`), not a dropped parameter. The surrounding parameters are written either way, so a silent drop looks like success.

Go path: `internal/model/custom/siren/sound.go::ConvertSoundfileIndex`, `SoundPlayer.AvailableSoundfiles`, `SoundPlayer.PlaySound`.

---

### A2-BD08 — RF colour dimmers drive colour through the single `COLOR` integer

`ColorLight` (`internal/model/custom/light/color.go`) resolves HUE + SATURATION on the light's own channel and, when that pair is absent, the profile's `FieldColor` mapping on the sibling channel.

HM-LC-RGBW-WM carries no HUE and no SATURATION on any channel; its colour is one `INTEGER 0..255` named `COLOR` on `VCU3747418:2` (RGBW_COLOR), which `RfDimmer_Color` maps at channel offset 1. The projection mirrors the reference `CustomDpColorDimmer` (`model/custom/light.py`): `COLOR >= 200` is the white point (saturation 0), otherwise the hue circle maps onto `0..199` and saturation reads back as 100.

`ColorLight.SupportsColor()` gates the `hs` colour mode in the discovery payload on one of the two sources being present, so a light with neither no longer advertises a colour wheel whose every command is refused.

Go path: `internal/model/custom/light/color.go::ColorLight`, `internal/model/custom/light/init.go::colorChannel`.

---

### A4-BD01 — `InFlightTracker` lives on `ValueWriter`, not on `InterfaceClient`

The reference implementation attaches `_in_flight_commands` to `InterfaceClient` and reads it as a fallback in `data_point.unconfirmed_last_value_send`. Go's `InFlightTracker` (`internal/client/reliability/in_flight_tracker.go`) lives on `ValueWriter` (`internal/client/value_writer.go:60`) and is used in the callback handler as an echo-suppress filter rather than as a reader fallback.

This is by design: Go's hexagonal architecture separates write orchestration (`ValueWriter`) from the CCU callback ingestion path (`CallbackHandlers`). Placing the in-flight state on `ValueWriter` co-locates it with the write lifecycle (stage on send, clear on echo-confirm), avoiding cross-package state sharing. The observable behaviour — suppressing duplicate north-bound emissions when an optimistic write echoes back — is equivalent to the Python approach; only the code path differs.

Go path: `internal/client/value_writer.go`, `internal/client/reliability/in_flight_tracker.go`, `internal/central/adapter/callback_handlers.go`.

---

### A4-BD02 — JSON-RPC `Interface.getDeviceDescription` / `listInterfaces` / `getParamset` absent from Go JSON-RPC client

The Go JSON-RPC transport (`internal/client/transport/jsonrpc/methods.go`) does not implement `Interface.getDeviceDescription`, `Interface.listInterfaces`, `Interface.getParamset`, or `Interface.getParamsetDescription`. These methods exist in the reference implementation's JSON-RPC client.

This is by design: OpenCCU-Loom's primary protocol for device discovery and paramset reads is XML-RPC (SPECIFICATION.md §5.1). The JSON-RPC client is used for Hub (SysVar / Program) operations and extended CCU management calls that the XML-RPC interface does not expose. The listed methods are reachable via the XML-RPC path on every supported CCU model. If `JsonCcuBackend` (CCU-Jack JSON-RPC-only mode) ever becomes a supported target, these methods will be added with a contract test.

Go path: `internal/client/transport/jsonrpc/methods.go`.

---

### A4-BD03 — Session-method CB-bypass list is defensive-only

`circuit.go:60-62` (`internal/client/reliability/circuit.go`) lists `Session.login`, `Session.logout`, and `Session.renew` as circuit-breaker bypass operations. In the current production wiring, session calls are made directly by the JSON-RPC transport layer without passing through the circuit breaker, so the bypass list is never consulted.

This is by design: the bypass list is a defensive safeguard for a future refactor that routes session calls through the `InterfaceClient` circuit-breaker path. If that refactor lands without the bypass list, a closed circuit would block login — a hard-to-diagnose failure. The list costs nothing at runtime and documents the intent explicitly.

Go path: `internal/client/reliability/circuit.go::bypassOps`.

Go path: `internal/central/statemachine/connection_state.go`, `internal/central/statemachine/central.go::MarkInterfaceDegraded`.

---

### A7-BD-WS-STUBS — 5 WebSocket commands registered as deferred-wiring stubs

Five WebSocket commands in `internal/north/rest/ws/commands_missing.go` are registered with a stub handler when the corresponding domain service is not wired into `MissingCommandsConfig`. The commands are:

- `schedules.set_enabled` — requires `SetScheduleEnabled` on the schedules domain, gated by `MissingCommandsConfig.ScheduleEnabler`.
- `links.get_form_schema` — requires `GetLinkParamsetDescription` on the paramsets domain, gated by `MissingCommandsConfig.LinkFormSchema`.
- `links.get_profiles` — requires the link-profile store, gated by `MissingCommandsConfig.LinkProfiles`.
- `links.test_profile` — requires the link-profile store and `put_link_paramset`, gated by `MissingCommandsConfig.LinkProfiles`.
- `paramset.determine` — requires `determine_parameter` on the InterfaceClient backend, gated by `MissingCommandsConfig.ParameterDeterminer`.

Each command is pre-registered so it appears in `system.commands` and in `assets/wsapi.json` even before the domain is wired. When the matching service is provided at daemon startup the real handler is used transparently; callers need no schema change.

The stub responses are intentional placeholders that communicate clearly to API clients that the feature is not yet available in the current build. The handler pattern — conditional real vs. stub registration — is the standard OpenCCU-Loom extension point for features whose domain layer is defined but whose wiring through the full stack has not been completed yet.

These are not permanent divergences from the Python reference; the Python equivalents in `websocket_api.py` (`ws_set_schedule_enabled`, `ws_get_link_form_schema`, `ws_get_link_profiles`, `ws_test_link_profile`, `ws_determine_parameter`) are the target parity surface. The wiring of each service into `daemon.go` was deferred to a post-0.1.0 milestone.

**Status (2026-06): implemented — all five providers are wired into `MissingCommandsConfig` in `cmd/openccu-loom/ws_adapters.go` (`ScheduleEnabler`, `LinkFormSchema`, `LinkProfiles` — backing both `links.get_profiles` and `links.test_profile`, and `ParameterDeterminer`, around lines 163–173); the `stubHandler` path now applies only when a provider is nil, and all providers are non-nil at daemon startup.**

Go paths: `internal/north/rest/ws/commands_missing.go` (stub registrations), `cmd/openccu-loom/ws_adapters.go` (service wiring site).

---

### A3-BD-LockPermission — `LockPermission` identifiers `Allowed/Denied` vs. Python `GRANTED/NOT_GRANTED`

Python uses `LockPermission.GRANTED / NOT_GRANTED` as enum member names (`const.py:2419-2423`). Go uses `LockPermissionAllowed / LockPermissionDenied` (`internal/model/schedule/simple.go:106-107`).

The identifier renaming is by design: Go convention uses positive/negative adjectives (`Allowed`, `Denied`) rather than past-participle forms to better align with the surrounding `LockAction*` constant group. The wire strings are identical (`"granted"` / `"not_granted"`), so HA Discovery, MQTT payloads, and all CCU round-trips are unaffected.

Go path: `internal/model/schedule/simple.go::LockPermissionAllowed`, `internal/model/schedule/simple.go::LockPermissionDenied`.

---

### A3-BD-DpDummy-HSColor — `HSColor.IsValid` uses observability flags, not a DpDummy sentinel

Python `CombinedDpHsColor.is_valid` checks `not isinstance(self._hue_dp, DpDummy)` (`hs_color.py:63-66`). Go's `HSColor` has no `DpDummy` concept; validity is inferred from data-point observability.

The difference is by design: Go's type system does not use sentinel objects. A missing underlying data-point results in a nil pointer or an unobserved data-point, not a `DpDummy` placeholder. `HSColor` is constructed only when both hue and saturation data-points are present, so a null-hue path cannot occur at runtime. Callers that need to test data-point availability query `Observed()` directly.

Go path: `internal/model/combined/hscolor.go`.

---

### A3-BD-DpDummy-Timer — `Timer` without a unit-DP sends seconds directly; no DpDummy construction

Python allows `unit_dp = DpDummy` for timers without a unit data-point, in which case the raw second value is sent without unit conversion (`timer.py:64-68`). Go returns a nil-subscribe when `unitDP == nil` (`timer.go:130-132`).

The difference is by design: Go's `Timer` requires a real unit data-point at construction time. A timer without a unit DP is not a valid configuration in any known device profile; the nil-subscribe guard is a defensive check, not a supported code path. If a device profile without a unit DP is ever added, the correct approach is an explicit `WithoutUnitDP` constructor option, not a sentinel type.

Go path: `internal/model/combined/timer.go::Subscribe`.

---

### A3-BD-MetricLastEventAge — `MetricLastEventAgeSecs` MQTT Discovery/Publish path

Python publishes `HmEventAgeSensor` via `metrics.py:219`. The original divergence: Go modelled `MetricLastEventAgeSecs` in `internal/model/hub/metrics.go` but had no corresponding Discovery builder or publish path in `hub_mqtt_publisher.go`, deferred as a diagnostic-only sensor that does not affect device control or state accuracy.

**Status (2026-06): implemented — the full MQTT path is wired.** `DefaultDiscoveryBuilder.BuildLastEventAgeDiscovery` (`internal/north/mqtt/hub_discovery.go:769`) emits a HA `sensor` (duration, seconds, diagnostic) on `<base>/<central>/system/last_event_age` (`internal/north/mqtt/topics.go::HubLastEventAge`), and `Bridge.PublishHubLastEventAge` (`internal/north/mqtt/bridge.go:1075`) publishes the retained state. `hub_mqtt_publisher.go` now publishes the discovery item, does the initial-state publish, and subscribes `Metrics.OnUpdate(MetricLastEventAgeSecs, …)` (`internal/central/adapter/hub_mqtt_publisher.go:207-233`). Coverage: `internal/north/mqtt/hub_singletons_parity_test.go::TestBuildLastEventAgeDiscovery`.

Go path: `internal/model/hub/metrics.go::MetricLastEventAgeSecs`, `internal/north/mqtt/hub_discovery.go::BuildLastEventAgeDiscovery`, `internal/north/mqtt/bridge.go::PublishHubLastEventAge`, `internal/central/adapter/hub_mqtt_publisher.go`.

---

### A5-BD-LoadDataCacheIface — `LoadDataCache` always loads all interfaces; no per-interface filter

Python `CacheCoordinator.load_data_cache(*, interface=None)` (`cache.py:313`) accepts an optional interface filter. Go's `CachePersister.LoadDataCache(ctx)` (`internal/central/coordinators/cache.go:43-45`) always loads the full cache.

The global-load approach is by design for v0.1.0: production call sites always load the full cache at boot. Selective interface recovery is handled by the ConnectionRecovery pipeline, which does not rely on per-interface cache reload semantics. A scoped query can be added when selective recovery requires it, without changing the interface contract.

Go path: `internal/central/coordinators/cache.go::LoadDataCache`.

---

### A5-BD-RestartClients — `RestartClients` is a stop/start sequence, not a de-init/init pipeline

Python `restart_clients` calls `_de_init_clients` followed by `_init_clients` (`client.py:232-242`). Go's `RestartClients` (`internal/central/coordinators/client.go:275`) calls `StopClients`, waits 500 ms, then calls `StartClients`.

The difference is by design: Go's `StopClients`/`StartClients` cycle is semantically equivalent — `StopClients` runs each entry's `StopFunc` (DeInit RPC + transport close) and `StartClients` runs each entry's `StartFunc` (Init RPC + callback registration). The 500 ms cooldown lets in-flight wire responses drain before the reconnect Init handshake. See A5-BD01 for the analogous `StartClients` rationale.

Go path: `internal/central/coordinators/client.go::RestartClients`.

---

### A5-BD-CreateDevices — `DeviceCoordinator.CreateDevices` lives in the adapter pipeline

Python `DeviceCoordinator.create_devices` (`device.py:350-434`) is a coordinator method. Go's equivalent logic lives in `internal/central/adapter/device_pipeline.go`.

The placement is by design: `create_devices` in Python requires cross-cutting access to paramset fetch, cache, event emission, and consistency check — all adapter-layer concerns in Go's hexagonal architecture. See A5-BD01 for the analogous `StartClients` rationale.

Go path: `internal/central/adapter/device_pipeline.go`, `internal/central/coordinators/device.go::CheckAndCreateDevicesFromCache`.

---

### A5-BD-MetricsObserver — no separate `MetricsObserver` lifecycle object; metrics updated via direct callbacks

Python `CentralUnit` holds a `_metrics_observer` started/stopped as part of central lifecycle (`central_unit.py:693`). Go has no equivalent object.

The omission is by design: Go's health and metrics subsystem uses direct callback subscriptions. The `Tracker` (`internal/health/tracker.go`) registers gauge functions invoked on-demand; there is no background observer goroutine requiring explicit lifecycle management. When a central stops, subscriptions are released via standard event-bus unsubscribe. Python's `MetricsObserver` exists because asyncio requires an explicit task for periodic metric aggregation; Go achieves the same result without a separate object.

Go path: `internal/health/tracker.go`, `internal/central/central.go::Stop`.

---

### A5-BD-GetConfigurableDevicesLocale — `GetConfigurableDevices` has no locale parameter

Python `get_configurable_devices(*, locale: str = "en")` (`configuration.py:355`) accepts a locale. Go's `GetConfigurableDevices(iface)` (`internal/central/coordinators/configuration.go:241`) does not.

The omission is by design: Go follows a request-scoped locale model (`pkg/hmreqctx`). REST handlers carry the locale in the request context and resolve labels at the handler boundary, not inside the coordinator. Adding a locale parameter to the coordinator would push presentation concerns into the domain layer.

Go path: `internal/central/coordinators/configuration.go::GetConfigurableDevices`, `internal/reqctx`.

---

### A5-BD-LinkCoordKwargs — `LinkCoordinator` methods take positional args, not keyword-only

Python `AddLink` / `RemoveLink` / `SetLinkInfo` use keyword-only arguments (`link.py:137,355`). Go takes the same parameters as positional arguments.

The difference is cosmetic: Go does not have keyword-only argument syntax. The Go call sites pass values in the documented order; accidental positional swap is a compile-time concern only in fully typed languages, and all string parameters carry distinct semantic roles documented in the method signature.

Go path: `internal/central/coordinators/link.go::AddLink`.

---

### BD-ConnectionRegistryUnwired — `ConnectionRegistry` / `Connection` surface not wired in production

`internal/health/connection.go` (476 LOC) provides a `Connection` type and a `ConnectionRegistry` that track per-interface connection state, staleness, RSSI/duty-cycle history, and reconnect counts at a finer granularity than the `Tracker`'s `ClientHealth` map. The surface is correct and fully tested (contract + unit) but has no production call site: `NewConnectionRegistry()` is only instantiated in tests.

The reason for retaining it rather than deleting: `Tracker.ClientHealth` covers per-call-site health records, while `ConnectionRegistry` is designed for per-interface historical connectivity (reconnect streaks, RSSI time series, SMA-filtered duty cycles). These serve different consumers — the REST `/health` diagnostic endpoint today uses `Tracker`; a future timeline-style sparkline in the Config UI would consume `ConnectionRegistry`. Deleting it now would require recreating the same data model later.

**Current state:** dormant production surface — no `NewConnectionRegistry()` call outside tests, no REST or MQTT wiring. The `loom:reachable` annotations have been removed. The code is retained as an intentional design stub; a future commit will wire it when the Config-UI sparkline panel is built.

Go path: `internal/health/connection.go`.

---

### BD-HubUpdateDiscoveryReadOnly — `BuildHubUpdateDiscovery` omits `command_topic` intentionally

The HA `update` entity for the CCU's own firmware (`hub_discovery.go::BuildHubUpdateDiscovery`) does not include a `command_topic`. The CCU firmware-update workflow requires an operator-confirmed REST action (`PUT /hub/{central}/update/install`); triggering it via an MQTT payload without confirmation is unsafe. HA's UI shows the update available (via `state_topic` + `latest_version_topic`) but the install action is exposed only through the REST API. This is intentional — a future commit may add `command_topic` once a hub-firmware MQTT command subscription is implemented and guarded behind the same operator-confirm guard as the REST path.

Go path: `internal/north/mqtt/hub_discovery.go::BuildHubUpdateDiscovery`.

---

### BD-DeviceUpdateDiscoveryReadOnly — the per-device update entity omits `command_topic` intentionally

The HA `update` entity for one device's firmware (`internal/model/device/update.go::Update.HADiscoveryPayload`) does not include a `command_topic` / `payload_install`. It used to: a plane-topic-roundtrip guard test driven over this entity found that no `CommandSubscriber` subscription — bucket-aware, schedule, week-profile, combined, custom-DP service-method, hub, alarm, or add-on-update — matches the topic shape the entity declared (`<base>/<central>/<iface>/<addr>/update/set`), so HA's "Install" button on a device's firmware card silently did nothing.

Rather than wire a new, unguarded MQTT install path, the entity was made read-only, matching `BD-HubUpdateDiscoveryReadOnly` above for the same reason: an unconfirmed MQTT payload triggering a firmware flash is unsafe, and a broker replaying a stale retained command on reconnect would be worse. The install control for a device's firmware lives at `POST /devices/{addr}/firmware/update`; HA still shows the available-version state via `state_topic` + `latest_version_topic`.

Go path: `internal/model/device/update.go::Update.HADiscoveryPayload`.

---

### BD-MetricLastEventAgeUnwired — `MetricLastEventAgeSecs` observer — RESOLVED (observe path)

`internal/model/hub/metrics.go` defines `MetricLastEventAgeSecs` and wires a `MetricHubSensor` for it in `NewMetricHubSensorPair`. The Observe call site is now wired: the `hub.last_event_age_refresh` scheduler job (registered in `internal/central/jobs.go`, closure built in `cmd/openccu-loom/daemon_jobs.go`) computes `time.Since(newest)` over the per-interface event clock and calls `Metrics.Observe(MetricLastEventAgeSecs, …)` on each tick (default 30 s). `newest` is the most recent stamp across all of the central's interfaces via `EventCoordinator.LastEventMonotonicForInterface` — the thread-safe sentinel the original deferral was waiting on. When no event has been observed yet the job reports nothing.

**MQTT path:** the MQTT Discovery/publish path for this metric is tracked under `A3-BD-MetricLastEventAge`. Status (2026-06): implemented — `hub_mqtt_publisher.go` now publishes `BuildLastEventAgeDiscovery` and subscribes `Metrics.OnUpdate(MetricLastEventAgeSecs, …)` alongside `MetricSystemHealth` (`internal/central/adapter/hub_mqtt_publisher.go:207-233`), so the observed value is reachable over MQTT as well as the hub-metrics model/REST surface.

Go path: `internal/central/jobs.go`, `cmd/openccu-loom/daemon_jobs.go`, `internal/model/hub/metrics.go::MetricLastEventAgeSecs`.

---

### BD-ClientCoordMethods — Several `ClientCoordinator` methods have no production caller

The following `ClientCoordinator` methods exist without a production call site outside `coordinators/client.go`:

| Method | Reason retained |
|---|---|
| `HasClient` | Guard for conditional wiring; will be used by CCU-Jack backend when multi-interface detection is added |
| `HasClients` | Equivalent of `len(c.items) > 0`; symmetric with `Available()` for callers that only need existence, not connectivity |
| `PrimaryClient` | Needed by sysvar/program dispatch once the CCU-backend sysvar path is wired through the coordinator instead of being bypassed via raw client lookup |
| `AllClientsActive` | Used internally by `Available()` indirection; exported for REST diagnostics handler that may expose it in a future `/status` field |
| `PollClients` | Planned use: REST `/clients/poll` endpoint to list interfaces stuck in polling mode |
| `LastFailureReason` / `LastFailureInterfaceID` | Structured diagnostics surface for WS `central.status` command; the fields are populated by `RecordLastFailure` which is called in production |
| `RestartClients` | Planned use: WS `central.restart_clients` command (operator-initiated interface reset) |
| `WaitForTCPReady` | Used internally by `CreateClient`; exported variant `IdentifyIPAddr` has the same body — redundant as a method, retained for API symmetry |
| `SubscribeToHealthEvents` | Event-driven client-state refresh; will be wired once the connection-recovery coordinator is updated to react to health events instead of polling |

None of these methods carry `loom:reachable` annotations. They are public API surface on a type that is already in production (`ClientCoordinator` is instantiated and used). Removing them would not reduce the MASKED count in the reachability audit (they are not annotated). They are retained as planned API surface.

Go path: `internal/central/coordinators/client.go`.

---

### BD-A1-V06 — `Device.ReloadDeviceConfig` (the model `OnConfigChanged` cascade) has no direct production caller

`Device.ReloadDeviceConfig` (`internal/model/device/device.go`) runs the per-channel `OnConfigChanged` cascade. The WS command `reload_device_config` reaches the `DeviceReloaderAdapter` (`internal/central/adapter/device_reloader.go`), which materialises the device through `DeviceCoordinator.RefreshDeviceDescriptionsAndCreateMissingDevices` rather than calling `Device.ReloadDeviceConfig`. The model method therefore has no production caller.

The adapter now scopes that refresh to the target device only: `singleDeviceDescFetcher` fetches `Backend.GetDeviceDescription` for the device plus each address in its `CHILDREN` list, instead of `ListDevices` over the whole interface (a per-channel fetch error is logged and skipped so one unreachable channel cannot abort the reload). What remains by design is the *materialisation path*: the adapter goes through the coordinator's additive create-missing flow, not the model's `OnConfigChanged` cascade. `Device.ReloadDeviceConfig` is kept in the model so that cascade stays unit-testable independently of the adapter layer; wiring the adapter onto it would be a deeper refresh-semantics change than the targeted-fetch optimisation that landed here.

Go path: `internal/model/device/device.go::ReloadDeviceConfig`, `internal/central/adapter/device_reloader.go::ReloadDeviceConfig`.

---

### BD-A1-V07 — `DeviceCoordinator.RefreshDeviceLinkPeers` has no production caller

`DeviceCoordinator.RefreshDeviceLinkPeers` (`internal/central/coordinators/device.go`) re-fetches link-peer addresses for every channel of a device and publishes a `LinkPeerChangedEvent`. The method exists to support the boot-time link-peer initialisation that the reference implementation performs in `Channel.__init__ → init_link_peer`.

For 0.1.0, boot-time link-peer fetching was deferred because it requires one RPC call per channel with link peers and the performance cost on a large inventory had not been profiled. The method was exercised in contract tests to keep the implementation correct. Until wired, the `RecoveryCompletedEvent` subscriber in `climate_link_peer_refresh.go` used the cached `ch.LinkPeers()` slice from the initial paramset load.

**Status (2026-06): implemented as on-demand refresh — `config.reload_device_config` now also calls `RefreshDeviceLinkPeers` (`internal/central/adapter/device_reloader.go`). Note: this is on-demand-on-reload, NOT a boot-time per-device sweep; the boot-time sweep remains intentionally avoided to prevent an RPC storm on large inventories.**

Go path: `internal/central/coordinators/device.go::RefreshDeviceLinkPeers`, `internal/central/adapter/device_reloader.go`.

---

### BD-DeviceCoordMethods — Several `DeviceCoordinator` methods have no direct production caller

The following `DeviceCoordinator` methods exist without a production call
site outside `coordinators/device.go`. Each has a reference-implementation
counterpart with a known wiring point, or is a deliberate API surface the
adapter layer currently bypasses:

| Method | Reason retained |
|---|---|
| `CheckForNewDeviceAddresses` | Reference counterpart `device.py::check_for_new_device_addresses`; the Go adapter resolves new-device detection through the `HandleNewDevices` push pipeline instead, so this stays as the pull-mode counterpart for a future poll path |
| `InitialPull` | Boot-load path is covered by `CheckAndCreateDevicesFromCache` + `RefreshDeviceDescriptionsAndCreateMissingDevices`; retained as the explicit pull-stage hook for post-0.1.0 boot-stage wiring |
| `RefreshAfterPair` / `RefreshAfterUnpair` | Pairing flow is a P2 feature; CCU-initiated pair/unpair runs through the `callback.deleteDevices` → `RemoveDevice` path today |
| Deferred-creation accept | Reference `device.py::add_new_devices_manually`; **implemented** — REST `POST /devices/{addr}/accept` (`internal/north/rest/handlers/device_admin.go`) and WS `inbox.accept` reach adapter `AcceptPendingDevice` (`internal/central/adapter/pending_devices.go`), which drains the coordinator queue (`TakeDelayedDeviceDescriptions`), runs the shared materialiser and publishes through `HandleAcceptedDevices` (`internal/central/coordinators/device.go`). The queue is mirrored onto the hub inbox aggregate, so `GET /inbox`, `inbox.list`, the `hub.<central>.inbox` broadcast and the MQTT inbox sensor list the parked devices. |
| `GetVirtualRemotes` / `GetVirtualRemoteAddresses` | Reference `device.py::get_virtual_remotes`; wired when virtual-remote support reaches the MQTT/REST surface |
| `DeleteDevice` / `HandleDeleteDevices` | Adapter `device_admin.go` calls `backend.DeleteDevice` directly; the coordinator variants are the pure-registry layer the adapter bypasses by design |
| `RefreshFirmwareDataByState` | WS `firmware.refresh` uses the `FirmwareRefresher` interface (`ws_adapters.go`); reference `device.py:710` counterpart. **Status (2026-06): implemented** — `FirmwareDomain` (`internal/central/adapter/firmware_domain.go`) backs the `firmware.refresh` WS command, wired in `cmd/openccu-loom/ws_adapters.go:135`. |
| `SetDeviceNameOverrideChecker` / `RenameNewDeviceFromOverride` | Optional operator name-override feature flag; not wired in `ccu_wiring.go` for 0.1.0 |

`CheckParamsetConsistency` is excluded from this list: it is indirectly
wired in production through `ScheduleParamsetConsistencyCheck`
(`ccu_wiring.go`).

---

### BD-A1-V08 — `DeviceStateChangedEvent` has no production producer or subscriber

`pkg/hmevent/catalogue.go` defines `DeviceStateChangedEvent` with 0 producers and 0 subscribers in production code. The event type covers the high-level device availability / reachability state summary.

This is by design for 0.1.0: the equivalent signal in Go is `DeviceLifecycleEvent` (produced in `internal/central/adapter/device_availability.go`) and the more granular `DataPointValueChanged` bus for individual state changes. `DeviceStateChangedEvent` is a reserved slot for a future north-bound adapter that needs a single compound device-state envelope. The type is retained so the event-bus subscriber pattern compiles without modification once a producer is wired.

Go path: `pkg/hmevent/catalogue.go::DeviceStateChangedEvent`.

---

### BD-A1-V13 — `EmitDeviceRemovedEvent` has no production call site

`EventCoordinator.EmitDeviceRemovedEvent` (`internal/central/coordinators/event.go`) publishes a `DeviceRemovedEvent` onto the bus. Device removal in 0.1.0 went through `CentralUnit.RemoveDevice` → `ModelRegistry.Remove` without calling `EmitDeviceRemovedEvent`. WS subscribers for `DeviceRemovedEvent` therefore did not receive the event in production.

This was a known gap for 0.1.0: live device-removal push to SPA clients was not yet wired. Device removal is an infrequent operator action and the SPA handled the stale-device case via periodic refresh. The fix was a one-line addition in `internal/central/central.go` at the device-removal path, deferred to a follow-up. The method was correct and its implementation was covered by a contract test; the call site was the missing piece.

**Status (2026-06): implemented — `DeviceLifecycleSubscriber` subscribes to `DeviceRemovedEvent` and publishes to the WS hub (`internal/north/rest/ws/device_lifecycle.go`), started at boot in `cmd/openccu-loom/daemon_sysstatus.go:41-42`.**

Go path: `internal/central/coordinators/event.go::EmitDeviceRemovedEvent`, `internal/north/rest/ws/device_lifecycle.go::DeviceLifecycleSubscriber`.

---

### BD-A4-BackupCreate — `CreateBackupAndDownload` — implemented

The Python reference routes backup creation through two ReGa scripts (`CREATE_BACKUP_START` and `CREATE_BACKUP_STATUS`) followed by an HTTP download from `/config/cp_security.cgi`. The Go implementation in `CcuBackend.CreateBackupAndDownload` now follows the same path: start via ReGa, poll status via ReGa, then HTTP-GET the archive from `cp_security.cgi`.

Both the ReGa script runner and the HTTP transport are wired into `CcuBackend` by `wireInterface` in `internal/central/adapter/ccu_wiring.go` via `SetScriptRunner` and `SetDownloadFirmwareTransport`. `DownloadFirmware` benefits from the same wiring automatically.

Go path: `internal/client/backends/ccu_extended.go::CreateBackupAndDownload`.

---

### BD-A6-Pipeline — `easymode.Pipeline` has no direct production caller; logic runs inline in `UISchemaAdapter`

`easymode.NewPipeline` and `(*Pipeline).Resolve/Validate/Apply` (`internal/configui/easymode/usecase.go`) aggregate use-cases into a single callable chain. In production, `UISchemaAdapter` (`internal/central/adapter/uischema_adapter.go`) invokes each use-case's methods inline rather than constructing a `Pipeline`. The `Pipeline` type therefore has no production caller.

This is by design: `UISchemaAdapter` integrates schema resolution, cross-validation, and apply in a single pass that interleaves reads from the easymode archive with session-state mutations that the `Pipeline` abstraction would need to thread through via a `ResolveContext`. Composing them as a `Pipeline` would require widening `ResolveContext` or passing additional state between stages. `Pipeline` is retained as a test helper — easymode unit tests use it to exercise `UseCase` implementations in isolation without the full `UISchemaAdapter` dependency graph — and as a future composition point if a second consumer needs the full UC pipeline without the adapter.

Go path: `internal/configui/easymode/usecase.go::Pipeline`, `internal/central/adapter/uischema_adapter.go`.

---

### BD-A3-CombinedSurfaces — combined.HSColor is a north-bound aggregate; WeekProfile uses its own pipeline

`combined.LevelCombined` and `combined.HSColor` are production-wired: both implement `IsCombined()`, are attached via `AttachCalculatedDataPoint` in `custom/cover/blind.go` and `custom/light/color.go` respectively, and the `materialiseCombinedDataPoints` pipeline pass in `device_pipeline.go` (directly after `materialiseCalculatedDataPoints`) bridges them to the event bus via `BridgeCombinedDataPoint`. The MQTT, WS, and REST surfaces (`publishCombinedLevelSensor` / `publishCombinedHSColorSensor`) are complete.

Two scoping notes:

- **`combined.HSColor` is a north-bound aggregate only — it does not feed Matter, and does not need to.** The Matter ColorControl projection for RGB lights is independent and complete: `ColorLight` (which embeds `Light`) projects the `ExtendedColorLight` (0x010D) device type with an HS-mode `ColorControl` (0x0300) cluster server (`hsColorServer` in `custom/light/matter_color.go`), reading and writing the underlying HUE / SATURATION data points directly. `combined.HSColor` is the MQTT/WS/REST aggregate of those same two data points; routing it into Matter as well would double-surface the colour.
- **WeekProfile pipeline**: `combined.WeekProfile` runs via its own dedicated pipeline in `model/weekprofile/` and is not wired through `materialiseCombinedDataPoints`. This is by design — week-profile scheduling has its own coordinator lifecycle and does not fit the per-device combined-DP pattern.

Go paths: `internal/model/combined/`, `internal/central/adapter/combined_bridge.go`, `internal/central/adapter/device_pipeline.go`, `internal/model/custom/light/matter_color.go`.

---

### BD-Matter-SpeakerPartialNoSoundfileNoRepetitions — the sound player is a Speaker minus the two data points that decide what plays

**Decision.** `siren.SoundPlayer` (the HmIP-MP3P channel-2 playback unit)
projects onto the Matter **Speaker** device type `0x0022` with the two clusters
that type mandates — OnOff `0x0006` and LevelControl `0x0008`
(`internal/model/custom/siren/sound_matter.go`). Two of the profile's own data
points are deliberately **not** projected and stay MQTT/REST-only:

| Data point | Go field | Why no cluster |
|---|---|---|
| `SOUNDFILE` | `SoundPlayer.soundfile` (`sound.go`, a `generic.Select` over the device's 256-entry VALUE_LIST) | Matter has no cluster for selecting a stored audio file on a speaker |
| `REPETITIONS` | `SoundPlayer.repetitions` (`sound.go`, a write-only `generic.ActionSelect` over the `REPETITIONS_nnn` labels) | Matter has no cluster for a playback repeat count |

The endpoint therefore reports
`MatterEligibility().State == interfaces.MatterEligibilityPartial`: volume
(LevelControl) and audible/silent (OnOff) map in full, the *content* of the
playback does not. Concretely, `OnOff.On` resumes whatever file the device last
selected and `SoundPlayer.playVolume` writes neither parameter — a Matter
controller has no vocabulary for either, so inventing a value would make the
Matter path change device state that no other surface changes.

**Why this is by design and not a gap.** The Speaker device type mandates
exactly these two clusters and nothing else — matter.js
`packages/model/src/standard/elements/speaker.element.ts:12-19` lists OnOff and
LevelControl with conformance `"M"` and declares no further requirement. There
is no optional Matter cluster left unmounted here: the missing capability has
no cluster to mount at all, in any device type. A projection that faked it
(e.g. mapping soundfile indices onto scene numbers or onto LevelControl's
`OnLevel`) would hand a controller a control that means something different
from what it appears to mean.

Note that ADR 0012's "Out of Matter scope" table still lists
`siren.SoundPlayer` as `Stays MQTT-only` on the ground that Matter 1.5.1 has no
speaker cluster. That row predates the Speaker projection and is now correct
only for the two data points named above, not for the data point as a whole.

**Guard.** `TestSoundPlayerEligibilityIsPartial`
(`internal/model/custom/siren/sound_matter_test.go`) pins the verdict: state
`Partial`, device type `0x0022`, and exactly the two mandatory Speaker
clusters. A future projection that mounted a third cluster, or that flipped the
verdict to full, fails there.

**Retirement condition.** A Matter release that adds a cluster carrying media
selection or a repeat count for a Speaker-class device — or a device type whose
requirements cover them — retires this entry: the two data points then get
projected, the verdict becomes full, and the ADR 0012 row goes with it.

### BD-Visibility-HiddenAliasesIgnored — IsParameterHidden returns the ignore decision

The reference `parameter_decider.py:parameter_is_hidden` computes
`parameter in HIDDEN_PARAMETERS and not un_ignored` — a created-but-hidden
surface distinct from ignored (not-created). OpenCCU-Loom's
`internal/store/visibility/decider.go::IsParameterHidden` currently returns the
same answer as `IsParameterIgnored`.

By design: OpenCCU-Loom does not carry a separate per-DP "hidden but created"
status enum. The created-but-UI-suppressed distinction is expressed through the
`DataPointUsage` mark pipeline (see `BD-Visibility-IgnoredVsNoCreate`), which is
where a consumer decides whether to render a created DP, rather than through a
second decider predicate. Collapsing `IsParameterHidden` onto the ignore
decision is consistent with that split; a future need for the finer distinction
would re-implement it as `inHiddenParameters(p) && !matchesUnIgnore(...)`.
(Re-audit 2026-05-31, finding V2-08.)

### BD-CCU-PatchSingleField — paramset patches apply one field via a closure, plus two additive built-ins

The reference paramset patch carries a `patches: dict[field→value]` and applies
all fields of the matched patch (`store/patches/matcher.py`). OpenCCU-Loom's
`internal/store/patches/patches.go::Patch.Apply` is a single closure that
conventionally mutates one field, and the built-in set adds two patches the
reference does not ship: HM-ES-PMSw1-Pl `ENERGY_COUNTER` unit and HmIP-RGBW
`SATURATION` EVENT-bit.

By design: the closure shape is the Go-idiomatic translation of the Python
dict-application (each built-in patch is self-contained); the two extra
built-ins are additive supersets that correct genuine CCU metadata gaps, not
downward drift from the reference. No reference patch is dropped. (Re-audit
2026-05-31, finding V2-06.)

### BD-Visibility-UnIgnoreMatchingEdges — un_ignore matching diverges from the reference on three edge cases

An adversarial review of the V2-01/V2-02 un_ignore rewrite surfaced three
matching-semantics divergences from the reference
(`store/visibility/parameter_decider.py`). All three are retained deliberately
— the common forms (bare `PARAMETER`, `PARAM:VALUES@MODEL:N` with a concrete
channel) match the reference exactly; only unusual forms differ, with low
real-world impact, and the "correct" target is partly ambiguous because the
reference's own wildcard handling is internally inconsistent.

- **Required-parameter short-circuit (pre-existing).** OpenCCU-Loom returns
  "not ignored" for a required parameter regardless of any ignore rule
  (`decider.go::computeIgnoredValues` leading guard). The reference gates
  `required` only on the static IGNORED/wildcard branch
  (`parameter_decider.py:378-380`), so a required parameter can still be
  suppressed by `IGNORE_PARAMETERS_BY_DEVICE`, event-suppression, or
  `ACCEPT_PARAMETER_ONLY_ON_CHANNEL`. OpenCCU-Loom's "required always surfaces"
  is the original author's documented choice (it predates the re-audit) and is
  retained: a parameter a custom profile declares required should not be hidden
  by a device-level suppression list.
- **Empty / `*` channel is a live "any-channel" wildcard.** A complex entry
  with an empty (`@MODEL:`) or `*` (`@MODEL:*`) channel un-ignores the
  parameter on every channel of the model. The reference leaves these inert:
  its search matrix keys on the literal `UN_IGNORE_WILDCARD = "all"` token while
  the parser stores `*`/`None`, so a `*`/empty channel never matches any lookup
  — the documented `@*:*` wildcard does not actually work in the reference.
  OpenCCU-Loom makes the wildcard honour the user's evident intent ("all
  channels") instead of silently inert. This is a deliberate
  better-than-reference behaviour, scoped to an uncommon entry form.

These were filed as re-audit findings V2-03 (deferred) and the un_ignore
adversarial-review Issues 1–3 (2026-05-31). The matrix for concrete-channel
entries was verified faithful; only the wildcard/empty-channel and
required-parameter edges diverge.

## Per-central behavior toggles — `enable_device_firmware_check` default

The reference stack defaults `enable_device_firmware_check` to **false** —
firmware-update entities are off until the operator opts in. OpenCCU-Loom
defaults it to **true**: the 0.2.0 release shipped per-device firmware-update
entities unconditionally, and flipping the new toggle's default to the
reference value would silently remove those entities from every existing
deployment on upgrade. The toggle still lets operators turn the surface off
(`behavior.enable_device_firmware_check: false`); only the default diverges,
preserving the shipped behaviour. The other eight behavior toggles
(`light_last_brightness`, `use_group_channel_for_cover_state`,
`enable_sysvar_scan`, `enable_program_scan`, `include_internal_sysvars`,
`include_internal_programs`, `sysvar_markers`/`program_markers`,
`delay_new_device_creation`) match the reference defaults.

## Per-class southbound throttles — independent bounded pools in production

`InterfaceClient` exposes three per-RPC-class throttle slots —
`ReadThrottle`, `WriteThrottle`, `ControlThrottle` — so reads are
paced independently of writes. Production wiring
(`internal/central/adapter/ccu_wiring.go` +
`cuxd_wiring.go` via `perClassThrottlePools` in
`internal/central/adapter/throttle_pools.go`) now fills all three
slots with independent bounded pools instead of aliasing them to one
shared pool. Read and write keep an in-flight capacity of 1 (matching
the historic single-pool capacity) but no longer share a permit;
control is sized near-unbounded (capacity 8) so a reconnect storm does
not stall device traffic. Each pool bounds its pending-waiter heap at
4× its capacity (`ThrottleConfig.MaxQueueDepth`) so a stalled CCU fails
non-critical work fast (`ErrThrottleQueueFull` → the retrier's backoff)
instead of growing the queue until the daemon OOMs. The operator's
`command_throttle_inter_command_delay` paces the **write** pool only —
RF duty cycle is a transmit-side concern, so reads and control are not
gated by it.

**Rationale:** The reference stack (aiohomematic) shares one
`InterCommandDelay` across all CCU RPCs, but that couples unrelated
traffic: a write backing off on a CCU `DUTY_CYCLE` fault (tens of
seconds) parked the sole shared permit and blocked every read and
liveness ping behind it. Splitting into independent pools decouples the
classes; combined with acquiring the throttle permit per wire-attempt
inside the retry loop (released before each backoff sleep, see
`InterfaceClient.Call` / `SetValue` / `PutParamset`) and checking the
circuit breaker before taking a permit, a backing-off or shed command
no longer starves independent traffic. Capacities are conservative
(read/write = 1, matching the historic single pool) rather than tuned
ratios; a future operator surface can expose them without an API break.

### BD-Export-OrderedFetchXMLBINOnly — device-definition export reads descriptions over XML-RPC + BIN-RPC only

The device-definition export (`GET
/api/v1/devices/{addr}/export-definition`,
`internal/model/device/definitionexport`) reproduces aiohomematic's
`export_device_definition` byte-for-byte. aiohomematic emits the raw CCU
descriptions in **wire member order** via orjson, so the export reads them
over a dedicated order-preserving path (`InterfaceClient.CallOrdered` →
`xmlrpcCaller`/`binrpcCaller` `CallOrdered` → `internal/orderedjson`)
instead of the normal flatten-to-`map[string]any` caller, which discards
member order.

That ordered path is wired for **XML-RPC and BIN-RPC only**, not JSON-RPC.
This is deliberate: `getDeviceDescription` / `getParamsetDescription` travel
over XML-RPC on every radio/wired interface (`CcuBackend`,
`HomegearBackend`) and over BIN-RPC on CUxD (`CuxdBackend`). The JSON-RPC
channel carries SysVars, programs, messages, install-mode and device names —
never descriptions — so a JSON-RPC ordered exporter would be unreachable
code. If a JSON-only backend that sources descriptions over JSON-RPC is ever
added, the ordered path extends with a `jsonrpcCaller.CallOrdered` plus an
order-preserving JSON decoder; until then it stays unwired.

**Rationale:** mirroring aiohomematic's exact bytes requires preserving the
CCU's wire order, which the existing flatten-to-map caller discards. Adding
the ordered path to a transport that never carries descriptions would add
surface with no reachable caller.

## Device replace — CCU migrates references, energy-counter sysvar rename is a known gap

The guided device-replace workflow (`POST /devices/{addr}/replace`) calls
the interface daemon's `replaceDevice(old, new)` and lets the CCU do the
heavy lifting: rfd / hs485d migrate the direct links, teams and link
paramsets, and ReGa re-binds the existing device/channel objects in place
(same ise-ID), so programs, names, rooms/functions and sysvar channel
bindings survive automatically. Loom therefore does **not** re-implement
any reference migration — it only refreshes its own model (eager swap plus
the CCU's `replaceDevice` callback, which dedups).

**Known gap (WebUI-only step not reproduced):** the CCU WebUI additionally
renames the energy-counter system variables whose *names* embed the device
address (`svEnergyCounter_<chId>_<addr:ch>` / `svEnergyCounterGas_`) for
POWERMETER / POWERMETER_IGL channels, because those are keyed by name, not
ise-ID, and so do not follow the swap. A headless Loom-triggered replace
leaves those name-embedded sysvars pointing at the old address. Porting the
rename (JSON-RPC `SysVar.getValueByName` / `createFloat` / `setFloat` /
`deleteSysVarByName` + `system.saveObjectModel`) is a possible follow-up;
until then it is documented here rather than silently diverging. Loom's own
MQTT auto-counter sysvar discovery has the same name-embeds-address shape
and would need the same treatment.

**Model-guard relaxation:** `DeviceCoordinator.ReplaceDevice` no longer
rejects a model-string mismatch between old and new device. The CCU (rfd /
hs485d) owns the type-compatibility check and legitimately approves
compatible cross-type swaps; rejecting one after the CCU already performed
it would strand Loom's model. A cross-type replace is logged
(`device_coordinator.replace_device.cross_type`) and proceeds.

### BD-Links-ApplyProfileNotTestProfile — the write is called apply, not test

`homematicip_local` exposes the link-profile write as `ws_test_link_profile`
(`custom_components/homematicip_local/websocket_api.py`), whose own docstring
says what it does: "Test a link profile by temporarily applying it. This writes
the profile's default values to the link paramset so the user can observe the
effect." OpenCCU-Loom calls the same operation `links.apply_profile`.

The divergence is from the HA integration, not from the origin, and it corrects
that layer rather than departing from the CCU. The firmware keeps two separate
operations and names them apart:

- `www/config/ic_ifacecmd.cgi` `cmd_set_profile` → `base_put_profile` →
  `putParamset $address $peer` — writing the profile. Its failure message is
  "Fehler beim Speichern des Profils".
- `www/config/ic_ifacecmd.cgi` `cmd_activateLinkParamset` →
  `activateLinkParamset $receiver $sender false` — triggering it so the operator
  can observe the effect. Its messages say the profile was "ausgelöst".

Both concepts already exist here under names that match the firmware:
`links.apply_profile` is the write, `links.activate_paramset` is the trigger.
Folding the write under the word "test" would have made the pair unreadable —
the name would suggest the trigger while the behaviour is the write.

Should the HA integration's naming ever be re-imported wholesale, this rename
must be reapplied.

### BD-Safety-SWDWindowRuleDropped — the reference's `HmIP-SWD → STATE → window` rule is a defect and is not reproduced

`../aiohomematic/aiohomematic/model/data_point_metadata.py:290-303` lists
`HmIP-SWD` in the tuple that maps `STATE` onto `Quantity.WINDOW`, alongside
`HmIP-SWDO`, `HmIP-SWDM`, `HM-Sec-SC`, `HM-SCI-3-FM` and `ZEL STG RM FFK`.
This is not a divergence of taste. The reference row is wrong, and
OpenCCU-Loom does not reproduce it.

**Why it is wrong.** Model matching in that table is a prefix walk on both
sides — `data_point_metadata.py:321-325` uses `startswith`, and this project
uses `strings.HasPrefix`. `HmIP-SWD` is therefore not one model among six: it
is a prefix that covers the whole `HmIP-SWDO*` / `HmIP-SWDM*` family, which the
two entries beside it already cover, *plus* the one device that carries the
name exactly — `HmIP-SWD`, the water sensor. The shorter prefix adds no
contact and one leak detector. `HmIP-SWD` is the standing special case that
must be excluded wherever device rules are matched by prefix, and this row is
what happens when it is not.

Today the row is inert here: the captured `HmIP-SWD` descriptor carries
`ALARMSTATE`, `MOISTURE_DETECTED`, `WATERLEVEL_DETECTED` and the
acoustic-alarm set, and no `STATE`, while `HmIP-SWDM`, `HmIP-SWDM-B2` and
`HmIP-SWDO-I` all declare `STATE`. Inert is not safe: the day a firmware gives
the water sensor a `STATE` parameter, it is published as a window contact on
the plane operators write automations against.

**Where the exclusion lives, and why it takes three assertions.** The rule is
carried in two places that both reach the wire, and neither alone is the
exclusion:

- `internal/parameter/metadata.go` — the domain rule, after the duplicated
  quantity tables in `internal/model/generic` and `internal/parameter` were
  single-sourced. Pinned negatively by `TestBinarySensorQuantityByDeviceAndParam`.
- `internal/north/mqtt/entity_description_rules_binary_sensors.go` and
  `internal/north/mqtt/entity_descriptions_table.go` — the HA-registry rules.
  The second matters more than it looks: `applyEntityDescription` overwrites
  `device_class` from it *after* discovery has set the domain's answer, so a
  row left there reaches the wire regardless of what the domain says.
  Pinned by `TestLookupBinarySensorRuleWindowContacts` and held equal to the
  domain by `TestRegistryBinarySensorClassesAgreeWithTheDomain`.

The third assertion — `resolveBinarySensorDeviceClass("HmIP-SWD", "STATE")` is
empty — exists because the table-level ones are not sufficient. During the
domain-core fold the model-side table re-acquired the shorter prefix, the wire
started answering `window` again, and the table-only assertion stayed green.
The negative must be taken on the path the payload actually travels.

Because this is a reference defect rather than a deliberate difference, a
wholesale re-import of the ported table must drop the row again, and the row
is worth reporting upstream.

## Homegear XML-RPC sysvar hub-wiring removed (dead until backend detection lands)

The XML-RPC sysvar loader for a Homegear-backed central
(`homegear_hub_wiring.go`, `wireHomegearHubIfPresent`) was removed along with
its wiring pin. It was correct code but unreachable: backend classification
(`backends.KindFor`) only ever yields `CCU` or `CUxD`, `backendKind` is never
mutated before `FactoryWithKind`, and `DetectBackend` — the only thing that
would classify a central as Homegear — has no production caller. So
`backendRegistry.homegearBackend()` always returned nil and the wiring was a
guaranteed no-op on every boot.

This matches the standing scope decision (`SPECIFICATION.md` §2.2: *No Homegear
depth-parity*; the backend abstraction exists but full parity is a post-0.1.0
milestone). Homegear support is not partially live — the whole detection path
is unwired, so the hub half was vestigial, not load-bearing.

When Homegear detection is eventually wired, the XML-RPC sysvar path has to come
back with it: Homegear speaks no JSON-RPC, so the CCU hub path
(`WireHub` → `loadSysvars` via `SysVar.getAll`) fails at login and never
populates the hub model. The reference shape is preserved in Git history
(`internal/central/adapter/homegear_hub_wiring.go` before its removal); the
per-sysvar writer routed through the XML-RPC `setSystemVariable` method and left
the create/update/delete mutator nil, which the milestone should restore.

### BD-MQTT-RawGateCoversRawTopicsOnly — `raw_enabled: false` silences the raw plane, not a discovery payload's own state topics

**Decision.** `north.mqtt.raw_enabled` gates the raw topic plane. It does
**not** gate `PublishAlarmState`, `PublishAlarmAvailability`,
`RetractAlarmTopic` or the Security & Safety publisher, and that asymmetry is
deliberate rather than an oversight. Only `PublishAlarmEvent` — a genuinely
raw-plane emission with no HA entity behind it — takes the gate.

**Why.** Those topics are the `state_topic` and `availability_topic` that the
alarm and security entities' own HA-Discovery payloads name. Silencing them
while discovery still declares them leaves every alarm entity in Home
Assistant present and permanently `unknown` or `unavailable` — the operator
sees a full set of controls that never report and never respond. That is the
exact shape the plane round-trip guards exist to catch: **declared and
published have to be the same set** (CLAUDE.md, wiring rule 4).

A blanket gate across every alarm/security publisher was proposed on the
grounds that the flag reads as "no raw topics at all". Taking it would have
traded a naming inconsistency for a broken plane. The honest fix for the
naming is documentation, not behaviour.

**What an operator should expect.** Turning the raw plane off removes the raw
mirror of alarm and security state. It does not remove the Home Assistant
entities, and it does not stop them updating — those ride the discovery
contract, which has its own switch (`ha_discovery_enabled`).

**Retirement condition.** None. If the two switches are ever merged, the merge
has to retract the discovery configs in the same step, or it reintroduces the
declared-but-never-published set this entry exists to prevent.

### BD-Auth-BasicGuessingSharesTheLoginBucket — a failed Basic credential and a failed login draw from one per-IP budget

**Decision.** `GuardBasicAuth` charges an unresolvable HTTP Basic header against
the same per-IP bucket `POST /auth/login` uses, rather than a bucket of its own.
An audit asked for the two to be separated; they are not, and will not be.

**Why.** It is one credential space. An attacker guessing a password can send it
as a Basic header on any API route or as a login body — the work the daemon does
is the same bcrypt verification either way, and the thing being guessed is the
same secret. Two buckets would simply hand a sweep twice the attempts for the
same wall-clock cost, which is the opposite of what a throttle is for.

The concern behind the request was real but different: collateral lockout. A
client that once answered a Basic challenge and still replays a stale credential
would deplete the bucket without any attacker present, and then be unable to log
in. Two things address that without splitting the budget:

- a Basic header the daemon never even attempts to verify — because the scheme
  is administratively off — is not charged at all
  (`basicSchemeDisabled`, `internal/auth/bruteforce.go`);
- the guard is mounted on the `/api/v1` subtree, not on the whole mux. Above it
  sit the SPA, `/app/*`, `/about`, `/health` and the UI bootstrap, and one page
  load is dozens of asset requests. Charging per asset drained the burst of five
  before the page finished rendering. Pinned by
  `TestBasicAuthGuardDoesNotChargeTheSPAMount` (`tests/contract/`), which
  measures the effect through the real router rather than reading the mount
  point out of the source.

**Retirement condition.** If Basic verification ever stops sharing a credential
store with the login route — a separate machine-account realm, say — the shared
bucket stops being justified and should split with it.

### BD-WeekProfile-CombinedStringOverPutParamset — the schedule toggle is written as a combined string, not the firmware's two-member putParamset

The CCU's own weekly-program editor toggles a channel's schedule with an
`Interface.putParamset` on VALUES carrying two members —
`WEEK_PROGRAM_TARGET_CHANNEL_LOCK` as a **string** (the mode name) and
`WEEK_PROGRAM_TARGET_CHANNEL_LOCKS` as an **int**
(`src/webui/www_source/ise/js/iseHmIPWeeklyProgram_AccessReceiver.js:98-99`).
OpenCCU-Loom writes a single `COMBINED_PARAMETER` value instead, in the
`WPTCLS=<bitmask>,WPTCL=<mode>` form
(`internal/model/weekprofile/channel_keys.go` `BuildCombinedParameterValue`).

That string does exist in the firmware, but only in a helper nothing calls —
`getConfigString()` at `:390` of the same file. Checked with a control: a
sibling method of the same object, `getMainHtml`, is invoked five times, so
the search shape does find callers when they exist; `getConfigString` occurs
exactly once, at its own definition. The deployed `www/` tree contains no
`WPTCLS` at all.

The divergence is kept because it is verified where it matters. Writing
`WPTCL=0` selected MANU and `WPTCL=2` selected AUTO on two live devices,
which is evidence about what a device accepts; the dead helper is evidence
about the CCU's web UI, and the two are not the same question. The combined
form is also atomic in one `setValue`, where the putParamset shape needs the
mask and the mode to agree across two members.

What this entry does NOT claim: that the putParamset shape would fail. It was
never tried. If the combined write ever turns out to be rejected by a device
family, the firmware's own shape is the documented fallback and is already
described above.

### BD-WeekProfile-AutoWithResetSkipped — the third WPTCL mode is not offered

`WEEK_PROGRAM_TARGET_CHANNEL_LOCK` declares
`["MANU_MODE", "AUTO_MODE_WITH_RESET", "AUTO_MODE_WITHOUT_RESET"]` with
`MIN = MANU_MODE` and `MAX = AUTO_MODE_WITHOUT_RESET` — unanimously across
all 45 device types in the corpus that carry it, and confirmed by a live read
of an HMIP-PS at `00021BE9957782:6`. Index 1 is therefore inside the declared
range. OpenCCU-Loom's surface is a boolean enable/disable and emits only 0
and 2.

This is not an oversight and not a gap in evidence. The CCU's own editor
comments that option out of its select in both places it builds one
(`iseHmIPWeeklyProgram_AccessReceiver.js:230`, `:280`), and the firmware's own
label says why:

> `stringTableWeekProgramTargetChannelLockAutoReset` =
> "Wochenprogramm: Auto mit Reset (Reset ohne Funktion)" /
> "week program: Auto with reset (reset without function)"

The vendor states the reset has no function, which makes index 1 a no-op
variant of index 2 rather than a third behaviour an operator could choose
between. Offering it would widen the north-bound contract from a toggle to a
three-valued mode in exchange for a choice the manufacturer labels as inert.

Should a device family ever be found where the reset does something, this is
the entry to revisit — and the measurement that would settle it is a write of
index 1 to a named device followed by an observation of the channel's
behaviour, not another read.

### BD-Links-SetLinkInfoUsesTheShortAddressKeys — the rename sends `sender`/`receiver`, not `senderAddress`/`receiverAddress`

`aiohomematic` sends the long address keys for `Interface.setLinkInfo`
(`aiohomematic/client/json_rpc.py`: `_JsonKey.SENDER_ADDRESS = "senderAddress"`,
`RECEIVER_ADDRESS = "receiverAddress"`, used by `set_link_info`). OpenCCU-Loom
sends the short form. This is not a style choice — the long form does not work.

The CCU's own method registry is asymmetric between the getter and the setter
(`www/api/methods.conf`):

```
Interface.setLinkInfo
  ARGUMENTS {_session_id_ interface sender receiver name description}

Interface.getLinkInfo
  ARGUMENTS {_session_id_ interface senderAddress receiverAddress}
```

and `www/api/methods/interface/setlinkinfo.tcl` reads `$args(sender)` /
`$args(receiver)`. The dispatcher builds `args` with
`array set args $JSONRPC(PARAMS)` and then runs `checkArguments`, which tests
`[info exists args($argName)]` for each declared argument. Sending the long
form therefore leaves two declared arguments unset and the call is rejected
before it reaches the interface process.

Observed as a user-facing failure: renaming a direct link on an HmIP-WRC6
returned `502` from `PATCH /devices/{addr}/links`. It is not an encoding
problem — the name's characters never matter, because the call fails on its
argument list.

`GetLinkInfo` keeps the long form, because that is what its own entry
declares. The two must not be unified.

The same sweep found four more of these, all in the sysvar creators:
`SysVar.createEnum` declares `valList` (we sent `valueList`), and the hub
writer sent `chn_id`, `min_value`, `max_value` where the registry declares
`chnID`, `minValue`, `maxValue`. `TestJSONRPCCallsUseTheDeclaredArgumentNames`
now pins the whole set.

Our `AddLink` / `RemoveLink` are unaffected: they go over XML-RPC with
positional arguments, where no parameter name exists to get wrong. That is
also why link creation worked while renaming did not.

### BD-Timer-PromotionTruncates — a promoted combined-timer duration is truncated toward zero, not rounded

**Decision.** `custom.EncodeTimerDuration`
(`internal/model/custom/mixins.go`) promotes a duration past the 16343
threshold by plain float division and then casts to `int32`, which truncates
toward zero. `16373 s` becomes `(272, M)`, not `(273, M)`. This matches the
CCU-side reference end to end and is deliberate.

**Why — where the reference truncates.** The promotion helper itself does not
round; it returns a float:

- `aiohomematic/aiohomematic/model/custom/mixins.py:305-332` —
  `recalc_unit_timer` does `time /= 60` and returns `(float, unit)`.

The fraction is lost one layer down, at the write boundary, because
`DURATION_VALUE` is an INTEGER parameter and Python's `int()` on a float
truncates toward zero:

- `aiohomematic/aiohomematic/parameter_tools.py:298-299` —
  `if param_type == ParameterType.INTEGER and isinstance(value, float): return int(value)`
- `aiohomematic/aiohomematic/model/support.py:605` —
  `if target_type == ParameterType.INTEGER: return int(float(value))`

So the reference's two-stage float-then-truncate is observationally identical
to this tree's single `int32(...)` cast, and the question "round or truncate?"
is settled: truncate.

**Guard.** `TestEncodeTimerDurationTruncatesTowardZero`
(`internal/model/custom/custom_test.go`) uses a duration whose promoted value
has a fraction above `.5`, so rounding and truncation disagree — the existing
`16344 s → 272.4 → (272, M)` case cannot tell them apart.

**Retirement condition.** If the reference ever rounds at the write boundary,
or `DURATION_VALUE` stops being an INTEGER parameter, this entry and the guard
go with it.
