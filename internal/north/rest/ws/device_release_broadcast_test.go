// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// waitForLifecycleFrame returns the first frame of the given type on a
// device's lifecycle topic, or fails.
func waitForLifecycleFrame(t *testing.T, hub *Hub, address, wantType string) Event {
	t.Helper()
	topic := DeviceLifecycleTopic(address)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res := hub.Replay(0, func(tp string) bool { return tp == topic })
		for _, ev := range res.Events {
			if ev.Type == wantType {
				return ev
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %q frame on %s within the deadline", wantType, topic)
	return Event{}
}

// TestReleaseReachesTheLifecyclePlane is the guard for the half of the
// onboarding gate this API had missing.
//
// The release is enforced on MQTT, Matter and the outbound webhook, but
// this surface deliberately still shows an unreleased device — the Config
// UI has to see it. That makes a consumer here responsible for its own
// filter, and a filter is only possible if the state is observable: the
// consumer that most needs the release signal is precisely the one that
// was already connected and dropped the device on its creation frame.
// Without this broadcast it would never learn the device became
// adoptable, and would surface it only after its next full reload.
func TestReleaseReachesTheLifecyclePlane(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	reg := central.NewRegistry()
	cu, err := central.New(central.Config{Name: "test-ccu"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(cu); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	sub := NewDeviceLifecycleSubscriber(reg, hub)
	sub.Start()
	t.Cleanup(sub.Stop)

	events.Publish(cu.EventBus, hmevent.DeviceReleasedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "test-ccu",
		InterfaceID: "HmIP-RF",
		Address:     "DEV900",
	})

	ev := waitForLifecycleFrame(t, hub, "DEV900", "device.released")
	p, ok := ev.Payload.(DeviceReleasedPayload)
	if !ok {
		t.Fatalf("payload type %T, want DeviceReleasedPayload", ev.Payload)
	}
	if p.Central != "test-ccu" || p.InterfaceID != "HmIP-RF" || p.DeviceAddress != "DEV900" {
		t.Fatalf("payload = %+v", p)
	}
}

// TestCreatedFrameCarriesTheReleaseState pins the flag onto the creation
// push itself.
//
// Leaving a consumer to look the state up separately is a race it cannot
// win: this push can arrive before its snapshot read completes, and it
// would adopt a device it would have filtered. The value has to travel
// with the frame that announces the device.
func TestCreatedFrameCarriesTheReleaseState(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	reg := central.NewRegistry()
	cu, err := central.New(central.Config{Name: "test-ccu"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(cu); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	sub := NewDeviceLifecycleSubscriber(reg, hub)
	sub.Start()
	t.Cleanup(sub.Stop)

	// A device that never entered the onboarding wizard. Absence of a hold
	// means released, which is what every device on an existing
	// installation looks like — so an existing consumer needs no filter.
	events.Publish(cu.EventBus, hmevent.DeviceCreatedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "test-ccu",
		InterfaceID: "HmIP-RF",
		Address:     "DEV901",
		Model:       "HmIP-STH",
	})

	ev := waitForLifecycleFrame(t, hub, "DEV901", "device.created")
	p, ok := ev.Payload.(DeviceCreatedPayload)
	if !ok {
		t.Fatalf("payload type %T, want DeviceCreatedPayload", ev.Payload)
	}
	if !p.Released {
		t.Error("released = false for a device that never entered the wizard — every existing consumer would filter its whole fleet")
	}
}
