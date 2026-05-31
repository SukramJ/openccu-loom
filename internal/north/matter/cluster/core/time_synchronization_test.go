// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestTimeSync_ClusterID(t *testing.T) {
	t.Parallel()
	ts := core.NewTimeSynchronization()
	if got := ts.MatterClusterID(); got != 0x0038 {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x0038", got)
	}
}

func TestTimeSync_ReadUTCTimePositive(t *testing.T) {
	t.Parallel()
	ts := core.NewTimeSynchronization()
	v, ok := ts.MatterRead(0x0000)
	if !ok {
		t.Fatal("UTCTime: ok=false")
	}
	// Matter epoch is 2000-01-01; any valid post-2000 host clock gives a
	// positive value.
	if v.(uint64) == 0 {
		t.Fatal("UTCTime = 0, want a post-Matter-epoch value")
	}
}

func TestTimeSync_ReadGranularity(t *testing.T) {
	t.Parallel()
	ts := core.NewTimeSynchronization()
	v, ok := ts.MatterRead(0x0001)
	if !ok {
		t.Fatal("Granularity: ok=false")
	}
	// Expect GranularityMillisecGran (3).
	if v.(uint8) != core.GranularityMillisecGran {
		t.Fatalf("Granularity = %v, want GranularityMillisecGran (%d)", v, core.GranularityMillisecGran)
	}
}

func TestTimeSync_ReadFeatureMapZero(t *testing.T) {
	t.Parallel()
	ts := core.NewTimeSynchronization()
	v, ok := ts.MatterRead(cluster.AttrGlobalFeatureMap)
	if !ok {
		t.Fatal("FeatureMap: ok=false")
	}
	if v.(uint32) != 0 {
		t.Fatalf("FeatureMap = %v, want 0", v)
	}
}

func TestTimeSync_ReadClusterRevision(t *testing.T) {
	t.Parallel()
	ts := core.NewTimeSynchronization()
	v, ok := ts.MatterRead(cluster.AttrGlobalClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if v.(uint16) != 2 {
		t.Fatalf("ClusterRevision = %v, want 2", v)
	}
}

func TestTimeSync_ReadUnknownAttrReturnsFalse(t *testing.T) {
	t.Parallel()
	ts := core.NewTimeSynchronization()
	if _, ok := ts.MatterRead(0xBEEF); ok {
		t.Fatal("MatterRead(0xBEEF) = true, want false")
	}
}

func TestTimeSync_WriteReturnsError(t *testing.T) {
	t.Parallel()
	ts := core.NewTimeSynchronization()
	for _, attrID := range []uint32{0x0000, 0x0001, 0xFFFD} {
		err := ts.MatterWrite(context.Background(), attrID, nil, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("MatterWrite(0x%04X) expected error, got nil", attrID)
		}
	}
}

func TestTimeSync_InvokeSetUTCTimeAccepted(t *testing.T) {
	t.Parallel()
	ts := core.NewTimeSynchronization()
	// SetUTCTime (0x00) is accepted and returns nil (Success) — the bridge
	// does not adjust the host clock but must not return UnsupportedCommand.
	_, err := ts.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Errorf("MatterInvoke(SetUTCTime/0x00) expected nil, got %v", err)
	}
}

func TestTimeSync_InvokeUnknownCmdReturnsError(t *testing.T) {
	t.Parallel()
	ts := core.NewTimeSynchronization()
	for _, cmdID := range []uint32{0x01, 0xFF} {
		_, err := ts.MatterInvoke(context.Background(), cmdID, nil, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("MatterInvoke(0x%02X) expected error, got nil", cmdID)
		}
	}
}

func TestTimeSync_MatterReportable(t *testing.T) {
	t.Parallel()
	ts := core.NewTimeSynchronization()
	list := ts.MatterReportable()
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	for _, want := range []uint32{0x0000, 0x0001} {
		if !have[want] {
			t.Errorf("MatterReportable() missing attr 0x%04X", want)
		}
	}
}

func TestTimeSync_MatterAttributes(t *testing.T) {
	t.Parallel()
	ts := core.NewTimeSynchronization()
	list := ts.MatterAttributes()
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	for _, want := range []uint32{0x0000, 0x0001} {
		if !have[want] {
			t.Errorf("MatterAttributes() missing attr 0x%04X", want)
		}
	}
}
