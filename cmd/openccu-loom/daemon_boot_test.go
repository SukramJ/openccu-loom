// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// ── openLoomDB ────────────────────────────────────────────────────────────────
//
// openLoomDB is the single place that opens the shared
// <DataDir>/openccu-loom.db handle threaded into wireAuditPersistenceWithDB,
// wireSessionRecorderPersistence, wireIncidentRecorder and startMatterBridge.
// These tests cover the cases that, before the shared-handle refactor, were
// exercised independently at each of those four call sites.

func TestOpenLoomDB_NilConfig_ReturnsNil(t *testing.T) {
	t.Parallel()
	got, err := openLoomDB(nil, slog.New(slog.DiscardHandler))
	if got != nil {
		t.Fatal("expected nil db for nil config")
	}
	if err != nil {
		t.Errorf("expected nil error for nil config (not a genuine open failure), got %v", err)
	}
}

func TestOpenLoomDB_ValidDataDir_ReturnsOpenDB(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	logger := slog.New(slog.DiscardHandler)

	gooseMigrateMu.Lock()
	db, err := openLoomDB(cfg, logger)
	gooseMigrateMu.Unlock()
	if db == nil {
		t.Fatal("expected non-nil db for a valid data dir")
	}
	if err != nil {
		t.Errorf("expected nil error alongside a non-nil db, got %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Ping(); err != nil {
		t.Fatalf("db.Ping: %v", err)
	}
}

// TestOpenLoomDB_BlockedPath_ReturnsNil exercises the warn-and-degrade path
// when <DataDir>/openccu-loom.db cannot be created because a path component
// is a regular file, not a directory. Every downstream wireX function
// (wireAuditPersistenceWithDB, wireSessionRecorderPersistence,
// wireIncidentRecorder, startMatterBridge) must accept the resulting nil
// handle and degrade gracefully rather than panicking.
func TestOpenLoomDB_BlockedPath_ReturnsNil(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	blockFile := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(blockFile, []byte("block"), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = blockFile // DataDir=file → file/openccu-loom.db can't be created

	gooseMigrateMu.Lock()
	db, err := openLoomDB(cfg, slog.New(slog.DiscardHandler))
	gooseMigrateMu.Unlock()
	if db != nil {
		t.Fatal("expected nil db when the data dir path is blocked by a file")
	}
	if err == nil {
		t.Error("expected a non-nil error alongside the nil db, so a caller can surface it on /health")
	}
}

// TestOpenLoomDB_EmptyDataDir_DoesNotPanic exercises the `dataDir = "./var"`
// fallback branch. Whether "./var" is writable in the test sandbox is
// environment-dependent; either outcome is acceptable as long as it doesn't
// panic, and a non-nil handle is cleaned up.
func TestOpenLoomDB_EmptyDataDir_DoesNotPanic(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = "" // triggers the `dataDir = "./var"` branch

	gooseMigrateMu.Lock()
	db, _ := openLoomDB(cfg, slog.New(slog.DiscardHandler))
	gooseMigrateMu.Unlock()
	if db != nil {
		t.Cleanup(func() { _ = db.Close() })
	}
}
