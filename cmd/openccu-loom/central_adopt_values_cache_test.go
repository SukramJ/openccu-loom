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
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestAdoptedCentralValuesArePersistedByThePeriodicFlush pins the runtime-adopt
// half of the values-cache wiring through the orchestrator that drives it.
//
// The flusher and the eviction handler both subscribe per central, and both
// were wired from a single walk of the registry at boot. A CCU adopted over
// POST /admin/centrals was therefore never registered with the dirty tracker:
// every periodic tick skipped it, and its observed values reached SQLite only
// through the graceful-shutdown flush — so a SIGKILL, an OOM kill or a host
// reboot left its cache exactly as empty as it was at adoption, and the next
// cold boot restored nothing for it.
//
// The assertion is the effect: a value change on the adopted central's own bus
// must end up in the values_cache table on a subsequent tick.
func TestAdoptedCentralValuesArePersistedByThePeriodicFlush(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	store := sqlitestore.NewValuesCacheStore(openMigratedTestDB(t, "adopt_values_cache_test.db"))

	logger := discardTestLogger()
	reg := central.NewRegistry()

	// Wired exactly as the composition root wires it (daemon_southbound.go):
	// the registry is empty at this point, which is the whole point — the
	// central below joins afterwards.
	flusher := adapter.WireValuesCacheFlusher(reg, store, 5*time.Millisecond, logger)
	t.Cleanup(flusher.Stop)
	evictor := adapter.WireValuesCacheEviction(reg, store, logger)
	t.Cleanup(evictor.Stop)

	orch := buildLiveTestOrchestrator(ctx, t, reg, &config.Config{})
	orch.setValuesCacheCentralHook(func(u *central.Unit) func() {
		stopFlush := flusher.StartCentral(u)
		stopEvict := evictor.StartCentral(u)
		return func() {
			stopFlush()
			stopEvict()
		}
	})

	const centralName = "adopted-live"
	if err := orch.adoptCentral(ctx, unreachableTestCentralConfig(centralName)); err != nil {
		t.Fatalf("adoptCentral: %v", err)
	}
	unit, ok := reg.Get(centralName)
	if !ok {
		t.Fatal("adopted central is not in the registry")
	}

	// The adopted CCU reports a value: the model gains a live data point and
	// its bus carries the change, exactly as the callback path produces it.
	const ifaceID = "adopted-live-HmIP-RF"
	dev := device.New(device.Config{
		InterfaceID: ifaceID, Interface: hmenum.InterfaceHmIPRF,
		Address: "ADOPTED01", Model: "HmIP-PS",
	})
	unit.ModelRegistry.Put(dev)
	ch := dev.AddChannel("ADOPTED01:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	key := hmtypes.DataPointKey{
		InterfaceID: ifaceID, ChannelAddress: "ADOPTED01:1",
		ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "STATE",
	}
	state := generic.NewDataPoint[bool](generic.Spec{
		Key: key,
		Descriptor: hmproto.ParameterData{
			Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	state.OnWireValue(true)
	ch.Put(state)
	events.Publish(unit.EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(), Key: key, NewValue: hmtypes.BoolValue(true),
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		rows, err := store.LoadChannel(ctx, centralName, ifaceID, "ADOPTED01:1")
		if err != nil {
			t.Fatalf("LoadChannel: %v", err)
		}
		if len(rows) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no values_cache row for the adopted central after repeated flush ticks; " +
				"it was never registered with the flusher, so every tick skipped it")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
