// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/channelflags"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestUnpairedDeviceLosesItsChannelFlags pins what the channel-flags evictor
// DOES. That the composition root actually calls it is a separate question,
// answered by TestEveryWireFunctionHasAProductionCaller in tests/contract —
// this test constructs the collaboration itself and so could never have caught
// the seam being unwired.
//
// The operator's Hidden/Locked overrides are keyed on a channel address, and a
// CCU reuses addresses: swap a failed actuator for a new one and the
// replacement is very likely to land on the address the old one had. Until
// this wiring existed, `ChannelFlagsStore.DeleteDevice` had no production
// caller at all and the in-memory overlay had no per-device delete, so the
// replacement silently inherited the previous device's visibility decisions —
// a channel the operator had hidden years ago stayed hidden on hardware that
// had never been configured.
//
// The assertion is the effect, not the call: the rows and the overlay entry
// are gone after the removal event a real unpair publishes.
func TestUnpairedDeviceLosesItsChannelFlags(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	store := sqlitestore.NewChannelFlagsStore(openMigratedTestDB(t, "channel_flags_evict_test.db"))
	overlay := channelflags.New()
	reg := central.NewRegistry()

	// Constructed with the same arguments daemon_southbound.go passes, and
	// before the central joins — an evictor that only sees the centrals present
	// at boot is a defect this family of tests exists to catch.
	evictor := adapter.WireChannelFlagsEviction(reg, store, overlay, discardTestLogger())
	t.Cleanup(evictor.Stop)

	const (
		centralName = "ccu-flags"
		ifaceID     = "HmIP-RF"
		gone        = "GONEDEV01"
		kept        = "KEPTDEV01"
	)
	unit := registerChannelFlagsTestCentral(t, reg, centralName)

	for _, addr := range []string{gone + ":1", gone + ":2", kept + ":1"} {
		if err := store.Set(ctx, centralName, addr, true, false, "test"); err != nil {
			t.Fatalf("seed %s: %v", addr, err)
		}
		overlay.Set(centralName, addr, channelflags.Flags{Hidden: true})
	}

	events.Publish(unit.EventBus, hmevent.DeviceRemovedEvent{
		Base:        hmevent.NewBase(),
		CentralName: centralName,
		InterfaceID: ifaceID,
		Address:     gone,
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		rows, err := store.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		var goneRows, keptRows int
		for _, r := range rows {
			switch {
			case len(r.ChannelAddress) >= len(gone) && r.ChannelAddress[:len(gone)] == gone:
				goneRows++
			case len(r.ChannelAddress) >= len(kept) && r.ChannelAddress[:len(kept)] == kept:
				keptRows++
			}
		}
		overlayGone := overlay.Get(centralName, gone+":1").Hidden
		overlayKept := overlay.Get(centralName, kept+":1").Hidden

		if goneRows == 0 && !overlayGone {
			if keptRows != 1 || !overlayKept {
				t.Fatalf("the eviction took the wrong device with it: %d rows and overlay=%v "+
					"left for %s, which was never unpaired", keptRows, overlayKept, kept)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after unpairing %s its channel flags survive: %d persisted row(s), "+
				"overlay hidden=%v. A device paired into the same address inherits the "+
				"previous one's visibility decisions.", gone, goneRows, overlayGone)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestModelTeardownKeepsChannelFlags pins the other half. A cache-clear
// re-init removes every device from the model and immediately re-pulls them;
// the operator asked for none of them to go. Evicting on that event would wipe
// the whole central's Hidden/Locked overrides on an operation whose entire
// purpose is to refresh data, not to discard the operator's own settings.
func TestModelTeardownKeepsChannelFlags(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	store := sqlitestore.NewChannelFlagsStore(openMigratedTestDB(t, "channel_flags_teardown_test.db"))
	overlay := channelflags.New()
	reg := central.NewRegistry()

	evictor := adapter.WireChannelFlagsEviction(reg, store, overlay, discardTestLogger())
	t.Cleanup(evictor.Stop)

	const (
		centralName = "ccu-teardown"
		addr        = "TORNDOWN1"
	)
	unit := registerChannelFlagsTestCentral(t, reg, centralName)

	if err := store.Set(ctx, centralName, addr+":1", true, false, "test"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	overlay.Set(centralName, addr+":1", channelflags.Flags{Hidden: true})

	events.Publish(unit.EventBus, hmevent.DeviceRemovedEvent{
		Base:          hmevent.NewBase(),
		CentralName:   centralName,
		InterfaceID:   "HmIP-RF",
		Address:       addr,
		ModelTeardown: true,
	})

	// Give the handler the same window the positive test allows it, then assert
	// nothing happened. Polling for absence needs a settle, not a deadline.
	time.Sleep(250 * time.Millisecond)

	rows, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) == 0 {
		t.Error("a cache-clear teardown wiped the operator's channel flags; the re-init " +
			"removes every device without the operator asking for any of them to go")
	}
	if !overlay.Get(centralName, addr+":1").Hidden {
		t.Error("a cache-clear teardown wiped the in-memory channel-flags overlay")
	}
}

// registerChannelFlagsTestCentral joins one central to the registry, which is
// what makes the evictor's OnRegister observer subscribe its bus.
func registerChannelFlagsTestCentral(t *testing.T, reg *central.Registry, name string) *central.Unit {
	t.Helper()
	u, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New(%s): %v", name, err)
	}
	if err := reg.Register(u); err != nil {
		t.Fatalf("reg.Register(%s): %v", name, err)
	}
	return u
}
