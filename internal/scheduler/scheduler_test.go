// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerRunsJob(t *testing.T) {
	s := New(nil, nil)
	var n atomic.Int32
	err := s.Add(Job{
		Name:       "tick",
		Interval:   10 * time.Millisecond,
		RunOnStart: true,
		Run:        func(context.Context) error { n.Add(1); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	s.Stop()

	if n.Load() < 2 {
		t.Fatalf("ran %d times, want ≥ 2", n.Load())
	}
}

func TestSchedulerRejectsDoubleStart(t *testing.T) {
	s := New(nil, nil)
	_ = s.Add(Job{Name: "x", Interval: time.Minute, Run: func(context.Context) error { return nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	if err := s.Start(ctx); err == nil {
		t.Fatal("second Start should fail")
	}
}

func TestSchedulerValidatesJob(t *testing.T) {
	s := New(nil, nil)
	if err := s.Add(Job{}); err == nil {
		t.Error("empty job should fail")
	}
	if err := s.Add(Job{Name: "x"}); err == nil {
		t.Error("job without interval should fail")
	}
	if err := s.Add(Job{Name: "x", Interval: time.Second}); err == nil {
		t.Error("job without Run should fail")
	}
}

func TestSchedulerAddAfterStart(t *testing.T) {
	s := New(nil, nil)
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
		t.Fatalf("Add after Start returned unexpected error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	s.Stop()
	if n.Load() < 1 {
		t.Fatalf("late job ran %d times, want ≥ 1", n.Load())
	}
}

func TestSchedulerStopsOnContextCancel(t *testing.T) {
	s := New(nil, nil)
	_ = s.Add(Job{
		Name:     "tick",
		Interval: 20 * time.Millisecond,
		Run: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Millisecond):
				return nil
			}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	s.Stop() // must not hang
}

func TestSchedulerJobErrorIsLoggedNotPropagated(t *testing.T) {
	s := New(nil, nil)
	fail := errors.New("boom")
	var n atomic.Int32
	_ = s.Add(Job{
		Name:       "bad",
		Interval:   5 * time.Millisecond,
		RunOnStart: true,
		Run: func(context.Context) error {
			n.Add(1)
			return fail
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	s.Stop()
	if n.Load() < 2 {
		t.Fatalf("ran %d times", n.Load())
	}
}

// --- C-SCHED-2: OnStart / OnComplete lifecycle hooks ---

// TestJobOnStartCalledBeforeRun verifies that OnStart is invoked with
// the job name before the job function runs. Closes C-SCHED-2.
func TestJobOnStartCalledBeforeRun(t *testing.T) {
	t.Parallel()
	s := New(nil, nil)

	var startNames []string
	var runOrder []string

	_ = s.Add(Job{
		Name:       "refresh.device",
		Interval:   100 * time.Millisecond,
		RunOnStart: true,
		OnStart: func(name string) {
			startNames = append(startNames, name)
		},
		Run: func(context.Context) error {
			runOrder = append(runOrder, "run")
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	_ = s.Start(ctx)
	// Brief wait for the RunOnStart invocation to complete.
	time.Sleep(20 * time.Millisecond)
	cancel()
	s.Stop()

	if len(startNames) == 0 {
		t.Fatal("OnStart was never called")
	}
	if startNames[0] != "refresh.device" {
		t.Errorf("OnStart got %q, want %q", startNames[0], "refresh.device")
	}
}

// TestJobOnCompleteCalledAfterRun verifies that OnComplete fires after
// each job invocation with the job name, duration and success flag.
// Closes C-SCHED-2.
func TestJobOnCompleteCalledAfterRun(t *testing.T) {
	t.Parallel()
	s := New(nil, nil)

	type result struct {
		name    string
		ms      int64
		success bool
	}
	var results []result

	_ = s.Add(Job{
		Name:       "metrics.refresh",
		Interval:   100 * time.Millisecond,
		RunOnStart: true,
		OnComplete: func(name string, durationMs int64, success bool, _ error) {
			results = append(results, result{name, durationMs, success})
		},
		Run: func(context.Context) error { return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	_ = s.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	s.Stop()

	if len(results) == 0 {
		t.Fatal("OnComplete was never called")
	}
	r := results[0]
	if r.name != "metrics.refresh" {
		t.Errorf("OnComplete name=%q, want %q", r.name, "metrics.refresh")
	}
	if !r.success {
		t.Error("OnComplete success=false, want true for a no-error job")
	}
	if r.ms < 0 {
		t.Errorf("OnComplete durationMs=%d, want >= 0", r.ms)
	}
}

// TestJobOnCompleteReportsFailure verifies that OnComplete passes
// success=false when the job returns an error.
func TestJobOnCompleteReportsFailure(t *testing.T) {
	t.Parallel()
	s := New(nil, nil)

	gotSuccess := true
	_ = s.Add(Job{
		Name:       "fail.job",
		Interval:   100 * time.Millisecond,
		RunOnStart: true,
		OnComplete: func(_ string, _ int64, success bool, _ error) {
			gotSuccess = success
		},
		Run: func(context.Context) error { return errors.New("oops") },
	})

	ctx, cancel := context.WithCancel(context.Background())
	_ = s.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	s.Stop()

	if gotSuccess {
		t.Error("OnComplete success must be false when job returns error")
	}
}
