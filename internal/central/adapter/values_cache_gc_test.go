// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"path/filepath"
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
	// A file, not ":memory:": the pool opens more than one connection, and
	// each in-memory connection gets its own empty database — a read that
	// happens to land on a fresh connection fails with "no such table".
	// "cache=shared" would fix that but shares the database across every
	// test in the process, so the file is the isolated option.
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "gc.db"))
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

// TestGCOnce_KeepsRowsOfCentralWhoseModelIsEmpty pins the rule that makes the
// sweep safe on a multi-CCU install: a central whose device model is empty
// contributed nothing to the sweep, so GC has no evidence about its rows and
// must leave every one of them alone. An offline CCU (still blocked in the
// readiness gate, or rebooting) looks exactly like a CCU whose devices all
// disappeared, and only one of those two readings is recoverable.
func TestGCOnce_KeepsRowsOfCentralWhoseModelIsEmpty(t *testing.T) {
	t.Parallel()

	store := openGCTestStore(t)
	reg, loadedCentral := buildGCTestRegistry(t)
	ctx := context.Background()
	now := time.Now()

	// The offline central sorts before the loaded one, so it is not the
	// registry-order tail that happens to survive.
	offline, err := central.New(central.Config{Name: "a-offline-central"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(offline); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	if err := store.SaveValue(ctx, loadedCentral, "if1", "DEV:1", "STATE", true, now, now); err != nil {
		t.Fatalf("SaveValue(loaded): %v", err)
	}
	if err := store.SaveValue(ctx, "a-offline-central", "if1", "OTHER:1", "STATE", true, now, now); err != nil {
		t.Fatalf("SaveValue(offline): %v", err)
	}

	gcOnce(ctx, reg, store, slog.New(slog.DiscardHandler))

	rows, err := store.LoadChannel(ctx, "a-offline-central", "if1", "OTHER:1")
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("gcOnce deleted the offline central's rows: got %d rows, want 1", len(rows))
	}
}

// TestGCOnce_KeepsRowsOfInterfaceThatContributedNoKeys is the same rule one
// level down: a single interface whose ingest failed leaves its devices out of
// the model while its siblings on the same central load normally. Scoping the
// sweep per central alone would classify that interface's whole cache as dead.
func TestGCOnce_KeepsRowsOfInterfaceThatContributedNoKeys(t *testing.T) {
	t.Parallel()

	store := openGCTestStore(t)
	reg, centralName := buildGCTestRegistry(t)
	ctx := context.Background()
	now := time.Now()

	if err := store.SaveValue(ctx, centralName, "if1", "DEV:1", "STATE", true, now, now); err != nil {
		t.Fatalf("SaveValue(loaded interface): %v", err)
	}
	if err := store.SaveValue(ctx, centralName, "if2", "CUX:1", "STATE", true, now, now); err != nil {
		t.Fatalf("SaveValue(silent interface): %v", err)
	}

	gcOnce(ctx, reg, store, slog.New(slog.DiscardHandler))

	rows, err := store.LoadChannel(ctx, centralName, "if2", "CUX:1")
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("gcOnce deleted the silent interface's rows: got %d rows, want 1", len(rows))
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
	// A 5ms flush interval pushes gcTickInterval's derived cadence down far
	// enough that the GC ticker fires promptly, without waiting out the
	// multi-minute production default.
	flusher := WireValuesCacheFlusher(reg, store, 5*time.Millisecond, logger)
	defer flusher.Stop()

	// Poll for the effect rather than sleeping a fixed span and checking
	// once: the assertion is that the GC runs, not that it runs within any
	// particular window, and a loaded CI runner can miss a tight one.
	var rows []sqlite.CachedValue
	start := time.Now()
	deadline := start.Add(10 * time.Second)
	for {
		var err error
		rows, err = store.LoadChannel(ctx, centralName, "if1", "DEV:1")
		if err != nil {
			t.Fatalf("LoadChannel: %v", err)
		}
		if len(rows) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("LoadChannel returned %d rows after %s, want 1 (orphan not GC'd): %+v",
				len(rows), time.Since(start).Round(time.Millisecond), rows)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
