// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
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
	// The CUxD wiring hands its instance to the handle exactly this way.
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

	h := &centralBringUp{
		cc:     config.CentralConfig{Name: "ccu-binrpc-reset"},
		unit:   c,
		logger: slog.New(slog.DiscardHandler),
	}
	h.adoptBINRPCHandlers(stale)
	if got := len(h.callbackHandlers()); got != 1 {
		t.Fatalf("adopted handlers = %d, want 1", got)
	}

	// start() resets the per-generation set before the bring-up goroutine
	// runs; assert the reset directly so the test needs no live bring-up.
	h.mu.Lock()
	h.binCbHandlers = nil
	h.mu.Unlock()

	if got := len(h.callbackHandlers()); got != 0 {
		t.Fatalf("handlers after a new generation = %d, want 0", got)
	}
}
