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
	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/diagnostics"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	metricswiring "github.com/SukramJ/openccu-loom/internal/metrics/wiring"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"

	// Side-effect import: aggregator package whose blank-imports
	// trigger every custom-DP sub-package's `init()` so the global
	// constructor catalogue is populated before the device pipeline
	// runs. See [internal/model/custom/builtins].
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/builtins"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/north/ui"
	"github.com/SukramJ/openccu-loom/internal/observability"
	"github.com/SukramJ/openccu-loom/internal/store/linkprofile"
	"github.com/SukramJ/openccu-loom/internal/store/masterprofile"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
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
	metricsReg := metrics.NewRegistry()
	healthTracker := health.NewTracker()
	catalogs, _ := i18n.NewCatalogs()

	// Outbound visibility filter (ADR 0007): wrap the default registry
	// as a filter.VisibilitySet so adapters never import the full
	// visibility loading machinery. The registry uses built-in rules by
	// default; operators can extend them via un-ignore files once that
	// config knob is wired. A nil adapter is never produced here
	// (NewRegistry always returns non-nil) but the Adapter is nil-safe.
	visReg := visibility.NewRegistry()
	// E.13: seed the required-parameter whitelist with every
	// parameter referenced by the generated profile catalogue plus
	// every Extended config. This is what protects required custom-DP
	// parameters (e.g. SET_POINT_TEMPERATURE) from being filtered out
	// by IGNORED_PARAMETERS during paramset hydration.
	visReg.SetRequiredParameters(custom.DefaultRegistry().RequiredParameters())
	visFilter := filter.NewAdapter(visReg)

	// Visibility / un_ignore — SQLite-backed store, bootstrap-seed from
	// config.yaml on first start, then wired into the REST surface via
	// the visibilityAdapter (see cmd/openccu-loom/visibility_adapter.go +
	// visibility_wiring.go + docs/ui/unignore-concept.md). The patterns
	// are applied to visReg after WireCentrals so the suppression marks
	// land on materialised devices.
	visibilityUnIgnoreStore := wireVisibilityUnIgnoreStore(cfg, logger) //nolint:contextcheck // wireVisibilityUnIgnoreStore has no ctx parameter
	defer func() { _ = visibilityUnIgnoreStore.Close() }()
	visibilityAdapter := newVisibilityAdapter(visReg, visibilityUnIgnoreStore, reg)
	masterValuesStore := wireMasterValuesStore(cfg, logger) //nolint:contextcheck // wireMasterValuesStore has no ctx parameter
	defer func() { _ = masterValuesStore.Close() }()
	valuesCacheStore := wireValuesCacheStore(cfg, logger) //nolint:contextcheck // wireValuesCacheStore has no ctx parameter
	defer func() { _ = valuesCacheStore.Close() }()

	wsHub := ws.NewHub()
	if n := cfg.North.REST.WS.ReplayCapacity; n > 0 {
		wsHub.SetReplayCapacity(n)
	}
	wsHandler := ws.Handler(wsHub, logger, wsAllowedOrigins(cfg))
	// WS subscriber-count gauge so the diagnostics dump shows how
	// many SPA clients are currently subscribed for live updates.
	// Registered against every central's tracker because the WS hub
	// is daemon-global; per-central scoping would double-count.
	if healthTracker != nil {
		hub := wsHub
		healthTracker.RegisterGauge("ws.subscribers",
			func() float64 { return float64(hub.ClientCount()) })
	}

	valueWriter := clientpkg.NewValueWriter()
	// Stamp the build version into MQTT Discovery payloads so the
	// `origin.sw_version` field reflects the running binary instead of
	// the "dev" default. Set before the supervisor starts emitting
	// Discovery so the very first payload already carries it.
	mqtt.SetOriginVersion(build.Version)
	mqttCollector := metrics.NewMqttCollector(metricsReg, pickFirstCentral(cfg))
	mqttSup := newMQTTSupervisor(logger, healthTracker)
	mqttSup.SetCollector(mqttCollector)
	if err := mqttSup.Start(ctx, cfg); err != nil {
		logger.Warn("mqtt.supervisor.start", slog.String("err", err.Error()))
	}
	defer func() { //nolint:contextcheck // shutdown path must not inherit the cancelled daemon ctx
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mqttSup.Shutdown(shutCtx)
	}()
	// Late-bind the supervisor + the live config snapshot into the
	// reload deps bag so the config-watcher's hot-reload handler can
	// issue an MQTT Swap when north.mqtt.* changes and the REST
	// trigger handler can replay the current config on demand. Nil
	// deps (direct daemonServe callers / tests) is fine — Swap simply
	// never fires.
	deps.SetMQTTSupervisor(mqttSup)
	deps.SetCurrentConfig(cfg)
	mqttWiring := mqttSup.Wiring()
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
	// Build the XML-RPC client per (central, interface), wrap it into
	// a backend, register it with the ValueWriter, then pull the device
	// snapshot through the DevicePipeline and load per-channel VALUES
	// paramset descriptions so channels carry their data points.
	//
	// The backup adapter is constructed up-front because WireCentrals
	// injects the live HTTPBackupRestorer into it after the first
	// successful hub handshake.
	backupAdapter := buildBackupAdapter(cfg, reg, logger)
	wireCtx, wireCancel := context.WithTimeout(ctx, 60*time.Second)
	wireTeardown, wireErr := adapter.WireCentrals(wireCtx, cfg, reg, adapter.WireDeps{
		Writer:               valueWriter,
		Translations:         translations,
		CallbackServer:       callbackSrv,
		CallbackBaseURL:      callbackBaseURL,
		BINRPCCallbackServer: binRPCSrv,
		BINRPCCallbackAddr:   binRPCAddr,
		Backup:               backupAdapter,
		Visibility:           visReg,
		MasterValues:         masterValuesStore,
		ValuesCache:          valuesCacheStore,
		ValuesCacheCentralFilter: func(centralName string) bool {
			return cfg.Persistence.ValuesCache.ValuesCacheEnabled(centralName)
		},
	}, logger)
	// Background flusher for the persistent VALUES cache. Runs every
	// flush_interval (default 60 s; override via
	// persistence.values_cache.flush_interval) and once more on
	// graceful shutdown. nil-store / nil-registry guards make this a
	// no-op when the feature is disabled.
	flushInterval := cfg.Persistence.ValuesCache.FlushInterval
	if flushInterval <= 0 {
		flushInterval = adapter.DefaultValuesCacheFlushInterval
	}
	if stopFlusher := adapter.WireValuesCacheFlusher(reg, valuesCacheStore, flushInterval, logger); stopFlusher != nil { //nolint:contextcheck // WireValuesCacheFlusher has no ctx parameter; it creates its own daemon-lifetime context internally
		defer stopFlusher()
	}
	// Surface the values-cache counters as health gauges so the
	// /diagnostics surface and any Prometheus scraper see how many
	// rows survived the last restart, how many got cast-rejected,
	// and how the periodic flusher is doing.
	if healthTracker != nil && valuesCacheStore != nil {
		store := valuesCacheStore
		healthTracker.RegisterGauge("values_cache.restored_rows",
			func() float64 { return float64(store.MetricsSnapshot().RestoredRows) })
		healthTracker.RegisterGauge("values_cache.cast_failures",
			func() float64 { return float64(store.MetricsSnapshot().CastFailures) })
		healthTracker.RegisterGauge("values_cache.gc_rows_deleted",
			func() float64 { return float64(store.MetricsSnapshot().GCRowsDeleted) })
		healthTracker.RegisterGauge("values_cache.flush_batches",
			func() float64 { return float64(store.MetricsSnapshot().FlushBatches) })
		healthTracker.RegisterGauge("values_cache.flushed_entries",
			func() float64 { return float64(store.MetricsSnapshot().FlushedEntries) })
		healthTracker.RegisterGauge("values_cache.row_count",
			func() float64 { //nolint:contextcheck // gauge callback fires on demand; must not inherit the (cancelled) daemon ctx
				gaugeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				stats, err := store.Stats(gaugeCtx)
				if err != nil {
					return 0
				}
				return float64(stats.Rows)
			})
		healthTracker.RegisterGauge("values_cache.value_json_bytes",
			func() float64 { //nolint:contextcheck // gauge callback fires on demand; must not inherit the (cancelled) daemon ctx
				gaugeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				stats, err := store.Stats(gaugeCtx)
				if err != nil {
					return 0
				}
				return float64(stats.ValueJSONSize)
			})
	}
	wireCancel()
	if wireErr != nil {
		logger.Warn("wire.partial", slog.String("err", wireErr.Error()))
	}
	if wireTeardown != nil {
		defer wireTeardown()
	}

	// Wire per-central health subscriptions AFTER WireCentrals so
	// the InterfaceClient registry is populated when the initial-
	// sync pass walks `unit.Clients.List()`. Without this hook,
	// ClientStateChanged / CircuitBreakerStateChanged / Recovery
	// events fly past with no Tracker subscriber, leaving the
	// diagnostics dump's `clients[]` array permanently empty.
	for _, u := range reg.List() {
		_ = adapter.WireHealth(u)
		// WireCentrals connected the interfaces BEFORE WireHealth
		// subscribed, so the startup ClientStateChanged transitions fired
		// with no health subscriber and the central kept the FAILED
		// evaluation taken at boot (before any interface was connected) —
		// surfacing as a permanently "degraded" CCU even though the
		// interfaces are connected and callbacks are flowing. Re-evaluate
		// now that the InterfaceClient registry reflects the connected
		// clients so the central state matches reality.
		u.EvaluateCentralState("post_wire", true)
	}

	// Register a post-stop hook on each central so the shared registry
	// entry is cleaned up after Stop() transitions to STOPPED. This
	// ensures that a central which has been stopped (e.g. due to a
	// fatal init error or a graceful reload) is no longer visible to
	// north-bound adapters that iterate the registry.
	for _, u := range reg.List() {
		centralName := u.Name()
		u.AddOnStopHook(func() {
			reg.Unregister(centralName)
		})
	}

	// Apply the per-central un_ignore lists from SQLite (seeded from
	// config.yaml when the table is empty) onto the shared visibility
	// registry. Runs after WireCentrals so every central's
	// ModelRegistry is populated with materialised devices that the
	// suppression-mark pass can flip. See docs/ui/unignore-concept.md
	// and visibility_wiring.go.
	applyVisibilityUnIgnore(ctx, cfg, reg, visibilityUnIgnoreStore, visReg, logger)

	// Wire device-availability propagation: when an InterfaceClient reports
	// CONNECTED / DISCONNECTED / FAILED, every device on that interface gets its
	// forced-availability override flipped accordingly so HA / REST / SPA stop
	// showing stale "online" entities after a CCU-side disconnect. Per-central
	// because the registry holds one Unit per CCU; closer is chained into
	// the daemon shutdown.
	var availClosers []func()
	for _, u := range reg.List() {
		if closer := adapter.WireDeviceAvailability(u); closer != nil {
			availClosers = append(availClosers, closer)
		}
	}
	defer func() {
		for _, close := range availClosers {
			close()
		}
	}()

	// Wire climate link-peer activity-source refresh: on every successful
	// RecoveryCompletedEvent the wiring re-subscribes all Climate custom DPs
	// on the recovered interface to their linked valve/switch peer channels
	// (LEVEL / STATE) so the activity field stays accurate after a reconnect.
	// LinkPeerChangedEvent re-wires on topology changes (links.add / remove).
	// Per-central, closer chained into daemon shutdown.
	var climateClosers []func()
	for _, u := range reg.List() {
		if closer := adapter.WireClimateLinkPeerRefresh(u); closer != nil {
			climateClosers = append(climateClosers, closer)
		}
	}
	defer func() {
		for _, close := range climateClosers {
			close()
		}
	}()

	// Stamp HubInfo onto the MQTT-Discovery builder now that
	// SystemInformation has been populated by WireCentrals (it set
	// the URL; model / version / serial follow once the JSON-RPC
	// system-info calls land). Without this the per-device
	// `configuration_url` and the synthetic hub device's metadata
	// stay empty.
	//
	// Multi-CCU: each registered central contributes its own HubInfo
	// entry. The discovery builder looks it up per `central` argument
	// so two CCUs display the right Name / Model / sw_version /
	// serial / configuration_url in HA.
	if mqttWiring != nil {
		bridge := mqttWiring.Bridge()
		for _, u := range reg.List() {
			si := u.SystemInformation()
			bridge.SetHubInfoFor(u.Name(), mqtt.HubInfo{
				Name:    u.Name(),
				Model:   si.Model,
				Version: si.Version,
				Serial:  si.Serial,
				URL:     si.URL,
			})
		}
	}

	// Boot-time stale cleanup — clear retained channel-aggregate
	// state topics from the previous build before the inventory wave.
	// Necessary when the discovery payload structure changes and
	// brokers still hold incompatible JSON: HA refuses to bind the
	// stale message and the entity stays unavailable until the
	// retained content is replaced. Runs against the legacy aggregate
	// shape (`<base>/<central>/<iface>/<addr>/<ch>/state`) — the
	// follow-up PublishInitialSnapshot republishes the current view
	// for every observed DP.
	//
	// Best-effort: a broker that doesn't support the cleanup
	// subscription path returns errCleanupClientLacksSubscribe; the
	// daemon logs and proceeds.
	if mqttWiring != nil {
		if mqttBridge := mqttWiring.Bridge(); mqttBridge != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(ctx, 10*time.Second)
			n, cleanupErr := mqttBridge.RunRetainCleanupOnce(cleanupCtx, 2*time.Second)
			cleanupCancel()
			if cleanupErr != nil {
				logger.Warn("mqtt.retain_cleanup", slog.String("err", cleanupErr.Error()))
			} else if n > 0 {
				logger.Info("mqtt.retain_cleanup", slog.Int("evicted", n))
			}
		}
	}

	// Push the post-hydration snapshot of every observed VALUES data
	// point through the EventBridge so the broker carries retained
	// state (and HA Discovery configs) for every device immediately
	// after start, not just after the first CCU-driven change.
	bridge.PublishInitialSnapshot(ctx)

	// Orphan HA-Discovery cleanup — after the initial snapshot has
	// repopulated `Bridge.declared` with every HA-Discovery topic the
	// daemon currently owns, evict every retained `homeassistant/...`
	// config topic for our device-namespace that is *not* in
	// `declared`. Catches entities that previous builds published
	// (e.g. MASTER-paramset spam for unlisted models like
	// HmIP-STE2-PCB / HmIP-SFD before the default-skip rule landed,
	// or BOOST_TIME_PERIOD on HmIP-BWTH before the MASTER-lookup fix);
	// without this pass the broker keeps the orphans retained
	// indefinitely and HA shows phantom entities the daemon no longer
	// drives. Best-effort — a broker that lacks subscribe support
	// just skips.
	if mqttWiring != nil {
		if mqttBridge := mqttWiring.Bridge(); mqttBridge != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(ctx, 10*time.Second)
			n, cleanupErr := mqttBridge.RunDiscoveryOrphanCleanupOnce(cleanupCtx, 2*time.Second)
			cleanupCancel()
			if cleanupErr != nil {
				logger.Warn("mqtt.discovery_orphan_cleanup", slog.String("err", cleanupErr.Error()))
			} else if n > 0 {
				logger.Info("mqtt.discovery_orphan_cleanup", slog.Int("evicted", n))
			}
		}
	}

	// Periodic unobserved-DP sweep — retries LoadValue for the
	// RELEVANT_INIT + readable-event whitelist on a slow cadence.
	// Catches stragglers that fetch_all_device_data omitted (event
	// DPs that have not fired) and fills any holes that opened
	// during a brief CCU outage between push events. 5-minute
	// cadence (DefaultUnobservedSweepInterval) — long enough that
	// steady-state operation costs nothing extra on the CCU.
	unobservedSweep := adapter.NewUnobservedSweep(reg, logger)
	stopSweep := adapter.StartUnobservedSweepLoop(
		ctx, unobservedSweep, 0, logger,
	)
	defer stopSweep()

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
	servers := newServerGroup(logger)
	if auditDB != nil && healthTracker != nil {
		stopProbe := sqlitestore.StartHealthProbe(ctx, auditDB, healthTracker, sqlitestore.DefaultProbeInterval)
		_ = stopProbe // daemon shutdown handled by the parent context cancel
	}

	if auditDB != nil {
		// One-shot seed: if SQLite users table is empty AND the
		// YAML carries legacy auth.users, copy them in so the
		// admin-edit surface starts pre-populated. Idempotent —
		// subsequent boots see Count() > 0 and skip the seed.
		if n, err := sqUsers.Count(ctx); err == nil && n == 0 {
			for name, pass := range cfg.North.REST.Auth.Users {
				if perr := sqUsers.Put(ctx, name, pass, auth.RoleAdmin); perr != nil {
					logger.Warn("auth.seed.user", slog.String("subject", name), slog.String("err", perr.Error()))
				}
			}
		}

		// Same idempotent seed for the centrals table: if SQLite is
		// empty AND the YAML lists at least one CCU, copy the list
		// into the centrals table so the SPA's CCUs tab shows the
		// running config from the first boot. After that, edit
		// authoritatively via the SPA.
		if n, err := sqCentrals.Count(ctx); err == nil && n == 0 {
			for i := range cfg.Centrals {
				cc := &cfg.Centrals[i]
				row := sqlitestore.CentralRow{
					Name:                  cc.Name,
					Host:                  cc.Host,
					Port:                  cc.Port,
					JSONRPCPort:           cc.JSONRPCPort,
					Username:              cc.Username,
					PasswordPlain:         cc.Password, // YAML password becomes the SQLite default
					TLS:                   cc.TLS,
					TLSInsecureSkipVerify: cc.TLSInsecureSkipVerify,
					PrimaryInterface:      cc.PrimaryInterface,
					Interfaces:            cc.Interfaces,
					Ports:                 cc.Ports,
					Visibility:            cc.Visibility,
					Enabled:               true,
				}
				if perr := sqCentrals.Put(ctx, row); perr != nil {
					logger.Warn("centrals.seed", slog.String("name", cc.Name), slog.String("err", perr.Error()))
				}
			}
		}

		// Layer SQLite stores on top of the Memory stores for
		// authentication so wizard-created users + YAML-pinned
		// users both resolve. The chain prefers SQLite; falls back
		// to Memory only on a clean "unauthenticated" miss.
		authMw = auth.NewMiddleware(
			auth.ChainedUserStore{Primary: sqUsers, Secondary: users},
			auth.ChainedTokenStore{Primary: sqTokens, Secondary: tokens},
		)
		// Re-bind the resolver after swapping the middleware so the
		// REST chain picks up the chained stores.
		restResolve = func(next http.Handler) http.Handler {
			return authMw.Resolve(sessionResolve(next))
		}
	}

	// REST status metrics — 5xx/4xx counters surfaced as health
	// gauges. Wired into the chi middleware chain via Deps.StatusMetrics
	// and read back through RegisterGauge so the diagnostics dump and
	// the SPA's Diagnostics view can render the values.
	restStatusMetrics := middleware.NewStatusMetrics()
	if healthTracker != nil {
		sm := restStatusMetrics
		healthTracker.RegisterGauge("rest.5xx",
			func() float64 { return float64(sm.ServerErrors()) })
		healthTracker.RegisterGauge("rest.4xx",
			func() float64 { return float64(sm.ClientErrors()) })
		healthTracker.RegisterGauge("rest.requests_total",
			func() float64 { return float64(sm.TotalRequests()) })
	}

	restAuth := &handlers.AuthDeps{
		Users:         users,
		Sessions:      sessions,
		Tokens:        tokens,
		Secure:        false, // dev/plain HTTP; flip when TLS is wired
		AuditRecorder: auditBuf,
	}
	// When SQLite-backed user persistence is available, route the
	// /auth/login resolver through the chained store so
	// wizard-created admins and YAML-pinned users both
	// authenticate. The legacy /auth/users read path continues to
	// hit the in-memory snapshot via AuthDeps.Users.
	if sqUsers != nil {
		restAuth.LoginUsers = auth.ChainedUserStore{Primary: sqUsers, Secondary: users}
	}

	// Wave-C admin services backed by the SQLite stores opened
	// above. Each handler-side interface (ConfigAdminService /
	// UserAdminService / TokenAdminService / CentralAdminService)
	// is satisfied directly by the corresponding *sqlite.Store —
	// no extra adapter required.
	var (
		configAdminSvc  handlers.ConfigAdminService
		userAdminSvc    handlers.UserAdminService
		tokenAdminSvc   handlers.TokenAdminService
		centralAdminSvc handlers.CentralAdminService
	)
	if configStore != nil {
		configAdminSvc = configAdminAdapter{store: configStore, sections: sqSections}
		userAdminSvc = sqUsers
		tokenAdminSvc = sqTokens
		centralAdminSvc = sqCentrals
	}

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
