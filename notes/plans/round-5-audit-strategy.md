# Round 5 — audit the detectors, not the code

- **Status**: complete. M1–M6 done, all follow-ups closed, and ADR 0065's
  first adoption shipped with the guard that makes it a check.
- **Scope**: what replaces a fifth full-codebase instance sweep
- **Related**: [`../audits/2026-08-17-round4-audit-findings.json`](../audits/2026-08-17-round4-audit-findings.json)
  (carries a per-finding `status` since PR #606),
  [`../audits/deep-audit-backlog.md`](../audits/deep-audit-backlog.md),
  [`roadmap.md`](./roadmap.md)

Four full-codebase audit rounds have run. Each found instances of the same
small set of classes, and each fix wave introduced new defects. This document
records what the numbers say the lever actually is, the six measures that
follow from it, and how we will know whether round 5 worked.

## What the numbers say

**Finding density is a contract-coverage signal, not a code-quality one.**

| Area | Findings (R4) | kLOC | Findings/kLOC |
|---|---:|---:|---:|
| `cmd/` (composition root) | 31 | 23 | **1.34** |
| `internal/central` | 38 | 47 | 0.80 |
| `internal/store` | 9 | 15 | 0.61 |
| `internal/north` | 55 | 107 | 0.51 |
| `internal/model` | 18 | 61 | 0.30 |

Read this with its caveat: round 4 deliberately avoided the package-partition
sweeps of rounds 1–3, so per-area density partly reflects where the lenses
pointed. Only one signal is stable across both the 2026-08-15 audit and this
one — **`cmd/` sits at roughly twice the average** (1.43 then, 1.34 now).

The decisive datum comes from the 2026-08-15 round: `internal/client`, the most
densely contract-tested package, carried **0.28 findings/kLOC** — a fifth of
`cmd/`. Where contracts exist, density collapses. That is the entire lever, and
it is an existence proof rather than a theory.

**The guard suite is large and partly untested.** 359 contract guards — the
figure first quoted, 272, counted only the top level of `tests/contract/` and
missed the 84 pins under `wiring_pins/`. 15 of them are ratchet or exemption
maps: surface that is frozen rather than shrinking. Three guards were found to
be decorative during the round-4 tail, all three by accident, which is what
motivated M1.

**The fix wave is itself a defect source.** The round-4 tail introduced six
significant defects into 43 fixes, two of them worse than the finding they
closed. The preceding round left 13 half-applied fixes. Nobody measures the fix
wave, although between audits it is the main source of change.

**The SPA's e2e suite tests against a second, drifting model.** `mock-api.ts` is
1179 hand-written lines, so the fixtures are different DTOs from the real ones,
and the visual baselines pin whatever is currently rendered at
`maxDiffPixels: 0` — including a broken rendering.

## The six measures

### M1 — mutation-test the contract guards

First, because if the guards do not bite, everything downstream is theatre.
For each guard: locate the production line it claims to protect, negate it, run
that single test, expect red. A guard that stays green is the finding.

The three decorative guards found so far show the shapes to expect: a matcher
that models a seam as a method and cannot see a free function; a test that
validates a snapshot's JSON shape while its name promises a dead-code check; a
fan-out table hand-written instead of derived from the domain's own catalogue.

Output is a report, not a diff. Which decorative guards get repaired is a
separate decision.

**Done, 2026-08-23.** All 359 guards mutation-tested, eight agents in isolated
worktrees:

| verdict | count |
|---|---:|
| bites | 333 |
| bites weakly | 6 |
| **decorative** | **17** |
| unclear | 3 |

Report: [`../audits/2026-08-23-m1-guard-mutation-report.md`](../audits/2026-08-23-m1-guard-mutation-report.md).
The suite is mostly sound — 93 % bite — and the failures cluster rather than
scatter.

**The helper is repaired.** `MustFindCallerInFile` took a `calleePackage`
argument and never read it, so a pin was satisfied by any identifier of that
name anywhere: the NOC-chain pin was held up by `spake2.NewVerifier`, which
shares nothing with certificate verification but the word. It now resolves the
path through the caller file's own imports — which keeps the property the old
comment defended ("survive import-alias changes") and adds package correctness,
a combination the original trade-off treated as impossible.

Sorting the 46 dependent pins is what the repair was for:

| | |
|---:|---|
| 24 | already correct |
| 21 | described a **method call**, not a package function; moved to `MustFindMethodCall` |
| 3 | named a package that does not exist — `internal/north/matter/cert` has been `secure/mattercert` all along |

The three stale arguments are the sharpest part: **being unread is what let them
rot unnoticed.** `MustFindMethodCall` was tightened in the same pass — it
compared receivers by string suffix, so a pin on `u` accepted `ccu` and one on
`ch` accepted `dispatch`.

### M2 — thaw the ratchets

Every entry is either "looked at and decided" or "nobody got to it".
`TestRatchetReasonsAreNotDeferrals` checks the wording, not the truth. Hold each
entry against the code once. This is cheap and it uncovers exactly the surface
that four rounds could not see, because it is marked as known.

**Done, 2026-08-23.** All 82 entries across 16 maps audited (the plan said 15
maps; the count was off by one).

| verdict | count |
|---|---:|
| decision | 58 |
| stale | **1** |
| deferral | 15 |
| **false reason** | **8** |

Report: [`../audits/2026-08-23-m2-ratchet-audit-report.md`](../audits/2026-08-23-m2-ratchet-audit-report.md).

**The expectation above was wrong, and the correction matters more than the
finding.** This section predicted free tightening as M2's yield. Exactly one
entry of 82 was stale. Ratchets are well guarded at *admission*; nothing was
being let in that should not have been.

What is actually broken is different: **eight reasons were false**, five of them
in `restDomainsWithoutMCPTools` — the map reserved for settled decisions. The
most confident map was the least accurate. Two more (`north.rest.auth.users`
and `.tokens`) reproduced the very defect their guard's own doc says it exists
to prevent: a leaf bound at boot, exempted as live, so revoking a YAML-only
credential reported "saved" while the daemon kept accepting it. Both now carry
a restart rule.

Both Tier-2 deferrals turned out to be delete decisions rather than build ones:
plumbing built for a granularity nothing asks for. Entry count 82 → 77.

**The mechanism finding.** Admission is guarded; *shrinking has no mechanism at
all* — no owner, no age, no expiry. That is why one entry was stale but eight
reasons were false and fifteen are deferrals: nothing is admitted wrongly,
nobody ever looks again. Two consequences worth acting on:

- the pre-release comment-claims sweep names ratchet justifications in its own
  scope line, and eight false ones survived it — that sweep does not do what it
  says, which is itself an M1-shaped finding;
- a ratchet entry needs an owner and a re-check date, or the next audit finds
  the same thing.

Thirteen deferrals remain (Tier 3–5), eight of them accurate MCP backlog items
that were simply never retired.

### M3 — detectors for the four recurring classes

The same four across four rounds, each with three or more instances and no
guard:

- **Return paths.** detach / disable / unwire are systematically weaker than
  attach. Rule: every `Wire*` / `Register*` that returns a closer needs a test
  asserting the closer's *effect*.
- **The multi-CCU dimension** dropping out of composite keys. An AST check for
  string-concatenated map keys in central-scoped packages.
- **Empty = absent = failed.** Force the type distinction at source boundaries.
- **Success without effect.** The round-trip pattern exists for MQTT; extend it
  to the other planes.

Evidence that this pays: the four guards written during the round-4 tail each
found something on their first run that four audit rounds had missed.

**Done, 2026-08-23.** Four detectors attempted, three shipped, and the fourth
is the interesting one.

| class | guard | result |
|---|---|---|
| return paths | `TestEveryTeardownCloserIsInvokedByATest` | bites |
| empty = absent = failed | `TestReadFunctionsDoNotCollapseFailureIntoEmpty` | bites |
| success without effect | `TestWSCentralStateBroadcastReachesHub` | bites, covers 1 of ~34 broadcasts |
| multi-CCU keys | `TestHADiscoveryIdentifiersComeFromOneBuilder` | bites — **4 production defects** |

**The multi-CCU one was first reported as impossible, and that was a framing
error rather than a fact.** The batch was told, correctly, that the obvious
detector — "find composite keys missing a central" — drowns in profile and
weekday strings, and concluded no detector could bite. The tractable rule is
the inverse: *only these functions may write the `openccu-loom_` prefix.*

That rule found four sites building a Home Assistant identifier by hand from
the bare device address. For the address classes that repeat between CCUs —
`INT000*`, CUxD, the virtual remotes — two CCUs then publish byte-identical
discovery configs for two different heating groups, and Home Assistant keeps
whichever arrived first, permanently, because the payload is retained. One of
the four needs no second CCU at all: a `via_device` pointing at a parent
identifier no device declares, so the sub-device floats unparented.

The lesson is worth more than the guard: **"no detector is possible" is a claim
about a rule, not about a class.** The batch reported honestly and the reviewer
disagreed by finding two instances by hand; the correctly-framed rule then found
four. Keep the instruction that an honest negative is a valid outcome — it is —
but treat one as a prompt to re-frame before accepting it.

One guard had committed its own defect: the empty-collapses-into-absent detector
swallowed a directory-walk error into a skip, which would have made it scan less
and report fewer offenders. Caught by lint, not by review.

### M4 — generate the SPA fixtures from `openapi.yaml`

Types *and* fixtures. This closes the gap where the e2e suite asserts against
DTOs that no longer match the spec, on the largest and least-protected surface.
The `maxDiffPixels: 0` baselines need their own decision in the same pass.

**Done, 2026-08-23 — and the premise above was wrong.** `mock-api.ts` is not
1179 lines of hand-written DTOs; it is a *router* mapping URLs to 59 JSON files
under `assets/ui/tests/e2e/fixtures/`. The gap is narrower and more actionable:
nothing validated those files against `assets/openapi.yaml`.

So the measure became validation rather than generation — generating fixtures
would lose the realistic German device names the screenshots depend on, and the
check is what was missing. `TestSPAE2EFixturesMatchOpenAPISchema` validates
every fixture against the 200 schema of the route that serves it.

Seven fixtures had drifted, and all seven are fixed rather than ratcheted. The
sharpest: `users.json` answered both `/auth/users` and `/users`, which use
different schemas for the same concept — `username` versus `subject` plus
`created_at` — so one file satisfied neither and could not. Fixing the first
missing field on a fixture repeatedly surfaced more behind it: `matter-status`
was short five of seven required counters, not one.

The `maxDiffPixels: 0` question is untouched and still open.

### M5 — hold the fix wave to the audit's standard

Three mandatory elements, all of which earned their place during the round-4
tail:

- an adversarial verifier per batch, whose job is to refute the fix — it caught
  six of six;
- a **sibling-site check**: "does this same shape exist elsewhere?" is precisely
  what the 13 half-applied fixes skipped;
- the brief asks *what could your fix make worse?* — one agent optimised for its
  own finding and silenced seven consumers of an event.

**Done, 2026-08-23.** Written into
[`../contributor/subagent-delegation.md`](../contributor/subagent-delegation.md),
where CLAUDE.md already points for the long form, with the numbers behind each
of the three obligations. M5 is a process change, so "done" means the rule is
recorded where a brief author will meet it — not that a guard enforces it.

### M6 — address `cmd/` structurally (ADR, not a sweep)

Twice the density, stable across two audits. Procedural wiring whose
correctness is checkable only by reading. This is the real answer to improving
code quality rather than defect count, and the largest and riskiest of the six.
It belongs in an ADR, decided, not done in passing.

**Done, 2026-08-23 — and accepted.**
[ADR 0065](../../docs/adr/0065-composition-root-wiring-is-checkable.md) records
the problem, four options and the decision. It was written as *proposed* and
accepted separately, because M6's whole point is that this is a decision and an
ADR self-approved in the same pass would not be one. The incremental adoption is
tracked in [`roadmap.md`](./roadmap.md).

The recommendation is a wiring manifest — each `wire*` function declaring what
it wires and its ordering constraint, so "is X wired, and does it run before Y"
becomes exactly decidable instead of approximated by name matching. It is
additive, needs no dependency, and can be adopted one function at a time.

The measurement behind it: 60 files, ~48 000 lines, 38 `wire*` functions, and
every existing guard over them matching a form rather than a fact.

## How we will know it worked

Without a number, "drastically fewer" is not a claim. Four metrics, per round:

1. **Guard hit rate** — what share of a round's findings an *existing* guard
   could have caught. Rising means M1 and M3 are working.
2. **Findings/kLOC per package** — must fall in `cmd/`.
3. **Defect-injection rate of the fix wave** — currently ~14 %. Target: zero.
4. **Ratchet entry count** — must shrink, not freeze. 82 at the start of
   round 5, 77 after M2, 71 once the deferrals were resolved by deleting the
   seams rather than re-wording their reasons.

### Where round 5 left the numbers

| metric | before | after |
|---|---:|---:|
| contract guards | 359 | 371 |
| decorative guards (mutation-tested) | 17 of 359 | 0 — ten repaired, seven deleted |
| ratchet entries | 82 | 71 |
| dead exported identifiers | 3023 | 3021 |
| production defects found by the new guards | — | 4 (multi-CCU identifiers) |
| contract drift found by the new guards | — | 7 (SPA fixtures) |
| MCP/REST domains with no tool | 9 | 0 |
| per-central seams checkable exactly | 0 | 18 |

The number that matters most is not in the table: **every one of the six
measures found something the four preceding instance sweeps had not.** Not
because the sweeps were careless, but because they were looking at the code and
the defects were in what looks at the code.

### The follow-ups, and what closing them cost

Everything M1–M6 left open is now closed. Three of them were worth more than
their size suggested:

- **The seventeen decorative guards.** Ten were repaired and seven deleted.
  Each repair carries a bite proof against a violation *different* from the one
  M1 used, and each was then re-attacked by an independent reviewer using a
  third violation — which is how the residual holes were found. The
  discovery-template repair is the one to remember: the guards compared
  substrings of the Jinja template, and when they were changed to render it,
  the test-side evaluator turned out to skip the very clause
  (`value_json.value is not none`) that the production constant's own comment
  named as load-bearing. The guard, the thing the guard measured with, and the
  clause were three separate failures stacked on each other.

- **The thirteen deferrals.** Four service hooks on `central.Unit` had accurate
  ratchet reasons and no implementation anywhere in the repository; deleting
  them revealed that `ServiceWiringComplete` had been permanently false in a
  running daemon, because it demanded six hooks of which production wires
  three. Its unit test wired all six itself. A bracketing test, exactly the
  shape CLAUDE.md names.

  The other nine were the MCP/REST parity backlog — a map whose every entry
  stayed accurate for months and none of which was ever retired. Eight are now
  tools; the ninth (`hub`) turned out to be a decision, not a backlog item, and
  is recorded as an exemption with the reason.

- **`maxDiffPixels: 0`** was not an open question. It is already justified in
  `assets/ui/playwright.config.ts` by measurement — two consecutive runs
  produce zero differing pixels, a renamed header costs about ninety — and
  pinned by `TestScreenshotComparisonBudgetIsTightEnoughToSeeDrift`.

### ADR 0065, adopted

The first adoption took a **seam class** rather than one `wire*` function:
every per-central registry observer, eighteen call sites across `cmd/` and five
`internal/` packages, now attaches through `Registry.OnRegisterDeclared`. The
ADR carries the reasoning; what matters for this plan is that the check is real
in all three directions — a raw `OnRegister` fails statically, a duplicated
seam name fails statically, and `GET /diagnostics/wiring` lets an end-to-end
test ask a *running* daemon what it wired. Deleting one wiring line from the
composition root turns that test red; nothing else about the daemon changes.

Two ordering phases are declared and unused. The observer class is
ordering-free by construction, so the first adoption cannot exercise
`before-southbound` / `after-southbound` — they are the shape the next
adoption fills, not a claim that ordering is checked today.

## What this plan deliberately does not do

A fifth instance sweep with 33 search units. Four rounds have shown it finds
new occurrences of the same twelve classes, and that fixing them injects
defects. If searching happens at all, **every finding is adversarially verified
before anyone touches it** — two of the round-4 tail's 43 were factually wrong,
and in two more the proposed fix would have made things worse.

## Ordering, and where a budget cliff should fall

Work in the order *verify → fix → write the status back, per batch*, never
*fix → document at the end*. The round-4 report carried no per-finding status
because the week's budget ran out before the documentation, and reconstructing
it later cost twelve read-only agents whose entire yield was information that
already existed. A cliff must leave a partial but truthful record, not a
complete silence.

## Carried over from round 4

Both recorded with `status: open` and a reason in the findings file:

- **F1-cache-coherency-3** — a CCU backup restore never invalidates the
  persisted MASTER cache.
- **G3-lifecycle-chaos-5** — a `north.mqtt` section save reports success and
  reaches no running bridge.
