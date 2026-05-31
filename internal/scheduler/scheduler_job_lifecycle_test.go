// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// scheduler_job_lifecycle_test.go covers Scheduler and Job lifecycle
// scenarios not covered by scheduler_test.go, scheduler_deep_test.go,
// or scheduler_clock_test.go.
//
// Covered:
//   - Job validation: non-positive interval, empty name, nil Run
//   - Start activates jobs; second Start returns an error
//   - Stop deactivates jobs; Stop before Start is a no-op
//   - Jobs execute and continue after errors
//   - Multiple jobs run on independent schedules
//   - Multiple jobs with RunOnStart each fire exactly once
//   - OnStart / OnComplete hooks table-driven (success + error)
//   - Jobs snapshot inspectable before Start; Add after Start is rejected
package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// ---------------------------------------------------------------------------
// Job property tests
// ---------------------------------------------------------------------------

// TestJobIntervalIsPositive verifies that a job with a non-positive
// interval is rejected at registration time.
func TestJobIntervalIsPositive(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		interval time.Duration
		wantErr  bool
	}{
		{"zero", 0, true},
		{"negative", -time.Second, true},
		{"positive", time.Second, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := New(nil, nil)
			err := s.Add(Job{
				Name:     "j",
				Interval: tc.interval,
				Run:      func(context.Context) error { return nil },
			})
			if tc.wantErr && err == nil {
				t.Error("expected error for non-positive interval, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestJobNameRequired verifies that registering a nameless job
// returns an error.
func TestJobNameRequired(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)
	err := s.Add(Job{
		Name:     "",
		Interval: time.Second,
		Run:      func(context.Context) error { return nil },
	})
	if err == nil {
		t.Error("expected error for empty job name, got nil")
	}
}

// TestJobRunFuncRequired verifies that a job without a Run
// function is rejected.
func TestJobRunFuncRequired(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)
	err := s.Add(Job{
		Name:     "j",
		Interval: time.Second,
		Run:      nil,
	})
	if err == nil {
		t.Error("expected error for nil Run, got nil")
	}
}

// ---------------------------------------------------------------------------
// Scheduler start / stop lifecycle
// ---------------------------------------------------------------------------

// TestSchedulerStartActivates verifies that calling Start causes
// registered jobs to become active (i.e. they start executing).
func TestSchedulerStartActivates(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)
	var ran atomic.Bool
	_ = s.Add(Job{
		Name:       "activate",
		Interval:   time.Minute,
		RunOnStart: true,
		Run: func(context.Context) error {
			ran.Store(true)
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	deadline := time.After(500 * time.Millisecond)
	for !ran.Load() {
		select {
		case <-deadline:
			t.Fatal("job did not run after Start")
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// TestSchedulerStartWhenAlreadyRunning verifies that a second
// Start call returns an error.
func TestSchedulerStartWhenAlreadyRunning(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)
	_ = s.Add(Job{
		Name:     "j",
		Interval: time.Minute,
		Run:      func(context.Context) error { return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	if err := s.Start(ctx); err == nil {
		t.Error("second Start must return an error")
	}
}

// TestSchedulerStopDeactivates verifies that after Stop the
// scheduler no longer executes jobs.
func TestSchedulerStopDeactivates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)
	s := New(nil, fake)

	var calls atomic.Int64
	_ = s.Add(Job{
		Name:     "count",
		Interval: 100 * time.Millisecond,
		Run:      func(context.Context) error { calls.Add(1); return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Fire one tick.
	time.Sleep(5 * time.Millisecond)
	fake.Advance(100 * time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	s.Stop()
	snapshot := calls.Load()

	// Advance the clock further after Stop — no new calls should arrive.
	fake.Advance(500 * time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	if calls.Load() != snapshot {
		t.Errorf("calls changed after Stop: before=%d after=%d", snapshot, calls.Load())
	}
}

// TestSchedulerStopWhenNotRunning verifies that calling Stop on a
// scheduler that was never started is safe and does not panic.
func TestSchedulerStopWhenNotRunning(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)
	// Must not panic.
	s.Stop()
}

// ---------------------------------------------------------------------------
// Job-level execution semantics
// ---------------------------------------------------------------------------

// TestJobExecutesAndContinues verifies that a job runs and that
// the scheduler keeps scheduling subsequent ticks after each execution.
func TestJobExecutesAndContinues(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)
	s := New(nil, fake)

	const ticks = 3
	var count atomic.Int64
	_ = s.Add(Job{
		Name:     "exec",
		Interval: 50 * time.Millisecond,
		Run:      func(context.Context) error { count.Add(1); return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	for range ticks {
		time.Sleep(5 * time.Millisecond)
		fake.Advance(50 * time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)

	if got := count.Load(); got != ticks {
		t.Errorf("expected %d executions, got %d", ticks, got)
	}
}

// TestJobErrorDoesNotStopScheduling verifies that a job returning
// an error does not prevent subsequent ticks from firing.
func TestJobErrorDoesNotStopScheduling(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)
	s := New(nil, fake)

	boom := errors.New("task failed")
	var calls atomic.Int64
	_ = s.Add(Job{
		Name:     "fail",
		Interval: 50 * time.Millisecond,
		Run: func(context.Context) error {
			calls.Add(1)
			return boom
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	const ticks = 4
	for range ticks {
		time.Sleep(5 * time.Millisecond)
		fake.Advance(50 * time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)

	if got := calls.Load(); got < ticks {
		t.Errorf("scheduler stopped after error: got %d calls, want >= %d", got, ticks)
	}
}

// ---------------------------------------------------------------------------
// Multiple jobs with independent schedules
// ---------------------------------------------------------------------------

// TestMultipleJobsIndependentSchedules verifies that two jobs
// registered at the same time accumulate invocations independently
// across multiple ticks.
func TestMultipleJobsIndependentSchedules(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)
	s := New(nil, fake)

	var count1, count2 atomic.Int64
	_ = s.Add(Job{
		Name:     "job1",
		Interval: 50 * time.Millisecond,
		Run:      func(context.Context) error { count1.Add(1); return nil },
	})
	_ = s.Add(Job{
		Name:     "job2",
		Interval: 50 * time.Millisecond,
		Run:      func(context.Context) error { count2.Add(1); return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	const ticks = 3
	for range ticks {
		time.Sleep(5 * time.Millisecond)
		fake.Advance(50 * time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	s.Stop()

	if got := count1.Load(); got != ticks {
		t.Errorf("job1: expected %d calls, got %d", ticks, got)
	}
	if got := count2.Load(); got != ticks {
		t.Errorf("job2: expected %d calls, got %d", ticks, got)
	}
}

// TestMultipleJobsRunOnStart verifies that each job fires its
// RunOnStart invocation independently.
func TestMultipleJobsRunOnStart(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)

	const n = 3
	counters := make([]atomic.Int64, n)
	for i := range n {
		idx := i
		_ = s.Add(Job{
			Name:       "ros" + string(rune('a'+idx)),
			Interval:   time.Minute,
			RunOnStart: true,
			Run:        func(context.Context) error { counters[idx].Add(1); return nil },
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Allow all RunOnStart invocations to complete.
	time.Sleep(50 * time.Millisecond)
	s.Stop()

	for i := range n {
		if got := counters[i].Load(); got != 1 {
			t.Errorf("job %d: expected 1 RunOnStart call, got %d", i, got)
		}
	}
}

// ---------------------------------------------------------------------------
// OnStart / OnComplete hooks
// ---------------------------------------------------------------------------

// TestHooksTableDriven drives the OnStart / OnComplete contract
// across success and error job variants, extending scheduler_test.go
// coverage with a table-driven approach.
func TestHooksTableDriven(t *testing.T) {
	t.Parallel()

	type tc struct {
		name   string
		jobErr error
		wantOK bool // expected success flag in OnComplete
	}

	cases := []tc{
		{"success", nil, true},
		{"error", errors.New("boom"), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			fake := clock.NewFake(now)
			s := New(nil, fake)

			startCh := make(chan string, 1)
			completeCh := make(chan bool, 1)

			_ = s.Add(Job{
				Name:       "hooks." + c.name,
				Interval:   100 * time.Millisecond,
				RunOnStart: true,
				OnStart: func(name string) {
					select {
					case startCh <- name:
					default:
					}
				},
				OnComplete: func(_ string, _ int64, success bool, _ error) {
					select {
					case completeCh <- success:
					default:
					}
				},
				Run: func(context.Context) error { return c.jobErr },
			})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if err := s.Start(ctx); err != nil {
				t.Fatal(err)
			}
			defer s.Stop()

			select {
			case name := <-startCh:
				if name != "hooks."+c.name {
					t.Errorf("OnStart: got %q, want %q", name, "hooks."+c.name)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("OnStart was not called")
			}

			select {
			case ok := <-completeCh:
				if ok != c.wantOK {
					t.Errorf("OnComplete success=%v, want %v", ok, c.wantOK)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("OnComplete was not called")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Job list snapshot
// ---------------------------------------------------------------------------

// TestJobsSnapshotBeforeStart verifies that Jobs() returns the
// registered jobs before Start is called.
func TestJobsSnapshotBeforeStart(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)
	jobs := []Job{
		{Name: "a", Interval: time.Second, Run: func(context.Context) error { return nil }},
		{Name: "b", Interval: 2 * time.Second, Run: func(context.Context) error { return nil }},
	}
	for _, j := range jobs {
		if err := s.Add(j); err != nil {
			t.Fatal(err)
		}
	}

	snap := s.Jobs()
	if len(snap) != len(jobs) {
		t.Fatalf("Jobs() returned %d entries, want %d", len(snap), len(jobs))
	}
	for i, j := range snap {
		if j.Name != jobs[i].Name {
			t.Errorf("snap[%d].Name=%q, want %q", i, j.Name, jobs[i].Name)
		}
		if j.Interval != jobs[i].Interval {
			t.Errorf("snap[%d].Interval=%v, want %v", i, j.Interval, jobs[i].Interval)
		}
	}
}

// TestAddAfterStartLaunchesImmediately verifies that Add succeeds after Start
// and that the newly registered job runs on the live context.
func TestAddAfterStartLaunchesImmediately(t *testing.T) {
	t.Parallel()

	s := New(nil, nil)
	_ = s.Add(Job{
		Name:     "seed",
		Interval: time.Minute,
		Run:      func(context.Context) error { return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	var n atomic.Int32
	err := s.Add(Job{
		Name:       "late",
		Interval:   10 * time.Millisecond,
		RunOnStart: true,
		Run:        func(context.Context) error { n.Add(1); return nil },
	})
	if err != nil {
		t.Errorf("Add after Start returned unexpected error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	s.Stop()
	if n.Load() < 1 {
		t.Errorf("late job ran %d times, want ≥ 1", n.Load())
	}
}
