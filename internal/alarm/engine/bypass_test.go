// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine_test

import (
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// seedAutoBypassZone seeds zone "ab" with one instant sensor flagged
// bypass_auto and one plain instant sensor.
func seedAutoBypassZone(h *harness) {
	h.t.Helper()
	h.seedZone("ab", "Anbau", engine.ZoneConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{
			hmenum.AlarmModeFull: {TriggerSeconds: 60},
		},
	})
	h.seedSensor("flaky", "ab", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes:      []hmenum.AlarmMode{hmenum.AlarmModeFull},
		BypassAuto: true,
	})
	h.seedSensor("solid", "ab", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
}

func TestArm_BypassAutoRecordsExclusionUntilDisarm(t *testing.T) {
	h := newHarness(t)
	seedAutoBypassZone(h)
	h.start()

	// The flagged sensor is open at arm time: the arm succeeds
	// without force, and the exclusion is recorded — never silent.
	h.eng.HandleSensorEvent(h.ctx, "flaky", true)
	res, err := h.eng.Arm(h.ctx, "ab", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"})
	if err != nil {
		t.Fatalf("arm with open bypass_auto sensor: %v", err)
	}
	if got := sortedStrings(res.Bypassed); len(got) != 1 || got[0] != "flaky" {
		t.Fatalf("Bypassed = %v, want [flaky]", got)
	}
	if !h.journal.has("sensor_bypassed") {
		t.Fatalf("missing bypass journal entry; got %v", h.journal.events())
	}
	h.wantState("ab", hmenum.AlarmZoneStateArmed)

	// Excluded until the next disarm: close + reopen must not trigger.
	h.eng.HandleSensorEvent(h.ctx, "flaky", false)
	h.eng.HandleSensorEvent(h.ctx, "flaky", true)
	h.wantState("ab", hmenum.AlarmZoneStateArmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("auto-bypassed sensor fired outputs: %d", n)
	}

	// After disarm + re-arm with the sensor closed, it is live again.
	if err := h.eng.Disarm(h.ctx, "ab", "tester", "test"); err != nil {
		t.Fatalf("disarm: %v", err)
	}
	h.eng.HandleSensorEvent(h.ctx, "flaky", false)
	if _, err := h.eng.Arm(h.ctx, "ab", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("re-arm: %v", err)
	}
	h.eng.HandleSensorEvent(h.ctx, "flaky", true)
	h.wantState("ab", hmenum.AlarmZoneStateTriggered)
}

func TestArm_BypassAutoDoesNotCoverOtherBlockers(t *testing.T) {
	h := newHarness(t)
	seedAutoBypassZone(h)
	h.start()

	// Both sensors open: the plain one still blocks the arm.
	h.eng.HandleSensorEvent(h.ctx, "flaky", true)
	h.eng.HandleSensorEvent(h.ctx, "solid", true)
	_, err := h.eng.Arm(h.ctx, "ab", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"})
	var nre *engine.NotReadyError
	if !errors.As(err, &nre) {
		t.Fatalf("err = %v, want *NotReadyError", err)
	}
	if len(nre.Blockers) != 1 || nre.Blockers[0] != "solid" {
		t.Fatalf("Blockers = %v, want [solid]", nre.Blockers)
	}
}

func TestRestore_ArmingCompletionAutoBypassesInsteadOfFailing(t *testing.T) {
	h := newHarness(t)
	h.seedZone("ab", "Anbau", engine.ZoneConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{
			hmenum.AlarmModeFull: {ExitDelaySeconds: 30, TriggerSeconds: 60},
		},
	})
	h.seedSensor("flaky", "ab", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes:      []hmenum.AlarmMode{hmenum.AlarmModeFull},
		BypassAuto: true,
	})
	h.start()
	if _, err := h.eng.Arm(h.ctx, "ab", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}

	// The flagged sensor opens during downtime; the completion
	// re-check excludes it instead of failing the arm.
	h.reader.set("flaky", true)
	h.restart(2 * time.Minute)
	h.wantState("ab", hmenum.AlarmZoneStateArmed)
	snap := h.mustSnapshot("ab")
	if got := sortedStrings(snap.Bypassed); len(got) != 1 || got[0] != "flaky" {
		t.Fatalf("Bypassed after restore = %v, want [flaky]", got)
	}
	if h.journal.has("arm_failed_on_restore") {
		t.Fatal("arm failed although the blocker is bypass_auto")
	}
}
