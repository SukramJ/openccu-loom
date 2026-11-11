// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestRemovedDevicePurgesItsRecordingOverrides pins the eviction with the
// overlay the daemon builds (wireRecordingOverrides) and asserts the effect
// in the database and in the overlay the forwarder reads.
//
// The CCU reuses addresses when hardware is swapped, so without the purge a
// replacement paired into the same address inherits the forwarding
// decisions an operator made for the previous device.
func TestRemovedDevicePurgesItsRecordingOverrides(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := openMigratedTestDB(t, "recording_override_evict.db")
	cfg := &config.Config{DataDir: t.TempDir()}
	logger := discardTestLogger()
	store, overlay := wireRecordingOverrides(ctx, cfg, db, logger)
	if store == nil || overlay == nil {
		t.Fatal("wireRecordingOverrides returned nil with a database")
	}

	reg := central.NewRegistry()
	// Wired as the composition root wires it (daemon_southbound.go), against
	// an empty registry: the central joins afterwards, which is what makes
	// this an adopt-safe seam rather than a boot-time walk.
	evictor := adapter.WireRecordingOverrideEviction(reg, overlay, logger)
	if evictor == nil {
		t.Fatal("WireRecordingOverrideEviction returned nil for a present overlay")
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

	if err := overlay.Set(ctx, centralName, ifaceID, channel, "POWER", false, "evict-test"); err != nil {
		t.Fatalf("overlay.Set: %v", err)
	}
	if overlay.Decide(centralName, ifaceID, channel, "POWER", true) {
		t.Fatal("precondition: the override must force POWER off")
	}

	c.EventBus.Publish(hmevent.DeviceRemovedEvent{
		CentralName: centralName, InterfaceID: ifaceID, Address: addr,
	})

	// The handler runs on the bus and bounds its own delete; poll rather
	// than reading once, so this measures the effect and not the scheduler.
	deadline := time.Now().Add(5 * time.Second)
	for {
		remaining, err := store.List(ctx)
		if err != nil {
			t.Fatalf("store.List: %v", err)
		}
		inOverlay := !overlay.Decide(centralName, ifaceID, channel, "POWER", true)
		if len(remaining) == 0 && !inOverlay {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after removal: %d stored override(s) remain, overlay still deciding: %v; want none",
				len(remaining), inOverlay)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
