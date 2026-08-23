# Round 5 — audit the detectors, not the code

- **Status**: accepted, M1 started
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

**The guard suite is large and partly untested.** 272 contract guards, of which
15 are ratchet or exemption maps — surface that is frozen rather than
shrinking. Three guards were found to be decorative during the round-4 tail,
all three by accident.

**The fix wave is itself a defect source.** The round-4 tail introduced six
significant defects into 43 fixes, two of them worse than the finding they
closed. The preceding round left 13 half-applied fixes. Nobody measures the fix
wave, although between audits it is the main source of change.

**The SPA's e2e suite tests against a second, drifting model.** `mock-api.ts` is
1179 hand-written lines, so the fixtures are different DTOs from the real ones,
and the visual baselines pin whatever is currently rendered at
`maxDiffPixels: 0` — including a broken rendering.

## The six measures

### M1 — mutation-test the 272 contract guards

First, because if the guards do not bite, everything downstream is theatre.
For each guard: locate the production line it claims to protect, negate it, run
that single test, expect red. A guard that stays green is the finding.

The three decorative guards found so far show the shapes to expect: a matcher
that models a seam as a method and cannot see a free function; a test that
validates a snapshot's JSON shape while its name promises a dead-code check; a
fan-out table hand-written instead of derived from the domain's own catalogue.

Output is a report, not a diff. Which decorative guards get repaired is a
separate decision.

### M2 — thaw the 15 ratchets

Every entry is either "looked at and decided" or "nobody got to it".
`TestRatchetReasonsAreNotDeferrals` checks the wording, not the truth. Hold each
entry against the code once. This is cheap and it uncovers exactly the surface
that four rounds could not see, because it is marked as known.

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

### M6 — address `cmd/` structurally (ADR, not a sweep)

Twice the density, stable across two audits. Procedural wiring whose
correctness is checkable only by reading. This is the real answer to improving
code quality rather than defect count, and the largest and riskiest of the six.
It belongs in an ADR, decided, not done in passing.

## How we will know it worked

Without a number, "drastically fewer" is not a claim. Four metrics, per round:

1. **Guard hit rate** — what share of a round's findings an *existing* guard
   could have caught. Rising means M1 and M3 are working.
2. **Findings/kLOC per package** — must fall in `cmd/`.
3. **Defect-injection rate of the fix wave** — currently ~14 %. Target: zero.
4. **Ratchet entry count** — must shrink, not freeze.

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
