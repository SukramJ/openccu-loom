// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// TestResolveBridgeUniqueIDStableAcrossRename pins Matter §11.1.5.13 quality F:
// BasicInformation.UniqueID must not change once the bridge is commissioned. A
// bridge rename (a node_label change, which also moves the derived serial) must
// leave the persisted-and-pinned UniqueID untouched, or every bridged accessory
// looks new to Apple Home / Google Home and has to be re-paired.
func TestResolveBridgeUniqueIDStableAcrossRename(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := matterstore.New(openTestLoomDB(t))
	logger := slog.New(slog.DiscardHandler)

	mc := config.NorthMatter{VendorID: 0xFFF1, ProductID: 0x8000, NodeLabel: "openccu-loom"}
	const rootSerial1 = "aaaabbbbccccdddd"

	// First boot: nothing persisted, so seed from the current derivation and
	// store it. The seed must equal what an un-pinned BasicInformation derives,
	// so upgrading an already-commissioned bridge keeps its identity.
	first := resolveBridgeUniqueID(ctx, store, mc, rootSerial1, logger)
	if first == "" {
		t.Fatal("resolveBridgeUniqueID returned empty on first boot")
	}
	if want := mattercore.DeriveUniqueID(mc.VendorID, mc.ProductID, mc.NodeLabel, rootSerial1); first != want {
		t.Fatalf("first-boot seed = %q, want the un-pinned derivation %q", first, want)
	}

	// The bridge is renamed: node_label changes and the derived serial with it.
	renamed := mc
	renamed.NodeLabel = "Living Room"
	const rootSerial2 = "1111222233334444"

	// Guard against an inert test: the renamed inputs must derive a DIFFERENT
	// value, so stability can only come from the persisted pin.
	if moved := mattercore.DeriveUniqueID(renamed.VendorID, renamed.ProductID, renamed.NodeLabel, rootSerial2); moved == first {
		t.Fatal("test is inert: the renamed derivation coincidentally equals the original")
	}

	second := resolveBridgeUniqueID(ctx, store, renamed, rootSerial2, logger)
	if second != first {
		t.Errorf("UniqueID changed across rename: %q -> %q (Matter §11.1.5.13 quality F violated)", first, second)
	}
}

// TestResolveBridgeUniqueIDNilStore covers the no-persistence path: without a
// store the value is derived but not pinned, matching the un-pinned behaviour.
func TestResolveBridgeUniqueIDNilStore(t *testing.T) {
	t.Parallel()
	mc := config.NorthMatter{VendorID: 0xFFF1, ProductID: 0x8000, NodeLabel: "x"}
	got := resolveBridgeUniqueID(context.Background(), nil, mc, "serial", slog.New(slog.DiscardHandler))
	if want := mattercore.DeriveUniqueID(mc.VendorID, mc.ProductID, mc.NodeLabel, "serial"); got != want {
		t.Fatalf("nil-store UniqueID = %q, want the derived value %q", got, want)
	}
}

// TestBuildRootClusters_WithLiveBridge exercises buildRootClusters with a
// live bridge instance and non-nil store to cover the store-guarded branches
// (AccessControl, OperationalCredentials, OpCreds callback, etc.).
func TestBuildRootClusters_WithLiveBridge(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.NodeLabel = "test"
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx := t.Context()
	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start")
	}
	t.Cleanup(bundle.stop)

	// buildRootClusters is indirectly called inside startMatterBridge.
	// Call it directly here with a live bridge + live store to cover the
	// additional store-guarded code paths (AccessControl, OperationalCredentials).
	clusters, opCreds, refs, err := buildRootClusters(
		context.Background(),
		cfg.North.Matter,
		bundle.store,
		bundle.bridge,
		nil,
		slog.New(slog.DiscardHandler),
		func(_ context.Context, _ uint8, _, _ uint64, _ []byte) {},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("buildRootClusters: %v", err)
	}
	if len(clusters) == 0 {
		t.Error("expected non-empty clusters slice")
	}
	if opCreds == nil {
		t.Error("expected non-nil OperationalCredentials when store is non-nil")
	}
	if refs.BasicInformation == nil {
		t.Error("expected non-nil BasicInformation ref")
	}
	if refs.GeneralCommissioning == nil {
		t.Error("expected non-nil GeneralCommissioning ref")
	}
}

// TestBuildRootClusters_NilStore exercises buildRootClusters without a store
// to cover the nil-store path (no AccessControl / OperationalCredentials).
func TestBuildRootClusters_NilStore(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.NodeLabel = "test"
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

	// nil store → store-guarded branches are skipped.
	clusters, opCreds, refs, err := buildRootClusters(
		context.Background(),
		cfg.North.Matter,
		nil, // nil store
		bundle.bridge,
		nil,
		slog.New(slog.DiscardHandler),
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("buildRootClusters with nil store: %v", err)
	}
	if len(clusters) == 0 {
		t.Error("expected non-empty clusters slice")
	}
	// opCreds is nil when store is nil.
	if opCreds != nil {
		t.Error("expected nil OperationalCredentials when store is nil")
	}
	if refs.BasicInformation == nil {
		t.Error("expected non-nil BasicInformation ref")
	}
}
