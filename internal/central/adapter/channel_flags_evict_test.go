// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/channelflags"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// freshChannelFlagsStoreForAdapter opens an in-memory SQLite database with
// all migrations applied and returns a ChannelFlagsStore backed by it.
func freshChannelFlagsStoreForAdapter(t *testing.T) *sqlite.ChannelFlagsStore {
	t.Helper()
	db, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlite.NewChannelFlagsStore(db)
}

// seedChannelFlags writes Hidden/Locked overrides for a device's own
// address and two of its channels into both the store and the overlay, the
// shape the daemon keeps them in during normal operation.
func seedChannelFlags(
	t *testing.T,
	store *sqlite.ChannelFlagsStore,
	overlay *channelflags.Overlay,
	centralName, deviceAddress string,
) {
	t.Helper()
	ctx := context.Background()
	addrs := []string{deviceAddress, deviceAddress + ":1", deviceAddress + ":2"}
	for _, addr := range addrs {
		if err := store.Set(ctx, centralName, addr, true, false, "tester"); err != nil {
			t.Fatalf("store.Set %s/%s: %v", centralName, addr, err)
		}
		overlay.Set(centralName, addr, channelflags.Flags{Hidden: true})
	}
}

// channelFlagsRowCount returns how many of the given device's channel-flag
// rows remain in the store.
func channelFlagsRowCount(t *testing.T, store *sqlite.ChannelFlagsStore, centralName, deviceAddress string) int {
	t.Helper()
	all, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	n := 0
	for _, f := range all {
		if f.CentralName != centralName {
			continue
		}
		if f.ChannelAddress == deviceAddress || len(f.ChannelAddress) > len(deviceAddress) &&
			f.ChannelAddress[:len(deviceAddress)+1] == deviceAddress+":" {
			n++
		}
	}
	return n
}

// TestWireChannelFlagsEviction_UnpairDropsDeviceKeepsOthers verifies that a
// real device removal (ModelTeardown unset) purges that device's flags from
// both the store and the overlay while leaving another device's flags, and
// the removed device's own overlay entries, intact for nobody else.
func TestWireChannelFlagsEviction_UnpairDropsDeviceKeepsOthers(t *testing.T) {
	t.Parallel()

	store := freshChannelFlagsStoreForAdapter(t)
	overlay := channelflags.New()
	reg, unit := registryWithEventBus(t)

	const (
		centralName = "ccu-test"
		devA        = "DEVICE-A"
		devB        = "DEVICE-B"
	)
	seedChannelFlags(t, store, overlay, centralName, devA)
	seedChannelFlags(t, store, overlay, centralName, devB)

	evictor := WireChannelFlagsEviction(reg, store, overlay, nil)
	t.Cleanup(evictor.Stop)

	events.Publish(unit.EventBus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBase(),
		CentralName: centralName,
		Address:     devA,
	})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if channelFlagsRowCount(t, store, centralName, devA) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if n := channelFlagsRowCount(t, store, centralName, devA); n != 0 {
		t.Errorf("device A: expected 0 store rows after unpair, got %d", n)
	}
	if n := channelFlagsRowCount(t, store, centralName, devB); n != 3 {
		t.Errorf("device B: expected 3 store rows intact, got %d", n)
	}
	if got := overlay.Get(centralName, devA+":1"); got != (channelflags.Flags{}) {
		t.Errorf("device A overlay entry survived unpair: %+v", got)
	}
	if got := overlay.Get(centralName, devB+":1"); got != (channelflags.Flags{Hidden: true}) {
		t.Errorf("device B overlay entry must survive, got %+v", got)
	}
}

// TestWireChannelFlagsEviction_ModelTeardownLeavesEverythingIntact verifies
// that a DeviceRemovedEvent carrying ModelTeardown (a cache-clear re-init,
// not an operator unpair) purges nothing: neither the removed device's own
// flags nor any other device's.
func TestWireChannelFlagsEviction_ModelTeardownLeavesEverythingIntact(t *testing.T) {
	t.Parallel()

	store := freshChannelFlagsStoreForAdapter(t)
	overlay := channelflags.New()
	reg, unit := registryWithEventBus(t)

	const (
		centralName = "ccu-test"
		devA        = "DEVICE-A"
		devB        = "DEVICE-B"
	)
	seedChannelFlags(t, store, overlay, centralName, devA)
	seedChannelFlags(t, store, overlay, centralName, devB)

	evictor := WireChannelFlagsEviction(reg, store, overlay, nil)
	t.Cleanup(evictor.Stop)

	events.Publish(unit.EventBus, hmevent.DeviceRemovedEvent{
		Base:          hmevent.NewBase(),
		CentralName:   centralName,
		Address:       devA,
		ModelTeardown: true,
	})

	// Give the handler a moment to (not) act, then assert nothing moved.
	time.Sleep(50 * time.Millisecond)

	if n := channelFlagsRowCount(t, store, centralName, devA); n != 3 {
		t.Errorf("teardown must not purge device A store rows, got %d", n)
	}
	if n := channelFlagsRowCount(t, store, centralName, devB); n != 3 {
		t.Errorf("teardown must not purge device B store rows, got %d", n)
	}
	if got := overlay.Get(centralName, devA+":1"); got != (channelflags.Flags{Hidden: true}) {
		t.Errorf("teardown must not purge device A overlay entry, got %+v", got)
	}
}

// TestWireChannelFlagsEviction_StopUnsubscribes verifies that Stop
// unsubscribes the handler so a subsequent publish does not delete flags.
func TestWireChannelFlagsEviction_StopUnsubscribes(t *testing.T) {
	t.Parallel()

	store := freshChannelFlagsStoreForAdapter(t)
	overlay := channelflags.New()
	reg, unit := registryWithEventBus(t)

	const (
		centralName = "ccu-test"
		devC        = "DEVICE-C"
	)
	seedChannelFlags(t, store, overlay, centralName, devC)

	evictor := WireChannelFlagsEviction(reg, store, overlay, nil)
	evictor.Stop()

	events.Publish(unit.EventBus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBase(),
		CentralName: centralName,
		Address:     devC,
	})

	time.Sleep(20 * time.Millisecond)

	if n := channelFlagsRowCount(t, store, centralName, devC); n != 3 {
		t.Errorf("after unsubscribe: expected 3 rows (no delete), got %d", n)
	}
	evictor.Stop() // idempotent
}

// TestWireChannelFlagsEviction_NilGuards verifies that a nil store, overlay
// or registry yields a handle whose methods are safe no-ops.
func TestWireChannelFlagsEviction_NilGuards(t *testing.T) {
	t.Parallel()

	store := freshChannelFlagsStoreForAdapter(t)
	overlay := channelflags.New()
	reg, _ := registryWithEventBus(t)

	cases := []struct {
		name    string
		reg     bool
		store   bool
		overlay bool
	}{
		{"nil registry", false, true, true},
		{"nil store", true, false, true},
		{"nil overlay", true, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := reg
			s := store
			o := overlay
			if !tc.reg {
				r = nil
			}
			if !tc.store {
				s = nil
			}
			if !tc.overlay {
				o = nil
			}
			evictor := WireChannelFlagsEviction(r, s, o, nil)
			evictor.Stop()
			evictor.Stop()
			evictor.StartCentral(nil)()
		})
	}
}
