// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

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
