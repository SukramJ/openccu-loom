// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// probeStatusReason marks the events this test fires so they can be told
// apart from the ones the bring-up of an unreachable CCU emits on its own.
const probeStatusReason = "test probe: callback timeout"

// publishSystemStatusOn fires one degraded-interface event on the central's
// own bus, the way the client reliability stack does.
func publishSystemStatusOn(u *central.Unit, iface string) {
	events.Publish(u.EventBus, hmevent.SystemStatusChangedEvent{
		CentralName: u.Name(),
		Component:   "interface",
		Healthy:     false,
		Reason:      probeStatusReason,
		InterfaceID: iface,
	})
}

// statusPublishesFor counts the probe payloads the noop broker saw on topic.
func statusPublishesFor(t *testing.T, client *mqtt.NoopClient, topic string) int {
	t.Helper()
	n := 0
	for _, p := range client.Published() {
		if p.Topic != topic {
			continue
		}
		var body struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(p.Payload, &body); err != nil {
			t.Fatalf("system-status payload on %q is not JSON: %v", topic, err)
		}
		if body.Reason == probeStatusReason {
			n++
		}
	}
	return n
}

// TestAdoptCentralWiresTheSystemStatusPlane is the end-to-end proof through
// the production adopt path: a central adopted at runtime — the same call the
// REST centrals admin API drives — must reach every system-status surface.
//
// The MQTT `<base>/<central>/system/status` topic is the one an operator's
// alerting rule watches for CCU interface degradation. Its publisher, like
// the WebSocket subscriber and the REST ring buffer beside it, walked the
// registry exactly once at boot: for a CCU adopted afterwards the rule stayed
// silent forever while it kept firing for the boot-time CCUs.
//
// The first half pins that pre-hook silence so the assertion cannot go
// vacuous.
func TestAdoptCentralWiresTheSystemStatusPlane(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	reg := central.NewRegistry()
	orch := buildLiveTestOrchestrator(ctx, t, reg, &config.Config{})

	client := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{Base: "openccu-loom", RawEnabled: true}, client)
	wiring := mqtt.NewWiring(bridge, discardTestLogger())
	wsHub := ws.NewHub()

	sysStatusBuf, _, sysStatusHook, _, teardown := wireSystemStatusSubscribers(
		reg, wsHub, wiring, nil, nil, nil, nil, "", "", discardTestLogger(),
	)
	t.Cleanup(teardown)
	if sysStatusHook == nil {
		t.Fatal("wireSystemStatusSubscribers returned a nil system-status hook")
	}

	// No hook installed yet — this is what a runtime-adopted central used to
	// get: registered, live, and unheard on every status surface.
	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("unhooked")); err != nil {
		t.Fatalf("adoptCentral(unhooked): %v", err)
	}
	unhooked, ok := reg.Get("unhooked")
	if !ok {
		t.Fatal("adopted central 'unhooked' not present in the registry")
	}
	publishSystemStatusOn(unhooked, "HmIP-RF")
	unhookedTopic := bridge.Topics().SystemStatus("unhooked")
	if got := statusPublishesFor(t, client, unhookedTopic); got != 0 {
		t.Fatalf("publishes to %q without the hook = %d, want 0", unhookedTopic, got)
	}

	orch.setSysStatusCentralHook(sysStatusHook)

	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("hooked")); err != nil {
		t.Fatalf("adoptCentral(hooked): %v", err)
	}
	hooked, ok := reg.Get("hooked")
	if !ok {
		t.Fatal("adopted central 'hooked' not present in the registry")
	}
	publishSystemStatusOn(hooked, "HmIP-RF")

	hookedTopic := bridge.Topics().SystemStatus("hooked")
	if got := statusPublishesFor(t, client, hookedTopic); got != 1 {
		t.Fatalf("publishes to %q = %d, want 1", hookedTopic, got)
	}
	// The same hook carries the WebSocket broadcast and the REST buffer, so
	// the adopted central is visible on all three surfaces or on none.
	waitForWSTopic(t, wsHub, ws.SystemStatusTopic("hooked"))
	entries := sysStatusBuf.SystemStatusEntries()
	seen := false
	for _, e := range entries {
		if e.Reason != probeStatusReason {
			continue
		}
		if e.Central == "hooked" {
			seen = true
		}
		if e.Central == "unhooked" {
			t.Errorf("REST buffer holds an entry for the central adopted before the hook")
		}
	}
	if !seen {
		t.Errorf("GET /system/status entries for the adopted central = %v, want one", entries)
	}

	// Removal must detach again: a central torn down at runtime keeps its bus
	// alive long enough for an in-flight reliability event to land, and after
	// a re-adopt under the same name it would publish twice.
	if err := orch.removeCentral(ctx, "hooked"); err != nil {
		t.Fatalf("removeCentral(hooked): %v", err)
	}
	before := statusPublishesFor(t, client, hookedTopic)
	publishSystemStatusOn(hooked, "HmIP-RF")
	// The bus subscriptions are dropped by Unit.Stop as well, so give the
	// publish path a moment rather than asserting on an instant.
	time.Sleep(20 * time.Millisecond)
	if after := statusPublishesFor(t, client, hookedTopic); after != before {
		t.Errorf("publishes after removeCentral = %d, want %d (the subscription leaked past removal)", after, before)
	}

	if err := orch.removeCentral(ctx, "unhooked"); err != nil {
		t.Fatalf("removeCentral(unhooked): %v", err)
	}
}
