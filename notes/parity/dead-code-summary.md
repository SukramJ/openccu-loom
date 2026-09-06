# Dead-Code Summary

Generated: 8144d929
HEAD: 8144d929

## Overview

| Metric | Count |
|---|---|
| Total Exported | 5620 |
| Reachable | 3578 |
| Whitelisted | 1981 |
| **Unreachable** | **61** |

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
| internal/payload | 0 | 3 | 0 |
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
| internal/payload | 0 | 3 | 0 |
| internal/store/sqlite | 0 | 0 | 1 |
| pkg/hmenum | 0 | 6 | 2 |
