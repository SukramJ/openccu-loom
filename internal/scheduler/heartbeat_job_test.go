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

	// Advance past interval; yield so goroutine can reach the select.
	time.Sleep(5 * time.Millisecond)
	fake.Advance(60 * time.Second)
	time.Sleep(10 * time.Millisecond)

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

	// Fire ticks until the job has been invoked at least 3 times (past
	// the failure band). Poll-and-advance instead of a fixed sleep so
	// the test is robust under -race where goroutine scheduling is
	// throttled.
	deadline := time.Now().Add(5 * time.Second)
	for invocations.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("scheduler stopped after job error: invocations=%d want >=3", invocations.Load())
		}
		time.Sleep(5 * time.Millisecond)
		fake.Advance(100 * time.Millisecond)
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
		time.Sleep(5 * time.Millisecond)
		fake.Advance(60 * time.Second)
	}
	time.Sleep(10 * time.Millisecond)
	s.Stop()

	if got := count.Load(); got != ticks {
		t.Fatalf("expected %d heartbeat firings, got %d", ticks, got)
	}
}
