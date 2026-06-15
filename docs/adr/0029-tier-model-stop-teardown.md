# ADR 0029 — Tier-model teardown for `Unit.Stop`

- **Status**: accepted
- **Date**: 2026-06-15
- **Related**:
  [ADR 0002 — multi-CCU first class](./0002-multi-ccu-first-class.md),
  `internal/central/central.go` (`Unit.Stop`, `AddOnStopHook`),
  `internal/central/adapter/ccu_wiring.go`,
  `cmd/openccu-loom/daemon_southbound.go`

## Context

`Unit.Stop` tears the central down in a fixed thirteen-step sequence:
persist cached state → drain the scheduler → stop the connection-recovery
coordinator → stop the InterfaceClients → hub logout/clear → unsubscribe
and clear caches → clear the event coordinator → clear the EventBus
subscriptions → recorder-persistence teardown → transition the state
machine to `STOPPED` → run the post-stop hooks.

The post-stop hooks are the only extension point external code has into
teardown. They are held in one registration-ordered slice
(`onStopHooks []func()`) and **all** run dead-last — after the EventBus
subscriptions are cleared and after the state machine has already
transitioned to `STOPPED`. `AddOnStopHook` is the only way to register
one.

That single late phase is correct for the cleanup it was built for:
removing the unit from `CentralRegistry`
(`daemon_southbound.go`) and cancelling a background hub-retry goroutine
(`ccu_wiring.go`). Both are pure external cleanup with no dependency on a
live coordinator.

It is the wrong shape for anything that needs a coordinator *still
running* during its own teardown. A north-bound adapter that wants to
emit a final `availability=offline` through the EventBus, or flush an
in-flight command through the south-bound clients, has nowhere to
register: by the time hooks run, the bus is cleared and the clients are
stopped. The architecture analysis flagged this (A1-P3): *a late hook
that assumes a still-running coordinator is not structurally prevented* —
the slice silently runs such a hook after the coordinator it depends on
is gone, and the failure is a quiet missing-final-message, not a loud
error.

## Decision

Replace the single registration-ordered slice with a small fixed set of
ordered **shutdown tiers**. A hook is registered against a tier; `Stop`
fires the tiers in order, each at the point in the sequence where its
documented invariant holds.

```go
type StopTier int

const (
    // StopTierNorthbound runs first, while every coordinator is still
    // live. North-bound adapters detach here so they can emit a final
    // availability=offline through the still-running EventBus / clients.
    StopTierNorthbound StopTier = iota
    // StopTierCoordinator runs after the south-bound clients have
    // stopped but before the EventBus subscriptions are cleared — for
    // adapters that bridge the bus and need it still addressable during
    // their own teardown.
    StopTierCoordinator
    // StopTierExternal runs last, after the state machine has
    // transitioned to STOPPED: pure external cleanup with no coordinator
    // dependency (registry unregister, health-tracker cleanup, metrics).
    StopTierExternal
)
```

Firing points inside `Stop`:

- **`StopTierNorthbound`** — at the very top, after the already-stopped
  guard and before any coordinator is touched. Everything is live.
- **`StopTierCoordinator`** — after the InterfaceClients are stopped and
  the hub is cleared, but before the cache/event/EventBus clears. The bus
  is still addressable; the south path is already down.
- **`StopTierExternal`** — the existing final step, after the `STOPPED`
  transition. Unchanged.

Within a tier, hooks run in registration order (preserving today's
behaviour for the external tier). A `nil` hook is ignored, as today.

### Public-API compatibility

`AddOnStopHook(fn)` is **retained verbatim** as a thin wrapper for
`AddStopHook(StopTierExternal, fn)`. Its documented contract — "called
after the central has transitioned to STOPPED, in registration order" —
is exactly the external tier, so every current caller
(`reg.Unregister`, the hub-retry `cancelRetry`) keeps firing at the same
point with no edit. The new `AddStopHook(tier, fn)` is purely additive.

The wiring-pin contract test in
`tests/contract/wiring_pins/daemon_wiring_test.go` continues to assert
that `AddOnStopHook` exists and that registry-unregister runs post-stop;
a new case asserts the three tiers fire in order.

## Alternatives considered

- **Keep the single slice, document the hazard.** Rejected: the analysis
  point is precisely that the hazard is *not structurally prevented*. A
  comment does not stop a future adapter from registering a
  bus-dependent hook that silently no-ops.
- **A `before`/`after` boolean instead of an enum.** Rejected: two
  phases cannot express "clients down but bus still live", which is the
  tier most north-bound adapters actually need.
- **Let hooks declare a dependency on a named coordinator and
  topologically sort.** Rejected as over-built for three call sites; the
  fixed tier ladder mirrors the fixed teardown sequence and is far easier
  to reason about than a dependency graph.

## Consequences

- North-bound adapters gain a structural home for graceful-offline
  teardown that runs while the bus/clients are live; the
  "late hook assumes a live coordinator" hazard is eliminated by
  construction — the tier you register against *is* the guarantee.
- `AddOnStopHook` callers and tests are untouched; the change is
  additive.
- Three tiers is a deliberate ceiling. A future need for a finer phase
  (e.g. "after caches cleared, before recorder teardown") adds one
  constant and one firing point — but each new tier is a documented
  invariant, not a free-for-all, so the ladder stays legible.
