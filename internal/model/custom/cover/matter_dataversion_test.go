// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
)

// TestParityMatterJS_CoverDataVersionBumpsOnInvoke verifies that a
// successful coverWCServer MatterInvoke increments Cover.MatterDataVersion.
// Controllers rely on this counter for DataVersionFilter evaluation.
func TestParityMatterJS_CoverDataVersionBumpsOnInvoke(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	before := c.MatterDataVersion()

	srv := c.MatterClusterServers()[0]
	// UpOrOpen command.
	if _, err := srv.MatterInvoke(context.Background(), matterCmdUpOrOpen, nil); err != nil {
		t.Fatalf("MatterInvoke(UpOrOpen): %v", err)
	}
	if after := c.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after cover invoke: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_CoverDataVersionBumpsOnGoToLift verifies that a
// GoToLiftPercentage invoke increments MatterDataVersion.
func TestParityMatterJS_CoverDataVersionBumpsOnGoToLift(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	before := c.MatterDataVersion()

	srv := c.MatterClusterServers()[0]
	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(5000)); err != nil {
		t.Fatalf("MatterInvoke(GoToLift): %v", err)
	}
	if after := c.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after GoToLift: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_CoverDataVersionMonotonicallyRises verifies that
// consecutive successful invokes each increment the counter strictly.
func TestParityMatterJS_CoverDataVersionMonotonicallyRises(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]

	cmds := []uint32{matterCmdUpOrOpen, matterCmdDownOrClose, matterCmdUpOrOpen}
	for i, cmd := range cmds {
		prev := c.MatterDataVersion()
		if _, err := srv.MatterInvoke(context.Background(), cmd, nil); err != nil {
			t.Fatalf("cmd %d: %v", i, err)
		}
		if next := c.MatterDataVersion(); next <= prev {
			t.Fatalf("cmd %d: DataVersion not monotonically rising: prev=%d next=%d", i, prev, next)
		}
	}
}

// TestParityMatterJS_CoverDataVersionStableOnRead verifies that
// MatterRead on coverWCServer does not alter MatterDataVersion.
func TestParityMatterJS_CoverDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	before := c.MatterDataVersion()

	srv := c.MatterClusterServers()[0]
	srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
	srv.MatterRead(matterAttrFeatureMap)
	srv.MatterRead(matterAttrClusterRevision)

	if after := c.MatterDataVersion(); after != before {
		t.Fatalf("MatterRead bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_BlindDataVersionBumpsOnInvoke verifies that a
// successful blindWCServer MatterInvoke (tilt) increments
// Cover.MatterDataVersion via the shared embedded Cover.dataVersion.
func TestParityMatterJS_BlindDataVersionBumpsOnInvoke(t *testing.T) {
	t.Parallel()
	w := &putWriter{}
	b := newBlindRig(t, "VCU3560967:1", w, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	before := b.MatterDataVersion()

	srv := b.MatterClusterServers()[0]
	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToTiltPercentage, uint16(2500)); err != nil {
		t.Fatalf("MatterInvoke(GoToTilt): %v", err)
	}
	if after := b.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after blind invoke: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_BlindDataVersionStableOnRead verifies that
// MatterRead on blindWCServer does not alter MatterDataVersion.
func TestParityMatterJS_BlindDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	before := b.MatterDataVersion()

	srv := b.MatterClusterServers()[0]
	srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
	srv.MatterRead(matterAttrCurrentPositionTiltPercent100ths)

	if after := b.MatterDataVersion(); after != before {
		t.Fatalf("MatterRead bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_GarageDataVersionBumpsOnInvoke verifies that a
// successful garageWCServer MatterInvoke increments Garage.MatterDataVersion.
func TestParityMatterJS_GarageDataVersionBumpsOnInvoke(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", w)
	before := g.MatterDataVersion()

	srv := g.MatterClusterServers()[0]
	if _, err := srv.MatterInvoke(context.Background(), matterCmdUpOrOpen, nil); err != nil {
		t.Fatalf("MatterInvoke(UpOrOpen): %v", err)
	}
	if after := g.MatterDataVersion(); after <= before {
		t.Fatalf("MatterDataVersion did not bump after garage invoke: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_GarageDataVersionStableOnRead verifies that
// MatterRead on garageWCServer does not alter MatterDataVersion.
func TestParityMatterJS_GarageDataVersionStableOnRead(t *testing.T) {
	t.Parallel()
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	before := g.MatterDataVersion()

	srv := g.MatterClusterServers()[0]
	srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
	srv.MatterRead(matterAttrEndProductType)

	if after := g.MatterDataVersion(); after != before {
		t.Fatalf("MatterRead bumped DataVersion: before=%d after=%d", before, after)
	}
}

// TestParityMatterJS_CoverDataVersionStableOnFailedInvoke verifies that
// an invoke with an unknown command ID does not increment MatterDataVersion.
func TestParityMatterJS_CoverDataVersionStableOnFailedInvoke(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	before := c.MatterDataVersion()

	srv := c.MatterClusterServers()[0]
	_, _ = srv.MatterInvoke(context.Background(), 0x06, nil)

	if after := c.MatterDataVersion(); after != before {
		t.Fatalf("failed invoke bumped DataVersion: before=%d after=%d", before, after)
	}
}
