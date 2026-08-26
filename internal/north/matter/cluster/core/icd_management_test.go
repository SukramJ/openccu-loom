// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestICD_ClusterID(t *testing.T) {
	t.Parallel()
	icd := core.NewICDManagement()
	if got := icd.MatterClusterID(); got != 0x0046 {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x0046", got)
	}
}

func TestICD_ReadAllMandatoryAttributes(t *testing.T) {
	t.Parallel()
	icd := core.NewICDManagement()
	cases := []struct {
		attrID uint32
		name   string
	}{
		{0x0000, "IdleModeDuration"},
		{0x0001, "ActiveModeDuration"},
		{0x0002, "ActiveModeThreshold"},
		{cluster.AttrGlobalFeatureMap, "FeatureMap"},
		{cluster.AttrGlobalClusterRevision, "ClusterRevision"},
	}
	for _, tc := range cases {
		v, ok := icd.MatterRead(tc.attrID)
		if !ok {
			t.Errorf("MatterRead(0x%04X %s) = (_, false), want true", tc.attrID, tc.name)
		}
		_ = v
	}
}

func TestICD_ReadIdleModeDuration(t *testing.T) {
	t.Parallel()
	icd := core.NewICDManagement()
	v, ok := icd.MatterRead(0x0000)
	if !ok {
		t.Fatal("IdleModeDuration: ok=false")
	}
	if v.(uint32) != 1 {
		t.Fatalf("IdleModeDuration = %v, want 1", v)
	}
}

func TestICD_ReadActiveModeDuration(t *testing.T) {
	t.Parallel()
	icd := core.NewICDManagement()
	v, ok := icd.MatterRead(0x0001)
	if !ok {
		t.Fatal("ActiveModeDuration: ok=false")
	}
	// Mirrors matter.js icd-management.element.ts default 300 ms.
	if v.(uint32) != 300 {
		t.Fatalf("ActiveModeDuration = %v, want 300", v)
	}
}

func TestICD_ReadActiveModeThreshold(t *testing.T) {
	t.Parallel()
	icd := core.NewICDManagement()
	v, ok := icd.MatterRead(0x0002)
	if !ok {
		t.Fatal("ActiveModeThreshold: ok=false")
	}
	// Mirrors icd-management.element.ts default 300.
	if v.(uint16) != 300 {
		t.Fatalf("ActiveModeThreshold = %v, want 300", v)
	}
}

func TestICD_ReadFeatureMapZero(t *testing.T) {
	t.Parallel()
	icd := core.NewICDManagement()
	v, ok := icd.MatterRead(cluster.AttrGlobalFeatureMap)
	if !ok {
		t.Fatal("FeatureMap: ok=false")
	}
	if v.(uint32) != 0 {
		t.Fatalf("FeatureMap = %v, want 0 (no LITS/CIP/UAT)", v)
	}
}

func TestICD_ReadUnknownAttrReturnsFalse(t *testing.T) {
	t.Parallel()
	icd := core.NewICDManagement()
	if _, ok := icd.MatterRead(0xBEEF); ok {
		t.Fatal("MatterRead(0xBEEF) = true, want false")
	}
}

func TestICD_WriteReturnsError(t *testing.T) {
	t.Parallel()
	icd := core.NewICDManagement()
	for _, attrID := range []uint32{0x0000, 0x0001, 0x0002} {
		err := icd.MatterWrite(context.Background(), attrID, nil, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("MatterWrite(0x%04X) expected error, got nil", attrID)
		}
	}
}

func TestICD_InvokeReturnsError(t *testing.T) {
	t.Parallel()
	icd := core.NewICDManagement()
	for _, cmdID := range []uint32{0x00, 0x01, 0x02, 0xFF} {
		_, err := icd.MatterInvoke(context.Background(), cmdID, nil, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("MatterInvoke(0x%02X) expected error, got nil", cmdID)
		}
	}
}

func TestICD_MatterReportableContainsAllThreeAttrs(t *testing.T) {
	t.Parallel()
	icd := core.NewICDManagement()
	list := icd.MatterReportable()
	have := make(map[uint32]bool, len(list))
	for _, a := range list {
		have[a] = true
	}
	for _, want := range []uint32{0x0000, 0x0001, 0x0002} {
		if !have[want] {
			t.Errorf("MatterReportable() missing attr 0x%04X", want)
		}
	}
}

func TestICD_MatterAttributesContainsAllThreeAttrs(t *testing.T) {
	t.Parallel()
	icd := core.NewICDManagement()
	list := icd.MatterAttributes()
	have := make(map[uint32]bool, len(list))
	for _, a := range list {
		have[a] = true
	}
	for _, want := range []uint32{0x0000, 0x0001, 0x0002} {
		if !have[want] {
			t.Errorf("MatterAttributes() missing attr 0x%04X", want)
		}
	}
}
