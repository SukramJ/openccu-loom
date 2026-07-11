// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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

// failingWriter fails every SetValue call. Used to verify the
// accepted-before-written contract: the commanded target stays stored
// even when the deferred CCU write later fails — matter.js
// goToLiftPercentage sets the target and returns while the movement
// runs as a detached worker (WindowCoveringServer.ts:574-589, :379-383),
// so a device-side failure never unwinds the acknowledged command.
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
	// Once the debounced CCU write fires, Cover's optimistic write means
	// Current mirrors the new target too — both attributes agree.
	flushGoToWrites(&c.matterGoTo)
	current, ok := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
	if !ok || current.(uint16) != 3000 {
		t.Fatalf("CurrentPositionLift after deferred GoToLift write = (%v, %v), want (3000, true) (optimistic mirror)", current, ok)
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

// TestCoverTargetPosition_DeferredWriteFailureKeepsCommandedTarget pins
// the accepted-before-written contract on the failure path: the
// GoToLiftPercentage invoke succeeds and stores the commanded target
// immediately; when the deferred CCU write later fails it is only
// logged — CurrentPosition stays untouched and the mismatch surfaces
// through the normal value-event echo. Mirrors matter.js, where
// goToLiftPercentage sets the target and returns while the movement
// runs as a detached worker (WindowCoveringServer.ts:574-589, :379-383).
func TestCoverTargetPosition_DeferredWriteFailureKeepsCommandedTarget(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", failingWriter{}, custom.CoverCapabilities{})
	c.OnLevel(0.4)
	srv := c.MatterClusterServers()[0]

	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke err=%v, want acceptance before the deferred write", err)
	}
	flushGoToWrites(&c.matterGoTo) // deferred write fails and is logged

	current, ok := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
	if !ok || current.(uint16) != 6000 {
		t.Fatalf("CurrentPositionLift after failed deferred write = (%v, %v), want (6000, true) — unchanged", current, ok)
	}
	target, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
	if !ok || target.(uint16) != 3000 {
		t.Fatalf("TargetPositionLift after failed deferred write = (%v, %v), want (3000, true) — commanded value kept", target, ok)
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
	flushGoToWrites(&g.matterGoTo)
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

// TestGarageTargetPosition_DeferredWriteFailureKeepsCommandedTarget
// mirrors [TestCoverTargetPosition_DeferredWriteFailureKeepsCommandedTarget]
// for Garage's own [matterTargetState] and debouncer instances.
func TestGarageTargetPosition_DeferredWriteFailureKeepsCommandedTarget(t *testing.T) {
	t.Parallel()
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", failingWriter{})
	g.OnState(DoorStateClosed)
	srv := g.MatterClusterServers()[0]

	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke err=%v, want acceptance before the deferred write", err)
	}
	flushGoToWrites(&g.matterGoTo) // deferred write fails and is logged

	current, ok := srv.MatterRead(matterAttrCurrentPositionLiftPercent100ths)
	if !ok || current.(uint16) != 10000 {
		t.Fatalf("CurrentPositionLift after failed deferred write = (%v, %v), want (10000, true) — unchanged", current, ok)
	}
	target, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
	if !ok || target.(uint16) != 3000 {
		t.Fatalf("TargetPositionLift after failed deferred write = (%v, %v), want (3000, true) — commanded value kept", target, ok)
	}
}

// --- Inferred target on externally initiated movement ---

// readTargetLift is a small assertion helper for the inferred-target
// tests below.
func readTargetLift(t *testing.T, srv interfaces.MatterClusterServer) any {
	t.Helper()
	v, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
	if !ok {
		t.Fatal("TargetPositionLift not readable")
	}
	return v
}

// newDirectionRig builds a channel carrying LEVEL plus a motion
// parameter (DIRECTION or ACTIVITY_STATE, selected by dirParam, with the
// matching VALUE_LIST) and constructs a Cover against it, matching the
// production assembly path where [resolveDirectionDP] picks up the
// channel's motion sensor.
func newDirectionRig(t *testing.T, dirParam hmenum.Parameter, caps custom.CoverCapabilities) (*Cover, *generic.Sensor[int32], *generic.Float) {
	t.Helper()
	const address = "ABC0001:1"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "BLIND", hmenum.ParamsetKeyValues)
	level := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: &stubWriter{},
	})
	ch.Put(level)
	valueList := []string{"NONE", "UP", "DOWN", "UNDEFINED"}
	if dirParam == hmenum.ParameterActivityState {
		valueList = []string{"UNKNOWN", "UP", "DOWN", "STABLE"}
	}
	dir := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(dirParam),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  valueList,
		},
	})
	ch.Put(dir)
	c := New(Config{Channel: ch, Writer: &stubWriter{}, Capabilities: caps})
	// Deferred GoTo*Percentage writes fire only via flushGoToWrites so
	// tests stay deterministic.
	neuterGoToTimers(&c.matterGoTo)
	return c, dir, level
}

// TestCoverInferredTarget_ExternalMovementOverridesStaleTarget: when the
// CCU reports motion opposite to (or past) the last commanded target —
// wall button, CCU program — the reported TargetPositionLift is the
// direction limit, not the stale commanded destination. Apple Home
// derives the motion arrow from target-vs-current, so the stale target
// renders the wrong arrow. No matter.js equivalent exists: its
// WindowCoveringServer derives OperationalStatus FROM the target
// (WindowCoveringServer.ts:271-281) because a native device's movement
// always starts with a target write; the inference here is
// bridge-domain behaviour for externally moved covers.
func TestCoverInferredTarget_ExternalMovementOverridesStaleTarget(t *testing.T) {
	t.Parallel()

	t.Run("OpeningAgainstCloseCommand", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
		srv := c.MatterClusterServers()[0]
		if _, err := srv.MatterInvoke(context.Background(), matterCmdDownOrClose, nil, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("DownOrClose: %v", err)
		}
		c.OnLevel(0.5) // CCU echo: cover halfway, Matter 5000.
		c.OnDirection(DirectionUp)

		if v := readTargetLift(t, srv); v.(uint16) != 0 {
			t.Fatalf("TargetPositionLift while externally opening = %v, want 0 (stale close target overridden)", v)
		}
	})

	t.Run("ClosingAgainstOpenCommand", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
		srv := c.MatterClusterServers()[0]
		if _, err := srv.MatterInvoke(context.Background(), matterCmdUpOrOpen, nil, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("UpOrOpen: %v", err)
		}
		c.OnLevel(0.5)
		c.OnDirection(DirectionDown)

		if v := readTargetLift(t, srv); v.(uint16) != matterCoverPctMax {
			t.Fatalf("TargetPositionLift while externally closing = %v, want 10000 (stale open target overridden)", v)
		}
	})
}

// TestCoverInferredTarget_CommandedTargetAheadPreserved: a commanded
// target that lies strictly ahead of the current position in the
// observed movement direction is the motion's genuine destination and
// must survive the inference.
func TestCoverInferredTarget_CommandedTargetAheadPreserved(t *testing.T) {
	t.Parallel()

	t.Run("Opening", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
		srv := c.MatterClusterServers()[0]
		if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("GoToLiftPercentage: %v", err)
		}
		c.OnLevel(0.2) // CCU echo: Matter 8000 — commanded 3000 is ahead when opening.
		c.OnDirection(DirectionUp)

		if v := readTargetLift(t, srv); v.(uint16) != 3000 {
			t.Fatalf("TargetPositionLift = %v, want 3000 (ahead of current 8000 while opening)", v)
		}
	})

	t.Run("Closing", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
		srv := c.MatterClusterServers()[0]
		if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(8000), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("GoToLiftPercentage: %v", err)
		}
		c.OnLevel(0.8) // CCU echo: Matter 2000 — commanded 8000 is ahead when closing.
		c.OnDirection(DirectionDown)

		if v := readTargetLift(t, srv); v.(uint16) != 8000 {
			t.Fatalf("TargetPositionLift = %v, want 8000 (ahead of current 2000 while closing)", v)
		}
	})
}

// TestCoverInferredTarget_ExternalMovementWithoutCommandTargetsLimit:
// movement with no Matter command in effect reports the direction limit
// as the target.
func TestCoverInferredTarget_ExternalMovementWithoutCommandTargetsLimit(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	c.OnLevel(0.5)

	c.OnDirection(DirectionUp)
	if v := readTargetLift(t, srv); v.(uint16) != 0 {
		t.Fatalf("TargetPositionLift while opening = %v, want 0", v)
	}
	c.OnDirection(DirectionDown)
	if v := readTargetLift(t, srv); v.(uint16) != matterCoverPctMax {
		t.Fatalf("TargetPositionLift while closing = %v, want 10000", v)
	}
}

// TestCoverInferredTarget_MotionStopSnapsTargetToCurrent: an externally
// reported moving→stopped transition clears the commanded target — the
// StopMotion snap semantics (WindowCoveringServer.ts:490-493) applied
// to a stop the CCU reports on its own. Afterwards the target mirrors
// CurrentPosition, including subsequent position echoes.
func TestCoverInferredTarget_MotionStopSnapsTargetToCurrent(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToLiftPercentage: %v", err)
	}
	c.OnLevel(0.2) // CCU echo: Matter 8000.
	c.OnDirection(DirectionUp)
	if v := readTargetLift(t, srv); v.(uint16) != 3000 {
		t.Fatalf("TargetPositionLift while moving = %v, want commanded 3000", v)
	}

	c.OnDirection(DirectionNone) // motion stopped without StopMotion command
	if v := readTargetLift(t, srv); v.(uint16) != 8000 {
		t.Fatalf("TargetPositionLift after stop = %v, want 8000 (mirror current)", v)
	}
	// The commanded target is gone, not just shadowed: a later position
	// echo moves the mirrored target with it.
	c.OnLevel(0.4) // Matter 6000.
	if v := readTargetLift(t, srv); v.(uint16) != 6000 {
		t.Fatalf("TargetPositionLift after later echo = %v, want 6000 (still mirroring)", v)
	}
}

// TestCoverInferredTarget_UnobservedPositionReportsDirectionLimit: with
// no observed position the movement direction alone determines the
// reported target; at rest the read stays transiently null.
func TestCoverInferredTarget_UnobservedPositionReportsDirectionLimit(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]

	c.OnDirection(DirectionUp)
	if v := readTargetLift(t, srv); v.(uint16) != 0 {
		t.Fatalf("TargetPositionLift opening w/o position = %v, want 0", v)
	}
	c.OnDirection(DirectionDown)
	if v := readTargetLift(t, srv); v.(uint16) != matterCoverPctMax {
		t.Fatalf("TargetPositionLift closing w/o position = %v, want 10000", v)
	}
	c.OnDirection(DirectionNone)
	if v := readTargetLift(t, srv); v != nil {
		t.Fatalf("TargetPositionLift stopped w/o position = %v, want nil (transiently null)", v)
	}
}

// TestCoverInferredTarget_InvertedControlFollowsDomainMotion: with
// InvertedControl the wire DIRECTION flips, but IsOpening/IsClosing
// already resolve the domain motion — the inferred target must follow
// the domain direction, not the raw wire value.
func TestCoverInferredTarget_InvertedControlFollowsDomainMotion(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{InvertedControl: true})
	srv := c.MatterClusterServers()[0]
	c.OnLevel(0.3) // inverted: domain position 0.7 → Matter 3000.

	c.OnDirection(DirectionDown) // inverted: domain opening
	if !c.IsOpening() {
		t.Fatal("inverted DirectionDown must report IsOpening")
	}
	if v := readTargetLift(t, srv); v.(uint16) != 0 {
		t.Fatalf("TargetPositionLift while domain-opening = %v, want 0", v)
	}
	c.OnDirection(DirectionUp) // inverted: domain closing
	if v := readTargetLift(t, srv); v.(uint16) != matterCoverPctMax {
		t.Fatalf("TargetPositionLift while domain-closing = %v, want 10000", v)
	}
}

// TestBlindInferredTarget_LiftInferredTiltCommandedUntilStop: the
// inference applies to the lift axis only (the model has no tilt motion
// signal), so a commanded tilt target survives lift movement — but an
// externally reported stop snaps BOTH axes back to mirroring, matching
// the StopMotion handler (WindowCoveringServer.ts:490-493) since the HM
// motor drives both axes in one motion.
func TestBlindInferredTarget_LiftInferredTiltCommandedUntilStop(t *testing.T) {
	t.Parallel()
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	b.OnLevel(0.5)        // Matter lift 5000.
	b.level2.OnEvent(0.6) // Matter tilt 4000.
	srv := b.MatterClusterServers()[0]

	if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToTiltPercentage, uint16(2500), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToTiltPercentage: %v", err)
	}
	b.OnDirection(DirectionDown) // external lift movement

	if v := readTargetLift(t, srv); v.(uint16) != matterCoverPctMax {
		t.Fatalf("TargetPositionLift while closing = %v, want 10000 (inferred)", v)
	}
	if v, ok := srv.MatterRead(matterAttrTargetPositionTiltPercent100ths); !ok || v.(uint16) != 2500 {
		t.Fatalf("TargetPositionTilt while moving = (%v, %v), want commanded 2500", v, ok)
	}

	b.OnDirection(DirectionNone) // stop snaps both axes
	if v := readTargetLift(t, srv); v.(uint16) != 5000 {
		t.Fatalf("TargetPositionLift after stop = %v, want 5000 (mirror current)", v)
	}
	if v, ok := srv.MatterRead(matterAttrTargetPositionTiltPercent100ths); !ok || v.(uint16) != 4000 {
		t.Fatalf("TargetPositionTilt after stop = (%v, %v), want 4000 (mirror current)", v, ok)
	}
}

// TestGarageInferredTarget_SectionMovementAndStop mirrors the Cover
// inference for the SECTION-derived motion signal on garage drives.
func TestGarageInferredTarget_SectionMovementAndStop(t *testing.T) {
	t.Parallel()

	t.Run("CommandedAheadPreservedThenStopSnaps", func(t *testing.T) {
		t.Parallel()
		g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
		g.OnState(DoorStateClosed) // Matter 10000.
		srv := g.MatterClusterServers()[0]
		if _, err := srv.MatterInvoke(context.Background(), matterCmdGoToLiftPercentage, uint16(3000), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("GoToLiftPercentage: %v", err)
		}
		g.OnSection(sectionOpening)
		if v := readTargetLift(t, srv); v.(uint16) != 3000 {
			t.Fatalf("TargetPositionLift while opening = %v, want commanded 3000 (ahead of 10000)", v)
		}
		g.OnSection(0) // motion phase left the opening section — stopped
		if v := readTargetLift(t, srv); v.(uint16) != 10000 {
			t.Fatalf("TargetPositionLift after stop = %v, want 10000 (mirror DOOR_STATE current)", v)
		}
	})

	t.Run("StaleCloseTargetOverriddenByExternalOpening", func(t *testing.T) {
		t.Parallel()
		g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
		g.OnState(DoorStateOpen) // Matter 0.
		srv := g.MatterClusterServers()[0]
		if _, err := srv.MatterInvoke(context.Background(), matterCmdDownOrClose, nil, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("DownOrClose: %v", err)
		}
		g.OnSection(sectionOpening) // user reversed at the wall button
		if v := readTargetLift(t, srv); v.(uint16) != 0 {
			t.Fatalf("TargetPositionLift while externally opening = %v, want 0 (stale close target overridden)", v)
		}
	})
}

// --- Change notifier: motion parameter wired ---

// TestCoverOnMatterValueChangedFiresOnMotionOnlyUpdate: a DIRECTION /
// ACTIVITY_STATE push with no accompanying LEVEL change must reach a
// registered OnMatterValueChanged callback — the movement-start
// transition changes OperationalStatus and the inferred TargetPosition,
// and without the notification no proactive report ships at all.
// Mirrors the reactor set matter.js installs in
// WindowCoveringServer.ts initialize() (:147-155).
func TestCoverOnMatterValueChangedFiresOnMotionOnlyUpdate(t *testing.T) {
	t.Parallel()
	for _, param := range []hmenum.Parameter{hmenum.ParameterDirection, hmenum.ParameterActivityState} {
		t.Run(string(param), func(t *testing.T) {
			t.Parallel()
			c, dir, level := newDirectionRig(t, param, custom.CoverCapabilities{})
			var count int
			unsub := c.OnMatterValueChanged(func() { count++ })

			dir.OnEvent(1) // motion start: UP — no LEVEL update involved
			if count != 1 {
				t.Fatalf("callbacks after motion-only update = %d, want 1", count)
			}
			level.OnEvent(0.5) // position echo still notifies
			if count != 2 {
				t.Fatalf("callbacks after LEVEL update = %d, want 2", count)
			}
			unsub()
			dir.OnEvent(0) // motion stop after unsubscribe
			if count != 2 {
				t.Fatalf("callbacks after unsubscribe = %d, want 2 (detached)", count)
			}
		})
	}
}

// TestBlindOnMatterValueChangedFiresOnTiltUpdate: the Blind notifier
// fans in the slat-tilt axis (LEVEL_2) feeding
// CurrentPositionTiltPercent100ths in addition to the Cover carriers.
func TestBlindOnMatterValueChangedFiresOnTiltUpdate(t *testing.T) {
	t.Parallel()
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	var count int
	unsub := b.OnMatterValueChanged(func() { count++ })

	b.level2.OnEvent(0.3)
	if count != 1 {
		t.Fatalf("callbacks after LEVEL_2 update = %d, want 1", count)
	}
	b.OnLevel(0.7)
	if count != 2 {
		t.Fatalf("callbacks after LEVEL update = %d, want 2", count)
	}
	unsub()
	b.level2.OnEvent(0.9)
	if count != 2 {
		t.Fatalf("callbacks after unsubscribe = %d, want 2 (detached)", count)
	}
}
