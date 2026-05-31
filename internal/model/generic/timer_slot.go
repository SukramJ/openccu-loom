// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"sync"
	"time"
)

// TimerSlot is an embeddable struct that carries the ON_TIME pending/running
// state shared by writable data points that support a timed-on duration.
// Embed it by value in any concrete data point type that needs the timer
// surface.
//
// All methods are safe for concurrent use. The mutex is unexported so
// embedders do not accidentally share it with their own locking.
type TimerSlot struct {
	timerMu        sync.Mutex
	pending        *time.Duration // deferred ON_TIME for next activation
	timerOnTimeEnd time.Time      // end time of the currently running on_time
}

// SetTimerOnTime stores `d` for the next activation call. The timer is
// consumed by the next action that starts the countdown and cleared
// afterwards. Pass a zero or negative duration to clear without applying.
// Also resets the running end-time to zero.
func (t *TimerSlot) SetTimerOnTime(d time.Duration) {
	t.timerMu.Lock()
	defer t.timerMu.Unlock()
	t.timerOnTimeEnd = time.Time{} // reset running end-time
	if d <= 0 {
		t.pending = nil
		return
	}
	t.pending = &d
}

// TimerOnTime returns the pending on-time duration and whether one is set.
func (t *TimerSlot) TimerOnTime() (time.Duration, bool) {
	t.timerMu.Lock()
	defer t.timerMu.Unlock()
	if t.pending == nil {
		return 0, false
	}
	return *t.pending, true
}

// TimerOnTimeRunning reports whether an on-time timer is currently active —
// i.e. the device output will revert to off at some future time.
//
// Returns false when no timer has been started.
func (t *TimerSlot) TimerOnTimeRunning() bool {
	t.timerMu.Lock()
	end := t.timerOnTimeEnd
	t.timerMu.Unlock()
	if end.IsZero() {
		return false
	}
	return !time.Now().After(end)
}

// IsTimerStateChange reports whether the timer state alone is enough to
// classify the next write as a state change. Returns true when an
// on-time timer is currently running OR when a pending on-time has been
// deferred via [TimerSlot.SetTimerOnTime] and is waiting for activation.
//
// State-change reasoning: any activation that touches a data point
// whose timer is pending or active must go through to the wire even if
// the target value matches the current state — otherwise the timer
// arming side-effect is silently lost.
func (t *TimerSlot) IsTimerStateChange() bool {
	t.timerMu.Lock()
	end := t.timerOnTimeEnd
	pending := t.pending
	t.timerMu.Unlock()
	if !end.IsZero() && !time.Now().After(end) {
		return true
	}
	return pending != nil
}

// GetAndStartTimer atomically retrieves the pending on-time value, starts
// the countdown, and returns the on-time in seconds.
//
//  1. If the timer is already running and pending ≤ 0, reset and return
//     (-1, true) — signal "already fired".
//  2. If pending is nil, reset and return (0, false).
//  3. Otherwise consume pending, stamp timerOnTimeEnd, and return the value.
//
// Returns (seconds, true) when a fresh on-time was consumed, (0, false) when
// none was pending, and (-1, true) when the timer was already running but
// the pending slot held a non-positive value.
func (t *TimerSlot) GetAndStartTimer() (seconds float64, ok bool) {
	t.timerMu.Lock()
	defer t.timerMu.Unlock()

	// Case 1: timer is running but pending is ≤ 0 — already fired.
	if !t.timerOnTimeEnd.IsZero() && !time.Now().After(t.timerOnTimeEnd) &&
		t.pending != nil && *t.pending <= 0 {
		t.pending = nil
		t.timerOnTimeEnd = time.Time{}
		return -1, true
	}
	// Case 2: nothing pending.
	if t.pending == nil {
		t.timerOnTimeEnd = time.Time{}
		return 0, false
	}
	// Case 3: consume the pending timer and stamp the end time.
	d := *t.pending
	t.pending = nil
	t.timerOnTimeEnd = time.Now().Add(d)
	return d.Seconds(), true
}

// ResetTimerOnTime clears the pending on-time and resets the running
// end-time to zero. Idempotent.
func (t *TimerSlot) ResetTimerOnTime() {
	t.timerMu.Lock()
	t.pending = nil
	t.timerOnTimeEnd = time.Time{}
	t.timerMu.Unlock()
}

// consumePending atomically drains the pending duration and returns it.
// Returns (nil) when nothing was pending. Used internally by Switch.turnOnWithTimer
// so the caller does not need to reach into the unexported fields.
func (t *TimerSlot) consumePending() *time.Duration {
	t.timerMu.Lock()
	defer t.timerMu.Unlock()
	if t.pending == nil {
		return nil
	}
	d := *t.pending
	t.pending = nil
	return &d
}
