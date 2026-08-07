// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// buildBridgeTestOrchestrator wires an orchestrator whose southbound deps
// carry a started EventBridge over the shared registry, the way the
// composition root does.
func buildBridgeTestOrchestrator(
	ctx context.Context, t *testing.T, reg *central.Registry, cfg *config.Config,
) (*centralOrchestrator, *ws.Hub) {
	t.Helper()
	logger := discardTestLogger()
	mgr, err := adapter.WireCentrals(ctx, cfg, reg, adapter.WireDeps{}, logger)
	if err != nil {
		t.Fatalf("adapter.WireCentrals: %v", err)
	}
	t.Cleanup(mgr.Teardown)

	hub := ws.NewHub()
	bridge := adapter.NewEventBridge(reg, hub, nil)
	// Start BEFORE the adopt, exactly like the daemon: the bridge comes up
	// during boot and only then can a central be adopted at runtime.
	bridge.Start(ctx)
	t.Cleanup(bridge.Stop)

	deps := southboundWiringDeps{reg: reg, logger: logger, bridge: bridge}
	orch := newCentralOrchestrator(reg, mgr, deps, cfg, logger, "", nil, nil, nil, nil)
	if orch == nil {
		t.Fatal("newCentralOrchestrator returned nil")
	}
	return orch, hub
}

// publishValueChange emits one value change on the named central's bus.
func publishValueChange(t *testing.T, reg *central.Registry, centralName, parameter string) {
	t.Helper()
	unit, ok := reg.Get(centralName)
	if !ok {
		t.Fatalf("central %q not registered", centralName)
	}
	events.Publish(unit.EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      parameter,
		},
		NewValue: hmtypes.BoolValue(true),
	})
}

// wsPushCount counts WS value-changed pushes carrying parameter.
func wsPushCount(hub *ws.Hub, parameter string) int {
	n := 0
	for _, e := range hub.Replay(0, nil).Events {
		if p, ok := e.Payload.(ws.DataPointValueChangedPayload); ok && p.Parameter == parameter {
			n++
		}
	}
	return n
}

// awaitWSPushCount waits for the hub to hold want pushes for parameter,
// then keeps watching briefly to catch a duplicate.
//
// Publish is not always synchronous: the bus dispatches inline only when
// it wins the dispatch lock, and a central's own bring-up publishes on
// its bus concurrently. A losing Publish is queued and drained by
// whichever goroutine holds the lock, so asserting the count on the next
// line races the drain — which is what made the first version of this
// test flake under -race.
func awaitWSPushCount(t *testing.T, hub *ws.Hub, parameter string, want int) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if wsPushCount(hub, parameter) >= want {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// A late duplicate must fail the test, not slip through the deadline.
	time.Sleep(100 * time.Millisecond)
	return wsPushCount(hub, parameter)
}

// TestAdoptedCentralReachesTheEventBridge pins that a central adopted at
// runtime is wired into the north-bound event bridge, and unwired again when
// it is removed.
//
// EventBridge.Start snapshots the registry once and subscribes per unit. A
// central that joins afterwards — the whole point of live adopt — was
// therefore attached to nothing: every value change, device creation and
// readiness transition it reported reached neither the MQTT fan-out nor the
// WebSocket live plane, and the CCU stayed invisible on every north-bound
// surface until the daemon restarted. Nothing failed; the adopt reported
// success and the central was in the registry.
//
// The test drives the real orchestrator (the composition seam REST calls)
// and asserts the effect on the hub, not that a wiring method was called.
func TestAdoptedCentralReachesTheEventBridge(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cfg := &config.Config{}
	reg := central.NewRegistry()
	orch, hub := buildBridgeTestOrchestrator(ctx, t, reg, cfg)

	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("adopted-live")); err != nil {
		t.Fatalf("adoptCentral: %v", err)
	}

	publishValueChange(t, reg, "adopted-live", "STATE")
	if got := awaitWSPushCount(t, hub, "STATE", 1); got != 1 {
		t.Fatalf("WS pushes from the adopted central = %d, want 1 — the central's bus reaches no north-bound surface", got)
	}

	// Removing the central must release the subscription again, or a removed
	// CCU keeps publishing onto the live plane.
	if err := orch.removeCentral(ctx, "adopted-live"); err != nil {
		t.Fatalf("removeCentral: %v", err)
	}
	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("adopted-live")); err != nil {
		t.Fatalf("re-adoptCentral: %v", err)
	}
	publishValueChange(t, reg, "adopted-live", "LEVEL")
	if got := awaitWSPushCount(t, hub, "LEVEL", 1); got != 1 {
		t.Fatalf("WS pushes after remove+re-adopt = %d, want exactly 1 (0 = detached too much, 2 = subscription leaked)", got)
	}
}
