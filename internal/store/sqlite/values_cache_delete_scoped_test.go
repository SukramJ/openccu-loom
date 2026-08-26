// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
	"time"
)

// TestValuesCache_DeleteForInterface verifies that DeleteForInterface removes
// only rows belonging to the targeted (central, interface) and reports the
// correct affected-row count.
func TestValuesCache_DeleteForInterface(t *testing.T) {
	t.Parallel()
	s := freshValuesCacheStore(t)
	ctx := context.Background()
	now := time.Now()

	save := func(central, iface, ch, param string) {
		t.Helper()
		if err := s.SaveValue(ctx, central, iface, ch, param, "v", now, now); err != nil {
			t.Fatalf("SaveValue: %v", err)
		}
	}

	// Two rows in the target scope, one in a different interface.
	save("ccu1", "HmIP-RF", "DEV:1", "P1")
	save("ccu1", "HmIP-RF", "DEV:2", "P2")
	save("ccu1", "BidCos-RF", "DEV:3", "P3")

	n, err := s.DeleteForInterface(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("DeleteForInterface: %v", err)
	}
	if n != 2 {
		t.Errorf("rows affected = %d, want 2", n)
	}

	// Rows for BidCos-RF must still exist.
	rows, err := s.LoadChannel(ctx, "ccu1", "BidCos-RF", "DEV:3")
	if err != nil {
		t.Fatalf("LoadChannel after delete: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("BidCos-RF row count = %d, want 1", len(rows))
	}

	// HmIP-RF rows must be gone.
	rows, err = s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEV:1")
	if err != nil {
		t.Fatalf("LoadChannel HmIP-RF: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("HmIP-RF rows remaining = %d, want 0", len(rows))
	}
}

// TestValuesCache_DeleteForInterface_NilStore verifies that a nil receiver
// returns (0, nil) without panicking.
func TestValuesCache_DeleteForInterface_NilStore(t *testing.T) {
	t.Parallel()
	var s *ValuesCacheStore
	n, err := s.DeleteForInterface(context.Background(), "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("nil store: unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("nil store: n = %d, want 0", n)
	}
}
