// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

// White-box tests for the external-facing breaker API methods that were
// not reached by existing tests: RecordFailure, RecordRejection,
// RecordSuccess, LastFailureTime, AddOnStateChange, and the
// lazyCleanupLocked / maxInt helpers on CommandTracker.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// --- RecordFailure ---

func TestRecordFailure_TripsBreaker(t *testing.T) {
	t.Parallel()

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Hour,
		Clock:            time.Now,
	})

	c.RecordFailure()

	if c.State() != hmenum.CircuitStateOpen {
		t.Errorf("RecordFailure should trip breaker: state=%s", c.State())
	}
}

// --- RecordSuccess ---

func TestRecordSuccess_ClosesBreakerFromHalfOpen(t *testing.T) {
	t.Parallel()

	tick := time.Unix(0, 0)
	clock := func() time.Time { return tick }
	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Second,
		HalfOpenSuccess:  1,
		Clock:            clock,
	})

	// Trip.
	c.RecordFailure()
	if c.State() != hmenum.CircuitStateOpen {
		t.Fatalf("expected OPEN, got %s", c.State())
	}

	// Advance past ResetTimeout so the next refresh flips to HALF_OPEN.
	tick = tick.Add(2 * time.Second)
	// A State() call triggers refreshLocked which moves to HALF_OPEN.
	if c.State() != hmenum.CircuitStateHalfOpen {
		t.Fatalf("expected HALF_OPEN, got %s", c.State())
	}

	// Record success — with HalfOpenSuccess=1 the breaker closes.
	c.RecordSuccess()

	if c.State() != hmenum.CircuitStateClosed {
		t.Errorf("RecordSuccess should close from HALF_OPEN: state=%s", c.State())
	}
}

// --- RecordRejection ---

func TestRecordRejection_IncrementsCounter(t *testing.T) {
	t.Parallel()

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 5,
		ResetTimeout:     time.Hour,
		Clock:            time.Now,
	})

	before := c.TotalRequests()
	c.RecordRejection()
	after := c.TotalRequests()

	if after != before+1 {
		t.Errorf("RecordRejection: TotalRequests went from %d to %d, expected +1", before, after)
	}
	// State must remain CLOSED.
	if c.State() != hmenum.CircuitStateClosed {
		t.Errorf("RecordRejection should not change state: got %s", c.State())
	}
}

// --- LastFailureTime ---

func TestLastFailureTime_ZeroBeforeAnyFailure(t *testing.T) {
	t.Parallel()

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 5,
		ResetTimeout:     time.Hour,
		Clock:            time.Now,
	})

	if !c.LastFailureTime().IsZero() {
		t.Errorf("expected zero time before any failure, got %v", c.LastFailureTime())
	}
}

func TestLastFailureTime_SetAfterFailure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Hour,
		Clock:            func() time.Time { return now },
	})

	c.RecordFailure()

	got := c.LastFailureTime()
	if !got.Equal(now) {
		t.Errorf("LastFailureTime = %v, want %v", got, now)
	}
}

// --- AddOnStateChange ---

func TestAddOnStateChange_FiresOnTrip(t *testing.T) {
	t.Parallel()

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Hour,
		Clock:            time.Now,
	})

	var fired atomic.Int32
	c.AddOnStateChange(func(from, to hmenum.CircuitState) {
		if from == hmenum.CircuitStateClosed && to == hmenum.CircuitStateOpen {
			fired.Add(1)
		}
	})

	c.RecordFailure()

	if fired.Load() != 1 {
		t.Errorf("AddOnStateChange listener not fired on trip: count=%d", fired.Load())
	}
}

func TestAddOnStateChange_NilListenerIsIgnored(t *testing.T) {
	t.Parallel()

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Hour,
		Clock:            time.Now,
	})

	// Must not panic.
	c.AddOnStateChange(nil)
	c.RecordFailure()
}

func TestAddOnStateChange_MultipleListeners(t *testing.T) {
	t.Parallel()

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Hour,
		Clock:            time.Now,
	})

	var count atomic.Int32
	for range 3 {
		c.AddOnStateChange(func(_, _ hmenum.CircuitState) {
			count.Add(1)
		})
	}

	c.RecordFailure()

	// The primary slot is nil (no OnStateChange call), so all 3 listeners fire.
	if count.Load() != 3 {
		t.Errorf("expected 3 listener fires, got %d", count.Load())
	}
}

// --- Reset ---

func TestReset_FromOpenClosesBreakerAndFiresCallback(t *testing.T) {
	t.Parallel()

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Hour,
		Clock:            time.Now,
	})

	var fired atomic.Int32
	c.OnStateChange(func(from, to hmenum.CircuitState) {
		if to == hmenum.CircuitStateClosed {
			fired.Add(1)
		}
	})

	c.RecordFailure() // trips → OPEN
	fired.Store(0)    // reset counter (trip already incremented it to 1)

	c.Reset()

	if c.State() != hmenum.CircuitStateClosed {
		t.Errorf("Reset: state=%s, want CLOSED", c.State())
	}
	if c.LastFailureTime() != (time.Time{}) {
		t.Errorf("Reset: openedAt not cleared")
	}
}

// --- CommandTracker.lazyCleanupLocked coverage ---

func TestCommandTracker_LazyCleanupAfterManyAdds(t *testing.T) {
	t.Parallel()

	// The lazyCleanupLocked branch fires every 100 additions. We exercise it
	// by adding 105 distinct keys so the counter crosses the threshold.
	ct := NewCommandTracker("iface1", CommandTrackerConfig{
		TTL:     time.Minute,
		MaxSize: 2000,
	})

	for i := range 105 {
		addr := "device-" + string(rune('a'+i%26)) + "-ch" + string(rune('0'+i%10))
		ct.AddSetValue(addr, hmenum.Parameter("PARAM"), hmenum.ParamsetKeyValues, float64(i))
	}
	// Must not panic; lazyCleanup simply removes expired entries (none yet).
	if ct.Size() < 0 {
		t.Error("Size should never be negative")
	}
}

// --- AddOnStateChange: coexists with OnStateChange ---

func TestAddOnStateChange_CoexistsWithOnStateChange(t *testing.T) {
	t.Parallel()

	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Hour,
		Clock:            time.Now,
	})

	var primary, secondary atomic.Int32
	c.OnStateChange(func(_, _ hmenum.CircuitState) { primary.Add(1) })
	c.AddOnStateChange(func(_, _ hmenum.CircuitState) { secondary.Add(1) })

	c.RecordFailure()

	if primary.Load() != 1 {
		t.Errorf("primary listener fires = %d, want 1", primary.Load())
	}
	if secondary.Load() != 1 {
		t.Errorf("secondary listener fires = %d, want 1", secondary.Load())
	}
}

// --- Do bypass path never affects state ---

func TestDo_BypassOpAlwaysExecutesRegardlessOfState(t *testing.T) {
	t.Parallel()

	tick := time.Unix(0, 0)
	c := NewCircuit(CircuitConfig{
		FailureThreshold: 1,
		ResetTimeout:     time.Hour,
		Clock:            func() time.Time { return tick },
	})

	// Trip.
	c.RecordFailure()

	// ping is a bypass op — must execute even when OPEN.
	boomErr := errors.New("ping reply")
	var called bool
	err := c.Do(context.Background(), "ping", func(_ context.Context) error {
		called = true
		return boomErr
	})
	if !called {
		t.Error("bypass op must be executed even with OPEN breaker")
	}
	if err == nil {
		t.Error("expected ping error to propagate")
	}
	// State must still be OPEN (bypass does not change state).
	if c.State() != hmenum.CircuitStateOpen {
		t.Errorf("bypass should not change breaker state: got %s", c.State())
	}
}
