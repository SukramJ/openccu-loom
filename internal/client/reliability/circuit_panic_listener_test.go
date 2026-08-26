// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCircuitPanickingListenerDoesNotUnwindTheBreaker verifies that a
// state-change listener which panics neither reaches the caller that
// produced the transition nor stops the remaining listeners — for every
// transition, whichever call publishes it.
func TestCircuitPanickingListenerDoesNotUnwindTheBreaker(t *testing.T) {
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

	c.OnStateChange(func(_, _ hmenum.CircuitState) {
		panic("intentional panic from test listener")
	})

	var (
		callsMu sync.Mutex
		gotTo   []hmenum.CircuitState
	)
	c.AddOnStateChange(func(_, to hmenum.CircuitState) {
		callsMu.Lock()
		gotTo = append(gotTo, to)
		callsMu.Unlock()
	})

	// CLOSED → OPEN is published by record().
	_ = c.Do(context.Background(), "setValue", func(_ context.Context) error {
		return errors.New("boom")
	})
	if got := c.State(); got != hmenum.CircuitStateOpen {
		t.Fatalf("state = %s, want OPEN", got)
	}

	// OPEN → HALF_OPEN is published by the caller of refreshLocked.
	advanceClock(2 * time.Second)
	if got := c.State(); got != hmenum.CircuitStateHalfOpen {
		t.Fatalf("state = %s, want HALF_OPEN", got)
	}

	// HALF_OPEN → CLOSED is published by Reset().
	c.Reset()

	callsMu.Lock()
	defer callsMu.Unlock()
	want := []hmenum.CircuitState{
		hmenum.CircuitStateOpen,
		hmenum.CircuitStateHalfOpen,
		hmenum.CircuitStateClosed,
	}
	if len(gotTo) != len(want) {
		t.Fatalf("listener saw %v, want %v — a panicking listener must not stop the ones after it", gotTo, want)
	}
	for i, w := range want {
		if gotTo[i] != w {
			t.Fatalf("transition[%d] = %s, want %s (saw %v)", i, gotTo[i], w, gotTo)
		}
	}
}
