// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
)

// TestCountdownKindsPublishedAreOnlyTheDeclaredTwo pins that every timer kind
// this plane can put into an AlarmCountdown is one the contract admits.
//
// The DTO's `kind` is constrained to exit_delay / entry_delay by
// assets/openapi.yaml and typed as those two in the SPA. The REST handler
// filtered to them; this plane published whatever the engine stamped, so a
// zone in pre-alarm, in trigger or waiting to auto-rearm sent a kind no client
// declares — three of the engine's five kinds.
func TestCountdownKindsPublishedAreOnlyTheDeclaredTwo(t *testing.T) {
	h := newAlarmPanelHarness(t)

	for _, kind := range []string{
		engine.TimerKindExit, engine.TimerKindEntry,
		engine.TimerKindTrigger, engine.TimerKindPreAlarm, engine.TimerKindAutoRearm,
	} {
		// Through the plane's own renderer, not through the predicate: a test
		// that asked engine.IsCountdownTimerKind directly would pass whether
		// or not this file consults it.
		st := alarmZoneStatus(h.eng, engine.ZoneSnapshot{
			ID: "z1", TimerKind: kind, TimerRemaining: 30 * time.Second,
		})
		contractAdmits := kind == engine.TimerKindExit || kind == engine.TimerKindEntry
		published := st.Countdown != nil
		if published != contractAdmits {
			t.Errorf("%s: this plane publishes a countdown=%v, the contract admits the kind=%v",
				kind, published, contractAdmits)
		}
		if published && st.Countdown.Kind != kind {
			t.Errorf("%s: published kind = %q", kind, st.Countdown.Kind)
		}
	}
}
