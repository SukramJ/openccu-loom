// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package switchdev

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestParityMatterJS_SwitchDataVersionBumpsOnWrite verifies that a
// successful MatterWrite increments MatterDataVersion. Controllers
// rely on this counter for DataVersionFilter evaluation; a stuck
// version means the controller skips reporting the cluster as changed.
func TestParityMatterJS_SwitchDataVersionBumpsOnWrite(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
	before := s.MatterDataVersion()

	if err := s.MatterWrite(context.Background(), matterAttrOnOff, true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite: %v", err)
	}
	if after := s.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after write: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_SwitchDataVersionBumpsOnInvokeOff verifies that a
// successful MatterInvoke(Off) increments MatterDataVersion.
func TestParityMatterJS_SwitchDataVersionBumpsOnInvokeOff(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
	before := s.MatterDataVersion()

	if _, err := s.MatterInvoke(context.Background(), matterCmdOff, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(Off): %v", err)
	}
	if after := s.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after invoke Off: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_SwitchDataVersionBumpsOnInvokeOn verifies that a
// successful MatterInvoke(On) increments MatterDataVersion.
func TestParityMatterJS_SwitchDataVersionBumpsOnInvokeOn(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
	before := s.MatterDataVersion()

	if _, err := s.MatterInvoke(context.Background(), matterCmdOn, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(On): %v", err)
	}
	if after := s.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after invoke On: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_SwitchDataVersionMonotonicallyRises verifies that
// consecutive successful mutations each increment the counter strictly.
func TestParityMatterJS_SwitchDataVersionMonotonicallyRises(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})

	for i, on := range []bool{true, false, true} {
		prev := s.MatterDataVersion()
		if err := s.MatterWrite(context.Background(), matterAttrOnOff, on, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		if next := s.MatterDataVersion(); next <= prev {
			t.Fatalf("write %d: DataVersion not monotonically rising: prev=%d next=%d", i, prev, next)
		}
	}
}

// TestParityMatterJS_SwitchDataVersionStableOnRead verifies that
// MatterRead does not alter MatterDataVersion.
func TestParityMatterJS_SwitchDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
	before := s.MatterDataVersion()

	s.MatterRead(matterAttrOnOff)
	s.MatterRead(matterAttrFeatureMap)
	s.MatterRead(matterAttrClusterRevision)

	if after := s.MatterDataVersion(); after != before {
		t.Fatalf("MatterRead bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_SwitchDataVersionStableOnUnknownAttrWrite verifies
// that a write to an unsupported attribute (which returns an error) does
// not increment MatterDataVersion.
func TestParityMatterJS_SwitchDataVersionStableOnUnknownAttrWrite(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
	before := s.MatterDataVersion()

	_ = s.MatterWrite(context.Background(), 0x4001, true, hmenum.CommandPriorityHigh)

	if after := s.MatterDataVersion(); after != before {
		t.Fatalf("failed write bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_SwitchDataVersionStableOnNilWrite verifies that a
// nil-value write (no-op path) does not increment MatterDataVersion.
func TestParityMatterJS_SwitchDataVersionStableOnNilWrite(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
	before := s.MatterDataVersion()

	if err := s.MatterWrite(context.Background(), matterAttrOnOff, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("nil write returned unexpected error: %v", err)
	}
	if after := s.MatterDataVersion(); after != before {
		t.Fatalf("nil write bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_SwitchDataVersionStableOnUnknownCommand verifies
// that MatterInvoke with an unknown command ID does not bump DataVersion.
func TestParityMatterJS_SwitchDataVersionStableOnUnknownCommand(t *testing.T) {
	t.Parallel()
	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
	before := s.MatterDataVersion()

	// 0x43 is above the highest defined OnOff command (OnWithTimedOff 0x42).
	_, _ = s.MatterInvoke(context.Background(), 0x43, nil, hmenum.CommandPriorityHigh)

	if after := s.MatterDataVersion(); after != before {
		t.Fatalf("failed invoke bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestSwitch_MatterDataVersion_BumpsOnCCUEcho verifies that
// DataVersionTracker.Bump is called via the OnConfirmedUpdate hook
// registered in New. The OnOff cluster DataVersion must advance on every
// real CCU STATE transition so Apple HAP-Mapper does not dedupe ReportData
// for changes that originate outside Matter (wall switch, MQTT, ReGa).
// Same-value echoes must NOT bump the version (matches OnConfirmedUpdate
// semantics: fires only when old != new or on first observation).
func TestSwitch_MatterDataVersion_BumpsOnCCUEcho(t *testing.T) {
	t.Parallel()
	sw := newTestSwitch(t, "00021BE9957782:4", "test", nil)

	// Step 1: first CCU echo (true) — no prior value, counts as transition.
	before := sw.MatterDataVersion()
	sw.OnEvent(true)
	if after := sw.MatterDataVersion(); after <= before {
		t.Fatalf("first CCU echo (true): DataVersion did not bump: before=%d after=%d", before, after)
	}

	// Step 2: same value echo (true → true) — confirmed-update skips no-op;
	// DataVersion must stay unchanged.
	stable := sw.MatterDataVersion()
	sw.OnEvent(true)
	if after := sw.MatterDataVersion(); after != stable {
		t.Fatalf("same-value CCU echo (true→true): DataVersion must not bump: stable=%d after=%d", stable, after)
	}

	// Step 3: real state transition (true → false) — must bump.
	before = sw.MatterDataVersion()
	sw.OnEvent(false)
	if after := sw.MatterDataVersion(); after <= before {
		t.Fatalf("CCU echo (true→false): DataVersion did not bump: before=%d after=%d", before, after)
	}
}
