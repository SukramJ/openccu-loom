// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/diagnostics"

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
	auditBuf := ov.buf
	auditRec := ov.rec
	auditDB := ov.db
	auditDurableStats := ov.durableStats
	sqUsers := ov.sqUsers
	sqTokens := ov.sqTokens
	sqCentrals := ov.sqCentrals
	sqSections := ov.sqSections
	configStore := ov.configStore

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
	// production-replay path. Closer is chained into shutdown.
	if recorderPersistTeardown := wireSessionRecorderPersistence(cfg, reg, logger); recorderPersistTeardown != nil { //nolint:contextcheck // wireSessionRecorderPersistence has no ctx parameter; it creates its own internal context
		defer recorderPersistTeardown()
	}
	// Wire SQLite-backed incident recording into every central's
	// CacheCoordinator. CallbackHandlers reads the recorder lazily from
	// CacheCoordinator.GetIncidentRecorder(), so no separate handler-level
	// wiring step is needed. Degrades gracefully when the DB cannot be opened.
	defer wireIncidentRecorder(cfg, reg, logger)() //nolint:contextcheck // wireIncidentRecorder has no ctx parameter; it creates its own internal context

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
	catalogs := si.catalogs
	visReg := si.visReg
	visFilter := si.visFilter
	visibilityUnIgnoreStore := si.visibilityStore
	visibilityAdapter := si.visibilityAdapter
	masterValuesStore := si.masterValuesStore
	valuesCacheStore := si.valuesCacheStore
	wsHub := si.wsHub
	wsHandler := si.wsHandler
	valueWriter := si.valueWriter
	mqttCollector := si.mqttCollector
	mqttSup := si.mqttSup
	mqttWiring := si.mqttWiring
	// --- CCU translation archive ------------------------------
	// Extracted into loadCCUArchive (daemon_boot.go).
	arch := loadCCUArchive(cfg, logger)
	translations := arch.translations
	easymode := arch.easymode
	profiles := arch.profiles

	// Daemon-locale-bound parameter translator — the shared label
	// source for MQTT discovery names and the Matter NodeLabel suffix.
	parameterLabels := adapter.NewParameterLabelAdapter(translations, cfg.Locale)

	bridge := adapter.NewEventBridge(reg, wsHub, mqttWiring).
		WithVisibility(visFilter).
		WithParameterLabels(adapter.NewMqttParameterLabelAdapter(parameterLabels))
	bridge.Start(ctx)
	defer bridge.Stop()

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
		defer hubMQTT.Stop()
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
	sb, southboundTeardown := wireSouthbound(ctx, southboundWiringDeps{
		cfg:                     cfg,
		reg:                     reg,
		logger:                  logger,
		valueWriter:             valueWriter,
		translations:            translations,
		callbackSrv:             callbackSrv,
		callbackPort:            callbackPort,
		callbackHost:            callbackHost,
		binRPCSrv:               binRPCSrv,
		binRPCPort:              binRPCPort,
		visReg:                  visReg,
		masterValuesStore:       masterValuesStore,
		valuesCacheStore:        valuesCacheStore,
		healthTracker:           healthTracker,
		visibilityUnIgnoreStore: visibilityUnIgnoreStore,
		mqttWiring:              mqttWiring,
		bridge:                  bridge,
		hubMQTT:                 hubMQTT,
	}, &availClosers)
	defer southboundTeardown()
	backupAdapter := sb.backupAdapter

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
	// legacy users keep working.
	as := buildAuthStores(cfg, wsHub)
	users := as.users
	tokens := as.tokens
	sessions := as.sessions
	authMw := as.authMw
	sessionResolve := as.sessionResolve
	restResolve := as.restResolve

	// --- REST --------------------------------------------------
	rw := wireREST(ctx, restWiringDeps{
		cfg:            cfg,
		logger:         logger,
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
	servers := rw.servers
	restStatusMetrics := rw.statusMetrics
	restAuth := rw.auth
	configAdminSvc := rw.configAdmin
	userAdminSvc := rw.userAdmin
	tokenAdminSvc := rw.tokenAdmin
	centralAdminSvc := rw.centralAdmin
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
	mqttSup.SetSubscriberBuilder(makeMQTTSubscriberBuilder(ctx, reg, valueWriter, schedulesDomain, mqttCollector, logger))
	if err := mqttSup.AttachSubscribers(ctx); err != nil {
		logger.Warn("mqtt.subscribers.attach", slog.String("err", err.Error()))
	}

	// --- Matter bridge ----------------------------------------
	// Feature-flagged. When matter.enabled is set we stand up
	// the UDP listener, assemble the endpoint topology, and publish
	// the operational mDNS record. Failure here never aborts the
	// daemon — the bridge is best-effort until GA.
	matter, matterAvailClosers, matterStop := wireMatterRuntime(ctx, cfg, reg, healthTracker, parameterLabels, logger, wsHub)
	defer matterStop()
	availClosers = append(availClosers, matterAvailClosers...)
	centralLinksDomain := adapter.NewCentralLinksDomain(reg, valueWriter)
	deviceAdminDomain := adapter.NewDeviceAdminDomain(reg, valueWriter)
	dpWriterAdapter := adapter.NewDataPointWriterAdapter(reg, valueWriter)
	customDPDispatcher := adapter.NewCustomDPDispatcher(reg).SetAuditRecorder(auditRec)
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
	wireWSCommands(wsHub, wsCommandWiring{
		health:          healthAdapter,
		devices:         devicesAdapter,
		hub:             hubAdapter,
		linksDomain:     linksDomain,
		schedulesDomain: schedulesDomain,
		centralLinks:    centralLinksDomain,
		deviceAdmin:     deviceAdminDomain,
		paramsets:       paramsetsDomain,
		customDP:        customDPDispatcher,
		masterProfiles:  masterProfilesStore,
		linkProfiles:    linkProfilesStore,
		valueWriter:     valueWriter,
		registry:        reg,
		// DeviceReloaderAdapter backs config.reload_device_config
		// and ccu.reload_device_config — re-pulls device descriptions from the
		// CCU and recreates missing channels/DPs.
		deviceReloader: adapter.NewDeviceReloaderAdapter(reg, valueWriter),
		logger:         logger,
		centralName:    singleCentralName(reg),
		sessionStore:   sessionStore,
		changeLog:      configChangeLog,
		labels:         parameterLabels,
	})
	_ = uiSchemaAdapter
	_ = dpWriterAdapter

	// --- SystemStatusChangedEvent north-bound subscribers --------
	sysStatusBuf, sysStatusTeardown := wireSystemStatusSubscribers(reg, wsHub, mqttWiring, logger) //nolint:contextcheck // subscribers' Start has no ctx parameter; they subscribe to the event bus internally
	defer sysStatusTeardown()

	// --- REST --------------------------------------------------
	// Build + mount the REST router/server (and optional mDNS advertiser).
	// No-op when REST is disabled. Extracted into mountRESTServer
	// (daemon_rest_mount.go); the returned teardown folds the inline mDNS
	// stop defer.
	restMountTeardown := mountRESTServer(ctx, cfg, logger, servers, restMountDeps{
		reg:                     reg,
		matter:                  matter,
		reload:                  deps,
		healthAdapter:           healthAdapter,
		configAdapter:           configAdapter,
		devicesAdapter:          devicesAdapter,
		deviceAdminDomain:       deviceAdminDomain,
		dpWriterAdapter:         dpWriterAdapter,
		customDPDispatcher:      customDPDispatcher,
		paramsetsDomain:         paramsetsDomain,
		hubAdapter:              hubAdapter,
		ifaceAdapter:            ifaceAdapter,
		sysStatusBuf:            sysStatusBuf,
		visFilter:               visFilter,
		metricsReg:              metricsReg,
		uiSchemaAdapter:         uiSchemaAdapter,
		linksDomain:             linksDomain,
		schedulesDomain:         schedulesDomain,
		centralLinksDomain:      centralLinksDomain,
		backupAdapter:           backupAdapter,
		auditBuf:                auditBuf,
		auditRec:                auditRec,
		restAuth:                restAuth,
		configSvc:               configAdminSvc,
		userSvc:                 userAdminSvc,
		tokenSvc:                tokenAdminSvc,
		centSvc:                 centralAdminSvc,
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
	})
	defer restMountTeardown()

	// --- UI ---------------------------------------------------
	// Build + mount the browser-facing UI router/server (no-op when UI is
	// disabled). Extracted into mountUIServer (daemon_north.go).
	mountUIServer(cfg, logger, servers, uiMountDeps{ //nolint:contextcheck // mountUIServer's OIDC discovery (buildOIDC) uses its own timeout; test callers outside the owned set prevent threading the daemon ctx
		healthAdapter: healthAdapter,
		catalogs:      catalogs,
		users:         users,
		sessions:      sessions,
		sqUsers:       sqUsers,
		sqCentrals:    sqCentrals,
		sqSections:    sqSections,
	})

	if err := servers.startAll(); err != nil { //nolint:contextcheck // startAll has no ctx parameter; individual servers manage their own lifecycle
		return fmt.Errorf("server start: %w", err)
	}

	// --- shutdown wait ---------------------------------------
	// Block until ctx is cancelled, then run the graceful shutdown
	// sequence (Matter ShutDown emit + bounded server stop). Extracted
	// into awaitShutdown (daemon_north.go).
	awaitShutdown(ctx, logger, matter, servers)
	return nil
}

// serverGroup bundles the REST + UI server lifecycles.
