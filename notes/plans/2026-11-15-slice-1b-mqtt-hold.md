# Slice 1b — deferred MQTT retraction across a central restart and the cache-reset re-pull

Read at HEAD 64e867cd, read-only. Every claim cites the line it was read from.
"UNVERIFIED" marks what was not readable from source. Paths are relative to
/home/markus/Dokumente/GitHub/openccu-loom unless absolute.

Corrections to the brief: the hub retract call is `cmd/openccu-loom/central_adopt.go:927`
(905 is `unwire(mode)`); `declareHubPlane` is defined at
`internal/central/adapter/hub_mqtt_publisher.go:914` and called at `:841`.

---

## 1. How it works today

### 1.1 Restart
`liveCentralAdmin.Put` (central_adopt.go:1212) -> only when a persisted previous row exists and
`centralConfigNeedsRestart` (1239, 1265-1281) -> `restartCentral` (757) ->
`teardownCentralLocked(name, teardownRestart, &cc)` (765, 844):
1. `o.bringUp.RemoveCentral(name)` (898)
2. `unwire(mode)` per domain (904-906)
3. `o.sbDeps.hubMQTT.RetractCentral(unit, connectivityInterfaces)` (927) — unconditional, no mode check
4. `evict(unit)` (929) = `evictModelForRestart` (874, 1044-1056): dropped-interface devices via
   `u.RemoveDevice` (ModelTeardown=false, 1049-1051), all others via `u.RemoveDeviceForTeardown` (1053)
5. `unit.Stop()` (930), `h.detachBridge()` (937)
then `purgeDroppedInterfaceState` (769), `adoptCentralLocked` (770); on adopt failure
`releaseCentralDomains` (771).

Device retraction: `Unit.removeDevice` publishes `DeviceRemovedEvent` (internal/central/central.go:955-962)
-> EventBridge subscription (internal/central/adapter/eventbridge.go:347-349) enqueues a durable job on the
shared single FIFO worker (430-437) -> `onDeviceRemoved` (630): `RetractDiscoveryForCentralDevice` (657)
and `RetractRawStateForDevice` (658), then `forgetAvailability` (675). No `ModelTeardown` read anywhere in
that function.

Hub retraction: `HubMQTTPublisher.RetractCentral` (hub_mqtt_publisher.go:302): stops the ReGa poller (336),
builds items from the live hub model — programs, sysvars, install-mode, connectivity, eight singletons
(367-425) — and queues `retractHubDiscoveryItems` + `retractHubRawState` (427-430; raw half 457-478:
`RetractProgramTopics`, `RetractSysvarState`, `RetractHubUpdate`, `RetractHubStatus`), then `p.rega.forget` (440).

### 1.2 Cache-reset re-pull
`cachereset.Service.Clear` (internal/central/cachereset/cachereset.go:248) -> `reinit` (386-393) ->
`BringUpManager.ReinitCentral` (central_bringup.go:580) -> `centralBringUp.reinit` (242): `teardown()` (250),
`clearModel()` (251; every device via `RemoveDeviceForTeardown`, 285-306), `start()` (257). Same event path
as above -> every device config and raw topic retracted. Also reached after a backup restore
(internal/central/adapter/stubs.go:742-765), i.e. while the CCU reboots.
The hub plane is NOT retracted on this path: `HubCoordinator.InitHub` -> `Clear` uses
`ClearPrograms`/`ClearSysvars` (internal/central/coordinators/hub.go:144-157, 982), which fire no removal
hooks (internal/model/hub/hub.go:452-456, 608-612). So cache-reset is device-side only.

### 1.3 Declared-key bookkeeping
- The declared set is NOT in loom. It is `hapublisher.Runtime.declared map[topic]cachedWrite`
  in go-hamqtt v0.40.0 (`/home/markus/go/pkg/mod/github.com/!sukram!j/go-hamqtt@v0.40.0/publisher/publisher.go`:
  Publish 508-571, Retract 915-934, Declared 940-950). Keyed by retained topic, bridge-wide, not per central.
  `Bridge.pub` (internal/north/mqtt/bridge.go:549), built in discovery_runtime.go:33.
- Dedup: `Runtime.Publish` returns `false,nil` for a byte-identical gated payload (publisher.go:519-521);
  `PublishBundle` likewise (633-638). `Bridge.publishDiscovery` (bridge.go:2206) sees only that bool (2251-2258).
- Central scoping is by node-id prefix only: `discoveryNodePrefixes` (retain_cleanup.go:813-832) used by
  `RetractDiscoveryForCentralDevice` (bridge.go:1960-2004), which walks `b.pub.Declared()` (1990-1994) and calls
  `b.pub.Retract` (1996-2002).
- The `centralName` argument of `publishDiscovery` is NOT usable for scoping: the hub plane passes `""`
  (hub_discovery.go:1324), the alarm plane too (alarm_publisher.go:747). Hub node ids carry the central
  (`hubNodeID`, hub_discovery.go:170-172).
- Bundle mode (`b.bundles != nil`, bridge.go:2207): `routeToBundle` (discovery_bundle_route.go:46-70) puts the
  component into `discoveryBundleStore.nodes[scopedNodeID].components[objectID]` (discovery_bundle_store.go:98-166);
  an empty payload is `Remove` -> platform-only tombstone (189-203); the document topic
  `homeassistant/device/<node>/config` is what lands in `declared` (publisher.go:628, 643).
  `BeginBundleBatch` is opened once at boot (cmd/openccu-loom/daemon_southbound.go:493) and closed by
  `FlushBundles` in the post-snapshot hook (723); after boot every Put publishes its document at once (route 62-69).
- Raw plane: `Bridge.rawTopics` / `configCache` (bridge.go:604, 594), bridge-wide, written by
  `publishRawRetained` (2107-2123), scoped at retraction by central prefix + address segment (2138-2167).
- Orphan sweep is once per central NAME per process (`sweptCentrals`, daemon_southbound.go:704, 727), so a
  restart or re-pull never re-arms it.

---

## 2. Design at seam level

### 2.1 Judgement on `ModelTeardown` (pkg/hmevent/catalogue.go:385-404)
It does not carry the decision, for two reasons read from source:
1. It is false for exactly the devices of a dropped interface in a restart (central_adopt.go:1049-1051, on
   purpose since Slice 1, so the stored-state evictors run). The brief wants those retracted "after the
   snapshot", so the flag cannot be the hold discriminator.
2. It reaches the bridge asynchronously (bus dispatch is not guaranteed synchronous,
   internal/central/events/bus.go:244-279, 326-334; then a FIFO job, eventbridge.go:348). A reconcile already
   in flight when a second teardown starts needs a signal that is set BEFORE the first device leaves the
   model. An event cannot give that.
=> No new event field, `ModelTeardown` untouched, decision in the subscriber from state the lifecycle code
sets synchronously. Recommended carrier: the unit itself (2.2).

### 2.2 New state

A. `central.Unit` (internal/central/central.go, next to `southboundReady` 169-181, 519-538) — a model
generation. `BeginModelTeardown()` (gen++), `MarkSouthboundReady()` additionally records `readyGen = gen`,
`ModelGeneration() (gen uint64, complete bool)` with `complete = ready && readyGen == gen`.
Why here: `southboundReady` is documented as never cleared (523), so during a re-pull the latch stays true
while the model is empty; `Readiness().Phase` flips to Ready only AFTER the ready event is published
(ccu_wiring.go:274-275 vs 283), so a phase check inside the ready-driven snapshot is a race. The generation
is set before the event (274) and before the first removal.

B. `adapter.EventBridge` — `holds map[centralName]*modelHold{devices map[address]{ifaceID, dropped, touched}, released bool}`.
Keyed by name so it survives the unit swap of a restart.

C. `mqtt.Bridge` — held discovery keys per central and group (group = device address or a hub group), a
new file `internal/north/mqtt/discovery_hold.go`. Semantics: a held key is a deferred retraction; a
non-empty publish of that key cancels it; an empty publish cancels it too (already retracted); the sweep
executes what is left. Must NOT be a `runtimeGate` (bridge.go:580-587): a broker reconnect must not clear it.

D. `adapter.HubMQTTPublisher` — per central the captured old programs / sysvars (as `pload.MQTTAddressable`
for the raw retract), install-mode and connectivity interfaces, dropped-interface set. Lives on the
publisher (under `p.mu`), not on the fan-out generation, because `Start` begins with `Stop` (211-212).

No new `Set*`/`Attach*`/`Register*`/`With*` (the guard's verbs, tests/contract/wiring_setter_callers_test.go:218):
every call below goes to a collaborator the caller already holds (`o.sbDeps.bridge`, `o.sbDeps.hubMQTT`,
`b.unit`). No registry walk is added. A wiring pin is still owed (rule 1 is about the effect) — see T9.

### 2.3 Who opens the hold
- Restart: `teardownCentralLocked`, `mode == teardownRestart`, before `evict(unit)` (929):
  `unit.BeginModelTeardown()`; and at 927 `hubMQTT.HoldCentral(unit, connectivityInterfaces, droppedInterfaces(h.cc, next))`
  instead of `RetractCentral`.
- Cache-reset: `centralBringUp.reinit`, under `reinitMu`, before `b.clearModel()` (251):
  `b.unit.BeginModelTeardown()`. Must be inside the lock: two queued re-inits serialise there (67-72).
- Subscriber: the `DeviceRemovedEvent` closure in `subscribeUnit(u)` (eventbridge.go:347) hands `u` to
  `onDeviceRemoved`; after `releaseLiveSubsForDevice` (638, unchanged) it holds instead of 657/658 when
  `_, complete := u.ModelGeneration(); !complete` and the central is not `released`. Hold =
  `bridge.HoldDiscoveryForCentralDevice(central, addr)` (the matcher of bridge.go:1964-1994, recording
  instead of retracting; in bundle mode the components of the matched node from the store) + record
  `(ifaceID, addr, dropped)` in B. `forgetAvailability` (675) stays.
  This holds dropped-interface devices and genuine CCU deletions during the window too; the sweep decides.
- Hub: `HoldCentral` = `stopRegaLivenessPoll` + `rega.forget` (as 336, 440), build the same items as
  302-425, queue: `bridge.HoldHubDiscovery(central, group, items)` per group, gate offline without dwell
  (new per-central form of `announceHubStatusOffline`, hub_status.go:390-400: forget + `publishOnline(false)`
  + `syncConnected`), no raw retract.

### 2.4 Mark
`Bridge.publishDiscovery` entry (bridge.go:2206), before the `b.bundles` branch and before `b.pub.Publish`
(2251): cancel the held key. In bundle mode in `routeToBundle` before the batch check (route 54-62).
EventBridge: `publishDeviceSnapshot` (878), after the release gate (885-888), marks the device touched.

### 2.5 Who closes it (sweep)
Device side — in `publishCentralSnapshot` (eventbridge.go:761): read `g0, complete := u.ModelGeneration()`
before the walk (789); after the hook returned (813; the hook flushed the bundles,
daemon_southbound.go:723) and only on the not-failed path (799-808): if `complete` and the generation is
still `g0`, reconcile each held device:
- touched -> `bridge.RetractHeldDiscovery(central, addr)` (leftover keys only; bundle: tombstones through the funnel), drop.
- untouched, still in `ModelRegistry` (unreleased) -> drop, no retraction.
- untouched, absent, interface dropped OR interface carries >=1 device in the new model
  (the rule of `countLoadedInterfaces`, ccu_wiring.go:384-397) -> today's two calls (657/658 code), drop.
- untouched, absent, interface configured but empty (ingest failed, ccu_wiring.go:547-560) -> stays held.
This is "chained after FlushBundles" without a second hook setter. A reconnect-driven snapshot
(daemon.go:404-408) during a re-pull finds `complete == false` and sweeps nothing.

Hub side — `declareHubPlane` (914): after the serial gate (915) and only when `u.ModelGeneration()` is
complete, queue a reconcile job behind the pass's declares (FIFO, 494-500):
- group fully cancelled -> drop.
- connectivity / install-mode of interface X: retract iff X is in the dropped set; else keep.
- programs / sysvars absent from the new generation: retract (discovery + `RetractProgramTopics` /
  `RetractSysvarState`) iff the system never offers the class (`lite_features.go:30-31`) or the new
  generation declared at least one member; else keep (load failure is only a warning, hub_wiring.go:280-296).
- singletons: never swept here — `applyHubFeatureGate` (888-901) already retracts what is not offered.
  `RetractHubUpdate` when `FeatureHubSystemUpdate` is known and absent (today no one clears that state topic
  except RetractCentral, 475).

### 2.6 Who aborts it
- Remove while held: `teardownCentralLocked`, `mode == teardownRemove`, before `evict`:
  `o.sbDeps.bridge.ReleaseModelHold(name)` — sets `released` synchronously (so the removal jobs of this
  teardown retract directly) and queues the retraction of the whole held device set; `RetractCentral`
  additionally retracts the held hub groups (its live model is empty for a unit that never became ready, 367).
  `released` is cleared by `AttachCentral` (eventbridge.go:268).
- Failed adopt: `restartCentral` error branch (770-772): the same two calls.
- Second restart: the new unit is not ready, its model is empty, nothing is evicted, the held set stays;
  a hold call for an already held key is a no-op.
- Box down: no snapshot (eventbridge.go:773), no serial (hub 915) -> no sweep. Daemon stop: the hold is
  process memory; the next boot's orphan sweep is the reconciler.

### 2.7 Subscribers of DeviceRemovedEvent — unchanged
The event and its publisher (central.go:955-962) do not change. Other subscribers:
ws/device_lifecycle.go:174; coordinators/cache.go:468; alarm/inputs.go:84; security/subscribe.go:96;
cmd/openccu-loom/daemon_matter.go:728-733 (reads ModelTeardown at 729); channel_flags_evict.go:58-59;
values_cache_evict.go:48-49; master_values_evict.go:52-53; recording_override_evict.go:48-49.
Only eventbridge.go:347/630 changes.

### 2.8 Comments that become false (Fable fact note -> Opus text)
pkg/hmevent/catalogue.go:389-393; central/central.go:900, 921-923; central_adopt.go:737-739, 1028-1030,
865-866 (hub retract comment 917-925); central_bringup.go:227-229, 272-283; eventbridge.go:622-629, 342-346.

---

## 3. Test plan

All behavioural tests are `tests/e2e` (`//go:build e2e`, built binary, mochi broker, godevccu), because
the in-process fixture has no bridge at all (`buildLiveTestOrchestrator`, cmd/openccu-loom/central_adopt_test.go:398:
`southboundWiringDeps{reg, logger}`) and tests/integration/cache_reset_repull_test.go calls
`ReinitCentral` itself. Style reference: tests/e2e/central_adopt_test.go:61-133 (second simulator via
`harness.StartCCU`, POST /api/v1/centrals) and tests/e2e/mqtt_discovery_test.go:33-56 (`h.MQTT().Subscribe`).
A restart needs a persisted previous row (`hadPrev`, central_adopt.go:1239), so the restarted central is
the adopted one, never the harness boot central. Production order: `ccu2.V().SetReady(false)` before the
POST, then flip (harness.Options doc, daemon.go:87-93).

Every "zero empties" test carries its negative control in the same run: a following step that MUST
produce empties on the same subscription.

| # | Test | Asserts | Bite line |
|---|---|---|---|
| T1 | `TestE2ECentralRestartWithUnchangedFleetRetractsNoDiscoveryConfig` | PUT with a restart-relevant change that keeps the fleet (`tls_insecure_skip_verify`); zero empty payloads under `homeassistant/#` for the central's nodes; retained config set after == before. Control: T2 step in the same harness. | device: the hold branch in `onDeviceRemoved` (restore eventbridge.go:657 unconditionally); hub: the mode branch at central_adopt.go:927 (restore `RetractCentral`) |
| T2 | `TestE2ECentralRestartRetractsExactlyTheDroppedInterface` | two-interface central, PUT drops one; empties == configs of that interface's devices (set derived from REST before the restart, not hard-coded) + its connectivity sensor + its install-mode entities if declared; first empty arrives after a non-empty raw state publish of a kept device | the reconcile call after eventbridge.go:813; the dropped rule in the hub reconcile |
| T3 | `TestE2ECentralRestartAgainstANotReadyCCURetractsNothingUntilItIsReady` | `SetReady(false)`, PUT dropping an interface: zero empties for the wait window; `SetReady(true)`: exactly the T2 set | `unit.BeginModelTeardown()` in teardownCentralLocked |
| T4 | `TestE2ECacheResetRepullRetractsNoConfigOfAnUnchangedDevice` | POST /api/v1/admin/cache/clear (router.go:306); zero empties. Control: delete one device on the simulator first -> exactly its configs, after the re-pull. Variant with the CCU not ready (the restore case). | `b.unit.BeginModelTeardown()` before central_bringup.go:251 |
| T5 | `TestE2ECentralRestartKeepsTheRawPlaneOfUnchangedDevices` (+ cache-reset twin) | subscription on `<base>/#`: zero empty payloads on the central's device and hub topics; gate topic reads false then true, never empty; dropped interface -> its device topics emptied | the raw half of the hold branch (eventbridge.go:658); `retractHubRawState` left out of `HoldCentral` |
| T6 | `TestE2ERemovingAHeldCentralRetractsEverythingItHeld` | restart against a not-ready CCU, then DELETE: every device and hub config of the central emptied | `ReleaseModelHold` in the remove branch; held groups in `RetractCentral` |
| T7 | `TestE2EASecondRestartKeepsTheFirstBaseline` | not ready, two PUTs, ready: T1/T2 outcome | idempotent hold (re-hold must not reset cancelled keys) |
| T8 | `internal/north/mqtt`: `TestAHeldConfigIsCancelledByAnIdenticalRepublish`, `TestHeldBundleComponentIsRetractedAsATombstone` | mark happens although `Runtime.Publish` reports not published; bundle leftover is a tombstone | move the mark below bridge.go:2251 |
| T9 | `tests/contract/wiring_pins/central_restart_mqtt_hold_test.go` (AST, style central_restart_security_test.go:30-73) | teardownCentralLocked branches hub hold/retract on mode and begins the teardown before evict; reinit begins it before clearModel; restart failure branch and remove branch release | each of those call lines |
| T10 | failed adopt | cannot be induced over REST (adopt fails only on internal faults, central_adopt.go:547-651). In-process test would have to build bridge + wiring itself = not a wiring proof; covered by T9 only. | — (stated, not claimed) |

Not bite-able end to end: the hub reconcile's ready gate. A not-ready unit has no serial, so
`declareHubPlane` returns at 915 first; the window between `SetSystemInformation` (hub_wiring.go:257) and
ready is not reachable from a test. Unit test with real publisher + real bridge + recording client, named
as a unit test.

Rule 4: extend the device and hub `Test*PlaneTopicsRoundTrip` so "declared" after a restart equals the
retained set.

---

## 4. Decisions for the owner

D1. Hold discriminator: unit-owned model generation (recommended; race-free, no public surface, also
    gives the sweep its "model complete" proof) vs. `ModelTeardown` in the subscriber (cannot defer dropped
    devices, cannot protect a sweep in flight).
D2. Devices of a dropped interface: retract at the sweep (recommended, as the brief states; with the box
    down they stay) vs. at teardown as today.
D3. Device availability while held: publish `false` per held device (recommended: with the box down a
    retained `online` is a false statement for an unbounded time; the hub gate is only on hub entities,
    hub_discovery.go:423) vs. leave untouched. Cost: two publishes per device per restart.
D4. "Ingest succeeded": derive from the model as `countLoadedInterfaces` does (recommended: one definition
    of "loaded" in the daemon) vs. an explicit ingested set from `bringUpCentral` (540-565). Edge of the
    recommendation: an interface that lost every device keeps them held until the next boot.
D5. Held devices of an interface whose ingest failed: left to the next boot's orphan sweep plus a log line
    (recommended for this slice) vs. a second reconcile on `RecoveryCompletedEvent` (ccu_wiring.go:414).
D6. Programs / sysvars after a failed load: sweep only on evidence (recommended, 2.5) vs. an explicit
    "loaded" flag on the hub model.
D7. Bundle mode: a device that is gone keeps today's whole-document clear; leftovers of a surviving device
    become tombstones (recommended) vs. tombstones for both.
D8. Raw topics of data points that vanished from a surviving device are not swept (design: device level
    only); they stay until the next boot's raw orphan sweep. Today they are cleared. Accept or add a raw mark.
D9. Harness: T2/T3 need a two-interface simulator in the e2e harness (`startMockCCU`,
    tests/e2e/harness/godevccu.go:112-130, passes no `InterfacePorts`).

## 5. Unverified
- godevccu `InterfacePorts` in CCU mode with JSON-RPC: only read in Homegear mode
  (tests/integration/interface_ports_test.go:50-56).
- Whether the harness boot central has a persisted row (assumed not; plan uses the adopted central).
- Whether an interface can count as ingested while single devices were skipped. If yes, the sweep retracts
  those devices (as today's teardown does).
- Home Assistant's handling of a document consisting only of tombstones, and of a changed `unique_id` on
  an unchanged topic (CCU -> lite with a different serial): not read.
- `naming.ScopedDiscoveryNodeID` idempotence (matters for retracting a bundle key through the funnel).
- Whether `Unit.Stop` touches the hub model; `HubCoordinator.Clear` is silent either way (hub.go:144-157).
- A harness switch for device-bundle mode.

## 6. Existing defects seen on the way (not this slice)
- [MEDIUM] A cancelled cache-reset leaves the central dead: `reinit` returns after `clearModel` and before
  `start` when the caller's context ended (central_bringup.go:251-256); no generation runs until the daemon
  restarts. With the hold the configs survive; today they are already gone.
- [LOW] Bundle store never forgets a removed device: only Put/Remove/Bundle/Nodes exist
  (discovery_bundle_store.go:98, 189, 212, 258) and the device retraction clears the topic alone
  (bridge.go:1996-2002), so a re-paired address republishes components that no longer exist.
- [LOW] A program or sysvar deleted on the CCU around a re-pull is never retracted: `InitHub` clears
  silently (coordinators/hub.go:144-157; hub.go:452, 608) and the re-pull registers new objects.

## Owner decisions of 2026-11-15

D1, D2, D4, D5, D6, D7 as recommended. D6 was then tightened (owner, same
day, in the session that wrote the second plan; confirmed here): a hub class
is swept when its feature is known absent or a complete fetch of it
succeeded (`hub.Hub.ProgramsObserved` / `SysvarsObserved`, set at the end of
`loadPrograms` / `loadSysvars`, reset by the clears) — not when it merely
has members, which left the entities of a CCU whose programs or sysvars were
all deleted in place. D3: every held device publishes
availability `false`. D8: accepted — vanished data-point raw topics of a
surviving device stay until the next boot's raw orphan sweep. D9: extend the
e2e harness to a two-interface simulator; if godevccu cannot, stop, file a
godevccu issue, and carry T2/T3 as named unit tests meanwhile.

## Implementation notes

- A second, independent Fable plan (from another session) agreed on C1–C3;
  it noted one limit this design keeps: the bridge-side hold table lives as
  long as the `mqtt.Bridge`. A bridge swap (MQTT reconfigured at runtime)
  loses the held keys; the EventBridge's held devices still reconcile, so a
  gone device is retracted, while a leftover config of a surviving device
  waits for the next boot's orphan sweep.
- e2e: T1–T7 in `tests/e2e/central_restart_mqtt_hold_test.go`; the harness
  gained `StartCCUWithInterfaces` (godevccu v0.12.0 serves `InterfacePorts`
  in CCU mode). T8 and the reconcile rules are unit tests
  (`internal/north/mqtt/discovery_hold_test.go`,
  `internal/central/adapter/eventbridge_hold_test.go`); T9 is
  `tests/contract/wiring_pins/central_restart_mqtt_hold_test.go`. The hub
  reconcile's ready gate has no test (not reachable, see §3).
- Known limit, accepted: the reload decision is read on the dispatch of the
  removal. When another goroutine is dispatching on the same bus, the event
  is deferred (`internal/central/events/bus.go` deferred queue) and its
  subscription runs once that dispatcher's in-flight handler returns; on a
  cache reset that run can land after the re-pull completed the generation,
  and the device is then retracted and republished as before this slice.
  The window is bounded by one handler's duration, not by the fan-out queue.
