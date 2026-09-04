// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core_test

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
)

func TestBinding_ClusterID(t *testing.T) {
	t.Parallel()
	b := core.NewBinding()
	if got := b.MatterClusterID(); got != 0x001E {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x001E", got)
	}
}

func TestBinding_ClusterRevision(t *testing.T) {
	t.Parallel()
	b := core.NewBinding()
	v, ok := b.MatterRead(cluster.AttrGlobalClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if v.(uint16) != 1 {
		t.Fatalf("ClusterRevision = %v, want 1", v)
	}
}

func TestBinding_ReadInitiallyEmpty(t *testing.T) {
	t.Parallel()
	b := core.NewBinding()
	v, ok := b.MatterRead(0x0000)
	if !ok {
		t.Fatal("Binding attr: ok=false")
	}
	list := v.([]core.TargetStruct)
	if len(list) != 0 {
		t.Fatalf("initial binding list len=%d, want 0", len(list))
	}
}

func TestBinding_WriteSetsList(t *testing.T) {
	t.Parallel()
	b := core.NewBinding()
	ctx := context.Background()
	targets := []core.TargetStruct{
		{Node: 1, Endpoint: 2, Cluster: 0x0006, FabricIndex: 1},
		{Node: 3, Group: 4, FabricIndex: 1},
	}
	if err := b.MatterWrite(ctx, 0x0000, targets); err != nil {
		t.Fatalf("MatterWrite: %v", err)
	}
	v, ok := b.MatterRead(0x0000)
	if !ok {
		t.Fatal("read after write: ok=false")
	}
	list := v.([]core.TargetStruct)
	if len(list) != 2 {
		t.Fatalf("binding list len=%d, want 2", len(list))
	}
	if list[0].Node != 1 || list[1].Group != 4 {
		t.Fatalf("unexpected binding values: %+v", list)
	}
}

func TestBinding_WriteWrongType(t *testing.T) {
	t.Parallel()
	b := core.NewBinding()
	err := b.MatterWrite(context.Background(), 0x0000, "not-a-slice")
	if err == nil {
		t.Fatal("expected error for wrong type, got nil")
	}
}

func TestBinding_WriteUnknownAttribute(t *testing.T) {
	t.Parallel()
	b := core.NewBinding()
	err := b.MatterWrite(context.Background(), 0xBEEF, []core.TargetStruct{})
	if err == nil {
		t.Fatal("expected error for unknown attribute, got nil")
	}
}

func TestBinding_ReadReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()
	b := core.NewBinding()
	ctx := context.Background()
	targets := []core.TargetStruct{{Node: 99, FabricIndex: 1}}
	if err := b.MatterWrite(ctx, 0x0000, targets); err != nil {
		t.Fatalf("MatterWrite: %v", err)
	}
	v1, _ := b.MatterRead(0x0000)
	list1 := v1.([]core.TargetStruct)
	list1[0].Node = 0xDEAD
	v2, _ := b.MatterRead(0x0000)
	list2 := v2.([]core.TargetStruct)
	if list2[0].Node == 0xDEAD {
		t.Fatal("mutation of returned slice affected internal state")
	}
}

func TestBinding_InvokeReturnsError(t *testing.T) {
	t.Parallel()
	b := core.NewBinding()
	ctx := context.Background()
	for _, cmdID := range []uint32{0x00, 0x01} {
		_, err := b.MatterInvoke(ctx, cmdID, nil)
		if err == nil {
			t.Errorf("MatterInvoke(0x%02X) expected error, got nil", cmdID)
		}
	}
}

func TestBinding_ConcurrentReadWrite(t *testing.T) {
	t.Parallel()
	b := core.NewBinding()
	ctx := context.Background()

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			targets := []core.TargetStruct{{Node: uint64(i), FabricIndex: 1}} //nolint:gosec // G115: i is a small goroutine index bounded by test parallelism count
			_ = b.MatterWrite(ctx, 0x0000, targets)
		}(i)
		go func() {
			defer wg.Done()
			_, _ = b.MatterRead(0x0000)
		}()
	}
	wg.Wait()
}

func TestBinding_MatterReportable(t *testing.T) {
	t.Parallel()
	b := core.NewBinding()
	list := b.MatterReportable()
	// Binding (0x0000) must be listed per Matter §9.6.
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	if !have[0x0000] {
		t.Errorf("MatterReportable() missing Binding attr (0x0000); list = %v", list)
	}
}

func TestBinding_MatterAttributes(t *testing.T) {
	t.Parallel()
	b := core.NewBinding()
	list := b.MatterAttributes()
	if len(list) == 0 {
		t.Fatal("MatterAttributes() is empty — dispatcher falls back to reportable-only surface")
	}
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	if !have[0x0000] {
		t.Errorf("MatterAttributes() missing Binding attr (0x0000); list = %v", list)
	}
}
