// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestWalkTest_AbortedByArmDoesNotSurviveDisarmCycle pins that a
// walk-test session left running by mistake cannot outlive an
// arm/disarm cycle. Without an abort at arm time, the session stays
// open through the whole armed period and, once the zone disarms
// again, silently resumes consuming every sensor activation as
// walk-test progress instead of real disarmed-state handling (the
// door chime here).
func TestWalkTest_AbortedByArmDoesNotSurviveDisarmCycle(t *testing.T) {
	h := newHarness(t)
	h.seedZone("eg", "Erdgeschoss", defaultZoneConfig())
	h.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
		Chime: true,
	})
	h.start()

	if err := h.eng.WalkTestStart(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("walk test start: %v", err)
	}

	h.armFull()

	status, err := h.eng.WalkTestStatus("eg")
	if err != nil {
		t.Fatalf("walk test status: %v", err)
	}
	if status.Active {
		t.Fatalf("walk test still active after arm: %+v", status)
	}
	if !h.journal.has("walktest_aborted_by_arm") {
		t.Fatalf("missing walktest_aborted_by_arm journal entry; got %v", h.journal.events())
	}

	if err := h.eng.Disarm(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("disarm: %v", err)
	}
	h.wantState("eg", hmenum.AlarmZoneStateDisarmed)

	h.eng.HandleSensorEvent(h.ctx, "door", true)

	if !hasChirp(h.outputs, engine.ChirpChime) {
		t.Fatalf("chime did not fire after the walk test survived an arm/disarm cycle; chirps = %v", chirpKinds(h.outputs))
	}
}
