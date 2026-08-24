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

#### Done — what the pass found

**191 entries checked, 5 falsifications planted, 5 found** — four by the
auditors, the fifth (`restDomainsWithoutMCPTools["auth"]`, claiming two MCP
tools that do not exist) only by the adversarial reviewer. The two-stage shape
earned its keep on exactly one entry, which is the rate at which it usually
does.

**The experiment had a flaw of its own, and it is worth recording.** Every
partition was told it contained a falsification; only three did. The two that
did not went hunting for a plant that was not there — and still returned real
findings. So "did not find the plant" was not evidence against those two, and
the brief should have said "may contain".

Findings that survived verification:

- **13 entries in `routerDepsLeftNil` were wrong**, in a ledger written the same
  week. Its three grouped reasons had been pattern-matched rather than measured:
  two booleans gating middleware, two values the router defaults, three plain
  parameters, and the two role gates that fall back to `AuthRequire` instead of
  disappearing were all filed as "optional service facades answering 503".
  Several genuine facades do not answer 503 either — a nil `Capabilities`
  detector silently shrinks a list, a nil `DeviceIcons` proxy answers 404, and
  `handlers.Health` does not nil-check its tracker at all. Re-measured per entry
  against the router into seven categories, and the facade reason no longer
  claims a status code it cannot support for all 57.
- **Two exemptions were hiding missing seams**, not describing absent ones.
  `registerFirmwareJobs` was filed as "constructs and returns" and returns
  nothing; `wireAddonUpdateWS` claimed its collaborators declare themselves
  through `OnRegisterDeclared`, and no such call exists anywhere in that path.
  Both now declare a seam. Without them an add-on update's progress never
  reaches a WebSocket client, and no central ever polls for device firmware.
- **Two reasons named a mechanism the code does not have.**
  `GetLinkProfiles` was said to "narrow" on an unknown receiver type; it
  collapses every `load` error, which is safe only because `load` returns
  exactly two, and the entry now says that instead.
  `wireConfigStoreCrypto` was attributed to `hmcli`, which does not reference
  it — it serves the daemon's own `config` subcommand.
- **Two entries were stale**: `retain_cleanup.go` no longer spells the
  identifier prefix it was exempted for, and the `snapshot` entry's comment
  still described a backlog that had been emptied.

**One reported finding was false** and did not survive: an auditor claimed
`cmd/openccu-loom/ws_adapters.go` does not exist. It does. A false finding costs
what a missed one costs — it sends somebody to correct something that was right.

**Rate.** Of 191 entries, 19 needed correction and 2 concealed real work. That
is roughly one in nine, in ledgers whose whole purpose is to record a decision
somebody checked.

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

#### Done — the transform that drops silently is the enum export

Four surfaces traced end to end. REST, WS payloads and the SPA types arrive;
the enums do too, but **nothing keeps them arriving**. `assets/schemas/*.json`
is what the Python types package is generated from, and only two of the 73
exported enums have a parity test. For the other 71 a new wire value reached
neither the JSON nor any client until somebody ran `make export-schemas` by
hand, and nothing failed in the meantime — the SPA's own generated types carry
exactly this gate, added after they had drifted a whole feature behind.

A CI step now regenerates and fails on a diff. Negative control: adding one
`RegaScript` constant makes `enums.json` diverge, and the step catches it.

Three smaller holes came with it, and two are now closed.

`assets/schemas/types.json` carried dangling `$ref`s: its one composite type
referenced `Interface`, `ParamsetKey` and `Parameter`, which are enums living
in a different document with a different shape that no `$ref` from here can
reach. All three dangled, and a strict JSON-Schema consumer would have failed
to resolve the type on its first attempt. Nothing said so because there is no
such consumer — which is the difficulty with a published artefact: it is
checked by being used, and an unused one is checked by nothing. The generator
now declares them as their wire type and names the vocabulary in the
description, and a guard fails on any same-document `$ref` that does not
resolve.

The WS command vocabulary's contract test justified itself by naming "clients
that want to generate type-safe wrappers" — no such client exists;
`scripts/gen_ws.py` reads the broadcast half and nothing else. The comment says
what the test does and does not do now.

Left open deliberately: 104 of 136 WS commands declare no `result` while their
handler publishes one, and the `display_value` field-drift mechanism is closed
for 35 of 43 broadcast payloads with eight documented exceptions. Both are
contract-shaped decisions rather than defects to patch, and neither should be
made by a sweep.

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

#### Done — eleven names, and the plan's own count was wrong

The plan said 45. That number counted test helpers and local variables; in
production code there are eleven, and four of those turned out to live in
`_test.go` files after all. The estimate was made from a grep and never
checked — the same failing this measure exists to find, in the document
proposing it.

Of the seven real ones, six hold and are guarded. **`AllChannelKeys` rested on
nothing:** it generates twenty-four keys from a loop while `channelKeyBitmask`
writes the same twenty-four out by hand next to their bits, with nothing
connecting them. A ninth actor in the loop, or one renamed key, and the daemon
offers a channel it cannot lock — no failure anywhere, the schedule just stops
locking one channel. Now pinned in both directions.

Also found: `reason.go` names `TestEveryHiddenCandidateHasAKnownReason` as the
check that fails on a drifted classifier. No such test exists; the real one is
a subtest that runs only under `-tags=integration`, so a unit run stays green
while the drift is present. The comment says that now, and a unit-level guard
pins the drift the comment was worried about: a reason `Classify` can match but
`reasonPrecedence` cannot order is matched and then silently dropped, and
`ClassifyPrimary` reports the parameter as hidden for an unknown reason.

That one is worth a note on method. A first pass measured the precedence list
with a regex window that stopped short of the end and concluded `ReasonReadOnly`
was missing — a live defect, on paper. Reading the actual slice showed it
present at line 104. The finding was the drift *risk*, not a drift; the guard is
right either way, and the difference between the two is what a second look
costs.

`fullDayWeekday` is gone: an unused test helper kept "for additional
weekday-layout tests", carrying an unenforced precondition (its `n` must divide
1440, and `n = 7` would silently build a 1435-minute day). A helper kept for a
future that has not arrived is dead code with a plan attached to it.

### M4 — mutation-test the guards round 5 added

Round 5's M1 mutation-tested all 359 contract guards: **17 did not bite**. The
suite is 374 now, and 51 guard files have changed since — the new cohort has
never been measured.

The base rate says roughly one in twenty is decorative. That is not a reason to
distrust the round-5 work; it is the reason the measurement exists.

**Method.** As M1 did — remove the production line each guard names, observe,
restore with `cp`, confirm byte-identical. The bite proof is the artefact, not a
green run.

#### Done — 13 of 13 bite, and one bites less than it looked

Better than the base rate predicted: none of the new cohort is decorative. One
guard (`TestValueSemanticsChangesAreWellFormed`) was not measured and is
recorded as unmeasured rather than as passing.

The finding is a limitation the mutation pass surfaced rather than a dead
guard. **The three wiring pins were satisfied by `Field: nil`** — the field
spelled out, the collaborator not handed over, which is precisely the state a
pin exists to rule out. `MustFindStructLiteralField` checked that the key
appeared, not that anything arrived. Fixed, and the pins now fail on a nil
value.

One methodological note worth keeping: a mutation that breaks the build prints
`[build failed]`, which reads as red and measures nothing. A bite proof has to
compile.

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

---

## Done — M5: the composition root against the structs it fills

Added after M1–M4, from the observation that closed M1 (a ledger's own entries
are worth auditing) applied one level down: the composition root is a ledger
too. Every `<pkg>.Deps{…}` literal is a claim that the daemon hands over what
the adapter declared, and nothing compared the two sets.

Twelve dependency structs measured, declared-vs-filled, each gap then read
against its consumer. Yield:

| Verdict | Count | What it was |
|---|---|---|
| Real defect | 1 | `bridge.Config.IncludeMeasurements` — see below |
| Real, trivial | 1 | `mqtt.BridgeConfig.DiscoveryObjectIDFmt`, a dead field whose comment advertised a default nothing applied |
| Refuted | 7 | wired after construction, which the measurement could not see |
| Known / by design | ~13 | defaulted values, stub handlers that answer with a visible error, one already-registered dead feature |

**The defect.** Eight calculated sensor types are reported `Mappable` by
`GET /api/v1/matter/exposable`, so the SPA lists them and an operator can
allowlist them; the assembler dropped every one, because the flag admitting
them was never handed to the bridge and no config key existed to set it. The
field's comment claimed operators enable it "via the config UI or daemon config
flag". The assembler was tested with the flag both true and false — it set the
flag itself, so it proved the assembler could honour a value and never that the
daemon supplied one. This is the bracketing test, in the one place seven audit
rounds had not looked.

**The refutations are the methodological result.** Seven of twenty-two findings
were wrong for a single reason: the measurement read composite literals, and
`deps.Foo = bar` after the literal, or `obj.SetFoo(…)` after construction, are
both ordinary wiring. The instrument had no negative control for its own blind
spot — it could not distinguish "never wired" from "wired in a shape I do not
parse", and returned the same answer for both. That is the rule the guards are
held to, failing on the tool auditing them, and it is why the guard that landed
covers five seams rather than twelve: the seven that wire post-construction are
named as uncovered, with their twenty-one unfilled fields counted, rather than
absorbed into a ledger whose entries would be excusing the guard's limitation.

**The guard.** `TestCompositionRootHandsOverEveryDeclaredField` catches the
class no per-field pin can: a *new* field added to a Deps struct whose author
fills it in the tests and forgets the daemon. A pin for that field would have
to be written by the same person in the same change that forgot it. Bite-proved
once per seam; the `matterbridge.Config` bite is the original defect.

### What the round confirmed about the thesis

M5 is the strongest evidence for round 6's premise. The defect had survived
seven green PRs and four audit rounds, and no instance sweep would have found
it: nothing is wrong at any single site. `eligibility.go` is correct,
`assembler.go` is correct, the config struct was consistent, and every test
passed. The defect lived in the *disagreement between two artefacts*, which is
only visible when you audit the relationship rather than the instances.
