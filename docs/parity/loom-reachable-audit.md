# loom:reachable Whitelist Audit

Generated: 2026-07-04T16:05:17Z

Total annotated items: 23 — PRODUCTIVE: 14 — MASKED: 9

## MASKED — Annotation hides genuine dead code (9 items)

These items have zero production call sites (cross-file, or same-file
outside the symbol's own declaration).
**Action required:** either wire them into a real production call site, or
document them as intentional dead code in `docs/parity/by_design.md`.
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
| `AliveKey` | `internal/store/sqlite/values_cache.go` | 579 | key encoder paired with GCDeadRows so alive-set construction stays in sync with the scan comparison; the GC job is not production-wired yet — callers are integration tests |

## PRODUCTIVE — Has production callers (14 items)

These items are genuinely reachable from production code.

| Identifier | File | Line | Callers | Reason |
|---|---|---|---|---|
| `ContextWithIdentity` | `internal/auth/middleware.go` | 115 | internal/north/rest/ws/client.go, internal/auth/ingress.go | used by WS upgrade handler and integration tests to inject pre-resolved identity |
| `NewJWKSCache` | `internal/auth/oidc/jwks.go` | 61 | internal/auth/oidc/client.go | used by OIDC auth middleware to verify JWT signatures against the provider's keyset |
| `LoadEasymode` | `internal/ccudata/easymode.go` | 182 | cmd/openccu-loom/daemon_ccudata.go | called by ccudata loader during daemon boot to populate easymode registry |
| `SafeGo` | `internal/central/adapter/safego.go` | 25 | internal/central/adapter/central_bringup.go, internal/central/adapter/eventbridge.go, internal/central/adapter/auto_refresh.go | utility wrapper for panic-safe goroutines; callers in adapter background tasks are added incrementally |
| `NewConnectionRecoveryCoordinatorWithLimit` | `internal/central/coordinators/connection_recovery.go` | 263 | internal/central/coordinators/connection_recovery.go (same file) | called by NewConnectionRecoveryCoordinator (the standard production entry point); also used directly in integration tests that need a bounded attempt count |
| `DefaultRecoveryPipeline` | `internal/central/coordinators/recovery_stages.go` | 91 | internal/central/adapter/ccu_wiring.go | called by NewConnectionRecoveryCoordinator to assemble the production pipeline |
| `NewQueryFacade` | `internal/central/queryfacade.go` | 68 | internal/central/central.go | used by REST/WS handler wiring in daemon.go to expose the per-central query surface |
| `NewParamsetRegistry` | `internal/central/registry/paramset.go` | 73 | internal/central/registry/paramset.go (same file) | called by NewParamsetRegistryWithPatches and directly in tests; production always uses the WithPatches variant |
| `NewBINRPCServer` | `internal/central/rpcserver/binrpc_server.go` | 79 | cmd/openccu-loom/daemon_boot.go | constructed in daemon.go WireDeps setup for the BIN-RPC callback listener used by CUxD interfaces |
| `RecalcUnit` | `internal/model/combined/timer.go` | 390 | internal/model/combined/timer.go (same file) | called by Timer.SetDuration to pick the correct unit before writing to the CCU |
| `NewAlarmMessagesWithCentral` | `internal/model/hub/messages.go` | 83 | internal/model/hub/messages.go (same file) | called by NewAlarmMessages (legacy wrapper) which is used by hub.NewHub to populate the Hub.Messages field |
| `NewDeviceStore` | `internal/store/sqlite/devices.go` | 43 | cmd/openccu-loom/descriptor_stores_wiring.go | constructed in daemon.go central wiring alongside other SQLite stores; device persistence for warm-boot cache |
| `Upsert` | `internal/store/sqlite/devices.go` | 46 | cmd/openccu-loom/configcli.go, internal/central/adapter/descriptor_persistence.go, internal/store/sqlite/paramsets.go (and 1 more) | constructed in daemon.go central wiring alongside other SQLite stores; device persistence for warm-boot cache |
| `TypeOfValue` | `internal/store/sqlite/values_cache.go` | 62 | internal/store/sqlite/values_cache.go (same file) | called internally by ValuesCacheStore.SaveValue and SaveBatch to determine column type |

