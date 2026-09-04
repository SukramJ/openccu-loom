// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
)

// TestAlarmCountdownTotalComesFromTheEngine pins the countdown's total to the
// length the engine armed the timer with.
//
// The handler used to read the zone's mode configuration back from the store
// and take EntryDelaySeconds. That is the zone default — the engine arms
// ModeConfig.entryDelay(sensor), which honours a per-sensor override. So a
// zone configured for 30 s with a sensor overriding to 90 s reported a total
// of 30 while counting down from 90: the progress bar a client draws from the
// two runs at the wrong rate, or shows a countdown already past its end.
func TestAlarmCountdownTotalComesFromTheEngine(t *testing.T) {
	t.Parallel()
	snap := engine.ZoneSnapshot{
		ID:             "zone-1",
		TimerKind:      engine.TimerKindEntry,
		TimerRemaining: 75 * time.Second,
		// What the engine armed: the sensor's override, not the zone's 30 s.
		TimerTotal: 90 * time.Second,
	}
	got := alarmCountdown(snap)
	if got == nil {
		t.Fatal("no countdown for a running entry timer")
	}
	if got.TotalS != 90 {
		t.Errorf("total = %d, want the engine's 90", got.TotalS)
	}
	if got.RemainingS != 75 {
		t.Errorf("remaining = %d, want 75", got.RemainingS)
	}
}

// TestAlarmCountdownTotalNeverBelowRemaining keeps the degradation rule: a
// snapshot without a total must not report a countdown that is already past
// its end.
func TestAlarmCountdownTotalNeverBelowRemaining(t *testing.T) {
	t.Parallel()
	got := alarmCountdown(engine.ZoneSnapshot{
		ID: "zone-1", TimerKind: engine.TimerKindExit, TimerRemaining: 20 * time.Second,
	})
	if got == nil {
		t.Fatal("no countdown for a running exit timer")
	}
	if got.TotalS != got.RemainingS {
		t.Errorf("total = %d, remaining = %d — a missing total must degrade to remaining", got.TotalS, got.RemainingS)
	}
}
