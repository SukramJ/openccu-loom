# Dead-Code Summary

Generated: ddabb47
HEAD: ddabb47

## Overview

| Metric | Count |
|---|---|
| Total Exported | 22185 |
| Reachable | 3690 |
| Whitelisted | 15813 |
| **Unreachable** | **2682** |

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
| internal/audit | AsyncSink | internal/audit/persist.go | 291 |
| internal/audit | BuildChangeDiff | internal/audit/change_log.go | 177 |
| internal/audit | NewBuffer | internal/audit/audit.go | 114 |
| internal/audit | NewChangeLog | internal/audit/change_log.go | 53 |
| internal/audit | NewDurableSink | internal/audit/persist.go | 167 |
| internal/audit | NewPersistedRecorder | internal/audit/persist.go | 40 |
| internal/audit | NoopRecorder | internal/audit/audit.go | 171 |
| internal/auth | CSRFMiddleware | internal/auth/csrf.go | 51 |
| internal/auth | CSRFToken | internal/auth/csrf.go | 27 |
| internal/auth | ClearSessionCookie | internal/auth/session.go | 119 |
| internal/auth | IdentityFrom | internal/auth/middleware.go | 104 |
| internal/auth | NewMemoryTokenStore | internal/auth/auth.go | 72 |
| internal/auth | NewMemoryUserStore | internal/auth/auth.go | 181 |
| internal/auth | NewMiddleware | internal/auth/middleware.go | 29 |
| internal/auth | NewSessionStore | internal/auth/session.go | 39 |
| internal/auth | SessionMiddleware | internal/auth/session.go | 83 |
| internal/auth | WriteSessionCookie | internal/auth/session.go | 104 |
| internal/auth/oidc | DecodeIDToken | internal/auth/oidc/client.go | 135 |
| internal/auth/oidc | Discover | internal/auth/oidc/discovery.go | 27 |
| internal/auth/oidc | New | internal/auth/oidc/client.go | 40 |
| internal/auth/oidc | NewPKCEPair | internal/auth/oidc/pkce.go | 23 |
| internal/auth/oidc | Verify | internal/auth/oidc/jwks.go | 129 |
| internal/auth/oidc | Verify | internal/auth/oidc/jwks.go | 129 |
| internal/ccudata | Empty | internal/ccudata/translations.go | 79 |
| internal/ccudata | EmptyEasymode | internal/ccudata/easymode.go | 237 |
| internal/ccudata | LoadEasymodeEmbedded | internal/ccudata/embed.go | 65 |
| internal/ccudata | LoadProfilesEmbedded | internal/ccudata/profiles.go | 109 |
| internal/ccudata | LoadTranslations | internal/ccudata/translations.go | 52 |
| internal/ccudata | LoadTranslationsEmbedded | internal/ccudata/embed.go | 48 |
| internal/central | NewRegistry | internal/central/central_registry.go | 26 |
| internal/central | RegisterStandardJobs | internal/central/jobs.go | 293 |
| internal/central/adapter | BridgeCombinedDataPoint | internal/central/adapter/combined_bridge.go | 51 |
| internal/central/adapter | BridgeCombinedDataPoint | internal/central/adapter/combined_bridge.go | 51 |
| internal/central/adapter | ClassifyLinkParameter | internal/central/adapter/link_param_metadata.go | 184 |
| internal/central/adapter | DecodeTimeValue | internal/central/adapter/link_param_metadata.go | 294 |
| internal/central/adapter | DecodeTimeValue | internal/central/adapter/link_param_metadata.go | 294 |
| internal/central/adapter | EncodeTimeValue | internal/central/adapter/link_param_metadata.go | 305 |
| internal/central/adapter | EncodeTimeValue | internal/central/adapter/link_param_metadata.go | 305 |
| internal/central/adapter | GetTimePresets | internal/central/adapter/link_param_metadata.go | 276 |
| internal/central/adapter | InitInterfaceID | internal/central/adapter/interface_id.go | 54 |
| internal/central/adapter | NewBackupAdapter | internal/central/adapter/stubs.go | 85 |
| internal/central/adapter | NewCallbackHandlers | internal/central/adapter/callback_handlers.go | 58 |
| internal/central/adapter | NewCentralLinksDomain | internal/central/adapter/central_links.go | 32 |
| internal/central/adapter | NewConfigAdapter | internal/central/adapter/config.go | 25 |
| internal/central/adapter | NewCustomDPDispatcher | internal/central/adapter/custom_dp_dispatcher.go | 57 |
| internal/central/adapter | NewDataPointWriterAdapter | internal/central/adapter/devices.go | 130 |
| internal/central/adapter | NewDeviceAdminDomain | internal/central/adapter/device_admin.go | 27 |
| internal/central/adapter | NewDevicePipeline | internal/central/adapter/device_pipeline.go | 122 |
| internal/central/adapter | NewDeviceReloaderAdapter | internal/central/adapter/device_reloader.go | 41 |

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
