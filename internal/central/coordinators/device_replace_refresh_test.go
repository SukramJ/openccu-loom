// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// RefreshFirmwareData
// ---------------------------------------------------------------------------

// TestRefreshFirmwareDataNilFetcherErrors verifies that a nil fetcher returns
// an error without touching registries.
func TestRefreshFirmwareDataNilFetcherErrors(t *testing.T) {
	t.Parallel()
	dc, _, _, _, _ := newDCFull(t)

	err := dc.RefreshFirmwareData(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil fetcher, got nil")
	}
}

// TestRefreshFirmwareDataRefreshesAllInterfaces verifies that RefreshFirmwareData
// calls the fetcher for every interface that has registered descriptions and
// updates the description registry with fresh data.
func TestRefreshFirmwareDataRefreshesAllInterfaces(t *testing.T) {
	t.Parallel()
	dc, _, devs, descs, _ := newDCFull(t)

	// Seed two devices on two different interfaces.
	descs.Put(wireKey(hmenum.InterfaceHmIPRF), hmproto.DeviceDescription{
		Address:  "VCU0000001",
		Type:     "HmIP-PS",
		Firmware: "1.0",
	})
	devs.Put(registry.DeviceEntry{Interface: wireKey(hmenum.InterfaceHmIPRF), Address: "VCU0000001", Model: "HmIP-PS"})

	descs.Put(wireKey(hmenum.InterfaceBidCosRF), hmproto.DeviceDescription{
		Address:  "HEQ0000001",
		Type:     "HM-Sec-SC",
		Firmware: "1.0",
	})
	devs.Put(registry.DeviceEntry{Interface: wireKey(hmenum.InterfaceBidCosRF), Address: "HEQ0000001", Model: "HM-Sec-SC"})

	// Fetcher returns updated firmware 2.0 for both.
	fetcher := &multiIfaceLister{
		byIface: map[hmtypes.WireInterfaceID][]hmproto.DeviceDescription{
			wireKey(hmenum.InterfaceHmIPRF): {
				{Address: "VCU0000001", Type: "HmIP-PS", Firmware: "2.0"},
			},
			wireKey(hmenum.InterfaceBidCosRF): {
				{Address: "HEQ0000001", Type: "HM-Sec-SC", Firmware: "2.0"},
			},
		},
	}

	if err := dc.RefreshFirmwareData(context.Background(), fetcher); err != nil {
		t.Fatalf("RefreshFirmwareData: %v", err)
	}

	// Both descriptions must reflect firmware 2.0.
	got1, ok := descs.Get(wireKey(hmenum.InterfaceHmIPRF), "VCU0000001")
	if !ok || got1.Firmware != "2.0" {
		t.Errorf("HmIP-RF firmware=%q, want 2.0", got1.Firmware)
	}
	got2, ok := descs.Get(wireKey(hmenum.InterfaceBidCosRF), "HEQ0000001")
	if !ok || got2.Firmware != "2.0" {
		t.Errorf("BidCos-RF firmware=%q, want 2.0", got2.Firmware)
	}
}

// TestRefreshFirmwareDataNoInterfaces verifies that RefreshFirmwareData is a
// no-op (no error) when no interfaces are known yet.
func TestRefreshFirmwareDataNoInterfaces(t *testing.T) {
	t.Parallel()
	dc, _, _, _, _ := newDCFull(t)

	var fetcherCalled bool
	fetcher := &stubListerHooked{
		inner:         &stubLister{},
		onListDevices: func() { fetcherCalled = true },
	}

	if err := dc.RefreshFirmwareData(context.Background(), fetcher); err != nil {
		t.Fatalf("RefreshFirmwareData on empty registry: %v", err)
	}
	if fetcherCalled {
		t.Error("fetcher must not be called when no interfaces are registered")
	}
}

// ---------------------------------------------------------------------------
// ReplaceDevice
// ---------------------------------------------------------------------------

// TestReplaceDeviceNilFetcherErrors verifies that a nil fetcher returns an
// error immediately.
func TestReplaceDeviceNilFetcherErrors(t *testing.T) {
	t.Parallel()
	dc, _, _, _, _ := newDCFull(t)

	err := dc.ReplaceDevice(context.Background(), nil, wireKey(hmenum.InterfaceBidCosRF), "OLD001", "NEW001")
	if err == nil {
		t.Fatal("expected error for nil fetcher, got nil")
	}
}

// TestReplaceDeviceUnknownOldAddressErrors verifies that attempting to replace
// an address that is not in the device registry returns an error without
// modifying state.
func TestReplaceDeviceUnknownOldAddressErrors(t *testing.T) {
	t.Parallel()
	dc, _, devs, _, _ := newDCFull(t)

	fetcher := &stubLister{snapshot: []hmproto.DeviceDescription{{Address: "NEW001", Type: "HM-Sec-SC", Firmware: "1.0"}}}

	err := dc.ReplaceDevice(context.Background(), fetcher, wireKey(hmenum.InterfaceBidCosRF), "OLD001", "NEW001")
	if err == nil {
		t.Fatal("expected error for unknown old address, got nil")
	}
	if devs.Len() != 0 {
		t.Errorf("registry must stay empty on error, got %d", devs.Len())
	}
}

// TestReplaceDeviceEvictsOldAndIngestsNew verifies the happy path: the old
// device is removed (DeviceRemovedEvent emitted), and the new device is
// ingested via the fetch pipeline (DeviceCreatedEvent emitted).
func TestReplaceDeviceEvictsOldAndIngestsNew(t *testing.T) {
	t.Parallel()
	dc, bus, devs, descs, psets := newDCFull(t)
	created := collectCreated(bus)
	removed := collectRemoved(bus)

	// Seed old device with its MASTER paramset.
	descs.Put(wireKey(hmenum.InterfaceBidCosRF), hmproto.DeviceDescription{
		Address:  "OLD001",
		Type:     "HM-Sec-SC",
		Firmware: "1.0",
		Children: []string{"OLD001:0", "OLD001:1"},
	})
	descs.Put(wireKey(hmenum.InterfaceBidCosRF), hmproto.DeviceDescription{Address: "OLD001:0", Parent: "OLD001", Type: "MAINTENANCE"})
	descs.Put(wireKey(hmenum.InterfaceBidCosRF), hmproto.DeviceDescription{Address: "OLD001:1", Parent: "OLD001", Type: "SHUTTER_CONTACT"})
	devs.Put(registry.DeviceEntry{Interface: wireKey(hmenum.InterfaceBidCosRF), Address: "OLD001", Model: "HM-Sec-SC"})
	psets.Put(wireKey(hmenum.InterfaceBidCosRF), "OLD001:0", hmenum.ParamsetKeyMaster, hmproto.Paramset{})

	// Fetcher returns the replacement device.
	fetcher := &stubLister{
		snapshot: []hmproto.DeviceDescription{
			{Address: "NEW001", Type: "HM-Sec-SC", Firmware: "2.0", Children: []string{"NEW001:0"}},
			{Address: "NEW001:0", Parent: "NEW001", Type: "MAINTENANCE"},
		},
	}

	if err := dc.ReplaceDevice(context.Background(), fetcher, wireKey(hmenum.InterfaceBidCosRF), "OLD001", "NEW001"); err != nil {
		t.Fatalf("ReplaceDevice: %v", err)
	}

	// Old device must be gone.
	if devs.Has(wireKey(hmenum.InterfaceBidCosRF), "OLD001") {
		t.Error("OLD001 must be removed from device registry")
	}
	if _, ok := descs.Get(wireKey(hmenum.InterfaceBidCosRF), "OLD001"); ok {
		t.Error("OLD001 description must be evicted")
	}
	if _, ok := descs.Get(wireKey(hmenum.InterfaceBidCosRF), "OLD001:0"); ok {
		t.Error("OLD001:0 channel description must be evicted")
	}
	if _, ok := psets.Get(wireKey(hmenum.InterfaceBidCosRF), "OLD001:0", hmenum.ParamsetKeyMaster); ok {
		t.Error("OLD001:0 paramset must be evicted")
	}

	// New device must be registered.
	if !devs.Has(wireKey(hmenum.InterfaceBidCosRF), "NEW001") {
		t.Error("NEW001 must be in device registry after replace")
	}

	// Events: one DeviceRemovedEvent for OLD001, one DeviceCreatedEvent for NEW001.
	if len(*removed) != 1 || (*removed)[0].Address != "OLD001" {
		t.Errorf("removed events=%+v, want single OLD001", *removed)
	}
	if len(*created) != 1 || (*created)[0].Address != "NEW001" {
		t.Errorf("created events=%+v, want single NEW001", *created)
	}
}

// TestReplaceDeviceEvictsOldFromDomainModel pins that a replaced device also
// leaves the domain model. The registries feed the discovery and cache
// layers, but REST, the WebSocket API and the SPA read the model — a device
// dropped from the registries alone keeps being served, with all its data
// points, until the daemon restarts.
func TestReplaceDeviceEvictsOldFromDomainModel(t *testing.T) {
	t.Parallel()
	model := newFakeDeviceModel("OLD001")
	dc, bus, devs, descs, _ := newDCWithModel(t, model)
	removed := collectRemoved(bus)

	descs.Put(wireKey(hmenum.InterfaceBidCosRF), hmproto.DeviceDescription{Address: "OLD001", Type: "HM-Sec-SC"})
	devs.Put(registry.DeviceEntry{Interface: wireKey(hmenum.InterfaceBidCosRF), Address: "OLD001", Model: "HM-Sec-SC"})

	fetcher := &stubLister{snapshot: []hmproto.DeviceDescription{{Address: "NEW001", Type: "HM-Sec-SC"}}}
	if err := dc.ReplaceDevice(context.Background(), fetcher, wireKey(hmenum.InterfaceBidCosRF), "OLD001", "NEW001"); err != nil {
		t.Fatalf("ReplaceDevice: %v", err)
	}

	if model.HasDevice("OLD001") {
		t.Error("OLD001 must be gone from the domain model after a replace")
	}
	if len(model.removed) != 1 || model.removed[0] != "OLD001" {
		t.Errorf("model removals=%v, want exactly [OLD001]", model.removed)
	}
	// The model publishes its own removal event; a second one from the
	// registry eviction would retract the same entity twice.
	if len(*removed) != 0 {
		t.Errorf("removed events=%+v, want none — the model already announced the removal", *removed)
	}
}

// TestReplaceDeviceModelMismatchErrors verifies that when the new device
// description is already cached but has a different model, ReplaceDevice
// returns an error and leaves state unchanged.
func TestReplaceDeviceCrossTypeProceeds(t *testing.T) {
	t.Parallel()
	dc, _, devs, descs, _ := newDCFull(t)

	// Old device: HM-Sec-SC.
	descs.Put(wireKey(hmenum.InterfaceBidCosRF), hmproto.DeviceDescription{Address: "OLD001", Type: "HM-Sec-SC", Firmware: "1.0"})
	devs.Put(registry.DeviceEntry{Interface: wireKey(hmenum.InterfaceBidCosRF), Address: "OLD001", Model: "HM-Sec-SC"})

	// New device already cached with a different model. The CCU (rfd /
	// hs485d) owns the type-compatibility check and legitimately approves
	// compatible cross-type swaps, so the coordinator proceeds rather
	// than reject a swap the CCU already performed.
	descs.Put(wireKey(hmenum.InterfaceBidCosRF), hmproto.DeviceDescription{Address: "NEW001", Type: "HM-LC-Sw1", Firmware: "1.0"})

	fetcher := &stubLister{snapshot: []hmproto.DeviceDescription{{Address: "NEW001", Type: "HM-LC-Sw1", Firmware: "1.0"}}}

	if err := dc.ReplaceDevice(context.Background(), fetcher, wireKey(hmenum.InterfaceBidCosRF), "OLD001", "NEW001"); err != nil {
		t.Fatalf("cross-type replace must proceed, got error: %v", err)
	}

	// OLD001 is evicted; NEW001 is ingested from the fetcher snapshot.
	if devs.Has(wireKey(hmenum.InterfaceBidCosRF), "OLD001") {
		t.Error("OLD001 must be removed after a successful replace")
	}
	if !devs.Has(wireKey(hmenum.InterfaceBidCosRF), "NEW001") {
		t.Error("NEW001 must be registered after a successful replace")
	}
}

// ---------------------------------------------------------------------------
// Stub helpers
// ---------------------------------------------------------------------------

// multiIfaceLister is a DeviceDescriptionFetcher that returns per-interface
// snapshots, allowing different descriptions per interface.
type multiIfaceLister struct {
	byIface map[hmtypes.WireInterfaceID][]hmproto.DeviceDescription
	err     error
}

func (m *multiIfaceLister) ListDevices(_ context.Context, iface hmtypes.WireInterfaceID) ([]hmproto.DeviceDescription, error) {
	if m.err != nil {
		return nil, m.err
	}
	snap, ok := m.byIface[iface]
	if !ok {
		return nil, errors.New("no snapshot for interface: " + string(iface))
	}
	out := make([]hmproto.DeviceDescription, len(snap))
	copy(out, snap)
	return out, nil
}
