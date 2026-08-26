// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// freshMasterValuesStoreForAdapter opens an in-memory SQLite database with
// all migrations applied and returns a MasterValuesStore backed by it.
func freshMasterValuesStoreForAdapter(t *testing.T) *sqlite.MasterValuesStore {
	t.Helper()
	db, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlite.NewMasterValuesStore(db)
}

// saveMasterRowForChannel upserts one MASTER value row for the given
// channel address ("<deviceAddress>:<no>").
func saveMasterRowForChannel(
	t *testing.T,
	store *sqlite.MasterValuesStore,
	centralName, interfaceID, channelAddress string,
	values map[string]any,
) {
	t.Helper()
	if err := store.SaveChannel(context.Background(), centralName, interfaceID, channelAddress, values); err != nil {
		t.Fatalf("SaveChannel %s/%s/%s: %v", centralName, interfaceID, channelAddress, err)
	}
}

// masterRowFound reports whether the channel's MASTER row still hits the
// cache (a genuine hit, not the "not found" zero value LoadChannel also
// returns for an empty map).
func masterRowFound(t *testing.T, store *sqlite.MasterValuesStore, centralName, interfaceID, channelAddress string) bool {
	t.Helper()
	_, found, err := store.LoadChannel(context.Background(), centralName, interfaceID, channelAddress)
	if err != nil {
		t.Fatalf("LoadChannel %s/%s/%s: %v", centralName, interfaceID, channelAddress, err)
	}
	return found
}

// TestWireMasterValuesEviction_DeletesDeviceAKeepsDeviceB is the regression
// guard for the stale-configuration-on-re-pair defect: a device removed
// (unpair, factory reset) must have its persisted MASTER values dropped so
// a later re-pair at the same address re-reads the CCU's current
// configuration instead of seeding from the previous pairing's cache.
func TestWireMasterValuesEviction_DeletesDeviceAKeepsDeviceB(t *testing.T) {
	t.Parallel()

	store := freshMasterValuesStoreForAdapter(t)
	reg, unit := registryWithEventBus(t)

	const (
		centralName = "ccu-test"
		ifaceID     = "HmIP-RF"
		devA        = "DEVICE-A"
		devB        = "DEVICE-B"
	)

	saveMasterRowForChannel(t, store, centralName, ifaceID, devA+":1", map[string]any{"TEMPERATURE_OFFSET": 1.5})
	saveMasterRowForChannel(t, store, centralName, ifaceID, devB+":1", map[string]any{"TEMPERATURE_OFFSET": -0.5})

	evictor := WireMasterValuesEviction(reg, store, nil)
	t.Cleanup(evictor.Stop)

	events.Publish(unit.EventBus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBase(),
		CentralName: centralName,
		InterfaceID: ifaceID,
		Address:     devA,
	})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !masterRowFound(t, store, centralName, ifaceID, devA+":1") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if masterRowFound(t, store, centralName, ifaceID, devA+":1") {
		t.Error("device A: MASTER row still present after removal")
	}
	if !masterRowFound(t, store, centralName, ifaceID, devB+":1") {
		t.Error("device B: MASTER row was deleted, want it intact")
	}
}

// TestWireMasterValuesEviction_StopUnsubscribes verifies that Stop
// unsubscribes the handler so a subsequent publish does not delete rows.
func TestWireMasterValuesEviction_StopUnsubscribes(t *testing.T) {
	t.Parallel()

	store := freshMasterValuesStoreForAdapter(t)
	reg, unit := registryWithEventBus(t)

	const (
		centralName = "ccu-test"
		ifaceID     = "HmIP-RF"
		devC        = "DEVICE-C"
	)

	saveMasterRowForChannel(t, store, centralName, ifaceID, devC+":1", map[string]any{"TEMPERATURE_OFFSET": 0.0})

	evictor := WireMasterValuesEviction(reg, store, nil)
	evictor.Stop()

	events.Publish(unit.EventBus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBase(),
		CentralName: centralName,
		InterfaceID: ifaceID,
		Address:     devC,
	})
	time.Sleep(20 * time.Millisecond)

	if !masterRowFound(t, store, centralName, ifaceID, devC+":1") {
		t.Error("after unsubscribe: MASTER row was deleted, want it intact")
	}

	evictor.Stop() // idempotent
}

// TestWireMasterValuesEviction_NilGuards verifies that a nil store or nil
// registry yields a handle whose methods are safe no-ops.
func TestWireMasterValuesEviction_NilGuards(t *testing.T) {
	t.Parallel()

	store := freshMasterValuesStoreForAdapter(t)
	reg, _ := registryWithEventBus(t)

	cases := []struct {
		name  string
		reg   *central.Registry
		store *sqlite.MasterValuesStore
	}{
		{"nil registry", nil, store},
		{"nil store", reg, nil},
		{"both nil", nil, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			evictor := WireMasterValuesEviction(tc.reg, tc.store, nil)
			evictor.Stop()
			evictor.Stop() // idempotent
			evictor.StartCentral(nil)()
		})
	}
}
