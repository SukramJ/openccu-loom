// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestIngestPopulatesIseIDFromDeviceDetails verifies the device-population half
// of the sysvar-to-device association chain: IngestFromBackend stamps
// Device.IseID and Channel.IseID from the DeviceDetails cache (which WireHub
// seeds before ingest), and the resulting model resolves a system-variable
// name carrying either ise_id to the owning channel via
// Device.IdentifyChannel. Without the pipeline reading GetAddressID, both ids
// stay 0 and only the address-suffix match would ever fire.
func TestIngestPopulatesIseIDFromDeviceDetails(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-01"})
	// Seed the DeviceDetails cache the way WireHub's populateDeviceDetailsCache
	// does, before the ingest runs.
	c.DeviceDetails.AddAddressISEID("0001ABCD", 100)
	c.DeviceDetails.AddAddressISEID("0001ABCD:1", 200)

	p := NewDevicePipeline(c).WithVisibility(newProductionVisibilityGate())
	b := newHydratingBackend()
	vw := &fakeWriter{}

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, vw, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("0001ABCD")
	if !ok {
		t.Fatal("device not in registry after IngestFromBackend")
	}
	if dev.IseID != 100 {
		t.Fatalf("Device.IseID = %d, want 100 (not stamped from DeviceDetails cache)", dev.IseID)
	}
	ch := dev.Channel("0001ABCD:1")
	if ch == nil {
		t.Fatal("channel 0001ABCD:1 not found")
	}
	if ch.IseID != 200 {
		t.Fatalf("Channel.IseID = %d, want 200 (not stamped from DeviceDetails cache)", ch.IseID)
	}

	// End-to-end: a sysvar name carrying the device ise_id (100) or the channel
	// ise_id (200) as a standalone token now resolves to the channel — the
	// association only works because the ids above were populated.
	if got := dev.IdentifyChannel("EnergyCounter 100"); got == nil || got.Address != "0001ABCD:1" {
		t.Fatalf("IdentifyChannel(device ise_id 100): got %v, want channel 0001ABCD:1", got)
	}
	if got := dev.IdentifyChannel("Sabotage 200"); got == nil || got.Address != "0001ABCD:1" {
		t.Fatalf("IdentifyChannel(channel ise_id 200): got %v, want channel 0001ABCD:1", got)
	}
}
