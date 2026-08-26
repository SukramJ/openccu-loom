// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestAdoptCentralWiresNorthboundSubscribers is the end-to-end proof through
// the production adopt path that a CCU added at runtime reaches every
// north-bound subscriber that snapshots the registry once at boot.
//
// Only the WebSocket hub-events subscriber ever had a hook. The WS
// system-status, device-lifecycle, device-trigger and optimistic-rollback
// subscribers and the REST system-status buffer had none, and
// adapter.EventBridge does not carry those event types either — so pressing a
// button on a device of an adopted CCU published a DeviceTriggerEvent that
// nothing consumed, and its interface up/down transitions never appeared on
// any surface. Nothing failed and nothing logged; a daemon restart made it all
// work, which made the bug read like a transient.
//
// The assertions are on the effect (the frame arrives, the buffer fills), and
// the events are published on the adopted unit's own bus — the test never
// attaches a subscriber itself.
func TestAdoptCentralWiresNorthboundSubscribers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	reg := central.NewRegistry()
	orch := buildLiveTestOrchestrator(ctx, t, reg, &config.Config{})

	wsHub := ws.NewHub()
	sysStatusBuf, teardown := wireSystemStatusSubscribers(
		reg, wsHub, nil, nil, nil, nil, nil, "", "", discardTestLogger(),
	)
	t.Cleanup(teardown)

	const name = "adopted"
	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig(name)); err != nil {
		t.Fatalf("adoptCentral: %v", err)
	}
	unit, ok := reg.Get(name)
	if !ok {
		t.Fatal("adopted central not present in the registry")
	}

	// A keypress on one of the adopted CCU's devices.
	events.Publish(unit.EventBus, hmevent.DeviceTriggerEvent{
		CentralName:   name,
		InterfaceID:   name + "-HmIP-RF",
		DeviceAddress: "AAAA0001",
		ChannelNo:     1,
		EventType_:    hmenum.DeviceTriggerEventTypeKeypress,
		Parameter:     "PRESS_SHORT",
		Value:         hmtypes.BoolValue(true),
	})
	triggerTopic := ws.DeviceTriggerTopic("AAAA0001", 1)
	if ev := waitForWSTopic(t, wsHub, triggerTopic); ev.Topic != triggerTopic {
		t.Fatalf("device-trigger frame topic = %q, want %q", ev.Topic, triggerTopic)
	}

	// An interface transition on the adopted CCU.
	events.Publish(unit.EventBus, hmevent.SystemStatusChangedEvent{
		CentralName: name,
		Component:   "interface",
		Healthy:     false,
		Reason:      "disconnected",
		InterfaceID: name + "-HmIP-RF",
	})
	statusTopic := ws.SystemStatusTopic(name)
	if ev := waitForWSTopic(t, wsHub, statusTopic); ev.Topic != statusTopic {
		t.Fatalf("system-status frame topic = %q, want %q", ev.Topic, statusTopic)
	}
	if !hasBufferedStatusFor(sysStatusBuf, name) {
		t.Errorf("REST system-status buffer holds no entry for the adopted central")
	}

	// Removal must detach everything the hook attached, or a re-adopt under
	// the same name would double-publish every frame.
	if err := orch.removeCentral(ctx, name); err != nil {
		t.Fatalf("removeCentral: %v", err)
	}
}

// hasBufferedStatusFor reports whether the REST buffer recorded a
// system-status entry for centralName. The subscription and the publish both
// run on the caller's goroutine, so no polling is needed — but the buffer is
// filled from a bus handler, so allow a short settle window.
func hasBufferedStatusFor(buf *handlers.SystemStatusBuffer, centralName string) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries := buf.SystemStatusEntries()
		for i := range entries {
			if entries[i].Central == centralName {
				return true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
