// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package custom

import (
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestProfileRegistryRoundTrip(t *testing.T) {
	r := NewRegistry()
	p := Profile{
		Name:         "IPSwitch",
		DeviceType:   "HmIP-PS",
		ProductGroup: hmenum.ProductGroupHmIP,
		Category:     hmenum.DataPointCategorySwitch,
		Channels:     []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
	}
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(p); err == nil {
		t.Fatal("duplicate register should fail")
	}
	got, err := r.Get(hmenum.DataPointCategorySwitch, "HmIP-PS")
	if err != nil || got.Name != "IPSwitch" {
		t.Fatalf("Get=%+v err=%v", got, err)
	}
	if list := r.DeviceTypes(); len(list) != 1 || list[0] != "hmip-ps" {
		t.Fatalf("DeviceTypes=%v", list)
	}
}

func TestRegistryGetMissing(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get(hmenum.DataPointCategorySwitch, "ghost"); !errors.Is(err, ErrProfileMissing) {
		t.Fatalf("got %v, want ErrProfileMissing", err)
	}
}

func TestStateChangeTimerDebounces(t *testing.T) {
	timer := NewStateChangeTimer(30 * time.Millisecond)
	fired := make(chan int, 3)
	timer.Schedule(func() { fired <- 1 })
	timer.Schedule(func() { fired <- 2 }) // supersedes the first
	timer.Schedule(func() { fired <- 3 }) // supersedes the second
	select {
	case got := <-fired:
		if got != 3 {
			t.Fatalf("got=%d, want 3", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timer never fired")
	}
}

func TestPositionClamps(t *testing.T) {
	if NewPosition(-1).Level() != 0 {
		t.Error("negative should clamp to 0")
	}
	if NewPosition(2).Level() != 1 {
		t.Error("> 1 should clamp to 1")
	}
	if NewPosition(0.25).OpenFraction() != 25 {
		t.Error("OpenFraction")
	}
}

func TestBrightnessByteMath(t *testing.T) {
	b := NewBrightness(0.5)
	if b.Byte() != 127 && b.Byte() != 128 {
		t.Fatalf("0.5 → Byte=%d", b.Byte())
	}
	if !b.IsOn() {
		t.Error("0.5 should be on")
	}
	if NewBrightness(0).IsOn() {
		t.Error("0 should be off")
	}
}

// TestBrightnessPct verifies that Brightness.Pct() mirrors
func TestBrightnessPct(t *testing.T) {
	t.Parallel()
	cases := []struct {
		level float64
		want  int
	}{
		{0.0, 0},
		{0.5, 50},
		{1.0, 100},
		{0.25, 25},
		{0.75, 75},
	}
	for _, tc := range cases {
		b := NewBrightness(tc.level)
		got := b.Pct()
		if got != tc.want {
			t.Errorf("NewBrightness(%v).Pct() = %d, want %d", tc.level, got, tc.want)
		}
	}
	// Clamp: out-of-range level must not produce out-of-range pct.
	if NewBrightness(1.5).Pct() != 100 {
		t.Error("clamped-to-1 brightness must return 100%%")
	}
	if NewBrightness(-0.5).Pct() != 0 {
		t.Error("clamped-to-0 brightness must return 0%%")
	}
}

func TestGroupStateAggregates(t *testing.T) {
	g := NewGroupState()
	g.Set("a", true)
	g.Set("b", true)
	if !g.AllOn() || !g.AnyOn() {
		t.Error("all on")
	}
	g.Set("b", false)
	if g.AllOn() {
		t.Error("one off should break AllOn")
	}
	if !g.AnyOn() {
		t.Error("still any on")
	}
	g.Set("a", false)
	if g.AnyOn() {
		t.Error("none on")
	}
}

// TestGroupStateGroupValue verifies that GroupValue() returns true
// only when all members are on (AllOn semantics), false otherwise.
func TestGroupStateGroupValue(t *testing.T) {
	g := NewGroupState()

	// Empty group → false.
	if g.GroupValue() {
		t.Error("empty group should return false")
	}
	// Single member on → true.
	g.Set("a", true)
	if !g.GroupValue() {
		t.Error("single-member-on should be true")
	}
	// Add second member off → false.
	g.Set("b", false)
	if g.GroupValue() {
		t.Error("one member off should yield false")
	}
	// Both on → true.
	g.Set("b", true)
	if !g.GroupValue() {
		t.Error("all members on should yield true")
	}
}

// TestStateChangeTimerIsTimerStateChange verifies that IsTimerStateChange
// returns true while a callback is armed and false after cancel.
func TestStateChangeTimerIsTimerStateChange(t *testing.T) {
	timer := NewStateChangeTimer(100 * time.Millisecond)

	if timer.IsTimerStateChange() {
		t.Error("should be false before any Schedule call")
	}
	done := make(chan struct{}, 1)
	timer.Schedule(func() { done <- struct{}{} })
	if !timer.IsTimerStateChange() {
		t.Error("should be true while armed")
	}
	timer.Cancel()
	if timer.IsTimerStateChange() {
		t.Error("should be false after Cancel")
	}
}

// TestStateChangeTimerIsStateChangeForOnOff verifies.
func TestStateChangeTimerIsStateChangeForOnOff(t *testing.T) {
	timer := NewStateChangeTimer(100 * time.Millisecond)

	trueVal := true
	falseVal := false

	// No timer, turning on when already on → no state change.
	if timer.IsStateChangeForOnOff(true, false, &trueVal) {
		t.Error("turn-on when already on should not be a state change")
	}
	// No timer, turning on when off → state change.
	if !timer.IsStateChangeForOnOff(true, false, &falseVal) {
		t.Error("turn-on when off should be a state change")
	}
	// No timer, turning off when already off → no state change.
	if timer.IsStateChangeForOnOff(false, true, &falseVal) {
		t.Error("turn-off when already off should not be a state change")
	}
	// No timer, nil current value → state change for both on/off.
	if !timer.IsStateChangeForOnOff(true, false, nil) {
		t.Error("nil current value + turn-on should be a state change")
	}
	// Timer armed → always a state change regardless of flags.
	timer.Schedule(func() {})
	if !timer.IsStateChangeForOnOff(false, false, &trueVal) {
		t.Error("armed timer should force state change even with no flags")
	}
	timer.Cancel()
}

// TestLevelToPercentIsOneRuleForPositionAndBrightness pins the two 0–100
// accessors to a single rule. The CCU reports LEVEL on a 0.01 grid, and three
// of those hundredths (0.29, 0.57, 0.58) land just below their exact value in
// binary64 — truncating reports them one percent low, which is visible to an
// operator as a slider that snaps back a step. Walking the whole grid catches
// exactly that.
func TestLevelToPercentIsOneRuleForPositionAndBrightness(t *testing.T) {
	t.Parallel()
	for i := range 101 {
		level := float64(i) / 100
		if got := NewPosition(level).OpenFraction(); got != i {
			t.Errorf("NewPosition(%v).OpenFraction() = %d, want %d", level, got, i)
		}
		if got := NewBrightness(level).Pct(); got != i {
			t.Errorf("NewBrightness(%v).Pct() = %d, want %d", level, got, i)
		}
	}
}
