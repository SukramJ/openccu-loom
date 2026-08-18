// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
)

// buildVisibilityStore returns a *sqlite.VisibilityUnIgnoreStore backed by a
// private, already-migrated database file in the test's temp dir.
func buildVisibilityStore(t *testing.T) *sqlitestore.VisibilityUnIgnoreStore {
	t.Helper()
	return sqlitestore.NewVisibilityUnIgnoreStore(openMigratedTestDB(t, "vis_test.db"))
}

// ── LoadUnIgnore with real store ──────────────────────────────────────────────

func TestVisibilityAdapter_LoadUnIgnore_EmptyPatterns_Zero(t *testing.T) {
	t.Parallel()
	store := buildVisibilityStore(t)
	visReg := visibility.NewRegistry()
	reg := buildTestRegistry(t, "ccu-01")

	a := newVisibilityAdapter(visReg, store, reg)
	count, parseErrors, err := a.LoadUnIgnore("ccu-01", nil)
	if err != nil {
		t.Fatalf("LoadUnIgnore: %v", err)
	}
	if len(parseErrors) != 0 {
		t.Errorf("expected no parse errors, got %v", parseErrors)
	}
	// Empty store → no devices in model registry → 0 affected.
	if count != 0 {
		t.Errorf("expected 0 affected devices, got %d", count)
	}
}

func TestVisibilityAdapter_LoadUnIgnore_WithPatterns_Succeeds(t *testing.T) {
	t.Parallel()
	store := buildVisibilityStore(t)
	visReg := visibility.NewRegistry()
	reg := buildTestRegistry(t, "ccu-01")

	// Seed some patterns for ccu-01.
	ctx := context.Background()
	if err := store.Replace(ctx, "ccu-01", []string{"ACTIVE", "LOWBAT"}, "test"); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	a := newVisibilityAdapter(visReg, store, reg)
	count, parseErrors, err := a.LoadUnIgnore("ccu-01", nil)
	if err != nil {
		t.Fatalf("LoadUnIgnore: %v", err)
	}
	if len(parseErrors) != 0 {
		t.Errorf("expected no parse errors, got %v", parseErrors)
	}
	// No devices in test model registry → 0 devices touched.
	if count != 0 {
		t.Errorf("expected 0 affected devices, got %d", count)
	}
}

func TestVisibilityAdapter_LoadUnIgnore_UnknownCentral_Errors(t *testing.T) {
	t.Parallel()
	store := buildVisibilityStore(t)
	visReg := visibility.NewRegistry()
	reg := buildTestRegistry(t, "ccu-01")

	a := newVisibilityAdapter(visReg, store, reg)
	// "ccu-99" is not in the central registry → must error.
	_, _, err := a.LoadUnIgnore("ccu-99", nil)
	if err == nil {
		t.Fatal("expected error for unknown central, got nil")
	}
}

// ── wireVisibilityUnIgnoreStore ───────────────────────────────────────────────

func TestWireVisibilityUnIgnoreStore_NilConfig_ReturnsNil(t *testing.T) {
	t.Parallel()
	got := wireVisibilityUnIgnoreStore(nil, slog.Default())
	if got != nil {
		t.Errorf("expected nil for nil config, got %v", got)
	}
}

func TestWireVisibilityUnIgnoreStore_ValidDataDir_ReturnsStore(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	logger := slog.New(slog.DiscardHandler)
	gooseMigrateMu.Lock()
	got := wireVisibilityUnIgnoreStore(cfg, logger)
	gooseMigrateMu.Unlock()
	if got == nil {
		t.Fatal("expected non-nil store for valid data dir")
	}
	t.Cleanup(func() { _ = got.Close() })
}

func TestWireVisibilityUnIgnoreStore_EmptyDataDir_ReturnsNilOrStore(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = ""
	logger := slog.New(slog.DiscardHandler)
	// ./var likely doesn't exist → returns nil; either way must not panic.
	got := wireVisibilityUnIgnoreStore(cfg, logger)
	_ = got
}

// ── applyVisibilityUnIgnore ───────────────────────────────────────────────────

func TestApplyVisibilityUnIgnore_WithPatterns_ReturnsOne(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01"}}
	store := buildVisibilityStore(t)
	visReg := visibility.NewRegistry()
	reg := buildTestRegistry(t, "ccu-01")
	logger := slog.New(slog.DiscardHandler)

	// Pre-seed patterns for ccu-01.
	ctx := context.Background()
	if err := store.Replace(ctx, "ccu-01", []string{"ACTIVE"}, "test"); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	n := applyVisibilityUnIgnore(ctx, cfg, reg, store, visReg, logger)
	// One central had patterns → n=1.
	if n != 1 {
		t.Errorf("expected 1 central with patterns, got %d", n)
	}
}

// TestApplyVisibilityUnIgnore_ReadFailureAfterSuccess_KeepsPreviousPatterns
// is the regression guard for a transient per-central read failure wiping
// every already-applied un-ignore pattern: a prior successful apply loads
// "ACTIVE" into the shared registry, then the store is closed to force the
// next apply's read to fail. Before the fix, a read failure was treated as
// "this central has no patterns" and the (now empty) union was still loaded
// into the registry unconditionally, un-applying every pattern that is in
// fact still persisted. The fix aborts the whole apply on any read failure
// and keeps the registry's previous contents.
func TestApplyVisibilityUnIgnore_ReadFailureAfterSuccess_KeepsPreviousPatterns(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01"}}
	db := openMigratedTestDB(t, "vis_apply_read_failure.db")
	store := sqlitestore.NewVisibilityUnIgnoreStore(db)
	visReg := visibility.NewRegistry()
	reg := buildTestRegistry(t, "ccu-01")
	logger := slog.New(slog.DiscardHandler)

	ctx := context.Background()
	if err := store.Replace(ctx, "ccu-01", []string{"ACTIVE"}, "test"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if n := applyVisibilityUnIgnore(ctx, cfg, reg, store, visReg, logger); n != 1 {
		t.Fatalf("initial apply: expected 1 central with patterns, got %d", n)
	}
	if entries := visReg.Parameter().UnIgnoreEntries(); len(entries) == 0 {
		t.Fatalf("initial apply: expected the registry to carry the un-ignore pattern, got none")
	}

	// Force every subsequent Patterns() read to fail.
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	applyVisibilityUnIgnore(ctx, cfg, reg, store, visReg, logger)

	entries := visReg.Parameter().UnIgnoreEntries()
	if len(entries) == 0 {
		t.Error("a read failure wiped the registry's previously-applied un-ignore patterns; want them kept")
	}
}

func TestApplyVisibilityUnIgnore_Seed_FromConfigOnFirstStart(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Centrals = []config.CentralConfig{{
		Name: "ccu-01",
		Visibility: config.VisibilityConfig{
			UnIgnore: []string{"LOWBAT", "UNREACH"},
		},
	}}
	store := buildVisibilityStore(t)
	visReg := visibility.NewRegistry()
	reg := buildTestRegistry(t, "ccu-01")
	logger := slog.New(slog.DiscardHandler)

	// applyVisibilityUnIgnore should seed from config since store is empty.
	ctx := context.Background()
	n := applyVisibilityUnIgnore(ctx, cfg, reg, store, visReg, logger)
	// Seeds + applies → 1 central.
	if n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}
