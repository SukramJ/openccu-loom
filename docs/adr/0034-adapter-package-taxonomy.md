# ADR 0034 — Keep `internal/central/adapter` one package; document its taxonomy

- **Status**: accepted
- **Date**: 2026-06-15
- **Related**:
  `internal/central/adapter/doc.go`,
  the analysis item Area 1 in
  `docs/audit/architecture-analysis-2026-06-15.md`

## Context

The architecture analysis (Area 1) flagged `internal/central/adapter`
as a ~64-file "grab-bag" and suggested sub-dividing it into sub-packages.
The package is large (≈66 production files) and navigation is the real
pain point.

Before splitting, the package's shape was examined:

- **It is a single cohesive wiring layer, not a set of independent
  modules.** The composition-root files (`ccu_wiring.go`,
  `hub_wiring.go`) call *into* the feature adapters (e.g. `WireBackup`,
  the hub/schedule/link wirings), and the feature files share unexported
  helpers and the `*central.Unit` type. The dependency graph is
  core→feature with feature→core helper sharing, not a clean tree.
- **19 packages import `internal/central/adapter`.** A sub-package split
  forces every moved symbol to be exported and re-imported across those
  19 callers plus the internal wiring.
- **Import-cycle hazard.** Splitting feature clusters into sub-packages
  while the wiring core both calls them and shares helpers with them
  invites `adapter ↔ adapter/<x>` cycles, which Go forbids — resolving
  them means hoisting a shared base package and a large, error-prone
  symbol reshuffle.

Under the repo's strict-mode branch protection (every PR re-validated
against 12 required checks, serialized), a 66-file move touching 19
importers is a high-churn, high-risk change for a benefit that is purely
organisational — the package compiles, is well-tested, and works.

## Decision

**Do not split** `internal/central/adapter` into sub-packages. Keep it as
one package and address the actual pain — navigability — with a
**file-taxonomy map** in the package `doc.go`. The map groups the files
into ~11 named clusters (composition root, transport callers, hub,
device lifecycle, links, schedules, UI schema, values cache, reliability,
north-bound sinks, backup, diagnostics) with a one-line purpose each, and
a rule to place new files in the matching cluster so the index stays
accurate.

This delivers most of the legibility benefit a split would, at zero
import-churn and zero cycle risk.

## Alternatives considered

- **Full sub-package split** (the analysis suggestion). Rejected for the
  cohesion / cycle / 19-importer churn reasons above — the cost/risk
  outweighs an organisational gain, especially under strict-mode CI.
- **Partial split of a few leaf clusters** (e.g. `backup`). Rejected —
  leaves the package half-split (inconsistent, arguably less legible
  than either extreme), and even leaf clusters are invoked from the
  wiring core, so each still churns importers.
- **Rename files into prefixed groups only.** The taxonomy doc already
  follows the existing prefix convention; renaming the handful of
  off-pattern files adds churn for little gain and is left to organic
  cleanup.

## Consequences

- The Area 1 adapter item is closed: the package stays unified, with a
  durable in-code taxonomy as the navigation aid.
- If the package later grows past comfortable single-package size, or a
  genuinely independent (no core-helper-sharing, no back-reference)
  cluster emerges, a targeted extraction can be revisited as its own
  ADR — but it is not warranted now.
- The taxonomy map must be kept current when files are added (noted in
  `doc.go`).
