// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"
)

func freshRecordingOverrideStore(t *testing.T) *RecordingOverrideStore {
	t.Helper()
	return NewRecordingOverrideStore(openHistoryDB(t, "hist.db"))
}

func TestRecordingOverrides_SetGetRoundTrip(t *testing.T) {
	t.Parallel()
	s := freshRecordingOverrideStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "ccu1", "ccu1-HmIP-RF", "DEV:1", "TEMPERATURE", true, "alice"); err != nil {
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
	if r.CentralName != "ccu1" || r.InterfaceID != "ccu1-HmIP-RF" ||
		r.ChannelAddress != "DEV:1" || r.Parameter != "TEMPERATURE" || !r.Record || r.UpdatedBy != "alice" {
		t.Errorf("row mismatch: %+v", r)
	}
}

func TestRecordingOverrides_Upsert(t *testing.T) {
	t.Parallel()
	s := freshRecordingOverrideStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "ccu1", "if", "DEV:1", "P", true, "alice")
	if err := s.Set(ctx, "ccu1", "if", "DEV:1", "P", false, "bob"); err != nil {
		t.Fatalf("Set upsert: %v", err)
	}
	rows, _ := s.List(ctx)
	if len(rows) != 1 {
		t.Fatalf("upsert must not duplicate; len = %d", len(rows))
	}
	if rows[0].Record || rows[0].UpdatedBy != "bob" {
		t.Errorf("upsert did not overwrite: %+v", rows[0])
	}
}

func TestRecordingOverrides_Clear(t *testing.T) {
	t.Parallel()
	s := freshRecordingOverrideStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "ccu1", "if", "DEV:1", "P", true, "alice")
	if err := s.Clear(ctx, "ccu1", "if", "DEV:1", "P"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	rows, _ := s.List(ctx)
	if len(rows) != 0 {
		t.Errorf("Clear must remove the row; len = %d", len(rows))
	}
	// Clearing an absent row is a no-op.
	if err := s.Clear(ctx, "ccu1", "if", "DEV:1", "P"); err != nil {
		t.Errorf("Clear of absent row must be a no-op, got %v", err)
	}
}

func TestRecordingOverrides_DeleteDevicePrefixSafe(t *testing.T) {
	t.Parallel()
	s := freshRecordingOverrideStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "ccu1", "if", "DEV:1", "P", true, "u")
	_ = s.Set(ctx, "ccu1", "if", "DEV:2", "P", true, "u")
	// A different device whose address shares the prefix must NOT be purged.
	_ = s.Set(ctx, "ccu1", "if", "DEV2:1", "P", true, "u")

	if err := s.DeleteDevice(ctx, "ccu1", "if", "DEV"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	rows, _ := s.List(ctx)
	if len(rows) != 1 || rows[0].ChannelAddress != "DEV2:1" {
		t.Errorf("DeleteDevice must purge only DEV's channels, left: %+v", rows)
	}
}

func TestRecordingOverrides_DeleteForCentral(t *testing.T) {
	t.Parallel()
	s := freshRecordingOverrideStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "ccuA", "if", "DEV:1", "P", true, "u")
	_ = s.Set(ctx, "ccuB", "if", "DEV:1", "P", true, "u")

	if err := s.DeleteForCentral(ctx, "ccuA"); err != nil {
		t.Fatalf("DeleteForCentral: %v", err)
	}
	rows, _ := s.List(ctx)
	if len(rows) != 1 || rows[0].CentralName != "ccuB" {
		t.Errorf("DeleteForCentral must scope to one central, left: %+v", rows)
	}
}

func TestRecordingOverrides_NilSafe(t *testing.T) {
	t.Parallel()
	var s *RecordingOverrideStore
	ctx := context.Background()
	if rows, err := s.List(ctx); err != nil || rows != nil {
		t.Errorf("nil List: %v %v", rows, err)
	}
	if err := s.Set(ctx, "c", "i", "ch", "p", true, "u"); err != nil {
		t.Errorf("nil Set: %v", err)
	}
	if err := s.Clear(ctx, "c", "i", "ch", "p"); err != nil {
		t.Errorf("nil Clear: %v", err)
	}
}
