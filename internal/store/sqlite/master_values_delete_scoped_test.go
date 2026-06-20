// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"
)

// TestMasterValues_DeleteForInterface verifies that DeleteForInterface removes
// only rows belonging to the targeted (central, interface) and reports the
// correct affected-row count.
func TestMasterValues_DeleteForInterface(t *testing.T) {
	t.Parallel()
	s := freshMasterValuesStore(t)
	ctx := context.Background()

	save := func(central, iface, ch string) {
		t.Helper()
		if err := s.SaveChannel(ctx, central, iface, ch, map[string]any{"P": 1}); err != nil {
			t.Fatalf("SaveChannel: %v", err)
		}
	}

	// Two rows in the target scope, one in a different interface.
	save("ccu1", "HmIP-RF", "DEV:1")
	save("ccu1", "HmIP-RF", "DEV:2")
	save("ccu1", "BidCos-RF", "DEV:3")

	n, err := s.DeleteForInterface(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("DeleteForInterface: %v", err)
	}
	if n != 2 {
		t.Errorf("rows affected = %d, want 2", n)
	}

	// BidCos-RF row must still exist.
	_, ok, err := s.LoadChannel(ctx, "ccu1", "BidCos-RF", "DEV:3")
	if err != nil {
		t.Fatalf("LoadChannel after delete: %v", err)
	}
	if !ok {
		t.Error("BidCos-RF row missing after DeleteForInterface for HmIP-RF")
	}

	// HmIP-RF rows must be gone.
	_, ok, err = s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEV:1")
	if err != nil {
		t.Fatalf("LoadChannel HmIP-RF: %v", err)
	}
	if ok {
		t.Error("HmIP-RF row still present after DeleteForInterface")
	}
}

// TestMasterValues_DeleteForInterface_NilStore verifies that a nil receiver
// returns (0, nil) without panicking.
func TestMasterValues_DeleteForInterface_NilStore(t *testing.T) {
	t.Parallel()
	var s *MasterValuesStore
	n, err := s.DeleteForInterface(context.Background(), "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("nil store: unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("nil store: n = %d, want 0", n)
	}
}
