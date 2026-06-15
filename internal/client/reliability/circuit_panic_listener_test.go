// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCircuitPanicingListenerInRefreshLockedDoesNotCrash verifies that a
// state-change listener that panics during the async OPEN→HALF_OPEN
// transition fired by refreshLocked does not kill the dispatcher goroutine
// and does not prevent subsequent listeners from being called.
//
// Background: refreshLocked fires listeners asynchronously (via safeFire)
// to avoid running user code while holding the mutex. A panicking listener
// would previously kill its goroutine silently; safeFire now recovers and
// logs, so the breaker continues operating normally.
func TestCircuitPanicingListenerInRefreshLockedDoesNotCrash(t *testing.T) {
	t.Parallel()

	tick := time.Unix(0, 0)
	var tickMu sync.Mutex
	clock := func() time.Time {
		tickMu.Lock()
		defer tickMu.Unlock()
		return tick
	}
	advanceClock := func(d time.Duration) {
		tickMu.Lock()
		defer tickMu.Unlock()
		tick = tick.Add(d)
	}

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     1 * time.Second,
		HalfOpenSuccess:  1,
		Clock:            clock,
	})

	// Trip the breaker to OPEN before registering any listeners so the
	// synchronous callbacks in record() do not fire against the panicking
	// listener below (record() fires synchronously and would propagate the
	// panic to this goroutine before we even reach the refreshLocked path).
	c.RecordFailure()
	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("expected OPEN after RecordFailure, got %s", c.State())
	}

	// Now install the panicking listener. It will only be called when
	// refreshLocked fires the OPEN→HALF_OPEN transition asynchronously.
	c.OnStateChange(func(_, _ hmenum.CircuitState) {
		panic("intentional panic from test listener in refreshLocked")
	})

	// Non-panicking second listener that records the state it receives.
	var (
		callsMu sync.Mutex
		gotTo   []hmenum.CircuitState
	)
	c.AddOnStateChange(func(_, to hmenum.CircuitState) {
		callsMu.Lock()
		gotTo = append(gotTo, to)
		callsMu.Unlock()
	})

	// Advance past ResetTimeout — State() calls refreshLocked which fires
	// listeners asynchronously via safeFire. The panicking listener must not
	// propagate to this goroutine.
	advanceClock(2 * time.Second)

	// This call triggers refreshLocked → safeFire for the OPEN→HALF_OPEN
	// transition. The panicking listener fires in its own goroutine;
	// the test goroutine must not crash.
	got := c.State()
	if got != hmenum.CircuitStateHalfOpen {
		t.Fatalf("state = %s, want HALF_OPEN", got)
	}

	// Give the async goroutines time to complete (both the panicking and the
	// recording listener each run in their own safeFire goroutine).
	time.Sleep(80 * time.Millisecond)

	// The non-panicking listener must have been called for the transition.
	callsMu.Lock()
	defer callsMu.Unlock()
	if len(gotTo) == 0 {
		t.Fatal("second listener was never called — safeFire goroutines must be independent")
	}
	last := gotTo[len(gotTo)-1]
	if last != hmenum.CircuitStateHalfOpen {
		t.Fatalf("second listener last seen to = %s, want HALF_OPEN", last)
	}
}
