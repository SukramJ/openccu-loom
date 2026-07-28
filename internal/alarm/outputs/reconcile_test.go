// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package outputs

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// containsOutput reports whether out has an entry for outputID.
func containsOutput(out []SoundingOutput, outputID string) (SoundingOutput, bool) {
	for _, o := range out {
		if o.OutputID == outputID {
			return o, true
		}
	}
	return SoundingOutput{}, false
}

// TestReconcile_SoundingReportsOnlyObservedActiveOutputs covers S4
// case 23: Sounding reports an output only when its live read-back is
// both active and observed; an active-but-unobserved siren is
// treated as silent, and a smoke sounder reports sounding only when
// it is both active and flagged as an intrusion sounder.
func TestReconcile_SoundingReportsOnlyObservedActiveOutputs(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()

	h.siren("sirA").setAcoustic(true, true)
	out := h.mgr.Sounding(h.ctx, "eg")
	found, ok := containsOutput(out, "sirA")
	if !ok {
		t.Fatal("expected sirA to be reported as sounding")
	}
	if found.SharedWithCCU {
		t.Fatal("sirA is not shared_with_ccu; SharedWithCCU must be false")
	}

	h.siren("sirA").setAcoustic(true, false) // active but unobserved
	out = h.mgr.Sounding(h.ctx, "eg")
	if _, ok := containsOutput(out, "sirA"); ok {
		t.Fatal("an unobserved-active siren must not be reported as sounding")
	}

	// The optical channel of a siren-resolved output is checked the
	// same way as the acoustic channel.
	h.siren("sirO").setOptical(true, true)
	out = h.mgr.Sounding(h.ctx, "eg")
	if _, ok := containsOutput(out, "sirO"); !ok {
		t.Fatal("expected sirO to be reported as sounding via its optical channel")
	}
	h.siren("sirO").setOptical(true, false)
	out = h.mgr.Sounding(h.ctx, "eg")
	if _, ok := containsOutput(out, "sirO"); ok {
		t.Fatal("an unobserved-active optical channel must not be reported as sounding")
	}

	// A switched-siren output is checked via the actuator's IsOn
	// read-back.
	h.actuator("plug").setOn(true, true)
	out = h.mgr.Sounding(h.ctx, "eg")
	if _, ok := containsOutput(out, "plug"); !ok {
		t.Fatal("expected plug to be reported as sounding via IsOn")
	}
	h.actuator("plug").setOn(true, false)
	out = h.mgr.Sounding(h.ctx, "eg")
	if _, ok := containsOutput(out, "plug"); ok {
		t.Fatal("an unobserved-on actuator must not be reported as sounding")
	}

	h.smoke("smoke").setActive(true, true)
	h.smoke("smoke").setIntrusion(true)
	out = h.mgr.Sounding(h.ctx, "eg")
	if _, ok := containsOutput(out, "smoke"); !ok {
		t.Fatal("expected smoke to be reported as sounding")
	}
}

// TestReconcile_AdoptBoundedArmsWatchdogAndAccountsFullDuration
// covers S4 case 24: adopting an already-sounding output arms its
// stop watchdog at the full bounded duration and accounts that full
// duration on the ledger — the elapsed sounding time is unknown, so
// over-counting is the safe direction.
func TestReconcile_AdoptBoundedArmsWatchdogAndAccountsFullDuration(t *testing.T) {
	h := newHarness(t)
	h.seedOutputs(outputRow("sirA", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{DurationSeconds: 120}))
	const incidentID = int64(24)

	h.mgr.AdoptBounded(h.ctx, "eg", incidentID, []string{"sirA"})

	calls := h.ledger.callsFor(incidentID)
	if len(calls) != 1 || calls[0].DeltaMS != 120_000 {
		t.Fatalf("ledger calls for incident %d = %+v, want one 120000ms entry", incidentID, calls)
	}

	h.advance(120 * time.Second)
	off := h.siren("sirA").turnOffCallsSnapshot()
	if len(off) != 1 {
		t.Fatalf("TurnOff calls after the bounded duration = %d, want 1", len(off))
	}
	if off[0].Priority != hmenum.CommandPriorityCritical {
		t.Fatalf("stop priority = %v, want Critical", off[0].Priority)
	}
}

// TestReconcile_StopUnownedStopsNonSharedButSparesSharedWithCCU
// covers S4 case 25: reconciliation stops a sounding output with no
// declared third-party owner, but a shared_with_ccu output is left
// alone.
func TestReconcile_StopUnownedStopsNonSharedButSparesSharedWithCCU(t *testing.T) {
	h := newHarness(t)
	h.seedOutputs(
		outputRow("sirA", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{DurationSeconds: 120}),
		outputRow("sirShared", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{DurationSeconds: 120, SharedWithCCU: true}),
	)
	h.siren("sirA").setAcoustic(true, true)
	h.siren("sirShared").setAcoustic(true, true)

	h.mgr.StopUnowned(h.ctx, "eg")

	if n := h.siren("sirA").turnOffCount(); n != 1 {
		t.Fatalf("sirA TurnOff calls = %d, want 1", n)
	}
	if p := h.siren("sirA").turnOffCallsSnapshot()[0].Priority; p != hmenum.CommandPriorityCritical {
		t.Fatalf("sirA stop priority = %v, want Critical", p)
	}
	if !h.journal.hasForOutput("reconcile_stopped_unowned_siren", "sirA") {
		t.Fatal("expected a reconcile_stopped_unowned_siren journal entry for sirA")
	}

	if n := h.siren("sirShared").turnOffCount(); n != 0 {
		t.Fatalf("sirShared TurnOff calls = %d, want 0 (shared_with_ccu must not be auto-stopped)", n)
	}
	if h.journal.hasForOutput("reconcile_stopped_unowned_siren", "sirShared") {
		t.Fatal("did not expect a reconcile_stopped_unowned_siren journal entry for sirShared")
	}
}
