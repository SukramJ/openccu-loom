# Implementation plan — a registry observer for per-central wiring

**Status:** executed in 0.59.1. Both halves shipped — the observer
(`central.Registry.OnRegister`) and the guard
(`TestEveryRegistryWalkerHasAnAdoptSeam`) — and the manual registrar
`centralOrchestrator.addCentralHook` is gone. What deliberately did **not**
change: the handful of hooks whose attach order relative to the south-bound
bring-up is load-bearing keep their named seams on `centralOrchestrator`, and
the migration landed as one commit rather than the fourteen this plan asked
for. Both are covered under [What did not, and why](#what-did-not-and-why).
**Audience:** a fresh agent with no access to the review conversation.
Everything needed is inline. The rule this work produced is now in
[`CLAUDE.md`](../../CLAUDE.md) §"Walking the central registry once is walking
it at boot" — read that first; this file is the record of how it got there.

## Why this exists

The second-largest defect class in the 0.59.x audit was this: a subsystem
walks the central registry once, at boot, subscribes to each unit it finds,
and never learns about a CCU adopted afterwards. Thirteen instances were
confirmed. For an adopted CCU the symptom is always the same and always
silent — measurement history stays empty, no webhook is ever sent, WebSocket
topics carry nothing, scheduled backups skip it. A restart makes it all work,
which makes the bug look like a glitch.

0.59.1 fixed every confirmed instance and introduced the seam that made it
tractable: `centralOrchestrator.addCentralHook`
(`cmd/openccu-loom/central_adopt.go`), an open registrar. Each collaborator
gained a per-central entry point (`StartCentral` / `SubscribeCentral` /
`WireCentral` / `AttachCentral`), its `Start` called that per registry entry,
and the composition root registered one hook. Teardown went through the same
ordered list.

**What was still open** at that point is that registration remained manual.
Adding a fourteenth per-central subsystem and forgetting the `addCentralHook`
line reproduced the exact defect, and nothing failed.

## Scope — verified, and smaller than first estimated

The original note said "126 `reg.List()` call sites". That count was taken
from every `.List()` in the tree and is wrong: the overwhelming majority are
`DeviceRegistry.List()`, an unrelated registry.

The real surface is `central.Registry`
(`internal/central/central_registry.go`, methods `Register` / `Get` / `List` /
`Unregister` / `Names` / `StartAll` / `StopAll`). **Fourteen production
files** referenced it:

```
internal/history/recorder.go              internal/north/rest/handlers/diagnostics.go
internal/security/service.go              internal/north/rest/handlers/system_status.go
internal/alarm/service.go                 internal/north/rest/handlers/visibility.go
internal/north/webhook/outbound.go        internal/north/rest/ws/device_lifecycle.go
internal/north/mcp/server.go              internal/north/rest/ws/device_trigger.go
internal/north/mqtt/system_status_publisher.go  internal/north/rest/ws/hub_events.go
                                          internal/north/rest/ws/optimistic_rollback.go
                                          internal/north/rest/ws/system_status.go
```

Most already had their per-central seam from 0.59.1. This change did not
create those seams — it made registration automatic instead of remembered.

## What was planned

1. **Give `central.Registry` an observer surface.** Something of the shape
   `OnRegister(func(*Unit) (unwire func())) (remove func())`, plus the
   symmetric teardown on `Unregister`. Registering an observer must also run
   it for every unit already present, otherwise the observer reintroduces the
   boot/runtime split it exists to remove. That "replay to existing members"
   behaviour is the heart of the design — write it first and test it first.
2. **Keep ordering explicit.** The `addCentralHook` list was ordered, and
   0.59.1 relied on that (the values-cache hook has to attach before bring-up
   starts so the first value marks the cache dirty; unwires run before the
   removed central's rows are purged). An observer set with unspecified order
   would silently break those guarantees. Either preserve registration order
   or give observers an explicit phase, and pin the guarantees that depend
   on it.
3. **Migrate the fourteen, one at a time**, each with its own commit and its
   own effect-level test. Do not migrate them in one sweep — the failure mode
   is silent, so a big-bang change is exactly the shape that hides a
   regression.
4. **Retire `addCentralHook`** once the last collaborator is migrated, so
   there is one way to do this rather than two.

## The guard is the point

The reason to do this at all is the guard it enables. Add a contract test
that resolves, through the type checker, every production site that walks the
central registry and subscribes to a unit's event bus, and requires each to be
reachable from the observer path — with a declared, reasoned ratchet entry for
deliberate exceptions, in the style of the existing ratchets
(`tests/contract/wiring_setter_callers_test.go` shows how this repo drives the
type checker).

Without that guard this change is a refactor with no safety gain: it makes the
fourteenth subsystem easier to wire but still not impossible to forget. **If
only one half can be done, do the guard, not the refactor** — the guard
catches the defect against the current manual registrar just as well.

## What shipped

Both halves, in that order.

**The guard first.** `TestEveryRegistryWalkerHasAnAdoptSeam`
(`tests/contract/registry_walker_adopt_seam_test.go`) resolves every range
over `(*central.Registry).List()` that carries the loop variable into a
subscription and requires a per-central entry point the composition root under
`cmd/` calls with a `*central.Unit`. It checks the *defect signature* rather
than the observer path specifically, because the fix has several legitimate
shapes — an exported `AttachCentral`, an unexported helper shared with the
boot walk, a composite hook. Its ratchet, `registryWalkersWithoutAdoptSeam`,
holds one entry: the hub MQTT plane, which re-runs its whole walk on every
central's southbound-ready event and therefore needs no per-central seam.

**The observer second.** `Registry.OnRegister` runs the observer for every
central already registered, in `List` order, and for every central registered
afterwards; the unwire it returns runs when that central leaves the registry
(`Unregister`, reverse attach order) or when the observer is removed. Boot and
runtime adopt became one registration, so there is no second half to forget.
The replay over existing members and the ordering guarantees are pinned in
`internal/central/central_registry_observer_test.go`
(`TestOnRegisterReplaysOverUnitsAlreadyRegistered`,
`TestObserversRunInRegistrationOrderAndUnwireInReverse`), and the adopt path
through the real composition root in
`cmd/openccu-loom/central_adopt_registry_observer_test.go`.

Migrated to one observer each: the five WebSocket subscribers, the REST
system-status buffer, the MQTT system-status publisher, the outbound webhook,
the measurement recorder, the values-cache flusher and evictor,
session-recorder persistence, the incident recorder, the program-execute audit
and the scheduled-backup jobs. `addCentralHook` went with them.

Step 2's ordering guarantee survives the move: observers fire inside
`Register`, which `adoptCentral` calls before `Unit.Start` and long before the
south-bound bring-up is launched, so a per-central subscription is in place
before the central can emit anything — including the opening
`SystemStatusChangedEvent` that `Start` itself publishes, which the old
post-`Start` hooks lost for every adopted CCU.
`TestAdoptCentralAttachesRegistryObserversBeforeTheUnitStarts` asserts that
effect through the real orchestrator rather than a hand-built pair.

## What did not, and why

- **Step 3's "one commit each" was not followed.** The migration landed as a
  single commit. The plan's reasoning still stands — a silent failure mode
  makes a big-bang change the shape that hides a regression — so the mitigation
  was per-subsystem effect tests plus two named negative controls: removing the
  observer fan-out from `Register` fails the per-subsystem tests, and removing
  the replay over the centrals already registered fails six packages. A reader
  reviewing that commit should re-run both before trusting it.
- **The order-sensitive hooks stayed named.** `attachCentralHooks` still holds
  the north-bound event bridge, the Matter, alarm, security and event-source
  hooks and the hub-ready trigger, because each has to attach at a defined
  point *before* `AddCentral` launches the bring-up — an observer fired at
  `Register` time cannot express that. `centralSeed` stays out for the opposite
  reason: it must run *before* the unit enters the registry, since it writes
  unsynchronised `Unit` fields the serving handlers read. These are documented
  as the exception in [`CLAUDE.md`](../../CLAUDE.md); they are not leftovers.
- **The boot-order e2e suite was not extended.** The plan's risk section
  suggested pinning the ordering guarantees in `tests/e2e/boot_order_test.go`
  rather than in package tests. They are pinned in package tests only. That is
  the one place a future ordering regression could still slip through, and it
  is the cheapest follow-up if this file is ever picked up again.

## Risks (as written before the work, kept for the record)

- The replay-to-existing-members step is where a mistake is silent: an
  observer that only fires for *future* registrations recreates the original
  bug with more machinery. Test it against a registry that already has members
  before the observer is added.
- Ordering regressions are invisible in unit tests that register a single
  collaborator. The boot-order e2e guard (`tests/e2e/boot_order_test.go`) is
  the one that would catch them; extend it rather than trusting package tests.
