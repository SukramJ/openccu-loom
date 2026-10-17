# Composition root and wiring

`cmd/openccu-loom` is the composition root: the one package where every subsystem is constructed and handed its collaborators. This page explains why wiring is pinned through it, which guards check the wiring, and what you add when you add a seam.

!!! info "Who this page is for"
    Contributors who add a subsystem, a `Set*` / `Attach*` / `Register*` method, a per-central subscriber, an MQTT plane or an event type. The decision record behind this page is [ADR 0065](../adr/0065-composition-root-wiring-is-checkable.md).

## Why the composition root gets its own rules

A collaboration between two packages can be correct in both packages and still never happen: the setter that hands one to the other has no production caller, or runs after the moment it mattered. Nothing in either package's own tests notices. The case that set the pattern: the hub coordinator's notifiers were dead because `SetHubModel` had no production caller — the coordinator tests called it themselves, so they stayed green while every hub push event was lost. The regression test for it, [`internal/central/hub_notifier_wiring_test.go`](https://github.com/SukramJ/openccu-loom/blob/main/internal/central/hub_notifier_wiring_test.go), goes through `central.New` alone and asserts the effect on the bus.

A test that constructs the collaboration itself is a **bracketing test**: it proves the collaboration *can* happen, never that a running daemon *makes* it happen. Treat one as a defect, not as coverage.

ADR 0065 adds a second line of defence: the composition root *declares* its wiring as data while performing it. The ledger is [`internal/wiring`](https://github.com/SukramJ/openccu-loom/tree/main/internal/wiring) (`wiring.Manifest`); a running daemon serves it on `GET /api/v1/diagnostics/wiring` (admin only).

## Per-central wiring: observe the registry, do not walk it

A subsystem that walks `central.Registry.List()` once at start-up subscribes to the centrals present at boot and never hears of a CCU adopted later. Do not write that walk. Register an observer instead:

- `Registry.OnRegister(observe)` runs `observe` for every central already registered, in list order, and for every later `Register`. The returned `remove` detaches everything it attached.
- `Registry.OnRegisterDeclared(seam, observe)` does the same and also declares the seam in the wiring manifest.

Both are in [`internal/central/central_registry.go`](https://github.com/SukramJ/openccu-loom/blob/main/internal/central/central_registry.go).

## The guards

| Guard | Where | What it protects |
| --- | --- | --- |
| `TestEveryWiringSetterHasAProductionCaller` | [`tests/contract/wiring_setter_callers_test.go`](https://github.com/SukramJ/openccu-loom/blob/main/tests/contract/wiring_setter_callers_test.go) | Every exported `Set*` / `Attach*` / `Register*` method has a caller in production code — a seam only tests call is unreachable. Declared exceptions live in the ratchet `wiringSettersWithoutCaller`, each with a reason; `wiringSeamsUnderInvestigation` is kept separate on purpose. A stale entry fails too. |
| `TestEveryRegistryWalkerHasAnAdoptSeam` | [`tests/contract/registry_walker_adopt_seam_test.go`](https://github.com/SukramJ/openccu-loom/blob/main/tests/contract/registry_walker_adopt_seam_test.go) | A collaborator that ranges over the registry and subscribes per central must also expose a per-central entry point the composition root calls with a `*central.Unit` — otherwise a runtime-adopted CCU stays silent until restart. Exceptions: `registryWalkersWithoutAdoptSeam`. |
| `TestEveryWiringFunctionDeclaresOrExplainsItself` | [`tests/contract/wiring_function_coverage_test.go`](https://github.com/SukramJ/openccu-loom/blob/main/tests/contract/wiring_function_coverage_test.go) | Every `wire*` function in `cmd/openccu-loom` declares a seam in the manifest or explains in `wiringFuncsWithoutSeam` why it hands nothing over. |
| Boot-order e2e | [`tests/e2e/boot_order_test.go`](https://github.com/SukramJ/openccu-loom/blob/main/tests/e2e/boot_order_test.go) (`-tags=e2e`) | Daemon-level subsystems derived from the device model hold real state after boot. It boots the simulated CCU *not ready* (`harness.Options{StartCCUNotReady: true}`) and flips it, which is the production order: a subsystem that reads the model once at its own start reads nothing. |
| `Test*PlaneTopicsRoundTrip` | `internal/north/mqtt/*_topics_roundtrip_test.go`, `internal/central/adapter/hub_mqtt_publisher_features_test.go` | One per MQTT plane (device, hub, alarm, security, add-on update): the topics a plane declares and the topics it publishes are the same set. |
| `TestEveryEventTypeHasASubscriber` | [`tests/contract/event_subscriber_coverage_test.go`](https://github.com/SukramJ/openccu-loom/blob/main/tests/contract/event_subscriber_coverage_test.go) | Every published event type has a production subscriber. Declared silence goes into the ratchet `eventsWithoutSubscriber`, with a reason. |
| `Test*SinkFansOutEveryEventType` | e.g. `internal/alarm/sink_fanout_test.go` (`TestAlarmSinkFansOutEveryEventType`) | One per fan-out: every event type the producers hand to the sink is driven through the real publish path and must reach the bus — a type switch that silently `default:`s fails it. |
| Wiring pins | [`tests/contract/wiring_pins/`](https://github.com/SukramJ/openccu-loom/tree/main/tests/contract/wiring_pins) | Pins for specific production wiring. Some are AST pins that fail when a constructor, method or field is no longer referenced from the expected file; others boot the real path and assert the effect. |
| Seam-effect tests | `cmd/openccu-loom/seam_effect_*_test.go` | Effect pins inside the composition root itself. |

The ratchet maps may shrink freely. Growing one needs a reason in the same commit, and "a caller is coming later" is not a reason.

## You add a seam — what you add with it

- **A new `Set*` / `Attach*` / `Register*` method.** Call it from the composition root and add a pin that constructs through the real constructor (or boots the daemon path) and asserts the *effect*, not the call — under `tests/contract/wiring_pins/` or as a `seam_effect_*_test.go` next to the wiring. `internal/central/hub_notifier_wiring_test.go` is the reference shape.
- **A new `wire*` function.** Declare its seam in the manifest, or add it to `wiringFuncsWithoutSeam` with one line saying why it hands nothing over.
- **Something that subscribes per central.** Use `Registry.OnRegister` / `OnRegisterDeclared`. Only a subsystem whose order relative to south-bound bring-up is load-bearing keeps a named seam on `centralOrchestrator` in `cmd/openccu-loom`.
- **A new MQTT plane.** Add its `Test*PlaneTopicsRoundTrip`.
- **A new event type.** Give it a subscriber, or declare the silence in `eventsWithoutSubscriber`. A new fan-out sink gets its `Test*SinkFansOutEveryEventType`.
- **A lifecycle test.** Use the production order: boot the CCU not ready, then flip it.

## The bite proof

A guard is only accepted once it has been seen failing. Remove the production line it protects, run the test and record the failure message, then restore the line and see it green again. A test that stays green without the line is untethered from it and verifies nothing. Restore from a copy of the file rather than `git checkout`, which would also discard the rest of your uncommitted change.

## Related

- [Architecture](architecture.md) — the layering the composition root assembles.
- [Testing](testing.md) — the test pillars these guards belong to.
- [`notes/contributor/engineering-rules.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/contributor/engineering-rules.md) — the case history behind each rule.
