// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	mstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// TestFabricFilteredFabricsRead — when the request has
// FabricFiltered=true and fabricIndex=2, MatterReadFiltered(Fabrics)
// returns only the fabric with FabricIndex==2.
// Mirrors matter.js OperationalCredentialsBehavior.fabrics getter —
// packages/node/src/behaviors/operational-credentials/
// OperationalCredentialsServer.ts.
func TestFabricFilteredFabricsRead(t *testing.T) {
	t.Parallel()

	fs := newFakeStore()
	// Pre-populate two fabrics so both paths are exercised.
	ctx := context.Background()
	idx1, err := fs.AddFabric(ctx, mstore.FabricRecord{
		FabricID:      0xAAAA0001,
		NodeID:        0xBBBB0001,
		RootPublicKey: []byte{0x04, 0x01},
		VendorID:      0x1234,
		Label:         "fabric-one",
	})
	if err != nil {
		t.Fatalf("AddFabric(1): %v", err)
	}
	idx2, err := fs.AddFabric(ctx, mstore.FabricRecord{
		FabricID:      0xAAAA0002,
		NodeID:        0xBBBB0002,
		RootPublicKey: []byte{0x04, 0x02},
		VendorID:      0x5678,
		Label:         "fabric-two",
	})
	if err != nil {
		t.Fatalf("AddFabric(2): %v", err)
	}

	oc, err := core.NewOperationalCredentials(fs, core.OpcredsConfig{SupportedFabrics: 5})
	if err != nil {
		t.Fatalf("NewOperationalCredentials: %v", err)
	}

	t.Run("filtered_returns_own_fabric_only", func(t *testing.T) {
		t.Parallel()
		ctx2 := im.WithFabricFilter(context.Background(), true, idx2)
		v, ok := oc.MatterReadFiltered(ctx2, 0x0001 /* opcredsAttrFabrics */)
		if !ok {
			t.Fatal("MatterReadFiltered(Fabrics): ok=false")
		}
		fabrics, ok := v.([]core.FabricDescriptorStruct)
		if !ok {
			t.Fatalf("expected []FabricDescriptorStruct, got %T", v)
		}
		if len(fabrics) != 1 {
			t.Fatalf("filtered list length: got %d, want 1", len(fabrics))
		}
		if fabrics[0].FabricIndex != idx2 {
			t.Errorf("FabricIndex: got %d, want %d", fabrics[0].FabricIndex, idx2)
		}
		if fabrics[0].Label != "fabric-two" {
			t.Errorf("Label: got %q, want %q", fabrics[0].Label, "fabric-two")
		}
	})

	t.Run("unfiltered_returns_all_fabrics", func(t *testing.T) {
		t.Parallel()
		ctx2 := im.WithFabricFilter(context.Background(), false, idx1)
		v, ok := oc.MatterReadFiltered(ctx2, 0x0001 /* opcredsAttrFabrics */)
		if !ok {
			t.Fatal("MatterReadFiltered(Fabrics) unfiltered: ok=false")
		}
		fabrics, ok := v.([]core.FabricDescriptorStruct)
		if !ok {
			t.Fatalf("expected []FabricDescriptorStruct, got %T", v)
		}
		if len(fabrics) != 2 {
			t.Fatalf("unfiltered list length: got %d, want 2", len(fabrics))
		}
	})

	t.Run("fabricIndex_zero_returns_all_fabrics", func(t *testing.T) {
		t.Parallel()
		// fabricIndex==0 means PASE; matter.js treats this as unfiltered.
		ctx2 := im.WithFabricFilter(context.Background(), true, 0)
		v, ok := oc.MatterReadFiltered(ctx2, 0x0001 /* opcredsAttrFabrics */)
		if !ok {
			t.Fatal("MatterReadFiltered(Fabrics) pase: ok=false")
		}
		fabrics, ok := v.([]core.FabricDescriptorStruct)
		if !ok {
			t.Fatalf("expected []FabricDescriptorStruct, got %T", v)
		}
		if len(fabrics) != 2 {
			t.Fatalf("PASE path list length: got %d, want 2 (unfiltered)", len(fabrics))
		}
	})

	t.Run("non_fabric_attr_forwards_to_matterread", func(t *testing.T) {
		t.Parallel()
		// SupportedFabrics (0x0002) is not fabric-scoped; it must still
		// be readable via MatterReadFiltered.
		ctx2 := im.WithFabricFilter(context.Background(), true, idx1)
		v, ok := oc.MatterReadFiltered(ctx2, 0x0002 /* opcredsAttrSupportedFabrics */)
		if !ok {
			t.Fatal("MatterReadFiltered(SupportedFabrics): ok=false")
		}
		sf, ok := v.(uint8)
		if !ok {
			t.Fatalf("expected uint8, got %T", v)
		}
		if sf < 5 {
			t.Errorf("SupportedFabrics: got %d, want >= 5 (spec floor)", sf)
		}
	})

	_ = idx1 // suppress unused-variable lint
}
