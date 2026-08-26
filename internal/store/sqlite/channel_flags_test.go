// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
)

func freshChannelFlagsStore(t *testing.T) *ChannelFlagsStore {
	t.Helper()
	return NewChannelFlagsStore(openTestDB(t, "channel_flags.db"))
}

func TestChannelFlags_SetThenList(t *testing.T) {
	t.Parallel()
	s := freshChannelFlagsStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "ccu1", "ABC:1", true, false, "alice"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List len = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.CentralName != "ccu1" || r.ChannelAddress != "ABC:1" || !r.Hidden || r.Locked || r.UpdatedBy != "alice" {
		t.Errorf("row mismatch: %+v", r)
	}
	if r.UpdatedAt == "" {
		t.Error("UpdatedAt must be populated by Set")
	}
}

func TestChannelFlags_SetUpsertOverwrites(t *testing.T) {
	t.Parallel()
	s := freshChannelFlagsStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "ccu1", "ABC:1", true, false, "alice")
	if err := s.Set(ctx, "ccu1", "ABC:1", false, true, "bob"); err != nil {
		t.Fatalf("Set upsert: %v", err)
	}
	rows, _ := s.List(ctx)
	if len(rows) != 1 {
		t.Fatalf("upsert must not duplicate; len = %d", len(rows))
	}
	r := rows[0]
	if r.Hidden || !r.Locked || r.UpdatedBy != "bob" {
		t.Errorf("upsert did not overwrite: %+v", r)
	}
}

func TestChannelFlags_SetBothFalseDeletes(t *testing.T) {
	t.Parallel()
	s := freshChannelFlagsStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "ccu1", "ABC:1", true, true, "alice")
	if err := s.Set(ctx, "ccu1", "ABC:1", false, false, "alice"); err != nil {
		t.Fatalf("Set both-false: %v", err)
	}
	rows, _ := s.List(ctx)
	if len(rows) != 0 {
		t.Errorf("Set with both flags false must delete the row; List = %+v", rows)
	}
}

func TestChannelFlags_SetBothFalseOnAbsentRowIsNoop(t *testing.T) {
	t.Parallel()
	s := freshChannelFlagsStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "ccu1", "ABC:1", false, false, "alice"); err != nil {
		t.Fatalf("Set both-false on absent row: %v", err)
	}
	rows, _ := s.List(ctx)
	if len(rows) != 0 {
		t.Errorf("List after no-op clear = %+v, want empty", rows)
	}
}

func TestChannelFlags_DeleteDevicePrefixSafe(t *testing.T) {
	t.Parallel()
	s := freshChannelFlagsStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "ccu1", "ABC:1", true, false, "u")
	_ = s.Set(ctx, "ccu1", "ABC:2", true, false, "u")
	// A different device whose address shares the prefix must NOT be purged.
	_ = s.Set(ctx, "ccu1", "ABC2:0", true, false, "u")

	if err := s.DeleteDevice(ctx, "ccu1", "ABC"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	rows, _ := s.List(ctx)
	if len(rows) != 1 || rows[0].ChannelAddress != "ABC2:0" {
		t.Errorf("DeleteDevice must purge only ABC's channels, left: %+v", rows)
	}
}

func TestChannelFlags_DeleteForCentral(t *testing.T) {
	t.Parallel()
	s := freshChannelFlagsStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "ccuA", "ABC:1", true, false, "u")
	_ = s.Set(ctx, "ccuB", "ABC:1", true, false, "u")

	if err := s.DeleteForCentral(ctx, "ccuA"); err != nil {
		t.Fatalf("DeleteForCentral: %v", err)
	}
	rows, _ := s.List(ctx)
	if len(rows) != 1 || rows[0].CentralName != "ccuB" {
		t.Errorf("DeleteForCentral must scope to one central, left: %+v", rows)
	}
}

func TestChannelFlags_MultiCCUIndependent(t *testing.T) {
	t.Parallel()
	s := freshChannelFlagsStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "ccuA", "ABC:1", true, false, "u"); err != nil {
		t.Fatalf("Set ccuA: %v", err)
	}
	if err := s.Set(ctx, "ccuB", "ABC:1", false, true, "u"); err != nil {
		t.Fatalf("Set ccuB: %v", err)
	}
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("List len = %d, want 2 (same channel address, two centrals)", len(rows))
	}
	byCentral := map[string]ChannelFlag{}
	for _, r := range rows {
		byCentral[r.CentralName] = r
	}
	if a := byCentral["ccuA"]; !a.Hidden || a.Locked {
		t.Errorf("ccuA row = %+v, want hidden=true locked=false", a)
	}
	if b := byCentral["ccuB"]; b.Hidden || !b.Locked {
		t.Errorf("ccuB row = %+v, want hidden=false locked=true", b)
	}

	// Removing one central's row must not affect the other.
	if err := s.DeleteForCentral(ctx, "ccuA"); err != nil {
		t.Fatalf("DeleteForCentral: %v", err)
	}
	rows2, _ := s.List(ctx)
	if len(rows2) != 1 || rows2[0].CentralName != "ccuB" {
		t.Errorf("after DeleteForCentral(ccuA), List = %+v, want only ccuB", rows2)
	}
}
