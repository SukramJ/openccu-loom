// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestCentralSouthboundReadyEventTriggersPerCentralSnapshot pins the gated-
// startup north-bound path: because each central's devices load asynchronously
// (behind the CCU-readiness gate), the boot-time PublishInitialSnapshot may run
// before they exist. The EventBridge therefore subscribes to
// CentralSouthboundReadyEvent and publishes THAT central's snapshot when it
// fires, so its devices reach the broker without waiting for a restart.
func TestCentralSouthboundReadyEventTriggersPerCentralSnapshot(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	if !dp.OnWireValue(true) {
		t.Fatalf("OnWireValue refused to seed")
	}

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	eb := NewEventBridge(reg, nil, mw)
	eb.Start(context.Background())
	defer eb.Stop()

	// Crucially: do NOT call PublishInitialSnapshot. Only the per-central
	// southbound-ready event should drive the publish.
	unit, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("unit ccu-01 not registered")
	}
	// Production latches the ready flag BEFORE publishing the event
	// (gatedCentralBringUp) — and the snapshot pass gates on it.
	unit.MarkSouthboundReady()
	events.Publish(unit.EventBus, hmevent.CentralSouthboundReadyEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-01",
	})
	// Barrier: the snapshot runs on the fan-out worker, not inline on the bus
	// dispatch goroutine.
	eb.Flush()

	var stateTopics, availability int
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, "/0001ABCD/1/values/STATE") {
			stateTopics++
		}
		if strings.HasSuffix(p.Topic, "/0001ABCD/availability") {
			availability++
		}
	}
	if stateTopics == 0 {
		t.Fatalf("CentralSouthboundReadyEvent did not publish the device's STATE snapshot")
	}
	if availability == 0 {
		t.Fatalf("CentralSouthboundReadyEvent did not publish the device availability")
	}
}

// TestPublishCentralSnapshotUnknownCentralIsNoop verifies the per-central
// publish is a safe no-op for a central that is not registered.
func TestPublishCentralSnapshotUnknownCentralIsNoop(t *testing.T) {
	t.Parallel()

	reg, _ := registryWithDevice(t)
	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true}, pub)
	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	eb.Start(context.Background())
	defer eb.Stop()

	eb.PublishCentralSnapshot(context.Background(), "does-not-exist")

	if got := len(pub.Published()); got != 0 {
		t.Fatalf("unknown-central snapshot published %d topics, want 0", got)
	}
}

// snapshotUnitWithDevice returns a registry whose single central is
// southbound-ready and holds one device with a seeded data point, so a
// snapshot pass has something to publish.
func snapshotUnitWithDevice(t *testing.T) *central.Registry {
	t.Helper()
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	if !dp.OnWireValue(true) {
		t.Fatal("OnWireValue refused to seed")
	}
	unit, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("unit ccu-01 not registered")
	}
	unit.MarkSouthboundReady()
	return reg
}

// snapshotHookBridge builds an EventBridge over pub and returns a reader for
// how often the post-snapshot hook fired.
func snapshotHookBridge(t *testing.T, reg *central.Registry, pub mqtt.Publisher) (eb *EventBridge, hookFired func() int) {
	t.Helper()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
	}, pub)
	eb = NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	var fired atomic.Int64
	eb.SetPostCentralSnapshotHook(func(context.Context, string) { fired.Add(1) })
	return eb, func() int { return int(fired.Load()) }
}

// TestPublishCentralSnapshotArmsSweepsOnlyOnFullSuccess pins the guard on the
// retained-orphan sweeps. The bridge records a discovery topic as declared
// only after the broker accepted it, and the sweeps the hook launches delete
// every retained config that is not in that set. A pass that lost publishes
// must therefore not arm them, or it deletes exactly the entities that failed
// to publish.
func TestPublishCentralSnapshotArmsSweepsOnlyOnFullSuccess(t *testing.T) {
	t.Parallel()

	t.Run("broker_rejects", func(t *testing.T) {
		t.Parallel()
		eb, fired := snapshotHookBridge(t, snapshotUnitWithDevice(t), failingPublisher{})
		eb.PublishInitialSnapshot(context.Background())
		if got := fired(); got != 0 {
			t.Fatalf("post-snapshot hook fired %d times after failed publishes, want 0", got)
		}
	})

	t.Run("broker_accepts", func(t *testing.T) {
		t.Parallel()
		eb, fired := snapshotHookBridge(t, snapshotUnitWithDevice(t), mqtt.NewNoopClient())
		eb.PublishInitialSnapshot(context.Background())
		if got := fired(); got != 1 {
			t.Fatalf("post-snapshot hook fired %d times after a clean pass, want 1", got)
		}
	})
}
