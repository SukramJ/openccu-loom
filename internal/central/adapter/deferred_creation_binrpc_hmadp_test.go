// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"net"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHmAdpToggleReachesTheBINRPCCallbackHandler pins that the runtime
// delay-new-device-creation decision reaches every live callback handler of a
// central, not only the XML-RPC one.
//
// A central with a CUxD interface runs two CallbackHandlers instances: one
// registered on the XML-RPC callback server by central name, one registered on
// the BIN-RPC server by interface id. The park-versus-ingest branch in
// CallbackHandlers.NewDevices reads per-instance state, so a toggle applied to
// one instance only makes the same operator decision answer differently
// depending on which transport announced the device — OFF→ON keeps ingesting
// CUxD announcements the operator just asked to hold, ON→OFF keeps parking
// them.
func TestHmAdpToggleReachesTheBINRPCCallbackHandler(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-binrpc-toggle"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	xmlrpcHandlers := NewCallbackHandlers(c, nil)
	t.Cleanup(xmlrpcHandlers.Stop)
	binrpcHandlers := NewCallbackHandlers(c, nil)
	t.Cleanup(binrpcHandlers.Stop)

	h := &centralBringUp{
		cc:         config.CentralConfig{Name: "ccu-binrpc-toggle"},
		unit:       c,
		cbHandlers: xmlrpcHandlers,
		logger:     slog.New(slog.DiscardHandler),
	}
	// Stands in for the wiring's own hand-over, which
	// TestHmAdpCUxDWiringAdoptsItsCallbackHandler pins separately — this
	// test is about what ApplyDeferredCreationBehavior does with an adopted
	// handler, not about who adopted it.
	h.adoptBINRPCHandlers(binrpcHandlers)

	for _, want := range []bool{true, false, true} {
		xmlrpcHandlers.SetDelayNewDeviceCreation(!want)
		binrpcHandlers.SetDelayNewDeviceCreation(!want)

		m := newBringUpManager()
		m.byCentral = map[string]*centralBringUp{"ccu-binrpc-toggle": h}
		cfg := &config.Config{Centrals: []config.CentralConfig{{
			Name:     "ccu-binrpc-toggle",
			Behavior: config.CentralBehavior{DelayNewDeviceCreation: boolPtr(want)},
		}}}
		if n := m.ApplyDeferredCreationBehavior(context.Background(), cfg); n != 1 {
			t.Fatalf("want=%v: applied to %d central(s), want 1", want, n)
		}
		if got := xmlrpcHandlers.DelayNewDeviceCreation(); got != want {
			t.Fatalf("want=%v: XML-RPC handler holds %v", want, got)
		}
		if got := binrpcHandlers.DelayNewDeviceCreation(); got != want {
			t.Fatalf("want=%v: BIN-RPC handler holds %v — the toggle never reached the CUxD callback handler",
				want, got)
		}
	}
}

// TestHmAdpBringUpGenerationDropsStaleBINRPCHandlers pins the reset half: a
// re-init builds fresh handlers, and a handler from a torn-down generation
// must not keep receiving toggle writes it can no longer act on.
func TestHmAdpBringUpGenerationDropsStaleBINRPCHandlers(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-binrpc-reset"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	stale := NewCallbackHandlers(c, nil)
	t.Cleanup(stale.Stop)

	dead, cancel := context.WithCancel(context.Background())
	cancel()
	h := &centralBringUp{
		cc:        config.CentralConfig{Name: "ccu-binrpc-reset", Host: "127.0.0.1"},
		unit:      c,
		parentCtx: dead,
		logger:    slog.New(slog.DiscardHandler),
	}
	h.adoptBINRPCHandlers(stale)
	if got := len(h.callbackHandlers()); got != 1 {
		t.Fatalf("adopted handlers = %d, want 1", got)
	}

	// Drive the production reset: start() clears the per-generation set
	// before launching the bring-up goroutine. The parent context is already
	// cancelled, so gatedCentralBringUp falls out of its readiness gate on
	// the first probe and the generation drains without any CCU.
	h.start()
	h.wg.Wait()

	if got := len(h.callbackHandlers()); got != 0 {
		t.Fatalf("handlers after a new generation = %d, want 0 — start() left a torn-down generation's BIN-RPC handler adopted", got)
	}
}

// hmAdpClosedPort binds a loopback port, records it and releases it again, so
// a dial against it is refused immediately instead of hanging on a route.
func hmAdpClosedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return port
}

// TestHmAdpCUxDWiringAdoptsItsCallbackHandler guards the seam rather than the
// helper: [centralBringUp.adoptBINRPCHandlers] works when called, but nothing
// asserted that [wireCUxDInterface] — the only place a BIN-RPC handler is ever
// created — actually calls it. Dropping that one call re-opens the whole
// finding, with every unit test still green.
//
// The CUxD host is a loopback port nothing listens on, so the outbound client
// builds, the callback registration happens, and the ingest fails fast. The
// adoption is on the registration path, ahead of the ingest, so the failure
// does not mask it.
func TestHmAdpCUxDWiringAdoptsItsCallbackHandler(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-cuxd-adopt"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	logger := slog.New(slog.DiscardHandler)
	cbServer, err := rpcserver.NewBINRPCServer(rpcserver.BINRPCConfig{Addr: "127.0.0.1:0", Logger: logger})
	if err != nil {
		t.Fatalf("NewBINRPCServer: %v", err)
	}
	t.Cleanup(func() { _ = cbServer.Close() })

	cc := config.CentralConfig{
		Name:  "ccu-cuxd-adopt",
		Host:  "127.0.0.1",
		Ports: map[string]int{string(hmenum.InterfaceCUxD): hmAdpClosedPort(t)},
	}
	h := &centralBringUp{cc: cc, unit: c, logger: logger}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the ingest must not sit through the boot-time backoff schedule

	closer, _, err := wireCUxDInterface(
		ctx, cc, c,
		NewDevicePipeline(c),
		client.NewValueWriter(),
		nil, // runner: the ReGa surface is not on the adoption path
		config.ReliabilityConfig{},
		nil, // masterValues: the CUxD poller tolerates a nil store
		newBackendRegistry(),
		cbServer, "127.0.0.1:8129",
		h.adoptBINRPCHandlers,
		logger,
	)
	if err != nil {
		t.Fatalf("wireCUxDInterface: %v", err)
	}
	if closer != nil {
		t.Cleanup(closer)
	}

	if got := len(h.callbackHandlers()); got != 1 {
		t.Fatalf("bring-up handle holds %d callback handler(s) after wireCUxDInterface, want 1 — "+
			"the CUxD wiring never handed its BIN-RPC handler over, so the runtime "+
			"delay-new-device-creation toggle cannot reach it", got)
	}
}
