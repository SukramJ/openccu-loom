# Dead-Code Summary

Generated: e358e833
HEAD: e358e833

## Overview

| Metric | Count |
|---|---|
| Total Exported | 19739 |
| Reachable | 3729 |
| Whitelisted | 12800 |
| **Unreachable** | **3210** |

## Top-20 Packages by Dead Code

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/north/mqtt | 44 | 90 | 4 |
| internal/model/custom | 40 | 54 | 10 |
| internal/store/sqlite | 38 | 100 | 22 |
| internal/model/naming | 28 | 2 | 4 |
| internal/auth | 26 | 32 | 4 |
| internal/metrics | 26 | 80 | 2 |
| internal/client/backends | 22 | 41 | 4 |
| internal/ccudata | 16 | 34 | 4 |
| internal/client/transport/binrpc | 14 | 12 | 0 |
| internal/configstore | 14 | 18 | 0 |
| internal/metrics/wiring | 14 | 14 | 0 |
| internal/payload | 14 | 232 | 6 |
| pkg/hmlog | 14 | 22 | 0 |
| internal/audit | 12 | 25 | 2 |
| internal/central/events | 10 | 9 | 0 |
| internal/client/transport/xmlrpc | 10 | 34 | 0 |
| internal/model/alarmpanel | 10 | 2 | 0 |
| internal/auth/oidc | 8 | 20 | 2 |
| internal/configui | 8 | 19 | 0 |
| internal/model/device | 8 | 43 | 12 |

## Top-50 Interesting Cases (kind=func, not in _test.go)

| Package | Identifier | File | Line |
|---|---|---|---|
| internal/alarm | DeviceBackedOutputClass | internal/alarm/candidates.go | 68 |
| internal/alarm | DeviceBackedOutputClass | internal/alarm/candidates.go | 68 |
| internal/alarm | NewService | internal/alarm/service.go | 101 |
| internal/alarm | NewService | internal/alarm/service.go | 101 |
| internal/alarm | NewStores | internal/alarm/stores.go | 26 |
| internal/alarm | NewStores | internal/alarm/stores.go | 26 |
| internal/alarm/codes | HashPIN | internal/alarm/codes/pin.go | 37 |
| internal/alarm/codes | HashPIN | internal/alarm/codes/pin.go | 37 |
| internal/alarm/codes | New | internal/alarm/codes/facade.go | 106 |
| internal/alarm/codes | New | internal/alarm/codes/facade.go | 106 |
| internal/alarm/codes | VerifyPIN | internal/alarm/codes/pin.go | 55 |
| internal/alarm/codes | VerifyPIN | internal/alarm/codes/pin.go | 55 |
| internal/alarm/outputs | NewManager | internal/alarm/outputs/manager.go | 85 |
| internal/alarm/outputs | NewManager | internal/alarm/outputs/manager.go | 85 |
| internal/alarm/outputs | ParseOutputConfig | internal/alarm/outputs/config.go | 102 |
| internal/alarm/outputs | ParseOutputConfig | internal/alarm/outputs/config.go | 102 |
| internal/audit | AsyncSink | internal/audit/persist.go | 291 |
| internal/audit | AsyncSink | internal/audit/persist.go | 291 |
| internal/audit | BuildChangeDiff | internal/audit/change_log.go | 177 |
| internal/audit | BuildChangeDiff | internal/audit/change_log.go | 177 |
| internal/audit | NewChangeLog | internal/audit/change_log.go | 53 |
| internal/audit | NewChangeLog | internal/audit/change_log.go | 53 |
| internal/audit | NewDurableSink | internal/audit/persist.go | 167 |
| internal/audit | NewDurableSink | internal/audit/persist.go | 167 |
| internal/audit | NewPersistedRecorder | internal/audit/persist.go | 40 |
| internal/audit | NewPersistedRecorder | internal/audit/persist.go | 40 |
| internal/audit | NoopRecorder | internal/audit/audit.go | 224 |
| internal/audit | NoopRecorder | internal/audit/audit.go | 224 |
| internal/auth | CSRFMiddleware | internal/auth/csrf.go | 52 |
| internal/auth | CSRFMiddleware | internal/auth/csrf.go | 52 |
| internal/auth | CSRFToken | internal/auth/csrf.go | 28 |
| internal/auth | CSRFToken | internal/auth/csrf.go | 28 |
| internal/auth | ClearSessionCookie | internal/auth/session.go | 268 |
| internal/auth | ClearSessionCookie | internal/auth/session.go | 268 |
| internal/auth | HashPassword | internal/auth/auth.go | 242 |
| internal/auth | HashPassword | internal/auth/auth.go | 242 |
| internal/auth | IdentityFrom | internal/auth/middleware.go | 104 |
| internal/auth | IdentityFrom | internal/auth/middleware.go | 104 |
| internal/auth | IngressPassthrough | internal/auth/ingress.go | 52 |
| internal/auth | IngressPassthrough | internal/auth/ingress.go | 52 |
| internal/auth | NewMemoryTokenStore | internal/auth/auth.go | 88 |
| internal/auth | NewMemoryTokenStore | internal/auth/auth.go | 88 |
| internal/auth | NewMemoryUserStore | internal/auth/auth.go | 206 |
| internal/auth | NewMemoryUserStore | internal/auth/auth.go | 206 |
| internal/auth | NewMiddleware | internal/auth/middleware.go | 29 |
| internal/auth | NewMiddleware | internal/auth/middleware.go | 29 |
| internal/auth | NewPersistentSessionStore | internal/auth/session.go | 82 |
| internal/auth | NewPersistentSessionStore | internal/auth/session.go | 82 |
| internal/auth | NewSessionStore | internal/auth/session.go | 72 |
| internal/auth | NewSessionStore | internal/auth/session.go | 72 |

## Full By-Package Breakdown

| Package | Funcs | Types | Other |
|---|---|---|---|
| internal/north/mqtt | 44 | 90 | 4 |
| internal/model/custom | 40 | 54 | 10 |
| internal/store/sqlite | 38 | 100 | 22 |
| internal/model/naming | 28 | 2 | 4 |
| internal/auth | 26 | 32 | 4 |
| internal/metrics | 26 | 80 | 2 |
| internal/client/backends | 22 | 41 | 4 |
| internal/ccudata | 16 | 34 | 4 |
| internal/client/transport/binrpc | 14 | 12 | 0 |
| internal/configstore | 14 | 18 | 0 |
| internal/metrics/wiring | 14 | 14 | 0 |
| internal/payload | 14 | 232 | 6 |
| pkg/hmlog | 14 | 22 | 0 |
| internal/audit | 12 | 25 | 2 |
| internal/central/events | 10 | 9 | 0 |
| internal/client/transport/xmlrpc | 10 | 34 | 0 |
| internal/model/alarmpanel | 10 | 2 | 0 |
| internal/auth/oidc | 8 | 20 | 2 |
| internal/configui | 8 | 19 | 0 |
| internal/model/device | 8 | 43 | 12 |
| pkg/hmenum | 8 | 114 | 36 |
| internal/alarm | 6 | 24 | 0 |
| internal/alarm/codes | 6 | 14 | 0 |
| internal/central | 6 | 25 | 2 |
| internal/north/webhook | 6 | 4 | 0 |
| internal/alarm/outputs | 4 | 26 | 4 |
| internal/central/rpcserver | 4 | 12 | 4 |
| internal/health | 4 | 16 | 0 |
| internal/history | 4 | 14 | 0 |
| internal/model/custom/cdpkind | 4 | 0 | 0 |
| internal/model/custom/light | 4 | 20 | 22 |
| internal/model/device/definitionexport | 4 | 6 | 2 |
| internal/north/discovery/mdns | 4 | 8 | 2 |
| internal/north/matter/secure/attestation | 4 | 2 | 16 |
| internal/north/matter/tlv | 4 | 11 | 26 |
| internal/orderedjson | 4 | 6 | 0 |
| internal/routingkey | 4 | 0 | 2 |
| internal/store/devicedetails | 4 | 4 | 0 |
| pkg/hmerr | 4 | 5 | 26 |
| internal/build | 2 | 0 | 8 |
| internal/central/cachereset | 2 | 21 | 0 |
| internal/client | 2 | 30 | 8 |
| internal/client/transport/jsonrpc | 2 | 11 | 0 |
| internal/model/custom/switch | 2 | 0 | 0 |
| internal/model/event | 2 | 5 | 0 |
| internal/model/hub | 2 | 49 | 32 |
| internal/model/optimistic | 2 | 6 | 0 |
| internal/north/bridge | 2 | 8 | 0 |
| internal/north/discovery | 2 | 2 | 0 |
| internal/north/discovery/ssdp | 2 | 4 | 0 |
| internal/north/matter/bootid | 2 | 0 | 0 |
| internal/north/matter/bridge | 2 | 61 | 38 |
| internal/north/matter/mdns | 2 | 10 | 4 |
| internal/north/matter/secure/setup | 2 | 3 | 0 |
| internal/north/matter/secure/sigma | 2 | 34 | 12 |
| internal/store/masterprofile | 2 | 6 | 2 |
| pkg/hmui | 2 | 2 | 0 |
| pkg/interfaces | 2 | 81 | 2 |
| internal/north/mcp | 1 | 8 | 0 |
| internal/alarm/engine | 0 | 25 | 6 |
| internal/alarm/journal | 0 | 1 | 0 |
| internal/auth/ccuauth | 0 | 2 | 0 |
| internal/central/coordinators | 0 | 127 | 4 |
| internal/central/registry | 0 | 17 | 0 |
| internal/client/observer | 0 | 3 | 0 |
| internal/clock | 0 | 5 | 0 |
| internal/diagnostics | 0 | 6 | 4 |
| internal/i18n | 0 | 2 | 0 |
| internal/model/calculated | 0 | 12 | 0 |
| internal/model/combined | 0 | 9 | 0 |
| internal/model/custom/climate | 0 | 8 | 18 |
| internal/model/custom/cover | 0 | 18 | 18 |
| internal/model/custom/lock | 0 | 12 | 14 |
| internal/model/custom/siren | 0 | 12 | 18 |
| internal/model/custom/textdisplay | 0 | 6 | 18 |
| internal/model/datapoint | 0 | 3 | 0 |
| internal/model/value | 0 | 0 | 1 |
| internal/north/filter | 0 | 1 | 0 |
| internal/north/matter/commissioning | 0 | 6 | 9 |
| internal/north/matter/endpoint | 0 | 9 | 0 |
| internal/north/matter/im | 0 | 74 | 14 |
| internal/north/matter/im/subscription | 0 | 5 | 4 |
| internal/north/matter/schema | 0 | 0 | 8 |
| internal/north/matter/secure/aesccm | 0 | 1 | 10 |
| internal/north/matter/secure/channel | 0 | 4 | 10 |
| internal/north/matter/secure/mattercert | 0 | 7 | 16 |
| internal/north/matter/secure/operational | 0 | 4 | 4 |
| internal/north/matter/secure/spake2 | 0 | 12 | 10 |
| internal/north/matter/store | 0 | 35 | 18 |
| internal/north/matter/transport/message | 0 | 6 | 12 |
| internal/north/matter/transport/mrp | 0 | 10 | 4 |
| internal/north/matter/transport/udp | 0 | 5 | 4 |
| internal/north/rest/problem | 0 | 3 | 4 |
| internal/restapi | 0 | 5 | 0 |
| internal/scheduler | 0 | 5 | 0 |
| internal/store/linkprofile | 0 | 1 | 1 |
| internal/store/patches | 0 | 4 | 0 |
| internal/store/session | 0 | 16 | 0 |
| pkg/hmapi | 0 | 117 | 14 |
| pkg/hmevent | 0 | 7 | 0 |
