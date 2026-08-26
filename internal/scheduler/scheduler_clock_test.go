// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

	ctx := t.Context()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Advance 5 × 100 ms of virtual time. advanceTick synchronises on the
	// pending-timer count so the goroutine has always re-registered its
	// timer before the next fire — no tick can be dropped.
	const steps = 5
	for range steps {
		advanceTick(t, fake, 100*time.Millisecond)
	}
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

	ctx := t.Context()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Do not advance the clock at all — no ticks should fire. Wait until
	// the goroutine has parked its (1-hour) timer, proving it reached the
	// select; since we never advance, that timer can never fire.
	waitForPending(t, fake, 1)
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

	ctx := t.Context()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Fire the first tick and wait until the job is actually running —
	// it then blocks on the barrier, so no new timer is armed.
	waitForPending(t, fake, 1)
	fake.Advance(100 * time.Millisecond)
	waitForCount(t, &calls, 1)

	// Advance the clock by many more intervals while the job is still running.
	// With pile-up behaviour the timer channel would buffer ticks; with the
	// recurring-NewTimer pattern no timers are registered during the run.
	fake.Advance(500 * time.Millisecond)

	// Release the blocked job; the loop returns and re-registers a timer.
	close(release)
	waitForPending(t, fake, 1)

	// Only one invocation should have happened (the first tick). After
	// release the goroutine re-registers a timer; it must not immediately
	// re-fire the accumulated advances.
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

	ctx := t.Context()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Advance the clock step by step. Wait for all three goroutines to
	// park their timers before each advance (and once more at the end so
	// the final invocations have completed), so every job sees every tick.
	for range steps {
		waitForPending(t, fake, numJobs)
		fake.Advance(100 * time.Millisecond)
	}
	waitForPending(t, fake, numJobs)
	s.Stop()

	for i := range numJobs {
		if got := counters[i].Load(); got != steps {
			t.Errorf("job %d: expected %d calls, got %d", i, steps, got)
		}
	}
}
