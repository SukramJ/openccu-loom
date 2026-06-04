// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/diagnostics"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	metricswiring "github.com/SukramJ/openccu-loom/internal/metrics/wiring"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"

	// Side-effect import: aggregator package whose blank-imports
	// trigger every custom-DP sub-package's `init()` so the global
	// constructor catalogue is populated before the device pipeline
	// runs. See [internal/model/custom/builtins].
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/builtins"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
	"github.com/SukramJ/openccu-loom/internal/north/ui"
	"github.com/SukramJ/openccu-loom/internal/observability"
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
func daemonServeWithDeps(ctx context.Context, cfg *config.Config, stdout, _ io.Writer, deps *reloadDeps) error { //nolint:gocognit,gocyclo,funlen // composition root: long sequential daemon wiring
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
	// Startup-capture: if the operator enabled the toggle in the SPA
	// (writes to <data_dir>/startup_capture.json) the daemon opens a
	// capture as the very first post-logging step so the bootstrap
	// phase — XML-RPC init, paramset hydration, callback setup —
	// ends up in the archive. Failure is logged but does not abort
	// boot; capture is a diagnostic affordance, not a hard
	// dependency.
	if sc, err := diagnostics.LoadStartupCapture(cfg.DataDir); err != nil {
		logger.Warn("diagnostics.startup_capture.load_failed",
			slog.String("err", err.Error()))
	} else if sc.Enabled {
		opts := diagnostics.StartOptions{
			Duration:  time.Duration(sc.DurationS) * time.Second,
			Anonymise: sc.Anonymise,
			Triggered: "daemon.startup",
		}
		if summary, err := captureManager.Start(opts); err != nil {
			logger.Warn("diagnostics.startup_capture.start_failed",
				slog.String("err", err.Error()))
		} else {
			logger.Info("diagnostics.startup_capture.started",
				slog.String("id", summary.ID),
				slog.Duration("duration", summary.EndsAt.Sub(summary.StartedAt)))
			// One-shot semantics: clear the Enabled flag now that the
			// capture is running. The operator's intent ("record the
			// next boot") was satisfied; the persisted duration /
			// anonymise values stay so the next toggle re-uses them.
			// Doing this right after Start (not at capture stop) is
			// crash-safe: a daemon that dies mid-capture does not boot
			// into a second capture on the next launch.
			cleared := sc
			cleared.Enabled = false
			if err := diagnostics.SaveStartupCapture(cfg.DataDir, cleared); err != nil {
				logger.Warn("diagnostics.startup_capture.clear_failed",
					slog.String("err", err.Error()))
			}
		}
	}
	startAttrs := []any{
		slog.String("locale", cfg.Locale),
		slog.String("log_level", cfg.Logging.Level),
	}
	if overrides := levels.Snapshot(); len(overrides) > 0 {
		paths := make([]string, 0, len(overrides))
		for _, ov := range overrides {
			paths = append(paths, ov.Path+"="+hmlog.FormatLevel(ov.Level))
		}
		startAttrs = append(startAttrs, slog.Any("log_overrides", paths))
	}
	logger.Info("daemon.start", startAttrs...)

	// --- audit DB + config overlay (early) --------------------
	// Open the SQLite-backed audit / config store BEFORE the
	// central registry is built. The DB carries SPA-side live
	// edits (centrals, MQTT section, …) that have to land in cfg
	// before bootstrap.Build snapshots cfg.Centrals — otherwise
	// a CCU the operator added via the SPA only takes effect on
	// the NEXT next restart.
	//
	// The seed-from-YAML logic + auth-chain rewire stay further
	// down where the in-memory user/token stores are constructed;
	// only the DB open + the section/central overlay run here.
	auditBuf := audit.NewBuffer(500)
	auditRec, auditDB, auditDurableStats := wireAuditPersistenceWithDB(cfg, auditBuf, logger) //nolint:contextcheck // wireAuditPersistenceWithDB has no ctx parameter; it creates its own internal context
	// Release the audit/config DB handle on shutdown. Registered early so
	// it runs late (LIFO) — after the health probe and the stores that
	// read it. A leaked handle blocks temp-dir cleanup on Windows.
	if auditDB != nil {
		defer func() { _ = auditDB.Close() }()
	}
	var (
		sqUsers     *sqlitestore.UserStore
		sqTokens    *sqlitestore.TokenStore
		sqCentrals  *sqlitestore.CentralsStore
		sqSections  *sqlitestore.ConfigSectionStore
		configStore *configstore.Store
	)
	if auditDB != nil {
		sqUsers = sqlitestore.NewUserStore(auditDB)
		sqTokens = sqlitestore.NewTokenStore(auditDB)
		sqCentrals = sqlitestore.NewCentralsStore(auditDB)
		sqSections = sqlitestore.NewConfigSectionStore(auditDB)
		bootstrapCfg := &config.BootstrapConfig{
			DataDir: cfg.DataDir,
			Logging: cfg.Logging,
			Listen:  config.BootstrapListen{REST: cfg.North.REST.Listen, UI: cfg.North.UI.Listen},
		}
		configStore = configstore.New(bootstrapCfg, sqSections, sqCentrals,
			configstore.WithEnvLookup(os.Getenv))
		if _, err := configStore.OverlayInto(ctx, cfg); err != nil {
			logger.Warn("configstore.overlay", slog.String("err", err.Error()))
		} else if err := cfg.Validate(); err != nil {
			logger.Warn("configstore.overlay.validate", slog.String("err", err.Error()))
		}
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

	// Seed every central's health tracker with a synthetic "started"
	// sample so the `/health` endpoint reports green as soon as the
	// daemon hits its signal loop.
	obsRecorder := observability.LogRecorder{Logger: logger.With(slog.String("component", "observability"))}
	for _, u := range reg.List() {
		u.Health.Record("central", health.Sample{Healthy: true, Note: "started"})
		u.SetObservabilityRecorder(obsRecorder)

		// Pin the primary interface explicitly when the operator
		// configured `primary_interface` for this central. Empty
		// (default) keeps the built-in HmIP-RF substring heuristic.
		// Multi-CCU setups with HmIP-Wired-only or BidCos-only
		// installations rely on this to score the right interface
		// as the central's primary.
		for i := range cfg.Centrals {
			if cfg.Centrals[i].Name == u.Name() && cfg.Centrals[i].PrimaryInterface != "" {
				u.Health.SetPrimaryInterface(cfg.Centrals[i].PrimaryInterface)
				logger.Info("health.primary_interface.pinned",
					slog.String("central", u.Name()),
					slog.String("interface", cfg.Centrals[i].PrimaryInterface))
				break
			}
		}

		// Surface the event-bus deferred high-water gauge through the
		// health tracker so admin endpoints can alert on pathological
		// handler recursion without owning the events package directly.
		bus := u.EventBus
		u.Health.RegisterGauge("event_bus.deferred_depth",
			func() float64 { return float64(bus.DeferredDepth()) })
		u.Health.RegisterGauge("event_bus.deferred_high_water",
			func() float64 { return float64(bus.DeferredHighWater()) })
		// Audit durability telemetry (O12 / R11). Surfaces the durable-
		// sink overflow counter so admin endpoints can alert on
		// audit-row loss before the database falls behind. Skipped
		// when the durable sink was not wired (in-memory-only audit).
		if auditDurableStats != nil {
			s := auditDurableStats
			u.Health.RegisterGauge("audit.dropped",
				func() float64 { return float64(s.Dropped()) })
			u.Health.RegisterGauge("audit.sink_errors",
				func() float64 { return float64(s.SinkErrors()) })
		}
		// Scheduler coverage: job-count + cumulative failure counter
		// (errors + recovered panics). Per-job breakdown is reachable
		// via the diagnostics-dump component map; the gauge gives the
		// SPA a single number for the at-a-glance tile.
		if u.Scheduler != nil {
			scheduler := u.Scheduler
			u.Health.RegisterGauge("scheduler.jobs",
				func() float64 { return float64(len(scheduler.Jobs())) })
			u.Health.RegisterGauge("scheduler.failures",
				func() float64 { return float64(scheduler.TotalFailures()) })
		}

		// Wire a per-central metrics aggregator. The Observer subscribes to
		// the central's EventBus so metric events published by clients and
		// coordinators are automatically funnelled into the snapshot.
		// All providers are wired with the components owned by this central;
		// nil providers are safe — Aggregator degrades to zero-value sections.
		obs := metrics.NewObserver()
		unsubMetrics := metricswiring.SubscribeObserver(u.EventBus, obs)
		_ = unsubMetrics // lifetime matches the central; detach on shutdown is best-effort

		agg := metrics.NewAggregator(
			u.Name(), obs,
			metrics.WithClientProvider(metricswiring.NewClientProvider(u.MetricsClients)),
			metrics.WithCacheProvider(metricswiring.NewCacheProvider(u.Cache)),
			metrics.WithRecoveryProvider(metricswiring.NewRecoveryProvider(u.Recovery)),
			metrics.WithEventBus(metricswiring.NewEventBusProvider(u.EventBus)),
			metrics.WithHealthTracker(metricswiring.NewHealthProvider(u.Health, u.Recovery)),
		)
		u.SetAggregator(agg)
	}

	// --- shared infrastructure ---------------------------------
	si, sharedInfraTeardown := wireSharedInfrastructure(ctx, cfg, logger, reg, deps)
	defer sharedInfraTeardown()
	metricsReg := si.metricsReg
	healthTracker := si.healthTracker
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
	// Optional: operators can drop the
	// into cfg.CCUData.TranslationsPath so the UI shows localised
	// device/parameter labels. Missing/empty path falls back to the
	// raw CCU strings. Parse errors are logged and degraded to empty.
	// Loaded BEFORE the EventBridge so the bridge can hand the labeler
	// down into the MQTT discovery `name` field — without it HA shows
	// raw uppercase parameter ids.
	translations := loadTranslations(cfg, logger)
	easymode := loadEasymode(logger)
	profiles := loadProfiles(logger)

	bridge := adapter.NewEventBridge(reg, wsHub, mqttWiring).
		WithVisibility(visFilter).
		WithParameterLabels(adapter.NewMqttParameterLabelAdapter(
			adapter.NewParameterLabelAdapter(translations, cfg.Locale),
		))
	bridge.Start(ctx)
	defer bridge.Stop()

	// Wire hub entity → MQTT publisher. Only active when MQTT is
	// configured; guards on mqttWiring == nil so the daemon degrades
	// gracefully without a broker. Re-fires on every broker reconnect
	// so retained hub state (sysvars, programs, alarm/service messages)
	// is restored after the broker drops its retained store or the
	// supervisor swaps the stack — Start() is idempotent (Stop+rewire).
	if mqttWiring != nil {
		hubMQTT := adapter.NewHubMQTTPublisher(reg, mqttWiring, logger)
		hubMQTT.Start(ctx)
		defer hubMQTT.Stop()
		mqttSup.OnConnect(func(ctx context.Context) {
			hubMQTT.Start(ctx)
		})
	}

	// --- XML-RPC callback server -------------------------------
	// Shared across every central; routes by `/RPC2/<central_name>`.
	// A binding failure is a hard error — the daemon would otherwise
	// silently lose every CCU value-change event.
	callbackCtx, cancelCallback := context.WithCancel(ctx)
	defer cancelCallback()
	callbackSrv, callbackBaseURL, err := startCallbackServer(callbackCtx, cfg, logger)
	if err != nil {
		logger.Warn("callback.start.failed", slog.String("err", err.Error()))
	}
	if callbackSrv != nil {
		logger.Info("callback.listen",
			slog.String("addr", callbackSrv.Addr().String()),
			slog.String("base_url", callbackBaseURL))
	}

	// --- BIN-RPC callback server --------------------------------
	// Shared listener for CUxD interfaces. Routing uses the interface_id
	// carried inside every BIN-RPC envelope. A nil server is a valid
	// degraded state — WireCentrals skips CUxD registration when
	// BINRPCCallbackServer is nil.
	var (
		binRPCSrv  *rpcserver.BINRPCServer
		binRPCAddr string
	)
	{
		binHost := cfg.Callback.Host
		if binHost == "" {
			binHost = "0.0.0.0"
		}
		binAddr := fmt.Sprintf("%s:%d", binHost, cfg.Callback.BinPort)
		binCfg := rpcserver.BINRPCConfig{
			Addr:   binAddr,
			Logger: logger.With(slog.String("component", "callback.binrpc")),
		}
		srv, binErr := rpcserver.NewBINRPCServer(binCfg) //nolint:contextcheck // NewBINRPCServer/bindAddr has no ctx parameter; bind is instantaneous
		if binErr != nil {
			logger.Warn("callback.binrpc.start.failed", slog.String("err", binErr.Error()))
		} else {
			binRPCSrv = srv
			go func() {
				if serveErr := srv.Serve(callbackCtx); serveErr != nil {
					logger.Warn("callback.binrpc.serve", slog.String("err", serveErr.Error()))
				}
			}()
			publicHost := cfg.Callback.PublicHost
			if publicHost == "" {
				publicHost = autodetectCallbackHost(cfg) //nolint:contextcheck // test callers outside owned set prevent threading ctx; UDP bind is instantaneous
			}
			if tcpAddr, ok := srv.Addr().(*net.TCPAddr); ok && publicHost != "" {
				binRPCAddr = fmt.Sprintf("%s:%d", publicHost, tcpAddr.Port)
			}
			logger.Info("callback.binrpc.listen",
				slog.String("addr", srv.Addr().String()),
				slog.String("public_addr", binRPCAddr))
		}
	}

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
		callbackBaseURL:         callbackBaseURL,
		binRPCSrv:               binRPCSrv,
		binRPCAddr:              binRPCAddr,
		visReg:                  visReg,
		masterValuesStore:       masterValuesStore,
		valuesCacheStore:        valuesCacheStore,
		healthTracker:           healthTracker,
		visibilityUnIgnoreStore: visibilityUnIgnoreStore,
		mqttWiring:              mqttWiring,
		bridge:                  bridge,
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
	users := auth.NewMemoryUserStore()
	for name, pass := range cfg.North.REST.Auth.Users {
		users.Put(name, pass, auth.RoleAdmin)
	}
	tokens := auth.NewMemoryTokenStore(buildTokenMap(cfg))
	wsHub.SetTokenStore(tokens)
	sessions := auth.NewSessionStore()
	authMw := auth.NewMiddleware(users, tokens)

	// The SPA authenticates via the session cookie the HTMX login
	// page sets. Since the browser sends cookies across ports to the
	// same hostname, we need to resolve both tokens (Bearer, Basic)
	// AND sessions on the REST listener. Chain the two resolvers so
	// either path identifies the request.
	sessionResolve := auth.SessionMiddleware(sessions)
	restResolve := func(next http.Handler) http.Handler {
		return authMw.Resolve(sessionResolve(next))
	}

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
	mqttSup.SetSubscriberBuilder(makeMQTTSubscriberBuilder(reg, valueWriter, schedulesDomain, mqttCollector, logger))
	if err := mqttSup.AttachSubscribers(ctx); err != nil {
		logger.Warn("mqtt.subscribers.attach", slog.String("err", err.Error()))
	}

	// --- Matter bridge ----------------------------------------
	// Feature-flagged. When matter.enabled is set we stand up
	// the UDP listener, assemble the endpoint topology, and publish
	// the operational mDNS record. Failure here never aborts the
	// daemon — the bridge is best-effort until GA.
	matter, matterAvailClosers, matterStop := wireMatterRuntime(ctx, cfg, reg, healthTracker, logger, wsHub)
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

	// Wire LinkCoordinator on every registered central — UNCONDITIONAL
	// Previously gated by `cfg.North.REST.Enabled`,
	// which left `central.Link.resolver==nil` when REST was off and
	// broke link operations from MQTT/WS adapters that bypass REST.
	for _, u := range reg.List() {
		if err := adapter.WireLinkCoordinator(u, linksDomain); err != nil {
			logger.Warn("wire link coordinator", slog.String("central", u.Name()), slog.String("err", err.Error()))
		}
	}

	// Wire ValueWriter event-bus resolver so WriteOptions.WaitForCallback
	// can subscribe to the right central's bus per call (Task
	// #25). Multi-CCU deployments hit different busses; the resolver
	// closes over the shared registry to dispatch correctly.
	valueWriter.SetBusResolver(func(centralName string) (any, bool) {
		c, ok := reg.Get(centralName)
		if !ok || c == nil {
			return nil, false
		}
		return c.EventBus, true
	})

	// W4: wire the optimistic-update CommandTracker hook. After each
	// successful SetValue the writer calls WriteUnconfirmedValue on the
	// matching InterfaceClient so north-bound adapters can return the
	// new value immediately before the CCU echoes back a callback.
	// The closure looks up the IC via Clients.Get(interfaceID) on the
	// owning central.
	valueWriter.SetCommandTrackerFn(func(interfaceID, channelAddress string, parameter hmenum.Parameter, paramsetKey hmenum.ParamsetKey, value any) {
		for _, u := range reg.List() {
			if entry, ok := u.Clients.Get(interfaceID); ok && entry != nil && entry.Client != nil {
				entry.Client.WriteUnconfirmedValue(channelAddress, parameter, paramsetKey, value)
				return
			}
		}
	})

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
	})
	_ = uiSchemaAdapter
	_ = dpWriterAdapter

	// --- SystemStatusChangedEvent north-bound subscribers --------
	sysStatusBuf, sysStatusTeardown := wireSystemStatusSubscribers(reg, wsHub, mqttWiring, logger) //nolint:contextcheck // subscribers' Start has no ctx parameter; they subscribe to the event bus internally
	defer sysStatusTeardown()

	if cfg.North.REST.IsEnabled() {
		var openapiValidator *middleware.OpenAPIValidator
		if cfg.North.REST.OpenAPIValidateEnabled() {
			openapiValidator = buildOpenAPIValidator(cfg, logger) //nolint:contextcheck // NewOpenAPIValidator/Validate uses context.Background() internally; non-owned code
		}
		// RPC session recorder (XML/JSON-RPC replay capture). Resume a
		// recording that was running before a restart, then expose it.
		rpcRecorder := adapter.NewRPCRecorderAdapter(reg, cfg.DataDir)
		if resumed := rpcRecorder.ResumeFromMarker(ctx); len(resumed) > 0 {
			logger.Info("diagnostics.rpc_recording.resumed", slog.Any("centrals", resumed))
		}
		deps := rest.Deps{
			Logger:         logger,
			StartedAt:      time.Now(),
			Health:         healthAdapter,
			Config:         configAdapter,
			Devices:        devicesAdapter,
			DeviceAdmin:    deviceAdminDomain,
			RefreshDevices: devicesAdapter,
			DPWriter:       dpWriterAdapter,
			CustomDPWriter: customDPDispatcher,
			Paramsets:      paramsetsDomain,
			Hub:            hubAdapter,
			InstallMode:    adapter.NewInstallModeAdapter(),
			Interfaces:     ifaceAdapter,
			Incidents:      adapter.NewIncidentsAdapter(),
			SystemStatus:   sysStatusBuf,
			Labels:         adapter.NewParameterLabelAdapter(translations, cfg.Locale),
			DataPointVis:   visFilter,
			Metrics:        metricsReg,
			UISchema:       uiSchemaAdapter,
			Links:          linksDomain,
			Schedules:      schedulesDomain,
			CentralLinks:   centralLinksDomain,
			Audit:          auditBuf,
			Auth:           restAuth,
			ConfigAdmin:    configAdminSvc,
			UserAdmin:      userAdminSvc,
			TokenAdmin:     tokenAdminSvc,
			CentralAdmin:   centralAdminSvc,
			MQTTReload:     newMQTTReloadAdapter(mqttSup, deps, cfg),
			OIDC:           buildOIDCRest(cfg, logger, restAuth), //nolint:contextcheck // test callers outside owned set prevent ctx signature; discovery uses its own timeout
			SPAHandler:     ui.SPAHandler(),
			Backup:         backupAdapter,
			EditSessions:   handlers.NewEditSessions(),
			WSHandler:      wsHandler,
			AuthResolve:    restResolve,
			AuthRequire:    authMw.Require,
			RequireOperator: func(next http.Handler) http.Handler {
				return authMw.RequireRole(auth.RoleOperator, next)
			},
			RequireAdmin: func(next http.Handler) http.Handler {
				return authMw.RequireRole(auth.RoleAdmin, next)
			},
			SystemCCU: newSystemCCUAdapter(reg, cfg),
			RateLimit: buildRateLimitConfig(cfg),
			Capabilities: runtimeCapabilityDetector{
				mqtt:              mqttWiring != nil,
				matter:            cfg.North.Matter.Enabled,
				oidc:              cfg.North.REST.Auth.OIDC.Enabled && cfg.North.REST.Auth.OIDC.Issuer != "",
				supervisedRestart: detectSupervisedRestart(),
			},
			CORS:       buildCORS(cfg),
			Idempotent: true,
			// When the daemon hosts exactly one central, capture its
			// name in the REST request scope so every slog record
			// carries `central_name` automatically. Multi-central
			// setups leave the field empty and rely on per-handler
			// SetCentralName resolution.
			CentralName:      singleCentralName(reg),
			OpenAPIValidator: openapiValidator,
			// Matter REST surface — fabric-list reads through to the
			// matter store; setup-payload reflects the bridge's
			// configured discriminator + passcode + vendor + product;
			// the commissioning-window opener routes
			// `POST /api/v1/matter/commissioning/window` through the
			// bridge's [matterbridge.CommissioningWindowOpener] (reuses
			// the configured PASE acceptor; ephemeral verifier
			// generation is a post-0.1.0 follow-up).
			MatterFabricStore: matter.fabricStore,
			MatterCommissioning: handlers.MatterCommissioning{
				Discriminator: cfg.North.Matter.Discriminator,
				Passcode:      cfg.North.Matter.Commissioning.Passcode,
				VendorID:      cfg.North.Matter.VendorID,
				ProductID:     cfg.North.Matter.ProductID,
			},
			MatterCommissioningOpener: matter.opener,
			MatterStatusReader:        matter.statusReader,
			MatterFabricRevoker:       matter.fabricRevoker,
			MatterCommissioningCloser: matter.closer,
			MatterExposureStore:       matter.exposureStore,
			MatterCandidateProvider:   matter.candidates,
			MatterEventPublisher:      matter.pub,
			MatterTopologyReassembler: matter.reassembler,
			MatterAuditRecorder:       auditRec,
			// Visibility / un-ignore surface — docs/ui/unignore-concept.md.
			VisibilityUnIgnoreStore:     visibilityUnIgnoreStore,
			VisibilityCentralLister:     visibilityAdapter,
			VisibilityCandidateProvider: visibilityAdapter,
			VisibilityRegistryLoader:    visibilityAdapter,
			// Diagnostics surface wiring (ADR 0017).
			LogLevels: levels,
			// HealthExtras goes through the multi-tracker adapter
			// (daemon-global + every per-central tracker) so the
			// diagnostics dump sees all client details, gauges, and
			// per-central scores regardless of which tracker the
			// producer wrote into.
			HealthExtras:    healthAdapter,
			Capture:         captureManager,
			LogFeed:         stack.Live,
			LogDefaultLevel: levels,
			RPCRecorder:     rpcRecorder,
			AuditRecorder:   auditRec,
			StatusMetrics:   restStatusMetrics,
			KnownCentrals:   reg.Names(),
			HealthGauges:    healthAdapter.Gauges,
			StartupCapture:  handlers.NewStartupCaptureFileService(cfg.DataDir),
			// Mount /system/restart only when a supervisor will
			// bring the daemon back up. On bare-metal dev runs the
			// endpoint stays unmounted (404), so the SPA's button
			// — which we also disable client-side via the
			// SupervisedRestart capability — fails closed.
			EnableRestartEndpoint: detectSupervisedRestart(),
			ValuesCache:           newValuesCacheHandlerAdapter(valuesCacheStore),
			DeviceLookup:          newDeviceLookupAdapter(reg),
			CSRFEnabled:           cfg.North.REST.CSRFIsEnabled(),
			CSRFSecure:            cfg.North.REST.CSRFSecure,
		}
		router := rest.NewRouter(deps)
		servers.add("rest", rest.NewServer(cfg.North.REST.Listen, router, logger))

		if cfg.North.Discovery.MDNS.IsEnabled() {
			if adv, err := startMDNSAdvertiser(ctx, cfg, logger); err != nil {
				logger.Warn("discovery.mdns.start_failed", slog.String("err", err.Error()))
			} else if adv != nil {
				defer func() {
					if err := adv.Stop(); err != nil {
						logger.Warn("discovery.mdns.stop_failed", slog.String("err", err.Error()))
					}
				}()
			}
		}
	}

	// --- UI ---------------------------------------------------
	if cfg.North.UI.IsEnabled() {
		var setupDeps *ui.SetupWizardDeps
		if sqUsers != nil {
			setupDeps = &ui.SetupWizardDeps{
				Users:    sqUsers,
				Centrals: sqCentrals,
				Sections: sqSections,
				Sessions: ui.NewSetupSessionStore(),
			}
		}
		uiRouter := ui.NewRouter(ui.Deps{
			Logger:      logger,
			Lang:        cfg.Locale,
			Health:      healthAdapter,
			Catalogs:    catalogs,
			Auth:        &ui.AuthDeps{Users: users, Sessions: sessions, Secure: false},
			Setup:       setupDeps,
			OIDC:        buildOIDC(cfg, logger), //nolint:contextcheck // test callers outside owned set prevent ctx signature; discovery uses its own timeout
			AuthResolve: auth.SessionMiddleware(sessions),
			AuthRequire: nil, // UI is browser-facing, wizard runs unauthenticated
		})
		servers.add("ui", rest.NewServer(cfg.North.UI.Listen, uiRouter, logger))
	}

	if err := servers.startAll(); err != nil { //nolint:contextcheck // startAll has no ctx parameter; individual servers manage their own lifecycle
		return fmt.Errorf("server start: %w", err)
	}

	// --- shutdown wait ---------------------------------------
	// Block until the supplied ctx is cancelled. Production wires
	// ctx to SIGINT/SIGTERM via [signal.NotifyContext] in main.go;
	// tests pass a [context.WithCancel] ctx so they can drive the
	// shutdown without delivering signals to the test process.
	logger.Info("daemon.ready")
	<-ctx.Done()
	logger.Info("daemon.shutdown", slog.String("cause", context.Cause(ctx).Error()))

	// Matter ShutDown event: spec §11.1.6.2 mandates the cluster
	// fires this event when the bridge is about to terminate so
	// commissioners can detach gracefully. Best-effort — failure
	// to emit is not fatal. matter.bi is nil when matter is disabled.
	if matter.bi != nil {
		matter.bi.EmitShutDown()
	}

	//nolint:contextcheck // shutdown path must not inherit the cancelled daemon ctx
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	servers.stopAll(shutdownCtx) //nolint:contextcheck // shutdown path: shutdownCtx intentionally not derived from daemon ctx
	return nil
}

// serverGroup bundles the REST + UI server lifecycles.
