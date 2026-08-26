// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// This file covers sensor and central health: tamper, availability,
// sabotage/low-battery blocker policies, central-connectivity
// policies, readiness events, and allow-open-after-arming.

// readinessEvents filters the harness's published events for
// AlarmReadinessChangedEvent, in publish order.
func readinessEvents(h *harness) []hmevent.AlarmReadinessChangedEvent {
	h.sink.mu.Lock()
	defer h.sink.mu.Unlock()
	var out []hmevent.AlarmReadinessChangedEvent
	for _, e := range h.sink.events {
		if r, ok := e.(hmevent.AlarmReadinessChangedEvent); ok {
			out = append(out, r)
		}
	}
	return out
}

func TestHealth_TamperWhileDisarmedIsJournaled(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.seedSensor("tamper1", "eg", hmenum.AlarmSensorTypeTamper, engine.SensorConfig{})
	h.start()
	h.wantState("eg", hmenum.AlarmZoneStateDisarmed)

	h.eng.HandleSensorEvent(h.ctx, "tamper1", true)
	if !h.journal.has("tamper_while_disarmed") {
		t.Fatalf("missing tamper_while_disarmed journal entry; got %v", h.journal.events())
	}
}

func TestHealth_UnavailabilityWarnsWhileArmedByDefault(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()
	h.armFull()

	h.eng.SetSensorAvailability(h.ctx, "window", false)
	if !h.journal.has("sensor_unavailable_while_armed") {
		t.Fatalf("missing sensor_unavailable_while_armed journal entry; got %v", h.journal.events())
	}
	h.wantState("eg", hmenum.AlarmZoneStateArmed)
}

func TestHealth_UnavailabilityTriggersWhenFlagged(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.seedSensor("flaky", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes:                  []hmenum.AlarmMode{hmenum.AlarmModeFull},
		TriggerWhenUnavailable: true,
	})
	h.start()
	h.armFull()

	h.eng.SetSensorAvailability(h.ctx, "flaky", false)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
}

func TestHealth_SabotageBlocksArm(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	h.eng.SetSensorHealth(h.ctx, "window", engine.SensorHealth{Sabotage: true})
	if !h.journal.has("sensor_sabotage") {
		t.Fatalf("missing sensor_sabotage journal entry; got %v", h.journal.events())
	}

	_, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"})
	var nre *engine.NotReadyError
	if !errors.As(err, &nre) {
		t.Fatalf("err = %v, want *engine.NotReadyError", err)
	}
	if got := sortedStrings(nre.Blockers); len(got) != 1 || got[0] != "window" {
		t.Fatalf("blockers = %v, want [window]", got)
	}
}

func TestHealth_LowBatteryWarnsWithoutBlockingArm(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	h.eng.SetSensorHealth(h.ctx, "window", engine.SensorHealth{LowBattery: true})

	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}

	events := readinessEvents(h)
	if len(events) == 0 {
		t.Fatal("expected at least one readiness event")
	}
	last := events[len(events)-1]
	full, ok := last.Readiness[hmenum.AlarmModeFull]
	if !ok {
		t.Fatalf("readiness missing full mode: %+v", last.Readiness)
	}
	if !full.Ready {
		t.Fatal("full.Ready = false, want true (low battery only warns)")
	}
	if got := sortedStrings(full.Warnings); len(got) != 1 || got[0] != "window" {
		t.Fatalf("full.Warnings = %v, want [window]", got)
	}
}

func TestHealth_CentralLossAlertKeepsArmedAndJournals(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()
	h.armFull()

	h.eng.HandleCentralConnectivity(h.ctx, "ccu-test", false)
	if !h.journal.has("central_lost_while_armed") {
		t.Fatalf("missing central_lost_while_armed journal entry; got %v", h.journal.events())
	}
	h.wantState("eg", hmenum.AlarmZoneStateArmed)

	h.eng.HandleCentralConnectivity(h.ctx, "ccu-test", true)
	if !h.journal.has("central_restored") {
		t.Fatalf("missing central_restored journal entry; got %v", h.journal.events())
	}
}

func TestHealth_CentralLossTriggerPolicyTriggers(t *testing.T) {
	h := newHarness(t)
	cfg := defaultZoneConfig()
	cfg.CentralLoss = hmenum.AlarmCentralLossTrigger
	h.seedZone("eg", "Erdgeschoss", cfg)
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	h.start()
	h.armFull()

	h.eng.HandleCentralConnectivity(h.ctx, "ccu-test", false)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
}

func TestHealth_ReadinessEventReflectsSensorOpenAndClose(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	snap := h.mustSnapshot("eg")
	if r, ok := snap.Readiness[hmenum.AlarmModeFull]; !ok || !r.Ready {
		t.Fatalf("initial full readiness = %+v, want ready", r)
	}

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	events := readinessEvents(h)
	if len(events) == 0 {
		t.Fatal("expected a readiness event after opening window")
	}
	last := events[len(events)-1]
	for _, mode := range []hmenum.AlarmMode{hmenum.AlarmModeFull, hmenum.AlarmModePerimeter} {
		r, ok := last.Readiness[mode]
		if !ok {
			t.Fatalf("readiness missing mode %s: %+v", mode, last.Readiness)
		}
		if r.Ready {
			t.Fatalf("%s.Ready = true, want false while window is open", mode)
		}
		if got := sortedStrings(r.Blockers); len(got) != 1 || got[0] != "window" {
			t.Fatalf("%s.Blockers = %v, want [window]", mode, got)
		}
	}

	h.eng.HandleSensorEvent(h.ctx, "window", false)
	events = readinessEvents(h)
	last = events[len(events)-1]
	for _, mode := range []hmenum.AlarmMode{hmenum.AlarmModeFull, hmenum.AlarmModePerimeter} {
		r, ok := last.Readiness[mode]
		if !ok {
			t.Fatalf("readiness missing mode %s: %+v", mode, last.Readiness)
		}
		if !r.Ready {
			t.Fatalf("%s.Ready = false after closing window, want true", mode)
		}
	}
}

func TestHealth_AllowOpenAfterArmingOnlyTriggersOnReactivation(t *testing.T) {
	h := newHarness(t)
	h.seedZone("eg", "Erdgeschoss", defaultZoneConfig())
	h.seedSensor("stayopen", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes:                []hmenum.AlarmMode{hmenum.AlarmModeFull},
		AllowOpenAfterArming: true,
	})
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "stayopen", true)
	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)

	h.eng.HandleSensorEvent(h.ctx, "stayopen", false)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)

	h.eng.HandleSensorEvent(h.ctx, "stayopen", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
}
