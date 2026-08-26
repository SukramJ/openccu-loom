// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// This file covers the two-phase pre-alarm (notes/concepts/alarm-concept.md §15
// row 21): a fresh, live trigger with PreAlarmSeconds configured first
// runs a pre-alarm-only output cycle, then escalates to the full policy
// on elapse; a silence during the pre-alarm phase cancels the
// escalation; a restore mid pre-alarm phase re-enters as a fresh full
// trigger, never the pre-alarm phase itself.

// preAlarmZoneConfig is the standard test zone with a 10s pre-alarm
// phase ahead of full's 60s trigger window.
func preAlarmZoneConfig() engine.ZoneConfig {
	cfg := defaultZoneConfig()
	full := cfg.Modes[hmenum.AlarmModeFull]
	full.PreAlarmSeconds = 10
	full.TriggerSeconds = 60
	cfg.Modes[hmenum.AlarmModeFull] = full
	return cfg
}

func seedPreAlarmZone(h *harness) {
	h.t.Helper()
	h.seedZone("eg", "Erdgeschoss", preAlarmZoneConfig())
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
}

func TestPreAlarm_TwoPhaseEscalatesToTheFullPolicyAfterTheWindow(t *testing.T) {
	h := newHarness(t)
	seedPreAlarmZone(h)
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count in the pre-alarm phase = %d, want 1", n)
	}
	if fire := h.outputs.lastFire(t); !fire.Opts.PreAlarm {
		t.Fatalf("Opts.PreAlarm = %v, want true for the first phase", fire.Opts.PreAlarm)
	}
	if !h.journal.has("pre_alarm_started") {
		t.Fatalf("missing pre_alarm_started journal entry; got %v", h.journal.events())
	}

	h.advance(10 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if n := h.outputs.fireCount(); n != 2 {
		t.Fatalf("FireCycle count after escalation = %d, want 2", n)
	}
	if fire := h.outputs.lastFire(t); fire.Opts.PreAlarm {
		t.Fatalf("Opts.PreAlarm = %v, want false for the escalated full phase", fire.Opts.PreAlarm)
	}
	if !h.journal.has("pre_alarm_escalated") {
		t.Fatalf("missing pre_alarm_escalated journal entry; got %v", h.journal.events())
	}

	h.advance(60 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)
}

func TestPreAlarm_SilenceDuringThePreAlarmPhaseCancelsTheFullEscalation(t *testing.T) {
	h := newHarness(t)
	seedPreAlarmZone(h)
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	if err := h.eng.Silence(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("silence: %v", err)
	}

	h.advance(10 * time.Second)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count = %d, want 1 (no full-phase escalation after a pre-alarm silence)", n)
	}
	if !h.journal.has("pre_alarm_silenced") {
		t.Fatalf("missing pre_alarm_silenced journal entry; got %v", h.journal.events())
	}
	h.wantState("eg", hmenum.AlarmZoneStateArmed)
	if _, ok := h.openIncident("eg"); ok {
		t.Fatal("incident should be closed once the pre-alarm phase cancels")
	}
}

func TestPreAlarm_RestoreDuringThePhaseEscalatesAsAFreshFullTriggerConservatively(t *testing.T) {
	h := newHarness(t)
	seedPreAlarmZone(h)
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	h.restart(2 * time.Second) // still inside the 10s pre-alarm window

	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if !h.journal.has("pre_alarm_restored_as_full") {
		t.Fatalf("missing pre_alarm_restored_as_full journal entry; got %v", h.journal.events())
	}
	fire := h.outputs.lastFire(t)
	if fire.Opts.PreAlarm {
		t.Fatalf("Opts.PreAlarm = %v, want false — a restored pre-alarm never re-enters the pre-alarm phase", fire.Opts.PreAlarm)
	}
	if fire.Opts.Policy.Silent {
		t.Fatalf("restored policy = %+v, want the full mode's policy (loud)", fire.Opts.Policy)
	}
	if !fire.Opts.Restored {
		t.Fatal("expected the restore-driven fire to be marked Restored")
	}

	// The fresh window is the mode's full TriggerSeconds (60s), not the
	// remaining pre-alarm time.
	h.advance(59 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	h.advance(1 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)
}

func TestPreAlarm_ZeroSecondsSkipsThePreAlarmPhase(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone() // full mode has no PreAlarmSeconds configured
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)

	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if fire := h.outputs.lastFire(t); fire.Opts.PreAlarm {
		t.Fatalf("Opts.PreAlarm = %v, want false when PreAlarmSeconds is unset", fire.Opts.PreAlarm)
	}
	if h.journal.has("pre_alarm_started") {
		t.Fatal("did not expect a pre_alarm_started journal entry")
	}
}
