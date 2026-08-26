// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// TestVisibilityUnIgnoreRoundtrip verifies the basic
// Replace → List → Patterns lifecycle for one central. Empty central →
// empty list. Replace with patterns → List/Patterns reflect them sorted.
func TestVisibilityUnIgnoreRoundtrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	s := sqlite.NewVisibilityUnIgnoreStore(db)
	ctx := context.Background()

	// Empty central returns no rows.
	got, err := s.Patterns(ctx, "ccu-01")
	if err != nil {
		t.Fatalf("Patterns empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Patterns empty: got %v, want []", got)
	}

	// Replace with three patterns + duplicates + blank — store dedupes
	// and orders alphabetically.
	if err := s.Replace(ctx, "ccu-01", []string{"*:*:RSSI_PEER", "HmIP-eTRV-2:0:LOW_BAT", "*:*:RSSI_PEER", "", "*:0:OPERATING_VOLTAGE"}, "alice"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	got, err = s.Patterns(ctx, "ccu-01")
	if err != nil {
		t.Fatalf("Patterns post-replace: %v", err)
	}
	want := []string{"*:*:RSSI_PEER", "*:0:OPERATING_VOLTAGE", "HmIP-eTRV-2:0:LOW_BAT"}
	if len(got) != len(want) {
		t.Fatalf("Patterns count = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, p := range want {
		if got[i] != p {
			t.Errorf("Patterns[%d] = %q, want %q", i, got[i], p)
		}
	}

	// List exposes updated_by + updated_at.
	entries, err := s.List(ctx, "ccu-01")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("List count = %d, want 3", len(entries))
	}
	for _, e := range entries {
		if e.UpdatedBy != "alice" {
			t.Errorf("UpdatedBy = %q, want alice", e.UpdatedBy)
		}
		if e.UpdatedAt.IsZero() {
			t.Errorf("UpdatedAt is zero for pattern %q", e.Pattern)
		}
	}

	// Replace with empty clears the list.
	if err := s.Replace(ctx, "ccu-01", nil, "alice"); err != nil {
		t.Fatalf("Replace clear: %v", err)
	}
	got, err = s.Patterns(ctx, "ccu-01")
	if err != nil {
		t.Fatalf("Patterns post-clear: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Patterns post-clear = %v, want []", got)
	}
}

// TestVisibilityUnIgnoreCentralPartitioning verifies that two centrals
// keep independent pattern sets — a write to ccu-01 does not affect
// ccu-02 (ADR 0002 multi-CCU first-class).
func TestVisibilityUnIgnoreCentralPartitioning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	s := sqlite.NewVisibilityUnIgnoreStore(db)
	ctx := context.Background()

	if err := s.Replace(ctx, "ccu-01", []string{"*:*:RSSI_PEER"}, "alice"); err != nil {
		t.Fatalf("Replace ccu-01: %v", err)
	}
	if err := s.Replace(ctx, "ccu-02", []string{"HmIP-SWDO:1:ERROR"}, "bob"); err != nil {
		t.Fatalf("Replace ccu-02: %v", err)
	}

	a, err := s.Patterns(ctx, "ccu-01")
	if err != nil {
		t.Fatalf("Patterns ccu-01: %v", err)
	}
	if len(a) != 1 || a[0] != "*:*:RSSI_PEER" {
		t.Errorf("ccu-01 patterns = %v, want [*:*:RSSI_PEER]", a)
	}
	b, err := s.Patterns(ctx, "ccu-02")
	if err != nil {
		t.Fatalf("Patterns ccu-02: %v", err)
	}
	if len(b) != 1 || b[0] != "HmIP-SWDO:1:ERROR" {
		t.Errorf("ccu-02 patterns = %v, want [HmIP-SWDO:1:ERROR]", b)
	}

	// Clear ccu-01 — ccu-02 stays.
	if err := s.Replace(ctx, "ccu-01", nil, "alice"); err != nil {
		t.Fatalf("Replace clear ccu-01: %v", err)
	}
	a, _ = s.Patterns(ctx, "ccu-01")
	if len(a) != 0 {
		t.Errorf("ccu-01 post-clear = %v, want []", a)
	}
	b, _ = s.Patterns(ctx, "ccu-02")
	if len(b) != 1 {
		t.Errorf("ccu-02 after ccu-01 clear = %v, want unchanged", b)
	}
}

// TestVisibilityUnIgnoreSeedIfEmpty verifies the bootstrap helper:
// inserts only when the central has zero rows; subsequent calls are
// no-ops (runtime edits via Replace win).
func TestVisibilityUnIgnoreSeedIfEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	s := sqlite.NewVisibilityUnIgnoreStore(db)
	ctx := context.Background()

	if err := s.SeedIfEmpty(ctx, "ccu-01", []string{"HmIP-eTRV-2:0:LOW_BAT"}); err != nil {
		t.Fatalf("Seed initial: %v", err)
	}
	got, _ := s.Patterns(ctx, "ccu-01")
	if len(got) != 1 || got[0] != "HmIP-eTRV-2:0:LOW_BAT" {
		t.Errorf("post-seed = %v, want [HmIP-eTRV-2:0:LOW_BAT]", got)
	}

	// A runtime Replace narrows the list.
	if err := s.Replace(ctx, "ccu-01", []string{"*:*:RSSI_PEER"}, "alice"); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	// SeedIfEmpty must be a no-op now — the YAML default does not
	// override what the operator changed at runtime.
	if err := s.SeedIfEmpty(ctx, "ccu-01", []string{"HmIP-eTRV-2:0:LOW_BAT"}); err != nil {
		t.Fatalf("Seed after replace: %v", err)
	}
	got, _ = s.Patterns(ctx, "ccu-01")
	if len(got) != 1 || got[0] != "*:*:RSSI_PEER" {
		t.Errorf("Seed must not overwrite runtime edits: got %v", got)
	}
}
