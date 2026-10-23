// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/onoff"
)

// The OnOff server's data version advances on every attribute change the
// server commits, and nothing that leaves the attributes alone may move
// it. (On the wire a bridged endpoint reports the version the endpoint
// hosts, which the bridge advances on the same changes.)

// TestSirenDataVersionRisesWithEveryAcceptedChange pins a strictly rising
// counter across alternating On / Off commands.
func TestSirenDataVersionRisesWithEveryAcceptedChange(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSiren(t)
	for i, cmd := range []uint32{onoff.CmdOn, onoff.CmdOff, onoff.CmdOn} {
		prev := srv.MatterDataVersion()
		if _, err := srv.MatterInvoke(context.Background(), cmd, nil); err != nil {
			t.Fatalf("cmd %d (0x%02X): %v", i, cmd, err)
		}
		if next := srv.MatterDataVersion(); next <= prev {
			t.Fatalf("cmd %d (0x%02X): data version not rising: prev=%d next=%d", i, cmd, prev, next)
		}
	}
}

// TestSirenDataVersionStableOnRead pins that reads leave the counter alone.
func TestSirenDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSiren(t)
	before := srv.MatterDataVersion()
	for _, id := range []uint32{onoff.AttrOnOff, 0xFFFC, 0xFFFD} {
		srv.MatterRead(id)
	}
	if after := srv.MatterDataVersion(); after != before {
		t.Fatalf("a read moved the data version: before=%d after=%d", before, after)
	}
}

// TestSirenDataVersionStableOnRejectedRequests pins that requests the
// cluster rejects — an undefined command, a write of the read-only OnOff
// attribute, a write of an attribute the cluster does not define — leave
// the counter alone.
func TestSirenDataVersionStableOnRejectedRequests(t *testing.T) {
	t.Parallel()
	_, srv, _ := matterSiren(t)
	before := srv.MatterDataVersion()
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
	if after := srv.MatterDataVersion(); after != before {
		t.Fatalf("a rejected request moved the data version: before=%d after=%d", before, after)
	}
}
