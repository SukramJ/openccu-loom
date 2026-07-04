# loom:reachable Whitelist Audit

Generated: 2026-07-04T12:11:03Z

Total annotated items: 32 — PRODUCTIVE: 9 — MASKED: 23

## MASKED — Annotation hides genuine dead code (23 items)

These items have zero production callers outside their definition file.
**Action required:** either wire them into a real production call site, or
document them as intentional dead code in `docs/parity/by_design.md`.
A `loom:reachable` annotation alone is not sufficient justification.

| Identifier | File | Line | Reason |
|---|---|---|---|
| `StaticCallbackBaseURL` | `internal/central/adapter/xmlrpc_announcer.go` | 20 | used in daemon.go WireDeps.CallbackBaseURL assembly for fixed-port mode |
| `NewConnectionRecoveryCoordinatorWithLimit` | `internal/central/coordinators/connection_recovery.go` | 263 | called by NewConnectionRecoveryCoordinator (the standard production entry point); also used directly in integration tests that need a bounded attempt count |
| `WithName` | `internal/central/events/bus.go` | 75 | called by north-bound adapters and diagnostic tooling to annotate subscriptions |
| `WithExternal` | `internal/central/events/bus.go` | 86 | called by MQTT and REST adapters to tag external subscriptions for grouped teardown |
| `NewParamsetRegistry` | `internal/central/registry/paramset.go` | 73 | called by NewParamsetRegistryWithPatches and directly in tests; production always uses the WithPatches variant |
| `NewCcuBackend` | `internal/client/backends/ccu.go` | 73 | used in tests and legacy call sites that do not need interface-type dispatch; production wiring uses NewCcuBackendForInterface |
| `WithHistorySize` | `internal/health/tracker.go` | 124 | functional option for NewTracker; consumed in tests and by future production callers that need non-default history depth |
| `WithStaleAfter` | `internal/health/tracker.go` | 138 | functional option for NewTracker; production callers that need custom stale windows will consume this |
| `Round2` | `internal/metrics/snapshots.go` | 239 | used by REST snapshot handlers and calculated-DP formulas to normalise float precision |
| `RecalcUnit` | `internal/model/combined/timer.go` | 390 | called internally by Timer.Set to pick the correct unit before writing to the CCU |
| `CheckChannelIsOnlyPrimaryChannel` | `internal/model/device/aggregate.go` | 968 | called by MQTT entity-description builder to determine whether the channel-number suffix can be omitted |
| `NewGroup` | `internal/model/event/group.go` | 48 | convenience wrapper for tests; production uses NewGroupWithCentral |
| `NewAlarmMessagesWithCentral` | `internal/model/hub/messages.go` | 83 | called by NewAlarmMessages (legacy wrapper) which is used by hub.NewHub to populate the Hub.Messages field |
| `NewHubPathData` | `internal/model/naming/pathdata.go` | 623 | called by Hub-DP constructors to set the north-bound publish path |
| `ClusterName` | `internal/north/matter/schema/lookup.go` | 20 | used in Matter debug logging, parity tests, and cluster-server introspection paths |
| `DeviceTypeName` | `internal/north/matter/schema/lookup.go` | 44 | used in Matter debug logging and device-type descriptor introspection |
| `NewMqttCircuitBreaker` | `internal/north/mqtt/circuit_breaker.go` | 65 | currently unwired; retained until breaker semantics move into the shared go-mqtt module, which will replace this type |
| `ForKinds` | `internal/payload/payload.go` | 79 | called by north-bound adapters that need all three payload buckets |
| `TypeOfValue` | `internal/store/sqlite/values_cache.go` | 62 | called internally by ValuesCacheStore.SaveValue and SaveBatch to determine column type |
| `AliveKey` | `internal/store/sqlite/values_cache.go` | 579 | called by GC callers building the alive-set before invoking GCDeadRows; exported so coordinator code can construct keys without importing internal format |
| `BuildStack` | `pkg/hmlog/factory.go` | 91 | legacy two-return-value variant used by tests and tooling that do not need TeeHandler |
| `DefaultSensitiveKeys` | `pkg/hmlog/redact.go` | 105 | used by config-UI and REST handlers that need to extend the redaction list |
| `NewRequestContextFilter` | `pkg/hmlog/request_filter.go` | 36 | used by BuildFullStack to enrich log records with per-request fields |

## PRODUCTIVE — Has production callers (9 items)

These items are genuinely reachable from production code.

| Identifier | File | Line | Callers | Reason |
|---|---|---|---|---|
| `ContextWithIdentity` | `internal/auth/middleware.go` | 115 | internal/north/rest/ws/client.go, internal/auth/ingress.go | used by WS upgrade handler and integration tests to inject pre-resolved identity |
| `NewJWKSCache` | `internal/auth/oidc/jwks.go` | 61 | internal/auth/oidc/client.go | used by OIDC auth middleware to verify JWT signatures against the provider's keyset |
| `LoadEasymode` | `internal/ccudata/easymode.go` | 182 | cmd/openccu-loom/daemon_ccudata.go | called by ccudata loader during daemon boot to populate easymode registry |
| `SafeGo` | `internal/central/adapter/safego.go` | 25 | internal/central/adapter/central_bringup.go, internal/central/adapter/eventbridge.go, internal/central/adapter/auto_refresh.go | utility wrapper for panic-safe goroutines; callers in adapter background tasks are added incrementally |
| `DefaultRecoveryPipeline` | `internal/central/coordinators/recovery_stages.go` | 91 | internal/central/adapter/ccu_wiring.go | called by NewConnectionRecoveryCoordinator to assemble the production pipeline |
| `NewQueryFacade` | `internal/central/queryfacade.go` | 68 | internal/central/central.go | used by REST/WS handler wiring in daemon.go to expose the per-central query surface |
| `NewBINRPCServer` | `internal/central/rpcserver/binrpc_server.go` | 79 | cmd/openccu-loom/daemon_boot.go | constructed in daemon.go WireDeps setup for the BIN-RPC callback listener used by CUxD interfaces |
| `NewDeviceStore` | `internal/store/sqlite/devices.go` | 43 | cmd/openccu-loom/descriptor_stores_wiring.go | constructed in daemon.go central wiring alongside other SQLite stores; device persistence for warm-boot cache |
| `Upsert` | `internal/store/sqlite/devices.go` | 46 | cmd/openccu-loom/configcli.go, internal/central/adapter/descriptor_persistence.go, internal/store/sqlite/paramsets.go (and 1 more) | constructed in daemon.go central wiring alongside other SQLite stores; device persistence for warm-boot cache |

