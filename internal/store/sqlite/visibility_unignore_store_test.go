// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
)

// ---------------------------------------------------------------------------
// VisibilityUnIgnoreStore.SeedIfEmpty — nil/empty guards and idempotency
// ---------------------------------------------------------------------------

// TestVisibilityUnIgnoreStoreSeedIfEmptyNilGuards covers the nil/empty guards.
func TestVisibilityUnIgnoreStoreSeedIfEmptyNilGuards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// nil store must not panic.
	var s *VisibilityUnIgnoreStore
	if err := s.SeedIfEmpty(ctx, "ccu1", []string{"pat"}); err != nil {
		t.Fatalf("nil store SeedIfEmpty: %v", err)
	}
	// empty patterns slice must be a no-op.
	db := openTestDB(t, "seed_guard.db")
	s2 := NewVisibilityUnIgnoreStore(db)
	if err := s2.SeedIfEmpty(ctx, "ccu1", nil); err != nil {
		t.Fatalf("empty patterns SeedIfEmpty: %v", err)
	}
}

func TestVisibilityUnIgnoreStoreSeedIfEmpty(t *testing.T) {
	db := openTestDB(t, "vis.db")
	s := NewVisibilityUnIgnoreStore(db)
	ctx := context.Background()

	// Empty table: seed should insert.
	if err := s.SeedIfEmpty(ctx, "ccu1", []string{"HMIP-*:*:VALUES:LEVEL"}); err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}
	entries, err := s.List(ctx, "ccu1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("SeedIfEmpty must insert rows when table is empty")
	}

	// Non-empty table: seed must be a no-op.
	if err := s.SeedIfEmpty(ctx, "ccu1", []string{"SOME-OTHER-PATTERN"}); err != nil {
		t.Fatalf("SeedIfEmpty(non-empty): %v", err)
	}
	entries2, _ := s.List(ctx, "ccu1")
	if len(entries2) != len(entries) {
		t.Errorf("SeedIfEmpty on non-empty table must not add rows; got %d, want %d",
			len(entries2), len(entries))
	}
}

// ---------------------------------------------------------------------------
// VisibilityUnIgnoreStore.Replace — deduplication and empty-list (clear) branch
// ---------------------------------------------------------------------------

// TestVisibilityUnIgnoreStoreReplaceDedupsAndEmpty covers the empty-patterns
// (clear-list) branch.
func TestVisibilityUnIgnoreStoreReplaceDedupsAndEmpty(t *testing.T) {
	db := openTestDB(t, "vis_replace.db")
	s := NewVisibilityUnIgnoreStore(db)
	ctx := context.Background()

	// Insert some patterns.
	if err := s.Replace(ctx, "ccu1", []string{"A", "B", "A"}, "test"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	patterns, _ := s.Patterns(ctx, "ccu1")
	if len(patterns) != 2 {
		t.Errorf("deduplicated patterns len=%d, want 2", len(patterns))
	}

	// Replace with empty list clears.
	if err := s.Replace(ctx, "ccu1", []string{}, "test"); err != nil {
		t.Fatalf("Replace empty: %v", err)
	}
	patterns2, _ := s.Patterns(ctx, "ccu1")
	if len(patterns2) != 0 {
		t.Errorf("after empty replace len=%d, want 0", len(patterns2))
	}
}
