// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/observer"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/devicedetails"
	"github.com/SukramJ/openccu-loom/internal/store/session"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// wireCUxDInterface wires the BIN-RPC outbound client, CuxdBackend,
// callback registration, and pipeline ingest for a single CUxD interface.
//
// CUxD speaks BIN-RPC natively — the XML-RPC wiring path used for
// HmIP-RF / BidCos / VirtualDevices is not applicable here. Each
// CUxD interface gets its own outbound [binrpc.Client] plus a
// registration on the shared [rpcserver.BINRPCServer] callback listener.
func wireCUxDInterface( //nolint:funlen,gocognit // composition/wiring: long sequential setup
	ctx context.Context,
	cc config.CentralConfig,
	unit *central.Unit,
	pipeline *DevicePipeline,
	writer *client.ValueWriter,
	runner *rega.Runner,
	relCfg config.ReliabilityConfig,
	masterValues *sqlite.MasterValuesStore,
	backendReg *backendRegistry,
	binrpcCallbackServer *rpcserver.BINRPCServer,
	binrpcCallbackAddr string,
	logger *slog.Logger,
) (func(), error) {
	iface := hmenum.InterfaceCUxD
	port := hmenum.DefaultBINRPCPort
	if ov := interfacePortOverride(cc, iface); ov > 0 {
		port = ov
	}
	addr := fmt.Sprintf("%s:%d", cc.Host, port)
	// wireID is the canonical host-independent id used for all internal
	// wiring; initID is the wire-boundary triple used for the CUxD init()
	// and the BIN-RPC callback-server registration (CUxD routes its
	// callbacks by interface_id). See [WireInterfaceID] / [InitInterfaceID].
	wireID := WireInterfaceID(cc.Name, iface)
	initID := InitInterfaceID(unit.InstanceName(), cc.Name, iface)

	binClient, err := binrpc.NewClient(binrpc.Config{
		Addr:      addr,
		Interface: initID,
		Logger:    logger.With(slog.String("interface", wireID)),
		Observer: observer.NewMulti(
			observer.NewLogging(observer.WithLogger(logger), observer.WithSlowThreshold(2*time.Second)),
			observer.NewHealth(unit.Health),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("binrpc client: %w", err)
	}

	binCaller := &binrpcCaller{client: binClient}
	announcer := newBINRPCAnnouncer(binClient)

	binSliceCaller := client.CallerFunc(func(ctx context.Context, method string, params []any) (any, error) {
		return binCaller.Call(ctx, method, params...)
	})
	// Order-preserving sibling for the device-definition export (CUxD speaks the
	// XML-RPC value set over BIN-RPC, so member order is recoverable here too).
	binOrderedCaller := client.OrderedCallerFunc(func(ctx context.Context, method string, params []any) (any, error) {
		return binCaller.CallOrdered(ctx, method, params...)
	})

	// Forward CUxD call traces to the session recorder under the BIN-RPC
	// type so a replay can distinguish them from the XML-RPC interfaces.
	// Nil-safe on both ends: the IC skips a nil hook and RecordSession is
	// a no-op until a recorder is wired and active.
	var sessionHook func(rpcType, method string, params, response any)
	if unit.Cache != nil {
		cache := unit.Cache
		sessionHook = func(_, method string, params, response any) {
			cache.RecordSession(session.RPCTypeBIN, method, params, response)
		}
	}

	icCfg := client.Config{
		CentralName:         cc.Name,
		Interface:           iface,
		InitInterfaceID:     initID,
		Caller:              binSliceCaller,
		OrderedCaller:       binOrderedCaller,
		Enabled:             true,
		Logger:              logger.With(slog.String("interface", wireID)),
		SessionRecorderHook: sessionHook,
	}
	// The retrier is always built here (not defaulted inside
	// client.New) so its exhausted-chain incident sink is installed
	// from the start.
	icCfg.Retrier = newClientRetrier(unit, wireID, relCfg.CommandRetryInitialDelay) //nolint:contextcheck // incident hooks are fire-and-forget and outlive the wiring ctx by design
	// Independent per-RPC-class throttle pools (read / write / control) so a
	// backing-off write does not block reads or liveness pings behind one
	// permit. See [perClassThrottlePools].
	icCfg.ReadThrottle, icCfg.WriteThrottle, icCfg.ControlThrottle = perClassThrottlePools(relCfg.CommandThrottleInterCommandDelay)
	ic, err := client.New(icCfg)
	if err != nil {
		return nil, fmt.Errorf("interface client: %w", err)
	}
	wireClientReliability(unit, ic, wireID) //nolint:contextcheck // incident hooks are fire-and-forget and outlive the wiring ctx by design
	bcaller := client.NewBackendCaller(ic, hmenum.CommandPriorityLow)

	backend, err := backends.FactoryWithKind(iface, backends.KindCUxD, backends.FactoryInput{
		BINRPC:    bcaller,
		Announcer: announcer,
	})
	if err != nil {
		return nil, fmt.Errorf("backend factory: %w", err)
	}
	if initErr := backends.MaybeInitialize(ctx, backend); initErr != nil {
		logger.Warn(
			"backend.initialize failed; using static capability defaults",
			slog.String("interface", string(iface)),
			slog.Any("err", initErr),
		)
	}

	writer.Register(cc.Name, hmtypes.ParseWireInterfaceID(wireID), backend)
	if backendReg != nil {
		backendReg.put(wireID, backend)
	}
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

	// Publish ClientStateChangedEvent on state transitions (keyed by
	// wireID) so the health tracker + device-availability wiring learn
	// when CUxD connects — otherwise the central stays DEGRADED after a
	// successful startup connect. See the matching call in ccu_wiring.go.
	if unit.EventBus != nil {
		ic.SetStateChangedBus(unit.EventBus, wireID)
	}

	WirePingPongBus(unit, ic, wireID, unit.Recovery)

	// Register BIN-RPC callback handlers so CUxD push events land.
	var callbackURL string
	var cbHandlers *CallbackHandlers
	if binrpcCallbackServer != nil && binrpcCallbackAddr != "" {
		handlers := NewCallbackHandlers(unit, logger)
		if writer != nil {
			handlers.SetWriter(writer)
		}
		handlers.SetDelayNewDeviceCreation(cc.Behavior.DelayNewDeviceCreationEnabled())
		binrpcCallbackServer.Register(initID, handlers)
		// Also answer under the id a pre-prefix release registered. CUxD
		// keys its callback registrations in a way the URL-only deinit does
		// not clear, so that registration survives the upgrade and CUxD
		// delivers every event twice — once here, once to the orphan. Left
		// unregistered the orphan's copy is rejected and logged for the life
		// of the daemon; registered, it is a duplicate the ingest pipeline
		// already collapses. Skipped when the two ids coincide.
		if legacyID := LegacyInitInterfaceID(unit.InstanceName(), cc.Name, iface); legacyID != initID {
			binrpcCallbackServer.Register(legacyID, handlers)
		}
		// xmlrpc_bin:// is the scheme every CCU-side component uses for a
		// BIN-RPC callback endpoint, and the one the CCU prints in its own
		// handler lists. CUxD itself accepts anything here — it speaks no
		// other protocol — but an unrecognised scheme makes the entry
		// unreadable for an operator comparing handler lists.
		callbackURL = "xmlrpc_bin://" + binrpcCallbackAddr
		cbHandlers = handlers
	}

	wireCUxDRecovery(unit, cuxdRecoveryTarget{
		ic:          ic,
		backend:     backend,
		cc:          cc,
		wireID:      wireID,
		initID:      initID,
		callbackURL: callbackURL,
		cuxdAddr:    addr,
	}, logger)

	poller := newMasterPollerForInterface(iface, unit, backend, masterValues, wireID, cc.Name, logger) //nolint:contextcheck // poller callback uses context.Background(); outlives the wiring ctx by design
	// Keyed by wireID: the poller reads through CUxD's own backend, and the
	// pipeline it is registered on serves every interface of the central.
	if poller != nil {
		pipeline.WithMasterRefreshHook(wireID, poller.SchedulePoll)
	} else {
		pipeline.WithMasterRefreshHook(wireID, nil)
	}

	// Set once activate() installed the hot-plug ingestor, so the closer
	// only resets the seam it actually claimed.
	var hotplugInstalled atomic.Bool

	// Boot-time activation of this interface. Wrapped in activate() so a
	// transient failure can be retried in the background instead of leaving
	// the interface permanently empty: CUxD is an addon that starts
	// independently of ReGaHss, so its BIN-RPC port is regularly still
	// closed when the readiness gate reports the CCU up. Without a retry the
	// first listDevices failure is terminal for this interface — the central
	// latches southbound-ready with zero CUxD devices and nothing re-runs the
	// ingest until the daemon restarts. Mirrors the XML-RPC path's retry in
	// wireInterface.
	activate := func(activateCtx context.Context) error {
		if err := pipeline.IngestFromBackend(activateCtx, wireID, iface, backend, writer, runner, logger); err != nil {
			return fmt.Errorf("ingest: %w", err)
		}
		logger.Info("wire.ingest.ok",
			slog.String("central", cc.Name),
			slog.String("interface", wireID))

		// Hot-plug: CUxD announces newly created virtual devices through the
		// same newDevices callback shape. Installed AFTER the bring-up ingest
		// (and before init announces the callback), mirroring the XML-RPC
		// path's ordering rationale in bringUpCentral.
		if cbHandlers != nil {
			var ddLoader *devicedetails.Loader
			if runner != nil {
				ddLoader = devicedetails.NewLoaderForJSONRPC(unit.DeviceDetails, runner.Client(), cc.Name, logger)
			}
			cuxdBackend := backend
			unit.SetDeviceIngestFn(newHotplugIngestor(
				unit, pipeline, writer, runner,
				func(string) backends.Operations { return cuxdBackend },
				ddLoader, logger,
			))
			hotplugInstalled.Store(true)
		}

		// (Re)establish hub-data-point device links now that this interface's
		// devices are materialised — see assignHubChannels. Idempotent across the
		// multiple per-interface ingests of one central.
		assignHubChannels(unit, logger)

		for _, d := range unit.ModelRegistry.List() {
			if d.InterfaceID != wireID {
				continue
			}
			d.SetValueLoader(backend)
		}

		seedRelevantInitParameters(activateCtx, unit, iface, logger)
		seedReadableEvents(activateCtx, unit, iface, logger)

		announceCUxDCallback(activateCtx, backend, ic, cc.Name, initID, callbackURL, logger)
		return nil
	}

	runCUxDActivation(ctx, cuxdIngestBackoff, activate, ic, cc.Name, wireID, logger)

	// The closer is returned unconditionally — every registration above
	// (writer backend, client entry, metrics client, BIN-RPC callback
	// routes) happens before the ingest, so an ingest that never succeeds
	// must still be releasable on teardown.
	//nolint:contextcheck // shutdown path must not inherit the already-expired wiring ctx; see deinitOnShutdown
	closer := func() {
		if binrpcCallbackServer != nil {
			deinitOnShutdown(backend, callbackURL, cc.Name, initID, logger)
			binrpcCallbackServer.Deregister(initID)
			if legacyID := LegacyInitInterfaceID(unit.InstanceName(), cc.Name, iface); legacyID != initID {
				binrpcCallbackServer.Deregister(legacyID)
			}
		}
		// Drain the callback handler's background goroutines (self-reload /
		// device-refresh) after the route is deregistered, mirroring the
		// XML-RPC deregister path in registerCentralCallbacks.
		if cbHandlers != nil {
			cbHandlers.Stop()
		}
		if poller != nil {
			poller.Close()
		}
		if hotplugInstalled.Load() {
			unit.SetDeviceIngestFn(nil)
		}
		writer.Deregister(cc.Name, hmtypes.ParseWireInterfaceID(wireID))
		if unit.Clients != nil {
			unit.Clients.Remove(wireID)
		}
		if unit.MetricsClients != nil {
			unit.MetricsClients.Deregister(iface)
		}
	}
	return closer, nil
}

// announceCUxDCallback tells CUxD where to push its events. A blank
// callbackURL means this deployment has no BIN-RPC listener, so the
// interface stays in read-through mode. Best-effort: a failed announce is
// logged, because the recovery pipeline re-announces on its next cycle.
func announceCUxDCallback(
	ctx context.Context,
	backend backends.Operations,
	ic *client.InterfaceClient,
	centralName, initID, callbackURL string,
	logger *slog.Logger,
) {
	if callbackURL == "" {
		return
	}
	// Pre-Init Deinit: tell CUxD to forget any registration made for this
	// callback URL before installing the fresh one. Best-effort — a previous
	// run may have left none.
	if err := backend.Deinit(ctx, callbackURL); err != nil {
		logger.Debug("wire.deinit.pre_init",
			slog.String("central", centralName),
			slog.String("interface", initID),
			slog.String("err", err.Error()))
	}
	if err := backend.Init(ctx, initID, callbackURL); err != nil {
		logger.Warn("wire.init.failed",
			slog.String("central", centralName),
			slog.String("interface", initID),
			slog.String("err", err.Error()))
		return
	}
	logger.Info("wire.init.ok",
		slog.String("central", centralName),
		slog.String("interface", initID))
	// Drive the client state machine to CONNECTED — the XML-RPC path does
	// this too. Without it CUxD sits at its initial state, so the central
	// check_connection job sees ClientState != CONNECTED and keeps flagging
	// the interface as a (false) connection loss.
	ensureConnectedClientState(ic, logger)
}

// cuxdIngestBackoff is the boot-time retry schedule for the CUxD ingest.
// Same shape as the XML-RPC path: a handful of short waits that cover the
// window in which the CUxD addon is still starting up behind a CCU that
// already reports ready.
var cuxdIngestBackoff = []time.Duration{
	1 * time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 15 * time.Second,
}

// runCUxDActivation drives activate through the boot-time retry schedule.
// It returns once the activation succeeded, the retries are spent, or the
// wiring context is cancelled; the interface is reported as wired either
// way, because the caller's closer owns the registrations made before the
// first attempt.
func runCUxDActivation(
	ctx context.Context,
	backoff []time.Duration,
	activate func(context.Context) error,
	ic *client.InterfaceClient,
	centralName, wireID string,
	logger *slog.Logger,
) {
	for attempt := 0; ; attempt++ {
		err := activate(ingestAttemptContext(ctx, attempt, len(backoff)))
		if err == nil {
			return
		}
		if attempt >= len(backoff) {
			// Every retry is spent and the interface stayed empty. Walk the
			// client to DISCONNECTED so the recovery pipeline finds a
			// CanReconnect-friendly state on its first probe success.
			logger.Error("wire.interface.ingest_failed",
				slog.String("central", centralName),
				slog.String("interface", wireID),
				slog.String("err", err.Error()))
			ensureDisconnectedClientState(ic, logger)
			return
		}
		logger.Debug("wire.interface.ingest_retry",
			slog.String("central", centralName),
			slog.String("interface", wireID),
			slog.Int("attempt", attempt+1),
			slog.String("err", err.Error()))
		t := time.NewTimer(backoff[attempt])
		select {
		case <-ctx.Done():
			t.Stop()
			ensureDisconnectedClientState(ic, logger)
			return
		case <-t.C:
		}
	}
}

// cuxdRecoveryTarget bundles what the recovery stages need to reach one
// CUxD interface.
type cuxdRecoveryTarget struct {
	ic          *client.InterfaceClient
	backend     backends.Operations
	cc          config.CentralConfig
	wireID      string
	initID      string
	callbackURL string
	cuxdAddr    string
}

// wireCUxDRecovery installs the recovery pipeline for a CUxD interface.
//
// Without one the interface cannot repair itself: a ConnectionLostEvent
// reaches the coordinator, finds no pipeline registered for this id, and is
// dropped with `recovery.skip reason=no_pipeline_registered` at debug level.
// Every other interface got its pipeline from the XML-RPC wiring path, which
// CUxD returns before reaching — so CUxD was the one interface for which a
// connection loss was terminal until the daemon restarted.
//
// Same stages as the XML-RPC path, with one difference: the TCP probe dials
// CUxD's own BIN-RPC port instead of a CCU interface port, because that is
// the socket whose loss this pipeline is recovering from.
func wireCUxDRecovery(unit *central.Unit, t cuxdRecoveryTarget, logger *slog.Logger) {
	if unit == nil || unit.Recovery == nil || t.callbackURL == "" {
		return
	}
	unit.Recovery.WithPipelineFor(t.wireID, coordinators.DefaultRecoveryPipeline(coordinators.RecoveryStageDeps{
		CooldownDuration: 3 * time.Second,
		WarmupDuration:   1 * time.Second,
		TCPProbe: func(ctx context.Context) error {
			conn, dialErr := (&net.Dialer{}).DialContext(ctx, "tcp", t.cuxdAddr)
			if dialErr != nil {
				return fmt.Errorf("tcp probe %s: %w", t.cuxdAddr, dialErr)
			}
			_ = conn.Close()
			return nil
		},
		RPCProbe:       func(ctx context.Context) error { return t.backend.Ping(ctx, t.initID) },
		StabilityProbe: func(ctx context.Context) error { return t.backend.Ping(ctx, t.initID) },
		Reconnect: func(rctx context.Context) error {
			if !WaitForCCUReady(rctx, t.cc, CCUReadinessConfig{}, logger) {
				return errors.New("reconnect: CCU not ready (checkrega.cgi != OK)")
			}
			attempts := 0
			ok, err := t.ic.Reconnect(rctx, t.backend, t.initID, t.callbackURL, nil, &attempts)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("reconnect: CanReconnect returned false")
			}
			return nil
		},
		LoadData: unit.Recovery.RefreshHubDataAfterRecovery(),
	}))
	unit.Recovery.SetLogger(logger)
	unit.Recovery.Subscribe() //nolint:contextcheck // Subscribe starts a background goroutine; it has no ctx parameter by design
}
