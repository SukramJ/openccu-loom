# ADR 0015 — Split `Ignored` from `NoCreate` in `DataPointUsage`

- **Status**: Accepted
- **Date**: 2026-05-17
- **Related**: ADR 0005 (visibility as outbound filter), ADR 0007 (strong model source), ADR 0014 (parameter ignore / un-ignore mechanics — superseded in the parts that conflict with this ADR)

## Context

ADR 0014 captured the architectural premise that OpenCCU-Loom
materializes a DataPoint for every wire parameter and uses
`DataPointUsage` to gate north-bound exposure. It did *not* capture
two crucial semantic distinctions that surface as soon as the
un-ignore feature is implemented faithfully:

1. **`NoCreate` is overloaded.** Today it labels two semantically
   different populations:
   - DPs the visibility gate suppressed because of `IGNORED_PARAMETERS`
     / `HIDDEN_PARAMETERS` / wildcard regex / channel-operation-mode
     mask. → *Real un-ignore candidates.*
   - **Generic DPs that exist as the underlying wire parameter for
     an aggregating DP (Custom / Combined / Week-Profile).** The
     Switch generic DP is alive — the Custom Switch DP consumes it —
     but it should not surface as its own north-bound entity
     because the parent already does. → *Component DP, not an
     un-ignore candidate.*
2. **`CDPSecondary` is not "hidden" in the OpenCCU-Loom sense.** It
   marks the default-disabled HmIP custom-DP replicas (e.g. the
   second and third switch of a multi-channel HmIP-PSM). Those DPs
   are real, surfaced through MQTT discovery with
   `enabled_by_default: false`, and the HA user activates them via
   the HA UI's `entity_registry`. They are **not** what the un-ignore
   list operates on.

The previous implementation conflated all three `NoCreate` populations
and tried to handle `CDPSecondary` inside the un-ignore filter; the
result was an un-ignore candidate list that either undershot
(filtering on the wire `FLAGS.VISIBLE` bit) or overreached (when the
filter switched to `!Visible()` and pulled CDPSecondary in).

This ADR fixes the model.

## Decision

### 1. New enum value `DataPointUsage.Ignored`

`pkg/hmenum/datapoint.go` gains a seventh value:

```go
const DataPointUsageIgnored DataPointUsage = "ignored"
```

`Ignored` is **the unique marker** for DPs suppressed by the
visibility gate's static rule sets:

- `IGNORED_PARAMETERS` membership
- `HIDDEN_PARAMETERS` membership
- `IGNORED_PARAMETERS_END_PATTERN` / `IGNORED_PARAMETERS_START_PATTERN`
  wildcard hits
- `IGNORE_PARAMETERS_BY_DEVICE` device-specific overrides
- Channel-operation-mode masking applied by
  `internal/store/visibility/operation_mode.go`

`NoCreate` keeps its single, narrower meaning: **the generic
parameter DP exists but is consumed by an aggregating parent DP
(Custom / Combined / Week-Profile)** and therefore should not
surface as a standalone north-bound entity. Example: the SWITCH
generic DP on channel 2 of an HmIP-PSM stays alive (the Custom
Switch DP reads / writes it), but the generic DP itself is marked
`NoCreate` so MQTT discovery does not emit a duplicate entity.

### 2. Visibility / EnabledByDefault semantics

`BaseDataPointFields.Visible()` and `EnabledByDefault()` extend
their hidden-mark set:

| forcedUsage    | Visible() | EnabledByDefault() | Surfaced north-bound by default |
|----------------|-----------|--------------------|----------------------------------|
| *nil*          | true      | true               | yes                              |
| `DataPoint`    | true      | true               | yes                              |
| `CDPPrimary`   | true      | true               | yes (as primary)                 |
| `CDPVisible`   | true      | true               | yes                              |
| `Event`        | true      | true               | as event                         |
| `Ignored`      | **false** | false              | **no — un-ignorable**            |
| `NoCreate`     | **false** | false              | no — internal bookkeeping        |
| `CDPSecondary` | **false** | false              | no — via parent custom DP (HA enabled_default=false) |

Both `Ignored` and `NoCreate` look identical on `Visible()`. The
distinction matters only at two places:

- **Un-ignore candidate list** (this ADR) — filters on `Ignored`.
- **Promotion path** — a matching un-ignore entry clears `Ignored`
  back to *nil* / `DataPoint`, making the DP visible. Other
  `NoCreate` populations are not user-toggleable.

### 3. Migration of existing `SetForcedUsage(NoCreate)` call sites

Four call sites in `internal/store/visibility/operation_mode.go`
move to `SetForcedUsage(Ignored)`:

- `ApplyIgnoredParameterMarks` (line 244-256) — `IGNORED_PARAMETERS`
- `ApplyHiddenParameterMarks` (line 333-344) — `HIDDEN_PARAMETERS`
- `ApplyChannelOperationMode` (line 80-120) — operation-mode mask
- `applyOperationModeMask` (line 405-415) — same family

The remaining `SetForcedUsage(NoCreate)` call sites stay unchanged
— each of them marks a generic DP that is being consumed by an
aggregating parent DP:

- `internal/model/weekprofile/datapoint.go:169` — week-profile init
  pins the leaf DP under the Week-Profile composite.
- `internal/model/combined/hscolor.go:90` — Combined-DP HS-color
  marks the underlying COLOR generic DP as parent-consumed.
- `internal/model/combined/level_combined.go:96` — Combined-DP
  level marks the LEVEL / LEVEL_2 generic DPs as parent-consumed.
- `internal/model/combined/weekprofile.go:119` — Combined-DP
  week-profile marks the week-profile component DP as parent-consumed.
- `internal/model/custom/materialize.go:535` — Custom-DP materialize
  marks the generic parameter DP under each custom field as
  parent-consumed (e.g. SWITCH generic DP under the Switch custom DP).

`NoCreate` therefore stays user-non-toggleable: the parent already
surfaces the value, and exposing the child separately would
duplicate the entity.

### 4. Un-ignore candidate selection

`QueryFacade.GetUnIgnoreCandidates` filters strictly on
`forcedUsage == Ignored`. `CDPSecondary` DPs are **not** candidates;
their default-hidden state is handled by MQTT discovery emitting
`enabled_by_default: false` (the HA user activates them via the HA
UI, mirroring `aiohomematic`'s `entity_registry_enabled_default`
behaviour). The transport-scope skip (`CONFIG_PENDING`,
`STICKY_UNREACH`, `UNREACH`) remains.

### 5. Output format — three concatenated lists

The handler returns three formats per parameter, matching
`aiohomematic`'s `get_un_ignore_candidates`:

1. **Simple name** — `ALARM_COUNT`. Pattern matches every device /
   channel exposing that parameter.
2. **Full-format with channel wildcard** — `ALARM_COUNT:VALUES@HmIP-SWSD:all`.
   Pattern matches every channel of the named model.
3. **Full-format with concrete channel number** — `ALARM_COUNT:VALUES@HmIP-SWSD:1`.
   Pattern matches exactly that channel.

The wildcard token is `"all"` (mirrors `aiohomematic.const.UN_IGNORE_WILDCARD`).
`include_master=true` adds a fourth tranche: MASTER paramset
parameters in full-format with concrete channel numbers (no
wildcard variant — MASTER paramsets are typically channel-specific
already).

Operations filtering mirrors `aiohomematic`:

- VALUES tranches: require `READ + EVENT` operations
- MASTER tranche: require `READ`

### 6. CDPSecondary stays out of the un-ignore loop

`CDPSecondary` is a Custom-DP property, not a generic-DP property.
The HmIP multi-channel device pattern is:

- Channel 1 = state channel — binary sensor with the aggregated
  status of channels 2/3/4 — typically `CDPVisible` via
  `visible(parameter=STATE)`
- Channel 2 = primary switch — `CDPPrimary`, default visible
- Channels 3, 4 = secondary switches — same kind as 2, but
  `CDPSecondary`, default disabled. User activates them through
  the HA UI's entity registry.

The MQTT discovery adapter is responsible for translating
`dp.EnabledByDefault() == false` to the HA-Discovery payload's
`enabled_by_default: false`. Un-ignore does not touch this path.

### 7. Invariants

- `dp.forcedUsage == Ignored ⇒ dp.Visible() == false ∧ dp.EnabledByDefault() == false`.
- `GetUnIgnoreCandidates` returns ⊆ `{p : ∃ DP with forcedUsage == Ignored ∧ p ∉ IgnoreForUnIgnoreParameters}`.
- `CDPSecondary` DPs never appear in `GetUnIgnoreCandidates` output.
- A parameter matched by a stored un-ignore entry exits the
  `Ignored` state in the next gate run.
- The visibility-gate paths in `operation_mode.go` are the **only**
  setters of `Ignored` in production code.

## Consequences

**Positive**

- Un-ignore candidate filter becomes a trivial equality check.
- Internal bookkeeping `NoCreate` marks are clearly separated from
  user-facing "hidden" marks. Bug surface drops sharply.
- MQTT-Discovery for HmIP custom-DP secondaries works without
  collision with un-ignore.
- Future audit-tests can pin "only the visibility gate produces
  `Ignored`" — a strong invariant.

**Negative**

- `DataPointUsage` enum now has seven values, deliberately
  divergent from aiohomematic's six. Anyone porting code between
  the projects must remember the split. Documented here + in the
  enum source.
- Two `Visible() == false` populations look identical on the bit
  level; tools that triage hidden DPs must read `ForcedUsage()` to
  tell them apart.

**Mitigations**

- The enum constant carries a doc comment that points back to this
  ADR.
- Contract tests in `tests/contract/` will pin invariants from §7.
- A migration helper (or grep-able test) verifies that no production
  code outside `operation_mode.go` sets `Ignored`.

## Alternatives Considered

1. **Stay with `NoCreate` for everything.** Rejected — the un-ignore
   filter then has to disambiguate populations heuristically, which
   is exactly the bug we are getting away from.
2. **Promote `CDPSecondary` into the un-ignore feed.** Rejected —
   the HA UI already handles `entity_registry_enabled_default`. Two
   sibling mechanisms for "user activates a hidden entity" creates
   surprise and a double sticking-out toggle in real HA installs.
3. **Add `is_un_ignored` as a separate bool, like aiohomematic.**
   Rejected — the OpenCCU-Loom visibility gate runs synchronously
   during hydration; storing the "did un-ignore promote this?"
   flag on the DP itself adds nothing the current setter pattern
   doesn't already convey by *not* setting `Ignored` in the first
   place.

## Notes

ADR 0014 ("Parameter Ignore / Un-Ignore Mechanics") was written
before this split was visible. The parts of ADR 0014 that:

- mention `NoCreate` as the un-ignore-candidate marker → superseded.
  Read `Ignored` everywhere `NoCreate` appeared in that role.
- describe `CDPSecondary` as "routed via parent custom DP" → was
  correct in intent but framed `CDPSecondary` as ignored. The
  correct framing is "default-disabled HmIP custom-DP replica,
  surfaced via MQTT-Discovery `enabled_by_default: false`".
- describe the candidate filter as `Usage == NoCreate` → superseded
  by `Usage == Ignored`.
- list `CDPSecondary` in the §2 "produces `Visible() == false`"
  table → still correct; this ADR just adds `Ignored` to the same
  table row.

The §1 ("every wire parameter is a DataPoint") premise, the §4
un-ignore storage description, the §5 decider mechanics, and the §8
contract-test invariants of ADR 0014 stay in force.
