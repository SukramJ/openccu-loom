// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// ── buildMQTT ────────────────────────────────────────────────────────────────

func TestBuildMQTT_Disabled_ReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.MQTT.Enabled = false
	logger := slog.Default()
	if got := buildMQTT(cfg, logger, nil, nil); got != nil {
		t.Errorf("expected nil when MQTT disabled, got %v", got)
	}
}

func TestBuildMQTT_EnabledNoBroker_ReturnsStackWithNoopClient(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.MQTT.Enabled = true
	cfg.North.MQTT.BrokerURL = "" // no broker → noop client
	logger := slog.Default()
	got := buildMQTT(cfg, logger, nil, nil)
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
	got := buildMQTT(cfg, logger, nil, nil)
	if got == nil {
		t.Fatal("expected non-nil stack when MQTT enabled with broker")
	}
	if got.lifecycle == nil {
		t.Error("expected non-nil lifecycle when broker is configured")
	}
}

// TestBuildMQTT_BrokerPath_CleanupPassesGetASubscriber pins that the
// composition root hands the bridge a subscribe-capable client for the
// boot-time cleanup passes. The bridge's publish path is wrapped in a
// publish-only circuit breaker, so without the explicit WithSubscriber
// wiring both RunRetainCleanupOnce and RunDiscoveryOrphanCleanupOnce
// fail their capability check on every boot — a silent no-op that only
// showed up as a WARN line in operator logs.
func TestBuildMQTT_BrokerPath_CleanupPassesGetASubscriber(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.MQTT.Enabled = true
	cfg.North.MQTT.BrokerURL = "tcp://192.0.2.1:1883" // unreachable but valid URL
	cfg.North.MQTT.ClientID = "openccu-loom-test"
	cfg.North.MQTT.RawEnabled = true
	got := buildMQTT(cfg, slog.Default(), nil, nil)
	if got == nil {
		t.Fatal("expected non-nil stack")
	}
	bridge := got.wiring.Bridge()
	if bridge == nil {
		t.Fatal("expected non-nil bridge")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// The broker is unreachable, so the pass fails at Subscribe — the
	// assertion is only that it gets PAST the capability check.
	_, err := bridge.RunRetainCleanupOnce(ctx, 50*time.Millisecond)
	if errors.Is(err, mqtt.ErrCleanupNeedsSubscriber) {
		t.Fatal("bridge has no subscribe-capable client wired — retain cleanup is a silent no-op in production")
	}
}

// TestBuildMQTT_RetainedSweepKnowsEveryConfiguredCentral pins that the
// composition root hands the bridge the whole set of configured CCUs,
// not just the default one.
//
// The retained sweep only clears a topic that no configured central
// publishes to, so a bridge that knows one CCU out of three leaves the
// other two's orphaned metric topics on the broker forever — and reports
// the same "nothing to clear" as a correctly guarded sweep would. The
// assertion is on the sweep's candidate set, and it names the SECOND
// central, which can only appear once the full list travelled.
func TestBuildMQTT_RetainedSweepKnowsEveryConfiguredCentral(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.MQTT.Enabled = true
	cfg.North.MQTT.BrokerURL = "tcp://192.0.2.1:1883" // unreachable but valid URL
	cfg.North.MQTT.ClientID = "openccu-loom-test"
	cfg.North.MQTT.RawEnabled = true
	cfg.North.MQTT.TopicBase = "openccu-loom"
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01"}, {Name: "Haus CCÜ"}}

	got := buildMQTT(cfg, slog.Default(), nil, nil)
	if got == nil {
		t.Fatal("expected non-nil stack")
	}
	bridge := got.wiring.Bridge()
	if bridge == nil {
		t.Fatal("expected non-nil bridge")
	}
	want := "openccu-loom/" + strings.ToLower("Haus CCÜ") + "/system/health_score"
	candidates := bridge.RetiredMetricTopics()
	if !slices.Contains(candidates, want) {
		t.Fatalf("retained-sweep candidates %v do not cover %q — the orphaned metric topics of every central but the first stay on the broker", candidates, want)
	}
	// The first central's name is unchanged by the escaping: its "old"
	// spelling IS its live topic and must never be a candidate.
	for _, topic := range candidates {
		if strings.Contains(topic, "/ccu-01/") {
			t.Fatalf("candidate %q is a live topic of ccu-01 — clearing it blanks a value in use", topic)
		}
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

	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.Default())
	if bundle == nil {
		t.Skip("bridge did not start; skipping empty-store test")
	}
	t.Cleanup(bundle.stop)

	// Empty store → no fabrics → function returns early.
	// Must not panic.
	announcePersistedFabric(context.Background(), bundle.store, bundle.bridge, slog.Default())
}
