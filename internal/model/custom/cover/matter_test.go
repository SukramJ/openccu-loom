// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	mattercluster "github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	clusterwire "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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

// TestCoverFeatureMapLiftOnly confirms a plain Cover advertises
// LF | PA_LF | ABS but not TL.
func TestCoverFeatureMapLiftOnly(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	v, ok := srv.MatterRead(0xFFFC)
	if !ok {
		t.Fatalf("FeatureMap not readable")
	}
	got := v.(uint32)
	wantSet := matterWCFeatureLift | matterWCFeaturePositionAwLft
	if got&wantSet != wantSet {
		t.Fatalf("FeatureMap=0x%08X missing required bits 0x%08X", got, wantSet)
	}
	if got&matterWCFeatureTilt != 0 {
		t.Fatalf("Cover FeatureMap=0x%08X includes Tilt bit; expected lift-only", got)
	}
	// Bit 3 is not a WindowCovering feature in matter.js HEAD.
	if got&(1<<3) != 0 {
		t.Fatalf("Cover FeatureMap=0x%08X advertises undefined bit 3", got)
	}
}

// TestBlindFeatureMapIncludesTilt confirms a Blind advertises both
// LF and TL plus the position-aware feature bits.
func TestBlindFeatureMapIncludesTilt(t *testing.T) {
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	srv := b.MatterClusterServers()[0]
	v, ok := srv.MatterRead(0xFFFC)
	if !ok {
		t.Fatalf("FeatureMap not readable")
	}
	got := v.(uint32)
	wantSet := matterWCFeatureLift | matterWCFeatureTilt |
		matterWCFeaturePositionAwLft | matterWCFeaturePositionAwTlt
	if got&wantSet != wantSet {
		t.Fatalf("Blind FeatureMap=0x%08X missing required bits 0x%08X", got, wantSet)
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

// TestPositionUnobservedReturnsStale ensures the stale-data path surfaces
// as (nil, true) — attribute is supported but value is transiently null.
// (nil, false) would signal UnsupportedAttribute to the HAP mapper.
func TestPositionUnobservedReturnsStale(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	v, ok := srv.MatterRead(0x000E)
	if !ok || v != nil {
		t.Fatalf("unobserved position read = (%v, %v), want (nil, true)", v, ok)
	}
}

// TestUpOrOpenCommand routes through Cover.Open.
func TestUpOrOpenCommand(t *testing.T) {
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	if _, err := srv.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh); err != nil {
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
	if _, err := srv.MatterInvoke(context.Background(), 0x01, nil, hmenum.CommandPriorityHigh); err != nil {
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
	if _, err := srv.MatterInvoke(context.Background(), 0x02, nil, hmenum.CommandPriorityHigh); err != nil {
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
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	if _, err := srv.MatterInvoke(context.Background(), 0x05, uint16(7500), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToLift err: %v", err)
	}
	flushGoToWrites(&c.matterGoTo)
	if w.last.(float64) != 0.25 {
		t.Fatalf("Matter 7500 → HM %v, want 0.25", w.last)
	}
}

// TestBlindGoToTiltPercentage exercises the lift+tilt projection's tilt write
// path. HM blinds write LEVEL_COMBINED as a comma-separated hex string
// "0xLL,0xTT" where each byte = int(position * 100 * 2).
func TestBlindGoToTiltPercentage(t *testing.T) {
	w := &putWriter{}
	b := newBlindRig(t, "VCU3560967:1", w, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	srv := b.MatterClusterServers()[0]
	if _, err := srv.MatterInvoke(context.Background(), 0x08, uint16(2500), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToTilt err: %v", err)
	}
	flushGoToWrites(&b.matterGoTo)
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
				clusterwire.MoveToRequest{Position: &target}, hmenum.CommandPriorityHigh); err != nil {
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

// TestUnknownCommandRejected ensures unknown command IDs surface
// errMatterUnknownCommand for the bridge to translate into
// UNSUPPORTED_COMMAND.
func TestUnknownCommandRejected(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	_, err := srv.MatterInvoke(context.Background(), 0x06, nil, hmenum.CommandPriorityHigh)
	if !errors.Is(err, errMatterUnknownCommand) {
		t.Fatalf("err=%v, want errMatterUnknownCommand", err)
	}
}

// TestGoToLiftWrongType rejects non-uint16 fields with errMatterValueType.
func TestGoToLiftWrongType(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	_, err := srv.MatterInvoke(context.Background(), 0x05, "5000", hmenum.CommandPriorityHigh)
	if !errors.Is(err, errMatterValueType) {
		t.Fatalf("err=%v, want errMatterValueType", err)
	}
}

// TestReportableAttributes locks the per-type reportable surface so
// regressions in cluster-side report-on-change wiring are visible. The
// Target attributes must be part of every set: controllers that derive
// the motion arrow from target-vs-current (Apple Home) need the
// inferred-target change delivered proactively on movement transitions,
// not only on the next full read.
func TestReportableAttributes(t *testing.T) {
	requireAttrs := func(t *testing.T, kind string, got, want []uint32) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s reportable=%v, want %v", kind, got, want)
			return
		}
		for _, attr := range want {
			if !slices.Contains(got, attr) {
				t.Errorf("%s reportable=%v missing 0x%04X", kind, got, attr)
			}
		}
	}

	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	requireAttrs(t, "Cover", c.MatterClusterServers()[0].MatterReportable(), []uint32{
		matterAttrOperationalStatus,
		matterAttrTargetPositionLiftPercent100ths,
		matterAttrCurrentPositionLiftPercent100ths,
	})
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	requireAttrs(t, "Blind", b.MatterClusterServers()[0].MatterReportable(), []uint32{
		matterAttrOperationalStatus,
		matterAttrTargetPositionLiftPercent100ths,
		matterAttrTargetPositionTiltPercent100ths,
		matterAttrCurrentPositionLiftPercent100ths,
		matterAttrCurrentPositionTiltPercent100ths,
	})
	// The garage reports the ClosureControl carriers instead: what the
	// drive is doing, where it is, and where it is heading.
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	requireAttrs(t, "Garage", g.MatterClusterServers()[0].MatterReportable(), []uint32{
		clusterwire.ClosureControlAttrMainState,
		clusterwire.ClosureControlAttrOverallCurrentState,
		clusterwire.ClosureControlAttrOverallTargetState,
	})
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

// TestGoToLiftPercentageClampsToPercent100thsMax drives an out-of-range
// Percent100ths through the production invoke path and pins that what
// reaches the stored Matter target is the saturated value, not the raw
// one.
//
// The wire shape is the tag-keyed map the bridge's generic field decoder
// produces, with the unsigned integer widened to uint64 — the shape a
// real GoToLiftPercentage lands in, since the command has no typed
// decoder. Reading TargetPositionLiftPercent100ths back is the only
// externally visible consequence of the clamp: the HM conversion
// saturates independently, so a missing clamp shows up here and nowhere
// else.
func TestGoToLiftPercentageClampsToPercent100thsMax(t *testing.T) {
	t.Parallel()
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]

	if _, err := srv.MatterInvoke(
		context.Background(),
		matterCmdGoToLiftPercentage,
		map[uint8]any{0: uint64(20000)},
		hmenum.CommandPriorityHigh,
	); err != nil {
		t.Fatalf("GoToLiftPercentage(20000): %v", err)
	}

	target, ok := srv.MatterRead(matterAttrTargetPositionLiftPercent100ths)
	if !ok {
		t.Fatal("TargetPositionLift not readable after GoToLiftPercentage")
	}
	if got := target.(uint16); got != matterCoverPctMax {
		t.Fatalf("TargetPositionLift after GoToLiftPercentage(20000) = %d, want %d", got, matterCoverPctMax)
	}
}
