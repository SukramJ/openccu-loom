# ADR 0035 — Extract the refresh-coordination sub-component from `HubCoordinator`

- **Status**: accepted
- **Date**: 2026-06-15
- **Related**:
  `internal/central/coordinators/hub.go`,
  the analysis item Area 1 (HubCoordinator) in
  `docs/audit/architecture-analysis-2026-06-15.md`

## Context

`HubCoordinator` (`hub.go`, 942 lines) aggregates every CCU "hub"
sub-domain — programs, sysvars, inbox, service/alarm messages, system
update, install mode, metrics, connectivity. The analysis (Area 1) called
it out and suggested splitting it into sub-coordinators.

The single largest source of its bulk is **mechanical repetition in the
periodic-refresh layer**. For each of the nine refresh types the struct
carries, byte-for-byte identical except for a name:

- a `xRefresh func(ctx) error` hook field;
- a `semaX sync.Mutex` (serialises concurrent runs of that one type);
- a `RefreshX(ctx)` method (lock sema → read hook under `h.mu` → run via
  `observability.Instrument`);
- a `getX()` accessor (read hook under `h.mu`);
- a branch in the 9-way `SetRefreshHooks`;
- a line in `InitHub` (same hook, `init_X` op name).

That is ~9 × 5 ≈ 45 near-duplicate members — well over 150 lines whose
only variation is the type name and the observability op string.

The per-domain *operation* methods (`ExecuteProgram`, `SetSystemVariable`,
`CreateSysvarFloat`, the `*DPs()` accessors) are by contrast thin
delegations to the shared `*hub.Hub` model and the south-bound writer
hooks. They are not repetitive and do not share state that would benefit
from being fragmented across separate coordinator structs — splitting the
single `hub.Hub` model behind several coordinators would add coupling for
little gain.

## Decision

Extract the refresh-coordination responsibility into a small,
table-driven component and leave the per-domain delegations in place.

```go
// refreshSlot owns one refresh type's hook + its serialisation.
type refreshSlot struct {
    sema sync.Mutex   // serialises concurrent runs of THIS type
    mu   sync.RWMutex // guards hook
    hook func(context.Context) error
}
func (s *refreshSlot) set(fn func(context.Context) error)       // no-op on nil
func (s *refreshSlot) get() func(context.Context) error
func (s *refreshSlot) run(ctx, rec, op) error                   // sema → get → Instrument

// hubRefreshSet bundles the nine slots.
type hubRefreshSet struct {
    programs, sysvars, inbox, serviceMessages, alarmMessages,
    systemUpdate, installMode, metrics, connectivity refreshSlot
}
```

`HubCoordinator` holds one `hubRefreshSet`. Each public `RefreshX`
delegates to `h.refresh.X.run(ctx, h.recorder, "refresh_X")`; `InitHub`
runs the same slots with `init_X` op names; `SetRefreshHooks` calls
`set` on each slot. The nine hook fields, nine semaphores, eight `getX`
accessors, and the bespoke `runRefresh` collapse into the slot type.

**Public API and behaviour are unchanged** — same method names, same
per-type serialisation, same `observability.Instrument` op strings, same
nil-hook = no-op semantics. The per-slot `mu` is finer-grained than the
former shared `h.mu` (which also guards the sysvars snapshot map); the
sysvars map keeps `h.mu`. Hooks and the sysvars map are independent
concerns, so separating their locks is sound (and removes incidental
contention).

## Alternatives considered

- **Full per-domain coordinator structs** (programsCoordinator, …). The
  per-domain methods delegate to one shared `*hub.Hub`; fragmenting that
  model behind several coordinators sharing a back-reference adds
  coupling and a lock-ownership question for modest gain. The
  refresh layer is the cohesive, repetitive responsibility worth
  extracting first; further per-domain extraction can be a follow-up ADR
  if warranted.
- **A `map[string]*refreshSlot` keyed by name.** Rejected — named struct
  fields keep the call sites compile-time-checked and avoid a map lookup
  + missing-key path on a fixed, known set of nine.

## Consequences

- ~150 lines of duplication collapse into a ~40-line component; the
  refresh-coordination responsibility is now a focused, separately
  reasoned-about type, and `hub.go` shrinks toward its genuine
  per-domain logic.
- Behaviour is locked by the existing `hub_test.go` suite plus a
  `-race` run (the refresh layer is concurrency-sensitive: scheduler and
  WS-triggered refreshes fire concurrently).
- A future per-domain split, if pursued, starts from a smaller, clearer
  `HubCoordinator`.
