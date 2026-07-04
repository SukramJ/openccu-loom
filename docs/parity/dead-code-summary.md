# Dead-Code Summary

Generated: eec4550
HEAD: eec4550

## Overview

| Metric | Count |
|---|---|
| Total Exported | 24002 |
| Reachable | 3788 |
| Whitelisted | 17337 |
| **Unreachable** | **2877** |

## Top-20 Packages by Dead Code

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/central/adapter | 66 | 88 | 46 |
| internal/model/naming | 25 | 2 | 4 |
| internal/model/custom | 23 | 49 | 10 |
| internal/north/mqtt | 21 | 76 | 4 |
| internal/client/backends | 19 | 37 | 4 |
| internal/store/sqlite | 19 | 60 | 22 |
| internal/auth | 14 | 24 | 4 |
| internal/metrics | 12 | 60 | 2 |
| internal/payload | 12 | 232 | 6 |
| pkg/hmlog | 12 | 18 | 0 |
| internal/central/events | 11 | 9 | 0 |
| internal/north/matter/im | 11 | 74 | 14 |
| internal/model/hub | 10 | 41 | 30 |
| internal/audit | 8 | 22 | 2 |
| internal/central/coordinators | 8 | 119 | 4 |
| internal/client/transport/binrpc | 8 | 11 | 0 |
| internal/configstore | 8 | 17 | 0 |
| internal/north/matter/tlv | 8 | 11 | 26 |
| internal/metrics/wiring | 7 | 7 | 0 |
| internal/routingkey | 7 | 0 | 2 |

## Top-50 Interesting Cases (kind=func, not in _test.go)

| Package | Identifier | File | Line |
|---|---|---|---|
| internal/audit | AsyncSink | internal/audit/persist.go | 291 |
| internal/audit | AsyncSink | internal/audit/persist.go | 291 |
| internal/audit | BuildChangeDiff | internal/audit/change_log.go | 177 |
| internal/audit | NewBuffer | internal/audit/audit.go | 140 |
| internal/audit | NewChangeLog | internal/audit/change_log.go | 53 |
| internal/audit | NewDurableSink | internal/audit/persist.go | 167 |
| internal/audit | NewPersistedRecorder | internal/audit/persist.go | 40 |
| internal/audit | NoopRecorder | internal/audit/audit.go | 197 |
| internal/auth | CSRFMiddleware | internal/auth/csrf.go | 52 |
| internal/auth | CSRFToken | internal/auth/csrf.go | 28 |
| internal/auth | CSRFToken | internal/auth/csrf.go | 28 |
| internal/auth | ClearSessionCookie | internal/auth/session.go | 268 |
| internal/auth | HashPassword | internal/auth/auth.go | 244 |
| internal/auth | IdentityFrom | internal/auth/middleware.go | 104 |
| internal/auth | IngressPassthrough | internal/auth/ingress.go | 52 |
| internal/auth | NewMemoryTokenStore | internal/auth/auth.go | 86 |
| internal/auth | NewMemoryUserStore | internal/auth/auth.go | 208 |
| internal/auth | NewMiddleware | internal/auth/middleware.go | 29 |
| internal/auth | NewPersistentSessionStore | internal/auth/session.go | 82 |
| internal/auth | NewSessionStore | internal/auth/session.go | 72 |
| internal/auth | SessionMiddleware | internal/auth/session.go | 232 |
| internal/auth | WriteSessionCookie | internal/auth/session.go | 253 |
| internal/auth/oidc | Discover | internal/auth/oidc/discovery.go | 27 |
| internal/auth/oidc | New | internal/auth/oidc/client.go | 41 |
| internal/auth/oidc | NewPKCEPair | internal/auth/oidc/pkce.go | 23 |
| internal/auth/oidc | Verify | internal/auth/oidc/jwks.go | 129 |
| internal/build | IsAddon | internal/build/version.go | 28 |
| internal/ccudata | Empty | internal/ccudata/translations.go | 79 |
| internal/ccudata | EmptyEasymode | internal/ccudata/easymode.go | 237 |
| internal/ccudata | LoadEasymodeEmbedded | internal/ccudata/embed.go | 65 |
| internal/ccudata | LoadProfilesEmbedded | internal/ccudata/profiles.go | 109 |
| internal/ccudata | LoadTranslations | internal/ccudata/translations.go | 52 |
| internal/ccudata | LoadTranslationsEmbedded | internal/ccudata/embed.go | 48 |
| internal/central | NewRegistry | internal/central/central_registry.go | 26 |
| internal/central | RegisterStandardJobs | internal/central/jobs.go | 300 |
| internal/central/adapter | BridgeCombinedDataPoint | internal/central/adapter/combined_bridge.go | 51 |
| internal/central/adapter | ClassifyLinkParameter | internal/central/adapter/link_param_metadata.go | 184 |
| internal/central/adapter | DecodeTimeValue | internal/central/adapter/link_param_metadata.go | 294 |
| internal/central/adapter | DecodeTimeValue | internal/central/adapter/link_param_metadata.go | 294 |
| internal/central/adapter | EncodeTimeValue | internal/central/adapter/link_param_metadata.go | 305 |
| internal/central/adapter | EncodeTimeValue | internal/central/adapter/link_param_metadata.go | 305 |
| internal/central/adapter | GetTimePresets | internal/central/adapter/link_param_metadata.go | 276 |
| internal/central/adapter | InitInterfaceID | internal/central/adapter/interface_id.go | 54 |
| internal/central/adapter | NewBackupAdapter | internal/central/adapter/stubs.go | 55 |
| internal/central/adapter | NewCCUAuthDomain | internal/central/adapter/ccu_auth.go | 51 |
| internal/central/adapter | NewCallbackHandlers | internal/central/adapter/callback_handlers.go | 79 |
| internal/central/adapter | NewCentralLinksDomain | internal/central/adapter/central_links.go | 32 |
| internal/central/adapter | NewConfigAdapter | internal/central/adapter/config.go | 26 |
| internal/central/adapter | NewCustomDPDispatcher | internal/central/adapter/custom_dp_dispatcher.go | 60 |
| internal/central/adapter | NewDataPointWriterAdapter | internal/central/adapter/devices.go | 140 |

## Full By-Package Breakdown

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/central/adapter | 66 | 88 | 46 |
| internal/model/naming | 25 | 2 | 4 |
| internal/model/custom | 23 | 49 | 10 |
| internal/north/mqtt | 21 | 76 | 4 |
| internal/client/backends | 19 | 37 | 4 |
| internal/store/sqlite | 19 | 60 | 22 |
| internal/auth | 14 | 24 | 4 |
| internal/metrics | 12 | 60 | 2 |
| internal/payload | 12 | 232 | 6 |
| pkg/hmlog | 12 | 18 | 0 |
| internal/central/events | 11 | 9 | 0 |
| internal/north/matter/im | 11 | 74 | 14 |
| internal/model/hub | 10 | 41 | 30 |
| internal/audit | 8 | 22 | 2 |
| internal/central/coordinators | 8 | 119 | 4 |
| internal/client/transport/binrpc | 8 | 11 | 0 |
| internal/configstore | 8 | 17 | 0 |
| internal/north/matter/tlv | 8 | 11 | 26 |
| internal/metrics/wiring | 7 | 7 | 0 |
| internal/routingkey | 7 | 0 | 2 |
| pkg/hmerr | 7 | 5 | 24 |
| internal/ccudata | 6 | 31 | 4 |
| internal/client/transport/xmlrpc | 6 | 21 | 0 |
| internal/model/device | 6 | 43 | 12 |
| internal/model/event | 6 | 5 | 0 |
| internal/north/webhook | 5 | 3 | 0 |
| internal/auth/oidc | 4 | 17 | 2 |
| internal/central/registry | 4 | 15 | 0 |
| internal/configui | 4 | 16 | 0 |
| internal/north/rest | 4 | 4 | 0 |
| internal/client | 3 | 26 | 8 |
| internal/north/discovery/mdns | 3 | 6 | 2 |
| internal/north/matter/secure/setup | 3 | 3 | 0 |
| internal/store/devicedetails | 3 | 2 | 0 |
| pkg/hmenum | 3 | 92 | 36 |
| internal/central | 2 | 20 | 2 |
| internal/central/rpcserver | 2 | 10 | 4 |
| internal/clock | 2 | 5 | 0 |
| internal/health | 2 | 16 | 0 |
| internal/history | 2 | 12 | 0 |
| internal/model/custom/cdpkind | 2 | 0 | 0 |
| internal/model/custom/light | 2 | 20 | 22 |
| internal/model/device/definitionexport | 2 | 6 | 2 |
| internal/model/optimistic | 2 | 6 | 0 |
| internal/north/matter/bootid | 2 | 0 | 0 |
| internal/north/matter/schema | 2 | 0 | 8 |
| internal/north/matter/secure/attestation | 2 | 2 | 16 |
| internal/north/matter/secure/sigma | 2 | 33 | 12 |
| internal/north/ui | 2 | 2 | 0 |
| internal/orderedjson | 2 | 5 | 0 |
| pkg/hmevent | 2 | 5 | 0 |
| internal/build | 1 | 0 | 8 |
| internal/central/cachereset | 1 | 20 | 0 |
| internal/client/transport/jsonrpc | 1 | 11 | 0 |
| internal/model/custom/switch | 1 | 0 | 0 |
| internal/north/bridge | 1 | 7 | 0 |
| internal/north/discovery | 1 | 1 | 0 |
| internal/north/discovery/ssdp | 1 | 3 | 0 |
| internal/north/matter/bridge | 1 | 51 | 38 |
| internal/north/matter/mdns | 1 | 8 | 4 |
| internal/north/matter/secure/spake2 | 1 | 11 | 10 |
| internal/store/masterprofile | 1 | 4 | 2 |
| internal/store/patches | 1 | 3 | 0 |
| internal/store/session | 1 | 15 | 0 |
| pkg/hmapi | 1 | 77 | 14 |
| pkg/hmui | 1 | 2 | 0 |
| pkg/interfaces | 1 | 78 | 0 |
| cmd/openccu-loom | 0 | 2 | 0 |
| internal/auth/ccuauth | 0 | 2 | 0 |
| internal/client/observer | 0 | 3 | 0 |
| internal/diagnostics | 0 | 6 | 4 |
| internal/i18n | 0 | 2 | 0 |
| internal/model/calculated | 0 | 10 | 0 |
| internal/model/combined | 0 | 8 | 0 |
| internal/model/custom/climate | 0 | 8 | 18 |
| internal/model/custom/cover | 0 | 18 | 18 |
| internal/model/custom/lock | 0 | 12 | 14 |
| internal/model/custom/siren | 0 | 12 | 18 |
| internal/model/custom/textdisplay | 0 | 5 | 18 |
| internal/model/datapoint | 0 | 3 | 0 |
| internal/model/value | 0 | 0 | 1 |
| internal/north/filter | 0 | 1 | 0 |
| internal/north/matter/commissioning | 0 | 6 | 9 |
| internal/north/matter/eligibility | 0 | 1 | 0 |
| internal/north/matter/endpoint | 0 | 9 | 0 |
| internal/north/matter/im/subscription | 0 | 5 | 4 |
| internal/north/matter/secure/aesccm | 0 | 1 | 10 |
| internal/north/matter/secure/channel | 0 | 4 | 10 |
| internal/north/matter/secure/mattercert | 0 | 7 | 16 |
| internal/north/matter/secure/operational | 0 | 2 | 4 |
| internal/north/matter/store | 0 | 17 | 9 |
| internal/north/matter/transport/message | 0 | 6 | 12 |
| internal/north/matter/transport/mrp | 0 | 10 | 4 |
| internal/north/matter/transport/udp | 0 | 5 | 4 |
| internal/north/mcp | 0 | 8 | 0 |
| internal/north/rest/problem | 0 | 3 | 4 |
| internal/restapi | 0 | 5 | 0 |
| internal/scheduler | 0 | 4 | 0 |
| internal/store/linkprofile | 0 | 1 | 1 |
