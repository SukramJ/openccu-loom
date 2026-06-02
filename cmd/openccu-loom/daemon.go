// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/auth/oidc"
	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
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
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"

	// Side-effect import: aggregator package whose blank-imports
	// trigger every custom-DP sub-package's `init()` so the global
	// constructor catalogue is populated before the device pipeline
	// runs. See [internal/model/custom/builtins].
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/builtins"
	discoverymdns "github.com/SukramJ/openccu-loom/internal/north/discovery/mdns"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/matter/bootid"
	matterbridge "github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	matterwire "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/eligibility"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
	matterschema "github.com/SukramJ/openccu-loom/internal/north/matter/schema"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/attestation"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/mattercert"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/operational"
	mattersetup "github.com/SukramJ/openccu-loom/internal/north/matter/secure/setup"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/sigma"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/spake2"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
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
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// bridgeSessionParameters returns the SessionParameters struct the
// CASE responder emits as Sigma2 context-tag 5 (`responderSessionParams`)
// per Matter §4.14.2.3. Mirrors the bridge's operational `_matter._tcp`
// mDNS advertisement so the commissioner's post-CASE MRP timer aligns
// with the discovery-time hint.
//
// The numeric values match the operational SII/SAI/SAT defaults from
// `mdns/service.go`: SII=500ms, SAI=300ms, SAT=4000ms.
func bridgeSessionParameters() *sigma.SessionParameters {
	return &sigma.SessionParameters{
		SessionIdleInterval:    500,
		SessionActiveInterval:  300,
		SessionActiveThreshold: 4000,
	}
}

// daemonServe boots the full process: config → registry → REST + UI
// + MQTT servers → blocks on ctx.Done(). It is the real composition
// root callers reach through `openccu-loom run --config path.yaml`.
//
// Production wires ctx via [signal.NotifyContext] so SIGINT / SIGTERM
// triggers shutdown. Tests pass a cancellable ctx and call cancel()
// instead of delivering a signal — that avoids syscall.Kill against
// the test process's own PID, which would tear down the test
// framework itself.
func daemonServe(ctx context.Context, cfg *config.Config, stdout, stderr io.Writer) error {
	return daemonServeWithDeps(ctx, cfg, stdout, stderr, nil)
}

// daemonServeWithDeps is the real composition root. It accepts an
// optional [reloadDeps] bag so the config-watcher's reload handler
// can reach live subsystems (currently: the MQTT supervisor) after
// boot completes. Direct callers — tests and the no-watcher path —
// pass nil and skip the late binding.
func daemonServeWithDeps(ctx context.Context, cfg *config.Config, stdout, _ io.Writer, deps *reloadDeps) error {
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
	auditRec, auditDB, auditDurableStats := wireAuditPersistenceWithDB(cfg, auditBuf, logger)
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
	reg, teardown, err := bootstrap.Build(context.Background(), cfg)
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
	for _, c := range reg.List() {
		c.WireDevicesCreatedGate()
	}

	// Register the standard per-central background jobs BEFORE
	// StartAll fires the scheduler. Without this, the
	// `central.health_heartbeat` job never runs and the per-central
	// "central" component decays to UNKNOWN ~90 s after boot via the
	// tracker's StaleAfter rule. `central.check_connection` is the
	// other unconditional job — it advances each interface's
	// circuit-breaker OPEN → HALF_OPEN → CLOSED on its own probe
	// cadence, no caller need invoke it.
	for _, c := range reg.List() {
		jobs := central.StandardJobs{}
		// Apply per-central overrides from the configuration. Currently
		// only check_connection_interval is overridable; zero means "use
		// the compiled-in default".
		for i := range cfg.Centrals {
			if cfg.Centrals[i].Name == c.Name() && cfg.Centrals[i].CheckConnectionInterval > 0 {
				jobs.CheckConnectionInterval = cfg.Centrals[i].CheckConnectionInterval
			}
		}
		if c.Hub != nil {
			// Hub-Refresh-Hooks delegate through the HubCoordinator's
			// RefreshXxx methods. The inner hooks (loadPrograms,
			// loadSysvars, …) are wired by WireHub after the JSON-RPC
			// session comes up; until then RefreshXxx returns nil. By
			// registering the jobs unconditionally here, the scheduler
			// picks up the cadence and starts firing the moment WireHub
			// installs the closures.
			jobs.ProgramRefresh = c.Hub.RefreshPrograms
			jobs.SysvarRefresh = c.Hub.RefreshSysvars
			jobs.InboxRefresh = c.Hub.RefreshInbox
			jobs.ServiceMessagesRefresh = c.Hub.RefreshServiceMessages
			jobs.AlarmMessagesRefresh = c.Hub.RefreshAlarmMessages
			jobs.SystemUpdateRefresh = c.Hub.RefreshSystemUpdate
			jobs.InstallModeRefresh = c.Hub.RefreshInstallMode
			jobs.HubMetricsRefresh = c.Hub.RefreshMetrics
			jobs.HubConnectivityRefresh = c.Hub.RefreshConnectivity
		}
		// Wire and register the Reconciler so the slow-cadence
		// connectivity/health pass emits ConnectivityChangedEvent on
		// drift. The Connectivity and Metrics slots come from the Hub
		// aggregate (wired by WireHub once the JSON-RPC session is up,
		// nil-tolerant before then). Without these the per-job
		// reconcileConnectivity / reconcileSystemHealth passes would
		// land on nil and short-circuit — the slow drift sweep would
		// never fire even though the job slot was registered.
		if c.Reconciler == nil {
			c.Reconciler = &coordinators.Reconciler{
				CentralName:  c.Name(),
				Bus:          c.EventBus,
				Connectivity: c.HubModel.ConnectivityDataPoints(),
				Metrics:      c.HubModel.Metrics,
			}
		}
		jobs.Reconcile = c.Reconciler.Reconcile
		if _, err := central.RegisterStandardJobs(c, jobs); err != nil {
			logger.Warn("central.standard_jobs.register_failed",
				slog.String("central", c.Name()),
				slog.String("err", err.Error()))
		}
	}

	if err := reg.StartAll(context.Background()); err != nil {
		return fmt.Errorf("central start: %w", err)
	}

	// Wire SQLite-backed disk-persistence for every central's
	// SessionRecorder. The recorder itself stays inactive until an
	// operator activates it via REST/WS — the wiring only ensures that,
	// when active, captured sessions survive a daemon restart.
	// production-replay path. Closer is chained into shutdown.
	if recorderPersistTeardown := wireSessionRecorderPersistence(cfg, reg, logger); recorderPersistTeardown != nil {
		defer recorderPersistTeardown()
	}
	// Wire SQLite-backed incident recording into every central's
	// CacheCoordinator. CallbackHandlers reads the recorder lazily from
	// CacheCoordinator.GetIncidentRecorder(), so no separate handler-level
	// wiring step is needed. Degrades gracefully when the DB cannot be opened.
	defer wireIncidentRecorder(cfg, reg, logger)()

	// Seed every central's health tracker with a synthetic "started"
	// sample so the `/health` endpoint reports green as soon as the
	// daemon hits its signal loop.
	obsRecorder := observability.LogRecorder{Logger: logger.With(slog.String("component", "observability"))}
	for _, c := range reg.List() {
		c.Health.Record("central", health.Sample{Healthy: true, Note: "started"})
		c.SetObservabilityRecorder(obsRecorder)

		// Pin the primary interface explicitly when the operator
		// configured `primary_interface` for this central. Empty
		// (default) keeps the built-in HmIP-RF substring heuristic.
		// Multi-CCU setups with HmIP-Wired-only or BidCos-only
		// installations rely on this to score the right interface
		// as the central's primary.
		for i := range cfg.Centrals {
			if cfg.Centrals[i].Name == c.Name() && cfg.Centrals[i].PrimaryInterface != "" {
				c.Health.SetPrimaryInterface(cfg.Centrals[i].PrimaryInterface)
				logger.Info("health.primary_interface.pinned",
					slog.String("central", c.Name()),
					slog.String("interface", cfg.Centrals[i].PrimaryInterface))
				break
			}
		}

		// Surface the event-bus deferred high-water gauge through the
		// health tracker so admin endpoints can alert on pathological
		// handler recursion without owning the events package directly.
		bus := c.EventBus
		c.Health.RegisterGauge("event_bus.deferred_depth",
			func() float64 { return float64(bus.DeferredDepth()) })
		c.Health.RegisterGauge("event_bus.deferred_high_water",
			func() float64 { return float64(bus.DeferredHighWater()) })
		// Audit durability telemetry (O12 / R11). Surfaces the durable-
		// sink overflow counter so admin endpoints can alert on
		// audit-row loss before the database falls behind. Skipped
		// when the durable sink was not wired (in-memory-only audit).
		if auditDurableStats != nil {
			s := auditDurableStats
			c.Health.RegisterGauge("audit.dropped",
				func() float64 { return float64(s.Dropped()) })
			c.Health.RegisterGauge("audit.sink_errors",
				func() float64 { return float64(s.SinkErrors()) })
		}
		// Scheduler coverage: job-count + cumulative failure counter
		// (errors + recovered panics). Per-job breakdown is reachable
		// via the diagnostics-dump component map; the gauge gives the
		// SPA a single number for the at-a-glance tile.
		if c.Scheduler != nil {
			scheduler := c.Scheduler
			c.Health.RegisterGauge("scheduler.jobs",
				func() float64 { return float64(len(scheduler.Jobs())) })
			c.Health.RegisterGauge("scheduler.failures",
				func() float64 { return float64(scheduler.TotalFailures()) })
		}

		// Wire a per-central metrics aggregator. The Observer subscribes to
		// the central's EventBus so metric events published by clients and
		// coordinators are automatically funnelled into the snapshot.
		// All providers are wired with the components owned by this central;
		// nil providers are safe — Aggregator degrades to zero-value sections.
		obs := metrics.NewObserver()
		unsubMetrics := metricswiring.SubscribeObserver(c.EventBus, obs)
		_ = unsubMetrics // lifetime matches the central; detach on shutdown is best-effort

		agg := metrics.NewAggregator(
			c.Name(), obs,
			metrics.WithClientProvider(metricswiring.NewClientProvider(c.MetricsClients)),
			metrics.WithCacheProvider(metricswiring.NewCacheProvider(c.Cache)),
			metrics.WithRecoveryProvider(metricswiring.NewRecoveryProvider(c.Recovery)),
			metrics.WithEventBus(metricswiring.NewEventBusProvider(c.EventBus)),
			metrics.WithHealthTracker(metricswiring.NewHealthProvider(c.Health, c.Recovery)),
		)
		c.SetAggregator(agg)
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
	visibilityUnIgnoreStore := wireVisibilityUnIgnoreStore(cfg, logger)
	defer func() { _ = visibilityUnIgnoreStore.Close() }()
	visibilityAdapter := newVisibilityAdapter(visReg, visibilityUnIgnoreStore, reg)
	masterValuesStore := wireMasterValuesStore(cfg, logger)
	defer func() { _ = masterValuesStore.Close() }()
	valuesCacheStore := wireValuesCacheStore(cfg, logger)
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
	if err := mqttSup.Start(context.Background(), cfg); err != nil {
		logger.Warn("mqtt.supervisor.start", slog.String("err", err.Error()))
	}
	defer func() {
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
	bridge.Start(context.Background())
	defer bridge.Stop()

	// Wire hub entity → MQTT publisher. Only active when MQTT is
	// configured; guards on mqttWiring == nil so the daemon degrades
	// gracefully without a broker. Re-fires on every broker reconnect
	// so retained hub state (sysvars, programs, alarm/service messages)
	// is restored after the broker drops its retained store or the
	// supervisor swaps the stack — Start() is idempotent (Stop+rewire).
	if mqttWiring != nil {
		hubMQTT := adapter.NewHubMQTTPublisher(reg, mqttWiring, logger)
		hubMQTT.Start(context.Background())
		defer hubMQTT.Stop()
		mqttSup.OnConnect(func(ctx context.Context) {
			hubMQTT.Start(ctx)
		})
	}

	// --- XML-RPC callback server -------------------------------
	// Shared across every central; routes by `/RPC2/<central_name>`.
	// A binding failure is a hard error — the daemon would otherwise
	// silently lose every CCU value-change event.
	callbackCtx, cancelCallback := context.WithCancel(context.Background())
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
		srv, binErr := rpcserver.NewBINRPCServer(binCfg)
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
				publicHost = autodetectCallbackHost(cfg)
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
	wireCtx, wireCancel := context.WithTimeout(context.Background(), 60*time.Second)
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
		ValuesCacheCentralFilter: func(central string) bool {
			return cfg.Persistence.ValuesCache.ValuesCacheEnabled(central)
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
	if stopFlusher := adapter.WireValuesCacheFlusher(reg, valuesCacheStore, flushInterval, logger); stopFlusher != nil {
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
			func() float64 {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				stats, err := store.Stats(ctx)
				if err != nil {
					return 0
				}
				return float64(stats.Rows)
			})
		healthTracker.RegisterGauge("values_cache.value_json_bytes",
			func() float64 {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				stats, err := store.Stats(ctx)
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
	for _, c := range reg.List() {
		_ = adapter.WireHealth(c)
		// WireCentrals connected the interfaces BEFORE WireHealth
		// subscribed, so the startup ClientStateChanged transitions fired
		// with no health subscriber and the central kept the FAILED
		// evaluation taken at boot (before any interface was connected) —
		// surfacing as a permanently "degraded" CCU even though the
		// interfaces are connected and callbacks are flowing. Re-evaluate
		// now that the InterfaceClient registry reflects the connected
		// clients so the central state matches reality.
		c.EvaluateCentralState("post_wire", true)
	}

	// Register a post-stop hook on each central so the shared registry
	// entry is cleaned up after Stop() transitions to STOPPED. This
	// ensures that a central which has been stopped (e.g. due to a
	// fatal init error or a graceful reload) is no longer visible to
	// north-bound adapters that iterate the registry.
	for _, c := range reg.List() {
		centralName := c.Name()
		c.AddOnStopHook(func() {
			reg.Unregister(centralName)
		})
	}

	// Apply the per-central un_ignore lists from SQLite (seeded from
	// config.yaml when the table is empty) onto the shared visibility
	// registry. Runs after WireCentrals so every central's
	// ModelRegistry is populated with materialised devices that the
	// suppression-mark pass can flip. See docs/ui/unignore-concept.md
	// and visibility_wiring.go.
	applyVisibilityUnIgnore(context.Background(), cfg, reg, visibilityUnIgnoreStore, visReg, logger)

	// Wire device-availability propagation: when an InterfaceClient reports
	// CONNECTED / DISCONNECTED / FAILED, every device on that interface gets its
	// forced-availability override flipped accordingly so HA / REST / SPA stop
	// showing stale "online" entities after a CCU-side disconnect. Per-central
	// because the registry holds one Unit per CCU; closer is chained into
	// the daemon shutdown.
	var availClosers []func()
	for _, c := range reg.List() {
		if closer := adapter.WireDeviceAvailability(c); closer != nil {
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
	for _, c := range reg.List() {
		if closer := adapter.WireClimateLinkPeerRefresh(c); closer != nil {
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
		for _, cu := range reg.List() {
			si := cu.SystemInformation()
			bridge.SetHubInfoFor(cu.Name(), mqtt.HubInfo{
				Name:    cu.Name(),
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
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	bridge.PublishInitialSnapshot(context.Background())

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
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
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
		context.Background(), unobservedSweep, 0, logger,
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
	if err := mqttSup.AttachSubscribers(context.Background()); err != nil {
		logger.Warn("mqtt.subscribers.attach", slog.String("err", err.Error()))
	}

	// --- Matter bridge ----------------------------------------
	// Feature-flagged. When matter.enabled is set we stand up
	// the UDP listener, assemble the endpoint topology, and publish
	// the operational mDNS record. Failure here never aborts the
	// daemon — the bridge is best-effort until GA.
	var (
		matterFabricStore   handlers.MatterFabricStore
		matterOpener        handlers.MatterCommissioningOpener
		matterStatusReader  handlers.MatterStatusReader
		matterFabricRevoker handlers.MatterFabricRevoker
		matterCloser        handlers.MatterCommissioningCloser
		matterExposureStore handlers.MatterExposureStore
		matterCandidates    handlers.MatterCandidateProvider
		matterPub           *matterEventPublisher
		matterReassembler   handlers.MatterTopologyReassembler
		matterBI            *mattercore.BasicInformation // captured for ShutDown emit
	)
	if bundle := startMatterBridge(ctx, cfg, reg, healthTracker, logger); bundle != nil {
		mb := bundle.bridge
		mfs := bundle.store
		defer bundle.stop()
		matterFabricStore = mfs
		matterExposureStore = mfs
		matterReassembler = mb
		// Wire the allowlist checker so the assembler only bridges
		// sources that the operator has explicitly enabled. Default
		// = empty allowlist = empty topology. The exposure-management
		// REST endpoints (`/api/v1/matter/exposable`) drive the
		// matter_exposures table the checker reads through.
		//
		// Bridge.Start has already assembled the initial topology with
		// the allow-all default, so we trigger a reassemble here to
		// discard the over-broad endpoint set and rebuild scoped to
		// the persisted exposures. Without this Apple Home sees every
		// CCU device as a Matter endpoint (1000+), the initial
		// Subscribe expands to 60+ KB, the post-CASE phase exceeds
		// Apple's pairing-UI timeout and the user sees a generic
		// add-failed error even though the cryptographic handshake completed.
		mb.AttachExposureChecker(mfs)
		if err := mb.Reassemble(ctx); err != nil {
			logger.Warn("matter.bridge.reassemble.after_exposure_checker",
				slog.String("err", err.Error()))
		}

		// Wire CCU device-availability → BridgedDeviceBasicInformation
		// Reachable. WireDeviceAvailability (above) publishes a
		// DeviceLifecycleEvent{AvailabilityChanged} on each central's bus
		// whenever a device's effective availability flips (interface
		// disconnect, STICKY_UNREACH, reconnect). Forward that to the
		// bridge so the matching bridged endpoint fires the §9.13.6
		// ReachableChanged event — without this Apple/Google always see
		// every bridged device as reachable even when the CCU device is
		// dead. The Reachable attribute itself reads dev.Available() live
		// per dispatch (endpoint/materialize.go); this supplies the push
		// half so active subscriptions are notified, not just polled reads.
		for _, c := range reg.List() {
			if c == nil || c.EventBus == nil {
				continue
			}
			cName := c.Name()
			unsub := events.Subscribe(c.EventBus, func(e hmevent.DeviceLifecycleEvent) {
				if e.Subtype != hmenum.DeviceLifecycleSubtypeAvailabilityChanged {
					return
				}
				mb.NotifyDeviceReachable(cName, e.Address, e.Available)
			})
			availClosers = append(availClosers, unsub)
		}
		// Wire the Root Descriptor's dynamic PartsList provider to
		// the bridge's live topology so `0:0x001D:0x0003` reflects the
		// freshly-assembled bridged endpoints. Apple Home reads
		// PartsList after CommissioningComplete; an empty list makes
		// the bridge look like an empty RootNode and Apple's UI ends
		// the commissioning with a generic add-failed error.
		// Root.Descriptor.PartsList — flat tree of all descendant
		// endpoints (Aggregator + bridged), matching matter.js HEAD's
		// `DescriptorServer.#updatePartsList` (packages/node/src/
		// behaviors/descriptor/DescriptorServer.ts:185-209) when
		// IndexBehavior is mounted on Root. matter.js's
		// `examples/device-bridge-onoff` Sample emits
		// `Root.PartsList=[1,2,3]` (Aggregator + 2 bridged children)
		// and pairs with Apple Home successfully — verified empirically
		// (matter.js byte-dump via InteractionMessenger.ts:681 hook).
		// Earlier `[1]`-only experiments produced the same Apple-Cache
		// `count: 3` symptom, so flat-tree is the matter.js-parity
		// answer, not the workaround.
		mb.AttachRootPartsListProvider(func() []uint16 {
			topo := mb.Topology()
			if topo == nil {
				return nil
			}
			ids := make([]uint16, 0, len(topo.Endpoints))
			for _, ep := range topo.Endpoints {
				if ep.IsRoot() {
					continue
				}
				ids = append(ids, ep.ID)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			return ids
		})
		// Aggregator endpoint (EP 1) PartsList: every bridged endpoint
		// (ID ≥ 2). Mirrors matter.js's `Aggregator.parts = [bridged
		// children]` (AggregatorEndpoint requirements list Parts as
		// mandatory in `packages/node/src/endpoints/aggregator.ts`).
		mb.AttachAggregatorPartsListProvider(func() []uint16 {
			topo := mb.Topology()
			if topo == nil {
				return nil
			}
			ids := make([]uint16, 0, len(topo.Endpoints))
			for _, ep := range topo.Endpoints {
				if ep.IsRoot() || ep.IsAggregator() {
					continue
				}
				ids = append(ids, ep.ID)
			}
			return ids
		})
		// Build the commissioning-window opener using the configured
		// PASE parameters. The opener reuses the bridge's already-open
		// PASE acceptor; it tracks the window state and emits
		// QR + manual codes for the caller (REST handler).
		window := matterbridge.NewCommissioningWindow()
		mb.AttachCommissioningWindow(window)
		// Wire FailSafeArmer and PaseSessionCloser hooks so CommissioningWindow
		// can arm the Matter fail-safe after opening a window (Matter §11.19.6)
		// and evict open PASE sessions when the window is revoked
		// (Matter §11.19.7.3 step 1). Both adapters delegate to their
		// respective production paths — see [failSafeArmerAdapter] and
		// [paseSessionCloserAdapter].
		window.SetFailSafeChecker(bundle.rootRefs.GeneralCommissioning)
		window.SetFailSafeArmer(&failSafeArmerAdapter{
			gc:     bundle.rootRefs.GeneralCommissioning,
			logger: logger,
		})
		window.SetPaseSessionCloser(&paseSessionCloserAdapter{
			opMgr:  bundle.opMgr,
			logger: logger,
		})
		opener := matterbridge.NewCommissioningWindowOpener(
			window,
			cfg.North.Matter.Discriminator,
			cfg.North.Matter.Commissioning.Passcode,
			cfg.North.Matter.VendorID,
			cfg.North.Matter.ProductID,
		)
		// Matter §4.3.1.5 requires a random 64-bit hex Instance Name on
		// every commissionable record. A fixed/zero value lets Apple
		// Home cache the lookup result across reboots and reject the
		// pairing as "already known"; rolling a fresh ID per process
		// boot avoids the stale-cache trap.
		var instanceID [8]byte
		if _, err := rand.Read(instanceID[:]); err != nil {
			logger.Warn("matter.bridge.instance_id.rand_failed", slog.String("err", err.Error()))
		}
		// Rotating Device Identifier per Matter §5.4.2.4. UniqueID is
		// derived stably from the bridge identity (VendorID, ProductID,
		// SerialNumber, NodeLabel) so the value survives daemon restarts
		// without an extra persistence slot. LifetimeCounter stays at 0
		// in 0.1.0; future iterations bump it on fabric-change events.
		rotatingUniqueID := mdns.DeriveUniqueIDFromIdentity(
			cfg.North.Matter.VendorID,
			cfg.North.Matter.ProductID,
			rotatingSerialPart(cfg.North.Matter.VendorID, cfg.North.Matter.ProductID, cfg.North.Matter.NodeLabel),
			cfg.North.Matter.NodeLabel,
		)
		rotatingID := mdns.GenerateRotatingID(rotatingUniqueID, 0)

		matterOpener = &matterCommissioningOpenerAdapter{
			inner:  opener,
			bridge: mb,
			advert: matterbridge.CommissioningAdvertisement{
				InstanceID:        instanceID,
				Discriminator:     cfg.North.Matter.Discriminator,
				VendorID:          cfg.North.Matter.VendorID,
				ProductID:         cfg.North.Matter.ProductID,
				NodeLabel:         cfg.North.Matter.NodeLabel,
				RotatingID:        rotatingID,
				CommissioningMode: 1, // §4.3.1.4 CM=1: open commissioning window
				// DT TXT-Record advertises the *primary* device-type
				// of the Node. matter.js's bridge sample sets it to
				// AggregatorEndpoint.deviceType (0x000E); we tested
				// 0x000E empirically against an iPhone with all four
				// fix-stack items in place (ACL, NetworkCommissioning,
				// 500ms chunk wait, ArmFailSafe→OpCreds reset) and
				// Apple's HMMTRBridgeDeviceTypeDeterminer still
				// produced endpointDeviceTypes={0=(22)} — the value
				// is not driven by DT. Side-effect of DT=0x000E was
				// that Apple skipped the SystemCommissioner pair
				// (no AddingSystemCommissioner state, no second AddNOC
				// for vendor 0x1384) which removed the second HAP
				// rebuild attempt and made the pair worse rather
				// than better. Kept at RootNode pending a wire-level
				// diff against a matter.js sample bridge.
				DeviceTypeID: 0x0016, // §10.3 RootNode — DT=0x000E empirically worse for Apple Multi-Admin flow
			},
		}
		// Withdraw the commissionable record on every transition into
		// "closed" so commissioners stop discovering the bridge once
		// the window has expired or been revoked.
		window.SetTransitionHook(func() {
			if mb == nil {
				return
			}
			if window.CurrentWindow().Status != matterwire.WindowStatusEnhanced {
				mb.WithdrawCommissioning(context.Background())
			}
		})

		// Ephemeral-window mode: each OpenCommissioningWindow call
		// generates a fresh discriminator + passcode + Spake2+ verifier
		// and swaps it onto the bridge's PASE adapter for the window
		// duration. Works with both `concurrent_pairings=false`
		// (singleton swap) and `concurrent_pairings=true` (per-exchange
		// PaseAdapter factory installed for the window).
		if cfg.North.Matter.Commissioning.EphemeralWindow {
			var configuredFactory func() *matterbridge.PaseAdapter
			if cfg.North.Matter.Commissioning.ConcurrentPairings {
				// Capture the operator's configured per-exchange factory
				// so the Restore closure can re-install it after the
				// ephemeral window closes.
				cmCopy := cfg.North.Matter.Commissioning
				opMgrLocal := bundle.opMgr
				opCredsLocal := bundle.opCreds
				gcLocal := bundle.rootRefs.GeneralCommissioning
				loggerLocal := logger
				configuredFactory = func() *matterbridge.PaseAdapter {
					a, err := buildPaseAdapter(cmCopy, opMgrLocal, opCredsLocal, gcLocal, loggerLocal)
					if err != nil {
						loggerLocal.Warn("matter.bridge.pase.build", slog.String("err", err.Error()))
						return nil
					}
					return a
				}
			}
			ephem := newMatterEphemeralProvider(mb, cfg.North.Matter.Commissioning, bundle.opMgr, bundle.opCreds, bundle.configuredPase, configuredFactory, logger)
			opener.SetEphemeralProvider(ephem)
			logger.Info("matter.bridge.pase.ephemeral_armed",
				slog.Bool("configured_fallback", bundle.configuredPase != nil),
				slog.Bool("concurrent_pairings", cfg.North.Matter.Commissioning.ConcurrentPairings))
		}
		// Wire the Reassemble → WS event emit pipeline so the SPA's
		// allowlist save flow gets a `matter.endpoint_assembled`
		// notification after the topology refresh completes.
		matterPub = &matterEventPublisher{hub: wsHub}
		// Composite onReassembled: WS event publish + Matter-spec
		// lifecycle events (BootReason + StartUp) on the FIRST
		// reassemble, when the cluster servers are wired to the
		// emitter pipeline. matter.js fires these on the equivalent
		// "behavior pipeline ready" hook (NodeServer.run).
		var reassembleOnce sync.Once
		biRef := bundle.rootRefs.BasicInformation
		gdRef := bundle.rootRefs.GeneralDiagnostics
		matterBI = biRef
		mb.SetOnReassembled(func(count int) {
			matterPub.publishEndpointAssembled(count)
			reassembleOnce.Do(func() {
				if gdRef != nil {
					gdRef.EmitBootReason()
				}
				if biRef != nil {
					biRef.EmitStartUp()
				}
			})
		})
		// IMPORTANT: SetOnReassembled is wired AFTER mb.Reassemble(ctx)
		// (called earlier in this function) — the bridge's first
		// topology-assembly pass therefore fires the hook with the
		// then-nil closure and the StartUp / BootReason events never
		// land in EventLog. Apple Home's MTRDevice waits for those
		// Critical events as part of its Subscribe-Initial state-
		// machine (verified via byte-diff
		// against matter.js Sample): without them the controller
		// transitions state `Subscribing` → `Unsubscribed` instead of
		// `Subscribing` → `InitialSubscriptionEstablished`, persists
		// only 3 cluster_information records instead of ~21, and
		// surfaces the bridge as "added but not supported".
		// Trigger the same once-only emit path the SetOnReassembled
		// callback would have triggered, now that the hook is wired
		// and the cluster-server emitters are bound via the topology
		// assembler.
		reassembleOnce.Do(func() {
			if gdRef != nil {
				gdRef.EmitBootReason()
			}
			if biRef != nil {
				biRef.EmitStartUp()
			}
		})
		mb.SetOnFabricAdded(matterPub.publishFabricAdded)
		mb.SetOnFabricRemoved(matterPub.publishFabricRemoved)
		matterStatusReader = &matterStatusReaderAdapter{
			enabled: cfg.North.Matter.Enabled,
			bridge:  mb,
			store:   mfs,
			window:  window,
			cfg: &matterStatusConfig{
				advertising: cfg.North.Matter.MDNSAdvertise == "zeroconf",
			},
		}
		if healthTracker != nil {
			_ = startMatterHealthProbe(ctx, matterStatusReader, healthTracker, matterHealthProbeInterval)
		}
		matterFabricRevoker = &matterFabricRevokerAdapter{store: mfs}
		matterCloser = &matterCommissioningCloserAdapter{window: window}
		matterCandidates = &matterCandidateProviderAdapter{
			walk: func() []eligibility.Candidate {
				var out []eligibility.Candidate
				for _, c := range reg.List() {
					if c == nil || c.ModelRegistry == nil {
						continue
					}
					out = append(out, eligibility.CollectCandidates(c.Name(), c.ModelRegistry.List())...)
				}
				return out
			},
		}
	}
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
	for _, c := range reg.List() {
		if err := adapter.WireLinkCoordinator(c, linksDomain); err != nil {
			logger.Warn("wire link coordinator", slog.String("central", c.Name()), slog.String("err", err.Error()))
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
		for _, c := range reg.List() {
			if entry, ok := c.Clients.Get(interfaceID); ok && entry != nil && entry.Client != nil {
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
	// All three subscribers are started unconditionally so the ring
	// buffer accumulates events regardless of whether a particular
	// north-bound plane is active.
	sysStatusBuf := handlers.NewSystemStatusBuffer(100)
	stopSysStatusBuf := sysStatusBuf.Subscribe(reg)
	defer stopSysStatusBuf()

	wsSysStatus := ws.NewSystemStatusSubscriber(reg, wsHub)
	wsSysStatus.Start()
	defer wsSysStatus.Stop()

	wsHubEvents := ws.NewHubEventsSubscriber(reg, wsHub)
	wsHubEvents.Start()
	defer wsHubEvents.Stop()

	wsDeviceLifecycle := ws.NewDeviceLifecycleSubscriber(reg, wsHub)
	wsDeviceLifecycle.Start()
	defer wsDeviceLifecycle.Stop()

	wsDeviceTrigger := ws.NewDeviceTriggerSubscriber(reg, wsHub)
	wsDeviceTrigger.Start()
	defer wsDeviceTrigger.Stop()

	wsOptimisticRollback := ws.NewOptimisticRollbackSubscriber(reg, wsHub)
	wsOptimisticRollback.Start()
	defer wsOptimisticRollback.Stop()

	if mqttWiring != nil {
		mqttSysStatus := mqtt.NewSystemStatusPublisher(reg, mqttWiring, logger)
		mqttSysStatus.Start()
		defer mqttSysStatus.Stop()
	}

	if cfg.North.REST.IsEnabled() {
		var openapiValidator *middleware.OpenAPIValidator
		if cfg.North.REST.OpenAPIValidateEnabled() {
			openapiValidator = buildOpenAPIValidator(cfg, logger)
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
			OIDC:           buildOIDCRest(cfg, logger, restAuth),
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
			MatterFabricStore: matterFabricStore,
			MatterCommissioning: handlers.MatterCommissioning{
				Discriminator: cfg.North.Matter.Discriminator,
				Passcode:      cfg.North.Matter.Commissioning.Passcode,
				VendorID:      cfg.North.Matter.VendorID,
				ProductID:     cfg.North.Matter.ProductID,
			},
			MatterCommissioningOpener: matterOpener,
			MatterStatusReader:        matterStatusReader,
			MatterFabricRevoker:       matterFabricRevoker,
			MatterCommissioningCloser: matterCloser,
			MatterExposureStore:       matterExposureStore,
			MatterCandidateProvider:   matterCandidates,
			MatterEventPublisher:      matterPub,
			MatterTopologyReassembler: matterReassembler,
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
			if adv, err := startMDNSAdvertiser(cfg, logger); err != nil {
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
			OIDC:        buildOIDC(cfg, logger),
			AuthResolve: auth.SessionMiddleware(sessions),
			AuthRequire: nil, // UI is browser-facing, wizard runs unauthenticated
		})
		servers.add("ui", rest.NewServer(cfg.North.UI.Listen, uiRouter, logger))
	}

	if err := servers.startAll(); err != nil {
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
	// to emit is not fatal. matterBI is nil when matter is disabled.
	if matterBI != nil {
		matterBI.EmitShutDown()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	servers.stopAll(shutdownCtx)
	return nil
}

// serverGroup bundles the REST + UI server lifecycles.
type serverGroup struct {
	logger  *slog.Logger
	mu      sync.Mutex
	servers map[string]*rest.Server
	errs    chan error
}

func newServerGroup(logger *slog.Logger) *serverGroup {
	return &serverGroup{logger: logger, servers: make(map[string]*rest.Server), errs: make(chan error, 4)}
}

func (g *serverGroup) add(name string, s *rest.Server) {
	g.mu.Lock()
	g.servers[name] = s
	g.mu.Unlock()
}

func (g *serverGroup) startAll() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for name, s := range g.servers {
		name, s := name, s
		go func() {
			if err := s.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				g.logger.Error("server.exit", slog.String("name", name), slog.String("err", err.Error()))
				g.errs <- err
			}
		}()
	}
	// Wait a beat so Listen gets a chance to fail-fast on bind issues.
	time.Sleep(20 * time.Millisecond)
	return nil
}

func (g *serverGroup) stopAll(ctx context.Context) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for name, s := range g.servers {
		if err := s.Shutdown(ctx); err != nil {
			g.logger.Warn("server.shutdown", slog.String("name", name), slog.String("err", err.Error()))
		}
	}
}

func newLogger(lc config.LoggingConfig, w io.Writer) *slog.Logger {
	logger, _, _ := newLoggerStack(lc, w)
	return logger
}

// newLoggerStack builds the root logger plus the [hmlog.LevelRegistry]
// that gates it. Subsystem loggers obtained via
// [hmlog.ForSubsystem] share the same handler chain and registry, so
// runtime overrides set via the diagnostics REST endpoint take effect
// across every subsystem without rebuilding loggers.
//
// The returned error surfaces only when the static `logging.overrides`
// map in the config carries an invalid level string. The root logger
// and registry are non-nil regardless.
func newLoggerStack(lc config.LoggingConfig, w io.Writer) (*slog.Logger, *hmlog.LevelRegistry, error) {
	stack, err := newFullLoggerStack(lc, w)
	if err != nil {
		return stack.Logger, stack.Levels, err
	}
	return stack.Logger, stack.Levels, nil
}

// newFullLoggerStack is the new entry point used by daemon code paths
// that also need the [hmlog.TeeHandler] for the diagnostics capture
// endpoint. The legacy [newLoggerStack] stays as a thin wrapper so
// the test surface in [daemon_io_test.go] continues to compile.
func newFullLoggerStack(lc config.LoggingConfig, w io.Writer) (hmlog.Stack, error) {
	defaultLevel, _ := hmlog.ParseLevel(lc.Level)
	opts := hmlog.StackOptions{
		Writer: w,
		Format: hmlog.ParseFormat(lc.Format),
	}
	stack := hmlog.BuildFullStack(opts, defaultLevel)
	if len(lc.Overrides) > 0 {
		if err := stack.Levels.ApplyConfig(lc.Overrides); err != nil {
			return stack, err
		}
	}
	return stack, nil
}

func buildTokenMap(cfg *config.Config) map[string]auth.Identity {
	out := make(map[string]auth.Identity, len(cfg.North.REST.Auth.Tokens))
	for token, role := range cfg.North.REST.Auth.Tokens {
		out[token] = auth.Identity{Subject: token, Role: auth.Role(role)}
	}
	return out
}

func buildCORS(cfg *config.Config) *middleware.CORSConfig {
	if len(cfg.North.REST.CORS) == 0 {
		return nil
	}
	return &middleware.CORSConfig{Origins: cfg.North.REST.CORS}
}

// mqttStack bundles every MQTT component the composition root builds
// and tears down as one unit.
type mqttStack struct {
	wiring    *mqtt.Wiring
	lifecycle *mqtt.Lifecycle
	client    mqtt.Client
}

// scheduleWeekProfileSink adapts [adapter.SchedulesDomain] to the
// [mqtt.WeekProfileSink] contract. The schedules domain resolves the
// device through the central registry passed at construction time, so
// the central + iface arguments from the MQTT topic are intentionally
// dropped here. Priority is also dropped: SchedulesDomain enforces the
// canonical per-DP priority via PutParamset.
type scheduleWeekProfileSink struct {
	sd *adapter.SchedulesDomain
}

func (a scheduleWeekProfileSink) SetActiveProfile(
	ctx context.Context, _, _, deviceAddress string, channelIdx int,
	profileKey string, _ hmenum.CommandPriority,
) error {
	return a.sd.SetActiveProfile(ctx, deviceAddress, channelIdx, profileKey)
}

// buildOIDC discovers the IdP and constructs the UI OIDC deps.
// Returns nil when OIDC is disabled or discovery fails — the daemon
// then renders the login page without the SSO button.
func buildOIDC(cfg *config.Config, logger *slog.Logger) *ui.OIDCDeps {
	client := buildOIDCClient(cfg, logger)
	if client == nil {
		return nil
	}
	return ui.NewOIDCDeps(client)
}

// buildOIDCRest reuses the same OIDC client for the REST mount so
// SPA-driven SSO and the HTMX wizard share state and credentials.
func buildOIDCRest(cfg *config.Config, logger *slog.Logger, authDeps *handlers.AuthDeps) *handlers.OIDCDeps {
	client := buildOIDCClient(cfg, logger)
	if client == nil {
		return nil
	}
	return handlers.NewOIDCDeps(client, authDeps, logger)
}

func buildOIDCClient(cfg *config.Config, logger *slog.Logger) *oidc.Client {
	oc := cfg.North.REST.Auth.OIDC
	if !oc.Enabled || oc.Issuer == "" {
		return nil
	}
	discoCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := oidc.New(discoCtx, oidc.Config{
		Issuer:       oc.Issuer,
		ClientID:     oc.ClientID,
		ClientSecret: oc.ClientSecret,
		RedirectURL:  oc.RedirectURL,
		RoleClaim:    oc.RoleClaim,
	}, nil)
	if err != nil {
		logger.Warn("oidc.discovery", slog.String("err", err.Error()))
		return nil
	}
	return client
}

func buildMQTT(cfg *config.Config, logger *slog.Logger, collector *metrics.MqttCollector) *mqttStack {
	if !cfg.North.MQTT.Enabled {
		return nil
	}
	var client mqtt.Client
	var connector mqtt.Connector
	if cfg.North.MQTT.BrokerURL == "" {
		// No broker configured but enabled → fall back to the
		// recording no-op client so developers can exercise the
		// wiring without a broker.
		client = mqtt.NewNoopClient()
	} else {
		tcp := mqtt.NewTCPClient(mqtt.TCPConfig{
			BrokerURL:    cfg.North.MQTT.BrokerURL,
			ClientID:     cfg.North.MQTT.ClientID,
			Username:     cfg.North.MQTT.Username,
			Password:     cfg.North.MQTT.Password,
			WillTopic:    buildLWTTopic(cfg),
			WillPayload:  []byte("offline"),
			WillRetain:   true,
			CleanSession: true,
			Logger:       logger,
		})
		client = tcp
		connector = tcp
	}

	startedAt := time.Now().UTC()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               cfg.North.MQTT.TopicBase,
		CentralName:        pickFirstCentral(cfg),
		RawEnabled:         cfg.North.MQTT.RawEnabled,
		HADiscoveryEnabled: cfg.North.MQTT.DiscoveryEnabled,
		SubDevicesEnabled:  cfg.North.MQTT.SubDevicesEnabled,
		HealthSupplier:     bridgeHealthSupplier(cfg, startedAt),
		Collector:          collector,
	}, client)
	wiring := mqtt.NewWiring(bridge, logger)

	stack := &mqttStack{wiring: wiring, client: client}
	if connector != nil {
		lc := mqtt.NewLifecycle(mqtt.DefaultLifecycle(), connector)
		lc.OnConnect(func(ctx context.Context) {
			_ = bridge.AnnounceOnline(ctx)
			// Re-publish every cached Discovery config so HA recovers
			// all entities after a broker restart or clean-session
			// reconnect that wiped the retained store.
			_ = bridge.RepublishDiscovery(ctx)
		})
		stack.lifecycle = lc
	}
	return stack
}

// buildLWTTopic assembles the retained LWT/online topic for the
// bridge. Mirrors mqtt.TopicBuilder.BridgeStatus without requiring
// bridge instantiation first.
func buildLWTTopic(cfg *config.Config) string {
	base := cfg.North.MQTT.TopicBase
	if base == "" {
		base = "openccu-loom"
	}
	return base + "/bridge/status"
}

// bridgeHealthSupplier returns a closure the MQTT bridge invokes on
// every AnnounceOnline to compose the `<base>/bridge/health` payload.
// The body carries operator-visible metadata that is more useful than
// a bare "online" flag — build identity, daemon boot timestamp, and
// the configured centrals.
func bridgeHealthSupplier(cfg *config.Config, startedAt time.Time) func() map[string]any {
	centrals := make([]string, 0, len(cfg.Centrals))
	for i := range cfg.Centrals {
		centrals = append(centrals, cfg.Centrals[i].Name)
	}
	return func() map[string]any {
		return map[string]any{
			"version":    build.Version,
			"commit":     build.Commit,
			"build_date": build.BuildDate,
			"started_at": startedAt.Format(time.RFC3339),
			"centrals":   centrals,
		}
	}
}

// startCallbackServer binds the XML-RPC callback listener on
// `cfg.Callback.{Host,Port}` and computes the URL the CCU is told
// to push events to. The URL's host comes from PublicHost when
// configured; otherwise it is autodetected via a UDP probe against
// the first central's host (the kernel picks the egress interface,
// which is what the CCU will see as our peer address).
//
// When cfg.Callback.Port is 0 and cfg.Callback.PortRange is set (e.g.
// "30000-30099"), the server scans the range and binds on the first
// available port. The effective port is always read from srv.Addr()
// after construction, never from the configured value.
func startCallbackServer(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*rpcserver.XMLRPCServer, string, error) {
	host := cfg.Callback.Host
	if host == "" {
		host = "0.0.0.0"
	}
	addr := fmt.Sprintf("%s:%d", host, cfg.Callback.Port)

	xcfg := rpcserver.XMLRPCConfig{
		Addr:   addr,
		Logger: logger.With(slog.String("component", "callback.xmlrpc")),
	}
	if cfg.Callback.Port == 0 && cfg.Callback.PortRange != "" {
		lo, hi, err := config.ParsePortRange(cfg.Callback.PortRange)
		if err != nil {
			return nil, "", fmt.Errorf("callback: %w", err)
		}
		xcfg.PortRange = rpcserver.NewPortRange(lo, hi)
	}

	srv, err := rpcserver.NewXMLRPCServer(xcfg)
	if err != nil {
		return nil, "", fmt.Errorf("callback listen %s: %w", addr, err)
	}
	go func() {
		if err := srv.Serve(ctx); err != nil {
			logger.Warn("callback.serve", slog.String("err", err.Error()))
		}
	}()

	publicHost := cfg.Callback.PublicHost
	if publicHost == "" {
		publicHost = autodetectCallbackHost(cfg)
	}
	if publicHost == "" {
		return srv, "", fmt.Errorf("callback: public host could not be determined — set callback.public_host")
	}
	tcpAddr, ok := srv.Addr().(*net.TCPAddr)
	port := cfg.Callback.Port
	if ok {
		port = tcpAddr.Port
	}
	baseURL := fmt.Sprintf("http://%s:%d", publicHost, port)
	return srv, baseURL, nil
}

// autodetectCallbackHost opens a throw-away UDP socket to the first
// configured central and reads back the local address. This is the
// standard "egress interface" trick — no packets are actually sent
// because UDP "Dial" only binds.
func autodetectCallbackHost(cfg *config.Config) string {
	if len(cfg.Centrals) == 0 {
		return ""
	}
	target := cfg.Centrals[0].Host
	conn, err := net.Dial("udp", target+":80")
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	return addr.IP.String()
}

// loadTranslations resolves the translation archive in this order:
// 1. cfg.CCUData.TranslationsPath (explicit operator override)
// 2. Embedded extract (shipped with the binary, see
// internal/ccudata/embedded)
// 3. Empty fallback (raw CCU strings in the UI)
//
// Every transition is logged at INFO so operators can tell at a
// glance which source their daemon is running against.
func loadTranslations(cfg *config.Config, logger *slog.Logger) *ccudata.Translations {
	if path := cfg.CCUData.TranslationsPath; path != "" {
		t, err := ccudata.LoadTranslations(path)
		if err != nil {
			logger.Warn("ccudata.translations.load",
				slog.String("path", path),
				slog.String("err", err.Error()))
		} else {
			logger.Info("ccudata.translations.ok",
				slog.String("source", "file"),
				slog.String("path", path),
				slog.Int("locales", len(t.Locales())))
			return t
		}
	}
	t, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		logger.Warn("ccudata.translations.embedded",
			slog.String("err", err.Error()))
		return ccudata.Empty()
	}
	logger.Info("ccudata.translations.ok",
		slog.String("source", "embedded"),
		slog.Int("locales", len(t.Locales())))
	return t
}

// loadEasymode decodes the embedded easymode archive; errors are
// logged and return an empty struct so the UI schema adapter sees a
// non-nil value and falls through to "no groups".
func loadEasymode(logger *slog.Logger) *ccudata.Easymode {
	em, err := ccudata.LoadEasymodeEmbedded()
	if err != nil {
		logger.Warn("ccudata.easymode.embedded", slog.String("err", err.Error()))
		return ccudata.EmptyEasymode()
	}
	logger.Info("ccudata.easymode.ok",
		slog.String("source", "embedded"),
		slog.Int("channels", len(em.ChannelMetadata)),
		slog.Int("presets", len(em.OptionPresets)),
		slog.Int("cross_rules", len(em.CrossValidations.Rules)))
	return em
}

// loadProfiles decodes the embedded receiver-profile catalogue.
func loadProfiles(logger *slog.Logger) *ccudata.ProfileStore {
	ps, err := ccudata.LoadProfilesEmbedded()
	if err != nil {
		logger.Warn("ccudata.profiles.embedded", slog.String("err", err.Error()))
	}
	if ps == nil {
		return nil
	}
	logger.Info("ccudata.profiles.ok",
		slog.String("source", "embedded"),
		slog.Int("receivers", len(ps.Receivers)),
		slog.Int("aliases", len(ps.Aliases)))
	return ps
}

func pickFirstCentral(cfg *config.Config) string {
	if len(cfg.Centrals) == 0 {
		return ""
	}
	return cfg.Centrals[0].Name
}

// buildOpenAPIValidator loads the OpenAPI spec from the configured
// path and returns a ready validator, or nil when the file is missing
// or fails to parse. Failures are logged but never abort the daemon —
// a missing spec must not take the REST surface offline; operators
// see the warning and can either supply the spec or flip
// OpenAPIValidate off.
func buildOpenAPIValidator(cfg *config.Config, logger *slog.Logger) *middleware.OpenAPIValidator {
	path := cfg.North.REST.OpenAPISpecPath
	if path == "" {
		path = "assets/openapi.yaml"
	}
	specBytes, err := os.ReadFile(path)
	if err != nil {
		logger.Warn("openapi.spec.read",
			slog.String("path", path),
			slog.String("err", err.Error()),
			slog.String("hint", "set north.rest.openapi_spec_path to the deployed location"))
		return nil
	}
	v, err := middleware.NewOpenAPIValidator(middleware.OpenAPIValidatorConfig{
		Spec:     specBytes,
		Logger:   logger.With(slog.String("component", "openapi")),
		FailOpen: false,
	})
	if err != nil {
		logger.Warn("openapi.spec.parse",
			slog.String("path", path),
			slog.String("err", err.Error()))
		return nil
	}
	logger.Info("openapi.spec.loaded",
		slog.String("path", path))
	return v
}

// singleCentralName returns the name of the registered central when
// the registry contains exactly one entry, otherwise the empty
// string. The REST router uses the result to pre-populate
// `central_name` in every request's [reqctx.RequestContext]. Multi-
// central deployments leave the request scope unset and rely on
// per-handler resolution.
func singleCentralName(reg *central.Registry) string {
	names := reg.Names()
	if len(names) == 1 {
		return names[0]
	}
	return ""
}

// Compile-time check that the unused handlers import stays referenced
// (the router builds DTOs from it when Paramsets is nil, which is the
// case in the MVP composition).
var _ handlers.ConfigReader = (*adapter.ConfigAdapter)(nil)

// startMatterBridge constructs and starts the Matter bridge when
// matter.enabled is set. Returns the bridge and a stop function the
// caller defers; both are nil when the feature flag is off or the
// bridge cannot stand up. Errors are logged at warn level but never
// abort the daemon — the bridge is feature-flagged and
// failing to start it must not take REST / MQTT down with it.
//
// Defaults applied here mirror the [config.NorthMatter] doc strings:
// VendorID 0xFFF1 (test vendor block — never ship), ProductID 0x8000,
// NodeLabel "openccu-loom", Discriminator 0xF00, Listen ":5540".
func buildMatterAdvertiser(mc config.NorthMatter, logger *slog.Logger) mdns.Advertiser {
	switch mc.MDNSAdvertise {
	case "", "noop":
		return mdns.NewNoop()
	case "zeroconf":
		z := mdns.NewZeroconf()
		// Side-car responder so service-subtype browses
		// (`_L<long>._sub`, `_S<short>._sub`, `_V<vid>._sub`,
		// `_CM._sub`) hit a PTR pointing at the primary instance —
		// Apple Home and Google Home filter the mDNS browse via
		// these subtypes and `grandcat/zeroconf` v1.0.0 has no
		// first-class subtype API.
		if r, err := mdns.NewSubtypeResponder(logger); err != nil {
			logger.Warn("matter.bridge.mdns.subtype_init_failed",
				slog.String("err", err.Error()),
				slog.String("hint", "primary records will publish, but Apple Home / Google Home subtype-filtered discovery may fail"))
		} else {
			r.Start(context.Background())
			z.AttachSubtypeResponder(r)
			logger.Info("matter.bridge.mdns.subtype_responder_started",
				slog.String("hint", "PTR responder for `_L*._sub`, `_S*._sub`, `_V*._sub`, `_CM._sub` queries active"))
		}
		logger.Info("matter.bridge.mdns.zeroconf",
			slog.String("hint", "primary record via grandcat/zeroconf, subtypes via side-car responder"))
		return z
	default:
		logger.Warn("matter.bridge.mdns.unknown",
			slog.String("value", mc.MDNSAdvertise),
			slog.String("hint", "falling back to noop; valid: noop|zeroconf"))
		return mdns.NewNoop()
	}
}

// matterBridgeBundle is the full set of artefacts the daemon needs
// from a started Matter bridge: the bridge itself, a stop closure,
// the underlying matter store, and the inner runtime objects the
// REST/UI layer wires into the [matterbridge.CommissioningWindowOpener]
// when ephemeral-window mode is enabled.
type matterBridgeBundle struct {
	bridge         *matterbridge.Bridge
	stop           func()
	store          *matterstore.Store
	opMgr          *operational.Manager
	opCreds        *mattercore.OperationalCredentials
	configuredPase *matterbridge.PaseAdapter // nil when ConcurrentPairings is enabled or no passcode is configured
	rootRefs       rootClusterRefs           // typed handles for daemon-side lifecycle wiring
}

func startMatterBridge(ctx context.Context, cfg *config.Config, reg *central.Registry, healthTracker *health.Tracker, logger *slog.Logger) *matterBridgeBundle {
	if cfg == nil || !cfg.North.Matter.Enabled {
		return nil
	}

	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./var"
	}
	dsn := "file:" + filepath.Join(dataDir, "openccu-loom.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(2000)"
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dbCancel()
	db, err := sqlitestore.Open(dbCtx, dsn)
	if err != nil {
		logger.Warn("matter.bridge.db_open", slog.String("dsn", dsn), slog.String("err", err.Error()))
		return nil
	}

	store := matterstore.New(db)

	mc := cfg.North.Matter
	if mc.VendorID == 0 {
		mc.VendorID = 0xFFF1
	}
	if mc.ProductID == 0 {
		mc.ProductID = 0x8000
	}
	if mc.NodeLabel == "" {
		mc.NodeLabel = "openccu-loom"
	}
	if mc.Discriminator == 0 {
		mc.Discriminator = 0xF00
	}
	if mc.DevRotateUniqueIDs {
		// Dev-mode: rotate every bridged endpoint's UniqueID at boot so
		// pair iteration cycles (chip-tool brief T11, Apple HMHome cache
		// recovery) see a fresh identity each daemon start. Disabled by
		// default — production Apple Home / Google Home accessory
		// recognition expects a stable UniqueID across restarts.
		bootid.EnableRotation()
		logger.Info("matter.bridge.bootid_rotation_enabled",
			slog.String("hint", "per-boot UniqueID salt active — accessory recognition will fail across restarts; toggle north.matter.dev_rotate_unique_ids=false for production"))
	}

	snap := func(_ context.Context) []endpoint.Snapshot {
		var out []endpoint.Snapshot
		for _, c := range reg.List() {
			if c == nil || c.ModelRegistry == nil {
				continue
			}
			out = append(out, endpoint.Snapshot{
				CentralName: c.Name(),
				Devices:     c.ModelRegistry.List(),
			})
		}
		return out
	}

	advertiser := buildMatterAdvertiser(mc, logger)
	bridge, err := matterbridge.New(store, snap, advertiser, matterbridge.Config{
		Listen:        mc.Listen,
		PreferIPv4:    mc.PreferIPv4,
		VendorID:      mc.VendorID,
		ProductID:     mc.ProductID,
		NodeLabel:     mc.NodeLabel,
		Discriminator: mc.Discriminator,
	}, logger)
	if err != nil {
		logger.Warn("matter.bridge.new", slog.String("err", err.Error()))
		_ = db.Close()
		return nil
	}

	// Enforce the AccessControl cluster's ACL on operational (CASE) IM
	// requests (Matter §9.10). Wired before Start so the first assembled
	// dispatcher already gates reads / writes / invokes.
	bridge.AttachACLLister(store)

	if err := bridge.Start(ctx); err != nil {
		logger.Warn("matter.bridge.start", slog.String("err", err.Error()))
		_ = db.Close()
		return nil
	}
	// mDNS Re-Announce-Loop: grandcat/zeroconf only Probe+Announce on
	// initial Register; commissioners that miss the announce window
	// (transient WiFi loss, late join, mDNS cache eviction past TTL)
	// never see the bridge again. Periodic re-publish at 30 min keeps
	// the records on the wire (Apple's mDNSResponder caches at TTL=4500
	// = 75 min; re-emit at less than half-TTL).
	var stopReannounce func()
	if z, ok := advertiser.(*mdns.Zeroconf); ok {
		stopReannounce, _ = z.StartReannounceLoop(ctx, 30*time.Minute)
		logger.Info("matter.bridge.mdns.reannounce_loop_started",
			slog.String("interval", "30m"))
	}
	logger.Info("matter.bridge.up",
		slog.String("addr", bridge.LocalAddr()),
		slog.Bool("ipv4_only", mc.PreferIPv4))

	// Wire the bridge into the health tracker. The probe reports the
	// `matter` component alongside `mqtt` and `sqlite` so the
	// Diagnostics surface shows every long-running subsystem on one
	// row each. Best-effort: a nil tracker disables the probe.
	if healthTracker != nil {
		_ = matterbridge.StartHealthProbe(ctx, bridge, healthTracker, matterbridge.DefaultProbeInterval)
	}

	// Boot-time fabric announce: if a fabric is already persisted
	// (commissioning happened in a previous run), publish the
	// operational mDNS record under that identity so commissioners
	// can resolve us via DNS-SD without having to re-pair.
	announcePersistedFabric(ctx, store, bridge, logger)

	// caseIdentityHolder is the live, swap-able operational identity
	// the per-exchange CASE provider seeds every fresh responder
	// from. AddNOC's OnFabricInstalled hook (registered inside
	// buildRootClusters) calls caseRefresh below to rebuild the
	// identity from the freshly-persisted store row; subsequent
	// Sigma1 arrivals from any peer (iPhone over IPv6, HomePod over
	// IPv4) get a fresh CaseAdapter wrapping a fresh sigma.Responder
	// seeded from this identity. A singleton CaseAdapter would land
	// in `Finished` after the first Sigma3 and reject every
	// subsequent Sigma1 with `ProcessSigma1 already called`.
	// Multi-fabric case identity registry. Apple Home's Multi-Admin pair
	// installs the primary Hub fabric AND the iCloud system commissioner
	// fabric within seconds; afterwards the iPhone reconnects via fabric
	// #1 while the iCloud Hub uses fabric #2 — both targeting the same
	// bridge over a shared UDP listener. A singleton holder that the
	// LAST `OnFabricInstalled` hook overwrites would force every fresh
	// Sigma1 onto the latest fabric's NOC; the iPhone's Sigma1 (which
	// targets fabric #1's destinationId) would then receive a Sigma2
	// signed under fabric #2's NOC and reject the exchange with
	// StatusReport(SecureChannel, INVALID_PARAMETER) — observed in
	// production logs as "MTRDevice getSessionForNode error 172" and
	// surfaces in Home as an unsupported-device error. The registry holds every
	// installed identity so [sigma.IdentityResolver] can pick the right
	// one per inbound Sigma1.DestinationID. Type definition is package-
	// level so the [caseDestinationResolver] (also package-level) can
	// share the same shape without an interface indirection.
	var (
		caseIdentityMu  sync.RWMutex
		caseFabrics     = make(map[uint8]*caseFabricEntry)
		caseIdentity    *sigma.Identity // last-installed; baseline + pre-AddNOC fallback
		caseVerifier    sigma.PeerVerifier
		caseFabricIndex uint8
	)
	caseRefresh := func(ctx context.Context, fabricIndex uint8, fabricID, nodeID uint64, _ []byte) {
		if store == nil {
			return
		}
		fabric, ferr := store.GetFabric(ctx, fabricIndex)
		if ferr != nil {
			logger.Warn("matter.bridge.case.refresh_get_fabric",
				slog.Int("fabric_index", int(fabricIndex)),
				slog.String("err", ferr.Error()))
			return
		}
		id, ierr := store.GetIdentity(ctx, fabricIndex)
		if ierr != nil {
			logger.Warn("matter.bridge.case.refresh_get_identity",
				slog.Int("fabric_index", int(fabricIndex)),
				slog.String("err", ierr.Error()))
			return
		}
		priv, perr := privKeyFromScalar(id.PrivateKey)
		if perr != nil {
			logger.Warn("matter.bridge.case.refresh_priv",
				slog.String("err", perr.Error()))
			return
		}
		ver, verr := mattercert.NewVerifier(fabric.RootPublicKey, mattercert.SystemTime{})
		if verr != nil {
			logger.Warn("matter.bridge.case.refresh_verifier",
				slog.String("err", verr.Error()))
			return
		}
		opIPK, ipkErr := deriveOperationalIPK(id.IPK, fabric.CompressedID)
		if ipkErr != nil {
			logger.Warn("matter.bridge.case.refresh_ipk",
				slog.Int("fabric_index", int(fabricIndex)),
				slog.String("err", ipkErr.Error()))
			return
		}
		newID := &sigma.Identity{
			NOC:                id.NOC,
			ICAC:               id.ICAC,
			PrivateKey:         priv,
			NodeID:             fabric.NodeID,
			FabricID:           fabric.FabricID,
			CompressedFabricID: fabric.CompressedID,
			IPK:                opIPK,
			FabricIndex:        fabricIndex,
		}
		caseIdentityMu.Lock()
		caseFabrics[fabricIndex] = &caseFabricEntry{
			identity:      newID,
			verifier:      ver,
			rootPublicKey: append([]byte(nil), fabric.RootPublicKey...),
			fabricIndex:   fabricIndex,
		}
		// Keep `caseIdentity` as the most-recently-installed identity so
		// fresh responders still have a sane default before the resolver
		// gets a chance to pick (e.g. malformed Sigma1 that fails to
		// parse the DestinationID).
		caseIdentity = newID
		caseVerifier = ver
		caseFabricIndex = fabricIndex
		caseIdentityMu.Unlock()
		logger.Info("matter.bridge.case.identity_swapped",
			slog.Int("fabric_index", int(fabricIndex)),
			slog.Uint64("fabric_id", fabricID),
			slog.Uint64("node_id", nodeID),
			slog.Int("noc_bytes", len(id.NOC)),
			slog.Int("icac_bytes", len(id.ICAC)),
			slog.Int("total_fabrics", len(caseFabrics)))
	}

	// Sigma1 destination resolver — picks the per-fabric identity whose
	// computed destinationID matches the inbound Sigma1 envelope.
	// Mirrors matter.js FabricManager.findFabricFromDestinationId.
	caseResolver := caseDestinationResolver{
		mu:      &caseIdentityMu,
		fabrics: &caseFabrics,
		logger:  logger,
	}

	// Construct + attach the root-endpoint cluster servers
	// (BasicInformation, GeneralCommissioning, …). Without these
	// chip-tool's ReadCommissioningInfo gets `UnsupportedCluster`
	// (0xC3) for every read and aborts before we can install a fabric.
	// Bind opMgr into the AdoptFabric closure via a deferred-slot
	// pattern: opMgr is constructed below (after buildRootClusters) but
	// the closure only fires when AddNOC is invoked — long after daemon
	// startup completes — so the late assignment is safe. After AddNOC
	// the PASE session's FabricIndex must be rewritten to the new fabric,
	// otherwise the follow-up CommissioningComplete lands on
	// FabricIndex=0 (PASE) and Apple aborts the pair.
	var opMgrSlot *operational.Manager
	adoptSessionForFabric := func(ctx context.Context, fabricIndex uint8) {
		sessionID := mattercore.InvokeSessionIDFromContext(ctx)
		if sessionID == 0 || opMgrSlot == nil {
			return
		}
		if err := opMgrSlot.AdoptFabricIndex(sessionID, fabricIndex); err != nil {
			logger.Debug("matter.bridge.session.adopt_fabric",
				slog.Int("session_id", int(sessionID)),
				slog.Int("fabric_index", int(fabricIndex)),
				slog.String("err", err.Error()))
		} else {
			logger.Debug("matter.bridge.session.adopt_fabric_ok",
				slog.Int("session_id", int(sessionID)),
				slog.Int("fabric_index", int(fabricIndex)))
		}
	}
	rootServers, opCreds, rootRefs, err := buildRootClusters(mc, store, bridge, advertiser, logger, caseRefresh, adoptSessionForFabric)
	if err != nil {
		logger.Warn("matter.bridge.root_clusters.build", slog.String("err", err.Error()))
	} else {
		bridge.AttachRootClusters(rootServers)
		logger.Info("matter.bridge.root_clusters.attached",
			slog.Int("count", len(rootServers)))
	}

	// Build + attach the Aggregator endpoint (EP 1) cluster servers.
	// Bug C topology fix: matter.js's bridge pattern always places the
	// Aggregator on a dedicated endpoint with its own Descriptor whose
	// PartsList enumerates the bridged children. Apple Home's HAP
	// service mapper walks RootNode.PartsList → Aggregator →
	// Aggregator.Descriptor.PartsList to find bridged devices; without
	// this layer Apple Home renders the bridge as empty.
	aggregatorServers, aerr := buildAggregatorClusters()
	if aerr != nil {
		logger.Warn("matter.bridge.aggregator_clusters.build", slog.String("err", aerr.Error()))
	} else {
		bridge.AttachAggregatorClusters(aggregatorServers)
		logger.Info("matter.bridge.aggregator_clusters.attached",
			slog.Int("count", len(aggregatorServers)))
	}

	// Seed GeneralDiagnostics with persisted counters: load the row,
	// bump RebootCount, persist back so the next boot starts at the
	// new value, then hand the seed to the cluster. The cluster adds
	// the live process uptime to BaseOperationalHours on every read.
	// The shutdown closure (further down) writes the final value.
	if rootRefs.GeneralDiagnostics != nil && store != nil {
		seedCtx, seedCancel := context.WithTimeout(ctx, 2*time.Second)
		if rec, lerr := store.LoadDiagnostics(seedCtx); lerr == nil {
			rec.RebootCount++
			if serr := store.SaveDiagnostics(seedCtx, rec); serr != nil {
				logger.Warn("matter.bridge.diagnostics.save_seed", slog.String("err", serr.Error()))
			}
			rootRefs.GeneralDiagnostics.SetPersistedCounters(rec.RebootCount, rec.BaseOperationalHours)
			logger.Info("matter.bridge.diagnostics.seeded",
				slog.Int("reboot_count", int(rec.RebootCount)),
				slog.Int("base_op_hours", int(rec.BaseOperationalHours)))
		} else {
			logger.Warn("matter.bridge.diagnostics.load", slog.String("err", lerr.Error()))
		}
		seedCancel()
	}

	// --- Adapter wiring ----------------------------------------
	// Every Matter bridge lifecycle starts with the operational
	// session manager + MRP ack tracker — both feed the receive
	// pipeline regardless of whether commissioning is active. PASE
	// is conditional on a configured passcode; CASE wiring is
	// deferred until fabric-identity persistence is plumbed.
	opMgr := operational.NewManager(store)
	opMgrSlot = opMgr // late-bind into the AdoptFabric closure above
	sessionLookup := matterbridge.NewOperationalSessionLookup(
		func(id uint16) (*channel.Session, bool) {
			entry, err := opMgr.Get(id)
			if err != nil {
				return nil, false
			}
			return entry.Session, true
		},
	).WithFabricResolver(func(id uint16) (uint8, bool) {
		entry, err := opMgr.Get(id)
		if err != nil {
			return 0, false
		}
		return entry.FabricIndex, true
	}).WithSubjectResolver(func(id uint16) (uint64, []uint32, bool) {
		// Resolves (peerNodeID, peerCATs) for the IM dispatcher's
		// per-subject ACL gate (Matter §9.10.5.6). Apple Home Multi-
		// Admin pairs install per-resident ACEs keyed on the resident's
		// operational NodeID; without this the ACEs collapse onto the
		// fabric-wide wildcard.
		entry, err := opMgr.Get(id)
		if err != nil || entry == nil || entry.Session == nil {
			return 0, nil, false
		}
		return entry.Session.PeerNodeID(), entry.Session.PeerCATs(), true
	})
	bridge.AttachSessionLookup(sessionLookup)

	ackTracker := mrp.NewAckTracker(mrp.DefaultStandaloneAckDelay)
	bridge.AttachAckTracker(ackTracker) // wires both Discharge handler + Owe-pump goroutine

	// Subscription manager: tracks active subscriptions + enforces
	// per-fabric quota and cadence floor/ceiling. The Reporter
	// re-reads the dirty paths through the bridge's IM dispatcher
	// and ships a fresh ReportData via the per-subscription src
	// captured in [Bridge.captureSubTarget]. Best-effort delivery
	// (no MRP retransmit on ongoing reports) — controllers tolerate
	// the occasional drop; the next dirty tick re-emits.
	subMgr := subscription.NewManager(subscription.Config{}, bridge.SubscriptionReporter(), logger)
	subMgr.Start(ctx)
	bridge.AttachSubscriptionManager(subMgr)

	// Wire the secure-session manager so it cascades subscription
	// cleanup whenever it tears down a session. Without this hook,
	// stale subscriptions tied to a closed CASE session linger in the
	// IM layer — Apple Home accumulates one ghost subscription per
	// reconnect, the engine then ships duplicate ReportData to a peer
	// whose decryption keys are already gone, and Apple deprioritizes
	// the burst as redundant noise. Mirrors matter.js
	// packages/protocol/src/session/SessionManager.ts — close
	// callbacks chain into SubscriptionHandler cleanup.
	opMgr.SetOnSessionClose(subMgr.CloseSession)

	// Initial value warm-up REMOVED. A previous per-device LoadAllValues
	// sweep ran here so Apple Home's HAP-mapper would see observed
	// values before the first Subscribe-Initial-Report. On a non-trivial
	// CCU (124 devices × multiple readable DPs each) the sweep fanned
	// out hundreds of `getValue` radio calls within a second or two and
	// drove the CCU DutyCycle into the warning band on every restart.
	// The persistent VALUES cache (ADR 0019) + push events already
	// hydrate the model for the vast majority of accessories; Apple
	// renders the remaining DPs as `unavailable` until the next wire
	// event populates them — the same trade-off MQTT / REST already
	// take. The reference design takes the same trade-off and does not
	// pre-load every DP on boot either.
	// Wire the symmetrical event-drain hook so cluster-emitted events
	// (e.g. GenericSwitch InitialPress / LongPress) ride the same
	// per-subscription [subTarget] machinery as attribute reports.
	subMgr.SetEventReporter(bridge.SubscriptionEventReporter())

	// Wire fabric-removed cleanup AFTER both managers exist. Without
	// this Apple's RemoveFabric (sent at the end of every aborted pair
	// chain) leaves the operational session and ongoing subscription
	// alive — the next pair retry collides on session-id 1 with
	// `aesccm: authentication failed` and Apple gives up.
	if opCreds != nil {
		biRefRemove := rootRefs.BasicInformation
		opCreds.SetOnFabricRemoved(func(ctx context.Context, fabricIndex uint8) {
			// Defer the operational + subscription teardown so the
			// in-flight RemoveFabric NOCResponse can ride out on the
			// just-removed CASE session before its keys are dropped.
			// matter.js's CaseServer fires the equivalent
			// "fabricRemoved" event AFTER the IM reply ships
			// (`InteractionMessenger` flushes the reply, then the
			// behaviors pipeline closes the session). With a sync
			// CloseFabric we'd close session N inline, the reply
			// encoder finds session N gone, and Apple sees a missing
			// NOCResponse → retries until the pair times out.
			//
			// 100ms is enough for the synchronous reply path to drain
			// (the IM dispatcher returns NOCResponse immediately, the
			// bridge encrypts + sends in < 5ms in practice).
			bridge.EmitFabricRemoved(fabricIndex)
			if biRefRemove != nil {
				biRefRemove.EmitLeave(fabricIndex)
			}
			logger.Info("matter.bridge.fabric.removed",
				slog.Int("fabric_index", int(fabricIndex)))
			go func() {
				time.Sleep(100 * time.Millisecond)
				opMgr.CloseFabric(fabricIndex)
				subMgr.CloseFabric(fabricIndex)
				// Explicit resumption-record cleanup. SQLite FK CASCADE
				// would also catch this once the matter_fabrics row is
				// deleted, but the store API decouples the two tables
				// — call RemoveResumptionsByFabric directly so the
				// teardown is defense-in-depth and visible in logs.
				// Mirrors chip src/credentials/FabricTable.cpp Delete().
				if store != nil {
					if err := store.RemoveResumptionsByFabric(context.Background(), fabricIndex); err != nil {
						logger.Debug("matter.bridge.fabric.remove_resumptions",
							slog.Int("fabric_index", int(fabricIndex)),
							slog.String("err", err.Error()))
					}
				}
			}()
		})

		// Wire fabric-updated cleanup. After UpdateNOC installs a new
		// NOC for an existing fabric, every OTHER CASE session bound to
		// that fabric is cryptographically stale — the commissioner must
		// re-CASE with the new NOC. OperationalCredentials fires this hook
		// (its AbortAllOtherCommunicationOnFabric mirror after UpdateNOC)
		// but it stays inert unless wired here. Without it the stale CASE
		// sessions linger and the commissioner's first post-UpdateNOC
		// message decrypts against the wrong identity. Unlike the removed
		// path we do NOT drop resumption records or emit a Leave event —
		// the fabric still exists, only its sessions must be torn down.
		// Mirrors chip FabricTable::AbortAllOtherCommunicationOnFabric.
		opCreds.SetOnFabricUpdated(func(ctx context.Context, fabricIndex uint8) {
			// Preserve the session that invoked UpdateNOC: it still has to
			// carry the NOCResponse, and the commissioner re-CASEs on it.
			// A plain CloseFabric would also delete the invoking session, so
			// its own response could not be sent and the commissioner would
			// time out. Mirrors chip AbortAllOtherCommunicationOnFabric,
			// which pins the invoking exchange's session.
			keepSession := mattercore.InvokeSessionIDFromContext(ctx)
			opMgr.CloseFabricExcept(fabricIndex, keepSession)
			subMgr.CloseFabricExcept(fabricIndex, keepSession)
			logger.Info("matter.bridge.fabric.updated",
				slog.Int("fabric_index", int(fabricIndex)),
				slog.Int("kept_session", int(keepSession)))
		})
	}

	var configuredPase *matterbridge.PaseAdapter
	if mc.Commissioning.Passcode != 0 {
		if mc.Commissioning.ConcurrentPairings {
			provider := matterbridge.NewPerExchangePaseProvider(func() *matterbridge.PaseAdapter {
				a, err := buildPaseAdapter(mc.Commissioning, opMgr, opCreds, rootRefs.GeneralCommissioning, logger)
				if err != nil {
					logger.Warn("matter.bridge.pase.build", slog.String("err", err.Error()))
					return nil
				}
				return a
			})
			// Reap stale per-exchange adapters every 30s with a 60s
			// TTL so a daemon serving thousands of distinct
			// commissioners doesn't grow the entries map unboundedly.
			// PASE exchanges normally finish < 5s; 60s comfortably
			// covers chip-tool retransmits.
			provider.StartReaper(ctx, 30*time.Second, 60*time.Second)
			bridge.AttachPaseHandlerProvider(provider.Resolve)
			logger.Info("matter.bridge.pase.armed",
				slog.Int("iterations", mc.Commissioning.Iterations),
				slog.String("mode", "per-exchange"))
		} else {
			paseAdapter, err := buildPaseAdapter(mc.Commissioning, opMgr, opCreds, rootRefs.GeneralCommissioning, logger)
			if err != nil {
				logger.Warn("matter.bridge.pase.build", slog.String("err", err.Error()))
			} else {
				bridge.AttachPaseHandler(paseAdapter)
				configuredPase = paseAdapter
				logger.Info("matter.bridge.pase.armed",
					slog.Int("iterations", mc.Commissioning.Iterations),
					slog.String("mode", "singleton"))
			}
		}
	}

	// CASE provider is wired unconditionally when the Matter bridge is
	// up: Sigma1 has to be answered by any daemon that ever wants to
	// serve operational reads, and the only thing the commissioner does
	// before sending Sigma1 is run AddNOC over PASE — at which point
	// `caseRefresh` swaps the freshly persisted identity into the
	// provider's factory. Gating this block on `mc.CASE.NodeID != 0`
	// (the pre-2026-05 behaviour) silently broke commissioning: PASE
	// completed, fabric installed, then every Sigma1 dropped with
	// `ErrCaseHandlerMissing` at DEBUG level and the commissioner timed
	// out. mc.CASE.NodeID / FabricID survive as soft preferences for
	// `loadPersistentCaseIdentity` (multi-fabric pin) and as the
	// fallback identity stamp on the pre-AddNOC ephemeral stub adapter.
	{
		// Seed caseIdentity / caseVerifier from a persisted fabric (if
		// any). caseRefresh updates them on every AddNOC; the
		// per-exchange provider's factory below reads them under the
		// lock when allocating each fresh adapter.
		seedID, seedVer, seedIdx, persisted, err := loadPersistentCaseIdentity(ctx, mc.CASE, store, logger)
		if err != nil {
			logger.Warn("matter.bridge.case.load_identity", slog.String("err", err.Error()))
		}
		if persisted {
			caseIdentityMu.Lock()
			caseIdentity = seedID
			caseVerifier = seedVer
			caseFabricIndex = seedIdx
			// Also stamp the seed into the multi-fabric registry so the
			// destination resolver can match it post-reboot before any
			// AddNOC fires.
			if rootPub, rpErr := loadFabricRootPublicKey(ctx, store, seedIdx); rpErr == nil {
				caseFabrics[seedIdx] = &caseFabricEntry{
					identity:      seedID,
					verifier:      seedVer,
					rootPublicKey: rootPub,
					fabricIndex:   seedIdx,
				}
			} else {
				logger.Warn("matter.bridge.case.persistent_rootpub",
					slog.Int("fabric_index", int(seedIdx)),
					slog.String("err", rpErr.Error()))
			}
			caseIdentityMu.Unlock()
			logger.Info("matter.bridge.case.persistent_identity",
				slog.Int("fabric_index", int(seedIdx)),
				slog.Uint64("node_id", seedID.NodeID),
				slog.Uint64("fabric_id", seedID.FabricID))

			// Multi-Fabric loading: until now only the lowest-index ("seed")
			// fabric was rehydrated on boot. Apple's Multi-Admin pair
			// flow installs two fabrics — iPhone (Apple Home) +
			// HomePod/Apple TV (Apple Home Hub). After a daemon restart
			// the second fabric's CASE-reconnect would land Sigma1
			// against an identity-less branch in the resolver and Apple's
			// MTRDevice for that fabric went `MTRDeviceStateUnreachable`.
			// Load every persisted fabric into `caseFabrics` so the
			// resolver has identities for both controllers from the
			// first packet onwards.
			rehydratedExtra := loadAdditionalFabricsForCase(ctx, store, seedIdx, caseFabrics, &caseIdentityMu, logger)
			if rehydratedExtra > 0 {
				logger.Info("matter.bridge.case.persistent_identities_extra",
					slog.Int("seed_fabric_index", int(seedIdx)),
					slog.Int("additional_fabrics_loaded", rehydratedExtra))
			}
		} else {
			logger.Info("matter.bridge.case.awaiting_addnoc",
				slog.String("hint", "no persisted fabric yet; first Sigma1 after AddNOC will use the fabric-driven identity"))
		}

		caseProvider := matterbridge.NewPerExchangeCaseProvider(func() *matterbridge.CaseAdapter {
			caseIdentityMu.RLock()
			id := caseIdentity
			ver := caseVerifier
			fIdx := caseFabricIndex
			caseIdentityMu.RUnlock()
			if id == nil {
				// Pre-AddNOC traffic: build a stub adapter on an
				// ephemeral key so Sigma3 fails cleanly instead of
				// panicking. Real CASE handshakes need the persisted
				// fabric to land first.
				ephem, ephemErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if ephemErr != nil {
					logger.Warn("matter.bridge.case.factory_ephemeral",
						slog.String("err", ephemErr.Error()))
					return nil
				}
				id = &sigma.Identity{PrivateKey: ephem, NodeID: mc.CASE.NodeID, FabricID: mc.CASE.FabricID}
				ver = trustAnyPeerVerifier{}
			}
			// Pre-allocate the operational session id BEFORE the
			// responder is constructed: Sigma2.responderSessionID is
			// what the commissioner echoes back as `dest.SessionID`
			// on every operational packet, and our session lookup is
			// keyed on that value. A hard-coded "1" makes every
			// parallel CASE session collide on the same lookup slot —
			// the iPhone's reads and the HomePod's reads end up in
			// the same bucket and only one of them wins.
			sessID, allocErr := opMgr.AllocateID()
			if allocErr != nil {
				logger.Warn("matter.bridge.case.factory_alloc",
					slog.String("err", allocErr.Error()))
				return nil
			}
			responder := sigma.NewResponder(id, ver, sessID)
			// Wire the multi-fabric destination resolver so the inbound
			// Sigma1's DestinationID picks the correct identity at
			// Sigma2-sign time — Bug G fix for Apple Multi-Admin pairing.
			responder.SetIdentityResolver(caseResolver)
			// Emit responderSessionParams (Sigma2 tag 5) so the commissioner
			// learns the bridge's preferred MRP intervals post-CASE — matches
			// the operational mDNS SII/SAI/SAT defaults. Mirrors chip
			// CASESession.cpp:1326 EncodeSessionParameters.
			responder.SetSessionParameters(bridgeSessionParameters())
			// Wire the resumption-record lookup so a fresh Sigma1 that
			// carries a known ResumptionID can fast-path through
			// Sigma2_Resume instead of the full handshake. Mirrors
			// matter.js CaseServer.ts and chip CASESession.cpp resumption
			// branch.
			responder.SetResumptionStore(caseResumptionStoreAdapter{mgr: opMgr})
			adapter := matterbridge.NewCaseAdapter(responder)
			adapter.SetOnSessionEstablished(func(keys sigma.SessionKeys, peerSessionID uint16) error {
				// fabricIndex / nodeID are read from the responder
				// AFTER ProcessSigma1 so the resolver-chosen identity
				// is reflected — using the factory-time `fIdx` / `id`
				// values would mismatch the actually-signed Sigma2
				// when the destination resolver picked a different
				// fabric. peerSessionID is the Sigma1.InitiatorSessionID
				// — outbound replies stamp it into Header.SessionID.
				// peerNodeID comes from the verified NOC (sigma.Responder
				// lifts it via PeerNodeIDExtractor) and feeds the AES-CCM
				// nonce for inbound packets — Matter §4.5.1.4 builds the
				// nonce from the *source* NodeID, which on the peer's
				// outbound is the peer's own NodeID.
				resolvedFabric := fIdx
				resolvedNode := id.NodeID
				peerNodeID := uint64(0)
				var peerCATs []uint32
				if resp := adapter.SnapshotResponder(); resp != nil {
					peerNodeID = resp.PeerNodeID()
					peerCATs = resp.PeerCATs()
					// SessionFabricIndex returns the fabric the resolver
					// landed on for this exchange — see [Responder.SessionFabricIndex].
					if fi, nid, ok := resp.SessionIdentity(); ok {
						resolvedFabric = fi
						resolvedNode = nid
					}
				}
				entry, openErr := opMgr.OpenFromSigmaWithID(sessID, resolvedFabric, resolvedNode, peerNodeID, peerSessionID, peerCATs, keys)
				if openErr != nil {
					opMgr.ReleaseID(sessID)
					return openErr
				}
				logger.Info("matter.bridge.case.session_established",
					slog.Int("session_id", int(entry.SessionID)),
					slog.Int("fabric_index", int(resolvedFabric)),
					slog.Uint64("node_id", resolvedNode),
					slog.Uint64("peer_node_id", peerNodeID),
					slog.Int("peer_session_id", int(peerSessionID)))
				// Persist ECDH shared secret + resumption ID so
				// Sigma2_Resume can short-circuit the full Sigma1-3
				// handshake on the peer's next reconnect (matter.js
				// CaseServer.ts:210). The accessors return defensive
				// copies; both are nil-safe if the handshake didn't
				// surface the values.
				if resp := adapter.SnapshotResponder(); resp != nil {
					rid := resp.ResumptionID()
					secret := resp.ECDHSharedSecret()
					if len(rid) > 0 && len(secret) > 0 {
						if persistErr := opMgr.PersistResumption(context.Background(), resolvedFabric, peerNodeID, rid, secret); persistErr != nil {
							logger.Debug("matter.bridge.case.resumption_persist_failed",
								slog.Int("session_id", int(entry.SessionID)),
								slog.String("err", persistErr.Error()))
						}
					}
				}
				return nil
			})
			return adapter
		})
		// Reaper: purge stale per-exchange CASE adapters every 30s
		// with a 60s TTL. CASE handshakes normally finish in well
		// under a second; 60s comfortably covers Apple's MRP
		// retransmits without leaking adapters across the daemon's
		// lifetime.
		// Route the reaper's per-exchange eviction into the bridge's
		// sigma1Replied dedupe map. The Sigma3 success path forgets the
		// entry inline, but an ABORTED CASE handshake (Sigma1 received,
		// Sigma3 never) would otherwise leak one entry permanently. Wiring
		// the evict hook lets the TTL reaper clean those up — without this
		// the dedupe map grows without bound on a daemon that sees many
		// half-completed CASE attempts.
		caseProvider.SetOnEvict(bridge.ForgetSigma1Replied)
		caseProvider.StartReaper(ctx, 30*time.Second, 60*time.Second)
		bridge.AttachCaseHandlerProvider(caseProvider.Resolve)
		logger.Info("matter.bridge.case.armed",
			slog.Uint64("node_id", mc.CASE.NodeID),
			slog.Uint64("fabric_id", mc.CASE.FabricID),
			slog.String("mode", "per-exchange"))
	}

	stop := func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if stopReannounce != nil {
			stopReannounce()
		}
		// Persist the diagnostics counters before tearing down the
		// store. RebootCount was already bumped at boot; here we add
		// the just-elapsed process uptime to BaseOperationalHours so
		// the next boot's TotalOperationalHours read picks up where we
		// left off.
		if rootRefs.GeneralDiagnostics != nil && store != nil {
			if rec, lerr := store.LoadDiagnostics(stopCtx); lerr == nil {
				delta := uint32(rootRefs.GeneralDiagnostics.UpTimeSeconds() / 3600) //nolint:gosec // hours fits uint32 for any realistic uptime
				rec.BaseOperationalHours += delta
				if serr := store.SaveDiagnostics(stopCtx, rec); serr != nil {
					logger.Warn("matter.bridge.diagnostics.save_shutdown", slog.String("err", serr.Error()))
				}
			}
		}
		if err := bridge.Stop(stopCtx); err != nil {
			logger.Warn("matter.bridge.stop", slog.String("err", err.Error()))
		}
		// Stop the subscription engine goroutine before closing the
		// DB so the Reporter never observes a torn-down store.
		subMgr.Stop()
		_ = db.Close()
	}
	return &matterBridgeBundle{
		bridge:         bridge,
		stop:           stop,
		store:          store,
		opMgr:          opMgr,
		opCreds:        opCreds,
		configuredPase: configuredPase,
		rootRefs:       rootRefs,
	}
}

// buildCaseAdapter constructs a sigma.Responder. When a fabric +
// node identity have been persisted (commissioner ran AddNOC +
// AddTrustedRootCertificate against the OperationalCredentials
// cluster) the responder uses the stored NOC + private key + root
// public key — peer-verification runs through [mattercert.Verifier]
// rooted at the stored RootPublicKey. When no fabric is persisted
// (first-boot / pre-commissioning) it falls back to an EPHEMERAL
// development identity with [trustAnyPeerVerifier]; CASE will reject
// every Sigma3 in that mode (the verifier hard-fails) but the path
// stays wired so PASE can still install a fabric and complete the
// flow on the next restart.
//
// `store` is the matter store that persists fabrics + identities.
// Pass nil to force the ephemeral fallback.
func buildCaseAdapter(ctx context.Context, cfg config.NorthMatterCASE, mgr *operational.Manager, store *matterstore.Store, logger *slog.Logger) (*matterbridge.CaseAdapter, error) {
	identity, verifier, fabricIndex, persisted, err := loadPersistentCaseIdentity(ctx, cfg, store, logger)
	if err != nil {
		return nil, err
	}
	if !persisted {
		ephem, ephemErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if ephemErr != nil {
			return nil, fmt.Errorf("ephemeral case key: %w", ephemErr)
		}
		identity = &sigma.Identity{
			PrivateKey: ephem,
			NodeID:     cfg.NodeID,
			FabricID:   cfg.FabricID,
			// NOC + ICAC + CompressedFabricID intentionally empty —
			// commissioner-side cert validation will reject. Acceptable
			// for development-only loop-back test rigs.
		}
		verifier = trustAnyPeerVerifier{}
		logger.Warn("matter.bridge.case.ephemeral_identity",
			slog.String("hint", "no persisted fabric — Sigma3 will reject; commissioner must run AddNOC first"))
	} else {
		logger.Info("matter.bridge.case.persistent_identity",
			slog.Int("fabric_index", int(fabricIndex)),
			slog.Uint64("node_id", identity.NodeID),
			slog.Uint64("fabric_id", identity.FabricID))
	}

	// SessionID 0 is reserved (unsecured), so allocate from 1.
	const initialSessionID uint16 = 1
	responder := sigma.NewResponder(identity, verifier, initialSessionID)
	// Emit responderSessionParams — see bridgeSessionParameters.
	responder.SetSessionParameters(bridgeSessionParameters())
	caseAdapter := matterbridge.NewCaseAdapter(responder)
	nodeID := identity.NodeID
	caseAdapter.SetOnSessionEstablished(func(keys sigma.SessionKeys, _ uint16) error {
		// Snapshot the responder so the post-Sigma3 peerNodeID flows into
		// the operational session entry (used as AES-CCM source NodeID for
		// inbound packets per Matter §4.5.1.4) and the resumption persist
		// below has access to the ECDH shared secret + resumption ID.
		// Mirrors the per-exchange CASE provider's onEstablished closure.
		peerNodeID := uint64(0)
		if resp := caseAdapter.SnapshotResponder(); resp != nil {
			peerNodeID = resp.PeerNodeID()
		}
		entry, err := mgr.OpenFromSigma(fabricIndex, nodeID, peerNodeID, keys)
		if err != nil {
			return err
		}
		logger.Info("matter.bridge.case.session_established",
			slog.Int("session_id", int(entry.SessionID)),
			slog.Uint64("node_id", nodeID),
			slog.Uint64("peer_node_id", peerNodeID))
		// Persist ECDH shared secret + resumption ID so Sigma2_Resume can
		// short-circuit the full Sigma1-3 handshake on the peer's next
		// reconnect (matter.js CaseServer.ts:210). Defensive guards: nil
		// snapshot or empty rid/secret skip the persist.
		if resp := caseAdapter.SnapshotResponder(); resp != nil {
			rid := resp.ResumptionID()
			secret := resp.ECDHSharedSecret()
			if len(rid) > 0 && len(secret) > 0 {
				if persistErr := mgr.PersistResumption(context.Background(), fabricIndex, peerNodeID, rid, secret); persistErr != nil {
					logger.Debug("matter.bridge.case.resumption_persist_failed",
						slog.Int("session_id", int(entry.SessionID)),
						slog.String("err", persistErr.Error()))
				}
			}
		}
		return nil
	})
	return caseAdapter, nil
}

// loadPersistentCaseIdentity attempts to construct a sigma.Identity
// + PeerVerifier from the matter store. The strategy:
//
//   - List fabrics; pick the one whose FabricID matches cfg.FabricID
//     (so multi-fabric daemons stay deterministic), else the lowest
//     FabricIndex when cfg.FabricID is 0.
//   - Load the matching IdentityRecord; convert the raw 32-byte
//     P-256 scalar back into an *ecdsa.PrivateKey.
//   - Build a [mattercert.Verifier] rooted at the fabric's
//     RootPublicKey so inbound NOCs are validated against the trust
//     anchor stored alongside the bridge's own identity.
//
// Returns persisted=false when no fabric matches; caller falls back
// to the ephemeral path.
func loadPersistentCaseIdentity(ctx context.Context, cfg config.NorthMatterCASE, store *matterstore.Store, logger *slog.Logger) (identity *sigma.Identity, verifier sigma.PeerVerifier, fabricIndex uint8, persisted bool, err error) {
	if store == nil {
		return nil, nil, 0, false, nil
	}
	fabrics, err := store.ListFabrics(ctx)
	if err != nil {
		logger.Warn("matter.bridge.case.list_fabrics", slog.String("err", err.Error()))
		return nil, nil, 0, false, nil
	}
	if len(fabrics) == 0 {
		return nil, nil, 0, false, nil
	}
	chosen := pickFabric(fabrics, cfg.FabricID)
	if chosen == nil {
		return nil, nil, 0, false, nil
	}
	id, err := store.GetIdentity(ctx, chosen.FabricIndex)
	if err != nil {
		logger.Warn("matter.bridge.case.get_identity",
			slog.Int("fabric_index", int(chosen.FabricIndex)),
			slog.String("err", err.Error()))
		return nil, nil, 0, false, nil
	}
	priv, err := privKeyFromScalar(id.PrivateKey)
	if err != nil {
		return nil, nil, 0, false, fmt.Errorf("matter case identity: %w", err)
	}
	verifier, err = mattercert.NewVerifier(chosen.RootPublicKey, mattercert.SystemTime{})
	if err != nil {
		return nil, nil, 0, false, fmt.Errorf("matter case verifier: %w", err)
	}
	opIPK, err := deriveOperationalIPK(id.IPK, chosen.CompressedID)
	if err != nil {
		logger.Warn("matter.bridge.case.load_ipk",
			slog.Int("fabric_index", int(chosen.FabricIndex)),
			slog.String("err", err.Error()))
		return nil, nil, 0, false, nil
	}
	identity = &sigma.Identity{
		NOC:                id.NOC,
		ICAC:               id.ICAC,
		PrivateKey:         priv,
		NodeID:             chosen.NodeID,
		FabricID:           chosen.FabricID,
		CompressedFabricID: chosen.CompressedID,
		IPK:                opIPK,
		FabricIndex:        chosen.FabricIndex,
	}
	return identity, verifier, chosen.FabricIndex, true, nil
}

// pickFabric chooses the fabric to back CASE: explicit FabricID match
// when cfg specifies one (and a row exists), else the lowest
// FabricIndex (deterministic for single-fabric daemons).
//
// The config-supplied `fabric_id` is a soft preference — every real
// fabric is installed via AddNOC with the *commissioner's* chosen
// FabricID (e.g. Apple Home picks a random 64-bit value), so pinning
// the daemon to `fabric_id: 1` would strand a freshly-paired ecosystem
// in the ephemeral path on the next restart. When the preference does
// not match any persisted fabric, fall back to the deterministic
// lowest-index row instead of returning nil.
func pickFabric(fabrics []matterstore.FabricRecord, wantFabricID uint64) *matterstore.FabricRecord {
	if wantFabricID != 0 {
		for i := range fabrics {
			if fabrics[i].FabricID == wantFabricID {
				return &fabrics[i]
			}
		}
		// Fall through to lowest-index fallback below.
	}
	lowest := &fabrics[0]
	for i := range fabrics[1:] {
		f := &fabrics[i+1]
		if f.FabricIndex < lowest.FabricIndex {
			lowest = f
		}
	}
	return lowest
}

// caseFabricEntry tracks one installed CASE fabric for the
// multi-fabric destination resolver — see the long comment at the
// `caseFabrics` declaration inside runDaemon.
type caseFabricEntry struct {
	identity      *sigma.Identity
	verifier      sigma.PeerVerifier
	rootPublicKey []byte // raw 0x04||X||Y, 65 bytes — input to the destinationID HMAC
	fabricIndex   uint8
}

// loadFabricRootPublicKey reads the raw P-256 root public key (65
// bytes 0x04||X||Y) for the given fabric_index from the matter store.
// Used by the daemon to populate the case-fabric registry on daemon
// start so [caseDestinationResolver] can match Sigma1's DestinationID
// before any AddNOC fires — required so a reboot doesn't lose the
// post-pair multi-fabric routing.
func loadFabricRootPublicKey(ctx context.Context, store *matterstore.Store, fabricIndex uint8) ([]byte, error) {
	if store == nil {
		return nil, fmt.Errorf("matter store: nil")
	}
	fabric, err := store.GetFabric(ctx, fabricIndex)
	if err != nil {
		return nil, fmt.Errorf("matter store: get fabric %d: %w", fabricIndex, err)
	}
	return append([]byte(nil), fabric.RootPublicKey...), nil
}

// loadAdditionalFabricsForCase rehydrates every persisted fabric apart
// from the seed (already loaded by the caller) into the
// `caseFabrics` registry so the multi-fabric destination resolver can
// satisfy a Sigma1 from ANY commissioned controller after a daemon
// restart. Apple's Multi-Admin pair installs two fabrics (iPhone Home
// + HomePod/Apple TV Hub) — without this, only the lowest-index one
// would have an identity available, and the other's MTRDevice would
// land in `MTRDeviceStateUnreachable` on first reconnect.
//
// Errors per row are logged at warn; the function never fails — a
// partial rehydration is strictly better than none. Returns the
// number of additional fabrics that were loaded.
func loadAdditionalFabricsForCase(
	ctx context.Context,
	store *matterstore.Store,
	seedIdx uint8,
	caseFabrics map[uint8]*caseFabricEntry,
	mu *sync.RWMutex,
	logger *slog.Logger,
) int {
	if store == nil {
		return 0
	}
	fabrics, err := store.ListFabrics(ctx)
	if err != nil {
		logger.Warn("matter.bridge.case.multi_fabric_list", slog.String("err", err.Error()))
		return 0
	}
	loaded := 0
	for i := range fabrics {
		f := fabrics[i]
		if f.FabricIndex == seedIdx {
			continue // already loaded by caller
		}
		id, idErr := store.GetIdentity(ctx, f.FabricIndex)
		if idErr != nil {
			logger.Warn("matter.bridge.case.multi_fabric_identity",
				slog.Int("fabric_index", int(f.FabricIndex)),
				slog.String("err", idErr.Error()))
			continue
		}
		priv, pkErr := privKeyFromScalar(id.PrivateKey)
		if pkErr != nil {
			logger.Warn("matter.bridge.case.multi_fabric_privkey",
				slog.Int("fabric_index", int(f.FabricIndex)),
				slog.String("err", pkErr.Error()))
			continue
		}
		verifier, verErr := mattercert.NewVerifier(f.RootPublicKey, mattercert.SystemTime{})
		if verErr != nil {
			logger.Warn("matter.bridge.case.multi_fabric_verifier",
				slog.Int("fabric_index", int(f.FabricIndex)),
				slog.String("err", verErr.Error()))
			continue
		}
		opIPK, ipkErr := deriveOperationalIPK(id.IPK, f.CompressedID)
		if ipkErr != nil {
			logger.Warn("matter.bridge.case.multi_fabric_ipk",
				slog.Int("fabric_index", int(f.FabricIndex)),
				slog.String("err", ipkErr.Error()))
			continue
		}
		identity := &sigma.Identity{
			NOC:                id.NOC,
			ICAC:               id.ICAC,
			PrivateKey:         priv,
			NodeID:             f.NodeID,
			FabricID:           f.FabricID,
			CompressedFabricID: f.CompressedID,
			IPK:                opIPK,
			FabricIndex:        f.FabricIndex,
		}
		entry := &caseFabricEntry{
			identity:      identity,
			verifier:      verifier,
			rootPublicKey: append([]byte(nil), f.RootPublicKey...),
			fabricIndex:   f.FabricIndex,
		}
		mu.Lock()
		caseFabrics[f.FabricIndex] = entry
		mu.Unlock()
		logger.Info("matter.bridge.case.multi_fabric_loaded",
			slog.Int("fabric_index", int(f.FabricIndex)),
			slog.Uint64("node_id", f.NodeID),
			slog.Uint64("fabric_id", f.FabricID))
		loaded++
	}
	return loaded
}

// caseDestinationResolver implements [sigma.IdentityResolver] by
// walking the daemon's `caseFabrics` map and matching every installed
// fabric's computed destinationID against the inbound Sigma1.
// Mirrors matter.js packages/protocol/src/fabric/FabricManager.ts:
// `findFabricFromDestinationId`. Returning (_, _, false) lets the
// responder fall through to its baseline identity (pre-AddNOC /
// single-fabric / test paths).
type caseDestinationResolver struct {
	mu      *sync.RWMutex
	fabrics *map[uint8]*caseFabricEntry
	logger  *slog.Logger
}

func (r caseDestinationResolver) ResolveSigma1Destination(destinationID [32]byte, initiatorRandom [sigma.RandomSize]byte) (*sigma.Identity, sigma.PeerVerifier, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.fabrics == nil {
		return nil, nil, false
	}
	for _, entry := range *r.fabrics {
		if entry == nil || entry.identity == nil {
			continue
		}
		cand := sigma.ComputeDestinationID(
			entry.identity.IPK,
			initiatorRandom,
			entry.rootPublicKey,
			entry.identity.FabricID,
			entry.identity.NodeID,
		)
		if cand == destinationID {
			if r.logger != nil {
				r.logger.Debug("matter.bridge.case.identity_resolved",
					slog.Int("fabric_index", int(entry.fabricIndex)),
					slog.Uint64("fabric_id", entry.identity.FabricID),
					slog.Uint64("node_id", entry.identity.NodeID))
			}
			return entry.identity, entry.verifier, true
		}
	}
	if r.logger != nil {
		r.logger.Debug("matter.bridge.case.identity_unresolved",
			slog.Int("installed_fabrics", len(*r.fabrics)))
	}
	return nil, nil, false
}

// deriveOperationalIPK turns the raw IPKValue persisted from
// AddNOC into the per-fabric "Operational Identity Protection Key"
// per Matter Core §11.2.4 (Group Identity Protection Key derivation):
//
//	operationalIPK = HKDF-SHA256(
//	    ikm  = rawIPK,                    // 16 bytes from AddNOC.IPKValue
//	    salt = compressedFabricID,        // 8 bytes
//	    info = "GroupKey v1.0",
//	    L    = 16,
//	)
//
// CASE Sigma2 / Sigma3 / secure-session salts use this derived key
// as their leading prefix; matter.js's `Fabric.create` does the same
// derivation. Skipping it (i.e. passing the raw IPKValue directly to
// the Sigma layer) makes Apple Home reject Sigma2 with
// SecureChannel/INVALID_PARAMETER because the responder produces a
// different S2K than the initiator.
func deriveOperationalIPK(rawIPK []byte, compressedFabricID [8]byte) ([16]byte, error) {
	var out [16]byte
	if len(rawIPK) != 16 {
		return out, fmt.Errorf("matter case ipk: raw IPK length %d, want 16", len(rawIPK))
	}
	derived, err := hkdf.Key(sha256.New, rawIPK, compressedFabricID[:], "GroupKey v1.0", 16)
	if err != nil {
		return out, fmt.Errorf("matter case ipk: hkdf: %w", err)
	}
	copy(out[:], derived)
	return out, nil
}

// privKeyFromScalar reconstructs an *ecdsa.PrivateKey from a 32-byte
// raw P-256 scalar (the storage format used by the matter store). The
// public key is computed via ScalarBaseMult.
func privKeyFromScalar(scalar []byte) (*ecdsa.PrivateKey, error) {
	if len(scalar) != 32 {
		return nil, fmt.Errorf("matter case identity: private key length %d, want 32", len(scalar))
	}
	curve := elliptic.P256()
	x, y := curve.ScalarBaseMult(scalar) //nolint:staticcheck // SA1019: curve API matches storage shape
	priv := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y},
		D:         new(big.Int).SetBytes(scalar),
	}
	return priv, nil
}

// trustAnyPeerVerifier is the explicit "CASE is not production-ready"
// sentinel sigma.PeerVerifier. It always returns an error so any
// real Sigma3 attempt fails loudly with "production verifier
// missing" — far better than the previous behaviour of returning a
// random ephemeral key (which caused Sigma3 signature validation to
// fail with an opaque crypto error and no clear signal that the
// fabric-identity wiring was missing).
//
// Replace with [mattercert.NewVerifier] once persistent fabric
// identity (NOC + ICAC + RootCert) is wired.
type trustAnyPeerVerifier struct{}

func (trustAnyPeerVerifier) VerifyAndExtractPubKey(_, _ []byte) (*ecdsa.PublicKey, error) {
	return nil, fmt.Errorf("trustAnyPeerVerifier: production verifier missing — wire mattercert.Verifier with persistent fabric identity")
}

// caseResumptionStoreAdapter bridges [sigma.ResumptionStore] to the
// operational [Manager]'s persistent record. Hands the responder a
// stable read-side so an inbound Sigma1 carrying a known
// ResumptionID can fast-path through Sigma2_Resume. Mirrors matter.js
// CaseServer.ts and chip CASESession.cpp resumption branch.
type caseResumptionStoreAdapter struct {
	mgr *operational.Manager
}

// GetByID implements [sigma.ResumptionStore].
func (a caseResumptionStoreAdapter) GetByID(resumptionID []byte) (*sigma.ResumptionRecord, error) {
	if a.mgr == nil {
		return nil, nil
	}
	rec, err := a.mgr.LookupResumption(context.Background(), resumptionID)
	if err != nil {
		// Treat any lookup failure as "no record": the responder will
		// fall through to the full Sigma1-3 handshake, which is the
		// correct behaviour when persistence is unavailable. The
		// nilerr suppression is deliberate — Sigma1 ID-not-found is
		// the documented "no record" signal and must not propagate.
		return nil, nil //nolint:nilerr // intentional fallthrough to full handshake
	}
	if len(rec.ResumptionID) == 0 || len(rec.SharedSecret) == 0 {
		return nil, nil
	}
	return &sigma.ResumptionRecord{
		ResumptionID: rec.ResumptionID,
		SharedSecret: rec.SharedSecret,
	}, nil
}

// rootClusterRefs carries the typed handles to selected root-endpoint
// cluster servers the daemon needs to drive lifecycle events (StartUp /
// ShutDown / Leave / BootReason) and persistence-seeded counters
// (RebootCount / TotalOperationalHours). The cluster servers are also
// in the slice returned alongside, so the bridge mounts them as usual;
// these refs are exclusively for daemon-side wiring.
type rootClusterRefs struct {
	BasicInformation     *mattercore.BasicInformation
	GeneralDiagnostics   *mattercore.GeneralDiagnostics
	GeneralCommissioning *mattercore.GeneralCommissioning
}

// buildRootClusters constructs the cluster servers the bridge mounts
// on endpoint 0. This is the surface chip-tool's
// `ReadCommissioningInfo` queries during commissioning — without
// these clusters the read returns `UnsupportedCluster` for every
// attribute and chip-tool aborts before installing a fabric.
//
// Returned set (Matter §11):
//
//   - 0x0028 BasicInformation         — VendorID, ProductID, NodeLabel, …
//   - 0x0030 GeneralCommissioning     — ArmFailSafe, RegulatoryConfig, …
//   - 0x0031 NetworkCommissioning     — wire interface info
//   - 0x0033 GeneralDiagnostics       — boot reason, network interfaces
//   - 0x003E OperationalCredentials   — NOC / fabric install (PASE → CASE)
//   - 0x003F GroupKeyManagement       — group quotas
//   - 0x0032 DiagnosticLogs           — placeholder
//   - 0x002A OTASoftwareUpdateRequestor — placeholder
//
// Operators that disable Matter via cfg.Enabled never reach this
// function. Construction errors are surfaced individually so a
// single misconfigured cluster cannot block the rest.
func buildRootClusters(mc config.NorthMatter, store *matterstore.Store, bridge *matterbridge.Bridge, adv mdns.Advertiser, logger *slog.Logger, onFabricInstalledExtra func(ctx context.Context, fabricIndex uint8, fabricID, nodeID uint64, rootPub []byte), adoptSessionForFabric func(ctx context.Context, fabricIndex uint8)) ([]interfaces.MatterClusterServer, *mattercore.OperationalCredentials, rootClusterRefs, error) {
	out := make([]interfaces.MatterClusterServer, 0, 8)
	var opCreds *mattercore.OperationalCredentials
	var refs rootClusterRefs

	// SerialNumber is a human-readable bridge identifier. ha-bridge
	// (`packages/backend/src/utils/json/create-bridge-server-config.ts`)
	// derives it from an MD5 hash of the bridge id; Apple Home's HAP-
	// service rebuild reads SerialNumber for accessory deduplication and
	// logs `could not find cached attribute values` when it is absent.
	// We synthesise a stable 16-char hex digest of
	// `VID|PID|NodeLabel` — distinct from UniqueID (full SHA-256
	// digest) so the matter.js
	// `basic-information-validators.ts:26` invariant
	// (`uniqueId !== serialNumber`) holds for the Root cluster too.
	rootSerial := func() string {
		h := sha256.Sum256([]byte(fmt.Sprintf("%04X|%04X|%s|serial",
			mc.VendorID, mc.ProductID, mc.NodeLabel)))
		return hex.EncodeToString(h[:8])
	}()
	bi, err := mattercore.NewBasicInformation(mattercore.Config{
		VendorID:        mc.VendorID,
		ProductID:       mc.ProductID,
		NodeLabel:       mc.NodeLabel,
		VendorName:      "openccu-loom",
		ProductName:     "openccu-loom Matter Bridge",
		SoftwareVersion: 1,
		HardwareVersion: 1,
		SerialNumber:    rootSerial,
	})
	if err != nil {
		return nil, nil, refs, fmt.Errorf("BasicInformation: %w", err)
	}
	out = append(out, bi)
	refs.BasicInformation = bi

	gc, err := mattercore.NewGeneralCommissioning(mattercore.GeneralCommissioningConfig{
		LocationCapability:           mattercore.RegulatoryIndoorOutdoor,
		SupportsConcurrentConnection: true,
		// L10-G fix: on a successful CommissioningComplete, revoke the
		// open enhanced-commissioning window — chip's
		// CommissioningWindowManager does this automatically. Lazy-resolves
		// the window through the bridge accessor to avoid the initialisation
		// ordering issue (the window is attached AFTER buildRootClusters
		// returns).
		OnCommissioningComplete: func(ctx context.Context, fabricIndex uint8) {
			w := bridge.CommissioningWindow()
			if w == nil {
				return
			}
			if err := w.RevokeWindow(ctx); err != nil {
				logger.Debug("matter.bridge.commissioning_window.revoke_on_complete",
					slog.Int("fabric_index", int(fabricIndex)),
					slog.String("err", err.Error()))
			}
		},
		// On FailSafe expiry (commissioner crashed mid-pair between
		// AddTrustedRootCertificate + CSRRequest + AddNOC and
		// CommissioningComplete) revoke the open commissioning window AND
		// let the OpCreds cluster clear its pending state
		// (pendingPrivKey, pendingTrustRoot, pendingCSRSessionID). Without
		// this hook a partial pair leaves the bridge in a state where the
		// next pair attempt collides with the orphan fabric row and Apple
		// Home aborts the second commissioning with FabricConflict.
		// OpCreds.handleRemoveFabric already clears pending state for the
		// active fabric; the FailSafe-expiry path catches the pre-AddNOC
		// race where no fabric exists yet.
		OnFailSafeExpired: func(ctx context.Context, fabricIndex uint8) {
			if w := bridge.CommissioningWindow(); w != nil {
				_ = w.RevokeWindow(ctx)
			}
			logger.Info("matter.bridge.failsafe.expired",
				slog.Int("fabric_index", int(fabricIndex)))
		},
	})
	if err != nil {
		return nil, nil, refs, fmt.Errorf("GeneralCommissioning: %w", err)
	}
	out = append(out, gc)
	refs.GeneralCommissioning = gc

	// NetworkCommissioning (0x0031) is MANDATORY on RootEndpoint per
	// matter.js HEAD `packages/model/src/standard/elements/root-node.element.ts:34`
	// (`conformance: "!CustomNetworkConfig"` = mandatory unless the
	// device exposes its own CustomNetworkConfig feature, which a
	// stationary Matter bridge never does). Without it Apple Home's
	// HMMTRAccessoryServerBrowser cannot determine "supported link
	// layer types" from Descriptor.ServerList and aborts the HAP
	// service rebuild with `Error retrieving supported link layers -
	// no topology` + `Nil supported link layer types`, leaving the
	// pair hanging at "Adding to Home" until the iOS Home UI gives
	// up with the "accessory could not be added" dialog.
	// FeatureMap = ETH (bit 2 = 0x4) — openccu-loom is a stationary
	// bridge running on an Ethernet/WiFi host; Ethernet-class is the
	// matter.js examples/device-onoff stack's choice too.
	netComm := mattercore.NewNetworkCommissioning(mattercore.NetworkCommissioningConfig{})
	out = append(out, netComm)

	// DiagnosticLogs (0x0032) is optional on RootEndpoint
	// (matter.js root-node.element.ts:35 `conformance: "O"`). Not
	// mounted here — the bridge has no diagnostic-log surface yet.
	gd := mattercore.NewGeneralDiagnostics(mattercore.BootReasonPowerOnReboot)
	out = append(out, gd)
	refs.GeneralDiagnostics = gd
	// OTASoftwareUpdateRequestor (0x002A) is intentionally NOT mounted
	// on the Root endpoint. matter.js's RootEndpoint
	// (packages/node/src/endpoints/root.ts:248-277) does not list it in
	// `optional` or `mandatory`; it is meant for a separate OTA-update
	// device-type composition. home-assistant-matter-bridge also does
	// not mount it. Apple Home's HAP service mapper reads
	// Descriptor.ServerList and rejects an unknown cluster on the
	// RootNode device type as schematic inconsistency.
	//
	// TimeSynchronization (0x0038) is intentionally NOT mounted on the
	// Root endpoint either. matter.js's RootEndpoint lists it in
	// `optional` only (root.ts:215), meant for hub/coordinator devices.
	// home-assistant-matter-bridge omits it. A Matter-Bridge has no
	// time-server role; Apple's HAP mapper similarly rejects the extra
	// cluster on RootNode.
	//
	// Re-add either cluster only if the bridge ever takes on the
	// corresponding device-class role (OTA provider / time
	// coordinator).
	// IcdManagement (0x0046) is intentionally NOT mounted on the Root
	// endpoint. Mirrors matter.js RootEndpoint
	// (packages/node/src/endpoints/root.ts:248-277): IcdManagement is
	// in `optional`, not `mandatory`. Strict controllers detect ICD-
	// status via `rootServerList.includes(IcdManagement.id)`
	// (NodePhysicalProperties.ts:33). Mounting the cluster makes them
	// treat the bridge as an Intermittently-Connected Device and
	// expect CheckIn-Protocol, StayActiveRequest, LIT/SIT features —
	// none of which a Matter-Bridge needs. Re-add only if the bridge
	// ever becomes a real ICD (battery-powered, long-idle).

	// Descriptor (0x001D) — MANDATORY on every Matter endpoint per
	// §9.5. Apple Home reads `DeviceTypeList`, `ServerList`, and
	// `PartsList` immediately after CommissioningComplete to confirm
	// the device type advertised matches a known Matter device-type
	// (RootNode 0x0016 here). Missing Descriptor surfaces as
	// UnsupportedCluster on the controller and the iCloud-Heim sync
	// step times out with a generic add-failed error.
	//
	// ServerList is the static set of clusters mounted on Endpoint 0;
	// PartsList enumerates the bridged dynamic endpoints (populated
	// after Reassemble via the bridge's endpoint registry, but the
	// initial value exposed here is empty — Apple re-reads after the
	// PartsList becomes non-empty).
	rootDescriptor, err := mattercore.NewDescriptor(
		[]mattercore.DeviceTypeStruct{
			// Matter Device Library §2.1 + matter.js's
			// `RootNodeDt.id = 0x16, revision = 4` (per
			// `packages/model/src/standard/elements/root-node.element.ts`
			// in matter.js HEAD). Every root endpoint MUST advertise the
			// RootNode device type. The schema table in
			// `internal/north/matter/schema/devicetypes.go` already
			// carries `0x0016: 4` — older hardcoded `Revision: 3` value
			// here drifted behind matter.js HEAD; Apple Home's HAP
			// mapper logs `Unable to find HAP service type for
			// deviceType 22` when an unexpected revision flows through.
			//
			// Apple-bridge topology: the Aggregator device type (0x000E)
			// is NOT advertised here — it lives on its own endpoint
			// (EP 1), exactly the way matter.js's
			// `examples/device-bridge-onoff/src/BridgedDevicesNode.ts`
			// composes a Matter bridge. Apple Home's
			// HMMTRAccessoryServerBrowser refuses to render bridged
			// devices when EP 0 carries both RootNode + Aggregator
			// simultaneously: it cannot disambiguate which endpoint to
			// crawl for the bridged-children PartsList and falls back to
			// "empty bridge" UX (pair lands, no devices visible).
			{DeviceType: 0x0016, Revision: matterschema.DeviceTypeRevisions[0x0016]},
		},
		// ServerList is derived AFTER the full endpoint composition via
		// SetServerListProvider (Bug K fix — see end of this function).
		// Mirrors matter.js DescriptorServer.#serverList
		// (DescriptorServer.ts:236-244): the advertised ServerList is
		// always the set of mounted behaviors, never a hardcoded list
		// that can diverge when a cluster is added without updating the
		// list. Apple's HAP mapper rejects an endpoint as schematically
		// inconsistent when the Subscribe stream contains AttributeReports
		// for a cluster not in ServerList (or vice versa).
		nil,
		nil, // ClientList — empty; we are not a controller
		nil, // PartsList — bridge dynamically populates after Reassemble
	)
	if err != nil {
		return nil, nil, refs, fmt.Errorf("descriptor: %w", err)
	}
	out = append(out, rootDescriptor)

	// AdministratorCommissioning (0x003C). Wires the bridge's
	// commissioning-window controller (set via
	// AttachCommissioningWindow earlier in the boot sequence) so
	// controllers can read WindowStatus + drive
	// OpenCommissioningWindow / RevokeCommissioning. Without a
	// window controller the cluster reports BUSY for every command,
	// which is the spec-correct behaviour.
	admComm := matterwire.NewAdministratorCommissioning()
	if w := bridge.CommissioningWindow(); w != nil {
		admComm.SetController(w)
	}
	if store != nil {
		admComm.SetVendorIDResolver(func(ctx context.Context, fabricIndex uint8) uint16 {
			rec, err := store.GetFabric(ctx, fabricIndex)
			if err != nil {
				return 0
			}
			return rec.VendorID
		})
		// Wire the fabric counter so the cluster can detect an
		// uncommissioned (factory) bridge. When FabricCount == 0 the
		// §11.19.8.1 OpenCommissioningWindow upper bound extends from
		// 900 s to 172 800 s (48 h) so a first-pairing window can outlast
		// the standard cap. Without this hook IsUncommissioned stays
		// false, the 900 s cap always applies, and a fresh bridge can
		// never open the long first-pairing window. Mirrors chip
		// CommissioningWindowManager::MaxCommissioningTimeout (= the 48h
		// bound when no fabrics are installed).
		admComm.SetFabricCounter(func(ctx context.Context) (int, error) {
			recs, err := store.ListFabrics(ctx)
			if err != nil {
				return 0, err
			}
			return len(recs), nil
		})
	}
	// Wire the FailSafe-armed accessor so OpenCommissioningWindow enforces
	// that no FailSafe window is active before opening a new one.
	// Mirrors chip CommissioningWindowManager + AdministratorCommissioningCluster
	// VerifyOrExit(IsFailSafeFullyDisarmed, ...).
	admComm.SetIsFailSafeArmed(gc.FailSafeArmed)
	out = append(out, admComm)

	if store != nil {
		// AccessControl (0x001F) is mandatory on the Root endpoint per
		// Matter Core §1.3.6.6. Apple Home reads `acl` immediately
		// after CASE; an absent cluster surfaces as
		// `UnsupportedCluster` for every subsequent fabric-scoped
		// read and Apple's UI tears the new fabric down via
		// RemoveFabric. AddNOC's default-entry path
		// (operational_credentials.handleAddNOC) populates the store
		// row this cluster reads back.
		if ac, err := mattercore.NewAccessControl(store); err == nil {
			out = append(out, ac)
		} else {
			logger.Warn("matter.bridge.access_control.build", slog.String("err", err.Error()))
		}
	}

	if store != nil {
		// DAC / PAI / CD bytes the bridge presents to commissioners.
		// Vendor-supplied bundle (mc.Attestation.*) wins when all
		// four files load + the DAC key matches the cert; otherwise
		// fall back to an ephemeral self-signed DAC that requires
		// chip-tool's `--bypass-attestation-verifier true` flag.
		var (
			dacKey *ecdsa.PrivateKey
			dac    []byte
			pai    []byte
			cd     []byte
		)
		if k, certBytes, paiBytes, cdBytes, vendorOK := loadVendorAttestation(mc.Attestation, logger); vendorOK {
			dacKey, dac, pai, cd = k, certBytes, paiBytes, cdBytes
			logger.Info("matter.bridge.attestation.vendor",
				slog.String("hint", "production DAC/PAI/CD loaded from config — chip-tool can validate without --bypass-attestation-verifier"))
		} else if chain, cdBytes, err := buildTestAttestation(mc.VendorID, mc.ProductID); err == nil {
			dacKey, dac, pai, cd = chain.DACKey, chain.DAC, chain.PAI, cdBytes
			logger.Info("matter.bridge.attestation.csa_test",
				slog.String("hint", "CSA Test PAA chain (PAA→PAI→DAC) + signed CD — Apple Home / Google Home / chip-tool accept this without --bypass-attestation-verifier"),
				slog.String("vendor_id", fmt.Sprintf("0x%04X", mc.VendorID)),
				slog.String("product_id", fmt.Sprintf("0x%04X", mc.ProductID)))
		} else {
			logger.Warn("matter.bridge.attestation.csa_test_failed", slog.String("err", err.Error()))
			var derr error
			dacKey, dac, pai, cd, derr = buildDevAttestation(mc.VendorID, mc.ProductID)
			if derr != nil {
				logger.Warn("matter.bridge.attestation.build", slog.String("err", derr.Error()))
			}
			logger.Warn("matter.bridge.attestation.dev",
				slog.String("hint", "ephemeral self-signed DAC fallback — chip-tool must run with --bypass-attestation-verifier true; iPhone / Google Home will reject"))
		}
		opcreds, err := mattercore.NewOperationalCredentials(store, mattercore.OpcredsConfig{
			// SupportedFabrics=0 → OpcredsConfig defaults to spec
			// maximum 254 per Matter §11.18.4.1. The previous cap of 5
			// rejected the 6th admin's AddNOC with TableFull and broke
			// Apple Multi-Admin chains that pair iPad + Apple Home Hub +
			// further controllers.
			SupportedFabrics:         0,
			DACPrivateKey:            dacKey,
			DAC:                      dac,
			PAI:                      pai,
			CertificationDeclaration: cd,
			OnMDNSReannounce: func(ctx context.Context) {
				// Withdraw the stale per-fabric _matter._tcp record after
				// RemoveFabric and republish the remaining fabric set so
				// commissioners do not resolve a CompressedFabricID that no
				// longer exists. Mirrors matter.js Fabric.remove() triggering
				// MdnsServer.reannounceInstance.
				if z, ok := adv.(*mdns.Zeroconf); ok {
					z.TriggerReannounce(ctx)
				}
			},
			OnFabricInstalled: func(ctx context.Context, fabricIndex uint8, fabricID, nodeID uint64, rootPub []byte) {
				// Adopt-Fabric on the invoking session FIRST so the
				// subsequent CommissioningComplete (and any ACL read /
				// group-key write) lands with the right accessing
				// fabric. chip OperationalCredentialsCluster.cpp:510-514
				// calls secureSession->AdoptFabricIndex(newFabricIndex)
				// BEFORE the ACL replace; we mirror the ordering. Apple
				// otherwise aborts the pair with HMMTRErrorDomain Code 9
				// "System Commissioner Pairing — Completing" because
				// CommissioningComplete arrives on the PASE session
				// (FabricIndex=0) and the access check fails.
				if adoptSessionForFabric != nil {
					adoptSessionForFabric(ctx, fabricIndex)
				}
				// Re-arm the GeneralCommissioning fail-safe to point at
				// the freshly-installed fabric. Matter Core §11.18.6.16:
				//
				//   "Successful operation of [AddNOC] SHALL also re-arm
				//   the fail-safe in such a way that the new fabric is
				//   the failsafe fabric. The remaining fail-safe expiry
				//   duration SHALL stay the same."
				//
				// Without this Apple's Multi-Admin pairing fails: Apple
				// arms FailSafe on the primary Hub's CASE session
				// (fabric=1), then AddNOC installs the system-commissioner
				// fabric (fabric=2), then Apple's iCloud Hub opens its
				// own CASE session on fabric 2 and calls
				// CommissioningComplete — our spec-correct fabric-match
				// check (handleCommissioningComplete:409) rejects with
				// InvalidAuthentication "session fabric 2 != failsafe
				// fabric 1" → Apple aborts with HMMTRErrorDomain Code 9
				// ("System Commissioner Pairing — Completing").
				// Mirrors matter.js packages/node/src/behaviors/
				// operational-credentials/OperationalCredentialsServer.ts
				// `#addNoc` → `failSafeContext.assignFabric(...)`.
				gc.SetCurrentFabric(fabricIndex)

				// Re-publish the operational mDNS record under the
				// just-installed fabric+node identity so commissioners
				// can resolve us via DNS-SD for fresh CASE handshakes.
				cid, derr := computeFabricCompressedID(rootPub, fabricID)
				if derr != nil {
					logger.Debug("matter.bridge.fabric.compressed_id",
						slog.Int("fabric_index", int(fabricIndex)),
						slog.String("err", derr.Error()))
					return
				}
				bridge.AnnounceFabric(ctx, cid, nodeID)
				logger.Info("matter.bridge.fabric.installed",
					slog.Int("fabric_index", int(fabricIndex)),
					slog.Uint64("fabric_id", fabricID),
					slog.Uint64("node_id", nodeID))
				// Fan the event out via the bridge so the WS event
				// publisher (wired post-buildRootClusters) sees it.
				bridge.EmitFabricAdded(fabricIndex)
				// Outer scope swaps the CASE responder to the freshly
				// persisted identity so Apple's follow-up Sigma1 lands
				// on a responder that signs Sigma2 with the real NOC.
				if onFabricInstalledExtra != nil {
					onFabricInstalledExtra(ctx, fabricIndex, fabricID, nodeID, rootPub)
				}
			},
		})
		if err != nil {
			logger.Warn("matter.bridge.opcreds.build", slog.String("err", err.Error()))
		} else {
			// Wire the FailSafe accessor so AddNOC, UpdateNOC, and
			// AddTrustedRootCertificate can enforce
			// Status::FailsafeRequired per Matter §11.18.6.4/.8/.9.
			// Without this any CASE session can mutate fabric state
			// without first arming a fail-safe window — chip + matter.js
			// both gate on `failSafeContext.IsFailSafeArmed(...)`.
			opcreds.SetIsFailSafeArmed(gc.FailSafeArmed)
			// Wire the ArmFailSafe-arming hook so every new arm (or
			// re-arm) wipes pending NOC / trust-root state on OpCreds.
			// Mirrors matter.js OperationalCredentialsServer.ts: a
			// FailSafeContext is freshly constructed on each arm, which
			// surfaces a clean `rootCertSet` / `fabricIndex`. Required
			// for Apple's multi-admin "SystemCommissioner" flow — the
			// second pair sequence (CSRRequest → AddTrustedRootCertificate
			// → AddNOC on the same CASE session after the iPhone fabric
			// is installed) needs the `nocWasInvoked` flag reset, else
			// AddTrustedRootCertificate fails with CONSTRAINT_ERROR
			// ("NOC command already invoked in this FailSafe window")
			// and Apple aborts with HMMTRAccessoryPairingStep_System
			// CommissionerPairing_FetchingRootCertificate.
			gcOpCreds := opcreds
			gc.SetOnFailSafeArmed(func(ctx context.Context, _ uint8) {
				gcOpCreds.ClearPendingState()
				_ = ctx
			})
			// Replace the constructor-time OnFailSafeExpired (window-revoke
			// only) with the production-correct hook that also clears
			// OpCreds pending state when the commissioner crashes mid-pair
			// (after ArmFailSafe + AddTrustedRootCertificate + CSRRequest
			// but before AddNOC, OR after AddNOC but before
			// CommissioningComplete). Without ClearPendingState the next
			// pair attempt trips on stale pendingPrivKey / pendingTrustRoot
			// / pendingCSRSessionID / nocWasInvoked and Apple Home aborts
			// the second commissioning with FabricConflict.
			//
			// Mirrors matter.js packages/node/src/behaviors/operational-
			// credentials/OperationalCredentialsServer.ts:#handleFailsafeClosed
			// which fires from fabricManager.events.failsafeClosed after the
			// FailSafeContext is torn down. chip's equivalent is
			// FailSafeContext::Reset() invoked from the expiry timer.
			gc.SetOnFailSafeExpired(func(ctx context.Context, fabricIndex uint8) {
				// OnFailSafeExpiry clears pending commissioning state AND
				// rolls back any half-paired fabric (pendingInstallFabricIndex)
				// that AddNOC committed but CommissioningComplete never confirmed.
				// Mirrors chip CommissioningWindowManager::OnFailSafeTimerExpired.
				gcOpCreds.OnFailSafeExpiry(ctx, fabricIndex)
				if w := bridge.CommissioningWindow(); w != nil {
					_ = w.RevokeWindow(ctx)
				}
				logger.Info("matter.bridge.failsafe.expired",
					slog.Int("fabric_index", int(fabricIndex)))
			})
			// Replace the constructor-time OnCommissioningComplete (window-revoke
			// only) with a hook that also clears OpCreds pending state — most
			// importantly pendingInstallFabricIndex. Without this reset a
			// subsequent ArmFailSafe → expiry cycle would call revertAddNOC
			// on the already-confirmed fabric and delete its ACL / GroupKey /
			// Fabric row. Mirrors chip FailSafeContext::Reset() on the success
			// path of CommissioningWindowManager::OnCommissioningComplete.
			gc.SetOnCommissioningComplete(func(ctx context.Context, fabricIndex uint8) {
				gcOpCreds.ClearPendingState()
				w := bridge.CommissioningWindow()
				if w == nil {
					return
				}
				if err := w.RevokeWindow(ctx); err != nil {
					logger.Debug("matter.bridge.commissioning_window.revoke_on_complete",
						slog.Int("fabric_index", int(fabricIndex)),
						slog.String("err", err.Error()))
				}
			})
			// Wire the commissioning-window predicate so ArmFailSafe can
			// distinguish a genuine CASE-steal attempt (FailSafe armed AND
			// window open) from a legitimate re-arm (FailSafe armed, no
			// window). Without this hook the nil-predicate defaults to true
			// and every CASE-fabric re-arm is rejected while another fabric's
			// FailSafe is active — even if no commissioning window is open.
			// Mirrors chip GeneralCommissioningCluster.cpp:419-427.
			gc.SetIsCommissioningWindowOpen(func() bool {
				w := bridge.CommissioningWindow()
				if w == nil {
					return false
				}
				return w.CurrentWindow().Status != matterwire.WindowStatusClosed
			})
			out = append(out, opcreds)
			opCreds = opcreds
		}
		gkm, err := mattercore.NewGroupKeyManagement(store, mattercore.GroupKeyMgmtConfig{})
		if err != nil {
			logger.Warn("matter.bridge.gkm.build", slog.String("err", err.Error()))
		} else {
			out = append(out, gkm)
		}
	}

	// Wire the Root Descriptor's ServerList provider to the assembled
	// cluster-server set. Mirrors matter.js DescriptorServer.#serverList
	// (DescriptorServer.ts:236-244) — the advertised ServerList is the
	// set of mounted behaviors. Closure captures `out` by reference so
	// any conditional append above is reflected. After this point `out`
	// must not be mutated; the bridge consumes the slice as-is.
	mounted := out
	rootDescriptor.SetServerListProvider(func() []uint32 {
		ids := make([]uint32, 0, len(mounted))
		seen := make(map[uint32]struct{}, len(mounted))
		for _, srv := range mounted {
			if srv == nil {
				continue
			}
			id := srv.MatterClusterID()
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		return ids
	})

	return out, opCreds, refs, nil
}

// buildAggregatorClusters constructs the cluster servers the bridge
// mounts on endpoint 1 (Aggregator). matter.js's
// `examples/device-bridge-onoff/src/BridgedDevicesNode.ts` places the
// AggregatorEndpoint on its own endpoint; matter.js's
// `packages/node/src/endpoints/aggregator.ts:60-63` lists Identify and
// Actions as optional. chip's reference bridge-app keeps both on the
// Aggregator (`connectedhomeip/examples/bridge-app/bridge-common/
// bridge-app.matter:2636-2647`).
//
// Returned set:
//
//   - 0x0003 Identify — mirrors chip's bridge-app composition. Apple's
//     HAP-Mapper has empirically required Identify on the Aggregator
//     endpoint to recognise it as a real endpoint and proceed to
//     traverse `Descriptor.PartsList` for bridged accessories.
//     Without it the Mapper has been observed to fall back to
//     `endpointDeviceTypes={0=(22)}` (RootNode only) and never
//     enumerate EP 2+. matter.js conformance lists Identify as
//     optional, so this is a conformance-strict addition not a spec
//     change.
//   - 0x001D Descriptor — DeviceTypeList=[{0x000E, rev 2}],
//     ServerList=[0x0003, 0x001D], PartsList=<dynamic provider,
//     populated by [matterbridge.Bridge.AttachAggregatorPartsListProvider]>.
//
// Actions (0x0025) is intentionally NOT mounted yet. chip's bridge-app
// ships it for the TC-BR test plan; we have no scene/action surface
// to model. Add when the bridge exposes Actions semantics.
func buildAggregatorClusters() ([]interfaces.MatterClusterServer, error) {
	aggregatorIdentify := mattercore.NewIdentify()
	aggregatorDescriptor, err := mattercore.NewDescriptor(
		[]mattercore.DeviceTypeStruct{
			// Matter Device Library §13.2 + matter.js's
			// `AggregatorDt.id = 0xe, revision = 2`.
			{DeviceType: 0x000E, Revision: 2},
		},
		// ServerList is wired below via SetServerListProvider so it
		// stays derived from the actually-mounted set, mirroring the
		// Root Descriptor Bug-K fix. A hardcoded static list here
		// would silently drift the moment Actions (0x0025) or another
		// Aggregator-side cluster is added.
		nil,
		nil, // ClientList — empty
		nil, // PartsList — dynamic, populated by AttachAggregatorPartsListProvider
	)
	if err != nil {
		return nil, fmt.Errorf("aggregator Descriptor: %w", err)
	}
	servers := []interfaces.MatterClusterServer{aggregatorIdentify, aggregatorDescriptor}
	mounted := servers
	aggregatorDescriptor.SetServerListProvider(func() []uint32 {
		ids := make([]uint32, 0, len(mounted))
		seen := make(map[uint32]struct{}, len(mounted))
		for _, srv := range mounted {
			id := srv.MatterClusterID()
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		return ids
	})
	return servers, nil
}

// announcePersistedFabric publishes the operational mDNS record for
// every previously-installed fabric on daemon boot. No-op when the
// matter store has no fabrics, or the lookup fails — commissioners
// fall back to direct-IP pairing in that case.
//
// Multi-Fabric advertise: Apple's Multi-Admin pair installs two fabrics
// (iPhone Home + HomePod/Apple TV Hub) — and a re-pair adds a third
// when the first goes stale. Each fabric's MTRDevice looks up its OWN
// `<compressedFabricID>-<nodeID>._matter._tcp.local` record over
// mDNS for CASE-reconnect. Publishing only the lowest-index fabric
// left every other commissioned controller's discovery empty and
// surfaced as "Bridge does not respond" in the Home App. The
// underlying mDNS advertiser (`zeroconf.Publish`) keys each register
// on `InstanceName + ServiceType`, so calling AnnounceFabric for
// every fabric publishes them all in parallel without overwriting
// each other.
func announcePersistedFabric(ctx context.Context, store *matterstore.Store, bridge *matterbridge.Bridge, logger *slog.Logger) {
	if store == nil || bridge == nil {
		return
	}
	fabrics, err := store.ListFabrics(ctx)
	if err != nil || len(fabrics) == 0 {
		return
	}
	for _, f := range fabrics {
		cid, err := computeFabricCompressedID(f.RootPublicKey, f.FabricID)
		if err != nil {
			logger.Debug("matter.bridge.fabric.compressed_id_boot",
				slog.Int("fabric_index", int(f.FabricIndex)),
				slog.String("err", err.Error()))
			continue
		}
		bridge.AnnounceFabric(ctx, cid, f.NodeID)
	}
}

// computeFabricCompressedID returns the 8-byte CompressedFabricID
// per Matter §4.13.2.4 — HKDF-SHA256(IKM=rootPubKey[1:],
// salt=fabricID-BE, info="CompressedFabric", L=8). Used by the
// daemon's OnFabricInstalled hook to compute the DNS-SD instance
// name for the operational mDNS record.
func computeFabricCompressedID(rootPubKey []byte, fabricID uint64) ([8]byte, error) {
	var out [8]byte
	if len(rootPubKey) != 65 || rootPubKey[0] != 0x04 {
		return out, fmt.Errorf("rootPubKey must be 65-byte uncompressed, got %d", len(rootPubKey))
	}
	salt := []byte{
		byte(fabricID >> 56), byte(fabricID >> 48), byte(fabricID >> 40), byte(fabricID >> 32), //nolint:gosec // G115: big-endian byte extraction; each shift result fits uint8
		byte(fabricID >> 24), byte(fabricID >> 16), byte(fabricID >> 8), byte(fabricID), //nolint:gosec // G115: big-endian byte extraction; each shift result fits uint8
	}
	der, err := hkdf.Key(sha256.New, rootPubKey[1:], salt, "CompressedFabric", 8)
	if err != nil {
		return out, err
	}
	copy(out[:], der)
	return out, nil
}

// loadVendorAttestation tries to load DAC/PAI/CD bytes plus the DAC
// private key from the operator-supplied paths. Returns
// (nil-everything, false) when any file is missing or unreadable —
// the caller falls back to [buildDevAttestation]. Returns a
// populated bundle + true on full load success.
//
// Format auto-detection: PEM blocks with `BEGIN CERTIFICATE` /
// `BEGIN EC PRIVATE KEY` / `BEGIN PRIVATE KEY` are decoded via
// `encoding/pem` + `crypto/x509`; raw DER bytes are detected by
// `0x30 0x82` ASN.1 sequence prefix. CD bytes are passed through
// verbatim — chip-tool decodes the CMS structure on its side.
//
// Errors at any step are logged at warn and the function returns
// false; the daemon then prints the dev-attestation hint.
func loadVendorAttestation(cfg config.NorthMatterAttestation, logger *slog.Logger) (key *ecdsa.PrivateKey, dac, pai, cd []byte, ok bool) {
	if cfg.DACPath == "" || cfg.DACKeyPath == "" || cfg.PAIPath == "" || cfg.CDPath == "" {
		return nil, nil, nil, nil, false
	}
	dac, err := readCertBytes(cfg.DACPath)
	if err != nil {
		logger.Warn("matter.bridge.attestation.dac", slog.String("path", cfg.DACPath), slog.String("err", err.Error()))
		return nil, nil, nil, nil, false
	}
	pai, err = readCertBytes(cfg.PAIPath)
	if err != nil {
		logger.Warn("matter.bridge.attestation.pai", slog.String("path", cfg.PAIPath), slog.String("err", err.Error()))
		return nil, nil, nil, nil, false
	}
	cd, err = os.ReadFile(cfg.CDPath)
	if err != nil {
		logger.Warn("matter.bridge.attestation.cd", slog.String("path", cfg.CDPath), slog.String("err", err.Error()))
		return nil, nil, nil, nil, false
	}
	key, err = readECDSAPrivateKey(cfg.DACKeyPath)
	if err != nil {
		logger.Warn("matter.bridge.attestation.dac_key", slog.String("path", cfg.DACKeyPath), slog.String("err", err.Error()))
		return nil, nil, nil, nil, false
	}
	// Sanity-check: the DAC private key must match the DAC cert's
	// public key. Mismatch silently fails commissioning later (chip-tool
	// signature-verifies the AttestationResponse against the cert), so
	// fail-fast here instead.
	cert, err := x509.ParseCertificate(dac)
	if err != nil {
		logger.Warn("matter.bridge.attestation.dac_parse", slog.String("err", err.Error()))
		return nil, nil, nil, nil, false
	}
	pub, ok2 := cert.PublicKey.(*ecdsa.PublicKey)
	//nolint:staticcheck // SA1019: pub.X/Y direct big.Int access kept — Matter wire shape (uncompressed P-256 point, 65 bytes) is checked here, not the modern crypto/ecdh `PublicKey.Bytes()` shape. Migration would force a wire-format audit.
	if !ok2 || pub.X.Cmp(key.X) != 0 || pub.Y.Cmp(key.Y) != 0 {
		logger.Warn("matter.bridge.attestation.key_mismatch",
			slog.String("hint", "DAC certificate public key does not match supplied private key — falling back to dev attestation"))
		return nil, nil, nil, nil, false
	}
	return key, dac, pai, cd, true
}

// readCertBytes reads a certificate file in PEM or raw DER format
// and returns the DER bytes. Auto-detects format by the leading
// bytes (`0x30 0x82` for DER ASN.1; otherwise PEM-decoded).
func readCertBytes(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) >= 2 && raw[0] == 0x30 && raw[1] == 0x82 {
		return raw, nil // DER
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}
	return block.Bytes, nil
}

// readECDSAPrivateKey reads a PKCS#8 or SEC1 P-256 ECDSA private
// key from `path`. PEM and DER both accepted via the same auto-
// detect logic as [readCertBytes].
func readECDSAPrivateKey(path string) (*ecdsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	der := raw
	if len(raw) < 2 || raw[0] != 0x30 || raw[1] != 0x82 {
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, fmt.Errorf("no PEM block in %s", path)
		}
		der = block.Bytes
	}
	// Try PKCS#8 first (most modern tooling default).
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		ec, ok := k.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%s: PKCS#8 key is not ECDSA", path)
		}
		return ec, nil
	}
	// Fall back to SEC1 (`BEGIN EC PRIVATE KEY`).
	return x509.ParseECPrivateKey(der)
}

// buildTestAttestation produces a fresh PAI + DAC chain rooted at the
// embedded CSA Test PAA, plus a CMS-signed Certification Declaration
// for the given (vendor, product) pair. This is the default the daemon
// presents when no operator-supplied attestation bundle is configured —
// matter.js follows the exact same pattern, and Apple Home, Google
// Home, and chip-tool all accept it without `--bypass-attestation`.
func buildTestAttestation(vendorID, productID uint16) (*attestation.Chain, []byte, error) {
	chain, err := attestation.BuildTestChain(vendorID, productID)
	if err != nil {
		return nil, nil, fmt.Errorf("build CSA test chain: %w", err)
	}
	cd, err := attestation.BuildTestCertificationDeclaration(vendorID, productID)
	if err != nil {
		return nil, nil, fmt.Errorf("build CSA test CD: %w", err)
	}
	return chain, cd, nil
}

// buildDevAttestation constructs an ephemeral DAC key + a
// self-signed DAC-shaped X.509 certificate. The PAI is the same
// cert (acts as its own intermediate; chip-tool's
// `--bypass-attestation-verifier true` skips chain validation). The
// CD is an empty placeholder — production deployments must wire
// CSA-signed CD bytes via config. Retained as the last-ditch fallback
// when even the CSA test chain fails to build (should never happen in
// practice — keeps the bridge bootable for diagnostic purposes).
//
// Returned material is cryptographically valid (chip-tool's
// AttestationResponse signature decodes), but NOT trusted by
// any production commissioner. Operators that ship a real device
// must replace via configuration.
func buildDevAttestation(vendorID, productID uint16) (dacKey *ecdsa.PrivateKey, dac, pai, cd []byte, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("dev DAC key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: fmt.Sprintf("Matter Dev DAC 0x%04X-0x%04X", vendorID, productID),
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(20 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("dev DAC cert: %w", err)
	}
	// Use the same cert for DAC + PAI in dev mode; real deployments
	// would generate a chain (PAA → PAI → DAC) per Matter §6.2.
	return priv, der, der, []byte{}, nil
}

// buildPaseAdapter constructs the Spake2+ verifier the bridge's PASE
// port consumes and wires its session-pickup callback to
// [operational.Manager.OpenFromPase] so a successful Pake3 lands a
// fresh session in the operational manager.
//
// **Single-verifier limitation**: the returned adapter wraps one
// shared Verifier; concurrent PASE attempts (rare — a commissioner
// retries one at a time) collide on Verifier state. Per-exchange
// adapter construction is a post-0.1.0 follow-up (lifecycle redesign).
// rotatingSerialPart mirrors the SerialNumber derivation in
// [rootSerial] so the Rotating Device Identifier draws on the same
// stable bridge identity tuple — keeps the commissionable `RI` TXT
// key consistent across daemon restarts without an extra
// persistence slot.
func rotatingSerialPart(vendorID, productID uint16, nodeLabel string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%04X|%04X|%s|serial", vendorID, productID, nodeLabel)))
	return hex.EncodeToString(h[:8])
}

func buildPaseAdapter(cfg config.NorthMatterCommissioning, mgr *operational.Manager, opCreds *mattercore.OperationalCredentials, gc *mattercore.GeneralCommissioning, logger *slog.Logger) (*matterbridge.PaseAdapter, error) {
	salt := []byte(cfg.Salt)
	if len(salt) == 0 {
		// Fixed development salt — never ship without setting Salt.
		salt = []byte("openccu-loom-dev0")
	}
	iterations := cfg.Iterations
	if iterations == 0 {
		iterations = 1000
	}
	return buildPaseAdapterFromCreds(cfg.Passcode, salt, iterations, mgr, opCreds, gc, logger)
}

// buildPaseAdapterFromCreds is the shared helper that turns a
// passcode + salt + iteration count into a fully wired
// [matterbridge.PaseAdapter]. Used by both the configured-mode build
// (read from `north.matter.commissioning.*`) and the ephemeral
// per-window provider (random passcode + salt regenerated per
// OpenCommissioningWindow call).
func buildPaseAdapterFromCreds(passcode uint32, salt []byte, iterations int, mgr *operational.Manager, opCreds *mattercore.OperationalCredentials, gc *mattercore.GeneralCommissioning, logger *slog.Logger) (*matterbridge.PaseAdapter, error) {
	// Reject trivially-guessable and out-of-range passcodes before deriving
	// the SPAKE2+ verifier. Mirrors chip src/crypto/CHIPCryptoPAL.cpp
	// IsValidSetupPIN which chip-tool calls at commissioning start.
	if !mattersetup.IsValidSetupPIN(passcode) {
		return nil, fmt.Errorf("matter: PASE passcode %d is invalid or trivially guessable", passcode)
	}
	vc, err := spake2.NewVerifierContext(passcode, salt, iterations)
	if err != nil {
		return nil, fmt.Errorf("spake2 verifier context: %w", err)
	}
	// Pre-allocate the operational session id BEFORE the adapter is
	// constructed. The id is embedded in PBKDFParamResponse as
	// ResponderSessionID; the commissioner echoes it back as
	// Header.SessionID on every post-PASE-establishment datagram
	// (IM reads, writes, invokes). Without pre-allocation the bridge
	// sends a fixed id (e.g. 1) in the response but registers the
	// actual session under a different dynamically-chosen id — the
	// inbound session lookup then misses, the datagram is dropped,
	// and chip-tool retransmits until max-retries. Mirrors the CASE
	// responder path which calls AllocateID before building Sigma2.
	sessID, err := mgr.AllocateID()
	if err != nil {
		return nil, fmt.Errorf("allocate PASE session id: %w", err)
	}
	// Use the factory variant so each Pake1 starts with a fresh
	// verifier whose context is bound to the actual PBKDFParam wire
	// bytes that crossed the channel (Matter §4.13.4). Empty
	// idA/idB per §3.10.4. The `context` argument the adapter
	// passes here is already the SPAKE2+ context HASH (32 bytes),
	// not the literal "CHIP PAKE V1 Commissioning" string.
	paseAdapter := matterbridge.NewPaseAdapterWithFactory(func(context []byte) *spake2.Verifier {
		return spake2.NewVerifier(vc, nil, nil, context)
	})
	// Wire the PBKDF response config so the adapter can answer
	// commissioner PBKDFParamRequest before Pake1 starts. Use the
	// pre-allocated sessID as ResponderSessionID so the commissioner
	// echoes back the correct id on post-PASE IM traffic.
	paseAdapter.SetPBKDFParams(uint32(iterations), salt, sessID) //nolint:gosec // iterations was validated by NewVerifierContext
	paseAdapter.SetOnSessionEstablished(func(sharedSecret []byte, peerSessionID uint16) error {
		// PASE pre-dates the operational fabric; both node ids ride
		// as the bridge-allocated PASE-temporary values per Matter
		// §4.13.2 (random ephemerals). Use 0/0 here.
		// Register the session under the pre-allocated sessID so the
		// inbound session lookup finds it when the commissioner sends
		// encrypted IM traffic with Header.SessionID==sessID.
		entry, err := mgr.OpenFromPaseWithID(sessID, 0, 0, peerSessionID, sharedSecret)
		if err != nil {
			mgr.ReleaseID(sessID)
			return err
		}
		// Plumb the AttestationChallenge into OperationalCredentials
		// so AttestationRequest / CSRRequest signatures bind to the
		// session per Matter §11.18.4.7. opCreds is nil when the
		// matter store is missing; in that case attestation falls
		// through to the all-zero stub.
		if opCreds != nil {
			opCreds.SetAttestationChallenge(entry.AttestationChallenge)
		}
		// Arm a 60-second FailSafe when no prior ArmFailSafe arrived.
		// Matter §11.10.5.2 sets the default expiry floor at 60 s;
		// the auto-arm ensures the fail-safe expiry hook fires and
		// cleans up any uncommitted state if the commissioner disappears
		// without calling CommissioningComplete.
		if gc != nil {
			gc.AutoArmOnPaseEstablished(context.Background())
		}
		logger.Info("matter.bridge.pase.session_established",
			slog.Int("session_id", int(entry.SessionID)))
		return nil
	})
	return paseAdapter, nil
}

// --- commissioning-window hook adapters ----------------------------------

// failSafeArmerAdapter wires the Matter fail-safe arm hook to
// GeneralCommissioning. Matter §11.19.6 mandates that
// OpenCommissioningWindow arm the fail-safe for the window duration.
//
// Mirrors matter.js packages/node/src/behaviors/administrator-commissioning/
// AdministratorCommissioningServer.ts:openCommissioningWindow → call to
// GeneralCommissioningBehavior.armFailSafeLogic.
type failSafeArmerAdapter struct {
	gc     *mattercore.GeneralCommissioning
	logger *slog.Logger
}

func (a *failSafeArmerAdapter) ArmFailSafeFor(ctx context.Context, seconds uint32, fabricIndex uint8) error {
	// Delegates to GeneralCommissioning.ArmFailSafeFor which arms the
	// timer per Matter §11.19.6. Mirrors matter.js
	// packages/node/src/behaviors/administrator-commissioning/
	// AdministratorCommissioningServer.ts:openCommissioningWindow →
	// GeneralCommissioningBehavior.armFailSafeLogic(timeoutSeconds) and
	// chip CommissioningWindowManager.cpp ArmFailSafe() call.
	if a.gc == nil {
		a.logger.Warn("failsafe.arm.skipped",
			slog.String("reason", "gc_nil"),
			slog.Int("seconds", int(seconds)),
			slog.Int("fabric_index", int(fabricIndex)))
		return nil
	}
	a.logger.Debug("failsafe.arm",
		slog.Int("seconds", int(seconds)),
		slog.Int("fabric_index", int(fabricIndex)))
	return a.gc.ArmFailSafeFor(ctx, seconds, fabricIndex)
}

// paseSessionCloserAdapter wires the PASE-session eviction hook to the
// operational session manager. Matter §11.19.7.3 step 1 mandates closing
// any open PASE session when the commissioning window is revoked.
//
// PASE sessions are stored with FabricIndex == 0 (pre-fabric, per Matter
// §4.13.2 / operational.Manager.OpenFromPase). CloseFabric(0) terminates
// all of them atomically.
//
// Mirrors matter.js packages/node/src/behaviors/administrator-commissioning/
// AdministratorCommissioningServer.ts:revokeCommissioning → call to
// paseCommissioner.close().
type paseSessionCloserAdapter struct {
	opMgr  *operational.Manager
	logger *slog.Logger
}

func (a *paseSessionCloserAdapter) ClosePaseSessions(_ context.Context) error {
	if a.opMgr == nil {
		a.logger.Debug("pase.close.skipped", slog.String("reason", "no_op_mgr"))
		return nil
	}
	// FabricIndex == 0 is the pre-fabric PASE bucket; CloseFabric closes
	// all sessions registered under that fabric index atomically.
	a.opMgr.CloseFabric(0)
	a.logger.Debug("pase.sessions.closed", slog.String("reason", "window_revoke"))
	return nil
}

// buildRateLimitConfig projects the YAML config into the middleware
// shape, returning nil when rate limiting is disabled so the router
// skips the middleware wiring entirely.
func buildRateLimitConfig(cfg *config.Config) *middleware.RateLimitConfig {
	rl := cfg.North.REST.RateLimit
	if !rl.Enabled {
		return nil
	}
	return &middleware.RateLimitConfig{
		RequestsPerSecond: rl.RequestsPerSecond,
		Burst:             rl.Burst,
	}
}

// runtimeCapabilityDetector implements handlers.CapabilityDetector with
// the runtime state captured at daemon-Deps construction time.
type runtimeCapabilityDetector struct {
	mqtt              bool
	matter            bool
	oidc              bool
	supervisedRestart bool
}

func (r runtimeCapabilityDetector) HasMQTTDiscovery() bool     { return r.mqtt }
func (r runtimeCapabilityDetector) HasMatterBridge() bool      { return r.matter }
func (r runtimeCapabilityDetector) HasOIDC() bool              { return r.oidc }
func (r runtimeCapabilityDetector) HasSupervisedRestart() bool { return r.supervisedRestart }

// detectSupervisedRestart reports whether the daemon is running
// under a supervisor that will restart it after a clean shutdown.
// The check is cheap + heuristic — we do not try to verify the
// supervisor's restart policy; we look for tight markers that
// imply the daemon's IMMEDIATE parent (or runtime) is a
// supervisor, not just the terminal session.
//
// Signals (any one fires):
//
//   - OPENCCU_LOOM_SUPERVISOR=1 — explicit operator override.
//   - JOURNAL_STREAM set AND getppid()==1 — systemd attached the
//     daemon's stdout/stderr to journald and re-parented it to PID 1
//     (the unambiguous "I am a systemd service" signal; INVOCATION_ID
//     alone is too lax because gnome-terminal inherits it).
//   - getppid()==1 AND /run/systemd/system exists — fallback when
//     JOURNAL_STREAM was suppressed but systemd is still PID 1.
//   - KUBERNETES_SERVICE_HOST set — Kubernetes injects it into
//     every Pod and the kubelet always restarts dead containers.
//   - /.dockerenv exists — Docker / Podman containers; restart
//     policy is operator-chosen but presence is the usual signal.
//
// Missing all of these means the binary is running on bare metal
// from a shell. The SPA disables the Restart-Daemon button in
// that case so an operator does not accidentally take the daemon
// permanently offline.
func detectSupervisedRestart() bool {
	if os.Getenv("OPENCCU_LOOM_SUPERVISOR") == "1" {
		return true
	}
	ppid := os.Getppid()
	if ppid == 1 {
		if os.Getenv("JOURNAL_STREAM") != "" {
			return true
		}
		if _, err := os.Stat("/run/systemd/system"); err == nil {
			return true
		}
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}

// startMDNSAdvertiser parses the REST listen address, builds the
// daemon-discovery TXT bundle, and starts a multicast advertiser.
// Returns (nil, nil) when the listen address has no usable port
// (Unix socket etc.) so the caller can skip without an error path.
// Returns (nil, err) when the port is malformed or zeroconf fails to
// register; the caller is expected to log and continue (mDNS is a
// convenience, not a hard dependency).
func startMDNSAdvertiser(cfg *config.Config, logger *slog.Logger) (discoverymdns.Advertiser, error) {
	port, ok := splitListenPort(cfg.North.REST.Listen)
	if !ok {
		return nil, nil
	}
	svc := discoverymdns.Service{
		InstanceName: cfg.North.Discovery.MDNS.InstanceName,
		Port:         port,
		TXT: []string{
			"path=/api/v1",
			"api_version=" + handlers.APIVersion,
			"tls=0",
		},
	}
	adv := discoverymdns.NewMulticast(svc)
	if err := adv.Start(context.Background()); err != nil {
		return nil, err
	}
	logger.Info("discovery.mdns.started",
		slog.String("service_type", discoverymdns.ServiceType),
		slog.Int("port", port))
	return adv, nil
}

// splitListenPort returns the TCP port from a Go net.Listen-style
// address (":8080", "0.0.0.0:8080", "[::]:8080"). Reports ok=false
// for addresses without a numeric port (e.g. Unix sockets, malformed
// strings) so the caller can degrade gracefully.
func splitListenPort(addr string) (int, bool) {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil || portStr == "" {
		return 0, false
	}
	var p int
	if _, err := fmt.Sscanf(portStr, "%d", &p); err != nil {
		return 0, false
	}
	if p <= 0 || p > 65535 {
		return 0, false
	}
	return p, true
}

// boolPtr returns a pointer to b. Used by the daemon-test
// fixtures that flip *bool config fields (NorthREST.Enabled,
// NorthUI.Enabled, …) into explicit true / false states.
func boolPtr(b bool) *bool { return &b }

// wsAllowedOrigins returns the list of origins the WebSocket handler
// should accept when CSRF protection is active. When CSRF is disabled
// (pure API-token setups without browser sessions) the list is empty
// and the handler skips the Origin check entirely. When CSRF is
// enabled the daemon derives the self-origin from its REST listen
// address so the embedded SPA — served on the same host:port — can
// always connect, and any CORS allowed-origins are appended as well.
func wsAllowedOrigins(cfg *config.Config) []string {
	if !cfg.North.REST.CSRFIsEnabled() {
		return nil
	}
	origins := make([]string, 0, 2)
	if port, ok := splitListenPort(cfg.North.REST.Listen); ok {
		origins = append(
			origins,
			fmt.Sprintf("http://localhost:%d", port),
			fmt.Sprintf("https://localhost:%d", port),
		)
	}
	origins = append(origins, cfg.North.REST.CORS...)
	return origins
}
