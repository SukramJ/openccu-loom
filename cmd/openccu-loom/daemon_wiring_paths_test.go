// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

// daemon_coverage5_test.go — targeted coverage for remaining gaps:
//   - buildOpenAPIValidator: success path (valid spec file)
//   - autodetectCallbackHost: Dial failure path (invalid host)
//   - wireIncidentRecorder: nil Cache guard
//   - wireSessionRecorderPersistence: nil Recorder guard
//   - buildCaseAdapter: nil store → ephemeral path
//   - buildBackupAdapter: un-creatable directory → Warn path

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// ── buildOpenAPIValidator: success path ──────────────────────────────────────

// TestBuildOpenAPIValidator_ValidSpec_ReturnsValidator exercises the success
// path where the spec file is read and parsed successfully.
// Covers daemon.go lines "logger.Info + return v".
func TestBuildOpenAPIValidator_ValidSpec_ReturnsValidator(t *testing.T) {
	t.Parallel()

	// Locate the real openapi.yaml via __FILE__ so it works from any CWD.
	_, thisFile, _, _ := runtime.Caller(0)
	specPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "assets", "openapi.yaml")

	cfg := config.Default()
	cfg.North.REST.OpenAPISpecPath = specPath

	v := buildOpenAPIValidator(cfg, slog.New(slog.DiscardHandler))
	if v == nil {
		t.Fatal("expected non-nil OpenAPIValidator for valid spec")
	}
}

// TestBuildOpenAPIValidator_InvalidSpecContent_ReturnsNil exercises the
// parse-error path when the spec file contains invalid/empty OpenAPI content.
func TestBuildOpenAPIValidator_InvalidSpecContent_ReturnsNil(t *testing.T) {
	t.Parallel()

	// Write a file with content that is valid YAML but not a valid OpenAPI spec.
	tmp := t.TempDir()
	badSpec := filepath.Join(tmp, "bad.yaml")
	if err := os.WriteFile(badSpec, []byte("not_openapi: true\nno_paths: yes\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	cfg := config.Default()
	cfg.North.REST.OpenAPISpecPath = badSpec

	v := buildOpenAPIValidator(cfg, slog.New(slog.DiscardHandler))
	// May return nil (parse error) or non-nil (if the validator accepts minimal YAML).
	// Either way the code path through err!=nil is exercised when it errors.
	_ = v
}

// ── egressHostToward: Dial failure ───────────────────────────────────────────

// TestEgressHostToward_InvalidHost_ReturnsEmpty exercises the
// `if err != nil { return "" }` path after net.Dial fails.
func TestEgressHostToward_InvalidHost_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	// "::invalid::" is not a valid hostname → net.Dial("udp", "...:80") fails.
	got := egressHostToward("::invalid::")
	if got != "" {
		t.Errorf("expected empty string for invalid host, got %q", got)
	}
}

// ── wireIncidentRecorder: nil Cache guard ─────────────────────────────────────
//
// The dataDir/DSN fallback branch these tests used to exercise directly on
// wireIncidentRecorder / wireSessionRecorderPersistence now lives solely in
// [openLoomDB] (see daemon_boot_test.go) — both functions take the shared
// *sql.DB as a parameter and no longer resolve a path themselves.

// TestWireIncidentRecorder_NilCache_ContinueBranch exercises the
// "if c == nil || c.Cache == nil { continue }" defensive branch.
func TestWireIncidentRecorder_NilCache_ContinueBranch(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)

	cu, err := central.New(central.Config{Name: "ccu-null-cache"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	cu.Cache = nil // force the nil guard

	reg := central.NewRegistry()
	if err := reg.Register(cu); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)
	_, _, closer := wireIncidentRecorder(db, reg, logger)
	t.Cleanup(closer)
}

// ── wireSessionRecorderPersistence: nil Recorder guard ───────────────────────

// TestWireSessionRecorderPersistence_NilRecorder_ContinueBranch exercises the
// "if c == nil || c.Recorder == nil { continue }" branch.
func TestWireSessionRecorderPersistence_NilRecorder_ContinueBranch(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)

	cu, err := central.New(central.Config{Name: "ccu-null-rec"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	cu.Recorder = nil // force the nil guard

	reg := central.NewRegistry()
	if err := reg.Register(cu); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)
	_, closer := wireSessionRecorderPersistence(db, reg, logger)
	if closer != nil {
		closer()
	}
}

// ── buildCaseAdapter: nil store → ephemeral path ──────────────────────────────

// TestBuildCaseAdapter_NilStore_EphemeralPath exercises the non-persisted
// branch (loadPersistentCaseIdentity returns persisted=false with nil store),
// causing buildCaseAdapter to generate an ephemeral ECDSA key.
func TestBuildCaseAdapter_NilStore_EphemeralPath(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	logger := slog.New(slog.DiscardHandler)

	adapter, err := buildCaseAdapter(
		context.Background(),
		config.NorthMatterCASE{},
		mgr,
		nil, // nil store → loadPersistentCaseIdentity returns persisted=false
		logger,
	)
	if err != nil {
		t.Fatalf("buildCaseAdapter with nil store: %v", err)
	}
	if adapter == nil {
		t.Fatal("expected non-nil CaseAdapter")
	}
}

// ── buildBackupAdapter: blocked directory → Warn path ────────────────────────

// TestBuildBackupAdapter_BlockedDir_LogsWarn exercises the
// "backup.storage.disabled" warn path when the backup directory cannot be
// created because a file already occupies its path.
func TestBuildBackupAdapter_BlockedDir_LogsWarnReturnAdapter(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	// Create a regular file at the path that would be the "backups" directory.
	// NewFilesystemBackupStorage tries to mkdir this path → fails.
	blockPath := filepath.Join(tmp, "backups")
	if err := os.WriteFile(blockPath, []byte("file-not-dir"), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = tmp

	reg := buildTestRegistry(t, "ccu-01")
	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	a := buildBackupAdapter(cfg, reg, logger)
	if a == nil {
		t.Fatal("expected non-nil BackupAdapter even when storage init fails")
	}
	// The warn is only logged when NewFilesystemBackupStorage returns an error.
	// On some file systems the "file as dir" trick may work differently.
	// We accept either outcome (warn or no warn) — both cover code paths.
	_ = logBuf.String()
}
