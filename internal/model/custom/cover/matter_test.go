// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	mattercluster "github.com/SukramJ/go-fabric/cluster"
	clusterwire "github.com/SukramJ/go-fabric/cluster/wire"

	windowcoveringspec "github.com/SukramJ/go-fabric/cluster/spec/windowcovering"

	"github.com/SukramJ/go-fabric/cluster/spec"

	fabriccover "github.com/SukramJ/go-fabric/cluster/cover"

	"github.com/SukramJ/go-fabric/im"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// TestMatterDeviceTypeIsWindowCovering covers the two lift projections —
// the device type ID is uniform; the per-type distinction lives in the
// cluster's Type / EndProductType attributes (Matter spec design). The
// garage projects as a Closure instead; see
// TestGarageMatterDeviceTypeIsClosure.
func TestMatterDeviceTypeIsWindowCovering(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	if c.MatterDeviceType() != 0x0202 {
		t.Fatalf("Cover.MatterDeviceType = 0x%04X, want 0x0202", c.MatterDeviceType())
	}
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	if b.MatterDeviceType() != 0x0202 {
		t.Fatalf("Blind.MatterDeviceType = 0x%04X, want 0x0202", b.MatterDeviceType())
	}
}

// TestGarageMatterDeviceTypeIsClosure pins the garage onto the Closure
// device type (0x0230) rather than WindowCovering.
//
// The Closure device type requires ClosureControl and forbids
// WindowCovering outright (matter.js closure-device.element.ts:20,
// conformance "X"), which is what lets a garage drive name its
// ventilation stop instead of encoding it as a lift percentage.
func TestGarageMatterDeviceTypeIsClosure(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	if g.MatterDeviceType() != 0x0230 {
		t.Fatalf("Garage.MatterDeviceType = 0x%04X, want 0x0230 (Closure)", g.MatterDeviceType())
	}
}

// TestPositionInversionConvention is the central encoding regression
// guard: HM 1.0 (fully open) MUST encode to Matter 0; HM 0.0 (fully
// closed) MUST encode to Matter 10000. This is the inverse of every
// other percent-style cluster in Matter.
func TestPositionInversionConvention(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	c.OnLevel(1.0) // HM "fully open"
	srv := c.MatterClusterServers()[0]
	v, ok := srv.MatterRead(0x000E) // CurrentPositionLiftPercent100ths
	if !ok {
		t.Fatalf("Position not observed")
	}
	if v.(uint16) != 0 {
		t.Fatalf("HM 1.0 (open) → Matter %d, want 0 (open)", v.(uint16))
	}

	c.OnLevel(0.0) // HM "fully closed"
	v, _ = srv.MatterRead(0x000E)
	if v.(uint16) != 10000 {
		t.Fatalf("HM 0.0 (closed) → Matter %d, want 10000 (closed)", v.(uint16))
	}
}

// TestPositionInversionMidpoint covers the midpoint encoding.
func TestPositionInversionMidpoint(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	c.OnLevel(0.25) // HM 25 % open
	srv := c.MatterClusterServers()[0]
	v, _ := srv.MatterRead(0x000E)
	// Matter "75 % closed" = 7500
	if v.(uint16) != 7500 {
		t.Fatalf("HM 0.25 → Matter %d, want 7500", v.(uint16))
	}
}

// TestUpOrOpenCommand routes through Cover.Open.
func TestUpOrOpenCommand(t *testing.T) {
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	if _, err := srv.MatterInvoke(context.Background(), 0x00, nil); err != nil {
		t.Fatalf("UpOrOpen err: %v", err)
	}
	if w.last.(float64) != 1.0 {
		t.Fatalf("UpOrOpen wrote %v, want 1.0 (HM domain-level)", w.last)
	}
}

// TestDownOrCloseCommand routes through Cover.Close.
func TestDownOrCloseCommand(t *testing.T) {
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	if _, err := srv.MatterInvoke(context.Background(), 0x01, nil); err != nil {
		t.Fatalf("DownOrClose err: %v", err)
	}
	if w.last.(float64) != 0.0 {
		t.Fatalf("DownOrClose wrote %v, want 0.0", w.last)
	}
}

// TestStopMotionCommand confirms StopMotion writes the STOP parameter
// when the device claims SupportsStop.
func TestStopMotionCommand(t *testing.T) {
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{SupportsStop: true})
	srv := c.MatterClusterServers()[0]
	if _, err := srv.MatterInvoke(context.Background(), 0x02, nil); err != nil {
		t.Fatalf("StopMotion err: %v", err)
	}
	if w.last != true {
		t.Fatalf("StopMotion wrote %v, want true", w.last)
	}
}

// TestGoToLiftPercentageInversion is the matching inversion test for
// the write side: Matter 7500 ("75 % closed") must reach the wire as
// HM domain-level 0.25 ("25 % open"). The CCU write is debounced, so
// the wire assertion runs after the flush.
func TestGoToLiftPercentageInversion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer drainOptimisticRollbacks()
		w := &stubWriter{}
		c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
		srv := c.MatterClusterServers()[0]
		if _, err := srv.MatterInvoke(context.Background(), 0x05, uint16(7500)); err != nil {
			t.Fatalf("GoToLift err: %v", err)
		}
		settleGoToWrites()
		if w.last.(float64) != 0.25 {
			t.Fatalf("Matter 7500 → HM %v, want 0.25", w.last)
		}
	})
}

// TestBlindGoToTiltPercentage exercises the lift+tilt projection's tilt write
// path. HM blinds write LEVEL_COMBINED as a comma-separated hex string
// "0xLL,0xTT" where each byte = int(position * 100 * 2).
func TestBlindGoToTiltPercentage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer drainOptimisticRollbacks()
		w := &putWriter{}
		b := newBlindRig(t, "VCU3560967:1", w, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
		srv := b.MatterClusterServers()[0]
		if _, err := srv.MatterInvoke(context.Background(), 0x08, uint16(2500)); err != nil {
			t.Fatalf("GoToTilt err: %v", err)
		}
		settleGoToWrites()
		cc := w.combinedCalls()
		if len(cc) != 1 {
			t.Fatalf("expected 1 LEVEL_COMBINED SetValue, got %d", len(cc))
		}
		// Matter 2500 → HM tilt 0.75 → int(0.75*100*2)=150=0x96;
		// level=0 (not observed) → 0x00 → "0x00,0x96".
		got, ok := cc[0].value.(string)
		if !ok {
			t.Fatalf("LEVEL_COMBINED value type = %T, want string", cc[0].value)
		}
		if got != "0x00,0x96" {
			t.Fatalf("LEVEL_COMBINED=%q, want 0x00,0x96 (tilt=0.75, level=0)", got)
		}
	})
}

// TestGarageStateMapsToNamedStops pins the door-state to
// CurrentPositionEnum mapping.
//
// The three states map onto named stops rather than onto percentages.
// The ventilation stop is the one that could not survive the old
// projection: as a lift percentage it was a value near the middle, which
// a controller cannot label and a read cannot tell apart from a door
// resting halfway.
func TestGarageStateMapsToNamedStops(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	srv := g.MatterClusterServers()[0]

	cases := []struct {
		state DoorState
		want  clusterwire.ClosureCurrentPosition
	}{
		{DoorStateOpen, clusterwire.ClosureCurrentPositionFullyOpened},
		{DoorStateVentilation, clusterwire.ClosureCurrentPositionOpenedForVentilation},
		{DoorStateClosed, clusterwire.ClosureCurrentPositionFullyClosed},
	}
	for _, tc := range cases {
		g.OnState(tc.state)
		v, ok := srv.MatterRead(clusterwire.ClosureControlAttrOverallCurrentState)
		if !ok {
			t.Fatalf("state=%s: OverallCurrentState not readable", tc.state)
		}
		st, ok := v.(*clusterwire.ClosureOverallCurrentState)
		if !ok {
			t.Fatalf("state=%s: OverallCurrentState = %T", tc.state, v)
		}
		if st.Position == nil {
			t.Fatalf("state=%s: Position is null, want %d", tc.state, tc.want)
		}
		if *st.Position != tc.want {
			t.Errorf("state=%s → Position %d, want %d", tc.state, *st.Position, tc.want)
		}
	}
}

// TestGarageTravellingReportsNullPosition pins the mid-travel shape: a
// door between named stops has no position to report.
//
// This is the case the percentage projection could not express at all —
// every lift value is some position, so a travelling door had to claim
// one.
func TestGarageTravellingReportsNullPosition(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	srv := g.MatterClusterServers()[0]

	g.OnState(DoorStateOpen)
	g.OnState(DoorStateUnknown)

	v, ok := srv.MatterRead(clusterwire.ClosureControlAttrOverallCurrentState)
	if !ok {
		t.Fatal("OverallCurrentState not readable")
	}
	st, _ := v.(*clusterwire.ClosureOverallCurrentState)
	if st.Position != nil {
		t.Errorf("Position = %d while travelling, want null", *st.Position)
	}
}

// TestGarageAdvertisesVentilationFeature pins that the FeatureMap carries
// Ventilation, with Positioning alongside it as its "[PS]" conformance
// requires.
//
// Without the feature bit a controller has no reason to offer the
// ventilation stop, whatever the position enum says.
func TestGarageAdvertisesVentilationFeature(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	srv := g.MatterClusterServers()[0]
	v, ok := srv.MatterRead(mattercluster.AttrGlobalFeatureMap)
	if !ok {
		t.Fatal("FeatureMap not readable")
	}
	fm, ok := v.(uint32)
	if !ok {
		t.Fatalf("FeatureMap = %T, want uint32", v)
	}
	if fm&clusterwire.ClosureControlFeatureVentilation == 0 {
		t.Error("FeatureMap does not advertise Ventilation")
	}
	if fm&clusterwire.ClosureControlFeaturePositioning == 0 {
		t.Error("FeatureMap advertises Ventilation without Positioning, which its \"[PS]\" conformance forbids")
	}
}

// TestGarageMoveToDispatchesDoorCommand wires each Matter MoveTo target
// onto the DOOR_COMMAND it means.
func TestGarageMoveToDispatchesDoorCommand(t *testing.T) {
	cases := []struct {
		name   string
		target clusterwire.ClosureTargetPosition
		want   string
	}{
		{"close", clusterwire.ClosureTargetPositionMoveToFullyClosed, "CLOSE"},
		{"open", clusterwire.ClosureTargetPositionMoveToFullyOpen, "OPEN"},
		{"ventilate", clusterwire.ClosureTargetPositionMoveToVentilationPosition, "PARTIAL_OPEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &stubWriter{}
			g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", w)
			srv := g.MatterClusterServers()[0]
			target := tc.target
			if _, err := srv.MatterInvoke(context.Background(), clusterwire.ClosureControlCmdMoveTo,
				clusterwire.MoveToRequest{Position: &target}); err != nil {
				t.Fatalf("MoveTo: %v", err)
			}
			if w.last != tc.want {
				t.Fatalf("MoveTo(%s) wrote %v, want %q", tc.name, w.last, tc.want)
			}
		})
	}
}

// TestOperationalStatusReflectsDirection covers the global+lift bits
// of the OperationalStatus byte while the cover is reported moving.
func TestOperationalStatusReflectsDirection(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]

	// Unobserved direction → stopped.
	v, _ := srv.MatterRead(0x000A)
	if v.(uint8) != 0 {
		t.Fatalf("unmoved status=0x%02X, want 0", v.(uint8))
	}

	c.OnDirection(DirectionUp)
	v, _ = srv.MatterRead(0x000A)
	// Opening: global=01, lift=01 → 0b00000101 = 0x05
	if v.(uint8) != 0x05 {
		t.Fatalf("opening status=0x%02X, want 0x05", v.(uint8))
	}

	c.OnDirection(DirectionDown)
	v, _ = srv.MatterRead(0x000A)
	// Closing: global=10, lift=10 → 0b00001010 = 0x0A
	if v.(uint8) != 0x0A {
		t.Fatalf("closing status=0x%02X, want 0x0A", v.(uint8))
	}
}

// --- OnMatterValueChanged (MatterChangeNotifier) ---
//
// Cover and Blind fan the LEVEL Float, the motion parameter (DIRECTION /
// ACTIVITY_STATE), and (Blind) LEVEL_2 into one notifier; their coverage
// lives in matter_target_position_test.go next to the inferred-target
// behaviour. Garage carries its own DPs (DOOR_STATE / SECTION) and gets
// dedicated coverage here.

// TestGarageOnMatterValueChangedFiresOnConfirmedDoorStateChange verifies
// that a CCU-confirmed DOOR_STATE change (e.g. the door operated at the
// wall button, not through Apple) reaches a registered
// OnMatterValueChanged callback.
func TestGarageOnMatterValueChangedFiresOnConfirmedDoorStateChange(t *testing.T) {
	g, doorStateDP, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	var count int
	_ = g.OnMatterValueChanged(func() { count++ })
	fireDoorState(t, doorStateDP, string(DoorStateOpen))
	fireDoorState(t, doorStateDP, string(DoorStateClosed))
	if count != 2 {
		t.Fatalf("expected 2 callback invocations, got %d", count)
	}
}

// TestGarageOnMatterValueChangedUnsubscribeStopsCallback verifies that
// the returned closure detaches every wired DP so a further confirmed
// change does not fire the callback again.
func TestGarageOnMatterValueChangedUnsubscribeStopsCallback(t *testing.T) {
	g, doorStateDP, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	var count int
	unsub := g.OnMatterValueChanged(func() { count++ })
	fireDoorState(t, doorStateDP, string(DoorStateOpen))
	unsub()
	fireDoorState(t, doorStateDP, string(DoorStateClosed))
	if count != 1 {
		t.Fatalf("expected 1 callback invocation after unsub, got %d", count)
	}
}

// TestGarageOnMatterValueChangedFansSection confirms the section DP
// (SECTION) also fans into the same callback, not just DOOR_STATE.
func TestGarageOnMatterValueChangedFansSection(t *testing.T) {
	g, _, sectionDP := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	var count int
	_ = g.OnMatterValueChanged(func() { count++ })
	sectionDP.OnEvent(1)
	if count != 1 {
		t.Fatalf("expected 1 callback invocation from section change, got %d", count)
	}
}

// TestGarageOnMatterValueChangedNilSafe verifies nil-receiver and
// nil-callback safety.
func TestGarageOnMatterValueChangedNilSafe(t *testing.T) {
	var g *Garage
	unsub := g.OnMatterValueChanged(func() {})
	if unsub == nil {
		t.Fatal("nil Garage: OnMatterValueChanged must return non-nil unsub")
	}
	unsub() // must not panic

	gg, doorStateDP, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	unsub2 := gg.OnMatterValueChanged(nil)
	if unsub2 == nil {
		t.Fatal("nil callback: OnMatterValueChanged must return non-nil unsub")
	}
	fireDoorState(t, doorStateDP, string(DoorStateOpen)) // must not panic with no subscriber
}

// windowCoveringServer returns the go-fabric WindowCovering server a
// Cover or Blind mounts.
func windowCoveringServer(t *testing.T, src interfaces.MatterEndpointSource) *fabriccover.WindowCoveringServer {
	t.Helper()
	servers := src.MatterClusterServers()
	if len(servers) != 1 {
		t.Fatalf("MatterClusterServers = %d servers, want the one WindowCovering server", len(servers))
	}
	srv, ok := servers[0].(*fabriccover.WindowCoveringServer)
	if !ok {
		t.Fatalf("WindowCovering server is %T, want *fabriccover.WindowCoveringServer", servers[0])
	}
	return srv
}

// readU16 reads a uint16 attribute from the server.
func readU16(t *testing.T, srv *fabriccover.WindowCoveringServer, attrID uint32) uint16 {
	t.Helper()
	v, ok := srv.MatterRead(attrID)
	if !ok {
		t.Fatalf("MatterRead(0x%04X): not served", attrID)
	}
	u, ok := v.(uint16)
	if !ok {
		t.Fatalf("MatterRead(0x%04X) = %T, want uint16", attrID, v)
	}
	return u
}

// TestCoverFeatureMapLiftOnly confirms a plain Cover serves the lift
// axis only: LF | PA_LF, no tilt bit, and the FeatureMap is the
// server's (go-fabric cover.ServedFeatureMap).
func TestCoverFeatureMapLiftOnly(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := windowCoveringServer(t, c)
	v, ok := srv.MatterRead(spec.AttrFeatureMap)
	if !ok {
		t.Fatal("FeatureMap not readable")
	}
	got := v.(uint32)
	want := uint32(windowcoveringspec.FeatureLift | windowcoveringspec.FeaturePositionAwareLift)
	if got != want {
		t.Fatalf("Cover FeatureMap = 0x%08X, want 0x%08X (LF | PA_LF)", got, want)
	}
	if got != fabriccover.ServedFeatureMap {
		t.Fatalf("Cover FeatureMap = 0x%08X, want go-fabric's lift-only ServedFeatureMap 0x%08X", got, fabriccover.ServedFeatureMap)
	}
}

// TestBlindFeatureMapIncludesTilt confirms a Blind serves both axes:
// LF | TL | PA_LF | PA_TL (go-fabric cover.ServedLiftTiltFeatureMap).
func TestBlindFeatureMapIncludesTilt(t *testing.T) {
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	srv := windowCoveringServer(t, b)
	v, ok := srv.MatterRead(spec.AttrFeatureMap)
	if !ok {
		t.Fatal("FeatureMap not readable")
	}
	got := v.(uint32)
	want := uint32(windowcoveringspec.FeatureLift | windowcoveringspec.FeatureTilt |
		windowcoveringspec.FeaturePositionAwareLift | windowcoveringspec.FeaturePositionAwareTilt)
	if got != want {
		t.Fatalf("Blind FeatureMap = 0x%08X, want 0x%08X (LF | TL | PA_LF | PA_TL)", got, want)
	}
	if got != fabriccover.ServedLiftTiltFeatureMap {
		t.Fatalf("Blind FeatureMap = 0x%08X, want go-fabric's ServedLiftTiltFeatureMap 0x%08X", got, fabriccover.ServedLiftTiltFeatureMap)
	}
}

// TestPositionUnobservedReportsOpen: the server carries no null
// position — a cover whose CCU position is not yet observed starts
// fully open (0), as matter.js's WindowCoveringServer initialises the
// attributes from their defaults, and the first confirmed LEVEL
// replaces it.
func TestPositionUnobservedReportsOpen(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := windowCoveringServer(t, c)
	if got := readU16(t, srv, windowcoveringspec.AttrCurrentPositionLiftPercent100ths); got != 0 {
		t.Fatalf("unobserved CurrentPositionLift = %d, want 0 (open)", got)
	}
	c.OnLevel(0.25)
	if got := readU16(t, srv, windowcoveringspec.AttrCurrentPositionLiftPercent100ths); got != 7500 {
		t.Fatalf("CurrentPositionLift after the first confirmed LEVEL 0.25 = %d, want 7500", got)
	}
}

// TestUnknownCommandRejected ensures a command id the cluster does not
// carry is refused by the server rather than reaching the cover.
func TestUnknownCommandRejected(t *testing.T) {
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	srv := windowCoveringServer(t, c)
	if _, err := srv.MatterInvoke(context.Background(), 0x06, nil); err == nil {
		t.Fatal("unknown command 0x06 was accepted")
	}
	if w.last != nil {
		t.Fatalf("unknown command reached the wire: %v", w.last)
	}
}

// TestGoToLiftWrongType: a GoToLiftPercentage whose fields are not a
// percentage is refused before the cover is asked.
func TestGoToLiftWrongType(t *testing.T) {
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	srv := windowCoveringServer(t, c)
	if _, err := srv.MatterInvoke(context.Background(), windowcoveringspec.CmdGoToLiftPercentage, "5000"); err == nil {
		t.Fatal("GoToLiftPercentage with a string field was accepted")
	}
	if w.last != nil {
		t.Fatalf("refused command reached the wire: %v", w.last)
	}
}

// TestReportableAttributes locks the per-type reportable surface so
// regressions in cluster-side report-on-change wiring are visible. The
// Target attributes must be part of every set: controllers that derive
// the motion arrow from target-vs-current (Apple Home) need the
// inferred-target change delivered proactively on movement transitions,
// not only on the next full read. For Cover and Blind the list is
// go-fabric's — every served attribute without the Fixed quality — so
// the test asserts the carriers are in it rather than its exact shape.
func TestReportableAttributes(t *testing.T) {
	requireContains := func(t *testing.T, kind string, got, want []uint32) {
		t.Helper()
		for _, attr := range want {
			if !slices.Contains(got, attr) {
				t.Errorf("%s reportable=%v missing 0x%04X", kind, got, attr)
			}
		}
	}
	requireAttrs := func(t *testing.T, kind string, got, want []uint32) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s reportable=%v, want %v", kind, got, want)
			return
		}
		requireContains(t, kind, got, want)
	}

	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	requireContains(t, "Cover", c.MatterClusterServers()[0].MatterReportable(), []uint32{
		windowcoveringspec.AttrOperationalStatus,
		windowcoveringspec.AttrTargetPositionLiftPercent100ths,
		windowcoveringspec.AttrCurrentPositionLiftPercent100ths,
	})
	if got := c.MatterClusterServers()[0].MatterReportable(); slices.Contains(got, windowcoveringspec.AttrType) ||
		slices.Contains(got, windowcoveringspec.AttrEndProductType) {
		t.Errorf("Cover reportable=%v carries a Fixed attribute (Type / EndProductType)", got)
	}
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	requireContains(t, "Blind", b.MatterClusterServers()[0].MatterReportable(), []uint32{
		windowcoveringspec.AttrOperationalStatus,
		windowcoveringspec.AttrTargetPositionLiftPercent100ths,
		windowcoveringspec.AttrTargetPositionTiltPercent100ths,
		windowcoveringspec.AttrCurrentPositionLiftPercent100ths,
		windowcoveringspec.AttrCurrentPositionTiltPercent100ths,
	})
	// The garage reports the ClosureControl carriers instead: what the
	// drive is doing, which errors it reports, where it is, and where it
	// is heading. The list is go-fabric closure.ControlServer's: every
	// served attribute without the Fixed quality (cluster/spec
	// Instance.MatterReportable). CurrentErrorList carries no Fixed
	// quality in matter.js (closure-control.element.ts:42, "R V", M), so
	// it is reported like the others.
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	requireAttrs(t, "Garage", g.MatterClusterServers()[0].MatterReportable(), []uint32{
		clusterwire.ClosureControlAttrMainState,
		clusterwire.ClosureControlAttrCurrentErrorList,
		clusterwire.ClosureControlAttrOverallCurrentState,
		clusterwire.ClosureControlAttrOverallTargetState,
	})
}

// TestGoToLiftPercentageAboveMaxIsRefused drives an out-of-range
// Percent100ths through the production invoke path: the server refuses
// it with CONSTRAINT_ERROR (window-covering-cluster.element.ts:72
// constraint "max 10000"), the stored target keeps its value and
// nothing reaches the wire.
//
// The wire shape is the tag-keyed map the bridge's generic field decoder
// produces, with the unsigned integer widened to uint64 — the shape a
// GoToLiftPercentage lands in when no typed decoder is linked.
func TestGoToLiftPercentageAboveMaxIsRefused(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	c.OnLevel(0.4)
	srv := windowCoveringServer(t, c)

	_, err := srv.MatterInvoke(context.Background(), windowcoveringspec.CmdGoToLiftPercentage, map[uint8]any{0: uint64(20000)})
	var sc im.StatusCodeError
	if !errors.As(err, &sc) || sc.MatterStatusCode() != im.StatusConstraintError {
		t.Fatalf("GoToLiftPercentage(20000) err = %v, want CONSTRAINT_ERROR", err)
	}
	if got := readU16(t, srv, windowcoveringspec.AttrTargetPositionLiftPercent100ths); got != 6000 {
		t.Fatalf("TargetPositionLift after the refused command = %d, want 6000 (unchanged)", got)
	}
	if w.last != nil {
		t.Fatalf("refused command reached the wire: %v", w.last)
	}
}

// TestCoverServesOneWindowCoveringServerAcrossReassemblies: the
// assembler re-reads the servers on every reassembly and the bridge keeps
// the ones it already serves, so a cover must hand out the same server
// each time — a rebuilt one would lose its target and the subscription
// state behind it. The Blind shares the server with its embedded Cover.
func TestCoverServesOneWindowCoveringServerAcrossReassemblies(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	coverFirst := windowCoveringServer(t, c)
	if coverFirst != windowCoveringServer(t, c) {
		t.Fatal("Cover built a second WindowCovering server on the second MatterClusterServers call")
	}
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	first := windowCoveringServer(t, b)
	if first != windowCoveringServer(t, b) {
		t.Fatal("Blind built a second WindowCovering server on the second MatterClusterServers call")
	}
	if first != windowCoveringServer(t, b.Cover) {
		t.Fatal("the embedded Cover hands out a different server than the Blind")
	}
}

// TestMoverFailureAnswersFailureAndRestoresTheTarget: a cover that
// cannot carry out a command (the CCU write fails) makes the server
// answer FAILURE and restore the previous target — the command's own
// target must not stay reported as a destination the cover never
// started for. UpOrOpen reaches the CCU at once (no debounce), so the
// failure is visible to the command itself.
func TestMoverFailureAnswersFailureAndRestoresTheTarget(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", failingWriter{}, custom.CoverCapabilities{})
	c.OnLevel(0.4) // Matter 6000
	srv := windowCoveringServer(t, c)

	_, err := srv.MatterInvoke(context.Background(), windowcoveringspec.CmdUpOrOpen, nil)
	var sc im.StatusCodeError
	if !errors.As(err, &sc) || sc.MatterStatusCode() != im.StatusFailure {
		t.Fatalf("UpOrOpen with a failing writer err = %v, want IM status FAILURE", err)
	}
	if got := readU16(t, srv, windowcoveringspec.AttrTargetPositionLiftPercent100ths); got != 6000 {
		t.Fatalf("TargetPositionLift after the failed UpOrOpen = %d, want 6000 (restored)", got)
	}
}

// TestConfirmedPositionReachesTheServerAndReports: a LEVEL the CCU
// confirms — the cover moved at the wall, by a CCU program, or the echo
// of a command — is stored on the server through its inbound setter and
// reported through its change notifier, which is how a subscribed
// controller learns about it. The Blind's LEVEL_2 reaches the tilt
// position the same way (the tilt axis has its own hook, so a tilt-only
// change is reported too).
func TestConfirmedPositionReachesTheServerAndReports(t *testing.T) {
	t.Parallel()

	t.Run("CoverLift", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
		srv := windowCoveringServer(t, c)
		reported := make(chan []uint32, 8)
		defer srv.OnMatterAttributesChanged(func(ids []uint32) { reported <- ids })()

		c.OnLevel(0.3) // Matter 7000
		if got := readU16(t, srv, windowcoveringspec.AttrCurrentPositionLiftPercent100ths); got != 7000 {
			t.Fatalf("CurrentPositionLift after the confirmed LEVEL = %d, want 7000", got)
		}
		awaitReported(t, reported, windowcoveringspec.AttrCurrentPositionLiftPercent100ths)
	})

	t.Run("BlindTilt", func(t *testing.T) {
		t.Parallel()
		b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
		srv := windowCoveringServer(t, b)
		reported := make(chan []uint32, 8)
		defer srv.OnMatterAttributesChanged(func(ids []uint32) { reported <- ids })()

		b.level2.OnEvent(0.4) // Matter 6000
		if got := readU16(t, srv, windowcoveringspec.AttrCurrentPositionTiltPercent100ths); got != 6000 {
			t.Fatalf("CurrentPositionTilt after the confirmed LEVEL_2 = %d, want 6000", got)
		}
		awaitReported(t, reported, windowcoveringspec.AttrCurrentPositionTiltPercent100ths)
	})

	t.Run("Motion", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
		srv := windowCoveringServer(t, c)
		reported := make(chan []uint32, 8)
		defer srv.OnMatterAttributesChanged(func(ids []uint32) { reported <- ids })()

		c.OnDirection(DirectionUp)
		awaitReported(t, reported, windowcoveringspec.AttrOperationalStatus)
	})
}

// awaitReported waits for a change notification that names attrID.
func awaitReported(t *testing.T, reported <-chan []uint32, attrID uint32) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ids := <-reported:
			if slices.Contains(ids, attrID) {
				return
			}
		case <-deadline:
			t.Fatalf("attribute 0x%04X was not reported through the change notifier", attrID)
		}
	}
}
