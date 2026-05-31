// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// P1-2 follow-up: Scheduler honours the injected clock. Tests can
// advance virtual time instead of sleeping — the entire tick cycle
// completes in microseconds.

package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// TestSchedulerDefaultClockIsReal verifies that New with a nil clk argument
// wires the production wall clock, not a fake.
func TestSchedulerDefaultClockIsReal(t *testing.T) {
	t.Parallel()
	s := New(nil, nil)
	if s.clk == nil {
		t.Fatal("expected clk to be non-nil after construction")
	}
	if _, ok := s.clk.(clock.Real); !ok {
		t.Fatalf("expected clock.Real, got %T", s.clk)
	}
}

// TestSchedulerFakeClockAdvancesTicksDeterministically verifies that advancing
// the fake clock by N × interval fires the job exactly N times without any
// real-time sleep.
func TestSchedulerFakeClockAdvancesTicksDeterministically(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)

	s := New(nil, fake)
	var calls atomic.Int64
	_ = s.Add(Job{
		Name:     "tick",
		Interval: 100 * time.Millisecond,
		Run:      func(context.Context) error { calls.Add(1); return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Advance 5 × 100 ms = 500 ms of virtual time. Each advance fires the
	// pending timer, then the goroutine re-registers a new one. We yield
	// briefly between steps so the scheduler goroutine has a chance to call
	// NewTimer again before the next Advance.
	const steps = 5
	for range steps {
		// Let the goroutine reach its next NewTimer call.
		time.Sleep(5 * time.Millisecond)
		fake.Advance(100 * time.Millisecond)
	}

	// Allow the last invocation to finish.
	time.Sleep(10 * time.Millisecond)
	s.Stop()

	got := calls.Load()
	if got != steps {
		t.Fatalf("expected exactly %d calls, got %d", steps, got)
	}
}

// TestSchedulerFakeClockStopBeforeFirstTick verifies that a job with a very
// long interval never runs when Stop is called before any tick fires.
func TestSchedulerFakeClockStopBeforeFirstTick(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)

	s := New(nil, fake)
	var calls atomic.Int64
	_ = s.Add(Job{
		Name:     "never",
		Interval: time.Hour,
		Run:      func(context.Context) error { calls.Add(1); return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Do not advance the clock at all — no ticks should fire.
	// Give the goroutine a moment to enter its select.
	time.Sleep(5 * time.Millisecond)
	s.Stop()

	if calls.Load() != 0 {
		t.Fatalf("expected 0 calls before any tick, got %d", calls.Load())
	}
}

// TestSchedulerOverrunWithFakeClock tests the "no pile-up" property
// deterministically: a slow job (that holds a barrier until the test
// advances the clock further) must not accumulate backlog ticks.
//
// The recurring-NewTimer pattern naturally prevents pile-up: a new timer
// is only created after the previous invocation returns, so overrun ticks
// are silently dropped — unlike time.Ticker which buffers one tick.
func TestSchedulerOverrunWithFakeClock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)

	s := New(nil, fake)
	var calls atomic.Int64

	// Barrier: the job blocks until the test releases it.
	release := make(chan struct{})

	_ = s.Add(Job{
		Name:     "slow",
		Interval: 100 * time.Millisecond,
		Run: func(ctx context.Context) error {
			calls.Add(1)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Fire the first tick — goroutine is now blocked in the job.
	time.Sleep(5 * time.Millisecond)
	fake.Advance(100 * time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	// Advance the clock by many more intervals while the job is still running.
	// With pile-up behaviour the timer channel would buffer ticks; with the
	// recurring-NewTimer pattern no timers are registered during the run.
	fake.Advance(500 * time.Millisecond)

	// Release the blocked job.
	close(release)
	time.Sleep(10 * time.Millisecond)

	// Only one invocation should have happened (the first tick).
	// After release the goroutine re-registers a timer; it should not
	// immediately re-fire the accumulated advances.
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 call (no pile-up), got %d", got)
	}
}

// TestSchedulerMultipleJobsAdvanceTogether verifies that three jobs all
// accumulate ticks when the clock is advanced repeatedly. Each job runs
// independently on its own goroutine.
func TestSchedulerMultipleJobsAdvanceTogether(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)

	s := New(nil, fake)

	const numJobs = 3
	const steps = 3
	counters := make([]atomic.Int64, numJobs)

	for i := range numJobs {
		idx := i
		_ = s.Add(Job{
			Name:     "job" + string(rune('a'+idx)),
			Interval: 100 * time.Millisecond,
			Run:      func(context.Context) error { counters[idx].Add(1); return nil },
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Advance the clock step by step. Between steps we yield so all three
	// goroutines can re-register their timers before the next advance.
	for range steps {
		time.Sleep(5 * time.Millisecond)
		fake.Advance(100 * time.Millisecond)
	}

	// Allow the final invocations to complete.
	time.Sleep(10 * time.Millisecond)
	s.Stop()

	for i := range numJobs {
		if got := counters[i].Load(); got != steps {
			t.Errorf("job %d: expected %d calls, got %d", i, steps, got)
		}
	}
}
