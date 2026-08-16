// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestWireMatterCentralReadiness_LatchesOnSouthboundReady verifies the
// per-central readiness latch: a central starts model-incomplete and flips to
// ready exactly when its CentralSouthboundReadyEvent fires, independently of
// its sibling centrals.
func TestWireMatterCentralReadiness_LatchesOnSouthboundReady(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-a", "ccu-b")
	readiness, unwire := wireMatterCentralReadiness(reg)
	t.Cleanup(unwire)
	if readiness.isReady("ccu-a") || readiness.isReady("ccu-b") {
		t.Fatal("centrals must start model-incomplete before their ready event")
	}

	unitA, _ := reg.Get("ccu-a")
	events.Publish(unitA.EventBus, hmevent.CentralSouthboundReadyEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-a",
	})

	if !readiness.isReady("ccu-a") {
		t.Error("ccu-a should be ready after its southbound-ready event")
	}
	if readiness.isReady("ccu-b") {
		t.Error("ccu-b must stay model-incomplete; only ccu-a signalled ready")
	}
}

// TestWireMatterCentralReadiness_SeedsFromLatchedUnitFlag closes the
// boot-window race: southbound bring-up goroutines start before the Matter
// bridge subscribes readiness, so a fast CCU can fire its
// CentralSouthboundReadyEvent before any subscriber exists. The unit's
// latched flag must seed the tracker at subscribe time — WITHOUT any event
// delivery — and the snapshotter must stamp ModelComplete accordingly, or
// the central would stay model-incomplete (endpoint GC deferred) for the
// whole process lifetime.
func TestWireMatterCentralReadiness_SeedsFromLatchedUnitFlag(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-a", "ccu-b")
	unitA, _ := reg.Get("ccu-a")
	// The bring-up completed (and its ready event fired) before the Matter
	// wiring exists — only the latched flag survives.
	unitA.MarkSouthboundReady()

	readiness, unwire := wireMatterCentralReadiness(reg)
	t.Cleanup(unwire)

	if !readiness.isReady("ccu-a") {
		t.Error("ccu-a should be seeded ready from the unit's latched flag, without any event")
	}
	if readiness.isReady("ccu-b") {
		t.Error("ccu-b must stay model-incomplete; its bring-up has not completed")
	}

	byName := map[string]endpoint.Snapshot{}
	for _, s := range matterSnapshotter(reg, readiness)(context.Background()) {
		byName[s.CentralName] = s
	}
	if !byName["ccu-a"].ModelComplete {
		t.Error("ccu-a: ModelComplete = false, want true (seeded from latched flag)")
	}
	if byName["ccu-b"].ModelComplete {
		t.Error("ccu-b: ModelComplete = true without readiness, want false")
	}
}

// TestWireMatterCentralReadiness_UnsubscribeStopsLatching verifies teardown:
// after the closers run, a late ready event no longer mutates the latch.
func TestWireMatterCentralReadiness_UnsubscribeStopsLatching(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-a")
	readiness, unwire := wireMatterCentralReadiness(reg)
	unwire()

	unitA, _ := reg.Get("ccu-a")
	events.Publish(unitA.EventBus, hmevent.CentralSouthboundReadyEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-a",
	})

	if readiness.isReady("ccu-a") {
		t.Error("ready event after unsubscribe must not latch readiness")
	}
}

// TestWireMatterCentralReadiness_NilRegistry returns an empty (all
// model-incomplete) latch and no closers so callers stay nil-safe.
func TestWireMatterCentralReadiness_NilRegistry(t *testing.T) {
	t.Parallel()
	readiness, unwire := wireMatterCentralReadiness(nil)
	if readiness == nil {
		t.Fatal("readiness must be non-nil even without a registry")
	}
	if unwire == nil {
		t.Fatal("the removal closure must be non-nil even without a registry")
	}
	unwire()
	if readiness.isReady("anything") {
		t.Error("nil-registry latch must report model-incomplete for every central")
	}
}

// TestMatterSnapshotter_StampsModelCompletePerCentral is the boot-wipe
// regression at the daemon layer: the snapshotter feeding the bridge's
// assembly must mark a registered-but-not-yet-loaded central as
// model-incomplete so the assembler keeps its persisted endpoint-ID rows,
// and must flip the flag to true once that central's southbound-ready
// event has been observed.
func TestMatterSnapshotter_StampsModelCompletePerCentral(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-a", "ccu-b")
	readiness, unwire := wireMatterCentralReadiness(reg)
	t.Cleanup(unwire)
	snap := matterSnapshotter(reg, readiness)

	// Boot shape: registered centrals, no device load completed yet.
	byName := map[string]endpoint.Snapshot{}
	for _, s := range snap(context.Background()) {
		byName[s.CentralName] = s
	}
	if len(byName) != 2 {
		t.Fatalf("snapshots = %d, want 2 (one per central)", len(byName))
	}
	for name, s := range byName {
		if s.ModelComplete {
			t.Errorf("%s: ModelComplete = true at boot, want false (device load pending)", name)
		}
	}

	// ccu-a completes its initial device load.
	unitA, _ := reg.Get("ccu-a")
	events.Publish(unitA.EventBus, hmevent.CentralSouthboundReadyEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-a",
	})

	byName = map[string]endpoint.Snapshot{}
	for _, s := range snap(context.Background()) {
		byName[s.CentralName] = s
	}
	if !byName["ccu-a"].ModelComplete {
		t.Error("ccu-a: ModelComplete = false after ready event, want true")
	}
	if byName["ccu-b"].ModelComplete {
		t.Error("ccu-b: ModelComplete = true without ready event, want false")
	}
}

// TestMatterSnapshotter_NilReadinessFailsSafe pins the fail-safe direction: a
// snapshotter without a readiness latch must mark every central
// model-incomplete so the assembler never garbage-collects persisted
// endpoint-ID rows on unvouched data.
func TestMatterSnapshotter_NilReadinessFailsSafe(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-a")
	snap := matterSnapshotter(reg, nil)
	snaps := snap(context.Background())
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(snaps))
	}
	if snaps[0].ModelComplete {
		t.Error("nil readiness must stamp ModelComplete = false")
	}
}

// TestUnregisteringACentralClearsItsReadinessLatch is the endpoint-wipe
// regression for centrals the live-adopt orchestrator never adopted.
//
// The latch is the one piece of per-central Matter state that does not ride
// the unit's own EventBus, so nothing about stopping the unit drops it. While
// it was wired per adopted central only, disabling a boot-configured CCU and
// re-enabling it left the latch set: the fresh unit registers with an empty
// ModelRegistry, the stale latch stamps that empty snapshot ModelComplete, and
// the assembler deletes every persisted endpoint row of the CCU — the fleet
// renumbers and controllers lose all of its accessories.
func TestUnregisteringACentralClearsItsReadinessLatch(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-boot")
	readiness, unwire := wireMatterCentralReadiness(reg)
	t.Cleanup(unwire)

	unit, _ := reg.Get("ccu-boot")
	events.Publish(unit.EventBus, hmevent.CentralSouthboundReadyEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-boot",
	})
	if !readiness.isReady("ccu-boot") {
		t.Fatal("central should be latched ready after its southbound-ready event")
	}

	if !reg.Unregister("ccu-boot") {
		t.Fatal("Unregister reported the central was not registered")
	}
	if readiness.isReady("ccu-boot") {
		t.Fatal("readiness latch survived the central leaving the registry — the next unit under that name would be stamped model-complete while empty")
	}

	// The re-add: a fresh unit under the same name must start
	// model-incomplete so its persisted endpoint rows survive the refill.
	fresh, err := central.New(central.Config{Name: "ccu-boot", Logger: discardTestLogger()})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(fresh); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	if readiness.isReady("ccu-boot") {
		t.Error("a re-registered central must start model-incomplete")
	}
	for _, s := range matterSnapshotter(reg, readiness)(context.Background()) {
		if s.CentralName == "ccu-boot" && s.ModelComplete {
			t.Error("snapshot of a re-registered, still-empty central is stamped ModelComplete — the assembler would GC its endpoints")
		}
	}
}
