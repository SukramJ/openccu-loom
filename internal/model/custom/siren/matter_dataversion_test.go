// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/onoff"
)

// The siren's MatterDataVersion is the counter controllers evaluate
// DataVersionFilters against. The OnOff server shares the siren's tracker,
// so every attribute change the server commits must surface there, and
// nothing that leaves the attributes alone may move it.

// TestSirenDataVersionIsTheOnOffServersVersion pins the sharing: the
// version the siren reports is the version its OnOff server reports.
func TestSirenDataVersionIsTheOnOffServersVersion(t *testing.T) {
	t.Parallel()
	r, srv, _ := matterSiren(t)
	if got, want := r.siren.MatterDataVersion(), srv.MatterDataVersion(); got != want {
		t.Fatalf("Siren.MatterDataVersion = %d, OnOff server's = %d; the tracker must be shared", got, want)
	}
	if _, err := srv.MatterInvoke(context.Background(), onoff.CmdOn, nil); err != nil {
		t.Fatalf("On: %v", err)
	}
	if got, want := r.siren.MatterDataVersion(), srv.MatterDataVersion(); got != want {
		t.Fatalf("after On: Siren.MatterDataVersion = %d, OnOff server's = %d", got, want)
	}
}

// TestSirenDataVersionRisesWithEveryAcceptedChange pins a strictly rising
// counter across alternating On / Off commands.
func TestSirenDataVersionRisesWithEveryAcceptedChange(t *testing.T) {
	t.Parallel()
	r, srv, _ := matterSiren(t)
	for i, cmd := range []uint32{onoff.CmdOn, onoff.CmdOff, onoff.CmdOn} {
		prev := r.siren.MatterDataVersion()
		if _, err := srv.MatterInvoke(context.Background(), cmd, nil); err != nil {
			t.Fatalf("cmd %d (0x%02X): %v", i, cmd, err)
		}
		if next := r.siren.MatterDataVersion(); next <= prev {
			t.Fatalf("cmd %d (0x%02X): data version not rising: prev=%d next=%d", i, cmd, prev, next)
		}
	}
}

// TestSirenDataVersionStableOnRead pins that reads leave the counter alone.
func TestSirenDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	r, srv, _ := matterSiren(t)
	before := r.siren.MatterDataVersion()
	for _, id := range []uint32{onoff.AttrOnOff, 0xFFFC, 0xFFFD} {
		srv.MatterRead(id)
	}
	if after := r.siren.MatterDataVersion(); after != before {
		t.Fatalf("a read moved the data version: before=%d after=%d", before, after)
	}
}

// TestSirenDataVersionStableOnRejectedRequests pins that requests the
// cluster rejects — an undefined command, a write of the read-only OnOff
// attribute, a write of an attribute the cluster does not define — leave
// the counter alone.
func TestSirenDataVersionStableOnRejectedRequests(t *testing.T) {
	t.Parallel()
	r, srv, _ := matterSiren(t)
	before := r.siren.MatterDataVersion()
	const undefinedCmdID = 0x7F // no OnOff command carries this id
	if _, err := srv.MatterInvoke(context.Background(), undefinedCmdID, nil); err == nil {
		t.Error("an undefined command must be rejected")
	}
	if err := srv.MatterWrite(context.Background(), onoff.AttrOnOff, true); err == nil {
		t.Error("a write of the read-only OnOff attribute must be rejected")
	}
	if err := srv.MatterWrite(context.Background(), 0x9999, true); err == nil {
		t.Error("a write of an undefined attribute must be rejected")
	}
	if after := r.siren.MatterDataVersion(); after != before {
		t.Fatalf("a rejected request moved the data version: before=%d after=%d", before, after)
	}
}
