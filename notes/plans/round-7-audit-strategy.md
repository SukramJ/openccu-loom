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
