// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// newTestMeasurementStore opens a fresh, migrated, empty history DB in t's
// temp directory. Not marked t.Parallel: goose's SetBaseFS is process-global
// state (see internal/store/sqlite/store.go) and races against any other
// sqlite-backed test running concurrently in this package.
func newTestMeasurementStore(t *testing.T) *sqlite.MeasurementStore {
	t.Helper()
	dsn := sqlite.FileDSN(filepath.Join(t.TempDir(), "hist.db"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := sqlite.OpenHistory(ctx, dsn)
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlite.NewMeasurementStore(db)
}

// energyAdapterQuery is a valid EnergyQuery for the empty store the tests
// below use: the tariff-stamping behaviour under test is independent of
// which rows QueryEnergy actually returns.
func energyAdapterQuery() handlers.EnergyQuery {
	return handlers.EnergyQuery{
		Central: "ccu1",
		From:    time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		To:      time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
		Group:   "day",
	}
}

// TestEnergyHandlerAdapterStampsTariffWhenConfigured verifies that a
// non-zero tariff and an explicit currency are echoed onto the response
// verbatim, so the SPA can derive costs.
func TestEnergyHandlerAdapterStampsTariffWhenConfigured(t *testing.T) {
	store := newTestMeasurementStore(t)
	a := newEnergyHandlerAdapter(store, nil, 0.32, "$")
	resp, err := a.Energy(context.Background(), energyAdapterQuery())
	if err != nil {
		t.Fatalf("Energy: %v", err)
	}
	if resp.PricePerKWh != 0.32 {
		t.Errorf("PricePerKWh = %v, want 0.32", resp.PricePerKWh)
	}
	if resp.Currency != "$" {
		t.Errorf("Currency = %q, want %q", resp.Currency, "$")
	}
}

// TestEnergyHandlerAdapterOmitsTariffWhenUnset is the load-bearing case: with
// no tariff configured, both PricePerKWh and Currency must stay at their
// zero values. Stamping a currency without a tariff would let a client
// render a misleading "0.00 $" instead of showing no cost figure at all.
func TestEnergyHandlerAdapterOmitsTariffWhenUnset(t *testing.T) {
	store := newTestMeasurementStore(t)
	a := newEnergyHandlerAdapter(store, nil, 0, "$")
	resp, err := a.Energy(context.Background(), energyAdapterQuery())
	if err != nil {
		t.Fatalf("Energy: %v", err)
	}
	if resp.PricePerKWh != 0 {
		t.Errorf("PricePerKWh = %v, want 0 (no tariff configured)", resp.PricePerKWh)
	}
	if resp.Currency != "" {
		t.Errorf("Currency = %q, want empty (no tariff configured)", resp.Currency)
	}
}

// TestEnergyHandlerAdapterDefaultsCurrencyToEuroSign verifies that an
// unconfigured currency falls back to the euro sign once a tariff is set —
// the tariff is only ever configured with an intended currency in mind, and
// the euro is this project's overwhelming default.
func TestEnergyHandlerAdapterDefaultsCurrencyToEuroSign(t *testing.T) {
	store := newTestMeasurementStore(t)
	a := newEnergyHandlerAdapter(store, nil, 0.30, "")
	resp, err := a.Energy(context.Background(), energyAdapterQuery())
	if err != nil {
		t.Fatalf("Energy: %v", err)
	}
	if resp.Currency != "€" {
		t.Errorf("Currency = %q, want €", resp.Currency)
	}
}

// TestEnergyHandlerAdapterNegativeTariffTreatedAsUnset pins the adapter's
// guard as `tariff > 0`, not `tariff != 0`: a negative tariff (e.g. a
// misconfigured feed-in-only value) must be treated exactly like "unset",
// never echoed onto the response.
func TestEnergyHandlerAdapterNegativeTariffTreatedAsUnset(t *testing.T) {
	store := newTestMeasurementStore(t)
	a := newEnergyHandlerAdapter(store, nil, -0.5, "$")
	resp, err := a.Energy(context.Background(), energyAdapterQuery())
	if err != nil {
		t.Fatalf("Energy: %v", err)
	}
	if resp.PricePerKWh != 0 {
		t.Errorf("PricePerKWh = %v, want 0 (negative tariff treated as unset)", resp.PricePerKWh)
	}
	if resp.Currency != "" {
		t.Errorf("Currency = %q, want empty (negative tariff treated as unset)", resp.Currency)
	}
}
