# Dead-Code Summary

Generated: 2026-06-04T16:02:49Z
HEAD: 069f654

## Overview

| Metric | Count |
|---|---|
| Total Exported | 22219 |
| Reachable | 3696 |
| Whitelisted | 15829 |
| **Unreachable** | **2694** |

## Top-20 Packages by Dead Code

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/central/adapter | 55 | 77 | 38 |
| internal/model/naming | 22 | 2 | 4 |
| internal/configui | 20 | 68 | 0 |
| internal/client/backends | 19 | 25 | 4 |
| internal/model/custom | 19 | 49 | 10 |
| internal/north/mqtt | 18 | 94 | 2 |
| internal/central/events | 13 | 11 | 0 |
| internal/payload | 12 | 232 | 6 |
| pkg/hmlog | 12 | 20 | 0 |
| internal/auth | 10 | 21 | 4 |
| internal/metrics | 10 | 63 | 2 |
| internal/model/hub | 10 | 33 | 16 |
| internal/store/sqlite | 10 | 53 | 18 |
| internal/audit | 8 | 20 | 2 |
| internal/central/coordinators | 8 | 117 | 4 |
| internal/client/transport/binrpc | 8 | 11 | 0 |
| internal/north/matter/im | 8 | 68 | 14 |
| internal/north/matter/tlv | 8 | 11 | 26 |
| internal/north/mqtt/protocol | 7 | 13 | 0 |
| pkg/hmerr | 7 | 7 | 22 |

## Top-50 Interesting Cases (kind=func, not in _test.go)

| Package | Identifier | File | Line |
|---|---|---|---|
| internal/audit | AsyncSink | internal/audit/persist.go | 291 |
| pkg/hmerr | ErrorContext | pkg/hmerr/errors.go | 153 |
| pkg/hmerr | LogBoundaryError | pkg/hmerr/boundary.go | 61 |
| pkg/hmerr | ExceptionToFailureReason | pkg/hmerr/errors.go | 395 |
| internal/auth/oidc | Verify | internal/auth/oidc/jwks.go | 129 |
| pkg/hmevent | NewBaseAt | pkg/hmevent/event.go | 45 |
| internal/central/events | WithPriority | internal/central/events/bus.go | 57 |
| internal/central/events | PublishSync | internal/central/events/bus.go | 274 |
| internal/central/events | Add | internal/central/events/batch.go | 41 |
| internal/central/events | Subscribe | internal/central/events/bus.go | 152 |
| internal/central/events | Publish | internal/central/events/bus.go | 202 |
| internal/central/events | WithKey | internal/central/events/bus.go | 64 |
| internal/model/event | Sources | internal/model/event/event.go | 76 |
| internal/model/optimistic | New | internal/model/optimistic/tracker.go | 112 |
| internal/payload | For | internal/payload/payload.go | 37 |
| internal/payload | Merge | internal/payload/payload.go | 89 |
| internal/routingkey | GenerateChannelUniqueID | internal/routingkey/uniqueid.go | 107 |
| internal/model/device | GenerateTranslationKey | internal/model/device/naming.go | 39 |
| internal/client/backends | UpdateCapabilitiesForVersion | internal/client/backends/capabilities.go | 229 |
| internal/client/backends | EncodeHMLevel | internal/client/backends/combined.go | 183 |
| internal/client/backends | Factory | internal/client/backends/factory.go | 25 |
| internal/client/backends | DetectBackend | internal/client/backends/detection.go | 67 |
| internal/client | SanitizeErrorMessage | internal/client/errors.go | 33 |
| internal/client/transport/xmlrpc | Format | internal/client/transport/xmlrpc/value.go | 370 |
| internal/model/hub | WrapSysvar | internal/model/hub/sysvar_subtypes.go | 302 |
| internal/health | NewConnectionRegistry | internal/health/connection.go | 311 |
| internal/health | WithConnectionClock | internal/health/connection.go | 90 |
| internal/health | NewConnection | internal/health/connection.go | 70 |
| internal/client/transport/binrpc | NewServer | internal/client/transport/binrpc/server.go | 55 |
| pkg/hmlog | Get | pkg/hmlog/contextual.go | 59 |
| pkg/hmlog | ForSubsystem | pkg/hmlog/factory.go | 120 |
| pkg/hmlog | WatchSlow | pkg/hmlog/slowquery.go | 35 |
| pkg/hmlog | WithContext | pkg/hmlog/contextual.go | 73 |
| pkg/hmlog | New | pkg/hmlog/contextual.go | 45 |
| internal/north/matter/tlv | ImplicitTag | internal/north/matter/tlv/tlv.go | 85 |
| internal/north/matter/tlv | FullyQualifiedTag | internal/north/matter/tlv/tlv.go | 95 |
| internal/configui | ClassifyLinkParameter | internal/configui/link_param_metadata.go | 181 |
| internal/configui | GetTimePresets | internal/configui/link_param_metadata.go | 272 |
| internal/configui | Generate | internal/configui/generator.go | 147 |
| internal/configui | DecodeTimeValue | internal/configui/link_param_metadata.go | 294 |
| internal/configui | Humanize | internal/configui/labels.go | 118 |
| internal/configui | DetermineWidget | internal/configui/widget.go | 62 |
| internal/configui | EncodeTimeValue | internal/configui/link_param_metadata.go | 308 |
| internal/configui | ParameterStep | internal/configui/step.go | 21 |
| internal/central/adapter | EncodeTimeValue | internal/central/adapter/link_param_metadata.go | 305 |
| internal/central/adapter | DecodeTimeValue | internal/central/adapter/link_param_metadata.go | 294 |
| internal/central/adapter | BridgeCombinedDataPoint | internal/central/adapter/combined_bridge.go | 51 |
| internal/north/discovery/mdns | NewNoop | internal/north/discovery/mdns/advertiser.go | 101 |
| internal/north/matter/schema | ClusterRevision | internal/north/matter/schema/lookup.go | 11 |
| internal/boundary | Execute | internal/boundary/execute.go | 47 |

## Full By-Package Breakdown

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/central/adapter | 55 | 77 | 38 |
| internal/model/naming | 22 | 2 | 4 |
| internal/configui | 20 | 68 | 0 |
| internal/client/backends | 19 | 25 | 4 |
| internal/model/custom | 19 | 49 | 10 |
| internal/north/mqtt | 18 | 94 | 2 |
| internal/central/events | 13 | 11 | 0 |
| internal/payload | 12 | 232 | 6 |
| pkg/hmlog | 12 | 20 | 0 |
| internal/auth | 10 | 21 | 4 |
| internal/metrics | 10 | 63 | 2 |
| internal/model/hub | 10 | 33 | 16 |
| internal/store/sqlite | 10 | 53 | 18 |
| internal/audit | 8 | 20 | 2 |
| internal/central/coordinators | 8 | 117 | 4 |
| internal/client/transport/binrpc | 8 | 11 | 0 |
| internal/north/matter/im | 8 | 68 | 14 |
| internal/north/matter/tlv | 8 | 11 | 26 |
| internal/north/mqtt/protocol | 7 | 13 | 0 |
| pkg/hmerr | 7 | 7 | 22 |
| internal/auth/oidc | 6 | 17 | 2 |
| internal/ccudata | 6 | 31 | 4 |
| internal/client/transport/xmlrpc | 6 | 21 | 0 |
| internal/metrics/wiring | 6 | 5 | 0 |
| internal/model/event | 6 | 5 | 0 |
| internal/routingkey | 6 | 0 | 0 |
| internal/health | 5 | 22 | 0 |
| internal/central/registry | 4 | 15 | 4 |
| internal/model/device | 4 | 43 | 12 |
| internal/north/ui | 4 | 13 | 0 |
| internal/client | 3 | 23 | 8 |
| internal/configstore | 3 | 17 | 0 |
| internal/north/discovery/mdns | 3 | 6 | 2 |
| internal/north/matter/secure/setup | 3 | 3 | 0 |
| internal/store/devicedetails | 3 | 2 | 0 |
| internal/boundary | 2 | 4 | 2 |
| internal/central | 2 | 18 | 2 |
| internal/central/rpcserver | 2 | 10 | 4 |
| internal/clock | 2 | 5 | 0 |
| internal/model/custom/cdpkind | 2 | 0 | 0 |
| internal/model/custom/light | 2 | 22 | 22 |
| internal/model/optimistic | 2 | 6 | 0 |
| internal/north/matter/bootid | 2 | 0 | 0 |
| internal/north/matter/secure/attestation | 2 | 2 | 16 |
| internal/north/matter/secure/sigma | 2 | 29 | 10 |
| internal/north/rest | 2 | 3 | 0 |
| pkg/hmenum | 2 | 88 | 34 |
| pkg/hmevent | 2 | 7 | 0 |
| internal/client/transport/jsonrpc | 1 | 13 | 0 |
| internal/model/custom/switch | 1 | 2 | 0 |
| internal/north/matter/bridge | 1 | 47 | 36 |
| internal/north/matter/mdns | 1 | 8 | 4 |
| internal/north/matter/schema | 1 | 0 | 8 |
| internal/north/matter/secure/spake2 | 1 | 11 | 10 |
| internal/store/masterprofile | 1 | 4 | 2 |
| internal/store/patches | 1 | 3 | 0 |
| internal/store/session | 1 | 15 | 0 |
| pkg/hmapi | 1 | 9 | 6 |
| pkg/hmui | 1 | 2 | 0 |
| pkg/interfaces | 1 | 44 | 0 |
| cmd/openccu-loom | 0 | 2 | 0 |
| internal/build | 0 | 0 | 3 |
| internal/client/observer | 0 | 3 | 0 |
| internal/configui/easymode | 0 | 4 | 0 |
| internal/configui/easymode/uc2 | 0 | 1 | 0 |
| internal/configui/easymode/uc5 | 0 | 2 | 0 |
| internal/configui/easymode/uc6 | 0 | 2 | 0 |
| internal/diagnostics | 0 | 6 | 4 |
| internal/i18n | 0 | 2 | 0 |
| internal/model/calculated | 0 | 10 | 0 |
| internal/model/combined | 0 | 8 | 0 |
| internal/model/custom/climate | 0 | 14 | 18 |
| internal/model/custom/cover | 0 | 20 | 18 |
| internal/model/custom/hood | 0 | 2 | 0 |
| internal/model/custom/lock | 0 | 14 | 14 |
| internal/model/custom/siren | 0 | 14 | 18 |
| internal/model/custom/textdisplay | 0 | 5 | 18 |
| internal/model/custom/valve | 0 | 2 | 0 |
| internal/model/datapoint | 0 | 3 | 0 |
| internal/model/value | 0 | 0 | 1 |
| internal/north/filter | 0 | 1 | 0 |
| internal/north/matter/commissioning | 0 | 6 | 9 |
| internal/north/matter/eligibility | 0 | 3 | 0 |
| internal/north/matter/endpoint | 0 | 9 | 0 |
| internal/north/matter/fabric | 0 | 2 | 2 |
| internal/north/matter/im/subscription | 0 | 5 | 4 |
| internal/north/matter/secure/aesccm | 0 | 1 | 10 |
| internal/north/matter/secure/channel | 0 | 4 | 10 |
| internal/north/matter/secure/mattercert | 0 | 7 | 16 |
| internal/north/matter/secure/operational | 0 | 3 | 4 |
| internal/north/matter/store | 0 | 17 | 9 |
| internal/north/matter/transport/message | 0 | 6 | 6 |
| internal/north/matter/transport/mrp | 0 | 8 | 2 |
| internal/north/matter/transport/udp | 0 | 5 | 4 |
| internal/north/rest/problem | 0 | 3 | 4 |
| internal/scheduler | 0 | 4 | 0 |
| internal/store/linkprofile | 0 | 1 | 1 |
