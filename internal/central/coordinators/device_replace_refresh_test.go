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
	descs.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{
		Address:  "VCU0000001",
		Type:     "HmIP-PS",
		Firmware: "1.0",
	})
	devs.Put(registry.DeviceEntry{Interface: hmenum.InterfaceHmIPRF, Address: "VCU0000001", Model: "HmIP-PS"})

	descs.Put(hmenum.InterfaceBidCosRF, hmproto.DeviceDescription{
		Address:  "HEQ0000001",
		Type:     "HM-Sec-SC",
		Firmware: "1.0",
	})
	devs.Put(registry.DeviceEntry{Interface: hmenum.InterfaceBidCosRF, Address: "HEQ0000001", Model: "HM-Sec-SC"})

	// Fetcher returns updated firmware 2.0 for both.
	fetcher := &multiIfaceLister{
		byIface: map[hmenum.Interface][]hmproto.DeviceDescription{
			hmenum.InterfaceHmIPRF: {
				{Address: "VCU0000001", Type: "HmIP-PS", Firmware: "2.0"},
			},
			hmenum.InterfaceBidCosRF: {
				{Address: "HEQ0000001", Type: "HM-Sec-SC", Firmware: "2.0"},
			},
		},
	}

	if err := dc.RefreshFirmwareData(context.Background(), fetcher); err != nil {
		t.Fatalf("RefreshFirmwareData: %v", err)
	}

	// Both descriptions must reflect firmware 2.0.
	got1, ok := descs.Get(hmenum.InterfaceHmIPRF, "VCU0000001")
	if !ok || got1.Firmware != "2.0" {
		t.Errorf("HmIP-RF firmware=%q, want 2.0", got1.Firmware)
	}
	got2, ok := descs.Get(hmenum.InterfaceBidCosRF, "HEQ0000001")
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

	err := dc.ReplaceDevice(context.Background(), nil, hmenum.InterfaceBidCosRF, "OLD001", "NEW001")
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

	err := dc.ReplaceDevice(context.Background(), fetcher, hmenum.InterfaceBidCosRF, "OLD001", "NEW001")
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
	descs.Put(hmenum.InterfaceBidCosRF, hmproto.DeviceDescription{
		Address:  "OLD001",
		Type:     "HM-Sec-SC",
		Firmware: "1.0",
		Children: []string{"OLD001:0", "OLD001:1"},
	})
	descs.Put(hmenum.InterfaceBidCosRF, hmproto.DeviceDescription{Address: "OLD001:0", Parent: "OLD001", Type: "MAINTENANCE"})
	descs.Put(hmenum.InterfaceBidCosRF, hmproto.DeviceDescription{Address: "OLD001:1", Parent: "OLD001", Type: "SHUTTER_CONTACT"})
	devs.Put(registry.DeviceEntry{Interface: hmenum.InterfaceBidCosRF, Address: "OLD001", Model: "HM-Sec-SC"})
	psets.Put(hmenum.InterfaceBidCosRF, "OLD001:0", hmenum.ParamsetKeyMaster, hmproto.Paramset{})

	// Fetcher returns the replacement device.
	fetcher := &stubLister{
		snapshot: []hmproto.DeviceDescription{
			{Address: "NEW001", Type: "HM-Sec-SC", Firmware: "2.0", Children: []string{"NEW001:0"}},
			{Address: "NEW001:0", Parent: "NEW001", Type: "MAINTENANCE"},
		},
	}

	if err := dc.ReplaceDevice(context.Background(), fetcher, hmenum.InterfaceBidCosRF, "OLD001", "NEW001"); err != nil {
		t.Fatalf("ReplaceDevice: %v", err)
	}

	// Old device must be gone.
	if devs.Has(hmenum.InterfaceBidCosRF, "OLD001") {
		t.Error("OLD001 must be removed from device registry")
	}
	if _, ok := descs.Get(hmenum.InterfaceBidCosRF, "OLD001"); ok {
		t.Error("OLD001 description must be evicted")
	}
	if _, ok := descs.Get(hmenum.InterfaceBidCosRF, "OLD001:0"); ok {
		t.Error("OLD001:0 channel description must be evicted")
	}
	if _, ok := psets.Get(hmenum.InterfaceBidCosRF, "OLD001:0", hmenum.ParamsetKeyMaster); ok {
		t.Error("OLD001:0 paramset must be evicted")
	}

	// New device must be registered.
	if !devs.Has(hmenum.InterfaceBidCosRF, "NEW001") {
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

// TestReplaceDeviceModelMismatchErrors verifies that when the new device
// description is already cached but has a different model, ReplaceDevice
// returns an error and leaves state unchanged.
func TestReplaceDeviceModelMismatchErrors(t *testing.T) {
	t.Parallel()
	dc, _, devs, descs, _ := newDCFull(t)

	// Old device: HM-Sec-SC.
	descs.Put(hmenum.InterfaceBidCosRF, hmproto.DeviceDescription{Address: "OLD001", Type: "HM-Sec-SC", Firmware: "1.0"})
	devs.Put(registry.DeviceEntry{Interface: hmenum.InterfaceBidCosRF, Address: "OLD001", Model: "HM-Sec-SC"})

	// New device already cached with a different model — conflict.
	descs.Put(hmenum.InterfaceBidCosRF, hmproto.DeviceDescription{Address: "NEW001", Type: "HM-LC-Sw1", Firmware: "1.0"})

	fetcher := &stubLister{snapshot: []hmproto.DeviceDescription{{Address: "NEW001", Type: "HM-LC-Sw1", Firmware: "1.0"}}}

	err := dc.ReplaceDevice(context.Background(), fetcher, hmenum.InterfaceBidCosRF, "OLD001", "NEW001")
	if err == nil {
		t.Fatal("expected error for model mismatch, got nil")
	}

	// OLD001 must still be registered — no state change on error.
	if !devs.Has(hmenum.InterfaceBidCosRF, "OLD001") {
		t.Error("OLD001 must not be removed on model-mismatch error")
	}
}

// ---------------------------------------------------------------------------
// Stub helpers
// ---------------------------------------------------------------------------

// multiIfaceLister is a DeviceDescriptionFetcher that returns per-interface
// snapshots, allowing different descriptions per interface.
type multiIfaceLister struct {
	byIface map[hmenum.Interface][]hmproto.DeviceDescription
	err     error
}

func (m *multiIfaceLister) ListDevices(_ context.Context, iface hmenum.Interface) ([]hmproto.DeviceDescription, error) {
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
