// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// This file covers the always-on hazard/panic class (notes/concepts/alarm-concept.md
// §6.1/§7): sensors flagged AlwaysOn bypass the arm-state machine
// entirely, drive the panel to triggered from any state, and return to
// the state they interrupted (preTriggerState) once the trigger window
// elapses — never into the state machine's own retrigger/post-trigger
// arithmetic.

// alwaysOnCause is the subset of the persisted incident-cause document
// this file asserts on.
type alwaysOnCause struct {
	Kind     string `json:"kind"`
	SensorID string `json:"sensor_id"`
}

func TestAlwaysOn_HazardTriggersFromDisarmedIndependentOfArmState(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.seedSensor("hazard-1", "eg", hmenum.AlarmSensorTypeHazard, engine.SensorConfig{AlwaysOn: true})
	h.start()
	h.wantState("eg", hmenum.AlarmZoneStateDisarmed)

	h.eng.HandleSensorEvent(h.ctx, "hazard-1", true)

	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count = %d, want 1", n)
	}
	if fire := h.outputs.lastFire(t); fire.Opts.Policy.Silent {
		t.Fatalf("hazard policy = %+v, want loud (Silent=false) by default", fire.Opts.Policy)
	}

	inc, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("expected an open incident")
	}
	var cause alwaysOnCause
	if err := jsonUnmarshal(inc.CauseJSON, &cause); err != nil {
		t.Fatalf("unmarshal cause: %v", err)
	}
	if cause.Kind != "hazard" || cause.SensorID != "hazard-1" {
		t.Fatalf("cause = %+v, want kind=hazard sensor_id=hazard-1", cause)
	}
}

func TestAlwaysOn_PanicSensorHonorsPerSensorSilentFlag(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.seedSensor("panic-1", "eg", hmenum.AlarmSensorTypePanic, engine.SensorConfig{
		AlwaysOn: true, PanicSilent: true,
	})
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "panic-1", true)

	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	fire := h.outputs.lastFire(t)
	if !fire.Opts.Policy.Silent {
		t.Fatalf("panic policy = %+v, want Silent=true (per-sensor PanicSilent)", fire.Opts.Policy)
	}
	inc, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("expected an open incident")
	}
	var cause alwaysOnCause
	if err := jsonUnmarshal(inc.CauseJSON, &cause); err != nil {
		t.Fatalf("unmarshal cause: %v", err)
	}
	if cause.Kind != "panic" {
		t.Fatalf("cause = %+v, want kind=panic", cause)
	}
}

func TestAlwaysOn_ElapsedReturnsToTheStateItInterrupted(t *testing.T) {
	t.Run("disarmed before stays disarmed after", func(t *testing.T) {
		h := newHarness(t)
		h.seedStandardZone()
		h.seedSensor("hazard-1", "eg", hmenum.AlarmSensorTypeHazard, engine.SensorConfig{AlwaysOn: true})
		h.start()

		h.eng.HandleSensorEvent(h.ctx, "hazard-1", true)
		h.wantState("eg", hmenum.AlarmZoneStateTriggered)

		// The zone's mode was "disarmed" (no Modes entry) at trigger time,
		// so the trigger window falls back to the engine default.
		h.advance(engine.DefaultTriggerSeconds * time.Second)
		h.wantState("eg", hmenum.AlarmZoneStateDisarmed)
		if got := h.mustSnapshot("eg").Mode; got != hmenum.AlarmModeDisarmed {
			t.Fatalf("mode after the always-on episode = %s, want disarmed", got)
		}
		if n := h.outputs.stopCount(); n == 0 {
			t.Fatal("expected StopAll at the end of the always-on episode")
		}
		if _, ok := h.openIncident("eg"); ok {
			t.Fatal("incident should be closed once the always-on episode ends")
		}
	})

	t.Run("armed before resumes armed after with a recaptured baseline", func(t *testing.T) {
		h := newHarness(t)
		h.seedStandardZone()
		h.seedSensor("panic-1", "eg", hmenum.AlarmSensorTypePanic, engine.SensorConfig{AlwaysOn: true})
		h.start()
		h.armFull()

		h.eng.HandleSensorEvent(h.ctx, "panic-1", true)
		h.wantState("eg", hmenum.AlarmZoneStateTriggered)

		// Full mode's configured trigger time (defaultZoneConfig: 60s).
		h.advance(60 * time.Second)
		h.wantState("eg", hmenum.AlarmZoneStateArmed)
		if got := h.mustSnapshot("eg").Mode; got != hmenum.AlarmModeFull {
			t.Fatalf("mode after the always-on episode = %s, want full (resumed)", got)
		}
	})
}

func TestAlwaysOn_SilenceStopsOutputsWithoutEndingTheEpisodeEarly(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.seedSensor("hazard-1", "eg", hmenum.AlarmSensorTypeHazard, engine.SensorConfig{AlwaysOn: true})
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "hazard-1", true)
	inc, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("expected an open incident")
	}

	if err := h.eng.Silence(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("silence: %v", err)
	}
	if n := h.outputs.stopCount(); n < 1 {
		t.Fatalf("StopAll calls = %d, want >= 1", n)
	}
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	got, ok, err := h.incidents.Get(h.ctx, inc.ID)
	if err != nil || !ok || !got.Silenced {
		t.Fatalf("incident silenced = %+v (ok=%v err=%v)", got, ok, err)
	}

	// A silenced always-on incident does not re-trigger (unlike an
	// intrusion incident it never runs retrigger cycles at all); it
	// still returns to the interrupted state once the window elapses.
	h.advance(engine.DefaultTriggerSeconds * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateDisarmed)
}

func TestAlwaysOn_SecondActivationWhileTriggeredLayersOutputsOnTheSameIncident(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.seedSensor("hazard-1", "eg", hmenum.AlarmSensorTypeHazard, engine.SensorConfig{AlwaysOn: true})
	h.seedSensor("panic-1", "eg", hmenum.AlarmSensorTypePanic, engine.SensorConfig{AlwaysOn: true})
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "hazard-1", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	inc, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("expected an open incident")
	}
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count = %d, want 1", n)
	}

	h.eng.HandleSensorEvent(h.ctx, "panic-1", true)

	if n := h.outputs.fireCount(); n != 2 {
		t.Fatalf("FireCycle count after the second class activation = %d, want 2 (layered, not a new incident)", n)
	}
	if !h.journal.has("always_on_activation") {
		t.Fatalf("missing always_on_activation journal entry; got %v", h.journal.events())
	}
	inc2, ok := h.openIncident("eg")
	if !ok || inc2.ID != inc.ID {
		t.Fatalf("expected the same incident to remain open, got %+v (was %d)", inc2, inc.ID)
	}
}

func TestAlwaysOn_FiresDuringARunningWalkTest(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.seedSensor("hazard-1", "eg", hmenum.AlarmSensorTypeHazard, engine.SensorConfig{AlwaysOn: true})
	h.start()

	if err := h.eng.WalkTestStart(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("walk test start: %v", err)
	}

	h.eng.HandleSensorEvent(h.ctx, "hazard-1", true)

	// A real hazard must never be swallowed by a walk-test session.
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count = %d, want 1", n)
	}
}

func TestAlwaysOn_RestoreAfterWindowElapsedWhileDownReturnsToPreTriggerState(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.seedSensor("hazard-1", "eg", hmenum.AlarmSensorTypeHazard, engine.SensorConfig{AlwaysOn: true})
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "hazard-1", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	h.restart(engine.DefaultTriggerSeconds*time.Second + time.Minute)

	h.wantState("eg", hmenum.AlarmZoneStateDisarmed)
	if _, ok := h.openIncident("eg"); ok {
		t.Fatal("incident should be closed after the restore")
	}
}

func TestAlwaysOn_RestoreInsideTheWindowResumesWithTheClassPolicy(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.seedSensor("panic-1", "eg", hmenum.AlarmSensorTypePanic, engine.SensorConfig{AlwaysOn: true})
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "panic-1", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	h.restart(5 * time.Second)

	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	fire := h.outputs.lastFire(t)
	if fire.Opts.Policy.Silent {
		t.Fatalf("restored panic policy = %+v, want loud (default PanicOutputs)", fire.Opts.Policy)
	}
	if !fire.Opts.Restored {
		t.Fatal("expected the restore-driven fire to be marked Restored")
	}
}
