// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// TestSchedulerStartStopLifecycle
//
// Verifies that jobs execute repeatedly while running and stop accumulating
// after Stop is called. Uses real-time short intervals (5 ms) because the
// production Scheduler uses time.NewTicker internally rather than a clock
// interface.
// ---------------------------------------------------------------------------

func TestSchedulerStartStopLifecycle(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)
	var counter atomic.Int64

	if err := s.Add(Job{
		Name:       "tick",
		Interval:   5 * time.Millisecond,
		RunOnStart: true,
		Run:        func(context.Context) error { counter.Add(1); return nil },
	}); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	if err := s.Start(ctx); err != nil {
		t.Fatal("Start:", err)
	}

	// Let several ticks fire.
	time.Sleep(40 * time.Millisecond)
	s.Stop()

	snapshot := counter.Load()
	if snapshot < 2 {
		t.Fatalf("expected ≥ 2 invocations before Stop, got %d", snapshot)
	}

	// After Stop, the counter must stay frozen.
	time.Sleep(30 * time.Millisecond)
	if counter.Load() != snapshot {
		t.Fatalf("counter changed after Stop: was %d, now %d", snapshot, counter.Load())
	}
}

// ---------------------------------------------------------------------------
// TestSchedulerJobErrorDoesNotCrashScheduler
//
// A job that always returns an error must not crash the scheduler. The
// scheduler must keep calling the job and keep running.
// ---------------------------------------------------------------------------

func TestSchedulerJobErrorDoesNotCrashScheduler(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)
	boom := errors.New("boom")
	var calls atomic.Int32

	_ = s.Add(Job{
		Name:       "bad",
		Interval:   5 * time.Millisecond,
		RunOnStart: true,
		Run: func(context.Context) error {
			calls.Add(1)
			return boom
		},
	})

	ctx := t.Context()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	s.Stop()

	// Scheduler must have kept invoking the failing job.
	if calls.Load() < 2 {
		t.Fatalf("scheduler stopped after error; calls=%d", calls.Load())
	}
}

// ---------------------------------------------------------------------------
// TestSchedulerMultipleJobsRunConcurrently
//
// Three jobs, each with RunOnStart, all block on a shared rendezvous
// channel. If the scheduler runs jobs concurrently all three can unblock
// together; if it were sequential the second job would deadlock waiting for
// the first to finish.
// ---------------------------------------------------------------------------

func TestSchedulerMultipleJobsRunConcurrently(t *testing.T) {
	t.Parallel()

	const n = 3
	// Each job sends to 'entered' when it starts, then waits on 'release'.
	entered := make(chan struct{}, n)
	release := make(chan struct{})

	jobFn := func(ctx context.Context) error {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}

	s := New(nil, nil)
	for i := range n {
		name := "job" + string(rune('a'+i))
		_ = s.Add(Job{
			Name:       name,
			Interval:   time.Minute, // only RunOnStart fires within the test
			RunOnStart: true,
			Run:        jobFn,
		})
	}

	ctx := t.Context()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Collect n entries within deadline; if sequential one would never arrive
	// because the first blocks the goroutine.
	deadline := time.After(500 * time.Millisecond)
	for range n {
		select {
		case <-entered:
		case <-deadline:
			t.Fatal("jobs did not run concurrently: timed out waiting for all to enter")
		}
	}

	// All three are in-flight concurrently; release them.
	close(release)
}

// ---------------------------------------------------------------------------
// TestSchedulerOverrunDoesNotPileUp
//
// Job duration (30 ms) > interval (10 ms). The ticker-based scheduler
// fires the job every interval regardless, so across 200 ms we expect
// roughly interval-limited (not queue-limited) invocations. The important
// invariant is that invocations stay bounded — i.e. the scheduler does not
// queue up a backlog of skipped ticks.
//
// The production scheduler uses time.NewTicker which *drops* ticks that
// fire while the previous one is still pending in the select, so the job
// runs no more than ⌈elapsed / duration⌉ times. We allow generous slack
// because CI runners may be slow.
// ---------------------------------------------------------------------------

func TestSchedulerOverrunDoesNotPileUp(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	s := New(nil, nil)
	_ = s.Add(Job{
		Name:     "slow",
		Interval: 10 * time.Millisecond,
		Run: func(ctx context.Context) error {
			calls.Add(1)
			// Job takes 30 ms — longer than the 10 ms interval.
			select {
			case <-ctx.Done():
			case <-time.After(30 * time.Millisecond):
			}
			return nil
		},
	})

	ctx := t.Context()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	s.Stop()

	got := calls.Load()
	// With 200 ms elapsed and 30 ms per job the theoretical sequential max is ~6.
	// Allow up to 10 for scheduling jitter, but NOT 20 (which would indicate backlog
	// queuing).
	if got > 10 {
		t.Fatalf("invocations=%d; looks like the scheduler is queuing overrun ticks", got)
	}
	if got < 1 {
		t.Fatal("job never ran")
	}
}

// ---------------------------------------------------------------------------
// TestSchedulerStopReturnsPromptly
//
// Stop must unblock well before the next scheduled tick.
// ---------------------------------------------------------------------------

func TestSchedulerStopReturnsPromptly(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)
	_ = s.Add(Job{
		Name:     "long-interval",
		Interval: 10 * time.Second,
		Run:      func(context.Context) error { return nil },
	})

	ctx := t.Context()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Let the goroutine enter its select loop.
	time.Sleep(5 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stop did not return within 500 ms")
	}
}

// ---------------------------------------------------------------------------
// TestSchedulerInstallModeDefaultInterval
//
// Verifies that RegisterStandardJobs wires the install-mode slot with the
// correct default interval (30 s) — mirrors the Python INTERVAL_INSTALL_MODE.
// The test lives in the scheduler package through the central/jobs linkage;
// here we only verify the scheduler Add path honours a 30 s interval.
// ---------------------------------------------------------------------------

func TestSchedulerAcceptsInstallModeInterval(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)
	err := s.Add(Job{
		Name:     "hub.install_mode_refresh",
		Interval: 30 * time.Second,
		Run:      func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("Add hub.install_mode_refresh: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestSchedulerRunOnStartFiresBeforeFirstTick
//
// When RunOnStart is true the job must execute once immediately after Start,
// before the first Interval tick.
// ---------------------------------------------------------------------------

func TestSchedulerRunOnStartFiresBeforeFirstTick(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)
	var calls atomic.Int32

	// Very long interval so the ticker never fires during the test window.
	_ = s.Add(Job{
		Name:       "runtstart",
		Interval:   10 * time.Second,
		RunOnStart: true,
		Run:        func(context.Context) error { calls.Add(1); return nil },
	})

	ctx := t.Context()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// Give the goroutine a moment to invoke the startup run.
	time.Sleep(20 * time.Millisecond)
	s.Stop()

	if calls.Load() != 1 {
		t.Fatalf("RunOnStart: expected exactly 1 call, got %d", calls.Load())
	}
}

// ---------------------------------------------------------------------------
// TestSchedulerAddAfterStartLaunchesJob
//
// Calling Add after Start must succeed and launch the job immediately on the
// running context. Components that wire up post-connect rely on this.
// ---------------------------------------------------------------------------

func TestSchedulerAddAfterStartLaunchesJob(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)
	_ = s.Add(Job{
		Name:     "first",
		Interval: time.Minute,
		Run:      func(context.Context) error { return nil },
	})

	ctx := t.Context()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	var ran atomic.Int32
	err := s.Add(Job{
		Name:       "late",
		Interval:   10 * time.Millisecond,
		RunOnStart: true,
		Run:        func(context.Context) error { ran.Add(1); return nil },
	})
	if err != nil {
		t.Fatalf("Add after Start returned unexpected error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	s.Stop()
	if ran.Load() < 1 {
		t.Fatalf("late job ran %d times, want ≥ 1", ran.Load())
	}
}

// ---------------------------------------------------------------------------
// TestSchedulerContextCancelStopsAllJobs
//
// Cancelling the parent context (rather than calling Stop) must cause all
// job goroutines to exit.
// ---------------------------------------------------------------------------

func TestSchedulerContextCancelStopsAllJobs(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)
	for i := range 5 {
		name := "job" + string(rune('a'+i))
		_ = s.Add(Job{
			Name:     name,
			Interval: 5 * time.Millisecond,
			Run:      func(context.Context) error { return nil },
		})
	}

	ctx, cancel := context.WithCancel(context.Background())

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	time.Sleep(20 * time.Millisecond)
	cancel()

	// Stop must return quickly because the ctx cancel already drove
	// all goroutines to exit.
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stop hung after context cancel")
	}
}

// ---------------------------------------------------------------------------
// TestSchedulerConcurrentOperationsRaceFree
//
// Fan-out: several goroutines call Stop concurrently while new goroutines
// try to call Add — verifies no data race under -race.
// ---------------------------------------------------------------------------

func TestSchedulerConcurrentOperationsRaceFree(t *testing.T) {
	t.Parallel()

	for range 5 {
		s := New(nil, nil)
		_ = s.Add(Job{
			Name:     "seed",
			Interval: 5 * time.Millisecond,
			Run:      func(context.Context) error { return nil },
		})

		ctx, cancel := context.WithCancel(context.Background())
		_ = s.Start(ctx)

		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				// These should all return error (started), no race.
				_ = s.Add(Job{
					Name:     "late",
					Interval: time.Minute,
					Run:      func(context.Context) error { return nil },
				})
			})
		}
		for range 3 {
			wg.Go(func() {
				s.Stop()
			})
		}
		cancel()
		wg.Wait()
	}
}
