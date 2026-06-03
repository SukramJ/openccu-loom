// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// failures_test.go covers the per-job and aggregate failure counters
// exposed by Scheduler.JobFailures and Scheduler.TotalFailures.
//
// Synchronisation with the scheduler goroutines is driven off the fake
// clock's pending-timer count rather than fixed real-time sleeps: the
// run loop re-registers its interval timer only after an invocation
// returns (by which point the failure counter is already updated), so a
// pending timer is a race-free signal that the prior invocation
// completed. This keeps the assertions deterministic on slow or loaded
// CI runners.
package scheduler_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/scheduler"
)

// waitUntil polls cond every millisecond until it holds or a generous
// deadline elapses, failing the test on timeout. It replaces fixed
// real-time sleeps so a slow runner cannot flake the result.
func waitUntil(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	const deadline = 2 * time.Second
	start := time.Now()
	for time.Since(start) < deadline {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s (waited %s)", msg, deadline)
}

// syncReady blocks until the scheduler has at least want interval timers
// parked — i.e. every RunOnStart / prior invocation has returned and the
// run loop is waiting on NewTimer. After this returns the corresponding
// failure counters are settled.
func syncReady(t *testing.T, fake *clock.Fake, want int) {
	t.Helper()
	waitUntil(t, func() bool { return fake.PendingCount() >= want },
		"scheduler did not register expected interval timers")
}

// advanceTick waits for the running job to park on its interval timer,
// fires that timer, then waits for the resulting invocation to finish
// (the loop re-registers a fresh timer only after invoke returns).
// Counter inspection after advanceTick is therefore race-free.
func advanceTick(t *testing.T, fake *clock.Fake, interval time.Duration) {
	t.Helper()
	syncReady(t, fake, 1)
	fake.Advance(interval)
	syncReady(t, fake, 1)
}

// TestJobFailures_SuccessfulJobDoesNotIncrement verifies that a job whose
// Run returns nil never increments either counter.
func TestJobFailures_SuccessfulJobDoesNotIncrement(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	s := scheduler.New(nil, fake)

	_ = s.Add(scheduler.Job{
		Name:       "ok",
		Interval:   100 * time.Millisecond,
		RunOnStart: true,
		Run:        func(context.Context) error { return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// RunOnStart fires immediately; wait for it to finish.
	syncReady(t, fake, 1)

	// Trigger one additional tick.
	advanceTick(t, fake, 100*time.Millisecond)

	s.Stop()

	if got := s.JobFailures("ok"); got != 0 {
		t.Errorf("JobFailures(%q) = %d, want 0", "ok", got)
	}
	if got := s.TotalFailures(); got != 0 {
		t.Errorf("TotalFailures() = %d, want 0", got)
	}
}

// TestJobFailures_ErrorJobIncrementsPerTick verifies that each Run returning a
// non-nil error increments the per-job counter by one.
func TestJobFailures_ErrorJobIncrementsPerTick(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	s := scheduler.New(nil, fake)

	_ = s.Add(scheduler.Job{
		Name:       "bad",
		Interval:   100 * time.Millisecond,
		RunOnStart: true,
		Run:        func(context.Context) error { return errors.New("injected error") },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// RunOnStart fires immediately; wait for completion → counter = 1.
	syncReady(t, fake, 1)

	if got := s.JobFailures("bad"); got != 1 {
		t.Errorf("after first run: JobFailures(%q) = %d, want 1", "bad", got)
	}

	// Trigger a second tick → counter = 2.
	advanceTick(t, fake, 100*time.Millisecond)

	if got := s.JobFailures("bad"); got != 2 {
		t.Errorf("after second run: JobFailures(%q) = %d, want 2", "bad", got)
	}
}

// TestJobFailures_PanicIncrementsAndGoroutineSurvives verifies that a
// panicking job is recovered (the goroutine stays alive), the failure counter
// is incremented, and the job continues to run on subsequent ticks.
func TestJobFailures_PanicIncrementsAndGoroutineSurvives(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	s := scheduler.New(nil, fake)

	var calls atomic.Int64
	_ = s.Add(scheduler.Job{
		Name:       "panicker",
		Interval:   100 * time.Millisecond,
		RunOnStart: true,
		Run: func(context.Context) error {
			calls.Add(1)
			panic("boom")
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// RunOnStart fires immediately; wait for recovery → counter = 1.
	syncReady(t, fake, 1)

	if got := s.JobFailures("panicker"); got < 1 {
		t.Errorf("after first panic: JobFailures(%q) = %d, want >= 1", "panicker", got)
	}

	// Trigger a second tick — verifies the goroutine survived the panic.
	advanceTick(t, fake, 100*time.Millisecond)

	if got := s.JobFailures("panicker"); got < 2 {
		t.Errorf("after second panic: JobFailures(%q) = %d, want >= 2", "panicker", got)
	}
	if calls.Load() < 2 {
		t.Errorf("job was only called %d time(s); goroutine appears to have died", calls.Load())
	}
}

// TestJobFailures_MultipleJobsSeparateCounters verifies that failures from one
// job do not bleed into another job's counter, and that TotalFailures is the
// exact sum of per-job counters.
func TestJobFailures_MultipleJobsSeparateCounters(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	s := scheduler.New(nil, fake)

	_ = s.Add(scheduler.Job{
		Name:       "good",
		Interval:   100 * time.Millisecond,
		RunOnStart: true,
		Run:        func(context.Context) error { return nil },
	})
	_ = s.Add(scheduler.Job{
		Name:       "failing",
		Interval:   100 * time.Millisecond,
		RunOnStart: true,
		Run:        func(context.Context) error { return errors.New("always fails") },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// RunOnStart fires for both jobs immediately; wait for both to park.
	syncReady(t, fake, 2)

	s.Stop()

	goodFail := s.JobFailures("good")
	failingFail := s.JobFailures("failing")

	if goodFail != 0 {
		t.Errorf("JobFailures(%q) = %d, want 0", "good", goodFail)
	}
	if failingFail < 1 {
		t.Errorf("JobFailures(%q) = %d, want >= 1", "failing", failingFail)
	}
	if got, want := s.TotalFailures(), failingFail; got != want {
		t.Errorf("TotalFailures() = %d, want %d (= JobFailures(%q))", got, want, "failing")
	}
}

// TestJobFailures_UnknownJobReturnsZero verifies that querying a job name that
// was never registered returns 0 without panicking.
func TestJobFailures_UnknownJobReturnsZero(t *testing.T) {
	t.Parallel()

	s := scheduler.New(nil, nil)

	if got := s.JobFailures("does-not-exist"); got != 0 {
		t.Errorf("JobFailures on unknown job = %d, want 0", got)
	}
}

// TestJobFailures_CtxCancelDoesNotPanic verifies that cancelling the context
// immediately after starting the scheduler does not cause any panic. When a
// job's error is a context error the scheduler suppresses the failure log and
// counter increment, so the exact counter value is indeterminate — we only
// assert stability (no panic, no race).
func TestJobFailures_CtxCancelDoesNotPanic(t *testing.T) {
	t.Parallel()

	s := scheduler.New(nil, nil)

	_ = s.Add(scheduler.Job{
		Name:       "ctx-aware",
		Interval:   10 * time.Millisecond,
		RunOnStart: true,
		Run: func(ctx context.Context) error {
			return ctx.Err()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Cancel immediately — the job may or may not have run yet.
	cancel()
	s.Stop()

	// Just verify the call does not panic.
	_ = s.JobFailures("ctx-aware")
	_ = s.TotalFailures()
}
