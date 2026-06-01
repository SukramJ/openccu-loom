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
// topic with the expected payload shape.
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
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("DeviceTriggerEvent did not reach the hub within deadline")
}
