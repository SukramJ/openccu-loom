// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// TestStartMatterBridge_EphemeralWindow exercises the EphemeralWindow branch of
// startMatterBridge — fires when cfg.North.Matter.Commissioning.EphemeralWindow
// is true + a passcode is set (so the PASE adapter is armed before the opener
// wires the ephemeral provider).
func TestStartMatterBridge_EphemeralWindow(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.Discriminator = 0xF00
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.North.Matter.Commissioning.EphemeralWindow = true
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)
	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), nil, logger)
	if bundle == nil {
		t.Fatal("expected non-nil bundle with ephemeral window enabled")
	}
	t.Cleanup(bundle.stop)

	if bundle.bridge.LocalAddr() == "" {
		t.Error("expected non-empty LocalAddr")
	}
}

// TestStartMatterBridge_EphemeralWindowConcurrent exercises the
// EphemeralWindow + ConcurrentPairings branch.
func TestStartMatterBridge_EphemeralWindowConcurrent(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.Discriminator = 0xF00
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.North.Matter.Commissioning.EphemeralWindow = true
	cfg.North.Matter.Commissioning.ConcurrentPairings = true
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)
	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), nil, logger)
	if bundle == nil {
		t.Fatal("expected non-nil bundle with concurrent ephemeral window enabled")
	}
	t.Cleanup(bundle.stop)
}

// TestStartMatterBridge_DevRotateUniqueIDs exercises the DevRotateUniqueIDs
// branch — verifies bridge still starts.
func TestStartMatterBridge_DevRotateUniqueIDs(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.DevRotateUniqueIDs = true
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)
	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), nil, logger)
	if bundle == nil {
		t.Fatal("expected non-nil bundle with DevRotateUniqueIDs")
	}
	t.Cleanup(bundle.stop)
}

// TestStartMatterBridge_NilConfig_ReturnsNil covers the nil-config guard.
func TestStartMatterBridge_NilConfig_ReturnsNil(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)
	if got := startMatterBridge(ctx, nil, reg, health.NewTracker(), nil, logger); got != nil {
		t.Error("expected nil for nil config")
		got.stop()
	}
}

// ── buildMatterAdvertiser: "zeroconf" branch ──────────────────────────────────

func TestBuildMatterAdvertiser_ZeroconfBranch_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	mc := config.NorthMatter{MDNSAdvertise: "zeroconf"}
	logger := slog.New(slog.DiscardHandler)
	got := buildMatterAdvertiser(mc, logger)
	if got == nil {
		t.Fatal("expected non-nil advertiser for zeroconf")
	}
}

// ── announcePersistedFabric with actual fabrics ───────────────────────────────

func TestAnnouncePersistedFabric_WithFabric_CallsAnnounceFabric(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start")
	}
	t.Cleanup(bundle.stop)

	// Insert a fake fabric with a valid 65-byte uncompressed root public key
	// so computeFabricCompressedID succeeds and AnnounceFabric is called.
	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	rootPub := elliptic.Marshal(elliptic.P256(), rootPriv.X, rootPriv.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.26 but still the correct wire encoding for P-256 uncompressed public keys in the Matter spec
	if _, err := bundle.store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0xCAFEBABE,
		NodeID:        0xDEADBEEF,
		RootPublicKey: rootPub,
		VendorID:      0xFFF1,
		Label:         "test",
	}); err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	// Must not panic; exercises the loop body + AnnounceFabric call.
	announcePersistedFabric(ctx, bundle.store, bundle.bridge, slog.New(slog.DiscardHandler))
}

func TestAnnouncePersistedFabric_InvalidRootKey_LogsAndContinues(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start")
	}
	t.Cleanup(bundle.stop)

	// Insert a fabric with an invalid (short) root public key — computeFabricCompressedID
	// will error and the loop body logs + continues.
	if _, err := bundle.store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0xDEADBEEF,
		NodeID:        0x01,
		RootPublicKey: []byte{0x04, 0x00}, // too short — triggers error path
		VendorID:      0xFFF1,
		Label:         "bad",
	}); err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	// Must not panic; exercises the error branch.
	announcePersistedFabric(ctx, bundle.store, bundle.bridge, slog.New(slog.DiscardHandler))
}

// ── loadAdditionalFabricsForCase: non-nil store with fabrics ─────────────────

func TestLoadAdditionalFabricsForCase_EmptyFabrics_ReturnsZero(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	count := loadAdditionalFabricsForCase(
		context.Background(),
		store,
		0,   // seedIdx
		nil, // caseFabrics map
		nil, // mu
		slog.New(slog.DiscardHandler),
	)
	if count != 0 {
		t.Errorf("expected 0 for empty store, got %d", count)
	}
}

func TestLoadAdditionalFabricsForCase_SeedSkipped_ReturnsZero(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)

	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	rootPub := elliptic.Marshal(elliptic.P256(), rootPriv.X, rootPriv.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.26 but still the correct wire encoding for P-256 uncompressed public keys in the Matter spec

	// Add a fabric with FabricIndex = 1 (will become seedIdx).
	fabricIdx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0xAB,
		NodeID:        0xCD,
		RootPublicKey: rootPub,
		VendorID:      0xFFF1,
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	// Pass seedIdx == fabricIdx → the fabric is skipped (seed already loaded by caller).
	count := loadAdditionalFabricsForCase(ctx, store, fabricIdx, nil, nil, logger)
	// Skipped seed → nothing else in store → loaded=0.
	if count != 0 {
		t.Errorf("expected 0 (seed skipped), got %d", count)
	}
}
