// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// TestParityCover_TargetPositionLiftPresent verifies that TargetPositionLift
// (0x000B) is listed in MatterAttributes for Cover, Blind, and Garage.
// For Blind, TargetPositionTilt (0x000C) must also be present.
// Conformance LF & PA_LF / TL & PA_TL requires these given the advertised
// FeatureMaps.
func TestParityCover_TargetPositionLiftPresent(t *testing.T) {
	t.Parallel()

	// matterAttrs extracts the advertised attribute list from a cluster server
	// via the optional MatterClusterAttributeLister interface.
	matterAttrs := func(t *testing.T, srv interfaces.MatterClusterServer) []uint32 {
		t.Helper()
		lister, ok := srv.(interfaces.MatterClusterAttributeLister)
		if !ok {
			t.Fatal("cluster server does not implement MatterClusterAttributeLister")
		}
		return lister.MatterAttributes()
	}

	hasAttr := func(attrs []uint32, id uint32) bool {
		for _, a := range attrs {
			if a == id {
				return true
			}
		}
		return false
	}

	t.Run("Cover", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
		attrs := matterAttrs(t, c.MatterClusterServers()[0])
		if !hasAttr(attrs, matterAttrTargetPositionLiftPercent100ths) {
			t.Errorf("Cover MatterAttributes missing 0x%04X (TargetPositionLiftPercent100ths); got %v",
				matterAttrTargetPositionLiftPercent100ths, attrs)
		}
	})

	t.Run("Blind_Lift", func(t *testing.T) {
		t.Parallel()
		b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
		attrs := matterAttrs(t, b.MatterClusterServers()[0])
		if !hasAttr(attrs, matterAttrTargetPositionLiftPercent100ths) {
			t.Errorf("Blind MatterAttributes missing 0x%04X (TargetPositionLiftPercent100ths); got %v",
				matterAttrTargetPositionLiftPercent100ths, attrs)
		}
	})

	t.Run("Blind_Tilt", func(t *testing.T) {
		t.Parallel()
		b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
		attrs := matterAttrs(t, b.MatterClusterServers()[0])
		if !hasAttr(attrs, matterAttrTargetPositionTiltPercent100ths) {
			t.Errorf("Blind MatterAttributes missing 0x%04X (TargetPositionTiltPercent100ths); got %v",
				matterAttrTargetPositionTiltPercent100ths, attrs)
		}
	})

	t.Run("Garage", func(t *testing.T) {
		t.Parallel()
		g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
		attrs := matterAttrs(t, g.MatterClusterServers()[0])
		if !hasAttr(attrs, matterAttrTargetPositionLiftPercent100ths) {
			t.Errorf("Garage MatterAttributes missing 0x%04X (TargetPositionLiftPercent100ths); got %v",
				matterAttrTargetPositionLiftPercent100ths, attrs)
		}
	})
}

// TestParityCover_TargetPositionMirrors_Current verifies that
// TargetPositionLiftPercent100ths (0x000B) returns the same encoded value as
// CurrentPositionLiftPercent100ths (0x000E) for Cover, Blind, and Garage.
// HM devices converge target == current at rest; this is the spec-compliant
// degenerate case.
func TestParityCover_TargetPositionMirrors_Current(t *testing.T) {
	t.Parallel()

	t.Run("Cover", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
		c.OnLevel(0.7)
		srv := c.MatterClusterServers()[0]

		current, ok := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
		if !ok || current == nil {
			t.Fatalf("CurrentPositionLift read = (%v, %v)", current, ok)
		}
		target, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
		if !ok || target == nil {
			t.Fatalf("TargetPositionLift read = (%v, %v)", target, ok)
		}
		if current.(uint16) != target.(uint16) {
			t.Errorf("Cover: target=%d != current=%d; expected mirror", target.(uint16), current.(uint16))
		}
	})

	t.Run("Blind_Lift", func(t *testing.T) {
		t.Parallel()
		b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
		b.OnLevel(0.4)
		srv := b.MatterClusterServers()[0]

		current, ok := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
		if !ok || current == nil {
			t.Fatalf("Blind CurrentPositionLift read = (%v, %v)", current, ok)
		}
		target, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
		if !ok || target == nil {
			t.Fatalf("Blind TargetPositionLift read = (%v, %v)", target, ok)
		}
		if current.(uint16) != target.(uint16) {
			t.Errorf("Blind lift: target=%d != current=%d; expected mirror", target.(uint16), current.(uint16))
		}
	})

	t.Run("Blind_Tilt", func(t *testing.T) {
		t.Parallel()
		b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
		b.level2.OnEvent(0.6)
		srv := b.MatterClusterServers()[0]

		current, ok := srv.MatterRead(matterAttrCurrentPositionTiltPercent100ths)
		if !ok || current == nil {
			t.Fatalf("Blind CurrentPositionTilt read = (%v, %v)", current, ok)
		}
		target, ok := srv.MatterRead(matterAttrTargetPositionTiltPercent100ths)
		if !ok || target == nil {
			t.Fatalf("Blind TargetPositionTilt read = (%v, %v)", target, ok)
		}
		if current.(uint16) != target.(uint16) {
			t.Errorf("Blind tilt: target=%d != current=%d; expected mirror", target.(uint16), current.(uint16))
		}
	})

	t.Run("Garage", func(t *testing.T) {
		t.Parallel()
		g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
		g.OnState(DoorStateOpen)
		srv := g.MatterClusterServers()[0]

		current, ok := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
		if !ok || current == nil {
			t.Fatalf("Garage CurrentPositionLift read = (%v, %v)", current, ok)
		}
		target, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
		if !ok || target == nil {
			t.Fatalf("Garage TargetPositionLift read = (%v, %v)", target, ok)
		}
		if current.(uint16) != target.(uint16) {
			t.Errorf("Garage: target=%d != current=%d; expected mirror", target.(uint16), current.(uint16))
		}
	})
}

// TestParityCover_TargetPosition_NullWhenUnavailable verifies that when
// position is unobserved, both Current and Target reads return (nil, true) —
// the attribute is supported but the value is transiently null (nullable quality
// per matter.js window-covering-cluster.element.ts).
func TestParityCover_TargetPosition_NullWhenUnavailable(t *testing.T) {
	t.Parallel()

	t.Run("Cover", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
		// No OnLevel called — position unobserved.
		srv := c.MatterClusterServers()[0]
		v, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
		if !ok || v != nil {
			t.Errorf("Cover unobserved TargetPositionLift = (%v, %v), want (nil, true)", v, ok)
		}
	})

	t.Run("Blind_Lift", func(t *testing.T) {
		t.Parallel()
		b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
		// No OnLevel called.
		srv := b.MatterClusterServers()[0]
		v, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
		if !ok || v != nil {
			t.Errorf("Blind unobserved TargetPositionLift = (%v, %v), want (nil, true)", v, ok)
		}
	})

	t.Run("Blind_Tilt", func(t *testing.T) {
		t.Parallel()
		b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
		// No level2.OnEvent called.
		srv := b.MatterClusterServers()[0]
		v, ok := srv.MatterRead(matterAttrTargetPositionTiltPercent100ths)
		if !ok || v != nil {
			t.Errorf("Blind unobserved TargetPositionTilt = (%v, %v), want (nil, true)", v, ok)
		}
	})

	t.Run("Garage", func(t *testing.T) {
		t.Parallel()
		g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
		// No OnState called — position unobserved.
		srv := g.MatterClusterServers()[0]
		v, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
		if !ok || v != nil {
			t.Errorf("Garage unobserved TargetPositionLift = (%v, %v), want (nil, true)", v, ok)
		}
	})
}
