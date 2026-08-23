# ADR 0065 — The composition root states its wiring, so a machine can check it

- Status: proposed
- Date: 2026-08-23

## Context

`cmd/openccu-loom` is where every subsystem meets every other one. It is also,
consistently across two full-codebase audits, the densest source of defects in
the repository:

| Area | Findings/kLOC (2026-08-15) | Findings/kLOC (round 4) |
| --- | ---: | ---: |
| `cmd/` | 1.43 | 1.34 |
| repository average | 0.67 | ~0.5 |
| `internal/client` | 0.28 | — |

`internal/client` is the control group: the most densely contract-tested package
carries a fifth of `cmd/`'s density. Where a contract exists, defects do not
survive. That is the shape of the problem — not that the composition root is
written carelessly, but that almost nothing about it is *decidable*.

Today it is 60 files and ~48 000 lines of procedural wiring: 38 `wire*`
functions, plus struct literals whose fields are seams, plus setter calls whose
order relative to south-bound bring-up is load-bearing. Correctness is
established by reading.

The guards that exist all approximate. `TestEveryWiringSetterHasAProductionCaller`
asks whether a `Set*` has a caller — and, as the 2026-08-15 audit found, *every*
finding had one. What was wrong was the moment it ran, or the value it carried,
or that it was injected through a struct field instead of a setter. Round 5's M1
found the same shape one level down: `MustFindCallerInFile` matched an
identifier's name and nothing else, so a pin asserting the daemon verifies a
Matter certificate chain was satisfied by an unrelated function that shared the
word `NewVerifier`. And a whole eviction subsystem — store method, overlay
method, unit tests, a doc comment saying it runs on unpair — shipped with no
line anywhere calling it, because the guard modelled a seam as a method and
could not see a free function.

Every one of those is the same failure: the wiring is expressed in a form that
only a reader can evaluate, so the checks approximate the form instead of the
fact.

## Decision

**Proposed, not accepted.** This ADR records the problem, the options and a
recommendation; it does not authorise the work.

The recommendation is to make the composition root *declare* its wiring as data
alongside performing it, so that "is X wired, and does it run before Y" becomes
a question a test can answer exactly rather than approximately.

Concretely: each `wire*` function registers what it wires — the seam, the
collaborator, and its ordering constraint relative to south-bound bring-up —
into a manifest the daemon builds at start-up and a test can read. The wiring
still happens in Go; what changes is that it also becomes inspectable.

That buys three things no current guard can express:

- **a seam with no entry is unwired**, exactly, with no name matching;
- **ordering** — the class the audits keep hitting, where a caller exists but
  runs before the value it needs — becomes a declared constraint rather than a
  property of line order in a 900-line function;
- the manifest is the same artefact a `/diagnostics` surface could serve, so an
  operator can see what a running daemon actually wired.

### Options considered

**A. Leave it, keep adding pins.** Cheapest, and the status quo. Round 5's M1
shows where it ends: pins that approximate, and one whole subsystem that reached
production unwired anyway. The density has not moved between two audits.

**B. A dependency-injection container.** Would make wiring declarative by
construction. Rejected: it moves the failure from "unread wiring" to "unread
container configuration", adds a dependency to a project that keeps them few,
and the ordering constraints here are domain facts (readiness-gated bring-up,
ADR 0002 multi-CCU), not lifecycle generics.

**C. The wiring manifest above.** No new dependency, no rewrite — an additive
registration next to code that already exists, adoptable one `wire*` function at
a time, and each adoption immediately makes that seam exactly checkable.

**D. Split `cmd/` into per-subsystem packages.** Attacks size, not decidability.
A seam wired at the wrong moment is wrong in a small package too.

C is recommended, adopted incrementally, highest-density subsystem first.

## Consequences

**If adopted.** A one-off cost per `wire*` function, and a standing obligation
that new wiring registers itself — enforceable by a guard, unlike the reading it
replaces. `TestEveryWiringSetterHasAProductionCaller` and its ratchet can then
shrink toward deletion rather than being frozen, which is what the round-5 M2
audit found they currently are.

**If not adopted.** The composition root stays at roughly twice the average
defect density, and each audit round keeps finding fresh instances of the same
class. That is a defensible choice — the work is real and the daemon ships — but
it should be a choice, not the default that follows from nobody deciding.

**Either way**, the two facts this ADR rests on are worth keeping: contract
coverage is what moves defect density, by a factor of five in the one package
that has it; and a guard that matches a *name* is not checking a *fact*.

## References

- [`notes/plans/round-5-audit-strategy.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/plans/round-5-audit-strategy.md)
  — measure M6, and the density figures above
- [`notes/audits/2026-08-23-m1-guard-mutation-report.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/audits/2026-08-23-m1-guard-mutation-report.md)
  — what mutation-testing all 359 contract guards found
- [ADR 0002](./0002-multi-ccu-first-class.md) — multi-CCU, the source of most
  ordering constraints
