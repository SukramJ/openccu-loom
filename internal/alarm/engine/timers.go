// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
)

// Timer kinds persisted in alarm_state.timers_json and stamped into
// [ZoneSnapshot.TimerKind]. The strings are part of the persisted
// format — keep them stable.
//
// Exported together with [IsCountdownTimerKind] because every
// north-bound surface that renders a snapshot has to answer the same
// question about them, and each surface that spells the tokens out
// again answers it independently.
const (
	TimerKindExit      = "exit_delay"
	TimerKindEntry     = "entry_delay"
	TimerKindTrigger   = "trigger_time"
	TimerKindPreAlarm  = "pre_alarm"
	TimerKindAutoRearm = "auto_rearm"
)

// IsCountdownTimerKind reports whether a timer kind is a user-facing
// countdown — one a panel renders as a running ring with a remaining
// time, and the only kinds the AlarmCountdown DTO admits (its schema in
// assets/openapi.yaml constrains the field to exit_delay and
// entry_delay, and the SPA's generated type mirrors that).
//
// The other kinds are internal phase timers: trigger_time and pre_alarm
// bound an incident the panel already shows as triggered, and
// auto_rearm runs on a disarmed zone. Publishing one of them as a
// countdown puts an out-of-enum value on the wire and renders a
// pre-alarm as an exit delay.
func IsCountdownTimerKind(kind string) bool {
	switch kind {
	case TimerKindExit, TimerKindEntry:
		return true
	default:
		return false
	}
}

// persistedTimer is the redundant countdown tuple stored per active
// timer (notes/concepts/alarm-concept.md §5): the wall-clock deadline for
// plausible-clock restores, the remaining duration for conservative
// restores under an implausible clock, the persist-time timestamp and
// boot counter to detect both.
type persistedTimer struct {
	Kind          string `json:"kind"`
	DeadlineMS    int64  `json:"deadline_ms"`
	RemainingMS   int64  `json:"remaining_ms"`
	PersistedAtMS int64  `json:"persisted_at_ms"`
	BootCount     int64  `json:"boot_count"`
}

// encodeTimers serializes timer tuples for alarm_state.timers_json.
func encodeTimers(ts []persistedTimer) string {
	if len(ts) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ts)
	if err != nil {
		// invariant: persistedTimer has no unmarshalable fields.
		return "[]"
	}
	return string(b)
}

// decodeTimers parses alarm_state.timers_json. Corrupt content
// degrades to "no timers" — restore then falls back to the
// conservative per-state defaults instead of failing the boot.
func decodeTimers(raw string) []persistedTimer {
	if raw == "" {
		return nil
	}
	var ts []persistedTimer
	if err := json.Unmarshal([]byte(raw), &ts); err != nil {
		return nil
	}
	return ts
}

// Clock-plausibility bounds (notes/concepts/alarm-concept.md §10.2). A restore
// only trusts wall-clock elapsed-time arithmetic when the current
// clock is not before the persisted timestamps (beyond a small skew
// tolerance) and not before the project epoch — the RTC-less-host
// case where the daemon starts before NTP sync. Under an implausible
// clock the engine falls back to the persisted remaining durations,
// which are relative and therefore trustworthy, and never
// auto-escalates or auto-completes off wall math.
const (
	// minPlausibleWallMS is 2026-01-01T00:00:00Z; any wall clock
	// before the project epoch is untrusted.
	minPlausibleWallMS = 1767225600000
	// clockSkewTolerance absorbs small backwards NTP corrections.
	clockSkewTolerance = 2 * time.Minute
)

// clockPlausible reports whether nowMS can be trusted for elapsed-time
// arithmetic against persistedAtMS.
func clockPlausible(nowMS, persistedAtMS int64) bool {
	if nowMS < minPlausibleWallMS {
		return false
	}
	if persistedAtMS > 0 && nowMS < persistedAtMS-clockSkewTolerance.Milliseconds() {
		return false
	}
	return true
}

// unixMS converts a time to epoch milliseconds.
func unixMS(t time.Time) int64 { return t.UnixMilli() }

// clockScheduler is the production TimerScheduler: one goroutine per
// scheduled callback, running on the injected clock seam so fake
// clocks drive it in tests and simulations.
type clockScheduler struct {
	clk clock.Clock
}

// NewClockScheduler returns a TimerScheduler running on clk.
func NewClockScheduler(clk clock.Clock) TimerScheduler {
	return &clockScheduler{clk: clk}
}

// Schedule implements TimerScheduler.
func (s *clockScheduler) Schedule(d time.Duration, fn func()) (cancel func()) {
	t := s.clk.NewTimer(d)
	stop := make(chan struct{})
	go func() {
		select {
		case <-t.C():
			fn()
		case <-stop:
			t.Stop()
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }) }
}
