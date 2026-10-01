// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tick-cycle tests for the scheduler. Every test runs inside a synctest
// bubble, so virtual time advances only once every goroutine is blocked
// and a whole tick cycle completes without any real-time sleep.

package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// TestSchedulerAdvancingTimeFiresTicksDeterministically verifies that
// advancing time by N × interval fires the job exactly N times.
func TestSchedulerAdvancingTimeFiresTicksDeterministically(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := New(nil)
		var calls atomic.Int64
		_ = s.Add(Job{
			Name:     "tick",
			Interval: 100 * time.Millisecond,
			Run:      func(context.Context) error { calls.Add(1); return nil },
		})

		if err := s.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		defer s.Stop()

		// synctest.Sleep returns only after the job goroutine has run
		// and re-registered its timer, so no tick can be dropped.
		const steps = 5
		for range steps {
			synctest.Sleep(100 * time.Millisecond)
		}
		s.Stop()

		got := calls.Load()
		if got != steps {
			t.Fatalf("expected exactly %d calls, got %d", steps, got)
		}
	})
}

// TestSchedulerStopBeforeFirstTick verifies that a job with a very long
// interval never runs when Stop is called before any tick fires.
func TestSchedulerStopBeforeFirstTick(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := New(nil)
		var calls atomic.Int64
		_ = s.Add(Job{
			Name:     "never",
			Interval: time.Hour,
			Run:      func(context.Context) error { calls.Add(1); return nil },
		})

		if err := s.Start(t.Context()); err != nil {
			t.Fatal(err)
		}

		// Time is not advanced — no tick should fire. Once every goroutine
		// is blocked the job has parked its (1-hour) timer, proving it
		// reached the select; it can never fire because time stands still.
		synctest.Wait()
		s.Stop()

		if calls.Load() != 0 {
			t.Fatalf("expected 0 calls before any tick, got %d", calls.Load())
		}
	})
}

// TestSchedulerOverrunDropsMissedTicks tests the "no pile-up" property
// deterministically: a slow job (that holds a barrier until the test
// advances time further) must not accumulate backlog ticks.
//
// The recurring-NewTimer pattern naturally prevents pile-up: a new timer
// is only created after the previous invocation returns, so overrun ticks
// are silently dropped — unlike time.Ticker which buffers one tick.
func TestSchedulerOverrunDropsMissedTicks(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := New(nil)
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

		if err := s.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		defer s.Stop()

		// Fire the first tick; the job then blocks on the barrier, so no
		// new timer is armed.
		synctest.Sleep(100 * time.Millisecond)
		if got := calls.Load(); got != 1 {
			t.Fatalf("expected 1 call after the first tick, got %d", got)
		}

		// Advance time by many more intervals while the job is still
		// running. With pile-up behaviour the timer channel would buffer
		// ticks; with the recurring-NewTimer pattern no timer exists
		// during the run.
		synctest.Sleep(500 * time.Millisecond)

		// Release the blocked job; the loop returns and re-registers a timer.
		close(release)
		synctest.Wait()

		// Only one invocation should have happened (the first tick). After
		// release the goroutine re-registers a timer; it must not immediately
		// re-fire the accumulated advances.
		if got := calls.Load(); got != 1 {
			t.Fatalf("expected 1 call (no pile-up), got %d", got)
		}
	})
}

// TestSchedulerMultipleJobsAdvanceTogether verifies that three jobs all
// accumulate ticks as time advances. Each job runs independently on its
// own goroutine.
func TestSchedulerMultipleJobsAdvanceTogether(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := New(nil)

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

		if err := s.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		defer s.Stop()

		// synctest.Sleep returns after every job has run and re-armed its
		// timer, so every job sees every tick.
		for range steps {
			synctest.Sleep(100 * time.Millisecond)
		}
		s.Stop()

		for i := range numJobs {
			if got := counters[i].Load(); got != steps {
				t.Errorf("job %d: expected %d calls, got %d", i, steps, got)
			}
		}
	})
}
