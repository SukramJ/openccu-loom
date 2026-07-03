// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package siren

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestParityMatterJS_SirenDataVersionBumpsOnWrite verifies that a
// successful OnOff attribute write via sirenOnOffServer increments
// Siren.MatterDataVersion. Controllers rely on this counter for
// DataVersionFilter evaluation.
func TestParityMatterJS_SirenDataVersionBumpsOnWrite(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	before := r.siren.MatterDataVersion()

	srv := findCluster(t, r.siren, matterClusterOnOff)
	if err := srv.MatterWrite(context.Background(), matterAttrOnOffOnOff, true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(OnOff): %v", err)
	}
	if after := r.siren.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after write: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_SirenDataVersionBumpsOnInvokeOff verifies that a
// successful Off invoke increments Siren.MatterDataVersion.
func TestParityMatterJS_SirenDataVersionBumpsOnInvokeOff(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	before := r.siren.MatterDataVersion()

	srv := findCluster(t, r.siren, matterClusterOnOff)
	if _, err := srv.MatterInvoke(context.Background(), matterCmdOff, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(Off): %v", err)
	}
	if after := r.siren.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after invoke Off: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_SirenDataVersionBumpsOnInvokeOn verifies that a
// successful On invoke increments Siren.MatterDataVersion.
func TestParityMatterJS_SirenDataVersionBumpsOnInvokeOn(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	before := r.siren.MatterDataVersion()

	srv := findCluster(t, r.siren, matterClusterOnOff)
	if _, err := srv.MatterInvoke(context.Background(), matterCmdOn, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(On): %v", err)
	}
	if after := r.siren.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after invoke On: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_SirenDataVersionMonotonicallyRises verifies that
// consecutive successful mutations each increment the counter strictly.
func TestParityMatterJS_SirenDataVersionMonotonicallyRises(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	srv := findCluster(t, r.siren, matterClusterOnOff)

	cmds := []uint32{matterCmdOn, matterCmdOff, matterCmdOn}
	for i, cmd := range cmds {
		prev := r.siren.MatterDataVersion()
		if _, err := srv.MatterInvoke(context.Background(), cmd, nil, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("cmd %d: %v", i, err)
		}
		if next := r.siren.MatterDataVersion(); next <= prev {
			t.Fatalf("cmd %d: DataVersion not monotonically rising: prev=%d next=%d", i, prev, next)
		}
	}
}

// TestParityMatterJS_SirenDataVersionStableOnRead verifies that
// MatterRead on sirenOnOffServer does not alter MatterDataVersion.
func TestParityMatterJS_SirenDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	before := r.siren.MatterDataVersion()

	srv := findCluster(t, r.siren, matterClusterOnOff)
	srv.MatterRead(matterAttrOnOffOnOff)
	srv.MatterRead(matterAttrFeatureMap)
	srv.MatterRead(matterAttrClusterRevision)

	if after := r.siren.MatterDataVersion(); after != before {
		t.Fatalf("MatterRead bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_SirenDataVersionStableOnUnknownAttrWrite verifies
// that a write to an unsupported attribute ID does not increment
// MatterDataVersion. 0x9999 is outside every attribute this projection
// implements (OnOff 0x0000 and the LT-gated 0x4000-0x4003 range).
func TestParityMatterJS_SirenDataVersionStableOnUnknownAttrWrite(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	before := r.siren.MatterDataVersion()

	srv := findCluster(t, r.siren, matterClusterOnOff)
	_ = srv.MatterWrite(context.Background(), 0x9999, true, hmenum.CommandPriorityHigh)

	if after := r.siren.MatterDataVersion(); after != before {
		t.Fatalf("failed write bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_SirenDataVersionStableOnUnknownCommand verifies that
// a MatterInvoke with an unknown command ID does not increment
// MatterDataVersion. Toggle (0x02) is deliberately absent from this
// projection's accepted-command set — Siren has no toggle-alarm role —
// so it remains a genuinely unimplemented command ID.
func TestParityMatterJS_SirenDataVersionStableOnUnknownCommand(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	before := r.siren.MatterDataVersion()

	srv := findCluster(t, r.siren, matterClusterOnOff)
	const toggleCmdID = 0x02
	_, _ = srv.MatterInvoke(context.Background(), toggleCmdID, nil, hmenum.CommandPriorityHigh)

	if after := r.siren.MatterDataVersion(); after != before {
		t.Fatalf("failed invoke bumped DataVersion: before=%d after=%d", before, after)
	}
}
