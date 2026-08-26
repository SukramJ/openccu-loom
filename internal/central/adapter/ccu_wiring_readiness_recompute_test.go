// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func readinessRecomputeTestConfig(centralName string) config.CentralConfig {
	return config.CentralConfig{
		Name: centralName,
		Interfaces: []config.InterfaceSpec{
			{Name: "HmIP-RF"},
			{Name: "CUxD"},
		},
	}
}

// TestWireReadinessRecomputeCountsARecoveredInterfaceBackIn is the
// regression guard for the boot-latched readiness tally: bringUpCentral
// records "interfaces loaded" only once, at the end of bring-up, so an
// interface whose boot ingest exhausted its retries and was later repaired
// by the recovery pipeline never moved the tally off its stale N-1/N value.
// The fix recomputes the tally from live registry state on every
// RecoveryCompletedEvent once the central has reached Ready.
func TestWireReadinessRecomputeCountsARecoveredInterfaceBackIn(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-readiness9"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	cc := readinessRecomputeTestConfig(c.Name())

	// Boot-time state: HmIP-RF ingested, CUxD exhausted its retries — the
	// exact "ready, N-1/N" state bringUpCentral latches per the finding.
	c.SetReadiness(hmenum.ReadinessReady, 1, 2)
	c.DeviceRegistry.Put(registry.DeviceEntry{
		Interface: hmtypes.ParseWireInterfaceID(WireInterfaceID(c.Name(), hmenum.InterfaceHmIPRF)),
		Address:   "DEV001",
	})

	closer := WireReadinessRecompute(c, cc, discardLogger())
	defer closer()

	var readinessEvents atomic.Int32
	unsub := events.Subscribe(c.EventBus, func(hmevent.CentralReadinessChangedEvent) {
		readinessEvents.Add(1)
	})
	defer unsub()

	// The recovery pipeline repairs CUxD and the hot-plug ingest that follows
	// materialises its device in the registry — this is the live-state signal
	// the recompute reads, not the RecoveryCompletedEvent payload itself.
	c.DeviceRegistry.Put(registry.DeviceEntry{
		Interface: hmtypes.ParseWireInterfaceID(WireInterfaceID(c.Name(), hmenum.InterfaceCUxD)),
		Address:   "DEV002",
	})
	events.Publish(c.EventBus, hmevent.RecoveryCompletedEvent{
		Base:        hmevent.NewBase(),
		CentralName: c.Name(),
		InterfaceID: WireInterfaceID(c.Name(), hmenum.InterfaceCUxD),
		Result:      hmenum.RecoveryResultSuccess,
	})

	r := c.Readiness()
	if r.InterfacesLoaded != 2 {
		t.Fatalf("InterfacesLoaded = %d, want 2 after the recovered interface materialised a device", r.InterfacesLoaded)
	}
	if r.InterfacesTotal != 2 {
		t.Fatalf("InterfacesTotal = %d, want 2", r.InterfacesTotal)
	}
	if got := readinessEvents.Load(); got != 1 {
		t.Fatalf("CentralReadinessChangedEvent fired %d times, want exactly 1", got)
	}
}

// TestWireReadinessRecomputeIsANoOpWhenTheCountDidNotChange verifies the
// recompute does not re-publish an identical readiness event for a recovery
// that did not change which interfaces are loaded — the same "changed event
// with a byte-identical payload" noise the install-mode poll finding flags
// elsewhere in this batch.
func TestWireReadinessRecomputeIsANoOpWhenTheCountDidNotChange(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-readiness-noop9"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	cc := readinessRecomputeTestConfig(c.Name())
	c.SetReadiness(hmenum.ReadinessReady, 2, 2)
	c.DeviceRegistry.Put(registry.DeviceEntry{
		Interface: hmtypes.ParseWireInterfaceID(WireInterfaceID(c.Name(), hmenum.InterfaceHmIPRF)),
		Address:   "DEV001",
	})
	c.DeviceRegistry.Put(registry.DeviceEntry{
		Interface: hmtypes.ParseWireInterfaceID(WireInterfaceID(c.Name(), hmenum.InterfaceCUxD)),
		Address:   "DEV002",
	})

	closer := WireReadinessRecompute(c, cc, discardLogger())
	defer closer()

	var readinessEvents atomic.Int32
	unsub := events.Subscribe(c.EventBus, func(hmevent.CentralReadinessChangedEvent) {
		readinessEvents.Add(1)
	})
	defer unsub()

	events.Publish(c.EventBus, hmevent.RecoveryCompletedEvent{
		Base:        hmevent.NewBase(),
		CentralName: c.Name(),
		InterfaceID: WireInterfaceID(c.Name(), hmenum.InterfaceHmIPRF),
		Result:      hmenum.RecoveryResultSuccess,
	})

	if got := readinessEvents.Load(); got != 0 {
		t.Fatalf("CentralReadinessChangedEvent fired %d times for an unchanged tally, want 0", got)
	}
}

// TestWireReadinessRecomputeIgnoresRecoveryBeforeReady verifies the
// recompute stays out of the way while bring-up itself still owns the
// tally: a RecoveryCompletedEvent that arrives before the central first
// reaches Ready must not race the bring-up path's own writes.
func TestWireReadinessRecomputeIgnoresRecoveryBeforeReady(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-readiness-early9"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	cc := readinessRecomputeTestConfig(c.Name())
	c.SetReadiness(hmenum.ReadinessLoadingDevices, 0, 2)

	closer := WireReadinessRecompute(c, cc, discardLogger())
	defer closer()

	var readinessEvents atomic.Int32
	unsub := events.Subscribe(c.EventBus, func(hmevent.CentralReadinessChangedEvent) {
		readinessEvents.Add(1)
	})
	defer unsub()

	c.DeviceRegistry.Put(registry.DeviceEntry{
		Interface: hmtypes.ParseWireInterfaceID(WireInterfaceID(c.Name(), hmenum.InterfaceHmIPRF)),
		Address:   "DEV001",
	})
	events.Publish(c.EventBus, hmevent.RecoveryCompletedEvent{
		Base:        hmevent.NewBase(),
		CentralName: c.Name(),
		InterfaceID: WireInterfaceID(c.Name(), hmenum.InterfaceHmIPRF),
		Result:      hmenum.RecoveryResultSuccess,
	})

	if got := readinessEvents.Load(); got != 0 {
		t.Fatalf("CentralReadinessChangedEvent fired %d times before Ready, want 0", got)
	}
	if r := c.Readiness(); r.Phase != hmenum.ReadinessLoadingDevices {
		t.Fatalf("Phase = %s, want unchanged %s", r.Phase, hmenum.ReadinessLoadingDevices)
	}
}
