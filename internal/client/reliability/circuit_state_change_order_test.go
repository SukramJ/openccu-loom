// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCircuitStateChangesArePublishedInOrder pins that every transition
// reaches subscribers in the order the state machine produced them.
//
// The lazy OPEN → HALF_OPEN flip used to be dispatched on its own
// goroutine while every other transition was published inline, so a probe
// that failed immediately published HALF_OPEN → OPEN first and left the
// incident log and the diagnostics stream reporting "half-open" for a
// breaker that was OPEN.
func TestCircuitStateChangesArePublishedInOrder(t *testing.T) {
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

	type transition struct{ from, to hmenum.CircuitState }
	var (
		seenMu sync.Mutex
		seen   []transition
	)
	c.OnStateChange(func(from, to hmenum.CircuitState) {
		seenMu.Lock()
		seen = append(seen, transition{from, to})
		seenMu.Unlock()
	})

	failFast := func(_ context.Context) error { return errors.New("connection refused") }

	// CLOSED → OPEN.
	_ = c.Do(context.Background(), "setValue", failFast)
	// ResetTimeout elapses; the probe fails immediately, so the breaker
	// re-opens while the OPEN → HALF_OPEN transition is still being
	// published.
	advanceClock(2 * time.Second)
	_ = c.Do(context.Background(), "setValue", failFast)

	seenMu.Lock()
	defer seenMu.Unlock()
	want := []transition{
		{hmenum.CircuitStateClosed, hmenum.CircuitStateOpen},
		{hmenum.CircuitStateOpen, hmenum.CircuitStateHalfOpen},
		{hmenum.CircuitStateHalfOpen, hmenum.CircuitStateOpen},
	}
	if len(seen) != len(want) {
		t.Fatalf("transitions = %v, want %v", seen, want)
	}
	for i, w := range want {
		if seen[i] != w {
			t.Fatalf("transition[%d] = %s→%s, want %s→%s (full order %v)",
				i, seen[i].from, seen[i].to, w.from, w.to, seen)
		}
	}
}
