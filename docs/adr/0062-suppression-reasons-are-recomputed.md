# ADR 0062 — Suppression reasons are recomputed, not recorded

- Status: accepted
- Date: 2026-08-11

## Context

[ADR 0015](0015-datapoint-usage-ignored.md) made
`DataPointUsage.Ignored` **the unique marker** for a data point the
visibility gate suppressed, and that is what makes the un-ignore
candidate filter a trivial equality check. The mark answers *whether* a
parameter is hidden. It does not answer *which rule* hid it, and by
construction it cannot: it is one enum value on one field.

Eight passes run over every freshly ingested device
(`internal/central/adapter/device_pipeline.go:769-807`), five of which
can write `Ignored`:

| Pass | Rule |
| --- | --- |
| `ApplyChannelOperationModeGating` | the channel's `CHANNEL_OPERATION_MODE` excludes the parameter |
| `ApplyIgnoredParameterMarks` | the decider: static ignore list, wildcard prefix/suffix, per-model suppression, channel restriction, event suppression, MASTER whitelist |
| `ApplyHiddenParameterMarks` | `hiddenParameters` — created and consumed elsewhere, not surfaced standalone |
| `ApplyInternalParameterMarks` | `FLAGS` carries `INTERNAL` and the parameter is not allow-listed |
| `ApplyNoEventNoWriteMarks` | `OPERATIONS` has neither `EVENT` nor `WRITE` |

They run in sequence against one shared field, each able to overwrite
what the previous wrote. So even a faithful record of "the pass that
marked it" would be a record of *the last pass to run*, which is an
artefact of pipeline ordering rather than an explanation an operator
could act on.

This did not matter while the un-ignore picker rendered a flat list. It
started to matter when that list turned out to be unusable at fleet
scale: on the 399-device embedded fleet the candidate set is 2800
pattern strings — the cross-product of parameter × device model ×
channel in three pattern formats — built from only 45 distinct
parameters. Making that screen workable meant grouping the rows and
letting an operator filter out the categories that are internal
plumbing, which requires naming the category. Measured on that fleet,
one category alone (`internal_flag`) accounts for 26 of the 45 groups
and is the *only* reason for all 26 of them.

## Decision

The suppression reason is **recomputed at query time from the same rule
sets the mark passes consult**, not stored when the mark is written.

`visibility.Classify` (`internal/store/visibility/reason.go`) takes the
wire facts — model, channel type and number, paramset, parameter,
`ParameterData`, and the channel's current operation mode — and returns
every rule that matches, ordered by how much each explains to an
operator rather than by pipeline order. `ClassifyPrimary` folds that to
the single reason a badge shows. Living in package `visibility` is the
substance of the decision, not a detail: the classifier reads
`ignoredParameters`, `ignoredParametersStartPattern`,
`ignoredParametersEndPattern`, `ignoreParametersByDevice`,
`hiddenParameters`, `channelOperationModeVisibility`,
`configurableChannelTypes` and `checkMasterParameterIgnored` — the very
identifiers the passes use — so a rule edit moves both sides at once.

Two consequences of that placement are deliberate:

1. **The classifier explains a suppression; it never decides one.**
   Callers pass parameters already known to be suppressed and ask why.
   Operator overrides (un-ignore entries, the required-parameter
   whitelist) are not consulted, because they govern whether a parameter
   is hidden at all — and a parameter they re-enabled is no longer a
   candidate to explain.
2. **A category whose rule lives elsewhere borrows the predicate rather
   than restating it.** The `week_profile` category — the cells of a
   device week profile, up to 6 profiles × 7 weekdays × 13 slots × 2
   fields on a single climate device — calls
   `weekprofile.IsParameterName`, which sits next to the parsers that
   define the two key grammars it recognises.

Rejected alternatives:

- **Record the reason at mark time**, by widening the forced-usage field
  or adding a second one. This is the shape that looks safest and is
  not: it records pipeline order, it puts a UI concern into the
  hot-path device model that every adapter reads, and it has to be
  maintained at five call sites instead of one.
- **Classify in the SPA by name pattern.** No backend change, but a
  second copy of `_STATUS`, `ERR_TTM_`, `hiddenParameters` and the
  MASTER whitelist in TypeScript, drifting from `rules.go` on the first
  edit and lying to the operator when it does.

## Consequences

The cost of this decision is a real drift risk: the classifier can fall
behind a new or changed suppression rule, and the symptom is a row whose
badge is wrong or missing — quiet, and invisible in every unit test that
supplies its own input.

That risk is carried by a guard rather than by discipline.
`TestUnIgnoreCandidateGroupsAgainstTheFleet/every_candidate_has_a_known_reason`
(`tests/integration/visibility_candidate_groups_test.go`) walks every
candidate the full embedded fleet produces and fails on any group that
no rule explains, listing the parameter and the models it appears on.
Deleting the `internal_flag` branch from `Classify` makes it fail with
26 unexplained groups.

Its limit is worth stating plainly, because it is the part a future
reader will otherwise assume away: the guard fires only when a candidate
has **no** matching rule at all. A parameter that keeps a second, less
apt reason after its primary rule is removed still classifies, just
worse — deleting the `read_only` branch changes nothing on the current
fleet, because `read_only` is never the sole reason there. The
per-rule table in `TestClassifyNamesTheRuleThatHidTheParameter`
(`internal/store/visibility/reason_test.go`) covers that half: one case
per rule set, each built from a real entry of the table it exercises, so
removing a branch breaks its case. The two guards are complementary and
neither is sufficient alone.

Further consequences:

- The reason vocabulary is published — `GET
  /api/v1/visibility/unignore/candidates` returns it in `reasons`, and
  `AllHiddenReasons` is its single source — so the SPA builds its filter
  chips from the server's list instead of a hard-coded copy, and a new
  category reaches the UI without a frontend release.
- `unknown` is deliberately *not* in that published vocabulary. It is a
  drift signal, and it renders as a visible badge rather than being
  dropped, so a classifier gap surfaces to whoever sees it first.
- Classification costs one model walk per request (~40 ms VALUES, ~120
  ms MASTER on the 399-device fleet, both paramsets in a single walk).
  That is acceptable for a settings screen and was never the bottleneck
  the rework addressed — the flat list's cost was client-side.
- Nothing in the device model changed. ADR 0015 stands unamended:
  `Ignored` remains the unique marker, and the candidate filter remains
  an equality check.
