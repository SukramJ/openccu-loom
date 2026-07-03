// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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

	t.Run("Cover", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
		attrs := matterAttrs(t, c.MatterClusterServers()[0])
		if !slices.Contains(attrs, matterAttrTargetPositionLiftPercent100ths) {
			t.Errorf("Cover MatterAttributes missing 0x%04X (TargetPositionLiftPercent100ths); got %v",
				matterAttrTargetPositionLiftPercent100ths, attrs)
		}
	})

	t.Run("Blind_Lift", func(t *testing.T) {
		t.Parallel()
		b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
		attrs := matterAttrs(t, b.MatterClusterServers()[0])
		if !slices.Contains(attrs, matterAttrTargetPositionLiftPercent100ths) {
			t.Errorf("Blind MatterAttributes missing 0x%04X (TargetPositionLiftPercent100ths); got %v",
				matterAttrTargetPositionLiftPercent100ths, attrs)
		}
	})

	t.Run("Blind_Tilt", func(t *testing.T) {
		t.Parallel()
		b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
		attrs := matterAttrs(t, b.MatterClusterServers()[0])
		if !slices.Contains(attrs, matterAttrTargetPositionTiltPercent100ths) {
			t.Errorf("Blind MatterAttributes missing 0x%04X (TargetPositionTiltPercent100ths); got %v",
				matterAttrTargetPositionTiltPercent100ths, attrs)
		}
	})

	t.Run("Garage", func(t *testing.T) {
		t.Parallel()
		g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
		attrs := matterAttrs(t, g.MatterClusterServers()[0])
		if !slices.Contains(attrs, matterAttrTargetPositionLiftPercent100ths) {
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

// failingWriter fails every SetValue call. Used to verify that a rejected
// wire write never lands in [matterTargetState] — WindowCoveringServer.ts
// only advances TargetPosition after the underlying set-position call
// resolves; matter.js command handlers throw before touching state on
// rejection.
type failingWriter struct{}

var errFailingWriterWrite = errors.New("cover: simulated write failure")

func (failingWriter) SetValue(context.Context, string, hmenum.Parameter, any, hmenum.CommandPriority) error {
	return errFailingWriterWrite
}

// TestCoverTargetPosition_GoToLiftPercentage_TracksIndependentlyOfCurrent
// is the behavioural core of the fix: TargetPositionLiftPercent100ths
// (0x000B) is a commanded destination stored in [matterTargetState],
// distinct from CurrentPositionLiftPercent100ths (0x000E) — the observed
// position (WindowCoveringServer.ts:578 sets the target on
// GoToLiftPercentage; :142 is the mirror-when-unset default).
//
// Cover.SetPosition applies its write optimistically
// (generic/datapoint.go sendAndObserve), so CurrentPosition also jumps to
// the commanded value the instant the invoke returns — target and current
// read equal right after a successful GoToLiftPercentage. The stored
// target only becomes externally observable once a later CCU-confirmed
// LEVEL report (a genuine mid-motion echo) lands with a different value:
// OnEventAt treats a mismatching confirmation as authoritative and
// replaces the optimistic guess, while [matterTargetState] is untouched
// by Position() updates — only a new MatterInvoke or StopMotion changes
// it. That is the moment Current and Target diverge.
func TestCoverTargetPosition_GoToLiftPercentage_TracksIndependentlyOfCurrent(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	c.OnLevel(0.4) // CCU-confirmed baseline: HM 0.4 → Matter 6000.
	srv := c.MatterClusterServers()[0]

	if v, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths); !ok || v.(uint16) != 6000 {
		t.Fatalf("baseline TargetPositionLift = (%v, %v), want (6000, true) — must mirror current", v, ok)
	}

	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToLiftPercentage(3000): %v", err)
	}
	target, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
	if !ok || target.(uint16) != 3000 {
		t.Fatalf("TargetPositionLift after GoToLift = (%v, %v), want (3000, true)", target, ok)
	}
	// Cover's optimistic write means Current mirrors the new target
	// immediately too — both attributes agree at this instant.
	current, ok := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
	if !ok || current.(uint16) != 3000 {
		t.Fatalf("CurrentPositionLift right after GoToLift = (%v, %v), want (3000, true) (optimistic mirror)", current, ok)
	}

	// A mismatching CCU-confirmed echo (mid-motion telemetry) replaces the
	// optimistic guess on Position() but must not touch the stored Matter
	// target — this is the divergence the fix exists to preserve.
	c.OnLevel(0.55) // HM 0.55 → Matter 4500, distinct from the 3000 target.
	current2, ok := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
	if !ok || current2.(uint16) != 4500 {
		t.Fatalf("CurrentPositionLift after mismatching echo = (%v, %v), want (4500, true)", current2, ok)
	}
	target2, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
	if !ok || target2.(uint16) != 3000 {
		t.Fatalf("TargetPositionLift after mismatching echo = (%v, %v), want (3000, true) — unaffected by Position()", target2, ok)
	}
	if current2.(uint16) == target2.(uint16) {
		t.Fatalf("expected Current(%d) != Target(%d) after the mismatching echo", current2, target2)
	}
}

// TestCoverTargetPosition_UpOrOpenAndDownOrClose_SetExtremes verifies
// UpOrOpen sets the lift target fully open (0) and DownOrClose sets it
// fully closed (10000) — WindowCoveringServer.ts:522 / :546.
func TestCoverTargetPosition_UpOrOpenAndDownOrClose_SetExtremes(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	c.OnLevel(0.4)
	srv := c.MatterClusterServers()[0]

	if _, err := srv.MatterInvoke(context.Background(), matterCmdUpOrOpen, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("UpOrOpen: %v", err)
	}
	if v, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths); !ok || v.(uint16) != 0 {
		t.Fatalf("TargetPositionLift after UpOrOpen = (%v, %v), want (0, true)", v, ok)
	}

	if _, err := srv.MatterInvoke(context.Background(), matterCmdDownOrClose, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("DownOrClose: %v", err)
	}
	if v, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths); !ok || v.(uint16) != 10000 {
		t.Fatalf("TargetPositionLift after DownOrClose = (%v, %v), want (10000, true)", v, ok)
	}
}

// TestCoverTargetPosition_StopMotionClearsTarget verifies StopMotion snaps
// TargetPositionLiftPercent100ths back to mirroring CurrentPosition
// (WindowCoveringServer.ts:490-493 handleStopMovement), both when the
// cover actually supports the STOP action and when [Cover.Stop] is a
// silent no-op (Capabilities.SupportsStop == false) — the server clears
// the stored target as soon as Stop returns a nil error, regardless of
// whether a wire write actually happened.
func TestCoverTargetPosition_StopMotionClearsTarget(t *testing.T) {
	t.Parallel()

	t.Run("SupportsStop", func(t *testing.T) {
		t.Parallel()
		w := &stubWriter{}
		c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{SupportsStop: true})
		c.OnLevel(0.4)
		srv := c.MatterClusterServers()[0]
		if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("GoToLift: %v", err)
		}
		if _, err := srv.MatterInvoke(context.Background(), matterCmdStopMotion, nil, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("StopMotion: %v", err)
		}
		if w.last != true {
			t.Fatalf("STOP parameter not written, last=%v", w.last)
		}
		current, _ := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
		target, _ := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
		if current.(uint16) != target.(uint16) {
			t.Fatalf("after Stop: current=%v target=%v, want equal (mirror)", current, target)
		}
	})

	t.Run("NoSupportsStop_SilentNoOpStillClears", func(t *testing.T) {
		t.Parallel()
		w := &stubWriter{}
		c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{}) // SupportsStop defaults false
		c.OnLevel(0.4)
		srv := c.MatterClusterServers()[0]
		if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("GoToLift: %v", err)
		}
		wireAfterGoToLift := w.last
		if _, err := srv.MatterInvoke(context.Background(), matterCmdStopMotion, nil, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("StopMotion: %v", err)
		}
		// Cover.Stop is a no-op without SupportsStop — no new wire write.
		if w.last != wireAfterGoToLift {
			t.Fatalf("STOP unexpectedly reached the wire: last=%v, want unchanged %v", w.last, wireAfterGoToLift)
		}
		current, _ := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
		target, _ := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
		if current.(uint16) != target.(uint16) {
			t.Fatalf("after silent Stop: current=%v target=%v, want equal (mirror) — clear() still ran", current, target)
		}
	})
}

// TestCoverTargetPosition_FailedInvokeLeavesTargetUnset verifies that a
// GoToLiftPercentage invoke whose underlying wire write fails neither
// stores a target nor changes CurrentPosition — matter.js command
// handlers only mutate state after the set-position call resolves.
func TestCoverTargetPosition_FailedInvokeLeavesTargetUnset(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", failingWriter{}, custom.CoverCapabilities{})
	c.OnLevel(0.4)
	srv := c.MatterClusterServers()[0]

	_, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh)
	if !errors.Is(err, errFailingWriterWrite) {
		t.Fatalf("MatterInvoke err=%v, want errFailingWriterWrite", err)
	}
	current, ok := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
	if !ok || current.(uint16) != 6000 {
		t.Fatalf("CurrentPositionLift after failed invoke = (%v, %v), want (6000, true) — unchanged", current, ok)
	}
	target, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
	if !ok || target.(uint16) != 6000 {
		t.Fatalf("TargetPositionLift after failed invoke = (%v, %v), want (6000, true) — still mirroring current", target, ok)
	}
}

// TestBlindTargetPosition_GoToLiftAndTilt_TrackAxesIndependently is the
// clearest demonstration of the fix: [Blind.SetPosition] / [Blind.SetTilt]
// write LEVEL/LEVEL_2 through [Blind.sendCombined], which never touches
// the underlying data points' optimistic-write machinery. So unlike plain
// Cover, a Blind's CurrentPosition genuinely stays put the instant a
// GoTo*Percentage invoke returns — Target and Current diverge without any
// extra CCU echo required.
func TestBlindTargetPosition_GoToLiftAndTilt_TrackAxesIndependently(t *testing.T) {
	t.Parallel()
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	b.OnLevel(0.4)        // Matter lift 6000.
	b.level2.OnEvent(0.6) // Matter tilt 4000.
	srv := b.MatterClusterServers()[0]

	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToLiftPercentage: %v", err)
	}
	if v, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths); !ok || v.(uint16) != 3000 {
		t.Fatalf("TargetPositionLift = (%v, %v), want (3000, true)", v, ok)
	}
	if v, ok := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths); !ok || v.(uint16) != 6000 {
		t.Fatalf("CurrentPositionLift = (%v, %v), want (6000, true) — sendCombined does not update LEVEL", v, ok)
	}
	// Tilt axis is untouched by a lift-only command.
	if v, ok := srv.MatterRead(matterAttrTargetPositionTiltPercent100ths); !ok || v.(uint16) != 4000 {
		t.Fatalf("TargetPositionTilt = (%v, %v), want (4000, true) — still mirroring current", v, ok)
	}

	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToTiltPercentage, uint16(2500), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToTiltPercentage: %v", err)
	}
	if v, ok := srv.MatterRead(matterAttrTargetPositionTiltPercent100ths); !ok || v.(uint16) != 2500 {
		t.Fatalf("TargetPositionTilt = (%v, %v), want (2500, true)", v, ok)
	}
	if v, ok := srv.MatterRead(matterAttrCurrentPositionTiltPercent100ths); !ok || v.(uint16) != 4000 {
		t.Fatalf("CurrentPositionTilt = (%v, %v), want (4000, true) — unchanged", v, ok)
	}
	// The earlier lift target must survive an unrelated tilt command.
	if v, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths); !ok || v.(uint16) != 3000 {
		t.Fatalf("TargetPositionLift after GoToTilt = (%v, %v), want (3000, true) — unaffected", v, ok)
	}
}

// TestBlindTargetPosition_UpOrOpenAndDownOrClose_SetBothAxes verifies
// UpOrOpen / DownOrClose set BOTH the lift and tilt targets in lockstep —
// WindowCoveringServer.ts:522-525 / :546-549.
func TestBlindTargetPosition_UpOrOpenAndDownOrClose_SetBothAxes(t *testing.T) {
	t.Parallel()
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	b.OnLevel(0.4)
	b.level2.OnEvent(0.6)
	srv := b.MatterClusterServers()[0]

	if _, err := srv.MatterInvoke(context.Background(), matterCmdUpOrOpen, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("UpOrOpen: %v", err)
	}
	lift, _ := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
	tilt, _ := srv.MatterRead(matterAttrTargetPositionTiltPercent100ths)
	if lift.(uint16) != 0 || tilt.(uint16) != 0 {
		t.Fatalf("after UpOrOpen: lift=%v tilt=%v, want both 0", lift, tilt)
	}

	if _, err := srv.MatterInvoke(context.Background(), matterCmdDownOrClose, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("DownOrClose: %v", err)
	}
	lift, _ = srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
	tilt, _ = srv.MatterRead(matterAttrTargetPositionTiltPercent100ths)
	if lift.(uint16) != 10000 || tilt.(uint16) != 10000 {
		t.Fatalf("after DownOrClose: lift=%v tilt=%v, want both 10000", lift, tilt)
	}
}

// TestBlindTargetPosition_StopMotionClearsBothAxes verifies StopMotion
// snaps both the lift and tilt targets back to mirroring their respective
// Current attributes (WindowCoveringServer.ts:490-493).
func TestBlindTargetPosition_StopMotionClearsBothAxes(t *testing.T) {
	t.Parallel()
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true, SupportsStop: true}, BlindKindHM)
	b.OnLevel(0.4)
	b.level2.OnEvent(0.6)
	srv := b.MatterClusterServers()[0]

	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToLiftPercentage: %v", err)
	}
	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToTiltPercentage, uint16(2500), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToTiltPercentage: %v", err)
	}
	if _, err := srv.MatterInvoke(context.Background(), matterCmdStopMotion, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("StopMotion: %v", err)
	}

	liftCur, _ := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
	liftTgt, _ := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
	if liftCur.(uint16) != liftTgt.(uint16) {
		t.Fatalf("after Stop: lift current=%v target=%v, want equal (mirror)", liftCur, liftTgt)
	}
	tiltCur, _ := srv.MatterRead(matterAttrCurrentPositionTiltPercent100ths)
	tiltTgt, _ := srv.MatterRead(matterAttrTargetPositionTiltPercent100ths)
	if tiltCur.(uint16) != tiltTgt.(uint16) {
		t.Fatalf("after Stop: tilt current=%v target=%v, want equal (mirror)", tiltCur, tiltTgt)
	}
}

// TestGarageTargetPosition_UpOrOpenAndDownOrClose_SetExtremes uses a fresh
// (state-unobserved) Garage rig per command. [Garage.Position] derives
// solely from DOOR_STATE, which a DOOR_COMMAND write never updates on its
// own — so [Garage.Open] / [Garage.Close] gate on
// Cover.IsStateChangeArgs's "not yet observed" branch (always true) to
// guarantee a real wire write. Reusing one rig across both commands would
// hit a different gotcha: after Open() the door state is still
// "unobserved" (no OnState follow-up), so a subsequent Close() also sees
// "not observed" and writes again — harmless here, but a rig seeded with
// OnState(DoorStateClosed) before an Open then a same-state Close would
// silently no-op the second command (IsStateChangeArgs sees the door
// already satisfies the requested axis) while still clearing/setting the
// Matter target, since the server only checks for a nil error.
func TestGarageTargetPosition_UpOrOpenAndDownOrClose_SetExtremes(t *testing.T) {
	t.Parallel()

	t.Run("UpOrOpen", func(t *testing.T) {
		t.Parallel()
		w := &stubWriter{}
		g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", w)
		srv := g.MatterClusterServers()[0]
		if _, err := srv.MatterInvoke(context.Background(), matterCmdUpOrOpen, nil, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("UpOrOpen: %v", err)
		}
		if w.last != string(DoorCommandOpen) {
			t.Fatalf("DOOR_COMMAND=%v, want OPEN", w.last)
		}
		current, ok := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
		if !ok || current != nil {
			t.Fatalf("CurrentPositionLift = (%v, %v), want (nil, true) — DOOR_STATE never observed", current, ok)
		}
		target, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
		if !ok || target.(uint16) != 0 {
			t.Fatalf("TargetPositionLift = (%v, %v), want (0, true) — stored independently of Position()", target, ok)
		}
	})

	t.Run("DownOrClose", func(t *testing.T) {
		t.Parallel()
		w := &stubWriter{}
		g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", w)
		srv := g.MatterClusterServers()[0]
		if _, err := srv.MatterInvoke(context.Background(), matterCmdDownOrClose, nil, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("DownOrClose: %v", err)
		}
		if w.last != string(DoorCommandClose) {
			t.Fatalf("DOOR_COMMAND=%v, want CLOSE", w.last)
		}
		target, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
		if !ok || target.(uint16) != 10000 {
			t.Fatalf("TargetPositionLift = (%v, %v), want (10000, true)", target, ok)
		}
	})
}

// TestGarageTargetPosition_GoToLiftPercentageStoresRawPercent verifies the
// stored target is the raw requested Percent100ths value
// (WindowCoveringServer.ts:578), not the coarse OPEN/VENT/CLOSE bucket
// [Garage.SetPosition] maps it onto on the wire.
func TestGarageTargetPosition_GoToLiftPercentageStoresRawPercent(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", w)
	g.OnState(DoorStateClosed)
	srv := g.MatterClusterServers()[0]

	// HM level 0.7 (> 0.50 threshold) maps onto DoorCommandOpen, but the
	// stored Matter target must stay the exact requested 3000.
	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToLiftPercentage: %v", err)
	}
	if w.last != string(DoorCommandOpen) {
		t.Fatalf("DOOR_COMMAND=%v, want OPEN", w.last)
	}
	target, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
	if !ok || target.(uint16) != 3000 {
		t.Fatalf("TargetPositionLift = (%v, %v), want (3000, true) — raw requested percent", target, ok)
	}
}

// TestGarageTargetPosition_StopMotionClearsTarget verifies StopMotion
// clears the stored target so it mirrors CurrentPosition again. Unlike
// Cover/Blind, [Garage.Stop] is unconditional — it always writes
// DOOR_COMMAND=STOP regardless of Capabilities.SupportsStop.
func TestGarageTargetPosition_StopMotionClearsTarget(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", w)
	g.OnState(DoorStateClosed)
	srv := g.MatterClusterServers()[0]

	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToLiftPercentage: %v", err)
	}
	if _, err := srv.MatterInvoke(context.Background(), matterCmdStopMotion, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("StopMotion: %v", err)
	}
	if w.last != string(DoorCommandStop) {
		t.Fatalf("DOOR_COMMAND=%v, want STOP", w.last)
	}
	current, _ := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
	target, _ := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
	if current.(uint16) != target.(uint16) {
		t.Fatalf("after Stop: current=%v target=%v, want equal (mirror)", current, target)
	}
}

// TestGarageTargetPosition_FailedInvokeLeavesTargetUnset mirrors
// [TestCoverTargetPosition_FailedInvokeLeavesTargetUnset] for Garage's own
// [matterTargetState] instance.
func TestGarageTargetPosition_FailedInvokeLeavesTargetUnset(t *testing.T) {
	t.Parallel()
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", failingWriter{})
	g.OnState(DoorStateClosed)
	srv := g.MatterClusterServers()[0]

	_, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh)
	if !errors.Is(err, errFailingWriterWrite) {
		t.Fatalf("MatterInvoke err=%v, want errFailingWriterWrite", err)
	}
	current, ok := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
	if !ok || current.(uint16) != 10000 {
		t.Fatalf("CurrentPositionLift after failed invoke = (%v, %v), want (10000, true) — unchanged", current, ok)
	}
	target, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
	if !ok || target.(uint16) != 10000 {
		t.Fatalf("TargetPositionLift after failed invoke = (%v, %v), want (10000, true) — still mirroring current", target, ok)
	}
}
