# Round 5 — audit the detectors, not the code

- **Status**: accepted; M1, M2, M5 done, M6 proposed as ADR 0065, M3–M4 in progress
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

### M4 — generate the SPA fixtures from `openapi.yaml`

Types *and* fixtures. This closes the gap where the e2e suite asserts against
DTOs that no longer match the spec, on the largest and least-protected surface.
The `maxDiffPixels: 0` baselines need their own decision in the same pass.

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

**Done, 2026-08-23 — as a proposal.**
[ADR 0065](../../docs/adr/0065-composition-root-wiring-is-checkable.md) records
the problem, four options and a recommendation, and is deliberately marked
*proposed* rather than accepted: M6's whole point is that this is a decision,
and an ADR written and self-approved in the same pass would not be one.

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
   round 5, 77 after M2.

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
