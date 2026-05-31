// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package events

import (
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// Batch collects events into a buffer and publishes them on Flush.
// Use it from coordinators that want subscribers to see a coherent
// "burst" of related changes instead of N individual dispatches.
//
// The batch holds a single dispatch closure per event so the type
// stays preserved through the buffer (events.Publish is generic; the
// closure captures the concrete type before it lands in the queue).
//
// Construction is cheap; reuse a Batch across loops or build a new
// one per critical section — either is fine. Methods are safe for
// concurrent use.
type Batch struct {
	bus *Bus

	mu      sync.Mutex
	pending []func()
}

// NewBatch returns a Batch that publishes through bus on Flush.
// Passing a nil bus produces a Batch whose Flush is a no-op — useful
// in tests that want to assert "no events left in the buffer" without
// wiring a real bus.
//
// loom:reachable:reason="used by coordinators that collect events during parameter-sync and flush as a unit"
func NewBatch(bus *Bus) *Batch {
	return &Batch{bus: bus}
}

// Add enqueues e for delivery on the next Flush.
func Add[T hmevent.Event](b *Batch, e T) {
	if b == nil {
		return
	}
	captured := e
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = append(b.pending, func() {
		Publish(b.bus, captured)
	})
}

// Len returns the number of pending events. Safe for concurrent use.
func (b *Batch) Len() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}

// Flush dispatches every queued event in insertion order and clears
// the buffer. Re-entrant publishes from within subscriber handlers
// follow the regular re-entrant defer path on the bus.
//
// Flush returns the number of events that were dispatched so callers
// can decide whether to log or skip.
func (b *Batch) Flush() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	pending := b.pending
	b.pending = nil
	b.mu.Unlock()
	if b.bus == nil {
		return 0
	}
	for _, fn := range pending {
		fn()
	}
	return len(pending)
}

// IsFlushed reports whether the batch has no pending events. A freshly
// constructed batch, one that has been Flushed, or one that has been
// Discarded all return true.
func (b *Batch) IsFlushed() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending) == 0
}

// Discard drops every queued event without dispatching. Use it when
// the surrounding operation aborts and the buffered events should not
// reach subscribers.
func (b *Batch) Discard() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	n := len(b.pending)
	b.pending = nil
	b.mu.Unlock()
	return n
}
