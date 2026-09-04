// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package outputs

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// asirOpticalSelections is the OPTICAL_ALARM_SELECTION list an HmIP-ASIR
// declares, in the device's own order: the four sustained alarm patterns sit
// at indices 1-4 and the three one-shot acknowledgement blinks at 5-7.
var asirOpticalSelections = []string{
	"DISABLE_OPTICAL_SIGNAL",
	"BLINKING_ALTERNATELY_REPEATING",
	"BLINKING_BOTH_REPEATING",
	"DOUBLE_FLASHING_REPEATING",
	"FLASHING_BOTH_REPEATING",
	"CONFIRMATION_SIGNAL_0",
	"CONFIRMATION_SIGNAL_1",
	"CONFIRMATION_SIGNAL_2",
}

// TestFireCycle_OpticalDefaultIsAnAlarmPatternNotTheLastListEntry pins the
// alarm that flashes an acknowledgement instead of an alarm.
//
// With no pattern configured the driver used to take the last entry of the
// device's list. On every ASIR variant that entry is CONFIRMATION_SIGNAL_2 —
// "long short short", the third of three one-shot confirmation blinks. The
// device accepts it, reports OPTICAL_ALARM_ACTIVE, and the watchdog is
// satisfied, so nothing errors, journals or reaches health: an optical-only
// activation shows a brief acknowledgement blink where an alarm was expected.
//
// The full list is what separates the two rules — a fixture holding only one
// repeating pattern cannot tell "first repeating" from "last entry" apart.
func TestFireCycle_OpticalDefaultIsAnAlarmPatternNotTheLastListEntry(t *testing.T) {
	h := newHarness(t)
	_, optical := sharedSirenChannelRows()
	h.seedOutputs(optical)
	dev := sirenAt(t, h, asirChannel)
	dev.setValueLists(
		[]string{"DISABLE_ACOUSTIC_SIGNAL", "FREQ_HIGH"},
		"DISABLE_ACOUSTIC_SIGNAL",
		asirOpticalSelections,
	)

	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(31, hmenum.AlarmModeFull),
		engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	calls := dev.turnOnCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("TurnOn calls = %d, want 1", len(calls))
	}
	got := selection(calls[0].Cfg.OpticalSelection)
	if got == "CONFIRMATION_SIGNAL_2" {
		t.Fatal("fired the last list entry, which is an acknowledgement blink and not an alarm pattern")
	}
	if got != "BLINKING_ALTERNATELY_REPEATING" {
		t.Errorf("optical selection = %q, want the first sustained alarm pattern", got)
	}
}

// TestFireCycle_OpticalSelectionWithheldWhenNoPatternRepeats pins that a
// device offering no sustained pattern gets no optical selection written at
// all, keeping the one it holds, rather than being sent an acknowledgement
// blink that would satisfy OPTICAL_ALARM_ACTIVE.
func TestFireCycle_OpticalSelectionWithheldWhenNoPatternRepeats(t *testing.T) {
	h := newHarness(t)
	_, optical := sharedSirenChannelRows()
	h.seedOutputs(optical)
	dev := sirenAt(t, h, asirChannel)
	dev.setValueLists(
		[]string{"DISABLE_ACOUSTIC_SIGNAL", "FREQ_HIGH"},
		"DISABLE_ACOUSTIC_SIGNAL",
		[]string{"DISABLE_OPTICAL_SIGNAL", "CONFIRMATION_SIGNAL_0", "CONFIRMATION_SIGNAL_2"},
	)

	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(32, hmenum.AlarmModeFull),
		engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	calls := dev.turnOnCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("TurnOn calls = %d, want 1", len(calls))
	}
	if got := selection(calls[0].Cfg.OpticalSelection); got != "" {
		t.Errorf("optical selection = %q, want none written", got)
	}
}
