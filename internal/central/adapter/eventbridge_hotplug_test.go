// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// newHotplugBridgeEnv wires an EventBridge against a registered central
// (via registryWithDeviceNotReady — the ready latch stays with each test so
// the not-ready guard below can exercise the mid-bring-up window) and a
// NoopClient-backed MQTT wiring, mirroring the setup
// TestCentralSouthboundReadyEventTriggersPerCentralSnapshot uses for the
// sibling southbound-ready snapshot path.
func newHotplugBridgeEnv(t *testing.T) (*EventBridge, *mqtt.NoopClient) {
	t.Helper()

	reg, _ := registryWithDeviceNotReady(t)
	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	eb := NewEventBridge(reg, nil, mw)
	eb.Start(context.Background())
	t.Cleanup(eb.Stop)

	return eb, pub
}

// publishedForDevice reports whether the fake publisher recorded at least
// one topic (availability or otherwise) that names the given device address.
func publishedForDevice(pub *mqtt.NoopClient, address string) bool {
	for _, p := range pub.Published() {
		if strings.Contains(p.Topic, "/"+address+"/") {
			return true
		}
	}
	return false
}

// TestOnDeviceCreatedPublishesSnapshotWhenCentralReadyAndDeviceKnown pins the
// hot-plug path: a device that materialises after its central's southbound
// bring-up completed must reach the broker (availability + state) as soon as
// the DeviceCreatedEvent fires, without waiting for a daemon restart.
func TestOnDeviceCreatedPublishesSnapshotWhenCentralReadyAndDeviceKnown(t *testing.T) {
	t.Parallel()

	eb, pub := newHotplugBridgeEnv(t)
	unit, ok := eb.registry.Get("ccu-01")
	if !ok {
		t.Fatal("unit ccu-01 not registered")
	}
	unit.MarkSouthboundReady()

	events.Publish(unit.EventBus, hmevent.DeviceCreatedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-01",
		InterfaceID: "HmIP-RF",
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
		Source:      hmenum.SourceOfDeviceCreationNew,
	})
	// Barrier: the snapshot runs on the fan-out worker, not inline on the bus
	// dispatch goroutine.
	eb.Flush()

	var availability, info int
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, "/0001ABCD/availability") {
			availability++
		}
		if strings.Contains(p.Topic, "/0001ABCD/") {
			info++
		}
	}
	if availability == 0 {
		t.Fatalf("DeviceCreatedEvent on a ready central did not publish device availability; published=%+v", pub.Published())
	}
	if info == 0 {
		t.Fatalf("DeviceCreatedEvent on a ready central did not publish any device topic")
	}
}

// TestOnDeviceCreatedSkipsPublishWhileCentralNotSouthboundReady guards the
// boot-time exclusion: a DeviceCreatedEvent fired before the central signals
// southbound-ready must NOT trigger a snapshot publish — that inventory is
// covered by the southbound-ready snapshot pass instead, and the device may
// not be fully hydrated yet.
func TestOnDeviceCreatedSkipsPublishWhileCentralNotSouthboundReady(t *testing.T) {
	t.Parallel()

	eb, pub := newHotplugBridgeEnv(t)
	unit, ok := eb.registry.Get("ccu-01")
	if !ok {
		t.Fatal("unit ccu-01 not registered")
	}
	// Deliberately do NOT call unit.MarkSouthboundReady().

	events.Publish(unit.EventBus, hmevent.DeviceCreatedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-01",
		InterfaceID: "HmIP-RF",
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
		Source:      hmenum.SourceOfDeviceCreationNew,
	})
	eb.Flush()

	if publishedForDevice(pub, "0001ABCD") {
		t.Fatalf("DeviceCreatedEvent published before southbound-ready; published=%+v", pub.Published())
	}
}

// TestRenameDevicePublishesUpdatedNameOverMQTT is the regression guard for
// the rename-notifies-nothing defect: Unit.RenameDevice used to mutate only
// the live model, publish no event, and leave MQTT's retained device-info
// topic (and with it the HA-Discovery device name / suggested_area) at
// whatever publishDeviceSnapshot last observed. Drives the real production
// entry point (Unit.RenameDevice) rather than publishing the event by hand,
// so the test also proves RenameDevice itself fires the notification.
func TestRenameDevicePublishesUpdatedNameOverMQTT(t *testing.T) {
	t.Parallel()

	eb, pub := newHotplugBridgeEnv(t)
	unit, ok := eb.registry.Get("ccu-01")
	if !ok {
		t.Fatal("unit ccu-01 not registered")
	}
	unit.MarkSouthboundReady()

	// registryWithDeviceNotReady seeds device 0001ABCD named "Flur" — publish
	// its initial snapshot first so the test observes only the rename's
	// effect, not the first-ever publish.
	if err := unit.RenameDevice(context.Background(), "0001ABCD", "Wohnzimmer"); err != nil {
		t.Fatalf("RenameDevice: %v", err)
	}
	eb.Flush()

	var found bool
	for _, p := range pub.Published() {
		if strings.Contains(p.Topic, "/0001ABCD/") && strings.Contains(string(p.Payload), "Wohnzimmer") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("RenameDevice did not republish the device's MQTT snapshot with the new name; published=%+v", pub.Published())
	}
}

// TestRenameDeviceRestampsCachedDataPointName is the regression guard for
// the stale-name-cache defect: the MQTT builder prefers each data point's
// construction-time cached NameData quadruple over recomputing it live, so
// republishing the snapshot alone (as onDeviceMetadataChanged's first cut
// would) still emits the pre-rename name — the fix must also re-run
// [device.BuildDataPointName] for every data point. This test exercises an
// UNNAMED channel deliberately: [device.BuildDataPointName]'s channel-name
// fallback bakes the device name directly into the cached quadruple
// ("<deviceName>:<no>"), which is exactly the case a bare snapshot
// re-publish cannot fix by itself.
func TestRenameDeviceRestampsCachedDataPointName(t *testing.T) {
	t.Parallel()

	eb, _ := newHotplugBridgeEnv(t)
	unit, ok := eb.registry.Get("ccu-01")
	if !ok {
		t.Fatal("unit ccu-01 not registered")
	}
	unit.MarkSouthboundReady()

	dev, ok := unit.ModelRegistry.Get("0001ABCD")
	if !ok {
		t.Fatal("seed device 0001ABCD not found")
	}
	// Deliberately no explicit channel name, so BuildDataPointName falls
	// back to "<deviceName>:<no>" — the shape that bakes the device name
	// into the cached quadruple.
	ch := dev.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	dp := floatDP("0001ABCD:1", hmenum.ParameterLevel, nil)
	ch.Put(dp)
	// Seed the cache the way the ingest pipeline does at construction time.
	dp.SetNameData(device.BuildDataPointName(ch, string(hmenum.ParameterLevel), ""))
	if got := dp.NameData().DeviceName; got != "Flur" {
		t.Fatalf("precondition: cached DeviceName = %q, want %q", got, "Flur")
	}

	if err := unit.RenameDevice(context.Background(), "0001ABCD", "Wohnzimmer"); err != nil {
		t.Fatalf("RenameDevice: %v", err)
	}
	eb.Flush()

	if got := dp.NameData().DeviceName; got != "Wohnzimmer" {
		t.Fatalf("cached DeviceName after rename = %q, want %q — the MQTT discovery name stayed stale", got, "Wohnzimmer")
	}
}

// TestOnDeviceCreatedSkipsPublishForUnknownDevice guards the ModelRegistry
// lookup: a DeviceCreatedEvent naming an address the registry does not (yet)
// know about must be a silent no-op rather than a panic or a garbage publish
// — the event can race the registry write it announces.
func TestOnDeviceCreatedSkipsPublishForUnknownDevice(t *testing.T) {
	t.Parallel()

	eb, pub := newHotplugBridgeEnv(t)
	unit, ok := eb.registry.Get("ccu-01")
	if !ok {
		t.Fatal("unit ccu-01 not registered")
	}
	unit.MarkSouthboundReady()

	events.Publish(unit.EventBus, hmevent.DeviceCreatedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-01",
		InterfaceID: "HmIP-RF",
		Address:     "FFFFDEAD",
		Model:       "HmIP-STH",
		Source:      hmenum.SourceOfDeviceCreationNew,
	})
	eb.Flush()

	if publishedForDevice(pub, "FFFFDEAD") {
		t.Fatalf("DeviceCreatedEvent published for a device absent from ModelRegistry; published=%+v", pub.Published())
	}
}
