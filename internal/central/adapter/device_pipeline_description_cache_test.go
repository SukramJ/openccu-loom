// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// fleetDescriptions is one device root with two channels, twice over — the
// smallest shape that has both address kinds and more than one device.
func fleetDescriptions() []hmproto.DeviceDescription {
	return []hmproto.DeviceDescription{
		{Address: "FLEET0001", Type: "HmIP-STH"},
		{Address: "FLEET0001:0", Type: "MAINTENANCE", Parent: "FLEET0001"},
		{Address: "FLEET0001:1", Type: "HEATING_CLIMATECONTROL_TRANSCEIVER", Parent: "FLEET0001"},
		{Address: "FLEET0002", Type: "HmIP-PS"},
		{Address: "FLEET0002:0", Type: "MAINTENANCE", Parent: "FLEET0002"},
		{Address: "FLEET0002:1", Type: "SWITCH_VIRTUAL_RECEIVER", Parent: "FLEET0002"},
	}
}

// TestIngestRecordsDescriptionsForTheWarmBootCache pins the pull as a writer
// of the description registry.
//
// It was the one path that built a whole model while writing nothing there,
// so on an installation whose descriptions had never come in over a
// newDevices callback the table stayed empty for good: every warm boot logged
// `wire.descriptors.hydrated devices=0` next to a healthy `paramsets=1896`,
// and everything keyed on a description read an empty cache.
func TestIngestRecordsDescriptionsForTheWarmBootCache(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-desccache"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	p := NewDevicePipeline(c)
	const ifaceID = "ccu-desccache-HmIP-RF"

	if err := p.Ingest(context.Background(), ifaceID, hmenum.InterfaceHmIPRF, fleetDescriptions()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	wireID := hmtypes.ParseWireInterfaceID(ifaceID)
	for _, d := range fleetDescriptions() {
		got, ok := c.DescRegistry.Get(wireID, d.Address)
		if !ok {
			t.Errorf("description registry has no entry for %s — the pull wrote the model but not the cache", d.Address)
			continue
		}
		if got.Type != d.Type {
			t.Errorf("%s: type = %q, want %q", d.Address, got.Type, d.Type)
		}
	}

	// Keyed by the canonical wire id, not the bare interface: every reader
	// looks it up that way, and a lookup against the wrong key finds
	// nothing while the registry looks populated.
	if _, ok := c.DescRegistry.Get(hmtypes.ParseWireInterfaceID("HmIP-RF"), "FLEET0001"); ok {
		t.Error("description stored under the bare interface id; readers key on the canonical wire id")
	}
}

// TestIngestedFleetIsNotParkedInTheInbox is the guard for what an operator
// actually saw: with `delay_new_device_creation` enabled, every device in the
// installation was presented as waiting for approval while simultaneously
// being fully materialised and visible.
//
// The CCU re-announces its complete inventory after every init — the daemon
// answers listDevices with an empty array, so it must. The deferred-creation
// filter skips an announcement whose device is known AND whose description is
// cached; with the cache empty the second half never held, so the whole fleet
// was parked.
func TestIngestedFleetIsNotParkedInTheInbox(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-inbox-flood"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c.SetDeviceIngestFn(func(context.Context, string, []hmproto.DeviceDescription) error { return nil })

	// The boot pull: materialise the fleet exactly as IngestFromBackend does.
	ifaceID := CanonicalInterfaceID(c.InstanceName(), c.Name(), "HmIP-RF")
	p := NewDevicePipeline(c)
	if err := p.Ingest(context.Background(), ifaceID, hmenum.InterfaceHmIPRF, fleetDescriptions()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// The CCU's post-init inventory announcement, with the operator's
	// deferred-creation toggle on.
	h := NewCallbackHandlers(c, nil)
	defer h.Stop()
	h.SetDelayNewDeviceCreation(true)
	if err := h.NewDevices(context.Background(), "HmIP-RF", fleetArrayValue()); err != nil {
		t.Fatalf("NewDevices: %v", err)
	}

	if listed := c.HubModel.Inbox.List(); len(listed) != 0 {
		t.Errorf("inbox lists %d already-materialised device(s) as waiting for approval: %+v",
			len(listed), listed)
	}
}

// fleetArrayValue is fleetDescriptions in the XML-RPC shape a newDevices
// callback carries.
func fleetArrayValue() xmlrpc.ArrayValue {
	descs := fleetDescriptions()
	out := make(xmlrpc.ArrayValue, 0, len(descs))
	for i := range descs {
		d := &descs[i]
		members := []xmlrpc.Member{
			{Name: "ADDRESS", Value: xmlrpc.StringValue(d.Address)},
			{Name: "TYPE", Value: xmlrpc.StringValue(d.Type)},
		}
		if d.Parent != "" {
			members = append(members, xmlrpc.Member{Name: "PARENT", Value: xmlrpc.StringValue(d.Parent)})
		}
		out = append(out, xmlrpc.StructValue{Members: members})
	}
	return out
}
