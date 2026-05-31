// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package combined

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// P1-5: WeekProfile is the combined data point for thermostat schedules
// — it bundles the underlying ClimateProfile, exposes an atomic Set, and
// fans out OnUpdate to subscribers.

type fakeWriter struct {
	mu      sync.Mutex
	called  atomic.Int32
	lastArg any
	err     error
}

func (w *fakeWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, value any, _ hmenum.CommandPriority) error {
	w.called.Add(1)
	w.mu.Lock()
	w.lastArg = value
	w.mu.Unlock()
	return w.err
}

type stubSaver struct {
	last atomic.Pointer[schedule.Climate]
	err  error
}

func (s *stubSaver) Save(_ context.Context, v *schedule.Climate) error {
	if s.err != nil {
		return s.err
	}
	s.last.Store(v)
	return nil
}

func TestWeekProfileSetSavesViaProfile(t *testing.T) {
	t.Parallel()
	saver := &stubSaver{}
	prof := weekprofile.NewClimate(nil, saver)
	wp := NewWeekProfile("0001ABCD:1", &fakeWriter{}, prof, "WEEK_PROFILE")
	defer wp.Close()

	clim := schedule.NewClimate()
	if err := wp.Set(context.Background(), clim, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := saver.last.Load(); got != clim {
		t.Fatalf("saver did not receive schedule")
	}
}

func TestWeekProfileSetFallsBackToWriterWithoutProfile(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	wp := NewWeekProfile("0001ABCD:1", w, nil, "WEEK_PROFILE")
	clim := schedule.NewClimate()
	if err := wp.Set(context.Background(), clim, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if w.called.Load() != 1 {
		t.Fatalf("writer not called: %d", w.called.Load())
	}
}

func TestWeekProfileSetSurfacesSaverError(t *testing.T) {
	t.Parallel()
	saver := &stubSaver{err: errors.New("disk full")}
	prof := weekprofile.NewClimate(nil, saver)
	wp := NewWeekProfile("0001ABCD:1", &fakeWriter{}, prof, "WEEK_PROFILE")
	defer wp.Close()
	if err := wp.Set(context.Background(), schedule.NewClimate(), hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("expected error from saver")
	}
}

func TestWeekProfileSetRejectsNilSchedule(t *testing.T) {
	t.Parallel()
	wp := NewWeekProfile("X", &fakeWriter{}, nil, "WEEK_PROFILE")
	if err := wp.Set(context.Background(), nil, hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("expected error for nil schedule")
	}
}

func TestWeekProfileObserveFiresCallbacks(t *testing.T) {
	t.Parallel()
	saver := &stubSaver{}
	prof := weekprofile.NewClimate(nil, saver)
	wp := NewWeekProfile("0001ABCD:1", &fakeWriter{}, prof, "WEEK_PROFILE")
	defer wp.Close()

	var fired atomic.Int32
	unsub := wp.OnUpdate(func(_, _ *schedule.Climate) { fired.Add(1) })
	defer unsub()

	clim := schedule.NewClimate()
	if err := wp.Set(context.Background(), clim, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := fired.Load(); got != 1 {
		t.Fatalf("callback fired %d times, want 1", got)
	}
	if got, ok := wp.Value(); !ok || got != clim {
		t.Fatalf("value=%v ok=%v", got, ok)
	}
}

func TestWeekProfileUnsubscribeIsIdempotent(t *testing.T) {
	t.Parallel()
	saver := &stubSaver{}
	prof := weekprofile.NewClimate(nil, saver)
	wp := NewWeekProfile("X", &fakeWriter{}, prof, "WEEK_PROFILE")
	defer wp.Close()

	var fired atomic.Int32
	unsub := wp.OnUpdate(func(_, _ *schedule.Climate) { fired.Add(1) })
	unsub()
	unsub() // second call must be a no-op
	_ = wp.Set(context.Background(), schedule.NewClimate(), hmenum.CommandPriorityHigh)
	if fired.Load() != 0 {
		t.Fatal("callback fired after unsubscribe")
	}
}

func TestWeekProfileNilReceiverErrors(t *testing.T) {
	t.Parallel()
	var wp *WeekProfile
	if err := wp.Set(context.Background(), schedule.NewClimate(), hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("expected error from nil receiver")
	}
}

// IsRefreshed / StateUncertain on WeekProfile

func TestWeekProfileIsRefreshedFalseBeforeAnySchedule(t *testing.T) {
	t.Parallel()
	prof := weekprofile.NewClimate(nil, &stubSaver{})
	wp := NewWeekProfile("X", &fakeWriter{}, prof, "WEEK_PROFILE")
	defer wp.Close()
	if wp.IsRefreshed() {
		t.Fatal("IsRefreshed must be false before any schedule observed")
	}
}

func TestWeekProfileIsRefreshedTrueAfterObserve(t *testing.T) {
	t.Parallel()
	saver := &stubSaver{}
	prof := weekprofile.NewClimate(nil, saver)
	wp := NewWeekProfile("X", &fakeWriter{}, prof, "WEEK_PROFILE")
	defer wp.Close()
	_ = wp.Set(context.Background(), schedule.NewClimate(), hmenum.CommandPriorityHigh)
	if !wp.IsRefreshed() {
		t.Fatal("IsRefreshed must be true after a schedule is observed")
	}
}

func TestWeekProfileStateUncertainAlwaysFalse(t *testing.T) {
	t.Parallel()
	wp := NewWeekProfile("X", &fakeWriter{}, nil, "WEEK_PROFILE")
	if wp.StateUncertain() {
		t.Fatal("StateUncertain must always be false for WeekProfile")
	}
}
