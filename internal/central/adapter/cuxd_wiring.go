// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/observer"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/session"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// wireCUxDInterface wires the BIN-RPC outbound client, CuxdBackend,
// callback registration, and pipeline ingest for a single CUxD interface.
//
// CUxD speaks BIN-RPC natively — the XML-RPC wiring path used for
// HmIP-RF / BidCos / VirtualDevices is not applicable here. Each
// CUxD interface gets its own outbound [binrpc.Client] plus a
// registration on the shared [rpcserver.BINRPCServer] callback listener.
func wireCUxDInterface( //nolint:funlen // composition/wiring: long sequential setup
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
		Caller:              binSliceCaller,
		Enabled:             true,
		Logger:              logger.With(slog.String("interface", wireID)),
		SessionRecorderHook: sessionHook,
	}
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
	bcaller := client.NewBackendCaller(ic, 0)

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

	writer.Register(cc.Name, wireID, backend)
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
	if binrpcCallbackServer != nil && binrpcCallbackAddr != "" {
		handlers := NewCallbackHandlers(unit, logger)
		if writer != nil {
			handlers.SetWriter(writer)
		}
		handlers.SetDelayNewDeviceCreation(cc.Behavior.DelayNewDeviceCreationEnabled())
		binrpcCallbackServer.Register(initID, handlers)
		callbackURL = "binary://" + binrpcCallbackAddr
		closerSrv := binrpcCallbackServer
		capturedInitID := initID
		_ = closerSrv // avoid unused-var lint for deregister call in closer
		_ = capturedInitID
	}

	poller := newMasterPollerForInterface(iface, unit, backend, masterValues, wireID, cc.Name, logger) //nolint:contextcheck // poller callback uses context.Background(); outlives the wiring ctx by design
	if poller != nil {
		pipeline.WithMasterRefreshHook(poller.SchedulePoll)
	} else {
		pipeline.WithMasterRefreshHook(nil)
	}

	if err := pipeline.IngestFromBackend(ctx, wireID, iface, backend, writer, runner, logger); err != nil {
		if poller != nil {
			poller.Close()
		}
		ensureDisconnectedClientState(ic, logger)
		return nil, fmt.Errorf("ingest: %w", err)
	}
	logger.Info("wire.ingest.ok",
		slog.String("central", cc.Name),
		slog.String("interface", wireID))

	for _, d := range unit.ModelRegistry.List() {
		if d.InterfaceID != wireID {
			continue
		}
		d.SetValueLoader(backend)
	}

	seedRelevantInitParameters(ctx, unit, iface, logger)
	seedReadableEvents(ctx, unit, iface, logger)

	if callbackURL != "" {
		if err := backend.Deinit(ctx, initID); err != nil {
			logger.Debug("wire.deinit.pre_init",
				slog.String("central", cc.Name),
				slog.String("interface", initID),
				slog.String("err", err.Error()))
		}
		if err := backend.Init(ctx, initID, callbackURL); err != nil {
			logger.Warn("wire.init.failed",
				slog.String("central", cc.Name),
				slog.String("interface", initID),
				slog.String("err", err.Error()))
		} else {
			logger.Info("wire.init.ok",
				slog.String("central", cc.Name),
				slog.String("interface", initID))
			// Drive the client state machine to CONNECTED — the XML-RPC path
			// does this too. Without it CUxD sits at its initial state, so the
			// central check_connection job sees ClientState != CONNECTED and
			// keeps flagging the interface as a (false) connection loss.
			ensureConnectedClientState(ic, logger)
		}
	}

	closer := func() {
		if binrpcCallbackServer != nil {
			if err := backend.Deinit(ctx, initID); err != nil {
				logger.Debug("wire.deinit.shutdown",
					slog.String("interface", initID), slog.String("err", err.Error()))
			}
			binrpcCallbackServer.Deregister(initID)
		}
		if poller != nil {
			poller.Close()
		}
	}
	return closer, nil
}
