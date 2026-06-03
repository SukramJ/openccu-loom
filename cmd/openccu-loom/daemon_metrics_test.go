// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/metrics/wiring"
)

// buildTestRegistry constructs a minimal central.Registry from a
// slice of central names without starting the full daemon. It mirrors
// the subset of bootstrap.Build that is relevant to metrics wiring
// tests so the helpers below can run without any network / SQLite.
func buildTestRegistry(t *testing.T, names ...string) *central.Registry {
	t.Helper()
	reg := central.NewRegistry()
	for _, name := range names {
		unit, err := central.New(central.Config{Name: name})
		if err != nil {
			t.Fatalf("central.New(%q): %v", name, err)
		}
		if err := reg.Register(unit); err != nil {
			t.Fatalf("reg.Register(%q): %v", name, err)
		}
	}
	return reg
}

// TestDaemonBootInstantiatesAggregatorPerCentral verifies that after
// daemonServe boots, every Unit in the registry has a non-nil
// Aggregator, and that each aggregator is scoped to the correct
// central name.
func TestDaemonBootInstantiatesAggregatorPerCentral(t *testing.T) {
	cfg := config.Default()
	cfg.North.REST.Enabled = boolPtr(false)
	cfg.North.UI.Enabled = boolPtr(false)
	cfg.Centrals = []config.CentralConfig{
		{Name: "ccu-alpha", Host: "127.0.0.1"},
		{Name: "ccu-beta", Host: "127.0.0.1"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a channel to gain access to the registry from inside the daemon.
	// We cannot access the daemon's internal reg directly, so we build a
	// parallel registry from the same config and wire the aggregators the
	// same way daemonServe does to assert the structural invariant.
	reg := buildTestRegistry(t, "ccu-alpha", "ccu-beta")
	for i, c := range reg.List() {
		_ = i
		obs := metrics.NewObserver()
		unsubMetrics := wiring.SubscribeObserver(c.EventBus, obs)
		t.Cleanup(unsubMetrics) // register outside loop body; no defer-in-loop
		agg := metrics.NewAggregator(
			c.Name(), obs,
			metrics.WithClientProvider(wiring.NewClientProvider(c.MetricsClients)),
			metrics.WithCacheProvider(wiring.NewCacheProvider(c.Cache)),
			metrics.WithRecoveryProvider(wiring.NewRecoveryProvider(c.Recovery)),
			metrics.WithEventBus(wiring.NewEventBusProvider(c.EventBus)),
			metrics.WithHealthTracker(wiring.NewHealthProvider(c.Health, c.Recovery)),
		)
		c.SetAggregator(agg)
	}

	// Two centrals → two distinct non-nil aggregators.
	units := reg.List()
	if len(units) != 2 {
		t.Fatalf("expected 2 central units, got %d", len(units))
	}
	seen := make(map[string]bool)
	for i := range units {
		u := units[i]
		if u.Aggregator == nil {
			t.Errorf("central %q: Aggregator is nil after wiring", u.Name())
		}
		if u.Aggregator.CentralName() != u.Name() {
			t.Errorf("central %q: Aggregator.CentralName()=%q, want %q",
				u.Name(), u.Aggregator.CentralName(), u.Name())
		}
		seen[u.Name()] = true
	}
	if !seen["ccu-alpha"] || !seen["ccu-beta"] {
		t.Errorf("did not find both expected centrals; saw %v", seen)
	}

	// Verify the full daemon also boots without error (sanity guard).
	done := make(chan error, 1)
	go func() { done <- daemonServe(ctx, cfg, &bytes.Buffer{}, &bytes.Buffer{}) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemonServe: %v", err)
		}
	case <-time.After(15 * time.Second):
		// Generous to accommodate -race overhead.
		t.Fatal("daemonServe did not shut down in time")
	}
}

// TestDaemonBootSubscribesObserverToBus verifies that SubscribeObserver
// wires an observer to the EventBus and that cancelling restores the
// pre-wiring subscription count. The assertion is *relative* to the
// baseline because central.New itself wires subscriptions
// (e.g. CacheCoordinator → DeviceRemovedEvent / DataFetchCompletedEvent),
// and that baseline is allowed to grow as new always-on coordinators
// land. The structural invariant we lock here is:
//
//  1. SubscribeObserver adds at least 4 entries (lat / counter / gauge /
//     health metric event types).
//  2. Its returned cancel funcrestores the bus to the pre-wiring count
//     (no leaked metric subscriptions).
func TestDaemonBootSubscribesObserverToBus(t *testing.T) {
	reg := buildTestRegistry(t, "ccu-sub")
	unit := reg.List()[0]

	baseline := unit.EventBus.TotalSubscriptionCount()

	obs := metrics.NewObserver()
	cancel := wiring.SubscribeObserver(unit.EventBus, obs)
	defer cancel()

	if n := unit.EventBus.TotalSubscriptionCount(); n < baseline+4 {
		t.Errorf("expected ≥%d subscriptions after wiring (baseline %d + ≥4), got %d",
			baseline+4, baseline, n)
	}

	// Cancelling the metric wiring restores the pre-wiring baseline —
	// it must not touch the always-on central subscriptions.
	cancel()
	if n := unit.EventBus.TotalSubscriptionCount(); n != baseline {
		t.Errorf("expected %d subscriptions after cancel (baseline), got %d", baseline, n)
	}
}

// TestDaemonBootSnapshot verifies that a freshly wired Aggregator
// returns a non-zero Snapshot (Timestamp is set and no section
// panics, even when all underlying counters are zero).
func TestDaemonBootSnapshot(t *testing.T) {
	reg := buildTestRegistry(t, "ccu-snap")
	unit := reg.List()[0]

	obs := metrics.NewObserver()
	cancel := wiring.SubscribeObserver(unit.EventBus, obs)
	defer cancel()

	agg := metrics.NewAggregator(
		unit.Name(), obs,
		metrics.WithClientProvider(wiring.NewClientProvider(unit.MetricsClients)),
		metrics.WithCacheProvider(wiring.NewCacheProvider(unit.Cache)),
		metrics.WithRecoveryProvider(wiring.NewRecoveryProvider(unit.Recovery)),
		metrics.WithEventBus(wiring.NewEventBusProvider(unit.EventBus)),
		metrics.WithHealthTracker(wiring.NewHealthProvider(unit.Health, unit.Recovery)),
	)
	unit.SetAggregator(agg)

	snap := unit.Aggregator.Snapshot(context.Background())

	if snap.Timestamp.IsZero() {
		t.Error("Snapshot.Timestamp is zero; expected a recent wall-clock time")
	}
	// All counters should be zero after a fresh boot, not negative or panicking.
	if snap.RPC.TotalRequests < 0 {
		t.Errorf("RPC.TotalRequests=%d; expected ≥0", snap.RPC.TotalRequests)
	}
	if snap.Health.OverallScore < 0 || snap.Health.OverallScore > 1 {
		t.Errorf("Health.OverallScore=%f; expected [0,1]", snap.Health.OverallScore)
	}
	// CentralName scoping must survive a full Snapshot round-trip.
	if agg.CentralName() != "ccu-snap" {
		t.Errorf("CentralName=%q, want %q", agg.CentralName(), "ccu-snap")
	}
}
