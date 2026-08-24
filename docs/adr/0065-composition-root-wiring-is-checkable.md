# ADR 0065 — The composition root states its wiring, so a machine can check it

- Status: accepted
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

Option **C** — the wiring manifest — is adopted, incrementally, highest-density
subsystem first. Nothing is rewritten: the manifest is an additive registration
next to wiring that already exists, and each `wire*` function that adopts it
makes that one seam exactly checkable from the moment it does.

The decision is to make the composition root *declare* its wiring as data
alongside performing it, so that "is X wired, and does it run before Y" becomes
a question a test can answer exactly rather than approximately.

Concretely: each `wire*` function registers what it wires — the seam, the
collaborator, and its ordering constraint relative to south-bound bring-up —
into a manifest the daemon builds at start-up and a test can read. The wiring
still happens in Go; what changes is that it also becomes inspectable.

### What the first adoption covers

The first adoption takes a **seam class** rather than one `wire*` function:
every per-central registry observer. `internal/wiring` holds `Manifest` and
`Seam`; `central.Registry` carries the manifest and gains
`OnRegisterDeclared`, which declares the seam and then attaches the observer.
All eighteen production `OnRegister` call sites — six in `cmd/`, twelve across
`internal/central/adapter`, `internal/history` and four north-bound packages —
now go through it.

The class was chosen over a single wiring function because it is the one
CLAUDE.md's second wiring rule names: walking the registry once is walking it
at boot, so every cross-central collaborator arrives through this single door.
Every call site has the same shape, which is what lets "declared" and
"attached" be compared exactly rather than approximately.

Three checks come with it, and each fails for a different reason:

- `TestEveryRegistryObserverDeclaresItsSeam` — a raw `OnRegister` outside the
  registry's own file. Without it one undeclared call would make "absent from
  the manifest" mean "either unwired, or wired the old way", which is no
  answer.
- `TestDeclaredSeamNamesAreDistinctAndScoped` — a duplicated or unscoped seam
  name, so the ledger can always say *which* seam is missing.
- `TestE2EDaemonDeclaresEverySeamItWires` — the effect. It boots a daemon
  against a not-yet-ready CCU and reads `GET /diagnostics/wiring`. A wiring
  line that compiles, satisfies every name-matching guard, and sits behind a
  condition that is false in production is invisible to everything except
  this.

The endpoint that makes the third one possible is `GET /diagnostics/wiring`
(admin-only). It answers with an empty list rather than a 503 when
nothing is wired: "the daemon wired none of these" and "the daemon cannot tell
you" must not be the same status, because only one of them is checkable.

The first run of that test found something immediately. Two seams —
`history.recorder` and `webhook.outbound` — are config-gated and absent from a
default deployment. That distinction, between a seam a build never wires and
one this operator has not switched on, previously required reading the
composition root with the config open beside it.

That buys three things no current guard can express:

- **a seam with no entry is unwired**, exactly, with no name matching;
- **ordering** — the class the audits keep hitting, where a caller exists but
  runs before the value it needs — becomes a declared constraint rather than a
  property of line order in a 900-line function;
- the manifest is the same artefact a `/diagnostics` surface could serve, so an
  operator can see what a running daemon actually wired.

### What the second adoption covers

Ordering — the class the audits actually keep hitting, and the reason this ADR
exists. It is expressed as **marks** rather than as the two phase constants the
first adoption declared and never used, because "before south-bound bring-up"
turned out to be the wrong axis: what these constraints are relative to is the
moment something *reads* its collaborator, which is usually its own `Start`.

`wiring.Mark` names a point the daemon passes; `Manifest.Mark` records the
passage; an ordered seam declares `Before` and `After` marks; and
`Manifest.Attach` evaluates them **at the moment the seam attaches** — the only
moment at which the answer is a fact rather than a reading of the source. A
broken constraint does not stop the wiring: refusing to attach would turn a
reporting problem into an outage, and the point of the manifest is that a
running daemon can be asked. The violation is recorded on the seam, served by
the endpoint, and failed on by the end-to-end test.

Four marks and a third phase. Thirty seams are declared across the daemon:
nineteen per-central observers from the first adoption, six ordered, and five
that carry no constraint. `PhaseOnce` exists because the
ADR's first promise — a seam with no entry is unwired — only holds while every
seam has an entry, and a seam with no ordering constraint had no way to declare
one. Choosing it is a claim in itself: that moving this attach earlier or later
changes nothing observable.

The worked example is `webhook.alarm_bus`:
`Outbound.Start` reads the alarm bus once and subscribes, so a bus handed over
after the north bridges start is stored and never read. The setter returns
nothing, the daemon reports healthy, every static guard and pin stays green, and
no alarm or security event is ever forwarded. That constraint used to live in a
comment five hundred lines from the `StartAll` it talked about. Moving the call
across the boundary now turns the end-to-end test red with the consequence
spelled out — verified by doing exactly that.

The `PhaseBeforeSouthbound` / `PhaseAfterSouthbound` constants are gone. They
were a guess at the shape of the ordering problem, made before a single instance
had been expressed in them; keeping them beside the marks would have offered two
ways to say one thing, one of which no seam had ever used.

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

## Consequences

**The cost.** A one-off effort per `wire*` function, and a standing obligation
that new wiring registers itself — enforceable by a guard, unlike the reading it
replaces. `TestEveryWiringSetterHasAProductionCaller` and its ratchet can then
shrink toward deletion rather than being frozen, which is what the round-5 M2
audit found they currently are.

**Where it stands.** Every wiring function in `cmd/openccu-loom` now either
declares a seam or records why it has none, and a guard fails when a new one
joins without answering the question — which is what turns the ADR's end-state
from a direction into a measurement. Of the forty-five, eight declare and
thirty-seven are exempt, and the exemptions are the informative half: fifteen
construct a value and hand it back, five run once per central, five compose
several attaches whose seams are declared one level down, and four are
aggregates.

**One blind spot, and it is in the worst place.** The manifest hangs off the
central registry, so anything wired before `bootstrap.Build` cannot declare into
it. Two functions are on that side of the line, and both are secret handling:
`wireAuditOverlay` installs the cipher and the secret transform on the config
stores, and its absence means operator credentials persist in cleartext —
exactly the silent, severe shape this ADR exists to make visible. The fix is
small and known: build the manifest at the top of `runDaemon` and hand it to the
registry, rather than letting the registry create it.

**Struct-field seams do not need this mechanism, which is a measured answer
rather than a deferral.** A collaborator handed over as a field of a deps
literal was expected to need a shape of its own, because a literal has no
attach point to hang a declaration on. It turns out not to need one:

- Where a nil field changes the *route surface*, the bidirectional
  router/OpenAPI walk catches it — and it is genuinely bidirectional, with nine
  exemptions, all on `/events`, so nothing is being absorbed there.
- Where a nil field changes the route's *tier* without changing the surface —
  the auth gates, the one instance with recorded harm — the authz-scope guard
  catches it. Verified by negative control: nil-ing `RequireAdmin` makes it
  report every admin route as mounted at viewer tier.
- Where a nil field changes neither, the route mounts anyway and the handler
  answers 503. Nine of `rest.Deps`' seventy-five unfilled fields gate a mount,
  all nine gate middleware or the SPA mount; the other sixty-six sit behind
  routes that mount regardless. A 503 for an unconfigured subsystem is the
  documented behaviour and it is loud.

A manifest census of nil fields would have restated all of that at the level of
the mechanism instead of the consequence, and flooded the ledger with a hundred
and forty entries per deps struct to do it.

What the investigation did find is one level up, in the *test helper*:
`fullyWiredRouterDeps` fills 68 of 140 fields, so every guard built on it is
blind to what the other 72 govern. That is now a ratchet of its own — each
unfilled field carries the reason its absence is harmless, and a new dep
joining the nil set fails until somebody decides.

The marks stay deliberately few. A mark is a boundary something downstream
genuinely depends on, and a guard asserts every declared mark is passed exactly
once: a mark nothing passes makes every `Before` naming it hold vacuously and
every `After` naming it fail, at which point the constraint has stopped
measuring the boot sequence and started measuring a typo.

That exactly-once rule is also the mechanism's one real limitation, and it is
worth naming because there is an instance. **A mark has to be an unconditional
boundary**, so a constraint relative to an *optional* subsystem's start cannot
be expressed: the Matter bridge latches per-central readiness before its first
assembly, and on a daemon with Matter switched off there is no assembly to
mark. The MQTT supervisor is the near miss that shows where the line falls —
its `Start` runs even with MQTT disabled, taking its own skip branch, so it is
a boundary of the boot sequence rather than of a feature and does qualify.

Lifting that would mean marks that may be absent, and then every `After`
constraint naming one needs a third answer beside satisfied and violated —
"could not be evaluated" — which is exactly the shape that lets an unchecked
claim read as a checked one. Better to have the gap named than papered over.

The live-CCU adopt path (`central_adopt.go`) is out of scope for the same
reason from the other direction: it runs once per adopted central, so its
orderings are relative to that central's own bring-up rather than to the
daemon's boot, and marks that fire repeatedly are not something this ledger can
reason about.

**The alternative that was rejected by accepting this.** Leaving the composition
root as it is would have kept it at roughly twice the average defect density,
with each audit round finding fresh instances of the same class. That was a
defensible position — the work is real and the daemon ships — but it would have
been a choice, and the reason this ADR exists is that until now it was instead
the default that followed from nobody deciding.

**The two facts this rests on** are worth keeping whatever happens to the
manifest itself: contract coverage is what moves defect density, by a factor of
five in the one package that has it; and a guard that matches a *name* is not
checking a *fact*.

## References

- [`notes/plans/round-5-audit-strategy.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/plans/round-5-audit-strategy.md)
  — measure M6, and the density figures above
- [`notes/audits/2026-08-23-m1-guard-mutation-report.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/audits/2026-08-23-m1-guard-mutation-report.md)
  — what mutation-testing all 359 contract guards found
- [ADR 0002](./0002-multi-ccu-first-class.md) — multi-CCU, the source of most
  ordering constraints
