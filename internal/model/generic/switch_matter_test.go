// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// switch_matter.go — all methods
// ---------------------------------------------------------------------------

func TestSwitchMatter_EligibleOnlyForState(t *testing.T) {
	t.Parallel()
	if !matterGenericSwitchEligible(hmenum.ParameterState) {
		t.Error("STATE should be eligible")
	}
	if matterGenericSwitchEligible(hmenum.ParameterOnTime) {
		t.Error("ON_TIME should not be eligible")
	}
}

func TestSwitchMatter_MatterDeviceType(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	if got := s.MatterDeviceType(); got != matterGenericSwitchDeviceType {
		t.Errorf("MatterDeviceType: got 0x%04X, want 0x%04X", got, matterGenericSwitchDeviceType)
	}
}

// TestSwitchMatter_MatterClusterServers_State pins the mandatory cluster
// set of the OnOffPlugInUnit device type this source advertises: OnOff
// plus the Groups and ScenesManagement stubs, both conformance "M" per
// matter.js on-off-plug-in-unit.element.ts.
func TestSwitchMatter_MatterClusterServers_State(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	mounted := map[uint32]bool{}
	for _, srv := range s.MatterClusterServers() {
		mounted[srv.MatterClusterID()] = true
	}
	for _, want := range []uint32{0x0006, 0x0004, 0x0062} {
		if !mounted[want] {
			t.Errorf("cluster 0x%04X not mounted; got %v", want, mounted)
		}
	}
}

func TestSwitchMatter_MatterClusterServers_NonState_ReturnsNil(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterOnTime, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	if servers := s.MatterClusterServers(); servers != nil {
		t.Errorf("MatterClusterServers with ON_TIME: want nil, got %v", servers)
	}
}

func TestSwitchMatter_MatterClusterServers_Nil(t *testing.T) {
	t.Parallel()
	var s *Switch
	if servers := s.MatterClusterServers(); servers != nil {
		t.Error("nil switch: MatterClusterServers must return nil")
	}
}

func TestSwitchMatter_MatterClusterID(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	if got := s.MatterClusterID(); got != matterGenericSwitchClusterOnOff {
		t.Errorf("MatterClusterID: got 0x%04X, want 0x%04X", got, matterGenericSwitchClusterOnOff)
	}
}

// TestSwitchMatter_MatterRead_Unobserved pins that a not-yet-observed
// STATE still answers the mandatory OnOff attribute with a concrete
// boolean.
//
// (nil, false) makes the dispatcher answer StatusUnsupportedAttribute for
// an attribute the device type mandates, and a controller commissioned
// before the first CCU push reads exactly that on its initial wildcard
// read. OnOff is not spec-nullable — matter.js OnOffCluster.ts:35
// declares it TlvBoolean — so the pre-observation answer is OFF, the same
// default matter.js's OnOffServer carries.
func TestSwitchMatter_MatterRead_Unobserved(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	v, ok := s.MatterRead(matterGenericSwitchAttrOnOff)
	if !ok || v != false {
		t.Errorf("unobserved: want (false, true), got (%v, %v)", v, ok)
	}
}

func TestSwitchMatter_MatterRead_Observed(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	s.OnEvent(true)
	v, ok := s.MatterRead(matterGenericSwitchAttrOnOff)
	if !ok || v != true {
		t.Errorf("observed true: want (true, true), got (%v, %v)", v, ok)
	}
}

// TestSwitchMatter_MatterRead_FeatureMap pins the LT (Lighting) bit: the
// advertised OnOffPlugInUnit device type requires the feature with
// conformance "M" (matter.js on-off-plug-in-unit.element.ts:26), and a
// controller reads FeatureMap before it trusts the LT-gated surface.
func TestSwitchMatter_MatterRead_FeatureMap(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	v, ok := s.MatterRead(matterGenericSwitchAttrFeatureMap)
	if !ok || v != matterGenericFeatureOnOffLT {
		t.Errorf("FeatureMap: want (0x%02X, true), got (%v, %v)", matterGenericFeatureOnOffLT, v, ok)
	}
}

// TestSwitchMatter_LTAttributesAnswer asserts every attribute the LT
// feature makes mandatory is readable, so a controller that trusts the
// FeatureMap bit never gets UnsupportedAttribute.
// matter.js on-off.element.ts:30-36.
func TestSwitchMatter_LTAttributesAnswer(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))

	if v, ok := s.MatterRead(matterGenericSwitchAttrGlobalSceneControl); !ok || v != true {
		t.Errorf("GlobalSceneControl: want (true, true), got (%v, %v)", v, ok)
	}
	if v, ok := s.MatterRead(matterGenericSwitchAttrOnTime); !ok || v != uint16(0) {
		t.Errorf("OnTime: want (uint16(0), true), got (%v, %v)", v, ok)
	}
	if v, ok := s.MatterRead(matterGenericSwitchAttrOffWaitTime); !ok || v != uint16(0) {
		t.Errorf("OffWaitTime: want (uint16(0), true), got (%v, %v)", v, ok)
	}
	// StartUpOnOff defaults to TLV null ("keep the last state").
	if v, ok := s.MatterRead(matterGenericSwitchAttrStartUpOnOff); !ok || v != nil {
		t.Errorf("StartUpOnOff: want (nil, true), got (%v, %v)", v, ok)
	}

	ctx := t.Context()
	if err := s.MatterWrite(ctx, matterGenericSwitchAttrOnTime, uint16(42), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("write OnTime: %v", err)
	}
	if v, _ := s.MatterRead(matterGenericSwitchAttrOnTime); v != uint16(42) {
		t.Errorf("OnTime after write: got %v, want 42", v)
	}
	if err := s.MatterWrite(ctx, matterGenericSwitchAttrStartUpOnOff, uint8(1), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("write StartUpOnOff: %v", err)
	}
	if v, _ := s.MatterRead(matterGenericSwitchAttrStartUpOnOff); v != uint8(1) {
		t.Errorf("StartUpOnOff after write: got %v, want 1", v)
	}
}

// TestSwitchMatter_LTCommandsAreAccepted asserts the three commands the
// LT feature makes mandatory are dispatched rather than rejected with
// UnsupportedCommand. matter.js on-off.element.ts:41,46,51.
func TestSwitchMatter_LTCommandsAreAccepted(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	s.Writer = &stubWriter{}
	ctx := t.Context()

	if _, err := s.MatterInvoke(ctx, matterGenericSwitchCmdOffWithEffect, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("OffWithEffect: %v", err)
	}
	if v, _ := s.MatterRead(matterGenericSwitchAttrGlobalSceneControl); v != false {
		t.Errorf("OffWithEffect must clear GlobalSceneControl, got %v", v)
	}
	if _, err := s.MatterInvoke(ctx, matterGenericSwitchCmdOnWithRecallGlobalScene, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("OnWithRecallGlobalScene: %v", err)
	}
	if v, _ := s.MatterRead(matterGenericSwitchAttrGlobalSceneControl); v != true {
		t.Errorf("OnWithRecallGlobalScene must set GlobalSceneControl, got %v", v)
	}
	if _, err := s.MatterInvoke(ctx, matterGenericSwitchCmdOnWithTimedOff, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("OnWithTimedOff: %v", err)
	}
}

func TestSwitchMatter_MatterRead_ClusterRevision(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	v, ok := s.MatterRead(matterGenericSwitchAttrClusterRevision)
	if !ok || v != matterGenericOnOffClusterRevision {
		t.Errorf("ClusterRevision: want (%v, true), got (%v, %v)", matterGenericOnOffClusterRevision, v, ok)
	}
}

func TestSwitchMatter_MatterRead_UnknownAttr(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	v, ok := s.MatterRead(0xBEEF)
	if ok || v != nil {
		t.Errorf("unknown attr: want (nil, false), got (%v, %v)", v, ok)
	}
}

func TestSwitchMatter_MatterWrite_WrongAttr(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	w := &stubWriter{}
	s.Writer = w
	if err := s.MatterWrite(context.Background(), 0xBEEF, true, hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("wrong attr ID: expected error")
	}
}

func TestSwitchMatter_MatterWrite_WrongType(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	w := &stubWriter{}
	s.Writer = w
	if err := s.MatterWrite(context.Background(), matterGenericSwitchAttrOnOff, "not-bool", hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("non-bool value: expected error")
	}
}

func TestSwitchMatter_MatterWrite_HappyPath(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	w := &stubWriter{}
	s.Writer = w
	if err := s.MatterWrite(context.Background(), matterGenericSwitchAttrOnOff, false, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite false: %v", err)
	}
}

func TestSwitchMatter_MatterInvoke_Off(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	w := &stubWriter{}
	s.Writer = w
	if _, err := s.MatterInvoke(context.Background(), matterGenericSwitchCmdOff, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Invoke Off: %v", err)
	}
}

func TestSwitchMatter_MatterInvoke_On(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	w := &stubWriter{}
	s.Writer = w
	if _, err := s.MatterInvoke(context.Background(), matterGenericSwitchCmdOn, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Invoke On: %v", err)
	}
}

func TestSwitchMatter_MatterInvoke_Toggle_Unobserved(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	w := &stubWriter{}
	s.Writer = w
	// Unobserved → toggle turns on.
	if _, err := s.MatterInvoke(context.Background(), matterGenericSwitchCmdToggle, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Invoke Toggle (unobserved): %v", err)
	}
}

func TestSwitchMatter_MatterInvoke_Toggle_OnToOff(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	w := &stubWriter{}
	s.Writer = w
	s.OnEvent(true)
	if _, err := s.MatterInvoke(context.Background(), matterGenericSwitchCmdToggle, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Invoke Toggle (on→off): %v", err)
	}
}

func TestSwitchMatter_MatterInvoke_UnknownCmd(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	w := &stubWriter{}
	s.Writer = w
	if _, err := s.MatterInvoke(context.Background(), 0xFF, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("unknown command: expected error")
	}
}

func TestSwitchMatter_MatterReportable(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	rep := s.MatterReportable()
	if len(rep) != 1 || rep[0] != matterGenericSwitchAttrOnOff {
		t.Errorf("MatterReportable: got %v, want [%d]", rep, matterGenericSwitchAttrOnOff)
	}
}

// TestSwitchMatter_MatterAttributes asserts a wildcard read expands to
// OnOff plus the four attributes the LT feature makes mandatory — a
// controller that discovers the cluster this way must find the same set
// the FeatureMap promises.
func TestSwitchMatter_MatterAttributes(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	listed := map[uint32]bool{}
	for _, a := range s.MatterAttributes() {
		listed[a] = true
	}
	for _, want := range []uint32{
		matterGenericSwitchAttrOnOff,
		matterGenericSwitchAttrGlobalSceneControl,
		matterGenericSwitchAttrOnTime,
		matterGenericSwitchAttrOffWaitTime,
		matterGenericSwitchAttrStartUpOnOff,
	} {
		if !listed[want] {
			t.Errorf("attribute 0x%04X not listed; got %v", want, s.MatterAttributes())
		}
	}
}

// TestSwitchMatter_LTWritesAcceptDecodedUint64 pins the type the IM
// layer actually delivers: the bridge decodes every unsigned TLV integer
// to uint64, so a uint16/uint8 assertion rejected every write a real
// controller sent for the three LT attributes the FeatureMap advertises
// as writable.
func TestSwitchMatter_LTWritesAcceptDecodedUint64(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cases := []struct {
		name  string
		attr  uint32
		write any
		want  any
	}{
		{"OnTime", matterGenericSwitchAttrOnTime, uint64(42), uint16(42)},
		{"OffWaitTime", matterGenericSwitchAttrOffWaitTime, uint64(7), uint16(7)},
		{"StartUpOnOff", matterGenericSwitchAttrStartUpOnOff, uint64(1), uint8(1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
				hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
			if err := s.MatterWrite(ctx, tc.attr, tc.write, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("write %s: %v", tc.name, err)
			}
			if v, _ := s.MatterRead(tc.attr); v != tc.want {
				t.Errorf("%s after write: got %v (%T), want %v", tc.name, v, v, tc.want)
			}
		})
	}
}

// TestSwitchMatter_AcceptedCommandsMatchInvoke pins that every command
// MatterInvoke handles is advertised in AcceptedCommandList. Without the
// lister the dispatcher answers an empty list, so a controller that
// derives write capability from it renders a plug it believes it cannot
// command.
func TestSwitchMatter_AcceptedCommandsMatchInvoke(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	s.Writer = &stubWriter{}

	want := []uint32{
		matterGenericSwitchCmdOff,
		matterGenericSwitchCmdOn,
		matterGenericSwitchCmdToggle,
		matterGenericSwitchCmdOffWithEffect,
		matterGenericSwitchCmdOnWithRecallGlobalScene,
		matterGenericSwitchCmdOnWithTimedOff,
	}
	got := s.MatterAcceptedCommands()
	if len(got) != len(want) {
		t.Fatalf("MatterAcceptedCommands = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("MatterAcceptedCommands = %v, want %v", got, want)
		}
	}
	// Each advertised command must actually dispatch.
	for _, cmd := range got {
		if _, err := s.MatterInvoke(t.Context(), cmd, nil, hmenum.CommandPriorityHigh); err != nil {
			t.Errorf("advertised command 0x%02X is rejected: %v", cmd, err)
		}
	}
	if s.MatterGeneratedCommands() != nil {
		t.Error("OnOff commands carry no response payload")
	}
}
