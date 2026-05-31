// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestGroups_MatterClusterID_NonZero(t *testing.T) {
	t.Parallel()
	g := wire.Groups{}
	if g.MatterClusterID() == 0 {
		t.Error("MatterClusterID = 0, want non-zero")
	}
}

func TestGroups_MatterRead_NameSupport(t *testing.T) {
	t.Parallel()
	g := wire.Groups{}
	v, ok := g.MatterRead(0x0000) // NameSupport
	if !ok {
		t.Fatal("MatterRead(NameSupport): ok=false")
	}
	ns, isUint8 := v.(uint8)
	if !isUint8 {
		t.Fatalf("NameSupport type = %T, want uint8", v)
	}
	// bit 7 must be set (GroupNames support per matter.js groups.element.ts:31)
	if ns&0x80 == 0 {
		t.Errorf("NameSupport = 0x%02X, want bit 7 set (GroupNames)", ns)
	}
}

func TestGroups_MatterRead_FeatureMap(t *testing.T) {
	t.Parallel()
	g := wire.Groups{}
	v, ok := g.MatterRead(0xFFFC) // FeatureMap
	if !ok {
		t.Fatal("MatterRead(FeatureMap): ok=false")
	}
	fm := v.(uint32)
	if fm == 0 {
		t.Error("FeatureMap = 0, want bit 0 set (GN feature)")
	}
}

func TestGroups_MatterRead_ClusterRevision(t *testing.T) {
	t.Parallel()
	g := wire.Groups{}
	v, ok := g.MatterRead(0xFFFD) // ClusterRevision
	if !ok {
		t.Fatal("MatterRead(ClusterRevision): ok=false")
	}
	rev := v.(uint16)
	if rev == 0 {
		t.Error("ClusterRevision = 0")
	}
}

func TestGroups_MatterRead_Unknown_ReturnsFalse(t *testing.T) {
	t.Parallel()
	g := wire.Groups{}
	_, ok := g.MatterRead(0x9999)
	if ok {
		t.Error("unknown attr: want ok=false")
	}
}

func TestGroups_MatterWrite_ReturnsError(t *testing.T) {
	t.Parallel()
	g := wire.Groups{}
	if err := g.MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite: want non-nil error")
	}
}

func TestGroups_MatterInvoke_ReturnsError(t *testing.T) {
	t.Parallel()
	g := wire.Groups{}
	_, err := g.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke: want non-nil error")
	}
}

func TestGroups_MatterReportable_IsNil(t *testing.T) {
	t.Parallel()
	g := wire.Groups{}
	if r := g.MatterReportable(); r != nil {
		t.Errorf("MatterReportable = %v, want nil (no subscribe-able attrs)", r)
	}
}

func TestGroups_MatterAttributes_ContainsNameSupport(t *testing.T) {
	t.Parallel()
	g := wire.Groups{}
	attrs := g.MatterAttributes()
	found := false
	for _, a := range attrs {
		if a == 0x0000 {
			found = true
		}
	}
	if !found {
		t.Errorf("MatterAttributes = %v, want to contain 0x0000 (NameSupport)", attrs)
	}
}
