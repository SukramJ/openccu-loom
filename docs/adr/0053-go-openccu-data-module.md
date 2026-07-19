# ADR 0053 — Consume CCU metadata via the go-openccu-data module

Date: 2026-07-19
Status: accepted
Supersedes the refresh mechanism of
[ADR 0003](./0003-embed-occu-extracts.md) (the embed decision itself
stands; only the data's source and refresh path change).

## Context

ADR 0003 embedded the openccu-data extracts by copying them into
`internal/ccudata/embedded/` via `make update-ccu-data` against a
local checkout, with a hand-regenerated `MANIFEST.json` as provenance.
That path was manual and demonstrably drift-prone: embed refreshes
lagged upstream releases, and local curation could diverge silently —
the 0.42.9 BWTH parameter labels were curated in loom's embed only and
never reached the shared source of truth (caught during this
migration).

## Decision

The extracts ship as a dedicated Go module,
`github.com/SukramJ/go-openccu-data`: a data artifact embedding the
upstream archives (translations, easymodes, MASTER-paramset profiles,
curated overlays, curated device semantics) with thin typed accessors
(`ReadFile`, `ReadDir`, `DoorbellModels`) and a `SnapshotVersion`
constant recording the upstream release.

- The module is regenerated automatically: the upstream release
  workflow fires a `repository_dispatch` (`data-release`), the
  module's regeneration workflow snapshots the tagged release and
  opens a version-bump PR; dependabot then delivers the loom bump.
- `internal/ccudata` keeps all decode and lookup semantics (overlay
  staging, profile store, translation fallback chains) and consumes
  the module only as its byte source. The module deliberately stays a
  data artifact, not a lookup framework.
- Module tags are independent SemVer (`v0.x`) — the upstream CalVer
  (`2026.7.0`) cannot be a Go major version — and the data stand is
  identified by `SnapshotVersion`, surfaced through
  `ccudata.SnapshotVersion()`.
- `internal/ccudata/embedded/`, `MANIFEST.json`, `make
  update-ccu-data` / `refresh-ccudata` and the drift-check script are
  removed; `make bump-ccudata` pulls the latest artifact.

## Consequences

- Data updates arrive as reviewable dependabot PRs pinned in `go.sum`;
  the manual sync step and its drift class disappear.
- Loom's git history stops accumulating archive churn; the eQ-3
  licensed data lives in a repo with its own NOTICE (the binary-level
  aggregation story of ADR 0003 is unchanged).
- A data fix now travels upstream-release → module regeneration → loom
  bump; every hop is automated, and local-only curation is no longer
  possible — fixes must be upstreamed first, which is the point.
- Operator overrides via configured file paths keep working; only the
  embedded default source changed.
