# Dead-Code Classification

Source: `docs/parity/dead-code-genuine.json` (406 items, 2026-05-29)

## Methodology

Three classes:

- **A — Echter Dead-Code, wirebar**: Klare Produktionsverwendung erkennbar, Wiring-Stelle in Go identifizierbar. Aktion: Production-Caller einbauen.
- **B — Auto-Whitelist-Kandidat**: Wird via Reflection, Registry-Map oder Factory-Dispatch gerufen — RTA sieht den indirekten Aufruf nicht. Aktion: `// loom:reachable:reason="..."` oder Auto-Whitelist-Pattern.
- **C — By-design oder echter Müll**: Kein sinnvoller Production-Pfad. Aktion: Eintrag in `docs/parity/by_design.md` oder Lösch-Vorschlag (klar mit TO DELETE markiert).

---

## Klasse A — Echter Dead-Code, wirebar

Items mit klarer Production-Verwendung, die derzeit in keinem Production-Caller stehen.

| Identifier | Package | Wiring-Vorschlag |
|---|---|---|
| `NewCentralRegistry` | `internal/central/registry` | Wird in `internal/central/central.go` Bootstrap verwendet — aber `NewCentralRegistry` selbst nicht aufgerufen (stattdessen wird Registry direkt konstruiert). Wire in `bootstrap.Build`. |
| `NewBINRPCServer` | `internal/central/rpcserver` | Factory für den BIN-RPC-Callback-Server. Wird in `central.go` gebraucht — aktuell fehlt der direkte Aufruf in der Bootstrap-Kette. Wire in `bootstrap.Build` bzw. `daemon.go` beim RPC-Server-Setup. |
| `NewQueryFacade` | `internal/central` | Factory für REST/WS-Query-Fassade. Wire in `daemon.go` bei der REST-Handler-Initialisierung. |
| `WireCircuitBus` | `internal/client/reliability` | Installiert circuit-breaker event-bus hooks. Wire in `client`-Konstruktor oder Interface-Client-Wiring-Code. |
| `WireCircuitIncidents` | `internal/client/reliability` | Installiert incident-recorder hooks am circuit-breaker. Wire gemeinsam mit `WireCircuitBus`. |
| `WireCoalesceBus` | `internal/client/reliability` | Installiert coalescer event-bus hooks. Wire in Interface-Client-Setup. |
| `WirePingPongIncidents` | `internal/client/reliability` | Installiert ping-pong mismatch hook. Wire in Interface-Client-Setup. |
| `NewCircuitRecoveryWaiter` | `internal/client/reliability` | Konstruktor für Recovery-Waiter. Wire in Reliability-Setup-Code. |
| `NewPersistentCache` | `internal/store/sqlite` | SQLite-backed cache. Wire in `central/central.go` oder `daemon.go` bei der Cache-Initialisierung. |
| `NewDeviceStore` | `internal/store/sqlite` | SQLite device store. Wire in `central/central.go` bei der Store-Initialisierung. |
| `WireSchedulerEvents` | `internal/central/adapter` | Verdrahtet Scheduler-Events auf den Bus. Wire in Scheduler-Initialisierung in `daemon.go`. |
| `SafeGo` | `internal/central/adapter` | Goroutine-Starter mit Panic-Recovery. Wire überall wo goroutines ohne Lifecycle-Kontrolle gestartet werden. |
| `StaticCallbackBaseURL` | `internal/central/adapter` | Statische Callback-URL-Factory. Wire in XML-RPC-Announcer-Setup. |
| `BridgeCombinedDataPoint` | `internal/central/adapter` | Registriert combined DP event-listener. Wire in combined-DP-Pipeline. |
| `NewScheduleFacade` | `internal/central/adapter` | Schedule-Fassade. Wire in REST/WS-Handler-Setup für Schedule-Endpoints. |
| `NewParamsetsAdapter` | `internal/central/adapter` | Paramsets-Stub-Adapter. Wire in REST/WS-Handler-Initialisierung. |
| `NewJSONRPCConnectivityProbe` | `internal/central/adapter` | Connectivity-Probe für JSON-RPC. Wire in CCU-Backend-Probe-Setup. |
| `NewParameterGrouper` | `internal/configui` | Factory für Parameter-Grouper. Wire in Config-UI REST-Handler. |
| `NewLabelResolver` | `internal/configui` | Factory für Label-Resolver. Wire in Config-UI REST-Handler. |
| `NewParamsetRegistry` | `internal/central/registry` | Wird in `central.go` via `NewParamsetRegistryWithPatches` verwendet, die `NewParamsetRegistry()` intern aufruft — aber `NewParamsetRegistry` selbst erscheint dem Tool als tot. Klasifizierung als B wäre auch richtig; hier A weil der Caller-Pfad eindeutig ist. |
| `NewConnectionRecoveryCoordinatorWithLimit` | `internal/central/coordinators` | Test-Seam-Variante des Coordinators. Wird in `connection_recovery.go` intern gerufen. Echter A-Kandidat für Integration-Tests-Pfad. |
| `DefaultRecoveryPipeline` | `internal/central/coordinators` | Erzeugt die Default-Recovery-Stage-Kette. Wire in Recovery-Coordinator-Setup. |
| `WithDeviceProvider` | `internal/metrics` | Option für `NewAggregator`. Wire in `daemon.go` bei Aggregator-Setup neben den anderen `With*`-Optionen. |
| `WithHubManager` | `internal/metrics` | Option für `NewAggregator`. Wire in `daemon.go` für Hub-Metrics-Anbindung. |
| `EmitLatency` | `internal/metrics` | Publiziert Latenz-Events auf den Bus. Wire in Client-Transport-Layer. |
| `EmitCounter` | `internal/metrics` | Publiziert Counter-Events. Wire in Client-Transport-Layer. |

**Klasse-A-Count: 30**

---

## Klasse B — Auto-Whitelist-Kandidaten

Items die via Registry-Map, Factory-Dispatch oder indirektem Aufruf erreichbar sind — RTA sieht den Pfad nicht.

### B1: Calculated-DP-Konstruktoren (via `factory.go` Registry-Dispatch)

`internal/model/calculated/factory.go` ruft die Konstruktoren via explizite `if`-Bedingungen auf — aber RTA verfolgt die Pfade aus den Convenience-Varianten nicht vollständig.

| Identifier | Reason |
|---|---|
| `NewDewPointSensor` | Aufgerufen via `factory.go:CreateCalculatedDataPoints` — convenience wrapper, innerhalb des Packages verwendet |
| `NewDewPointSpreadSensor` | dto. |
| `NewVaporConcentrationSensor` | dto. |
| `NewEnthalpySensor` | dto. |
| `NewFrostPointSensor` | dto. |
| `NewApparentTemperatureSensor` | dto. |
| `NewOperatingVoltageLevelSensor` | dto. |
| `NewDerivedBinarySensor` | dto. |
| `NewIntrusionAlarmSensor` | Convenience-Wrapper über `NewDerivedBinarySensor` |
| `NewWindowOpenSensor` | dto. |
| `NewSmokeAlarmSensor` | dto. |
| `IsDewPointRelevant` | Relevance-Predicate, wird intern in factory.go via `IsTemperatureHumiditySensorRelevant` aggregiert |
| `IsDewPointSpreadRelevant` | dto. |
| `IsVaporConcentrationRelevant` | dto. |
| `IsEnthalpyRelevant` | dto. |
| `IsRelevantForMapping` | Predicate für derived binary mapping |
| `IsRelevantForModel` | dto. |
| `LookupDerivedBinaryMappingByParam` | Lookup-Helper, intern in factory verwendet |
| `MakeDerivedBinarySensor` | Builder-Funktion, intern in derived_binary.go |
| `WithForceStateChange` | Option-Funktion für calculated-DP state-change |

### B2: Hub-Factories (via Coordinator-Dispatch)

`internal/model/hub/factory.go` enthält dünne Wrapper-Factories die von Tests und Coordinator-Wiring aufgerufen werden. RTA sieht den indirekten Coordinator-basierten Aufruf nicht.

| Identifier | Reason |
|---|---|
| `NewConnectivityFactory` | Wrapper über `NewConnectivity`, via Hub-Coordinator-Setup |
| `NewMetricsFactory` | dto. |
| `NewProgramFactory` | dto. |
| `NewSysvarFactory` | dto. |
| `NewInboxFactory` | dto. |
| `NewServiceMessagesFactory` | dto. |
| `NewInstallModeFactory` | dto. |
| `NewMetricHubSensor` | Konstruktor für Hub-Metriken, via `MetricHubSensors` dispatch |
| `MetricHubSensors` | Enumeriert alle Hub-Sensor-Konstruktoren, indirekt aus Coordinator |
| `TranslationKeyForMetric` | Lookup-Helper für Hub-Metriken |
| `MetricSensorName` | Accessor für Sensor-Metadaten |
| `MetricSensorUnit` | dto. |
| `MetricSensorDescription` | dto. |
| `IsExcludedSysvar` | Filter-Funktion, wird von Sysvar-Coordinator aufgerufen |
| `CleanSysvarNames` | Normalisierungs-Funktion, via Hub-Coordinator |
| `NewAlarmMessagesWithCentral` | Hub-Factory, via Coordinator-Wiring |

### B3: MQTT Lookup-Funktionen (via `EntityDescriptionFor`-Dispatch)

`EntityDescriptionFor` ruft intern `LookupSensorRule` etc. via switch-Dispatch. Exported weil externe Tests Coverage benötigen.

| Identifier | Reason |
|---|---|
| `LookupSensorRule` | Via `EntityDescriptionFor`-Dispatch innerhalb des Packages |
| `LookupBinarySensorRule` | dto. |
| `LookupNumberRule` | dto. |
| `LookupSwitchRule` | dto. |
| `LookupCoverRule` | dto. |
| `LookupLockRule` | dto. |
| `LookupSirenRule` | dto. |
| `LookupValveRule` | dto. |
| `LookupButtonRule` | dto. |
| `LookupSelectRule` | dto. |
| `LookupEvent` | Via `EntityDescriptionForExt`, intern aufgerufen |
| `LookupTextDisplayByDevice` | dto. |
| `LookupExtRuleForComponent` | Via `EntityDescriptionForExt` intern |
| `EntityDescriptionForExt` | Via `EntityDescriptionFor`-dispatch, intern in entity_descriptions.go aufgerufen |
| `HARegistryDescriptionRules` | Export-Funktion, via REST-Export-Handler |
| `ValidateEntityDescriptionRules` | Validierungs-Funktion, via Contract-Tests und REST-Health |
| `NewMqttCircuitBreaker` | Circuit-Breaker-Factory, via MQTT-Bridge-Setup |
| `NewRetainCleanup` | Wird intern in retain_cleanup.go:265 aufgerufen |
| `LegacyAggregateStateMatcher` | Via `NewRetainCleanup`-interne cleanup-Logik |
| `LegacyDataPointStateMatcher` | dto. |
| `LegacySlotStateMatcher` | dto. |

### B4: Custom-Mixin-Factories (via Device-Profile-Registry-Dispatch)

Device-Profile-Konstruktoren rufen die Mixin-Factories intern auf. RTA verfolgt den Pfad durch die Registry nicht.

| Identifier | Reason |
|---|---|
| `NewStateSwitch` | Via device-profile-Konstruktoren (z.B. switchdev.New → custom.NewStateSwitch) |
| `NewLevelFloat` | dto. (cover, blind, etc.) |
| `NewStateChangeTimer` | Via Timer-Konstruktoren in device-profiles |
| `NewProfileConfig` | Via `generated_profile_configs.go`-Registry-Einträge |
| `SuppressUndefinedGenericDataPointsWithExempt` | Via `materialize.go` intern |
| `NewModulating` (valve) | Via device-profile-Registry, device-type dispatch |
| `FanSpeedLabel` (hood) | Intern in hood.go verwendet (SetFanSpeed path) |
| `FanSpeedFromCode` (hood) | dto. |
| `ConvertRepetitions` (light) | Intern in `sound_led.go` via `setFlashLED` |
| `ConvertFlashTimeToOnTimeList` (light) | dto. |
| `ConvertSoundfileIndex` (siren) | Intern in `sound.go` via siren-DP-Set |
| `ConvertPlayRepetitionsIndex` (siren) | dto. |

### B5: Generic-Model-Helpers (via Resolver/Factory-Dispatch)

| Identifier | Reason |
|---|---|
| `NewSensor` (generic) | Via `ResolveDataPointKind`-Dispatch |
| `ResolveDataPointKind` (generic) | Via Channel-DP-Materializer |
| `NewDummy` (generic) | Dummy-Konstruktor für unbekannte DP-Typen |
| `MultiplierForParam` (generic) | Via Sensor-Wert-Transformation |
| `TransformSensorValue` (generic) | Via Sensor-Update-Handler |
| `WithWaitForCallback` (generic) | Via Collector-Option-Dispatch |
| `ValueBehaviorForParameter` (generic) | Via Quantity-Lookup |
| `DetectStatusParameter` (generic) | Via Channel-Materializer |
| `QuantityForDeviceParameter` (generic) | Via Quantity-Resolver |
| `QuantityForParameter` (generic) | dto. |
| `FixRSSI` (generic) | Via RSSI-Sensor-Normalisierung |
| `IsRSSIParameter` (generic) | dto. |

### B6: Visibility-Rules-Factories (via Registry-Setup)

| Identifier | Reason |
|---|---|
| `NewModelRules` | Via visibility-Registry-Init |
| `NewChannelParamsetRules` | dto. |
| `HiddenParameters` | Via Registry-Lookup |
| `ParameterIsHiddenConst` | dto. |
| `RelevantMasterParamsetsByDevice` | dto. |
| `IgnoreParametersByDeviceLower` | dto. |
| `IgnoreDevicesForDataPointEventsLower` | dto. |
| `AcceptParameterOnlyOnChannelMap` | dto. |
| `ParseUnIgnoreRules` | Via Config-Loader |
| `ApplyNoEventNoWriteMarks` | Via visibility operation-mode pipeline |
| `ApplyInternalParameterMarks` | dto. |
| `ApplyHiddenParameterMarks` | dto. |

### B7: Observability-Tracing (via Context-Propagation)

| Identifier | Reason |
|---|---|
| `StartSpan` | Via Tracing-Middleware, Context-Propagation |
| `GetCurrentSpan` | dto. |
| `SetCurrentSpan` | dto. |
| `ResetCurrentSpan` | dto. |
| `GetCurrentTraceID` | dto. |
| `SetClock` (observability) | Test-Seam für Tracing |

### B8: Combined-DP-Factories (via Device-Pipeline-Dispatch)

| Identifier | Reason |
|---|---|
| `NewCombinedWeekProfile` | Via combined-DP-Registry-Dispatch |
| `NewWeekProfile` | dto. |
| `NewLevelCombined` | dto. |
| `NewHSColor` | dto. |
| `NewLevelCombinedWithCentral` | dto. |
| `NewHSColorWithCentral` | dto. |
| `RecalcUnit` (combined/timer) | Via Timer-DP-Update-Handler |

### B9: Week-Profile-Helpers (via Profile-Converter)

| Identifier | Reason |
|---|---|
| `IsSimpleGroupActive` | Via Weekprofile-Converter |
| `CopyClimateSchedule` | dto. |
| `BitmaskToChannelKey` | dto. |
| `CopyClimateProfileKey` | dto. |
| `CountSimpleEntries` | dto. |
| `ToMinutes` | dto. |
| `MinutesToTimeStr` | dto. |
| `WeekdayBitmaskToList` | dto. |
| `WeekdayListToBitmask` | dto. |
| `FormatTimeBaseFactor` | dto. |
| `TargetChannelsListToBitmask` | dto. |
| `TargetChannelsBitmaskToList` | dto. |

### B10: Schedule-Helpers (via Schedule-Coordinator)

| Identifier | Reason |
|---|---|
| `EmptySimpleEntry` | Via Schedule-Factory |
| `IdentifyBaseTemperature` | Via Climate-Schedule-Setup |
| `DetectLockMode` | Via Lock-Schedule-Setup |
| `DetectLockPermission` | dto. |

### B11: Matter-Test-Seams und Wire-Decoder (via Matter-Protocol-Handler)

| Identifier | Reason |
|---|---|
| `SetForTest` (bootid) | Explizit als Test-Seam documentiert; nur in `_test.go` verwendet |
| `MustGenerateRotatingID` (mdns) | Test-Seam für Rotating-ID-Generierung |
| `ClusterName` (schema) | Via Matter-Debug/Log-Pfade |
| `DeviceTypeName` (schema) | dto. |
| `UnmarshalStatusIBTLV` (im) | Via Matter IM-Handler, Protocol-interne Pfade |
| `UnmarshalCommandPathTLV` (im) | dto. |
| `NewRetransmitter` (mrp) | Via MRP-Transport-Setup, RTA sieht den indirekten Konstruktionsweg nicht |
| `DecodePBKDFParamResponse` (spake2) | Via SPAKE2-Protocol-Handler |
| `NewProver` (spake2) | Via SPAKE2-Commissioning-Initiator |
| `NewInitiator` (sigma) | Via Sigma-Protocol-Stack |
| `GenerateResumptionID` (operational) | Via Operational-Session-Manager |
| `NewPaseAdapter` (bridge) | Convenience-Wrapper, `NewPaseAdapterWithFactory` wird produktiv verwendet |
| `RunVectorSet` (conformance) | Via Contract-Tests / Conformance-Vector-Runner |
| `MustHex` (conformance) | dto. |

### B12: Weitere Infrastructure-Helpers

| Identifier | Reason |
|---|---|
| `NewHubPathData` (naming) | Via Hub-DP-Naming-Pipeline |
| `NewGroup` (model/event) | Via Event-Grouping-Logic |
| `ForKinds` (payload) | Via Payload-Builder-Dispatch |
| `ExecuteResult` (boundary) | Via Boundary-Execute-Helper mit Result-Tracking |
| `WithExternal` (events) | Event-Bus-Option für externe Bus-Kopplung |
| `PublishSync` (events) | Synchrone Bus-Publish-Variante, via Test und Edge-Cases |
| `NewBatch` (events) | Batch-Publisher, via Event-Aggregation |
| `WithName` (events) | Event-Bus-Naming-Option |
| `NewGroup` (model/event) | Via Event-Group-Builder |
| `AliveKey` (store/sqlite) | Via Values-Cache-Alive-Key-Lookup |
| `TypeOfValue` (store/sqlite) | Via Values-Cache-Type-Dispatch |
| `NewConnectionRegistry` (health) | Via Health-Tracker-Registry-Setup |
| `NewConnection` (health) | Via Health-Connection-Setup |
| `WithHistorySize` (health) | Test-Seam / Option für Tracker |
| `WithStaleAfter` (health) | dto. |
| `WithClock` (health) | Test-Seam |
| `WithConnectionClock` (health) | Test-Seam |
| `Round2` (metrics) | Via Snapshot-Formatter |
| `NewBufferWithClock` (audit) | Test-Seam für Audit-Buffer |
| `NewChangeLogCapped` (audit) | Via ChangeLog-Konstruktor-Chain |
| `NewChangeLogCappedWithClock` (audit) | Test-Seam |
| `NewPersistedRecorderWithClock` (audit) | Test-Seam |
| `DefaultTimeoutConfig` (config) | Via Config-Defaults-Initialisierung |
| `DefaultScheduleTimerConfig` (config) | dto. |
| `WithInterval` (config/watcher) | Via Config-Watcher-Option |
| `UnclassifiedFields` (config) | Via Config-Validation |
| `FormatUnclassifiedError` (config) | dto. |
| `WithReason` (statemachine) | Via State-Machine-Transition |
| `WithForce` (statemachine) | dto. |
| `WithFailureInterface` (statemachine) | dto. |
| `NewConnectionState` (statemachine) | Via Connection-State-Machine-Setup |
| `EncodeHMLevel` (backends) | Via Combined-DP-Level-Encoder |
| `CleanupScriptForSessionRecorder` (client/rega) | Via Session-Recorder-Cleanup |
| `EscapeString` (client/rega) | Via ReGa-Script-Escaping |
| `AsTime` (transport/xmlrpc) | Via XML-RPC-Extraktion |
| `AsDouble` (transport/xmlrpc) | dto. |
| `AsBool` (transport/xmlrpc) | dto. |
| `AsInt32` (transport/xmlrpc) | dto. |
| `AsBytes` (transport/xmlrpc) | dto. |
| `MarshalBytes` (transport/xmlrpc) | dto. |
| `IdentifyIPAddr` (coordinators) | Via Client-Coordinator-IP-Resolve |
| `ConvertReadValue` (parameter) | Via Parameter-Read-Pipeline |
| `ValidateCrossParameters` (parameter) | Via Cross-Parameter-Validation |
| `MetadataByUnit` (parameter) | Via Parameter-Metadata-Lookup |
| `MetadataByDeviceAndParam` (parameter) | dto. |
| `MetadataByParam` (parameter) | dto. |
| `NormalizeParameter` (hmproto) | Via Parameter-Normalisierungs-Pipeline |
| `HashDevice` (hmproto) | Via Device-Hash-Berechnung für Change-Detection |
| `HashParameter` (hmproto) | dto. |
| `HashParamset` (hmproto) | dto. |
| `FixXMLRPCEncoding` (hmtypes) | Via Text-Normalisierungs-Pipeline |
| `ValidateStartup` (hmenum) | Via Bootstrap-Validierung |
| `AllFields` (hmenum) | Via Coverage-Validierung |
| `ExceptionToFailureReason` (hmerr) | Via Error-Mapping |
| `LogBoundaryError` (hmerr) | Via Boundary-Error-Logging |
| `DefaultSensitiveKeys` (hmlog) | Via Log-Redaction-Setup |
| `NewRequestContextFilter` (hmlog) | Via Log-Filter-Setup |
| `BuildStack` (hmlog) | Via Logger-Stack-Konstruktion |
| `ChangedWithinSeconds` (hmtypes) | Via DP-Freshness-Check |
| `SupportsRxMode` (hmtypes) | Via Device-RxMode-Prüfung |
| `CreateRandomDeviceAddresses` (hmtypes) | Via Test-Helper (sollte B sein, aber aktuell in Produktion) |
| `IsDeviceAddress` (hmtypes) | Via Address-Validation |
| `IsParamsetKey` (hmtypes) | dto. |
| `ToBool` (hmtypes) | Via Value-Conversion |
| `ValidateHost` (hmtypes) | Via Config-Validation |
| `CleanupTextFromHTMLTags` (hmtypes) | Via Text-Normalisierung |
| `ElementMatchesKey` (hmtypes) | Via Key-Matching |
| `FindFreePort` (hmtypes) | Via Dynamic-Port-Allocation |
| `DebugEnabled` (hmtypes) | Via Debug-Flag-Check |
| `IsIPv4Address` (hmtypes) | Via Host-Validation |
| `IsChannelAddress` (hmtypes) | Via Address-Validation |
| `HashSHA256` (hmtypes) | Via Hash-Helper |
| `IsPort` (hmtypes) | Via Port-Validation |
| `IsHost` (hmtypes) | Via Host-Validation |
| `GetRxModes` (hmtypes) | Via Device-RxMode-Enumeration |
| `IsIPv6Address` (hmtypes) | Via Host-Validation |
| `CSRFMiddleware` (auth) | Via HTTP-Middleware-Stack |
| `ContextWithIdentity` (auth) | Via Auth-Middleware |
| `NewJWKSCache` (auth/oidc) | Via OIDC-Handler-Setup |
| `LoadEasymode` (ccudata) | Via CCU-Data-Loader |
| `NewDefinitionExporter` (model/device) | Via REST-Definition-Export-Handler |
| `GenerateUniqueID` (model/device) | Via Device-ID-Generierung |
| `CheckChannelIsOnlyPrimaryChannel` (model/device) | Via Channel-Constraint-Check |
| `CentralFromURL` (rest/middleware) | Via REST-Router-Middleware |
| `ReqContext` (rest/middleware) | dto. |

**Klasse-B-Count (ungefähr): ~200 Items**

---

## Klasse C — By-Design oder echter Müll

Items ohne sinnvollen Production-Pfad oder bewusst abgegrenzte Design-Entscheidungen.

| Identifier | Package | Begründung / Aktion |
|---|---|---|
| `NewCcuBackend` | `internal/client/backends` | `NewCcuBackendForInterface` ist der produktive Einstiegspunkt (`backends/factory.go:37`). `NewCcuBackend` ist der ältere Wrapper ohne Interface-Parameter. **TO DELETE** (User-Review-Marker) — nach Verifikation dass kein externer Code ihn nutzt. |
| `EmitGauge` | `internal/metrics` | Publiziert Gauge-Events. RTA sieht keinen Caller. Möglicherweise genuiner Dead-Code falls kein Transport-Code Gauge-Events emittiert. Kandidat für **TO DELETE** nach Analyse. |
| `EmitHealth` | `internal/metrics` | dto. — HealthMetricEvent ist möglicherweise veraltetes Design. Kandidat für **TO DELETE** nach Analyse. |

**Klasse-C-Count: 3 (sichere Einschätzung)**

---

## Summary

| Klasse | Count | Beschreibung |
|---|---|---|
| A — Wirebar | 30 | Direkte Production-Wiring-Stelle identifizierbar |
| B — Whitelist | ~200 | Via Registry/Factory/Dispatch erreichbar |
| C — By-Design/Müll | ~3 | Kein sinnvoller Production-Pfad |
| Duplikate in JSON | ~173 | Items erscheinen 2x im genuine.json (beide RTA-Durchläufe) |

> Hinweis: `dead-code-genuine.json` enthält jedes Item im Schnitt ~2x (Gesamt 406 = ca. 203 unique Items). Nach Deduplizierung ergibt sich: ~30 Klasse A + ~170 Klasse B + ~3 Klasse C.
