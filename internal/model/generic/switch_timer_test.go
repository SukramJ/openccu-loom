// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// switchCfg returns a Config suitable for a Switch data point.
func switchCfg() Spec {
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Kind = KindSwitch
	return cfg
}

// ─── TimerOnTimeRunning ───────────────────────────────────────────────────────

// TestTimerOnTimeRunningFalseInitially verifies that a freshly constructed
// Switch reports TimerOnTimeRunning() == false before any timer has been
// started.
func TestTimerOnTimeRunningFalseInitially(t *testing.T) {
	t.Parallel()

	s := NewSwitch(switchCfg())
	if s.TimerOnTimeRunning() {
		t.Fatal("TimerOnTimeRunning() must be false for a freshly constructed Switch")
	}
}

// TestTimerOnTimeRunningTrueAfterGetAndStartTimer verifies that TimerOnTimeRunning()
// returns true immediately after GetAndStartTimer() has consumed a pending timer
// and set the end-time to now + duration.
func TestTimerOnTimeRunningTrueAfterGetAndStartTimer(t *testing.T) {
	t.Parallel()

	s := NewSwitch(switchCfg())
	s.SetTimerOnTime(5 * time.Second)
	_, ok := s.GetAndStartTimer()
	if !ok {
		t.Fatal("GetAndStartTimer() must return (ok=true) when a timer was pending")
	}
	if !s.TimerOnTimeRunning() {
		t.Fatal("TimerOnTimeRunning() must be true immediately after GetAndStartTimer()")
	}
}

// TestTimerOnTimeRunningFalseAfterExpiry verifies that once the on-time window
// passes, TimerOnTimeRunning() returns false.
func TestTimerOnTimeRunningFalseAfterExpiry(t *testing.T) {
	t.Parallel()

	s := NewSwitch(switchCfg())
	s.SetTimerOnTime(1 * time.Millisecond)
	_, _ = s.GetAndStartTimer()
	time.Sleep(10 * time.Millisecond)
	if s.TimerOnTimeRunning() {
		t.Fatal("TimerOnTimeRunning() must be false after the on-time window has passed")
	}
}

// ─── timerOnTimeEnd reset on SetTimerOnTime ───────────────────────────────────

// TestSetTimerOnTimeResetsEndTime verifies that calling SetTimerOnTime after a
// Timer is running resets the running end-time. This mirrors
// set_timer_on_time which always resets _timer_on_time_end = INIT_DATETIME.
func TestSetTimerOnTimeResetsEndTime(t *testing.T) {
	t.Parallel()

	s := NewSwitch(switchCfg())
	// Start a timer.
	s.SetTimerOnTime(5 * time.Second)
	_, _ = s.GetAndStartTimer()
	if !s.TimerOnTimeRunning() {
		t.Fatal("precondition: timer must be running after GetAndStartTimer")
	}
	// Overwrite with a new pending timer — end-time must reset.
	s.SetTimerOnTime(10 * time.Second)
	if s.TimerOnTimeRunning() {
		t.Fatal("TimerOnTimeRunning() must be false after SetTimerOnTime resets the end-time")
	}
}

// ─── GetAndStartTimer ─────────────────────────────────────────────────────────

// TestGetAndStartTimerNoPending verifies that GetAndStartTimer returns (0,
// false) when no pending timer is set.
func TestGetAndStartTimerNoPending(t *testing.T) {
	t.Parallel()

	s := NewSwitch(switchCfg())
	secs, ok := s.GetAndStartTimer()
	if ok {
		t.Fatalf("GetAndStartTimer() must return ok=false when no timer is pending; got secs=%f ok=%v", secs, ok)
	}
	if secs != 0 {
		t.Fatalf("GetAndStartTimer() must return 0 seconds when no timer is pending; got %f", secs)
	}
}

// TestGetAndStartTimerConsumesPending verifies that calling GetAndStartTimer
// clears the pending slot so a second call returns (0, false).
func TestGetAndStartTimerConsumesPending(t *testing.T) {
	t.Parallel()

	s := NewSwitch(switchCfg())
	s.SetTimerOnTime(3 * time.Second)

	secs, ok := s.GetAndStartTimer()
	if !ok {
		t.Fatal("first GetAndStartTimer() must return ok=true")
	}
	if secs != 3.0 {
		t.Fatalf("expected 3.0 seconds, got %f", secs)
	}
	// Second call: pending has been consumed.
	secs2, ok2 := s.GetAndStartTimer()
	if ok2 {
		t.Fatalf("second GetAndStartTimer() must return ok=false; got secs=%f ok=%v", secs2, ok2)
	}
}

// ─── ResetTimerOnTime ─────────────────────────────────────────────────────────

// TestResetTimerOnTimeClearsPending verifies that ResetTimerOnTime clears
// the pending on-time so a subsequent GetAndStartTimer returns (0, false).
func TestResetTimerOnTimeClearsPending(t *testing.T) {
	t.Parallel()

	s := NewSwitch(switchCfg())
	s.SetTimerOnTime(5 * time.Second)
	s.ResetTimerOnTime()

	secs, ok := s.GetAndStartTimer()
	if ok || secs != 0 {
		t.Fatalf("GetAndStartTimer() after ResetTimerOnTime() must return (0, false); got (%f, %v)", secs, ok)
	}
}

// TestResetTimerOnTimeStopsRunningTimer verifies that ResetTimerOnTime also
// resets a running on-time end-marker so TimerOnTimeRunning() returns false.
func TestResetTimerOnTimeStopsRunningTimer(t *testing.T) {
	t.Parallel()

	s := NewSwitch(switchCfg())
	s.SetTimerOnTime(5 * time.Second)
	_, _ = s.GetAndStartTimer()
	if !s.TimerOnTimeRunning() {
		t.Fatal("precondition: timer must be running before reset")
	}
	s.ResetTimerOnTime()
	if s.TimerOnTimeRunning() {
		t.Fatal("TimerOnTimeRunning() must be false after ResetTimerOnTime()")
	}
}
