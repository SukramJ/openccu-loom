// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// freshValuesCacheStoreForAdapter opens an in-memory SQLite database with all
// migrations applied and returns a ValuesCacheStore backed by it.
func freshValuesCacheStoreForAdapter(t *testing.T) *sqlite.ValuesCacheStore {
	t.Helper()
	db, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlite.NewValuesCacheStore(db)
}

// registryWithEventBus returns a Registry with a single CentralUnit that has
// a live EventBus. Its name is "ccu-test".
func registryWithEventBus(t *testing.T) (*central.Registry, *central.Unit) {
	t.Helper()
	unit, err := central.New(central.Config{Name: "ccu-test"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	return reg, unit
}

// nowMS returns a time.Time truncated to millisecond precision.
func nowMSForEvict() time.Time {
	return time.UnixMilli(time.Now().UnixMilli())
}

// saveRowsForDevice inserts one cache row per parameter into the store under
// (centralName, interfaceID, deviceAddress+":"+channel).
func saveRowsForDevice(
	t *testing.T,
	store *sqlite.ValuesCacheStore,
	centralName, interfaceID, deviceAddress string,
	params []string,
) {
	t.Helper()
	ctx := context.Background()
	now := nowMSForEvict()
	for _, p := range params {
		ch := deviceAddress + ":1"
		if err := store.SaveValue(ctx, centralName, interfaceID, ch, p, true, now, now); err != nil {
			t.Fatalf("SaveValue %s/%s/%s/%s: %v", centralName, interfaceID, ch, p, err)
		}
	}
}

// rowCount returns the number of cache rows matching (central, interface,
// deviceAddress prefix).
func rowCountForDevice(t *testing.T, store *sqlite.ValuesCacheStore, centralName, interfaceID, deviceAddress string) int {
	t.Helper()
	got, err := store.LoadChannel(context.Background(), centralName, interfaceID, deviceAddress+":1")
	if err != nil {
		t.Fatalf("LoadChannel %s/%s/%s:1: %v", centralName, interfaceID, deviceAddress, err)
	}
	return len(got)
}

// TestWireValuesCacheEviction_DeletesDeviceAKeepsDeviceB verifies that
// publishing DeviceRemovedEvent for device A deletes A's cache rows while
// leaving device B's rows intact.
func TestWireValuesCacheEviction_DeletesDeviceAKeepsDeviceB(t *testing.T) {
	t.Parallel()

	store := freshValuesCacheStoreForAdapter(t)
	reg, unit := registryWithEventBus(t)

	const (
		centralName = "ccu-test"
		ifaceID     = "HmIP-RF"
		devA        = "DEVICE-A"
		devB        = "DEVICE-B"
	)

	// Seed two rows for device A and two for device B.
	saveRowsForDevice(t, store, centralName, ifaceID, devA, []string{"STATE", "RSSI_DEVICE"})
	saveRowsForDevice(t, store, centralName, ifaceID, devB, []string{"TEMPERATURE", "HUMIDITY"})

	evictor := WireValuesCacheEviction(reg, store, nil)
	t.Cleanup(evictor.Stop)

	// Publish removal for device A only.
	events.Publish(unit.EventBus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBase(),
		CentralName: centralName,
		InterfaceID: ifaceID,
		Address:     devA,
	})

	// Allow up to 1 s for the DELETE to propagate (the handler is
	// synchronous on the bus goroutine, so it should be immediate, but
	// we poll to be robust against any scheduler latency).
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if rowCountForDevice(t, store, centralName, ifaceID, devA) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if n := rowCountForDevice(t, store, centralName, ifaceID, devA); n != 0 {
		t.Errorf("device A: expected 0 rows after removal, got %d", n)
	}
	if n := rowCountForDevice(t, store, centralName, ifaceID, devB); n != 2 {
		t.Errorf("device B: expected 2 rows intact, got %d", n)
	}
}

// TestWireValuesCacheEviction_StopUnsubscribes verifies that Stop
// unsubscribes the handler so that a subsequent publish does not delete
// cache rows (and must not panic or leak).
func TestWireValuesCacheEviction_StopUnsubscribes(t *testing.T) {
	t.Parallel()

	store := freshValuesCacheStoreForAdapter(t)
	reg, unit := registryWithEventBus(t)

	const (
		centralName = "ccu-test"
		ifaceID     = "HmIP-RF"
		devC        = "DEVICE-C"
	)

	saveRowsForDevice(t, store, centralName, ifaceID, devC, []string{"STATE"})

	evictor := WireValuesCacheEviction(reg, store, nil)
	evictor.Stop() // unsubscribe before any publish

	// Publish removal — the handler must no longer be active.
	events.Publish(unit.EventBus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBase(),
		CentralName: centralName,
		InterfaceID: ifaceID,
		Address:     devC,
	})

	// Give any lingering async path a moment, then assert the row survived.
	time.Sleep(20 * time.Millisecond)

	if n := rowCountForDevice(t, store, centralName, ifaceID, devC); n != 1 {
		t.Errorf("after unsubscribe: expected 1 row (no delete), got %d", n)
	}

	// Calling Stop again must not panic (idempotent).
	evictor.Stop()
}

// TestWireValuesCacheEviction_NilGuards verifies that a nil store or nil
// registry yields a handle whose methods are safe no-ops.
func TestWireValuesCacheEviction_NilGuards(t *testing.T) {
	t.Parallel()

	store := freshValuesCacheStoreForAdapter(t)
	reg, _ := registryWithEventBus(t)

	cases := []struct {
		name  string
		reg   *central.Registry
		store *sqlite.ValuesCacheStore
	}{
		{"nil registry", nil, store},
		{"nil store", reg, nil},
		{"both nil", nil, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			evictor := WireValuesCacheEviction(tc.reg, tc.store, nil)
			// Must not panic, including on the nil handle a disabled cache
			// yields — the composition root calls Stop / StartCentral
			// unconditionally.
			evictor.Stop()
			evictor.Stop() // idempotent
			evictor.StartCentral(nil)()
		})
	}
}
