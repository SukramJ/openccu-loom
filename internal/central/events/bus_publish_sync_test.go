// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package events

// Tests for the PublishSync API.
//
// In Go the bus is already synchronous, so PublishSync is an alias for Publish.
// The tests verify that the documented guarantees hold: handlers run before the
// call returns, re-entrant calls are safely deferred, and the publish counter
// is incremented.

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// w10EvtSync is a local test event used only in this file.
type w10EvtSync struct{ hmevent.Base }

func (w10EvtSync) Type() hmevent.EventType { return "w10.evt.sync" }

// syncEvtOther is a distinct event type used to exercise PublishSync under
// dispatch contention from a different handler.
type syncEvtOther struct{ hmevent.Base }

func (syncEvtOther) Type() hmevent.EventType { return "w10.evt.sync.other" }

// ──────────────────────────────────────────────────────────────────────────────
// TestPublishSyncDispatchesHandlersSynchronously
// ──────────────────────────────────────────────────────────────────────────────

// TestPublishSyncDispatchesHandlersSynchronously verifies that handlers
// registered via Subscribe are called before PublishSync returns — identical
// to the Publish guarantee (the bus is always synchronous in Go).
func TestPublishSyncDispatchesHandlersSynchronously(t *testing.T) {
	t.Parallel()
	bus := NewBus()

	var called atomic.Int32
	unsub := Subscribe(bus, func(_ w10EvtSync) { called.Add(1) })
	defer unsub()

	PublishSync(bus, w10EvtSync{Base: hmevent.NewBase()})

	if got := called.Load(); got != 1 {
		t.Fatalf("expected handler called 1 time, got %d", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// TestPublishSyncInsideHandlerIsDeferred
// ──────────────────────────────────────────────────────────────────────────────

// TestPublishSyncInsideHandlerIsDeferred verifies that a PublishSync called
// from within a handler is buffered and flushed after the outer dispatch
// completes, preventing infinite recursion. This mirrors the re-entrancy
// protection that Publish provides.
//
// The handler republishes only once (gated by didReentrant) to keep the
// test bounded — without the gate, the deferred-flush would re-trigger the
// handler in turn and grow the queue without limit.
func TestPublishSyncInsideHandlerIsDeferred(t *testing.T) {
	t.Parallel()
	bus := NewBus()

	var outer, inner atomic.Int32
	var didReentrant atomic.Bool
	unsub := Subscribe(bus, func(_ w10EvtSync) {
		outer.Add(1)
		if didReentrant.CompareAndSwap(false, true) {
			// Single re-entrant publish from inside a handler.
			PublishSync(bus, w10EvtSync{Base: hmevent.NewBase()})
		}
	})
	defer unsub()

	unsub2 := Subscribe(bus, func(_ w10EvtSync) { inner.Add(1) })
	defer unsub2()

	// First publish: outer fires once, defers the re-entrant one.
	PublishSync(bus, w10EvtSync{Base: hmevent.NewBase()})

	// After flush the re-entrant publish ran: outer=2, inner=2.
	if o := outer.Load(); o != 2 {
		t.Errorf("outer handler calls: want 2, got %d", o)
	}
	if i := inner.Load(); i != 2 {
		t.Errorf("inner handler calls: want 2, got %d", i)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// TestPublishSyncCountedInEventStats
// ──────────────────────────────────────────────────────────────────────────────

// TestPublishSyncCountedInEventStats verifies that events published via
// PublishSync are reflected in the bus event statistics, just like Publish.
func TestPublishSyncCountedInEventStats(t *testing.T) {
	t.Parallel()
	bus := NewBus()

	// No subscriber needed — stats count publishes regardless.
	PublishSync(bus, w10EvtSync{Base: hmevent.NewBase()})
	PublishSync(bus, w10EvtSync{Base: hmevent.NewBase()})

	stats := bus.EventStats()
	typ := hmevent.EventType("w10.evt.sync")
	if got := stats[string(typ)]; got != 2 {
		t.Errorf("event stats for %q: want 2, got %d", typ, got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// TestPublishSyncIsNotGuaranteedSynchronousUnderContention
// ──────────────────────────────────────────────────────────────────────────────

// TestPublishSyncIsNotGuaranteedSynchronousUnderContention pins the honest
// contract: PublishSync is a plain alias of Publish, so when another goroutine
// already holds the dispatch lock, PublishSync buffers the event and returns
// BEFORE its handler runs — it does not block-drain. A caller must not treat it
// as a synchronous read-after-publish barrier.
func TestPublishSyncIsNotGuaranteedSynchronousUnderContention(t *testing.T) {
	t.Parallel()
	bus := NewBus()

	inHandler := make(chan struct{})
	release := make(chan struct{})
	var otherRan atomic.Bool

	// Handler A occupies the dispatch lock for the duration of the test.
	unsubA := Subscribe(bus, func(_ w10EvtSync) {
		close(inHandler)
		<-release
	})
	unsubB := Subscribe(bus, func(_ syncEvtOther) { otherRan.Store(true) })
	defer unsubB()

	go Publish(bus, w10EvtSync{Base: hmevent.NewBase()}) // acquires dispatch, then blocks
	<-inHandler

	// The dispatch lock is held by A. PublishSync of a different event must
	// return promptly (buffered to the deferred queue), not block until B runs.
	done := make(chan struct{})
	go func() {
		PublishSync(bus, syncEvtOther{Base: hmevent.NewBase()})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PublishSync blocked while another goroutine held the dispatch lock")
	}

	// B has not run yet — proving PublishSync is not synchronous under
	// contention. B is still queued and drains once A releases the lock.
	if otherRan.Load() {
		t.Fatal("PublishSync ran the handler synchronously despite dispatch contention")
	}

	close(release)
	unsubA() // barrier: A's dispatch (and the deferred drain of B) has completed
}
