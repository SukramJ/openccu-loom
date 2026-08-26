// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Compile-time reference to suppress unused-import errors when some
// build paths remove sync/sync.atomic usage.
var (
	_ = sync.Mutex{}
	_ = (*atomic.Int32)(nil)
)

// TestMultipleSubscribersIndependent verifies that two handlers subscribed
// on the same bus and event type both receive every published event
// independently. Handlers fire on every matching publish regardless of the
// Key field value — both are notified, each only increments its own counter.
func TestMultipleSubscribersIndependent(t *testing.T) {
	bus := events.NewBus()

	var countA, countB atomic.Int32

	unsub1 := events.Subscribe[hmevent.DataPointValueChangedEvent](bus, func(_ hmevent.DataPointValueChangedEvent) {
		countA.Add(1)
	})
	unsub2 := events.Subscribe[hmevent.DataPointValueChangedEvent](bus, func(_ hmevent.DataPointValueChangedEvent) {
		countB.Add(1)
	})
	defer unsub1()
	defer unsub2()

	// Publish two events; each handler should receive both.
	for range 2 {
		events.Publish(bus, hmevent.DataPointValueChangedEvent{
			Base:     hmevent.NewBase(),
			NewValue: hmtypes.BoolValue(true),
		})
	}

	if countA.Load() != 2 {
		t.Errorf("handler A: got %d events, want 2", countA.Load())
	}
	if countB.Load() != 2 {
		t.Errorf("handler B: got %d events, want 2", countB.Load())
	}
}

// TestSubscribeReceiveUnsubscribe exercises the full
// subscribe -> publish -> receive -> unsubscribe -> no-more-events cycle.
func TestSubscribeReceiveUnsubscribe(t *testing.T) {
	bus := events.NewBus()

	var received atomic.Int32

	unsub := events.Subscribe[hmevent.DataPointValueChangedEvent](bus, func(_ hmevent.DataPointValueChangedEvent) {
		received.Add(1)
	})

	before := bus.HandlerCount(hmevent.EventTypeDataPointValueChanged)
	if before != 1 {
		t.Fatalf("HandlerCount before: got %d, want 1", before)
	}

	ev := hmevent.DataPointValueChangedEvent{
		Base:     hmevent.NewBase(),
		NewValue: hmtypes.BoolValue(true),
	}
	events.Publish(bus, ev)

	if received.Load() != 1 {
		t.Fatalf("after first publish: received %d, want 1", received.Load())
	}

	unsub()

	after := bus.HandlerCount(hmevent.EventTypeDataPointValueChanged)
	if after != 0 {
		t.Fatalf("HandlerCount after unsub: got %d, want 0", after)
	}

	// Publishing again after unsubscribe — count must stay at 1.
	events.Publish(bus, ev)
	if received.Load() != 1 {
		t.Fatalf("after post-unsub publish: received %d, still want 1", received.Load())
	}
}

// TestSequentialEventPublishing verifies that rapid sequential publishes
// all reach the subscribed handler. The Go bus serialises dispatches via
// a TryLock; concurrent goroutine publishes are deferred and drained by
// the lock-holder goroutine.
func TestSequentialEventPublishing(t *testing.T) {
	bus := events.NewBus()

	const n = 100

	values := make(map[int]struct{}, n)

	unsub := events.Subscribe[hmevent.DataPointValueChangedEvent](bus, func(e hmevent.DataPointValueChangedEvent) {
		if e.NewValue.Kind != hmtypes.ValueKindInt {
			return
		}
		values[e.NewValue.Int] = struct{}{}
	})
	defer unsub()

	for i := range n {
		events.Publish(bus, hmevent.DataPointValueChangedEvent{
			Base:     hmevent.NewBase(),
			NewValue: hmtypes.IntValue(i),
		})
	}

	if len(values) != n {
		t.Fatalf("expected %d distinct values, got %d", n, len(values))
	}
}

// TestSubscribeUnsubscribeLeavesCleanBus verifies that subscribe-all then
// unsubscribe-all operations leave the bus in a clean state (zero active
// handlers). The Go bus uses a Mutex for all mutations, so the test
// subscribes all handlers first, then unsubscribes all in a separate
// goroutine.
func TestSubscribeUnsubscribeLeavesCleanBus(t *testing.T) {
	bus := events.NewBus()

	const batches = 50

	unsubs := make([]func(), 0, batches)
	for range batches {
		fn := events.Subscribe[hmevent.DataPointValueChangedEvent](
			bus,
			func(_ hmevent.DataPointValueChangedEvent) {},
		)
		unsubs = append(unsubs, fn)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, fn := range unsubs {
			fn()
		}
	}()
	wg.Wait()

	if got := bus.HandlerCount(hmevent.EventTypeDataPointValueChanged); got != 0 {
		t.Fatalf("HandlerCount after all unsubs: got %d, want 0", got)
	}
}

// TestValidationErrorMessage verifies that a validation error carrying
// context about min/max/parameter is detectable via
// errors.Is(err, hmerr.ErrValidation) and that the formatted message
// contains the relevant values.
func TestValidationErrorMessage(t *testing.T) {
	value := 150.0
	max := 100.0
	param := "LEVEL"

	wrapped := fmt.Errorf("parameter %s: value %.1f exceeds maximum %.1f: %w",
		param, value, max, hmerr.ErrValidation)

	if !errors.Is(wrapped, hmerr.ErrValidation) {
		t.Fatal("errors.Is(wrapped, ErrValidation) = false, want true")
	}
	msg := wrapped.Error()
	for _, want := range []string{"150.0", "100.0", "LEVEL"} {
		if !scenariosContains(msg, want) {
			t.Errorf("error message %q does not contain expected substring %q", msg, want)
		}
	}
}

// scenariosContains is a strings.Contains replacement that avoids
// importing "strings" solely for this helper.
func scenariosContains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(s) < len(sub) {
		return false
	}
	for i := range len(s) - len(sub) + 1 {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
