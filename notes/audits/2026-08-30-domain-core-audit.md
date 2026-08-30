# Domain-core audit — internal duplication, layering, inversion

**Run** 2026-08-30, against `main` at 0.71.0 (untagged) · **Scope**
`internal/central`, `internal/model`, `internal/store`, `internal/alarm`,
`internal/security`, `internal/parameter`, `internal/routingkey`,
`internal/config`, `internal/history`, `pkg` — 532 non-test Go files,
158,785 lines.

**No code was changed.** This document is the report that was asked for
instead.

## Why this pass existed, and why it is not the adapter audit

Three earlier passes read `internal/north/**` against one rule: an adapter
must not re-derive what the model owns. They fixed 37 violations. Every one
of those passes read parts of the core too — but always from the outside,
always asking "did the adapter copy this?".

Nobody had asked whether the core duplicates **itself**. This pass asks three
questions instead:

| | Question | Confirmed |
|---|---|---|
| Q1 | Is the same domain rule defined twice inside the core? | **35** |
| Q2 | Does knowledge sit in the wrong layer? | 3 |
| Q3 | Has target-system knowledge leaked into the core? | **0** |

107 candidates, **38 confirmed, 69 refuted** by two
independent adversarial lenses.

## Coverage

**522 of 532 files read end to end (98%), 10 scanned, 0 unopened.**
Every finder returned a per-file receipt, so "no findings" in a subtree means
*clean* rather than *not looked at*. The ten scanned files are protocol or
generated material, each named with a reason by its reader.

## The result worth stating first: Q3 is zero

No Home Assistant vocabulary, no MQTT topic syntax, no Matter cluster
semantics, and no REST/WS field name was found inside the core. The
hexagonal boundary holds in that direction — which is the direction the
adapter audits could not check, because they were standing on the other side
of it.

The 35 Q1 findings say something different: the boundary is sound, and the
inside is not. A rule defined twice in one package pair is invisible from
either side, because each copy is internally consistent.

## The finding that should not wait

**The sensor-activation rule exists twice, and the two copies already
disagree.** `internal/alarm/activation.go:187` and
`internal/security/subscribe.go:286` both decide which raw wire value of an
enrolled sensor counts as an activation. Both files claim, in their own
comments, to be the single source — and each is, inside its own package.

Four helpers are duplicated one-for-one; `normalizeActive` is byte-identical
in `internal/alarm/resolver.go:193` and `internal/security/subscribe.go:319`.

They have already drifted on one input: an enumeration index outside the
declared value list — a firmware that added a value, a stale paramset cache,
a CUxD channel with a short list.

- `internal/alarm` returns **inactive**, deliberately, pinned by
  `TestResolveActiveIndexOutsideValueListIsInactive`.
- `internal/security` falls through its loop to `normalizeActive`, which
  reports any non-zero index **active**.

Same device, same wire value, opposite verdicts on the two safety surfaces —
and the silent one is the alarm engine. No cross-package guard exists; the
only test naming these helpers pins the alarm copy alone.

## Recurring shapes

Four patterns account for most of the rest.

**A rule defined three or four times, with different behaviour in each.**
The `LEVEL_COMBINED` percent→hex encoding exists four times across four
packages with two roundings; the `REPETITIONS` wire-label rule three times
with three behaviours; the climate `HH:MM ↔ minutes` grammar three times; the
climate base-temperature derivation three times, two of them live with
different rounding; the timer sentinel `111600.0` three times.

**A model copy with no production caller, while an adapter uses its own.**
The sysvar exclusion list (`internal/model/hub/sysvar.go:153` versus
`internal/central/adapter/hub_wiring.go:1146`) and the HM_INIT channel-0
parameter set (`internal/model/device/value_cache.go:408`). The tested,
exported copy is the dead one — so a test passes while the live path uses
something else.

**Two spellings of one limit that count differently.** The CCU's 13-slot
climate limit is a constant in `internal/model/weekprofile/slot.go:12` and
another in `internal/model/schedule/climate.go:37`.

**Two stores reading one archive incompatibly.** `masterprofile` and
`linkprofile` disagree on both the profile key grammar
(`store/masterprofile/store.go:234`) and the constraint grammar (`:63`).

## What the 69 refutations say

The lenses refuted roughly two of every three candidates, and the reasons are
worth trusting: similar-looking device profiles under `model/custom`,
repetition the type system forces, and stores that legitimately persist what
the model defines. Several proposed "duplicates" turned out to be deliberate
divergences of the `EnumLabel` / `EnumLabelFromWire` kind — two readings of
one datum, each right for its caller.

That ratio is itself a result: after four passes over this code, what
survives adversarial review is a real backlog rather than a list of
suspicions.

## Full findings

Grouped by question, then by severity. Each entry names both sites, the
knowledge at stake, the damage when they drift, and whether a guard exists.

## Q1 — the same rule twice inside the core

### The sensor-activation rule exists twice in the core (internal/alarm vs internal/security), and the two copies already disagree on an out-of-range enum index

**high** · `internal/alarm/activation.go:187` ↔ internal/security/subscribe.go:286-301 (func activeFromRaw)

*Knowledge* — "Which raw wire value of an enrolled Homematic sensor counts as an activation" — the default rule (bool direct, number non-zero), the string-label match against a configured active-value list, the enumeration-index→label narrowing, and the fallback when the value list is unavailable. Four helper functions are duplicated one-for-one across the two packages: alarm's resolveActive (activation.go:162) ↔ security's activeFromRaw (subscribe.go:286); alarm's rawInt (activation.go:191) ↔ security's rawIndex (subscribe.go:304); alarm's activationRule.matches (activation.go:41) ↔ security's containsString (subscribe.go:336); and normalizeActive, which is byte-identical in internal/alarm/resolver.go:19

*Damage on drift* — The copies have already drifted on one input: an enumeration index that is not in the declared value list. alarm/activation.go returns (false, true, true) — inactive, deliberately, and internal/alarm/activation_test.go:187 TestResolveActiveIndexOutsideValueListIsInactive pins it. security/subscribe.go:293-300 finds no matching index, falls out of the loop, and returns normalizeActive(raw), which for any non-zero index returns active=true. So for one physical sensor enrolled with active_values, emitting an index beyond the cached DESCRIPTION value list (a firmware that added a value, a stale paramset cache, a CUxD channel with a short list), the alarm engine reads it inactive and never arms/triggers on it, while the security domain reads it active and publishes a detection with its class en

*Guard* — No cross-package guard. `grep -rl 'activeFromRaw\|resolveActive\|normalizeActive' --include='*_test.go' .` returns exactly one file, internal/alarm/activation_test.go, which pins only the alarm copy. Nothing compares the two, and nothing exercises th

### The siren "disable selection" rule is defined twice: the model resolves the declared DEFAULT, the alarm output driver takes VALUE_LIST[0]

**high** · `internal/alarm/outputs/manager.go:469` ↔ internal/model/custom/siren/siren.go:453 (sirenSelectionDefaultString) → internal/model/generic/action_select.go:55 (ActionSelect.DefaultLabel)

*Knowledge* — "Which ACOUSTIC_ALARM_SELECTION label means 'do not sound'." The model owns it and reads it from the CCU paramset description (Descriptor.Default, spelled as a label or an index), falling back to VALUE_LIST[0] only when the descriptor declares nothing. Siren.TurnOff (siren.go:377) is built on that resolution. The alarm output driver re-derives the same value from the flattened []string the SirenDevice port exposes (outputs/ports.go:23 AvailableTones), where the declared DEFAULT is no longer visible, so it can only assume position 0.

*Damage on drift* — The two agree only while every siren's declared DEFAULT happens to sit at VALUE_LIST index 0. On a device whose alarm-selection descriptor declares any other DEFAULT, TurnOff writes the declared disable label while the optical-only fire path writes VALUE_LIST[0] — a real tone. That write is the one taken when the acoustic budget is exhausted or the cycle is optical-only, i.e. exactly when no acoustic budget was reserved (manager.go:438 reserveAcoustic is skipped) and only sirenStopper(inst, acoustic=false) is armed. Concretely: a zone whose OutputPolicy.Silent excludes every acoustic class (manager.go:567) still sounds its ASIR, because the silent path is the one that writes the tone selection — a silent panic becomes audible, unbudgeted (S1) and watchdogged against the optical half only.

*Guard* — No. internal/alarm/outputs/manager_firecycle_test.go:641-655 asserts the selection equals "DISABLE_ACOUSTIC_SIGNAL", but the fake is constructed with tones[0] == "DISABLE_ACOUSTIC_SIGNAL" (line 641), so the test pins the convention rather than the ru

### The sensor-activation rule (raw wire value + value list + active-value labels → active) is implemented twice, in internal/alarm and internal/security, and the two already return opposite verdicts for an out-of-range enum index

**high** · `internal/alarm/resolver.go:185` ↔ internal/security/subscribe.go:275

*Knowledge* — Both take the identical three inputs — the raw Homematic wire value, the configured active-value labels, and the parameter's ValueList — and answer the identical domain question: does this value count as an activation? Each copy claims sole ownership in its own comment. internal/alarm/activation.go:23-27: "Both the live event path and the restore path resolve through this one type. They used to carry separate copies of the rule (paramValueActive and normalizeActive), and a divergence between them would mean a sensor reads active while running and inactive after a restart, or the reverse." internal/security/subscribe.go:277-278: "activeFromRaw is the single activation rule of the domain, shar

*Damage on drift* — One physical channel that both domains observe — a hazard/smoke status enumeration is the case both packages' comments name — reports a non-zero index the model's ValueList does not cover (a firmware that added a value, or a ValueList not yet hydrated on that channel). The Security & Safety aggregate reports the source ACTIVE and lights the hazard/fault plane on REST/WS/MQTT and the SPA; the alarm engine reads the same value as INACTIVE and fires nothing. The operator sees a hazard asserted on one surface while the alarm system stays silent, and nothing logs a disagreement — each side is internally consistent. The reverse direction is equally reachable if either fallback is later touched, and it is the reverse direction (alarm sees active where security does not) that produces a false alar

*Guard* — No. internal/alarm/activation_test.go:21 pins alarm's own copy against alarm's own default ("reproduce normalizeActive exactly, value for value") — it re-states one copy, it does not tie it to the other package. Grep over internal + tests for `active

### Central-link eligibility is decided twice: the model excludes virtual remotes, the adapter that actually dispatches does not

**high** · `internal/central/adapter/central_links.go:275` ↔ internal/model/device/aggregate.go:545

*Knowledge* — "Which devices are candidates for CCU central-link (press-event forwarding) management." Both sites name the same reference rule; aggregate.go:538-544 states it as two conjuncts — interface in {BidCos-RF, BidCos-Wired, HmIP-RF} AND model not a virtual-remote pseudo-device (virtualRemoteModels = HM-RCV-50, HMW-RCV-50, HmIP-RCV-50, aggregate.go:522-526). The adapter copy implements only the first conjunct. Device.RelevantForCentralLinkManagement has ZERO production callers (grep over internal, pkg, assets, tests: only aggregate.go and device_test.go); isCentralLinkInterface is the sole gate on all three production entry points — CentralLinksStatus (central_links.go:86) and runReport (:184), wh

*Damage on drift* — The model rule is already dead and the two already disagree, so this is drift that has landed, not drift that might. All three virtual remotes sit on an eligible interface (HM-RCV-50 on BidCos-RF, HMW-RCV-50 on BidCos-Wired, HmIP-RCV-50 on HmIP-RF) and their press parameters are real VALUES parameters (aggregate.go:528-532: "Their press parameters are real, clickable actions"), so channelHasPressEvents (central_links.go:288) passes too. Consequence: GET /devices/HM-RCV-50-address/central-links returns Supported:true with every channel Eligible — the SPA renders the create/remove buttons — and POST issues ReportValueUsage(<channel>, "PRESS_SHORT", 1) against the pseudo-device. That is verbatim the outcome aggregate.go:517-521 says is skipped at the dispatch boundary: "Including them in cent

*Guard* — No guard pins the two together. Each side has its own test, which is exactly what keeps both copies internally consistent while they disagree: internal/model/device/device_test.go:743 TestRelevantForCentralLinkManagementVirtualRemoteFalse asserts the

### The HA light command payload contract (state/brightness/colour) is implemented twice — the dispatcher copy silently drops colour, kelvin and effect

**high** · `internal/central/adapter/custom_dp_dispatcher.go:280` ↔ internal/model/custom/light/payload.go:246

*Knowledge* — What an HA-shaped light command payload means: which keys exist, that brightness is 0..255 and must be divided by 255 to reach the model's 0..1 LEVEL, that a bare brightness with no state is a level-only adjust, that `{"level":x}` is the legacy scalar form, and that colour / color_temp_kelvin / effect ride the same object and must be applied alongside the on/off decision.

*Damage on drift* — The two copies have already drifted in both directions. The model copy applies colour/kelvin/effect via applyHALightAttributes (payload.go:190-217); the dispatcher copy has no colour handling at all, so `set_level` with `{"state":"ON","color":{"h":120,"s":80}}` reaching the CDP-invoke path (REST POST /devices/.../cdps/{name}/set_level, ws cdp.invoke, and the MQTT custom-dp invoke topic via MQTTCommandSink.InvokeCustomDP → CustomDPDispatcher, mqtt_sink.go:303) turns the lamp on and silently ignores the colour — exactly the failure the model-side comment says it exists to prevent. A colour-only `{"color":{...}}` returns ErrBadParam ("set_level needs state, brightness, or level", line 313) on the dispatcher path and sets the colour on the service path. Conversely the dispatcher accepts any ca

*Guard* — No. TestSPACDPOperationsMatchDispatcher (tests/contract/spa_cdp_operation_contract_test.go) pins the SPA widgets against the dispatcher only; TestServiceMethodScalarArgPinned (tests/contract/service_method_topic_routing_test.go:55) pins the scalar `s

### The week-profile slot predicate the model declares as its home is re-derived, looser, in the adapter — and the pipeline drops parameters on the copy's answer

**high** · `internal/central/adapter/device_pipeline.go:2013` ↔ internal/model/weekprofile/rawconvert.go:97

*Knowledge* — "Which MASTER parameter name is one cell of a device week profile." The model declares itself the home in prose ("Keeping the predicate here means a change to either format updates the parser and its consumers together") and internal/store/visibility consumes it correctly (reason.go:171 `if weekprofile.IsParameterName(name)`). internal/central/adapter carries a second, hand-rolled implementation — week_profile_filter.go:597 `isWeekProfileSlotParameter`, whose Schema-A branch accepts `P` + digit 1-6 + `_` + any run of [A-Z0-9_] — and device_pipeline.go:2013 is its only paramset-hydration consumer. The two grammars disagree in both directions, and each side's own test records the disagreement:

*Damage on drift* — Silently and unrecoverably drops MASTER parameters. Any MASTER parameter named `P<1-6>_<uppercase body>` that is not a real slot — `P1_X`, `P2_MODE` — is `continue`d at device_pipeline.go:2013 before `resolveDataPointWithUnIgnore`, so no data point is ever built: it is invisible to REST, MQTT, the SPA, MCP and diagnostics, and an operator `un_ignore.txt` entry cannot bring it back, because every other suppression in this pipeline creates the DP and only marks it NoCreate — the file states that principle at line 2027 ("openccu-loom's architecture says every parameter becomes a DP"). At the same time line 2022 fires `attachWeekProfileToChannel`, so the channel advertises a week profile whose parser (`ParseClimateRawParamset`, same regex as `IsParameterName`) has no cell for that parameter —

*Guard* — No cross-site guard. Each implementation is pinned in isolation — internal/central/adapter/week_profile_filter_test.go for the copy, internal/model/weekprofile/profile_helpers_test.go and internal/store/visibility/reason_test.go for the home — which

### The channel-0 diagnostics parameter set is hand-listed in the event bridge and omits the classic-HM spellings pkg/hmenum declares

**high** · `internal/central/adapter/eventbridge.go:2263` ↔ pkg/hmenum/parameter.go:268

*Knowledge* — Which Homematic parameters are device-level channel-0 state/diagnostics. pkg/hmenum declares it as a named set with an accessor (`IsDeviceLevel`, parameter.go:282) and knowingly carries BOTH CCU spellings of the two parameters that have them: ParameterLowBat = "LOW_BAT" / ParameterLowbat = "LOWBAT" (parameter.go:95) and ParameterDutyCycle = "DUTY_CYCLE" / ParameterDutycycle = "DUTYCYCLE" (parameter.go:66). Both spellings are first-class elsewhere in the core: internal/model/custom/default_data_points.go:18-21 marks all four for creation on channel offset 0, and internal/model/device/availability.go:249 checks `hmenum.ParameterLowBat, hmenum.ParameterLowbat` when deciding battery state.

*Damage on drift* — The drift is already realized, in the direction that loses data. `ch.Parameter` is an exact map lookup (internal/model/device/channel.go:392-396 `return c.valuePoints[p]`), so on a classic BidCos device whose channel 0 carries LOWBAT and DUTYCYCLE rather than LOW_BAT / DUTY_CYCLE, the loop finds nil for both and the retained `<addr>/diagnostics` topic silently ships without a low-battery and without a duty-cycle field. Nothing errors; the topic just has eight possible keys and publishes six. The same devices' per-parameter `channels/0/values/LOWBAT/state` topics are published normally, so the aggregate an operator subscribes to for device health disagrees with the granular plane it claims to summarize. Any later addition to DeviceChannel0Parameters likewise never reaches this topic.

*Guard* — No. `IsDeviceLevel` has no production caller at all (only pkg/hmenum/parameter_test.go:130-149); the only test touching this function is internal/central/adapter/eventbridge_test.go:487 `TestPublishDeviceDiagnosticsNilMQTT`, which exercises the nil-M

### Sysvar-exclusion rule lives in two packages; the model's exported, tested copy has no production caller

**high** · `internal/central/adapter/hub_wiring.go:1146` ↔ internal/model/hub/sysvar.go:153

*Knowledge* — Which CCU system variables are internal scratch values that must never enter the hub model. Both sites cite the same reference authority — the adapter comment says "model/hub/hub.py `_EXCLUDED`" and "const.py `IGNORE_SYSVARS_BY_ID`", the model comment says "the Go equivalent of Python's _EXCLUDED list".

*Damage on drift* — The live filter is the private one in the wiring layer (hub_wiring.go:1202, `if v.Name == "" || sysvarIsExcluded(v.Name, v.ID) { continue }`); it is the only gate a sysvar passes before entering the model, MQTT discovery, Matter and REST. The model's copy is exported, documented and unit-tested but has ZERO production callers — grep over internal, pkg, cmd and tests shows `IsExcludedSysvar` / `CleanSysvarNames` referenced only by their own tests. A maintainer adding the next CCU scratch-name token to `excludedSysvarMarkers` (the obvious home: exported API, in the package that owns hub domain, with tests) changes nothing — the daemon keeps importing and publishing the variable. The two halves already disagree: the model copy has no equivalent of the fixed IDs 40/41 rule, so any future north

*Guard* — No cross-check. Each copy has its own green test — internal/central/adapter/sysvar_exclusion_test.go (TestSysvarIsExcluded) and internal/model/hub/sysvar_sensor_metric_hub_test.go (TestIsExcludedSysvarOldVal / …PcCCUID / TestCleanSysvarNamesFiltersEx

### WEEK_PROGRAM channel-lock bitmask table and the WPTCLS/WPTCL wire encoding exist twice — once in the model that owns them, once in the adapter fallback

**high** · `internal/central/adapter/schedule_enabled.go:43` ↔ internal/model/weekprofile/channel_keys.go:20 and :241

*Knowledge* — Which bit of the CCU's WEEK_PROGRAM_CHANNEL_LOCKS integer belongs to which `<actor>_<sub>` schedule channel, and how a schedule enable/disable is rendered as a COMBINED_PARAMETER write (`WPTCLS=<bits>,WPTCL=<0|2>`). This is Homematic wire semantics that internal/model/weekprofile owns — it also owns the reverse direction (ParseChannelLocks, BitmaskToChannelKey) and the documented inverted-bit rule. internal/central/adapter is wiring, and holds a full second copy of the forward half only.

*Damage on drift* — Two live write paths use different copies. The modelled path (weekprofile/datapoint.go:648-663) resolves via ChannelKeyToBitmask + BuildCombinedParameterValue; the adapter fallback (schedule_enabled.go:120-140), taken exactly when a device's week profile is not modelled or the pipeline has not attached a writer, resolves via its own table and its own format string. Correct a bit assignment or a WPTCL mode value in the model and the fallback keeps emitting the old frame: the operator's toggle appears to succeed, the SPA state is updated optimistically by applyScheduleEnabledToModel, and the device's weekly program stays in the previous mode — or a different actor channel is toggled than the one asked for. Nothing fails, because each copy is internally consistent and the fallback runs on the

*Guard* — No. The only test that touches the adapter table asserts it against itself — internal/central/adapter/adapter_multi_unit_test.go:8123 `if bitmask != scheduleActorChannelBitmasks["1_1"]` — which is a tautology and cannot detect divergence from the mod

### The climate slot-key regex is compiled twice in two packages, with the bare (prefix-less) BidCos form recognised by only one of them

**high** · `internal/central/adapter/schedules.go:100` ↔ internal/model/weekprofile/rawconvert.go:46

*Knowledge* — The CCU climate week-profile MASTER key grammar, including the fact that classic BidCos thermostats (HM-CC-RT-DN, HM-CC-RT-DN-BoM, HM-CC-VG-1) carry it prefix-less on the device root.

*Damage on drift* — The two spellings have already drifted in both directions. (a) The model's pattern requires the `P<n>_` prefix, so weekprofile.ParseClimateRawParamset / BuildClimateRawParamset / IsParameterName are blind to the bare schema the adapter supports with a dedicated Path 3 (schedules.go:179-189, returning device.ChannelNumberDevice), a dedicated serializer (serializeClimateScheduleBare, :1389) and a dedicated detector (climateScheduleIsBare, :1461). Consequences: weekprofile.IsParameterName returns false for all 7×13×2 bare cells of an HM-CC-RT-DN, and the typed I/O path in schedule_io.go gates on the adapter's bare-tolerant hasScheduleParams (:331) but then parses with the model's P-required pattern, so ReloadAndCacheSchedule caches an empty-but-successful *schedule.Climate for such a device,

*Guard* — No cross-package guard. internal/central/adapter/schedules_bare_test.go exercises the adapter pattern; internal/model/weekprofile/rawconvert_test.go exercises the model pattern; nothing asserts the two accept the same key set.

### Primary-client candidate interface set defined twice: an ordered private slice in coordinators, an exported set in hmenum that nothing in production reads

**high** · `internal/central/coordinators/client.go:165` ↔ pkg/hmenum/interface.go:264

*Knowledge* — Which CCU interfaces can serve as the primary InterfaceClient for sysvar / program / JSON-RPC-facade calls — i.e. which radios' daemons expose the ReGa-backed calls that need exactly one client. This is an interface classification set, and CLAUDE.md names pkg/hmenum as the home for exactly those ("interface classification sets"); pkg/hmenum/interface.go:146 states it in the file itself: "Classification sets that drive protocol selection and capability computation. Keep in sync with SPECIFICATION.md §5.1 — the contract tests in tests/contract/ assert exactly these memberships."

*Damage on drift* — The exported hmenum set has NO production consumer — `grep -rn 'PrimaryClientCandidateInterfaces'` returns only its own declaration and pkg/hmenum/interface_routing_test.go. The only code that actually selects a primary client reads the private slice. So the two can diverge with nothing failing: editing hmenum.PrimaryClientCandidateInterfaces (the documented home, the file a maintainer opens when SPECIFICATION.md §5.1 membership changes — e.g. HmIP-Wired gaining its own interface, or BidCos-Wired being dropped) changes nothing about which client PrimaryClient returns. ClientCoordinator.PrimaryClient would then treat the newly-classified interface as a non-candidate and fall through to its "first connected non-candidate" branch (client.go:198), silently routing every sysvar and program call

*Guard* — No cross-check guard. Two guards exist and each pins one copy against its own hard-coded literal, independently: internal/central/coordinators/primary_client_test.go:24 TestPrimaryClient_CandidateSetDefined and pkg/hmenum/interface_routing_test.go:12

### The "central is operational" state set is defined twice — the state machine owns it, but every background job runs the private copy

**high** · `internal/central/jobs.go:255` ↔ internal/central/statemachine/central.go:420

*Knowledge* — Which CentralState values mean "the central is serving traffic", i.e. which lifecycle states permit background work. `unit.StateMachine` is a `*statemachine.Central` (internal/central/central.go:74), so jobs.go reaches past the owner's own predicate to re-derive it from the raw `State()` string. Measured: `grep -rn "IsOperational" internal cmd tests` returns no production caller of `statemachine.Central.IsOperational` outside its own package test — the declared owner's predicate is dead while the copy is what the daemon runs.

*Damage on drift* — The set is two-valued, so it is exactly the kind that grows. Adding a state to the operational set (a RECOVERING central that should keep refreshing, say) or removing DEGRADED is a one-line edit on `statemachine.Central.IsOperational` — the predicate every reader would look at. jobs.go keeps the stale set, and `gatedRun` / `gatedRunWithDevicesCreatedGate` / `reconcileRun` gate ALL scheduled work on it: hub.sysvar_refresh, hub.program_refresh, hub.inbox_refresh, hub.service_messages_refresh, hub.alarm_messages_refresh, hub.system_update_refresh, hub.install_mode_refresh, the three firmware jobs, central.refresh_client_data and central.reconcile. They would silently stop firing (or start firing against a CCU the state machine considers down) with no error, no log and no failing test — a daem

*Guard* — No. Both copies have their own green tests and neither pins the agreement: internal/central/jobs_test.go:798 `TestIsOperationalReturnsTrueOnlyForRunningOrDegraded` exercises the private copy through `isOperational(c)`; internal/central/statemachine/s

### The HM LEVEL_COMBINED percent→hex encoding exists four times across four packages, carrying two different rounding behaviours

**high** · `internal/model/custom/cover/blind.go:593` ↔ internal/parameter/converter.go:259 (plus internal/model/value/converter.go:116 and internal/client/backends/combined.go:184)

*Knowledge* — The CCU's BidCos combined-parameter encoding: one byte per axis = position × 100 × 2, rendered as Python `format(n, '#04x')`.

*Damage on drift* — blind.go:587-591 records the bug the divergence produces, in its own words: "The product is rounded, not truncated: 0.29, 0.57 and 0.58 land just below an exact half-percent step in binary64, so truncation moved the blind one 0.5 % step below the commanded position while the HmIP branch of the same switch (and internal/client/backends EncodeHMLevel, the twin encoder) rounded half-up." internal/parameter's copy still truncates; internal/model/value's rounds (math.Round); internal/client/backends' rounds half-up and additionally clamps to [0,1]; blind.go's rounds and does not clamp there (callers clamp upstream). Currently dormant: grepping the three exported names over internal/, pkg/, cmd/ and tests/ excluding *_test.go finds no non-test caller — internal/client/backends/combined.go:180-18

*Guard* — No cross-package guard. internal/client/backends/combined_test.go:202 TestEncodeHMLevel pins only the backends copy; each of the four copies is exercised, if at all, against itself.

### The cover's motion parameter is resolved twice, and the two resolutions land on different channels for every HmIP cover

**high** · `internal/model/custom/cover/cover.go:632` ↔ internal/model/custom/cover/cover.go:234 (resolveDirectionDP), whose home is internal/model/custom/field_resolve.go:162 (ResolveSlotOnCarryingChannel)

*Knowledge* — Which parameter, on which channel, carries a cover's motion signal for a given device family. The profile schema states it: internal/model/custom/profile_configs.go:71-74 maps IPCover's FieldDirection under `ChannelFields: map[int]map[hmenum.Field]FieldValue{ -1: { ... hmenum.FieldDirection: Bare(hmenum.ParameterActivityState),` — i.e. on the channel at offset -1, not the cover's own. internal/model/custom/field_resolve.go:118-122 names exactly this as the defect the resolver exists to prevent: "Reaching for a fixed parameter name on the DP's own channel instead is the defect this exists to prevent ... the lookup returns nil, the accessor reports the feature as unsupported, and nothing fails

*Damage on drift* — Measured, not hypothetical. RebaseChannelGroup (internal/model/custom/profile_schema.go:270, `rebased[ch+groupNo] = fields`) turns offset -1 into an absolute channel number. In the shipped device snapshot (tests/integration/testdata/model_snapshot_openccu-loom.json) every HmIP-BROLL / HmIP-FROLL / HmIP-FBL cover channel (4,5,6) carries group_no 4, so the schema resolves FieldDirection to channel 3 (SHUTTER_TRANSMITTER / BLIND_TRANSMITTER, which does carry ACTIVITY_STATE), while Subscribe binds channel 4/5/6's OWN ACTIVITY_STATE. Same for HmIP-DRBLI4 and HmIPW-DRBL4. So c.directionDp and c.direction observe two different data points on the same device. c.directionDp drives the Matter DataVersion bump (cover.go:222) and the OnMatterValueChanged fan-out (matter.go:60); c.direction drives IsOp

*Guard* — No. internal/model/custom/cover/cover_subscribe_test.go:40 TestCoverSubscribeRoutesDirectionUpdates and :74 TestCoverSubscribeRoutesActivityStateUpdates both place the parameter on the cover's own channel with a zero-value group, so they pass without

### The CCU timer-disabled sentinel 111600.0 is declared three times in three packages, and the one test that spans two of them derives its expectation from the coupling it appears to guard

**high** · `internal/model/custom/light/light.go:753` ↔ internal/model/custom/mixins.go:129 (and a third copy at internal/model/combined/timer.go:44)

*Knowledge* — The exact float64 (111600 seconds) the CCU reads as "this timer is not in use", together with the rule that it must be encoded as (111600, unit=hours) rather than promoted through the ordinary 16343-second unit chain. One CCU wire fact, three independent declarations: internal/model/custom/light, internal/model/custom, internal/model/combined.

*Damage on drift* — The two copies are coupled at runtime by exact float equality, not by import. custom.EncodeTimerDuration (mixins.go:154-157) recognises the sentinel with `if secs == timerNotUsed { return clamp(secs), 2 }`; Light.stageOnTimeParam (light.go:1039-1052) and Light.TurnOn (light.go:645) feed it `time.Duration(NotUsed * float64(time.Second))` from light's own copy. If either constant moves, the equality silently fails and the value falls into the promotion chain instead: e.g. 111660 s -> 111660/60 = 1861, unit=1 -> the signal light receives a real 31-hour on-timer where the daemon meant "cancel the timer". The device then switches itself off long after a plain turn-on — precisely the regression TestFixedColorLightPlainTurnOnSendsNotUsedSentinel was written to prevent. Both halves still compile a

*Guard* — No cross-pin. internal/model/custom/custom_test.go:240 pins only custom's copy to the literal (`{"111600s→(111600,H)", time.Duration(111600) * time.Second, 111600, 2}`); light.NotUsed and combined.timerNotUsed are pinned to no literal anywhere (grep

### materialize.go parses the channel number out of a CCU channel address with its own hand-rolled scanner instead of pkg/hmtypes, and the existing address guard cannot see it

**high** · `internal/model/custom/materialize.go:641` ↔ pkg/hmtypes/address.go:55 (ChannelNo); the same value also has pkg/hmtypes/datapoint_key.go:75 DataPointKey.ChannelNo()

*Knowledge* — The CCU channel-address grammar — a channel address is a device address plus a ':'-separated numeric suffix, first colon, non-negative integer. pkg/hmtypes owns it (separator constant, ChannelNo, SplitChannelAddress, and the channelAddressPattern regex `^[0-9a-zA-Z-]{5,20}:\d{1,3}$`). materialize.go:611-621 re-derives it from a value that is already a hmtypes.DataPointKey — `channelAddr := dp.DataPointKey().ChannelAddress` — so the canonical parse is a method call away on the very object being taken apart.

*Damage on drift* — This parse gates a device-wide visibility decision. lookupProfileForCustomDP returns (Profile{}, false) when the parse fails; deviceAllowsUndefinedDPs (materialize.go:583-585) turns that into `return false`, and SuppressUndefinedGenericDataPointsWithExempt then force-marks every unmarked VALUES and MASTER data point on the device with DataPointUsageNoCreate — the generic data points vanish from MQTT discovery, REST and the SPA with no error anywhere. The two parses already disagree on two inputs: `atoiSmall` rejects a leading '+' that strconv.Atoi accepts, and it overflows silently on a long digit run where Atoi returns an error (unreachable on today's wire, but nothing constrains it). The header comment also asserts a property of the data source that contradicts the source of truth: "The

*Guard* — Partially, and it is blind here. tests/contract/address_rule_single_source_test.go:46 TestAddressSplittingHasOneSource walks internal/ and pkg/ for private address parsers, but its filter at :57 is `!strings.Contains(strings.ToLower(fn.Name.Name), "d

### The CCU combined-timer unit encoding is implemented twice inside internal/model, and the two copies already return different values

**high** · `internal/model/custom/mixins.go:140` ↔ internal/model/combined/timer.go:390 (constants at :37 and :44)

*Knowledge* — The CCU's timer wire encoding: a duration is written as a (value, unit) pair; the unit ordinals are 0=seconds, 1=minutes, 2=hours; promotion to the next coarser unit happens above 16343 (not 60/3600); and the exact value 111600 s is the "timer not used" sentinel that must be written as (111600, hours) rather than converted. Both copies carry all four facts independently — same two magic numbers, same promotion chain, same sentinel rule, two spellings of the threshold constant name (timeUnitThreshold vs timerUpperBoundSeconds) and two spellings of the unit ordinals (bare int32 literals 0/1/2 vs the named TimerUnit enum).

*Damage on drift* — They have already drifted in rounding, read from the code (not executed): for 20000 s, custom.EncodeTimerDuration promotes to minutes and then clamps through int32(333.333…) = 333, i.e. 19980 s — 20 s silently dropped; combined.RecalcUnit returns 333.333… as float64 and Timer.SetDuration passes it to SetValue unrounded (timer.go:272). So the same requested duration reaches the CCU as two different values depending on whether it flows through a custom siren/light (EncodeTimerDuration is called from siren/siren.go:298, siren/sound.go:377, light/light.go:867/877/1053, light/color.go:807) or through a combined Timer DP. Whether the truncation is wrong on the wire depends on the DURATION_VALUE parameter type, which I did not read from a device description — that half is unverified. The structur

*Guard* — No. Each copy has its own local test asserting against itself — internal/model/custom/custom_test.go:211 TestEncodeTimerDurationThreshold and internal/model/combined/combined_test.go:179 TestTimerRecalcUnitThresholds — and neither compares the two. B

### The numeric→REPETITIONS wire-label rule is defined three times in internal/model/custom, with three different behaviours

**high** · `internal/model/custom/textdisplay/textdisplay.go:761` ↔ internal/model/custom/siren/sound.go:278 (third copy: internal/model/custom/light/sound_led.go:74)

*Knowledge* — How a caller's logical repetition count maps onto the label grammar of the shared CCU parameter REPETITIONS (hmenum.ParameterRepetitions). Three model packages each own a private answer: textdisplay hardcodes the labels and returns "" out of range; light hardcodes the same labels and returns an error; siren refuses to hardcode and looks the label up positionally in the device's own VALUE_LIST (its comment at sound.go:270-272 states the spelling is device-dependent: "conventionally \"INFINITE\" or \"INFINITE_REPETITIONS\"" / "\"NO_REP\" or \"NO_REPETITION\""). textdisplay.go:225 documents yet a fourth spelling for the same field ("NO_REP", "REPETITIONS_2", "INFINITE").

*Damage on drift* — Measured against the fixtures the code itself cites (../godevccu/internal/embed/data/paramset_descriptions/HmIP-WRCD.json → VCU4243444:3/VALUES and HmIP-MP3P.json → VCU1543608:2,:6,:7,:8/VALUES): REPETITIONS carries 16 entries, ['NO_REPETITION','REPETITIONS_001'…'REPETITIONS_014','INFINITE_REPETITIONS'] — max label 014, not 018. So the same operator input diverges three ways. repeat=15: (a) textdisplay produces "REPETITIONS_015", which its own list validator then rejects (textdisplay.go:677-682, list filled from that VALUE_LIST in init.go:71-73) — ErrInvalidRepetitions, the write never reaches the wire; (b) light writes "REPETITIONS_015" straight into the paramset with no list check (sound_led.go:238 and :262) — a label the device does not offer; (c) siren clamps slot to len-1 and returns

*Guard* — No cross-package guard. Each copy is pinned only by its own package test — internal/model/custom/light/light_test.go:333 (TestConvertRepetitions) and internal/model/custom/siren/siren_test.go:215 (TestConvertPlayRepetitionsIndex); textdisplay's conve

### The HM_INIT channel-0 parameter set is defined twice — and the model's copy has no production caller

**high** · `internal/model/device/value_cache.go:408` ↔ internal/central/adapter/relevant_init.go:22

*Knowledge* — "Which channel-0 VALUES parameters must be force-loaded during boot because fetch_all_device_data may omit them" — the set that drives availability tracking (UNREACH / STICKY_UNREACH / CONFIG_PENDING). Both copies carry the same three parameters, the same channel-0 restriction, and the same not-yet-observed gate; both name themselves `relevantInitParameters`. They differ already: the model copy additionally skips non-readable parameters (`if !dp.ParameterData().Operations.IsReadable() { continue }`, value_cache.go:453) while the adapter copy has no readability check.

*Damage on drift* — `grep -rn "LoadInitialDataPoints" --include='*.go' .` returns only internal/model/device/value_cache.go and internal/model/device/load_initial_test.go — the model's loader has NO production caller. The live boot path is seedRelevantInitParameters, called from ccu_wiring.go:1226, cuxd_wiring.go:270 and hotplug_wiring.go:78. So the copy that carries the doc comment ("the selective boot-time loader", "callers that want only the critical init subset call this method first") is the dead one. A maintainer adding a fourth parameter (e.g. UPDATE_PENDING, already in hmenum.DeviceChannel0Parameters) to the documented set changes nothing at boot: availability tracking silently keeps defaulting to "reachable" for that signal until the first push event, which is the exact gap the adapter comment says t

*Guard* — No. `relevantInitParameters` appears in adapter tests only against the adapter's own copy; load_initial_test.go exercises the model copy through the method. Nothing compares the two sets, and TestEveryWiringSetterHasAProductionCaller does not cover a

### RequiresPolling re-derives the product group from an interface-ID string prefix, in an identifier space that never matches in production

**high** · `internal/model/generic/datapoint.go:1504` ↔ pkg/hmenum/backend.go:60

*Knowledge* — Which physical product family a device belongs to, and hence whether the CCU pushes MASTER-paramset changes for it. pkg/hmenum owns this twice over: ProductGroupForModel(model, iface) is the canonical classifier (model-name prefix wins, interface is the fallback — ADR 0023), and PushesConfigPendingFor (pkg/hmenum/interface.go:136) already encodes the HM/HMW-vs-HmIP split as a switch on ProductGroup. The generic DataPoint re-derives the same classification by string-prefixing Key.InterfaceID against the ProductGroup constants (ProductGroupHM = "BidCos-RF", ProductGroupHmW = "BidCos-Wired", pkg/hmenum/backend.go:40,43) — even though Spec.DeviceModel (datapoint.go:97, "the parent device's CCU m

*Damage on drift* — The re-derivation reads the wrong identifier space, which the repo deliberately typed apart. hmtypes.NewWireInterfaceID (pkg/hmtypes/wire_interface_id.go:46) returns `centralName + "-" + string(iface)`, and internal/central/adapter/device_pipeline.go feeds that same `interfaceID` variable both to hmtypes.ParseWireInterfaceID (:408, :435, :574, :2096) and to hmtypes.NewDataPointKey (:2035). So on any named central Key.InterfaceID is "ccu1-BidCos-RF", and HasPrefix(…, "BidCos-RF") is false for every device the daemon actually ingests — the HM/HMW arm can only fire on the empty-central-name fixture. Second divergence even if the prefix matched: an HM-* model on the VirtualDevices interface classifies as ProductGroupHM canonically (model prefix wins) but as non-HM here. Damage is latent rather

*Guard* — internal/model/generic/datapoint_validity_test.go:155 and :172 pin the rule with `InterfaceID: string(hmenum.ProductGroupHM)` — a bare interface name the production path never produces — so the guard is green against a fixture that cannot occur.

### The sensor quantity / value-behavior metadata tables exist twice in the core — internal/model/generic and internal/parameter — and have already drifted

**high** · `internal/model/generic/quantity.go:100` ↔ internal/parameter/metadata.go:140

*Knowledge* — Which semantic quantity (temperature, battery, window, smoke, …) and which value behaviour (instantaneous / monotonic) a (device model, wire parameter, unit) triple reports. All five tables are duplicated under the SAME variable names in both packages: sensorMetadataByParam, sensorMetadataByDeviceAndParam, sensorMetadataByUnit, binarySensorQuantityByParam, binarySensorQuantityByDeviceAndParam (internal/model/generic/quantity.go:30,107,117,140,172 vs internal/parameter/metadata.go:32,198,245,257,337). I diffed both mechanically rather than by eye: sensorMetadataByParam has 66 entries in generic and 62 in parameter; OPERATING_VOLTAGE_LEVEL carries QuantityBattery in one and no quantity in the

*Damage on drift* — internal/parameter is the live copy: internal/north/mqtt/discovery_metadata.go:80,91,102,113 call parameter.MetadataFor / parameter.BinarySensorQuantityFor, and internal/north/mqtt/entity_descriptions_table.go:144,150 carries the same HmIP-DLP / WINDOW_OPEN rules. The internal/model/generic copy is the one a core caller naturally reaches for — it is the DataPoint's own dp.Quantity() / dp.ValueBehavior() (quantity.go:317,363) and internal/central/adapter/device_pipeline_naming_test.go:157 already asserts against it through an interface. The two answer differently today for OPERATING_VOLTAGE_LEVEL and for every HmIP-DLP / HmIP-SWSD / WINDOW_OPEN reading. The concrete break: the day any core or non-MQTT plane reads dp.Quantity(), an HmIP-DLP door contact publishes device_class "door" over MQT

*Guard* — No guard pins the two tables against each other. Both sides are pinned separately — internal/parameter/metadata_test.go against the live copy, internal/model/generic/quantity_test.go against the dormant one — so both are green while disagreeing.

### The CCU-internal sysvar exclusion token list exists twice — model copy is dead, adapter copy carries an extra rule

**high** · `internal/model/hub/sysvar.go:153` ↔ internal/central/adapter/hub_wiring.go:1142

*Knowledge* — Which CCU system variables are internal bookkeeping and must never enter the hub model — a classification of Homematic wire data. The model layer declares it (exported IsExcludedSysvar / CleanSysvarNames, with a doc comment naming each token's meaning: "OldVal" = internal change-detection helper, "pcCCUID" = internal CCU device-ID variable). internal/central/adapter is wiring, and it re-decides the same classification at fetch time (hub_wiring.go:1202, `if v.Name == "" || sysvarIsExcluded(v.Name, v.ID)`).

*Damage on drift* — The two copies have ALREADY diverged: the adapter additionally excludes the fixed IDs 40/41 (alarm/service messages), which the model copy knows nothing about. Verified by grep across the whole repo (`grep -rn "IsExcludedSysvar\|CleanSysvarNames\|excludedSysvarMarkers" --include='*.go' .`): the only non-test references are the definitions themselves — the model copy has zero production callers, so it is the spelling that is silently wrong. Adding a fourth exclusion token to the model's list changes nothing the daemon publishes; adding it only to the adapter leaves an exported, unit-tested model API reporting a catalogue the daemon does not actually hold. Any north-bound or MCP surface that later reaches for the obvious model-layer helper (the one whose doc comment reads like the rule) filt

*Guard* — No. internal/model/hub/sysvar_sensor_metric_hub_test.go asserts the model list's tokens ({"OldVal", "MyVarOldVal", ...}, {"pcCCUID", "device_pcCCUID_x"}) in isolation; nothing compares the two token sets, and the adapter's copy has no test that would

### Climate base-temperature derivation exists three times in the core, two of them live, with different rounding and tie-break

**high** · `internal/model/schedule/climate.go:254` ↔ internal/central/adapter/schedules.go:1264 (second live copy); internal/model/weekprofile/slot.go:133 (third copy, also live)

*Knowledge* — "Which temperature of a CCU climate weekday is the base temperature, and which segments are therefore explicit periods" — the fold from the 13-slot wire form to the (base + periods) form. Measured facts: (a) internal/model/schedule/climate.go:254 IdentifyBaseTemperature is exported and documented as the home, but grep over internal/**/*.go excluding _test.go finds NO production caller — only its own package doc references it; (b) internal/model/weekprofile/slot.go:133 identifyBaseTemperature is called from rawconvert.go:384 (RawToClimate → schedule.ClimateWeekday), which feeds the week-profile data point and internal/model/custom/climate/payload.go:445 "base_temperature"; (c) internal/centra

*Damage on drift* — The two north-bound surfaces disagree about the same device's schedule. Concrete case, tie-break: a weekday holding 21.0 °C from 00:00–12:00 and 17.0 °C from 12:00–24:00 (720 minutes each) is reported by the week-profile plane as base 21.0 with one 17.0 period, and by REST GET climate schedule as base 17.0 with one 21.0 period. Concrete case, rounding: any slot temperature not on the 0.5 °C grid is published by the REST surface shifted to the grid (I did not verify whether the fleet produces such values), and serializeClimateSchedule writes the base back into every unlisted slot — so a read-modify-write through that surface writes the shifted value to the device. Nothing errors; both answers look plausible, and the divergence only shows up as "the schedule looks different in the app than i

*Guard* — No. grep over tests/ for BaseTemperature/base_temperature yields only tests/contract/testdata/api_surface.json and tests/integration/schedule_bare_e2e_test.go:74, which writes a FLAT day (single temperature, 19.5, on-grid, no ties) — a case in which

### The climate "HH:MM ↔ minutes since midnight" grammar is implemented three times inside the core, with three different acceptance sets

**high** · `internal/model/weekprofile/slot.go:220` ↔ internal/central/adapter/schedules.go:1607-1627 (third copy: internal/model/schedule/climate.go:320 `func toMinutes`)

*Knowledge* — The CCU climate-slot time grammar: an "HH:MM" wall-clock string, with "24:00" as the end-of-day sentinel, mapped onto 0..1440 minutes since midnight. Three independent implementations, all on the same climate-schedule data as it travels REST DTO -> central/adapter -> model/weekprofile -> CCU paramset: (1) internal/model/weekprofile/slot.go:235 `ToMinutes` — accepts "24:00", no hour/minute range check at all; its sibling `climateTimeOK` (:250) additionally demands len>=5 so "1:30" is rejected; (2) internal/model/schedule/climate.go:320 `toMinutes` — accepts "24:00", no range check, -1 on a missing colon; its validator `validateClimateTime` (:307) special-cases "24:00" then defers to `timePatt

*Damage on drift* — They already disagree, in both directions, on the same input. "24:30": the adapter's parser returns 1470, so `expandWeekday` sorts and pads on it and `serializeClimateSchedule` (schedules.go:1374) writes `out[endKey] = slot.endMin` = 1470 to the CCU MASTER paramset. Reading the same channel back goes through weekprofile.ParseSlotTime -> MinutesToTimeStr, which errors for 1470 ("out of range (0..1440)"), and internal/model/weekprofile/rawconvert.go:137 does `if err != nil { continue }` — the slot is dropped silently, so the period the operator saved disappears from the next GET with no error anywhere. "1:30": accepted by the model validator (timePattern allows a one-digit hour), rejected by weekprofile.climateTimeOK (len<5). "25:00": weekprofile.ToMinutes returns 1500, the adapter returns -

*Guard* — No shared helper and no cross-copy guard. The opposite: three separate unit tests each pin their own copy's behaviour, and one records the divergence as expected — internal/model/weekprofile/slot_public_test.go:48 `// Note: ToMinutes does not bounds-

### The profiles/ archive key grammar is read two incompatible ways: linkprofile keys it (receiverChannelType file × senderChannelType bucket) with alias resolution, masterprofile keys the same files (deviceType file × channelType bucket) with a "KEY" catch-all and no aliases

**high** · `internal/store/masterprofile/store.go:234` ↔ internal/store/linkprofile/store.go:344 (load) and :11-14 (package doc)

*Knowledge* — What identifies a profile document in the shared profiles/ archive. Both stores are constructed in the composition root (cmd/openccu-loom/daemon.go:771 masterprofile.New(), :776 linkprofile.New()) and both read the SAME flat directory — ccudata.ProfilesFS() is rooted at profiles/ (internal/ccudata/profilefs.go:34 `const profilesDir = "profiles"`). Measured against go-openccu-data@v0.1.3/data/profiles: 65 .json.gz files, every basename a receiver CHANNEL type (ACCESS_RECEIVER, BLIND, SWITCH_VIRTUAL_RECEIVER, …); 33 distinct top-level bucket keys, all sender channel types; "KEY" is one of those 33 sender types, present in 24 of 65 files, not a catch-all; _receiver_type_aliases.json holds 3 ali

*Damage on drift* — The REST master-profile endpoints resolve their key from the live model as (device Model, channel Type): internal/north/rest/handlers/master_profiles.go:59 `return d.Model, ch.Type, nil`. A Model ("HmIP-eTRV", "HmIP-BSM") is never one of the 65 basenames, so load() fails, Profiles() returns ErrNotFound, and ListMasterProfiles converts that to an empty array (master_profiles.go:77-80 `JSON(w, http.StatusOK, []masterProfileSummary{})`) — silently, because absence is defined as not-an-error. Every GET /devices/{addr}/channels/{no}/master-profiles therefore returns [] and the channel editor shows no templates. On the WS path (`master_profiles.list` with an empty device_type → DeviceTypes() → basenames) a caller CAN hit a real file, and then the second half bites: the channel_type it passes is

*Guard* — No. The only archive-backed masterprofile tests call the store with file basenames directly (internal/store/masterprofile/store_test.go:31 `s.Profiles("BLIND", "")`, :134 `s.Profiles("BLIND", "KEY")`), which is the linkprofile key space, not the prod

### The same archive's constraint grammar is decoded twice: linkprofile models fixed/list/range (value, values, min_value/max_value); masterprofile models only `value`, so every list and range constraint decodes to nil and disqualifies the profile

**high** · `internal/store/masterprofile/store.go:63` ↔ internal/store/linkprofile/store.go:74

*Knowledge* — How a profile parameter constraint is encoded in the shared archive. Measured over all 65 archives in go-openccu-data@v0.1.3/data/profiles (1113 profiles, 38572 constraints): exactly three key shapes occur — ('constraint_type','value') ×28021, ('constraint_type','default','max_value','min_value') ×8142, ('constraint_type','values') ×2409. constraint_type is 'fixed' 28021×, 'range' 8142×, 'list' 2409×; it is never absent or empty. No constraint anywhere carries a `value` key together with constraint_type list or range, and the two container shapes masterprofile assumes (a 2-element []any, and map{"min","max"}) do not occur in the archive at all. 545 of the 1113 profiles carry at least one lis

*Damage on drift* — For a list or range constraint the JSON has no `value` key, so masterprofile's `Value any` stays nil. scoreProfile (match.go:79-86) then calls listContains/inRange, whose type switches fall to `default` on a nil any: listContains returns valuesEqual(nil, current) = false, inRange returns false. Both make scoreProfile return -1 — 'disqualified' — so MatchActiveProfile skips the profile (match.go:49-51) and reports 0 = Expert. Any of the 545 archive profiles whose list/range parameter is present in current_values is therefore reported as not-matching, and `master_profiles.match` (WS, commands_extended.go:851; REST MatchMasterProfile) tells the operator no profile is active while the device is in fact configured to one. The apply path is unaffected (fixed constraints do carry `value`), so an

*Guard* — No. Every MatchActiveProfile test injects hand-built fixtures through the cache instead of the archive (internal/store/masterprofile/match_test.go:10-15 matchTestStore, then e.g. :95 `{ConstraintType: "range", Value: []any{15.0, 22.0}}` and :126 `{Co

### "which end of this link is the queried device" is decided twice inside internal/central, by two different rules that disagree

**medium** · `internal/central/adapter/link_resolver.go:75` ↔ internal/central/adapter/links.go:102 and :123

*Knowledge* — The direction of a direct CCU link is relative to the device being asked about: outgoing when that device owns the sender channel, incoming when it owns the receiver channel. LinksDomain.enrichLink implements exactly that and stamps the answer onto hmapi.Link.Direction. linkClientAdapter.GetLinks consumes those very rows (link_resolver.go:69) and then discards the computed field, deciding the same question again from a presence test on Sender/Receiver.

*Damage on drift* — The two rules disagree on the ordinary row. enrichLink copies link.Sender and link.Receiver verbatim from the CCU's getLinks reply, so for any link where both endpoints are populated the adapter's condition is false and it labels the row "outgoing" — including every link where the queried device is the receiver, which links.go labelled "incoming". coordinators.DeviceLink.Direction is documented as `// "outgoing" or "incoming"` (coordinators/link.go:25) and is the field LinkCoordinator.GetLinksForLocale filters on (link.go:189 `if links[i].Direction == role`). Today nothing reads it: cmd/openccu-loom/daemon_wiring.go:154-158 states "WireLinkCoordinator's resolver is set but has no production reader today — kept unconditional because a resolver that exists only when REST happens to be enable

*Guard* — No. No test compares linkClientAdapter.GetLinks output against hmapi.Link.Direction, and the coordinator's own role-filter test (internal/central/coordinators/client_device_event_methods_test.go:363) passes "outgoing" against a hand-built fake, so it

### SMOKE_DETECTOR_ALARM_STATUS "means smoke" label set defined in two model packages

**medium** · `internal/model/calculated/derived_binary.go:198` ↔ internal/model/safety/classify.go:57

*Knowledge* — Which SMOKE_DETECTOR_ALARM_STATUS ENUM labels of an HmIP-SWSD mean "this detector sensed smoke", and specifically that INTRUSION_ALARM does not. Both sites encode the identical rule and the identical INTRUSION_ALARM carve-out — calculated by putting it in OffValues, safety by omitting it from the active set and explaining why in a comment. Both packages sit inside internal/model; neither imports the other, and no shared constant exists.

*Damage on drift* — The two copies feed different consumers of the same CCU parameter: internal/model/calculated builds the SMOKE_ALARM derived binary sensor that MQTT/HA and REST render, internal/model/safety feeds the security classifier the alarm engine triggers on. Add or correct a label on one side only — a new firmware status, or SECONDARY_ALARM being reclassified — and the two disagree silently: the alarm engine fires on a status the HA smoke binary sensor still reports as off (or the reverse, a smoke entity showing alarm while the engine never triggers). Nothing fails; each copy stays internally consistent, exactly the failure shape the twelve-copy address-grammar case had.

*Guard* — No. `grep -rn 'smokeActiveValues' --include='*.go' .` returns only internal/model/safety/classify.go; no test or contract test compares the two sets. tests/contract/safety_classifier_test.go exercises the safety side alone.

### level→percent is derived three times with two different roundings: custom.Brightness.Pct() truncates, both Light percentage accessors round

**medium** · `internal/model/custom/light/light.go:388` ↔ internal/model/custom/mixins.go:396

*Knowledge* — The projection of a 0..1 HM LEVEL onto a 0..100 integer percentage. custom.Brightness is the model's own home for this — Light already delegates the sibling projection, GroupBrightness (light.go:387) returns `custom.NewBrightness(v).Byte()` — but both percentage accessors bypass Brightness.Pct() and re-derive the value with a different rule (round-half-up vs truncate).

*Damage on drift* — The two rules disagree for 499 of 1001 levels sampled at 0.001 steps, including levels a CCU actually reports at 0.01 resolution: level 0.29 -> Pct() 28 vs Light 29; 0.57 -> 56 vs 57; 0.58 -> 57 vs 58 (float64 arithmetic, verified with the same IEEE-754 rules). A consumer that reads one light's brightness through Brightness.Pct() and the same light's group brightness through GroupBrightnessPct() gets values that differ by one for the same physical level. Today this is latent rather than live: `grep -rn "\.Pct()|BrightnessPct" internal pkg` excluding tests finds no production caller for any of the three, so nothing breaks now — the cost lands on whoever first wires a percentage surface and picks whichever accessor is nearest.

*Guard* — None found. No test compares Brightness.Pct() against Light.BrightnessPct()/GroupBrightnessPct(), and neither rounding rule is pinned to a table of expected values.

### Which LOCK_TARGET_LEVEL value locks an HmIP lock is spelled twice — as enum labels in the write path, as positional indices in the discovery payload

**medium** · `internal/model/custom/lock/payload.go:191` ↔ internal/model/custom/lock/lock.go:513

*Knowledge* — "Which LOCK_TARGET_LEVEL value performs lock / unlock / open on an HmIP lock." The label form is the model's real write path (lock.go:533 `l.writer.SetValue(ctx, l.Address, hmenum.ParameterLockTargetLevel, label, priority)`), reached by every REST/service/Matter caller. The index form is what the HA discovery payload publishes as payload_lock/payload_unlock/payload_open onto the raw wire-parameter command topic, where internal/north/mqtt/command_subscriber.go:1346 turns "0" into int64(0) and hands it straight to SetValue. The two forms are tied together only by the parameter's VALUE_LIST order, which neither site reads. The ordering does exist in a source — godevccu's embedded descriptor for

*Damage on drift* — The index form holds only while every LOCK_TARGET_LEVEL VALUE_LIST starts LOCKED, UNLOCKED, OPEN; nothing in the daemon reads that list to build the payload, and nothing checks it. A family whose descriptor orders the enum differently, or prepends a value, makes HA's lock button write UNLOCKED through the wire-parameter topic while the same daemon's Lock.Lock() still writes "LOCKED" — and the divergence is silent, because the state topic keeps rendering the label form from LockState() and the operator sees a lock that reports LOCKED after being told to lock. The reverse hazard is equally live: an edit that fixes one representation (say to send labels from HA too) leaves the other untouched, since neither site references the other.

*Guard* — Only for the label half. tests/integration/spa_e2e_lock_test.go:44-58 pins the write path to `LOCK_TARGET_LEVEL: "LOCKED"/"UNLOCKED"/"OPEN"` (its header comment at :15-19 states the wire value is the enum label because the descriptor's MIN is a strin

### "Is this device a CCU virtual remote" is classified twice inside the core — exact-set in the model, prefix-match in the coordinator

**medium** · `internal/model/device/aggregate.go:522` ↔ internal/central/coordinators/device.go:701

*Knowledge* — Which CCU device TYPE denotes a virtual-remote pseudo-device. Both sites classify the same input string: the model's Device.Model is assigned from the wire description's Type (internal/central/adapter/device_pipeline.go:437 "Model:        dd.Type," and :577), which is exactly what isVirtualRemoteType receives (device.go:730 "isVirtualRemoteType(d.Type)").

*Damage on drift* — The coordinator's set is strictly broader than the model's. Any RCV type that is not exactly "-50" would be a virtual remote to GetVirtualRemotes / GetVirtualRemoteAddresses (press-event dispatch) and a normal radio device to Device.IsVirtualRemote — which gates RelevantForCentralLinkManagement (aggregate.go:545-556), alarm candidate selection (internal/alarm/candidates.go:211) and the device pipeline (internal/central/adapter/device_pipeline.go:1215). The consequence is the one aggregate.go:519-521 says the set exists to prevent: "the CCU attempt to add KEY_*-source links onto a device that has no physical button to press". Honest limit: I found no non-"-50" RCV model in the local reference data (../godevccu fixtures carry exactly HM-RCV-50, HMW-RCV-50, HmIP-RCV-50; ../openccu-data yields

*Guard* — None found: no test or contract pins the two spellings against each other, and neither site references the other.

### The CCU 13-slot climate limit is spelled independently in two packages and the two spellings count different things

**medium** · `internal/model/schedule/climate.go:37` ↔ internal/model/weekprofile/slot.go:12

*Knowledge* — One CCU firmware fact: a climate weekday holds 13 (ENDTIME, TEMPERATURE) slots. The two constants apply it to different quantities and never reference each other: MaxClimatePeriods bounds PERIODS in the (base + periods) form (climate.go:52, live — ClimateWeekday.Validate is called from internal/central/adapter/schedule_io.go:225, :266, :431), while slotCount bounds and pads WIRE SLOTS (rawconvert.go:124 drops any parsed slot number above it; slot.go:109 fillUpWeekdaySlots pads to exactly it and, per its own comment, "Any slots beyond 13 are trimmed"). The two are not interchangeable: internal/model/weekprofile/rawconvert.go:334 climateWeekdayToSlotsExpand emits one extra slot per gap between

*Damage on drift* — Raise one constant and not the other and the failure is silent in both directions: raise slotCount alone and BuildClimateRawParamset emits slots that ClimateWeekday.Validate then rejects on the next save (a schedule readable but not writable); raise MaxClimatePeriods alone and the extra periods are accepted, expanded past 13 and trimmed away by fillUpWeekdaySlots with no error — the operator's last periods of the day never reach the device, which is exactly the truncation the SimpleMaxGroup comment records for the simple twin. The link is also what would make the periods-vs-slots mismatch visible: as written, nothing in the model bounds what the wire encoder must fit.

*Guard* — No. slotCount is unexported, so no cross-package test can reference both; grep over tests/ for slotCount / MaxClimatePeriods returns nothing.

### The CCU's 13-slots-per-weekday limit is a constant in model/weekprofile, a second constant in model/schedule, and a bare literal in central/adapter

**medium** · `internal/model/weekprofile/slot.go:12` ↔ internal/model/schedule/climate.go:34-37, and bare literals at internal/central/adapter/schedules.go:1528,1531-1533

*Knowledge* — One CCU firmware fact — a climate weekday paramset holds exactly 13 ENDTIME/TEMPERATURE slot pairs — spelled three times in three packages, plus a fourth derived spelling at internal/model/weekprofile/datapoint.go:59 `const maxClimateSlots = 13 * 7 * 6`. internal/model/weekprofile already imports internal/model/schedule (rawconvert.go:397 builds `schedule.ClimatePeriod`), so it could consume the exported constant; there is no import cycle forcing the second copy.

*Damage on drift* — The three sites do not even measure the same quantity today, which is what makes a shared constant load-bearing rather than cosmetic: schedule.MaxClimatePeriods bounds declared *periods* (climate.go:52), the adapter bounds *expanded stretches* (a period preceded by a gap yields two), and weekprofile.slotCount silently *trims* — fillUpWeekdaySlots's own doc says "Any slots beyond 13 are trimmed." A future correction to any one of them (a device family with a different slot count, or a fix to one bound) leaves the other two unchanged: the model validator would accept a payload the adapter rejects with "too many periods", or worse, weekprofile would drop the tail slots with no error on the path that writes them. Additionally, the model's `ClimateWeekday.Validate` is never reached on the REST

*Guard* — No test relates the three; each package tests its own constant. TestDocPurity would not see this either (though it is worth noting the adapter comment at :1531 is German prose, which that guard does ban).

### The wire-value activation rule of the safety domain exists twice — internal/security and internal/alarm — with a byte-identical normalizeActive and two divergent enum-narrowing paths that answer an out-of-range index oppositely

**medium** · `internal/security/subscribe.go:286` ↔ internal/alarm/activation.go:162 (resolveActive) and internal/alarm/resolver.go:193 (normalizeActive)

*Knowledge* — 'Does this wire value mean the sensor is active?' — the boolean/non-zero default plus the ENUM value-list narrowing. Both copies are fed from the same two model sources: the active-value labels come from safety.Classify(...).ActiveValues (internal/security/index.go:316,325,330,344 `src.activeValues = cls.ActiveValues`; internal/alarm/inputs.go:471 reads the same `cls.ActiveValues`), and the value list comes from the same accessor (internal/security/index.go:266 `src.valueList = dp.ParameterData().ValueList`; internal/alarm/activation.go:134 `return p.ParameterData().ValueList`). normalizeActive is byte-identical in both packages (security/subscribe.go:319-334 and alarm/resolver.go:193-208),

*Damage on drift* — Two divergences on the same input. (1) An enum index outside the declared value list: alarm returns inactive by design ('cannot be an intended active value'), security falls through to normalizeActive and reports any non-zero index as ACTIVE. Same device, same event, opposite verdicts — the alarm engine does not trigger while the security plane publishes a hazard class event, a retained MQTT state and a notification. (2) A configured active-value set on a parameter whose value list is unavailable: alarm falls back to the default rule AND surfaces it (activation.go:176 resolved=false → activation.go:213 `s.log.Warn("alarm sensor active_values unresolvable: …")`), while security applies the identical fallback silently, with no `resolved` signal and no log — so a source that has quietly stopp

*Guard* — No. Only internal/alarm/activation_test.go exercises an activation rule; no test file references activeFromRaw or security's normalizeActive, and no contract test compares the two packages' verdicts on the same input. tests/contract/safety_classifier

### The CCU weekday list is a second, string-typed copy in the schedules adapter alongside the model's typed one

**low** · `internal/central/adapter/schedules.go:83` ↔ internal/model/schedule/simple.go:33

*Knowledge* — The CCU's seven weekday labels and their Monday-first order. internal/model/schedule owns them as the typed Weekday enum; internal/model/weekprofile validates against that home (rawconvert.go:299-301: `func isValidWeekday(w schedule.Weekday) bool { return slices.Contains(schedule.Weekdays, w) }`).

*Damage on drift* — Two independent accept-lists gate the same write. The adapter's isValidWeekdayName (schedules.go:1553-1555, `slices.Contains(scheduleWeekdays, s)`) gates serializeClimateSchedule / serializeClimateScheduleBare, and scheduleWeekdays additionally fixes the render order of parseClimateSchedule's output (:1247); the model's schedule.Weekdays gates weekprofile.RawToClimate. The same seven names are then spelled a third time inside slotPattern's alternation (:101). A rename or an ordering change applied to one home leaves the other silently accepting or rejecting the opposite set, and the adapter's copy is string-typed so the compiler cannot relate it to schedule.Weekday at all. Damage is bounded in practice — CCU weekday labels are fixed by the firmware and are unlikely to move — which is why t

*Guard* — No cross-package guard. internal/central/adapter/adapter_multi_unit_test.go:189-212 tests isValidWeekdayName against the adapter's own literal list; nothing compares it to schedule.Weekdays.

## Q2 — knowledge in the wrong layer

### "Which interfaces push CONFIG_PENDING" is owned by pkg/hmenum with a device-level refinement, and re-decided interface-only inside internal/central/adapter — the layer that consumes it

**high** · `internal/central/adapter/auto_refresh.go:37` ↔ pkg/hmenum/interface.go:125 (and the set at :257)

*Knowledge* — This is one Homematic fact — HmIP interfaces emit a reliable CONFIG_PENDING True→False on a MASTER write, BidCos does not — and pkg/hmenum is its declared home: "Classification sets that drive protocol selection and capability computation. Keep in sync with SPECIFICATION.md §5.1 — the contract tests in tests/contract/ assert exactly these memberships" (interface.go:146-148), with `InterfacesPushingConfigPending = map[Interface]struct{}{ InterfaceHmIPRF: {} }` (:257-259). The adapter's own doc comment restates the same fact in its own words rather than calling it: "The HmIP-RF interface serves both HmIP-RF and HmIP-Wired devices on the CCU and signals a completed MASTER write through the CONF

*Damage on drift* — The REST DeviceSummary already advertises `master_pushes_config_pending: true` for an HmIP-flavoured VirtualDevices device (an HmIP-HEATING group), and devices.go:90-96 says the SPA chooses its post-save refresh strategy from that flag — i.e. it stands down and waits for the daemon. The daemon does not deliver for that device: isHmIPInterface(VirtualDevices) is false, so wireConfigPendingHook returns at auto_refresh.go:96-98 and the whole CONFIG_PENDING settle leg never runs for it — the week-profile reload (reloadDeviceWeekProfiles, the only path that refreshes a written schedule; the MasterPoller path does not call it) and the targeted getParamset(MASTER) into MasterValuesStore. After a schedule write on such a group the retained MQTT schedule_data and the SPA both keep showing the pre-w

*Guard* — No guard ties them. The contract tests pin hmenum's set only. The adapter's copy is pinned by literal re-statement of the same three cases — internal/central/adapter/auto_refresh_test.go:30-42 and adapter_multi_unit_test.go:1403-1422 assert isHmIPInt

### Lock-mode and lock-permission rules re-decided in internal/central/adapter while its sibling read path uses the model's

**high** · `internal/model/schedule/lock.go:75` ↔ internal/central/adapter/schedules.go:649 and :665

*Knowledge* — How the CCU encodes a door-lock schedule slot: which target channel means "door lock action" versus "user permission", and which LEVEL threshold means the permission is granted. The model owns all three halves of that rule (DetectLockMode, DetectLockAction, DetectLockPermission, plus the threshold constant lockPermissionThreshold = 0.5 at lock.go:42) and its own doc says the model exists "so the domain model and tests can round-trip wire-level lock entries without depending on the adapter package". Measured: the adapter delegates exactly one of the three — schedules.go:661 `return string(schedule.DetectLockAction(level, durBase, durFactor))` — and keeps private re-implementations of the othe

*Damage on drift* — The REST /schedules read (parseSimpleScheduleWithDomain → detectLockMode/detectLockPermission) and the week-profile read (decodeLockScheduleFields → schedule.DetectLockMode/DetectLockPermission) describe the same HmIP-DLD slot. Change the rule in the model — a device family that puts the lock actor on channel 2, or a permission threshold that is not 0.5 — and only the week-profile plane follows: one surface then calls a slot a door-lock action and the other calls it a user permission for the same paramset. That verdict is not cosmetic: it selects which encoder runs on save (applyLockEncoding at schedules.go:884 branches on the mode), so a schedule read on the disagreeing surface and saved back writes the wrong (LEVEL, DURATION_BASE, DURATION_FACTOR) triple — and the permanent sentinel (7,

*Guard* — No. internal/central/adapter/schedules_pure_test.go:276-353 tests the adapter's private copies; internal/model/weekprofile/rawconvert_test.go:886 and the schedule package tests cover the model's. Neither compares the two, and grep over tests/ for loc

### The WEEK_PROGRAM_CHANNEL_LOCKS wire decode is re-implemented in the event bridge while the model's own method sits unused

**medium** · `internal/central/adapter/eventbridge.go:3165` ↔ internal/model/weekprofile/datapoint.go:740

*Knowledge* — How the CCU's WEEK_PROGRAM_CHANNEL_LOCKS bitfield is turned into a per-target-channel enabled map: which wire types the parameter can arrive as, which key set to decode against, and what to do when the DP has no registered target channels. The model owns this — the bit table and `ParseChannelLocks` live in internal/model/weekprofile/channel_keys.go, and `SyncChannelLocksFromWire` is the model's own exported answer to the same question, documented as "the boot-time and event-update path".

*Damage on drift* — The model's method has no production caller — grep over internal, pkg, cmd and tests finds it only at its definition and in internal/model/weekprofile/datapoint_test.go:596-630. The live path is the adapter copy, and the two already disagree on both decisions the model made explicitly: (1) negatives — the model guards every signed case with `if v >= 0`, the adapter does not, so a negative int/int32/int64 that the model ignores wraps in the adapter (e.g. -1 → 0xFFFFFFFF) and `ParseChannelLocks` reads it as every bit set, i.e. every schedule switch published as disabled; (2) the empty-target fallback — the model falls back to `AllChannelKeys()`, while `orderedTargetKeys` returns nil for an empty map, so the adapter decodes nothing. Because the model's version is dead, any future fix to it (a

*Guard* — No guard ties the two. `TestSyncChannelLocksFromWire*` pins only the model's unused method; nothing asserts that the adapter's decode agrees with it, and nothing fails when they diverge.

## Q3 — target-system knowledge in the core

None found.

## Suggested order, if these get fixed

1. **The activation rule**, on its own and first. It is the only finding with
   a live safety consequence, and it should not wait behind thirty-seven
   reviews.
2. **The rules defined three or four times.** Each is a single fold with a
   guard, and the differing roundings need a decision recorded rather than a
   winner picked silently.
3. **The dead model copies.** Cheap, and they remove a test that passes while
   the live path does something else — the most misleading state in the list.
4. **The two archive grammars.** These touch a persisted format, so a fold is
   not a free refactor; it needs the same care as a published identity.

Nothing here is a published-identity change, so ADR 0068's obligations do not
apply — with the archive grammars as the one place to check that claim before
acting on it.
