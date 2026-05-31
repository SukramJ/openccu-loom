// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package store_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// TestLoadDiagnostics_InitialSeedIsZero verifies that the migration's seeded
// singleton row has reboot_count=0 and base_operational_hours=0.
func TestLoadDiagnostics_InitialSeedIsZero(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)

	got, err := s.LoadDiagnostics(context.Background())
	if err != nil {
		t.Fatalf("LoadDiagnostics: %v", err)
	}
	if got.RebootCount != 0 {
		t.Errorf("RebootCount = %d, want 0 (fresh migration)", got.RebootCount)
	}
	if got.BaseOperationalHours != 0 {
		t.Errorf("BaseOperationalHours = %d, want 0 (fresh migration)", got.BaseOperationalHours)
	}
}

// TestSaveDiagnostics_RoundTrip verifies that SaveDiagnostics persists values
// and LoadDiagnostics reads them back.
func TestSaveDiagnostics_RoundTrip(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	want := store.DiagnosticsRecord{
		RebootCount:          7,
		BaseOperationalHours: 42,
	}
	if err := s.SaveDiagnostics(ctx, want); err != nil {
		t.Fatalf("SaveDiagnostics: %v", err)
	}

	got, err := s.LoadDiagnostics(ctx)
	if err != nil {
		t.Fatalf("LoadDiagnostics after Save: %v", err)
	}
	if got.RebootCount != want.RebootCount {
		t.Errorf("RebootCount = %d, want %d", got.RebootCount, want.RebootCount)
	}
	if got.BaseOperationalHours != want.BaseOperationalHours {
		t.Errorf("BaseOperationalHours = %d, want %d", got.BaseOperationalHours, want.BaseOperationalHours)
	}
}

// TestSaveDiagnostics_IncrementRebootCount simulates what the daemon does
// on startup: read → increment → save → read back.
func TestSaveDiagnostics_IncrementRebootCount(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	initial, err := s.LoadDiagnostics(ctx)
	if err != nil {
		t.Fatalf("LoadDiagnostics: %v", err)
	}

	incremented := initial
	incremented.RebootCount++
	if err := s.SaveDiagnostics(ctx, incremented); err != nil {
		t.Fatalf("SaveDiagnostics: %v", err)
	}

	got, err := s.LoadDiagnostics(ctx)
	if err != nil {
		t.Fatalf("second LoadDiagnostics: %v", err)
	}
	if got.RebootCount != initial.RebootCount+1 {
		t.Errorf("RebootCount = %d, want %d", got.RebootCount, initial.RebootCount+1)
	}
}

// TestSaveDiagnostics_UpdatedAtIsSet verifies that SaveDiagnostics writes a
// non-zero UpdatedAt timestamp (the store uses time.Now().Unix() internally).
func TestSaveDiagnostics_UpdatedAtIsSet(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	if err := s.SaveDiagnostics(ctx, store.DiagnosticsRecord{RebootCount: 1, BaseOperationalHours: 0}); err != nil {
		t.Fatalf("SaveDiagnostics: %v", err)
	}

	got, err := s.LoadDiagnostics(ctx)
	if err != nil {
		t.Fatalf("LoadDiagnostics: %v", err)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero after SaveDiagnostics")
	}
}

// TestSaveDiagnostics_LargeValues exercises boundaries for the counter fields.
func TestSaveDiagnostics_LargeValues(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	want := store.DiagnosticsRecord{
		RebootCount:          0xFFFF,     // max uint16
		BaseOperationalHours: 0xFFFFFFFF, // max uint32
	}
	if err := s.SaveDiagnostics(ctx, want); err != nil {
		t.Fatalf("SaveDiagnostics large values: %v", err)
	}
	got, err := s.LoadDiagnostics(ctx)
	if err != nil {
		t.Fatalf("LoadDiagnostics: %v", err)
	}
	if got.RebootCount != want.RebootCount {
		t.Errorf("RebootCount = %d, want %d", got.RebootCount, want.RebootCount)
	}
	if got.BaseOperationalHours != want.BaseOperationalHours {
		t.Errorf("BaseOperationalHours = %d, want %d", got.BaseOperationalHours, want.BaseOperationalHours)
	}
}

// TestSaveDiagnostics verifies that diagnostics can be stored and immediately
// loaded back (increment pattern).
func TestSaveDiagnostics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	// Load the seed row written by the migration.
	orig, err := s.LoadDiagnostics(ctx)
	if err != nil {
		t.Fatalf("LoadDiagnostics (initial): %v", err)
	}

	updated := store.DiagnosticsRecord{
		RebootCount:          orig.RebootCount + 1,
		BaseOperationalHours: orig.BaseOperationalHours + 10,
	}
	if err := s.SaveDiagnostics(ctx, updated); err != nil {
		t.Fatalf("SaveDiagnostics: %v", err)
	}
	got, err := s.LoadDiagnostics(ctx)
	if err != nil {
		t.Fatalf("LoadDiagnostics: %v", err)
	}
	if got.RebootCount != updated.RebootCount {
		t.Errorf("RebootCount = %d, want %d", got.RebootCount, updated.RebootCount)
	}
}

// TestDiagnostics_SaveZeroValues verifies that SaveDiagnostics with a zero-value
// record does not error and LoadDiagnostics returns zero counts.
func TestDiagnostics_SaveZeroValues(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := store.New(db)
	ctx := context.Background()

	if err := s.SaveDiagnostics(ctx, store.DiagnosticsRecord{}); err != nil {
		t.Fatalf("SaveDiagnostics zero values: %v", err)
	}
	got, err := s.LoadDiagnostics(ctx)
	if err != nil {
		t.Fatalf("LoadDiagnostics: %v", err)
	}
	if got.RebootCount != 0 {
		t.Errorf("RebootCount = %d, want 0", got.RebootCount)
	}
}

// TestDiagnostics_LoadFreshDB verifies that on a fresh DB the seeded singleton
// row has reboot_count=0.
func TestDiagnostics_LoadFreshDB(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	got, err := s.LoadDiagnostics(ctx)
	if err != nil {
		t.Fatalf("LoadDiagnostics: %v", err)
	}
	// Migration seeds a row with zeros.
	if got.RebootCount != 0 {
		t.Errorf("RebootCount=%d want 0", got.RebootCount)
	}
}

// TestDiagnostics_SaveAndLoad verifies that SaveDiagnostics persists values and
// LoadDiagnostics reads them back.
func TestDiagnostics_SaveAndLoad(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	if err := s.SaveDiagnostics(ctx, store.DiagnosticsRecord{
		RebootCount:          7,
		BaseOperationalHours: 100,
	}); err != nil {
		t.Fatalf("SaveDiagnostics: %v", err)
	}
	got, err := s.LoadDiagnostics(ctx)
	if err != nil {
		t.Fatalf("LoadDiagnostics: %v", err)
	}
	if got.RebootCount != 7 {
		t.Errorf("RebootCount=%d want 7", got.RebootCount)
	}
	if got.BaseOperationalHours != 100 {
		t.Errorf("BaseOperationalHours=%d want 100", got.BaseOperationalHours)
	}
}
