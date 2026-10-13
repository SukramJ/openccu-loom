// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"testing"

	windowcoveringspec "github.com/SukramJ/go-fabric/cluster/spec/windowcovering"
)

// The Cover and Blind WindowCovering servers carry no DataVersion of
// their own: go-fabric's server reports its attribute changes through
// the endpoint's change notifier, and the endpoint's version advances
// with every report. Only the Garage closure projection keeps a
// cluster-level counter.

// TestParityMatterJS_GarageDataVersionBumpsOnInvoke verifies that a
// successful closure MatterInvoke increments Garage.MatterDataVersion.
func TestParityMatterJS_GarageDataVersionBumpsOnInvoke(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", w)
	before := g.MatterDataVersion()

	srv := g.MatterClusterServers()[0]
	if _, err := srv.MatterInvoke(context.Background(), windowcoveringspec.CmdUpOrOpen, nil); err != nil {
		t.Fatalf("MatterInvoke(UpOrOpen): %v", err)
	}
	if after := g.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after garage invoke: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_GarageDataVersionStableOnRead verifies that
// MatterRead on the closure server does not alter MatterDataVersion.
func TestParityMatterJS_GarageDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	before := g.MatterDataVersion()

	srv := g.MatterClusterServers()[0]
	srv.MatterRead(windowcoveringspec.AttrCurrentPositionLiftPercent100ths)
	srv.MatterRead(windowcoveringspec.AttrEndProductType)

	if after := g.MatterDataVersion(); after != before {
		t.Fatalf("MatterRead bumped DataVersion: before=%d after=%d", before, after)
	}
}
