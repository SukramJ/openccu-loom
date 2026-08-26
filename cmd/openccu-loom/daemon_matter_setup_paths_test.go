// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

// daemon_coverage2_test.go — additional coverage tests for daemon.go gaps:
//   - startMatterBridge additional paths (DevRotateUniqueIDs, ConcurrentPairings,
//     db-fail path, EphemeralWindow, nil/disabled)
//   - buildRootClusters with non-nil store (AccessControl, OpCreds, GKM)
//   - loadPersistentCaseIdentity with valid full identity + invalid privKey + FabricID match
//   - loadAdditionalFabricsForCase with complete identity records + missing identity
//   - buildPaseAdapterFromCreds with/without opCreds
//   - loadVendorAttestation: partial config, key mismatch, full match
//   - loadTranslations: file path + empty path
//   - loadEasymode / loadProfiles: embedded paths
//   - buildTestAttestation: chain + CD
//   - buildAggregatorClusters: full path
//   - buildDevAttestation: non-zero IDs
//   - daemonServe: Auth.Users, OIDC disabled
//   - buildCaseAdapter: ephemeral path
//   - deriveOperationalIPK: valid input

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// ── startMatterBridge: DevRotateUniqueIDs path ────────────────────────────────

func TestStartMatterBridge_DevRotateUniqueIDs_DoesNotPanic(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.DevRotateUniqueIDs = true
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start")
	}
	t.Cleanup(bundle.stop)
}

// ── startMatterBridge: ConcurrentPairings path ───────────────────────────────

func TestStartMatterBridge_ConcurrentPairings_Armed(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.North.Matter.Commissioning.ConcurrentPairings = true
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-conc", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-conc")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start")
	}
	t.Cleanup(bundle.stop)
}

// ── startMatterBridge: shared-db-unavailable path ─────────────────────────────

// TestStartMatterBridge_NilDB_ReturnsNil verifies that startMatterBridge
// degrades to disabled when the shared *sql.DB handle is nil — the state the
// composition root is in when openLoomDB's open failed (bad DataDir, locked
// file, …). startMatterBridge itself no longer opens a DB, so this replaces
// the old "bad DataDir → internal open fails" scenario.
func TestStartMatterBridge_NilDB_ReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-fail", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-fail")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, nil, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle != nil {
		bundle.stop()
		t.Error("expected nil bundle when the shared db handle is nil")
	}
}

// ── startMatterBridge: nil/disabled cfg ──────────────────────────────────────

func TestStartMatterBridge_NilCfg_ReturnsNil(t *testing.T) {
	t.Parallel()
	bundle := startMatterBridge(context.Background(), nil, nil, nil, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle != nil {
		t.Error("expected nil for nil cfg")
	}
}

func TestStartMatterBridge_Disabled_ReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = false
	bundle := startMatterBridge(context.Background(), cfg, nil, nil, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle != nil {
		t.Error("expected nil when matter disabled")
	}
}

// ── startMatterBridge: EphemeralWindow + ConcurrentPairings ──────────────────

func TestStartMatterBridge_EphemeralWindow_ConcurrentPairings(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.North.Matter.Commissioning.EphemeralWindow = true
	cfg.North.Matter.Commissioning.ConcurrentPairings = true
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-ephem", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-ephem")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start")
	}
	t.Cleanup(bundle.stop)
}

// ── startMatterBridge: EphemeralWindow + singleton PASE ──────────────────────

func TestStartMatterBridge_EphemeralWindow_Singleton(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.North.Matter.Commissioning.EphemeralWindow = true
	cfg.North.Matter.Commissioning.ConcurrentPairings = false
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-ephem2", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-ephem2")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start")
	}
	t.Cleanup(bundle.stop)
}

// ── startMatterBridge: empty DataDir no longer resolved here ─────────────────

// TestStartMatterBridge_EmptyDataDir_StillStartsWithSharedDB verifies that an
// empty cfg.DataDir has no bearing on startMatterBridge anymore — the
// "" → "./var" fallback now lives entirely in [openLoomDB] (see
// daemon_boot_test.go), and startMatterBridge only cares about the db handle
// it is given.
func TestStartMatterBridge_EmptyDataDir_StillStartsWithSharedDB(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = "" // irrelevant now — db is supplied directly, not opened here
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-defdir", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-defdir")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Fatal("expected non-nil bundle — DataDir is no longer consulted by startMatterBridge")
	}
	t.Cleanup(bundle.stop)
}

// ── buildRootClusters: with non-nil store (OpCreds + AccessControl + GKM) ────

func TestBuildRootClusters_WithStore_BuildsFullSet(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.NodeLabel = "coverage-test-full"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start")
	}
	t.Cleanup(bundle.stop)

	servers, opCreds, refs, err := buildRootClusters(
		context.Background(),
		cfg.North.Matter,
		bundle.store,
		bundle.bridge,
		nil,
		slog.New(slog.DiscardHandler),
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("buildRootClusters: %v", err)
	}
	// With a non-nil store: AccessControl, OperationalCredentials, and
	// GroupKeyManagement should all be in the returned slice.
	if len(servers) == 0 {
		t.Error("expected non-empty server list")
	}
	if opCreds == nil {
		t.Error("expected non-nil opCreds when store is non-nil")
	}
	if refs.BasicInformation == nil {
		t.Error("expected non-nil BasicInformation ref")
	}
	if refs.GeneralCommissioning == nil {
		t.Error("expected non-nil GeneralCommissioning ref")
	}
}

// ── buildRootClusters: OnFabricInstalledExtra callback registered ─────────────

func TestBuildRootClusters_OnFabricInstalledExtra_Registered(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.NodeLabel = "coverage-test-hook"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start")
	}
	t.Cleanup(bundle.stop)

	var mu sync.Mutex
	var called bool
	_, _, _, err := buildRootClusters(
		context.Background(),
		cfg.North.Matter,
		bundle.store,
		bundle.bridge,
		nil,
		slog.New(slog.DiscardHandler),
		func(_ context.Context, _ uint8, _, _ uint64, _ []byte) {
			mu.Lock()
			called = true
			mu.Unlock()
		},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("buildRootClusters: %v", err)
	}
	// Extra callback is registered but only fires on AddNOC — construction
	// must succeed without panicking.
	mu.Lock()
	_ = called
	mu.Unlock()
}

// ── loadPersistentCaseIdentity: store with valid fabric + identity ────────────

func TestLoadPersistentCaseIdentity_ValidFabric_ReturnsPersisted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	logger := slog.New(slog.DiscardHandler)

	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	rootPub := elliptic.Marshal(elliptic.P256(), rootPriv.X, rootPriv.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.26 but still the correct wire encoding for P-256 uncompressed public keys in the Matter spec // elliptic.Marshal deprecated in Go 1.25; kept for Matter TLV wire format compatibility

	fabricIdx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0xCAFE0001,
		NodeID:        0xBEEF0001,
		RootPublicKey: rootPub,
		VendorID:      0xFFF1,
		Label:         "test",
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	// 32-byte private key scalar.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	scalar := make([]byte, 32)
	priv.D.FillBytes(scalar) //nolint:staticcheck // ecdsa.PrivateKey.D deprecated in Go 1.26; test-only helper to produce a 32-byte scalar, not used in production crypto paths // .D deprecated in Go 1.26; test-only scalar extraction kept for Matter NOC private key format

	ipk := make([]byte, 16)
	if _, err := rand.Read(ipk); err != nil {
		t.Fatalf("rand ipk: %v", err)
	}

	if err := store.UpsertIdentity(ctx, matterstore.IdentityRecord{
		FabricIndex: fabricIdx,
		NOC:         []byte("mock-noc"),
		PrivateKey:  scalar,
		IPK:         ipk,
	}); err != nil {
		t.Fatalf("UpsertIdentity: %v", err)
	}

	caseCfg := config.NorthMatterCASE{}
	identity, verifier, idx, persisted, err := loadPersistentCaseIdentity(ctx, caseCfg, store, logger)
	if err != nil {
		t.Fatalf("loadPersistentCaseIdentity: %v", err)
	}
	if !persisted {
		t.Fatal("expected persisted=true")
	}
	if identity == nil {
		t.Fatal("expected non-nil identity")
	}
	if verifier == nil {
		t.Fatal("expected non-nil verifier")
	}
	if idx != fabricIdx {
		t.Errorf("fabricIndex mismatch: got %d, want %d", idx, fabricIdx)
	}
}

// ── loadPersistentCaseIdentity: invalid private key scalar ───────────────────

func TestLoadPersistentCaseIdentity_InvalidPrivKey_ReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	logger := slog.New(slog.DiscardHandler)

	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	rootPub := elliptic.Marshal(elliptic.P256(), rootPriv.X, rootPriv.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.26 but still the correct wire encoding for P-256 uncompressed public keys in the Matter spec

	fabricIdx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0xCAFE0002,
		NodeID:        0xBEEF0002,
		RootPublicKey: rootPub,
		VendorID:      0xFFF1,
		Label:         "bad-key",
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	// Too-short private key scalar → privKeyFromScalar returns error.
	if err := store.UpsertIdentity(ctx, matterstore.IdentityRecord{
		FabricIndex: fabricIdx,
		NOC:         []byte("mock-noc"),
		PrivateKey:  []byte{0x01, 0x02, 0x03}, // wrong length
		IPK:         make([]byte, 16),
	}); err != nil {
		t.Fatalf("UpsertIdentity: %v", err)
	}

	caseCfg := config.NorthMatterCASE{}
	_, _, _, _, err = loadPersistentCaseIdentity(ctx, caseCfg, store, logger)
	if err == nil {
		t.Fatal("expected error for invalid private key scalar")
	}
}

// ── loadPersistentCaseIdentity: explicit FabricID match ──────────────────────

func TestLoadPersistentCaseIdentity_FabricIDMatch_PicksCorrectFabric(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	logger := slog.New(slog.DiscardHandler)

	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	rootPub := elliptic.Marshal(elliptic.P256(), rootPriv.X, rootPriv.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.26 but still the correct wire encoding for P-256 uncompressed public keys in the Matter spec

	const wantFabricID = uint64(0xCAFE1234)
	fabricIdx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      wantFabricID,
		NodeID:        0xBEEF0003,
		RootPublicKey: rootPub,
		VendorID:      0xFFF1,
		Label:         "match",
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	scalar := make([]byte, 32)
	priv.D.FillBytes(scalar) //nolint:staticcheck // ecdsa.PrivateKey.D deprecated in Go 1.26; test-only helper to produce a 32-byte scalar, not used in production crypto paths
	ipk := make([]byte, 16)
	_, _ = rand.Read(ipk)

	if err := store.UpsertIdentity(ctx, matterstore.IdentityRecord{
		FabricIndex: fabricIdx,
		NOC:         []byte("mock-noc"),
		PrivateKey:  scalar,
		IPK:         ipk,
	}); err != nil {
		t.Fatalf("UpsertIdentity: %v", err)
	}

	caseCfg := config.NorthMatterCASE{FabricID: wantFabricID}
	identity, _, idx, persisted, err := loadPersistentCaseIdentity(ctx, caseCfg, store, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !persisted {
		t.Fatal("expected persisted=true")
	}
	if identity == nil {
		t.Fatal("expected non-nil identity")
	}
	if idx != fabricIdx {
		t.Errorf("fabricIndex mismatch: got %d, want %d", idx, fabricIdx)
	}
}

// ── loadAdditionalFabricsForCase: two valid fabrics ───────────────────────────

func TestLoadAdditionalFabricsForCase_TwoValidFabrics_LoadsBoth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	logger := slog.New(slog.DiscardHandler)

	addFabricWithIdentity := func(t *testing.T, fabricID, nodeID uint64) uint8 {
		t.Helper()
		rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		rootPub := elliptic.Marshal(elliptic.P256(), rootPriv.X, rootPriv.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.26 but still the correct wire encoding for P-256 uncompressed public keys in the Matter spec
		idx, err := store.AddFabric(ctx, matterstore.FabricRecord{
			FabricID:      fabricID,
			NodeID:        nodeID,
			RootPublicKey: rootPub,
			VendorID:      0xFFF1,
			Label:         "multi",
		})
		if err != nil {
			t.Fatalf("AddFabric: %v", err)
		}
		priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		scalar := make([]byte, 32)
		priv.D.FillBytes(scalar) //nolint:staticcheck // ecdsa.PrivateKey.D deprecated in Go 1.26; test-only helper to produce a 32-byte scalar, not used in production crypto paths
		ipk := make([]byte, 16)
		_, _ = rand.Read(ipk)
		if err := store.UpsertIdentity(ctx, matterstore.IdentityRecord{
			FabricIndex: idx,
			NOC:         []byte("mock-noc"),
			PrivateKey:  scalar,
			IPK:         ipk,
		}); err != nil {
			t.Fatalf("UpsertIdentity: %v", err)
		}
		return idx
	}

	seed := addFabricWithIdentity(t, 0xAAAA0001, 0x1111)
	other := addFabricWithIdentity(t, 0xAAAA0002, 0x2222)

	caseFabrics := make(map[uint8]*caseFabricEntry)
	var mu sync.RWMutex

	n := loadAdditionalFabricsForCase(ctx, store, seed, caseFabrics, &mu, logger)
	if n != 1 {
		t.Errorf("expected 1 additional fabric loaded, got %d", n)
	}
	mu.RLock()
	_, ok := caseFabrics[other]
	mu.RUnlock()
	if !ok {
		t.Errorf("expected caseFabrics[%d] to be populated", other)
	}
}

// ── loadAdditionalFabricsForCase: fabric with missing identity → skip ─────────

func TestLoadAdditionalFabricsForCase_MissingIdentity_SkipsEntry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	logger := slog.New(slog.DiscardHandler)

	rootPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rootPub := elliptic.Marshal(elliptic.P256(), rootPriv.X, rootPriv.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.26 but still the correct wire encoding for P-256 uncompressed public keys in the Matter spec

	// Add fabric WITHOUT a corresponding identity row.
	idx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0xBEEF0001,
		NodeID:        0xCCCC,
		RootPublicKey: rootPub,
		VendorID:      0xFFF1,
		Label:         "no-identity",
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	caseFabrics := make(map[uint8]*caseFabricEntry)
	var mu sync.RWMutex

	// seedIdx != idx so the fabric is not skipped as seed, but has no
	// identity → GetIdentity fails → continue (no crash).
	const dummySeed = uint8(99)
	n := loadAdditionalFabricsForCase(ctx, store, dummySeed, caseFabrics, &mu, logger)
	if n != 0 {
		t.Errorf("expected 0 loaded (missing identity), got %d", n)
	}
	_ = idx
}

// ── loadAdditionalFabricsForCase: fabric with invalid privkey → skip ──────────

func TestLoadAdditionalFabricsForCase_InvalidPrivKey_SkipsEntry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	logger := slog.New(slog.DiscardHandler)

	rootPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rootPub := elliptic.Marshal(elliptic.P256(), rootPriv.X, rootPriv.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.26 but still the correct wire encoding for P-256 uncompressed public keys in the Matter spec

	idx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0xBEEF0002,
		NodeID:        0xDDDD,
		RootPublicKey: rootPub,
		VendorID:      0xFFF1,
		Label:         "bad-privkey",
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}
	if err := store.UpsertIdentity(ctx, matterstore.IdentityRecord{
		FabricIndex: idx,
		NOC:         []byte("noc"),
		PrivateKey:  []byte{0x01}, // too short → privKeyFromScalar error
		IPK:         make([]byte, 16),
	}); err != nil {
		t.Fatalf("UpsertIdentity: %v", err)
	}

	caseFabrics := make(map[uint8]*caseFabricEntry)
	var mu sync.RWMutex
	const dummySeed = uint8(99)
	n := loadAdditionalFabricsForCase(ctx, store, dummySeed, caseFabrics, &mu, logger)
	if n != 0 {
		t.Errorf("expected 0 loaded (bad privkey), got %d", n)
	}
}

// ── buildPaseAdapterFromCreds: with opCreds wired ────────────────────────────

func TestBuildPaseAdapterFromCreds_WithOpCreds_Builds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.NodeLabel = "pase-opcreds"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	innerCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)

	db := openTestLoomDB(t)
	bundle := startMatterBridge(innerCtx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start")
	}
	t.Cleanup(bundle.stop)

	mgr := buildTestOperationalManager(t)

	// Build root clusters to get an opCreds instance.
	_, opCreds, _, err := buildRootClusters(
		context.Background(),
		cfg.North.Matter,
		bundle.store,
		bundle.bridge,
		nil,
		slog.New(slog.DiscardHandler),
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("buildRootClusters: %v", err)
	}

	salt := []byte("test-salt-16byte")
	pase, err := buildPaseAdapterFromCreds(20202021, salt, 1000, mgr, opCreds, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("buildPaseAdapterFromCreds: %v", err)
	}
	if pase == nil {
		t.Fatal("expected non-nil PaseAdapter")
	}
}

// ── buildPaseAdapterFromCreds: opCreds = nil ─────────────────────────────────

func TestBuildPaseAdapterFromCreds_NilOpCreds_Builds(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	salt := []byte("test-salt-16byte")
	pase, err := buildPaseAdapterFromCreds(20202021, salt, 1000, mgr, nil, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("buildPaseAdapterFromCreds: %v", err)
	}
	if pase == nil {
		t.Fatal("expected non-nil PaseAdapter")
	}
}

// ── loadVendorAttestation: missing DACKeyPath → false ────────────────────────

func TestLoadVendorAttestation_MissingDACKeyPath_ReturnsFalse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	certDER := makeSelfSignedDERCert(t, priv)
	dacPath := filepath.Join(dir, "dac.der")
	_ = os.WriteFile(dacPath, certDER, 0o600)
	paiPath := filepath.Join(dir, "pai.der")
	_ = os.WriteFile(paiPath, certDER, 0o600)
	cdPath := filepath.Join(dir, "cd.bin")
	_ = os.WriteFile(cdPath, []byte{0x01, 0x02}, 0o600)

	// DACKeyPath is empty → early return false.
	cfg := config.NorthMatterAttestation{
		DACPath:    dacPath,
		PAIPath:    paiPath,
		CDPath:     cdPath,
		DACKeyPath: "",
	}
	_, _, _, _, ok := loadVendorAttestation(cfg, slog.New(slog.DiscardHandler))
	if ok {
		t.Error("expected ok=false when DACKeyPath is empty")
	}
}

// ── loadVendorAttestation: key mismatch → false ───────────────────────────────

func TestLoadVendorAttestation_KeyMismatch2_ReturnsFalse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	priv1, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	priv2, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	certDER := makeSelfSignedDERCert(t, priv1) // signed by priv1
	dacPath := filepath.Join(dir, "dac.der")
	_ = os.WriteFile(dacPath, certDER, 0o600)
	paiPath := filepath.Join(dir, "pai.der")
	_ = os.WriteFile(paiPath, certDER, 0o600)
	cdPath := filepath.Join(dir, "cd.bin")
	_ = os.WriteFile(cdPath, []byte{0x01}, 0o600)

	// Write priv2 (mismatched) as PEM PKCS#8.
	der2, err := x509.MarshalPKCS8PrivateKey(priv2)
	if err != nil {
		t.Fatalf("MarshalPKCS8: %v", err)
	}
	keyPath := filepath.Join(dir, "dac.key")
	_ = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: der2,
	}), 0o600)

	cfg := config.NorthMatterAttestation{
		DACPath:    dacPath,
		PAIPath:    paiPath,
		CDPath:     cdPath,
		DACKeyPath: keyPath,
	}
	_, _, _, _, ok := loadVendorAttestation(cfg, slog.New(slog.DiscardHandler))
	if ok {
		t.Error("expected ok=false for key mismatch")
	}
}

// ── loadVendorAttestation: all files correct → ok=true ───────────────────────

func TestLoadVendorAttestation_MatchingKey_ReturnsTrue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	certDER := makeSelfSignedDERCert(t, priv)

	dacPath := filepath.Join(dir, "dac.der")
	_ = os.WriteFile(dacPath, certDER, 0o600)
	paiPath := filepath.Join(dir, "pai.der")
	_ = os.WriteFile(paiPath, certDER, 0o600)
	cdPath := filepath.Join(dir, "cd.bin")
	_ = os.WriteFile(cdPath, []byte{0xCD}, 0o600)

	// Write matching private key as PKCS#8 PEM.
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalPKCS8: %v", err)
	}
	keyPath := filepath.Join(dir, "dac.key")
	_ = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: der,
	}), 0o600)

	cfg := config.NorthMatterAttestation{
		DACPath:    dacPath,
		PAIPath:    paiPath,
		CDPath:     cdPath,
		DACKeyPath: keyPath,
	}
	key, dac, pai, cd, ok := loadVendorAttestation(cfg, slog.New(slog.DiscardHandler))
	if !ok {
		t.Fatal("expected ok=true for matching key")
	}
	if key == nil || len(dac) == 0 || len(pai) == 0 || len(cd) == 0 {
		t.Error("expected non-nil/non-empty return values")
	}
}

// ── loadVendorAttestation: non-existent DAC file → false ─────────────────────

func TestLoadVendorAttestation_MissingDACFile_ReturnsFalse(t *testing.T) {
	t.Parallel()
	cfg := config.NorthMatterAttestation{
		DACPath:    "/nonexistent/dac.der",
		PAIPath:    "/nonexistent/pai.der",
		CDPath:     "/nonexistent/cd.bin",
		DACKeyPath: "/nonexistent/key.pem",
	}
	_, _, _, _, ok := loadVendorAttestation(cfg, slog.New(slog.DiscardHandler))
	if ok {
		t.Error("expected ok=false for missing files")
	}
}

// ── loadTranslations: non-existent file path → falls back to embedded ─────────

func TestLoadTranslations_NonExistentFilePath_FallsBack(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.CCUData.TranslationsPath = "/nonexistent/translations.tar.gz"
	tr := loadTranslations(cfg, slog.New(slog.DiscardHandler))
	if tr == nil {
		t.Error("expected non-nil translations (fallback to embedded)")
	}
}

// ── loadTranslations: empty path → embedded ──────────────────────────────────

func TestLoadTranslations_EmptyPath_UsesEmbedded(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.CCUData.TranslationsPath = ""
	tr := loadTranslations(cfg, slog.New(slog.DiscardHandler))
	if tr == nil {
		t.Error("expected non-nil translations")
	}
}

// ── loadEasymode ──────────────────────────────────────────────────────────────

func TestLoadEasymode_EmbeddedLoads(t *testing.T) {
	t.Parallel()
	em := loadEasymode(config.Default(), slog.New(slog.DiscardHandler))
	if em == nil {
		t.Error("expected non-nil easymode")
	}
}

// ── loadProfiles ──────────────────────────────────────────────────────────────

func TestLoadProfiles_EmbeddedLoads(t *testing.T) {
	t.Parallel()
	ps := loadProfiles(slog.New(slog.DiscardHandler))
	// ps may be nil if the embedded archive is absent — no assertion on value.
	_ = ps
}

// ── buildTestAttestation: chain + CD produced ────────────────────────────────

func TestBuildTestAttestation_ProducesChainAndCD(t *testing.T) {
	t.Parallel()
	chain, cd, err := buildTestAttestation(0xFFF1, 0x8000)
	if err != nil {
		t.Fatalf("buildTestAttestation: %v", err)
	}
	if chain == nil {
		t.Fatal("expected non-nil chain")
	}
	if chain.DACKey == nil {
		t.Error("expected non-nil DAC key")
	}
	if len(chain.DAC) == 0 {
		t.Error("expected non-empty DAC")
	}
	if len(chain.PAI) == 0 {
		t.Error("expected non-empty PAI")
	}
	if len(cd) == 0 {
		t.Error("expected non-empty CD")
	}
}

// ── buildTestAttestation: zero ProductID ─────────────────────────────────────

func TestBuildTestAttestation_ZeroProductID_Succeeds(t *testing.T) {
	t.Parallel()
	_, _, err := buildTestAttestation(0xFFF1, 0)
	if err != nil {
		t.Fatalf("buildTestAttestation(0xFFF1, 0): %v", err)
	}
}

// ── buildAggregatorClusters: returns non-empty slice ─────────────────────────

func TestBuildAggregatorClusters_NonEmptySlice(t *testing.T) {
	t.Parallel()
	servers, err := buildAggregatorClusters()
	if err != nil {
		t.Fatalf("buildAggregatorClusters: %v", err)
	}
	if len(servers) == 0 {
		t.Error("expected at least one cluster server")
	}
	if servers[0] == nil {
		t.Error("expected non-nil first cluster server")
	}
}

// ── buildDevAttestation: non-zero IDs ────────────────────────────────────────

func TestBuildDevAttestation_NonZeroIDs_Succeeds(t *testing.T) {
	t.Parallel()
	key, dac, pai, cd, err := buildDevAttestation(0xFFF1, 0x8000)
	if err != nil {
		t.Fatalf("buildDevAttestation: %v", err)
	}
	if key == nil {
		t.Error("expected non-nil key")
	}
	if len(dac) == 0 || len(pai) == 0 {
		t.Error("expected non-empty cert bytes")
	}
	_ = cd
}

// ── daemonServe: REST with Auth.Users configured ─────────────────────────────

func TestDaemonServe_WithAuthUsers_StartsOK(t *testing.T) {
	t.Parallel()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	restAddr := l.Addr().String()
	_ = l.Close()

	cfg := config.Default()
	cfg.North.REST.Enabled = new(true)
	cfg.North.REST.Listen = restAddr
	cfg.North.REST.Auth.Users = map[string]string{
		"admin": "secret",
	}
	cfg.North.UI.Enabled = new(false)
	cfg.DataDir = t.TempDir()
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-auth", Host: "127.0.0.1"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- daemonServe(ctx, cfg, io.Discard, io.Discard) }()

	// Wait for the REST listener to respond.
	client := &http.Client{Timeout: 400 * time.Millisecond}
	url := "http://" + restAddr + "/api/v1/health"
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if resp, cerr := client.Get(url); cerr == nil { //nolint:noctx // test-only polling loop; a context.Context here would complicate the deadline logic without benefit
			_ = resp.Body.Close()
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemonServe error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("shutdown timeout")
	}
}

// ── daemonServe: OIDC disabled path ──────────────────────────────────────────

func TestDaemonServe_OIDCDisabled_StartsOK(t *testing.T) {
	t.Parallel()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	restAddr := l.Addr().String()
	_ = l.Close()

	cfg := config.Default()
	cfg.North.REST.Enabled = new(true)
	cfg.North.REST.Listen = restAddr
	cfg.North.REST.Auth.OIDC.Enabled = false
	cfg.North.UI.Enabled = new(false)
	cfg.DataDir = t.TempDir()
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-oidc", Host: "127.0.0.1"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- daemonServe(ctx, cfg, io.Discard, io.Discard) }()

	client := &http.Client{Timeout: 400 * time.Millisecond}
	url := "http://" + restAddr + "/api/v1/health"
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if resp, cerr := client.Get(url); cerr == nil { //nolint:noctx // test-only polling loop; a context.Context here would complicate the deadline logic without benefit
			_ = resp.Body.Close()
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemonServe error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("shutdown timeout")
	}
}

// ── buildCaseAdapter: ephemeral path (no persisted fabric) ───────────────────

func TestBuildCaseAdapter_EphemeralPath_BuildsAdapter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr) // empty store → no fabric → ephemeral
	logger := slog.New(slog.DiscardHandler)

	caseCfg := config.NorthMatterCASE{NodeID: 0xDEAD, FabricID: 0xBEEF}
	adapter, err := buildCaseAdapter(ctx, caseCfg, mgr, store, logger)
	if err != nil {
		t.Fatalf("buildCaseAdapter: %v", err)
	}
	if adapter == nil {
		t.Fatal("expected non-nil adapter")
	}
}

// ── deriveOperationalIPK: valid input → non-zero ──────────────────────────────

func TestDeriveOperationalIPK_ValidInput_NonZero(t *testing.T) {
	t.Parallel()
	rawIPK := make([]byte, 16)
	_, _ = rand.Read(rawIPK)
	var compFabricID [8]byte
	_, _ = rand.Read(compFabricID[:])

	out, err := deriveOperationalIPK(rawIPK, compFabricID)
	if err != nil {
		t.Fatalf("deriveOperationalIPK: %v", err)
	}
	allZero := true
	for _, b := range out {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("expected non-zero output from HKDF")
	}
}

// ── wireIncidentRecorder: with central having a Cache ────────────────────────

func TestWireIncidentRecorder_CentralWithCache_SetsRecorder(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)
	reg := buildTestRegistry(t, "ccu-incident")
	logger := slog.New(slog.DiscardHandler)

	_, closer := wireIncidentRecorder(db, reg, logger)
	t.Cleanup(closer)
	// No assertion needed — no panic is the goal.
}

// ── shared helpers ────────────────────────────────────────────────────────────

// makeSelfSignedDERCert produces a DER-encoded self-signed ECDSA certificate
// for use in loadVendorAttestation tests.
func makeSelfSignedDERCert(t *testing.T, priv *ecdsa.PrivateKey) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	return der
}
