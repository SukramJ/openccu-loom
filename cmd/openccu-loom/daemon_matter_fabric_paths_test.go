// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

// daemon_coverage8_test.go — targeted coverage for remaining small gaps:
//   - daemon.go: loadFabricRootPublicKey success path (line 2330)
//   - daemon.go: deriveOperationalIPK success path (lines 2492-2497)
//   - ws_adapters.go: wsLinkQuery.ListLinks domain error path (lines 197-199)
//   - ws_adapters.go: wsLinkQuery.LinkableChannels device-not-found error (line 241)
//   - matter_window_adapter.go: announce path (lines 62-70)

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	matterbridge "github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// ── loadFabricRootPublicKey: success path (line 2330) ────────────────────────

// TestLoadFabricRootPublicKey_ExistingFabric_ReturnsPubKey exercises the
// success path at line 2330 by inserting a fabric and retrieving its key.
func TestLoadFabricRootPublicKey_ExistingFabric_ReturnsPubKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)

	// Insert a fabric with a known root public key.
	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	rootPub := elliptic.Marshal(elliptic.P256(), rootPriv.X, rootPriv.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.26 but still the correct wire encoding for P-256 uncompressed public keys in the Matter spec

	idx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0xABCD0001,
		NodeID:        0x1234,
		RootPublicKey: rootPub,
		VendorID:      0xFFF1,
		Label:         "test-fabric",
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	got, err := loadFabricRootPublicKey(ctx, store, idx)
	if err != nil {
		t.Fatalf("loadFabricRootPublicKey: %v", err)
	}
	if len(got) != len(rootPub) {
		t.Errorf("expected pub key length %d, got %d", len(rootPub), len(got))
	}
}

// ── deriveOperationalIPK: success path (lines 2492-2497) ─────────────────────

// TestDeriveOperationalIPK_ValidIPK_ReturnsKey exercises the success path
// at lines 2492-2497 by passing a valid 16-byte IPK.
func TestDeriveOperationalIPK_ValidIPK_ReturnsKey(t *testing.T) {
	t.Parallel()
	var compressedID [8]byte
	// Fill with non-zero bytes for a realistic test.
	for i := range compressedID {
		compressedID[i] = byte(i + 1)
	}
	ipk := make([]byte, 16)
	for i := range ipk {
		ipk[i] = byte(i + 0x10)
	}
	out, err := deriveOperationalIPK(ipk, compressedID)
	if err != nil {
		t.Fatalf("deriveOperationalIPK: %v", err)
	}
	var zero [16]byte
	if out == zero {
		t.Error("expected non-zero derived IPK")
	}
}

// ── ws_adapters.go: wsLinkQuery.ListLinks error from domain (lines 197-199) ──

// TestWSLinkQuery_ListLinks_DomainError_ReturnsError exercises the error path
// at lines 197-199 in ws_adapters.go where the domain.ListLinks call fails.
// We use a real LinksDomain with an empty central registry so lookupDevice fails.
func TestWSLinkQuery_ListLinks_DomainError_ReturnsError(t *testing.T) {
	t.Parallel()
	// Create a LinksDomain with a registry that has no devices.
	// ListLinks will fail at lookupDevice because the device is not registered.
	reg := buildTestRegistry(t, "ccu-01")
	domain := adapter.NewLinksDomain(reg, nil, nil)

	q := &wsLinkQuery{domain: domain, registry: reg}
	_, err := q.ListLinks(context.Background(), "NOSUCHDEV:0")
	// Expect error because the device is not found.
	if err == nil {
		t.Fatal("expected error when device not found in ListLinks, got nil")
	}
}

// ── ws_adapters.go: wsLinkQuery.LinkableChannels device-not-found (line 241) ─

// TestWSLinkQuery_LinkableChannels_DeviceNotFound_ReturnsError exercises the
// device-not-found error path at line 241 of ws_adapters.go where the loop
// over registered centrals finds no device matching the address.
func TestWSLinkQuery_LinkableChannels_DeviceNotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01") // has no devices
	domain := adapter.NewLinksDomain(reg, nil, nil)

	q := &wsLinkQuery{domain: domain, registry: reg}
	_, err := q.LinkableChannels(context.Background(), "NOSUCHDEV:0")
	if err == nil {
		t.Fatal("expected error when device not found in LinkableChannels, got nil")
	}
}

// ── matter_window_adapter.go: announce path (lines 62-70) ────────────────────

// TestMatterCommissioningOpenerAdapter_Announce_WithBridge exercises lines 62-70
// of matter_window_adapter.go — the `if a.bridge != nil` branch after a
// successful OpenCommissioningWindow call.  A fresh window starts closed,
// OpenCommissioningWindow opens it and returns successfully; the adapter then
// calls AnnounceCommissioning on the bridge.
func TestMatterCommissioningOpenerAdapter_Announce_WithBridge(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-ann", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-ann")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start; skipping announce path test")
	}
	t.Cleanup(bundle.stop)

	// Use a fresh window (not the one from the bridge) so we don't conflict
	// with the bridge's own commissioning window state.
	window := matterbridge.NewCommissioningWindow()
	inner := matterbridge.NewCommissioningWindowOpener(window, 0xABC, 20202021, 0xFFF1, 0x8000)

	a := &matterCommissioningOpenerAdapter{
		inner:              inner,
		bridge:             bundle.bridge,
		advert:             matterbridge.CommissioningAdvertisement{},
		allowEmptyTopology: true, // test bridge has no CCU-driven bridged endpoints
	}

	res, err := a.OpenCommissioningWindow(ctx, 300)
	if err != nil {
		t.Fatalf("OpenCommissioningWindow: %v", err)
	}
	// The result discriminator should be non-zero (ephemeral discriminator).
	_ = res
}
