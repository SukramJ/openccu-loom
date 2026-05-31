# Dead-Code Summary

Generated: 2026-05-30T22:45:52Z
HEAD: c890b9d

## Overview

| Metric | Count |
|---|---|
| Total Exported | 22306 |
| Reachable | 3462 |
| Whitelisted | 15916 |
| **Unreachable** | **2928** |

## Top-20 Packages by Dead Code

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/central/adapter | 53 | 77 | 38 |
| internal/model/custom | 25 | 54 | 10 |
| internal/model/naming | 23 | 4 | 4 |
| pkg/hmlog | 22 | 25 | 0 |
| internal/configui | 21 | 69 | 0 |
| internal/client/backends | 19 | 25 | 4 |
| internal/north/mqtt | 18 | 94 | 2 |
| internal/store/sqlite | 14 | 55 | 18 |
| internal/central/events | 13 | 11 | 0 |
| internal/payload | 12 | 232 | 6 |
| internal/north/matter/im | 11 | 68 | 14 |
| internal/auth | 10 | 21 | 4 |
| internal/metrics | 10 | 63 | 2 |
| internal/model/hub | 10 | 33 | 16 |
| internal/client | 9 | 28 | 8 |
| internal/model/calculated | 9 | 12 | 0 |
| internal/north/matter/bridge | 9 | 52 | 36 |
| internal/north/matter/tlv | 9 | 11 | 26 |
| internal/audit | 8 | 20 | 2 |
| internal/central/coordinators | 8 | 121 | 4 |

## Top-50 Interesting Cases (kind=func, not in _test.go)

| Package | Identifier | File | Line |
|---|---|---|---|
| internal/clock | NewFake | internal/clock/clock.go | 88 |
| internal/audit | AsyncSink | internal/audit/persist.go | 291 |
| pkg/hmerr | LogBoundaryError | pkg/hmerr/boundary.go | 61 |
| pkg/hmerr | ExceptionToFailureReason | pkg/hmerr/errors.go | 396 |
| pkg/hmerr | ErrorContext | pkg/hmerr/errors.go | 153 |
| internal/auth/oidc | Verify | internal/auth/oidc/jwks.go | 129 |
| pkg/hmevent | NewBaseAt | pkg/hmevent/event.go | 45 |
| internal/central/events | Publish | internal/central/events/bus.go | 201 |
| internal/central/events | Add | internal/central/events/batch.go | 41 |
| internal/central/events | WithKey | internal/central/events/bus.go | 63 |
| internal/central/events | Subscribe | internal/central/events/bus.go | 151 |
| internal/central/events | PublishSync | internal/central/events/bus.go | 273 |
| internal/central/events | WithPriority | internal/central/events/bus.go | 56 |
| internal/model/event | NewSource | internal/model/event/event.go | 136 |
| internal/model/event | Sources | internal/model/event/event.go | 76 |
| internal/model/event | Classify | internal/model/event/event.go | 56 |
| internal/model/optimistic | New | internal/model/optimistic/tracker.go | 112 |
| internal/payload | For | internal/payload/payload.go | 36 |
| internal/payload | Merge | internal/payload/payload.go | 88 |
| internal/model/device | GenerateTranslationKey | internal/model/device/naming.go | 79 |
| internal/client/backends | DetectBackend | internal/client/backends/detection.go | 67 |
| internal/client/backends | EncodeHMLevel | internal/client/backends/combined.go | 183 |
| internal/client/backends | Factory | internal/client/backends/factory.go | 25 |
| internal/client/backends | UpdateCapabilitiesForVersion | internal/client/backends/capabilities.go | 229 |
| internal/client | SanitizeErrorMessage | internal/client/errors.go | 33 |
| internal/client/transport/xmlrpc | Format | internal/client/transport/xmlrpc/value.go | 370 |
| internal/model/hub | WrapSysvar | internal/model/hub/sysvar_subtypes.go | 301 |
| internal/health | WithConnectionClock | internal/health/connection.go | 90 |
| internal/health | NewConnection | internal/health/connection.go | 70 |
| internal/health | NewConnectionRegistry | internal/health/connection.go | 311 |
| internal/client/transport/binrpc | NewServer | internal/client/transport/binrpc/server.go | 55 |
| internal/reqctx | SetRequestContextForTesting | internal/reqctx/reqctx.go | 166 |
| internal/reqctx | IsInService | internal/reqctx/reqctx.go | 158 |
| internal/reqctx | ResetRequestContextForTesting | internal/reqctx/reqctx.go | 172 |
| internal/reqctx | TraceparentFromContext | internal/reqctx/trace.go | 123 |
| pkg/hmlog | WithContext | pkg/hmlog/contextual.go | 72 |
| pkg/hmlog | WatchSlow | pkg/hmlog/slowquery.go | 35 |
| pkg/hmlog | ForSubsystem | pkg/hmlog/factory.go | 120 |
| pkg/hmlog | Get | pkg/hmlog/contextual.go | 58 |
| pkg/hmlog | New | pkg/hmlog/contextual.go | 44 |
| internal/north/matter/tlv | CommonTag | internal/north/matter/tlv/tlv.go | 76 |
| internal/north/matter/tlv | ImplicitTag | internal/north/matter/tlv/tlv.go | 85 |
| internal/north/matter/tlv | FullyQualifiedTag | internal/north/matter/tlv/tlv.go | 95 |
| internal/configui | Generate | internal/configui/generator.go | 146 |
| internal/configui | ParameterStep | internal/configui/step.go | 21 |
| internal/configui | DetermineWidget | internal/configui/widget.go | 62 |
| internal/configui | ClassifyLinkParameter | internal/configui/link_param_metadata.go | 181 |
| internal/configui | Humanize | internal/configui/labels.go | 118 |
| internal/configui | GetTimePresets | internal/configui/link_param_metadata.go | 272 |
| internal/configui | EncodeTimeValue | internal/configui/link_param_metadata.go | 308 |

## Full By-Package Breakdown

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/central/adapter | 53 | 77 | 38 |
| internal/model/custom | 25 | 54 | 10 |
| internal/model/naming | 23 | 4 | 4 |
| pkg/hmlog | 22 | 25 | 0 |
| internal/configui | 21 | 69 | 0 |
| internal/client/backends | 19 | 25 | 4 |
| internal/north/mqtt | 18 | 94 | 2 |
| internal/store/sqlite | 14 | 55 | 18 |
| internal/central/events | 13 | 11 | 0 |
| internal/payload | 12 | 232 | 6 |
| internal/north/matter/im | 11 | 68 | 14 |
| internal/auth | 10 | 21 | 4 |
| internal/metrics | 10 | 63 | 2 |
| internal/model/hub | 10 | 33 | 16 |
| internal/client | 9 | 28 | 8 |
| internal/model/calculated | 9 | 12 | 0 |
| internal/north/matter/bridge | 9 | 52 | 36 |
| internal/north/matter/tlv | 9 | 11 | 26 |
| internal/audit | 8 | 20 | 2 |
| internal/central/coordinators | 8 | 121 | 4 |
| internal/client/transport/binrpc | 8 | 11 | 0 |
| internal/health | 8 | 25 | 0 |
| internal/model/event | 8 | 5 | 0 |
| pkg/hmerr | 8 | 7 | 22 |
| internal/north/mqtt/protocol | 7 | 13 | 0 |
| internal/auth/oidc | 6 | 17 | 2 |
| internal/ccudata | 6 | 31 | 4 |
| internal/client/transport/xmlrpc | 6 | 21 | 0 |
| internal/metrics/wiring | 6 | 5 | 0 |
| internal/model/device | 6 | 45 | 12 |
| internal/model/value | 6 | 0 | 1 |
| internal/north/matter/secure/spake2 | 6 | 14 | 10 |
| internal/central/registry | 4 | 15 | 4 |
| internal/north/matter/commissioning | 4 | 7 | 9 |
| internal/north/ui | 4 | 13 | 0 |
| internal/reqctx | 4 | 0 | 0 |
| internal/central | 3 | 19 | 2 |
| internal/clock | 3 | 5 | 0 |
| internal/configstore | 3 | 17 | 0 |
| internal/north/discovery/mdns | 3 | 6 | 2 |
| internal/north/matter/mdns | 3 | 12 | 4 |
| internal/north/matter/schema | 3 | 0 | 8 |
| internal/north/matter/secure/channel | 3 | 5 | 10 |
| internal/north/matter/secure/setup | 3 | 3 | 0 |
| internal/store/devicedetails | 3 | 2 | 0 |
| pkg/hmevent | 3 | 57 | 0 |
| pkg/hmproperty | 3 | 6 | 1 |
| pkg/interfaces | 3 | 46 | 0 |
| internal/boundary | 2 | 4 | 2 |
| internal/central/rpcserver | 2 | 10 | 4 |
| internal/model/custom/cdpkind | 2 | 0 | 0 |
| internal/model/custom/light | 2 | 22 | 22 |
| internal/model/optimistic | 2 | 6 | 0 |
| internal/north/matter/bootid | 2 | 0 | 0 |
| internal/north/matter/endpoint | 2 | 13 | 0 |
| internal/north/matter/secure/attestation | 2 | 2 | 16 |
| internal/north/matter/secure/mattercert | 2 | 11 | 16 |
| internal/north/matter/secure/sigma | 2 | 32 | 10 |
| internal/north/rest | 2 | 3 | 0 |
| pkg/hmapi | 2 | 10 | 6 |
| pkg/hmenum | 2 | 94 | 34 |
| internal/client/transport/jsonrpc | 1 | 13 | 0 |
| internal/configui/easymode | 1 | 5 | 0 |
| internal/configui/easymode/crossvalidation | 1 | 1 | 0 |
| internal/configui/easymode/uc2 | 1 | 2 | 0 |
| internal/configui/easymode/uc5 | 1 | 3 | 0 |
| internal/configui/easymode/uc6 | 1 | 3 | 0 |
| internal/i18n | 1 | 3 | 0 |
| internal/model/custom/cover | 1 | 20 | 18 |
| internal/model/custom/switch | 1 | 2 | 0 |
| internal/north/matter/parity | 1 | 0 | 0 |
| internal/north/matter/store | 1 | 17 | 9 |
| internal/north/matter/transport/mrp | 1 | 11 | 2 |
| internal/scheduler | 1 | 5 | 0 |
| internal/store/masterprofile | 1 | 4 | 2 |
| internal/store/patches | 1 | 3 | 0 |
| internal/store/session | 1 | 15 | 0 |
| pkg/hmui | 1 | 2 | 0 |
| cmd/openccu-loom | 0 | 2 | 0 |
| internal/build | 0 | 0 | 3 |
| internal/client/observer | 0 | 3 | 0 |
| internal/diagnostics | 0 | 6 | 4 |
| internal/model/combined | 0 | 9 | 0 |
| internal/model/custom/climate | 0 | 14 | 18 |
| internal/model/custom/hood | 0 | 2 | 0 |
| internal/model/custom/lock | 0 | 14 | 14 |
| internal/model/custom/siren | 0 | 14 | 18 |
| internal/model/custom/textdisplay | 0 | 5 | 18 |
| internal/model/custom/valve | 0 | 2 | 0 |
| internal/model/datapoint | 0 | 3 | 0 |
| internal/north/filter | 0 | 1 | 0 |
| internal/north/matter/eligibility | 0 | 3 | 0 |
| internal/north/matter/fabric | 0 | 2 | 2 |
| internal/north/matter/im/subscription | 0 | 5 | 4 |
| internal/north/matter/secure/aesccm | 0 | 1 | 10 |
| internal/north/matter/secure/operational | 0 | 4 | 4 |
| internal/north/matter/transport/message | 0 | 6 | 6 |
| internal/north/matter/transport/udp | 0 | 5 | 4 |
| internal/north/rest/problem | 0 | 3 | 4 |
| internal/store/linkprofile | 0 | 1 | 1 |
