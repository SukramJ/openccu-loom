// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// deviceEventProbe is one WebSocket device-event family: the event a
// central publishes on its own bus and the topic a client subscribed to
// expects it on.
type deviceEventProbe struct {
	name    string
	address string
	topic   func(address string) string
	publish func(u *central.Unit, address string)
}

// deviceEventProbes covers every family the device-events hook carries.
// A fourth subscriber added later is one row here, not a new test file.
func deviceEventProbes() []deviceEventProbe {
	return []deviceEventProbe{
		{
			name:    "trigger",
			address: "TRIG0000001",
			topic:   func(a string) string { return ws.DeviceTriggerTopic(a, 1) },
			publish: func(u *central.Unit, a string) {
				events.Publish(u.EventBus, hmevent.DeviceTriggerEvent{
					Base:          hmevent.NewBase(),
					CentralName:   u.Name(),
					InterfaceID:   "HmIP-RF",
					DeviceAddress: a,
					ChannelNo:     1,
					EventType_:    hmenum.DeviceTriggerEventTypeKeypress,
					Parameter:     "PRESS_SHORT",
				})
			},
		},
		{
			name:    "lifecycle",
			address: "LIFE0000001",
			topic:   ws.DeviceLifecycleTopic,
			publish: func(u *central.Unit, a string) {
				events.Publish(u.EventBus, hmevent.DeviceCreatedEvent{
					Base:        hmevent.NewBase(),
					CentralName: u.Name(),
					InterfaceID: "HmIP-RF",
					Address:     a,
					Model:       "HmIP-PS",
				})
			},
		},
		{
			name:    "optimistic rollback",
			address: "ROLL0000001",
			topic:   func(a string) string { return ws.DataPointTopic(a, 1, "STATE") },
			publish: func(u *central.Unit, a string) {
				events.Publish(u.EventBus, hmevent.DataPointOptimisticRolledBackEvent{
					Base: hmevent.NewBase(),
					Key: hmtypes.DataPointKey{
						InterfaceID:    "HmIP-RF",
						ChannelAddress: a + ":1",
						ParamsetKey:    hmenum.ParamsetKeyValues,
						Parameter:      "STATE",
					},
					Reason: hmenum.RollbackReasonTimeout,
				})
			},
		},
	}
}

// TestAdoptCentralWiresTheDeviceEventPlanes is the end-to-end proof
// through the production adopt path: a central adopted at runtime — the
// same call the REST centrals admin API drives — must reach every
// WebSocket device-event plane.
//
// Each of those subscribers walked the registry exactly once at boot, so
// for a CCU added afterwards every keypress on one of its remotes, every
// pairing and every optimistic rollback was lost to every WS client
// while the boot-time CCUs kept publishing normally. Only a daemon
// restart repaired it.
//
// The first half pins the pre-hook silence so the assertion cannot go
// vacuous.
func TestAdoptCentralWiresTheDeviceEventPlanes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	reg := central.NewRegistry()
	orch := buildLiveTestOrchestrator(ctx, t, reg, &config.Config{})
	wsHub := ws.NewHub()

	_, centralHook, teardown := wireSystemStatusSubscribers(
		reg, wsHub, nil, nil, nil, nil, nil, "", "", discardTestLogger(),
	)
	t.Cleanup(teardown)
	if centralHook == nil {
		t.Fatal("wireSystemStatusSubscribers returned a nil per-central hook")
	}

	// No hook installed yet — this is what a runtime-adopted central used
	// to get: registered, live, and unheard on every device-event plane.
	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("unhooked")); err != nil {
		t.Fatalf("adoptCentral(unhooked): %v", err)
	}
	unhooked, ok := reg.Get("unhooked")
	if !ok {
		t.Fatal("adopted central 'unhooked' not present in the registry")
	}
	for _, p := range deviceEventProbes() {
		p.publish(unhooked, p.address)
		if got := len(wsEventsOnTopic(wsHub, p.topic(p.address))); got != 0 {
			t.Fatalf("%s broadcasts without the hook = %d, want 0", p.name, got)
		}
	}

	orch.addCentralHook(centralHook)

	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("hooked")); err != nil {
		t.Fatalf("adoptCentral(hooked): %v", err)
	}
	hooked, ok := reg.Get("hooked")
	if !ok {
		t.Fatal("adopted central 'hooked' not present in the registry")
	}

	for _, p := range deviceEventProbes() {
		t.Run(p.name, func(t *testing.T) {
			p.publish(hooked, p.address)
			waitForWSTopic(t, wsHub, p.topic(p.address))
		})
	}

	// Removal must detach again: a central torn down at runtime would
	// otherwise publish twice after a re-adopt under the same name.
	if err := orch.removeCentral(ctx, "hooked"); err != nil {
		t.Fatalf("removeCentral(hooked): %v", err)
	}
	for _, p := range deviceEventProbes() {
		before := len(wsEventsOnTopic(wsHub, p.topic(p.address)))
		p.publish(hooked, p.address)
		if after := len(wsEventsOnTopic(wsHub, p.topic(p.address))); after != before {
			t.Errorf("%s broadcasts after removeCentral = %d, want %d (the subscription leaked past removal)",
				p.name, after, before)
		}
	}

	if err := orch.removeCentral(ctx, "unhooked"); err != nil {
		t.Fatalf("removeCentral(unhooked): %v", err)
	}
}
