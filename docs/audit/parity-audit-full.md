# Full parity audit — triaged findings

Run: 2026-05-30, branch `audit/full-parity`. Three briefs (the
since-retired parity audit-regeneration templates) executed in parallel
against the live reference codebases:

- CCU vs. aiohomematic family (`../aiohomematic`, `../homematicip_local`)
- Matter vs. matter.js HEAD (`../matter.js`)
- Matter wire-truth vs. connectedhomeip HEAD (`../connectedhomeip`)

Each finding was independently re-verified against both sides before any
fix. The CCU **model snapshot** (the cross-stack snapshot pipeline under
`script/`) was already driven to **0 drift** earlier in the cycle (see
`by_design.md` BD-Visibility-*), so this audit covers the method /
algorithm / wire layers on top of that.

Status legend: **FIXED** (landed + test), **FIX-BACKLOG** (real, deferred —
needs a larger change or live chip-tool validation), **BY-DESIGN**
(intentional divergence, documented in `by_design.md`).

## Fixed this pass

| ID | Layer | What | File |
|---|---|---|---|
| M-L6-02 | Matter IM | Read DataVersion sentinel guard was `!=0`; a controller replaying the §10.6.1.4 floor of 1 collapsed the whole cluster (Apple topology). Now `>1`, matching the Subscribe path. | `internal/north/matter/im/read.go` |
| CHIP-mDNS-F1 | Matter mDNS | `T` (TCP support) flag bits were shifted: client→bit0, server→bit1. chip Advertiser.h: client=bit1, server=bit2, bit0 reserved. Fixed + tests. | `internal/north/matter/mdns/service.go` |
| Matter-F1 | Matter DoorLock | UBOLT feature advertised at bit 4 (= WeekDayAccessSchedules); matter.js constraint is bit 12. Fixed + tests. | `internal/north/matter/cluster/lock/doorlock_server.go` |
| Matter-F2 | Matter Thermostat | Mandatory `ControlSequenceOfOperation` (0x1b) attribute was absent; now derived from the feature set (HeatingOnly/CoolingOnly/CoolingAndHeating). | `internal/north/matter/cluster/thermo/thermostat_server.go` |
| C-08 | CCU Health | Circuit-breaker HALF_OPEN weight was 0.5; aiohomematic `health.py:138` uses 0.33. Aligned. | `internal/health/client.go` |

## Wave 2 — meta-audit of the fixes + sibling sweep

A second agent independently re-verified the five wave-1 fixes against
both reference sides and swept **every** hand-coded Matter feature-bit /
attribute-ID / command-ID / cluster-revision in
`internal/north/matter/cluster/**` + `internal/model/custom/**/matter.go`
for siblings of the UBOLT off-by-N class.

**Verdict on wave 1:** four fixes fully correct (DataVersion, mDNS `T`,
DoorLock UBOLT, health). The thermostat `ControlSequenceOfOperation` fix
had the right id/enum/list but matter.js declares it `RW`; the bridge
exposes it read-only because the value follows the wrapped HM device's
immutable HEAT/COOL capability (nothing for a controller to change, hence
also not reportable). Documented in-code; not a defect.

**Three new UBOLT-class drifts found and fixed (all advertised on the
wire, none in `by_design.md`):**

| ID | What | Fix | File |
|---|---|---|---|
| B1 | WindowCovering advertised FeatureMap bit 3 ("ABS"); matter.js HEAD has no bit-3 feature (LF=0,TL=1,PA_LF=2,PA_TL=4 only). | Removed the bogus bit from the FeatureMap (absolute positioning is implied by PositionAware*). + test asserts bit 3 unset. | `internal/model/custom/cover/matter.go` |
| B2 | SmokeCOAlarm mandatory `TestInProgress` published at 0x0008 (= InterconnectSmokeAlarm); matter.js id is 0x0005. | `matterAttrTestInProgress = 0x0005`. | `internal/model/custom/siren/matter.go` |
| B3 | Thermostat advertised a fabricated bool at 0x0030 (= `SetpointChangeSource` in matter.js); `LocalTemperatureNotExposed` is FeatureMap bit 6, not an attribute. | Dropped the 0x0030 attribute (LTNE stays a feature concept; HM always exposes temperature so the bit is unset). + tests. | `internal/model/custom/climate/matter.go` |

All other feature bits, attribute/command IDs and cluster revisions
across the checked clusters (thermo, lock, colorcontrol, measurement,
power_source, network_commissioning, genericswitch) match matter.js HEAD
exactly. `go test ./...` green; `make lint` 0.

## Wave 3 — ACL enforcement (the security gap)

**M-L7-01 FIXED.** The IM read/write/invoke gates already called
`ACLChecker.CheckACL` when the dispatcher implemented it — but the
production `TopologyDispatcher` did not, so AccessControl entries were
stored yet **never enforced**: every operational fabric had implicit full
access.

`TopologyDispatcher` now implements `CheckACL` (Matter §9.10): it lists
the requesting fabric's ACL entries and grants the request only when a
CASE entry whose target covers (endpoint, cluster) carries a privilege at
least the required one, using the Administer > Manage > Operate > View
hierarchy. PASE (fabricIndex 0) is bypassed (commissioning); a store error
fails **closed**; an unwired lister fails **open** (so tests/dev are
unaffected). The bridge wires the SQLite ACL store via
`Bridge.AttachACLLister`, called from the daemon before `Start`.

`endpoint/dispatcher_acl_test.go` covers the matrix (administer-grants-all,
view-denies-operate, cluster-scoped targets, foreign-fabric denial,
non-CASE rejection, store-error fail-closed) — closing the M-L7-03
"no production ACL test" gap too.

**Per-subject matching (Wave 6 follow-up).** The signature now carries
the requesting peer's operational NodeID and CASE Authenticated Tag
set (lifted out of the verified NOC subject at Sigma3 via
`mattercert.Verifier.PeerCATsFromNOC` and threaded through
`channel.Session.PeerCATs` →
`OperationalSessionLookup.SubjectFor` → `im.WithSubject`). The
dispatcher now applies the chip access-granting algorithm verbatim:
empty `Subjects` list = fabric-wide wildcard; operational node ids
require exact match; CAT subjects match per
connectedhomeip/src/lib/core/CASEAuthTag.h:174-190
(identifier equality + monotonic version). 20-case test matrix in
`endpoint/dispatcher_acl_test.go` pins every branch.

## Wave 4 — safe hardening batch

- **Matter-F5 FIXED** — OccupancySensing dropped the deprecated
  `PirOccupiedToUnoccupiedDelay` (0x10). matter.js
  occupancy-sensing.element.ts marks it `D` (deprecated) and
  conformance-gates it on the optional HoldTime (0x3) attribute, which the
  bridge does not serve. The rev-6 surface is now Occupancy +
  OccupancySensorType + OccupancySensorTypeBitmap. Tests updated to assert
  0x10 is not advertised. (`cluster/measurement/measurement.go`)
- **CHIP-mDNS F4 FIXED** — the commissionable record now omits `VP` and the
  `_V` subtype when VendorID is 0 (reserved/invalid → `VP=0+0` is
  non-conformant; chip gates both on a present vendor id). Vendor-only `VP`
  form when ProductID is 0. Test added. (`mdns/service.go`)
- The remaining mDNS conditional-emission items (DT/_T, SAT, _CM subtype)
  are intentionally left as-is: a configured production bridge emits valid
  values, the bridge only publishes the commissionable record while a
  window is open (so `_CM` is never spuriously present), and the DT=0x0016
  choice is the documented BD-Matter-mDNSDeviceType divergence — gating
  them would be churn without practical benefit.

## Wave 5 — CCU behavioural items reclassified to by-design

Investigated C-04 and C-07 before touching the (hot, multi-consumer) CCU
event path. Both turned out to be **architectural divergences, not bugs**;
forcing aiohomematic's behaviour would conflict with OpenCCU-Loom's
accepted design and regress.

- **C-04** (equal-value events suppressed) — OpenCCU-Loom handles
  refresh-without-change via the wire-DP source-token lifecycle
  (`unobserved→cache→live→stale→live`, **ADR 0019**), which republishes MQTT
  on freshness transitions, instead of aiohomematic's publish-every-equal-value.
  Forcing equal-value publishes would *double* the source-token republish.
  Documented as `BD-CCU-RefreshViaSourceLifecycle`. (Narrow residual edge
  noted: a never-stale, unchanged DP past `expire_after` — if it matters,
  the right fix is a heartbeat republish, not equal-value events.)
- **C-07** (`<X>_STATUS` validity) — OpenCCU-Loom routes `_STATUS` to the
  optimistic tracker and exposes `state_uncertain` via `StateUncertain()`
  (aggregated by calculated sensors), rather than a separate
  `ParameterStatus` enum. Documented as `BD-CCU-StatusUncertainViaTracker`.

## Prepared — pending live chip-tool validation

**Matter-F3/F4 IMPLEMENTED (not yet certified).** The OnOff and LevelControl
LIGHTING (LT) feature + its mandatory attributes/commands are now advertised,
verbatim per matter.js HEAD:

- OnOff FeatureMap 0 → `0x01` (LT bit 0); added GlobalSceneControl (0x4000,
  default true), OnTime (0x4001, 0), OffWaitTime (0x4002, 0), StartUpOnOff
  (0x4003, null), and the LT commands OffWithEffect (0x40),
  OnWithRecallGlobalScene (0x41), OnWithTimedOff (0x42).
- LevelControl FeatureMap `0x01` → `0x03` (OO|LT); added RemainingTime
  (0x0001, 0) + StartUpCurrentLevel (0x4000, null).
- Files: `internal/model/custom/{light,switch}/matter.go` (+ tests; stale
  FeatureMap=0/0x01 pins updated). All values carry matter.js `path:line`
  provenance. `go test ./...` + matter schema-parity + lint green.

⚠️ **GATE:** this changes the advertised cluster surface (FeatureMap +
attribute/command lists), which Apple Home / Google Home re-evaluate at
pairing. A **live chip-tool re-pair against a real controller is required
before this is certified** — it cannot be validated in CI / without a local
controller. Until then it is staged on `audit/full-parity`, not merged to
a release.

## Fix-backlog (real, deferred)

| ID | Sev | What | Why deferred |
|---|---|---|---|
| C-09 | GAP | Health circuit component derives one scalar from a note string; aiohomematic weights XML-RPC and JSON-RPC circuits independently at half weight each. | Needs both circuit states tracked + weighted in `composeClientScore`. |
| C-12 | GAP | `EmitHubRefreshedEvent` emits the generic `DataRefreshCompletedEvent`; aiohomematic has a distinct `HubRefreshedEvent`. | Add a dedicated event type once a consumer needs the distinction. |
| CCU-F2/F3 | GAP | The faithful `PingPongCombinedTracker` + unknown-PONG 15 s reconcile are not wired into the production `InterfaceClient` (the `reliability` tracker is used). | Wire the combined tracker or port its low-state/reconcile branches. |
| Matter-F5 | GAP | OccupancySensing advertises deprecated `PirOccupiedToUnoccupiedDelay` (0x10, rev-6 `D`) without the gating HoldTime attribute. | Drop the attr or add HoldTime; small, low-risk next wave. |
| CHIP L9-01..04 | GAP (hardening) | Sigma/PASE/TLV live-decode is more permissive than chip's always-on `VerifyElement` (accepts unordered fields, trailing bytes, missing-mandatory post-decrypt). No Apple-reject (Apple emits valid TLV) — fuzz/robustness only. | Fold `tlv.Validate` strictness into the live `Decoder.Next()`; defensive, not interop-blocking. |
| CHIP-mDNS F3/F4/F6/F11 | GAP | `DT`/`_T`, `VP`/`_V`, `SAT`, `_CM` subtype emitted unconditionally where chip gates them on presence/mode. | Add presence/mode gating to the TXT/subtype builder; low-risk next wave. |

## By-design (documented in `by_design.md`)

| ID | What | Rationale |
|---|---|---|
| CCU-Sched (C-01/02/03) | Scheduler refresh intervals diverge from aiohomematic const.py (program/sysvar 5 min vs 30 s, firmware 60 min vs 6 h, periodic-refresh 5 min vs 15 s, …). | OpenCCU-Loom is push-event-first (no polling data path in the MVP) — periodic jobs are reconciliation safety nets, not the primary data path, so they run far less often by design. See BD-CCU-SchedulerIntervals. |
| CHIP-TLV-Permissive | The responder accepts some malformed TLV that connectedhomeip rejects (unordered optionals, trailing bytes). | OpenCCU-Loom is a pure responder; real controllers emit valid TLV, so the permissiveness is never reached on the wire. Strictness is a fuzz-hardening backlog, not an interop gap. See BD-Matter-TLVPermissive. |
| CHIP-mDNS-DT (F2) | The commissionable `DT`/`_T` advertises RootNode 0x0016, not Aggregator 0x000E. | Empirical Apple-pairing observation. See BD-Matter-mDNSDeviceType. |

## Meta-audit (review of the fixes)

- All five fixes re-verified against both reference codebases (file:line) before landing; each carries a provenance comment.
- Three tests pinned the *old, buggy* values (`mdns` T-flags, `custom/lock` UBOLT bit) — updated to the chip/matter.js-correct values, not silenced.
- `make lint` 0, matter schema-parity lockstep test green, full matter tree + health + custom DP packages green.
- The CHIP crypto/wire core (HKDF salts/info/nonces, Sigma TLV tags, status codes, control octets) was **verified byte-aligned** with connectedhomeip HEAD — no wire-crypto drift exists.

## Recommended next waves

1. **Matter-F3/F4 LIGHTING FeatureMaps** — with chip-tool re-pair
   validation against a real controller (needs the live-CCU +
   Apple-independence test, operator-authorised).
2. **Fix-backlog items** as appetite allows — `C-09` (health-circuit
   weighted XML-RPC+JSON-RPC), `C-12` (`HubRefreshedEvent`),
   `CCU-F2/F3` (production `PingPongCombinedTracker`),
   `CHIP-mDNS F3/F4/F6/F11` (conditional emission gaps),
   `CHIP L9-01..04` (TLV live-decode strictness, fuzz-hardening).

---

## Re-audit (second pass) — drift, gaps, wiring, dead code

A second sweep (five parallel slices: CCU model, Matter cluster behaviour,
Matter IM/commissioning, Matter dormant-wiring, CCU dead-code) targeted the
classes the first pass under-covered: **behaviour-enforcement** (not just
schema) and **dormant wiring** (implemented capability with no production call
site). ~35 findings; dispositions below.

### Dormant wiring — fixed (the headline class)

The first audit's archetype was the ACL gate (CheckACL existed, source never
attached). The second pass found more of the same shape, now all wired and
guarded by build-time pins in
`tests/contract/wiring_pins/dormant_capability_wiring_test.go`:

- **D1 connectivity reconcile probe** — `WireHub` seeded the Connectivity cache
  but left `Reconciler.Connect` nil, so `reconcileConnectivity` short-circuited.
  Probe now wired (`hub_wiring.go`). The `Reconciler.Health` / `.Unobserved`
  slots are reclassified as intentionally-optional nil hooks (system-health is a
  derived score, the unobserved sweep needs a load-safe design) — documented in
  `by_design.md` A3-BD03, not dormant.
- **D2 ping-pong PONG correlation** — `RecordPing`/`RecordPong` had zero
  production callers and PONG callbacks were dropped. Wired end-to-end:
  keepalive (`jobs.go`) now probes with ping-pong on, PONG events route through
  `HandleRawEventNormalized` → tracker → per-interface `RecordPong`
  (`pingpong_wiring.go`).
- **D3 BridgedDevice reachability** — `SetReachable` had zero callers; bridged
  devices showed Reachable=true forever. Wired CCU availability →
  `Bridge.NotifyDeviceReachable` via a daemon subscription; also fixed a
  constructor that ignored `cfg.Reachable`.
- **D4** UpdateNOC now tears down stale CASE sessions (`SetOnFabricUpdated`).
- **D5** aborted-CASE `sigma1Replied` map leak closed (`SetOnEvict` →
  `ForgetSigma1Replied`).
- **D6** `AdministratorCommissioning.SetFabricCounter` wired (uncommissioned
  48 h first-pairing window).
- **D7** MQTT Discovery `origin.sw_version` now stamped from the build version.

### Matter behaviour — fixed (vs matter.js HEAD)

Write-constraint enforcement and command semantics that schema-parity tests did
not cover, each mirrored to matter.js with a parity test: thermostat setpoint
limits + SystemMode gating + SetpointRaiseLower (M2-01/02/03), DoorLock
`SupportedOperatingModes` 0xFFFE→0xFFF6 (M2-04), ColorControl
MoveToColorTemperature attribute update (M2-05), WindowCovering `>10000`
constraint (M2-06); writable-attribute gaps M2-07 (CommissioningTimeout →
InvalidCommand), M2-08 (OnOff OnTime/OffWaitTime/StartUpOnOff), M2-09
(WindowCovering Mode).

### Matter IM — fixed (vs connectedhomeip HEAD)

F1 ArmFailSafe(0) disarm now runs the full revert path; F2 `checkTimedGate`
now returns TIMED_REQUEST_MISMATCH for a flag/state mismatch.

### CCU model — fixed (vs aiohomematic)

**V2-01** the `un_ignore` parser grammar was structurally incompatible with the
reference (`PARAMETER:MODEL:CHANNEL_TYPE:PARAMSET` vs the reference's
`PARAMETER:PARAMSET@MODEL:CHANNEL_NO`) — real entries parsed to garbage;
rewritten to the reference grammar (`@`-delimited, `ChannelNo` int, wildcard +
MASTER validation). **V2-02** un-ignore now short-circuits all VALUES ignore
branches. **V2-04/05** calculated-sensor relevance tightened (ApparentTemperature
exact params; OperatingVoltageLevel LOW_BAT_LIMIT presence).

### By-design (documented in `by_design.md`)

M2-10 (WindowCovering deprecated-percent non-null), F5 (failsafe disarm
ownership), F3/F4 (per-attribute timed enforcement + subscribe quota eviction —
unbuilt, unreachable today), V2-06 (single-field patch closure + two additive
built-ins), V2-08 (`IsParameterHidden` aliases the ignore decision). **V2-03**
(un-ignore-by-device prefix direction) deferred for separate investigation —
the reference's own `startswith` direction is unusual and a blind flip risks a
regression.

### Dead code

- **Deleted:** `coordinators.IdentifyIPAddr` (genuine — superseded by the
  `WaitForTCPReady` method, only a trivial test caller).
- **Retained after verification (agent over-flags):** `health.ConnectionRegistry`
  / `Connection` — *not* dead; it is a parity-tested port of the reference
  availability model (~10 `TestParity*` cases). `events.PublishSync` — carries an
  explicit `loom:reachable` annotation as a deliberate API-parity alias.
  `core.PowerSource` push-setters were confirmed unused (production uses the
  live-read `PowerSourceServer`) and deleted. Lesson reinforced: every proposed
  deletion was build+test verified before acting; two of the three CCU
  candidates were wrong to delete.

### Validation outcome

Full `go build ./...` clean; full `go test ./...` green (one pre-existing flaky
timing test in `coordinators`, passes 5× in isolation, unrelated);
`golangci-lint` 0 issues on all touched packages. Eleven new wiring pins lock
the dormant-capability class at build time.

### Is a further re-audit worthwhile, and better validation methods

Diminishing returns: pass 1 caught loud schema/wire drifts, pass 2 caught
behaviour-enforcement + dormant wiring (a different class). A third pass would
find fewer, subtler items. Higher-leverage than re-auditing:

1. **Wiring contract pins** (now started) — convert the dormant-wiring class
   from manual-audit-only into a build-time guard. Extend the curated pin list
   whenever a new capability gate/setter lands.
2. **Behaviour parity, not just schema parity** — the matter.js parity tests
   locked IDs/revisions but not write-constraint enforcement (all of M2-01..06
   slipped through). Add negative cases: a write matter.js rejects must be
   rejected by Loom too.
3. **Golden-replay for CCU visibility** — feed real `un_ignore` files through
   both stacks and diff the visibility decisions; would have caught V2-01
   instantly.
4. **Expand the chip-tool suite with negative writes** — it currently validates
   the happy path.
