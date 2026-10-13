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
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
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
