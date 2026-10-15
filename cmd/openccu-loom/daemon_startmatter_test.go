// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"log/slog"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

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
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, logger)
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
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, logger)
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
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, logger)
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
	if got := startMatterBridge(ctx, nil, reg, nil, health.NewTracker(), nil, logger); got != nil {
		t.Error("expected nil for nil config")
		got.stop()
	}
}

// ── buildMatterAdvertiser: "zeroconf" branch ──────────────────────────────────

func TestBuildMatterAdvertiser_ZeroconfBranch_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	mc := config.NorthMatter{MDNSAdvertise: "zeroconf"}
	logger := slog.New(slog.DiscardHandler)
	got, closeAdv := buildMatterAdvertiser(mc, logger)
	t.Cleanup(closeAdv)
	if got == nil {
		t.Fatal("expected non-nil advertiser for zeroconf")
	}
	if closeAdv == nil {
		t.Fatal("zeroconf advertiser returned no closer; its subtype responder would outlive the bridge")
	}
}

// TestBuildMatterAdvertiserCloserReleasesTheSubtypeResponder pins that the
// closer actually ends what the advertiser started.
//
// The side-car subtype responder runs a receive goroutine per address family
// on multicast sockets it owns, and the Zeroconf close path only retires the
// subtype NAMES. While nothing called its Close, a bridge that failed to bind
// its UDP port — or any daemon shutdown — left both goroutines and both
// sockets alive for the process lifetime.
//
// Counting by stack keeps the assertion immune to whatever else the test
// binary runs; on a host where the multicast sockets cannot be opened the
// responder never starts and the counts are zero on both sides.
func TestBuildMatterAdvertiserCloserReleasesTheSubtypeResponder(t *testing.T) {
	base := subtypeResponderGoroutines(t)
	_, closeAdv := buildMatterAdvertiser(config.NorthMatter{MDNSAdvertise: "zeroconf"}, slog.New(slog.DiscardHandler))
	closeAdv()

	deadline := time.Now().Add(2 * time.Second)
	for {
		got := subtypeResponderGoroutines(t)
		if got <= base {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d subtype-responder goroutines still running after the closer, want %d", got, base)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// subtypeResponderGoroutines counts the responder's serve loops by stack.
func subtypeResponderGoroutines(t *testing.T) int {
	t.Helper()
	var buf bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&buf, 2); err != nil {
		t.Fatalf("goroutine profile: %v", err)
	}
	return strings.Count(buf.String(), "SubtypeResponder).serve")
}

// ── announcePersistedFabric with actual fabrics ───────────────────────────────
