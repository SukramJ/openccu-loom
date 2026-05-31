// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package switchdev

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// TestMatterDeviceType locks the OnOffPlugInUnit (0x010A) device-type
// projection. Endpoint-assembler logic depends on this exact value;
// changes here must coordinate with the bridge endpoint topology.
func TestMatterDeviceType(t *testing.T) {
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	if got := s.MatterDeviceType(); got != 0x010A {
		t.Fatalf("MatterDeviceType = 0x%04X, want 0x010A", got)
	}
}

// TestMatterClusterServersExposesOnOff confirms the Switch contributes
// exactly the three mandatory OnOffPlugInUnit baseline clusters: OnOff
// (0x0006), Groups (0x0004), and ScenesManagement (0x0062).
// Adding or reordering clusters here is a breaking change to the endpoint shape.
func TestMatterClusterServersExposesOnOff(t *testing.T) {
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	servers := s.MatterClusterServers()
	if len(servers) != 3 {
		t.Fatalf("expected 3 cluster servers, got %d", len(servers))
	}
	if id := servers[0].MatterClusterID(); id != 0x0006 {
		t.Fatalf("servers[0] MatterClusterID = 0x%04X, want 0x0006 (OnOff)", id)
	}
}

// TestMatterReadOnOffUnobserved confirms an unobserved Switch reads (false, true).
// Apple Home's HAP-mapper drops the OnOffPlugInUnit accessory when OnOff=null on
// the Subscribe-Initial-Report — matter.js's OnOff attribute is TlvBoolean, not
// TlvNullable, so defaulting to false on cold-start matches both spec shape and
// Apple's HMOutlet projection requirements. The first observed CCU state
// overwrites the default.
func TestMatterReadOnOffUnobserved(t *testing.T) {
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	v, ok := s.MatterRead(0x0000)
	if !ok || v != false {
		t.Fatalf("MatterRead(OnOff) on unobserved = (%v, %v), want (false, true)", v, ok)
	}
}

// TestMatterReadOnOffObserved walks the read path after OnState.
func TestMatterReadOnOffObserved(t *testing.T) {
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	s.OnState(true)
	v, ok := s.MatterRead(0x0000)
	if !ok || v != true {
		t.Fatalf("MatterRead(OnOff) = (%v, %v), want (true, true)", v, ok)
	}
}

// TestMatterReadClusterRevisionAndFeatureMap locks the static
// metadata attributes against the Matter 1.5.1 OnOff cluster baseline.
// The OnOffPlugInUnit (0x010A) device type mandates the LT (Lighting)
// feature, so FeatureMap advertises bit 0 (0x01).
// matter.js on-off.element.ts:24 (Field LT, constraint "0").
func TestMatterReadClusterRevisionAndFeatureMap(t *testing.T) {
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	rev, ok := s.MatterRead(0xFFFD)
	if !ok || rev.(uint16) != 6 {
		t.Fatalf("ClusterRevision = (%v, %v), want (6, true)", rev, ok)
	}
	fmap, ok := s.MatterRead(0xFFFC)
	if !ok || fmap.(uint32) != 0x01 {
		t.Fatalf("FeatureMap = (%v, %v), want (0x01 LT, true)", fmap, ok)
	}
}

// TestMatterReadLightingGatedAttributes verifies the four LT-mandatory
// OnOff attributes read with their matter.js-default values.
// GlobalSceneControl=true (OnOffServer.ts:75), OnTime=0, OffWaitTime=0
// (OnOffServer.ts:80,102), StartUpOnOff=null (OnOffServer.ts:39).
func TestMatterReadLightingGatedAttributes(t *testing.T) {
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	if v, ok := s.MatterRead(0x4000); !ok || v != true {
		t.Fatalf("GlobalSceneControl = (%v, %v), want (true, true)", v, ok)
	}
	if v, ok := s.MatterRead(0x4001); !ok || v.(uint16) != 0 {
		t.Fatalf("OnTime = (%v, %v), want (0, true)", v, ok)
	}
	if v, ok := s.MatterRead(0x4002); !ok || v.(uint16) != 0 {
		t.Fatalf("OffWaitTime = (%v, %v), want (0, true)", v, ok)
	}
	if v, ok := s.MatterRead(0x4003); !ok || v != nil {
		t.Fatalf("StartUpOnOff = (%v, %v), want (nil/null, true)", v, ok)
	}
}

// TestMatterAttributesEnumeratesLightingGated confirms MatterAttributes
// lists the four LT-mandatory attributes alongside OnOff.
func TestMatterAttributesEnumeratesLightingGated(t *testing.T) {
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	got := map[uint32]bool{}
	for _, a := range s.MatterAttributes() {
		got[a] = true
	}
	for _, want := range []uint32{0x0000, 0x4000, 0x4001, 0x4002, 0x4003} {
		if !got[want] {
			t.Errorf("MatterAttributes missing 0x%04X", want)
		}
	}
}

// TestMatterAcceptedCommandsEnumeratesLightingGated confirms the OnOff
// baseline plus the three LT-mandatory commands are enumerated.
func TestMatterAcceptedCommandsEnumeratesLightingGated(t *testing.T) {
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	got := map[uint32]bool{}
	for _, c := range s.MatterAcceptedCommands() {
		got[c] = true
	}
	for _, want := range []uint32{0x00, 0x01, 0x02, 0x40, 0x41, 0x42} {
		if !got[want] {
			t.Errorf("MatterAcceptedCommands missing 0x%02X", want)
		}
	}
}

// TestMatterInvokeLightingGatedCommands verifies the three LT-mandatory
// commands are accepted without error and route to plain On/Off.
func TestMatterInvokeLightingGatedCommands(t *testing.T) {
	cases := []struct {
		cmd  uint32
		want bool // expected southbound STATE value
	}{
		{0x40, false}, // OffWithEffect → Off
		{0x41, true},  // OnWithRecallGlobalScene → On
		{0x42, true},  // OnWithTimedOff → On
	}
	for _, tc := range cases {
		w := &stubWriter{}
		s := newTestSwitch(t, "HmIP-PS:3", "", w)
		if _, err := s.MatterInvoke(context.Background(), tc.cmd, nil, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("MatterInvoke(0x%02X) error: %v", tc.cmd, err)
		}
		if w.lastVal != tc.want {
			t.Fatalf("cmd 0x%02X wrote STATE=%v, want %v", tc.cmd, w.lastVal, tc.want)
		}
	}
}

// TestMatterReadUnknownAttribute returns (nil, false) for IDs the
// projection does not implement.
func TestMatterReadUnknownAttribute(t *testing.T) {
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	// 0x4004 is above the highest LT-gated attribute (StartUpOnOff 0x4003)
	// and is not implemented by the OnOff projection.
	if v, ok := s.MatterRead(0x4004); ok {
		t.Fatalf("MatterRead(unknown) = (%v, true), want (nil, false)", v)
	}
}

// TestMatterWriteOnOffSetsState routes the write through to the
// southbound stubWriter as a STATE setvalue.
func TestMatterWriteOnOffSetsState(t *testing.T) {
	w := &stubWriter{}
	s := newTestSwitch(t, "HmIP-PS:3", "", w)
	if err := s.MatterWrite(context.Background(), 0x0000, true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(OnOff, true) error: %v", err)
	}
	if w.lastParm != hmenum.ParameterState || w.lastVal != true {
		t.Fatalf("setvalue = (%v=%v), want (STATE=true)", w.lastParm, w.lastVal)
	}
}

// TestMatterWriteOnOffWrongType refuses non-bool values; the bridge
// never reaches this path because TLV decoding catches the type
// mismatch first, but defence-in-depth matters.
func TestMatterWriteOnOffWrongType(t *testing.T) {
	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
	err := s.MatterWrite(context.Background(), 0x0000, "true", hmenum.CommandPriorityHigh)
	if !errors.Is(err, errMatterValueType) {
		t.Fatalf("MatterWrite(OnOff, string) err = %v, want errMatterValueType", err)
	}
}

// TestMatterWriteUnknownAttributeRejected rejects writes to
// unsupported attribute IDs. Attribute 0x4004 is not defined on OnOff.
func TestMatterWriteUnknownAttributeRejected(t *testing.T) {
	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
	err := s.MatterWrite(context.Background(), 0x4004, uint16(0), hmenum.CommandPriorityHigh)
	if !errors.Is(err, errMatterUnknownAttribute) {
		t.Fatalf("MatterWrite(unknown) err = %v, want errMatterUnknownAttribute", err)
	}
}

// TestMatterInvokeOff dispatches the Off command and confirms the
// southbound STATE=false write.
func TestMatterInvokeOff(t *testing.T) {
	w := &stubWriter{}
	s := newTestSwitch(t, "HmIP-PS:3", "", w)
	if _, err := s.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(Off) error: %v", err)
	}
	if w.lastParm != hmenum.ParameterState || w.lastVal != false {
		t.Fatalf("setvalue = (%v=%v), want (STATE=false)", w.lastParm, w.lastVal)
	}
}

// TestMatterInvokeOn dispatches the On command.
func TestMatterInvokeOn(t *testing.T) {
	w := &stubWriter{}
	s := newTestSwitch(t, "HmIP-PS:3", "", w)
	if _, err := s.MatterInvoke(context.Background(), 0x01, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(On) error: %v", err)
	}
	if w.lastParm != hmenum.ParameterState || w.lastVal != true {
		t.Fatalf("setvalue = (%v=%v), want (STATE=true)", w.lastParm, w.lastVal)
	}
}

// TestMatterInvokeToggleFromOn flips an observed-on Switch off.
func TestMatterInvokeToggleFromOn(t *testing.T) {
	w := &stubWriter{}
	s := newTestSwitch(t, "HmIP-PS:3", "", w)
	s.OnState(true)
	if _, err := s.MatterInvoke(context.Background(), 0x02, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(Toggle) error: %v", err)
	}
	if w.lastVal != false {
		t.Fatalf("Toggle from On wrote %v, want false", w.lastVal)
	}
}

// TestMatterInvokeToggleUnobserved treats unknown current state as
// off (Matter spec: unobserved → transition to On).
func TestMatterInvokeToggleUnobserved(t *testing.T) {
	w := &stubWriter{}
	s := newTestSwitch(t, "HmIP-PS:3", "", w)
	if _, err := s.MatterInvoke(context.Background(), 0x02, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(Toggle) error: %v", err)
	}
	if w.lastVal != true {
		t.Fatalf("Toggle when unobserved wrote %v, want true", w.lastVal)
	}
}

// TestMatterInvokeUnknownCommand rejects unsupported command IDs. 0x43
// is above the highest defined OnOff command (OnWithTimedOff 0x42).
func TestMatterInvokeUnknownCommand(t *testing.T) {
	s := newTestSwitch(t, "HmIP-PS:3", "", &stubWriter{})
	_, err := s.MatterInvoke(context.Background(), 0x43, nil, hmenum.CommandPriorityHigh)
	if !errors.Is(err, errMatterUnknownCommand) {
		t.Fatalf("MatterInvoke(0x43) err = %v, want errMatterUnknownCommand", err)
	}
}

// TestMatterReportableListsOnOff confirms the OnOff attribute is the
// only reportable attribute on this projection.
func TestMatterReportableListsOnOff(t *testing.T) {
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	r := s.MatterReportable()
	if len(r) != 1 || r[0] != 0x0000 {
		t.Fatalf("MatterReportable = %v, want [0x0000]", r)
	}
}

// TestMatterWriteNilValueIsNoOp verifies that a nil value write to the OnOff
// attribute is silently ignored rather than panicking on a type assertion.
// Matter OnOff has quality "N S" (non-volatile + scene); a scene controller
// may write nil to reset a scene-tagged attribute. The attribute is non-nullable
// (no quality X), so nil carries no spec-defined meaning — no-op is safe.
// Mirrors matter.js packages/model/src/standard/elements/on-off.element.ts:29.
func TestMatterWriteNilValueIsNoOp(t *testing.T) {
	w := &stubWriter{}
	s := newTestSwitch(t, "HmIP-PS:3", "", w)
	err := s.MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterWrite(OnOff, nil) = %v, want nil (no-op)", err)
	}
	if w.lastParm != "" {
		t.Fatalf("nil write reached the wire unexpectedly: lastParm=%v", w.lastParm)
	}
}

// TestMatterClusterServerInterfaceSatisfaction confirms the Switch
// satisfies the source-surface interface contracts at runtime through
// the same path the endpoint assembler will use.
func TestMatterClusterServerInterfaceSatisfaction(t *testing.T) {
	s := newTestSwitch(t, "VCU0000001:1", "", &stubWriter{})
	var src interfaces.MatterEndpointSource = s
	servers := src.MatterClusterServers()
	if len(servers) == 0 {
		t.Fatalf("expected ≥1 cluster server")
	}
	srv := servers[0]
	if srv.MatterClusterID() != 0x0006 {
		t.Fatalf("cluster ID = 0x%04X, want 0x0006", srv.MatterClusterID())
	}
}
