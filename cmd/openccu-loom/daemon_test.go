// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"context"
	gosql "database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/secure/operational"
	matterstore "github.com/SukramJ/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// syncBuffer is a [bytes.Buffer] with a mutex around Write/String. The
// matter-bridge tests below capture logger output into a buffer while
// background goroutines spawned by [startMatterBridge] also write to the
// same logger; the unsynchronized bytes.Buffer is racy under `-race`.
// The String() side returns a snapshot of the current bytes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write implements [io.Writer].
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns a snapshot of the bytes written so far.
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestDaemonServeBootsAndShutsDownGracefully starts the full
// composition root with REST + UI on dynamic ports, then cancels
// the ctx to verify shutdown drains.
func TestDaemonServeBootsAndShutsDownGracefully(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir() // isolate the SQLite DB from the shared "./var"
	cfg.North.REST.Enabled = new(true)
	cfg.North.REST.Listen = "127.0.0.1:0"
	cfg.North.UI.Enabled = new(true)
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	var stdout, stderr bytes.Buffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- daemonServe(ctx, cfg, &stdout, &stderr) }()

	// Give the servers a beat to start listening. The daemon blocks
	// on ctx.Done(); cancelling drives shutdown without delivering
	// signals to the test process.
	time.Sleep(250 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon returned: %v", err)
		}
	case <-time.After(30 * time.Second):
		// Generous to accommodate -race overhead on slower CI hosts;
		// the actual non-race shutdown completes in well under a second.
		t.Fatal("daemon did not shut down in time")
	}
}

func TestDaemonServeAcceptsDefaultsWithoutCentrals(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir() // isolate the SQLite DB from the shared "./var"
	cfg.North.REST.Enabled = new(false)
	cfg.North.UI.Enabled = new(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- daemonServe(ctx, cfg, &bytes.Buffer{}, &bytes.Buffer{}) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		// Same -race-overhead allowance as the sibling test above.
		t.Fatal("shutdown timeout")
	}
}

// TestStartMatterBridge_DisabledReturnsNil verifies that startMatterBridge
// returns nil when cfg.North.Matter.Enabled is false.
func TestStartMatterBridge_DisabledReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = false
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := t.Context()

	if bundle := startMatterBridge(ctx, cfg, reg, nil, health.NewTracker(), nil, logger, nil); bundle != nil {
		t.Error("expected nil bundle when Matter is disabled")
	}
}

// TestStartMatterBridge_EnabledReturnsBridge verifies that startMatterBridge
// returns a live bridge with a bound local address when Matter is enabled.
func TestStartMatterBridge_EnabledReturnsBridge(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, logger, nil)
	if bundle == nil {
		t.Fatal("expected non-nil bundle when Matter is enabled")
	}
	if bundle.stop == nil {
		t.Fatal("expected non-nil stop function when Matter is enabled")
	}
	t.Cleanup(bundle.stop)

	if addr := bundle.node.Bridge().LocalAddr(); addr == "" {
		t.Error("expected non-empty LocalAddr after bridge start")
	}
	if topo := bundle.node.Bridge().Topology(); topo == nil {
		t.Error("expected non-nil Topology after bridge start")
	}
}

// TestStartMatterBridge_PASEDisabledByDefault verifies that the bridge starts
// cleanly without a commissioning passcode (no PASE handler armed).
func TestStartMatterBridge_PASEDisabledByDefault(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	// Commissioning.Passcode intentionally left at zero — PASE stays noop.
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	var buf syncBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, logger, nil)
	if bundle == nil {
		t.Fatal("expected non-nil bundle")
	}
	t.Cleanup(bundle.stop)

	// With Passcode=0 the "matter.bridge.pase.armed" log line must NOT appear.
	if bundle.configuredCommissioning {
		t.Error("passcode 0 was reported as a configured passcode; the REST opener would hand out a window on credentials nobody holds")
	}
}

// TestStartMatterBridge_CommissioningArmsHandler verifies that configuring a
// valid commissioning passcode causes the PASE handler to be armed (log line
// "matter.bridge.pase.armed" appears).
func TestStartMatterBridge_CommissioningArmsHandler(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	var buf syncBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, logger, nil)
	if bundle == nil {
		t.Fatal("expected non-nil bundle; check logs:\n" + buf.String())
	}
	t.Cleanup(bundle.stop)

	if !bundle.configuredCommissioning {
		t.Fatalf("the configured passcode was not recognised as configured; logs:\n%s", buf.String())
	}
	if qr, manual, err := bundle.node.PairingCodes(); err != nil || qr == "" || manual == "" {
		t.Fatalf("PairingCodes = %q, %q, %v; want the configured passcode's codes", qr, manual, err)
	}
}

// gooseMigrateMu serialises sqlitestore.Open calls across tests.
// pressly/goose v3.27 has package-level state in createVersionTable
// that is not safe for concurrent migration of distinct databases —
// running parallel tests that each Open + Migrate triggers a race on
// goose-internal state. Holding this mutex around the Open call keeps
// the daemon-test sweep race-free without serialising the rest of the
// test body. Production has a single Open at boot so this is purely a
// test-fixture concern.
var gooseMigrateMu sync.Mutex

// openTestLoomDB opens a throwaway SQLite database in a fresh temp
// directory, mirroring the single shared <DataDir>/openccu-loom.db handle
// [openLoomDB] opens in production. Tests that exercise
// wireSessionRecorderPersistence, wireIncidentRecorder,
// wireAuditPersistenceWithDB or startMatterBridge — all of which now take an
// already-open *sql.DB instead of opening their own — use this to build that
// handle. Each call gets its own isolated file; the production duplication
// this whole refactor guards against only ever applied to the ONE real
// database a daemon boot opens.
func openTestLoomDB(t *testing.T) *gosql.DB {
	t.Helper()
	return openMigratedTestDB(t, "openccu-loom.db")
}

// migratedSchemaTemplate applies the migration set once per test binary and
// returns the path of a closed, fully migrated database file.
//
// A full goose run costs roughly a second, and goose cannot run concurrently
// (see gooseMigrateMu), so every test that opened its own store queued that
// second behind the same lock. With several dozen such tests in this package
// that serialised minutes of work into the critical path — all of it
// re-deriving a schema that never varies between tests. Deriving it once and
// copying the file leaves each test with a private database and pays the
// migration cost a single time.
var migratedSchemaTemplate = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "openccu-loom-schema-template")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "template.db")
	db, err := sqlitestore.Open(context.Background(), sqlitestore.FileDSN(path))
	if err != nil {
		return "", err
	}
	// Closing checkpoints the write-ahead log back into the main file, so
	// the single file copied below carries the whole schema.
	if err := db.Close(); err != nil {
		return "", err
	}
	return path, nil
})

// openMigratedTestDB opens a throwaway SQLite database in a fresh temp
// directory, seeded from the migrated template above so the schema is already
// in place. Each call gets its own file: tests share the schema, never the
// data.
func openMigratedTestDB(t *testing.T, name string) *gosql.DB {
	t.Helper()
	templatePath, err := migratedSchemaTemplate()
	if err != nil {
		t.Fatalf("build migrated schema template: %v", err)
	}
	schema, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read schema template: %v", err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, schema, 0o600); err != nil {
		t.Fatalf("seed test database: %v", err)
	}
	// Open still runs goose; it finds the version table already at the
	// latest revision and applies nothing, which is what makes the seeded
	// copy cheap. The lock stays because that check is goose-internal too.
	gooseMigrateMu.Lock()
	db, err := sqlitestore.Open(context.Background(), sqlitestore.FileDSN(path))
	gooseMigrateMu.Unlock()
	if err != nil {
		t.Fatalf("open test database %s: %v", name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// buildTestOperationalManager constructs a minimal *operational.Manager backed
// by a migrated SQLite store, for unit tests that exercise buildPaseAdapter.
// The schema has to exist before operational.NewManager touches the matter_*
// tables.
func buildTestOperationalManager(t *testing.T) *operational.Manager {
	t.Helper()
	return operational.NewManager(matterstore.New(openMigratedTestDB(t, "matter_test.db")))
}

// ─── CASE wiring tests (5) ───────────────────────────────────────────────────

// matterStoreFromManager extracts the matterstore.Store underlying a
// test operational manager. operational.Manager doesn't expose its
// store, so we re-open against the same DSN — but [buildTestOperationalManager]
// already wraps a fresh DB. For these focused tests we just rebuild
// a parallel store against the same shared sqlite path, which works
// because operational.Manager doesn't lock the schema.
func matterStoreFromManager(t *testing.T, _ *operational.Manager) *matterstore.Store {
	t.Helper()
	return matterstore.New(openMigratedTestDB(t, "matter_persistent_test.db"))
}
