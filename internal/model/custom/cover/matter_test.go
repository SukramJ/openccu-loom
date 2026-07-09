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

// TestMatterDeviceTypeIsWindowCovering covers all three projections —
// the device type ID is uniform; the per-type distinction lives in the
// cluster's Type / EndProductType attributes (Matter spec design).
func TestMatterDeviceTypeIsWindowCovering(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	if c.MatterDeviceType() != 0x0202 {
		t.Fatalf("Cover.MatterDeviceType = 0x%04X, want 0x0202", c.MatterDeviceType())
	}
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	if b.MatterDeviceType() != 0x0202 {
		t.Fatalf("Blind.MatterDeviceType = 0x%04X, want 0x0202", b.MatterDeviceType())
	}
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	if g.MatterDeviceType() != 0x0202 {
		t.Fatalf("Garage.MatterDeviceType = 0x%04X, want 0x0202", g.MatterDeviceType())
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
// HM domain-level 0.25 ("25 % open").
func TestGoToLiftPercentageInversion(t *testing.T) {
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	srv := c.MatterClusterServers()[0]
	if _, err := srv.MatterInvoke(context.Background(), 0x05, uint16(7500), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("GoToLift err: %v", err)
	}
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

// TestGarageStateMapsToDiscretePositions confirms the door-state →
// percent mapping: Open → 0, Vent → 5000, Closed → 10000.
func TestGarageStateMapsToDiscretePositions(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	srv := g.MatterClusterServers()[0]

	cases := []struct {
		state DoorState
		want  uint16
	}{
		{DoorStateOpen, 0},
		{DoorStateVentilation, 5000},
		{DoorStateClosed, 10000},
	}
	for _, tc := range cases {
		g.OnState(tc.state)
		v, ok := srv.MatterRead(0x000E)
		if !ok {
			t.Fatalf("state=%s: position not observed", tc.state)
		}
		if v.(uint16) != tc.want {
			t.Errorf("state=%s → Matter %d, want %d", tc.state, v.(uint16), tc.want)
		}
	}
}

// TestGarageEndProductTypeIsGarageDoor confirms the EndProductType
// attribute carries the GarageDoor (8) discriminator.
func TestGarageEndProductTypeIsGarageDoor(t *testing.T) {
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	srv := g.MatterClusterServers()[0]
	v, ok := srv.MatterRead(0x000D) // EndProductType
	if !ok {
		t.Fatalf("EndProductType not readable")
	}
	if v.(uint8) != matterWCEndProductGarageDoor {
		t.Fatalf("Garage EndProductType=%d, want %d (GarageDoor)", v.(uint8), matterWCEndProductGarageDoor)
	}
}

// TestGarageDownOrCloseDispatchesDoorCommand wires the Matter
// DownOrClose into the Garage's DOOR_COMMAND parameter.
func TestGarageDownOrCloseDispatchesDoorCommand(t *testing.T) {
	w := &stubWriter{}
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", w)
	srv := g.MatterClusterServers()[0]
	if _, err := srv.MatterInvoke(context.Background(), 0x01, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("DownOrClose err: %v", err)
	}
	if w.last != "CLOSE" {
		t.Fatalf("Garage DownOrClose wrote %v, want \"CLOSE\"", w.last)
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
// regressions in cluster-side report-on-change wiring are visible.
func TestReportableAttributes(t *testing.T) {
	c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
	if r := c.MatterClusterServers()[0].MatterReportable(); len(r) != 2 {
		t.Errorf("Cover reportable=%v, want 2 attrs", r)
	}
	b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	if r := b.MatterClusterServers()[0].MatterReportable(); len(r) != 3 {
		t.Errorf("Blind reportable=%v, want 3 attrs (lift, tilt, opstatus)", r)
	}
	g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	if r := g.MatterClusterServers()[0].MatterReportable(); len(r) != 2 {
		t.Errorf("Garage reportable=%v, want 2 attrs", r)
	}
}

// --- OnMatterValueChanged (MatterChangeNotifier) ---
//
// Cover and Blind inherit OnMatterValueChanged from the embedded
// *generic.Float (LEVEL) — that confirmed-only contract is already
// locked by the Float tests in internal/model/generic/matter_test.go.
// Garage carries its own DPs (DOOR_STATE / SECTION) and implements the
// method explicitly, so it gets dedicated coverage here.

// TestGarageOnMatterValueChangedFiresOnConfirmedDoorStateChange verifies
// that a CCU-confirmed DOOR_STATE change (e.g. the door operated at the
// wall button, not through Apple) reaches a registered
// OnMatterValueChanged callback.
func TestGarageOnMatterValueChangedFiresOnConfirmedDoorStateChange(t *testing.T) {
	g, doorStateDP, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
	var count int
	_ = g.OnMatterValueChanged(func() { count++ })
	doorStateDP.OnEvent(string(DoorStateOpen))
	doorStateDP.OnEvent(string(DoorStateClosed))
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
	doorStateDP.OnEvent(string(DoorStateOpen))
	unsub()
	doorStateDP.OnEvent(string(DoorStateClosed))
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
	doorStateDP.OnEvent(string(DoorStateOpen)) // must not panic with no subscriber
}
