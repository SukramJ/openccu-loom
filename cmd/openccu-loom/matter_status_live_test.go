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

// TestMatterStatusAdapter_WithLiveBridge_Enabled exercises the
// bridge-non-nil path of MatterStatus, covering the
// LocalAddr / Topology / cfg / window / store branches.
func TestMatterStatusAdapter_WithLiveBridge_Enabled(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start; skipping live bridge test")
	}
	t.Cleanup(bundle.stop)

	adapter := &matterStatusReaderAdapter{
		enabled: true,
		bridge:  bundle.bridge,
		store:   bundle.store,
		window:  nil,
		cfg:     &matterStatusConfig{advertising: true},
	}

	resp := adapter.MatterStatus(context.Background())
	if !resp.Enabled {
		t.Error("expected Enabled=true")
	}
	if !resp.Listening {
		t.Error("expected Listening=true (bridge has bound address)")
	}
	if resp.ListenAddr == "" {
		t.Error("expected non-empty ListenAddr")
	}
	// Advertising is read from cfg when bridge != nil.
	if !resp.Advertising {
		t.Error("expected Advertising=true from matterStatusConfig")
	}
}

// TestMatterStatusAdapter_WithStore_FabricCount exercises the store
// branch in MatterStatus by wiring a store with no fabrics (count=0).
func TestMatterStatusAdapter_WithStore_EmptyFabrics(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Skip("bridge did not start; skipping store test")
	}
	t.Cleanup(bundle.stop)

	adapter := &matterStatusReaderAdapter{
		enabled: true,
		bridge:  bundle.bridge,
		store:   bundle.store,
		window:  nil,
		cfg:     nil,
	}
	resp := adapter.MatterStatus(context.Background())
	// Empty store → FabricCount == 0.
	if resp.FabricCount != 0 {
		t.Errorf("expected FabricCount=0, got %d", resp.FabricCount)
	}
}
