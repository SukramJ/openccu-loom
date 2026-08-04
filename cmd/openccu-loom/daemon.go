// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/channelflags"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/diagnostics"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	northbridge "github.com/SukramJ/openccu-loom/internal/north/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/discovery"
	"github.com/SukramJ/openccu-loom/internal/north/discovery/ssdp"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/webhook"

	// Side-effect import: aggregator package whose blank-imports
	// trigger every custom-DP sub-package's `init()` so the global
	// constructor catalogue is populated before the device pipeline
	// runs. See [internal/model/custom/builtins].
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/builtins"
	"github.com/SukramJ/openccu-loom/internal/store/linkprofile"
	"github.com/SukramJ/openccu-loom/internal/store/masterprofile"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

func daemonServe(ctx context.Context, cfg *config.Config, stdout, stderr io.Writer) error {
	return daemonServeWithDeps(ctx, cfg, stdout, stderr, nil)
}

// daemonServeWithDeps is the real composition root. It accepts an
// optional [reloadDeps] bag so the config-watcher's reload handler
// can reach live subsystems (currently: the MQTT supervisor) after
// boot completes. Direct callers — tests and the no-watcher path —
// pass nil and skip the late binding.
func daemonServeWithDeps(ctx context.Context, cfg *config.Config, stdout, _ io.Writer, deps *reloadDeps) error { //nolint:funlen // composition root: long sequential daemon wiring (statement count is dominated by deps-bag destructuring; gocognit/gocyclo are within budget)
	stack, err := newFullLoggerStack(cfg.Logging, stdout)
	if err != nil {
		return fmt.Errorf("logging.overrides: %w", err)
	}
	logger := stack.Logger
	levels := stack.Levels
	captureManager := diagnostics.NewManager(stack.Tee, levels)
	// Every sub-package that uses slog.Default() inherits the
	// configured level + format. Without this line debug-level logs
	// silently disappear.
	slog.SetDefault(logger)
	// Run the startup-capture phase + emit `daemon.start`. Extracted into
	// logDaemonStart (daemon_logging.go).
	logDaemonStart(cfg, captureManager, levels, logger)

	// --- audit DB + config overlay (early) --------------------
	// Extracted into wireAuditOverlay (daemon_boot.go). The returned
	// teardown is deferred early so it runs late (LIFO) — after the
	// health probe and the stores that read the DB handle.
	ov, auditOverlayTeardown := wireAuditOverlay(ctx, cfg, logger)
	defer auditOverlayTeardown()

	// Persist the external Config-UI URL hint now that the overlay has
	// merged any SPA-persisted north.rest.public_url into cfg, so the CCU
	// add-on's config.cgi can link at the operator's reverse proxy. Best-
	// effort; empty value removes the hint. The field is restart-required
	// (restart.go / reload.go), so writing once at boot is sufficient.
	writeConfigUIHint(cfg.DataDir, cfg.North.REST.ConfigUIURL(), logger)
	auditBuf := ov.buf
	auditRec := ov.rec
	auditDB := ov.db
	// Audit read service: the in-memory buffer alone, or — when the
	// durable DB is present — combined with the SQLite store so the SPA
	// can page / filter / CSV-export over the full retained history.
	var auditRead handlers.AuditService = auditBuf
	if auditDB != nil {
		auditRead = auditReadService{Buffer: auditBuf, store: sqlitestore.NewAuditStore(auditDB)}
	}
	// Per-channel operator overrides (G12): visibility + operation lock, in
	// the main app DB. The overlay is a save-through read cache the ingest and
	// control-write paths consult; hydrated once here at boot.
	var channelFlagsStore *sqlitestore.ChannelFlagsStore
	channelFlagsOverlay := channelflags.New()
	if auditDB != nil {
		channelFlagsStore = sqlitestore.NewChannelFlagsStore(auditDB)
		loadCtx, cancelLoad := context.WithTimeout(ctx, 10*time.Second)
		if list, err := channelFlagsStore.List(loadCtx); err != nil {
			logger.Warn("channel_flags.load_failed", slog.String("err", err.Error()))
		} else {
			for _, f := range list {
				channelFlagsOverlay.Set(f.CentralName, f.ChannelAddress,
					channelflags.Flags{Hidden: f.Hidden, Locked: f.Locked})
			}
		}
		cancelLoad()
	}
	auditDurableStats := ov.durableStats
	sqUsers := ov.sqUsers
	sqTokens := ov.sqTokens
	sqCentrals := ov.sqCentrals
	sqSections := ov.sqSections
	sqDiscoveryIgnore := ov.sqDiscoveryIgnore
	configStore := ov.configStore

	// SSDP/UPnP CCU discovery (ADR 0046): a long-lived scan loop that surfaces
	// Homematic/OpenCCU centrals found on the LAN. Lifecycle mirrors the mDNS
	// advertiser — start here, stop on daemon exit. Nil when disabled, so the
	// REST handlers report an empty discovery set.
	var ssdpDiscoverer *ssdp.Discoverer
	if cfg.North.Discovery.SSDP.IsEnabled() {
		ssdpDiscoverer = ssdp.New(cfg.North.Discovery.SSDP.ResolveInterval(), logger)
		if err := ssdpDiscoverer.Start(ctx); err != nil {
			logger.Warn("discovery.ssdp.start_failed", slog.String("err", err.Error()))
			ssdpDiscoverer = nil
		} else {
			defer func() { _ = ssdpDiscoverer.Stop() }()
		}
	}
	// Assemble the discovery REST deps. Fields are set only when their backing
	// component exists so a disabled scanner / absent DB leaves a true-nil
	// interface (not a non-nil interface wrapping a nil pointer).
	discoveryDeps := &handlers.DiscoveryDeps{}
	if ssdpDiscoverer != nil {
		discoveryDeps.Discoverer = ssdpDiscoverer
	}
	if sqDiscoveryIgnore != nil {
		discoveryDeps.Ignore = sqDiscoveryIgnore
	}
	if sqCentrals != nil {
		discoveryDeps.Centrals = sqCentrals
	}
	// Suggest a stable adoption address per discovered CCU: localhost for a
	// co-located CCU, a reverse-resolved docker hostname for a supervised HA
	// add-on (ADR 0046). Inert (raw host) on a plain build.
	discoveryDeps.SuggestHost = discovery.NewHostSuggester(isSupervised()).Suggest

	// Start the periodic WAL checkpoint for the audit/config DB. Keeps the
	// WAL file bounded on embedded and busy targets without blocking readers.
	// Guard on auditDB != nil — the checkpoint loop is a no-op when the DB
	// was not opened (in-memory-only audit path). The stop function defers
	// the final shutdown checkpoint via StartWALCheckpointLoop's closer.
	if auditDB != nil {
		defer sqlitestore.StartWALCheckpointLoop(auditDB, 0, logger)() //nolint:contextcheck // StartWALCheckpointLoop creates its own daemon-lifetime context internally
	}

	// --- central registry --------------------------------------
	bootstrap := &central.Bootstrap{Logger: logger}
	reg, teardown, err := bootstrap.Build(ctx, cfg)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	defer teardown()

	// Wire the devices-created gate BEFORE StartAll so the
	// `gatedRunWithDevicesCreatedGate`-protected hub jobs registered
	// during scheduler bring-up have a working gate from t=0.
	// `IsDevicesCreated()` returns true automatically once the first
	// `DeviceCreatedEvent` lands on the bus during WireCentrals.
	// follow-up.
	for _, u := range reg.List() {
		u.WireDevicesCreatedGate()
	}

	registerStandardJobs(reg, cfg, logger)

	if err := reg.StartAll(ctx); err != nil {
		return fmt.Errorf("central start: %w", err)
	}

	// Wire SQLite-backed disk-persistence for every central's
	// SessionRecorder. The recorder itself stays inactive until an
	// operator activates it via REST/WS — the wiring only ensures that,
	// when active, captured sessions survive a daemon restart.
	// production-replay path. Closer is chained into shutdown. Shares
	// auditDB (ov.db) rather than opening its own handle — see openLoomDB.
	if recorderPersistTeardown := wireSessionRecorderPersistence(auditDB, reg, logger); recorderPersistTeardown != nil { //nolint:contextcheck // persist tickers outlive this call; their lifecycle is owned by the returned closer, not the boot ctx
		defer recorderPersistTeardown()
	}
	// Wire SQLite-backed incident recording into every central's
	// CacheCoordinator. CallbackHandlers reads the recorder lazily from
	// CacheCoordinator.GetIncidentRecorder(), so no separate handler-level
	// wiring step is needed. Degrades gracefully when auditDB is nil. Shares
	// auditDB (ov.db) rather than opening its own handle — see openLoomDB.
	incidentStore, incidentTeardown := wireIncidentRecorder(auditDB, reg, logger)
	defer incidentTeardown()

	// Seed every central's health tracker (synthetic "started" sample,
	// primary-interface pin, event-bus / audit / scheduler gauges) and wire
	// its metrics aggregator. Extracted into seedCentralHealthAndMetrics
	// (daemon_wiring.go).
	seedCentralHealthAndMetrics(reg, cfg, auditDurableStats, logger)

	// --- shared infrastructure ---------------------------------
	si, sharedInfraTeardown := wireSharedInfrastructure(ctx, cfg, logger, reg, deps)
	defer sharedInfraTeardown()
	metricsReg := si.metricsReg
	healthTracker := si.healthTracker
	// Surface whether config secrets are encrypted at rest — only meaningful
	// when the SQLite store that holds them is in use.
	if ov.db != nil {
		recordSecretHealth(healthTracker, metricsReg, ov.secretsAvailable)
	}
	// Teach the reload path how to re-derive the effective config, so a
	// REST-triggered reload picks up section edits the SPA persisted to the
	// database after boot.
	wireConfigAssembler(deps, ov)
	catalogs := si.catalogs
	visReg := si.visReg
	visFilter := si.visFilter
	visibilityUnIgnoreStore := si.visibilityStore
	visibilityAdapter := si.visibilityAdapter
	masterValuesStore := si.masterValuesStore
	valuesCacheStore := si.valuesCacheStore
	historyStore := si.historyStore
	recordingOverrides := si.recordingOverrides
	recordingStore := si.recordingStore
	wsHub := si.wsHub
	wsHandler := si.wsHandler
	valueWriter := si.valueWriter
	mqttCollector := si.mqttCollector
	mqttSup := si.mqttSup
	// G12: let the (re)built MQTT bridge skip operator-hidden channels, so a
	// hidden channel disappears from the MQTT plane like it does from the REST
	// operation list and Matter. The overlay is keyed on (central, address).
	if mqttSup != nil {
		mqttSup.SetChannelHidden(func(central, channelAddress string) bool {
			return channelFlagsOverlay.Get(central, channelAddress).Hidden
		})
	}
	mqttWiring := si.mqttWiring
	// Firmware polling jobs arrive in a second registration pass: their
	// hooks resolve CCU backends through the ValueWriter, which does not
	// exist yet when registerStandardJobs runs above.
	registerFirmwareJobs(reg, valueWriter, logger)
	// --- CCU translation archive ------------------------------
	// Extracted into loadCCUArchive (daemon_boot.go).
	arch := loadCCUArchive(cfg, logger)
	translations := arch.translations
	easymode := arch.easymode
	profiles := arch.profiles

	// Daemon-locale-bound parameter translator — the shared label
	// source for MQTT discovery names and the Matter NodeLabel suffix.
	parameterLabels := adapter.NewParameterLabelAdapter(translations, cfg.Locale)

	// North-bound bridge registry — the uniform lifecycle owner for the
	// north-bound surfaces (ADR 0047). Created here (before the MQTT fan-out)
	// so the MQTT service registers FIRST and therefore stops LAST in the
	// reverse-order StopAll, matching the previous LIFO defer placement.
	northBridges := northbridge.NewRegistry(logger)

	bridge := adapter.NewEventBridge(reg, wsHub, mqttWiring).
		WithVisibility(visFilter).
		WithParameterLabels(adapter.NewMqttParameterLabelAdapter(parameterLabels)).
		WithLocale(cfg.Locale)
	// EventBridge starts here (PhaseEarly, before southbound hydration) so the
	// boot-time initial snapshot publishes onto a live bridge; its ordered
	// teardown is owned by northBridges via the mqttService registered below.
	bridge.Start(ctx)

	// Expose the snapshot re-seed to the config-watcher's reload
	// handler. A runtime MQTT swap rebuilds the bridge from scratch
	// (empty Discovery cache + slot state); the supervisor's OnConnect
	// hook fires during buildSwap, before the new bridge is installed
	// into the shared Wiring, so it re-publishes onto the old bridge.
	// The handler invokes this hook AFTER Swap returns — when the
	// Wiring already points at the new bridge — so enabling HA
	// discovery (or any MQTT edit) at runtime seeds the new bridge
	// just as a full restart would. The EventBridge holds the shared
	// Wiring, so the same call routes to whichever bridge is live.
	deps.SetMQTTReseed(bridge.PublishInitialSnapshot)

	// Wire hub entity → MQTT publisher. Only active when MQTT is
	// configured; guards on mqttWiring == nil so the daemon degrades
	// gracefully without a broker. Re-fires on every broker reconnect
	// so retained hub state (sysvars, programs, alarm/service messages)
	// is restored after the broker drops its retained store or the
	// supervisor swaps the stack — Start() is idempotent (Stop+rewire).
	//
	// hubMQTT is declared outside the guard so wireSouthbound can re-run
	// Start after stamping the per-central HubInfo (CCU serials) — hub
	// discovery payloads are skipped until the serial that feeds their
	// unique_ids is known.
	var hubMQTT *adapter.HubMQTTPublisher
	if mqttWiring != nil {
		hubMQTT = adapter.NewHubMQTTPublisher(reg, mqttWiring, logger)
		hubMQTT.Start(ctx)
		mqttSup.OnConnect(func(ctx context.Context) {
			hubMQTT.Start(ctx)
		})

		// Re-publish the full raw-plane snapshot (per-device availability
		// + every DP's slot state) on every broker (re)connect. The boot
		// path also calls PublishInitialSnapshot directly after CCU
		// hydration (see wireSouthbound), but that call races the MQTT
		// connection: when the initial broker connect is slow or fails and
		// only succeeds later in the background reconnect loop, the
		// post-hydration publishes hit a not-yet-connected client and are
		// dropped. Without this hook the availability + slot-state topics
		// the HA-Discovery payloads reference (availability_mode: all) are
		// never (re)published until a live CCU value change trickles in, so
		// HA renders every entity `unavailable`. AnnounceOnline +
		// RepublishDiscovery already run on connect (see daemon_north.go);
		// this restores the raw plane they depend on, and also recovers it
		// after a broker restart drops its retained store. Snapshot is
		// idempotent (retained topics, same payload) and a no-op before
		// hydration has populated the model.
		mqttSup.OnConnect(func(ctx context.Context) {
			bridge.PublishInitialSnapshot(ctx)
		})
	}

	// --- north-bound bridge registry ---------------------------
	// Uniform lifecycle for the north-bound surfaces (ADR 0047). The MQTT
	// fan-out is registered FIRST (so it stops LAST in the reverse-order
	// StopAll) as PhaseEarly — it must be live before southbound hydration so
	// the boot-time initial snapshot of retained CCU state publishes onto a
	// live bridge; StartPhase(PhaseEarly) here is a no-op (it self-started
	// above). The webhook is a real PhaseLate Service: it is NOT started here
	// but later with Matter + REST (northBridges.StartAll after the REST
	// mount), so it only subscribes once the daemon is fully up — a datapoint
	// flood during boot hydration would otherwise POST the whole device state
	// on every restart. Matter and REST register later (also PhaseLate).
	northBridges.RegisterPhase(newMQTTService(bridge, hubMQTT), northbridge.PhaseEarly)
	webhookOutbound := webhook.NewOutbound(reg, cfg.North.Webhook, logger)
	northBridges.Register(webhookOutbound)
	// Alarm engine: a PhaseLate service so it subscribes and reconciles
	// only once the daemon is fully up (its stores ride the shared
	// daemon DB; see wireAlarmService).
	alarmSvc := wireAlarmService(cfg, reg, auditDB, healthTracker, catalogs, logger)
	// The MQTT alarm command sink is built once here (nil when the alarm
	// service is disabled) and shared by the command subscriber and the
	// MQTT alarm publisher, which wires its FAILED_TO_ARM hook — see
	// wireSystemStatusSubscribers.
	alarmMQTTSink := newAlarmMQTTSink(alarmSvc)
	if alarmSvc != nil {
		northBridges.Register(alarmSvc)
		// The alarm collector subscribes to the service's own event bus
		// (see alarm.Service.Bus), so it can only be wired once alarmSvc
		// exists — unlike NewMqttCollector in wireSharedInfrastructure,
		// which runs before this point in the composition root.
		_, stopAlarmCollector := metrics.NewAlarmCollector(metricsReg, alarmSvc.Bus())
		defer stopAlarmCollector()
		// Forward alarm-panel events (state, trigger, journal, health,
		// reminder, duress) through the outbound webhook. Set before the
		// PhaseLate StartAll so the bridge subscribes the alarm bus on
		// start (docs/alarm-concept.md §13.4).
		webhookOutbound.SetAlarmBus(alarmSvc.Bus())
	}

	// The Security & Safety domain aggregates above the alarm engine but
	// does not require it: an installation with smoke and water detectors
	// and no burglar alarm still gets the hazard classes, the fault plane
	// and the notifications.
	securitySvc := wireSecurityService(cfg, reg, auditDB, alarmSvc, catalogs, logger)
	if securitySvc != nil {
		northBridges.Register(securitySvc)
		// Forward the rendered reports and fault transitions through the
		// outbound webhook. Set before the PhaseLate StartAll so the
		// bridge subscribes on start, mirroring SetAlarmBus.
		webhookOutbound.SetSecurityBus(securitySvc.Bus())
		wireSecurityIndexRefresh(alarmSvc, securitySvc, logger)
		_, stopSecurityCollector := metrics.NewSecurityCollector(metricsReg, securitySvc.Bus())
		defer stopSecurityCollector()
	}

	// CCU add-on self-update (ADR 0057): constructed only when the
	// platform capability check passes (add-on build + firmware
	// installer present); nil everywhere else so REST/WS/MQTT all
	// degrade to "unsupported" without re-probing. The WS broadcast is
	// wired for the daemon's whole lifetime here; the MQTT discovery +
	// state publish + command sink are wired per-broker-connection
	// inside makeMQTTSubscriberBuilder below (they must target whichever
	// bridge instance is currently live).
	addonUpdater := wireAddonUpdate(ctx, logger)
	defer wireAddonUpdateWS(addonUpdater, wsHub)()
	defer startAddonUpdatePeriodicCheck(ctx, addonUpdater, cfg, logger)()

	if err := northBridges.StartPhase(ctx, northbridge.PhaseEarly); err != nil {
		logger.Warn("north.bridge.start_early", slog.String("err", err.Error()))
	}
	defer northBridges.StopAll(context.Background()) //nolint:contextcheck // shutdown teardown must not hang on the already-cancelled daemon ctx; a fresh background ctx lets each bridge drain cleanly

	// --- XML-RPC callback server -------------------------------
	// Extracted into wireXMLRPCCallback (daemon_boot.go). The returned
	// teardown cancels the callback context (folds the original
	// `defer cancelCallback()`).
	cb, cancelCallback := wireXMLRPCCallback(ctx, cfg, logger)
	defer cancelCallback()
	callbackCtx := cb.ctx
	callbackSrv := cb.srv
	callbackPort := cb.port

	// --- BIN-RPC callback server --------------------------------
	// Extracted into wireBINRPCCallback (daemon_boot.go). Serves on
	// callbackCtx so it shuts down with the XML-RPC callback server.
	binCB := wireBINRPCCallback(callbackCtx, cfg, logger) //nolint:contextcheck // callbackCtx is the cancellable callback context the BIN-RPC listener serves on; it is intentionally not re-derived from the daemon ctx
	binRPCSrv := binCB.srv
	binRPCPort := binCB.port

	// Resolve the host advertised to each CCU per-central: loopback for a
	// co-located CCU, the LAN IP for an external one (or PublicHost when
	// set). A single global host would mis-advertise to any central not
	// reached over the same interface as the first.
	//nolint:contextcheck // egressHostToward does an instantaneous UDP bind on context.Background(); there is no cancellation point worth threading a ctx through
	callbackHost := func(cc *config.CentralConfig) string { return callbackHostFor(cfg, cc) }

	// --- southbound wiring -------------------------------------
	// Per-central XML-RPC/BIN-RPC client wiring, device pipeline and
	// paramset hydration, VALUES-cache flusher + health gauges,
	// per-central health / availability / climate-link subscriptions,
	// visibility un-ignore application, HubInfo stamping, boot-time MQTT
	// cleanup + initial snapshot, and the periodic unobserved-DP sweep.
	// Extracted into wireSouthbound (daemon_southbound.go).
	//
	// availClosers is declared here (not inside the helper) because the
	// Matter phase appends its own closers to the same slice further
	// down; the helper appends through the pointer and its returned
	// teardown drains the slice at exit, after the Matter append landed.
	var availClosers []func() //nolint:prealloc // length unknown: appended per-central in wireSouthbound and again by the Matter phase
	// sbDeps is a named local (not an inline literal) so the live-CCU-adopt
	// orchestrator (central_adopt.go) can capture the same wiring deps and
	// call [wireCentralNorthbound] for a runtime-added central exactly as
	// this boot-time call does.
	sbDeps := southboundWiringDeps{
		cfg:                     cfg,
		reg:                     reg,
		logger:                  logger,
		valueWriter:             valueWriter,
		translations:            translations,
		catalogs:                catalogs,
		callbackSrv:             callbackSrv,
		callbackPort:            callbackPort,
		callbackHost:            callbackHost,
		binRPCSrv:               binRPCSrv,
		binRPCPort:              binRPCPort,
		visReg:                  visReg,
		masterValuesStore:       masterValuesStore,
		valuesCacheStore:        valuesCacheStore,
		descriptorStores:        si.descriptorStores,
		sqCentrals:              sqCentrals,
		historyStore:            historyStore,
		recordingOverrides:      recordingOverrides,
		recordingStore:          recordingStore,
		channelFlagsOverlay:     channelFlagsOverlay,
		healthTracker:           healthTracker,
		visibilityUnIgnoreStore: visibilityUnIgnoreStore,
		mqttWiring:              mqttWiring,
		bridge:                  bridge,
		hubMQTT:                 hubMQTT,
		postHubReady: func() {
			if f := deps.mdnsTXTRefresh.Load(); f != nil {
				(*f)()
			}
		},
	}
	sb, southboundTeardown := wireSouthbound(ctx, sbDeps, &availClosers)
	defer southboundTeardown()
	backupAdapter := sb.backupAdapter
	// Automatic scheduled CCU backups (opt-in via cfg.Backup.Schedule). Wired
	// here, after the storage-backed backupAdapter exists; the scheduler is
	// already running, so the first backup fires one interval in, not at boot.
	registerScheduledBackupJobs(reg, cfg, backupAdapter, logger)
	// Cache-reset service (ADR 0042) — drives POST /admin/cache/clear.
	// Nil when south-bound never came up (no re-init manager); the route
	// then stays unmounted.
	cacheResetSvc := buildCacheResetService(cfg, reg, valuesCacheStore, masterValuesStore, si.descriptorStores, sb.bringUpManager, auditBuf, logger)

	// Live CCU adopt: the orchestrator
	// that lets POST/DELETE /admin/centrals bring a CCU's southbound + model
	// + scheduler-jobs up or down without a daemon restart. instanceName is
	// recomputed (not threaded out of central.Bootstrap) — it is a pure
	// function of cfg, identical to the one Bootstrap.Build used for the
	// boot-time centrals. Nil when south-bound never came up; the decorator
	// below then leaves the plain SQLite-backed service in place.
	instanceName := cfg.North.Discovery.MDNS.ResolveInstanceName()
	centralOrch := newCentralOrchestrator(reg, sb.bringUpManager, sbDeps, cfg, logger, instanceName,
		valuesCacheStore, masterValuesStore, historyStore, recordingStore)
	// A runtime-adopted central must join the hub-discovery ready pipeline the
	// same way boot-time centrals do, so its serial-gated hub discovery publishes
	// once its bring-up resolves the serial.
	centralOrch.setHubReadyTrigger(sb.hubReadyTrigger)

	// --- adapters ----------------------------------------------
	devicesAdapter := adapter.NewDevicesAdapter(reg).WithWriter(valueWriter)
	hubAdapter := adapter.NewHubAdapter(reg)
	reconnector := adapter.NewRecoveryReconnector(reg, nil)
	ifaceAdapter := adapter.NewInterfacesAdapter(reg, reconnector)
	configAdapter := adapter.NewConfigAdapter(cfg, reg)
	healthAdapter := adapter.NewHealthAdapter(reg, healthTracker)

	// Auth stores — a minimal in-memory state fed from cfg.Users.
	// The SQLite-backed stores (Wave B) are added below as the
	// primary authentication source when persistence is available;
	// the Memory store remains as the secondary fallback so
	// YAML-pinned legacy users keep working.
	// Build the in-memory auth stores (users/tokens/sessions) + the chained
	// token/session resolvers. Extracted into buildAuthStores
	// (daemon_north.go). The SQLite-backed stores are layered on top inside
	// wireREST below; these stay as the secondary fallback so YAML-pinned
	// legacy users keep working. The session store is durable (save-through
	// SQLite) when the DB opened — a typed-nil store must be passed as a
	// genuine nil interface so the in-memory fallback path is taken.
	var sessionPersist auth.SessionPersistence
	if ov.authSessions != nil {
		sessionPersist = ov.authSessions
	}
	as := buildAuthStores(cfg, wsHub, sessionPersist, logger) //nolint:contextcheck // session-store hydration runs at boot wiring with a background ctx; there is no request ctx here
	users := as.users
	tokens := as.tokens
	sessions := as.sessions
	authMw := as.authMw
	sessionResolve := as.sessionResolve
	restResolve := as.restResolve

	// Periodically sweep expired auth sessions from memory and (when
	// durable) the SQLite store. Stopped on shutdown via the daemon ctx.
	defer startSessionPurge(ctx, sessions, logger, sessionPurgeInterval)()

	// --- REST --------------------------------------------------
	rw := wireREST(ctx, restWiringDeps{
		cfg:            cfg,
		logger:         logger,
		reg:            reg,
		auditBuf:       auditBuf,
		auditDB:        auditDB,
		healthTracker:  healthTracker,
		configStore:    configStore,
		sqUsers:        sqUsers,
		sqCentrals:     sqCentrals,
		sqTokens:       sqTokens,
		sqSections:     sqSections,
		users:          users,
		tokens:         tokens,
		sessions:       sessions,
		authMw:         authMw,
		restResolve:    restResolve,
		sessionResolve: sessionResolve,
	})
	restStatusMetrics := rw.statusMetrics
	restAuth := rw.auth
	configAdminSvc := rw.configAdmin
	userAdminSvc := rw.userAdmin
	// Self-service password change needs the concrete SQLite store
	// (verify current password + write). Keep a true nil interface when
	// the store is absent so the handler's nil-guard fires.
	var selfPasswordSvc handlers.SelfPasswordService
	if sqUsers != nil {
		selfPasswordSvc = sqUsers
	}
	prefSvc, diagramSvc, areaSvc := appDBServices(auditDB)
	tokenAdminSvc := rw.tokenAdmin
	// Wrap the persisted CentralAdminService so POST/PUT/DELETE
	// /admin/centrals also drive the live orchestrator built above — the
	// REST injection seam for live CCU adopt. No-op wrap (returns rw.centralAdmin
	// unchanged) when persistence or the orchestrator is unavailable.
	centralAdminSvc := newLiveCentralAdmin(rw.centralAdmin, centralOrch, logger)
	authMw = rw.authMw
	restResolve = rw.authResolve

	// --- Adapter-Hoist ---------------------------------------- Adapters used
	// by BOTH the WS-command router and the REST router are constructed once
	// here so the WS hub can register its full command set even when REST is
	// disabled (e.g. UI-only deployments or dev-loopback).
	linksDomain := adapter.NewLinksDomain(reg, valueWriter, translations).SetAuditRecorder(auditRec)
	paramsetsDomain := adapter.NewParamsetsDomain(reg, valueWriter).SetAuditRecorder(auditRec).SetVisibilityGate(visReg)
	schedulesDomain := adapter.NewSchedulesDomain(reg, valueWriter).SetAuditRecorder(auditRec)

	// --- MQTT subscribers --------------------------------------
	// Wired post-schedulesDomain so the WeekProfileSink can be
	// attached. The supervisor's builder closure is invoked here
	// once for the initial stack, and re-invoked automatically on
	// every Swap (hot-reload) so birth sync + command subscriber
	// follow the new broker without manual re-attachment.
	mqttSup.SetSubscriberBuilder(makeMQTTSubscriberBuilder(ctx, reg, valueWriter, schedulesDomain, mqttCollector, alarmMQTTSink, addonUpdater, logger))
	if err := mqttSup.AttachSubscribers(ctx); err != nil {
		logger.Warn("mqtt.subscribers.attach", slog.String("err", err.Error()))
	}

	// --- Matter bridge ----------------------------------------
	// Feature-flagged. When matter.enabled is set we stand up
	// the UDP listener, assemble the endpoint topology, and publish
	// the operational mDNS record. Failure here never aborts the
	// daemon — the bridge is best-effort until GA.
	// auditDB (ov.db) is threaded through rather than opened again — see
	// openLoomDB in daemon_boot.go.
	matter, matterAvailClosers, matterStop := wireMatterRuntime(ctx, cfg, reg, auditDB, healthTracker, parameterLabels, logger, wsHub)
	// Hand the per-central Matter wiring hook to the live-adopt
	// orchestrator so a runtime-added central latches readiness, triggers
	// a reassemble, and forwards reachability exactly like a boot-time
	// central. Nil-safe on both sides (bridge disabled / orchestrator
	// unavailable).
	centralOrch.setMatterCentralHook(matter.centralHook)
	centralOrch.setAlarmCentralHook(alarmCentralHook(alarmSvc))
	centralOrch.setSecurityCentralHook(securityCentralHook(securitySvc))
	// Matter's ordered teardown is owned by the north-bound registry (it
	// stops after REST, before the webhook). Only registered when enabled —
	// a disabled bridge yields a no-op matterStop and is not a surface. The
	// registry's StopAll (awaitShutdown + the boot-error defer) runs it; see
	// matterService for why Start is a no-op (self-started at construction).
	if cfg.North.Matter.Enabled {
		northBridges.Register(newMatterService(matterStop))
	}
	availClosers = append(availClosers, matterAvailClosers...)
	centralLinksDomain := adapter.NewCentralLinksDomain(reg, valueWriter)
	definitionExportDomain := adapter.NewDefinitionExportDomain(reg)
	deviceAdminDomain := adapter.NewDeviceAdminDomain(reg, valueWriter)
	ccuMaintenanceDomain := adapter.NewCCUMaintenanceDomain(reg, valueWriter)
	groupsDomain := adapter.NewGroupsDomain(reg, valueWriter)
	dpWriterAdapter := adapter.NewDataPointWriterAdapter(reg, valueWriter)
	customDPDispatcher := adapter.NewCustomDPDispatcher(reg).SetAuditRecorder(auditRec)
	roomFunctionAdmin := adapter.NewRoomFunctionAdminDomain(reg)
	uiSchemaAdapter := adapter.NewUISchemaAdapter(reg, valueWriter, translations, easymode, profiles)
	masterProfilesStore := masterprofile.New()
	// linkProfilesStore holds easymode link profiles, loaded lazily from the
	// embedded archives when a (receiver, sender) channel-type pair is first
	// requested. The WS layer returns profiles immediately once the pair is
	// looked up — no external generator step is required.
	linkProfilesStore := linkprofile.New()

	// sessionStore tracks in-progress config.session.* edits. It is shared
	// between the WS command handlers so sessions survive individual command
	// round-trips within the same daemon lifetime.
	sessionStore := configui.NewSessionStore()

	// configChangeLog records every successful paramset write that goes
	// through config.session.save. The SPA's change-history panel reads
	// from it via audit WS commands.
	configChangeLog := audit.NewChangeLog()

	// Wire the per-central LinkCoordinator (UNCONDITIONAL — link ops from
	// MQTT/WS adapters that bypass REST need it) plus the two ValueWriter
	// resolver hooks (bus resolver for WaitForCallback, optimistic
	// CommandTracker for unconfirmed-value echo). Extracted into
	// wireValueWriterHooks (daemon_wiring.go).
	wireValueWriterHooks(reg, valueWriter, linksDomain, logger)

	// --- WS-Commands wiring (Task #35 — KRITISCH) ------------- Bridges the
	// REST domain adapters onto the narrower WS-specific interfaces (LinkQuery,
	// ScheduleQuery, HubQuery, …). Without this the wsHub.Router() is empty and
	// every {"op":"call"} frame returns unknown_command — the entire SPA WS
	// surface is dead.
	// DeviceReloaderAdapter backs config.reload_device_config and
	// ccu.reload_device_config — re-pulls device descriptions from the CCU
	// and recreates missing channels/DPs. The same instance is reused for
	// both the WS reload commands and the REST reload endpoints.
	deviceReloader := adapter.NewDeviceReloaderAdapter(reg, valueWriter)
	// Shared edit-lock registry: one instance backs both the REST
	// `/sessions/edit` endpoints (+ the strict MASTER/LINK paramset-write
	// gate) and the WS `paramset.put` enforcement, so both transports
	// share a single lock namespace.
	editSessions := handlers.NewEditSessions()
	wireWSCommands(wsHub, wsCommandWiring{
		health:           healthAdapter,
		devices:          devicesAdapter,
		hub:              hubAdapter,
		linksDomain:      linksDomain,
		schedulesDomain:  schedulesDomain,
		centralLinks:     centralLinksDomain,
		groupsDomain:     groupsDomain,
		definitionExport: definitionExportDomain,
		deviceAdmin:      deviceAdminDomain,
		paramsets:        paramsetsDomain,
		customDP:         customDPDispatcher,
		masterProfiles:   masterProfilesStore,
		linkProfiles:     linkProfilesStore,
		valueWriter:      valueWriter,
		registry:         reg,
		deviceReloader:   deviceReloader,
		backups:          backupAdapter,
		editSessions:     editSessions,
		// cacheResetSvc backs ccu.cache_clear — scope-aware clear + re-pull.
		cacheResetSvc: cacheResetSvc,
		// alarm backs alarm_panel.* — nil when the alarm service is disabled.
		alarm:        alarmSvc,
		addonUpdater: wsAddonUpdaterFrom(addonUpdater),
		logger:       logger,
		centralName:  singleCentralName(reg),
		sessionStore: sessionStore,
		changeLog:    configChangeLog,
		labels:       parameterLabels,
	})
	_ = uiSchemaAdapter
	_ = dpWriterAdapter

	// --- SystemStatusChangedEvent north-bound subscribers --------
	sysStatusBuf, hubEventsCentralHook, sysStatusTeardown := wireSystemStatusSubscribers(reg, wsHub, mqttWiring, mqttSup, alarmSvc, alarmMQTTSink, securitySvc, cfg.Locale, cfg.North.REST.PublicURL, logger) //nolint:contextcheck // subscribers' Start has no ctx parameter; they subscribe to the event bus internally
	defer sysStatusTeardown()
	// Installed here rather than next to the Matter/alarm hooks above because
	// the subscriber it closes over is built by the call right above. Runtime
	// adoption is driven over REST, which is not listening yet, so no central
	// can be adopted before this point.
	centralOrch.setHubEventsCentralHook(hubEventsCentralHook)

	// --- REST --------------------------------------------------
	// Build + mount the REST router/server (and optional mDNS advertiser).
	// Build the server-rendered HTMX bootstrap surface (login / first-run
	// setup / about / OIDC) once and fold it onto the REST listener instead of
	// a separate :8081 server (ADR 0044), so onboarding works through one port
	// / HA Ingress. noUsers drives the first-run SPA→/setup redirect.
	bootstrapRouter := buildBootstrapRouter(cfg, logger, uiMountDeps{
		healthAdapter: healthAdapter,
		catalogs:      catalogs,
	})
	// First-run probe gates the SPA onboarding endpoints. Built independently
	// of the UI router so it works whenever REST is up, even with the
	// diagnostic UI disabled.
	noUsers := firstRunProbe(cfg, sqUsers, sqCentrals)

	// No-op when REST is disabled. Extracted into mountRESTServer
	// (daemon_rest_mount.go); the returned teardown folds the inline mDNS
	// stop defer.
	restMountTeardown := mountRESTServer(ctx, cfg, logger, northBridges, restMountDeps{
		reg:                     reg,
		bootstrap:               bootstrapRouter,
		noUsers:                 noUsers,
		sqUsers:                 sqUsers,
		sqCentrals:              sqCentrals,
		sqSections:              sqSections,
		sqTokens:                sqTokens,
		sessions:                sessions,
		matter:                  matter,
		reload:                  deps,
		healthAdapter:           healthAdapter,
		configAdapter:           configAdapter,
		devicesAdapter:          devicesAdapter,
		deviceAdminDomain:       deviceAdminDomain,
		ccuMaintenanceDomain:    ccuMaintenanceDomain,
		addonUpdater:            addonUpdater,
		groupsDomain:            groupsDomain,
		deviceReloader:          deviceReloader,
		firmwareRefresher:       adapter.NewFirmwareDomain(reg, valueWriter),
		editSessions:            editSessions,
		dpWriterAdapter:         dpWriterAdapter,
		customDPDispatcher:      customDPDispatcher,
		paramsetsDomain:         paramsetsDomain,
		parameterDeterminer:     adapter.NewParameterDeterminerAdapter(reg, valueWriter),
		hubAdapter:              hubAdapter,
		ifaceAdapter:            ifaceAdapter,
		incidents:               adapter.NewIncidentsStoreReader(incidentStore, reg, logger),
		alarm:                   alarmSvc,
		security:                securitySvc,
		masterProfiles:          masterProfilesStore,
		sysStatusBuf:            sysStatusBuf,
		visFilter:               visFilter,
		metricsReg:              metricsReg,
		uiSchemaAdapter:         uiSchemaAdapter,
		linksDomain:             linksDomain,
		schedulesDomain:         schedulesDomain,
		centralLinksDomain:      centralLinksDomain,
		definitionExportDomain:  definitionExportDomain,
		backupAdapter:           backupAdapter,
		roomFunctionAdmin:       roomFunctionAdmin,
		cacheResetSvc:           cacheResetReset(cacheResetSvc),
		auditBuf:                auditBuf,
		auditRead:               auditRead,
		auditRec:                auditRec,
		restAuth:                restAuth,
		configSvc:               configAdminSvc,
		userSvc:                 userAdminSvc,
		passwordSvc:             selfPasswordSvc,
		prefSvc:                 prefSvc,
		diagramSvc:              diagramSvc,
		areaSvc:                 areaSvc,
		tokenSvc:                tokenAdminSvc,
		centSvc:                 centralAdminSvc,
		discovery:               discoveryDeps,
		translations:            translations,
		mqttSup:                 mqttSup,
		mqttAvailable:           mqttWiring != nil,
		restResolve:             restResolve,
		authMw:                  authMw,
		wsHandler:               wsHandler,
		levels:                  levels,
		liveFeed:                stack.Live,
		captureManager:          captureManager,
		restStatusMetrics:       restStatusMetrics,
		visibilityUnIgnoreStore: visibilityUnIgnoreStore,
		visibilityAdapter:       visibilityAdapter,
		valuesCacheStore:        valuesCacheStore,
		historyStore:            historyStore,
		recordingOverrides:      recordingOverrides,
		channelFlagsStore:       channelFlagsStore,
		channelFlagsOverlay:     channelFlagsOverlay,
	})
	defer restMountTeardown()

	// The browser-facing bootstrap surface is folded into the REST server
	// above (ADR 0044) — there is no separate UI listener.

	// Registration-completeness observation point (ADR 0047 §7): every
	// north-bound surface is now registered (MQTT + webhook above, Matter and
	// REST during their wiring). The guard test inspects the registry here to
	// pin that no surface is hand-wired past the registry and that the
	// reverse-stop order is stable. No-op in production (deps/hook nil).
	if deps != nil && deps.onNorthBridges != nil {
		deps.onNorthBridges(northBridges)
	}

	// Start the PhaseLate north-bound surfaces — the webhook, Matter and the
	// REST/HTTP server — now that the daemon is fully up (router assembled,
	// devices hydrated). StartAll skips the already-started PhaseEarly MQTT
	// service.
	if err := northBridges.StartAll(ctx); err != nil {
		return fmt.Errorf("north bridge start: %w", err)
	}

	// --- shutdown wait ---------------------------------------
	// Block until ctx is cancelled, then run the graceful shutdown
	// sequence (Matter ShutDown emit + bounded reverse-order StopAll, which
	// stops REST first — registered last — then the webhook). Extracted
	// into awaitShutdown (daemon_north.go).
	awaitShutdown(ctx, logger, matter, northBridges)
	return nil
}
