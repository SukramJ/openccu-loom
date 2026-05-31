// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
)

// TestParityMatterJS_FabricFilterContextThreading verifies the
// WithFabricFilter / FabricFilterFromContext roundtrip.
// Mirrors matter.js InteractionServer.ts:startReadInteraction →
// OnlineContext.forFabricFilteredRead + fabricIndex — the context must
// carry the flag through the call stack unchanged.
func TestParityMatterJS_FabricFilterContextThreading(t *testing.T) {
	t.Parallel()

	t.Run("no_filter_context_returns_defaults", func(t *testing.T) {
		t.Parallel()
		filtered, fabricIndex := im.FabricFilterFromContext(context.Background())
		if filtered {
			t.Errorf("filtered: got true, want false for plain context")
		}
		if fabricIndex != 0 {
			t.Errorf("fabricIndex: got %d, want 0 for plain context", fabricIndex)
		}
	})

	t.Run("filter_false_fabricIndex_zero", func(t *testing.T) {
		t.Parallel()
		ctx := im.WithFabricFilter(context.Background(), false, 0)
		filtered, fabricIndex := im.FabricFilterFromContext(ctx)
		if filtered {
			t.Errorf("filtered: got true, want false")
		}
		if fabricIndex != 0 {
			t.Errorf("fabricIndex: got %d, want 0", fabricIndex)
		}
	})

	t.Run("filter_true_fabricIndex_nonzero", func(t *testing.T) {
		t.Parallel()
		const wantFabric uint8 = 3
		ctx := im.WithFabricFilter(context.Background(), true, wantFabric)
		filtered, fabricIndex := im.FabricFilterFromContext(ctx)
		if !filtered {
			t.Errorf("filtered: got false, want true")
		}
		if fabricIndex != wantFabric {
			t.Errorf("fabricIndex: got %d, want %d", fabricIndex, wantFabric)
		}
	})

	t.Run("inner_context_shadows_outer", func(t *testing.T) {
		t.Parallel()
		// Outer: fabric 1. Inner: fabric 2. The dispatcher may
		// in theory re-wrap the context; inner must win.
		outer := im.WithFabricFilter(context.Background(), true, 1)
		inner := im.WithFabricFilter(outer, true, 2)
		_, fabricIndex := im.FabricFilterFromContext(inner)
		if fabricIndex != 2 {
			t.Errorf("fabricIndex: got %d, want 2 (inner context should shadow outer)", fabricIndex)
		}
	})

	t.Run("filter_true_fabricIndex_zero_is_pase_path", func(t *testing.T) {
		t.Parallel()
		// fabricIndex==0 with filtered=true represents a PASE session.
		// matter.js treats this as unfiltered. Our implementation
		// preserves the raw values; the consumer (MatterReadFiltered)
		// is responsible for treating fabricIndex==0 as unfiltered.
		ctx := im.WithFabricFilter(context.Background(), true, 0)
		filtered, fabricIndex := im.FabricFilterFromContext(ctx)
		if !filtered {
			t.Errorf("filtered: got false, want true")
		}
		if fabricIndex != 0 {
			t.Errorf("fabricIndex: got %d, want 0", fabricIndex)
		}
	})
}
