// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func TestCacheCoordinator(t *testing.T) {
	c := NewCacheCoordinator()
	key := hmtypes.DataPointKey{InterfaceID: "i", ChannelAddress: "A:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "LEVEL"}
	c.Set(key, hmtypes.FloatValue(0.5), "test")
	e, ok := c.Get(key)
	if !ok || e.Value.Float != 0.5 {
		t.Fatalf("Get=%+v ok=%v", e, ok)
	}
	if c.Len() != 1 {
		t.Fatal("Len=1")
	}
	if !c.Delete(key) {
		t.Fatal("Delete missed")
	}
}

func TestEventCoordinatorConfigPendingFiresHookOnTrueToFalse(t *testing.T) {
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)
	var hits atomic.Int32
	var seenIface, seenAddress string
	ec.SetOnConfigSettled(func(iface, addr string) {
		hits.Add(1)
		seenIface = iface
		seenAddress = addr
	})

	// Initial value: CONFIG_PENDING = true (no hook yet — first
	// observation establishes the baseline).
	ec.HandleRawEvent(context.Background(), "HmIP-RF", "ABC0001:0", "CONFIG_PENDING", hmtypes.BoolValue(true))
	if hits.Load() != 0 {
		t.Fatalf("first event must not fire hook, got %d", hits.Load())
	}

	// Transition to false → hook must fire.
	ec.HandleRawEvent(context.Background(), "HmIP-RF", "ABC0001:0", "CONFIG_PENDING", hmtypes.BoolValue(false))
	if hits.Load() != 1 {
		t.Fatalf("True→False must fire hook once, got %d", hits.Load())
	}
	if seenIface != "HmIP-RF" || seenAddress != "ABC0001" {
		t.Fatalf("hook saw iface=%q address=%q (expected HmIP-RF / ABC0001)", seenIface, seenAddress)
	}

	// False → False must NOT fire again.
	ec.HandleRawEvent(context.Background(), "HmIP-RF", "ABC0001:0", "CONFIG_PENDING", hmtypes.BoolValue(false))
	if hits.Load() != 1 {
		t.Fatalf("repeated false must not re-fire hook, got %d", hits.Load())
	}

	// New True → False cycle on a different device fires again.
	ec.HandleRawEvent(context.Background(), "HmIP-RF", "DEF0002:0", "CONFIG_PENDING", hmtypes.BoolValue(true))
	ec.HandleRawEvent(context.Background(), "HmIP-RF", "DEF0002:0", "CONFIG_PENDING", hmtypes.BoolValue(false))
	if hits.Load() != 2 {
		t.Fatalf("second device True→False must fire hook, got %d", hits.Load())
	}
	if seenAddress != "DEF0002" {
		t.Fatalf("address=%q want DEF0002", seenAddress)
	}
}

func TestEventCoordinatorPublishesOnChange(t *testing.T) {
	bus := events.NewBus()
	var n atomic.Int32
	events.Subscribe(bus, func(e hmevent.DataPointValueChangedEvent) {
		n.Add(1)
		_ = e
	})
	ec := NewEventCoordinator(bus, NewCacheCoordinator(), nil)
	ec.HandleRawEvent(context.Background(), "iface", "A:1", "LEVEL", hmtypes.FloatValue(0.5))
	if n.Load() != 1 {
		t.Fatalf("first event not published, n=%d", n.Load())
	}
	// Same value → no second event.
	ec.HandleRawEvent(context.Background(), "iface", "A:1", "LEVEL", hmtypes.FloatValue(0.5))
	if n.Load() != 1 {
		t.Fatalf("no-change should not publish, n=%d", n.Load())
	}
	// Different value → new event.
	ec.HandleRawEvent(context.Background(), "iface", "A:1", "LEVEL", hmtypes.FloatValue(0.75))
	if n.Load() != 2 {
		t.Fatalf("change not published, n=%d", n.Load())
	}
}

func TestDeviceCoordinatorChecksForNewDeviceAddresses(t *testing.T) {
	bus := events.NewBus()
	devs := registry.NewDeviceRegistry()
	descs := registry.NewDeviceDescriptionRegistry()
	ps := registry.NewParamsetRegistry()
	dc := NewDeviceCoordinator("main", bus, devs, descs, ps, nil, nil)

	// Seed registry with one known device.
	dc.HandleNewDevices(context.Background(), wireKey(hmenum.InterfaceHmIPRF), []hmproto.DeviceDescription{
		{Address: "ABC0001"},
		{Address: "ABC0001:1", Parent: "ABC0001"},
	})

	// Snapshot reports the known device + a freshly-paired one.
	snapshot := []hmproto.DeviceDescription{
		{Address: "ABC0001"},
		{Address: "ABC0001:1", Parent: "ABC0001"},
		{Address: "DEF0002"},
		{Address: "DEF0002:0", Parent: "DEF0002"},
	}
	got := dc.CheckForNewDeviceAddresses(wireKey(hmenum.InterfaceHmIPRF), snapshot)
	if len(got) != 2 || got[0] != "DEF0002" || got[1] != "DEF0002:0" {
		t.Fatalf("expected [DEF0002 DEF0002:0], got %v", got)
	}

	// All known → empty result.
	allKnown := []hmproto.DeviceDescription{{Address: "ABC0001"}, {Address: "ABC0001:1"}}
	if got := dc.CheckForNewDeviceAddresses(wireKey(hmenum.InterfaceHmIPRF), allKnown); len(got) != 0 {
		t.Fatalf("all-known snapshot should produce empty result, got %v", got)
	}
}

func TestEventCoordinatorTracksLastEventPerInterface(t *testing.T) {
	bus := events.NewBus()
	ec := NewEventCoordinator(bus, NewCacheCoordinator(), nil)

	if _, observed := ec.LastEventMonotonicForInterface("iface"); observed {
		t.Fatalf("never-observed interface should report observed=false")
	}

	before := time.Now()
	ec.HandleRawEvent(context.Background(), "iface", "A:1", "LEVEL", hmtypes.FloatValue(0.5))
	stamp, observed := ec.LastEventMonotonicForInterface("iface")
	if !observed {
		t.Fatalf("interface should be observed after first event")
	}
	if stamp.Before(before) {
		t.Fatalf("stamp %v should be >= %v", stamp, before)
	}

	// Manual MarkEvent path (used by ping-pong / init).
	manual := time.Now().Add(time.Hour)
	ec.MarkEvent("iface2", manual)
	got, _ := ec.LastEventMonotonicForInterface("iface2")
	if !got.Equal(manual) {
		t.Fatalf("MarkEvent should set stamp, got %v want %v", got, manual)
	}
}

func TestDeviceCoordinatorRegistersAndRemoves(t *testing.T) {
	bus := events.NewBus()
	var created, removed atomic.Int32
	events.Subscribe(bus, func(e hmevent.DeviceCreatedEvent) { created.Add(1) })
	events.Subscribe(bus, func(e hmevent.DeviceRemovedEvent) { removed.Add(1) })

	devs := registry.NewDeviceRegistry()
	descs := registry.NewDeviceDescriptionRegistry()
	ps := registry.NewParamsetRegistry()
	dc := NewDeviceCoordinator("main", bus, devs, descs, ps, nil, nil)

	dc.HandleNewDevices(context.Background(), wireKey(hmenum.InterfaceHmIPRF), []hmproto.DeviceDescription{
		{Address: "A"},
		{Address: "A:1", Parent: "A"},
	})
	if devs.Len() != 1 || descs.Len() != 2 {
		t.Fatalf("devs=%d descs=%d", devs.Len(), descs.Len())
	}
	if created.Load() != 1 {
		t.Fatalf("created=%d, only the top-level device should fire", created.Load())
	}
	dc.HandleDeleteDevices(context.Background(), wireKey(hmenum.InterfaceHmIPRF), []string{"A"})
	if removed.Load() != 1 {
		t.Fatalf("removed=%d", removed.Load())
	}
}

func TestHubCoordinatorSysvarUpdate(t *testing.T) {
	bus := events.NewBus()
	var n atomic.Int32
	events.Subscribe(bus, func(e hmevent.SysvarChangedEvent) { n.Add(1) })
	h := NewHubCoordinator("main", bus)
	h.UpdateSysvar(context.Background(), SysvarSnapshot{Name: "X", Value: hmtypes.IntValue(1), ValueType: hmenum.HubValueTypeInteger})
	if n.Load() != 1 {
		t.Fatal("first update should publish")
	}
	h.UpdateSysvar(context.Background(), SysvarSnapshot{Name: "X", Value: hmtypes.IntValue(1), ValueType: hmenum.HubValueTypeInteger})
	if n.Load() != 1 {
		t.Fatal("no-change must not publish")
	}
	h.UpdateSysvar(context.Background(), SysvarSnapshot{Name: "X", Value: hmtypes.IntValue(2), ValueType: hmenum.HubValueTypeInteger})
	if n.Load() != 2 {
		t.Fatal("change must publish")
	}
	if len(h.Sysvars()) != 1 {
		t.Fatal("snapshot should have one sysvar")
	}
}

func TestConnectionRecoveryHappyPath(t *testing.T) {
	bus := events.NewBus()
	var started, completed atomic.Int32
	events.Subscribe(bus, func(e hmevent.RecoveryStartedEvent) { started.Add(1) })
	events.Subscribe(bus, func(e hmevent.RecoveryCompletedEvent) { completed.Add(1) })

	rc := NewConnectionRecoveryCoordinator("main", bus)
	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageTCPChecking, Run: func(context.Context) error { return nil }},
		{Stage: hmenum.RecoveryStageRPCChecking, Run: func(context.Context) error { return nil }},
		{Stage: hmenum.RecoveryStageReconnecting, Run: func(context.Context) error { return nil }},
	}
	res := rc.Run(context.Background(), "HmIP-RF", pipeline)
	if res != hmenum.RecoveryResultSuccess {
		t.Fatalf("result=%s", res)
	}
	if started.Load() != 1 || completed.Load() != 1 {
		t.Fatalf("started=%d completed=%d", started.Load(), completed.Load())
	}
}

func TestConnectionRecoveryStopsOnError(t *testing.T) {
	bus := events.NewBus()
	var failed atomic.Int32
	events.Subscribe(bus, func(e hmevent.RecoveryFailedEvent) { failed.Add(1) })
	rc := NewConnectionRecoveryCoordinator("main", bus)
	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageTCPChecking, Run: func(context.Context) error { return context.Canceled }},
	}
	res := rc.Run(context.Background(), "HmIP-RF", pipeline)
	if res != hmenum.RecoveryResultFailed {
		t.Fatalf("result=%s", res)
	}
	if failed.Load() != 1 {
		t.Fatalf("failed=%d", failed.Load())
	}
}
