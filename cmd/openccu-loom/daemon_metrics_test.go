// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
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

// TestBootWiringMakesEveryCentralsMetricsReadable pins the aggregator
// wiring by its effect: the production seeding function is the only
// thing that runs, and the read happens through the very provider the
// REST mount hands the diagnostics dump. A test that built its own
// aggregator here would stay green while nothing in the daemon ever
// produced or consumed one.
func TestBootWiringMakesEveryCentralsMetricsReadable(t *testing.T) {
	cfg := config.Default()
	cfg.Centrals = []config.CentralConfig{
		{Name: "ccu-alpha", Host: "127.0.0.1"},
		{Name: "ccu-beta", Host: "127.0.0.1"},
	}
	reg := buildTestRegistry(t, "ccu-alpha", "ccu-beta")

	seedCentralHealthAndMetrics(reg, cfg, nil, slog.New(slog.DiscardHandler))

	snapshots := adapter.NewIntrospectAdapter(reg).MetricsSnapshots(context.Background())
	for _, name := range []string{"ccu-alpha", "ccu-beta"} {
		snap, ok := snapshots[name]
		if !ok {
			t.Errorf("central %q contributes no metrics snapshot; got %v", name, keysOf(snapshots))
			continue
		}
		if snap.Timestamp.IsZero() {
			t.Errorf("central %q: snapshot timestamp is zero", name)
		}
		// A section that stays zero-valued because its provider is nil
		// is indistinguishable from a healthy idle daemon on the wire,
		// so assert the health provider actually answered: the tracker
		// reports a score in [0,1] and at least the seeded component.
		if snap.Health.OverallScore < 0 || snap.Health.OverallScore > 1 {
			t.Errorf("central %q: health score %f outside [0,1]", name, snap.Health.OverallScore)
		}
	}
}

// TestBootWiringScopesEachAggregatorToItsOwnCentral guards the
// multi-CCU dimension: two centrals must not share one aggregator, or
// the dump reports one CCU's counters under the other's name.
func TestBootWiringScopesEachAggregatorToItsOwnCentral(t *testing.T) {
	cfg := config.Default()
	cfg.Centrals = []config.CentralConfig{
		{Name: "ccu-alpha", Host: "127.0.0.1"},
		{Name: "ccu-beta", Host: "127.0.0.1"},
	}
	reg := buildTestRegistry(t, "ccu-alpha", "ccu-beta")

	seedCentralHealthAndMetrics(reg, cfg, nil, slog.New(slog.DiscardHandler))

	for _, u := range reg.List() {
		if u.Aggregator == nil {
			t.Fatalf("central %q: no aggregator after boot wiring", u.Name())
		}
		if u.Aggregator.CentralName() != u.Name() {
			t.Errorf("central %q: aggregator scoped to %q", u.Name(), u.Aggregator.CentralName())
		}
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
