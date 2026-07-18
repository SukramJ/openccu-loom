// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// This file covers auto-rearm (docs/alarm-concept.md §15 row 22): after
// a post-trigger disarm, the area re-arms to its pre-incident mode
// after a quiet period; member-sensor activity resets the countdown; a
// blocked rearm attempt stays disarmed with a fail-visible journal
// entry; and both flavors of the restart-restore table (remaining
// countdown resumed, window elapsed while down) apply.

// autoRearmAreaConfig disarms after a trigger episode and re-arms 30s
// later once the area has been quiet.
func autoRearmAreaConfig() engine.AreaConfig {
	cfg := defaultAreaConfig()
	cfg.PostTrigger = hmenum.AlarmPostTriggerDisarm
	cfg.AutoRearmSeconds = 30
	return cfg
}

func seedAutoRearmArea(h *harness) {
	h.t.Helper()
	h.seedArea("eg", "Erdgeschoss", autoRearmAreaConfig())
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
}

// triggerAndDisarm arms full, trips the window sensor, advances past the
// trigger window so the area lands in the post-trigger disarmed state
// with an auto-rearm timer freshly scheduled, and settles the sensor
// closed again — a still-open window is a readiness blocker in its own
// right (default Open policy) and would confound the auto-rearm
// assertions below, which are about the quiet-period timer, not sensor
// readiness.
func triggerAndDisarm(h *harness) {
	h.t.Helper()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmAreaStateTriggered)
	h.advance(60 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)
	h.eng.HandleSensorEvent(h.ctx, "window", false)
}

func TestAutoRearm_SchedulesAfterPostTriggerDisarmAndRearmsAfterTheQuietPeriod(t *testing.T) {
	h := newHarness(t)
	seedAutoRearmArea(h)
	h.start()
	triggerAndDisarm(h)

	if !h.journal.has("auto_rearm_scheduled") {
		t.Fatalf("missing auto_rearm_scheduled journal entry; got %v", h.journal.events())
	}

	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateArming) // full's own exit delay is now running
	if !h.journal.has("auto_rearmed") {
		t.Fatalf("missing auto_rearmed journal entry; got %v", h.journal.events())
	}

	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
	if got := h.mustSnapshot("eg").Mode; got != hmenum.AlarmModeFull {
		t.Fatalf("auto-rearm mode = %s, want full", got)
	}
}

func TestAutoRearm_MemberActivityResetsTheQuietPeriod(t *testing.T) {
	h := newHarness(t)
	seedAutoRearmArea(h)
	h.start()
	triggerAndDisarm(h)

	h.advance(20 * time.Second)
	h.eng.HandleSensorEvent(h.ctx, "window", true) // fresh activity while disarmed
	if !h.journal.has("auto_rearm_deferred") {
		t.Fatalf("missing auto_rearm_deferred journal entry; got %v", h.journal.events())
	}
	h.eng.HandleSensorEvent(h.ctx, "window", false) // settles closed again

	// The original 30s deadline (10s away) has passed, but the timer
	// was pushed back by the activity.
	h.advance(10 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)

	h.advance(20 * time.Second)                    // the deferred 30s window from the activity
	h.wantState("eg", hmenum.AlarmAreaStateArming) // full's own exit delay is now running
	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
}

func TestAutoRearm_ExplicitDisarmCancelsAPendingRearm(t *testing.T) {
	h := newHarness(t)
	seedAutoRearmArea(h)
	h.start()
	triggerAndDisarm(h)

	if err := h.eng.Disarm(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("explicit disarm: %v", err)
	}
	if !h.journal.has("auto_rearm_cancelled") {
		t.Fatalf("missing auto_rearm_cancelled journal entry; got %v", h.journal.events())
	}

	h.advance(time.Minute)
	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)
}

func TestAutoRearm_FreshArmSupersedesAPendingRearm(t *testing.T) {
	h := newHarness(t)
	seedAutoRearmArea(h)
	h.start()
	triggerAndDisarm(h)

	h.armFull()

	// The superseded auto-rearm timer must not fire a second, redundant
	// arm attempt later.
	before := len(h.journal.events())
	h.advance(time.Minute)
	if after := len(h.journal.events()); after != before {
		t.Fatalf("journal grew from %d to %d entries after the superseded auto-rearm window", before, after)
	}
}

func TestAutoRearm_BlockedAtElapseStaysDisarmedAndJournalsFailedToArm(t *testing.T) {
	h := newHarness(t)
	seedAutoRearmArea(h)
	h.start()
	triggerAndDisarm(h)

	// A sabotage flag blocks arm readiness without touching activation
	// or deferring the quiet period (a health signal, not member
	// activity).
	h.eng.SetSensorHealth(h.ctx, "window", engine.SensorHealth{Sabotage: true})

	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)
	if !h.journal.has("failed_to_arm") {
		t.Fatalf("missing failed_to_arm journal entry; got %v", h.journal.events())
	}
}

func TestAutoRearm_RestoreResumesTheRemainingQuietPeriod(t *testing.T) {
	h := newHarness(t)
	seedAutoRearmArea(h)
	h.start()
	triggerAndDisarm(h)

	h.restart(10 * time.Second) // 20s of the 30s quiet period remain
	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)
	if !h.journal.has("auto_rearm_resumed") {
		t.Fatalf("missing auto_rearm_resumed journal entry; got %v", h.journal.events())
	}

	// The quiet period elapses, then full mode's own 30s exit delay
	// completes the arm.
	h.advance(20 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateArming)
	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
}

func TestAutoRearm_RestoreElapsedWhileDownRearmsImmediately(t *testing.T) {
	h := newHarness(t)
	seedAutoRearmArea(h)
	h.start()
	triggerAndDisarm(h)

	h.restart(time.Minute)                         // the whole 30s quiet period elapsed while down
	h.wantState("eg", hmenum.AlarmAreaStateArming) // beginArm ran; full's own exit delay is now running
	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
}
