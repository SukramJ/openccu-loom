// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestZonePhasesStampTheDeclaredTimerKinds walks a zone through every
// phase that arms a state timer and pins the token the snapshot carries
// against the engine's exported vocabulary, plus whether that phase is a
// user-facing countdown ([engine.IsCountdownTimerKind]).
//
// The tokens are what a north-bound surface renders a countdown ring
// from. They were re-typed per surface, and the copies disagreed: a zone
// in its pre-alarm phase published kind "pre_alarm" over one surface and
// no countdown at all over another, against a DTO whose enum admits only
// the two delay kinds. This is the one place the phase, the token and
// the countdown decision are measured together.
func TestZonePhasesStampTheDeclaredTimerKinds(t *testing.T) {
	h := newHarness(t)
	seedPreAlarmZone(h)
	h.start()

	// Exit delay: full mode's 30s arming countdown.
	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{
		Mode: hmenum.AlarmModeFull, By: "tester", Source: "test",
	}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.wantState("eg", hmenum.AlarmZoneStateArming)
	hmAlmWantTimerKind(t, h, engine.TimerKindExit, true)

	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)

	// Pre-alarm: a phase timer on a zone the panel already shows as
	// triggered — never a countdown.
	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	hmAlmWantTimerKind(t, h, engine.TimerKindPreAlarm, false)

	// Trigger window: same, the incident's own bound.
	h.advance(10 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	hmAlmWantTimerKind(t, h, engine.TimerKindTrigger, false)
}

// TestEntryDelayStampsTheDeclaredTimerKind pins the second countdown
// kind, which needs a delayed door rather than the pre-alarm zone.
func TestEntryDelayStampsTheDeclaredTimerKind(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()
	h.armFull()

	// The delayed door starts the entry countdown instead of triggering.
	h.eng.HandleSensorEvent(h.ctx, "door", true)
	h.wantState("eg", hmenum.AlarmZoneStatePending)
	hmAlmWantTimerKind(t, h, engine.TimerKindEntry, true)
}

// hmAlmWantTimerKind asserts the zone's snapshot carries want as its
// active timer kind, and that the engine classifies that kind as a
// user-facing countdown exactly as wantCountdown says.
func hmAlmWantTimerKind(t *testing.T, h *harness, want string, wantCountdown bool) {
	t.Helper()
	snap := h.mustSnapshot("eg")
	if snap.TimerKind != want {
		t.Fatalf("snapshot timer kind = %q, want %q", snap.TimerKind, want)
	}
	if snap.TimerRemaining <= 0 {
		t.Fatalf("timer kind %q carries remaining = %s, want a running countdown", want, snap.TimerRemaining)
	}
	if got := engine.IsCountdownTimerKind(snap.TimerKind); got != wantCountdown {
		t.Errorf("IsCountdownTimerKind(%q) = %v, want %v — a surface renders this phase as a "+
			"countdown ring exactly when this is true", snap.TimerKind, got, wantCountdown)
	}
}
