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

// This file covers the Arm verb: the exit-delay countdown, immediate
// arms, blocker resolution (refuse / force / explicit bypass /
// bypass_auto), the sentinel errors, re-arming, and the two
// exit-delay sensor behaviors (instant trigger, arm-after-closing).

func TestArm_FullHappyPathArmsAfterExitDelay(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.wantState("eg", hmenum.AlarmZoneStateArming)

	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)
	if got := h.mustSnapshot("eg").Mode; got != hmenum.AlarmModeFull {
		t.Fatalf("mode = %s, want full", got)
	}
	if !h.journal.has("arming_started") || !h.journal.has("armed") {
		t.Fatalf("missing arm journal entries; got %v", h.journal.events())
	}

	changes := h.sink.stateChanges()
	if len(changes) != 2 {
		t.Fatalf("state changes = %+v, want 2 entries", changes)
	}
	if changes[0].From != hmenum.AlarmZoneStateDisarmed || changes[0].To != hmenum.AlarmZoneStateArming {
		t.Fatalf("first state change = %+v, want disarmed->arming", changes[0])
	}
	if changes[1].From != hmenum.AlarmZoneStateArming || changes[1].To != hmenum.AlarmZoneStateArmed {
		t.Fatalf("second state change = %+v, want arming->armed", changes[1])
	}
}

func TestArm_PerimeterImmediateArmsWithoutDelay(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	res, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModePerimeter, By: "tester"})
	if err != nil {
		t.Fatalf("arm: %v", err)
	}
	if res.State != hmenum.AlarmZoneStateArmed {
		t.Fatalf("result state = %s, want armed", res.State)
	}
	h.wantState("eg", hmenum.AlarmZoneStateArmed)

	changes := h.sink.stateChanges()
	if len(changes) != 1 {
		t.Fatalf("state changes = %+v, want a single entry", changes)
	}
	if changes[0].From != hmenum.AlarmZoneStateDisarmed || changes[0].To != hmenum.AlarmZoneStateArmed {
		t.Fatalf("state change = %+v, want disarmed->armed", changes[0])
	}
}

func TestArm_SkipDelayArmsFullImmediately(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	res, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, SkipDelay: true, By: "tester"})
	if err != nil {
		t.Fatalf("arm: %v", err)
	}
	if res.State != hmenum.AlarmZoneStateArmed {
		t.Fatalf("result state = %s, want armed", res.State)
	}
	h.wantState("eg", hmenum.AlarmZoneStateArmed)
}

func TestArm_RefusedWithoutForceReportsBlockers(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	_, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"})
	var nre *engine.NotReadyError
	if !errors.As(err, &nre) {
		t.Fatalf("err = %v, want *engine.NotReadyError", err)
	}
	if got := sortedStrings(nre.Blockers); len(got) != 1 || got[0] != "window" {
		t.Fatalf("blockers = %v, want [window]", got)
	}
	h.wantState("eg", hmenum.AlarmZoneStateDisarmed)
}

func TestArm_ForceArmsAndBypassesBlockers(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	res, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, Force: true, By: "tester"})
	if err != nil {
		t.Fatalf("force arm: %v", err)
	}
	if got := sortedStrings(res.Bypassed); len(got) != 1 || got[0] != "window" {
		t.Fatalf("bypassed = %v, want [window]", got)
	}
	if !h.journal.has("sensor_bypassed") {
		t.Fatalf("missing sensor_bypassed journal entry; got %v", h.journal.events())
	}
	if got := sortedStrings(h.mustSnapshot("eg").Bypassed); len(got) != 1 || got[0] != "window" {
		t.Fatalf("snapshot bypassed = %v, want [window]", got)
	}
}

func TestArm_ExplicitBypassAcceptedWithoutForce(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	res, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{
		Mode: hmenum.AlarmModeFull, Bypass: []string{"window"}, By: "tester",
	})
	if err != nil {
		t.Fatalf("arm with explicit bypass: %v", err)
	}
	if got := sortedStrings(res.Bypassed); len(got) != 1 || got[0] != "window" {
		t.Fatalf("bypassed = %v, want [window]", got)
	}
}

func TestArm_UnknownZoneReturnsError(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	_, err := h.eng.Arm(h.ctx, "does-not-exist", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"})
	if !errors.Is(err, engine.ErrUnknownZone) {
		t.Fatalf("err = %v, want ErrUnknownZone", err)
	}
}

func TestArm_UnconfiguredModeReturnsError(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	_, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeNight, By: "tester"})
	if !errors.Is(err, engine.ErrUnknownMode) {
		t.Fatalf("err = %v, want ErrUnknownMode", err)
	}
}

func TestArm_DisarmedModeReturnsError(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	_, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeDisarmed, By: "tester"})
	if !errors.Is(err, engine.ErrUnknownMode) {
		t.Fatalf("err = %v, want ErrUnknownMode", err)
	}
}

func TestArm_WhilePendingReturnsInvalidState(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "door", true)
	h.wantState("eg", hmenum.AlarmZoneStatePending)

	_, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"})
	if !errors.Is(err, engine.ErrInvalidState) {
		t.Fatalf("err = %v, want ErrInvalidState", err)
	}
}

func TestArm_WhileTriggeredReturnsInvalidState(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	_, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"})
	if !errors.Is(err, engine.ErrInvalidState) {
		t.Fatalf("err = %v, want ErrInvalidState", err)
	}
}

func TestArm_ReArmChangesModeWithoutError(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()
	h.armFull()

	res, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModePerimeter, By: "tester"})
	if err != nil {
		t.Fatalf("re-arm: %v", err)
	}
	if res.State != hmenum.AlarmZoneStateArmed {
		t.Fatalf("result state = %s, want armed", res.State)
	}
	if got := h.mustSnapshot("eg").Mode; got != hmenum.AlarmModePerimeter {
		t.Fatalf("mode = %s, want perimeter", got)
	}
}

func TestArm_InstantSensorDuringExitDelayTriggersImmediately(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.eng.HandleSensorEvent(h.ctx, "window", true)

	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count = %d, want 1", n)
	}
}

func TestArm_ExitDelayFlaggedSensorDoesNotInterruptArming(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.eng.HandleSensorEvent(h.ctx, "motion", true)
	h.wantState("eg", hmenum.AlarmZoneStateArming)

	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("FireCycle count = %d, want 0", n)
	}
}

func TestArm_AfterClosingDebounceCompletesEarly(t *testing.T) {
	h := newHarness(t)
	h.seedZone("og", "Obergeschoss", defaultZoneConfig())
	h.seedSensor("closer", "og", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{
		Modes:           []hmenum.AlarmMode{hmenum.AlarmModeFull},
		UseExitDelay:    true,
		ArmAfterClosing: true,
	})
	h.start()

	if _, err := h.eng.Arm(h.ctx, "og", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.eng.HandleSensorEvent(h.ctx, "closer", true)
	h.eng.HandleSensorEvent(h.ctx, "closer", false)
	h.wantState("og", hmenum.AlarmZoneStateArming)

	// The 5 s settle timer completes the arm long before the 30 s
	// exit delay would have elapsed on its own.
	h.advance(5 * time.Second)
	h.wantState("og", hmenum.AlarmZoneStateArmed)
	if !h.journal.has("armed_after_closing") {
		t.Fatalf("missing armed_after_closing journal entry; got %v", h.journal.events())
	}
}

func TestArm_AfterClosingDebounceAbortsOnReopen(t *testing.T) {
	h := newHarness(t)
	h.seedZone("og", "Obergeschoss", defaultZoneConfig())
	h.seedSensor("closer", "og", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{
		Modes:           []hmenum.AlarmMode{hmenum.AlarmModeFull},
		UseExitDelay:    true,
		ArmAfterClosing: true,
	})
	h.start()

	if _, err := h.eng.Arm(h.ctx, "og", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.eng.HandleSensorEvent(h.ctx, "closer", true)
	h.eng.HandleSensorEvent(h.ctx, "closer", false)
	// Re-opened before the 5 s settle timer fires: the early
	// completion must not happen.
	h.eng.HandleSensorEvent(h.ctx, "closer", true)

	h.advance(5 * time.Second)
	h.wantState("og", hmenum.AlarmZoneStateArming)
}
