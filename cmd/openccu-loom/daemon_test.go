// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/operational"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
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
	cfg.North.REST.Enabled = new(true)
	cfg.North.REST.Listen = "127.0.0.1:0"
	cfg.North.UI.Enabled = new(true)
	cfg.North.UI.Listen = "127.0.0.1:0"
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
	case <-time.After(15 * time.Second):
		// Generous to accommodate -race overhead on slower CI hosts;
		// the actual non-race shutdown completes in well under a second.
		t.Fatal("daemon did not shut down in time")
	}
}

func TestDaemonServeAcceptsDefaultsWithoutCentrals(t *testing.T) {
	cfg := config.Default()
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
	case <-time.After(15 * time.Second):
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

	if bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), nil, logger); bundle != nil {
		t.Error("expected nil bundle when Matter is disabled")
	}
}

// TestStartMatterBridge_EnabledReturnsBridge verifies that startMatterBridge
// returns a live bridge with a bound local address when Matter is enabled.
func TestStartMatterBridge_EnabledReturnsBridge(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), nil, logger)
	if bundle == nil {
		t.Fatal("expected non-nil bundle when Matter is enabled")
	}
	if bundle.stop == nil {
		t.Fatal("expected non-nil stop function when Matter is enabled")
	}
	t.Cleanup(bundle.stop)

	if addr := bundle.bridge.LocalAddr(); addr == "" {
		t.Error("expected non-empty LocalAddr after bridge start")
	}
	if topo := bundle.bridge.Topology(); topo == nil {
		t.Error("expected non-nil Topology after bridge start")
	}
}

// TestStartMatterBridge_PASEDisabledByDefault verifies that the bridge starts
// cleanly without a commissioning passcode (no PASE handler armed).
func TestStartMatterBridge_PASEDisabledByDefault(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	// Commissioning.Passcode intentionally left at zero — PASE stays noop.
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	var buf syncBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), nil, logger)
	if bundle == nil {
		t.Fatal("expected non-nil bundle")
	}
	t.Cleanup(bundle.stop)

	// With Passcode=0 the "matter.bridge.pase.armed" log line must NOT appear.
	if logs := buf.String(); containsSubstring(logs, "pase.armed") {
		t.Errorf("unexpected pase.armed log when passcode is 0; logs:\n%s", logs)
	}
}

// TestStartMatterBridge_CommissioningArmsHandler verifies that configuring a
// valid commissioning passcode causes the PASE handler to be armed (log line
// "matter.bridge.pase.armed" appears).
func TestStartMatterBridge_CommissioningArmsHandler(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	var buf syncBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), nil, logger)
	if bundle == nil {
		t.Fatal("expected non-nil bundle; PASE adapter construction may have failed — check logs:\n" + buf.String())
	}
	t.Cleanup(bundle.stop)

	if logs := buf.String(); !containsSubstring(logs, "pase.armed") {
		t.Errorf("expected 'pase.armed' log line; got:\n%s", logs)
	}
}

// TestBuildPaseAdapter_InvalidPasscodeRejects verifies that buildPaseAdapter
// rejects a config whose effective PBKDF parameters are invalid. An explicit
// negative Iterations value bypasses the zero-default path and triggers the
// spake2.PBKDF guard (iterations must be > 0).
func TestBuildPaseAdapter_InvalidPasscodeRejects(t *testing.T) {
	t.Parallel()
	cfg := config.NorthMatterCommissioning{
		Passcode:   20202021,
		Salt:       "openccu-loom-dev0", // 16 bytes — valid
		Iterations: -1,                  // non-zero so the default path is skipped; PBKDF rejects ≤ 0
	}
	var buf syncBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Construct a minimal operational.Manager (nil store is accepted by
	// manager.NewManager for in-memory-only use in tests).
	mgr := buildTestOperationalManager(t)

	_, err := buildPaseAdapter(cfg, mgr, nil, nil, logger)
	if err == nil {
		t.Error("expected error for invalid (iterations=-1) PBKDF config, got nil")
	}
}

// TestBuildPaseAdapter_DefaultsApplied verifies that buildPaseAdapter succeeds
// when Salt and Iterations are zero-valued, applying the dev-salt and
// iterations-1000 defaults internally.
func TestBuildPaseAdapter_DefaultsApplied(t *testing.T) {
	t.Parallel()
	cfg := config.NorthMatterCommissioning{
		Passcode:   20202021,
		Salt:       "", // triggers dev-salt default
		Iterations: 0,  // triggers 1000 default
	}
	var buf syncBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mgr := buildTestOperationalManager(t)

	adapter, err := buildPaseAdapter(cfg, mgr, nil, nil, logger)
	if err != nil {
		t.Fatalf("expected success with defaults applied, got: %v", err)
	}
	if adapter == nil {
		t.Error("expected non-nil PaseAdapter")
	}
}

// containsSubstring is a lightweight helper (avoids strings import at
// package level when "strings" is not otherwise needed in this file).
func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
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

// buildTestOperationalManager constructs a minimal *operational.Manager backed
// by an in-memory SQLite store for use in unit tests that exercise
// buildPaseAdapter. The DB is migrated via sqlitestore.Open so the matter_*
// tables exist before operational.NewManager accesses them.
func buildTestOperationalManager(t *testing.T) *operational.Manager {
	t.Helper()
	ctx := context.Background()
	dsn := "file:" + t.TempDir() + "/matter_test.db?_pragma=journal_mode(WAL)"
	gooseMigrateMu.Lock()
	db, err := sqlitestore.Open(ctx, dsn)
	gooseMigrateMu.Unlock()
	if err != nil {
		t.Fatalf("sqlitestore.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := matterstore.New(db)
	return operational.NewManager(store)
}

// ─── CASE wiring tests (5) ───────────────────────────────────────────────────

// TestStartMatterBridge_CASEArmedByDefault verifies that the CASE
// provider is wired even when cfg.North.Matter.CASE.NodeID is left at
// its zero default. The pre-2026-05 implementation gated the entire
// CASE block on NodeID != 0; with `node_id: 0` in config the bridge
// happily ran PASE + AddNOC, then dropped every Sigma1 with
// `ErrCaseHandlerMissing` at DEBUG level, leaving the commissioner to
// time out (CHIP Error 0x32 in chip-tool, "verbinden..." spinner in
// Apple Home). CASE has to be armed whenever Matter is enabled — the
// identity is filled in by `caseRefresh` on the first AddNOC.
func TestStartMatterBridge_CASEArmedByDefault(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	// CASE.NodeID intentionally left at zero — provider must still arm.
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	var buf syncBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), nil, logger)
	if bundle == nil {
		t.Fatal("expected non-nil bundle")
	}
	t.Cleanup(bundle.stop)

	logs := buf.String()
	if !containsSubstring(logs, "case.armed") {
		t.Errorf("expected 'case.armed' log line when CASE.NodeID=0; logs:\n%s", logs)
	}
	if !containsSubstring(logs, "case.awaiting_addnoc") {
		t.Errorf("expected 'case.awaiting_addnoc' log line when no fabric persisted; logs:\n%s", logs)
	}
}

// TestStartMatterBridge_CASEArmedWhenConfigured verifies that setting
// cfg.North.Matter.CASE.NodeID=42 causes the "case.armed" log line to appear.
func TestStartMatterBridge_CASEArmedWhenConfigured(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.North.Matter.CASE.NodeID = 42
	cfg.North.Matter.CASE.FabricID = 1
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	var buf syncBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), nil, logger)
	if bundle == nil {
		t.Fatal("expected non-nil bundle; CASE adapter construction may have failed — check logs:\n" + buf.String())
	}
	t.Cleanup(bundle.stop)

	if logs := buf.String(); !containsSubstring(logs, "case.armed") {
		t.Errorf("expected 'case.armed' log line; got:\n%s", logs)
	}
}

// TestBuildCaseAdapter_HappyPath verifies that buildCaseAdapter returns a
// non-nil adapter without error for a valid (NodeID=42, FabricID=1) config.
func TestBuildCaseAdapter_HappyPath(t *testing.T) {
	t.Parallel()
	cfg := config.NorthMatterCASE{NodeID: 42, FabricID: 1}
	var buf syncBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mgr := buildTestOperationalManager(t)

	adapter, err := buildCaseAdapter(t.Context(), cfg, mgr, nil /* no store → ephemeral fallback */, logger)
	if err != nil {
		t.Fatalf("buildCaseAdapter: %v", err)
	}
	if adapter == nil {
		t.Error("expected non-nil CaseAdapter")
	}
}

// TestBuildCaseAdapter_LogsEphemeralWarning verifies that buildCaseAdapter
// emits the "ephemeral_identity" warning regardless of config values.
func TestBuildCaseAdapter_LogsEphemeralWarning(t *testing.T) {
	t.Parallel()
	cfg := config.NorthMatterCASE{NodeID: 1, FabricID: 1}
	var buf syncBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mgr := buildTestOperationalManager(t)

	if _, err := buildCaseAdapter(t.Context(), cfg, mgr, nil /* no store → ephemeral fallback */, logger); err != nil {
		t.Fatalf("buildCaseAdapter: %v", err)
	}
	if logs := buf.String(); !containsSubstring(logs, "ephemeral_identity") {
		t.Errorf("expected 'ephemeral_identity' warning in logs; got:\n%s", logs)
	}
}

// TestTrustAnyPeerVerifier_EmptyNocErrors verifies that
// trustAnyPeerVerifier.VerifyAndExtractPubKey returns an error when the NOC
// slice is empty.
func TestTrustAnyPeerVerifier_EmptyNocErrors(t *testing.T) {
	t.Parallel()
	var v trustAnyPeerVerifier
	pub, err := v.VerifyAndExtractPubKey(nil, nil)
	if err == nil {
		t.Errorf("expected error for empty NOC, got nil (pub=%v)", pub)
	}
}

// ─── persistent CASE identity tests ──────────────────────────────────────

// TestLoadPersistentCaseIdentity_NoStoreNoFabric verifies that the
// loader returns persisted=false when no store is wired (caller
// falls back to the ephemeral path).
func TestLoadPersistentCaseIdentity_NoStoreNoFabric(t *testing.T) {
	t.Parallel()
	cfg := config.NorthMatterCASE{NodeID: 1, FabricID: 0}
	logger := slog.New(slog.DiscardHandler)
	_, _, _, persisted, err := loadPersistentCaseIdentity(t.Context(), cfg, nil, logger)
	if err != nil {
		t.Fatalf("loadPersistentCaseIdentity: %v", err)
	}
	if persisted {
		t.Error("persisted=true with nil store, want false")
	}
}

// TestLoadPersistentCaseIdentity_EmptyStore verifies that an
// empty-but-migrated store returns persisted=false rather than
// surfacing an error.
func TestLoadPersistentCaseIdentity_EmptyStore(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	cfg := config.NorthMatterCASE{NodeID: 1, FabricID: 0}
	logger := slog.New(slog.DiscardHandler)
	_, _, _, persisted, err := loadPersistentCaseIdentity(t.Context(), cfg, store, logger)
	if err != nil {
		t.Fatalf("loadPersistentCaseIdentity: %v", err)
	}
	if persisted {
		t.Error("persisted=true with empty store, want false")
	}
}

// TestLoadPersistentCaseIdentity_PicksFabric verifies that with a
// persisted (fabric, identity) pair the loader returns a
// fully-populated *sigma.Identity backed by a real *ecdsa.PrivateKey.
func TestLoadPersistentCaseIdentity_PicksFabric(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	ctx := t.Context()

	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("root key: %v", err)
	}
	rootPub := elliptic.Marshal(elliptic.P256(), rootPriv.X, rootPriv.Y) //nolint:staticcheck // SA1019: matches store wire shape
	if rootPub[0] != 0x04 || len(rootPub) != 65 {
		t.Fatalf("root pub shape: len=%d prefix=%#x", len(rootPub), rootPub[0])
	}

	fabricIdx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0xCAFEBABE,
		NodeID:        0xDEADBEEF,
		RootPublicKey: rootPub,
		VendorID:      0xFFF1,
		Label:         "test",
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	nodePriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("node key: %v", err)
	}
	scalar := nodePriv.D.FillBytes(make([]byte, 32)) //nolint:staticcheck // SA1019: direct D access is the Matter NOC private-scalar bytes — crypto/ecdh wraps these in an opaque PrivateKey.
	if err := store.UpsertIdentity(ctx, matterstore.IdentityRecord{
		FabricIndex: fabricIdx,
		NOC:         []byte{0xDE, 0xAD},
		PrivateKey:  scalar,
		IPK:         make([]byte, 16),
	}); err != nil {
		t.Fatalf("UpsertIdentity: %v", err)
	}

	cfg := config.NorthMatterCASE{NodeID: 0xDEADBEEF, FabricID: 0xCAFEBABE}
	logger := slog.New(slog.DiscardHandler)
	identity, verifier, idx, persisted, err := loadPersistentCaseIdentity(ctx, cfg, store, logger)
	if err != nil {
		t.Fatalf("loadPersistentCaseIdentity: %v", err)
	}
	if !persisted {
		t.Fatal("persisted=false, want true")
	}
	if idx != fabricIdx {
		t.Errorf("FabricIndex: got %d, want %d", idx, fabricIdx)
	}
	if identity == nil || identity.PrivateKey == nil {
		t.Fatalf("identity = %+v, want non-nil w/ PrivateKey", identity)
	}
	if identity.NodeID != 0xDEADBEEF || identity.FabricID != 0xCAFEBABE {
		t.Errorf("identity ids: got node=%x fabric=%x", identity.NodeID, identity.FabricID)
	}
	if verifier == nil {
		t.Error("verifier = nil, want a non-nil mattercert.Verifier")
	}
}

// matterStoreFromManager extracts the matterstore.Store underlying a
// test operational manager. operational.Manager doesn't expose its
// store, so we re-open against the same DSN — but [buildTestOperationalManager]
// already wraps a fresh DB. For these focused tests we just rebuild
// a parallel store against the same shared sqlite path, which works
// because operational.Manager doesn't lock the schema.
func matterStoreFromManager(t *testing.T, _ *operational.Manager) *matterstore.Store {
	t.Helper()
	ctx := context.Background()
	dsn := "file:" + t.TempDir() + "/matter_persistent_test.db?_pragma=journal_mode(WAL)"
	gooseMigrateMu.Lock()
	db, err := sqlitestore.Open(ctx, dsn)
	gooseMigrateMu.Unlock()
	if err != nil {
		t.Fatalf("matterStoreFromManager: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return matterstore.New(db)
}
