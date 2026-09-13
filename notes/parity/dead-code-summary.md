# Dead-Code Summary

Generated: 19600b2a
HEAD: 19600b2a

## Overview

| Metric | Count |
|---|---|
| Total Exported | 5609 |
| Reachable | 3505 |
| Whitelisted | 1975 |
| **Unreachable** | **129** |

## What these numbers cannot see

Two structural blind spots. Neither is fixed by regenerating this file, and
both have been measured on real deletions — read the counts above with them
in mind.

1. **Package-level members only — no method and no struct field is ever
   classified.** The analyzer walks each SSA package's Members map, which
   holds package-level funcs, types, vars and consts. Methods are in the
   program's method sets, not in Members; fields are not members at all.
   PR #808 deleted four dead things: payload.MQTTTopicSet.Config (a
   field), payload.MQTTTopicSet.IsZero and
   hub.InstallMode.MQTTTopics (methods), and
   naming.MQTTHubInstallMode (a package-level func). Total Exported moved
   by **exactly one**, 5607 -> 5606, and Unreachable did not move at all: the
   field and the two methods were never counted in either direction. A count
   that holds steady across a deletion is not evidence that nothing dead was
   removed.

2. **A flag-gated dead subtree reads as reachable.** RTA reasons about call
   edges, not values, so it cannot evaluate a config flag. PR #799 found
   internal/north/mqtt/legacy_alias.go and six guarded branches in
   bridge.go dead for the daemon's whole life — the gating field had no
   YAML key, no environment override, no flag and no build tag, and the one
   production construction site never assigned it — while the analyzer
   counted all of it reachable, because the edges are there.

The two point in opposite directions, which is why neither surfaces as
drift: the first under-counts what exists, the second over-counts what is
live. Each needs a different question than "is there an edge to it".

## Top-20 Packages by Dead Code

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/central/events | 5 | 1 | 0 |
| pkg/hmlog | 4 | 2 | 0 |
| pkg/hmreqctx | 4 | 0 | 0 |
| internal/central/adapter | 2 | 3 | 0 |
| internal/client/backends | 2 | 8 | 0 |
| internal/north/webhook | 2 | 0 | 0 |
| internal/audit | 1 | 0 | 0 |
| internal/auth | 1 | 0 | 1 |
| internal/ccudata | 1 | 0 | 0 |
| internal/client/transport/binrpc | 1 | 2 | 0 |
| internal/client/transport/xmlrpc | 1 | 0 | 0 |
| internal/model/optimistic | 1 | 3 | 0 |
| internal/north/discovery/mdns | 1 | 1 | 0 |
| pkg/hmapi | 1 | 5 | 3 |
| pkg/hmenum | 1 | 22 | 3 |
| pkg/hmerr | 1 | 0 | 2 |
| internal/central/coordinators | 0 | 2 | 0 |
| internal/metrics | 0 | 2 | 0 |
| internal/model/custom | 0 | 4 | 1 |
| internal/model/custom/climate | 0 | 1 | 3 |

## Top-50 Interesting Cases (kind=func, not in _test.go)

| Package | Identifier | File | Line |
|---|---|---|---|
| internal/audit | AsyncSink | internal/audit/persist.go | 293 |
| internal/auth | CSRFToken | internal/auth/csrf.go | 29 |
| internal/ccudata | SnapshotVersion | internal/ccudata/embed.go | 35 |
| internal/central/adapter | DecodeTimeValue | internal/central/adapter/link_param_metadata.go | 294 |
| internal/central/adapter | EncodeTimeValue | internal/central/adapter/link_param_metadata.go | 305 |
| internal/central/events | Publish | internal/central/events/bus.go | 244 |
| internal/central/events | PublishSync | internal/central/events/bus.go | 337 |
| internal/central/events | Subscribe | internal/central/events/bus.go | 174 |
| internal/central/events | WithKey | internal/central/events/bus.go | 66 |
| internal/central/events | WithPriority | internal/central/events/bus.go | 59 |
| internal/client/backends | DetectBackend | internal/client/backends/detection.go | 84 |
| internal/client/backends | Factory | internal/client/backends/factory.go | 25 |
| internal/client/transport/binrpc | NewServer | internal/client/transport/binrpc/server.go | 55 |
| internal/client/transport/xmlrpc | Format | internal/client/transport/xmlrpc/value.go | 403 |
| internal/model/optimistic | New | internal/model/optimistic/tracker.go | 112 |
| internal/north/discovery/mdns | NewNoop | internal/north/discovery/mdns/advertiser.go | 112 |
| internal/north/webhook | WithBackoff | internal/north/webhook/outbound.go | 108 |
| internal/north/webhook | WithHTTPClient | internal/north/webhook/outbound.go | 105 |
| pkg/hmapi | New | pkg/hmapi/api.go | 109 |
| pkg/hmenum | SecurityVerbs | pkg/hmenum/security.go | 265 |
| pkg/hmerr | ErrorContext | pkg/hmerr/errors.go | 237 |
| pkg/hmlog | ForSubsystem | pkg/hmlog/factory.go | 131 |
| pkg/hmlog | Get | pkg/hmlog/contextual.go | 57 |
| pkg/hmlog | New | pkg/hmlog/contextual.go | 43 |
| pkg/hmlog | WithContext | pkg/hmlog/contextual.go | 71 |
| pkg/hmreqctx | IsInService | pkg/hmreqctx/hmreqctx.go | 175 |
| pkg/hmreqctx | ResetRequestContextForTesting | pkg/hmreqctx/hmreqctx.go | 189 |
| pkg/hmreqctx | SetRequestContextForTesting | pkg/hmreqctx/hmreqctx.go | 183 |
| pkg/hmreqctx | TraceparentFromContext | pkg/hmreqctx/trace.go | 130 |

## Full By-Package Breakdown

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/central/events | 5 | 1 | 0 |
| pkg/hmlog | 4 | 2 | 0 |
| pkg/hmreqctx | 4 | 0 | 0 |
| internal/central/adapter | 2 | 3 | 0 |
| internal/client/backends | 2 | 8 | 0 |
| internal/north/webhook | 2 | 0 | 0 |
| internal/audit | 1 | 0 | 0 |
| internal/auth | 1 | 0 | 1 |
| internal/ccudata | 1 | 0 | 0 |
| internal/client/transport/binrpc | 1 | 2 | 0 |
| internal/client/transport/xmlrpc | 1 | 0 | 0 |
| internal/model/optimistic | 1 | 3 | 0 |
| internal/north/discovery/mdns | 1 | 1 | 0 |
| pkg/hmapi | 1 | 5 | 3 |
| pkg/hmenum | 1 | 22 | 3 |
| pkg/hmerr | 1 | 0 | 2 |
| internal/central/coordinators | 0 | 2 | 0 |
| internal/metrics | 0 | 2 | 0 |
| internal/model/custom | 0 | 4 | 1 |
| internal/model/custom/climate | 0 | 1 | 3 |
| internal/model/custom/cover | 0 | 0 | 6 |
| internal/model/custom/light | 0 | 0 | 6 |
| internal/model/custom/lock | 0 | 1 | 3 |
| internal/model/custom/siren | 0 | 0 | 4 |
| internal/model/datapoint | 0 | 1 | 0 |
| internal/model/device | 0 | 0 | 1 |
| internal/model/hub | 0 | 1 | 1 |
| internal/north/mcp | 0 | 1 | 0 |
| internal/north/mqtt | 0 | 1 | 1 |
| internal/payload | 0 | 2 | 0 |
| internal/store/sqlite | 0 | 0 | 1 |
| pkg/hmevent | 0 | 1 | 0 |
