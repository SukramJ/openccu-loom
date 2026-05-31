// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package events

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ---------- SubscriptionGroup tests ----------

func TestSubscriptionGroupCloseRunsEveryUnsub(t *testing.T) {
	t.Parallel()

	b := NewBus()
	var g SubscriptionGroup

	var mu sync.Mutex
	received := 0
	counter := func(hmevent.DeviceCreatedEvent) {
		mu.Lock()
		received++
		mu.Unlock()
	}

	g.Add(Subscribe(b, counter))
	g.Add(Subscribe(b, counter))

	// Both handlers must fire before Close.
	Publish(b, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase(), CentralName: "main"})
	mu.Lock()
	if received != 2 {
		t.Fatalf("before Close: received=%d, want 2", received)
	}
	mu.Unlock()

	g.Close()

	// After Close, neither subscription should fire.
	mu.Lock()
	received = 0
	mu.Unlock()
	Publish(b, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase(), CentralName: "main"})
	mu.Lock()
	defer mu.Unlock()
	if received != 0 {
		t.Fatalf("after Close: received=%d, want 0", received)
	}
}

func TestSubscriptionGroupCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	var g SubscriptionGroup
	b := NewBus()
	g.Add(Subscribe(b, func(hmevent.DeviceCreatedEvent) {}))

	// Neither call must panic.
	g.Close()
	g.Close()
}

func TestSubscriptionGroupAddIgnoresNil(t *testing.T) {
	t.Parallel()

	var g SubscriptionGroup
	b := NewBus()
	realUnsub := Subscribe(b, func(hmevent.DeviceCreatedEvent) {})

	g.Add(nil, nil, realUnsub, nil)

	if got := g.Len(); got != 1 {
		t.Fatalf("Len=%d, want 1", got)
	}
}

func TestSubscriptionGroupNilReceiverNoop(t *testing.T) {
	t.Parallel()

	var g *SubscriptionGroup
	b := NewBus()

	// None of these must panic.
	g.Add(Subscribe(b, func(hmevent.DeviceCreatedEvent) {}))
	g.Close()
	if got := g.Len(); got != 0 {
		t.Fatalf("nil receiver Len=%d, want 0", got)
	}
}

func TestSubscriptionGroupLenAfterClose(t *testing.T) {
	t.Parallel()

	var g SubscriptionGroup
	b := NewBus()
	g.Add(Subscribe(b, func(hmevent.DeviceCreatedEvent) {}))
	g.Add(Subscribe(b, func(hmevent.DeviceCreatedEvent) {}))

	if got := g.Len(); got != 2 {
		t.Fatalf("before Close: Len=%d, want 2", got)
	}

	g.Close()

	if got := g.Len(); got != 0 {
		t.Fatalf("after Close: Len=%d, want 0", got)
	}
}

// ---------- Batch tests ----------

func TestBatchFlushDispatchesInOrder(t *testing.T) {
	t.Parallel()

	b := NewBus()
	batch := NewBatch(b)

	var mu sync.Mutex
	var got []string

	Subscribe(b, func(e hmevent.DeviceCreatedEvent) {
		mu.Lock()
		got = append(got, e.Address)
		mu.Unlock()
	})

	e1 := hmevent.DeviceCreatedEvent{Base: hmevent.NewBase(), CentralName: "main", Address: "first"}
	e2 := hmevent.DeviceCreatedEvent{Base: hmevent.NewBase(), CentralName: "main", Address: "second"}
	Add(batch, e1)
	Add(batch, e2)

	n := batch.Flush()
	if n != 2 {
		t.Fatalf("Flush returned %d, want 2", n)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("order=%v, want [first second]", got)
	}
}

func TestBatchFlushClearsBuffer(t *testing.T) {
	t.Parallel()

	b := NewBus()
	batch := NewBatch(b)

	var dispatched int
	Subscribe(b, func(hmevent.DeviceCreatedEvent) { dispatched++ })

	Add(batch, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase()})
	batch.Flush()

	// Second flush must dispatch nothing and return 0.
	n := batch.Flush()
	if n != 0 {
		t.Fatalf("second Flush returned %d, want 0", n)
	}
	if dispatched != 1 {
		t.Fatalf("dispatched=%d, want 1", dispatched)
	}
}

func TestBatchDiscardDropsWithoutDispatch(t *testing.T) {
	t.Parallel()

	b := NewBus()
	batch := NewBatch(b)

	var dispatched int
	Subscribe(b, func(hmevent.DeviceCreatedEvent) { dispatched++ })

	Add(batch, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase()})
	Add(batch, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase()})

	n := batch.Discard()
	if n != 2 {
		t.Fatalf("Discard returned %d, want 2", n)
	}

	flushed := batch.Flush()
	if flushed != 0 {
		t.Fatalf("Flush after Discard returned %d, want 0", flushed)
	}
	if dispatched != 0 {
		t.Fatalf("dispatched=%d after Discard, want 0", dispatched)
	}
}

func TestBatchLenReflectsPending(t *testing.T) {
	t.Parallel()

	b := NewBus()
	batch := NewBatch(b)

	if got := batch.Len(); got != 0 {
		t.Fatalf("initial Len=%d, want 0", got)
	}

	Add(batch, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase()})
	if got := batch.Len(); got != 1 {
		t.Fatalf("after first Add: Len=%d, want 1", got)
	}

	Add(batch, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase()})
	if got := batch.Len(); got != 2 {
		t.Fatalf("after second Add: Len=%d, want 2", got)
	}

	batch.Flush()
	if got := batch.Len(); got != 0 {
		t.Fatalf("after Flush: Len=%d, want 0", got)
	}
}

func TestBatchNilReceiverIsNoop(t *testing.T) {
	t.Parallel()

	var b *Batch

	// None of these must panic.
	Add(b, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase()})
	if n := b.Flush(); n != 0 {
		t.Fatalf("nil receiver Flush=%d, want 0", n)
	}
	if n := b.Discard(); n != 0 {
		t.Fatalf("nil receiver Discard=%d, want 0", n)
	}
}

func TestBatchNilBusFlushIsNoop(t *testing.T) {
	t.Parallel()

	batch := NewBatch(nil)

	var dispatched int
	// We cannot subscribe to a nil bus, so we wire a separate bus just
	// to confirm nothing arrives — the nil-bus batch won't touch it.
	realBus := NewBus()
	Subscribe(realBus, func(hmevent.DeviceCreatedEvent) { dispatched++ })

	Add(batch, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase()})

	// Flush on a nil-bus batch must return 0 and not panic.
	n := batch.Flush()
	if n != 0 {
		t.Fatalf("nil-bus Flush returned %d, want 0", n)
	}
	if dispatched != 0 {
		t.Fatalf("nil-bus Flush dispatched %d events to real bus, want 0", dispatched)
	}
}
