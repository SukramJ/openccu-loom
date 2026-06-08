// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// TestHeartbeatJobRunsOnTick verifies that a job registered with Add is
// invoked when the fake-clock tick fires. This is the foundation for the
// heartbeat job — the scheduler's core tick contract.
func TestHeartbeatJobRunsOnTick(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)
	s := New(nil, fake)

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

	// Fire one interval tick deterministically: wait for the job to park
	// on its timer, fire it, then wait for the invocation to complete.
	advanceTick(t, fake, 60*time.Second)

	if count.Load() < 1 {
		t.Fatalf("heartbeat job did not run after tick; count=%d", count.Load())
	}
}

// TestHeartbeatJobRunOnStartInvokesBeforeFirstTick verifies that
// RunOnStart=true causes the job to fire immediately on Start, before any
// clock tick. This allows a heartbeat check at daemon boot time.
func TestHeartbeatJobRunOnStartInvokesBeforeFirstTick(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)
	s := New(nil, fake)

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
	select {
	case <-ran:
		// OK
	case <-time.After(500 * time.Millisecond):
		t.Fatal("RunOnStart job did not run immediately after Start")
	}
}

// TestHeartbeatJobErrorDoesNotStopScheduler verifies that the heartbeat
// job returning an error does not abort the scheduler. The heartbeat may
// transiently fail during startup (e.g. no clients registered yet) and
// the scheduler must keep ticking so subsequent intervals retry normally.
func TestHeartbeatJobErrorDoesNotStopScheduler(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)
	s := New(nil, fake)

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
		advanceTick(t, fake, 100*time.Millisecond)
	}
	if invocations.Load() < 3 {
		t.Fatalf("scheduler stopped after job error: invocations=%d want >=3", invocations.Load())
	}
}

// TestHeartbeatJobMultipleIntervalsFire verifies that the heartbeat job
// fires on every interval cycle, producing exactly N events for N ticks.
// This is the key property for the 60-second liveness check.
func TestHeartbeatJobMultipleIntervalsFire(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)
	s := New(nil, fake)

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
		advanceTick(t, fake, 60*time.Second)
	}
	s.Stop()

	if got := count.Load(); got != ticks {
		t.Fatalf("expected %d heartbeat firings, got %d", ticks, got)
	}
}

// waitForPending polls until the fake clock has at least want timers
// parked — i.e. the run loop has (re-)registered its interval timer
// after the previous invocation returned — or a generous deadline
// elapses. Synchronising on the pending-timer count instead of a fixed
// real-time sleep keeps these tests deterministic on slow or loaded CI
// runners, where the scheduler goroutine can otherwise miss a tick
// window between two Advance calls.
func waitForPending(t *testing.T, fake *clock.Fake, want int) {
	t.Helper()
	const deadline = 2 * time.Second
	start := time.Now()
	for time.Since(start) < deadline {
		if fake.PendingCount() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("scheduler did not park %d interval timer(s) within %s", want, deadline)
}

// advanceTick fires exactly one interval tick without racing the run
// loop: it waits for the job to park on its timer, fires that timer,
// then waits for the resulting invocation to finish (the loop
// re-registers a fresh timer only after invoke returns). Counter
// inspection after advanceTick is therefore race-free.
func advanceTick(t *testing.T, fake *clock.Fake, interval time.Duration) {
	t.Helper()
	waitForPending(t, fake, 1)
	fake.Advance(interval)
	waitForPending(t, fake, 1)
}

// waitForCount polls until the counter reaches at least want, or a
// generous deadline elapses. Used where a job blocks inside Run (so the
// run loop cannot re-arm its timer) and advanceTick's re-arm wait would
// therefore deadlock — there the test synchronises on the invocation
// count instead.
func waitForCount(t *testing.T, c *atomic.Int64, want int64) {
	t.Helper()
	const deadline = 2 * time.Second
	start := time.Now()
	for time.Since(start) < deadline {
		if c.Load() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("counter did not reach %d within %s (got %d)", want, deadline, c.Load())
}
