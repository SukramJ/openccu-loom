# by_design.md — Intentional Architecture Divergences Go ↔ Reference

**As of:** 2026-05-10
**Purpose:** Catalogue of all intentional structural divergences between OpenCCU-Loom (Go) and its two reference implementations:

- **CCU side** — [aiohomematic](https://github.com/SukramJ/aiohomematic) (Python). Sections §1+ below carry the original aiohomematic-vs-Go content.
- **Matter side** — [matter.js](https://github.com/project-chip/matter.js) HEAD. ([home-assistant-matter-bridge](https://github.com/Nabu-Casa/home-assistant-matter-bridge) is a supplementary read for bridge composition patterns but **not** a gold standard — it carries HA-specific shims that do not translate to OpenCCU-Loom.) See section §"Matter / matter.js Divergences" near the end.

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
ParameterStatus enum. (Parity audit 2026-05-30 C-07 — reclassified.) The
one case not yet covered — a *sensor* reporting `STATUS=UNKNOWN` to
invalidate a reading with no optimistic write in flight — is a narrow
follow-up that needs golden-replay evidence that HM devices use `_STATUS`
that way.

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
| M5050 | `ClientCoordinator._primary_client` cache + `_clients_started` flag | central/coordinators/client.py | `PrimaryClient()` recomputed from sorted list; `AllClientsActive()` as read proxy | `internal/central/coordinators/client.go` | Avoid caching; deterministic re-computation from the registry list is sufficient (at most N=8 interfaces) | W6/Wave-E |
| M5051 | `ClientCoordinator.poll_clients` filter | central/coordinators/client.py | `DataPoint.NoPushUpdates` flag per-DP via `internal/model/generic/datapoint.go` | (cross-cutting) | Push capability is a DataPoint property, not a client property; modelling it per-DataPoint is closer to wire behaviour and decouples the coordinator from DP details | W6/Wave-E |
| M5052 | `ClientCoordinator.wait_for_tcp_ready()` | central/coordinators/client.py | `ConnectionRecoveryCoordinator.RecoveryStageTCPChecking` as own recovery stage | `internal/central/coordinators/connection_recovery.go` | TCP readiness is a recovery concern, not a client-init concern; a stage-based pipeline with classification is cleaner than a client method call | W6/Wave-E |
| M5053 | `ClientCoordinator._on_health_record_event` | central/coordinators/client.py | Health tracking in `internal/health/connection.go`; coordinator status is not driven by health events | `internal/health/` | Health subscription on a pure-registry coordinator is a layer violation; health lives in its own package and propagates via dedicated events | W6/Wave-E |

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
| `CacheClearer` | `ccu.cache_clear` | ditto |
| `DeviceStatisticsQuery` | `ccu.device_statistics` | ditto |
| `FirmwareRefresher` | `firmware.refresh` | ditto |
| `IncidentClearer` | `incidents.clear` | ditto |
| `ExtendedHub` | `service_messages.disable` | ditto |
| `ParamsetReader` | `paramset.copy` | ditto |

**Rationale:** The Svelte SPA has REST counterparts for all operations relevant to it (`/install-mode`, `/devices/{addr}/firmware/update`, `/devices/{addr}/channels/{no}/config/export`, `/devices/{addr}/links`, `/sessions/edit/*`, `/incidents`, `/audit`, `/metrics`). The WS command frames are a second address space that OpenCCU-Loom does not actively maintain. If someone wanted to run the `homematicip-local-frontend` Lit SPA against OpenCCU-Loom, the bridge would need to wire the `nil` providers.

**Marked:** WS API cleanup 2026-05-05.

---

## Matter / matter.js Divergences

### BD-Matter-TLVPermissive — responder TLV decode is more permissive than chip

connectedhomeip enforces container/tag strictness on every element via the
always-on `VerifyElement` (`src/lib/core/TLVReader.cpp`) and rejects
unordered Sigma/PASE optional fields, trailing bytes after the top-level
container, and missing-mandatory post-decrypt fields. OpenCCU-Loom's live
`Decoder.Next()` (`internal/north/matter/tlv/`) enforces these only via the
opt-in `Validate()`, which the Sigma/PASE decoders do not call.

**Rationale:** OpenCCU-Loom is a pure Matter **responder**; real
controllers (Apple Home, Google Home, chip-tool) emit spec-valid TLV, so
the permissive path is never reached on the wire — confirmed by the
byte-aligned crypto/tag/status verification in the 2026-05-30 audit. The
strictness is a fuzz/robustness backlog (parity audit CHIP L9-01..04), not
an interop gap. The crypto core (HKDF salts/info/nonces, Sigma TLV tags,
status codes, control octets) IS byte-verbatim with connectedhomeip HEAD.

### BD-Matter-mDNSDeviceType — commissionable DT advertises RootNode, not Aggregator

The commissionable `DT` TXT key + `_T<id>` subtype advertise RootNode
(0x0016) rather than the Aggregator application device-type (0x000E) that
connectedhomeip's `Dnssd.cpp` would emit (`cmd/openccu-loom/daemon.go`).

**Rationale:** empirical Apple-pairing observation — recorded here so the
audit does not re-flag it (parity audit 2026-05-30 CHIP-mDNS F2). The wire
diff that motivated it should be attached when next reproduced.

> **Rule of thumb (CLAUDE.md):** matter.js HEAD is the gold standard for everything under `internal/north/matter/`. Cluster IDs / revisions / attribute IDs / constraints / defaults / wire shape are taken verbatim. Any item below is a **deliberate** divergence with a documented reason. Bug-class drift (hand-coded revisions etc.) does **not** belong here — it belongs in a fix.

### Idiomatic translations TypeScript → Go (not real divergence)

| matter.js pattern | Go idiom in OpenCCU-Loom | Files |
| --- | --- | --- |
| `Behavior.with(BasicInformationServer, ...)` Mixin | Concrete struct `core.BasicInformation` with the same fields + `MatterRead`/`MatterWrite`/`MatterAttributes` methods | `internal/north/matter/cluster/core/basic_information.go` |
| `Promise<T>` async chain | Goroutine + `context.Context` + return value | every I/O method |
| `@matter/types` `TlvSchema<T>` derived from class fields | `tlv.Encoder` / `tlv.Decoder` with explicit `PutUint16` / `PutUint32` per spec-typed slot | `internal/north/matter/tlv/encode.go` |
| Decorators (`@validate`, `@quality(...)`) | Comment + manual constraint check at write boundary | `cluster/core/*_information.go::MatterWrite` |
| Behavior-state proxies via Reflect | Direct struct fields under `sync.RWMutex` | every cluster server |

These rewrite TypeScript constructs into Go. Wire output is identical; the surface API differs because Go does not have decorators or Reflect-based mixins.

### Production deviations (require ADR if architectural)

| # | matter.js source | OpenCCU-Loom deviation | rationale |
|---|---|---|---|
| L4-1 | `packages/model/src/standard/elements/root-node-device.element.ts` — Revision 4 | `cmd/openccu-loom/daemon.go:1995` `{DeviceType: 0x0016, Revision: 3}` | Apple-Home-Bypass: Revision 4 löste pre-2026-05-09 `HAPErrorDomain Code=24` aus (siehe `docs/matter/apple_pair_status_2026-05-10.md`). Gilt bis Apple's HAP-Mapper aktualisiert ist. |
| L3-PFAD-1 | `packages/protocol/src/session/case/Fabric.ts` — FabricLabel default `""` | `internal/north/matter/cluster/core/operational_credentials.go:688` `Label: "openccu-loom"` | Apple Home rejectet leeres Label nach CommissioningComplete mit RemoveFabric. Workaround setzt einen non-empty Label; Commissioner kann via `UpdateFabricLabel` umbenennen. Spec-konform (max 32 printable bytes). |
| L3-PFAD-2 | `packages/node/src/behaviors/basic-information/BasicInformationServer.ts:88` — reactTo emitter für Reachable | `internal/north/matter/cluster/core/basic_information.go:279` returns `true, true` hardcoded | Root-Endpoint ist der Bridge-Daemon selbst; während daemon läuft ist die Bridge per definitionem reachable. Kein ReachableChanged-Event nötig. |
| L3-PFAD-3 | `BasicInformationServer.ts:110-127` — StartUp/ShutDown/Leave events emittiert | `internal/north/matter/cluster/core/basic_information.go` — kein Event-Emitter | Optional-Events (`conformance: "O"`); deferred bis v1.2. Bridge-State-Tracking läuft über mDNS + commissioning-window. |
| L3-PFAD-4 | `GeneralCommissioningServer.ts:54` `this.state.breadcrumb = 0` in `initialize()` | `internal/north/matter/cluster/core/general_commissioning.go:138` — Go struct zero-value | Funktional äquivalent (Go zero = 0); idiomatisch in Go. |
| L3-PFAD-5 | `NetworkCommissioningServer.ts` — ScanNetworks per WiFi/Thread feature | `internal/north/matter/cluster/core/network_commissioning.go:183` — rejectet mit UnsupportedCommand | Bridge ist Ethernet-only. Per Matter §11.9 ist ScanNetworks nicht mandatory für ETH; UnsupportedCommand ist spec-konform. |
| L3-PFAD-6 | `GeneralCommissioningServer.ts:67-82` — auto-armiert 60 s ArmFailSafe bei jeder neuen PASE-Session | `internal/north/matter/cluster/core/general_commissioning.go::AutoArmOnPaseEstablished` — implementiert, optionaler Hook | Spec §11.10 stellt ArmFailSafe-Pflicht auf den Commissioner. `AutoArmOnPaseEstablished` ist ein defensives Sicherheitsnetz das der Daemon via PaseAdapter.onEstablished einweben kann; es ist ein No-op wenn der Commissioner bereits explizit ArmFailSafe gerufen hat. Alle bekannten Commissioner (chip-tool, Apple Home, HA) callen ArmFailSafe explizit; der Hook macht das robuster. |
| L4-TagList | `Descriptor` — TAGLIST feature, conformance `"desc"` | `internal/north/matter/cluster/core/descriptor.go` — leere Liste | Optionales TAGLIST-Feature. Apple Home / chip-tool fragen es im Standard-Pair-Flow nicht ab. Erweiterbar wenn ein Commissioner es benötigt. |
| L5-FullyQualified | `packages/types/src/tlv/TlvCodec.ts::writeTag` — FullyQualified48 schreibt `profile(uint32)+id(uint16)` | `internal/north/matter/tlv/encode.go::Tag` — `Vendor(uint16)+Profile(uint16)+Number(uint16)` per Matter Core Spec §A.7.3 Table 74 | Spec-konform (matter.js conflated profile+vendor in uint32). Kein Live-Impact: keine IM-Message emittiert FullyQualified-Tags. Decoder akzeptiert beide Shapes. |
| L6-PFAD-1 | `packages/protocol/src/interaction/SubscriptionHandler.ts:284` — per-subscription timer mit `sendInterval = max(0.8 × maxInterval, minIntervalFloor)` → 24–48 s | `internal/north/matter/im/subscription/subscription.go:133` `sendIntervalLocked()` + `engine.go:28-35` shared 250 ms ticker | OpenCCU-Loom implementiert die matter.js-Formel `min(maxInterval/2, max(minFloor, 0.8 × maxInterval))` korrekt; der hard-cap 5 s aus früheren Implementierungen ist entfernt. Architektur-Drift: matter.js nutzt per-subscription Timer, OpenCCU-Loom nutzt einen shared 250 ms Ticker — funktional äquivalent, Go-idiomatischer. |
| L6-PFAD-2 | insertion-order via behavior-layer attribute-map | `internal/north/matter/endpoint/dispatcher.go:253` — numerisch sortiert | Spec stellt keine Ordering-Garantie; Commissioners dürfen sich nicht auf eine bestimmte Reihenfolge verlassen. |
| L6-PFAD-3 | `IncomingInteractionClientMessenger.readDataReports:881` — sendet `Status.Success` per chunk | `internal/north/matter/bridge/subscribe.go::handleSubscribeRequest` — burst-sendet ohne intermediate Status | Apple Home akzeptiert Burst (empirisch 2026-05-09); chip-tool akzeptiert beides. Kein wire-impact heute. |
| L6-PFAD-4 | `InteractionServer.ts` — erlaubt Endpoint-Wildcard für bestimmte Cluster | `internal/north/matter/endpoint/dispatcher.go::Invoke` — rejectet `!path.HasEndpoint` | Kein OpenCCU-Loom-Cluster benötigt Endpoint-Wildcard-Invoke heute. |
| L7-2 | testet gegen NIST SP 800-38C reference vectors | `internal/north/matter/secure/aesccm/aesccm_test.go` — round-trip-only | End-to-end via Apple-Pair-Audit validiert (Sessions establish, ACL-Write commits). Symmetrisches Seal+Open-Bug wäre theoretisch maskiert; pragmatisch akzeptables Restrisiko. |
| L8-IPv4+IPv6 | `MdnsService.ts` — advertised auf allen Interfaces | `internal/north/matter/mdns/zeroconf.go::primaryHostIPs` — IPv4-first + ein routable IPv6, einziges Interface | Apple-Home-friendly (Apple wählt das erste advertisierte Interface); Bridge auf Multi-Homed-Server würde sonst inkonsistente Pfade emittieren. Dokumentiert in Code-Comment. |
| D-9 | matter.js MatterDefinition trackt Matter Core 1.5.1 — kein Cluster 0x0024 (Schedules) | `internal/north/matter/cluster/wire/schedules.go` existiert; `internal/model/custom/climate/matter.go:190-199` mountet ihn nicht | Schedules Cluster ist pre-publication/draft/dropped. Apple Home's HAP service mapper rejectet Endpoint-Clusters, die nicht in matter.js stehen, mit `HAPErrorDomain Code=24`. Code bleibt für spätere Re-Aktivierung erhalten. |
| chip-tool C6 / F3 Voll-Wildcard | matter.js / chip-tool akzeptiert `(cluster=0xFFFFFFFF, attr=0xFFFFFFFF, ep=0xFFFF)` | `internal/north/matter/bridge/subscribe.go` + `endpoint/dispatcher.go` emittieren ~3000 Reports problemlos | chip-tool exit=1 bei Voll-Wildcard ohne Bridge-side-Fehler. Vermutet UDP-fragment- oder chip-tool-internes Buffer-Limit gegen unsere Bridge-Größe. Workaround: Cluster-spezifische Wildcards (`(cluster=0x001D, attr=*, ep=*)` oder `(cluster=*, attr=*, ep=0)`) — die laufen sauber. Kein Bridge-Fix möglich; Drift ist commissioner-side. |
| L3-PFAD-7 | `packages/node/src/behaviors/operational-credentials/OperationalCredentialsServer.ts` — `Fabrics`/`NOCs` attributes backed by in-memory fabric state | `internal/north/matter/cluster/core/operational_credentials.go` — live-reads from SQLite store on every attribute access | Avoids stale-read races when a second commissioner modifies the fabric list concurrently. Functionally equivalent: the spec requires the attribute to reflect the current fabric list; reading from the authoritative store (not a cache) guarantees freshness without an explicit invalidation mechanism. |
| L4-PFAD-1 | `packages/node/src/endpoints/aggregator.ts` — `AggregatorEndpointDefinition.deviceType = 0xe`, device-type filter exposed via mDNS `_T14._sub._matterc._udp.local` | `internal/north/matter/mdns/service.go::BuildCommissionableService` — `DT` TXT key + `_T<deviceTypeID>` subtype hardcoded to `0x000E` (Aggregator) for the bridge | Apple Home's commissioning flow uses the `DT` hint to render a bridge-type icon in the pairing dialog. The bridged endpoints expose their own device types via Descriptor.DeviceTypeList after pairing; the commissionable record advertises the Aggregator (bridge top-level) type only. |
| L7-PFAD-1 | `packages/node/src/behaviors/administrator-commissioning/AdministratorCommissioningServer.ts` — `windowStatus` derived from internal commissioning state machine | `internal/north/matter/cluster/wire/admincommissioning.go` + `internal/north/matter/bridge/commissioning_window.go` — `windowStatus` driven by a separate `Bridge.WindowController` | Decouples cluster read path from commissioning logic. The cluster server reads window state from the controller interface; the controller owns the timer and PASE verifier lifecycle. This is a Go-idiomatic separation of concerns (hexagonal architecture) that produces identical wire output. |
| L5-Apple-Width | `packages/types/src/tlv/TlvCodec.ts::encodeTlv` — SubscriptionID and DataVersion encoded as `TypeUnsignedInt` with magnitude-driven byte width (1/2/4 bytes) | `internal/north/matter/tlv/encode.go::PutUint32` — always encodes as explicit 4-byte unsigned integer | Apple MTRDevice rejected the magnitude-driven 1-byte encoding of SubscriptionID values below 256, causing Subscribe-Initial to be dropped with no error. The 4-byte explicit form is valid per Matter Core Spec §A.7.1 Table 74 (`UInt32` tag) and accepted by all tested commissioners (Apple Home, chip-tool). Verified via pair-debug session. |
| L3-FQ64-Decoder | `packages/types/src/tlv/TlvCodec.ts:175-176` — `default: throw new NotImplementedError("Unexpected tagControl …")` — FullyQualified64 (TagControl 7) falls to the default throw branch; matter.js never decodes FQ64 tags | `internal/north/matter/tlv/decode.go::readTag` — `case TagKindFullyQualified8:` reads vendor+profile+uint32 tag number per Matter Core Spec §A.7.3 | OpenCCU-Loom is more spec-compliant than matter.js for this tag form. FullyQualified64 is a valid Matter TLV tag class and chip's TLVReader.cpp supports it. No live impact: no IM message in a standard bridge exchange uses FQ64 tags. By design: OpenCCU-Loom mirrors the spec; matter.js omits it. |
| L3-ContainerTypeValidation | `chip TLVWriter.cpp:686-699` — `WriteElementHead` returns `CHIP_ERROR_INVALID_TLV_TAG` when a context-specific tag is used outside Structure/List, or a non-anonymous tag inside Array | `internal/north/matter/tlv/encode.go::writeControlAndTag` — emits tag bytes unconditionally; no container-type stack | Defensive-coding gap: chip rejects wire-invalid tag/container combinations at write time; OpenCCU-Loom does not. All struct-building call sites in `bridge/reply.go` and cluster servers are hand-typed and correct, so no active interop breakage exists. A container-type stack will be added if a fuzz regression surfaces the gap; until then the cost-benefit does not justify the overhead. |
| L3-ImplicitProfile-Decode | `packages/types/src/tlv/TlvCodec.ts:170-172` — `case TagControl.ImplicitProfile16: case TagControl.ImplicitProfile32: throw new NotImplementedError(…)` | `internal/north/matter/tlv/decode.go::readTag` — silently decodes ImplicitProfile tags as raw `Tag{Kind, Number}` without profile resolution | OpenCCU-Loom acts only as a TLV responder (never as an initiator that would need to generate ImplicitProfile tags); no incoming IM message in a standard commissioning or subscription exchange uses ImplicitProfile tags. Profile resolution (chip's `ImplicitProfileId` pattern) is deferred until a code path that needs it materialises. matter.js throws; OpenCCU-Loom decodes without error and without resolution — both are non-breaking divergences from chip's conditional resolution. |

| L7-D03 | matter.js `MdnsAdvertisement.ts:153` — MAC-derived hostname `<12hexUC>0000.local`; chip `ServiceNaming.cpp:89` `MakeHostName` from MAC/EUI-64 | `internal/north/matter/mdns/service.go::defaultHostName` — uses OS hostname (with `.local` stripped); `zeroconf.go:189-194` — falls back to `os.Hostname()` | OpenCCU-Loom runs as a daemon on Linux/macOS where the OS mDNS responder (avahi / mDNSResponder) already owns the A/AAAA record for the OS hostname. Using the OS hostname lets the OS-registered record resolve the SRV target without a second A/AAAA advertisement from the bridge. A MAC-derived label is the recommended Matter approach but requires the bridge to also register the A/AAAA record itself (outside the scope of the current mDNS layer). `Config.HostName` can be set explicitly when a MAC-derived label is preferred. `MACAddress [6]byte` field can be added in a future iteration. Drift L7-D03 (LOW). |
| BD-CCU-IsConnected | `interface_client.py:841-866` — `is_connected()` runs an active RPC ping via `check_connection_availability()`, increments `_connection_error_count` on failure, transitions CONNECTED → DISCONNECTED + `_mark_all_devices_forced_availability(FORCE_FALSE)` when count exceeds `connectivity_error_threshold` | `internal/central/jobs.go:328-372` — `central.check_connection` job polls every 120 s; publishes `ConnectionLostEvent` immediately on `StateMachine.State() != Connected` OR `!IsCallbackAlive()`, recovery coordinator's `alreadyActive` guard dedupes subsequent firings, `WireDeviceAvailability` (`internal/central/adapter/device_availability.go`) propagates the `ClientStateChanged → Disconnected` transition to per-device Force-False | openccu-loom's BIN-RPC push-event architecture inverts the responsibility: the bridge does not need an active probe because callback events are the primary liveness signal. The check-connection job validates the state machine + callback-alive flag instead of issuing an RPC ping. The error-count threshold from aiohomematic's `is_connected()` is replaced by the `alreadyActive` deduplication inside the recovery coordinator (`triggerRecovery`) — a single-tick glitch does not start a second recovery cycle. By design: equivalent end-state (FORCE_FALSE on persistent failure) reached via a different control loop. (Drift D-02 from `audit_runs/2026-05-18_resolution_status.md`, classified as PFAD-ASYMMETRIE on re-analysis.) |
| BD-CCU-Fault-Timeout-Retryable | `client/command_retry.py:50-56` — `_RETRYABLE_FAULT_CODES = {-1, -8, -9, -10}`; fault -2 (XML-RPC generic Timeout) is NOT retried | `pkg/hmerr/errors.go::IsRetryable` — adds `-2 XMLRPCFaultTimeout` to the retryable set with the comment "emitted by transports as a generic timeout fault" | OpenCCU-Loom's transports surface generic socket-timeout / read-deadline failures via XML-RPC fault code -2; the reference Python stack maps those to a different exception type (`asyncio.TimeoutError`) that gets handled outside the fault-code retry path. Treating -2 as retryable is a Go-side equivalence path, not a behavioural deepening: both stacks retry transient transport timeouts; only the dispatch shape differs. (Drift D-09 from `audit_runs/2026-05-18_resolution_status.md`.) |
| BD-Matter-UniqueID-Stable | matter.js `BasicInformationServer.createUniqueId()` — 32-char random persisted with Quality "FN" so the value survives bridge restarts | `internal/north/matter/cluster/core/basic_information.go::uniqueID` mixes `bootid.Salt()` into a deterministic SHA-256 derivation; `internal/north/matter/bootid/bootid.go` defaults `rotationEnabled = false`, so `Salt()` returns `[16]byte{}` and the hash collapses to a stable function of vendor/product/nodeLabel/serialNumber | OpenCCU-Loom produces a stable UniqueID across daemon restarts by default. Rotation is opt-in via `matter.dev_rotate_unique_ids=true` and is intended for the dev/debug workflow where pair-iteration has corrupted Apple's HMHome state. Audit drift L1-D19 (in `audit_runs/2026-05-18_resolution_status.md`) is a **false positive**: it assumes `bootid.Salt()` rotates unconditionally, but the default zeroed salt means the production daemon's UniqueID is stable. The bootid package docstring spells out the contract; no code change required. |
| BD-CCU-FieldNaming | `aiohomematic/model/custom/field.py` — `CustomDataPointField` with descriptor-style attribute names | `internal/model/custom/profile_schema.go` — `FieldValue` struct, semantically equivalent | Different module/type names reflect the Python-descriptor vs. Go-struct idiom split (cross-referenced in §A2 of this doc). Naming divergence only; behavioural parity verified by the custom-DP materialisation tests. (Audit drift D-12.) |
| BD-CCU-PatchParameter | (no Python pendant) | `internal/central/coordinators/configuration.go` — `PatchParameter`, `ClearPatch` | Go-side extension: lets operators apply a runtime parameter patch (e.g. fix a vendor-side typo in a paramset description) without restarting the daemon. The reference Python stack requires a paramset-cache reload or a code-side patch file. No drift downward — Go strict-superset. (Audit drift D-13.) |
| BD-CCU-PublishBatch | `central/events/bus.py:761` — `publish_batch` fans out via `asyncio.gather` so handlers run concurrently | `internal/central/events/batch.go` — `events.Batch.Flush()` publishes sequentially in batch-add order | Sequential flush gives deterministic ordering across batched DataPointValueChanged events at the cost of one extra wall-clock tick when handler counts are large. The reference behaviour is faster but loses ordering across concurrent handlers; for our north-bound consumers (MQTT, Matter, REST) ordering is more valuable than µs-scale latency. Sequential dispatch also avoids fan-out re-entrancy footguns in the Go event-bus (no goroutine-pool-managed concurrency). (Audit drift D-14.) |
| BD-CCU-CB-MultiListener | `client/circuit_breaker.py` — single `_state_change_callback` slot | `internal/client/reliability/circuit.go` — `listeners []func(from, to CircuitState)` via `AddOnStateChange` | Go-side extension: lets the breaker fan state-change events out to multiple coordinators (metrics, recovery, health tracker) without forcing one coordinator to multiplex for the others. Strict superset of the reference contract — a single `AddOnStateChange` call lands the same behaviour as Python's single slot. (Audit drift D-17.) |
| BD-CCU-ConnectivityThreshold | `const.py:95` — `connectivity_error_threshold` (default 1) consumed by `is_connected()` | (no Go consumer) | Subsumed by `BD-CCU-IsConnected` above: OpenCCU-Loom's push-event architecture replaces the threshold-based active-probe logic with a `StateMachine.State() != Connected` / `!IsCallbackAlive()` check inside the `central.check_connection` job. The configuration key is reserved in the YAML schema but unused at runtime; surfacing it would be a no-op. (Audit drift D-11.) |
| BD-Matter-TimeSync-NoUTCFeature | matter.js `time-synchronization.element.ts:32-35` — `UtcTime` and `Granularity` both carry `conformance: "M"` unconditionally; no `UTC` feature flag exists | `internal/north/matter/cluster/core/time_synchronization.go::MatterRead` returns `FeatureMap = 0` while exposing UTCTime + Granularity | The matter-exhaustive audit drift L1-D14 ("UTC-Feature-Bit nicht gesetzt obwohl UTCTime exponiert wird") is a **false positive**: it presumes a non-existent UTC feature flag. UTCTime and Granularity are mandatory regardless of any feature flag; the optional TZ / NTPC / NTPS / TSC features gate additional attributes (TimeZone, DefaultNtp, TrustedTimeSource, …) that the bridge intentionally does not implement. `FeatureMap = 0` is therefore correct — the bridge advertises no optional time-sync features, which matches the implemented surface. |
| BD-Matter-Dispatcher-Synthesises-Globals | matter.js's behaviour layer auto-generates the Matter §7.13.2 global attributes (FeatureMap, ClusterRevision, AttributeList, AcceptedCommandList, GeneratedCommandList) on every cluster server | `internal/north/matter/endpoint/dispatcher.go::attributesFor` seeds the five universal globals at the start of every wildcard-attribute expansion and dedupes them against the per-server `MatterAttributes()` extras (lines 269-300 + 378-405) | The audit drifts L1-D04 / L1-D06 / L1-D12 ("MatterAttributes lister fehlt FeatureMap + ClusterRevision") are **false positives**: per-cluster `MatterAttributes()` enumerations focus on cluster-specific attributes; the dispatcher universally adds the five Matter-1.3 globals (EventList intentionally omitted — Apple iOS Matter SDK schema-mismatch reject). Regression tripwire: `TestRead_WildcardAttribute_ListerWithoutGlobalsStillGetsThem`. The wildcard-expansion contract is centrally enforced; per-cluster servers neither need nor benefit from listing globals again. |
| BD-Matter-EndpointID-Persistent | matter.js `@matter/node` Storage-Layer persists endpoint IDs across restarts so commissioners can re-use cached subscription targets | `internal/north/matter/endpoint/assembler.go::assignOrReuseID` looks the (source-key → endpoint-id) record up in the `store.EndpointStore` (SQLite) and reuses the persisted ID; a fresh allocation only happens when no record exists for that source key | Audit drift L6-D01 ("Endpoint-IDs werden beim Reassemble potenziell neu vergeben … bootid-basiert") is a **false positive**. Endpoint IDs come from the persistent endpoint store (not from `bootid.Salt()` — that one only feeds the UniqueID hash, see `BD-Matter-UniqueID-Stable`). A reassemble that finds the same source key reuses the same endpoint ID, so Apple's HAP-Mapper does not see a topology shuffle. |
| BD-Matter-Reassemble-Closes-Removed | matter.js packages/protocol/src/interaction/SubscriptionHandler.ts — when a BridgedNode endpoint is removed from the Aggregator the `endpoint.lifecycle.remove()` path causes any subscription targeting that endpoint to be closed by the InteractionServer | `internal/north/matter/im/subscription/manager.go::CloseEndpoint` terminates only the subscriptions whose paths reference the removed endpoint ID (via `subReferencesEndpoint`) — subscriptions on the other 29 of 30 endpoints stay open | Audit drift L6-D08 ("Reassemble closes all subscriptions; matter.js does partial update") is a **false positive**: it conflates "all subscriptions targeting the removed endpoint" with "all subscriptions in the bridge". `CloseEndpoint(endpointID)` is targeted; the partial-update story holds. matter.js's "partielles Endpoint-Update" applies to attribute reports on existing endpoints, not to endpoint removal. |
| BD-Matter-SetReachable-LockReleased | matter.js BridgedDeviceBasicInformationServer emits ReachableChanged through its async `events.reachableChanged.emit` EventEmitter pattern | `internal/north/matter/cluster/core/bridged_device_basic_information.go::SetReachable` releases the internal `b.mu` **before** calling `emitter.MatterEmitEvent` (lines 438-455) so the emit path cannot deadlock against a Subscribe-Manager mutex acquired downstream | Audit drift L10-D06 ("ReachableChanged-Event synchron emittiert; Deadlock-Risiko wenn unter Lock") is a **false positive**: the emit runs after the unlock; there is no nesting of `b.mu` and any downstream lock. The synchronous-vs-asynchronous distinction is purely an internal-dispatch shape; observable behaviour is identical. |
| BD-Matter-DN-TXT-Set | matter.js `MdnsBroadcaster.ts::buildCommissionableInstanceData` emits the `DN` TXT key when a device name is supplied | `internal/north/matter/mdns/service.go::BuildCommissionableService` emits `DN` when `cfg.DeviceName != ""`; `internal/north/matter/bridge/bridge.go:1156` sets `DeviceName: params.NodeLabel` so the bridge's NodeLabel surfaces as the DN value | Audit drift L7-D03 ("DN TXT-Key fehlt im commissionable Record") is a **false positive**: the DN key is already wired through the bridge's parameter pipe; an empty DeviceName (and therefore an omitted DN) only happens when the bridge is started without a NodeLabel, which is rejected by `Config.Validate()`. |
| BD-Matter-PBKDF2-Documented | matter.js `PasePairing.ts` makes the PBKDF2 iteration count configurable; spec default is 1000 (Matter §3.10) | `config.example.yaml:240` documents `iterations: 1000` with the spec-mandated 1000..100000 range comment | Audit drift L5-D02 ("PBKDF2-Iterationsanzahl nicht in config.example.yaml dokumentiert") is a **false positive**: the annotated value is already present. `internal/north/matter/cluster/wire/admincommissioning.go:403-404` enforces the spec floor/ceiling at runtime. |
| BD-Matter-CriticalEventBypassMin | matter.js `ServerSubscription.ts:281` — "Urgent events are sent immediately" — events tagged urgent bypass the MinIntervalFloor gate | `internal/north/matter/im/subscription/subscription.go::drainEventsIfElapsed` (lines 235-258) sets `hasCritical = true` for any event with `im.EventPriorityCritical` and short-circuits past the MinIntervalFloor check | Audit drift L10-D02 ("EventPriorityCritical als Bypass-Kriterium vs. matter.js 'urgent flag'") is functionally equivalent: OpenCCU-Loom's enum value is the equivalent of matter.js's urgent flag for Matter 1.3 / 1.4. When matter.js introduces additional priority/urgency tiers we will mirror them at the same point; until then the wire behaviour is identical. |
| BD-Matter-RemainingAuditTestGaps | matter.js carries its own cluster-revision regression suite via `@matter/model` and per-behaviour parity tests | OpenCCU-Loom has parity tests for the actively-pair-tested cluster surface (OnOff, LevelControl on Switch/Light/Siren projections, the BridgedDeviceBasicInformation surface, the Aggregator + Root composition) but not for every device-type-projection cluster | Audit drifts L1-D07 (Thermostat), L1-D08 (DoorLock), L1-D09 (WindowCovering), L2-D03 (Switch power-source-attachment), L2-D04 (OnOffPlug→OnOffLight upgrade), L2-D05 (LevelControl OO feature-bit), L7-D06 (commissionable TXT golden test) are **PARITY-TEST-GAPs**, not code drifts. The corresponding production paths have been Apple-pair-verified end-to-end; adding parity tests is desirable but does not block release. Tracked as an open behavioural-parity-test gap; new cases are added per `docs/matter-parity-contract.md`. |
| BD-Matter-TouchLastReport-Timing | matter.js sets the subscription's `lastReport` timestamp in the SubscribeResponse handler after the final Initial-Chunk has been pushed | `internal/north/matter/im/subscription/subscription.go:192-197` calls `TouchLastReport()` immediately after the Initial-Report flush sequence completes from the bridge's perspective | Audit drift L10-D04 is a timing-shape difference, not a correctness drift: the chunked Initial-Report sequence is owned by the bridge, which only signals "done" to the subscription after every chunk has been ack'd. The two stacks therefore land on the same effective `lastReport` instant; the only observable difference would be if a peer raced a Report into the gap before the final ack, which the bridge's ack-walk prevents. |
| L7-D07 | matter.js `MdnsAdvertiser.ts:213-232` — `DefaultBroadcastSchedule` with exponential back-off starting at 1 s, max 90 s; chip is event-driven (re-advertise on fabric change / reconnect) | `internal/north/matter/mdns/zeroconf.go:301` + `cmd/openccu-loom/daemon.go:1511` — fixed 30-min `StartReannounceLoop` | The 30-min cadence keeps Apple's `mDNSResponder` cache warm (TTL=4500 s ≈ 75 min). No controller correctness issue — all known commissioners accept periodic re-announcement. Event-driven re-announce (fabric add/remove, reconnect) plus a backoff burst matching matter.js's `DefaultBroadcastSchedule` is a v1.2 milestone. Drift L7-D07 (LOW by-design for v1.0). |
| L8-D03 | matter.js `AdministratorCommissioningServer.ts:283-290` — 48-h extended window when `FabricCount == 0`; chip `CommissioningWindowManager.cpp:313-325` — `MaxCommissioningTimeout()` extends to 48 h for uncommissioned nodes | `internal/north/matter/bridge/commissioning_window.go` — `OpenWindowParams.IsUncommissioned` flag enables the 172800 s (48 h) cap via `commissioningWindowMaxSecUncommissioned` | Implemented (C-P2-4): the constant `commissioningWindowMaxSecUncommissioned = 172800` is wired into `OpenWindow`; the daemon must set `IsUncommissioned: fabricStore.Count() == 0` before calling OpenWindow. Default (IsUncommissioned=false) retains the 900-s cap. Wiring the fabric-count query into the commissioning-window open path is a daemon-side follow-up. |
| L9-D9 | AccessControl.MatterReadFiltered + UpdateFabricLabel + UpdateNOC fabric resolution | `internal/north/matter/cluster/core/operational_credentials.go:252-286, 1058-1065, 1003-1008` | Confirmed correct by audit 2026-05-12 (L9-D9): Bug M + Bug P fixes are wire-correct; all three paths use `im.FabricFilterFromContext` with `currentFabric` fallback. No action required. Drift L9-D9 (LOW, confirmed ✓). |
| BD-Matter-Dispatcher-StringHeuristic | matter.js `InteractionServer.ts` and chip `WriteHandler.cpp` / `CommandHandler.cpp` map typed errors via `StatusCodeError` / `MatterClusterStatusError` interfaces only | `internal/north/matter/endpoint/dispatcher.go::writeErrorStatus` / `invokeErrorStatus` (lines ~447-509) keep a string-contains fallback for "read-only", "unknown attribute", "constraint", "resource exhausted", "unknown command", "invalid command argument" | The 2026-05-19 chip-audit drift M-DRIFT-02 / L4-D03 is a **defense-in-depth** entry: every production cluster server already returns typed errors that implement `im.StatusCodeError` (verified via `grep -rn 'errors.New(' internal/north/matter/cluster/` — zero hits in production paths; the matches in `tests/` and `endpoint/*_test.go` are intentional fake-server fixtures). The string heuristic survives so legacy fakes keep working; removing it would only break tests, not production wire behaviour. New cluster code is expected to return typed errors via the `StatusCodeError` pattern. |
| BD-Matter-DataPoint-Quantity-Already-Present | aiohomematic `data_point.py:1007 value_behavior` property derived from metadata | `internal/model/generic/quantity.go:318 (*DataPoint[T]).Quantity()` + `quantity.go:353 (*DataPoint[T]).ValueBehavior()` already expose both via the three-tier `MetadataFor` resolver | Audit drift G08 ("ValueBehavior property fehlt auf DataPoint") is a **false positive**: both methods are present and unit-tested under `internal/model/generic/coverage_gap_test.go:591`. The `pkg/hmenum.ValueBehavior` enum (`Instantaneous`/`Cumulative`/`Monotonic`/empty) covers the same cases as aiohomematic's `ValueBehavior`. |
| BD-Matter-Device-HasSubDevices-Already-Present | aiohomematic `device.py:591 has_sub_devices` property | `internal/model/device/aggregate.go:408 Device.HasSubDevices()` returns true when ≥ 2 distinct channel-groups exist | Audit drift G12 is a **false positive**: the method is present and covered by `TestHasSubDevicesFalseNoGroups` / `TestHasSubDevicesFalseOneGroup` under `internal/model/device/device_test.go:556+`. |
| BD-Matter-Groups-Already-Mounted | chip + matter.js mandate Groups (0x0004) + ScenesManagement (0x0062) on every OnOff-mapped device-type (OnOffPlugInUnit 0x010A, OnOffLight 0x0100) | OpenCCU-Loom mounts both stub servers on `Switch` (`internal/model/custom/switch/matter.go:74-75`), `Light` (both dimmable and non-dimmable branches in `internal/model/custom/light/matter.go:114-115, 120-121`), and `Siren` (`internal/model/custom/siren/matter.go:131-132`) | Audit drift L2-D01-NEW is a **false positive**: every OnOff endpoint mounts both stubs already. Climate / Cover / Lock correctly have no Groups attachment (non-OnOff device types). |

| BD-Matter-TimeSync-NotMounted | matter.js `packages/node/src/endpoints/root.ts:215` lists TimeSynchronization (0x0038) as `optional` on RootNode | `cmd/openccu-loom/daemon.go::buildRootClusters` — not mounted (see comment) | TimeSynchronization is optional on a Matter-Bridge; home-assistant-matter-bridge omits it. Apple's HAP mapper rejects unexpected clusters on the RootNode device-type as schematic inconsistency. The cluster implementation exists (`internal/north/matter/cluster/core/time_synchronization.go`) and may be mounted if the bridge ever takes on a time-coordinator role. (M-P2-03 documented as by-design.) |
| BD-Matter-Actions-NotMounted | chip bridge-app mounts Actions (0x0025) on the Aggregator endpoint for the TC-BR test plan | `cmd/openccu-loom/daemon.go::buildAggregatorClusters` — Actions not mounted (comment present) | OpenCCU-Loom has no scene/action surface to model via Actions. Identify (0x0003) is already mounted; Actions will be added if bridge-app TC-BR conformance is required. (C-P2-1 documented as by-design.) |
| BD-Matter-ARL-NotMounted | Matter 1.4 Access Restriction List (cluster 0x002B) for Managed Aggregator use-case | `internal/north/matter/cluster/core/access_restriction.go` — skeleton constant only, not mounted | OpenCCU-Loom does not implement the Managed Aggregator use-case. ARL cluster constant is in the skeleton for the integration point. Mount on Root endpoint + wire commands when Managed Aggregator is in scope. (C-P2-3 by-design.) |

Bei jeder Änderung an einer der oben gelisteten Stellen ist zu prüfen, ob die Divergenz noch begründbar ist; ggf. Eintrag aktualisieren oder entfernen.

### L00 Schema Audit — chip-lag items (2026-05-12)

The following entries reflect clusters where OpenCCU-Loom follows matter.js HEAD (the gold standard) while chip HEAD has advanced further. These are **not** bugs in OpenCCU-Loom; they are tracked here so re-audits score them ✅ rather than flagging false drift.

- **L00-D04** TemperatureMeasurement ClusterRevision=5 — OpenCCU-Loom follows matter.js HEAD (`temperature-measurement.element.ts:14` default=5); chip HEAD is at rev 4 (`zzz_generated/app-common/clusters/TemperatureMeasurement/Metadata.h:20`). Reason: chip lags matter.js on this cluster; gold standard is matter.js.
- **L00-D05** RelativeHumidityMeasurement ClusterRevision=4 — OpenCCU-Loom follows matter.js HEAD (`relative-humidity-measurement.element.ts:14` default=4); chip HEAD is at rev 3 (`zzz_generated/app-common/clusters/RelativeHumidityMeasurement/Metadata.h:20`). Reason: chip lags matter.js; gold standard is matter.js.
- **L00-D06** BooleanState ClusterRevision=2 — OpenCCU-Loom follows matter.js HEAD (`boolean-state.element.ts:19` default=2); chip HEAD is at rev 3. OpenCCU-Loom does not implement StateChangeEvent (rev-3 addition) because StateChangeEvent is conformance `"O"` in matter.js; HM push events are handled via DP value changes. Reason: matter.js gold standard at rev 2; omitting optional event is intentional.
- **L00-D07** BasicInformation ClusterRevision=5 — OpenCCU-Loom follows matter.js HEAD (`basic-information.element.ts:20` default=5); chip HEAD is at rev 6 (adds PowerCycleCount). Reason: chip ahead of matter.js; gold standard is matter.js at rev 5.
- **L00-D08** BridgedDeviceBasicInformation ClusterRevision=5 — OpenCCU-Loom follows matter.js HEAD (`bridged-device-basic-information.element.ts:20` default=5); chip HEAD is at rev 6 (adds ProductAppearance+ConfigurationVersion mandates). Reason: chip ahead of matter.js; gold standard is matter.js at rev 5.
- **L00-D09** AccessControl ClusterRevision=2 — OpenCCU-Loom follows matter.js HEAD (`access-control.element.ts:21` default=2); chip HEAD is at rev 3 (adds MNGD feature). Reason: chip ahead of matter.js; gold standard is matter.js at rev 2.
- **L00-D10** NetworkCommissioning ClusterRevision=2 — OpenCCU-Loom follows matter.js HEAD (`network-commissioning.element.ts:20` default=2); chip HEAD is at rev 3 (multi-interface prioritization). Reason: chip ahead of matter.js; gold standard is matter.js at rev 2.
- **L00-D11** GeneralDiagnostics ClusterRevision=2 — OpenCCU-Loom follows matter.js HEAD (`general-diagnostics.element.ts:21` default=2); chip HEAD is at rev 3 (PayloadTestRequest command). Reason: chip ahead of matter.js; gold standard is matter.js at rev 2.
- **L00-D12** OnOff Options attribute (0x000F) — ✅ **Resolved**. The spurious attribute has been removed from `internal/model/custom/switch/matter.go`: the constant, the `MatterRead` case, and its entry in `MatterAttributes()` are gone, matching matter.js `on-off.element.ts` and chip `zzz_generated/.../OnOff/AttributeIds.h`. Regression tripwires `TestParityMatterJS_SwitchOnOffNoSpuriousOptions` and `TestParityMatterJS_OnOffServer_OptionsAbsent` assert the absence so a future re-port cannot reintroduce it.
- **L00-D13** IlluminanceMeasurement ClusterRevision=4 — OpenCCU-Loom follows matter.js HEAD (`illuminance-measurement.element.ts:19` default=4); chip HEAD is at rev 3. Reason: chip lags matter.js; gold standard is matter.js at rev 4.
- **L00-D14** PressureMeasurement ClusterRevision=4 — OpenCCU-Loom follows matter.js HEAD (`pressure-measurement.element.ts:18` default=4); chip HEAD is at rev 3. Reason: chip lags matter.js; gold standard is matter.js at rev 4.

### L00 Schema Audit — cluster-stub design choices (2026-05-12)

- **L00-BD-Groups** Groups/ScenesManagement as stubs — HM has no group/scene concept; OpenCCU-Loom's Groups and ScenesManagement cluster servers return empty collections and reject all writes. Presence is mandated by Matter device-type requirements (OnOff Light, Dimmable Light, etc.), not feature preference. matter.js `packages/node/src/behaviors/groups/GroupsServer.ts` / `packages/node/src/behaviors/scenes-management/`. Reason: no Homematic CCU-side primitive to map to; stubs satisfy the device-type conformance requirement without exposing broken functionality.
- **L00-BD-OnOffDefault** OnOff default-false on unobserved state — matter.js `OnOffServer.ts` defaults `onOff` to `false` when the underlying DP has not yet reported; OpenCCU-Loom mirrors this rather than returning null (which matter.js also documents as not spec-nullable for the OnOff attribute). Reason: spec-aligned default; null would be a schema violation.
- **L00-BD-BoolStateEvent** BooleanState no StateChangeEvent — StateChangeEvent (event id 0x0) has conformance `"O"` in matter.js `packages/model/src/standard/elements/boolean-state.element.ts`; OpenCCU-Loom omits it because HM push events are handled via DP value changes, not a dedicated state-change event mechanism. Reason: optional event, no HM-side equivalent event source.

### Parity Audit 2026-05-12 — L4 IM Engine + L10 Subscribe Lifecycle

The following items were classified during the 2026-05-12 L4/L10 parity
audit. Code-fixed items have regression tests; by-design items are
documented here; out-of-scope items (bridge/) are deferred.

| ID | Severity | Status | Notes |
|---|---|---|---|
| L4-D04 | MED | By-design (already `L6-PFAD-4`) | Wildcard-endpoint Invoke rejects with `UnsupportedEndpoint`; no cluster requires it today. |
| L4-D05 | MED | **Fixed** (2026-05-12) | `timedDeadlines` now keyed by `struct{sessionID, exchangeID uint16}` (`bridge/bridge.go::timedKey`). Regression test: `TestTimedRequest_SessionScopeIsolation`. |
| L4-D06 | LOW | Fixed (regression test added) | CommandRef absent from response when not in request. Guard via `HasCommandRef` is correct; regression test added to `im_test.go`. |
| L10-D05 | MED | By-design (chip alignment) | `SuppressResponse` not set on ongoing subscription ReportData; follows chip pattern; Apple Home expects IM:StatusResponse. See entry below. |
| L10-D06 | MED | **Fixed** (2026-05-12) | KeepSubscriptions=false PASE path now calls `m.CloseSession(sessionID)` instead of skipping (`bridge/subscribe.go`). Regression test: `TestCloseSession_ClosesMatchingSessionSubscription`. |
| L10-D07 | MED | Fixed (code comment) | `snapshot()` concurrency safety documented in `subscription/manager.go`. No bug; comment added for future readers. |
| L10-D08 | LOW | **Fixed** (2026-05-12) | Send-failure path in `reportSubscription` now calls `m.Close(sub.ID)` so the manager evicts the dead subscription. Regression test: `TestReport_PeerUnreachable_ClosesSubscriptionWithoutMRP`. |

#### L10-D05 detail — SuppressResponse not set on ongoing subscription ReportData (chip alignment)

**matter.js ref:** `packages/protocol/src/interaction/InteractionMessenger.ts:679`
— `suppressResponse: dataReport.moreChunkedMessages ? false : dataReport.suppressResponse`;
`ServerSubscription#sendUpdateMessage` sets `suppressResponse: true` for
non-chunked single-chunk ongoing reports so the subscriber ACKs via MRP
standalone ACK.

**chip ref:** `src/app/ReadHandler.cpp:340`
— `responseExpected = IsType(Subscribe) || aMoreChunks`; for ongoing
non-chunked reports `IsType(Subscribe)=true` forces `responseExpected=true`,
meaning chip always expects an IM:StatusResponse on subscription reports.

**OpenCCU-Loom:** Follows chip (`SuppressResponse=false` zero value).

**Rationale:** Apple Home uses the chip pattern in practice (ongoing
reports receive IM:StatusResponse, not bare MRP ACK — empirically
verified 2026-05-11 via tcpdump). Switching to matter.js's
`suppressResponse=true` behaviour could break Apple Home's ReadClient
flow. Alignment with chip is safer. Revisit if testing shows a specific
commissioner uses matter.js's path.

**File:** `internal/north/matter/bridge/subscribe.go:reportSubscription`.

---

### Parity Audit 2026-05-12 — L5 Secure Channel + L6 Bridge + Resumption Store

| ID | Severity | Status | Notes |
|---|---|---|---|
| L5-D5 | LOW | **By-design** | Resumption store has no LRU cap per fabric. See detail below. |
| L5-D6 | LOW | **Fixed** (2026-05-12) | `sigma1Replied` map pruned on TTL-reap via `PerExchangeCaseProvider.SetOnEvict` + `forgetSigma1Replied`. Regression tests: `TestSigma1Replied_PrunedOnSigma3`, `TestPerExchangeCaseProvider_OnEvictPrunesSigma1Replied`. |
| L6-D04 | MED | **Fixed** (2026-05-12) | Subscriptions for removed endpoints reaped after reassemble via `Manager.CloseEndpoint`. Regression tests: `TestReassemble_ReapsSubscriptionsForRemovedEndpoints`, `TestCloseEndpoint_ClosesMatchingSubscriptions`. |

#### L5-D5 detail — Resumption store: SQLite upsert vs. in-memory LRU cap

**matter.js ref:** `packages/protocol/src/session/SessionManager.ts` —
in-memory `ResumptionRecord` map; no hard cap; bounded by fabric/node count.

**chip ref:** `src/protocols/secure_channel/DefaultSessionResumptionStorage.h:kSessionResumptionStorageMaxEntries = 8` per node.

**OpenCCU-Loom:** `internal/north/matter/store/resumption.go:UpsertResumption` uses
`ON CONFLICT(fabric_index, peer_node_id) DO UPDATE` — exactly **one row per (fabric, peer) pair**.
The unique constraint on `(fabric_index, peer_node_id)` already enforces a natural cap: each peer
has at most one active resumption record at any time. Adding a per-fabric LRU cap (chip's 8 / matter.js
soft 32) would require a DELETE-after-upsert pass over the SQLite table. For a bridge with a typical
fleet of 3–10 controllers per fabric this would be pure overhead; the SQLite unique index is a more
durable and auditable enforcement mechanism than an in-memory counter.

**Rationale:** The by-design cap is one-per-peer (implicit via the DB unique constraint), which is
stricter than chip's 8-per-node in the single-peer case and weaker in the many-peer case. For a
homematic bridge with a single controller hub this is never observable. If a future production scenario
requires 8+ distinct peers per fabric, a `LIMIT 8 ORDER BY last_used_at` eviction pass can be added
to `UpsertResumption` without a schema change (migration `last_used_at` column is already present).

---

### L01 Cluster Server Audit — by-design items (2026-05-12)

The following L01 entries were reviewed during the 2026-05-12 MED/LOW wave and
classified as by-design divergences. Code-fixed items (L01-D06..D10) have
regression tests in the respective source packages.

**L01-D06** celsiusToInt16 clamp fixed: 32767 is TLV-null sentinel; -32768 is below
absolute-zero floor. Clamped to 32766 / -27315 per chip `kMaxMeasuredValueRange` /
`kMinMeasuredValueRange`. Tests: `TestTemperatureServerSaturatesHigh/Low`.

**L01-D07** PressureServer min/max fixed: `MinMeasuredValue=0`, `MaxMeasuredValue=32766`.
Tests: `TestPressureServerMinMeasuredValueIsZero` / `TestPressureServerMaxMeasuredValueIs32766`.

**L01-D08** ScenesManagement.MatterInvoke fixed: error now contains "no commands" for
correct UnsupportedCommand (0x81) mapping in the bridge dispatcher.
Test: `TestScenesManagementInvokeContainsNoCommands`.

**L01-D09** BooleanState non-nullable default fixed: returns `(false, true)` when
unobserved. StateValue has no quality X per matter.js. Test: `TestBooleanStateServerUnobserved`.

**L01-D10** OnOff MatterWrite nil guard fixed: `Switch.MatterWrite` no-ops on nil value
preventing future panic if a scene controller writes nil.
Test: `TestMatterWriteNilValueIsNoOp`.

---

### L02 DeviceType Audit — by-design items (2026-05-12)

**L02-D01** Siren: BooleanState removed from `Siren.MatterClusterServers()`. BooleanState
(0x0045) is not in the OnOffPlugInUnit (0x010A) mandatory/optional cluster set per
matter.js `packages/node/src/devices/on-off-plug-in-unit.ts:86-105`; mounting it
causes UnsupportedCluster rejection by strict controllers. The sirenBooleanStateServer
type is retained for potential future use. Test: `TestSirenClusterServersIncludeOnOff`
asserts 0x0045 is absent.

**L02-D02** OccupancySensing PIROccupiedToUnoccupiedDelay (0x0010) added: returns
`uint16(0)` (no configurable HM hold-time). Conditionally conformant when PIR feature
is active per matter.js `OccupancySensingServer.ts:17-60`.
Tests: `TestOccupancyServerPIRDelayPresent` / `TestOccupancyServerMatterAttributesIncludesPIRDelay`.

**L02-D03 — Siren device-type semantic approximation (by-design).**
Generic Siren (HmIP-ASIR) maps to OnOffPlugInUnit (0x010A). No better-fit Matter
device type exists in Matter 1.5.1 for a multi-tone siren. Renders as plug/outlet in
Apple Home — accepted until Matter introduces a dedicated Siren/Alarm device type.

---

### L06 Bridge Composition Audit — by-design items (2026-05-12)

**L06-D01 — Aggregator Identify absent (by-design).**
matter.js `aggregator.ts:62` marks Identify optional on EP 1. No Apple/chip-tool
failure mode known. chip mounts it in the reference bridge but chip's static-endpoint
composition is not a gold standard for optional cluster policy.

**L06-D02 — Aggregator Actions absent (roadmap v1.1).**
matter.js marks Actions optional. Enables Matter action automation across bridged
endpoints. No short-term breakage. Deferred to v1.1.

**L06-D03 — mDNS operational record not re-published after topology change (by-design).**
Operational `_matter._tcp` record is fabric-specific (NodeID/CompressedFabricID are
static per fabric). Topology changes do not require a new record; commissioners
reconnect via the same record. No functional gap.

**L06-D04 — Vanished endpoint: stale subscriptions reaped after reassemble. FIXED 2026-05-12.**
`Bridge.reassembleLocked` now calls `Manager.CloseEndpoint(ep.ID)` for every endpoint present
in the previous topology but absent in the new one. The new `CloseEndpoint` method in
`im/subscription/manager.go` closes any subscription whose AttributePaths or EventPaths reference
the removed endpoint. Regression tests: `TestReassemble_ReapsSubscriptionsForRemovedEndpoints`,
`TestCloseEndpoint_ClosesMatchingSubscriptions`, `TestCloseEndpoint_EventPathAlsoMatches`.

### Systematic Parity Run #02 — PFAD-ASYMMETRIEN (2026-05-12)

Aus `docs/parity/audit_runs/2026-05-12_systematic_02.md` §5 — 38 neue
🔄 Einträge, gruppiert nach Plan-Layer. Quelle: 7 parallele Sonnet
Sub-Agents über die 11 Plan-Layer.

**L0-D04 — Descriptor.TagList not advertised when TAGLIST feature absent (by-design).**
TagList (0x0004) conformance is "TAGLIST" per Matter §9.5.4.1 + §9.5.6.5 — only present
when the TAGLIST FeatureMap bit is set. OpenCCU-Loom advertises FeatureMap=0 (no TAGLIST).
`descriptor.go:113-123` returns `(nil, false)` for TagList: Apple's iOS Matter SDK does
not yet ship the `semtag` struct schema and rejects the whole Descriptor cluster when
TagList appears in wildcard expansion (HAPErrorDomain Code=14). matter.js and chip-tool
tolerate the absence. Re-enable only when TAGLIST FeatureMap bit is set AND Apple ships
semtag schema. matter.js ref: `packages/model/src/standard/elements/descriptor.element.ts`.

**L2-D01 + EventList suppression — EventList (0xFFFA) suppressed on all clusters / all endpoints (by-design).**
All 17 Apple cache-drops for attribute 0xFFFA (EventList) across every cluster on every
endpoint (EP0 root, EP1 aggregator, EP14, EP28, …) are expected and harmless. Both
matter.js HEAD and chip return `StatusUnsupportedAttribute` for 0xFFFA when Apple's
iOS Matter SDK (pre-1.4 schema) reads it; OpenCCU-Loom does the same via
`endpoint/dispatcher.go:413-424`. Apple iOS 26 does not ship a schema for EventList
(Matter 1.4 global attr); returning `[]uint32{}` caused `MTRErrorDomain Code=12` and
dropped the entire ReportData stream. UnsupportedAttribute is handled gracefully.
Re-enable when Apple iOS ships Matter 1.4 SDK schema. No code change needed.
matter.js ref: `packages/protocol/src/interaction/InteractionServer.ts`.
chip ref: `src/app/clusters/*/` Attribute::kEventList.

**L2-D04 — OnOffLight/OnOffPlugInUnit: Groups + ScenesManagement stubs present; chip bridge-app omits them (by-design).**
matter.js `packages/node/src/devices/on-off-light.ts` and `on-off-plug-in-unit.ts` list
Groups (0x0004) and ScenesManagement (0x0062) as mandatory. OpenCCU-Loom's
`internal/model/custom/switch/matter.go:74-86` returns them from `MatterClusterServers()`,
matching matter.js. chip's `examples/bridge-app/linux/main.cpp:113-151` minimal sample
omits them. Per §3.A.4 resolution rule: matter.js > chip for schema. Apple pairs cleanly
with stubs present (verified 2026-05-12). No code change needed.

**L6-D03 — Aggregator Descriptor.ServerList composition (resolved).**
matter.js `packages/node/src/endpoints/aggregator.ts:56-62` lists Identify, Actions, and
CommissionerControl as optional. Matter Device Library §13.2 mandates no cluster other
than Descriptor on an Aggregator. OpenCCU-Loom now mounts Identify + Descriptor on EP1:
chip's reference bridge-app composes the Aggregator with Identify + Descriptor + Actions,
and the Apple-pair empirics confirmed that an Identify-less Aggregator triggers Apple's
HAP-Mapper to fall back to "structural placeholder" treatment and skip PartsList
traversal. ServerList is now derived from the mounted set via `SetServerListProvider`
(`buildAggregatorClusters` in `cmd/openccu-loom/daemon.go`) — no static list to drift
when Actions or another optional cluster lands.

**L0/L1 — Structural Decorator-Patterns (5):**
- matter.js Behavior-Decorator (`Behavior.with(...)` mixin) ↔ Go struct
  + Constructor mit defaults (`NewBasicInformation(...)`). PFAD-ASYMMETRIE
  weil TS-Decorator-Composition kein Go-Pendant hat; Go nutzt Constructor +
  Method-Receiver-Pattern.
- matter.js `FabricScopedReader` interface ↔ Go inline access check via
  `FabricFilterFromContext(...)` (Bug M Pattern).
- matter.js CSR session binding via Promise-chain ↔ Go pointer-pinned
  `pendingCSRSessionID` (L9-D5).
- matter.js DataVersion mechanism (`@matter/protocol` `version` field) ↔
  Go `DataVersionTracker` per cluster.
- matter.js cluster-server lifecycle hooks (`onInitialize`) ↔ Go
  Constructor + Registry-Wire.

**L2/L6 — Bridge composition (5):**
- matter.js Behavior-init via `Node.startup` ↔ Go endpoint-materialize in
  `endpoint/materialize.go::Materialize`.
- matter.js Effects-API reactive state ↔ Go EventBus subscriber.
- matter.js Aggregator hierarchy via `parts: { child: { ... }}` nested ↔
  Go flat slice + `ParentEndpointID` hint (verifyable via Descriptor).
- matter.js auto-persisted UniqueID per endpoint via `@matter/node`
  storage ↔ Go bootid.Salt mixed in.
- chip C++ atomic ExchangeID seeding ↔ Go `atomic.Uint16`.

**L3 — TLV Wire Codec (Systematic Parity Run #02 — 2026-05-12):**

- **L3-D2 ImplicitProfile-tag pass-through:** matter.js throws
  `NotImplementedError` for `ImplicitProfile16`/`ImplicitProfile32` tags
  (TlvCodec.ts:171-172); chip resolves them against `ImplicitProfileId` and
  returns `CHIP_ERROR_UNKNOWN_IMPLICIT_TLV_TAG` when no profile ID is set
  (TLVReader.cpp:872-879). OpenCCU-Loom's `Decoder.readTag` silently surfaces
  them as `Tag{Kind: TagKindImplicitProfile2/4, Number: n}` without
  resolution. By-design: no commissioner sends ImplicitProfile-tagged TLV to
  a bridge in any known commissioning or interaction flow (verified 60+ pair
  iterations). The comment at `internal/north/matter/tlv/decode.go:83` records
  the open risk for future code paths that might require chip's
  `ImplicitProfileId` pattern. Wire impact: none (zero byte difference on
  current production paths).

- **L3-D5 Container-type tag validation:** chip's TLVWriter.cpp:WriteElementHead
  rejects context-tagged elements inside an Array container with
  `CHIP_ERROR_INVALID_TLV_TAG`. matter.js enforces the same rule at the schema
  layer (TlvArray/TlvObject). **Implemented in Systematic Run #02:**
  `internal/north/matter/tlv/encode.go` now maintains a `containerStack
  []ElementType` and panics when a context tag is written inside an Array.
  This is an implemented fix, not a by-design entry; the earlier by-design
  comment in encode.go has been replaced with the implementation.

- **L3-D6 PutUintSized / smallest-fit difference:** matter.js
  `TlvUInt64.encodeTlvInternal` (TlvNumber.ts:112-115) uses smallest-fit width
  for any schema-declared `uint64` field (e.g. `BigInt(1)` → 1 byte via
  `TlvLength.OneByte`). OpenCCU-Loom's `PutUint64` always emits 8 bytes; chip
  tolerates both widths (TLVReader.cpp:GetValue template). **Implemented
  helper in Run #02:** `PutUintWidth(tag, value, widthBytes)` now allows
  callers to emit any exact fixed width. `PutUint64` continues to emit 8 bytes
  (no wire impact vs Apple Home or chip-tool). Cosmetic difference only.

- **L3-D7 TlvObject.injectField not implemented:** matter.js
  TlvObject.ts:279-294 provides `injectField` for IM-layer DataVersion
  injection. OpenCCU-Loom handles DataVersion at the IM call site
  (im/read.go, im/subscribe.go) by constructing the full AttributeReport
  struct before encoding. No wire difference; different API shape only.

- **L3-D8 TlvObject.removeField not implemented:** matter.js
  TlvObject.ts:300-313 provides `removeField` for fabric-scope filtering.
  OpenCCU-Loom filters at the cluster-server level before TLV encoding. No wire
  difference; different API shape only.

- **L3-D9 No TlvSchema.validate layer:** matter.js (TlvSchema.ts abstract base
  + TlvNumber.ts:124-135) runs `validate()` post-decode to enforce
  cluster-defined constraint bounds (min/max). chip enforces via
  EmberAfAttributeType at AttributeValueDecoder level. OpenCCU-Loom uses Go
  struct validation at the cluster-server boundary (`PutInt16Bounded`,
  `PutUint8Bounded`, etc.) — the Bounded helpers reject nullable sentinels on
  encode. The TLV decoder itself is a raw byte reader with no schema layer.
  Architectural reason: Go's type system and struct validation at the
  cluster-server boundary replace the TypeScript schema-object approach.
  Nullable sentinel protection is explicit-opt-in per attribute; cluster
  servers must use Bounded helpers for nullable attributes. The existing
  `TestObjectParity_ValidationError_NotImplemented` skip in
  object_parity_test.go documents the gap. Risk: a cluster server that uses
  raw `PutInt16` for a nullable int16 attribute could accept the `-32768`
  sentinel as a regular value; this is caught in code review, not at the TLV
  layer.

- **L3-D10 allowProtocolSpecificTags not validated:** matter.js
  TlvObject.ts:71,195 `TlvTaggedList` schema has an `allowProtocolSpecificTags`
  flag; when false it throws on non-context tags inside a List. chip has no
  direct equivalent flag. OpenCCU-Loom's decoder passes non-context tags through
  without error. No Apple-pair impact: no commissioner sends ImplicitProfile or
  FullyQualified tags inside List elements to a bridge in the current bridge
  surface. The `TestObjectParity_TaggedList_ProtocolSpecificTags_NotImplemented`
  skip documents the gap.

- **L3-D11 Chunked-array API:** OpenCCU-Loom chunks ReportData at the
  AttributeReport boundary (bridge/reply.go:274-368); matter.js supports
  per-element list-index chunking inside a single AttributeReport
  (TlvArray.ts:156-176). Apple Home tolerates over-budget single-attribute
  chunks empirically. Listed as v1.1 future work in memory note
  `matter_udp_mtu_2048.md`.

- **L3-D13 Nullable bounds restriction:** matter.js TlvNullable.ts:27-44
  arithmetically shrinks the inner schema's max by 1 (unsigned) or min by 1
  (signed) to reserve the null-sentinel slot, preventing the sentinel from
  being a valid value at schema construction time. OpenCCU-Loom implements the
  equivalent guard at encode time via the `Bounded` helpers
  (`PutUint8Bounded`/`PutInt16Bounded`/etc.) which return
  `ErrUint8NullableSentinel`/`ErrInt16NullableSentinel`/etc. when the sentinel
  value is passed. Callers on nullable attributes must use the Bounded path;
  using raw `PutInt16` / `PutUint8` on a nullable attribute bypasses the
  guard. This is enforced by convention and code review, not by the type
  system. No wire difference when Bounded helpers are used correctly.

**L4 — IM Engine (3):**
- StatusResponseError typing: matter.js exception hierarchy ↔ Go typed
  error interface (`StatusCodeError` aus L4-D03 fix).
- DataVersionFilter cache: chip `mCommissionerSessionId` per-session ↔
  Go per-IM-context `DataVersionTracker`.
- Path-Interpreter delegation: chip `ConcreteAttributePath` static ↔ Go
  `dispatcher.Resolve(...)` runtime resolution.

**L7 — mDNS (3 + 1 from Run #02):**
- Hostname: chip MAC-derived (`{macHex16}.{network}`) ↔ OpenCCU-Loom OS
  hostname (`<host>.local.`). Beide commissioners akzeptieren beide.
  (L7-D02 entry — confirmed by-design.)
- Subtype-emit: OpenCCU-Loom emittiert `_T<DeviceTypeID>._sub`
  (verifiziert `mdns/service.go:408`). Kein drift.
- TXT-builder: matter.js incremental builder pattern ↔ Go static
  `mdns/txt.go` constants.
- **L7-D02 (Run #02) Subtype PTR TTL 120 s:** `SubtypeResponder` emits
  PTR TTL=120 s (`internal/north/matter/mdns/subtype_responder.go:355`).
  chip `src/lib/dnssd/minimal_mdns/records/ResourceRecord.h:35`
  `kDefaultTtl=120` for SRV/PTR — OpenCCU-Loom matches chip exactly.
  matter.js offloads TTL to the DNS-SD library (no explicit value set
  for subtype PTRs). Audit #02 verdict: ✓ No drift against chip.
  By-design: 120 s is the chip-verified correct PTR TTL for subtype records.

**L8 — Commissioning Window (3):**
- Window-Timer: chip `CHIP_System_Layer` for cross-platform timer ↔ Go
  `time.AfterFunc`.
- PASE-only session lifecycle: chip global `PASESession.*` ↔ Go
  `pase.Manager.Close()`.
- AdminFabricIndex resolution: chip `OnSessionEstablished` callback
  binds index ↔ Go IM context `fabric.Index()`.

**L10 — Subscribe lifecycle (3 + 3 new from agent4 L10 run):**
- Heartbeat mechanism: matter.js system clock + Promise ↔ Go `time.Ticker`
  + goroutine.
- Resubscribe trigger: matter.js Promise-chain in
  `SubscriptionHandler.shouldResubscribe()` ↔ Go goroutine-loop in
  `im/subscription/resubscribe.go`.
- Dirty-marking: matter.js `Set<AttributeKey>` ↔ Go
  `map[paramset.Path]struct{}` with `sync.Mutex`.

**L10-D09 — Randomisation window absent (PFAD-ASYMMETRIE, by-design):**
matter.js `packages/node/src/node/server/ServerSubscription.ts:282` adds
`subscriptionRandomizationWindow * Math.random()` to the heartbeat send
interval to distribute publisher traffic across subscriptions sharing one
JS event loop. chip `src/app/ReadHandler.cpp:769-809` applies NO such
randomisation — it uses the negotiated MaxInterval directly. OpenCCU-Loom
follows the chip model: subscriptions are created at different wall-clock
offsets so their `lastReport` stamps already diverge organically, providing
natural phase scatter without explicit randomisation. The omission is
unobservable by Apple Home or chip-tool. Code: `im/subscription/subscription.go`
`sendIntervalLocked()` comment.

**L10-D10 — MaxInterval publisher selection semantics (chip alignment, no drift):**
Matter §10.6.3.2 requires the SubscribeResponse MaxInterval ≤ requested
MaxInterval. chip `src/app/ReadHandler.cpp:769-809` (non-ICD path) accepts
the subscriber's MaxInterval and bounds it by `kSubscriptionMaxIntervalPublisherLimit`
(3600 s). matter.js `packages/node/src/node/server/ServerSubscription.ts:269-282`
additionally lifts by `minIntervalFloor`. OpenCCU-Loom clamps `maxCeil` down to
`cfg.MaxIntervalCeilingSeconds` (default 3600 s) and `minFloor` up to
`cfg.MinIntervalFloorSeconds`; the post-clamp inversion check
(`ErrCadenceInvertedAfterClamp`) rejects inverted cadences — equivalent to
matter.js's lower-bound guarantee. The advertised MaxInterval in SubscribeResponse
satisfies §10.6.3.2. Classified as ✓ (chip-aligned, no code change needed).
Code: `im/subscription/manager.go` `Subscribe()` comment.

**L10-D11 — Shared engine ticker vs. matter.js per-subscription timer (PFAD-ASYMMETRIE, by-design):**
matter.js `packages/node/src/node/server/ServerSubscription.ts:191` gives each
ServerSubscription its own `Time.getTimer(sendInterval, callback)`. chip uses
per-ReadHandler state in the IM engine. OpenCCU-Loom uses one shared ticker at
`cfg.TickInterval` (default 250 ms) that iterates all subscriptions. A
subscription can fire up to 250 ms early compared to a strict per-timer model —
unobservable by any Matter commissioner because it falls within the spec window
and the MinIntervalFloor gate prevents over-reporting. The shared ticker is the
idiomatic Go approach; per-subscription goroutines would create O(N) timers
unnecessarily. Code: `im/subscription/engine.go` `run()` comment.

### Systematic Parity Run #02 — Closed Drift Items (2026-05-12)

Drifts closed by implementation wave closing audit `2026-05-12_systematic_02`.

**L0-D01 — BasicInformation.ConfigurationVersion omitted when unset (by-design, consistent with matter.js bridge sample).**
matter.js `packages/model/src/standard/elements/basic-information.element.ts:104` specifies
ConfigurationVersion (0x0018) with `default: 1` and `constraint: "min 1"`, but the matter.js
bridge sample (`packages/node/src/devices/root-node.ts`) omits ConfigurationVersion on the Root
endpoint when not explicitly provisioned. chip's bridge-app also omits the attribute. OpenCCU-Loom
mirrors this bridge-sample behaviour: the attribute is present on the wire only when the caller
supplies `Config.ConfigurationVersion > 0`; when unset, `MatterRead(0x0018)` returns `(nil, false)`.
Apple does not request EP0:0x28:0x0018 in any of the 21 pair syslogs. The spec `default: 1` is
a schema-level declaration for clients building provisioned devices, not a mandate for bridges to
emit the attribute when not provisioned. By-design for v1.0; emit when Apple requests it via PICS
test DT.S.A0018.

**L1-D06 — GeneralDiagnostics.BootReason attribute omitted (by-design).**
chip `src/app/clusters/general-diagnostics-server/` serves BootReason (0x0004)
as a mandatory attribute. matter.js `packages/model/src/standard/elements/
general-diagnostics.element.ts:46` lists BootReason as optional (conformance "O").
OpenCCU-Loom deliberately omits the attribute and surfaces BootReason exclusively
via the §11.12.8.1 BootReason *event* (emitted on Subscribe-Initial via
`EmitBootReason()`), which Apple parses into `estimated start time forward to ...`.
No Apple cache-drop observed for EP0:0x33:0x0004 across 21 pair syslogs — Apple
does not request the attribute when the event is present. The matter.js spec-sample
`examples/device-bridge-onoff` also omits the attribute. Code path:
`internal/north/matter/cluster/core/general_diagnostics.go:234-235` (`case
gendiagAttrBootReason: return nil, false`). By-design for v1.1; implement if a
future Matter-certification test (PICS DT.S.A0004) requires it.

**L10-D09 — Randomisation window absent in sendIntervalLocked (by-design).**
matter.js `packages/node/src/node/server/ServerSubscription.ts:282` incorporates
`subscriptionRandomizationWindow * Math.random()` in the heartbeat interval. chip
uses the negotiated max directly (no randomisation). OpenCCU-Loom mirrors chip:
`internal/north/matter/im/subscription/subscription.go:129-161` computes from
`MaxIntervalCeiling` without randomisation. In Go, each subscription goroutine is
independent so the TS-runtime jitter concern (multiple subscriptions sharing one
event loop) does not apply. No commissioner correctness impact.

**L10-D11 — Shared engine ticker vs. per-subscription timer (by-design).**
matter.js `packages/node/src/node/server/ServerSubscription.ts:191` owns an
independent `Time.getTimer(sendInterval, callback)` per subscription. chip has
per-ReadHandler state machines. OpenCCU-Loom uses a shared 250 ms engine ticker in
`internal/north/matter/im/subscription/engine.go:22-36` polling all subscriptions.
Per-subscription goroutines would create O(N_subscriptions) goroutines with no
functional advantage for a bridge with O(10) concurrent subscribers. Granularity
is 250 ms vs. per-subscription precision — practically irrelevant.

**L4-D07 — Lenient timedFlag=false + pending TimedRequest (by-design).**
chip `src/app/WriteHandler.cpp:669-673` rejects with `TimedRequestMismatch` when a
non-timed write arrives on an exchange with a pending TimedRequest. matter.js is
silent on this combination. OpenCCU-Loom `internal/north/matter/bridge/receive.go:613-617`
proceeds leniently (clears deadline, continues). Apple's commissioner issues timed
writes correctly for all operations requiring them; no Apple write path triggers
this branch. Matches matter.js semantics. Lenient path is correct for Apple pairing.

**L5-NEW-D02 — Sigma1 Marshal omits initiator-side `initiatorSessionParams` (PFAD-ASYMMETRIE, by-design).**
matter.js `packages/protocol/src/session/case/CaseClient.ts` (Sigma1 initiator)
encodes its MRP params as tag 5 `initiatorSessionParams` so the responder can
adapt its retransmit budget. chip `src/protocols/secure_channel/CASESession.cpp:860`
does the same. OpenCCU-Loom `internal/north/matter/secure/sigma/sigma.go:219-228`
(`Sigma1.Marshal()`) emits fields 1-4 only (no field 5). The receive path
(`sigma.go:270-276`) already drains field 5 from inbound Sigma1 correctly.
The send path is dormant: OpenCCU-Loom always acts as the CASE *responder* in the
v1.0 bridge role — `Sigma1.Marshal()` is exercised only in test round-trips (via
`Initiator.GenerateSigma1()`), never on a real outbound CASE exchange. Becomes
relevant only if an outbound CASE initiator role is added (v1.2 milestone).
PFAD-ASYMMETRIE: bridge-responder use-case only. No wire impact for v1.0.

**L5-NEW-D04 — PaseServer single-active-exchange guard (IMPLEMENTED, noted for completeness).**
This item was closed by implementation rather than by-design. Recorded here for
audit trail completeness. matter.js `packages/protocol/src/session/pase/PaseServer.ts:70-85`
`onNewExchange` checks `this.#pairingMessenger !== undefined` and drops additional
exchanges while one is in-flight; chip `src/protocols/secure_channel/PASESession.cpp:113`
guards the single-session invariant. Fix applied: `internal/north/matter/bridge/pase_provider.go`
`Resolve()` now returns nil (SC router drops the datagram) when `len(p.entries) > 0`
and the incoming exchangeID is new. Drift L5-NEW-D04.

**L5-NEW-D05 — PASE 60 s timeout hard-enforcement (IMPLEMENTED, noted for completeness).**
This item was closed by implementation rather than by-design. Recorded here for
audit trail completeness. matter.js `packages/protocol/src/session/pase/PaseServer.ts:37`
starts `PASE_PAIRING_TIMEOUT = Seconds(60)` on `handlePairingRequest`. chip uses
per-step timeouts via `mDelegate`. Fix applied: `internal/north/matter/bridge/pase_provider.go`
`Resolve()` starts `time.AfterFunc(60*time.Second, ...)` per new exchange that calls
`Forget(exchangeID)` on expiry. The existing TTL reaper provides the soft reap;
the per-exchange timer is the spec-mandated hard cap. Drift L5-NEW-D05.

**L1-D02 — OperationalCredentials.Fabrics Apple cache-miss (root cause L4/L10, by-design at L1).**
Audit `2026-05-12_systematic_02/agent1_L0_L1.md` records an Apple MTRDevice
cache-miss for EP0:0x3E:0x0001 (Fabrics). Root cause analysis: the cache-drop
co-occurs with ALL global attributes for cluster 0x3E in the same Apple syslog
burst, confirming the entire cluster's Subscribe-Initial ReportData block was
not delivered or not accepted by Apple's MTRDevice. The L1 code
(`operational_credentials.go` `MatterReadFiltered` for attr 0x0001, fabric-scoped
read via `im.FabricFilterFromContext`) is correct — it correctly enumerates
fabric entries and returns a fabric-scoped slice. This is a L4/L10 delivery gap
(Subscribe-Initial chunking; IM-revision field present-check) rather than a L1
attribute code defect. Marked dependent on L4-D03 (Subscribe-Initial delivery)
and L10-D02 (IM-revision field guard). Will re-test as part of the L4/L10
implementation wave.

**L9-NEW-7 — GroupKeyManagement.GroupTable always empty (by-design, v1.1).**
Matter §11.2.7.5 `GroupTable` (attr 0x0001) lists all multicast group memberships
for the node. OpenCCU-Loom v1.0 does not implement group multicast — there are no
multicast group entries to report. chip `src/app/clusters/group-key-mgmt-server/
group-key-mgmt-server.cpp` returns the entries from `GroupDataProvider`; in
OpenCCU-Loom `GroupKeyManagement.MatterRead` returns `[]GroupEntry{}` for attr 0x0001.
Apple Home does not emit an error for an empty GroupTable during commissioning or
normal operation. Implementation deferred to v1.1 when group multicast is added.
No interop impact for the bridge-only v1.0 use-case.

### Open audit hooks

| Audit | Status | Source of truth |
| --- | --- | --- |
| Cluster ID + revision parity | locked via `parity_matterjs_test.go` in 8 packages (cluster/core, cluster/measurement, cluster/wire, cluster/tlv, model/custom/{climate, cover, light, lock, siren, switch}) | `docs/parity/matter/matter-schema-snapshot.json` |
| TLV wire byte parity | locked via `tlv/parity_matterjs_test.go` (23 fixtures) | `docs/parity/matter/tlv-wire-fixtures.json` |
| Behavior layer (BasicInformationBehavior, BridgedDeviceBasicInformationBehavior, OperationalCredentialsServer, etc.) | open — see `docs/matter/apple_pair_status_2026-05-10.md` §"Was matter.js NICHT für uns liefert" | `../matter.js/packages/node/src/behaviors/` |
| Bridge composition (Aggregator + BridgedNode + per-device-type endpoints) | open — same | `../matter.js/packages/node/src/devices/aggregator.ts`, `bridged-device.ts` |

---

## Appendix: Where are these items marked in the sources?

The items in this file originate from the following audit sources. These files are historical and do NOT serve as the source of truth for open gaps — for that, the current `parity_audit_v4.md` and `/tmp/parity_v4_a*.md` apply.

| File | PATH-ASYMMETRY by design (items) |
|---|---|
| `docs/parity/v2/a1_model_core_generic.md` | M1001–M1009, M1039, M1073, M1171, M1177, M1209, M1211, M1225, M1244, M1245 (12 explicitly marked) |
| `docs/parity/v2/a2_custom.md` | M2029, M2035, M2036, M2047 (4 explicitly marked) |
| `docs/parity/v2/a3_calc_combined_hub.md` | M3008, M3028, M3030, M3031, M3036, M3037, M3053, M3056, M3069, M3070, M3071, M3072, M3084, M3085, M3089, M3090, M3091, M3097, M3099, M3101, M3104, M3112, M3114, M3124, M3128 (25 explicitly marked) |
| `docs/parity/v2/a4_central.md` | M4002, M4006, M4011, M4033, M4034, M4040, M4046, M4047, M4093, M4099, M4105, M4114, M4124, M4193, M4211, M4212, M4234, M4235, M4236 (19 explicitly marked) |
| `docs/parity/v2/a5_client.md` | M5049 (1 explicitly marked) |
| `docs/parity/v2/a6_store_schemas.md` | M6006–M6011, M6013–M6015, M6017, M6188 (11 explicitly marked) |
| `docs/parity/v2/a7_crosscut_subprojects.md` | M7010, M7015, M7016, M7019, M7020, M7021, M7024, M7035, M7054, M7064–M7068 (14 explicitly marked) |

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
| `valve.Modulating` (Go-only forward declaration) | `internal/model/custom/valve/valve.go` | No aiohomematic peer; the LEVEL-based modulating valve infrastructure is in place for when a future device profile maps onto it. Until then it remains unregistered (no `init.go` entry). |

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

### C-L1-ARL — Access Restriction List (Matter 1.4) — Phase 2 pending

**Status:** Skeleton present, full implementation deferred to Phase 2.

**Matter reference:** Matter Core Specification 1.4 §11.19 (Access Restriction List cluster, cluster ID 0x002B). Introduced alongside the Matter 1.4 Managed Aggregator use-case, which allows a fabric administrator to restrict which subjects can access specific clusters or endpoints on a bridged node.

**Existing skeleton:**
- Constant declaration: `internal/north/matter/cluster/core/access_restriction.go` — defines `ARLClusterID uint32 = 0x002B` and documents the integration point.
- The cluster is intentionally **not mounted** on any endpoint (see `BD-Matter-ARL-NotMounted` row above). OpenCCU-Loom does not currently implement the Managed Aggregator use-case.

**What is missing for a full implementation:**

1. **Cluster server struct** — `BridgedARLCluster` implementing `interfaces.MatterClusterServer` with:
   - Attribute 0x0000 `CommissioningARLEntries` (fabric-scoped list).
   - Attribute 0x0001 `ARLEntries` (fabric-scoped list).
   - Event 0x0000 `AccessRestrictionEntryChanged`.
2. **Command handlers** — `CommitRestrictionEntries` (0x01) and `ReviewFabricRestrictions` (0x02), both fabric-scoped.
3. **Fabric-store integration** — persistence of ARL entries per fabric in `internal/store/sqlite/` (new migration + store type), mirroring the ACL store pattern in `internal/north/matter/cluster/core/access_control.go`.
4. **Root-endpoint mount** — `cmd/openccu-loom/daemon.go::buildRootClusters` must mount the cluster when `cfg.North.Matter.ManagedAggregator` is enabled (opt-in, default off).
5. **Parity tests** — `internal/north/matter/cluster/core/access_restriction_test.go` covering attribute read/write, event emission, and fabric-scoped isolation.

**Why deferred:** The Managed Aggregator use-case has no demand in the current device fleet. ARL enforcement is only meaningful once the bridge exposes commissioner-level fabric administration, which is a multi-week effort touching the fabric store, the IM engine, and the commissioning flow. Implementing a partial ARL that does not enforce restrictions would be worse than not implementing it (false compliance signal to the commissioner).

**Re-activation checklist:**
- [ ] Add `cfg.North.Matter.ManagedAggregator bool` to `internal/config/`.
- [ ] Implement `BridgedARLCluster` in `internal/north/matter/cluster/core/access_restriction.go`.
- [ ] Add SQLite migration for ARL table under `internal/store/sqlite/migrations/`.
- [ ] Mount on Root endpoint behind the `ManagedAggregator` flag in `daemon.go`.
- [ ] Add `TestARL_*` parity tests.
- [ ] Update `BD-Matter-ARL-NotMounted` row to reflect the new mounted state.

---

### BD-Matter-P2-D13 — GeneralCommissioning optional Matter 1.5 attributes not exposed

**Affected attributes:** `IsCommissioningWithoutPower` (id 0x0C), TC-feature attrs (ids 0x05–0x09), NetworkRecovery attrs (ids 0x0A, 0x0B). All carry `conformance: "O"` or are feature-gated in matter.js `packages/model/src/standard/elements/general-commissioning.element.ts`.

**Go path:** `internal/north/matter/cluster/core/general_commissioning.go`.

**Rationale:** These attributes are optional in Matter 1.3 / 1.4 and are only required when the corresponding features (TC: TermsAndConditions; NetworkRecovery) are advertised via FeatureMap. OpenCCU-Loom does not implement TC or NetworkRecovery — the FeatureMap reports neither feature. Advertising attributes without the corresponding feature bit is a schema violation; adding stubs without feature-bit support would be worse than omitting them. Implementation is gated on feature-demand; no Apple / chip-tool pair-abort risk in the current build.

---

### BD-Matter-P2-D18 — ScenesManagement stub returns empty / rejects writes

**Go path:** `internal/north/matter/cluster/wire/scenes_management.go`.

**Rationale:** HomeMatic has no scene concept. ScenesManagement (0x0062) is mounted as a mandatory stub on OnOff device types (per Matter device-type conformance). The stub correctly returns `SceneTableSize=0` and rejects AddScene / RemoveScene / StoreScene / RecallScene with `UnsupportedCommand`. This is the same pattern as the matter.js `ScenesManagementBehavior` when no store backend is wired. Full implementation would require a scene store (new SQLite migration) and a HM-side trigger mapping — out of scope for 0.1.0.

---

### BD-Matter-P2-D19 — Groups stub returns NameSupport=0 / rejects writes

**Go path:** `internal/north/matter/cluster/wire/groups.go`.

**Rationale:** HomeMatic has no group concept. Groups (0x0004) is mounted as a mandatory stub on OnOff device types. The stub returns `NameSupport=0x00` (no group name support) and rejects AddGroup / RemoveGroup / AddGroupIfIdentifying. Apple Home, Google Home, and chip-tool all tolerate a Groups stub that advertises `NameSupport=0` — this is the same surface that matter.js's default `GroupsBehavior` exposes when no membership provider is wired. Full implementation requires a group-membership store and coordination with the GroupKeyManagement cluster; deferred to a future release.

---

### BD-Matter-P2-D21 — AdministratorCommissioning 48h uncommissioned-window parity test absent

**Go path:** `internal/north/matter/cluster/wire/admincommissioning.go` — `commissioningWindowMaxSecUncommissioned = 172800`.

**Rationale:** The constant and the `IsUncommissioned` flag are implemented and wired correctly (see `BD-Matter-48h-UncommissionedWindow` in `by_design.md`). A dedicated parity test asserting the 172800-second (48-hour) constant against matter.js's `AdministratorCommissioning.element.ts` is listed as a follow-up item but does not block commissioning correctness — the external chip-tool validation suite passes on this path (62 PASS / 0 FAIL). Adding the test is a hardening step for the next parity-test sweep.

---

### BD-Matter-P2-D24 — `[]any` wire encoder in reply.go covers empty-list only

**Go path:** `internal/north/matter/bridge/reply.go` — `case []any:` encoder loop.

**Rationale:** The `[]any` case in the TLV struct encoder is used exclusively for the AccessControl Extension attribute, which is always an empty list (`[]any{}`). The loop body never executes a per-element type dispatch because the list is empty. A full type-dispatch encoder (mirroring matter.js's per-element codec) would be required only if the Extension list ever carries entries — which it never does in the current implementation (Extension is served as an empty-list placeholder, conforming to the `EXTS` feature surface without real content). This is a documented intentional scope boundary; the by-design entry `BD-Matter-AccessControl-Extension-Empty` above covers the Extension-empty-list design choice.

---

### BD-Matter-P1-D8 — ColorControl.Options attribute is read-only

**Go path:** `internal/north/matter/cluster/light/colorcontrol_server.go` — `case wire.ColorCtrlAttrOptions: return uint8(0), true`.

**Rationale:** matter.js `color-control.element.ts` marks Options (0x000F) as access "RW VO" (view-optional write). In practice Apple Home, Google Home, and chip-tool do not write the Options bitmap on a CT-only bridge — they read it once and cache. The attribute is always 0 (no overrides) which is the correct default for a CT-only profile with no scenes. Implementing a write handler would require persisting the bitmap per device and plumbing it into the command-execution gate; deferred to a future release when a use-case arises.

---

### BD-Matter-P1-D9 — WindowCovering.Mode attribute is read-only

**Go path:** `internal/north/matter/cluster/cover/windowcovering_server.go` — `case wire.WindowCoveringAttrMode: return uint8(0), true`.

**Rationale:** matter.js `window-covering-cluster.element.ts` marks Mode (0x0017) as access "RW VM". The Mode bitmap controls motor-reversal and calibration flags that the HM device exposes through dedicated paramsets, not through a simple writable attribute. Apple Home does not write the Mode attribute on WindowCovering devices in normal operation; neither does chip-tool in the standard pairing suite. Implementing Mode write would require mapping Mode bits to HM MASTER-paramset fields and persisting the state across restarts — deferred to a future release.

---

### BD-Matter-P2-D22 — Wire-encoder parity tests for NOCStruct/FabricDescriptor new fields absent

**Go path:** `internal/north/matter/bridge/reply.go` NOCStruct + FabricDescriptorStruct encoders.

**Rationale:** The NOCStruct Vvsc field (tag 3) and FabricDescriptorStruct VidVerificationStatement field (tag 6) are both implemented and encoded correctly when non-nil. The parity tests that would assert the wire-byte shape of these new fields against matter.js's TLV codec are listed as a follow-up hardening step. The existing `bridge/scenario_tlv_test.go` covers the happy-path for NOCStruct and FabricDescriptor; extending it with VID-verification field cases is the remaining gap.

---

### BD-Matter-P2-D23 — AccessControl EXTS FeatureMap / ExtensionChanged event

**Go path:** `internal/north/matter/cluster/core/access_control.go` — FeatureMap = 0x1 (EXTS bit), `accessControlEventExtensionChanged` const in `MatterEvents()`.

**Rationale:** The EXTS feature bit is intentionally advertised because the Extension list attribute is served (as an empty list). matter.js `AccessControlServer.with("Extension")` sets the EXTS feature flag whenever the Extension attribute is present; we do the same. The `AccessControlExtensionChanged` event (0x1) is listed in `MatterEvents()` for EventList completeness — the event would be emitted if Extension entries were ever written. Since Extension is always empty, the event never fires in practice. This is not a drift: the feature advertisement is correct (EXTS=present means "Extension attribute is served"), and advertising an event that fires on write is conformance-correct even when writes are rejected.

---

### BD-chip-P2-D-L4-NEW-1 — ListWriteBegin / ListWriteEnd notifications absent

**chip reference:** `src/app/WriteHandler.cpp:259-330` `DeliverListWriteBegin` / `DeliverListWriteEnd`.

**Go path:** `internal/north/matter/im/write.go`, `internal/north/matter/endpoint/dispatcher.go`.

**Rationale:** chip notifies cluster servers at the start and end of a list-attribute write so they can apply transactional semantics (all-or-nothing replace). OpenCCU-Loom's write dispatcher treats every list write as a whole-list replace — which is exactly how Apple Home and all known Matter 1.x controllers write the ACL and other list attributes (full list in a single `WriteRequest`, no `ListIndex` partial-append). The spec's `ListIndex` partial-append path (`chip-tool accesscontrol write-attr acl append`) is a chip-tool-specific test-suite path that no production controller uses. Adding `ListMutationNotifier` is deferred until a use-case that requires partial-list-append arrives.

---

### BD-chip-P1-D-L8-NEW-2 — OnPASEEstablished does not re-arm FailSafe to 60 s

**chip reference:** `src/app/server/CommissioningWindowManager.cpp:209-251` — `kFailSafeTimeoutPostPaseCompletion = 60s`.

**Go path:** `internal/north/matter/bridge/pase_provider.go`, `internal/north/matter/bridge/commissioning_window.go`.

**Rationale:** chip re-arms the FailSafe timer to 60 s at PASE session establishment as a defensive net: if a buggy commissioner opens PASE but then never sends an explicit ArmFailSafe, the 60 s cap bounds how long the pending-commissioning state lingers. OpenCCU-Loom sets the FailSafe duration to the full `CommissioningTimeoutSeconds` at OpenWindow time (typically 180 s); an explicit ArmFailSafe from the commissioner overwrites it. In practice every correct commissioner (Apple Home, chip-tool) sends ArmFailSafe within seconds of PASE success — the 60 s post-PASE cap only matters for buggy commissioners. Apple pair correctness is unaffected by this difference. Adding a 60 s re-arm on OnPASEEstablished is a defensive-coding improvement, deferred to a future release.

---

### BD-chip-P2-D-L9-NEW-1 — AddNOC rollback is layer-specific, not canonical

**chip reference:** `src/app/clusters/operational-credentials-server/OperationalCredentialsCluster.cpp:539-552` — `needRevert` boolean with single rollback block.

**Go path:** `internal/north/matter/cluster/core/operational_credentials.go::handleAddNOC` (lines 1265–1330).

**Rationale:** chip uses a single `needRevert` flag that triggers a canonical rollback sequence (FabricTable + GroupDataProvider + AccessControl) on any error after the first persistent write. OpenCCU-Loom's AddNOC path is a sequential store pipeline with per-step rollback that covers the same stores. The code correctly cleans up on failure but does not use a single extracted helper. Refactoring to a canonical `revertAddNOC` helper is a defensive-coding improvement and is deferred; the current behaviour is functionally correct for all tested paths.

---

### BD-chip-P2-D-L10-NEW-1 — ICDConfigurationData-driven MaxInterval rounding absent

**chip reference:** `src/app/ReadHandler.cpp:783-810`.

**Go path:** `internal/north/matter/im/subscription/manager.go`.

**Rationale:** chip rounds the negotiated MaxInterval to the nearest multiple of `ICDConfigurationData::GetIdleModeDuration()` when `CHIP_CONFIG_ENABLE_ICD_SERVER=1`. OpenCCU-Loom is a non-ICD bridge (`ICD` mDNS TXT key is absent; `ICDManagement` cluster advertises `IdleModeDuration=1s` as a bridge-always-active signal). The rounding logic only has effect when the bridge is an ICD server — which it is not and will not be in 0.1.0. When / if ICD capability is activated in a future release, this section must be revisited and the `IdleModeDuration`-based rounding added to `subscription/manager.go::clampMaxInterval`.

### BD-Matter-P2-D13-18 — SetVidVerificationStatement returns InvalidCommand

**matter.js reference:** `node/src/behaviors/operational-credentials/OperationalCredentialsServer.ts:486-505` — validates all three optional fields and calls `fabric.update` with the VID verification statement.

**Go path:** `internal/north/matter/cluster/core/operational_credentials.go::handleSetVidVerificationStatement` (line ~1726) — returns `InvalidCommand` unconditionally.

**Rationale:** VID Verification is an optional Matter 1.3+ feature used by device manufacturers for supply-chain attestation. OpenCCU-Loom is a bridge for Homematic devices whose attestation chain is not managed through Matter VID verification. Returning `InvalidCommand` is spec-conformant per Matter §11.18.6.2 for devices that do not support VID Verification. Re-enable (with real fabric.update integration) if iPhone Multi-Admin commissioning requires VID verification in practice.

---

### BD-Matter-v13-D-AccessControl-Extension-WriteHandler

**matter.js reference:** `AccessControlServer.ts` Extension write-path stores entries and fires `ExtensionChanged` event.

**Go path:** `internal/north/matter/cluster/core/access_control.go` — `MatterWrite` returns read-only error for the Extension attribute (0x0001); EXTS feature and event remain advertised.

**Rationale:** AccessControl Extension writes come from commercial Matter controllers that enforce policy (enterprise key management, multi-admin policy stores). No known consumer controller used in OpenCCU-Loom deployments (Apple Home, Google Home, chip-tool in interop tests) writes Extension entries. Implementing the full Extension store (fabric-scoped TLV blobs, conflict resolution, ExtensionChanged fan-out) is deferred until a concrete use-case arrives. The EXTS feature bit and the ExtensionChanged event remain advertised because the spec requires advertising EXTS when the Extension attribute is served — serving an empty list is conformant.

---

### BD-Matter-v13-D-mDNS-FabricChange-ReAnnounce

**chip reference:** `src/platform/Darwin/MdnsError.cpp` + `MdnsAdvertiser.cpp` — event-driven re-announce on fabric change and reconnect.

**matter.js reference:** `packages/mdns/src/MdnsScanner.ts` — `DefaultBroadcastSchedule` with exponential back-off starting at 1 s, max 90 s.

**Go path:** `internal/north/matter/mdns/zeroconf.go::StartReannounceLoop` + `cmd/openccu-loom/daemon.go` — 30-min fixed cadence; `bridge.EmitFabricAdded/Removed` does not trigger an immediate re-announce.

**Rationale:** The 30-min cadence keeps Apple's `mDNSResponder` cache warm (TTL ≈ 75 min). The `zeroconf.go` trigger channel is wired and functional; coupling it to fabric-add/remove events is a straightforward one-hour wiring task deferred to v1.1. Controllers that query mDNS immediately after fabric add will see the new record at the next periodic announcement (≤ 30 min). No production controller (Apple Home, Google Home, chip-tool) has been observed to suffer from this in testing. See `docs/parity/by_design.md` entry L7-D07 (Matter/matter.js section) for the cadence divergence.

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

### A3-G5 — MetricHubSensor MQTT publishing deferred

Python `hub.py:388,510,554` wires `fetch_metrics_data`, `init_metrics`, and `publish_metrics_refreshed` so that SystemHealth, ConnectionLatency, and LastEventAge sensor values are published to MQTT topics.

OpenCCU-Loom's `MetricHubSensor` family (`internal/model/hub/metrics.go:176-242`) is fully implemented at the model level but the `hub_mqtt_publisher.go` does not yet include a metrics wiring block. The REST API exposes metrics via `GET /api/v1/hub/{central}/metrics`; the MQTT surface is absent.

**Re-activation checklist:**
- [ ] Add `BuildMetricsDiscovery` + `PublishMetrics` to the MQTT topic builder.
- [ ] Add a `--- Metrics ---` block in `wireOneCentral` (`internal/central/adapter/hub_mqtt_publisher.go`) analogous to the programs block.
- [ ] Wire `MetricHubSensor` construction into the Hub coordinator init path.
- [ ] Add unit test covering the metrics publish path.

Go paths: `internal/model/hub/metrics.go`, `internal/central/adapter/hub_mqtt_publisher.go`.

---

### A3-G9 — Inbox MQTT publishing deferred

Python `hub.py:763` fires `SystemEventType.HUB_REFRESHED` when inbox data changes, which the MQTT adapter picks up to publish pending-device notifications.

OpenCCU-Loom's `hub_mqtt_publisher.wireOneCentral` publishes Programs, Sysvars, AlarmMessages, ServiceMessages, InstallMode, and Connectivity — but has no Inbox block. Pending devices waiting in the CCU inbox are not signalled via MQTT; operators must use the SPA or REST API to view inbox state.

**Re-activation checklist:**
- [ ] Add `BuildInboxDiscovery` + `PublishInbox` to the MQTT topic builder.
- [ ] Add an `--- Inbox ---` block in `wireOneCentral` analogous to the AlarmMessages block.
- [ ] Subscribe `hubModel.Inbox.OnUpdate` in the publisher.
- [ ] Add unit test covering the inbox publish path.

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

### A5 — `LinkCoordinator.GetLinksForLocale` — locale parameter accepted but not applied

Python `link.py:168-261` enriches link labels with `get_channel_type_translation(locale=locale)`. Go `LinkCoordinator.GetLinksForLocale` (`internal/central/coordinators/link.go:180`) accepts the `locale` parameter but the channel-type-label enrichment from the i18n catalogue is not yet applied.

The missing enrichment is a v0.1.1 gap. The parameter is accepted in the API so callers need no signature change when the enrichment is added. Translation tables for channel types are available in `internal/i18n/catalogs/`; wiring them into `GetLinksForLocale` is a one-function change.

Go path: `internal/central/coordinators/link.go::GetLinksForLocale`.

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

Python exposes one `HmInstallModeSensor` per interface on the hub. Go's `InstallMode` (`internal/model/hub/install_mode.go`) is a single aggregate tracking install mode state without per-interface granularity.

This is a tracked TODO for a post-0.1.0 milestone. Per-interface install mode requires the `Connectivity`-style per-interface map pattern applied to `InstallMode`. The REST API currently exposes install-mode state as a single hub-level resource; per-interface splitting would require a REST API revision.

**Re-activation checklist:**
- [ ] Add `states map[string]bool` to `InstallMode` mirroring `Connectivity`.
- [ ] Add `OnStateWithInterface` and `List()` methods.
- [ ] Update hub coordinator to populate per-interface install mode.
- [ ] Update REST handler to surface per-interface state.

Go path: `internal/model/hub/install_mode.go`.

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

### A4-P01 — CONVERTABLE_PARAMETERS auto-routing deferred: Go routes combined parameters at call-site

Python `CommandTracker.add_set_value` (command.py:131–134) automatically routes `COMBINED_PARAMETER` / `LEVEL_COMBINED` to `add_combined_parameter` based on parameter membership in `CONVERTABLE_PARAMETERS`. Go's `CommandTracker.AddCombinedParameter` exists, but `InterfaceClient.SetValue` always calls `AddSetValue` without auto-routing.

The auto-routing is deferred by design: the parameter-identity check requires loading the full parameter descriptor at the tracker call-site, which couples the tracker to the parameter catalogue. In Go the call-site (`ValueWriter`) already knows the parameter descriptor; adding the routing there is a one-liner. Implementing it inside the tracker would require injecting a parameter-lookup function, adding allocator pressure on every `SetValue` call. The correct resolution is for the north-bound write path to call `AddCombinedParameter` explicitly when it detects a combined-type write, not for the tracker to re-derive the type internally.

Go path: `internal/client/reliability/command_tracker.go`, `internal/client/value_writer.go`.

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

### A4-P04 — 6 dead ReGa scripts: JSON-RPC equivalents preferred in Go

Python uses all 20 ReGa scripts directly. Six of Go's 20 scripts (`get_alarm_messages`, `get_backend_info`, `get_program_descriptions`, `get_serial`, `get_service_messages`, `get_system_variable_descriptions`) have no production callers because Go prefers the equivalent JSON-RPC methods (`Alarm.getAll`, `System.getSystemInformation`, `Interface.getServiceMessages`).

The JSON-RPC preference is by design: JSON-RPC responses are strongly typed and do not require ReGa TCL script parsing. The scripts are retained as enum constants and embedded `.fn` files because the CCU falls back to them when the JSON-RPC method is not available (older CCU firmware). They serve as backup paths for firmware-compatibility layers and are referenced in integration test helpers.

Go path: `pkg/hmenum/rega_script.go`, `internal/client/rega/`.

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

### A5-P05 — `HubCoordinator.ConnectivityDPs` returns nil: connectivity DP factory deferred

Python `hub.py:141` exposes a `ConnectivityDPs` property returning custom data points for CCU-side metrics. Go's `ConnectivityDPs()` (hub.go:392) returns nil.

Connectivity DPs are deferred by design: the sensor model that surfaces CCU-side metrics (duty cycle, signal strength) as custom DPs is not yet implemented. The stub return ensures the coordinator compiles and callers can nil-check correctly. Tracked as post-0.1.0 in SPECIFICATION.md §2.2 Non-Goals.

Go path: `internal/central/coordinators/hub.go::ConnectivityDPs`.

---

### A6-P01 — UC5/UC6 easymode pipeline not wired in the live REST adapter

Python `_enrich_easymode` (form_schema.py:472–528) runs UC5 (preset chips) and UC6 (subset group membership per parameter) as part of the `FormSchemaGenerator` pipeline. Go has isolated UC5 and UC6 packages under `internal/configui/easymode/` that are tested in isolation, but the live REST adapter `UISchemaAdapter.UISchema` (uischema_adapter.go) does not invoke them.

The wiring is deferred by design: UC5/UC6 output requires a strict SPA-side type contract (`UISchemaParameter.presets` and `UISchemaParameter.subset_group_id`) that is present in `assets/ui/src/lib/api/types.ts`. Wiring it in the live adapter without matching SPA integration tests would silently produce broken UI. The correct path is to wire UC5/UC6 in `UISchemaAdapter.UISchema` together with SPA rendering tests. Planned for the next UI wave.

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

### BD-mDNS-SAT-AlwaysEmitted — SAT key always emitted on commissionable record

**matter.js reference:** `packages/protocol/src/mdns/MdnsBroadcaster.ts::buildCommissionableInstanceData` — matter.js emits SAT conditionally in some code paths.

**Go path:** `internal/north/matter/mdns/service.go::BuildCommissionableService` — always emits SAT.

**Rationale:** Matter §4.3.1.6 lists SAT as a mandatory TXT key on the commissionable `_matterc._udp` record. The parity test `TestParityMdnsServer_CommissionableTXTSchemaLock` enforces this. Always emitting SAT (even at the spec-default 4000 ms) is the correct, spec-conformant behaviour; a conditional gate would violate §4.3.1.6 and break chip-tool and Apple Home commissioning flows that depend on SAT being present. The audit suggestion to gate it as "only if non-default" would constitute a spec regression and is intentionally not applied.

---

### BD-chip-DiagLogs-NoBDX — DiagnosticLogs cluster does not initiate BDX transfers

**chip reference:** `src/app/clusters/diagnostic-logs-server/DiagnosticLogsServer.cpp` — checks `TransferProtocol == BDX` and starts a BDX session.

**Go path:** `internal/north/matter/cluster/core/diagnostic_logs.go:67-181` — `decodeRetrieveLogsIntent` extracts only the `intent` field; `RequestedProtocol` is not examined. When `BDX` is requested, the handler falls back to inline log delivery with `LogStatusExhausted`.

**Rationale:** BDX (Bulk Data Exchange) is used by enterprise diagnostic tools and certification test suites. No production Matter controller (Apple Home, Google Home, chip-tool interop) requests BDX for bridge diagnostic logs. Implementing BDX upload requires a full BDX initiator session (separate exchange ID, block ack protocol, connection-oriented flow control) which is a non-trivial effort with no concrete deployment need in 0.1.0. The spec permits `LogStatusDenied` for devices that do not support BDX; returning `LogStatusExhausted` with inline content is a graceful fallback that satisfies chip-tool's `62 PASS` baseline. No Apple-reject risk.

---

### BD-chip-ICD-Attrs-0x3-0x5 — ICDManagement attributes 0x0003–0x0005 not implemented

**chip reference:** `src/app/clusters/icd-management-server/ICDManagementServer.cpp` — implements `RegisteredClients` (0x0003), `ICDCounter` (0x0004), `ClientsSupportedPerFabric` (0x0005).

**Go path:** `internal/north/matter/cluster/core/icd_management.go:34-100` — implements `IdleModeDuration` (0x0000), `ActiveModeDuration` (0x0001), `ActiveModeThreshold` (0x0002) only. Parity test comment marks this as intentional.

**Rationale:** `RegisteredClients` (0x0003), `ICDCounter` (0x0004), and `ClientsSupportedPerFabric` (0x0005) belong to the ICD client-registration feature (`LITS` / `CIP` feature flags). OpenCCU-Loom is not an ICD server; the bridge is always-active (mains-powered). The three attributes are only meaningful for battery-operated ICD devices that register wake-up clients. Advertising them without ICD-server semantics would mislead controllers into attempting ICD client registration against a non-ICD bridge. No Apple-reject risk — Apple Home does not attempt ICD client registration on bridges.

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

**Go path:** `internal/ccudata/embedded/MANIFEST.json` — `snapshot_date: 2026-04-27`.

**Rationale:** The embedded snapshot is updated deliberately, not on every upstream release. Translation updates only affect labels for new OCCU firmware parameters and do not change any data-path logic. Schema is unchanged; custom overlay files in `translation_custom/` are unaffected. The snapshot will be refreshed before 0.1.0 final. Regeneration is documented in `docs/contributor/regenerate-openccu-data.md`.

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

### A1-BD01 — `AdditionalInformation()` defined but not emitted by north-bound adapters

`ServiceMessages.AdditionalInformation()` (`internal/model/hub/messages.go:487`) and `OperatingVoltageLevelSensor.AdditionalInformation()` (`internal/model/calculated/voltage.go:129`) expose enriched metadata maps. No MQTT state payload or REST response currently merges these maps.

This is by design for v0.1.0: the north-bound payload layer (`internal/payload/`, `internal/north/mqtt/`) assembles sensor state from the typed scalar value only. Merging `AdditionalInformation` into the MQTT state topic would change the schema of an established payload and is therefore scoped to a versioned MQTT-schema extension. The method surface exists and is tested; integration into the north-bound adapters is a post-0.1.0 enhancement.

Go path: `internal/model/hub/messages.go::AdditionalInformation`, `internal/model/calculated/voltage.go::AdditionalInformation`.

---

### A1-BD02 — `GetReadableDataPoints` not used by `MasterPoller`

`Device.GetReadableDataPoints(paramsetKey)` (`internal/model/device/aggregate.go:715`) returns the subset of data points that advertise READ in their operations bitmask. The `MasterPoller` (`internal/client/backends/master_poll.go`) does not call this method — it schedules a full `getParamset` fetch for the entire address/paramset pair after a write.

This is by design: a full `getParamset` round-trip is cheaper in practice than constructing N individual `getValue` calls (one per readable DP), because the CCU's XML-RPC server processes the paramset fetch in a single handler. `GetReadableDataPoints` is retained as a public helper for callers that need the filtered list for display or diffing purposes; removing it would reduce the surface available to future REST/WS handlers that want per-parameter read exposure.

Go path: `internal/model/device/aggregate.go::GetReadableDataPoints`, `internal/client/backends/master_poll.go`.

---

### A1-BD03 — `EmptySimpleEntry` has no production caller in v0.1.0

`EmptySimpleEntry(category)` (`internal/model/schedule/simple.go:284`) constructs a minimal but valid `SimpleEntry` suitable as a UI default when a user adds a new schedule slot. At v0.1.0 the Config UI schedule editor is not yet wired — the REST/WS schedule write path exists, but the Svelte SPA does not yet expose an "add slot" button.

This is by design: the function mirrors the reference implementation's `empty_schedule_entry` property and is part of the model surface that the UI will call once the schedule editor is implemented. It has unit tests via the schedule package test suite. It will gain a production caller in the schedule-editor UI milestone.

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

### A2-BD02 — Hood custom data point is Go-only

`Hood` (`internal/model/custom/hood/hood.go`) exposes the HmIP-COOK fan-speed and light switch as a unified custom DP. The reference implementation has no `Hood` custom DP; the HmIP-COOK is handled generically there.

This is by design: OpenCCU-Loom targets the HmIP-COOK as a first-class device with a dedicated MQTT/Matter surface (fan-speed levels 0..3, light on/off). The reference implementation surfaces it as plain generic DPs. No parity gap exists — the Go DP is an extension, not a divergence from a Python counterpart.

Go path: `internal/model/custom/hood/hood.go`.

---

### A2-BD03 — Valve modulating data point is Go-only

`Valve.Modulating` (`internal/model/custom/valve/valve.go:139-205`) exposes a continuous LEVEL parameter on modulating valve actuators (e.g. HmIP-FALMOT-C12). The reference implementation contains only `CustomDpIpIrrigationValve` (a switch-based on/off valve).

This is by design: modulating valve actuators require a float-valued write path that the switch model cannot express. Go adds this as a dedicated type rather than coercing it into a boolean switch. No Python counterpart exists because the reference implementation does not target modulating valve actuators.

Go path: `internal/model/custom/valve/valve.go::Modulating`.

---

### A2-BD04 — `EffectLight.Effects()` sourced dynamically from `PROGRAM.VALUE_LIST`

`EffectLight` (`internal/model/custom/light/effect.go:27-44`) populates its effects list from the PROGRAM data point's `VALUE_LIST` at construction time. The reference implementation uses a fixed seven-element list `("Off", "Slow color change", …, "TV simulation")`.

This is a deliberate improvement: the dynamic approach automatically reflects any firmware-added effects without a code change. The fixed Python list was chosen for simplicity; Go's approach is strictly more correct for future-proof effect discovery. The downside (empty list before subscription) is guarded by the nil check in `Effects()`.

Go path: `internal/model/custom/light/effect.go::EffectLight`.

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

These are not permanent divergences from the Python reference; the Python equivalents in `websocket_api.py` (`ws_set_schedule_enabled`, `ws_get_link_form_schema`, `ws_get_link_profiles`, `ws_test_link_profile`, `ws_determine_parameter`) are the target parity surface. The wiring of each service into `daemon.go` is deferred to a post-0.1.0 milestone.

Go paths: `internal/north/rest/ws/commands_missing.go` (stub registrations), `cmd/openccu-loom/daemon.go` (service wiring site).

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

### A3-BD-MetricLastEventAge — `MetricLastEventAgeSecs` has no MQTT Discovery/Publish path in v0.1.0

Python publishes `HmEventAgeSensor` via `metrics.py:219`. Go models `MetricLastEventAgeSecs` in `internal/model/hub/metrics.go` but has no corresponding Discovery builder or publish path in `hub_mqtt_publisher.go`.

The omission is by design for v0.1.0: the last-event-age metric is a diagnostic sensor that does not affect device control or state accuracy. The infrastructure (the `Metrics` aggregate, the `MetricKind`, `TranslationKeyForMetric`) is in place; adding the Discovery builder and subscriber is a straightforward extension deferred to a post-0.1.0 depth-parity milestone. The metric is available internally via the REST API health endpoint.

Go path: `internal/model/hub/metrics.go::MetricLastEventAgeSecs`, `internal/central/adapter/hub_mqtt_publisher.go`.

---

### A3-BD-MessageDisplayName — `AlarmMessage`/`ServiceMessage` use `Name` instead of `display_name`

Python `AlarmMessageData` and `ServiceMessageData` carry a `display_name` field (`const.py:2131-2145`). Go's `AlarmMessage` and `ServiceMessage` use the `Name` field from the wire response in `AdditionalInformationIndexed`.

The difference is by design for v0.1.0: the CCU's JSON-RPC message response does not provide a separate `display_name` per entry directly; Python populates it via a secondary device-registry lookup that is deferred to a depth-parity milestone. The HA `json_attributes_template` shows raw device identifiers rather than human-readable names until this is addressed.

Go path: `internal/model/hub/messages.go::AlarmMessage`, `internal/model/hub/messages.go::ServiceMessage`.

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

The omission is by design: Go follows a request-scoped locale model (`internal/reqctx`). REST handlers carry the locale in the request context and resolve labels at the handler boundary, not inside the coordinator. Adding a locale parameter to the coordinator would push presentation concerns into the domain layer.

Go path: `internal/central/coordinators/configuration.go::GetConfigurableDevices`, `internal/reqctx`.

---

### A5-BD-LinkCoordKwargs — `LinkCoordinator` methods take positional args, not keyword-only

Python `AddLink` / `RemoveLink` / `SetLinkInfo` use keyword-only arguments (`link.py:137,355`). Go takes the same parameters as positional arguments.

The difference is cosmetic: Go does not have keyword-only argument syntax. The Go call sites pass values in the documented order; accidental positional swap is a compile-time concern only in fully typed languages, and all string parameters carry distinct semantic roles documented in the method signature.

Go path: `internal/central/coordinators/link.go::AddLink`.

---

### BD-HoodPackage — `internal/model/custom/hood/` has no equivalent in the reference implementation

The `hood` package (`internal/model/custom/hood/hood.go`) implements a range-hood / extractor-fan custom data point for HmIP-COOK class devices (LEVEL = fan speed code 0..3). No equivalent class exists in the reference implementation; the reference covers only the device types it encountered during development, and HmIP-COOK was not among them.

The package is intentional Go-only functionality: the CCU-side device model for HmIP-COOK is well-defined (INTEGER LEVEL parameter, 4 discrete states), and the wrapper provides clean typed access for any future north-bound adapter (MQTT, Matter). The package is currently not imported from any production path (no profile registration), making it dead code from the north-bound perspective. A future commit will either register it in `generated_profiles.go` when a matching device profile is added, or remove it if HmIP-COOK support is deferred beyond 0.1.0.

Go path: `internal/model/custom/hood/hood.go`.

---

### BD-ModulatingValve — `Modulating` valve type has no equivalent in the reference implementation

`internal/model/custom/valve/valve.go` includes a `Modulating` type for proportional-control valves (analogue actuators, 0..100 %). The reference implementation only covers `CustomDpIpIrrigationValve` (on/off irrigation). The modulating path is Go-only extended functionality for HmIP-FALMOT-C12 and similar devices. It follows the same structural pattern (State/Open/Close/SetLevel) and is not a port of any reference-implementation class.

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

### BD-MetricLastEventAgeUnwired — `MetricLastEventAgeSecs` hub sensor not yet populated

`internal/model/hub/metrics.go` defines `MetricLastEventAgeSecs` and wires a `MetricHubSensor` for it in `NewMetricHubSensorPair`. No production code calls `Metrics.Observe(MetricLastEventAgeSecs, …)`: the value would need to come from a scheduler job that measures `time.Since(lastDataPointEventTimestamp)` and writes it on each tick. The scheduler job was deferred because the canonical `lastEvent` clock requires a thread-safe sentinel per `CentralUnit`, which interacts with the unobserved-DP sweep path.

**Current state:** the metric kind, the sensor model, and the discovery builder are complete. The Observe call site is missing. The `loom:reachable` annotation was never added (the item is not in the reachability whitelist); the sensor remains unobserved at runtime and the MQTT state topic for it is never published.

Retained rather than deleted: removing the sensor type would break the `MetricHubSensorPair` shape which tests pin. Once the scheduler job is added (post-0.1.0), no model changes are needed.

Go path: `internal/model/hub/metrics.go::MetricLastEventAgeSecs`, `internal/central/adapter/hub_mqtt_publisher.go`.

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

### BD-A1-V06 — `Device.ReloadDeviceConfig` has no direct production caller; adapter delegates to `RefreshDeviceDescriptionsAndCreateMissingDevices`

`Device.ReloadDeviceConfig` (`internal/model/device/device.go`) runs the per-channel `OnConfigChanged` cascade. The WS command `reload_device_config` reaches the `DeviceReloaderAdapter` (`internal/central/adapter/device_reloader.go`), which calls `DeviceCoordinator.RefreshDeviceDescriptionsAndCreateMissingDevices` for the full interface rather than `Device.ReloadDeviceConfig` for the single device. `Device.ReloadDeviceConfig` therefore has no production caller.

This is by design for the current 0.1.0 scope. The adapter deliberately delegates to the broader refresh because a `DeviceCoordinator.RefreshSingleDevice` method (using `Backend.GetDeviceDescription` + per-channel `GetParamsetDescription`) does not yet exist. From the operator's perspective the result is equivalent: the target device is refreshed. `Device.ReloadDeviceConfig` is the future target for a single-device-level refresh and is kept in the model so the semantic OnConfigChanged cascade is testable independently of the adapter layer. A future `RefreshSingleDevice` method will call it.

Go path: `internal/model/device/device.go::ReloadDeviceConfig`, `internal/central/adapter/device_reloader.go::ReloadDeviceConfig`.

---

### BD-A1-V07 — `DeviceCoordinator.RefreshDeviceLinkPeers` has no production caller

`DeviceCoordinator.RefreshDeviceLinkPeers` (`internal/central/coordinators/device.go`) re-fetches link-peer addresses for every channel of a device and publishes a `LinkPeerChangedEvent`. The method exists to support the boot-time link-peer initialisation that the reference implementation performs in `Channel.__init__ → init_link_peer`.

This is by design for 0.1.0: boot-time link-peer fetching has been deferred because it requires one RPC call per channel with link peers and the performance cost on a large inventory has not been profiled. The method is exercised in contract tests to keep the implementation correct. A future `ccu_wiring.go` boot step will call it after `CheckAndCreateDevicesFromCache`; until then the `RecoveryCompletedEvent` subscriber in `climate_link_peer_refresh.go` uses the cached `ch.LinkPeers()` slice from the initial paramset load.

Go path: `internal/central/coordinators/device.go::RefreshDeviceLinkPeers`.

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
| `AddNewDevicesManually` | Reference `device.py::add_new_devices_manually`; no WS/REST endpoint exposes manual add yet (post-0.1.0) |
| `GetVirtualRemotes` / `GetVirtualRemoteAddresses` | Reference `device.py::get_virtual_remotes`; wired when virtual-remote support reaches the MQTT/REST surface |
| `DeleteDevice` / `HandleDeleteDevices` | Adapter `device_admin.go` calls `backend.DeleteDevice` directly; the coordinator variants are the pure-registry layer the adapter bypasses by design |
| `RefreshFirmwareDataByState` | WS `firmware.refresh` uses the `FirmwareRefresher` interface (`ws_adapters.go`); reference `device.py:710` counterpart, wired post-0.1.0 |
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

`EventCoordinator.EmitDeviceRemovedEvent` (`internal/central/coordinators/event.go`) publishes a `DeviceRemovedEvent` onto the bus. Device removal in 0.1.0 goes through `CentralUnit.RemoveDevice` → `ModelRegistry.Remove` without calling `EmitDeviceRemovedEvent`. WS subscribers for `DeviceRemovedEvent` therefore never receive the event in production.

This is a known gap for 0.1.0: live device-removal push to SPA clients is not yet wired. Device removal is an infrequent operator action and the SPA handles the stale-device case via periodic refresh. The fix is a one-line addition in `internal/central/central.go` at the device-removal path, deferred to a follow-up. The method is correct and its implementation is covered by a contract test; the call site is the missing piece.

Go path: `internal/central/coordinators/event.go::EmitDeviceRemovedEvent`, `internal/central/central.go::RemoveDevice`.

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

### BD-A3-CombinedUnused — combined.HSColor / LevelCombined / WeekProfile / BridgeCombinedDataPoint have no production callers

The `internal/model/combined` package ships four types that have zero production callers:

- `combined.HSColor` / `NewHSColor` / `NewHSColorWithCentral` (`combined/hscolor.go`)
- `combined.LevelCombined` / `NewLevelCombined` / `NewLevelCombinedWithCentral` (`combined/level_combined.go`)
- `combined.WeekProfile` / `NewWeekProfile` / `NewCombinedWeekProfile` (`combined/weekprofile.go`)
- `adapter.BridgeCombinedDataPoint` (`internal/central/adapter/combined_bridge.go`)
- `backends.EncodeHMLevel` (`internal/client/backends/combined.go`)

These are structural scaffolding that never received a device-pipeline caller. Custom device types implement the same semantics inline: `custom/light/color.go` writes HUE/SATURATION directly; `custom/cover/blind.go` encodes LEVEL_COMBINED via an inline helper. `materialiseCombinedDataPoints` — the device-pipeline counterpart to `materialiseCalculatedDataPoints` (`device_pipeline.go:783`) — was never added, so the `CombinedDataPoint` interface (`device/aggregate.go:748`) would not surface them in any case. Only `combined.Timer` implements `IsCombined() bool` and is used productively via `custom/siren/siren.go`.

All five items are retained as tested library code. They represent the correct abstraction for a future `materialiseCombinedDataPoints` pass; the implementation is complete, the wiring is not. `BridgeCombinedDataPoint` is the correct bus-bridge for that future pass.

Go paths: `internal/model/combined/`, `internal/central/adapter/combined_bridge.go`, `internal/client/backends/combined.go`.

---

### BD-Matter-InteractionModelRevision — Loom emits interactionModelRevision on every IM response

matter.js HEAD commit `47e7f2f78` (`#3751`, 2026-05-17) marks `interactionModelRevision` (tag 0xFF) as `TlvOptionalField` in 10 IM message schemas. OpenCCU-Loom emits this field on every response (`internal/north/matter/im/subscribe.go:37-41`, `MatterInteractionModelRevision = 13`).

This is by design: matter.js' own send-path (`TlvDataReportForSend`, `TlvInvokeResponseForSend`) continues to emit the field. Apple Home fails silently when expected fields are absent. Emitting the field unconditionally matches both the prior spec requirement and the actual matter.js wire output; removing it would risk silent Apple Home pairing regressions. No code change is planned.

---

### BD-Matter-WindowCoveringPercentNonNull — deprecated CurrentPositionLiftPercentage projects 0 instead of null

matter.js `window-covering-cluster.element.ts:47-49` marks the deprecated
`CurrentPositionLiftPercentage` (0x0008) attribute `quality "X N"` (nullable)
with `default: null`; OpenCCU-Loom
(`internal/north/matter/cluster/cover/windowcovering_server.go`) always returns
a non-null scaled uint8 derived from the canonical
`CurrentPositionLiftPercent100ths` (0x000E).

By design: the bridge always has a concrete position once a HmIP cover reports
LEVEL, so a null projection would only appear in the brief pre-first-report
window. The deprecated 8-bit attribute is retained purely for legacy
controllers; projecting it from the live 100ths value (rather than tracking a
separate nullable lifecycle) keeps the two position attributes consistent and
avoids a transient null that a legacy controller might mishandle. The modern
100ths attribute carries the correct nullability. (Re-audit 2026-05-31, finding
M2-10.)

### BD-Matter-FailSafeDisarmOwnership — ArmFailSafe(0) disarm is not gated on the arming fabric

chip `GeneralCommissioningCluster.cpp:420` gates the whole ArmFailSafe body —
including the `ExpiryLengthSeconds==0` disarm branch — on
`!IsFailSafeArmed() || MatchesFabricIndex(accessingFabricIndex)`, so only the
fabric that armed the failsafe may disarm it. OpenCCU-Loom
(`internal/north/matter/cluster/core/general_commissioning.go`) lets any CASE
fabric send ArmFailSafe(0).

By design (low impact): a foreign fabric disarming another fabric's failsafe
window mid-commission is already an abnormal flow that cannot be reached in a
normal single-commissioner pairing, and the disarm now runs the full revert
path (re-audit finding F1) so a stray disarm cleans up rather than corrupts.
The ownership guard is a defence-in-depth refinement, not a correctness fix; it
is tracked here rather than implemented to keep the disarm path simple. (Re-audit
2026-05-31, finding F5.)

### BD-Matter-TimedAndQuotaDeferred — per-attribute timed-interaction enforcement and subscribe-quota eviction are unbuilt

Two IM robustness behaviours from chip are intentionally not yet implemented
because the bridged-cluster surface cannot reach them today:

- **Per-attribute/-command `kTimed` enforcement** (chip
  `WriteHandler.cpp:810-812`): an untimed write/invoke targeting a
  timed-required attribute must be rejected with NEEDS_TIMED_INTERACTION. None
  of the bridge's currently exposed attributes/commands (OnOff, LevelControl,
  sensors, GeneralCommissioning) carry the `kTimed` quality, so the gate
  (`internal/north/matter/bridge/receive.go`) only enforces the request-level
  timed flag. A future timed-quality surface (e.g. a DoorLock unlock routed
  through the bridge) will need the per-path predicate.
- **Subscribe per-fabric quota eviction** (chip
  `InteractionModelEngine.cpp:1263`): chip evicts an existing subscription to
  guarantee the per-fabric minimum, returning ResourceExhausted only when truly
  out of resources. OpenCCU-Loom
  (`internal/north/matter/bridge/subscribe.go`) caps at the default 16
  per-fabric and falls through; with a 1–10 controller bridge fleet the cap is
  effectively unreachable.

Both are documented unbuilt extension points, not dormant wiring — there is no
implemented-but-unwired capability behind them. They become real work when the
bridge exposes a timed-quality attribute or targets large controller fleets.
(Re-audit 2026-05-31, findings F3 / F4.)

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
