# loom:reachable Whitelist Audit

Generated: 2026-05-29T06:43:46Z

Total annotated items: 57 — PRODUCTIVE: 4 — MASKED: 53

## MASKED — Annotation hides genuine dead code (53 items)

These items have zero production callers outside their definition file.
**Action required:** either wire them into a real production call site, or
document them as intentional dead code in `docs/parity/by_design.md`.
A `loom:reachable` annotation alone is not sufficient justification.

| Identifier | File | Line | Reason |
|---|---|---|---|
| `ContextWithIdentity` | `internal/auth/middleware.go` | 115 | used by WS upgrade handler and integration tests to inject pre-resolved identity |
| `NewJWKSCache` | `internal/auth/oidc/jwks.go` | 61 | used by OIDC auth middleware to verify JWT signatures against the provider's keyset |
| `ExecuteResult` | `internal/boundary/execute.go` | 107 | generic helper used by any boundary caller that needs to extract a typed result |
| `LoadEasymode` | `internal/ccudata/easymode.go` | 182 | called by ccudata loader during daemon boot to populate easymode registry |
| `BridgeCombinedDataPoint` | `internal/central/adapter/combined_bridge.go` | 47 | installed at combined-DP materialise time in device pipeline; the HSColor/LevelCombined/WeekProfile surface is scaffolding wired incrementally per device type |
| `SafeGo` | `internal/central/adapter/safego.go` | 25 | utility wrapper for panic-safe goroutines; callers in adapter background tasks are added incrementally |
| `NewScheduleFacade` | `internal/central/adapter/schedule_facade.go` | 44 | entry point for WS/REST schedule command handlers; wired in daemon.go north-side setup |
| `WireSchedulerEvents` | `internal/central/adapter/scheduler_events.go` | 21 | scheduler event wiring; called in daemon.go after scheduler job assembly |
| `NewParamsetsAdapter` | `internal/central/adapter/stubs.go` | 30 | wired into REST paramset handler in daemon.go north-side setup |
| `StaticCallbackBaseURL` | `internal/central/adapter/xmlrpc_announcer.go` | 20 | used in daemon.go WireDeps.CallbackBaseURL assembly for fixed-port mode |
| `NewConnectionRecoveryCoordinatorWithLimit` | `internal/central/coordinators/connection_recovery.go` | 256 | called by NewConnectionRecoveryCoordinator (the standard production entry point); also used directly in integration tests that need a bounded attempt count |
| `DefaultRecoveryPipeline` | `internal/central/coordinators/recovery_stages.go` | 91 | called by NewConnectionRecoveryCoordinator to assemble the production pipeline |
| `NewBatch` | `internal/central/events/batch.go` | 36 | used by coordinators that collect events during parameter-sync and flush as a unit |
| `WithName` | `internal/central/events/bus.go` | 72 | called by north-bound adapters and diagnostic tooling to annotate subscriptions |
| `WithExternal` | `internal/central/events/bus.go` | 83 | called by MQTT and REST adapters to tag external subscriptions for grouped teardown |
| `NewQueryFacade` | `internal/central/queryfacade.go` | 68 | used by REST/WS handler wiring in daemon.go to expose the per-central query surface |
| `NewCentralRegistry` | `internal/central/registry/central.go` | 35 | constructed in daemon.go bootstrap to hold all CentralUnit instances; multi-CCU-safe by design |
| `NewParamsetRegistry` | `internal/central/registry/paramset.go` | 50 | called by NewParamsetRegistryWithPatches and directly in tests; production always uses the WithPatches variant |
| `NewCcuBackend` | `internal/client/backends/ccu.go` | 61 | used in tests and legacy call sites that do not need interface-type dispatch; production wiring uses NewCcuBackendForInterface |
| `EncodeHMLevel` | `internal/client/backends/combined.go` | 181 | called by combined-DP write paths that send LEVEL_COMBINED wire values to the CCU |
| `NewParameterGrouper` | `internal/configui/grouping.go` | 149 | instantiated in Config-UI REST handler to group MASTER paramset parameters for the operator UI |
| `NewLabelResolver` | `internal/configui/labels.go` | 59 | instantiated in Config-UI REST handler per request to resolve parameter labels in the operator's locale |
| `WithHistorySize` | `internal/health/tracker.go` | 122 | functional option for NewTracker; consumed in tests and by future production callers that need non-default history depth |
| `WithStaleAfter` | `internal/health/tracker.go` | 136 | functional option for NewTracker; production callers that need custom stale windows will consume this |
| `WithDeviceProvider` | `internal/metrics/aggregator.go` | 203 | passed to NewAggregator in daemon.go alongside WithClientProvider and other metric options |
| `WithHubManager` | `internal/metrics/aggregator.go` | 210 | passed to NewAggregator in daemon.go for hub data point metrics aggregation |
| `EmitLatency` | `internal/metrics/emitter.go` | 25 | called by client transport layer to report per-interface round-trip latency |
| `EmitCounter` | `internal/metrics/emitter.go` | 41 | called by client transport layer to count requests and errors per interface |
| `EmitGauge` | `internal/metrics/emitter.go` | 57 | called by coordinators to publish queue-depth and connection-count gauges |
| `EmitHealth` | `internal/metrics/emitter.go` | 73 | called by health tracker to surface connection-health transitions as metric events |
| `Round2` | `internal/metrics/snapshots.go` | 239 | used by REST snapshot handlers and calculated-DP formulas to normalise float precision |
| `NewHSColor` | `internal/model/combined/hscolor.go` | 69 | combined-DP constructor for color light devices; used by device-profile registry dispatch |
| `RecalcUnit` | `internal/model/combined/timer.go` | 390 | called internally by Timer.Set to pick the correct unit before writing to the CCU |
| `NewWeekProfile` | `internal/model/combined/weekprofile.go` | 82 | combined-DP constructor for climate devices; called from device-profile registry dispatch once climate profile wiring is complete |
| `NewCombinedWeekProfile` | `internal/model/combined/weekprofile.go` | 102 | multi-CCU combined-DP constructor; called from climate device-profile wiring in device pipeline |
| `CheckChannelIsOnlyPrimaryChannel` | `internal/model/device/aggregate.go` | 958 | called by MQTT entity-description builder to determine whether the channel-number suffix can be omitted |
| `NewDefinitionExporter` | `internal/model/device/definition_exporter.go` | 22 | instantiated by REST device-definition handler to produce JSON diagnostic snapshots |
| `GenerateUniqueID` | `internal/model/device/naming.go` | 46 | used by MQTT entity-ID generation and REST definition export to produce stable HA-compatible IDs |
| `NewGroup` | `internal/model/event/group.go` | 46 | convenience wrapper for tests; production uses NewGroupWithCentral |
| `NewAlarmMessagesWithCentral` | `internal/model/hub/messages.go` | 80 | called by NewAlarmMessages (legacy wrapper) which is used by hub.NewHub to populate the Hub.Messages field |
| `NewHubPathData` | `internal/model/naming/pathdata.go` | 623 | called by Hub-DP constructors to set the north-bound publish path |
| `ClusterName` | `internal/north/matter/schema/lookup.go` | 20 | used in Matter debug logging, parity tests, and cluster-server introspection paths |
| `DeviceTypeName` | `internal/north/matter/schema/lookup.go` | 44 | used in Matter debug logging and device-type descriptor introspection |
| `NewMqttCircuitBreaker` | `internal/north/mqtt/circuit_breaker.go` | 63 | constructed in MQTT supervisor setup to gate broker publish operations during connectivity failures |
| `ForKinds` | `internal/payload/payload.go` | 78 | called by north-bound adapters that need all three payload buckets |
| `NewPersistentCache` | `internal/store/sqlite/cache.go` | 88 | constructed in central wiring for debounced persistence of master-values and device data |
| `NewDeviceStore` | `internal/store/sqlite/devices.go` | 43 | constructed in daemon.go central wiring alongside other SQLite stores; device persistence for warm-boot cache |
| `TypeOfValue` | `internal/store/sqlite/values_cache.go` | 57 | called internally by ValuesCacheStore.SaveValue and SaveBatch to determine column type |
| `AliveKey` | `internal/store/sqlite/values_cache.go` | 533 | called by GC callers building the alive-set before invoking GCDeadRows; exported so coordinator code can construct keys without importing internal format |
| `BuildStack` | `pkg/hmlog/factory.go` | 87 | legacy two-return-value variant used by tests and tooling that do not need TeeHandler |
| `DefaultSensitiveKeys` | `pkg/hmlog/redact.go` | 98 | used by config-UI and REST handlers that need to extend the redaction list |
| `NewRequestContextFilter` | `pkg/hmlog/request_filter.go` | 36 | used by BuildFullStack to enrich log records with per-request fields |
| `Delegated` | `pkg/hmproperty/delegated.go` | 38 | used by device-profile paramset-config and MQTT entity descriptions to resolve delegated property paths |

## PRODUCTIVE — Has production callers (4 items)

These items are genuinely reachable from production code.

| Identifier | File | Line | Callers | Reason |
|---|---|---|---|---|
| `CSRFMiddleware` | `internal/auth/csrf.go` | 40 | internal/north/rest/router.go | wired into the REST router middleware chain for SPA mutation protection |
| `GetParamset` | `internal/central/adapter/stubs.go` | 33 | cmd/openccu-loom/ws_adapters.go, internal/north/rest/handlers/paramsets.go, internal/north/rest/ws/commands_extended.go (and 17 more) | wired into REST paramset handler in daemon.go north-side setup |
| `NewBINRPCServer` | `internal/central/rpcserver/binrpc_server.go` | 64 | cmd/openccu-loom/daemon.go | constructed in daemon.go WireDeps setup for the BIN-RPC callback listener used by CUxD interfaces |
| `Upsert` | `internal/store/sqlite/devices.go` | 46 | cmd/openccu-loom/configcli.go, internal/store/sqlite/paramsets.go | constructed in daemon.go central wiring alongside other SQLite stores; device persistence for warm-boot cache |

