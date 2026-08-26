// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package events

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// panickingEvent panics when asked for its type. Real events cannot do
// this by construction today, but Type and EventKey are methods on
// caller-supplied structs: a nil field dereferenced in one of them is a
// single edit away, and the bus must survive it.
type panickingEvent struct{ hmevent.Base }

func (panickingEvent) Type() hmevent.EventType {
	panic("event identity blew up")
}

func (panickingEvent) EventKey() string { return "" }

// TestPanicReadingEventIdentityDoesNotWedgeTheBus pins that a panic
// while reading an event's identity leaves the bus usable.
//
// The dispatch lock is released by flushDeferred, not by a defer, and
// Type() used to be called inside dispatchNow with no recovery around
// it. One panicking Type() therefore left b.dispatch held forever: every
// later Publish queued into the deferred backlog with nothing left to
// drain it, and every cross-goroutine unsubscribe blocked on an
// in-flight mark that was never released. The daemon kept running and
// silently stopped delivering every event of every type.
func TestPanicReadingEventIdentityDoesNotWedgeTheBus(t *testing.T) {
	t.Parallel()

	b := NewBus()
	var delivered atomic.Int32
	unsub := Subscribe(b, func(advEvtGamma) { delivered.Add(1) })

	Publish(b, panickingEvent{Base: hmevent.NewBase()})

	// The bus must still deliver.
	Publish(b, advEvtGamma{Base: hmevent.NewBase()})
	if got := delivered.Load(); got != 1 {
		t.Fatalf("deliveries after a panicking event = %d, want 1 — the bus is wedged", got)
	}

	// And a cross-goroutine unsubscribe must not block on a leaked
	// in-flight mark.
	done := make(chan struct{})
	go func() {
		unsub()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("unsubscribe blocked: an in-flight mark was never released")
	}
}
