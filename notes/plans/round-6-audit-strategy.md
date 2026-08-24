# Round 6 — audit what everyone trusts

- **Status**: proposed
- **Scope**: what follows round 5, and why it is not a fifth instance sweep
- **Related**: [`round-5-audit-strategy.md`](./round-5-audit-strategy.md),
  [`roadmap.md`](./roadmap.md),
  [`../audits/2026-08-17-round4-audit-findings.json`](../audits/2026-08-17-round4-audit-findings.json),
  [ADR 0065](../../docs/adr/0065-composition-root-wiring-is-checkable.md)

## What the last five rounds actually taught

Rounds 1–4 were full-codebase instance sweeps. Each found fresh occurrences of
the same twelve classes, each fix wave injected roughly 14 % new defects, and
the defect density did not move between rounds 3 and 4. Round 5 stopped
searching the code and audited the **detectors** instead. Every one of its six
measures found something four sweeps had missed.

The follow-up work is the more interesting evidence, because it was not planned.
Five findings came out of it, and they have one shape in common:

| finding | what was actually checked |
| --- | --- |
| 58 request/response bodies invisible to every generated client | a generated package |
| `fullyWiredRouterDeps` fills 68 of 140 fields | a helper's **name** |
| `webhook.alarm_bus` must precede a `StartAll` 500 lines away | a **comment** |
| the wiring ledger could not see the config-store crypto | the **ledger itself** |
| `itoa` rendered 197 as `"C7"` | the **instrument**, mid-measurement |

None of them came from reading production code looking for bugs. Each came from
checking an artefact everybody treats as trustworthy. That is the thesis of
round 6.

## The four measures

### M1 — audit the 191 exemption entries

Sixteen exemption ledgers hold **191 entries**, each a claim written once that
silences a guard for a named case:

| entries | ledger |
| ---: | --- |
| 75 | `routerDepsLeftNil` |
| 36 | `wiringFuncsWithoutSeam` |
| 18 | `wiringSettersWithoutCaller` |
| 15 | `restDomainsWithoutMCPTools` |
| 9 | `routerOpenAPIExemptions` |
| 8 | `emptyAbsentFailedCollapseExemptions` |
| 7 | `eventsWithoutSubscriber` |
| 23 | ten smaller ledgers |

Round 5's M2 audited exactly one of them and found stale entries, entries whose
reason was accurate but whose resolution was deletion, and one reason that was
simply false. **Roughly 165 entries have never been checked.**

They are load-bearing in the worst way: a wrong reason does not fail, it
*silences*. A guard with a false exemption is indistinguishable from a guard
that passes.

**Method.** Partition by ledger, one read-only agent per partition, each finding
re-verified in the main conversation. For every entry: does the named condition
still hold, is the reason true today, and is the honest resolution "keep",
"correct the reason", or "delete the thing"? Round 5's answer for most of one
ledger was the third.

**Negative control.** Seed each partition with one entry whose reason has been
deliberately falsified. A pass that does not surface it did not measure.

### M2 — follow the contract downstream instead of searching the repo

The inline-schema gap had grown to 58 bodies and was **invisible from inside
this repository**. It surfaced only because a types regeneration produced no
model at all for a release that had added an endpoint and three fields.

**Method.** For each contract surface — REST models, WS payloads, enums, the MCP
catalogue, the SPA's generated types — take a recent additive change and follow
it to a generated consumer. The question is not "is it in the spec" but "can a
client hold it".

**Negative control.** Add a field somewhere the generator cannot see and confirm
the pass reports it. If it does not, the pass is measuring the spec rather than
the consumer, which is the mistake being audited.

### M3 — measure the names that claim completeness

`fullyWiredRouterDeps` filled 68 of 140 fields, and a test built on it was
silently vacuous for months. The name was the bug report; nobody read it as one.

45 identifiers in this tree begin with `all`, `every`, `full`, `complete` or
`strict`. Each is a testable assertion.

**Method.** Enumerate them, and for each ask what the name promises and how that
promise could be false. Most will be fine. The ones that are not will be found
in an afternoon.

**Negative control.** Include one identifier whose name is known to be accurate;
a method that flags everything is measuring its own suspicion.

### M4 — mutation-test the guards round 5 added

Round 5's M1 mutation-tested all 359 contract guards: **17 did not bite**. The
suite is 374 now, and 51 guard files have changed since — the new cohort has
never been measured.

The base rate says roughly one in twenty is decorative. That is not a reason to
distrust the round-5 work; it is the reason the measurement exists.

**Method.** As M1 did — remove the production line each guard names, observe,
restore with `cp`, confirm byte-identical. The bite proof is the artefact, not a
green run.

## What this plan deliberately does not do

**A fifth instance sweep.** Four rounds have shown it finds new occurrences of
the same twelve classes and that fixing them injects defects.

**An unaudited fix wave.** Seven green PRs let 72 defects through, two of them
critical. Whatever round 6 finds is audited before it is called done.

## Three process rules, from round 5's own mistakes

These are not general advice. Each one was violated during round 5 and cost
something.

1. **A mutation must be proved to have applied before a "no defect" counts.** A
   sweep over 65 `rest.Deps` fields reported seven unguarded fields. Three were
   artefacts: a multi-line literal the mutation never matched, and two fields
   already nil. The script reported "unguarded" identically for "nothing sees
   this" and "nothing happened".

2. **An agent's classification is a claim, not a result.** A read-only sweep
   classified `wireSystemStatusSubscribers` as constructs-and-returns. It starts
   four registry observers. The final classification was measured from the
   function bodies instead.

3. **No script result without a positive control.** An instrument that returns
   the same answer whether or not the condition holds has measured nothing —
   the same rule the guards are held to, applied to the tools that audit them.

## How we will know it worked

1. **Exemption entries that survive their audit** — the number that stay
   unchanged, versus corrected or deleted. Round 5's single-ledger sample
   suggests well under half survive untouched.
2. **Findings per measure that a sweep could have produced.** If round 6's
   findings look like instance findings, the thesis is wrong and the next round
   should say so.
3. **Decorative guards in the new cohort.** Expect one or two; zero would be
   surprising enough to re-check the method.
4. **Contract surfaces where a generated client cannot see an addition.** The
   target is zero, and the guard added in 0.64.3 should already hold it there.

## Ordering, and where a budget cliff should fall

Work **verify → fix → write the status back, per batch**, never *fix → document
at the end*. Round 4's report carried no per-finding status because the week's
budget ran out before the documentation, and reconstructing it later cost twelve
read-only agents whose entire yield was information that already existed. A
cliff must leave a partial but truthful record, not a complete silence.

M1 first: it is the largest concentrated body of unverified claims, and it is
the one whose failure mode is silence rather than noise.
