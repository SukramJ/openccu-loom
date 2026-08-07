// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestEventBridgePublishesUpdateForEveryDevice pins the end-to-end
// requirement that PublishInitialSnapshot emits a firmware-state
// publish and an HA-Discovery retain for every updatable device.
//
// Contract:
// - A registry with 2 updatable devices → at least 2 publishes to
// topics ending in ".../update/state" (one per device).
// - The state JSON contains the required "firmware" key.
// - The HA-Discovery publish goes to "homeassistant/update/..." when
// HADiscoveryEnabled is true.
func TestEventBridgePublishesUpdateForEveryDevice(t *testing.T) {
	t.Parallel()

	// Build a registry with two updatable devices.
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Production order: the snapshot only observes a central after its
	// bring-up latched ready.
	c.MarkSouthboundReady()

	dev1 := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "AABB1100", Model: "HmIP-eTRV-2", Name: "Heizung Bad",
		Updatable: true,
		Firmware:  device.FirmwareInfo{Current: "1.0.0"},
	})
	dev2 := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "AABB2200", Model: "HmIP-STH", Name: "Sensor Flur",
		Updatable: true,
		Firmware:  device.FirmwareInfo{Current: "2.0.0"},
	})
	dev3 := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "CCDD3300", Model: "HmIP-SWDO", Name: "Rollladen",
		Updatable: false, // NOT updatable — must NOT produce an update entity.
	})
	c.ModelRegistry.Put(dev1)
	c.ModelRegistry.Put(dev2)
	c.ModelRegistry.Put(dev3)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               "openccu-loom",
		CentralName:        "ccu-01",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, pub)
	mw := mqtt.NewWiring(bridge, nil)

	eb := NewEventBridge(reg, nil, mw)
	eb.Start(context.Background())
	defer eb.Stop()

	eb.PublishInitialSnapshot(context.Background())

	got := pub.Published()

	// Count update state topics (one per updatable device).
	updateStateCount := 0
	updateDiscoveryCount := 0
	nonUpdatableUpdateState := 0

	for _, p := range got {
		if strings.HasSuffix(p.Topic, "/update") {
			updateStateCount++
			// Verify the JSON payload contains the "firmware" key.
			if !strings.Contains(string(p.Payload), `"firmware"`) {
				t.Errorf("update payload missing \"firmware\" key: %s", p.Payload)
			}
		}
		if strings.HasPrefix(p.Topic, "homeassistant/update/") {
			updateDiscoveryCount++
		}
		// The non-updatable device must never appear in update topics.
		if strings.Contains(p.Topic, "/CCDD3300/") && strings.Contains(p.Topic, "update") {
			nonUpdatableUpdateState++
		}
	}

	if updateStateCount != 2 {
		t.Errorf("expected 2 update topic publishes (one per updatable device), got %d (topics=%v)",
			updateStateCount, topicsOf(got))
	}
	if updateDiscoveryCount != 2 {
		t.Errorf("expected 2 homeassistant/update/... discovery publishes, got %d (topics=%v)",
			updateDiscoveryCount, topicsOf(got))
	}
	if nonUpdatableUpdateState != 0 {
		t.Errorf("non-updatable device CCDD3300 must not produce any update topics; got %d", nonUpdatableUpdateState)
	}
}

// topicsOf extracts the topic strings from a publication slice for readable
// test failure output.
func topicsOf(ps []mqtt.Publication) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Topic
	}
	return out
}
