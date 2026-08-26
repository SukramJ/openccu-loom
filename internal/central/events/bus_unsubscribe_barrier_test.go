// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package events

// Tests for the unsubscribe barrier: the closure returned by Subscribe must
// block until any dispatch pass that already captured the handler has finished
// invoking it, and a handler unsubscribed mid-pass must never fire. Both are
// concurrency invariants, so the suite is written to surface violations under
// `go test -race`.

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

type barrierEvt struct{ hmevent.Base }

func (barrierEvt) Type() hmevent.EventType { return "barrier.evt" }

type barrierBlockerEvt struct{ hmevent.Base }

func (barrierBlockerEvt) Type() hmevent.EventType { return "barrier.blocker.evt" }

// TestUnsubscribeWaitsForInflightHandler asserts that unsubscribe does not
// return while its handler is still executing. The handler writes to an
// unsynchronised variable that the test reads only after unsubscribe returns —
// if the barrier is absent the two accesses race and `go test -race` fails, and
// the value read would not yet be the handler's write.
func TestUnsubscribeWaitsForInflightHandler(t *testing.T) {
	t.Parallel()
	bus := NewBus()

	// resource is deliberately not guarded by a mutex: the barrier is the
	// only thing that orders the handler's write before the post-unsub read.
	var resource int
	started := make(chan struct{})
	release := make(chan struct{})

	unsub := Subscribe(bus, func(_ barrierEvt) {
		close(started)
		<-release
		resource = 42
	})

	go Publish(bus, barrierEvt{Base: hmevent.NewBase()})

	<-started // the handler is now mid-flight, blocked on release

	// Release the handler only after unsub has had time to enter its Wait, so
	// the barrier path (not the trivial already-finished path) is exercised.
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(release)
	}()

	unsub() // must block until the handler finishes writing resource

	if resource != 42 {
		t.Fatalf("unsubscribe returned before the in-flight handler completed: resource=%d", resource)
	}
}

// TestUnsubscribedHandlerDetachedMidPassDoesNotFire asserts the dead-flag skip:
// a handler removed after a dispatch already snapshotted it (because an
// earlier, higher-priority handler is still running) must be skipped rather
// than invoked, and unsubscribe must still return once the pass reaches and
// skips it.
func TestUnsubscribedHandlerDetachedMidPassDoesNotFire(t *testing.T) {
	t.Parallel()
	bus := NewBus()

	blockerIn := make(chan struct{})
	blockerRelease := make(chan struct{})

	// High-priority blocker runs first and holds the dispatch open, so the
	// target handler is captured in the snapshot but not yet invoked.
	unsubBlocker := Subscribe(bus, func(_ barrierBlockerEvt) {
		close(blockerIn)
		<-blockerRelease
	}, WithPriority(PriorityHigh))
	defer unsubBlocker()

	var targetCalls atomic.Int32
	unsubTarget := Subscribe(bus, func(_ barrierBlockerEvt) {
		targetCalls.Add(1)
	})

	go Publish(bus, barrierBlockerEvt{Base: hmevent.NewBase()})

	<-blockerIn // dispatch is inside the blocker; target is snapshotted + in-flight

	// Detach the target. unsubscribe blocks on the target's in-flight count
	// until the pass reaches and skips it, so run it in the background and
	// release the blocker to let the pass proceed.
	unsubReturned := make(chan struct{})
	go func() {
		unsubTarget()
		close(unsubReturned)
	}()

	// Give unsubTarget a moment to flip dead before the pass reaches target.
	time.Sleep(20 * time.Millisecond)
	close(blockerRelease)

	<-unsubReturned

	if got := targetCalls.Load(); got != 0 {
		t.Fatalf("handler fired after being unsubscribed mid-pass: calls=%d", got)
	}
}
