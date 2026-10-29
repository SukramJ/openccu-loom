// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestStartMatterBridge_EphemeralWindow exercises the EphemeralWindow branch of
// startMatterBridge — fires when cfg.North.Matter.Commissioning.EphemeralWindow
// is true + a passcode is set (so the PASE adapter is armed before the opener
// wires the ephemeral provider).
func TestStartMatterBridge_EphemeralWindow(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
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
	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, logger, nil)
	if bundle == nil {
		t.Fatal("expected non-nil bundle with ephemeral window enabled")
	}
	t.Cleanup(bundle.stop)

	if bundle.node.Bridge().LocalAddr() == "" {
		t.Error("expected non-empty LocalAddr")
	}
}

// TestStartMatterBridge_EphemeralWindowConcurrent exercises the
// EphemeralWindow + ConcurrentPairings branch.
func TestStartMatterBridge_EphemeralWindowConcurrent(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
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
	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, logger, nil)
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
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.North.Matter.VendorID = 0xFFF1
	cfg.North.Matter.ProductID = 0x8000
	cfg.North.Matter.DevRotateUniqueIDs = true
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)
	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, logger, nil)
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
	if got := startMatterBridge(ctx, nil, reg, nil, health.NewTracker(), nil, logger, nil); got != nil {
		t.Error("expected nil for nil config")
		got.stop()
	}
}

// ── announcePersistedFabric with actual fabrics ───────────────────────────────
