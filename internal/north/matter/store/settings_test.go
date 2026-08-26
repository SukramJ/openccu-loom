// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package store_test

import (
	"context"
	"testing"

	store "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// TestSettings_GetSetting_MissReturnsNotOK verifies that GetSetting
// reports ok=false for a key that has never been written, so callers
// keep their configured default.
func TestSettings_GetSetting_MissReturnsNotOK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	got, ok, err := s.GetSetting(ctx, store.SettingNodeLabel)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if ok {
		t.Fatal("GetSetting: ok=true for a key never written")
	}
	if got != "" {
		t.Fatalf("GetSetting: value=%q, want empty on miss", got)
	}
}

// TestSettings_SetSetting_RoundTrips verifies that a value written via
// SetSetting is returned unchanged by a subsequent GetSetting.
func TestSettings_SetSetting_RoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	if err := s.SetSetting(ctx, store.SettingNodeLabel, "living-room-bridge"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	got, ok, err := s.GetSetting(ctx, store.SettingNodeLabel)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if !ok {
		t.Fatal("GetSetting: ok=false after SetSetting")
	}
	if got != "living-room-bridge" {
		t.Fatalf("GetSetting = %q, want living-room-bridge", got)
	}
}

// TestSettings_SetSetting_UpsertOverwrites verifies that a second
// SetSetting call for the same key overwrites the previously persisted
// value rather than erroring on the UNIQUE key constraint.
func TestSettings_SetSetting_UpsertOverwrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	if err := s.SetSetting(ctx, store.SettingLocation, "DE"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := s.SetSetting(ctx, store.SettingLocation, "FR"); err != nil {
		t.Fatalf("SetSetting overwrite: %v", err)
	}
	got, ok, err := s.GetSetting(ctx, store.SettingLocation)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if !ok {
		t.Fatal("GetSetting: ok=false after overwrite")
	}
	if got != "FR" {
		t.Fatalf("GetSetting = %q, want FR (overwritten value)", got)
	}
}

// TestSettings_GetMetadataCounter_MissReturnsNotOK verifies that
// GetMetadataCounter reports ok=false for a key that has never been
// written.
func TestSettings_GetMetadataCounter_MissReturnsNotOK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	got, ok, err := s.GetMetadataCounter(ctx, store.MetadataKeyEventNumber)
	if err != nil {
		t.Fatalf("GetMetadataCounter: %v", err)
	}
	if ok {
		t.Fatal("GetMetadataCounter: ok=true for a key never written")
	}
	if got != 0 {
		t.Fatalf("GetMetadataCounter: value=%d, want 0 on miss", got)
	}
}

// TestSettings_SetMetadataCounter_RoundTrips verifies that a counter
// written via SetMetadataCounter is returned unchanged by a subsequent
// GetMetadataCounter.
func TestSettings_SetMetadataCounter_RoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	if err := s.SetMetadataCounter(ctx, store.MetadataKeyEventNumber, 4096); err != nil {
		t.Fatalf("SetMetadataCounter: %v", err)
	}
	got, ok, err := s.GetMetadataCounter(ctx, store.MetadataKeyEventNumber)
	if err != nil {
		t.Fatalf("GetMetadataCounter: %v", err)
	}
	if !ok {
		t.Fatal("GetMetadataCounter: ok=false after SetMetadataCounter")
	}
	if got != 4096 {
		t.Fatalf("GetMetadataCounter = %d, want 4096", got)
	}
}

// TestSettings_SetMetadataCounter_UpsertOverwrites verifies that a
// second SetMetadataCounter call for the same key overwrites the
// previously persisted counter.
func TestSettings_SetMetadataCounter_UpsertOverwrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	if err := s.SetMetadataCounter(ctx, store.MetadataKeyEventNumber, 100); err != nil {
		t.Fatalf("SetMetadataCounter: %v", err)
	}
	if err := s.SetMetadataCounter(ctx, store.MetadataKeyEventNumber, 200); err != nil {
		t.Fatalf("SetMetadataCounter overwrite: %v", err)
	}
	got, ok, err := s.GetMetadataCounter(ctx, store.MetadataKeyEventNumber)
	if err != nil {
		t.Fatalf("GetMetadataCounter: %v", err)
	}
	if !ok {
		t.Fatal("GetMetadataCounter: ok=false after overwrite")
	}
	if got != 200 {
		t.Fatalf("GetMetadataCounter = %d, want 200 (overwritten value)", got)
	}
}
