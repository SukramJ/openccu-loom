# Dead-Code Summary

Generated: cc927f2b
HEAD: cc927f2b

## Overview

| Metric | Count |
|---|---|
| Total Exported | 23998 |
| Reachable | 2890 |
| Whitelisted | 19724 |
| **Unreachable** | **1384** |

## Top-20 Packages by Dead Code

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/central/events | 5 | 4 | 0 |
| pkg/hmlog | 4 | 9 | 0 |
| internal/central/adapter | 2 | 27 | 26 |
| internal/client/backends | 2 | 20 | 2 |
| internal/north/matter/tlv | 2 | 5 | 13 |
| internal/north/webhook | 2 | 1 | 0 |
| internal/payload | 2 | 117 | 3 |
| internal/audit | 1 | 9 | 1 |
| internal/auth | 1 | 8 | 2 |
| internal/ccudata | 1 | 14 | 2 |
| internal/client/transport/binrpc | 1 | 5 | 0 |
| internal/client/transport/xmlrpc | 1 | 4 | 0 |
| internal/model/optimistic | 1 | 3 | 0 |
| internal/north/discovery/mdns | 1 | 2 | 2 |
| pkg/hmenum | 1 | 24 | 22 |
| pkg/hmerr | 1 | 0 | 19 |
| cmd/openccu-loom | 0 | 1 | 0 |
| internal/addonupdate | 0 | 6 | 9 |
| internal/alarm | 0 | 12 | 0 |
| internal/alarm/codes | 0 | 6 | 0 |

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
| internal/north/discovery/mdns | NewNoop | internal/north/discovery/mdns/advertiser.go | 111 |
| internal/north/matter/tlv | FullyQualifiedTag | internal/north/matter/tlv/tlv.go | 95 |
| internal/north/matter/tlv | ImplicitTag | internal/north/matter/tlv/tlv.go | 85 |
| internal/north/webhook | WithBackoff | internal/north/webhook/outbound.go | 108 |
| internal/north/webhook | WithHTTPClient | internal/north/webhook/outbound.go | 105 |
| internal/payload | For | internal/payload/payload.go | 39 |
| internal/payload | Merge | internal/payload/payload.go | 115 |
| pkg/hmenum | SecurityVerbs | pkg/hmenum/security.go | 265 |
| pkg/hmerr | ErrorContext | pkg/hmerr/errors.go | 219 |
| pkg/hmlog | ForSubsystem | pkg/hmlog/factory.go | 131 |
| pkg/hmlog | Get | pkg/hmlog/contextual.go | 57 |
| pkg/hmlog | New | pkg/hmlog/contextual.go | 43 |
| pkg/hmlog | WithContext | pkg/hmlog/contextual.go | 71 |

## Full By-Package Breakdown

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/central/events | 5 | 4 | 0 |
| pkg/hmlog | 4 | 9 | 0 |
| internal/central/adapter | 2 | 27 | 26 |
| internal/client/backends | 2 | 20 | 2 |
| internal/north/matter/tlv | 2 | 5 | 13 |
| internal/north/webhook | 2 | 1 | 0 |
| internal/payload | 2 | 117 | 3 |
| internal/audit | 1 | 9 | 1 |
| internal/auth | 1 | 8 | 2 |
| internal/ccudata | 1 | 14 | 2 |
| internal/client/transport/binrpc | 1 | 5 | 0 |
| internal/client/transport/xmlrpc | 1 | 4 | 0 |
| internal/model/optimistic | 1 | 3 | 0 |
| internal/north/discovery/mdns | 1 | 2 | 2 |
| pkg/hmenum | 1 | 24 | 22 |
| pkg/hmerr | 1 | 0 | 19 |
| cmd/openccu-loom | 0 | 1 | 0 |
| internal/addonupdate | 0 | 6 | 9 |
| internal/alarm | 0 | 12 | 0 |
| internal/alarm/codes | 0 | 6 | 0 |
| internal/alarm/engine | 0 | 30 | 6 |
| internal/alarm/journal | 0 | 1 | 0 |
| internal/alarm/outputs | 0 | 12 | 2 |
| internal/auth/ccuauth | 0 | 2 | 0 |
| internal/auth/oidc | 0 | 7 | 1 |
| internal/backup/sbk | 0 | 1 | 2 |
| internal/build | 0 | 0 | 4 |
| internal/central | 0 | 10 | 1 |
| internal/central/cachereset | 0 | 9 | 0 |
| internal/central/coordinators | 0 | 47 | 3 |
| internal/central/registry | 0 | 5 | 0 |
| internal/central/rpcserver | 0 | 6 | 2 |
| internal/client | 0 | 13 | 4 |
| internal/client/observer | 0 | 3 | 0 |
| internal/client/transport/jsonrpc | 0 | 5 | 0 |
| internal/clock | 0 | 2 | 0 |
| internal/configstore | 0 | 8 | 0 |
| internal/configui | 0 | 7 | 0 |
| internal/diagnostics | 0 | 6 | 4 |
| internal/health | 0 | 8 | 0 |
| internal/history | 0 | 5 | 0 |
| internal/i18n | 0 | 1 | 1 |
| internal/metrics | 0 | 22 | 1 |
| internal/model/calculated | 0 | 5 | 0 |
| internal/model/combined | 0 | 3 | 0 |
| internal/model/custom | 0 | 23 | 5 |
| internal/model/custom/climate | 0 | 4 | 7 |
| internal/model/custom/cover | 0 | 9 | 9 |
| internal/model/custom/light | 0 | 10 | 11 |
| internal/model/custom/lock | 0 | 6 | 7 |
| internal/model/custom/siren | 0 | 6 | 9 |
| internal/model/custom/switch | 0 | 1 | 0 |
| internal/model/custom/textdisplay | 0 | 2 | 9 |
| internal/model/custom/valve | 0 | 1 | 0 |
| internal/model/datapoint | 0 | 3 | 0 |
| internal/model/device | 0 | 20 | 7 |
| internal/model/device/definitionexport | 0 | 3 | 1 |
| internal/model/event | 0 | 2 | 0 |
| internal/model/group | 0 | 7 | 0 |
| internal/model/hub | 0 | 32 | 23 |
| internal/model/naming | 0 | 1 | 2 |
| internal/model/safety | 0 | 1 | 0 |
| internal/model/security | 0 | 5 | 0 |
| internal/model/value | 0 | 0 | 1 |
| internal/north/bridge | 0 | 3 | 0 |
| internal/north/discovery/ssdp | 0 | 1 | 0 |
| internal/north/filter | 0 | 1 | 0 |
| internal/north/matter/bridge | 0 | 23 | 19 |
| internal/north/matter/commissioning | 0 | 6 | 9 |
| internal/north/matter/endpoint | 0 | 3 | 0 |
| internal/north/matter/im | 0 | 30 | 8 |
| internal/north/matter/im/subscription | 0 | 5 | 4 |
| internal/north/matter/mdns | 0 | 4 | 2 |
| internal/north/matter/schema | 0 | 0 | 4 |
| internal/north/matter/secure/aesccm | 0 | 0 | 5 |
| internal/north/matter/secure/attestation | 0 | 1 | 8 |
| internal/north/matter/secure/channel | 0 | 2 | 5 |
| internal/north/matter/secure/mattercert | 0 | 3 | 8 |
| internal/north/matter/secure/operational | 0 | 1 | 2 |
| internal/north/matter/secure/setup | 0 | 1 | 0 |
| internal/north/matter/secure/sigma | 0 | 13 | 6 |
| internal/north/matter/secure/spake2 | 0 | 4 | 5 |
| internal/north/matter/store | 0 | 17 | 9 |
| internal/north/matter/transport/message | 0 | 2 | 6 |
| internal/north/matter/transport/mrp | 0 | 4 | 2 |
| internal/north/matter/transport/udp | 0 | 2 | 2 |
| internal/north/mcp | 0 | 12 | 0 |
| internal/north/mqtt | 0 | 36 | 2 |
| internal/north/rest/problem | 0 | 3 | 4 |
| internal/orderedjson | 0 | 2 | 0 |
| internal/restapi | 0 | 5 | 0 |
| internal/routingkey | 0 | 0 | 1 |
| internal/scheduler | 0 | 2 | 0 |
| internal/security | 0 | 3 | 0 |
| internal/store/linkprofile | 0 | 1 | 1 |
| internal/store/patches | 0 | 1 | 0 |
| internal/store/session | 0 | 7 | 0 |
| internal/store/sqlite | 0 | 41 | 14 |
| pkg/hmapi | 0 | 81 | 8 |
| pkg/hmevent | 0 | 4 | 0 |
| pkg/hmui | 0 | 1 | 0 |
| pkg/interfaces | 0 | 20 | 3 |
| pkg/mattercontract | 0 | 20 | 0 |
