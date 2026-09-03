// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	gosql "database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestRemovedDevicePurgesItsMeasurementHistory pins the eviction through the
// real constructor and asserts the effect in the database.
//
// Both stores carried a DeleteDevice whose comment said it runs on
// device-remove / unpair, and neither had a production caller: a device's
// history across all three tiers and the operator's recording overrides
// survived unpairing forever. The CCU reuses addresses when hardware is
// swapped, so a replacement paired into the same address inherited the
// previous device's series — the exact resurfacing the multi-tier delete
// exists to prevent.
func TestRemovedDevicePurgesItsMeasurementHistory(t *testing.T) {
	t.Parallel()

	// The measurement history lives in its own database with its own
	// migration series (migrations_history), which is what OpenHistory applies.
	db, err := sqlitestore.OpenHistory(context.Background(), filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	history := sqlitestore.NewMeasurementStore(db)
	overrides := sqlitestore.NewRecordingOverrideStore(db)
	logger := discardTestLogger()

	reg := central.NewRegistry()
	// Wired exactly as the composition root wires it (daemon_southbound.go),
	// against an empty registry: the central joins afterwards, which is what
	// makes this an adopt-safe seam rather than a boot-time walk.
	evictor := adapter.WireMeasurementEviction(reg, history, overrides, logger)
	if evictor == nil {
		t.Fatal("WireMeasurementEviction returned nil for a complete set of stores")
	}
	t.Cleanup(evictor.Stop)

	const (
		centralName = "evict-live"
		ifaceID     = "evict-live-HmIP-RF"
		addr        = "EVICT01"
		channel     = addr + ":1"
	)
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := history.SaveBatch(context.Background(), []sqlitestore.MeasurementSample{{
		CentralName: centralName, InterfaceID: ifaceID, ChannelAddress: channel,
		Parameter: "POWER", TS: time.Now().Add(-time.Hour), Value: 42,
	}}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if err := overrides.Set(context.Background(), centralName, ifaceID, channel, "POWER", true, "audit-test"); err != nil {
		t.Fatalf("overrides.Set: %v", err)
	}
	if n := countMeasurementRows(t, db, centralName, channel); n != 1 {
		t.Fatalf("seeded measurement rows = %d, want 1", n)
	}

	events.Publish(c.EventBus, hmevent.DeviceRemovedEvent{
		CentralName: centralName, InterfaceID: ifaceID, Address: addr,
	})

	// The handler runs on the bus and bounds its own deletes; poll rather than
	// reading once, so this measures the effect and not the scheduler.
	deadline := time.Now().Add(5 * time.Second)
	for {
		measurements := countMeasurementRows(t, db, centralName, channel)
		remaining, err := overrides.List(context.Background())
		if err != nil {
			t.Fatalf("overrides.List: %v", err)
		}
		if measurements == 0 && len(remaining) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after removal: %d measurement row(s) and %d override(s) remain, want none",
				measurements, len(remaining))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// countMeasurementRows counts raw measurement rows for one channel.
func countMeasurementRows(t *testing.T, db *gosql.DB, centralName, channelAddress string) int {
	t.Helper()
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM measurements WHERE central_name = ? AND channel_address = ?`,
		centralName, channelAddress,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count measurements: %v", err)
	}
	return n
}
