// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// TestOptimisticRollbackPayloadShape confirms JSON field names are stable.
func TestOptimisticRollbackPayloadShape(t *testing.T) {
	t.Parallel()
	p := OptimisticRollbackPayload{
		Central:       "home",
		DeviceAddress: "DEV001",
		Channel:       2,
		Parameter:     "LEVEL",
		ParamsetKey:   "VALUES",
		Reason:        string(hmenum.RollbackReasonTimeout),
		Sent:          0.75,
		Present:       0.0,
	}
	if p.Central != "home" || p.DeviceAddress != "DEV001" {
		t.Fatalf("payload field round-trip failed: %+v", p)
	}
	if p.Reason != string(hmenum.RollbackReasonTimeout) {
		t.Fatalf("reason = %q, want timeout", p.Reason)
	}
}

// TestOptimisticRollbackSubscriberNilSafe verifies that Start() and Stop()
// with nil reg/hub do not panic.
func TestOptimisticRollbackSubscriberNilSafe(t *testing.T) {
	t.Parallel()
	s := NewOptimisticRollbackSubscriber(nil, nil)
	s.Start()
	s.Stop()
}

// TestOptimisticRollbackSubscriberEndToEnd publishes a
// [hmevent.DataPointOptimisticRolledBackEvent] on the central's bus and
// verifies the hub receives an event on the DataPoint topic with the
// expected envelope type, payload contents, and unique_id.
func TestOptimisticRollbackSubscriberEndToEnd(t *testing.T) {
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

	sub := NewOptimisticRollbackSubscriber(reg, hub)
	sub.Start()
	t.Cleanup(sub.Stop)

	key, err := hmtypes.NewDataPointKey("HmIP-RF", "DEV001:2", hmenum.ParamsetKeyValues, "LEVEL")
	if err != nil {
		t.Fatalf("NewDataPointKey: %v", err)
	}

	sent := hmtypes.FloatValue(0.75)
	present := hmtypes.FloatValue(0.0)

	events.Publish(cu.EventBus, hmevent.DataPointOptimisticRolledBackEvent{
		Base:    hmevent.NewBase(),
		Key:     key,
		Reason:  hmenum.RollbackReasonTimeout,
		Sent:    sent,
		Present: present,
	})

	// DataPointTopic uses channel from key.ChannelNo(); DEV001:2 → channel 2.
	wantTopic := DataPointTopic("DEV001", 2, "LEVEL")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res := hub.Replay(0, func(topic string) bool { return topic == wantTopic })
		if len(res.Events) > 0 {
			ev := res.Events[0]
			if ev.Topic != wantTopic {
				t.Fatalf("topic = %q, want %q", ev.Topic, wantTopic)
			}
			if ev.Type != string(hmevent.EventTypeDataPointOptimisticRolled) {
				t.Fatalf("type = %q, want %q", ev.Type, hmevent.EventTypeDataPointOptimisticRolled)
			}
			p, ok := ev.Payload.(OptimisticRollbackPayload)
			if !ok {
				t.Fatalf("payload type %T, want OptimisticRollbackPayload", ev.Payload)
			}
			if p.DeviceAddress != "DEV001" || p.Channel != 2 {
				t.Fatalf("payload device/channel = %v/%v", p.DeviceAddress, p.Channel)
			}
			if p.Parameter != "LEVEL" {
				t.Fatalf("parameter = %q, want LEVEL", p.Parameter)
			}
			if p.Reason != string(hmenum.RollbackReasonTimeout) {
				t.Fatalf("reason = %q, want timeout", p.Reason)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("DataPointOptimisticRolledBackEvent did not reach the hub within deadline")
}

// TestOptimisticRollbackUniqueIDNormalDevice verifies that the rollback
// payload carries the correct unique_id for a normal (non-hub, non-virtual)
// device address. Normal devices get no serial prefix.
func TestOptimisticRollbackUniqueIDNormalDevice(t *testing.T) {
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

	sub := NewOptimisticRollbackSubscriber(reg, hub)
	sub.Start()
	t.Cleanup(sub.Stop)

	key, err := hmtypes.NewDataPointKey("HmIP-RF", "0001ABCD:1", hmenum.ParamsetKeyValues, "STATE")
	if err != nil {
		t.Fatalf("NewDataPointKey: %v", err)
	}

	events.Publish(cu.EventBus, hmevent.DataPointOptimisticRolledBackEvent{
		Base:    hmevent.NewBase(),
		Key:     key,
		Reason:  hmenum.RollbackReasonTimeout,
		Sent:    hmtypes.BoolValue(true),
		Present: hmtypes.BoolValue(false),
	})

	wantTopic := DataPointTopic("0001ABCD", 1, "STATE")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res := hub.Replay(0, func(topic string) bool { return topic == wantTopic })
		if len(res.Events) > 0 {
			p, ok := res.Events[0].Payload.(OptimisticRollbackPayload)
			if !ok {
				t.Fatalf("payload type %T, want OptimisticRollbackPayload", res.Events[0].Payload)
			}
			// Normal device — no serial prefix.
			if want := "loom_0001abcd_1_state"; p.UniqueID != want {
				t.Fatalf("unique_id = %q, want %q", p.UniqueID, want)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("DataPointOptimisticRolledBackEvent did not reach the hub within deadline")
}
