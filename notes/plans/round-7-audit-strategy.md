# Round 7 — audit the seams between artefacts, from the consumer's side

Status: in progress. Written 2026-08-24, after round 6 closed.

## Why this round exists

Rounds 1–4 were instance sweeps and are exhausted: round 4 found 165 defects
and they were the same class repeatedly. Round 5 audited wiring. Round 6
audited the artefacts everyone trusts — ledgers, generated packages, guards,
the composition root — and its own numbers say the seam is narrowing:

| Round 6 measure | Checked | Wrong |
|---|---|---|
| M1 — exemption ledger entries | 191 | 19 |
| M2 — the generated enum export | 73 enums | no freshness gate at all |
| M3 — wiring pins accepting `nil` | — | 3 |
| M4 — inline OpenAPI bodies | 58 | invisible to every generated client |
| M5 — composition-root handover | 12 structs | 1 defect, 1 dead field |

The instructive part is not the totals. It is that round 6's **two real
findings share one property**, and it is not the property the round was
designed around.

- **The Matter defect.** `eligibility.go` is correct. `assembler.go` is
  correct. The config struct was consistent and every test passed. The defect
  lived in the *disagreement between two artefacts*: one surface reported eight
  derived sensor types as mappable and offered them to the operator, and
  another silently declined every one.
- **The inline schemas.** `openapi.yaml` is correct. The Python types package
  is correct. The gap was only visible from **outside the repo**, by following
  a release into the package generated from that spec.

Both are invisible from inside any single artefact. That is precisely why they
survived seven green PRs and four audit rounds.

## Thesis

**Audit the seams between artefacts, and audit them from the side of the
consumer.** A defect that lives in a relationship cannot be found by reading
either end.

## Measures

Ordered. M1 first: it has the only confirmed starting instance and the
clearest method.

### M1 — capability surfaces against what actually happens

Every surface that answers "what can this daemon do with X", compared against
the code path that does X.

Confirmed instance (fixed in round 6): `GET /api/v1/matter/exposable` reported
eight calculated sensor types as `Mappable`; the assembler dropped every one.

Candidates, each verified present:

- the `Capabilities` detector — its own ledger entry already records that a nil
  detector *silently shrinks the list*
- the surface registry (`internal/north/ui/surface/`) against the views the
  router actually mounts
- `RestartRequiredFieldPaths` against the sites that actually read each value
- `/health` against the subsystems it claims to cover
- `restDomainsAwaitingMCPTools` against the REST domains that exist
- the WS command registry against `wsapi.json`
- `pkg/hmapi` DTOs against what the handlers actually emit

The rule being applied is the one CLAUDE.md already states for MQTT discovery —
*declared and published must be the same set* — carried to every other plane
that declares.

### M2 — the consumer's view

For each artefact that leaves the repo: produce or consume it the way the real
consumer does, and look at what arrives. From inside, each of these looks
correct; that is the point.

- `assets/openapi.yaml` → `datamodel-codegen` (this is what found round 6's M4)
- `assets/wsapi.json` → is there any generated consumer at all?
- MQTT discovery payloads → Home Assistant's own discovery schema
- Matter → chip-tool / matter.js
- Prometheus metrics → `promtool check`
- the SPA's generated types → `tsc`

### M3 — runtime self-report against runtime behaviour

Newly possible since ADR 0065: `/api/v1/diagnostics/wiring` is a declaration
the *running* daemon makes about itself. Every round so far has been static.
Apply the negative-control rule to the daemon's own account of its wiring:
what a seam claims, against the observable effect.

### M4 — bite audit of the guard corpus

287 test functions in `tests/contract/` alone. A guard with no recorded bite
proof is a hypothesis. Round 6's M3 found three pins that accepted `nil`, but
only among those somebody had already looked at. Cheaper than M1–M3 and
probably lower-yield — last, not first.

## The rule round 6 earned the hard way

**Every instrument states its blind spot before it reports, and is run against
a known positive and a known negative before its output is trusted.**

Seven of M5's twenty-two candidate findings were wrong, all for one reason: the
measurement read composite literals, so it could not distinguish "never wired"
from "wired in a shape I do not parse" and returned the same answer for both.
That is the bite rule failing on the tool that audits the guards.

Applied here, it means: before any sweep reports, it must be shown to produce a
*different* result on a case where the condition is known absent. An instrument
that cannot be made to disagree has measured nothing.

Two standing constraints carried from earlier rounds: no sub-agent runs
`make test` or a repo-wide lint (the full gate runs once, in the main
conversation), and no agent brief permits destructive git — the revert path is
`cp` from a copy, never `git checkout --`.

## Ordering, and the stop condition

Work **verify → fix → write the status back, per batch**. Round 4's report
carried no per-finding status because the budget ran out before the
documentation; reconstructing it later cost twelve agents whose entire yield
was information that already existed.

The exchange rate is now explicit: round 6's M5 workflow alone cost ~1.3M
sub-agent tokens for one real defect, one dead field and one guard. It is still
falling.

**Stop condition.** If a measure yields only findings a sweep would also have
produced, the thesis is wrong for that measure and this document says so. That
is a legitimate outcome and a more useful one than a padded total.

---

## Done — M1: capability surfaces against what actually happens

Six surfaces measured. Each instrument had to produce a positive control, a
negative control and a stated blind spot **before** reporting; all six
qualified, and one (surface-registry) established its negative control
experimentally by deleting a member from scratchpad copies and confirming the
comparison named it.

Thirty-two candidate gaps went through an adversarial refute pass: **21
survived, 11 were refuted**. The refutations were not noise — they were the
instrument's blind spots showing, which is why the pass exists.

Closed from M1 in this round:

- **`hmapi.ConfigSnapshot.Extras` and `hmapi.InterfaceState.Note`** are declared
  in `assets/openapi.yaml` and written by nothing, so every generated client
  carries an attribute that can never hold a value.
  `TestEveryPublishedDTOFieldHasAWriter` now pins the whole 492-field surface.
  `Note` additionally has a privacy scrub — `anonymiseDiagnostics` redacts a
  field nothing fills — which is why it is kept rather than removed.

Left open, recorded rather than closed: the remaining 19 confirmed gaps sit in
three groups — health-component coverage (5 subsystems that can fail with no
`/health` component: security, rest, webhook, mDNS, config.overlay), capability
tokens that report configuration rather than liveness (5), and surface-registry
gate/role mismatches (4). Each needs a decision about what the surface *should*
promise, not a patch.

### The instrument corrections are the reusable result

The DTO guard needed three corrections, each found by disagreeing with a hand
check, and together they are a catalogue of how a write hides from a reader:

1. **Name-based matching** missed `InterfaceState.Note` — six unrelated types
   also have a `Note`, and one of them is assigned. Only `types.Info` separates
   them.
2. **Direction.** Twenty-four false positives were inbound request fields: the
   client sends an `AlarmArmRequest` and the handler reads it, so nothing on
   this side writes it. Scoping by "does the daemon ever build a literal of this
   type" is right; scoping by a `Request` name suffix would have missed
   `SecuritySourceOverride`.
3. **Write shape.** `json.Unmarshal(&out.Perms)` and `report.Touched++` are
   writes that neither a composite literal nor an assignment statement can see.
   Six more false positives, all real code.

## Done — M2: the consumer's view

Regenerated `openccu-loom-types` from the current daemon assets. Round 6's M4
fix materialises exactly as intended: 559 new lines in `rest.py`, the 58
promoted schemas arriving as real models.

**The finding is the check standing between the daemon and Prometheus.** The
daemon hand-writes the text exposition format, and `TestPrometheusMetricsExposed`
returned early on an empty body ("acceptable for a fresh registry") and, when a
body was present, accepted any line containing a space as a sample — `%%%bad{{{ 1`
passed. It could not fail for the reason it existed. Replaced by a
text-format 0.0.4 parser with twelve negative controls beside it and two bite
proofs against the real renderer through the HTTP path.

The escaping half is the one that matters going forward: `internal/metrics`
renders label values with Go's `%q`, which also emits `\t`, `\r` and `\xNN` —
none of which Prometheus defines. Nothing escapes today because the only label
value in the repo is a central name and those are `^[A-Za-z0-9_-]+$`. The
parser is what stands between that and the first label sourced from a device
name.

Also measured, and refuted as findings: the WS command vocabulary is already
guarded (`TestWSSchemaArgsResultUseTypedVocabulary` covers all 347 values across
12 tokens), and the SPA's generated types are fresh. One stale comment
corrected: it conflated the 104 commands that declare no `result` with the 82
whose handler actually publishes one.

### An e2e bite proof measures the last build, not the tree

`tests/e2e` execs a prebuilt `./bin/openccu-loom`. Two bite proofs against
`internal/metrics/registry.go` reported green with the file demonstrably
broken, because the test ran the previous binary — the untethered check this
round exists to find, performed while auditing for it. `make build` belongs
between the break and the run, and again after the restore.

## Done — M3: runtime self-report against runtime behaviour

Twenty wiring seams, each carrying a `Why` that states what stops working
without it. Every `Why` is a testable claim, and half of them are wrong:

| | Count |
|---|---|
| `Why` accurate | 9 |
| overstated | 5 |
| understated | 5 |
| **false** | 1 |
| asserts the effect | 11 |
| asserts only that the manifest declares the seam | 6 |
| asserts only that a setter appears — the decorative pin | 3 |
| bracketing tests | 8 |

The false one, verified by hand: `client.value_writer_hooks` claimed "publishes
no event … so every north-bound plane keeps showing the value the device held
before the write". Neither clause holds. An absent hook skips the
CommandTracker record and the wait-for-callback path; nothing there publishes
events at all, and the tracked value has no north-bound reader —
`GetLastSentValue` and `HasInFlight` have zero production callers, while the
same type's `ClearForKey` and `Size()` have several, so the search finds callers
that exist. North-bound planes show the new value through the model's own
optimistic layer, independently of this seam. Corrected, and the resolver half
— which no production site can reach, since `WaitForCallback` is set only in
tests — is now written down rather than left to be inferred.

The other ten inaccurate `Why` texts and the nine seams without an effect test
are **recorded, not closed**. Correcting ten prose claims on agent evidence
would repeat exactly the error this round is about, and the durable fix is an
effect test per seam rather than better prose.

## Where the thesis stands

M2's finding could not have come from a sweep: no line of `prometheus_test.go`
is wrong, and the file passes. M3's could not either: every seam is declared,
the manifest is honest about what it wired, and the endpoint answers correctly —
the defect is that half the declarations describe a consequence that does not
follow. Both live in a relationship rather than at a site.

M1 is more mixed. Its DTO finding is genuine and the guard is durable, but a
third of its candidates were the instrument's own blind spots, and the surviving
19 are decisions rather than defects. On its own M1 would be a weaker case for
the thesis than M2 or M3.

## Done — M4: bite audit of the guard corpus

Four partitions over `tests/contract/`, each agent reading test bodies rather
than doc comments — this repo's decorative guards have all had confident doc
comments. Eleven candidates, put through a refute pass that ran real mutations
against scratchpad copies rather than arguing: **five survived, six were
refuted**, and several refutations came with a named production edit that does
turn the test red.

Three fixed:

- **`TestDocPurity`'s German-word list was hand-picked.** Its doc says it bans
  German function-words; the list held fifteen, and a whole German sentence
  containing none of them passed. The *mechanism* had already been measured —
  `TestGermanWordRuleBites` exists because a plain `\b` never matched an
  umlaut-initial word — but nobody had measured the *list*. Eighteen words
  added, twelve exposed comment lines translated, four of them in production
  packages. Bite-proved in both directions, which a word list needs more than
  most guards: German fires, English containing the same letter sequences does
  not.
- **`TestValueSemanticsChangesAreWellFormed` compared majors only** while its
  register is minor-level, so an entry naming 7.99.0 against API 7.11.0 passed —
  the exact case its own error text calls "a promise, not a record".
- **The reachability shape tests** claim more than they check: their counters
  are the `len()` of the slices beside them, written in one generator pass, so
  the equalities hold by construction. Kept, because they do catch a bad merge
  of a thirty-thousand-line generated file, but the comments now say what they
  cover and name the guard that is tethered to the tree.

One agent claim corrected: an empty inventory does **not** slide through — it
fails two sibling guards first. The finding was real for the individual guard
and overstated in consequence.

### What M4 says about the corpus

Eleven candidates from roughly 287 test functions, five confirmed, and the two
strongest were about **guards whose own doc comment overclaimed** rather than
guards that were empty. That is a milder result than rounds 5 and 6 found in
wiring and ledgers, and it is worth saying plainly: the contract corpus is in
better shape than the artefacts around it. The refutation rate — six of eleven,
with mutations run to prove it — also suggests the remaining candidates would
be mostly noise.

## Round 7 verdict

The thesis held for M2 and M3 and held weakly for M1 and M4.

- **M2 and M3 produced findings no sweep could reach.** Every line of
  `prometheus_test.go` is correct and the file passes; every wiring seam is
  declared and the endpoint answers correctly. Both defects live in a
  relationship — between a check and what it claims to check, between a
  declaration and its consequence.
- **M1 and M4 produced fewer, and a third of M1's candidates were the
  instrument's own blind spots.** Both remain worth having for the guards they
  left behind, but neither is evidence for the thesis on its own.

Carried forward as the round's most reusable output: **an instrument that
cannot be made to disagree has measured nothing**, and the three ways a write
hid from the DTO reader (name collision, direction, write shape) are a
checklist for the next reader-shaped audit.

Left open, recorded with counts rather than silently dropped: 19 confirmed M1
surface gaps (health coverage, capability liveness, surface-registry gates),
ten inaccurate seam `Why` texts and nine seams without an effect test from M3,
and two decorative-but-useful reachability guards from M4.

---

## Follow-up: the open points, worked

### Closed

**A seam that declared itself and wrapped nothing.** `mqtt.hub_ready_restart`
was attached with an empty `func() {}`, its subscription set up in the next
statement — so deleting the subscription left the daemon still reporting the
seam as wired on `/api/v1/diagnostics/wiring`. That inverts ADR 0065:
declaration and handover are meant to be one statement. Fixed, and
`TestEverySeamAttachWrapsItsHandover` rules the shape out across both declaring
forms (`Manifest.Attach`, `Registry.OnRegisterDeclared`).

**Nine seam `Why` texts.** Each was measured against the code before it was
rewritten. The two that mattered most:

- `secret.config_store_crypto` claimed CCU passwords land in the database in
  cleartext. They are refused — `sealPlain` returns
  `ErrPlaintextSecretNotAllowed` and the save becomes a 400. Config *sections*
  are the half that really is written in the clear, while `/health` still
  reports secrets as encrypted.
- `jobs.standard_per_central` claimed no circuit breaker ever recovers. A
  per-interface ticker probes availability independently of the seam.

**Two subsystems that could die silently.** `wireSecurityService` returns nil
on two log-only paths, twelve lines from an alarm service that had recorded on
the health tracker since it was written; and a failed mDNS advertiser left one
Warn line while Matter QR pairing quietly stopped working.
`TestEveryBridgeServiceReportsHealth` holds the class, with `rest` and
`webhook` recorded as deliberately uncovered and why.

### Two instruments corrected by their own negative controls

Both belong with the three from the DTO guard — same shape, more evidence that
this is the recurring failure and not a one-off:

- The health-coverage guard reported the two **best**-instrumented services as
  uninstrumented, because both name their component through a constant and the
  walk matched string literals only. A second correction followed: the tracker
  scopes components as `matter.bridge`, `startup.<central>`, so an exact-match
  comparison called the Matter bridge uncovered.
- The mDNS fix **did not fire**. Enabling mDNS in the harness and re-checking
  showed the component still absent: `startMDNSAdvertiser` has a third outcome,
  `(nil, nil)` for an unusable listen port, which the first version covered
  neither way. Without that control this would have shipped as a fix that never
  runs — the exact failure mode the round is named after, caught on the round's
  own work.

### Still open, and why

- **Capability tokens report configuration, not liveness** (5). `matter.bridge.v1`
  is emitted from `cfg.North.Matter.Enabled`, not from a running bridge. Whether
  a capability token should mean "configured" or "working" is a contract
  decision with a client-visible answer either way, not a defect to patch.
- **Four surfaces have no token at all** — the MQTT raw plane, inbound webhook,
  Diagrams, and the persistence-backed admin surface. Same decision.
- **Surface-registry gate and role mismatches** (4).
- **Nine seams without an effect test.** The Attach guard now pins that each
  wraps real work, which is the structural half; asserting the *consequence*
  each `Why` names is still one test per seam.
- **`webhook` has no health component.** What counts as unhealthy for a
  fire-and-forget sender needs deciding before it can be recorded.

---

## Follow-up 2: the four decisions

### 1. A capability token means CONFIGURED

Decided and written into the contract at three places: the `Info.capabilities`
description in `assets/openapi.yaml`, the constant block in `info.go`, and
`TestCapabilityDetectorsReportConfigurationNotLiveness`, which fails if a
detector getter calls out instead of returning a bool captured once. Prose
alone would not have held — the question comes back every time someone wants
`matter.bridge.v1` to mean "the bridge is up".

The reasoning: a client asks "may I use this path at all", and a briefly
unreachable broker is not a missing capability. A token that came and went with
connectivity would force every client to re-derive its feature set on each
poll. Liveness is `/health`'s answer, and after this round it has the
components for it.

### 2. Four tokens added — API 7.12.0

`mqtt.raw.v1`, `webhook.inbound.v1`, `diagrams.v1`, `admin.persistence.v1`.
Each detector reads the condition that mounts **its own** surface, even where
two resolve to the same opened database today: deriving one from the other
would make them agree by construction and hide the release where they stop
being the same question.

`TestEveryCapabilityTokenIsEmittedAndDocumented` found a pre-existing gap on
its first run — `auth.ccu.v1`, `history.v1`, `mcp.write.v1` and
`addon_self_update` reached every client and appeared in no spec at all. Now
documented.

The SPA's diagram panel gated on `history.v1` as a stand-in. That reads as
correct and fails in one direction: with recording on and no database the view
renders, the editor opens, and every save is refused. It now requires both.

### 3. All nine effect tests — the thorough option, not the recommended one

The recommendation was three; the answer was nine, and nine is what landed.
Seven carry a paired negative control. Two per-central seams register their
central **after** the wiring runs, which is the only ordering that separates a
seam from one that walked the registry once at boot.

`central.devices_created_gate` needed its loop extracted into a named function
so the seam wraps exactly the handover — the shape half the seams already use,
and the one that makes them testable at all.

**Three bite proofs lied before they were fixed**, and each is a rule:

1. A `cp`-based revert whose replacement string does not match reports green
   and looks like a passing bite. Assert the anchor before editing.
2. `runHubDiscoveryRestart` recovers panics, so a nil publisher in the fixture
   made the hub-ready test fail for a reason unrelated to the seam. A swallowed
   panic looks exactly like a seam that never fired.
3. The alarm engine must be started before `Reload` reaches the config-changed
   hook; an unstarted one returns early and the seam looks broken.

That is now six instruments this round corrected by their own controls — three
on the DTO guard, two on health coverage and mDNS, and these three. The pattern
is stable enough to state as a finding in its own right: **the instrument is
wrong about as often as the code is**, and the only thing that separates the
two is a control that can produce the other answer.

### Still open

- **Surface-registry gate and role mismatches** (4). Not touched: they are SPA
  navigation role/gate details, and nothing in this round's work went near that
  surface.
- **`webhook` has no health component.** What counts as unhealthy for a
  fire-and-forget sender is still undecided.
