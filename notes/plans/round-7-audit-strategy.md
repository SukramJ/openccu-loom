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
