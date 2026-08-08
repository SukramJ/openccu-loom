# Dead-Code Summary

Generated: 6011aed4
HEAD: 6011aed4

## Overview

| Metric | Count |
|---|---|
| Total Exported | 28019 |
| Reachable | 4955 |
| Whitelisted | 20102 |
| **Unreachable** | **2962** |

## Top-20 Packages by Dead Code

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/central/events | 10 | 9 | 0 |
| internal/client/backends | 8 | 45 | 4 |
| pkg/hmlog | 8 | 18 | 0 |
| internal/central/adapter | 4 | 92 | 48 |
| internal/north/matter/tlv | 4 | 11 | 26 |
| internal/north/webhook | 4 | 3 | 0 |
| internal/payload | 4 | 236 | 6 |
| pkg/hmerr | 4 | 5 | 32 |
| internal/audit | 2 | 22 | 2 |
| internal/auth | 2 | 24 | 4 |
| internal/ccudata | 2 | 31 | 4 |
| internal/client/transport/binrpc | 2 | 11 | 0 |
| internal/client/transport/xmlrpc | 2 | 21 | 0 |
| internal/model/device | 2 | 43 | 14 |
| internal/model/event | 2 | 5 | 0 |
| internal/model/hub | 2 | 61 | 42 |
| internal/model/optimistic | 2 | 6 | 0 |
| internal/north/discovery/mdns | 2 | 6 | 4 |
| internal/routingkey | 2 | 0 | 2 |
| pkg/hmenum | 2 | 105 | 46 |

## Top-50 Interesting Cases (kind=func, not in _test.go)

| Package | Identifier | File | Line |
|---|---|---|---|
| internal/audit | AsyncSink | internal/audit/persist.go | 293 |
| internal/audit | AsyncSink | internal/audit/persist.go | 293 |
| internal/auth | CSRFToken | internal/auth/csrf.go | 28 |
| internal/auth | CSRFToken | internal/auth/csrf.go | 28 |
| internal/ccudata | SnapshotVersion | internal/ccudata/embed.go | 35 |
| internal/ccudata | SnapshotVersion | internal/ccudata/embed.go | 35 |
| internal/central/adapter | DecodeTimeValue | internal/central/adapter/link_param_metadata.go | 294 |
| internal/central/adapter | DecodeTimeValue | internal/central/adapter/link_param_metadata.go | 294 |
| internal/central/adapter | EncodeTimeValue | internal/central/adapter/link_param_metadata.go | 305 |
| internal/central/adapter | EncodeTimeValue | internal/central/adapter/link_param_metadata.go | 305 |
| internal/central/events | Publish | internal/central/events/bus.go | 244 |
| internal/central/events | Publish | internal/central/events/bus.go | 244 |
| internal/central/events | PublishSync | internal/central/events/bus.go | 337 |
| internal/central/events | PublishSync | internal/central/events/bus.go | 337 |
| internal/central/events | Subscribe | internal/central/events/bus.go | 174 |
| internal/central/events | Subscribe | internal/central/events/bus.go | 174 |
| internal/central/events | WithKey | internal/central/events/bus.go | 66 |
| internal/central/events | WithKey | internal/central/events/bus.go | 66 |
| internal/central/events | WithPriority | internal/central/events/bus.go | 59 |
| internal/central/events | WithPriority | internal/central/events/bus.go | 59 |
| internal/client/backends | DetectBackend | internal/client/backends/detection.go | 67 |
| internal/client/backends | DetectBackend | internal/client/backends/detection.go | 67 |
| internal/client/backends | EncodeHMLevel | internal/client/backends/combined.go | 183 |
| internal/client/backends | EncodeHMLevel | internal/client/backends/combined.go | 183 |
| internal/client/backends | Factory | internal/client/backends/factory.go | 25 |
| internal/client/backends | Factory | internal/client/backends/factory.go | 25 |
| internal/client/backends | UpdateCapabilitiesForVersion | internal/client/backends/capabilities.go | 273 |
| internal/client/backends | UpdateCapabilitiesForVersion | internal/client/backends/capabilities.go | 273 |
| internal/client/transport/binrpc | NewServer | internal/client/transport/binrpc/server.go | 55 |
| internal/client/transport/binrpc | NewServer | internal/client/transport/binrpc/server.go | 55 |
| internal/client/transport/xmlrpc | Format | internal/client/transport/xmlrpc/value.go | 370 |
| internal/client/transport/xmlrpc | Format | internal/client/transport/xmlrpc/value.go | 370 |
| internal/model/device | GenerateTranslationKey | internal/model/device/naming.go | 39 |
| internal/model/device | GenerateTranslationKey | internal/model/device/naming.go | 39 |
| internal/model/event | Sources | internal/model/event/event.go | 76 |
| internal/model/event | Sources | internal/model/event/event.go | 76 |
| internal/model/hub | WrapSysvar | internal/model/hub/sysvar_subtypes.go | 302 |
| internal/model/hub | WrapSysvar | internal/model/hub/sysvar_subtypes.go | 302 |
| internal/model/optimistic | New | internal/model/optimistic/tracker.go | 112 |
| internal/model/optimistic | New | internal/model/optimistic/tracker.go | 112 |
| internal/north/discovery/mdns | NewNoop | internal/north/discovery/mdns/advertiser.go | 109 |
| internal/north/discovery/mdns | NewNoop | internal/north/discovery/mdns/advertiser.go | 109 |
| internal/north/matter/tlv | FullyQualifiedTag | internal/north/matter/tlv/tlv.go | 95 |
| internal/north/matter/tlv | FullyQualifiedTag | internal/north/matter/tlv/tlv.go | 95 |
| internal/north/matter/tlv | ImplicitTag | internal/north/matter/tlv/tlv.go | 85 |
| internal/north/matter/tlv | ImplicitTag | internal/north/matter/tlv/tlv.go | 85 |
| internal/north/webhook | WithBackoff | internal/north/webhook/outbound.go | 97 |
| internal/north/webhook | WithBackoff | internal/north/webhook/outbound.go | 97 |
| internal/north/webhook | WithHTTPClient | internal/north/webhook/outbound.go | 94 |
| internal/north/webhook | WithHTTPClient | internal/north/webhook/outbound.go | 94 |

## Full By-Package Breakdown

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/central/events | 10 | 9 | 0 |
| internal/client/backends | 8 | 45 | 4 |
| pkg/hmlog | 8 | 18 | 0 |
| internal/central/adapter | 4 | 92 | 48 |
| internal/north/matter/tlv | 4 | 11 | 26 |
| internal/north/webhook | 4 | 3 | 0 |
| internal/payload | 4 | 236 | 6 |
| pkg/hmerr | 4 | 5 | 32 |
| internal/audit | 2 | 22 | 2 |
| internal/auth | 2 | 24 | 4 |
| internal/ccudata | 2 | 31 | 4 |
| internal/client/transport/binrpc | 2 | 11 | 0 |
| internal/client/transport/xmlrpc | 2 | 21 | 0 |
| internal/model/device | 2 | 43 | 14 |
| internal/model/event | 2 | 5 | 0 |
| internal/model/hub | 2 | 61 | 42 |
| internal/model/optimistic | 2 | 6 | 0 |
| internal/north/discovery/mdns | 2 | 6 | 4 |
| internal/routingkey | 2 | 0 | 2 |
| pkg/hmenum | 2 | 105 | 46 |
| cmd/openccu-loom | 0 | 2 | 0 |
| internal/addonupdate | 0 | 18 | 18 |
| internal/alarm | 0 | 25 | 0 |
| internal/alarm/codes | 0 | 13 | 0 |
| internal/alarm/engine | 0 | 26 | 6 |
| internal/alarm/journal | 0 | 1 | 0 |
| internal/alarm/outputs | 0 | 24 | 4 |
| internal/auth/ccuauth | 0 | 2 | 0 |
| internal/auth/oidc | 0 | 17 | 2 |
| internal/backup/sbk | 0 | 2 | 4 |
| internal/build | 0 | 0 | 8 |
| internal/central | 0 | 24 | 2 |
| internal/central/cachereset | 0 | 20 | 0 |
| internal/central/coordinators | 0 | 109 | 6 |
| internal/central/registry | 0 | 15 | 0 |
| internal/central/rpcserver | 0 | 10 | 4 |
| internal/channelflags | 0 | 2 | 0 |
| internal/client | 0 | 26 | 8 |
| internal/client/observer | 0 | 3 | 0 |
| internal/client/transport/jsonrpc | 0 | 13 | 0 |
| internal/clock | 0 | 5 | 0 |
| internal/configstore | 0 | 17 | 0 |
| internal/configui | 0 | 16 | 0 |
| internal/diagnostics | 0 | 7 | 4 |
| internal/health | 0 | 16 | 0 |
| internal/history | 0 | 13 | 0 |
| internal/i18n | 0 | 2 | 2 |
| internal/metrics | 0 | 63 | 2 |
| internal/metrics/wiring | 0 | 7 | 0 |
| internal/model/alarmpanel | 0 | 1 | 0 |
| internal/model/calculated | 0 | 10 | 0 |
| internal/model/combined | 0 | 8 | 0 |
| internal/model/custom | 0 | 49 | 10 |
| internal/model/custom/climate | 0 | 8 | 18 |
| internal/model/custom/cover | 0 | 18 | 18 |
| internal/model/custom/light | 0 | 20 | 22 |
| internal/model/custom/lock | 0 | 12 | 14 |
| internal/model/custom/siren | 0 | 12 | 18 |
| internal/model/custom/textdisplay | 0 | 5 | 18 |
| internal/model/datapoint | 0 | 3 | 0 |
| internal/model/device/definitionexport | 0 | 6 | 2 |
| internal/model/group | 0 | 14 | 0 |
| internal/model/naming | 0 | 2 | 4 |
| internal/model/safety | 0 | 2 | 0 |
| internal/model/security | 0 | 5 | 0 |
| internal/model/value | 0 | 0 | 1 |
| internal/north/bridge | 0 | 7 | 0 |
| internal/north/discovery | 0 | 1 | 0 |
| internal/north/discovery/ssdp | 0 | 3 | 0 |
| internal/north/filter | 0 | 1 | 0 |
| internal/north/matter/bridge | 0 | 51 | 38 |
| internal/north/matter/commissioning | 0 | 6 | 9 |
| internal/north/matter/endpoint | 0 | 9 | 0 |
| internal/north/matter/im | 0 | 74 | 14 |
| internal/north/matter/im/subscription | 0 | 5 | 4 |
| internal/north/matter/mdns | 0 | 8 | 4 |
| internal/north/matter/schema | 0 | 0 | 8 |
| internal/north/matter/secure/aesccm | 0 | 1 | 10 |
| internal/north/matter/secure/attestation | 0 | 2 | 16 |
| internal/north/matter/secure/channel | 0 | 4 | 10 |
| internal/north/matter/secure/mattercert | 0 | 7 | 16 |
| internal/north/matter/secure/operational | 0 | 2 | 4 |
| internal/north/matter/secure/setup | 0 | 3 | 0 |
| internal/north/matter/secure/sigma | 0 | 33 | 12 |
| internal/north/matter/secure/spake2 | 0 | 11 | 10 |
| internal/north/matter/store | 0 | 34 | 18 |
| internal/north/matter/transport/message | 0 | 6 | 12 |
| internal/north/matter/transport/mrp | 0 | 10 | 4 |
| internal/north/matter/transport/udp | 0 | 5 | 4 |
| internal/north/mcp | 0 | 8 | 0 |
| internal/north/mqtt | 0 | 86 | 4 |
| internal/north/rest | 0 | 4 | 0 |
| internal/north/rest/problem | 0 | 3 | 4 |
| internal/north/ui | 0 | 1 | 0 |
| internal/orderedjson | 0 | 5 | 0 |
| internal/restapi | 0 | 5 | 0 |
| internal/scheduler | 0 | 4 | 0 |
| internal/security | 0 | 7 | 0 |
| internal/store/devicedetails | 0 | 2 | 0 |
| internal/store/linkprofile | 0 | 1 | 1 |
| internal/store/masterprofile | 0 | 4 | 2 |
| internal/store/patches | 0 | 3 | 0 |
| internal/store/session | 0 | 15 | 0 |
| internal/store/sqlite | 0 | 108 | 28 |
| pkg/hmapi | 0 | 151 | 16 |
| pkg/hmevent | 0 | 9 | 0 |
| pkg/hmui | 0 | 2 | 0 |
| pkg/interfaces | 0 | 81 | 6 |
