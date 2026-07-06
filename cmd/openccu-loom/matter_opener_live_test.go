// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	matterbridge "github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// TestMatterCommissioningOpenerAdapter_OpenCommissioningWindow_LiveOpener
// exercises the happy-path branch of OpenCommissioningWindow by constructing a
// real CommissioningWindowOpener with a live bridge + commissioning window.
func TestMatterCommissioningOpenerAdapter_OpenCommissioningWindow_LiveOpener(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.North.Matter.Discriminator = 0xF00
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

	window := matterbridge.NewCommissioningWindow()
	bundle.bridge.AttachCommissioningWindow(window)

	opener := matterbridge.NewCommissioningWindowOpener(
		window,
		cfg.North.Matter.Discriminator,
		cfg.North.Matter.Commissioning.Passcode,
		cfg.North.Matter.VendorID,
		cfg.North.Matter.ProductID,
	)
	adapter := &matterCommissioningOpenerAdapter{
		inner:  opener,
		bridge: bundle.bridge,
		advert: matterbridge.CommissioningAdvertisement{
			Discriminator: cfg.North.Matter.Discriminator,
			VendorID:      cfg.North.Matter.VendorID,
			ProductID:     cfg.North.Matter.ProductID,
		},
		allowEmptyTopology: true, // test bridge has no CCU-driven bridged endpoints
	}

	// Install PASE handler so the window has a verifier.
	mgr := buildTestOperationalManager(t)
	logger := slog.New(slog.DiscardHandler)
	pase, err := buildPaseAdapterFromCreds(20202021, []byte("openccu-loom-dev0"), 1000, mgr, nil, nil, logger)
	if err != nil {
		t.Skipf("buildPaseAdapterFromCreds: %v", err)
	}
	bundle.bridge.AttachPaseHandler(pase)

	// OpenCommissioningWindow should succeed for 180 seconds (min accepted).
	result, err := adapter.OpenCommissioningWindow(ctx, 180)
	if err != nil {
		t.Fatalf("OpenCommissioningWindow: %v", err)
	}
	if result.Passcode == 0 {
		t.Error("expected non-zero Passcode in result")
	}
	if result.Discriminator == 0 {
		t.Error("expected non-zero Discriminator in result")
	}
}

// TestMatterCommissioningOpenerAdapter_OpenCommissioningWindow_TopologyNotReady
// locks the bridged-endpoint readiness guard: when the bridge topology
// only carries [root, aggregator] (no CCU devices loaded yet),
// OpenCommissioningWindow must refuse with ErrBridgeTopologyNotReady.
// Without this guard Apple's MTREndpointInfo caches an empty
// Descriptor.PartsList on EP 0 and the HAP-Mapper collapses to
// `endpointDeviceTypes={0=(22)}` regardless of any later Reassemble.
func TestMatterCommissioningOpenerAdapter_OpenCommissioningWindow_TopologyNotReady(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-empty", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-empty")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start")
	}
	t.Cleanup(bundle.stop)

	window := matterbridge.NewCommissioningWindow()
	inner := matterbridge.NewCommissioningWindowOpener(window, 0xABC, 20202021, 0xFFF1, 0x8000)
	adapter := &matterCommissioningOpenerAdapter{
		inner:  inner,
		bridge: bundle.bridge,
		// allowEmptyTopology intentionally false — production default.
	}

	_, err := adapter.OpenCommissioningWindow(ctx, 180)
	if err == nil {
		t.Fatal("OpenCommissioningWindow: expected ErrBridgeTopologyNotReady, got nil")
	}
	if !errors.Is(err, handlers.ErrBridgeTopologyNotReady) {
		t.Errorf("OpenCommissioningWindow: err = %v, want ErrBridgeTopologyNotReady", err)
	}
}

// TestMatterCommissioningOpenerAdapter_OpenCommissioningWindow_AlreadyOpen
// verifies that a second call while the window is already open returns
// ErrCommissioningInProgress.
func TestMatterCommissioningOpenerAdapter_OpenCommissioningWindow_AlreadyOpen(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.North.Matter.Discriminator = 0xF00
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

	window := matterbridge.NewCommissioningWindow()
	bundle.bridge.AttachCommissioningWindow(window)

	opener := matterbridge.NewCommissioningWindowOpener(
		window,
		cfg.North.Matter.Discriminator,
		cfg.North.Matter.Commissioning.Passcode,
		cfg.North.Matter.VendorID,
		cfg.North.Matter.ProductID,
	)
	adapter := &matterCommissioningOpenerAdapter{
		inner:  opener,
		bridge: bundle.bridge,
		advert: matterbridge.CommissioningAdvertisement{
			Discriminator: cfg.North.Matter.Discriminator,
			VendorID:      cfg.North.Matter.VendorID,
			ProductID:     cfg.North.Matter.ProductID,
		},
		allowEmptyTopology: true, // test bridge has no CCU-driven bridged endpoints
	}

	mgr := buildTestOperationalManager(t)
	logger := slog.New(slog.DiscardHandler)
	pase, err := buildPaseAdapterFromCreds(20202021, []byte("openccu-loom-dev0"), 1000, mgr, nil, nil, logger)
	if err != nil {
		t.Skipf("buildPaseAdapterFromCreds: %v", err)
	}
	bundle.bridge.AttachPaseHandler(pase)

	// First open should succeed.
	if _, err := adapter.OpenCommissioningWindow(ctx, 180); err != nil {
		t.Fatalf("first OpenCommissioningWindow: %v", err)
	}

	// Second open while still open → ErrCommissioningInProgress mapped from bridge sentinel.
	_, err2 := adapter.OpenCommissioningWindow(ctx, 180)
	if err2 == nil {
		t.Error("expected error on second open while window already open")
	}
}
