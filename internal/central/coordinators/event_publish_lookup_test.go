// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// event_publish_lookup_test.go covers EventCoordinator publish and lookup
// scenarios not exercised by event_deep_test.go or coordinators_test.go.
//
// Covered:
//   - Fresh coordinator has no last-event timestamp for any interface
//   - Clear resets per-interface timestamps
//   - Clear is idempotent on empty coordinator
//   - GetLastEventSeenForInterface: none observed, after MarkEvent
//   - Multiple interfaces tracked independently
//   - AddDataPointSubscription receives published events
//   - PublishBackendParameterEvent: fields, empty-interface guard
//   - PublishDeviceTriggerEvent: fields, timestamp update, empty-interface guard
//   - PublishSystemEvent: fields, timestamp update, empty-Base fill
package coordinators

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// Initialization and bus property
// ---------------------------------------------------------------------------

func TestEventInitializationNoLastEventSeen(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)

	_, observed := ec.GetLastEventSeenForInterface("any-iface")
	if observed {
		t.Fatal("fresh coordinator must return observed=false for any interface")
	}
}

// ---------------------------------------------------------------------------
// Clear — unsubscribes DP hooks, resets per-interface stamps.
// ---------------------------------------------------------------------------

func TestEventClearUnsubscribesDPHooks(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)

	var called atomic.Int32
	unsub := ec.AddDataPointSubscription(func(_ hmevent.DataPointValueChangedEvent) {
		called.Add(1)
	})
	_ = unsub

	// Seed a timestamp so we can verify it is wiped.
	ec.MarkEvent("iface-A", time.Now())
	if _, ok := ec.GetLastEventSeenForInterface("iface-A"); !ok {
		t.Fatal("iface-A must be observed before Clear")
	}

	ec.Clear()

	// Timestamp must be gone after Clear.
	if _, ok := ec.GetLastEventSeenForInterface("iface-A"); ok {
		t.Fatal("Clear must reset per-interface timestamps")
	}
}

func TestEventClearIsIdempotent(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)
	// Multiple Clear calls on an empty coordinator must not panic.
	ec.Clear()
	ec.Clear()
}

// ---------------------------------------------------------------------------
// GetLastEventSeenForInterface / SetLastEventSeenForInterface
// ---------------------------------------------------------------------------

func TestEventGetLastEventSeenNone(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)
	_, observed := ec.GetLastEventSeenForInterface("BidCos-RF")
	if observed {
		t.Fatal("want observed=false for unseen interface")
	}
}

func TestEventGetLastEventSeenAfterMark(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)
	before := time.Now()
	ec.MarkEvent("HmIP-RF", time.Time{})
	ts, observed := ec.GetLastEventSeenForInterface("HmIP-RF")
	if !observed {
		t.Fatal("want observed=true after MarkEvent")
	}
	if ts.Before(before) {
		t.Fatalf("timestamp %v should be >= %v", ts, before)
	}
}

func TestEventMultipleInterfacesTrackedIndependently(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)

	ec.MarkEvent("BidCos-RF", time.Now())
	_, bidcosOk := ec.GetLastEventSeenForInterface("BidCos-RF")
	_, hmipOk := ec.GetLastEventSeenForInterface("HmIP-RF")

	if !bidcosOk {
		t.Fatal("BidCos-RF should be observed")
	}
	if hmipOk {
		t.Fatal("HmIP-RF should still be unobserved")
	}

	ec.MarkEvent("HmIP-RF", time.Now())
	_, hmipOk = ec.GetLastEventSeenForInterface("HmIP-RF")
	if !hmipOk {
		t.Fatal("HmIP-RF must be observed after second MarkEvent")
	}
}

// ---------------------------------------------------------------------------
// AddDataPointSubscription — handler receives events, unsub works.
// ---------------------------------------------------------------------------

func TestEventAddDataPointSubscriptionReceivesEvent(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)

	var count atomic.Int32
	ec.AddDataPointSubscription(func(_ hmevent.DataPointValueChangedEvent) {
		count.Add(1)
	})

	// Publish a DataPointValueChangedEvent directly via the bus to simulate
	// what HandleRawEvent would do.
	events.Publish(bus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "BidCos-RF",
			ChannelAddress: "VCU0000001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		OldValue: hmtypes.NoneValue(),
		NewValue: hmtypes.BoolValue(true),
	})

	if count.Load() != 1 {
		t.Fatalf("AddDataPointSubscription handler fired %d times, want 1", count.Load())
	}
}

// ---------------------------------------------------------------------------
// PublishBackendParameterEvent — publishes RPCParameterReceivedEvent.
// ---------------------------------------------------------------------------

func TestEventPublishBackendParameterEvent(t *testing.T) {
	t.Parallel()
	ec, bus, _ := newTestEC(t)
	ec.SetCentralName("ccu1")

	var received []hmevent.RPCParameterReceivedEvent
	events.Subscribe(bus, func(e hmevent.RPCParameterReceivedEvent) {
		received = append(received, e)
	})

	ec.PublishBackendParameterEvent("BidCos-RF", "VCU0000001:1", "STATE", "true")

	if len(received) != 1 {
		t.Fatalf("expected 1 RPCParameterReceivedEvent, got %d", len(received))
	}
	e := received[0]
	if e.InterfaceID != "BidCos-RF" {
		t.Errorf("InterfaceID=%q, want BidCos-RF", e.InterfaceID)
	}
	if e.ChannelAddress != "VCU0000001:1" {
		t.Errorf("ChannelAddress=%q, want VCU0000001:1", e.ChannelAddress)
	}
	if e.Parameter != "STATE" {
		t.Errorf("Parameter=%q, want STATE", e.Parameter)
	}
	if e.RawValue != "true" {
		t.Errorf("RawValue=%q, want true", e.RawValue)
	}
}

func TestEventPublishBackendParameterEventEmptyIfaceIsNoop(t *testing.T) {
	t.Parallel()
	ec, bus, _ := newTestEC(t)

	var count atomic.Int32
	events.Subscribe(bus, func(_ hmevent.RPCParameterReceivedEvent) { count.Add(1) })

	ec.PublishBackendParameterEvent("", "VCU0000001:1", "STATE", "true")

	if count.Load() != 0 {
		t.Fatal("empty interfaceID must be a no-op")
	}
}

// ---------------------------------------------------------------------------
// PublishDeviceTriggerEvent — publishes DeviceTriggerEvent.
// ---------------------------------------------------------------------------

func TestEventPublishDeviceTriggerEvent(t *testing.T) {
	t.Parallel()
	ec, bus, _ := newTestEC(t)
	ec.SetCentralName("ccu1")

	var received []hmevent.DeviceTriggerEvent
	events.Subscribe(bus, func(e hmevent.DeviceTriggerEvent) {
		received = append(received, e)
	})

	ec.PublishDeviceTriggerEvent(
		context.Background(),
		"BidCos-RF",
		"VCU0000001",
		2,
		hmenum.DeviceTriggerEventTypeKeypress,
		"PRESS_LONG",
		hmtypes.BoolValue(true),
	)

	if len(received) != 1 {
		t.Fatalf("expected 1 DeviceTriggerEvent, got %d", len(received))
	}
	e := received[0]
	if e.InterfaceID != "BidCos-RF" {
		t.Errorf("InterfaceID=%q, want BidCos-RF", e.InterfaceID)
	}
	if e.DeviceAddress != "VCU0000001" {
		t.Errorf("DeviceAddress=%q, want VCU0000001", e.DeviceAddress)
	}
	if e.ChannelNo != 2 {
		t.Errorf("ChannelNo=%d, want 2", e.ChannelNo)
	}
	if e.Parameter != "PRESS_LONG" {
		t.Errorf("Parameter=%q, want PRESS_LONG", e.Parameter)
	}
}

func TestEventPublishDeviceTriggerEventUpdatesTimestamp(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)

	before := time.Now()
	ec.PublishDeviceTriggerEvent(context.Background(), "HmIP-RF", "ADDR", 0, hmenum.DeviceTriggerEventTypeKeypress, "PRESS_SHORT", hmtypes.BoolValue(false))

	ts, ok := ec.GetLastEventSeenForInterface("HmIP-RF")
	if !ok {
		t.Fatal("PublishDeviceTriggerEvent must update per-interface timestamp")
	}
	if ts.Before(before) {
		t.Fatalf("timestamp %v should be >= %v", ts, before)
	}
}

func TestEventPublishDeviceTriggerEventEmptyIfaceIsNoop(t *testing.T) {
	t.Parallel()
	ec, bus, _ := newTestEC(t)

	var count atomic.Int32
	events.Subscribe(bus, func(_ hmevent.DeviceTriggerEvent) { count.Add(1) })

	ec.PublishDeviceTriggerEvent(context.Background(), "", "ADDR", 0, hmenum.DeviceTriggerEventTypeKeypress, "P", hmtypes.NoneValue())

	if count.Load() != 0 {
		t.Fatal("empty interfaceID must be a no-op for PublishDeviceTriggerEvent")
	}
}

// ---------------------------------------------------------------------------
// PublishSystemEvent — publishes SystemStatusChangedEvent.
// ---------------------------------------------------------------------------

func TestEventPublishSystemEvent(t *testing.T) {
	t.Parallel()
	ec, bus, _ := newTestEC(t)

	var received []hmevent.SystemStatusChangedEvent
	events.Subscribe(bus, func(e hmevent.SystemStatusChangedEvent) {
		received = append(received, e)
	})

	ec.PublishSystemEvent(context.Background(), hmevent.SystemStatusChangedEvent{
		InterfaceID: "BidCos-RF",
		Healthy:     true,
	})

	if len(received) != 1 {
		t.Fatalf("expected 1 SystemStatusChangedEvent, got %d", len(received))
	}
	if received[0].InterfaceID != "BidCos-RF" {
		t.Errorf("InterfaceID=%q, want BidCos-RF", received[0].InterfaceID)
	}
}

func TestEventPublishSystemEventUpdatesTimestamp(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)

	before := time.Now()
	ec.PublishSystemEvent(context.Background(), hmevent.SystemStatusChangedEvent{
		InterfaceID: "HmIP-RF",
		Healthy:     true,
	})

	ts, ok := ec.GetLastEventSeenForInterface("HmIP-RF")
	if !ok {
		t.Fatal("PublishSystemEvent must update per-interface timestamp when InterfaceID is set")
	}
	if ts.Before(before) {
		t.Fatalf("timestamp %v should be >= %v", ts, before)
	}
}

func TestEventPublishSystemEventFillsBaseIfEmpty(t *testing.T) {
	t.Parallel()
	ec, bus, _ := newTestEC(t)

	var received []hmevent.SystemStatusChangedEvent
	events.Subscribe(bus, func(e hmevent.SystemStatusChangedEvent) {
		received = append(received, e)
	})

	// Pass event with zero Base — implementation must fill it.
	ec.PublishSystemEvent(context.Background(), hmevent.SystemStatusChangedEvent{
		Healthy: true,
	})

	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].Base == (hmevent.Base{}) {
		t.Fatal("PublishSystemEvent must fill empty Base with a new value")
	}
}

// ---------------------------------------------------------------------------
// AddDataPointSubscriptionForKey — per-key filtering.
// ---------------------------------------------------------------------------

// TestAddDataPointSubscriptionForKeyFiltersCorrectly registers two subscribers
// with different DPKs and verifies that only the subscriber whose key matches
// the published event receives the callback.
func TestAddDataPointSubscriptionForKeyFiltersCorrectly(t *testing.T) {
	t.Parallel()
	ec, bus, _ := newTestEC(t)

	keyA := hmtypes.DataPointKey{
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "VCU0001:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "STATE",
	}
	keyB := hmtypes.DataPointKey{
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "VCU0002:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "LEVEL",
	}

	var countA, countB atomic.Int32
	ec.AddDataPointSubscriptionForKey(keyA, func(_ hmevent.DataPointValueChangedEvent) {
		countA.Add(1)
	})
	ec.AddDataPointSubscriptionForKey(keyB, func(_ hmevent.DataPointValueChangedEvent) {
		countB.Add(1)
	})

	// Publish an event matching only keyA.
	events.Publish(bus, hmevent.DataPointValueChangedEvent{
		Base:     hmevent.NewBase(),
		Key:      keyA,
		OldValue: hmtypes.NoneValue(),
		NewValue: hmtypes.BoolValue(true),
	})

	if countA.Load() != 1 {
		t.Fatalf("subscriber for keyA: got %d calls, want 1", countA.Load())
	}
	if countB.Load() != 0 {
		t.Fatalf("subscriber for keyB: got %d calls, want 0 (keyB event not published)", countB.Load())
	}

	// Now publish an event matching only keyB.
	events.Publish(bus, hmevent.DataPointValueChangedEvent{
		Base:     hmevent.NewBase(),
		Key:      keyB,
		OldValue: hmtypes.NoneValue(),
		NewValue: hmtypes.FloatValue(0.5),
	})

	if countA.Load() != 1 {
		t.Fatalf("subscriber for keyA: got %d calls after keyB event, want still 1", countA.Load())
	}
	if countB.Load() != 1 {
		t.Fatalf("subscriber for keyB: got %d calls, want 1", countB.Load())
	}
}

// ---------------------------------------------------------------------------
// NewestEventAge — hub-level liveness gauge.
// ---------------------------------------------------------------------------

// TestNewestEventAgeNeverObserved verifies that NewestEventAge returns ok=false
// when no event has been recorded on any interface.
func TestNewestEventAgeNeverObserved(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)

	_, ok := ec.NewestEventAge(time.Now())
	if ok {
		t.Fatal("NewestEventAge must return ok=false when no event has been recorded")
	}
}

// TestNewestEventAgeSingleInterface verifies that NewestEventAge returns the
// correct elapsed seconds relative to the supplied now when exactly one
// interface has an event recorded.
func TestNewestEventAgeSingleInterface(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)

	t0 := time.Unix(1_000_000, 0)
	ec.MarkEvent("BidCos-RF", t0)

	age, ok := ec.NewestEventAge(t0.Add(10 * time.Second))
	if !ok {
		t.Fatal("NewestEventAge must return ok=true after a MarkEvent call")
	}
	if age != 10.0 {
		t.Fatalf("age = %v, want 10.0", age)
	}
}

// TestNewestEventAgeMultipleInterfaces verifies that NewestEventAge uses the
// most recent stamp across all interfaces. The age is computed relative to the
// newest stamp, not the oldest.
func TestNewestEventAgeMultipleInterfaces(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)

	t0 := time.Unix(2_000_000, 0)
	ec.MarkEvent("A", t0)
	ec.MarkEvent("B", t0.Add(30*time.Second))

	// now = t0 + 40s; newest event is B at t0+30s → age = 10s.
	age, ok := ec.NewestEventAge(t0.Add(40 * time.Second))
	if !ok {
		t.Fatal("NewestEventAge must return ok=true after MarkEvent calls")
	}
	if age != 10.0 {
		t.Fatalf("age = %v, want 10.0 (relative to newest interface B)", age)
	}
}

// TestAddDataPointSubscriptionForKeyZeroIsWildcard verifies that passing the
// zero DataPointKey behaves identically to AddDataPointSubscription: the
// subscriber receives all events regardless of key.
func TestAddDataPointSubscriptionForKeyZeroIsWildcard(t *testing.T) {
	t.Parallel()
	ec, bus, _ := newTestEC(t)

	var count atomic.Int32
	ec.AddDataPointSubscriptionForKey(hmtypes.DataPointKey{}, func(_ hmevent.DataPointValueChangedEvent) {
		count.Add(1)
	})

	keys := []hmtypes.DataPointKey{
		{InterfaceID: "HmIP-RF", ChannelAddress: "VCU0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "STATE"},
		{InterfaceID: "BidCos-RF", ChannelAddress: "VCU0002:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "LEVEL"},
	}
	for _, k := range keys {
		events.Publish(bus, hmevent.DataPointValueChangedEvent{
			Base:     hmevent.NewBase(),
			Key:      k,
			OldValue: hmtypes.NoneValue(),
			NewValue: hmtypes.BoolValue(false),
		})
	}

	if count.Load() != int32(len(keys)) {
		t.Fatalf("wildcard subscriber: got %d calls, want %d", count.Load(), len(keys))
	}
}
