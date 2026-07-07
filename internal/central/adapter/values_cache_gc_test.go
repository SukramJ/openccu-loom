// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// openGCTestStore opens an in-memory, fully-migrated values-cache database
// for the GC tests. Closed automatically via t.Cleanup.
func openGCTestStore(t *testing.T) *sqlite.ValuesCacheStore {
	t.Helper()
	db, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlite.NewValuesCacheStore(db)
}

// buildGCTestRegistry returns a *central.Registry with a single registered
// unit that owns exactly one device, one channel, and one VALUES data point
// (interface "if1", channel "DEV:1", parameter "STATE"). That triple is the
// only key the GC pass should consider alive.
func buildGCTestRegistry(t *testing.T) (reg *central.Registry, centralName string) {
	t.Helper()
	centralName = "gc-central"
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: "if1",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "DEV",
		Model:       "HmIP-PS",
	})
	c.ModelRegistry.Put(dev)
	ch := dev.AddChannel("DEV:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "if1",
			ChannelAddress: "DEV:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)

	reg = central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	return reg, centralName
}

// TestGCOnce_DeletesOrphanRowsKeepsAliveRows is the failing-first
// reproducer for wiring GCDeadRows into the periodic flusher: a row for a
// parameter that no longer exists in the current device model ("orphan")
// must be deleted, while a row whose (channel, parameter) is still part of
// the model ("alive") must survive untouched.
func TestGCOnce_DeletesOrphanRowsKeepsAliveRows(t *testing.T) {
	t.Parallel()

	store := openGCTestStore(t)
	reg, centralName := buildGCTestRegistry(t)
	ctx := context.Background()
	now := time.Now()

	// Alive row: matches the DEV:1/STATE data point registered above.
	if err := store.SaveValue(ctx, centralName, "if1", "DEV:1", "STATE", true, now, now); err != nil {
		t.Fatalf("SaveValue(alive): %v", err)
	}
	// Orphan row: no data point named LEVEL exists on DEV:1 in the model.
	if err := store.SaveValue(ctx, centralName, "if1", "DEV:1", "LEVEL", 1.0, now, now); err != nil {
		t.Fatalf("SaveValue(orphan): %v", err)
	}

	logger := slog.New(slog.DiscardHandler)
	gcOnce(ctx, reg, store, logger)

	rows, err := store.LoadChannel(ctx, centralName, "if1", "DEV:1")
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("LoadChannel after gcOnce returned %d rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].Parameter != "STATE" {
		t.Fatalf("surviving row parameter = %q, want STATE", rows[0].Parameter)
	}
}

// TestGCOnce_SkipsWhenAliveSetEmpty guards the defensive empty-alive-set
// path: when no central has any device loaded yet (e.g. GC firing during a
// slow boot before the device pipeline populated ModelRegistry), gcOnce
// must not call GCDeadRows at all — otherwise the very first tick would
// wipe every previously-flushed row for a central that just has not
// finished loading.
func TestGCOnce_SkipsWhenAliveSetEmpty(t *testing.T) {
	t.Parallel()

	store := openGCTestStore(t)
	ctx := context.Background()
	now := time.Now()
	if err := store.SaveValue(ctx, "idle-central", "if1", "DEV:1", "STATE", true, now, now); err != nil {
		t.Fatalf("SaveValue: %v", err)
	}

	c, err := central.New(central.Config{Name: "idle-central"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)
	gcOnce(ctx, reg, store, logger)

	rows, err := store.LoadChannel(ctx, "idle-central", "if1", "DEV:1")
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("gcOnce deleted rows despite an empty alive set: got %d rows, want 1", len(rows))
	}
}

// TestWireValuesCacheFlusher_RunsPeriodicGC verifies the production wiring
// path: WireValuesCacheFlusher must itself drive gcOnce on a low-frequency
// tick (derived from the flush interval) so GCDeadRows is reachable from
// the daemon's existing flusher call site without a second wiring call.
func TestWireValuesCacheFlusher_RunsPeriodicGC(t *testing.T) {
	t.Parallel()

	store := openGCTestStore(t)
	reg, centralName := buildGCTestRegistry(t)
	ctx := context.Background()
	now := time.Now()

	if err := store.SaveValue(ctx, centralName, "if1", "DEV:1", "STATE", true, now, now); err != nil {
		t.Fatalf("SaveValue(alive): %v", err)
	}
	if err := store.SaveValue(ctx, centralName, "if1", "DEV:1", "ORPHAN", 1.0, now, now); err != nil {
		t.Fatalf("SaveValue(orphan): %v", err)
	}

	logger := slog.New(slog.DiscardHandler)
	// A 5ms flush interval pushes gcTickInterval's derived cadence down
	// far enough that the GC ticker also fires within the sleep window
	// below, without needing to wait out the multi-minute production
	// default.
	closer := WireValuesCacheFlusher(reg, store, 5*time.Millisecond, logger)
	time.Sleep(200 * time.Millisecond)
	closer()

	rows, err := store.LoadChannel(ctx, centralName, "if1", "DEV:1")
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("LoadChannel after flusher run returned %d rows, want 1 (orphan not GC'd): %+v", len(rows), rows)
	}
}
