// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// This file covers the per-sensor noise filters ahead of the arm-state
// machine (docs/alarm-concept.md §6.2): the hold-time debounce and the
// cross-zoning group rule, plus how the two compose.

// seedHoldSensor seeds zone "eg" with one instant motion sensor carrying
// a hold-time debounce.
func seedHoldSensor(h *harness, id string, holdSeconds int) {
	h.t.Helper()
	h.seedZone("eg", "Erdgeschoss", defaultZoneConfig())
	h.seedSensor(id, "eg", hmenum.AlarmSensorTypeMotion, engine.SensorConfig{
		Modes:           []hmenum.AlarmMode{hmenum.AlarmModeFull},
		HoldTimeSeconds: holdSeconds,
	})
}

// seedCrossZoneArea seeds zone "eg" with two instant motion sensors that
// share a cross-zoning group.
func seedCrossZoneArea(h *harness, group string) {
	h.t.Helper()
	h.seedZone("eg", "Erdgeschoss", defaultZoneConfig())
	h.seedSensor("motion-a", "eg", hmenum.AlarmSensorTypeMotion, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
		Group: group,
	})
	h.seedSensor("motion-b", "eg", hmenum.AlarmSensorTypeMotion, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
		Group: group,
	})
}

func TestHoldTime_ClearingBeforeTheWindowDiscardsTheActivation(t *testing.T) {
	h := newHarness(t)
	seedHoldSensor(h, "motion", 5)
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "motion", true)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)

	h.eng.HandleSensorEvent(h.ctx, "motion", false)

	h.advance(6 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("FireCycle count = %d, want 0 (cleared before the hold window elapsed)", n)
	}
}

func TestHoldTime_StandingActivationTriggersAfterTheWindow(t *testing.T) {
	h := newHarness(t)
	seedHoldSensor(h, "motion", 5)
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "motion", true)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)

	h.advance(5 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count = %d, want 1", n)
	}
}

func TestHoldTime_DoesNotDelayAlwaysOnSensors(t *testing.T) {
	h := newHarness(t)
	h.seedZone("eg", "Erdgeschoss", defaultZoneConfig())
	h.seedSensor("hazard-1", "eg", hmenum.AlarmSensorTypeHazard, engine.SensorConfig{
		AlwaysOn: true, HoldTimeSeconds: 30,
	})
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "hazard-1", true)

	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count = %d, want 1 (always-on bypasses hold time entirely)", n)
	}
}

func TestCrossZone_SingleHitIsSuppressedAndJournaled(t *testing.T) {
	h := newHarness(t)
	seedCrossZoneArea(h, "gz")
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "motion-a", true)

	h.wantState("eg", hmenum.AlarmZoneStateArmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("FireCycle count = %d, want 0 (single group member never fires alone)", n)
	}
	if !h.journal.has("cross_zone_first_hit") {
		t.Fatalf("missing cross_zone_first_hit journal entry; got %v", h.journal.events())
	}
}

func TestCrossZone_SecondDistinctMemberWithinTheWindowTriggers(t *testing.T) {
	h := newHarness(t)
	seedCrossZoneArea(h, "gz")
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "motion-a", true)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)

	h.advance(5 * time.Second)
	h.eng.HandleSensorEvent(h.ctx, "motion-b", true)

	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count = %d, want 1", n)
	}
}

func TestCrossZone_SameSensorTwiceDoesNotTrigger(t *testing.T) {
	h := newHarness(t)
	seedCrossZoneArea(h, "gz")
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "motion-a", true)
	h.eng.HandleSensorEvent(h.ctx, "motion-a", false)

	h.advance(5 * time.Second)
	h.eng.HandleSensorEvent(h.ctx, "motion-a", true)

	h.wantState("eg", hmenum.AlarmZoneStateArmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("FireCycle count = %d, want 0 (same sensor twice is not a second distinct member)", n)
	}
}

func TestCrossZone_WindowExpiryTreatsTheNextHitAsFirst(t *testing.T) {
	h := newHarness(t)
	seedCrossZoneArea(h, "gz")
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "motion-a", true)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)

	h.advance(61 * time.Second)
	h.eng.HandleSensorEvent(h.ctx, "motion-b", true)

	h.wantState("eg", hmenum.AlarmZoneStateArmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("FireCycle count = %d, want 0 (motion-a's hit expired outside the 60s window)", n)
	}
	if !h.journal.has("cross_zone_first_hit") {
		t.Fatalf("missing cross_zone_first_hit journal entry for the fresh hit; got %v", h.journal.events())
	}
}

func TestHoldTime_ComposesWithCrossZoneGroup(t *testing.T) {
	h := newHarness(t)
	h.seedZone("eg", "Erdgeschoss", defaultZoneConfig())
	h.seedSensor("motion-a", "eg", hmenum.AlarmSensorTypeMotion, engine.SensorConfig{
		Modes:           []hmenum.AlarmMode{hmenum.AlarmModeFull},
		HoldTimeSeconds: 5,
		Group:           "gz",
	})
	h.seedSensor("motion-b", "eg", hmenum.AlarmSensorTypeMotion, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
		Group: "gz",
	})
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "motion-a", true)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)

	// motion-a's hold elapses and becomes the group's first hit.
	h.advance(5 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)
	if !h.journal.has("cross_zone_first_hit") {
		t.Fatalf("missing cross_zone_first_hit journal entry; got %v", h.journal.events())
	}

	h.eng.HandleSensorEvent(h.ctx, "motion-b", true)

	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count = %d, want 1", n)
	}
}
