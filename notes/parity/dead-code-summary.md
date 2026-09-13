# Dead-Code Summary

Generated: f6cbc9aa
HEAD: f6cbc9aa

## Overview

| Metric | Count |
|---|---|
| Total Exported | 5606 |
| Reachable | 3572 |
| Whitelisted | 1975 |
| **Unreachable** | **59** |

## What these numbers cannot see

Two structural blind spots. Neither is fixed by regenerating this file, and
both have been measured on real deletions — read the counts above with them
in mind.

1. **Package-level members only — no method and no struct field is ever
   classified.** The analyzer walks each SSA package's `Members` map, which
   holds package-level funcs, types, vars and consts. Methods are in the
   program's method sets, not in `Members`; fields are not members at all.
   PR #808 deleted four dead things: `payload.MQTTTopicSet.Config` (a
   field), `payload.MQTTTopicSet.IsZero` and
   `hub.InstallMode.MQTTTopics` (methods), and
   `naming.MQTTHubInstallMode` (a package-level func). Total Exported moved
   by **exactly one**, 5607 -> 5606, and Unreachable did not move at all: the
   field and the two methods were never counted in either direction. A count
   that holds steady across a deletion is not evidence that nothing dead was
   removed.

2. **A flag-gated dead subtree reads as reachable.** RTA reasons about call
   edges, not values, so it cannot evaluate a config flag. PR #799 found
   `internal/north/mqtt/legacy_alias.go` and six guarded branches in
   `bridge.go` dead for the daemon's whole life — the gating field had no
   YAML key, no environment override, no flag and no build tag, and the one
   production construction site never assigned it — while the analyzer
   counted all of it reachable, because the edges are there.

The two point in opposite directions, which is why neither surfaces as
drift: the first under-counts what exists, the second over-counts what is
live. Each needs a different question than "is there an edge to it".

## Top-20 Packages by Dead Code

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/central/events | 3 | 1 | 0 |
| internal/model/optimistic | 1 | 3 | 0 |
| pkg/hmlog | 1 | 0 | 0 |
| internal/auth | 0 | 0 | 1 |
| internal/central/adapter | 0 | 3 | 0 |
| internal/client/backends | 0 | 6 | 0 |
| internal/model/custom | 0 | 3 | 0 |
| internal/model/custom/climate | 0 | 1 | 3 |
| internal/model/custom/cover | 0 | 0 | 6 |
| internal/model/custom/light | 0 | 0 | 6 |
| internal/model/custom/lock | 0 | 0 | 3 |
| internal/model/custom/siren | 0 | 0 | 4 |
| internal/model/hub | 0 | 0 | 1 |
| internal/north/mcp | 0 | 1 | 0 |
| internal/north/mqtt | 0 | 1 | 1 |
| internal/payload | 0 | 1 | 0 |
| internal/store/sqlite | 0 | 0 | 1 |
| pkg/hmenum | 0 | 6 | 2 |

## Top-50 Interesting Cases (kind=func, not in _test.go)

| Package | Identifier | File | Line |
|---|---|---|---|
| internal/central/events | Publish | internal/central/events/bus.go | 244 |
| internal/central/events | PublishSync | internal/central/events/bus.go | 337 |
| internal/central/events | Subscribe | internal/central/events/bus.go | 174 |
| internal/model/optimistic | New | internal/model/optimistic/tracker.go | 112 |
| pkg/hmlog | ForSubsystem | pkg/hmlog/factory.go | 131 |

## Full By-Package Breakdown

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/central/events | 3 | 1 | 0 |
| internal/model/optimistic | 1 | 3 | 0 |
| pkg/hmlog | 1 | 0 | 0 |
| internal/auth | 0 | 0 | 1 |
| internal/central/adapter | 0 | 3 | 0 |
| internal/client/backends | 0 | 6 | 0 |
| internal/model/custom | 0 | 3 | 0 |
| internal/model/custom/climate | 0 | 1 | 3 |
| internal/model/custom/cover | 0 | 0 | 6 |
| internal/model/custom/light | 0 | 0 | 6 |
| internal/model/custom/lock | 0 | 0 | 3 |
| internal/model/custom/siren | 0 | 0 | 4 |
| internal/model/hub | 0 | 0 | 1 |
| internal/north/mcp | 0 | 1 | 0 |
| internal/north/mqtt | 0 | 1 | 1 |
| internal/payload | 0 | 1 | 0 |
| internal/store/sqlite | 0 | 0 | 1 |
| pkg/hmenum | 0 | 6 | 2 |
