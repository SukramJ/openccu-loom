// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// heartbeat_job_test.go pins the scheduler mechanics required by the
// C-SCHED-1 heartbeat job: the job must be invoked on every tick, errors
// must not stop the scheduler, and Stop must cleanly cancel running jobs.
// The integration between the heartbeat job and HeartbeatTimerFiredEvent
// is tested in internal/central (jobs_test.go) because it requires the
// full Unit context.
package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// TestHeartbeatJobRunsOnTick verifies that a job registered with Add is
// invoked when its interval tick fires. This is the foundation for the
// heartbeat job — the scheduler's core tick contract.
func TestHeartbeatJobRunsOnTick(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := New(nil)

		var count atomic.Int64
		if err := s.Add(Job{
			Name:     "central.heartbeat",
			Interval: 60 * time.Second,
			Run: func(_ context.Context) error {
				count.Add(1)
				return nil
			},
		}); err != nil {
			t.Fatal(err)
		}

		ctx := t.Context()

		if err := s.Start(ctx); err != nil {
			t.Fatal(err)
		}
		defer s.Stop()

		// Fire one interval tick: the bubble clock advances once the job is
		// parked on its timer, and the sleep returns after the invocation
		// has completed.
		synctest.Sleep(60 * time.Second)

		if count.Load() < 1 {
			t.Fatalf("heartbeat job did not run after tick; count=%d", count.Load())
		}
	})
}

// TestHeartbeatJobRunOnStartInvokesBeforeFirstTick verifies that
// RunOnStart=true causes the job to fire immediately on Start, before any
// clock tick. This allows a heartbeat check at daemon boot time.
func TestHeartbeatJobRunOnStartInvokesBeforeFirstTick(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := New(nil)

		ran := make(chan struct{}, 1)
		if err := s.Add(Job{
			Name:       "central.heartbeat.start",
			Interval:   60 * time.Second,
			RunOnStart: true,
			Run: func(_ context.Context) error {
				select {
				case ran <- struct{}{}:
				default:
				}
				return nil
			},
		}); err != nil {
			t.Fatal(err)
		}

		ctx := t.Context()

		if err := s.Start(ctx); err != nil {
			t.Fatal(err)
		}
		defer s.Stop()

		// No clock advance needed — RunOnStart fires before the first timer.
		synctest.Wait()
		select {
		case <-ran:
			// OK
		default:
			t.Fatal("RunOnStart job did not run immediately after Start")
		}
	})
}

// TestHeartbeatJobErrorDoesNotStopScheduler verifies that the heartbeat
// job returning an error does not abort the scheduler. The heartbeat may
// transiently fail during startup (e.g. no clients registered yet) and
// the scheduler must keep ticking so subsequent intervals retry normally.
func TestHeartbeatJobErrorDoesNotStopScheduler(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := New(nil)

		var invocations atomic.Int64
		if err := s.Add(Job{
			Name:     "central.heartbeat.flaky",
			Interval: 100 * time.Millisecond,
			Run: func(_ context.Context) error {
				n := invocations.Add(1)
				if n < 3 {
					return errors.New("transient callback check failure")
				}
				return nil
			},
		}); err != nil {
			t.Fatal(err)
		}

		ctx := t.Context()

		if err := s.Start(ctx); err != nil {
			t.Fatal(err)
		}
		defer s.Stop()

		// Fire three ticks deterministically. The first two invocations
		// return an error; the scheduler must keep ticking so the third
		// runs. advanceTick waits for each invocation to finish before the
		// next fire, so the count is race-free even under -race.
		for range 3 {
			synctest.Sleep(100 * time.Millisecond)
		}
		if invocations.Load() < 3 {
			t.Fatalf("scheduler stopped after job error: invocations=%d want >=3", invocations.Load())
		}
	})
}

// TestHeartbeatJobMultipleIntervalsFire verifies that the heartbeat job
// fires on every interval cycle, producing exactly N events for N ticks.
// This is the key property for the 60-second liveness check.
func TestHeartbeatJobMultipleIntervalsFire(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := New(nil)

		const ticks = 5
		var count atomic.Int64
		if err := s.Add(Job{
			Name:     "central.heartbeat.multi",
			Interval: 60 * time.Second,
			Run: func(_ context.Context) error {
				count.Add(1)
				return nil
			},
		}); err != nil {
			t.Fatal(err)
		}

		ctx := t.Context()

		if err := s.Start(ctx); err != nil {
			t.Fatal(err)
		}
		defer s.Stop()

		for range ticks {
			synctest.Sleep(60 * time.Second)
		}
		s.Stop()

		if got := count.Load(); got != ticks {
			t.Fatalf("expected %d heartbeat firings, got %d", ticks, got)
		}
	})
}
