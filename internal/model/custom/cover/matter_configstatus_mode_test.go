// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCoverConfigStatusAdvertisesLiftPositionAware locks the
// ConfigStatus (0x0007) value for a lift-only Cover: Operational
// (bit0) | LiftPositionAware (bit3) = 0x09. matter.js
// WindowCoveringServer.ts:120-135 initialize() sets liftPositionAware
// whenever the device advertises PA_LF, which every Cover here does
// (see TestCoverFeatureMapLiftOnly). LiftMovementReversed (bit2) must
// stay clear — it only flips on a Mode write with
// MotorDirectionReversed set (WindowCoveringServer.ts:188), which this
// projection never issues.
func TestCoverConfigStatusAdvertisesLiftPositionAware(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	v, ok := srv.MatterRead(matterAttrConfigStatus)
	if !ok {
		t.Fatal("ConfigStatus not readable")
	}
	got := v.(uint8)
	if got != 0x09 {
		t.Fatalf("Cover ConfigStatus = 0x%02X, want 0x09 (Operational|LiftPositionAware)", got)
	}
	if got&matterWCConfigLiftPositionAware == 0 {
		t.Errorf("Cover ConfigStatus = 0x%02X, missing LiftPositionAware bit", got)
	}
}

// TestBlindConfigStatusAdvertisesLiftAndTiltPositionAware locks the
// ConfigStatus value for a Blind: Operational | LiftPositionAware |
// TiltPositionAware = 0x19 (bits 0, 3, 4).
func TestBlindConfigStatusAdvertisesLiftAndTiltPositionAware(t *testing.T) {
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	srv := b.MatterClusterServers()[0]
	v, ok := srv.MatterRead(matterAttrConfigStatus)
	if !ok {
		t.Fatal("ConfigStatus not readable")
	}
	got := v.(uint8)
	if got != 0x19 {
		t.Fatalf("Blind ConfigStatus = 0x%02X, want 0x19 (Operational|LiftPositionAware|TiltPositionAware)", got)
	}
}

// TestGarageConfigStatusAdvertisesLiftPositionAware mirrors
// TestCoverConfigStatusAdvertisesLiftPositionAware for the Garage
// projection (also lift-only, PA_LF).
func TestGarageConfigStatusAdvertisesLiftPositionAware(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	srv := g.MatterClusterServers()[0]
	v, ok := srv.MatterRead(matterAttrConfigStatus)
	if !ok {
		t.Fatal("ConfigStatus not readable")
	}
	if got := v.(uint8); got != 0x09 {
		t.Fatalf("Garage ConfigStatus = 0x%02X, want 0x09 (Operational|LiftPositionAware)", got)
	}
}

// TestCoverMatterWriteAcceptsValidMode confirms Mode (0x0017) writes
// succeed for constraint-valid values (max 15) — window-covering-
// cluster.element.ts:76-79 declares Mode "RW VM". Every prior
// WindowCovering write was rejected outright; this is the one
// writable attribute the cluster mandates.
func TestCoverMatterWriteAcceptsValidMode(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	if err := srv.MatterWrite(context.Background(), matterAttrMode, uint8(0x01), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(Mode, 0x01) = %v, want nil (valid ModeBitmap value)", err)
	}
	if err := srv.MatterWrite(context.Background(), matterAttrMode, uint8(0x0F), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(Mode, 0x0F) = %v, want nil (constraint boundary)", err)
	}
}

// TestCoverMatterWriteRejectsOutOfRangeMode confirms a Mode value
// above the "constraint: max 15" bound is rejected — the four
// ModeBitmap bits span the full legal range.
func TestCoverMatterWriteRejectsOutOfRangeMode(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	err := srv.MatterWrite(context.Background(), matterAttrMode, uint8(0x10), hmenum.CommandPriorityHigh)
	if !errors.Is(err, errMatterValueType) {
		t.Fatalf("MatterWrite(Mode, 0x10) = %v, want errMatterValueType (constraint max 15)", err)
	}
}

// TestCoverMatterWriteOtherAttributesStillRejected confirms the Mode
// carve-out did not accidentally open every attribute for writing —
// Type (0x0000, quality F, fixed) must still be rejected.
func TestCoverMatterWriteOtherAttributesStillRejected(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	err := srv.MatterWrite(context.Background(), matterAttrType, uint8(0), hmenum.CommandPriorityHigh)
	if !errors.Is(err, errMatterUnknownAttribute) {
		t.Fatalf("MatterWrite(Type, 0) = %v, want errMatterUnknownAttribute", err)
	}
}

// TestBlindAndGarageMatterWriteAcceptValidMode confirms the Mode
// accept-path is wired identically on the other two WindowCovering
// projections.
func TestBlindAndGarageMatterWriteAcceptValidMode(t *testing.T) {
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	if err := b.MatterClusterServers()[0].MatterWrite(context.Background(), matterAttrMode, uint8(0x03), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Blind MatterWrite(Mode, 0x03) = %v, want nil", err)
	}
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	if err := g.MatterClusterServers()[0].MatterWrite(context.Background(), matterAttrMode, uint8(0x03), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Garage MatterWrite(Mode, 0x03) = %v, want nil", err)
	}
}
