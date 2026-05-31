// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package events

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

func TestBusDispatchesToSubscriber(t *testing.T) {
	b := NewBus()
	var called int
	Subscribe(b, func(e hmevent.CentralStateChangedEvent) {
		called++
		_ = e
	})
	Publish(b, hmevent.CentralStateChangedEvent{CentralName: "main"})
	if called != 1 {
		t.Fatalf("handler called %d times, want 1", called)
	}
}

func TestBusUnsubscribe(t *testing.T) {
	b := NewBus()
	var called int
	unsub := Subscribe(b, func(e hmevent.CentralStateChangedEvent) { called++ })
	Publish(b, hmevent.CentralStateChangedEvent{})
	unsub()
	unsub() // idempotent
	Publish(b, hmevent.CentralStateChangedEvent{})
	if called != 1 {
		t.Fatalf("post-unsubscribe called=%d, want 1", called)
	}
}

func TestBusPriorityOrdering(t *testing.T) {
	b := NewBus()
	var order []string
	Subscribe(b, func(e hmevent.CentralStateChangedEvent) { order = append(order, "low") }, WithPriority(PriorityLow))
	Subscribe(b, func(e hmevent.CentralStateChangedEvent) { order = append(order, "normal") })
	Subscribe(b, func(e hmevent.CentralStateChangedEvent) { order = append(order, "high") }, WithPriority(PriorityHigh))

	Publish(b, hmevent.CentralStateChangedEvent{})
	if len(order) != 3 || order[0] != "high" || order[1] != "normal" || order[2] != "low" {
		t.Fatalf("order=%v", order)
	}
}

func TestBusPriorityTieFIFO(t *testing.T) {
	b := NewBus()
	var order []string
	Subscribe(b, func(e hmevent.CentralStateChangedEvent) { order = append(order, "first") })
	Subscribe(b, func(e hmevent.CentralStateChangedEvent) { order = append(order, "second") })

	Publish(b, hmevent.CentralStateChangedEvent{})
	if order[0] != "first" || order[1] != "second" {
		t.Fatalf("FIFO broken: %v", order)
	}
}

func TestBusHandlerCount(t *testing.T) {
	b := NewBus()
	u1 := Subscribe(b, func(e hmevent.DataPointValueChangedEvent) {})
	Subscribe(b, func(e hmevent.DataPointValueChangedEvent) {})
	if got := b.HandlerCount(hmevent.EventTypeDataPointValueChanged); got != 2 {
		t.Fatalf("HandlerCount=%d, want 2", got)
	}
	u1()
	if got := b.HandlerCount(hmevent.EventTypeDataPointValueChanged); got != 1 {
		t.Fatalf("HandlerCount after unsub=%d, want 1", got)
	}
}

func TestBusReentrantPublishDefers(t *testing.T) {
	b := NewBus()
	var inner atomic.Int32
	Subscribe(b, func(e hmevent.CentralStateChangedEvent) {
		if e.CentralName == "outer" {
			// Publish from inside a handler frame — must not run immediately.
			Publish(b, hmevent.CentralStateChangedEvent{CentralName: "inner"})
			// When we read here the inner handler has not yet executed.
			if inner.Load() != 0 {
				t.Errorf("re-entrant publish was not deferred (inner=%d)", inner.Load())
			}
		}
	})
	Subscribe(b, func(e hmevent.CentralStateChangedEvent) {
		if e.CentralName == "inner" {
			inner.Add(1)
		}
	})
	Publish(b, hmevent.CentralStateChangedEvent{CentralName: "outer"})
	if inner.Load() != 1 {
		t.Fatalf("deferred publish never ran, inner=%d", inner.Load())
	}
}

func TestBusDeferredHighWaterTracksRecursion(t *testing.T) {
	b := NewBus()
	// Outer handler publishes N inner events before returning. The
	// deferred buffer hits depth N, the high-water gauge captures it,
	// then flushDeferred drains the queue and runs every inner
	// handler. No events are lost — the buffer is unbounded by design;
	// the gauge is the operator's only signal of pathological recursion.
	const recursionBurst = 200
	var innerCalls atomic.Int32
	Subscribe(b, func(e hmevent.CentralStateChangedEvent) {
		if e.CentralName != "outer" {
			innerCalls.Add(1)
			return
		}
		for i := 0; i < recursionBurst; i++ {
			Publish(b, hmevent.CentralStateChangedEvent{CentralName: "inner"})
		}
	})
	Publish(b, hmevent.CentralStateChangedEvent{CentralName: "outer"})

	if got := innerCalls.Load(); got != int32(recursionBurst) {
		t.Fatalf("inner handler ran %d times, want %d (no events should be dropped)",
			got, recursionBurst)
	}
	if hw := b.DeferredHighWater(); hw < uint64(recursionBurst) {
		t.Fatalf("DeferredHighWater=%d, want >= %d", hw, recursionBurst)
	}
	// After draining, the live depth must be zero.
	if d := b.DeferredDepth(); d != 0 {
		t.Errorf("DeferredDepth after drain=%d, want 0", d)
	}
}

func TestBusSubscribeUnrelatedEventNotCalled(t *testing.T) {
	b := NewBus()
	var state int
	var dp int
	Subscribe(b, func(e hmevent.CentralStateChangedEvent) { state++ })
	Subscribe(b, func(e hmevent.DataPointValueChangedEvent) { dp++ })

	Publish(b, hmevent.CentralStateChangedEvent{})
	if state != 1 || dp != 0 {
		t.Fatalf("state=%d dp=%d", state, dp)
	}
}

func TestBusConcurrentSubscribersAreSafe(t *testing.T) {
	b := NewBus()
	var mu sync.Mutex
	var count int
	// Register a single handler; a race condition during subscription
	// with Publish should never observe an invalid slice.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unsub := Subscribe(b, func(e hmevent.CentralStateChangedEvent) {
				mu.Lock()
				count++
				mu.Unlock()
			})
			Publish(b, hmevent.CentralStateChangedEvent{})
			unsub()
		}()
	}
	wg.Wait()
	// No assertion on count (depends on scheduler); just ensure we
	// didn't deadlock or race.
	_ = hmenum.CentralStateRunning
}
