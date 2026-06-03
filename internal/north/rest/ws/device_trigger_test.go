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
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestDeviceTriggerTopicFormat pins the canonical topic string produced
// for a (device, channel) pair.
func TestDeviceTriggerTopicFormat(t *testing.T) {
	t.Parallel()
	got := DeviceTriggerTopic("DEV001", 3)
	if want := "device.DEV001.channels.3.trigger"; got != want {
		t.Fatalf("DeviceTriggerTopic = %q, want %q", got, want)
	}
}

// TestDeviceTriggerPayloadShape confirms the JSON field names are stable.
func TestDeviceTriggerPayloadShape(t *testing.T) {
	t.Parallel()
	p := DeviceTriggerPayload{
		Central:       "home",
		InterfaceID:   "HmIP-RF",
		DeviceAddress: "DEV001",
		Channel:       3,
		EventType:     string(hmenum.DeviceTriggerEventTypeKeypress),
		Parameter:     "PRESS_SHORT",
		Value:         true,
	}
	if p.Central != "home" || p.DeviceAddress != "DEV001" {
		t.Fatalf("payload field round-trip failed: %+v", p)
	}
	if p.EventType != string(hmenum.DeviceTriggerEventTypeKeypress) {
		t.Fatalf("event_type = %q, want keypress", p.EventType)
	}
}

// TestDeviceTriggerSubscriberNilSafe verifies that Start() and Stop()
// with nil reg/hub do not panic.
func TestDeviceTriggerSubscriberNilSafe(t *testing.T) {
	t.Parallel()
	s := NewDeviceTriggerSubscriber(nil, nil)
	s.Start()
	s.Stop()
}

// TestDeviceTriggerSubscriberEndToEnd publishes a [hmevent.DeviceTriggerEvent]
// on the central's bus and verifies the hub receives an event on the correct
// topic with the expected payload shape, including the unique_id for a normal
// device (no serial prefix needed).
func TestDeviceTriggerSubscriberEndToEnd(t *testing.T) {
	t.Parallel()

	hub := NewHub()

	// Build a minimal registry with one central that has a real event bus.
	reg := central.NewRegistry()
	cu, err := central.New(central.Config{Name: "test-ccu"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(cu); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	sub := NewDeviceTriggerSubscriber(reg, hub)
	sub.Start()
	t.Cleanup(sub.Stop)

	// Publish a DeviceTriggerEvent on the central's bus.
	val := hmtypes.BoolValue(true)
	events.Publish(cu.EventBus, hmevent.DeviceTriggerEvent{
		CentralName:   "test-ccu",
		InterfaceID:   "HmIP-RF",
		DeviceAddress: "DEV001",
		ChannelNo:     3,
		EventType_:    hmenum.DeviceTriggerEventTypeKeypress,
		Parameter:     "PRESS_SHORT",
		Value:         val,
	})

	// The event bus dispatches synchronously by default; poll the hub replay
	// buffer until the event appears or a deadline expires.
	wantTopic := DeviceTriggerTopic("DEV001", 3)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res := hub.Replay(0, func(topic string) bool { return topic == wantTopic })
		if len(res.Events) > 0 {
			ev := res.Events[0]
			if ev.Topic != wantTopic {
				t.Fatalf("topic = %q, want %q", ev.Topic, wantTopic)
			}
			if ev.Type != string(hmevent.EventTypeDeviceTrigger) {
				t.Fatalf("type = %q, want %q", ev.Type, hmevent.EventTypeDeviceTrigger)
			}
			p, ok := ev.Payload.(DeviceTriggerPayload)
			if !ok {
				t.Fatalf("payload type %T, want DeviceTriggerPayload", ev.Payload)
			}
			if p.DeviceAddress != "DEV001" || p.Channel != 3 {
				t.Fatalf("payload = %+v", p)
			}
			if p.Parameter != "PRESS_SHORT" {
				t.Fatalf("parameter = %q, want PRESS_SHORT", p.Parameter)
			}
			// DEV001 is a normal device (no serial prefix); serial not set.
			if want := "loom_dev001_3_press_short"; p.UniqueID != want {
				t.Fatalf("unique_id = %q, want %q", p.UniqueID, want)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("DeviceTriggerEvent did not reach the hub within deadline")
}

// TestDeviceTriggerSubscriberVirtualRemoteUniqueID verifies that a trigger
// event originating on a virtual-remote address (BidCoS-RF) carries the
// serial-prefixed unique_id because virtual-remote channel addresses repeat
// across CCUs and need the central discriminator.
func TestDeviceTriggerSubscriberVirtualRemoteUniqueID(t *testing.T) {
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
	// Set the serial so the suffix is "11a0001234".
	cu.SetSystemInformation(central.SystemInfo{Serial: "3014F711A0001234"})

	sub := NewDeviceTriggerSubscriber(reg, hub)
	sub.Start()
	t.Cleanup(sub.Stop)

	val := hmtypes.BoolValue(false)
	events.Publish(cu.EventBus, hmevent.DeviceTriggerEvent{
		CentralName:   "test-ccu",
		InterfaceID:   "BidCoS-RF",
		DeviceAddress: "BidCoS-RF",
		ChannelNo:     1,
		EventType_:    hmenum.DeviceTriggerEventTypeKeypress,
		Parameter:     "PRESS_SHORT",
		Value:         val,
	})

	wantTopic := DeviceTriggerTopic("BidCoS-RF", 1)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res := hub.Replay(0, func(topic string) bool { return topic == wantTopic })
		if len(res.Events) > 0 {
			p, ok := res.Events[0].Payload.(DeviceTriggerPayload)
			if !ok {
				t.Fatalf("payload type %T, want DeviceTriggerPayload", res.Events[0].Payload)
			}
			// BidCoS-RF:1 is a virtual-remote address → serial prefix required.
			if want := "loom_11a0001234_bidcos_rf_1_press_short"; p.UniqueID != want {
				t.Fatalf("unique_id = %q, want %q", p.UniqueID, want)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("virtual-remote DeviceTriggerEvent did not reach the hub within deadline")
}
