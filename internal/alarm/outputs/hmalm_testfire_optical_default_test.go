// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package outputs

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestTestFireOpticalDefaultMatchesTheFireCycle pins the operator's
// test fire to the pattern a real alarm uses.
//
// A test fire exists to show what a real fire will do, so the two paths
// must resolve the unconfigured optical pattern the same way. They read
// the same rule today ([alarmOpticalSelection]); before that they were
// two hand-written copies of a positional convention, and only the fire
// path was asserted anywhere — moving the rule in one of them left every
// test green while the operator validated a pattern the alarm would not
// use.
func TestTestFireOpticalDefaultMatchesTheFireCycle(t *testing.T) {
	fireSelection := hmAlmOpticalSelectionOfFireCycle(t)

	h := newHarness(t)
	_, optical := sharedSirenChannelRows()
	h.seedOutputs(optical)
	dev := sirenAt(t, h, asirChannel)
	dev.setValueLists(
		[]string{"DISABLE_ACOUSTIC_SIGNAL", "FREQ_HIGH"},
		"DISABLE_ACOUSTIC_SIGNAL",
		asirOpticalSelections,
	)

	if err := h.mgr.TestFire(h.ctx, optical.ID, true); err != nil {
		t.Fatalf("TestFire: %v", err)
	}

	calls := dev.turnOnCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("TurnOn calls = %d, want 1", len(calls))
	}
	got := selection(calls[0].Cfg.OpticalSelection)
	if got != fireSelection {
		t.Errorf("test-fire optical selection = %q, fire-cycle selection = %q — "+
			"the operator validates a pattern the alarm will not use", got, fireSelection)
	}
	if got == "CONFIRMATION_SIGNAL_2" {
		t.Error("test fire took the last list entry, an acknowledgement blink rather than an alarm pattern")
	}
}

// hmAlmOpticalSelectionOfFireCycle reports the optical selection a real
// fire cycle writes for an ASIR channel with no pattern configured. It
// is measured, not spelled out, so the assertion above compares the two
// driver paths rather than one path against a literal.
func hmAlmOpticalSelectionOfFireCycle(t *testing.T) string {
	t.Helper()
	h := newHarness(t)
	_, optical := sharedSirenChannelRows()
	h.seedOutputs(optical)
	dev := sirenAt(t, h, asirChannel)
	dev.setValueLists(
		[]string{"DISABLE_ACOUSTIC_SIGNAL", "FREQ_HIGH"},
		"DISABLE_ACOUSTIC_SIGNAL",
		asirOpticalSelections,
	)
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(41, hmenum.AlarmModeFull),
		engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}
	calls := dev.turnOnCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("FireCycle TurnOn calls = %d, want 1", len(calls))
	}
	return selection(calls[0].Cfg.OpticalSelection)
}
