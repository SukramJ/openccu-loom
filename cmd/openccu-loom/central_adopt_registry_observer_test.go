// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestAdoptCentralAttachesRegistryObserversBeforeTheUnitStarts pins the
// ordering guarantee the registry observer replaces the old registrar with.
//
// The hooks the composition root used to register ran after Unit.Start, so the
// very first SystemStatusChangedEvent — the one Start emits from its initial
// state evaluation, before any interface has reported in — reached nobody for
// an adopted CCU. The observer runs inside Registry.Register, which
// adoptCentral calls before Start and long before the south-bound bring-up is
// launched, so that first event already has its listener.
//
// The assertion is the effect: an observer registered before the adopt sees
// the adopted central's opening status event without anyone attaching it.
func TestAdoptCentralAttachesRegistryObserversBeforeTheUnitStarts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	reg := central.NewRegistry()
	orch := buildLiveTestOrchestrator(ctx, t, reg, &config.Config{})

	var mu sync.Mutex
	var seen int
	remove := reg.OnRegister(func(u *central.Unit) func() {
		return events.Subscribe(u.EventBus, func(hmevent.SystemStatusChangedEvent) {
			mu.Lock()
			seen++
			mu.Unlock()
		})
	})
	t.Cleanup(remove)

	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig("adopted")); err != nil {
		t.Fatalf("adoptCentral: %v", err)
	}

	mu.Lock()
	afterAdopt := seen
	mu.Unlock()
	if afterAdopt == 0 {
		t.Fatal("no SystemStatusChangedEvent reached the observer; it was attached after " +
			"Unit.Start, so the adopted central's opening status was lost")
	}

	unit, ok := reg.Get("adopted")
	if !ok {
		t.Fatal("adopted central not present in the registry")
	}

	// Removal detaches through the same ledger: the central's own bus, which
	// outlives the registry entry, must reach the observer no more.
	if err := orch.removeCentral(ctx, "adopted"); err != nil {
		t.Fatalf("removeCentral: %v", err)
	}
	mu.Lock()
	afterRemove := seen
	mu.Unlock()

	events.Publish(unit.EventBus, hmevent.SystemStatusChangedEvent{CentralName: "adopted"})
	mu.Lock()
	defer mu.Unlock()
	if seen != afterRemove {
		t.Errorf("the observer saw %d further event(s) after removeCentral, want 0", seen-afterRemove)
	}
}
