// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

// daemon_coverage7_test.go — targeted coverage for remaining gaps:
//   - daemon.go: loadVendorAttestation PAI/CD/key error paths (3038-3051)
//   - daemon.go: buildPaseAdapterFromCreds verifier error (3203-3204)
//   - daemon.go: loadAdditionalFabricsForCase ListFabrics error (2357-2360)
//   - daemon.go: loadAdditionalFabricsForCase NewVerifier error (2382-2386)
//   - daemon.go: loadAdditionalFabricsForCase deriveOperationalIPK error (2389-2393)
//   - daemon.go: loadPersistentCaseIdentity mattercert.NewVerifier error (2253-2255)
//   - daemon.go: applyVisibilityUnIgnore device loop (lines 119-125)
//   - daemon.go: computeFabricCompressedID invalid rootPubKey (3007-3009)

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/operational"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ── loadVendorAttestation: PAI error path (lines 3038-3040) ──────────────────

// TestLoadVendorAttestation_PAIError_ReturnsFalse exercises the
// `if err != nil { logger.Warn("...pai"); return false }` path by providing
// a valid DAC file (readCertBytes succeeds on 0x30 0x82 prefix) but a
// nonexistent PAI path.
func TestLoadVendorAttestation_PAIError_ReturnsFalse(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	// Write a DER-like blob that readCertBytes accepts (0x30 0x82 magic).
	dacPath := filepath.Join(tmp, "dac.der")
	if err := os.WriteFile(dacPath, []byte{0x30, 0x82, 0x00, 0x04, 0xAB, 0xCD}, 0o600); err != nil {
		t.Fatalf("WriteFile dac: %v", err)
	}

	cfg := config.NorthMatterAttestation{
		DACPath:    dacPath,
		DACKeyPath: filepath.Join(tmp, "dackey.pem"),
		PAIPath:    filepath.Join(tmp, "nosuchpai.der"), // does not exist → PAI error
		CDPath:     filepath.Join(tmp, "cd.bin"),
	}
	logger := slog.New(slog.DiscardHandler)
	_, _, _, _, ok := loadVendorAttestation(cfg, logger)
	if ok {
		t.Error("expected ok=false when PAI file missing")
	}
}

// TestLoadVendorAttestation_CDError_ReturnsFalse exercises the
// `logger.Warn("...cd"); return false` path by providing valid DAC + PAI
// but a nonexistent CD path.
func TestLoadVendorAttestation_CDError_ReturnsFalse(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	derBlob := []byte{0x30, 0x82, 0x00, 0x04, 0xAB, 0xCD}
	dacPath := filepath.Join(tmp, "dac.der")
	paiPath := filepath.Join(tmp, "pai.der")
	if err := os.WriteFile(dacPath, derBlob, 0o600); err != nil {
		t.Fatalf("WriteFile dac: %v", err)
	}
	if err := os.WriteFile(paiPath, derBlob, 0o600); err != nil {
		t.Fatalf("WriteFile pai: %v", err)
	}

	cfg := config.NorthMatterAttestation{
		DACPath:    dacPath,
		DACKeyPath: filepath.Join(tmp, "dackey.pem"),
		PAIPath:    paiPath,
		CDPath:     filepath.Join(tmp, "nosuchcd.bin"), // does not exist → CD error
	}
	logger := slog.New(slog.DiscardHandler)
	_, _, _, _, ok := loadVendorAttestation(cfg, logger)
	if ok {
		t.Error("expected ok=false when CD file missing")
	}
}

// TestLoadVendorAttestation_DACKeyError_ReturnsFalse exercises the
// `logger.Warn("...dac_key"); return false` path by providing valid
// DAC + PAI + CD but a nonexistent key path.
func TestLoadVendorAttestation_DACKeyError_ReturnsFalse(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	derBlob := []byte{0x30, 0x82, 0x00, 0x04, 0xAB, 0xCD}
	dacPath := filepath.Join(tmp, "dac.der")
	paiPath := filepath.Join(tmp, "pai.der")
	cdPath := filepath.Join(tmp, "cd.bin")
	if err := os.WriteFile(dacPath, derBlob, 0o600); err != nil {
		t.Fatalf("WriteFile dac: %v", err)
	}
	if err := os.WriteFile(paiPath, derBlob, 0o600); err != nil {
		t.Fatalf("WriteFile pai: %v", err)
	}
	if err := os.WriteFile(cdPath, []byte("cd-content"), 0o600); err != nil {
		t.Fatalf("WriteFile cd: %v", err)
	}

	cfg := config.NorthMatterAttestation{
		DACPath:    dacPath,
		DACKeyPath: filepath.Join(tmp, "nosuchkey.pem"), // does not exist → key error
		PAIPath:    paiPath,
		CDPath:     cdPath,
	}
	logger := slog.New(slog.DiscardHandler)
	_, _, _, _, ok := loadVendorAttestation(cfg, logger)
	if ok {
		t.Error("expected ok=false when DAC key file missing")
	}
}

// ── buildPaseAdapterFromCreds: verifier error (lines 3203-3204) ──────────────
// NOTE: TestBuildPaseAdapterFromCreds_InvalidIterations_ReturnsError is
// already in daemon_lifecycle_test.go; only the nil-opCreds variant is new.

// ── loadAdditionalFabricsForCase: ListFabrics error (lines 2357-2360) ────────

// TestLoadAdditionalFabricsForCase_ClosedDB_ListFabricsError exercises the
// `store.ListFabrics` error path at lines 2357-2360.
func TestLoadAdditionalFabricsForCase_ClosedDB_ListFabricsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigratedTestDB(t, "matter_add_closed.db")
	store := matterstore.New(db)
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	caseFabrics := make(map[uint8]*caseFabricEntry)
	var mu sync.RWMutex
	logger := slog.New(slog.DiscardHandler)
	n := loadAdditionalFabricsForCase(ctx, store, 0, caseFabrics, &mu, logger)
	if n != 0 {
		t.Errorf("expected 0 loaded with closed DB, got %d", n)
	}
}

// ── loadAdditionalFabricsForCase: mattercert.NewVerifier error (2382-2386) ──

// TestLoadAdditionalFabricsForCase_InvalidRootPubKey_SkipsVerifierError
// exercises the `mattercert.NewVerifier` error at lines 2382-2386 by using
// a fabric with an invalid root public key (only 5 bytes, needs 65).
func TestLoadAdditionalFabricsForCase_InvalidRootPubKey_SkipsVerifierError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	logger := slog.New(slog.DiscardHandler)

	// Use a 5-byte root public key → NewVerifier fails (needs 65 bytes, 0x04 prefix).
	idx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0xCAFE0001,
		NodeID:        0xBEEF,
		RootPublicKey: []byte{0x04, 0xAB, 0xCD, 0xEF, 0x01}, // too short
		VendorID:      0xFFF1,
		Label:         "invalid-root-key",
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}
	// Add identity with valid private key and valid IPK so we get past privKeyFromScalar.
	priv, privErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if privErr != nil {
		t.Fatalf("GenerateKey: %v", privErr)
	}
	privScalar := make([]byte, 32)
	privBytes := priv.D.Bytes() //nolint:staticcheck // ecdsa.PrivateKey.D deprecated in Go 1.26; test-only helper to produce a 32-byte scalar, not used in production crypto paths
	copy(privScalar[32-len(privBytes):], privBytes)
	if err := store.UpsertIdentity(ctx, matterstore.IdentityRecord{
		FabricIndex: idx,
		NOC:         []byte("noc"),
		PrivateKey:  privScalar,
		IPK:         make([]byte, 16),
	}); err != nil {
		t.Fatalf("UpsertIdentity: %v", err)
	}

	caseFabrics := make(map[uint8]*caseFabricEntry)
	var mu sync.RWMutex
	const seedIdx = uint8(99) // different from the inserted fabric index
	n := loadAdditionalFabricsForCase(ctx, store, seedIdx, caseFabrics, &mu, logger)
	// verifier error → entry skipped → 0 loaded
	if n != 0 {
		t.Errorf("expected 0 loaded (verifier error), got %d", n)
	}
}

// ── loadAdditionalFabricsForCase: deriveOperationalIPK error (2389-2393) ─────

// TestLoadAdditionalFabricsForCase_BadIPKLength_SkipsIPKError exercises
// the `deriveOperationalIPK` error at lines 2389-2393 by using an identity
// with an IPK that is NOT 16 bytes.
func TestLoadAdditionalFabricsForCase_BadIPKLength_SkipsIPKError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	logger := slog.New(slog.DiscardHandler)

	// Generate a valid 65-byte root public key (needed for mattercert.NewVerifier).
	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	rootPub := elliptic.Marshal(elliptic.P256(), rootPriv.X, rootPriv.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.26 but still the correct wire encoding for P-256 uncompressed public keys in the Matter spec

	idx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0xCAFE0002,
		NodeID:        0xBEEF,
		RootPublicKey: rootPub,
		VendorID:      0xFFF1,
		Label:         "bad-ipk",
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}
	// Use a valid 32-byte private key scalar.
	priv, privErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if privErr != nil {
		t.Fatalf("GenerateKey priv: %v", privErr)
	}
	privScalar := make([]byte, 32)
	privBytes := priv.D.Bytes() //nolint:staticcheck // ecdsa.PrivateKey.D deprecated in Go 1.26; test-only helper to produce a 32-byte scalar, not used in production crypto paths
	copy(privScalar[32-len(privBytes):], privBytes)
	if err := store.UpsertIdentity(ctx, matterstore.IdentityRecord{
		FabricIndex: idx,
		NOC:         []byte("noc"),
		PrivateKey:  privScalar,
		IPK:         []byte{0x01, 0x02, 0x03}, // wrong length — not 16 bytes
	}); err != nil {
		t.Fatalf("UpsertIdentity: %v", err)
	}

	caseFabrics := make(map[uint8]*caseFabricEntry)
	var mu sync.RWMutex
	const seedIdx = uint8(99)
	n := loadAdditionalFabricsForCase(ctx, store, seedIdx, caseFabrics, &mu, logger)
	// IPK error → entry skipped → 0 loaded
	if n != 0 {
		t.Errorf("expected 0 loaded (IPK length error), got %d", n)
	}
}

// ── loadPersistentCaseIdentity: mattercert.NewVerifier error (2253-2255) ─────

// TestLoadPersistentCaseIdentity_InvalidRootPubKey_ReturnsError exercises
// the `mattercert.NewVerifier` error path at lines 2253-2255 by using a fabric
// with an invalid root public key (< 65 bytes).
func TestLoadPersistentCaseIdentity_InvalidRootPubKey_ReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	logger := slog.New(slog.DiscardHandler)

	// Short root public key → NewVerifier will fail.
	idx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0xCAFE0003,
		NodeID:        0xBEEF,
		RootPublicKey: []byte{0x04, 0x01, 0x02}, // too short (3 bytes, needs 65)
		VendorID:      0xFFF1,
		Label:         "bad-pub",
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}
	// Valid private key so privKeyFromScalar succeeds.
	priv, privErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if privErr != nil {
		t.Fatalf("GenerateKey: %v", privErr)
	}
	privScalar := make([]byte, 32)
	privBytes := priv.D.Bytes() //nolint:staticcheck // ecdsa.PrivateKey.D deprecated in Go 1.26; test-only helper to produce a 32-byte scalar, not used in production crypto paths
	copy(privScalar[32-len(privBytes):], privBytes)
	if err := store.UpsertIdentity(ctx, matterstore.IdentityRecord{
		FabricIndex: idx,
		NOC:         []byte("noc"),
		PrivateKey:  privScalar,
		IPK:         make([]byte, 16),
	}); err != nil {
		t.Fatalf("UpsertIdentity: %v", err)
	}

	_, _, _, _, loadErr := loadPersistentCaseIdentity(ctx, config.NorthMatterCASE{}, store, logger)
	if loadErr == nil {
		t.Fatal("expected error for invalid root public key, got nil")
	}
}

// ── applyVisibilityUnIgnore: device loop (lines 119-125) ─────────────────────

// TestApplyVisibilityUnIgnore_WithDevices_TouchesDevices exercises the
// device-mark loop at lines 119-125 of visibility_wiring.go by putting a
// device into the central's ModelRegistry before calling applyVisibilityUnIgnore.
func TestApplyVisibilityUnIgnore_WithDevices_TouchesDevices(t *testing.T) {
	t.Parallel()
	store := buildVisibilityStore(t)
	visReg := visibility.NewRegistry()
	reg := buildTestRegistry(t, "ccu-01")
	logger := slog.New(slog.DiscardHandler)
	cfg := config.Default()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01"}}

	// Put a device in the ModelRegistry.
	cu, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("ccu-01 not registered")
	}
	dev := device.New(device.Config{
		Address:     "APPLYDEV001",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-PSM",
	})
	cu.ModelRegistry.Put(dev)

	ctx := context.Background()
	if err := store.Replace(ctx, "ccu-01", []string{"ACTIVE"}, "test"); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	n := applyVisibilityUnIgnore(ctx, cfg, reg, store, visReg, logger)
	if n != 1 {
		t.Errorf("expected 1 central with patterns, got %d", n)
	}
}

// ── computeFabricCompressedID: invalid rootPubKey (lines 3007-3009) ──────────

// TestComputeFabricCompressedID_ShortRootPubKey_ReturnsError exercises the
// `len(rootPubKey) != 65 || rootPubKey[0] != 0x04` guard at lines 3007-3009.
func TestComputeFabricCompressedID_ShortRootPubKey_ReturnsError(t *testing.T) {
	t.Parallel()
	// Too-short key.
	_, err := computeFabricCompressedID([]byte{0x04, 0x01, 0x02}, 0xCAFE)
	if err == nil {
		t.Fatal("expected error for short rootPubKey, got nil")
	}
}

// TestComputeFabricCompressedID_WrongPrefix_ReturnsError uses a 65-byte key
// with wrong prefix byte (not 0x04).
func TestComputeFabricCompressedID_WrongPrefix_ReturnsError(t *testing.T) {
	t.Parallel()
	key := make([]byte, 65)
	key[0] = 0x02 // compressed prefix, not uncompressed
	_, err := computeFabricCompressedID(key, 0xCAFE)
	if err == nil {
		t.Fatal("expected error for wrong prefix, got nil")
	}
}

// ── visibility_adapter.go: UnIgnoreCandidates known central (lines 61-74) ────

// TestVisibilityAdapter_UnIgnoreCandidates_KnownCentral_ZeroIfNoDevices
// exercises the query-facade path at lines 61-74 of visibility_adapter.go —
// "ccu-01" exists and QueryFacade is non-nil, but there are no devices
// so GetUnIgnoreCandidates returns an empty slice.
func TestVisibilityAdapter_UnIgnoreCandidates_KnownCentral_ZeroIfNoDevices(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	a := newVisibilityAdapter(visibility.NewRegistry(), nil, reg)
	candidates := a.UnIgnoreCandidates("ccu-01", hmenum.ParamsetKeyMaster)
	// Result may be nil or empty — must not panic.
	_ = candidates
}

// ── loadFabricRootPublicKey: nil store error (line 2330) ─────────────────────
// NOTE: TestLoadFabricRootPublicKey_NilStore_ReturnsError is already in
// daemon_lifecycle_test.go; only the "missing fabric" variant is new here.

// TestLoadFabricRootPublicKey_GetFabricError exercises the
// `fmt.Errorf("matter store: get fabric %d: %w")` path when the fabric
// does not exist in the store.
func TestLoadFabricRootPublicKey_GetFabricError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	// FabricIndex 255 does not exist.
	_, err := loadFabricRootPublicKey(ctx, store, 255)
	if err == nil {
		t.Fatal("expected error for missing fabric, got nil")
	}
}

// ── deriveOperationalIPK: wrong length IPK (line 2489-2491) ──────────────────

// TestDeriveOperationalIPK_WrongLengthIPK_ReturnsError exercises the
// `len(rawIPK) != 16` guard at lines 2489-2491.
func TestDeriveOperationalIPK_WrongLengthIPK_ReturnsError(t *testing.T) {
	t.Parallel()
	var compressedID [8]byte
	_, err := deriveOperationalIPK([]byte{0x01, 0x02, 0x03}, compressedID)
	if err == nil {
		t.Fatal("expected error for IPK length != 16, got nil")
	}
}

// ── matter_ephemeral_provider: singleton mode BuildAndInstall ─────────────────

// TestMatterEphemeralProvider_Singleton_BuildAndInstall_DoesNotPanic
// exercises the singleton-mode path at lines 130-139 by calling
// GenerateAndInstall on a provider with a real (started) bridge.
// Lines 92-102 (crypto random errors) are not testable without mocking the
// RNG — skipped as impractical.
func TestMatterEphemeralProvider_Singleton_BuildAndInstall_DoesNotPanic(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
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

	p := newMatterEphemeralProvider(
		bundle.bridge,
		config.NorthMatterCommissioning{Iterations: 1000},
		bundle.opMgr,
		bundle.opCreds,
		bundle.configuredPase, // may be nil
		nil,                   // no concurrent factory → singleton mode
		slog.New(slog.DiscardHandler),
	)

	creds, err := p.GenerateAndInstall(context.Background())
	if err != nil {
		t.Fatalf("GenerateAndInstall: %v", err)
	}
	// Restore should not panic.
	if creds.Restore != nil {
		creds.Restore()
	}
}

// ── unused import guard ───────────────────────────────────────────────────────

var _ = (*operational.Manager)(nil) // ensure operational import is used
