// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package weekprofile

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
)

// ---------------------------------------------------------------------------
// CopyClimateProfileKey
// ---------------------------------------------------------------------------

// TestCopyClimateProfileKey verifies that a named profile slot is copied from
// source to destination and that modifications to the copy do not affect the
// original (deep copy).
func TestCopyClimateProfileKey(t *testing.T) {
	t.Parallel()

	srcSched := makeClimateScheduleWithP1()
	src := NewClimate(nil, &noopClimateSaver{})
	if err := src.Save(context.Background(), srcSched); err != nil {
		t.Fatalf("src.Save: %v", err)
	}

	dstSched := schedule.NewClimate()
	dstSaver := &capturingClimateSaver{}
	dst := NewClimate(nil, dstSaver)
	if err := dst.Save(context.Background(), dstSched); err != nil {
		t.Fatalf("dst.Save: %v", err)
	}

	if err := CopyClimateProfileKey(context.Background(), src, "P1", dst, "P2"); err != nil {
		t.Fatalf("CopyClimateProfileKey: %v", err)
	}

	// The destination saver must have been called.
	if dstSaver.calls == 0 {
		t.Fatal("destination saver was not called")
	}

	// dst must now have P2 with the same day structure as src P1.
	saved := dstSaver.last
	p2, ok := saved.Profiles["P2"]
	if !ok {
		t.Fatal("destination schedule must contain P2 after copy")
	}
	wd, ok := p2.Days[schedule.WeekdayMonday]
	if !ok {
		t.Fatal("P2 Monday must exist after copy")
	}
	if wd.BaseTemperature != 21.0 {
		t.Errorf("P2 Monday BaseTemperature = %v, want 21.0", wd.BaseTemperature)
	}

	// Mutating dst must not affect src (deep copy).
	p2.Days[schedule.WeekdayMonday] = schedule.ClimateWeekday{BaseTemperature: 99.0}
	srcP1 := srcSched.Profiles["P1"]
	if srcP1.Days[schedule.WeekdayMonday].BaseTemperature == 99.0 {
		t.Error("mutating destination affected source (shallow copy detected)")
	}
}

// TestCopyClimateProfileKeySourceNotLoaded verifies that the function returns
// an error wrapping ErrNotLoaded when the source profile has not been loaded.
func TestCopyClimateProfileKeySourceNotLoaded(t *testing.T) {
	t.Parallel()

	src := NewClimate(nil, nil) // never saved/loaded
	dst := NewClimate(nil, &noopClimateSaver{})
	_ = dst.Save(context.Background(), schedule.NewClimate())

	err := CopyClimateProfileKey(context.Background(), src, "P1", dst, "P1")
	if err == nil {
		t.Fatal("expected error when source is not loaded")
	}
	if !errors.Is(err, ErrNotLoaded) {
		t.Errorf("expected ErrNotLoaded in chain, got %v", err)
	}
}

// TestCopyClimateProfileKeyMissingSourceKey verifies that requesting a
// non-existent profile key in the source returns an error.
func TestCopyClimateProfileKeyMissingSourceKey(t *testing.T) {
	t.Parallel()

	src := NewClimate(nil, &noopClimateSaver{})
	_ = src.Save(context.Background(), schedule.NewClimate()) // empty — no P1

	dst := NewClimate(nil, &noopClimateSaver{})
	_ = dst.Save(context.Background(), schedule.NewClimate())

	err := CopyClimateProfileKey(context.Background(), src, "P1", dst, "P2")
	if err == nil {
		t.Fatal("expected error for missing source key")
	}
}

// ---------------------------------------------------------------------------
// CopyClimateSchedule
// ---------------------------------------------------------------------------

// TestCopyClimateSchedule verifies that the full schedule is copied from
// source to destination and that the copy is independent (deep).
func TestCopyClimateSchedule(t *testing.T) {
	t.Parallel()

	srcSched := makeClimateScheduleWithP1()
	src := NewClimate(nil, &noopClimateSaver{})
	_ = src.Save(context.Background(), srcSched)

	dstSaver := &capturingClimateSaver{}
	dst := NewClimate(nil, dstSaver)
	_ = dst.Save(context.Background(), schedule.NewClimate())

	if err := CopyClimateSchedule(context.Background(), src, dst); err != nil {
		t.Fatalf("CopyClimateSchedule: %v", err)
	}

	if dstSaver.calls == 0 {
		t.Fatal("destination saver was not called")
	}

	saved := dstSaver.last
	p1, ok := saved.Profiles["P1"]
	if !ok {
		t.Fatal("destination must contain P1 after full schedule copy")
	}
	if p1.Days[schedule.WeekdayMonday].BaseTemperature != 21.0 {
		t.Errorf("P1 Monday BaseTemperature = %v, want 21.0", p1.Days[schedule.WeekdayMonday].BaseTemperature)
	}

	// Deep-copy check: mutate dst copy; src must be unchanged.
	p1.Days[schedule.WeekdayMonday] = schedule.ClimateWeekday{BaseTemperature: 99.0}
	if srcSched.Profiles["P1"].Days[schedule.WeekdayMonday].BaseTemperature == 99.0 {
		t.Error("mutating destination affected source (shallow copy detected)")
	}
}

// TestCopyClimateScheduleSourceNotLoaded verifies error propagation when the
// source profile has never been loaded.
func TestCopyClimateScheduleSourceNotLoaded(t *testing.T) {
	t.Parallel()

	src := NewClimate(nil, nil)
	dst := NewClimate(nil, &noopClimateSaver{})
	_ = dst.Save(context.Background(), schedule.NewClimate())

	err := CopyClimateSchedule(context.Background(), src, dst)
	if err == nil {
		t.Fatal("expected error when source is not loaded")
	}
	if !errors.Is(err, ErrNotLoaded) {
		t.Errorf("expected ErrNotLoaded in chain, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeClimateScheduleWithP1 builds a minimal [schedule.Climate] containing
// one profile (P1) with a Monday entry.
func makeClimateScheduleWithP1() *schedule.Climate {
	sched := schedule.NewClimate()
	prof := schedule.NewClimateProfile()
	prof.Days[schedule.WeekdayMonday] = schedule.ClimateWeekday{
		BaseTemperature: 21.0,
		Periods:         []schedule.ClimatePeriod{},
	}
	sched.Profiles["P1"] = prof
	return sched
}

type noopClimateSaver struct{}

func (n *noopClimateSaver) Save(_ context.Context, _ *schedule.Climate) error { return nil }

type capturingClimateSaver struct {
	calls int
	last  *schedule.Climate
}

func (c *capturingClimateSaver) Save(_ context.Context, v *schedule.Climate) error {
	c.calls++
	c.last = v
	return nil
}
