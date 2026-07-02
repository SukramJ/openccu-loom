# Matter behaviour-parity findings register

!!! info "Who this page is for"
    Contributors and AI agents implementing Matter-side fixes. This is a
    **work backlog**, not an audit-trail report: each entry is a scoped,
    independently testable fix package against matter.js HEAD. It complements
    the [behavioural-parity contract](../matter-parity-contract.md) (which
    defines *what* parity means) and [`by_design.md`](./by_design.md) (the
    catalogue of *intentional* divergences). Nothing here is a deliberate
    divergence — every item is a fix, and closing one removes it from this list.

**Method.** Code-against-code review of `internal/north/matter/**` against the
matter.js HEAD checkout at `../matter.js` (Go file:line vs matter.js file:line).
Comments and prior parity docs were deliberately ignored; only running code was
trusted. Scenario scoped to this deployment: a pure Matter **bridge**
(server/responder only) — RootNode + Aggregator + bridged HomeMatic endpoints,
Ethernet/IP only, commissioned and controlled concurrently by Apple Home,
Google Home, Alexa and chip-tool.

**Confidence.** The five CRITICAL items were re-verified in code by hand
(C1, C3, C4, the C5 chain, App-C0 — all confirmed). HIGH/MEDIUM items come from
single-pass subsystem review: before implementing one, read the cited matter.js
source yourself (that is the contract rule anyway) and confirm the Go side still
matches these line references.

**How to use an entry.** Each finding gives (1) the Go defect with file:line,
(2) the matter.js reference to mirror, (3) the failure it produces in the bridge
scenario, (4) a fix package a mid-level implementation agent can execute,
including the test to add. Cite the matter.js `path:line` in the Go code you
write, per the contract.

---

## Suggested implementation order

Sorted by user impact × risk; packages barely overlap. C1+C2 share the ACL-gate
extraction — give both to one agent. C2 and H7 are the same defect.

| Wave | Packages | Rationale |
| --- | --- | --- |
| 1 | App-C0, C4 | The bridge does its core job again (actuate + answer reliably) |
| 2 | C1, C2 | Close the privilege-escalation / cross-fabric-leak holes |
| 3 | C6, C7, H9, H5 | Multi-admin onboarding + window / fail-safe lifecycle |
| 4 | C5, H1, H2, H4 | CASE / fabric correctness across multiple fabrics |
| 5 | C3, H3, H6, H8 | Steady-state efficiency + cleanup |
| 6 | Tier 2 / Tier 3 | Conformance & wire polish |

---

## Tier 0 — CRITICAL

### App-C0 — Wire decoder emits `map[uint8]any`, command servers expect `map[string]any`/typed (systemic)

> **Status: FIXED (Unreleased).** The four production extractors
> (`cover.extractGoToPercentage`, `light.extractStepSize`/`extractStepMode`,
> `light.extractColorTempMireds`, `climate.extractSetpointRaiseLower`) now accept
> the real `map[uint8]any` / typed-request wire shapes; `GoTo*Percentage` clamps
> to 10000. Wire-shape reproducer tests cover each server. Left below for
> provenance.

- **Go:** `bridge/fields_reader.go:83` returns `decodeGenericTagMap(dec)` (which returns `map[uint8]any`, `bridge/fields_reader.go:221`) for every non-ColorControl command. The mounted extractors only accept string-keyed/typed shapes:
  - `internal/model/custom/cover/matter.go:531` `extractGoToPercentage` accepts only `uint16` / `map[string]any` → default error branch.
  - `internal/model/custom/light/matter_color.go:600` `extractColorTempMireds` lacks the `wire.MoveToColorTemperatureRequest` case the reader delivers.
  - `internal/model/custom/light/matter.go:570` `extractStepSize` keys on `map[string]any` `"step_size"`.
  - `internal/north/matter/cluster/thermo/thermostat_server.go:391` asserts `map[string]any` → falls through to a silent `return nil` (SetpointRaiseLower is a no-op).
- **matter.js:** consumes schema-decoded typed requests everywhere, e.g. `packages/node/src/behaviors/window-covering/WindowCoveringServer.ts:574` `goToLiftPercentage({ liftPercent100thsValue })`.
- **Failure:** blind-to-percentage, colour-temperature, dimmer Step and thermostat setpoint changes fail or silently no-op over the real Apple/Google invoke path; only field-less commands (UpOrOpen/DownOrClose/On/Off) work. The extractors were written against string-keyed test inputs; the wire path was never exercised.
- **Fix:** make every extractor accept `map[uint8]any` (context-tag keyed) plus the typed wire structs; add per-command constraint checks (≤10000, mireds crop) while touching them. Test: end-to-end invoke tests driving `commandFieldsReader` output through each mounted server (extend the `bridge/colorcontrol_fields_e2e_test.go` pattern to cover / thermo / level-Step).

### C1 — ACL enforced at a flat Operate privilege → privilege escalation

> **Status: FIXED (Unreleased).** Added `im.AttributeWritePrivilegeProvider` /
> `im.CommandInvokePrivilegeProvider` (+ the `MatterClusterAttributeWritePrivilege`
> / `MatterClusterCommandInvokePrivilege` cluster capabilities), implemented on
> `TopologyDispatcher`, and threaded per-element privilege into
> `HandleWriteRequest` / `HandleInvokeRequest`. Per-cluster values (all verified
> against matter.js element files) added to AccessControl, OperationalCredentials,
> GeneralCommissioning, AdministratorCommissioning, GroupKeyManagement,
> BasicInformation, GeneralDiagnostics, NetworkCommissioning.

- **Go:** `internal/north/matter/im/write.go:238` and `internal/north/matter/im/invoke.go:311` hard-code `privilegeOperate uint8 = 3` for every write and every command on every cluster.
- **matter.js:** `packages/protocol/src/action/server/AccessControl.ts:480` `session.authorityAt(limits.writeLevel, location)` with the required level fed per element from the model (`RemoveFabric` / `ArmFailSafe` / OperationalCredentials / `AccessControl.Acl` → Administer; `OpenCommissioningWindow` → Administer + Timed).
- **Failure:** a subject holding an Operate-privilege CAT (as Apple/Google issue to household members) can write `AccessControl.ACL` to grant itself Administer, invoke `OpenCommissioningWindow` to commission a rogue admin, or `RemoveFabric` to evict another ecosystem.
- **Fix:** add optional interfaces `MatterClusterCommandInvokePrivilege` / `MatterClusterAttributeWritePrivilege` mirroring the existing `MinReadPrivilege` (`internal/north/matter/endpoint/dispatcher.go:587`); dispatcher lookup defaulting to 3; thread into `HandleWriteRequest` / `HandleInvokeRequest`. Set: AccessControl ACL/Extension → 5, OperationalCredentials all commands → 5, GeneralCommissioning commands + Breadcrumb write → 5, AdministratorCommissioning commands → 5, GroupKeyManagement.KeySetWrite → 5, BasicInformation/BDBI NodeLabel → 4. Test: an Operate-subject deny table per element in `endpoint/dispatcher_acl_test.go`.

### C2 — Subscribe path bypasses ACL and fabric-filtering entirely

> **Status: FIXED (Unreleased).** Added `Bridge.readAuthorizedResults` (ACL +
> read-privilege gate mirroring `HandleReadRequest`) and used it in both
> `buildInitialReport` and `reportSubscription`; persisted
> `(fabricIndex, subject, fabricFiltered)` on `subTarget`; added
> `authorizedEventReports` to gate ongoing event reports. Unauthorized
> attributes/events are dropped from the subscription.

- **Go:** the ACL gate lives only in `internal/north/matter/im/read.go:440` / `:462` (`HandleReadRequest`). Subscribe never calls it: `bridge/subscribe_dispatch.go:44` (initial) and `bridge/subscribe.go:249` (ongoing) call `dispatcher.Read` directly; `subTarget` (`bridge/subscribe.go:95`) stores no subject, so ongoing ticks carry no fabric/subject at all. Event reports are also unfiltered.
- **matter.js:** `packages/protocol/src/interaction/AttributeSubscriptionResponse` inherits `authorityAt` from the read response, so every subscription report re-authorises.
- **Failure:** a View-only (or ACE-less) subject subscribes wildcard and receives Administer-only data (ACL entries, NOCs); a fabric whose ACE was revoked keeps streaming forever. (Same defect as H7.)
- **Fix:** extract the gate from `HandleReadRequest` into a shared helper; persist `(fabricIndex, nodeID, CATs, fabricFiltered)` into `subTarget` at `captureSubTarget`; stamp ctx and filter in `buildInitialReport` / `reportSubscription` (wildcard-expanded misses dropped silently, concrete paths → `UnsupportedAccess`); privilege-filter the event fan-out. Test: a View-subject subscription must never contain `0x001F/0x0000`.

### C3 — DataVersion regenerated randomly on every dispatch
- **Go:** `internal/north/matter/endpoint/materialize.go:66` builds cluster servers fresh on every call (`measurement.FromMeasurementClass(...)`, plus fresh BDBI/Descriptor); each embeds a fresh tracker; `pkg/hmtypes/dataversion.go:85` lazily installs a random initial value per instance.
- **matter.js:** `packages/node/src/behavior/state/managed/Datasource.ts:349` sets the version once per lifetime, `:949` increments per change.
- **Failure:** the same cluster reports a different random DataVersion on every read/report → controllers' DataVersionFilters never match (Apple resends all ~50 endpoints on every re-subscribe); priming vs. update versions diverge; the `SetReachable` bump mutates a throwaway instance and is lost.
- **Fix:** materialise cluster servers once per topology assembly and cache them on `Endpoint` (or host the tracker on the persistent `ep.Measurement` / `ep.Source` keyed by cluster ID); bump on value change. Test: two consecutive `dispatcher.Read` calls return equal DataVersion; a change yields +1.

### C4 — Replies are not MRP-reliable; duplicates never re-trigger the reply

> **Status: FIXED (Unreleased).** Part 1: WriteResponse, InvokeResponse, the
> TimedRequest StatusResponse, and the PASE/CASE continuation replies (Pake2,
> Sigma2) ship via `sendReplyReliable` (retransmitted until acked);
> `reply_reliability_test.go` guards it. Part 2: session-0 (unsecured/PASE)
> duplicate detection added — `decryptIfNeeded` now runs a per-source-node-id
> `mrp.Window` so a retransmitted Pake1/Pake3 is acked without re-invoking the
> handshake handler (`receive_unsecured_dup_test.go`), cleared on each PASE
> acceptor swap. **Correction on matter.js re-read:** the finding's other Part-2
> half — "cache and re-send the full reply on a duplicate" — does NOT match
> matter.js. `MessageExchange.ts:409-415` sends only a *standalone ack* on a
> `duplicate` and does not re-send the reply (the `:416` reply-resend is a
> narrow non-duplicate edge, and Part 1's independent reliable retransmit
> already delivers the lost reply). Go already sends a standalone ack on
> duplicates, so no divergent reply-datagram cache was added.

- **Go:** `bridge/reply.go:55` `sendReply` uses `NeedsAck=false` for WriteResponse/InvokeResponse/StatusResponse (`bridge/receive_dispatch.go:279`, `:366`, `:415`) and all PASE/CASE replies (`bridge/securechannel.go:438`, `:512`). Duplicates are ack-only; a Sigma1 replay is dropped without re-sending Sigma2.
- **matter.js:** `packages/protocol/src/protocol/MessageExchange.ts:602` makes every reply reliable, and `:416` re-sends the previous reply (which carries the ack) on a received retransmission.
- **Failure:** one lost InvokeResponse = a failed Apple Home toggle ("Not Responding"); one lost Sigma2/Pake2 = a dead commissioning attempt.
- **Fix:** switch the PASE/CASE + Write/Invoke/Status paths to `sendReplyReliable` (already exists at `bridge/reply.go:78`); cache the last reply datagram per `(sessionID, exchangeID)` and re-send it on duplicate / Sigma1-replay instead of ack-only. Test: drop the first Sigma2/InvokeResponse and assert re-emission.

### C5 — CASE Sigma2Resume establishes a dead session
- **Go:** `internal/north/matter/secure/sigma/protocol.go:514-596` (`tryResume`) never sets `peerSessionID`, `peerNodeID`, `peerCATs`, `shared` or `resumptionID`; `ResumptionRecord` (`:81`) carries only SharedSecret/ResumptionID, dropping the store's FabricIndex/PeerNodeID/CATs (`store/resumption.go:26`); `bridge/handlers.go:495` passes the bridge's own session ID as the peer session ID; the fresh resumptionId is never persisted.
- **matter.js:** `packages/protocol/src/session/case/CaseServer.ts:152` `const { sharedSecret, fabric, peerNodeId, caseAuthenticatedTags } = cx.resumptionRecord;`, `:179` `peerSessionId: cx.peerSessionId`, `:212` saves the fresh record.
- **Failure:** any controller reconnect presenting a resumptionId (idle timeout, controller reboot) takes the fast path; the resumed session then fails every inbound decrypt (AES-CCM nonce uses peerNodeId=0) → "not responding" until the controller abandons resumption.
- **Fix:** extend `sigma.ResumptionRecord` with FabricIndex/PeerNodeID/CATs; in `tryResume` set peerSessionID from `sigma1.InitiatorSessionID`, peerNodeID/CATs from the record, resolve identity by the record's FabricIndex, retain the fresh resumptionId + shared secret; the adapter passes `r.PeerSessionID()`; the daemon persists the fresh id. Test: full CASE → resume → assert traffic decrypts, session fabric == record fabric, store row updated.

### C6 — Multi-admin broken: PAKE verifier ignored + ECM window never advertised

> **Status: FIXED (Unreleased); interop confirmation pending.** Both halves
> landed and are hermetically tested:
> - **(a) verifier install.** `spake2.NewVerifierFromValue(w0[32], L[65])`
>   reconstructs the device verifier (mirrors matter.js
>   `PaseServer.fromVerificationValue`, on-curve L validation). A new
>   `PaseVerifierInstaller` hook (bridge interface, daemon
>   `matterVerifierInstaller`) builds a `PaseAdapter` via
>   `buildPaseAdapterFromVerifier` and installs it as the window-lifetime PASE
>   acceptor in `CommissioningWindow.OpenWindow`, restoring the configured
>   acceptor on close via `setRestore`. Only the Matter cluster path (supplied
>   verifier) installs it; the REST opener keeps its own passcode.
> - **(b) ECM mDNS.** The window transition hook advertises a verifier-backed
>   Enhanced window with `CM=2` + its discriminator (`matterWindowTransitionHook`),
>   gated on a new `HasSuppliedVerifier()` flag so the REST path's CM=1 announce
>   is untouched. Mirrors matter.js `DeviceCommissioner.ts:166`.
>
> Tests: spake2 round-trip (verifier-from-value ↔ prover-with-passcode → same
> Ke) + wrong-passcode/off-curve negatives; bridge wiring
> (install/restore/basic-skip/error-revoke); daemon `buildPaseAdapterFromVerifier`
> smoke + wrong-length. **Interop confirmation** (real Apple + Google multi-admin,
> or chip-tool on Linux — NOT required on macOS) is decoupled and pending; the
> Matter bridge is opt-in/default-off so this lands behind that gate.

- **(a) Verifier — Go:** `bridge/commissioning_window.go:218` validates the `PAKEPasscodeVerifier` then discards it; `spake2` has no constructor from raw `(w0, L)`. **matter.js:** `packages/node/src/behaviors/administrator-commissioning/AdministratorCommissioningServer.ts:103` `PaseServer.fromVerificationValue(...)`, `packages/protocol/src/session/pase/PaseServer.ts:52` splits `w0 = slice(0,32)`, `L = slice(32,97)`.
- **(b) mDNS — Go:** `AnnounceCommissioning` is called only by the REST opener with a hard-coded `CommissioningMode: 1` (`cmd/openccu-loom/daemon_matter.go:2524`); the ECM transition (`bridge/commissioning_window.go:197`) never publishes `CM=2` / `_L<discriminator>`. **matter.js:** `packages/protocol/src/protocol/DeviceCommissioner.ts:166` advertises the enhanced mode.
- **Failure:** "add to another ecosystem" (Apple → Google) fails twice — the second controller cannot find the bridge (b), and even on discovery Pake2 `cB` mismatches (a).
- **Fix:** (a) add `spake2.NewVerifierFromValue(w0[32], L[65])` (on-curve validation) and install a window-lifetime `PaseAdapter` wrapping it, restoring the configured adapter on close. (b) On ECM open, call `AnnounceCommissioning(mode=2, discriminator)`. Tests: an externally-derived verifier from a distinct passcode completes Pake3; a fake advertiser sees `CM=2` + `_L<disc>`.

### C7 — ArmFailSafe ownership semantics inverted

> **Status: FIXED (Unreleased).** `handleArmFailSafe` now enforces, before the
> disarm/arm branches: (a) not-armed + CASE + window-open → BusyWithOtherAdmin;
> (b)+(c) armed + requesting fabric ≠ owner → BusyWithOtherAdmin for both re-arm
> and disarm, window-independent. Verified against matter.js
> `GeneralCommissioningServer.ts:82-90` + `FailsafeTimer.reArm` (`:53-57`).
> 6 ownership tests added.

- **Go:** `internal/north/matter/cluster/core/general_commissioning.go:549-557` — the matter.js guard is missing; the `failSafeFabricIndex != 0` clause is dead (window-open arms with fabric 0), so any CASE fabric can hijack; re-arm by a different fabric is allowed (`:566`); disarm (`ExpiryLengthSeconds==0`, `:481-514`) checks no fabric at all.
- **matter.js:** `packages/node/src/behaviors/general-commissioning/GeneralCommissioningServer.ts:82` (not-armed + window-open + CASE → BusyWithOtherAdmin) and `FailsafeTimer.ts:53` (re-arm by a different fabric always fails).
- **Failure:** fabric B can disarm fabric A's fail-safe mid-commissioning and trigger the rollback that deletes A's pending NOC.
- **Fix:** in `handleArmFailSafe`, reject any arm/re-arm/disarm when `failSafeArmed && sessFabric != failSafeFabricIndex` (BusyWithOtherAdmin, regardless of window); add the not-armed + window-open + CASE → BusyWithOtherAdmin branch; record the window-arm owner properly. Test: an owner / other-CASE / PASE / window-open table in `general_commissioning_test.go`.

---

## Tier 1 — HIGH

- **H1 (CASE) — UpdateNOC persists an unvalidated NOC**, never updates the fabric record or mDNS, never invalidates resumption. `cluster/core/operational_credentials.go:1484` vs `OperationalCredentialsServer.ts:353`, `Fabric.ts:524`. Fix: mirror AddNOC's verifier path (chain vs stored RootPublicKey, NOC pubkey == pending private key else `InvalidPublicKey`, NOC fabricId == fabric.FabricID), update the fabric NodeID, drop resumption records.
- **H2 (CASE) — AddNOC has no FabricConflict detection**; the NOC-pubkey-vs-CSR check is missing so `InvalidPublicKey` is unreachable. `cluster/core/operational_credentials.go:1377` vs `FailsafeContext.ts:257`, `Fabric.ts:524`. Fix: before `AddFabric`, scan for a matching `(FabricID, RootPublicKey)` → FabricConflict; compare NOC pubkey to the pending key → InvalidPublicKey.
- **H3 (MRP) — peer MRP intervals parsed then discarded**; a fixed 300 ms base is used and the spec-correct backoff is dead code. `secure/spake2/wire.go:41`, `bridge/outbound_reliable.go:88` / `:258` vs `MRP.ts:134` / `:140`. **FIXED (Unreleased, CASE):** Sigma1 tag 5 (initiatorSessionParams) is parsed (`parseSessionParameters`) and retained on the `sigma.Responder` (`PeerSessionParameters()`); both daemon CASE session-open sites stamp them onto `operational.Entry` (`SetPeerMRPIntervals`). `Entry.RetransmitBaseInterval(now)` selects active vs idle by Rx-only peer activity (`lastPeerActivity`, chip `GetLastPeerActivityTime`) against the peer's active threshold, with spec-default fallbacks 500/300/4000 ms (`SessionIntervals.ts:45-49`). The outbound tracker resolves the base per (re)transmission through the new `SessionRetransmitIntervalResolver` lookup capability and applies the shared exported `mrp.BackoffDuration` (base × 1.1 × 1.6^max(0,n-1) × jitter — `MRP.ts:125-146`); `mrp.Retransmitter.backoff` delegates to the same formula. **PASE note:** PBKDFParamRequest tag-5 params are still skipped — unsecured-session (session 0) retransmits use the spec idle default (500 ms), which is what chip-tool advertises anyway; plumbing them through the ephemeral PASE provider is deferred.
- **H4 (MRP) — ack obligations and reply targets keyed on bare ExchangeID** across all sessions. `transport/mrp/ack.go:97`, `bridge/ackpump.go:94` vs `ExchangeManager.ts:287`. **FIXED (Unreleased, core):** `mrp.ExchangeKey{SessionID, ExchangeID}` now keys the `AckTracker` pending map (Owe/Discharge/LookupAndDischarge take the session), the `exchangeSrcs` standalone-ack reply routes, and the `statusResponseWaits` chunk rendezvous (arm/disarm/signal); the `AckHandler` port carries the session. Mirrors matter.js session-scoped exchange resolution (`ExchangeManager.ts:287`). Two-session collision tests added. **Deferred remainder:** `outboundReliable` stays keyed on the outbound message counter (collision needs two sessions to hit the same 32-bit random-init counter — rare; fold into the MRP counter-init/rollover item) and `sigma1Replied` stays exchange-keyed (Sigma1 always rides session 0; the payload-hash equality already prevents wrong dedup).
- **H5 (PASE) — no brute-force / error cap**; the window stays open under attack (up to 48 h uncommissioned). `bridge/handlers.go` (no counter) vs `PaseServer.ts:34` (max 20). **FIXED (Unreleased):** `bridge.recordPaseFailure` (called from the handlePase error path for genuine failures) increments a per-bridge `paseFailures` counter and calls `RevokeWindow` at `paseMaxErrors=20`; `resetPaseFailures` (wired into `AttachPaseHandler`/`AttachPaseHandlerProvider`, a window boundary) gives each window a fresh budget. Mirrors matter.js `PaseServer.ts:95-110` + `DeviceCommissioner.ts:70-72`. Tests: cap-revokes / below-cap-open / reset-fresh-budget / nil-window-safe.
- **H6 (mDNS) — RemoveFabric leaves the operational record advertised forever** (only re-announce, never withdraw). `cluster/core/operational_credentials.go:1682`, `mdns/zeroconf.go:340` vs `DeviceAdvertiser.ts:84`. Fix: compute the removed fabric's instance name before removal, `Withdraw(instance, Operational)` before the re-announce.
- **H7 (IM) — ongoing subscription reports drop FabricFiltered/subject/ACL** (same as C2). `bridge/subscribe.go:249` vs `ServerSubscription.ts:721`.
- **H8 (IM) — chunked WriteRequest (`MoreChunkedMessages`) silently ignored** → premature WriteResponse. `im/write.go:19` (tag defined, never read) vs `InteractionServer.ts:331` / `:395`. **FIXED (Unreleased):** tag 3 is decoded into `WriteRequest.MoreChunkedMessages`; the dispatch rejects chunked+SuppressResponse (`InteractionServer.ts:397-402`) and chunked-inside-timed (`:408-413`) with InvalidAction. **Finding correction on re-read:** matter.js HEAD does NOT accumulate chunks into one final WriteResponse — it answers *every* chunk with its own WriteResponse and then reads the next chunk on the exchange (`InteractionServer.ts:521-532`, `:539-542`), which is exactly what the bridge's per-datagram dispatch already did; the "single response at the end" half of the original fix package was wrong. Known limitation (documented, not spec-visible for conformant clients): a follow-up chunk carrying SuppressResponse is treated as a standalone suppressed write instead of the `:552` InvalidAction, because the stateless dispatch keeps no per-exchange chunk marker; cross-chunk list-ADD batching (`:445-511`) is out of scope while the bridge has no list-write support.
- **H9 (Core) — RevokeCommissioning never expires the fail-safe** → re-pairing Busy-locked for up to 900 s. `bridge/commissioning_window.go:330` vs `AdministratorCommissioningServer.ts:146`. **FIXED (Unreleased):** `RevokeWindow` now disarms the fail-safe (`ArmFailSafeFor(ctx, 0, 0)`) as Matter §11.19.7.3 step 1, before the window-state check; the misleading "matter.js arms on window open" comment is corrected (matter.js does not — verified in `AdministratorCommissioningServer.ts` openCommissioningWindow). 2 tests added (disarm-on-revoke + open-after-revoke-not-busy).
- **H10 (App) — LevelControl ignores Options/ExecuteIfOff and min/max crop**; plain MoveToLevel executes while Off. `internal/model/custom/light/matter.go:436` vs `LevelControlServer.ts:244` / `:596`. **FIXED (Unreleased):** `decodeMoveToLevelRequest` now returns the full `wire.MoveToLevelRequest` (Level, TransitionTime, OptionsMask, OptionsOverride) instead of the bare Level byte; `lightLevelServer` gates the non-WithOnOff MoveToLevel/Step on the effective ExecuteIfOff (mask bit AND override bit, Options attribute is 0) while off — silent Success no-op, mirroring `#optionsAllowExecution` (`LevelControlServer.ts:596`). Level is cropped to [1, 254] (`:249`); Step floors at MinLevel=1 (Transitions.ts:139 clamp) so a plain Step can never power off. WithOnOff variants couple a MinLevel target to Off per `couple()` (`:500`) / spec §1.6.4.1.2 / §1.6.7.6 — projected to HM LEVEL=0 (single-knob). Move/MoveWithOnOff with Rate=0 → InvalidCommand (`#assertRateValue`, `:271`). Note: matter.js compares the *pre-clamp* step target against minLevel in `couple()`, so an over-shooting StepWithOnOff down stays ON at MinLevel there; chip clamps first (`LevelControlCluster.cpp:492` → `:508`, verified) and spec §1.6.7.6 agrees → OFF. The Go side follows chip/spec (also the observable-equivalent for a single-knob HM dimmer). Behaviour tests in `matter_level_options_test.go` + decoder tests.
- **H11 (App) — mounted colour servers advertise an empty AcceptedCommandList** and reject mandatory HS/CT commands. `internal/model/custom/light/matter_color.go:365` / `:493`, `endpoint/dispatcher.go:449` vs `color-control.element.ts:193`. **FIXED (Unreleased):** all three mounted servers (ctColorServer, hsColorServer, rgbwColorServer) now implement `MatterAcceptedCommands` with their feature-appropriate mandatory set and accept the continuous-rate Move/Step/StopMoveStep commands as no-ops; rgbwColorServer gained MoveToHue/MoveToSaturation handlers. 7 tests.

---

## Tier 2 — MEDIUM

> **Batch 1 FIXED (Unreleased):** OperationalCredentials.NOCs read privilege →
> Administer (security); GeneralDiagnostics.TestEventTrigger enumerated +
> returns ConstraintError (conformance M); event read EpochTimestamp µs→ms
> (`im/eventlog.go` `EpochUS`→`EpochMS`); commissionable mDNS SII 5000→500 ms.
> All with tests. **Skipped with reason:** SetRegulatoryConfig CountryCode → the
> Go decoder deliberately leaves CountryCode empty because a non-empty value was
> observed to make Apple Home send RemoveFabric ~80 s after Subscribe-Initial
> (documented in `bridge/fields_reader.go`); applying it needs the same
> real-controller interop validation as C6, so it is deferred, not
> forgotten. The bullets below are the remaining Tier-2 backlog.
>
> **Batch 2 FIXED (Unreleased):** BridgedDeviceBasicInformation.Reachable attribute
> is now marked dirty on a reachability flip (`NotifyDeviceReachable`), not just
> the event; empty subscription keepalives set `SuppressResponse=true`
> (`reportSubscription`); vendor-qualified protocol datagrams (`HasVendorID` +
> non-Common vendor) are rejected before the IM/SecureChannel dispatch switch
> (`receive.go`).
>
> **Batch 3 FIXED (Unreleased):** ColorControl CT servers (`ctColorServer`,
> `rgbwColorServer`) now serve the mandatory `CoupleColorTempToLevelMinMireds`
> (0x400D = PhysicalMinMireds) and `StartUpColorTemperatureMireds` (0x4010 =
> null) attributes, which were missing from the read surface
> (`matter_color.go`). Additional skips-with-reason: CASE unresolvable
> destinationId fallback is a documented deliberate pre-AddNOC single-fabric
> path (`sigma/protocol.go` comment); CASE per-session attestation challenge and
> WindowCovering TargetPosition mirror are commissioning-crypto / documented
> items deferred for care.
>
> **Batch 4 FIXED (Unreleased):** AccessControl.ACL write now rejects
> out-of-range `Privilege` (not 1..5) / `AuthMode` (not 1..3) enum values with
> ConstraintError (`access_control.go`); GroupKeyManagement KeySetRead /
> KeySetRemove of a missing key set return NotFound via a typed
> `groupKeyNotFoundErr` (`group_key_management.go`) — KeySetRemove previously
> succeeded silently. **Deferred:** DeviceType-only ACL targets always deny
> (needs the endpoint device-type-**list** plumbed into `aclTargetMatches`; rare
> use case).

**TLV / message codec**
- Vendor-qualified protocol IDs collide with standard dispatch (VendorID parsed but never consulted). `bridge/receive.go:195` vs `MessageCodec.ts:377`. Fix: treat `HasVendorID` as `ErrUnknownProtocol` before the switch.
- Security-flags decoded with a 5-bit session-type mask, no reject of unknown types / Control flag; Control/Privacy echoed into replies. `transport/message/message.go:40`, `bridge/reply.go:148` vs `MessageCodec.ts:150` / `:195` / `:212`. Fix: 2-bit mask, error on type ∉ {0,1} and on C-flag, drop the echo; keep the raw security-flags byte for AEAD fidelity.
- Control byte with EndOfContainer type does not consume its tag bytes → decoder desync on malformed TLV. `tlv/decode.go:56` vs `TlvCodec.ts:156`. Fix: call `readTag` before the EndContainer early-return.

**IM / events**
- Event EpochTimestamp: read path microseconds, subscribe path milliseconds; the wire wants POSIX ms. `im/eventlog.go:103`, `im/subscription/engine.go:87` vs `TlvEventData.ts:22`. Fix: store ms on both paths.
- EventNumber resets to 1 after restart (in-memory only). `im/eventlog.go:64` vs `OccurrenceManager.ts:34`. Fix: persist the counter in the SQLite store.
- Empty keepalive reports sent with SuppressResponse=false. `bridge/subscribe.go:244` vs `ServerSubscription.ts:782`. Fix: set true when `len(paths)==0`.
- Subscribe matching zero attributes/events not rejected. `bridge/subscribe_dispatch.go:29` vs `ServerSubscription.ts:610`. Fix: StatusResponse(InvalidAction).

**Core clusters**
- CommissioningComplete / expiry skip PASE cleanup. `cluster/core/general_commissioning.go:657` vs `FailsafeContext.ts:153`.
- SetRegulatoryConfig ignores CountryCode (Location stays "XX"). `cluster/core/general_commissioning.go:629` vs `GeneralCommissioningServer.ts:210`.
- GroupKeyManagement: KeySetRemove/Read of a missing id returns Success not NotFound; no `maxGroupKeysPerFabric` cap. `store/groupkeys.go:120` vs `GroupKeyManagementServer.ts:420` / `:367`.
- TestEventTrigger (conformance M) missing. `cluster/core/general_diagnostics.go:298` vs `GeneralDiagnosticsServer.ts:96`.
- NodeLabel/Location writes not persisted across restart. `cluster/core/basic_information.go:500`.

**CASE**
- Unresolvable destinationId falls back to a wrong identity instead of `NO_SHARED_TRUST_ROOTS`. `secure/sigma/protocol.go:705` vs `CaseServer.ts:238`.
- Attestation challenge is one global slot, set only on PASE. `cluster/core/operational_credentials.go:158` vs `OperationalCredentialsServer.ts:112`.

**Endpoint / ACL**
- NOCs readable at View (only AccessControl implements `MinReadPrivilege`). `cluster/core/access_control.go:199` vs `operational-credentials.element.ts:24`.
- ACL write validation laxer (no privilege/authMode enum range checks). `cluster/core/access_control.go:428` vs `AccessControlServer.ts:202`.
- DeviceType-only ACL targets always deny. `endpoint/dispatcher.go:752` vs `FabricAccessControl.ts:281`.
- BDBI Reachable flip emits the event but never dirties the attribute for subscriptions. `bridge/receive.go:413` vs `BridgedDeviceBasicInformationServer.ts:46`.

**PASE**
- Single-active-PASE invariant not enforced (in-flight verifier overwritten). `bridge/handlers.go:269` vs `PaseServer.ts:80`.
- SessionParameters (tag 5) neither honoured nor emitted. `secure/spake2/wire.go:41` vs `PaseServer.ts:151`.

**MRP**
- Duplicate ack not immediate (200 ms delay). `bridge/receive.go:160` vs `MessageExchange.ts:409`.
- No duplicate detection for unencrypted (session-0) traffic. `bridge/receive.go:222` vs `UnsecuredSession.ts:31`.
- Message-counter init/rollover diverges (full-32-bit seed + silent wrap). `transport/mrp/counter.go:30` vs `MessageCounter.ts:60`, `NodeSession.ts:111`.

**mDNS**
- Commissionable SII default 5000 ms (matter.js 500 ms). `mdns/service.go:324` vs `SessionIntervals.ts:44`.
- 30-minute re-announce broadcasts TTL-0 goodbyes → Apple cache-flush churn. `mdns/zeroconf.go:163` vs `MdnsAdvertisement.ts:96`.

**Application clusters**
- OnWithTimedOff ignores AcceptOnlyWhenOn + timed-off; LT attributes not writable. `internal/model/custom/light/matter.go:302` / `:257` vs `OnOffServer.ts:199` / `:216`. **FIXED (Unreleased):** full engine in `matter_timed_onoff.go` (owned by `Light`, survives server reconstruction): AcceptOnlyWhenOn gate (`OnOffServer.ts:199-201`), delayed-off guard period with lower-only OffWaitTime (`:203-214`), OnTime=max(req,current)+arm (`:216-224`), 100 ms tick countdowns (`:239-253`/`:312-325` — timed-on expiry clears OffWaitTime then turns off; 0xFFFF holds), on()/off() bookkeeping hooks on every OnOff command path (`:97-112`/`:119-139`), armed-countdown rollback when the CCU TurnOn fails (matter.js state is transactional). OnTime/OffWaitTime/StartUpOnOff are now writable (park-write stops a countdown, writes never start one — `#stopHeldTimer :66-84`; StartUpOnOff RW VM → `MinWritePrivilege`=Manage, nullable, constraint 0..2, in-memory like NodeLabel). Command-field constraints (control 0..1, times ≤65534) → ConstraintError. Tick goroutine lifecycle documented; hermetic test seam via `loopDisabled` + `matterTimedAdvance`.
- CT feature missing mandatory StartUpColorTemperatureMireds + CoupleColorTempToLevelMinMireds. `internal/model/custom/light/matter_color.go:307` vs `color-control.element.ts:182`.
- WindowCovering TargetPosition mirrors CurrentPosition (no in-motion target). `internal/model/custom/cover/matter.go:322` vs `WindowCoveringServer.ts:578`. **FIXED (Unreleased):** `matterTargetState` (lift+tilt, Matter pct-100ths) on `Cover`/`Garage` holds the last commanded destination — UpOrOpen→0, DownOrClose→10000 (blind: both axes, `WindowCoveringServer.ts:522-525`/`:546-549`), GoToLift/TiltPercentage→requested (`:578`/`:600`); StopMotion clears the store so the reads snap back to mirroring CurrentPosition (`:490-493` handleStopMovement; the mirror also matches the startup init `:142`). A failed CCU command leaves the target untouched. All three projections (cover, blind, garage) updated; behaviour tests added.
- DoorLock emits no LockOperation/DoorLockAlarm events. `cluster/lock/doorlock_server.go:117` vs `DoorLockServer.ts:822`. **FIXED (Unreleased):** `DoorLockServer` now implements `MatterEventReceiver`/`SetEndpoint` and emits LockOperation (id 0x02, priority critical) after every successful LockDoor/UnlockDoor/UnboltDoor — operation type Lock/Unlock/Unlatch per `DoorLockServer.ts:119-143` (UnboltDoor reports **Unlatch**, not Unlock), OperationSource=Remote(7), UserIndex/Credentials null (no USR/PIN feature), FabricIndex+SourceNode from the invoking session ctx (`:911-939`; PASE → null). `MatterEvents` advertises the three conformance-M events (DoorLockAlarm 0x00, LockOperation 0x02, LockOperationError 0x03 — `door-lock-cluster.element.ts:172/181/198`); DoorLockAlarm/LockOperationError have no emission path without PIN credentials, matching matter.js where they fire only from the wrong-code path (`:889`/`:941`). TLV payload encoder in `bridge/reply.go` (nullable tags 2-4). Behaviour + wire-shape tests added.

---

## Tier 3 — LOW

Fixed-width integer encodes exceed the sanctioned SubscriptionID/DataVersion workaround family-wide, and the provenance comments misstate matter.js (`tlv/encode.go:119`); invalid UTF-8 preserved on decode (`tlv/decode.go:201`); maxPathsPerInvoke unenforced and Invoke SuppressResponse ignored (`im/invoke.go:307`); CSRRequest lacks the post-NOC ConstraintError guard; CaseAdminSubject CAT version 0 accepted (`cluster/core/operational_credentials.go:1831`); unfiltered NOCs read returns other fabrics' cert bytes; advertised port ignores the effective bind port (`bridge/bridge.go:1168`); ephemeral PASE provider leaks a reaper goroutine per close (`cmd/openccu-loom/matter_ephemeral_provider.go:164`); encrypted receive window permits rollover (`transport/mrp/window.go:88`); negative-write parity guards pin unmounted implementations (`cluster/matter_negative_write_parity_test.go:231`).

---

## Verified sound (no action)

TLV element codes/widths, bool/null/float shapes, string/octet length forms, tag-encode thresholds; message/protocol header field order and presence rules; Sigma KDF salts/infos/nonces, TBE/TBS tag order, destinationId HMAC, RCAC/chain validation, AddNOC rollback + fail-safe-expiry revert, RemoveFabric teardown; SPAKE2 generators/point validation/Ke/transcript, PBKDF bounds; MRP max-transmissions = 5, nonce layout, standalone-ack delay, session-id collision avoidance; timed-invoke gate, keepSubscriptions purge, sendInterval formula, subscription death after 3 failures; EP0 cluster set, full-family PartsList, ServerList, empty-ACL fail-closed, CAT matching; OnOff toggle semantics, level scaling, thermostat limit-clamp + deadband, measurement null semantics, illuminance formula, cover inversion; mDNS operational instance-name + subtypes + TXT formats, multi-fabric boot re-announce, AddNOC-time publish, shutdown withdraw.
