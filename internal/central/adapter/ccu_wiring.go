// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/observer"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/session"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// centralScopedValuesCache returns deps.ValuesCache when the filter
// (if any) allows the named central; nil otherwise. Keeps the
// per-central decision out of the pipeline wiring itself.
func centralScopedValuesCache(deps WireDeps, centralName string) *sqlite.ValuesCacheStore {
	if deps.ValuesCache == nil {
		return nil
	}
	if deps.ValuesCacheCentralFilter != nil && !deps.ValuesCacheCentralFilter(centralName) {
		return nil
	}
	return deps.ValuesCache
}

// connectionCheckerInterval is the cadence at which each interface's
// per-client probe goroutine fires (detection only — actual recovery
// is owned by ConnectionRecoveryCoordinator). 15s is the established
// upstream value: short enough that a CCU reboot is noticed within a
// scheduler tick, long enough that the CCU's RPC surface is not
// hammered.
const connectionCheckerInterval = 15 * time.Second

// WireDeps bundles the shared infrastructure [WireCentrals] needs
// beyond the config + registry pair. Fields marked optional can be
// nil — the daemon degrades gracefully (no callbacks wired).
type WireDeps struct {
	Writer          *client.ValueWriter
	Translations    *ccudata.Translations
	CallbackServer  *rpcserver.XMLRPCServer // optional
	CallbackBaseURL string                  // e.g. "http://192.168.1.20:8120"; required when CallbackServer != nil

	// BINRPCCallbackServer is the shared BIN-RPC TCP callback listener
	// for CUxD. Optional — CUxD interfaces are skipped when nil.
	BINRPCCallbackServer *rpcserver.BINRPCServer
	// BINRPCCallbackAddr is the effective host:port of BINRPCCallbackServer
	// as seen by the CCU (e.g. "192.168.1.20:8129"). Required when
	// BINRPCCallbackServer is non-nil.
	BINRPCCallbackAddr string

	// Backup, when non-nil, gets a [HTTPBackupRestorer] wired against
	// the first successfully-initialised central's JSON-RPC session.
	// Multi-CCU backup-source selection mirrors the BackupAdapter's
	// "first registered central" rule and is a follow-up.
	Backup *BackupAdapter

	// Visibility, when non-nil, is installed on every per-central
	// [DevicePipeline] via [DevicePipeline.WithVisibility] so the
	// southbound paramset hydration consults the visibility decider
	// before creating each generic data point. The required-parameter
	// whitelist must be pre-populated by the caller via
	// [visibility.Registry.SetRequiredParameters] (see daemon
	// composition root).
	//
	// nil disables the gate (all parameters pass through) — used in
	// tests and tooling that drive [WireCentrals] without a daemon
	// composition ( E.13).
	Visibility *visibility.Registry

	// MasterValues, when non-nil, is installed on every per-central
	// [DevicePipeline] via [DevicePipeline.WithMasterValuesStore]. The
	// pipeline then prefers the persisted MASTER snapshot over a fresh
	// getParamset(MASTER) call at hydration time, which is the only way
	// to avoid the CCU duty-cycle burst on a cold CCU+daemon-reboot.
	//
	// nil disables the cache (every channel hits the CCU at hydration).
	MasterValues *sqlite.MasterValuesStore

	// ValuesCache, when non-nil, is installed on every per-central
	// [DevicePipeline] via [DevicePipeline.WithValuesCacheStore]. The
	// pipeline applies the persisted wire-VALUES snapshot between
	// hydration and the live seedValues round so the SPA / MQTT /
	// Matter surfaces have the last known values immediately on boot.
	//
	// nil disables the cache (every cold boot starts with unobserved
	// data points until the first push event or fetch_all_device_data
	// round fills them).
	ValuesCache *sqlite.ValuesCacheStore

	// ValuesCacheCentralFilter, when non-nil, is consulted per
	// central before the [sqlite.ValuesCacheStore] is wired into
	// that central's [DevicePipeline]. Returns false to skip the
	// cache for that central — useful for excluding test-rig
	// centrals from the persistence path in multi-CCU setups. The
	// daemon's composition root builds this from the
	// `persistence.values_cache.disabled_centrals` config key.
	ValuesCacheCentralFilter func(centralName string) bool
}

// WireCentrals performs the full southbound bootstrap: per central it
// wires the hub (JSON-RPC), every configured interface (XML-RPC +
// backend + ingest + value seeding), and — when [WireDeps.CallbackServer]
// is set — registers the callback handler + announces the daemon's
// callback URL to the CCU so live events start flowing.
func WireCentrals(
	ctx context.Context,
	cfg *config.Config,
	reg *central.Registry,
	deps WireDeps,
	logger *slog.Logger,
) (func(), error) {
	writer := deps.Writer
	translations := deps.Translations
	if logger == nil {
		logger = slog.Default()
	}

	var (
		closers []func()
		joined  []string
	)
	teardown := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	for i := range cfg.Centrals {
		cc := &cfg.Centrals[i]
		unit, ok := reg.Get(cc.Name)
		if !ok {
			joined = append(joined, fmt.Sprintf("%s: not registered", cc.Name))
			continue
		}
		// Register callback handlers first so events that arrive during
		// (or right after) Init land on a live route.
		var callbackURL string
		if deps.CallbackServer != nil && deps.CallbackBaseURL != "" {
			handlers := NewCallbackHandlers(unit, logger)
			if deps.Writer != nil {
				handlers.SetWriter(deps.Writer)
			}
			deps.CallbackServer.Register(cc.Name, handlers)
			callbackURL = fmt.Sprintf("%s/RPC2/%s", strings.TrimRight(deps.CallbackBaseURL, "/"), cc.Name)
			centralName := cc.Name
			srv := deps.CallbackServer
			closers = append(closers, func() { srv.Deregister(centralName) })
		}

		// Hub wiring runs first so programs + sysvars are available and
		// so subsequent interface wiring can reuse the authenticated
		// Rega runner for value seeding. A hub-wiring failure leaves
		// the daemon functional at reduced fidelity.
		var (
			runner  *rega.Runner
			hubData HubData
		)
		if r, h, closer, err := WireHub(ctx, *cc, unit, logger); err != nil {
			joined = append(joined, fmt.Sprintf("%s/hub: %v", cc.Name, err))
			logger.Warn("wire.hub.failed",
				slog.String("central", cc.Name),
				slog.String("err", err.Error()))
		} else {
			runner = r
			hubData = h
			if closer != nil {
				closers = append(closers, closer)
			}
			// Wire the backup restorer the first time a central comes
			// up successfully — the BackupAdapter selects the first
			// central as its source (multi-CCU support is a
			// follow-up). Subsequent centrals do not overwrite the
			// existing restorer.
			if deps.Backup != nil && deps.Backup.Restorer() == nil {
				deps.Backup.SetRestorer(&HTTPBackupRestorer{
					BaseURL:               ccuBaseURLFor(*cc),
					Session:               runner.Client(),
					InsecureSkipTLSVerify: cc.TLSInsecureSkipVerify,
				})
				logger.Info("wire.backup.restorer_ready",
					slog.String("central", cc.Name))
			}
		}

		// Per-central interface→backend lookup, populated by
		// wireInterface as each iface comes up. The CONFIG_PENDING hook
		// uses it lazily to resolve the right backend at event time —
		// the hook is installed BEFORE the ifaces loop runs, so the
		// map starts empty and fills as the wiring progresses.
		backendsByInterface := newBackendRegistry()

		// Install the CONFIG_PENDING True→False handler so HmIP devices
		// re-sync MASTER values from the CCU (after a 10 s carenz) into
		// the persistent SQLite cache. Classic HM devices ignore this
		// signal and use the MasterPoller path.
		//
		// Note: getParamset(MASTER) on a non-battery device costs
		// duty-cycle — the CCU fetches the values directly from the
		// device by radio. CONFIG_PENDING True→False from the CCU
		// means it just completed its own sync round, so reading
		// straight after is the cheapest moment we get; that is the
		// only justification for this hook running at all. Anything
		// more aggressive (periodic refresh, recovery-triggered roll
		// across the whole installation) is unsafe.
		wireConfigPendingHook(unit, deps.MasterValues, cc.Name, backendsByInterface.getter, logger)

		// Wire the source-token lifecycle on the central's event bus.
		// ConnectionLost flips every wire DP to `stale`;
		// RecoveryCompleted flips them back to `live`. Independent of
		// the values_cache being enabled — the source token is
		// computed from the in-memory state machine on the data point
		// itself; persistence is just the survival layer.
		if closer := WireValueSourceLifecycle(unit, logger); closer != nil {
			closers = append(closers, closer)
		}

		pipeline := NewDevicePipeline(unit).
			WithTranslations(translations, cfg.Locale).
			WithNames(hubData.Names).
			WithRooms(hubData.Rooms).
			WithFunctions(hubData.Functions).
			WithVisibility(deps.Visibility).
			WithMasterValuesStore(deps.MasterValues, cc.Name).
			WithValuesCacheStore(centralScopedValuesCache(deps, cc.Name), cc.Name)

		// Build a Caller adapter for the hub's JSON-RPC session so
		// CcuBackend can dispatch JSON-RPC-only operations (install-mode,
		// service messages, renaming, sysvar/program calls). The adapter
		// is nil when hub wiring failed — CcuBackend falls back to
		// ErrUnsupported for those operations.
		var jCaller backends.Caller
		if runner != nil {
			jCaller = &jsonrpcCaller{client: runner.Client()}
		}

		for _, ifaceSpec := range cc.Interfaces {
			iface := hmenum.Interface(strings.TrimSpace(ifaceSpec.Name))
			closer, err := wireInterface(ctx, *cc, iface, unit, pipeline, writer, runner, callbackURL, cfg.Reliability, deps.MasterValues, backendsByInterface, jCaller, deps.BINRPCCallbackServer, deps.BINRPCCallbackAddr, logger)
			if err != nil {
				joined = append(joined, fmt.Sprintf("%s/%s: %v", cc.Name, iface, err))
				logger.Warn("wire.interface.failed",
					slog.String("central", cc.Name),
					slog.String("interface", string(iface)),
					slog.String("err", err.Error()))
				continue
			}
			if closer != nil {
				closers = append(closers, closer)
			}
			logger.Info("wire.interface.ok",
				slog.String("central", cc.Name),
				slog.String("interface", string(iface)))
		}

		// Wire the periodic data-refresh handler now that the pipeline and
		// Rega runner are established. The central.refresh_client_data
		// scheduler job (default 5 min) delegates here to re-run the
		// fetch-all-device-data sweep per interface — the reconciliation
		// safety net the push-event-first architecture relies on. Without
		// it the job failed every tick with "LoadAndRefreshDataPointData
		// not wired". Best-effort per interface, mirroring the boot-time
		// seed (IngestFromBackend → seedValues); a runner is required
		// because the sweep is a Rega script call. The scheduler gates the
		// job to the operational state, so the sweep never races bootstrap.
		if runner != nil {
			refreshPipeline, refreshRunner := pipeline, runner
			refreshIfaces := cc.Interfaces
			unit.SetLoadAndRefreshFn(func(ctx context.Context) error {
				var firstErr error
				for _, ifaceSpec := range refreshIfaces {
					id := strings.TrimSpace(ifaceSpec.Name)
					if id == "" {
						continue
					}
					if err := refreshPipeline.seedValues(ctx, id, refreshRunner, logger); err != nil && firstErr == nil {
						firstErr = err
					}
				}
				return firstErr
			})
		}

		// wire the sysvar creator after all interfaces are
		// registered so the primary client + backend are both available
		// when the first CreateSysvar* call arrives.
		WireSysvarCreator(unit, writer)
		// Same late-binding precondition: the backup-and-download handler
		// resolves the primary backend at trigger time.
		WireBackupAndDownload(unit, writer)
		logger.Info("wire.sysvar_creator.ok",
			slog.String("central", cc.Name))
	}
	if len(joined) > 0 {
		return teardown, fmt.Errorf("wire: %s", strings.Join(joined, "; "))
	}
	return teardown, nil
}

// backendRegistry is the central-scoped interface→backend lookup the
// CONFIG_PENDING hook consults at event time. Populated synchronously
// as each interface comes up in wireInterface.
type backendRegistry struct {
	mu sync.RWMutex
	m  map[string]backends.Operations
}

func newBackendRegistry() *backendRegistry {
	return &backendRegistry{m: make(map[string]backends.Operations)}
}

func (r *backendRegistry) put(interfaceID string, b backends.Operations) {
	if r == nil || interfaceID == "" || b == nil {
		return
	}
	r.mu.Lock()
	r.m[interfaceID] = b
	r.mu.Unlock()
}

// getter is a closure suitable for [wireConfigPendingHook]. Resolves
// the backend lazily at event-fire time so the wiring order between
// the hook installer and per-interface wiring stays simple.
func (r *backendRegistry) getter(interfaceID string) backends.MasterGetter {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	b := r.m[interfaceID]
	r.mu.RUnlock()
	if b == nil {
		return nil
	}
	return b
}

func wireInterface(
	ctx context.Context,
	cc config.CentralConfig,
	iface hmenum.Interface,
	unit *central.Unit,
	pipeline *DevicePipeline,
	writer *client.ValueWriter,
	runner *rega.Runner,
	callbackURL string,
	relCfg config.ReliabilityConfig,
	masterValues *sqlite.MasterValuesStore,
	backendReg *backendRegistry,
	jsonCaller backends.Caller,
	binrpcCallbackServer *rpcserver.BINRPCServer,
	binrpcCallbackAddr string,
	logger *slog.Logger,
) (func(), error) {
	// CUxD speaks BIN-RPC natively. It gets its own dedicated wiring
	// path: a BIN-RPC client (outbound calls), a BIN-RPC callback
	// registration (inbound push), and no XML-RPC client at all.
	if iface == hmenum.InterfaceCUxD {
		return wireCUxDInterface(ctx, cc, unit, pipeline, writer, runner, relCfg, masterValues, backendReg, binrpcCallbackServer, binrpcCallbackAddr, logger)
	}

	url, err := interfaceURL(cc, iface)
	if err != nil {
		return nil, err
	}

	// wireID is the canonical, host-independent interface identifier used
	// for all daemon-internal wiring (writer, registries, bus, stamping,
	// MQTT/REST surfaces). initID is the wire-boundary triple advertised to
	// the CCU at init()/deinit() — the CCU echoes it back in callbacks and
	// the inbound handler strips it back to wireID. See [WireInterfaceID] /
	// [InitInterfaceID] (ADR-0024).
	wireID := WireInterfaceID(cc.Name, iface)
	initID := InitInterfaceID(unit.InstanceName(), cc.Name, iface)

	xmlClient, err := xmlrpc.NewClient(xmlrpc.Config{
		URL:                url,
		Username:           cc.Username,
		Password:           cc.Password,
		Interface:          initID,
		Host:               cc.Host,
		InsecureSkipVerify: cc.TLSInsecureSkipVerify,
		Logger:             logger.With(slog.String("interface", wireID)),
		Observer: observer.NewMulti(
			observer.NewLogging(observer.WithLogger(logger), observer.WithSlowThreshold(2*time.Second)),
			observer.NewHealth(unit.Health),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("xmlrpc client: %w", err)
	}

	xmlCaller := &xmlrpcCaller{client: xmlClient}
	announcer := newXMLRPCAnnouncer(xmlClient)

	backendKind := backends.KindFor(iface)

	// W5/W6: create an InterfaceClient that wraps the transport caller
	// with the reliability stack (circuit breaker, retry, throttle,
	// coalescer, ping-pong). The BackendCaller bridges the IC's Call()
	// into the backends.Caller interface expected by the factory.
	//
	// client.Caller uses a []any params slice; xmlrpcCaller uses ...any
	// (backends.Caller convention). Bridge with CallerFunc so both
	// interfaces are satisfied without duplicating the transport.
	xmlSliceCaller := client.CallerFunc(func(ctx context.Context, method string, params []any) (any, error) {
		return xmlCaller.Call(ctx, method, params...)
	})
	// Build the session-recorder hook that forwards SetValue
	// PutParamset call traces to the CacheCoordinator recorder.
	// The hook is nil-safe on both ends: the IC skips the call
	// when nil and CacheCoordinator.RecordSession is a no-op when
	// no session recorder is wired. Closes the Item-2 gap in
	// (RecordSession-Wiring).
	var sessionHook func(rpcType, method string, params, response any)
	if unit.Cache != nil {
		cache := unit.Cache
		sessionHook = func(rpcType, method string, params, response any) {
			rpc := session.RPCTypeXML
			if rpcType == "json-rpc" {
				rpc = session.RPCTypeJSON
			}
			cache.RecordSession(rpc, method, params, response)
		}
	}

	icCfg := client.Config{
		CentralName:         cc.Name,
		Interface:           iface,
		Caller:              xmlSliceCaller,
		Enabled:             true,
		Logger:              logger.With(slog.String("interface", wireID)),
		SessionRecorderHook: sessionHook,
	}
	// L10/L11: operator-supplied reliability overrides. Both fields
	// default to openccu-loom's Go-idiomatic values when zero; setting
	// A positive duration pins behaviour. See
	// `config.example.yaml` (reliability: section) for the Python
	// reference values.
	if relCfg.CommandRetryInitialDelay > 0 {
		icCfg.Retrier = reliability.NewRetrier(reliability.RetryConfig{
			Initial: relCfg.CommandRetryInitialDelay,
		})
	}
	if relCfg.CommandThrottleInterCommandDelay > 0 {
		icCfg.Throttle = reliability.NewThrottle(reliability.ThrottleConfig{
			InterCommandDelay: relCfg.CommandThrottleInterCommandDelay,
		})
	}
	ic, err := client.New(icCfg)
	if err != nil {
		return nil, fmt.Errorf("interface client: %w", err)
	}
	bcaller := client.NewBackendCaller(ic, 0 /* Low priority — backends override per method */)

	backend, err := backends.FactoryWithKind(iface, backendKind, backends.FactoryInput{
		XMLRPC:    bcaller,
		JSONRPC:   jsonCaller,
		Announcer: announcer,
	})
	if err != nil {
		return nil, fmt.Errorf("backend factory: %w", err)
	}
	// Probe runtime capabilities once before the first operation.
	// Failures are soft: the backend keeps its conservative static defaults.
	if initErr := backends.MaybeInitialize(ctx, backend); initErr != nil {
		logger.Warn(
			"backend.initialize failed; using static capability defaults",
			slog.String("interface", string(iface)),
			slog.Any("err", initErr),
		)
	}

	// Wire the ReGa script runner and HTTP download transport into the CCU
	// backend so operations that require them (e.g. CreateBackupAndDownload,
	// DownloadFirmware) are reachable in production. Both setters are no-ops
	// on non-CCU backends; the type assertion ensures we only call them when
	// the concrete type is *backends.CcuBackend.
	if ccuBackend, ok := backend.(*backends.CcuBackend); ok {
		if runner != nil {
			ccuBackend.SetScriptRunner(runner)
		}
		hc := jsonrpcHTTPClient(cc)
		if hc == nil {
			hc = &http.Client{}
		}
		jc := runner
		var sessionIDFn func() string
		if jc != nil {
			sessionIDFn = jc.Client().SessionID
			// The backup download (cp_security.cgi) authenticates by session
			// id and serves a login page under HTTP 200 for a stale one, so
			// force a fresh login first — mirrors the reference stack's
			// login-or-renew before the backup download.
			rpcClient := jc.Client()
			ccuBackend.SetSessionRenewer(func(ctx context.Context) (string, error) {
				if err := rpcClient.Login(ctx); err != nil {
					return "", err
				}
				return rpcClient.SessionID(), nil
			})
		}
		ccuBackend.SetDownloadFirmwareTransport(ccuBaseURLFor(cc), hc, sessionIDFn)
	}

	// Register the backend so REST / MQTT command paths can dispatch.
	writer.Register(cc.Name, wireID, backend)

	// Also expose the backend through the per-central registry so the
	// CONFIG_PENDING hook can resolve it at event-fire time. The hook
	// was installed before this loop ran, so this is when the lookup
	// becomes usable.
	if backendReg != nil {
		backendReg.put(wireID, backend)
	}

	// Register the IC with the central's client coordinator so the
	// daemon can look up the IC by interface ID (used by, e.g., the
	// W4 CommandTracker hook in the ValueWriter and the metrics aggregator).
	if unit.Clients != nil {
		_ = unit.Clients.Register(&coordinators.ClientEntry{
			InterfaceID: wireID,
			Interface:   iface,
			Host:        cc.Host,
			Client:      ic,
		})
	}
	if unit.MetricsClients != nil {
		unit.MetricsClients.Register(ic)
	}

	// Publish a ClientStateChangedEvent on every state-machine transition
	// so WireHealth (health tracker → central-state re-evaluation) and
	// WireDeviceAvailability learn when the client connects. Keyed by
	// wireID to match the Clients registry + health component names.
	// Without this the startup connect (created→…→connected) is silent:
	// the health tracker never sees the client become healthy and the
	// central stays DEGRADED even though the interface is connected and
	// receiving callbacks.
	if unit.EventBus != nil {
		ic.SetStateChangedBus(unit.EventBus, wireID)
	}

	// W6: wire the IC's PingPong tracker to the central event bus and
	// the connection-recovery coordinator so threshold-crossing events
	// are published and false-alarm PING tracking is suppressed during
	// known outages.
	WirePingPongBus(unit, ic, wireID, unit.Recovery)

	// W5: install the eight-stage recovery pipeline so the coordinator runs
	// Cooldown → TCP_CHECKING → RPC_CHECKING → WARMING_UP →
	// STABILITY_CHECK → RECONNECTING → DATA_LOADING → RECOVERED after
	// every connection loss or circuit-breaker open event. The gates
	// prevent thundering-herd reconnects after power glitches.
	if unit.Recovery != nil {
		captured := ic
		capturedBackend := backend
		capturedWireID := wireID
		capturedInitID := initID
		capturedCallbackURL := callbackURL
		// Wire hub refresh into recovery so sysvar/program data is
		// reloaded after a successful reconnect.
		if unit.Hub != nil {
			unit.Recovery.SetHubRefresher(unit.Hub)
		}

		// Resolve the CCU's TCP address from the XML-RPC URL so the
		// TCP-probe stage can dial without knowing the per-interface port.
		ccuTCPAddr := cc.Host + ":2010" // fallback: CCU homematic2 port
		if parsed, parseErr := neturl.Parse(url); parseErr == nil && parsed.Host != "" {
			ccuTCPAddr = parsed.Host // already "host:port"
		}
		ccuTCPAddrCaptured := ccuTCPAddr

		deps := coordinators.RecoveryStageDeps{
			CooldownDuration: 3 * time.Second,
			WarmupDuration:   1 * time.Second,
			TCPProbe: func(ctx context.Context) error {
				conn, dialErr := (&net.Dialer{}).DialContext(ctx, "tcp", ccuTCPAddrCaptured)
				if dialErr != nil {
					return fmt.Errorf("tcp probe %s: %w", ccuTCPAddrCaptured, dialErr)
				}
				_ = conn.Close()
				return nil
			},
			RPCProbe: func(ctx context.Context) error {
				return capturedBackend.Ping(ctx, capturedInitID)
			},
			StabilityProbe: func(ctx context.Context) error {
				return capturedBackend.Ping(ctx, capturedInitID)
			},
			Reconnect: func(rctx context.Context) error {
				attempts := 0
				ok, err := captured.Reconnect(rctx, capturedBackend, capturedInitID, capturedCallbackURL, nil, &attempts)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("reconnect: CanReconnect returned false")
				}
				return nil
			},
			LoadData: unit.Recovery.RefreshHubDataAfterRecovery(),
		}
		unit.Recovery.WithPipelineFor(capturedWireID, coordinators.DefaultRecoveryPipeline(deps))
		// Wire the daemon logger so recovery.trigger / recovery.started /
		// recovery.completed / recovery.failed surface in the log
		// alongside the existing wire.init.ok / wire.reinit.ok lines.
		unit.Recovery.SetLogger(logger)
		unit.Recovery.Subscribe()
	}

	// Per-interface connection probe — pings the CCU every 30 s so the
	// circuit breaker advances OPEN → HALF_OPEN → CLOSED on its own
	// schedule. Without this loop the breaker only refreshes when an
	// unrelated code path happens to call Do(), which on a quiet daemon
	// can leave it stuck on OPEN for minutes after the CCU recovers.
	//
	// Driven by a standalone time.Ticker goroutine: the central's
	// scheduler is already running by the time WireCentrals fires
	// (reg.StartAll runs before WireCentrals in the daemon bootstrap),
	// so scheduler.Add would reject the late registration.
	probeCentral := cc.Name
	probeWireID := wireID
	probeIC := ic
	probeBus := unit.EventBus
	probeCtx, probeCancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(connectionCheckerInterval)
		defer ticker.Stop()
		publishLost := func() {
			if probeBus == nil {
				return
			}
			events.Publish(probeBus, hmevent.ConnectionLostEvent{
				CentralName: probeCentral,
				InterfaceID: probeWireID,
				Reason:      hmenum.FailureReasonNetwork,
			})
		}
		for {
			select {
			case <-probeCtx.Done():
				return
			case <-ticker.C:
				// Detection only. ConnectionLostEvent triggers the
				// ConnectionRecoveryCoordinator, which owns the
				// actual reconnect pipeline.
				//
				// Three independent signals — any one of them is
				// enough to publish ConnectionLost:
				//
				//  1. Ping fails (non-bypass check_connection — also
				//     drives the circuit breaker on the way through).
				//  2. State lag: client sits on DISCONNECTED / FAILED
				//     while the wire is healthy (previous reconnect
				//     attempt hit a transient 401 during the CCU's
				//     rega-startup window, or the daemon booted
				//     while the CCU was down — without this the
				//     daemon sits stale forever).
				//  3. Silent callback channel (no inbound event
				//     within callbackFreshness). IsCallbackAlive
				//     guards the post-init window so a freshly
				//     initialised client cannot trip the check
				//     before the first push event lands.
				if !probeIC.CheckConnectionAvailability(probeCtx, false) {
					publishLost()
					continue
				}
				switch probeIC.ClientState() {
				case hmenum.ClientStateDisconnected, hmenum.ClientStateFailed:
					publishLost()
					continue
				case hmenum.ClientStateCreated, hmenum.ClientStateInitializing,
					hmenum.ClientStateInitialized, hmenum.ClientStateConnecting,
					hmenum.ClientStateConnected, hmenum.ClientStateReconnecting,
					hmenum.ClientStateStopping, hmenum.ClientStateStopped:
					// Transient or active states — probe continues to callback-alive check.
				}
				if !probeIC.IsCallbackAlive() {
					publishLost()
				}
			}
		}
	}()
	logger.Info("wire.check_connection.started",
		slog.String("central", probeCentral),
		slog.String("interface", probeWireID),
		slog.Duration("interval", connectionCheckerInterval))

	// For classic HM interfaces (BidCos-RF, BidCos-Wired, VirtualDevices,
	// CUxD), construct a MasterPoller and wire its SchedulePoll as the
	// post-MASTER-write hook on every channel. HmIP interfaces use the
	// CONFIG_PENDING event path instead and get a nil hook (no polling).
	poller := newMasterPollerForInterface(iface, unit, backend, masterValues, wireID, cc.Name, logger)
	if poller != nil {
		pipeline.WithMasterRefreshHook(poller.SchedulePoll)
	} else {
		pipeline.WithMasterRefreshHook(nil)
	}

	// Pull the device snapshot and hydrate data points. Without this
	// the domain stays empty and every `/api/v1/devices` call returns
	// nothing; without the per-channel paramset load the devices have
	// no parameters underneath their channels.
	//
	// On failure (e.g. transient http 401 during the CCU's
	// rega-startup window) the client is already registered with
	// `unit.Clients` in state CREATED. Walk it to DISCONNECTED before
	// returning so the recovery pipeline's CanReconnect probe accepts
	// the state on the first retry — without this every subsequent
	// recovery.trigger is rejected with "CanReconnect returned false"
	// and the daemon needs a manual restart to come up.
	if err := pipeline.IngestFromBackend(ctx, wireID, iface, backend, writer, runner, logger); err != nil {
		// Ingest failed before the closer was registered — stop the
		// connection-probe goroutine here so it does not outlive the
		// half-wired client and keep probing a backend nobody owns.
		probeCancel()
		if poller != nil {
			poller.Close()
		}
		ensureDisconnectedClientState(ic, logger)
		return nil, fmt.Errorf("ingest: %w", err)
	}
	logger.Info("wire.ingest.ok",
		slog.String("central", cc.Name),
		slog.String("interface", wireID),
		slog.Int("devices", unit.ModelRegistry.Len()))

	// Schedule a background paramset-consistency check for the HmIP-RF
	// interface. HmIP-RF and HmIP-Wired devices both arrive through this
	// single service and are affected by the HmIPServer stale-files bug
	// after firmware updates. The check runs asynchronously so it does
	// not block the wiring path; mismatches are logged and can be used
	// to drive re-ingest or alerts.
	if unit.Devices != nil && iface == hmenum.InterfaceHmIPRF {
		var deviceAddrs []string
		for _, d := range unit.ModelRegistry.List() {
			if d.InterfaceID == wireID && !strings.Contains(d.Address, ":") {
				deviceAddrs = append(deviceAddrs, d.Address)
			}
		}
		if len(deviceAddrs) > 0 {
			unit.Devices.ScheduleParamsetConsistencyCheck(
				context.Background(), iface, deviceAddrs, backend,
				func(inconsistencies []coordinators.ParamsetInconsistency) {
					for _, inc := range inconsistencies {
						logger.Warn("wire.paramset_inconsistency",
							slog.String("central", cc.Name),
							slog.String("interface", wireID),
							slog.String("device", inc.DeviceAddress),
							slog.Int("missing", len(inc.MissingParameters)))
					}
				},
			)
		}
	}

	// Wire the on-demand value loader on every device that belongs to
	// this interface. The cache + singleflight on the device picks up
	// from here; subsequent `LoadValue` calls (REST / WS reads,
	// reconciler sweeps, RELEVANT_INIT_PARAMETERS bootstrap) coalesce
	// concurrent loads for the same channel/parameter through it.
	for _, d := range unit.ModelRegistry.List() {
		if d.InterfaceID != wireID {
			continue
		}
		d.SetValueLoader(backend)
	}

	// RELEVANT_INIT_PARAMETERS bootstrap
	// `init_base_data_points` (model/device.py:1934-1977) explicitly
	// loads UNREACH / STICKY_UN_REACH / CONFIG_PENDING on channel 0
	// because fetch_all_device_data does not always include them. The
	// daemon's availability tracking depends on these values being
	// present, so we mirror the explicit load here. Errors are logged
	// at debug level — the daemon still works without these (just with
	// availability defaulted to "reachable" until the first push event).
	seedRelevantInitParameters(ctx, unit, iface, logger)

	// Readable-events bootstrap
	// (model/device.py:1947-1958) explicitly loads every event DP that
	// reports as readable. fetch_all_device_data only ships DPs with a
	// non-zero timestamp, so events that have not fired since the last
	// CCU restart end up unobserved otherwise — REST/MQTT consumers
	// then see "unknown" until the user actually presses the button.
	seedReadableEvents(ctx, unit, iface, logger)

	// Announce the callback URL to the CCU so it starts pushing live
	// events. A non-callback-capable setup (no server, no URL) skips
	// this step and leaves the daemon in read-through mode.
	if callbackURL != "" {
		// Pre-Init Deinit: tell the CCU to forget any callback URL
		// previously registered for this interface_id before we
		// install the fresh one. Mirrors the recovery pipeline's
		// ReinitProxy (interface_client.go:653) two-step sequence.
		// A previous daemon-run that died without invoking the
		// shutdown closer (SIGKILL, panic, host reboot, pair-test
		// restart) leaves a dangling registration on the CCU; the
		// CCU then fans state-echo events to the orphan URL and our
		// fresh process never receives them — the live-Subscribe
		// path (Matter, WS Hub, MQTT) then reports stale state
		// indefinitely. Best-effort: a Deinit failure does not abort
		// the subsequent Init (the CCU may already have timed the
		// old registration out, or this is a first-ever boot).
		if err := backend.Deinit(ctx, initID); err != nil {
			logger.Debug("wire.deinit.pre_init",
				slog.String("central", cc.Name),
				slog.String("interface", initID),
				slog.String("err", err.Error()))
		}
		// Snapshot the last-event monotonic timestamp before the init
		// call. If the CCU's `init` RPC times out — a known
		// VirtualDevices-service-bug pattern — but the listDevices
		// callback was nonetheless dispatched, the event coordinator
		// stamps a fresh time during init. Treating that as success
		// matches the reference init_proxy fallback at
		// interface_client.py:749-781. Best-effort: a missing event
		// coordinator (test fixture) leaves the snapshot at zero, the
		// post-error comparison short-circuits to "no callback seen",
		// and the legacy hard-failure log fires.
		var preInitEventAt time.Time
		if unit.Events != nil {
			if at, ok := unit.Events.LastEventMonotonicForInterface(wireID); ok {
				preInitEventAt = at
			}
		}
		if err := backend.Init(ctx, initID, callbackURL); err != nil {
			callbackSeen := false
			if unit.Events != nil {
				if at, ok := unit.Events.LastEventMonotonicForInterface(wireID); ok && at.After(preInitEventAt) {
					callbackSeen = true
				}
			}
			if callbackSeen {
				logger.Info("wire.init.timeout_callback_received",
					slog.String("central", cc.Name),
					slog.String("interface", wireID),
					slog.String("callback", callbackURL),
					slog.String("err", err.Error()),
					slog.String("hint", "CCU processed init() despite RPC timeout; callback received during init window"))
				ensureConnectedClientState(ic, logger)
			} else {
				logger.Warn("wire.init.failed",
					slog.String("central", cc.Name),
					slog.String("interface", wireID),
					slog.String("err", err.Error()))
				// Walk CREATED → INITIALIZING → FAILED → DISCONNECTED so
				// the recovery pipeline finds a CanReconnect-friendly
				// state on the first probe success. Without this the
				// client sits in CREATED forever once the boot-time
				// init() failed, and every subsequent recovery.trigger
				// is rejected with "CanReconnect returned false".
				ensureDisconnectedClientState(ic, logger)
			}
		} else {
			logger.Info("wire.init.ok",
				slog.String("central", cc.Name),
				slog.String("interface", wireID),
				slog.String("callback", callbackURL))
			// Walk the client state forward so the recovery pipeline
			// sees a CanReconnect-friendly state on the next CCU
			// outage. Without this the state stays at CREATED, and
			// every recovery.trigger fails immediately with
			// "CanReconnect returned false".
			ensureConnectedClientState(ic, logger)
		}
	}

	// Closer deregisters the callback + unregisters the backend writer
	// on daemon shutdown. The XML-RPC client itself is stateless.
	centralName := cc.Name
	ifaceID := wireID
	deinitID := initID
	closer := func() {
		// Stop the connection-probe goroutine first so the next tick
		// does not race against the backend being torn down.
		probeCancel()
		// Stop the MasterPoller before deregistering so in-flight polls
		// do not race against the backend being torn down.
		if poller != nil {
			poller.Close()
		}
		if callbackURL != "" {
			deinitCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			if err := backend.Deinit(deinitCtx, deinitID); err != nil {
				logger.Debug("wire.deinit",
					slog.String("central", centralName),
					slog.String("interface", deinitID),
					slog.String("err", err.Error()))
			}
			cancel()
		}
		writer.Deregister(centralName, ifaceID)
		if unit.Clients != nil {
			unit.Clients.Remove(ifaceID)
		}
		if unit.MetricsClients != nil {
			unit.MetricsClients.Deregister(iface)
		}
	}
	return closer, nil
}

// ensureDisconnectedClientState walks the InterfaceClient's state machine
// from CREATED → INITIALIZING → FAILED → DISCONNECTED so the recovery
// coordinator can subsequently transition into RECONNECTING. Used when
// the boot-time init() failed (CCU unreachable at daemon start).
func ensureDisconnectedClientState(ic *client.InterfaceClient, logger *slog.Logger) {
	if ic == nil {
		return
	}
	transitions := []struct {
		target hmenum.ClientState
		reason string
	}{
		{hmenum.ClientStateInitializing, "wire.init.failed: created→initializing"},
		{hmenum.ClientStateFailed, "wire.init.failed: initializing→failed"},
		{hmenum.ClientStateDisconnected, "wire.init.failed: failed→disconnected (ready for reconnect)"},
	}
	for _, t := range transitions {
		if err := ic.TransitionTo(t.target, t.reason, false, hmenum.FailureReasonNetwork); err != nil {
			logger.Debug("wire.init.state_transition_skipped",
				slog.String("target", string(t.target)),
				slog.String("err", err.Error()))
		}
	}
}

// ensureConnectedClientState walks the InterfaceClient's state machine
// from its current CREATED state through INITIALIZING → INITIALIZED →
// CONNECTING → CONNECTED so the connection-loss recovery pipeline can
// later transition into DISCONNECTED + RECONNECTING. Invalid moves are
// silently skipped — the state machine validates each step. Logged at
// Debug so the boot path stays readable.
func ensureConnectedClientState(ic *client.InterfaceClient, logger *slog.Logger) {
	if ic == nil {
		return
	}
	transitions := []struct {
		target hmenum.ClientState
		reason string
	}{
		{hmenum.ClientStateInitializing, "wire.init.ok: created→initializing"},
		{hmenum.ClientStateInitialized, "wire.init.ok: initializing→initialized"},
		{hmenum.ClientStateConnecting, "wire.init.ok: initialized→connecting"},
		{hmenum.ClientStateConnected, "wire.init.ok: connecting→connected"},
	}
	for _, t := range transitions {
		if err := ic.TransitionTo(t.target, t.reason, false, hmenum.FailureReasonNone); err != nil {
			logger.Debug("wire.init.state_transition_skipped",
				slog.String("target", string(t.target)),
				slog.String("err", err.Error()))
		}
	}
}

// interfaceURL composes the XML-RPC endpoint for (central, interface)
// using the SPECIFICATION §7.2 detection ports. CUxD is BIN-RPC only
// and therefore rejected here — callers that want CUxD must wire the
// BIN-RPC caller separately.
func interfaceURL(cc config.CentralConfig, iface hmenum.Interface) (string, error) {
	if iface == hmenum.InterfaceCUxD {
		return "", fmt.Errorf("CUxD requires a BIN-RPC caller; XML-RPC wiring is not applicable")
	}
	ports, ok := hmenum.DetectionPorts[iface]
	if !ok {
		return "", fmt.Errorf("no known port for interface %q", iface)
	}
	port := ports.Plain
	scheme := "http"
	if cc.TLS {
		if ports.TLS == 0 {
			return "", fmt.Errorf("interface %q has no TLS port", iface)
		}
		port = ports.TLS
		scheme = "https"
	}
	// Per-interface override takes precedence over the central-wide
	// fallback so operators can pin, e.g., HmIP-RF to a non-standard
	// port without disturbing other interfaces.
	if p, ok := cc.Ports[string(iface)]; ok && p > 0 {
		port = p
	} else if cc.Port > 0 {
		port = cc.Port
	}
	// Path mirrors the CCU's XML-RPC routing: /RPC2 is the default
	// endpoint, /groups is the VirtualDevices variant. POSTing to the
	// bare "/" path causes the CCU's putParamset handler to crash
	// internally (Vert.x NPE or fault -5) while reads still succeed —
	// keep paths explicit. Operators with non-standard CCU routing
	// can override via the per-interface remote_path config field.
	path := "/RPC2"
	if iface == hmenum.InterfaceVirtualDevices {
		path = "/groups"
	}
	return fmt.Sprintf("%s://%s:%d%s", scheme, cc.Host, port, path), nil
}
