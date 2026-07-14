// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// This file covers the pending/triggered side of the state machine:
// instant vs. entry-delayed activation, escalation while pending,
// mode-scoped and bypassed sensors, bounded re-trigger cycles, the
// post-trigger policies, and the per-sensor entry-delay override.

func TestTrigger_InstantSensorOpensIncidentAndFiresOnce(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)

	h.wantState("eg", hmenum.AlarmAreaStateTriggered)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count = %d, want 1", n)
	}
	if fire := h.outputs.lastFire(t); fire.Opts.Cycle != 0 {
		t.Fatalf("Opts.Cycle = %d, want 0", fire.Opts.Cycle)
	}

	triggered := h.sink.triggered()
	if len(triggered) != 1 || triggered[0].SensorID != "window" {
		t.Fatalf("triggered events = %+v, want one entry with SensorID window", triggered)
	}

	inc, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("expected an open incident")
	}
	if inc.Mode != hmenum.AlarmModeFull {
		t.Fatalf("incident mode = %s, want full", inc.Mode)
	}
}

func TestTrigger_EntryDelaySensorGoesPendingThenTriggers(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "door", true)
	h.wantState("eg", hmenum.AlarmAreaStatePending)
	if !h.journal.has("pending_started") {
		t.Fatalf("missing pending_started journal entry; got %v", h.journal.events())
	}
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("FireCycle count = %d, want 0 while pending", n)
	}

	h.advance(15 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateTriggered)
	if !h.journal.has("triggered") {
		t.Fatalf("missing triggered journal entry; got %v", h.journal.events())
	}
}

func TestTrigger_DisarmDuringPendingNeverOpensIncident(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "door", true)
	h.wantState("eg", hmenum.AlarmAreaStatePending)

	if err := h.eng.Disarm(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("disarm: %v", err)
	}
	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)

	h.advance(2 * time.Hour)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("FireCycle count = %d, want 0", n)
	}
	if _, ok := h.openIncident("eg"); ok {
		t.Fatal("no incident should ever have been created")
	}
}

func TestTrigger_InstantSensorEscalatesPendingImmediately(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "door", true)
	h.wantState("eg", hmenum.AlarmAreaStatePending)

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmAreaStateTriggered)
}

func TestTrigger_ModeScopedSensorIgnoredOutsideItsMode(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()

	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModePerimeter, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.wantState("eg", hmenum.AlarmAreaStateArmed)

	// motion only participates in full; perimeter must ignore it.
	h.eng.HandleSensorEvent(h.ctx, "motion", true)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("FireCycle count = %d, want 0", n)
	}
}

func TestTrigger_BypassedSensorNeverTriggersUntilDisarm(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, Force: true, By: "tester"}); err != nil {
		t.Fatalf("force arm: %v", err)
	}
	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)

	h.eng.HandleSensorEvent(h.ctx, "window", false)
	h.eng.HandleSensorEvent(h.ctx, "window", true)

	h.wantState("eg", hmenum.AlarmAreaStateArmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("FireCycle count = %d, want 0 (bypass holds until disarm)", n)
	}
}

func TestTrigger_RetriggerCyclesBoundedByMaxThenReturnsToArmed(t *testing.T) {
	h := newHarness(t)
	cfg := defaultAreaConfig()
	full := cfg.Modes[hmenum.AlarmModeFull]
	full.MaxRetriggerCycles = 2
	cfg.Modes[hmenum.AlarmModeFull] = full
	h.seedArea("eg", "Erdgeschoss", cfg)
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmAreaStateTriggered)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count after initial trigger = %d, want 1", n)
	}

	h.advance(60 * time.Second)
	if n := h.outputs.fireCount(); n != 2 {
		t.Fatalf("FireCycle count after 1st retrigger = %d, want 2", n)
	}
	if fire := h.outputs.lastFire(t); fire.Opts.Cycle != 1 {
		t.Fatalf("Opts.Cycle = %d, want 1", fire.Opts.Cycle)
	}
	if !h.journal.has("retrigger_cycle") {
		t.Fatalf("missing retrigger_cycle journal entry; got %v", h.journal.events())
	}

	h.advance(60 * time.Second)
	if n := h.outputs.fireCount(); n != 3 {
		t.Fatalf("FireCycle count after 2nd retrigger = %d, want 3", n)
	}
	if fire := h.outputs.lastFire(t); fire.Opts.Cycle != 2 {
		t.Fatalf("Opts.Cycle = %d, want 2", fire.Opts.Cycle)
	}

	h.advance(60 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
	if n := h.outputs.fireCount(); n != 3 {
		t.Fatalf("FireCycle count after post-trigger = %d, want 3 (no further fire)", n)
	}
	if n := h.outputs.stopCount(); n == 0 {
		t.Fatal("expected StopAll to be called at post-trigger")
	}
	if _, ok := h.openIncident("eg"); ok {
		t.Fatal("incident should be closed after post-trigger")
	}
}

func TestTrigger_PostTriggerDisarmPolicyDisarmsAfterWindow(t *testing.T) {
	h := newHarness(t)
	cfg := defaultAreaConfig()
	cfg.PostTrigger = hmenum.AlarmPostTriggerDisarm
	h.seedArea("eg", "Erdgeschoss", cfg)
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmAreaStateTriggered)

	h.advance(60 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)
	if !h.journal.has("disarmed_post_trigger") {
		t.Fatalf("missing disarmed_post_trigger journal entry; got %v", h.journal.events())
	}
}

func TestTrigger_EntryDelayOverrideReplacesModeDefault(t *testing.T) {
	h := newHarness(t)
	override := 5
	h.seedArea("eg", "Erdgeschoss", defaultAreaConfig())
	h.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{
		Modes:                     []hmenum.AlarmMode{hmenum.AlarmModeFull},
		UseEntryDelay:             true,
		EntryDelayOverrideSeconds: &override,
	})
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "door", true)
	h.wantState("eg", hmenum.AlarmAreaStatePending)

	// The mode's own entry delay is 15 s; the sensor override is 5 s.
	h.advance(4 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStatePending)

	h.advance(1 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateTriggered)
}
