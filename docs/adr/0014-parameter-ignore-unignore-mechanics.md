# ADR 0014 — Parameter Ignore / Un-Ignore Mechanics

- **Status**: Accepted
- **Date**: 2026-05-17
- **Related**: ADR 0002 (multi-CCU), ADR 0005 (visibility as outbound filter), ADR 0007 (strong model source)

## Context

The CCU exposes a complete `VALUES` + `MASTER` paramset per channel.
A subset of those parameters is uninteresting or actively harmful to
surface in normal use (`AES_KEY`, `ACCESS_AUTHORIZATION`,
`CONFIG_PENDING`, `INHIBIT`, the `IDENTIFY_*` / `STATUS_FLAG_*`
family, …). The Python reference family (`aiohomematic`) carries two
overlapping concepts:

- **`IGNORED_PARAMETERS`** — never create a data point in the first
  place.
- **`HIDDEN_PARAMETERS`** — create the data point but suppress it in
  the UI / Home Assistant entity registry.

`aiohomematic` further provides per-device static overrides
(`UN_IGNORE_PARAMETERS_BY_DEVICE`) and a user-level
`CONF_UN_IGNORES` list that promotes an "ignored" parameter back to
a visible data point.

The Python implementation fuses "should this parameter exist as a
DP?" with "is it visible right now?" — the same set of rules
controls both DP construction and outbound visibility, and
`get_un_ignore_candidates()` walks the **raw paramset descriptions**
(pre-DP) to discover what a user *could* un-ignore.

OpenCCU-Loom takes a **fundamentally different** stance.

> **The model is complete. Visibility is a property of the data
> point, not a gate on its existence.**

Every wire parameter becomes a `device.ParameterDataPoint`. The
visibility gate marks DPs that should not appear in north-bound
surfaces by setting `DataPointUsage = NoCreate`. North-bound
adapters (REST, MQTT, WS, Matter) consult `dp.Visible()` /
`dp.EnabledByDefault()` and skip non-visible DPs by default.

This ADR captures the **why** and the **invariants** of that
choice, and pins the mechanics of the un-ignore feature against
those invariants. ADR 0005 covers *where* the outbound filter is
applied; this ADR covers *what makes a parameter hidden* and *how a
user un-hides one*.

## Decision

### 1. Every wire parameter is a DataPoint

The device-hydration pipeline
(`internal/central/adapter/device_pipeline.go`) materializes a
`generic.DataPoint[T]` for every parameter the CCU returns in
`getParamsetDescription`, regardless of whether the parameter is
listed in `IGNORED_PARAMETERS`, `HIDDEN_PARAMETERS`, or any
device-specific masking rule. This is non-negotiable: the model is
the canonical view of the CCU.

Consequences:

- `dev.AllDataPoints()` is exhaustive — diagnostic tooling, snapshot
  comparisons, parity scripts and patch-application logic all see
  the full parameter graph.
- The internal event bus emits value-changed events for every
  parameter; subscribers decide which to forward.
- Custom-DP composition can attach a parameter as a child
  (`Usage = CDPSecondary`) without first checking whether the
  visibility gate would have filtered it out.

### 2. Visibility is encoded in `DataPointUsage`

`internal/model/datapoint.BaseDataPointFields.forcedUsage` is the
single switch that determines north-bound exposure. The gate populates
it during hydration:

| forcedUsage          | Visible() | EnabledByDefault() | Surfaced north-bound |
|----------------------|-----------|--------------------|----------------------|
| *nil* (default)      | true      | true               | yes                  |
| `DataPoint`          | true      | true               | yes                  |
| `CDPPrimary`         | true      | true               | yes (as primary)     |
| `CDPVisible`         | true      | true               | yes                  |
| `Event`              | true      | true               | as event             |
| `NoCreate`           | **false** | false              | **no — ignored**     |
| `CDPSecondary`       | **false** | false              | no — routed via parent |

The two rows that produce `Visible() == false` look identical on a
boolean check but mean very different things:

- `NoCreate` — the parameter is **ignored** because of a static rule
  (`IGNORED_PARAMETERS`, `HIDDEN_PARAMETERS`, wildcard regex, or a
  channel-operation-mode mask). This is the population a user *might*
  want to un-ignore.
- `CDPSecondary` — the parameter is part of a parent custom DP
  (e.g. a single climate composite owns several VALUES parameters).
  Surfacing it independently would duplicate the parent entity.
  **`CDPSecondary` is not an un-ignore candidate.**

### 3. `IGNORED_PARAMETERS` and friends mark DPs `NoCreate`

The static rule sets live in
`internal/store/visibility/rules.go` (`ignoredParameters`,
`hiddenParameters`, the regex wildcards
`ignoredParametersEndPattern` / `ignoredParametersStartPattern`,
`ignoreParametersByDevice`, `unIgnoreParametersByDevice`,
`relevantMasterParamsetsByChannel`,
`relevantMasterParamsetsByDevice`). The decider in
`internal/store/visibility/decider.go` combines those rules with the
required-parameter whitelist and the user un-ignore list, and the
device pipeline calls `dp.SetForcedUsage(hmenum.DataPointUsageNoCreate)`
on every DP whose parameter matches.

Critical invariants:

- **Setting `NoCreate` does not destroy the DP.** It can be promoted
  back to visible by clearing the forced usage or via the un-ignore
  override (see §5).
- **The required-parameter whitelist overrides the ignore rules.** A
  parameter in `IGNORED_PARAMETERS` that also appears in the
  whitelist is left visible — useful for custom DP wiring where an
  otherwise-hidden parameter is a mandatory child.

### 4. Un-Ignore Storage

Per-central un-ignore overrides are persisted in the
`visibility_unignore` SQLite table (`internal/store/sqlite/migrations/014_visibility_unignore.sql`).
Each row holds an `(central_name, pattern)` tuple plus audit
metadata (`updated_at`, `updated_by`). Patterns parse into
`visibility.UnIgnoreEntry` (`internal/store/visibility/parser.go`)
with optional model, channel-type, channel-number and paramset-key
restrictions.

The pattern grammar accepts the same forms `aiohomematic`'s
HA-config flow stores:

- Simple parameter name: `ALARM_COUNT`
- Full-format with channel wildcard: `ALARM_COUNT:VALUES@HmIP-SWSD:all`
- Full-format with channel number: `ALARM_COUNT:VALUES@HmIP-SWSD:1`

`UnIgnoreWildcard` is the string `"all"` (mirrors `aiohomematic.const.UN_IGNORE_WILDCARD`).

### 5. The Un-Ignore Decider — promotion path

`internal/store/visibility/decider.ParameterDecider` exposes
`IsUnIgnored(model, channelType, paramset, parameter)`. During
hydration the visibility gate consults the decider **before** it
would otherwise set `Usage = NoCreate`: a matching un-ignore entry
short-circuits the gate, leaving the DP at its default visible
state. The static `UN_IGNORE_PARAMETERS_BY_DEVICE` table is folded
into the same check via `deviceUnIgnoresByPrefix`.

Live re-loading of un-ignore rules (via REST PUT
`/api/v1/visibility/unignore`) re-runs the gate and re-marks
affected DPs accordingly. Audit entries record the diff per central
(added / removed patterns + affected device count).

### 6. The Un-Ignore Candidate List

The UI offers users a discoverable list of parameter names they
*can* un-ignore. The list is built by `QueryFacade.GetUnIgnoreCandidates`
(`internal/central/queryfacade.go`):

1. Walk every DataPoint in the model (we have them all — see §1).
2. Keep DPs whose forced `DataPointUsage` is exactly `NoCreate`.
   - DPs without a forced usage are visible; not candidates.
   - DPs with `Usage = CDPSecondary` are routed via parent; not
     candidates (see §2).
3. Skip parameters listed in
   `internal/store/visibility/un_ignore.go::ignoreForUnIgnoreParameters`
   (`CONFIG_PENDING`, `STICKY_UNREACH`, `UNREACH`) — these are
   device-wide transport-state parameters that exist on every
   device; un-ignoring them per-parameter is meaningless.
4. Deduplicate by parameter name, sort.

The REST handler `ListVisibilityUnIgnoreCandidates`
(`internal/north/rest/handlers/visibility.go`) calls the facade once
per central, optionally with `paramset=MASTER`
(`?include_master=true`), and unions the results.

### 7. Divergence from aiohomematic

| Concern | aiohomematic | OpenCCU-Loom |
|---------|--------------|-------------|
| Where ignored parameters live | Don't exist as DP | Exist as DP with `Usage = NoCreate` |
| Candidate-list data source | Raw paramset descriptions | Materialized DataPoint graph |
| Filter check on outbound | `is_un_ignored` + per-DP `enabled_default` | `dp.Visible()` (single bit) |
| Surfacing a hidden parameter requires | Restart of integration | Live re-load of un-ignore rules |
| Diagnostic visibility into hidden parameters | None (DP missing) | Full (DP present, marked) |

This divergence is intentional and **load-bearing**:

- Live reload depends on every parameter already existing as a DP.
- Custom-DP composition can refer to hidden children safely.
- Snapshot parity scripts diff the full parameter graph, not the
  visibility-filtered subset.

### 8. Invariants — what tests must enforce

- `dp.Visible() == false ⇔ ForcedUsage() ∈ {NoCreate, CDPSecondary}`.
- `IGNORED_PARAMETERS` membership at hydration time ⇒ DP exists with
  `ForcedUsage() == NoCreate` unless overridden by required-whitelist
  or un-ignore.
- `GetUnIgnoreCandidates` returns ⊆ `{p : ∃ DP with ForcedUsage == NoCreate ∧ p ∉ IgnoreForUnIgnoreParameters}`.
- `CDPSecondary` DPs never appear in `GetUnIgnoreCandidates` output.
- Un-ignore PUT followed by GET round-trips the persisted pattern
  set verbatim per central.

## Consequences

**Positive**

- Single source of truth for the parameter graph. Diagnostic
  tooling, snapshot diffs, custom-DP composition and live-reload
  all read the same model.
- Live re-configuration without restart: REST PUT mutates the
  visibility gate and re-marks DPs in place.
- Bug surface is small — one switch (`forcedUsage`) drives every
  outbound check.

**Negative**

- Memory overhead for parameters that will never be surfaced.
  Bounded — a typical CCU exposes ~10⁴ parameters; the constant
  factor of the extra `BaseDataPointFields` per DP is on the order
  of 100 B, so the total cost is in megabytes, not gigabytes.
- A bug in the visibility gate leaks every parameter through every
  north-bound adapter. ADR 0005's defense-in-depth write gate
  mitigates the write side; reads rely on contract tests.

**Mitigations**

- Contract tests under `tests/contract/` lock the invariants in §8.
- The hub-visibility matrix in `tests/contract/hub_visibility_test.go`
  pins the `ForcedUsage + EnabledDefault` combinations per channel
  family.
- The candidate-list regression test
  (`internal/central/central_coverage_boost_test.go::TestGetUnIgnoreCandidates_*`)
  pins the four hot cases: NoCreate is a candidate, default-visible
  is not, CDPSecondary is not, transport-scope parameters are not.

## Alternatives Considered

1. **Mirror `aiohomematic` 1:1 — only create DPs for visible
   parameters.** Rejected: breaks the strong-model premise (ADR
   0007), forces hot-restart for un-ignore changes, complicates
   custom-DP composition.
2. **Use the CCU `FLAGS.VISIBLE` wire bit as the visibility
   source.** Rejected — that's the CCU's WebUI hint, not a
   OpenCCU-Loom policy lever. Most parameters carry `FLAGS = 1`
   (visible) on the wire and would slip through.
3. **Keep ignore rules pre-hydration but allow DP creation
   on-demand from un-ignore patterns.** Rejected — same restart
   problem, plus an asymmetric API surface.

## Notes

- `aiohomematic` source paths referenced above are reproduced in
  the matter-style **provenance** convention used elsewhere in this
  repo only when they pin a wire-level decision; for the un-ignore
  rules they live in `aiohomematic/store/visibility/rules.py`,
  `aiohomematic/store/visibility/parameter_decider.py`, and
  `aiohomematic/central/query_facade.py`. The corresponding
  OpenCCU-Loom packages are
  `internal/store/visibility/`, `internal/store/visibility/decider.go`,
  and `internal/central/queryfacade.go`.
