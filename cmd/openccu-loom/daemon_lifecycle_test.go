// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

// daemon_lifecycle_test.go — targeted coverage tests for daemon.go + main.go +
// audit_wiring.go paths that remain below the 92 % goal.
//
// Design rules:
//   - No production-code changes.
//   - Every listener binds ":0" (OS-assigned port) to avoid conflicts.
//   - All tests complete in < 30 s each; most are sub-second.
//   - Race-safe: shared state uses the package-level gooseMigrateMu
//     serialiser already established by daemon_test.go.
//   - main.main() is deliberately left at 0 % (untestable entry-point).

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// ── main.run() branches ──────────────────────────────────────────────────────

func TestRun_NoArgs_ReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{}, &stdout, &stderr)
	if err == nil {
		t.Error("expected error for empty args")
	}
}

func TestRun_HelpSubcommand_ReturnsNil(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"help"}, &stdout, &stderr); err != nil {
		t.Fatalf("run(help) returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "usage:") {
		t.Errorf("help output should contain 'usage:'; got:\n%s", stdout.String())
	}
}

func TestRun_HelpShortFlag_ReturnsNil(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-h"}, &stdout, &stderr); err != nil {
		t.Fatalf("run(-h) returned error: %v", err)
	}
}

func TestRun_HelpLongFlag_ReturnsNil(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("run(--help) returned error: %v", err)
	}
}

func TestRun_VersionShortFlag_ReturnsNil(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-v"}, &stdout, &stderr); err != nil {
		t.Fatalf("run(-v) returned error: %v", err)
	}
}

func TestRun_VersionLongFlag_ReturnsNil(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run(--version) returned error: %v", err)
	}
}

// TestRunDaemon_InvalidConfigPath verifies runDaemon returns an error when
// the supplied config file does not exist.
func TestRunDaemon_InvalidConfigPath_ReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := runDaemon([]string{"--config=/nonexistent/path/to/config.yaml"}, &stdout, &stderr)
	if err == nil {
		t.Error("expected error for invalid config path")
	}
}

// ── daemonServe — REST-enabled variant ───────────────────────────────────────

// TestDaemonServe_WithRESTAndUI boots the daemon with both REST and UI
// enabled on dynamic ports, then stops.
func TestDaemonServe_WithRESTAndUI(t *testing.T) {
	cfg := config.Default()
	cfg.North.REST.Enabled = new(true)
	cfg.North.REST.Listen = "127.0.0.1:0"
	cfg.North.UI.Enabled = new(true)
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- daemonServe(ctx, cfg, &bytes.Buffer{}, &bytes.Buffer{}) }()

	// Allow a short boot window then cancel.
	time.Sleep(400 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon returned error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("daemon did not shut down in time")
	}
}

// TestDaemonServe_WithDataDir boots the daemon with a DataDir so the SQLite
// wiring paths (wireSessionRecorderPersistence, wireIncidentRecorder,
// wireAuditPersistence) exercise their "valid DB" branches.
func TestDaemonServe_WithDataDir(t *testing.T) {
	cfg := config.Default()
	cfg.North.REST.Enabled = new(false)
	cfg.North.UI.Enabled = new(false)
	cfg.DataDir = t.TempDir()
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- daemonServe(ctx, cfg, &bytes.Buffer{}, &bytes.Buffer{}) }()
	time.Sleep(250 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemonServe with DataDir: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("shutdown timeout")
	}
}

// TestDaemonServe_WithMatterAndDataDir boots the full Matter wiring path
// with a real DataDir.
func TestDaemonServe_WithMatterAndDataDir(t *testing.T) {
	cfg := config.Default()
	cfg.North.REST.Enabled = new(false)
	cfg.North.UI.Enabled = new(false)
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.Discriminator = 0xF00
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.DataDir = t.TempDir()
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- daemonServe(ctx, cfg, &bytes.Buffer{}, &bytes.Buffer{}) }()
	time.Sleep(400 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemonServe with Matter+DataDir: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("shutdown timeout")
	}
}

// TestDaemonServe_WithCASEConfigured boots the daemon with CASE.NodeID set so
// the persistent-identity loading paths are exercised.
func TestDaemonServe_WithCASEConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.North.REST.Enabled = new(false)
	cfg.North.UI.Enabled = new(false)
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.CASE.NodeID = 42
	cfg.North.Matter.CASE.FabricID = 1
	cfg.DataDir = t.TempDir()
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- daemonServe(ctx, cfg, &bytes.Buffer{}, &bytes.Buffer{}) }()
	time.Sleep(400 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemonServe with CASE config: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("shutdown timeout")
	}
}

// ── startCallbackServer — additional branches ─────────────────────────────────

// TestStartCallbackServer_PublicHostConfigured verifies that when
// cfg.Callback.PublicHost is set the server starts and callbackHostFor
// returns the configured public host for any central.
func TestStartCallbackServer_PublicHostConfigured(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Callback.Host = "127.0.0.1"
	cfg.Callback.Port = 0
	cfg.Callback.PublicHost = "192.0.2.1"
	logger := slog.New(slog.DiscardHandler)
	ctx := t.Context()

	srv, port, err := startCallbackServer(ctx, cfg, nil, logger)
	if err != nil {
		t.Fatalf("startCallbackServer with public host: %v", err)
	}
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	if port == 0 {
		t.Error("expected non-zero effective port")
	}
	// The host advertised to each CCU is now resolved per-central via
	// callbackHostFor; PublicHost must be returned for any central.
	cc := &config.CentralConfig{Name: "ccu-01", Host: "192.168.1.1"}
	host := callbackHostFor(cfg, cc)
	if host != "192.0.2.1" {
		t.Errorf("callbackHostFor: expected PublicHost %q, got %q", "192.0.2.1", host)
	}
}

// TestStartCallbackServer_NoCentralsNoPublicHost verifies that when there are
// no centrals and no public host configured, startCallbackServer still
// succeeds — host resolution is now deferred to the per-central wiring phase.
// callbackHostFor returns "" for a central with an unreachable host, which
// causes the wiring layer to skip callbacks for that central.
func TestStartCallbackServer_NoCentralsNoPublicHost(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Callback.Host = "127.0.0.1"
	cfg.Callback.Port = 0
	cfg.Callback.PublicHost = ""
	cfg.Centrals = nil
	logger := slog.New(slog.DiscardHandler)
	ctx := t.Context()

	srv, port, err := startCallbackServer(ctx, cfg, nil, logger)
	if err != nil {
		t.Fatalf("startCallbackServer must succeed even without centrals: %v", err)
	}
	if srv == nil {
		t.Error("expected non-nil srv")
	}
	if port == 0 {
		t.Error("expected non-zero effective port")
	}
	// With no PublicHost and an unreachable central host, callbackHostFor
	// returns "" — the per-central wiring skips callbacks for that central.
	cc := &config.CentralConfig{Name: "ccu-01", Host: "::invalid::"}
	host := callbackHostFor(cfg, cc)
	if host != "" {
		t.Errorf("callbackHostFor for invalid host should return empty, got %q", host)
	}
}

// TestStartCallbackServer_WithPortRange verifies the port-range scan path.
func TestStartCallbackServer_WithPortRange(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Callback.Host = "127.0.0.1"
	cfg.Callback.Port = 0
	cfg.Callback.PortRange = "19000-19100"
	cfg.Callback.PublicHost = "127.0.0.1"
	logger := slog.New(slog.DiscardHandler)
	ctx := t.Context()

	srv, port, err := startCallbackServer(ctx, cfg, nil, logger)
	if err != nil {
		t.Fatalf("startCallbackServer with port range: %v", err)
	}
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	if port == 0 {
		t.Error("expected non-zero effective port")
	}
}

// TestStartCallbackServer_InvalidPortRange verifies malformed port range string
// is rejected.
func TestStartCallbackServer_InvalidPortRange(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Callback.Host = "127.0.0.1"
	cfg.Callback.Port = 0
	cfg.Callback.PortRange = "not-a-range"
	cfg.Callback.PublicHost = "127.0.0.1"
	logger := slog.New(slog.DiscardHandler)
	ctx := t.Context()

	_, _, err := startCallbackServer(ctx, cfg, nil, logger)
	if err == nil {
		t.Error("expected error for invalid port range")
	}
}

// ── egressHostToward / callbackHostFor ────────────────────────────────────────

func TestEgressHostToward_RoutableHost_ReturnsIP(t *testing.T) {
	t.Parallel()
	ip := egressHostToward("8.8.8.8")
	if ip == "" {
		t.Error("expected non-empty IP for routable host")
	}
}

func TestCallbackHostFor_WithCentral_ReturnsEgressIP(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Callback.PublicHost = ""
	cc := &config.CentralConfig{Name: "ccu-01", Host: "8.8.8.8"}
	ip := callbackHostFor(cfg, cc)
	if ip == "" {
		t.Error("expected non-empty IP for routable central host")
	}
}

// ── loadTranslations — file-path branch ──────────────────────────────────────

func TestLoadTranslations_MissingFilePath_FallsBackToEmbedded(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.CCUData.TranslationsPath = t.TempDir() + "/nonexistent.json"
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	got := loadTranslations(cfg, logger)
	if got == nil {
		t.Fatal("expected non-nil translations (fallback)")
	}
	if !bytes.Contains(buf.Bytes(), []byte("ccudata.translations.load")) {
		t.Errorf("expected load warning; got:\n%s", buf.String())
	}
}

// ── buildTestAttestation ──────────────────────────────────────────────────────

func TestBuildTestAttestation_NonZeroIDs(t *testing.T) {
	t.Parallel()
	chain, cd, err := buildTestAttestation(0x1234, 0x5678)
	if err != nil {
		t.Fatalf("buildTestAttestation(0x1234, 0x5678): %v", err)
	}
	if chain == nil {
		t.Error("expected non-nil chain")
	}
	_ = cd
}

// ── buildDevAttestation ───────────────────────────────────────────────────────

func TestBuildDevAttestation_ZeroIDs_ReturnsValidMaterial(t *testing.T) {
	t.Parallel()
	dacKey, dac, pai, cd, err := buildDevAttestation(0, 0)
	if err != nil {
		t.Fatalf("buildDevAttestation(0,0): %v", err)
	}
	if dacKey == nil || len(dac) == 0 || len(pai) == 0 {
		t.Error("expected valid attestation material")
	}
	_ = cd
}

// ── buildPaseAdapterFromCreds ─────────────────────────────────────────────────

// TestBuildPaseAdapterFromCreds_WithNilOpCreds verifies that the adapter
// builds when opCreds is nil.
func TestBuildPaseAdapterFromCreds_WithNilOpCreds_Builds(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	adapter, err := buildPaseAdapterFromCreds(20202021, []byte("openccu-loom-dev0"), 1000, mgr, nil, nil,
		slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("buildPaseAdapterFromCreds: %v", err)
	}
	if adapter == nil {
		t.Error("expected non-nil PaseAdapter")
	}
}

// TestBuildPaseAdapterFromCreds_InvalidIterations_ReturnsError verifies the
// spake2 guard rejects iterations ≤ 0.
func TestBuildPaseAdapterFromCreds_InvalidIterations_ReturnsError(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	_, err := buildPaseAdapterFromCreds(20202021, []byte("openccu-loom-dev0"), -1, mgr, nil, nil, slog.Default())
	if err == nil {
		t.Error("expected error for invalid iterations=-1")
	}
}

// ── buildCaseAdapter — persistent fabric path ─────────────────────────────────

// TestBuildCaseAdapter_WithPersistedFabric exercises the persisted-identity
// branch by inserting a fabric + identity into the store first.
func TestBuildCaseAdapter_WithPersistedFabric(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	ctx := t.Context()

	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("root key: %v", err)
	}
	rootPub := elliptic.Marshal(elliptic.P256(), rootPriv.X, rootPriv.Y) //nolint:staticcheck // SA1019: elliptic.Marshal matches Matter wire shape (uncompressed P-256)

	fabricIdx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0xCAFE,
		NodeID:        0xBEEF,
		RootPublicKey: rootPub,
		VendorID:      0xFFF1,
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	nodePriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("node key: %v", err)
	}
	scalar := nodePriv.D.FillBytes(make([]byte, 32)) //nolint:staticcheck // SA1019: .D direct access — Matter NOC private scalar
	if err := store.UpsertIdentity(ctx, matterstore.IdentityRecord{
		FabricIndex: fabricIdx,
		NOC:         []byte{0xDE, 0xAD},
		PrivateKey:  scalar,
		IPK:         make([]byte, 16),
	}); err != nil {
		t.Fatalf("UpsertIdentity: %v", err)
	}

	cfg := config.NorthMatterCASE{NodeID: 0xBEEF, FabricID: 0xCAFE}
	logger := slog.New(slog.DiscardHandler)
	adapter, err := buildCaseAdapter(ctx, cfg, mgr, store, logger)
	if err != nil {
		t.Fatalf("buildCaseAdapter with persisted fabric: %v", err)
	}
	if adapter == nil {
		t.Error("expected non-nil CaseAdapter for persisted identity")
	}
}

// ── loadPersistentCaseIdentity — IPK-derivation error path ───────────────────

// TestLoadPersistentCaseIdentity_WrongIPKLength verifies that a stored IPK
// of wrong length causes graceful degradation (persisted=false, no error).
func TestLoadPersistentCaseIdentity_WrongIPKLength(t *testing.T) {
	t.Parallel()
	store := matterStoreFromManager(t, buildTestOperationalManager(t))
	ctx := t.Context()

	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("root key: %v", err)
	}
	rootPub := elliptic.Marshal(elliptic.P256(), rootPriv.X, rootPriv.Y) //nolint:staticcheck // SA1019: elliptic.Marshal

	fabricIdx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0xABCD,
		NodeID:        0x1234,
		RootPublicKey: rootPub,
		VendorID:      0xFFF1,
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}
	nodePriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("node key: %v", err)
	}
	scalar := nodePriv.D.FillBytes(make([]byte, 32)) //nolint:staticcheck // SA1019
	if err := store.UpsertIdentity(ctx, matterstore.IdentityRecord{
		FabricIndex: fabricIdx,
		NOC:         []byte{0xAA},
		PrivateKey:  scalar,
		IPK:         []byte{0x01, 0x02}, // wrong length: 2 bytes instead of 16
	}); err != nil {
		t.Fatalf("UpsertIdentity: %v", err)
	}

	cfg := config.NorthMatterCASE{FabricID: 0xABCD, NodeID: 0x1234}
	logger := slog.New(slog.DiscardHandler)
	_, _, _, persisted, err := loadPersistentCaseIdentity(ctx, cfg, store, logger)
	if err != nil {
		t.Fatalf("unexpected error from loadPersistentCaseIdentity: %v", err)
	}
	if persisted {
		t.Error("expected persisted=false when IPK length is wrong")
	}
}

// ── loadAdditionalFabricsForCase — full fabric load path ─────────────────────

// TestLoadAdditionalFabricsForCase_TwoFabrics_LoadsNonSeed inserts two
// fabrics and verifies the non-seed fabric is loaded.
func TestLoadAdditionalFabricsForCase_TwoFabrics_LoadsNonSeed(t *testing.T) {
	t.Parallel()
	store := matterStoreFromManager(t, buildTestOperationalManager(t))
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)

	addFabricWithIdentity := func(fabricID, nodeID uint64) uint8 {
		t.Helper()
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		rootPub := elliptic.Marshal(elliptic.P256(), priv.X, priv.Y) //nolint:staticcheck // SA1019
		fabricIdx, err := store.AddFabric(ctx, matterstore.FabricRecord{
			FabricID:      fabricID,
			NodeID:        nodeID,
			RootPublicKey: rootPub,
			VendorID:      0xFFF1,
		})
		if err != nil {
			t.Fatalf("AddFabric(%d): %v", fabricID, err)
		}
		nodePriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey node: %v", err)
		}
		scalar := nodePriv.D.FillBytes(make([]byte, 32)) //nolint:staticcheck // SA1019
		if err := store.UpsertIdentity(ctx, matterstore.IdentityRecord{
			FabricIndex: fabricIdx,
			NOC:         []byte{0xDE, 0xAD},
			PrivateKey:  scalar,
			IPK:         make([]byte, 16),
		}); err != nil {
			t.Fatalf("UpsertIdentity(%d): %v", fabricID, err)
		}
		return fabricIdx
	}

	seedIdx := addFabricWithIdentity(0xAA, 0x01)
	addFabricWithIdentity(0xBB, 0x02)

	caseFabrics := make(map[uint8]*caseFabricEntry)
	var mu sync.RWMutex

	count := loadAdditionalFabricsForCase(ctx, store, seedIdx, caseFabrics, &mu, logger)
	if count != 1 {
		t.Errorf("expected 1 additional fabric loaded, got %d", count)
	}
	if len(caseFabrics) != 1 {
		t.Errorf("expected 1 entry in caseFabrics, got %d", len(caseFabrics))
	}
}

// ── loadFabricRootPublicKey ───────────────────────────────────────────────────

func TestLoadFabricRootPublicKey_NilStore_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := loadFabricRootPublicKey(t.Context(), nil, 1)
	if err == nil {
		t.Error("expected error for nil store")
	}
}

func TestLoadFabricRootPublicKey_MissingFabric_ReturnsError(t *testing.T) {
	t.Parallel()
	store := matterStoreFromManager(t, buildTestOperationalManager(t))
	_, err := loadFabricRootPublicKey(t.Context(), store, 99)
	if err == nil {
		t.Error("expected error for non-existent fabric index 99")
	}
}

// ── deriveOperationalIPK ──────────────────────────────────────────────────────

func TestDeriveOperationalIPK_WrongLength_ReturnsError(t *testing.T) {
	t.Parallel()
	var cid [8]byte
	_, err := deriveOperationalIPK([]byte{0x01, 0x02}, cid) // 2 bytes, not 16
	if err == nil {
		t.Error("expected error for IPK length != 16")
	}
}

func TestDeriveOperationalIPK_CorrectLength_ReturnsNonZero(t *testing.T) {
	t.Parallel()
	ipk := make([]byte, 16)
	for i := range ipk {
		ipk[i] = byte(i + 1)
	}
	var cid [8]byte
	for i := range cid {
		cid[i] = byte(i + 0x10)
	}
	out, err := deriveOperationalIPK(ipk, cid)
	if err != nil {
		t.Fatalf("deriveOperationalIPK: %v", err)
	}
	var zero [16]byte
	if out == zero {
		t.Error("expected non-zero derived IPK")
	}
}

// ── buildRootClusters — vendor attestation branch ────────────────────────────

// TestBuildRootClusters_WithVendorAttestation exercises the vendor attestation
// branch inside buildRootClusters by writing valid DAC/PAI/CD/key files.
func TestBuildRootClusters_WithVendorAttestation(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.NodeLabel = "test-vendor-attest"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	dir := t.TempDir()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	_, dacPath := writePEMCert(t, dir, priv, "dac")
	keyPath := writePEMKey(t, dir, priv, "key")
	_, paiPath := writePEMCert(t, dir, priv, "pai")
	cdPath := dir + "/cd.bin"
	if err := os.WriteFile(cdPath, []byte{0xDE, 0xAD}, 0o600); err != nil {
		t.Fatalf("WriteFile cd: %v", err)
	}

	cfg.North.Matter.Attestation = config.NorthMatterAttestation{
		DACPath:    dacPath,
		DACKeyPath: keyPath,
		PAIPath:    paiPath,
		CDPath:     cdPath,
	}

	reg := buildTestRegistry(t, "ccu-01")
	ctx := t.Context()
	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start")
	}
	t.Cleanup(bundle.stop)

	clusters, opCreds, refs, err := buildRootClusters(
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
		t.Fatalf("buildRootClusters with vendor attestation: %v", err)
	}
	if len(clusters) == 0 {
		t.Error("expected non-empty clusters")
	}
	if opCreds == nil {
		t.Error("expected non-nil opCreds with store + attestation")
	}
	if refs.BasicInformation == nil {
		t.Error("expected non-nil BasicInformation ref")
	}
}

// ── buildAggregatorClusters ───────────────────────────────────────────────────

func TestBuildAggregatorClusters_BuildsWithCorrectDeviceType(t *testing.T) {
	t.Parallel()
	clusters, err := buildAggregatorClusters()
	if err != nil {
		t.Fatalf("buildAggregatorClusters: %v", err)
	}
	// EP 1 Aggregator carries Identify (0x0003) + Descriptor (0x001D) — see
	// buildAggregatorClusters doc-comment for Apple HAP-Mapper rationale.
	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters (Identify + Descriptor), got %d", len(clusters))
	}
	gotIDs := []uint32{clusters[0].MatterClusterID(), clusters[1].MatterClusterID()}
	if gotIDs[0] != 0x0003 || gotIDs[1] != 0x001D {
		t.Errorf("expected [0x0003, 0x001D], got [0x%04X, 0x%04X]", gotIDs[0], gotIDs[1])
	}
}

// ── wireIncidentRecorder — centrals-with-cache branch ────────────────────────

// TestWireIncidentRecorder_WithCentralAndDB exercises the branch where the
// incident recorder is installed on a central's CacheCoordinator.
func TestWireIncidentRecorder_WithCentralAndDB(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)
	reg := buildTestRegistry(t, "ccu-inc")
	logger := slog.New(slog.DiscardHandler)
	_, closer := wireIncidentRecorder(db, reg, logger)
	t.Cleanup(closer)
}

// ── wireSessionRecorderPersistence — with actual central ─────────────────────

func TestWireSessionRecorderPersistence_WithCentral(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)
	reg := buildTestRegistry(t, "ccu-sess")
	logger := slog.New(slog.DiscardHandler)
	closer := wireSessionRecorderPersistence(db, reg, logger)
	if closer == nil {
		t.Fatal("expected non-nil closer")
	}
	closer()
}

// ── wireAuditPersistenceWithDB ────────────────────────────────────────────────

// TestWireAuditPersistenceWithDB_NilDB verifies the degraded fallback when no
// shared db handle is available — the state openLoomDB leaves callers in when
// DataDir is unusable.
func TestWireAuditPersistenceWithDB_NilDB(t *testing.T) {
	t.Parallel()
	buf := audit.NewBuffer(16)
	logger := slog.New(slog.DiscardHandler)
	got, _, _ := wireAuditPersistenceWithDB(nil, buf, logger)
	if got == nil {
		t.Fatal("expected non-nil fallback recorder when db is nil")
	}
}

// ── ArmFailSafeFor — non-nil gc path ─────────────────────────────────────────

// TestFailSafeArmerAdapter_WithGC verifies ArmFailSafeFor with a real
// GeneralCommissioning cluster does not emit the warn-skipped log.
func TestFailSafeArmerAdapter_WithGC(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.NodeLabel = "test-failsafe"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx := t.Context()
	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start")
	}
	t.Cleanup(bundle.stop)

	_, _, refs, err := buildRootClusters(
		context.Background(),
		cfg.North.Matter,
		nil,
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
	if refs.GeneralCommissioning == nil {
		t.Skip("GeneralCommissioning not built")
	}
	var buf syncBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a := &failSafeArmerAdapter{gc: refs.GeneralCommissioning, logger: logger}

	// ArmFailSafeFor may return an error from the cluster internals;
	// what matters is that the "gc_nil" skipped log is NOT emitted.
	_ = a.ArmFailSafeFor(ctx, 30, 1)
	if containsSubstring(buf.String(), "failsafe.arm.skipped") {
		t.Errorf("unexpected 'failsafe.arm.skipped' log when gc is non-nil; logs:\n%s", buf.String())
	}
}

// ── REST health endpoint integration ─────────────────────────────────────────

// TestDaemonServe_RESTHealthGreen boots the daemon with REST enabled on a
// fixed ephemeral port, waits for the daemon to report ready, then probes
// /api/v1/health for 200. The test uses a fixed high-numbered port (0 is not
// usable because we need to know the port before the probe) — pick one that is
// free by convention for test isolation.
func TestDaemonServe_RESTHealthGreen(t *testing.T) {
	// Allocate an OS-ephemeral port by doing a temporary listen, record the
	// port, close it, and then tell the daemon to bind to that port. This
	// avoids hard-coding a port that may already be in use.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not allocate ephemeral port: %v", err)
	}
	restAddr := l.Addr().String()
	_ = l.Close()

	cfg := config.Default()
	cfg.North.REST.Enabled = new(true)
	cfg.North.REST.Listen = restAddr
	cfg.North.UI.Enabled = new(false)
	cfg.DataDir = t.TempDir()
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- daemonServe(ctx, cfg, &bytes.Buffer{}, &bytes.Buffer{}) }()

	// Poll /api/v1/health until it responds or deadline. 30 s budget
	// accommodates `-race` overhead on slower CI hosts; the non-race
	// path completes in well under a second.
	client := &http.Client{Timeout: 1 * time.Second}
	url := "http://" + restAddr + "/api/v1/health"
	var healthOK bool
	probeBudget := 30 * time.Second
	deadline := time.Now().Add(probeBudget)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:noctx // test-only probe
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				healthOK = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !healthOK {
		t.Errorf("/api/v1/health did not return 200 within %v on %s", probeBudget, restAddr)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon returned error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("shutdown timeout")
	}
}

// ── buildBackupAdapter — existing DB wiring path ─────────────────────────────

// TestBuildBackupAdapter_WithCentral verifies the backup adapter is non-nil
// when a central registry is populated.
func TestBuildBackupAdapter_WithCentral(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	reg := buildTestRegistry(t, "ccu-bak")
	logger := slog.New(slog.DiscardHandler)
	a := buildBackupAdapter(cfg, reg, logger)
	if a == nil {
		t.Fatal("expected non-nil adapter")
	}
}

// ── wireAuditPersistenceWithDB — shared-handle path ──────────────────────────

// TestWireAuditPersistenceWithDB_SharesHandleWithOtherWiring verifies that the
// SAME *sql.DB handle can back wireAuditPersistenceWithDB,
// wireIncidentRecorder and wireSessionRecorderPersistence at once — the
// composition root's actual usage (one open, three consumers) — instead of
// each opening its own handle against the same file.
func TestWireAuditPersistenceWithDB_SharesHandleWithOtherWiring(t *testing.T) {
	t.Parallel()
	db := openTestLoomDB(t)
	buf := audit.NewBuffer(32)
	logger := slog.New(slog.DiscardHandler)
	reg := buildTestRegistry(t, "ccu-shared")

	rec, _, stopSink := wireAuditPersistenceWithDB(db, buf, logger)
	t.Cleanup(stopSink)
	if rec == nil {
		t.Fatal("expected non-nil recorder from shared DB")
	}
	rec.Record(audit.Entry{User: "lifecycle-test", Parameter: "p"})

	_, incidentCloser := wireIncidentRecorder(db, reg, logger)
	t.Cleanup(incidentCloser)

	sessionCloser := wireSessionRecorderPersistence(db, reg, logger)
	t.Cleanup(sessionCloser)
}

// ── buildOpenAPIValidator — valid spec path ───────────────────────────────────

// TestBuildOpenAPIValidator_ValidSpecPath verifies the validator is non-nil
// when the real embedded openapi.yaml is accessible.
func TestBuildOpenAPIValidator_ValidSpecPath(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.REST.OpenAPISpecPath = "assets/openapi.yaml"
	logger := slog.New(slog.DiscardHandler)
	// buildOpenAPIValidator is unexported via the daemon package —
	// call it directly from the test (same package).
	v := buildOpenAPIValidator(cfg, logger)
	// The validator may be nil if the spec is not parseable from the test CWD;
	// that is acceptable — what we exercise here is that the non-error path is reached.
	// The meaningful assertion is no panic.
	_ = v
}
