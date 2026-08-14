// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

func TestDeviceLifecycleTopicFormat(t *testing.T) {
	t.Parallel()
	got := DeviceLifecycleTopic("0001ABCDE")
	if want := "device.0001ABCDE.lifecycle"; got != want {
		t.Fatalf("DeviceLifecycleTopic = %q, want %q", got, want)
	}
}

func TestDeviceLifecycleSubscriberNilSafe(t *testing.T) {
	t.Parallel()
	s := NewDeviceLifecycleSubscriber(nil, nil)
	s.Start()
	s.Stop()
}

func TestDeviceCreatedPayloadShape(t *testing.T) {
	t.Parallel()
	p := DeviceCreatedPayload{
		Central:       "home",
		InterfaceID:   "HmIP-RF",
		DeviceAddress: "0001ABCDE",
		Model:         "HmIP-eTRV-2",
		Source:        hmenum.SourceOfDeviceCreationCache,
	}
	if p.DeviceAddress != "0001ABCDE" || p.Model != "HmIP-eTRV-2" {
		t.Fatalf("payload round-trip failed: %+v", p)
	}
}

// TestDeviceLifecycleSubscriberPublishesAvailabilityChange drives the
// availability sub-type of [hmevent.DeviceLifecycleEvent] through the real
// subscriber and asserts the frame reaches the hub.
//
// Without this bridge a device that went unreachable stayed `available` on
// every WebSocket consumer until the next full resync: the daemon published
// the transition on the central bus and nothing carried it north.
func TestDeviceLifecycleSubscriberPublishesAvailabilityChange(t *testing.T) {
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

	events.Publish(cu.EventBus, hmevent.DeviceLifecycleEvent{
		Base:        hmevent.NewBase(),
		CentralName: "test-ccu",
		InterfaceID: "HmIP-RF",
		Address:     "DEV001",
		Subtype:     hmenum.DeviceLifecycleSubtypeAvailabilityChanged,
		Available:   false,
	})

	wantTopic := DeviceLifecycleTopic("DEV001")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res := hub.Replay(0, func(topic string) bool { return topic == wantTopic })
		if len(res.Events) == 0 {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		ev := res.Events[0]
		if ev.Type != "device.availability_changed" {
			t.Fatalf("type = %q, want device.availability_changed", ev.Type)
		}
		p, ok := ev.Payload.(DeviceAvailabilityChangedPayload)
		if !ok {
			t.Fatalf("payload type %T, want DeviceAvailabilityChangedPayload", ev.Payload)
		}
		if p.Central != "test-ccu" || p.InterfaceID != "HmIP-RF" || p.DeviceAddress != "DEV001" {
			t.Fatalf("payload = %+v", p)
		}
		if p.Available {
			t.Fatalf("available = true, want false")
		}
		return
	}
	t.Fatal("availability change did not reach the hub within deadline")
}

// TestDeviceLifecycleSubscriberIgnoresNonAvailabilitySubtypes pins that the
// availability bridge stays narrow: the other lifecycle sub-types are carried
// by their own dedicated events, so relaying them here would double every
// creation and deletion frame.
func TestDeviceLifecycleSubscriberIgnoresNonAvailabilitySubtypes(t *testing.T) {
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

	for _, st := range []hmenum.DeviceLifecycleSubtype{
		hmenum.DeviceLifecycleSubtypeCreated,
		hmenum.DeviceLifecycleSubtypeDelayed,
		hmenum.DeviceLifecycleSubtypeUpdated,
		hmenum.DeviceLifecycleSubtypeRemoved,
	} {
		events.Publish(cu.EventBus, hmevent.DeviceLifecycleEvent{
			Base:        hmevent.NewBase(),
			CentralName: "test-ccu",
			InterfaceID: "HmIP-RF",
			Address:     "DEV002",
			Subtype:     st,
		})
	}

	wantTopic := DeviceLifecycleTopic("DEV002")
	res := hub.Replay(0, func(topic string) bool { return topic == wantTopic })
	if len(res.Events) != 0 {
		t.Fatalf("frames on %s = %d, want 0", wantTopic, len(res.Events))
	}
}

func TestDeviceRemovedPayloadShape(t *testing.T) {
	t.Parallel()
	p := DeviceRemovedPayload{
		Central:       "home",
		InterfaceID:   "HmIP-RF",
		DeviceAddress: "0001ABCDE",
	}
	if p.Central != "home" || p.DeviceAddress != "0001ABCDE" {
		t.Fatalf("payload round-trip failed: %+v", p)
	}
}
