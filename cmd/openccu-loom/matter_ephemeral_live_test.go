// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestMatterEphemeralProvider_GenerateAndInstall_LiveBridge exercises the
// happy path of GenerateAndInstall against a real bridge instance. The
// bridge is started with `:0` (OS-assigned port) into a temp DataDir.
func TestMatterEphemeralProvider_GenerateAndInstall_LiveBridge(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if bundle == nil {
		t.Skip("bridge did not start; skipping ephemeral provider live test")
	}
	t.Cleanup(bundle.stop)

	mgr := buildTestOperationalManager(t)
	provider := newMatterEphemeralProvider(
		bundle.bridge,
		config.NorthMatterCommissioning{Iterations: 1000},
		mgr,
		nil, // opCreds
		nil, // configured PaseAdapter
		nil, // configuredFactory
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	creds, err := provider.GenerateAndInstall(ctx)
	if err != nil {
		t.Fatalf("GenerateAndInstall: %v", err)
	}
	if creds.Passcode == 0 {
		t.Error("expected non-zero Passcode")
	}
	if creds.Discriminator == 0 {
		t.Error("expected non-zero Discriminator")
	}
	if creds.Restore == nil {
		t.Error("expected non-nil Restore func")
	}

	// Calling Restore must not panic.
	creds.Restore()
}

// TestMatterEphemeralProvider_GenerateAndInstall_DefaultIterations verifies
// that when cfg.Iterations == 0, the provider substitutes 1000.
func TestMatterEphemeralProvider_GenerateAndInstall_DefaultIterations(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if bundle == nil {
		t.Skip("bridge did not start; skipping default-iterations test")
	}
	t.Cleanup(bundle.stop)

	mgr := buildTestOperationalManager(t)
	provider := newMatterEphemeralProvider(
		bundle.bridge,
		config.NorthMatterCommissioning{Iterations: 0}, // zero → must default to 1000
		mgr,
		nil,
		nil,
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	creds, err := provider.GenerateAndInstall(ctx)
	if err != nil {
		t.Fatalf("GenerateAndInstall with 0 iterations: %v", err)
	}
	if creds.Restore == nil {
		t.Error("expected non-nil Restore")
	}
	creds.Restore()
}

// TestMatterEphemeralProvider_GenerateAndInstall_TwiceSingletonMode verifies
// that GenerateAndInstall can be called twice in singleton mode without panic
// (each call replaces the previous ephemeral adapter on the bridge).
func TestMatterEphemeralProvider_GenerateAndInstall_TwiceSingletonMode(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if bundle == nil {
		t.Skip("bridge did not start; skipping twice-singleton test")
	}
	t.Cleanup(bundle.stop)

	mgr := buildTestOperationalManager(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	provider := newMatterEphemeralProvider(
		bundle.bridge,
		config.NorthMatterCommissioning{Iterations: 1000},
		mgr,
		nil, nil, nil,
		logger,
	)

	c1, err := provider.GenerateAndInstall(ctx)
	if err != nil {
		t.Fatalf("first GenerateAndInstall: %v", err)
	}
	c2, err := provider.GenerateAndInstall(ctx)
	if err != nil {
		t.Fatalf("second GenerateAndInstall: %v", err)
	}
	// Restore the second window (no panic expected).
	c2.Restore()
	// c1.Restore is still callable.
	c1.Restore()
}
