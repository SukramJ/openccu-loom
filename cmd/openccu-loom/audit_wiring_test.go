// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// ── buildBackupAdapter ────────────────────────────────────────────────────────

func TestBuildBackupAdapter_NilConfig_ReturnsAdapter(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := buildBackupAdapter(nil, reg, slog.Default())
	if a == nil {
		t.Fatal("expected non-nil adapter for nil config")
	}
}

func TestBuildBackupAdapter_ValidDataDir_ReturnsAdapter(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	reg := central.NewRegistry()
	logger := slog.New(slog.DiscardHandler)
	a := buildBackupAdapter(cfg, reg, logger)
	if a == nil {
		t.Fatal("expected non-nil adapter for valid data dir")
	}
}

func TestBuildBackupAdapter_EmptyDataDir_FallsBackToVar(t *testing.T) {
	t.Parallel()
	// When DataDir is empty the function uses ./var — this may fail to
	// create the backup dir if ./var doesn't exist, but must not panic
	// and must return a non-nil (degraded) adapter.
	cfg := config.Default()
	cfg.DataDir = ""
	reg := central.NewRegistry()
	logger := slog.New(slog.DiscardHandler)
	a := buildBackupAdapter(cfg, reg, logger)
	if a == nil {
		t.Fatal("expected non-nil adapter even when backup dir creation fails")
	}
}

// ── wireSessionRecorderPersistence ───────────────────────────────────────────
//
// wireSessionRecorderPersistence now receives the shared *sql.DB handle
// (opened once by openLoomDB in the composition root) instead of opening its
// own — see openLoomDB_test.go / daemon_boot_test.go for the open-path tests.

func TestWireSessionRecorderPersistence_NilDB_ReturnsNoop(t *testing.T) {
	t.Parallel()
	closer := wireSessionRecorderPersistence(nil, central.NewRegistry(), slog.Default())
	// Must not panic; nil db → early return noop func.
	if closer == nil {
		t.Fatal("expected non-nil closer")
	}
	closer() // must not panic
}

func TestWireSessionRecorderPersistence_NilRegistry_ReturnsNoop(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)
	closer := wireSessionRecorderPersistence(db, nil, slog.Default())
	if closer == nil {
		t.Fatal("expected non-nil closer")
	}
	closer()
}

func TestWireSessionRecorderPersistence_ValidDB_ReturnsCloser(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)
	reg := central.NewRegistry()
	logger := slog.New(slog.DiscardHandler)
	closer := wireSessionRecorderPersistence(db, reg, logger)
	if closer == nil {
		t.Fatal("expected non-nil closer")
	}
	closer() // must not panic

	// The shared handle is owned by the caller that opened it — the
	// wiring's own teardown must not have closed it.
	if err := db.Ping(); err != nil {
		t.Fatalf("shared db closed by wireSessionRecorderPersistence teardown: %v", err)
	}
}

// ── wireIncidentRecorder ──────────────────────────────────────────────────────

func TestWireIncidentRecorder_NilDB_IsNoop(t *testing.T) {
	t.Parallel()
	// Must not panic.
	_, closer := wireIncidentRecorder(nil, central.NewRegistry(), slog.Default())
	t.Cleanup(closer)
}

func TestWireIncidentRecorder_NilRegistry_IsNoop(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)
	_, closer := wireIncidentRecorder(db, nil, slog.Default())
	t.Cleanup(closer)
}

func TestWireIncidentRecorder_ValidDB_DoesNotPanic(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)
	reg := central.NewRegistry()
	logger := slog.New(slog.DiscardHandler)
	store, closer := wireIncidentRecorder(db, reg, logger)
	t.Cleanup(closer)
	if store == nil {
		t.Fatal("expected non-nil incident store for a valid shared db")
	}

	closer()
	// The shared handle is owned by the caller that opened it — the
	// wiring's own teardown must not have closed it.
	if err := db.Ping(); err != nil {
		t.Fatalf("shared db closed by wireIncidentRecorder teardown: %v", err)
	}
}

// ── wireAuditPersistenceWithDB ────────────────────────────────────────────────

func TestWireAuditPersistenceWithDB_NilDB_ReturnsBuf(t *testing.T) {
	t.Parallel()
	buf := audit.NewBuffer(16)
	got, stats := wireAuditPersistenceWithDB(nil, buf, slog.Default())
	// nil db → returns the buf unchanged.
	if got == nil {
		t.Fatal("expected non-nil recorder")
	}
	if stats != nil {
		t.Fatal("expected nil durable-sink stats when db is nil")
	}
}

func TestWireAuditPersistenceWithDB_NilBuf_ReturnsBuf(t *testing.T) {
	t.Parallel()
	got, _ := wireAuditPersistenceWithDB(nil, nil, slog.Default())
	// nil buf → returns nil (the function returns buf which is nil).
	_ = got
}

func TestWireAuditPersistenceWithDB_ValidDB_ReturnsPersistedRecorder(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)
	buf := audit.NewBuffer(16)
	logger := slog.New(slog.DiscardHandler)
	got, _ := wireAuditPersistenceWithDB(db, buf, logger)
	if got == nil {
		t.Fatal("expected non-nil recorder")
	}
	// The result must accept a Record call without panicking.
	got.Record(audit.Entry{User: "test", Parameter: "key"})
}
