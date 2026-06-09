// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- helpers -----------------------------------------------------------------

func newTestEC(t *testing.T) (*EventCoordinator, *events.Bus, *CacheCoordinator) {
	t.Helper()
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)
	return ec, bus, cache
}

// ---------------------------------------------------------------------------
// 1. NewEventCoordinator with nil logger must not panic.
// ---------------------------------------------------------------------------

func TestNewEventCoordinatorNilLogger(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)
	if ec == nil {
		t.Fatal("NewEventCoordinator returned nil")
	}
}

// ---------------------------------------------------------------------------
// 2. LastEventMonotonicForInterface returns (zero, false) for unknown iface.
// (Complementary to the existing test — isolated, parallel-safe.)
// ---------------------------------------------------------------------------

func TestLastEventMonotonicForInterfaceNeverSeen(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)
	_, observed := ec.LastEventMonotonicForInterface("unknown-iface")
	if observed {
		t.Fatal("expected observed=false for an interface with no recorded events")
	}
}

// ---------------------------------------------------------------------------
// 3. MarkEvent with an explicit non-zero time stores that exact stamp.
// ---------------------------------------------------------------------------

func TestMarkEventStoresExplicitTimestamp(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)
	want := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	ec.MarkEvent("iface-x", want)
	got, observed := ec.LastEventMonotonicForInterface("iface-x")
	if !observed {
		t.Fatal("MarkEvent should make the interface observed")
	}
	if !got.Equal(want) {
		t.Fatalf("stamp mismatch: got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// 4. MarkEvent with zero time uses time.Now() — stamp must be >= before.
// ---------------------------------------------------------------------------

func TestMarkEventZeroTimeUsesNow(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)
	before := time.Now()
	ec.MarkEvent("iface-y", time.Time{})
	got, observed := ec.LastEventMonotonicForInterface("iface-y")
	if !observed {
		t.Fatal("expected observed=true after MarkEvent with zero time")
	}
	if got.Before(before) {
		t.Fatalf("stamp %v should be >= %v (time.Now at call)", got, before)
	}
}

// ---------------------------------------------------------------------------
// 5. MarkEvent with empty interfaceID is a no-op — no entry added.
// ---------------------------------------------------------------------------

func TestMarkEventEmptyInterfaceIDIsNoop(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)
	ec.MarkEvent("", time.Now())
	_, observed := ec.LastEventMonotonicForInterface("")
	if observed {
		t.Fatal("empty interfaceID must not be stored in lastEventStamp")
	}
}

// ---------------------------------------------------------------------------
// 6. MarkEvent is not guarded by monotonicity — a newer call always wins.
// (The implementation has no "only advance forward" guard; this test
// documents and verifies the actual behaviour.)
// ---------------------------------------------------------------------------

func TestMarkEventAlwaysOverwrites(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)

	ec.MarkEvent("iface-z", future)
	ec.MarkEvent("iface-z", past) // older — must still overwrite
	got, _ := ec.LastEventMonotonicForInterface("iface-z")
	if !got.Equal(past) {
		t.Fatalf("got %v; want %v — implementation unconditionally overwrites", got, past)
	}
}

// ---------------------------------------------------------------------------
// 7. HandleRawEvent persists the value in the cache for the correct key.
// ---------------------------------------------------------------------------

func TestHandleRawEventStoresValueInCache(t *testing.T) {
	t.Parallel()
	ec, _, cache := newTestEC(t)

	key := hmtypes.DataPointKey{
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "DEV001:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "LEVEL",
	}
	ec.HandleRawEvent(context.Background(), key.InterfaceID, key.ChannelAddress, key.Parameter, xmlrpc.DoubleValue(0.42))

	entry, ok := cache.Get(key)
	if !ok {
		t.Fatal("HandleRawEvent must store value in cache")
	}
	if entry.Value.Kind != hmtypes.ValueKindFloat {
		t.Fatalf("kind=%v; want float", entry.Value.Kind)
	}
	if entry.Value.Float != 0.42 {
		t.Fatalf("value=%v; want 0.42", entry.Value.Float)
	}
}

// ---------------------------------------------------------------------------
// 8. HandleRawEvent: first observation publishes with OldValue = NoneValue.
// ---------------------------------------------------------------------------

func TestHandleRawEventFirstObservationOldValueIsNone(t *testing.T) {
	t.Parallel()
	ec, bus, _ := newTestEC(t)

	var received []hmevent.DataPointValueChangedEvent
	events.Subscribe(bus, func(e hmevent.DataPointValueChangedEvent) {
		received = append(received, e)
	})

	ec.HandleRawEvent(context.Background(), "iface", "A:1", "STATE", xmlrpc.BoolValue(true))

	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].OldValue.Kind != hmtypes.ValueKindNone {
		t.Fatalf("OldValue.Kind=%v; want none for first observation", received[0].OldValue.Kind)
	}
	if received[0].NewValue.Kind != hmtypes.ValueKindBool || !received[0].NewValue.Bool {
		t.Fatalf("NewValue=%+v; want bool(true)", received[0].NewValue)
	}
}

// ---------------------------------------------------------------------------
// 9. HandleRawEvent: nil wire value maps to NoneValue in cache + event.
// ---------------------------------------------------------------------------

func TestHandleRawEventNilWireValueMapsToNone(t *testing.T) {
	t.Parallel()
	ec, bus, cache := newTestEC(t)

	var received []hmevent.DataPointValueChangedEvent
	events.Subscribe(bus, func(e hmevent.DataPointValueChangedEvent) {
		received = append(received, e)
	})

	ec.HandleRawEvent(context.Background(), "iface", "B:1", "PARAM", xmlrpc.NilValue{})

	key := hmtypes.DataPointKey{
		InterfaceID:    "iface",
		ChannelAddress: "B:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "PARAM",
	}
	entry, ok := cache.Get(key)
	if !ok {
		t.Fatal("expected cache entry even for nil wire value")
	}
	if entry.Value.Kind != hmtypes.ValueKindNone {
		t.Fatalf("cache Kind=%v; want none", entry.Value.Kind)
	}
	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].NewValue.Kind != hmtypes.ValueKindNone {
		t.Fatalf("event NewValue.Kind=%v; want none", received[0].NewValue.Kind)
	}
}

// ---------------------------------------------------------------------------
// 10. CONFIG_PENDING false→true does NOT fire the hook.
// ---------------------------------------------------------------------------

func TestHandleRawEventConfigPendingFalseToTrueNoHook(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)
	var hookFired atomic.Int32
	ec.SetOnConfigSettled(func(_, _ string) { hookFired.Add(1) })

	// Establish baseline: false (device already settled).
	ec.HandleRawEvent(context.Background(), "HmIP-RF", "DEV:0", "CONFIG_PENDING", xmlrpc.BoolValue(false))
	// Transition false→true (write in progress — must NOT fire hook).
	ec.HandleRawEvent(context.Background(), "HmIP-RF", "DEV:0", "CONFIG_PENDING", xmlrpc.BoolValue(true))

	if hookFired.Load() != 0 {
		t.Fatalf("false→true must not fire hook, fired %d time(s)", hookFired.Load())
	}
}

// ---------------------------------------------------------------------------
// 11. false→true→false cycle fires hook exactly once.
// ---------------------------------------------------------------------------

func TestHandleRawEventConfigPendingFalseTrueFalseFiresOnce(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)
	var hookFired atomic.Int32
	ec.SetOnConfigSettled(func(_, _ string) { hookFired.Add(1) })

	ec.HandleRawEvent(context.Background(), "iface", "X:0", "CONFIG_PENDING", xmlrpc.BoolValue(false))
	ec.HandleRawEvent(context.Background(), "iface", "X:0", "CONFIG_PENDING", xmlrpc.BoolValue(true))
	ec.HandleRawEvent(context.Background(), "iface", "X:0", "CONFIG_PENDING", xmlrpc.BoolValue(false))

	if hookFired.Load() != 1 {
		t.Fatalf("expected hook to fire exactly once, fired %d time(s)", hookFired.Load())
	}
}

// ---------------------------------------------------------------------------
// 12. SetOnConfigSettled(nil) — subsequent CONFIG_PENDING transition is safe.
// ---------------------------------------------------------------------------

func TestSetOnConfigSettledNilIsSafe(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)

	// First install a hook, then remove it.
	ec.SetOnConfigSettled(func(_, _ string) { /* installed */ })
	ec.SetOnConfigSettled(nil) // detach

	// Trigger the transition — must not panic.
	ec.HandleRawEvent(context.Background(), "iface", "A:0", "CONFIG_PENDING", xmlrpc.BoolValue(true))
	ec.HandleRawEvent(context.Background(), "iface", "A:0", "CONFIG_PENDING", xmlrpc.BoolValue(false))
	// No assertion beyond "didn't panic".
}

// ---------------------------------------------------------------------------
// 13. Concurrent HandleRawEvent calls on different keys must not race.
// Run with -race to detect data races.
// ---------------------------------------------------------------------------

func TestHandleRawEventConcurrentNoRace(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)
	ctx := context.Background()

	const goroutines = 20
	const eventsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := range goroutines {
		go func() {
			defer wg.Done()
			iface := "iface"
			addr := "ADDR:1"
			for i := range eventsPerGoroutine {
				// Alternate value types to exercise different branches.
				if (g+i)%2 == 0 {
					ec.HandleRawEvent(ctx, iface, addr, "LEVEL", xmlrpc.DoubleValue(float64(i)/100.0))
				} else {
					ec.HandleRawEvent(ctx, iface, addr, "LEVEL", xmlrpc.DoubleValue(float64(i+1)/100.0))
				}
			}
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// 14. Concurrent MarkEvent + LastEventMonotonicForInterface must not race.
// ---------------------------------------------------------------------------

func TestMarkEventConcurrentNoRace(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)
	for i := range goroutines {

		go func() {
			defer wg.Done()
			ec.MarkEvent("iface", time.Now().Add(time.Duration(i)*time.Millisecond))
		}()
		go func() {
			defer wg.Done()
			_, _ = ec.LastEventMonotonicForInterface("iface")
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// 15. splitDeviceAddress (package-internal): strips channel suffix correctly.
// A device address with no colon must be returned unchanged.
// ---------------------------------------------------------------------------

func TestSplitDeviceAddress(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"ABC0001:0", "ABC0001"},
		{"ABC0001:12", "ABC0001"},
		{"NOCOTON", "NOCOTON"},
		{"", ""},
		{":0", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := splitDeviceAddress(tc.in); got != tc.want {
				t.Fatalf("splitDeviceAddress(%q)=%q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 16. paramValueFromWire — unknown / unhandled type maps to NoneValue.
// ---------------------------------------------------------------------------

func TestParamValueFromWireUnknownType(t *testing.T) {
	t.Parallel()
	// xmlrpc.DateTimeValue is a defined type but not handled in the switch —
	// the default branch returns NoneValue.
	v := paramValueFromWire(xmlrpc.DateTimeValue(time.Now()))
	if v.Kind != hmtypes.ValueKindNone {
		t.Fatalf("unknown wire type should map to NoneValue, got Kind=%v", v.Kind)
	}
}

// ---------------------------------------------------------------------------
// 17. paramValueFromWire — ArrayValue with mixed types: only strings extracted.
// ---------------------------------------------------------------------------

func TestParamValueFromWireArrayValue(t *testing.T) {
	t.Parallel()
	arr := xmlrpc.ArrayValue{
		xmlrpc.StringValue("a"),
		xmlrpc.IntValue(42), // non-string; must be silently skipped
		xmlrpc.StringValue("b"),
	}
	v := paramValueFromWire(arr)
	if v.Kind != hmtypes.ValueKindList {
		t.Fatalf("ArrayValue should map to ListValue, got Kind=%v", v.Kind)
	}
	if len(v.List) != 2 || v.List[0] != "a" || v.List[1] != "b" {
		t.Fatalf("list=%v; want [a b]", v.List)
	}
}

// ---------------------------------------------------------------------------
// 18. HandleRawEvent updates the per-interface timestamp even for duplicates.
// ---------------------------------------------------------------------------

func TestHandleRawEventUpdatesStampOnDuplicate(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)

	ec.HandleRawEvent(context.Background(), "iface", "C:1", "TEMP", xmlrpc.DoubleValue(21.0))
	t1, _ := ec.LastEventMonotonicForInterface("iface")

	// Small sleep so time.Now() advances.
	time.Sleep(time.Millisecond)
	ec.HandleRawEvent(context.Background(), "iface", "C:1", "TEMP", xmlrpc.DoubleValue(21.0)) // duplicate
	t2, _ := ec.LastEventMonotonicForInterface("iface")

	// The stamp must be refreshed even though no event was emitted.
	if !t2.After(t1) {
		t.Fatalf("stamp should advance on duplicate event: t1=%v t2=%v", t1, t2)
	}
}

// TestHandleRawEventNormalizedPONGForwardsCallerID verifies that a PONG event
// is forwarded to the tracker hook with the event's interfaceID and the raw
// echoed caller_id. Token extraction + prefix verification live in the hook
// (wiring layer), which has the client identity — the coordinator only routes.
func TestHandleRawEventNormalizedPONGForwardsCallerID(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)

	var gotIfID, gotCallerID string
	ec.SetPingPongTracker(func(ifID, callerID string) {
		gotIfID = ifID
		gotCallerID = callerID
	})

	const ifaceID = "HmIP-RF"
	callerID := ifaceID + "#42"
	ec.HandleRawEventNormalized(
		context.Background(),
		ifaceID, "", "PONG",
		xmlrpc.StringValue(callerID),
	)

	if gotIfID != ifaceID {
		t.Errorf("interfaceID = %q, want %q", gotIfID, ifaceID)
	}
	if gotCallerID != callerID {
		t.Errorf("callerID = %q, want %q", gotCallerID, callerID)
	}
}

// TestHandleRawEventNormalizedPONGForwardsBareCallerID verifies that the
// coordinator forwards the raw caller_id even when it carries no '#' token.
// The coordinator does not filter — that is the hook's job (it discards
// tokenless and foreign-prefixed caller_ids so they are never recorded as
// unknown mismatches). See the wiring-layer correlation test for the discard
// behaviour.
func TestHandleRawEventNormalizedPONGForwardsBareCallerID(t *testing.T) {
	t.Parallel()
	ec, _, _ := newTestEC(t)

	var gotCallerID string
	called := false
	ec.SetPingPongTracker(func(_, callerID string) {
		called = true
		gotCallerID = callerID
	})

	ec.HandleRawEventNormalized(
		context.Background(),
		"HmIP-RF", "", "PONG",
		xmlrpc.StringValue("HmIP-RF"), // no '#'
	)

	if !called || gotCallerID != "HmIP-RF" {
		t.Errorf("coordinator must forward the raw caller_id; called=%v callerID=%q",
			called, gotCallerID)
	}
}

// TestHandleRawEventNormalizedPONGNotDispatchedAsDataPoint verifies that a
// PONG event never reaches the HandleRawEvent data-point path (no bus event).
func TestHandleRawEventNormalizedPONGNotDispatchedAsDataPoint(t *testing.T) {
	t.Parallel()
	ec, bus, _ := newTestEC(t)
	ec.SetCentralName("ccu-01")

	var fired atomic.Int32
	unsub := events.Subscribe(bus, func(_ hmevent.DataPointValueChangedEvent) {
		fired.Add(1)
	})
	defer unsub()

	ec.HandleRawEventNormalized(
		context.Background(),
		"HmIP-RF", "ADDR:1", "PONG",
		xmlrpc.StringValue("HmIP-RF#99"),
	)

	if n := fired.Load(); n != 0 {
		t.Fatalf("PONG must not produce a DataPointValueChangedEvent, got %d", n)
	}
}
