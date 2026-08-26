// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestGatedBringUpReadinessPhaseSequence pins the readiness phase sequence a
// successful gated bring-up drives through: waiting_for_ccu -> loading_hub ->
// loading_devices (once per interface as it wires) -> ready. The calls below
// reproduce, in order, exactly what gatedCentralBringUp and bringUpCentral
// invoke around the CCU-readiness gate, the hub load, and the per-interface
// wiring loop — driving the full path via a live CCU + JSON-RPC session is
// out of scope for a unit test, so this exercises the shared
// recordCentralReadiness/recordCentralWaiting building blocks those functions
// call, which is where the phase transitions and event publishing actually
// happen.
func TestGatedBringUpReadinessPhaseSequence(t *testing.T) {
	t.Parallel()

	_, unit := registryWithUnit(t, "ccu-readiness-seq")

	var got []hmevent.CentralReadinessChangedEvent
	unsub := events.Subscribe(unit.EventBus, func(e hmevent.CentralReadinessChangedEvent) {
		got = append(got, e)
	})
	defer unsub()

	// Mirrors gatedCentralBringUp's initial recordCentralWaiting call.
	recordCentralWaiting(unit)

	// Mirrors bringUpCentral: hub load starts.
	recordCentralReadiness(unit, hmenum.ReadinessLoadingHub, 0, 0)

	// Mirrors bringUpCentral: per-interface wiring loop, two configured
	// interfaces, both wired successfully.
	const total = 2
	recordCentralReadiness(unit, hmenum.ReadinessLoadingDevices, 0, total)
	recordCentralReadiness(unit, hmenum.ReadinessLoadingDevices, 1, total)
	recordCentralReadiness(unit, hmenum.ReadinessLoadingDevices, 2, total)

	// Mirrors gatedCentralBringUp's success tail.
	recordCentralReadiness(unit, hmenum.ReadinessReady, total, total)

	wantPhases := []hmenum.ReadinessPhase{
		hmenum.ReadinessWaitingForCCU,
		hmenum.ReadinessLoadingHub,
		hmenum.ReadinessLoadingDevices,
		hmenum.ReadinessLoadingDevices,
		hmenum.ReadinessLoadingDevices,
		hmenum.ReadinessReady,
	}
	if len(got) != len(wantPhases) {
		t.Fatalf("got %d readiness events, want %d: %+v", len(got), len(wantPhases), got)
	}
	for i, wantPhase := range wantPhases {
		if got[i].Phase != wantPhase {
			t.Errorf("event[%d].Phase = %q, want %q", i, got[i].Phase, wantPhase)
		}
		if got[i].CentralName != unit.Name() {
			t.Errorf("event[%d].CentralName = %q, want %q", i, got[i].CentralName, unit.Name())
		}
	}

	// The interface-loaded counters only advance on the loading_devices
	// events, and only count successful wires — never regress or exceed total.
	loadingEvents := got[2:5]
	for i, e := range loadingEvents {
		if e.InterfacesLoaded != i {
			t.Errorf("loading_devices event[%d].InterfacesLoaded = %d, want %d", i, e.InterfacesLoaded, i)
		}
		if e.InterfacesTotal != total {
			t.Errorf("loading_devices event[%d].InterfacesTotal = %d, want %d", i, e.InterfacesTotal, total)
		}
	}

	// The terminal ready event reports every interface loaded.
	final := got[len(got)-1]
	if final.InterfacesLoaded != total || final.InterfacesTotal != total {
		t.Errorf("ready event counts = (%d, %d), want (%d, %d)", final.InterfacesLoaded, final.InterfacesTotal, total, total)
	}

	// The unit's queryable Readiness() view reflects the final transition.
	r := unit.Readiness()
	if r.Phase != hmenum.ReadinessReady {
		t.Errorf("unit.Readiness().Phase = %q, want %q", r.Phase, hmenum.ReadinessReady)
	}
	if r.InterfacesLoaded != total || r.InterfacesTotal != total {
		t.Errorf("unit.Readiness() counts = (%d, %d), want (%d, %d)", r.InterfacesLoaded, r.InterfacesTotal, total, total)
	}
}

// TestGatedBringUpReadinessReGatesOnHubFailure verifies that when the hub
// load fails after the CCU reported ready, gatedCentralBringUp's re-gate path
// (recordCentralWaiting again) is reflected in Readiness() — a failed
// interface wire never advances the phase past loading_hub, and the central
// goes back to waiting_for_ccu rather than staying stuck loading_hub forever.
func TestGatedBringUpReadinessReGatesOnHubFailure(t *testing.T) {
	t.Parallel()

	_, unit := registryWithUnit(t, "ccu-readiness-regate")

	recordCentralWaiting(unit)
	recordCentralReadiness(unit, hmenum.ReadinessLoadingHub, 0, 0)
	if got := unit.Readiness().Phase; got != hmenum.ReadinessLoadingHub {
		t.Fatalf("precondition: Readiness().Phase = %q, want %q", got, hmenum.ReadinessLoadingHub)
	}

	// Hub load fails; gatedCentralBringUp re-gates.
	recordCentralWaiting(unit)

	r := unit.Readiness()
	if r.Phase != hmenum.ReadinessWaitingForCCU {
		t.Errorf("Readiness().Phase after re-gate = %q, want %q", r.Phase, hmenum.ReadinessWaitingForCCU)
	}
	if r.InterfacesLoaded != 0 || r.InterfacesTotal != 0 {
		t.Errorf("Readiness() counts after re-gate = (%d, %d), want (0, 0)", r.InterfacesLoaded, r.InterfacesTotal)
	}
}
