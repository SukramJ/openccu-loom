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

func TestWireSessionRecorderPersistence_NilConfig_ReturnsNoop(t *testing.T) {
	t.Parallel()
	closer := wireSessionRecorderPersistence(nil, central.NewRegistry(), slog.Default())
	// Must not panic; nil config → early return noop func.
	if closer == nil {
		t.Fatal("expected non-nil closer")
	}
	closer() // must not panic
}

func TestWireSessionRecorderPersistence_NilRegistry_ReturnsNoop(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	closer := wireSessionRecorderPersistence(cfg, nil, slog.Default())
	if closer == nil {
		t.Fatal("expected non-nil closer")
	}
	closer()
}

func TestWireSessionRecorderPersistence_ValidDir_ReturnsCloser(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	reg := central.NewRegistry()
	logger := slog.New(slog.DiscardHandler)
	gooseMigrateMu.Lock()
	closer := wireSessionRecorderPersistence(cfg, reg, logger)
	gooseMigrateMu.Unlock()
	if closer == nil {
		t.Fatal("expected non-nil closer")
	}
	closer() // must not panic
}

// ── wireIncidentRecorder ──────────────────────────────────────────────────────

func TestWireIncidentRecorder_NilConfig_IsNoop(t *testing.T) {
	t.Parallel()
	// Must not panic.
	_, closer := wireIncidentRecorder(nil, central.NewRegistry(), slog.Default())
	t.Cleanup(closer)
}

func TestWireIncidentRecorder_NilRegistry_IsNoop(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	_, closer := wireIncidentRecorder(cfg, nil, slog.Default())
	t.Cleanup(closer)
}

func TestWireIncidentRecorder_ValidConfig_DoesNotPanic(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	reg := central.NewRegistry()
	logger := slog.New(slog.DiscardHandler)
	gooseMigrateMu.Lock()
	_, closer := wireIncidentRecorder(cfg, reg, logger)
	gooseMigrateMu.Unlock()
	t.Cleanup(closer)
}

// ── wireAuditPersistence ──────────────────────────────────────────────────────

func TestWireAuditPersistence_NilConfig_ReturnsBuf(t *testing.T) {
	t.Parallel()
	buf := audit.NewBuffer(16)
	got := wireAuditPersistence(nil, buf, slog.Default())
	// nil config → returns the buf unchanged.
	if got == nil {
		t.Fatal("expected non-nil recorder")
	}
}

func TestWireAuditPersistence_NilBuf_ReturnsBuf(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	got := wireAuditPersistence(cfg, nil, slog.Default())
	// nil buf → returns nil (the function returns buf which is nil).
	_ = got
}

func TestWireAuditPersistence_ValidConfig_ReturnsPersistedRecorder(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	buf := audit.NewBuffer(16)
	logger := slog.New(slog.DiscardHandler)
	gooseMigrateMu.Lock()
	got, db, _ := wireAuditPersistenceWithDB(cfg, buf, logger)
	gooseMigrateMu.Unlock()
	if db != nil {
		t.Cleanup(func() { _ = db.Close() })
	}
	if got == nil {
		t.Fatal("expected non-nil recorder")
	}
	// The result must accept a Record call without panicking.
	got.Record(audit.Entry{User: "test", Parameter: "key"})
}

func TestWireAuditPersistence_EmptyDataDir_FallsBack(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = ""
	buf := audit.NewBuffer(16)
	logger := slog.New(slog.DiscardHandler)
	got := wireAuditPersistence(cfg, buf, logger)
	// ./var likely does not exist in test; degrades to buf.
	if got == nil {
		t.Fatal("expected non-nil fallback")
	}
}
