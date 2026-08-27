// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestSnapshotWithholdsUnreleasedDevicesFromMQTT pins the Home-Assistant
// half of the release gate, through the publisher every MQTT path funnels
// into.
//
// The observable is the availability cache: publishDeviceSnapshot's first
// act is markAvailability, which records the device before any broker
// call. A gated device never reaches it. That makes the assertion
// independent of a broker while still driving the real function.
//
// This is the gate that matters most in practice — it is the one an
// operator notices, because a device published before the wizard finishes
// arrives in Home Assistant with the entity ids of its unnamed self, and
// renaming afterwards does not take those back.
func TestSnapshotWithholdsUnreleasedDevicesFromMQTT(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-mqtt-gate"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	ctx := context.Background()
	iface := hmtypes.ParseWireInterfaceID("ccu-mqtt-gate-HmIP-RF")

	c.Devices.StoreDelayedDeviceDescriptions(ctx, iface, gateDescs()[:2])
	_ = c.Devices.TakeDelayedDeviceDescriptions(ctx, iface, "GATE0001")
	p := NewDevicePipeline(c)
	if err := p.Ingest(ctx, string(iface), hmenum.InterfaceHmIPRF, gateDescs()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	b := &EventBridge{registry: reg}

	held, ok := c.ModelRegistry.Get("GATE0001")
	if !ok {
		t.Fatal("the withheld device was not materialised — the wizard would have nothing to configure")
	}
	b.publishDeviceSnapshot(ctx, c.Name(), held)
	if _, published := b.availabilityCache.Load(
		availabilityCacheKey(c.Name(), held.InterfaceID, held.Address),
	); published {
		t.Error("the withheld device was published to MQTT — it reaches Home Assistant before the operator named it")
	}

	// Negative control: releasing it must publish. Without this half the
	// test would pass on a gate that withholds everything, forever.
	if !ReleaseDevice(ctx, c, "GATE0001") {
		t.Fatal("ReleaseDevice reported nothing to release")
	}
	b.publishDeviceSnapshot(ctx, c.Name(), held)
	if _, published := b.availabilityCache.Load(
		availabilityCacheKey(c.Name(), held.InterfaceID, held.Address),
	); !published {
		t.Error("the released device was still withheld from MQTT")
	}
}
