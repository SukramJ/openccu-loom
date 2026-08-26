// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/wiring"
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

// TestBuildBackupAdapter_ConfiguredBackupDir_Wins verifies that an explicit
// cfg.Backup.Dir is used verbatim instead of the <DataDir>/backups default —
// the CCU add-on points it at the CCU's own backup target so the daemon's
// data directory is never conflated with the downloaded archives.
func TestBuildBackupAdapter_ConfiguredBackupDir_Wins(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	backupDir := filepath.Join(t.TempDir(), "custom-backups")
	cfg.Backup.Dir = backupDir
	reg := central.NewRegistry()
	logger := slog.New(slog.DiscardHandler)

	a := buildBackupAdapter(cfg, reg, logger)
	if a == nil {
		t.Fatal("expected non-nil adapter")
	}
	if fi, err := os.Stat(backupDir); err != nil || !fi.IsDir() {
		t.Fatalf("configured Backup.Dir %q was not created: %v", backupDir, err)
	}
	defaultDir := filepath.Join(cfg.DataDir, ccuBackupsDirName)
	if _, err := os.Stat(defaultDir); err == nil {
		t.Errorf("default backup dir %q was created even though Backup.Dir was configured", defaultDir)
	}
}

// TestBuildBackupAdapter_WhitespaceBackupDir_FallsBackToDataDir verifies that
// a whitespace-only Backup.Dir is treated the same as unset, not as a literal
// (invalid) directory name.
func TestBuildBackupAdapter_WhitespaceBackupDir_FallsBackToDataDir(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.Backup.Dir = "   "
	reg := central.NewRegistry()
	logger := slog.New(slog.DiscardHandler)

	a := buildBackupAdapter(cfg, reg, logger)
	if a == nil {
		t.Fatal("expected non-nil adapter")
	}
	wantDir := filepath.Join(cfg.DataDir, ccuBackupsDirName)
	if fi, err := os.Stat(wantDir); err != nil || !fi.IsDir() {
		t.Fatalf("fallback dir %q was not created for a whitespace-only Backup.Dir: %v", wantDir, err)
	}
}

// TestBuildBackupAdapter_EmptyBackupDir_FallsBackToDataDir verifies the
// documented default: an empty Backup.Dir resolves to <DataDir>/backups, and
// that resolved directory actually exists on disk afterwards.
func TestBuildBackupAdapter_EmptyBackupDir_FallsBackToDataDir(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.Backup.Dir = ""
	reg := central.NewRegistry()
	logger := slog.New(slog.DiscardHandler)

	a := buildBackupAdapter(cfg, reg, logger)
	if a == nil {
		t.Fatal("expected non-nil adapter")
	}
	wantDir := filepath.Join(cfg.DataDir, ccuBackupsDirName)
	if fi, err := os.Stat(wantDir); err != nil || !fi.IsDir() {
		t.Fatalf("fallback dir %q was not created for an empty Backup.Dir: %v", wantDir, err)
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
	got, stats, _ := wireAuditPersistenceWithDB(nil, buf, slog.Default())
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
	got, _, _ := wireAuditPersistenceWithDB(nil, nil, slog.Default())
	// nil buf → returns nil (the function returns buf which is nil).
	_ = got
}

func TestWireAuditPersistenceWithDB_ValidDB_ReturnsPersistedRecorder(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)
	buf := audit.NewBuffer(16)
	logger := slog.New(slog.DiscardHandler)
	got, _, stop := wireAuditPersistenceWithDB(db, buf, logger)
	t.Cleanup(stop)
	if got == nil {
		t.Fatal("expected non-nil recorder")
	}
	// The result must accept a Record call without panicking.
	got.Record(audit.Entry{User: "test", Parameter: "key"})
}

// TestAuditOverlayTeardownPersistsQueuedEntries pins the shutdown order the
// composition root owns: the durable audit sink is joined — draining its
// queue — BEFORE the shared database handle it writes through is closed.
//
// The sink persists off a 256-entry channel on its own goroutine, so a burst
// of writes followed by an immediate SIGTERM leaves entries queued. With the
// stop closure discarded, teardown closed the handle under the still-running
// worker: every queued mutation was lost with nothing but a warn line, and the
// operator's audit trail simply ends mid-burst — the one guarantee an
// append-only trail exists to give.
//
// The assertion is on the persisted rows, not on the closure having run.
func TestAuditOverlayTeardownPersistsQueuedEntries(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dataDir
	logger := slog.New(slog.DiscardHandler)

	ov, teardown := wireAuditOverlay(context.Background(), wiring.NewManifest(), cfg, logger)
	if ov.db == nil {
		t.Fatal("the audit overlay opened no database")
	}

	const entries = 200
	for i := range entries {
		ov.rec.Record(audit.Entry{
			User:      "burst",
			Action:    audit.ActionParamsetWrite,
			Parameter: fmt.Sprintf("BURST_%03d", i),
		})
	}
	// The burst is still draining; this is the shutdown that must wait for it.
	teardown()

	db, err := sql.Open("sqlite", sqlitestore.FileDSN(filepath.Join(dataDir, "openccu-loom.db")))
	if err != nil {
		t.Fatalf("reopen audit db: %v", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := sqlitestore.NewAuditStore(db).Query(context.Background(), audit.Query{Limit: entries * 2})
	if err != nil {
		t.Fatalf("query audit rows: %v", err)
	}
	persisted := 0
	for _, e := range rows {
		if e.User == "burst" {
			persisted++
		}
	}
	if persisted != entries {
		t.Errorf("audit rows surviving shutdown: got %d of %d — the queue was dropped when the handle closed",
			persisted, entries)
	}
}
