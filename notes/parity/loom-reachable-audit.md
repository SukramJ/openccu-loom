# loom:reachable Whitelist Audit

Generated: 2026-07-10T16:33:14Z

Total annotated items: 29 — PRODUCTIVE: 21 — MASKED: 8

## MASKED — Annotation hides genuine dead code (8 items)

These items have zero production call sites (cross-file, or same-file
outside the symbol's own declaration).
**Action required:** either wire them into a real production call site, or
document them as intentional dead code in `notes/parity/by_design.md`.
A `loom:reachable` annotation alone is not sufficient justification.

| Identifier | File | Line | Reason |
|---|---|---|---|
| `WithName` | `internal/central/events/bus.go` | 75 | sole setter for the handler name surfaced by Bus.HandlerStats; retained as a diagnostics seam — test-only callers today |
| `WithExternal` | `internal/central/events/bus.go` | 86 | sole setter for the external flag consumed by Bus.ClearExternalSubscriptions during central teardown; no production subscriber passes it yet |
| `NewCcuBackend` | `internal/client/backends/ccu.go` | 73 | test convenience constructor without interface-type dispatch; production wiring always uses NewCcuBackendForInterface |
| `WithHistorySize` | `internal/health/tracker.go` | 124 | functional option retained as a deliberate NewTracker config seam; test-only callers today |
| `WithStaleAfter` | `internal/health/tracker.go` | 138 | functional option retained as a deliberate NewTracker config seam; test-only callers today |
| `NewGroup` | `internal/model/event/group.go` | 48 | convenience wrapper for tests; production uses NewGroupWithCentral |
| `ClusterName` | `internal/north/matter/schema/lookup.go` | 20 | introspection companion to the generated cluster tables; retained for diagnostics — no production caller yet |
| `DeviceTypeName` | `internal/north/matter/schema/lookup.go` | 44 | introspection companion to the generated device-type tables; retained for diagnostics — no production caller yet |

## PRODUCTIVE — Has production callers (21 items)

These items are genuinely reachable from production code.

| Identifier | File | Line | Callers | Reason |
|---|---|---|---|---|
| `ContextWithIdentity` | `internal/auth/middleware.go` | 115 | internal/north/rest/ws/client.go, internal/auth/ingress.go | used by WS upgrade handler and integration tests to inject pre-resolved identity |
| `NewNonce` | `internal/auth/oidc/client.go` | 76 | internal/north/rest/handlers/oidc.go | minted by the REST OIDC start handler; the OIDC surface is wired conditionally so the production callgraph does not reach it |
| `NewJWKSCache` | `internal/auth/oidc/jwks.go` | 61 | internal/auth/oidc/client.go | used by OIDC auth middleware to verify JWT signatures against the provider's keyset |
| `LoadEasymode` | `internal/ccudata/easymode.go` | 182 | cmd/openccu-loom/daemon_ccudata.go | called by ccudata loader during daemon boot to populate easymode registry |
| `SafeGo` | `internal/central/adapter/safego.go` | 25 | internal/central/adapter/central_bringup.go, internal/central/adapter/eventbridge.go, internal/central/adapter/auto_refresh.go | utility wrapper for panic-safe goroutines; callers in adapter background tasks are added incrementally |
| `NewConnectionRecoveryCoordinatorWithLimit` | `internal/central/coordinators/connection_recovery.go` | 263 | internal/central/coordinators/connection_recovery.go (same file) | called by NewConnectionRecoveryCoordinator (the standard production entry point); also used directly in integration tests that need a bounded attempt count |
| `DefaultRecoveryPipeline` | `internal/central/coordinators/recovery_stages.go` | 91 | internal/central/adapter/ccu_wiring.go | called by NewConnectionRecoveryCoordinator to assemble the production pipeline |
| `NewQueryFacade` | `internal/central/queryfacade.go` | 68 | internal/central/central.go | used by REST/WS handler wiring in daemon.go to expose the per-central query surface |
| `NewParamsetRegistry` | `internal/central/registry/paramset.go` | 73 | internal/central/registry/paramset.go (same file) | called by NewParamsetRegistryWithPatches and directly in tests; production always uses the WithPatches variant |
| `NewBINRPCServer` | `internal/central/rpcserver/binrpc_server.go` | 85 | cmd/openccu-loom/daemon_boot.go | constructed in daemon.go WireDeps setup for the BIN-RPC callback listener used by CUxD interfaces |
| `CrossValidationRule` | `internal/configui/schema.go` | 12 | internal/configui/schema.go (same file) | type of CrossValidationConstraint.Rule; constructed from embedded metadata and evaluated in session cross-validation, which the production callgraph reaches only via the conditionally wired config UI |
| `RecalcUnit` | `internal/model/combined/timer.go` | 390 | internal/model/combined/timer.go (same file) | called by Timer.SetDuration to pick the correct unit before writing to the CCU |
| `ErrValidation` | `internal/model/device/channel_set.go` | 45 | internal/north/rest/problem/problem.go, internal/north/rest/handlers/devices.go, internal/central/adapter/paramsets.go (and 3 more) | matched by the REST PUT /value handler (internal/north/rest/handlers/devices.go) via errors.Is to map a client-side validation rejection to 400 instead of a 502 upstream failure; the reachability heuristic does not follow sentinel-var references through errors.Is |
| `NewAlarmMessagesWithCentral` | `internal/model/hub/messages.go` | 83 | internal/model/hub/messages.go (same file) | called by NewAlarmMessages (legacy wrapper) which is used by hub.NewHub to populate the Hub.Messages field |
| `IsVirtualInterfaceName` | `internal/netutil/interfaces.go` | 42 | internal/north/discovery/mdns/interfaces.go, internal/north/matter/mdns/zeroconf.go | statically called by the client-discovery and Matter mDNS advertisers (internal/north/discovery/mdns, internal/north/matter/mdns); those advertiser paths sit behind the daemon wiring the RTA entry-point analysis already tolerates as unreachable for the caller packages |
| `Candidate` | `internal/north/matteradapter/candidates.go` | 27 | cmd/openccu-loom/matter_status_adapter.go, internal/north/rest/handlers/matter_exposures.go, internal/north/matteradapter/candidates.go (same file) | returned by CollectCandidates and consumed by the REST /matter/exposable handler; a method-less data struct that the reachability analyzer's type heuristic (which marks a type reachable only via its methods) cannot see used |
| `MatchesSpecialValue` | `internal/parameter/validate.go` | 230 | internal/model/generic/bounds.go, internal/parameter/coerce.go, internal/parameter/validate.go (same file) | single source of truth for the SPECIAL MIN/MAX-bypass rule; called by the write-coerce path (coerce.go), the validation path (validate.go), and the runtime read path (internal/model/generic bounds.go) — the reachability heuristic does not resolve the delegated cross-package call |
| `NewDeviceStore` | `internal/store/sqlite/devices.go` | 43 | cmd/openccu-loom/descriptor_stores_wiring.go | constructed in daemon.go central wiring alongside other SQLite stores; device persistence for warm-boot cache |
| `Upsert` | `internal/store/sqlite/devices.go` | 46 | cmd/openccu-loom/configcli.go, internal/central/adapter/descriptor_persistence.go, internal/store/sqlite/paramsets.go (and 1 more) | constructed in daemon.go central wiring alongside other SQLite stores; device persistence for warm-boot cache |
| `TypeOfValue` | `internal/store/sqlite/values_cache.go` | 62 | internal/store/sqlite/values_cache.go (same file) | called internally by ValuesCacheStore.SaveValue and SaveBatch to determine column type |
| `AliveKey` | `internal/store/sqlite/values_cache.go` | 580 | internal/central/adapter/values_cache_flush.go | called in production by buildAliveKeySet on the periodic values-cache GC path (internal/central/adapter/values_cache_flush.go), which runs inside the flusher goroutine closure the static reachability pass does not trace |

