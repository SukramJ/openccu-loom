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

// ── buildMQTT ────────────────────────────────────────────────────────────────

func TestBuildMQTT_Disabled_ReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.MQTT.Enabled = false
	logger := slog.Default()
	if got := buildMQTT(cfg, logger, nil); got != nil {
		t.Errorf("expected nil when MQTT disabled, got %v", got)
	}
}

func TestBuildMQTT_EnabledNoBroker_ReturnsStackWithNoopClient(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.MQTT.Enabled = true
	cfg.North.MQTT.BrokerURL = "" // no broker → noop client
	logger := slog.Default()
	got := buildMQTT(cfg, logger, nil)
	if got == nil {
		t.Fatal("expected non-nil stack when MQTT enabled without broker")
	}
	if got.wiring == nil {
		t.Error("expected non-nil wiring")
	}
	// Without broker URL, no lifecycle is created.
	if got.lifecycle != nil {
		t.Error("expected nil lifecycle when no broker is configured")
	}
}

func TestBuildMQTT_EnabledWithBroker_ReturnsStackWithLifecycle(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.MQTT.Enabled = true
	cfg.North.MQTT.BrokerURL = "tcp://192.0.2.1:1883" // unreachable but valid URL
	cfg.North.MQTT.ClientID = "openccu-loom-test"
	logger := slog.Default()
	got := buildMQTT(cfg, logger, nil)
	if got == nil {
		t.Fatal("expected non-nil stack when MQTT enabled with broker")
	}
	if got.lifecycle == nil {
		t.Error("expected non-nil lifecycle when broker is configured")
	}
}

// ── announcePersistedFabric ───────────────────────────────────────────────────

func TestAnnouncePersistedFabric_NilStore_IsNoop(t *testing.T) {
	t.Parallel()
	// Must not panic.
	announcePersistedFabric(context.Background(), nil, nil, slog.Default())
}

func TestAnnouncePersistedFabric_NilBridge_IsNoop(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	store := matterStoreFromManager(t, mgr)
	// nil bridge — must not panic.
	announcePersistedFabric(context.Background(), store, nil, slog.Default())
}

func TestAnnouncePersistedFabric_EmptyStore_IsNoop(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01"}}

	reg := buildTestRegistry(t, "ccu-01")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, health.NewTracker(), nil, slog.Default())
	if bundle == nil {
		t.Skip("bridge did not start; skipping empty-store test")
	}
	t.Cleanup(bundle.stop)

	// Empty store → no fabrics → function returns early.
	// Must not panic.
	announcePersistedFabric(context.Background(), bundle.store, bundle.bridge, slog.Default())
}
