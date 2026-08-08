# ADR 0030 — Event-bus dispatch striping: rejected (per-central isolation already satisfies the goal)

- **Status**: rejected
- **Date**: 2026-06-15
- **Related**:
  [ADR 0002 — multi-CCU first class](./0002-multi-ccu-first-class.md),
  `internal/central/events/bus.go`,
  `internal/central/central.go` (`New`),
  the analysis item Area 3 [W5]/[P2] in
  `notes/audits/architecture-analysis-2026-06-15.md`

## Context

The architecture analysis (Area 3, [W5]) observed that the event bus
serialises all dispatch through a single `dispatch` mutex
(`bus.go`) and proposed ([P2, M]): *"Stripe the bus dispatch by
`EventType` hash bucket so unrelated types proceed in parallel while
preserving within-type order."* The stated motivation was contention
"under multi-CCU callback fan-in".

Both halves of that motivation were re-examined against the code before
implementing. Both fail.

## Why the premise does not hold

**1. The bus is already per-central, so cross-CCU events already
dispatch in parallel.** `events.NewBus()` is called exactly once in the
tree — inside `central.New(cfg)` (`central.go`), the per-`Unit`
constructor. Each `CentralUnit` therefore owns an independent `*Bus`
with its own `dispatch` mutex. Two CCUs fanning callbacks in concurrently
dispatch on two different buses with no shared lock. The isolation is a
deliberate, tested property (`TestMultiCCUBusIsolation`,
`TestMultiCCUBusIsolationDifferentTypes`). The "single lane under
multi-CCU fan-in" described in [W5] does not exist: the only shared lane
is *within one central*, whose event stream is causally related (one
CCU's own devices) and radio-rate-bounded.

**2. Within a single bus, type-striping is unsound — it breaks a pinned
behavioural contract.** The bus guarantees that a re-entrant publish from
inside a handler is deferred until the **entire** outer dispatch frame
completes — *including when the re-entrant event is a different type*.
This is pinned by `TestReentrantPublishDeferredCrossType`
(`bus_behavior_test.go`): a handler for `DeviceCreatedEvent` publishes a
`ClientStateChangedEvent`, and the test asserts the second type's handler
does **not** run until the first handler's frame has returned.

Type-striping would route the two types to different stripe locks. The
re-entrant publish would find its stripe free and dispatch **nested**,
inside the outer handler — the second handler would run *before* the
first frame completes, failing the test and changing observable causal
ordering for every cross-type re-entrant publish in the daemon.

The deeper reason striping cannot preserve this contract: Go has no
goroutine-local state, so the bus cannot distinguish a *re-entrant*
publish (same goroutine, inside a handler) from a *concurrent* publish
(another goroutine) — handlers call the plain generic `Publish(b, e)`
with no dispatch token, and threading one through every handler signature
is not viable. The current single `dispatch` lane deliberately conflates
the two cases and routes both through one deferred queue; that conflation
is exactly what makes the global re-entrancy-deferral guarantee
expressible. Splitting the lane by type removes the only mechanism that
enforces the guarantee.

## Decision

Do **not** stripe the bus dispatch by event type. The parallelism the
proposal sought already exists at the granularity that is safe and
useful (per-central). Finer-grained (within-central, per-type)
parallelism is rejected because it is incompatible with the global
re-entrancy-deferral contract that `TestReentrantPublishDeferredCrossType`
locks.

No code change. The constraint is recorded here and enforced by the two
existing tests cited above; a future contributor tempted to re-attempt
striping should read them first.

## Alternatives considered

- **Stripe by event type (the original proposal).** Rejected — unsound
  against the cross-type re-entrancy contract, as above.
- **Stripe by `central_name`.** Redundant — the bus is already one
  instance per central; this is the status quo.
- **Goroutine-token re-entrancy detection** (thread a dispatch token
  through handlers so concurrent same-bus publishes can parallelise while
  re-entrant ones still defer). Rejected — requires changing every
  handler signature and the generic `Publish`/`Subscribe` surface for a
  benefit (within-one-central dispatch parallelism) that has no measured
  bottleneck; a single CCU's event rate is radio-bounded.
- **Measure first, then revisit.** If a single hot central ever shows the
  `dispatch` mutex as a profiled bottleneck, the safe lever is to make
  the *handlers* cheaper (they already run outside `b.mu`; the copy of the
  handler slice at `dispatchNow` already releases the registry lock before
  invocation), not to split the dispatch lane. This ADR can be superseded
  with profile evidence.

## Consequences

- The Area 3 [P2] striping item is closed as "evaluated, rejected with
  rationale" rather than implemented.
- The per-central bus isolation and the global re-entrancy-deferral
  contract are documented as load-bearing, with the enforcing tests named
  so they are not weakened by accident.
- The Area 3 [W5] finding is corrected: the dispatch lane is per-central,
  not a global single lane.
