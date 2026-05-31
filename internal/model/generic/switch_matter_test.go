// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

func TestSwitchMatter_MatterClusterServers_State(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	servers := s.MatterClusterServers()
	if len(servers) != 1 {
		t.Fatalf("MatterClusterServers with STATE: want 1, got %d", len(servers))
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

func TestSwitchMatter_MatterRead_Unobserved(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	v, ok := s.MatterRead(matterGenericSwitchAttrOnOff)
	if ok || v != nil {
		t.Errorf("unobserved: want (nil, false), got (%v, %v)", v, ok)
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

func TestSwitchMatter_MatterRead_FeatureMap(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	v, ok := s.MatterRead(matterGenericSwitchAttrFeatureMap)
	if !ok || v != uint32(0) {
		t.Errorf("FeatureMap: want (uint32(0), true), got (%v, %v)", v, ok)
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

func TestSwitchMatter_MatterAttributes(t *testing.T) {
	t.Parallel()
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	attrs := s.MatterAttributes()
	if len(attrs) != 1 || attrs[0] != matterGenericSwitchAttrOnOff {
		t.Errorf("MatterAttributes: got %v, want [%d]", attrs, matterGenericSwitchAttrOnOff)
	}
}
