// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// DeviceCoordinator edge-case tests covering manual add-device guards,
// firmware-state-driven refresh, and the factory-reset scenario where a
// device's parent description is known but its channel descriptions are
// missing.
//   - TestRefreshFirmwareDataByStateNilStateReaderIsNoop
//     (test_refresh_firmware_data_specific_device — Python calls
//     refresh_firmware_data(device_address=...); Go exposes
//     RefreshFirmwareDataByState with a stateReader. The nil-stateReader
//     early-return covers the "no capability" skip-path.)
//   - TestRefreshFirmwareDataByStateFetcherUpdatesDescriptions
//     (test_refresh_firmware_data_updates_cache_for_existing_devices)
//   - TestHandleNewDevicesMissingParentIsStored
//     (test_device_entry_without_parent — Python _identify_missing checks
//     entries without PARENT; Go HandleNewDevices stores all entries
//     including top-level devices without Parent.)
//   - TestStoreDelayedDescriptionsParentKnownChannelsMissing
//     (test_parent_known_but_channels_missing_factory_reset_scenario —
//     StoreDelayedDeviceDescriptions keys channel entries by Parent so
//     they are retrievable even when the parent is already in the
//     description registry.)
//
// Skipped / shape-mismatch:
//   - test_create_central_links — Python DeviceCoordinator has
//     create_central_links() iterating Device.create_central_links().
//     Go's DeviceCoordinator tracks registry entries (Address+Model),
//     not rich Device objects; link logic lives in the adapter layer
//     (internal/central/adapter/central_links.go). No equivalent method
//     on DeviceCoordinator to test.
//   - test_remove_central_links — same as above.
//   - test_device_without_paramsets_field — Python
//     _identify_devices_missing_paramsets() does not exist in Go's
//     DeviceCoordinator (paramset-completeness check is separate).
//   - test_parent_known_but_channels_missing (identify_missing vs
//     identify_new) — Python distinguishes these two private methods;
//     Go has no direct equivalent private method.
//   - test_refresh_firmware_data_specific_device (exact Python shape) —
//     Go RefreshFirmwareDataByState takes a stateReader + state set rather
//     than a device address; covered by the nil-stateReader noop test.

package coordinators

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// AddNewDevicesManually — no delayed descriptions
// ---------------------------------------------------------------------------

// TestAddNewDeviceManuallyNoDelayedDescriptions verifies that when there are
// no delayed descriptions for the requested interface, AddNewDevicesManually
// returns without creating any device or calling the acceptor.
//
// Mirrors: test_add_new_device_manually_no_client.
// Python skips when client doesn't exist; Go skips when delayedDescs is
// empty for the interface. Both guard the actual device-creation path.
func TestAddNewDeviceManuallyNoDelayedDescriptions(t *testing.T) {
	t.Parallel()
	dc, _, devs, _, _ := newDCFull(t)

	var acceptorCalled bool
	acceptFn := func(_ context.Context, _ hmtypes.WireInterfaceID, _ string) error {
		acceptorCalled = true
		return nil
	}

	// No StoreDelayedDeviceDescriptions call → delayedDescs is empty.
	err := dc.AddNewDevicesManually(
		context.Background(),
		wireKey(hmenum.InterfaceBidCosRF),
		map[string]string{"VCU0000001": ""},
		acceptFn,
	)
	if err != nil {
		t.Fatalf("AddNewDevicesManually: unexpected error: %v", err)
	}
	if devs.Len() != 0 {
		t.Errorf("no device must be created when delayed queue is empty, devs=%d", devs.Len())
	}
	if acceptorCalled {
		t.Error("acceptor must not be called when no delayed descriptions exist")
	}
}

// ---------------------------------------------------------------------------
// AddNewDevicesManually — partial batch continues
// ---------------------------------------------------------------------------

// TestAddNewDeviceManuallyPartialBatchContinues verifies that a missing
// delayed description for one address does not abort the entire batch —
// remaining addresses with descriptions are still processed.
//
// Mirrors: test_add_new_device_manually_partial_batch_continues.
func TestAddNewDeviceManuallyPartialBatchContinues(t *testing.T) {
	t.Parallel()
	dc, bus, devs, _, _ := newDCFull(t)
	created := collectCreated(bus)

	// Only VCU0000001 has delayed descriptions; VCU9999999 has none.
	dc.StoreDelayedDeviceDescriptions(wireKey(hmenum.InterfaceBidCosRF), []hmproto.DeviceDescription{
		{Address: "VCU0000001", Type: "HM-Test"},
		{Address: "VCU0000001:0", Parent: "VCU0000001", Type: "MAINTENANCE"},
	})

	err := dc.AddNewDevicesManually(
		context.Background(),
		wireKey(hmenum.InterfaceBidCosRF),
		map[string]string{
			"VCU9999999": "", // missing — should be skipped, not fatal
			"VCU0000001": "",
		},
		nil,
	)
	if err != nil {
		t.Fatalf("AddNewDevicesManually: %v", err)
	}

	// VCU0000001 must still be registered.
	if devs.Len() != 1 {
		t.Fatalf("expected 1 device, got %d", devs.Len())
	}
	if _, ok := devs.Get(wireKey(hmenum.InterfaceBidCosRF), "VCU0000001"); !ok {
		t.Error("VCU0000001 must be in DeviceRegistry after partial batch")
	}
	if len(*created) != 1 || (*created)[0].Address != "VCU0000001" {
		t.Errorf("created events=%+v, want single VCU0000001", *created)
	}
	if (*created)[0].Source != hmenum.SourceOfDeviceCreationManual {
		t.Errorf("source=%v, want MANUAL", (*created)[0].Source)
	}
}

// ---------------------------------------------------------------------------
// RefreshFirmwareDataByState — nil stateReader is a noop
// ---------------------------------------------------------------------------

// TestRefreshFirmwareDataByStateNilStateReaderIsNoop verifies that when
// stateReader is nil RefreshFirmwareDataByState returns nil without calling
// the fetcher. This covers the "no firmware capability" skip-path analogous
// to Python's refresh_firmware_data skipping when the device is not
// updatable or the backend capability is absent.
//
// Mirrors: test_refresh_firmware_data_specific_device (skip-path aspect).
func TestRefreshFirmwareDataByStateNilStateReaderIsNoop(t *testing.T) {
	t.Parallel()
	dc, _, devs, descs, _ := newDCFull(t)

	// Seed one device so the registry is non-empty.
	descs.Put(wireKey(hmenum.InterfaceHmIPRF), hmproto.DeviceDescription{
		Address:  "VCU0000001",
		Type:     "HmIP-X",
		Firmware: "1.0",
	})
	devs.Put(registry.DeviceEntry{
		Interface: wireKey(hmenum.InterfaceHmIPRF),
		Address:   "VCU0000001",
		Model:     "HmIP-X",
	})

	var fetcherCalled bool
	fetcher := &stubListerHooked{
		inner:         &stubLister{snapshot: []hmproto.DeviceDescription{{Address: "VCU0000001", Type: "HmIP-X", Firmware: "2.0"}}},
		onListDevices: func() { fetcherCalled = true },
	}

	// nil stateReader → early return, fetcher must not be called.
	err := dc.RefreshFirmwareDataByState(
		context.Background(),
		fetcher,
		nil, // stateReader is nil
		wireKey(hmenum.InterfaceHmIPRF),
		[]hmenum.DeviceFirmwareState{hmenum.DeviceFirmwareStateNewFirmwareAvailable},
	)
	if err != nil {
		t.Fatalf("RefreshFirmwareDataByState: unexpected error: %v", err)
	}
	if fetcherCalled {
		t.Error("fetcher must not be called when stateReader is nil")
	}
}

// ---------------------------------------------------------------------------
// RefreshFirmwareDataByState — matching state triggers fetcher
// ---------------------------------------------------------------------------

// TestRefreshFirmwareDataByStateFetcherUpdatesDescriptions verifies that
// when a device's firmware state matches one of the requested states, the
// fetcher is called and new descriptions are stored.
//
// Mirrors: test_refresh_firmware_data_updates_cache_for_existing_devices.
func TestRefreshFirmwareDataByStateFetcherUpdatesDescriptions(t *testing.T) {
	t.Parallel()
	dc, _, devs, descs, _ := newDCFull(t)

	// Seed existing device with firmware 1.0.
	descs.Put(wireKey(hmenum.InterfaceHmIPRF), hmproto.DeviceDescription{
		Address:  "VCU0000001",
		Type:     "HmIP-X",
		Firmware: "1.0",
	})
	devs.Put(registry.DeviceEntry{
		Interface: wireKey(hmenum.InterfaceHmIPRF),
		Address:   "VCU0000001",
		Model:     "HmIP-X",
	})

	// stateReader reports device is in NEW_FIRMWARE_AVAILABLE state.
	stateReader := &fakeFirmwareStateReader{
		states: map[string]hmenum.DeviceFirmwareState{
			"VCU0000001": hmenum.DeviceFirmwareStateNewFirmwareAvailable,
		},
	}

	// fetcher returns updated firmware 2.0.
	fetcher := &stubLister{
		snapshot: []hmproto.DeviceDescription{
			{Address: "VCU0000001", Type: "HmIP-X", Firmware: "2.0"},
		},
	}

	err := dc.RefreshFirmwareDataByState(
		context.Background(),
		fetcher,
		stateReader,
		wireKey(hmenum.InterfaceHmIPRF),
		[]hmenum.DeviceFirmwareState{hmenum.DeviceFirmwareStateNewFirmwareAvailable},
	)
	if err != nil {
		t.Fatalf("RefreshFirmwareDataByState: %v", err)
	}

	// Description registry must now reflect firmware 2.0.
	got, ok := descs.Get(wireKey(hmenum.InterfaceHmIPRF), "VCU0000001")
	if !ok {
		t.Fatal("VCU0000001 must still be in description registry after refresh")
	}
	if got.Firmware != "2.0" {
		t.Errorf("firmware=%q, want 2.0", got.Firmware)
	}
}

// ---------------------------------------------------------------------------
// HandleNewDevices — device entry without PARENT is stored
// ---------------------------------------------------------------------------

// TestHandleNewDevicesMissingParentIsStored verifies that a top-level device
// description without a Parent field is correctly stored in both the
// description registry and the device registry.
//
// Mirrors: test_device_entry_without_parent — Python
// _identify_missing_device_descriptions also checks entries without PARENT;
// Go's HandleNewDevices stores them unconditionally as top-level devices.
func TestHandleNewDevicesMissingParentIsStored(t *testing.T) {
	t.Parallel()
	dc, bus, devs, descs, _ := newDCFull(t)
	created := collectCreated(bus)

	// Device description has no PARENT (top-level device entry).
	dc.HandleNewDevices(context.Background(), wireKey(hmenum.InterfaceBidCosRF), []hmproto.DeviceDescription{
		{Address: "HEQ0128279", Type: "HM-Sec-SC"}, // no Parent
	})

	if devs.Len() != 1 {
		t.Fatalf("expected 1 device in registry, got %d", devs.Len())
	}
	if _, ok := devs.Get(wireKey(hmenum.InterfaceBidCosRF), "HEQ0128279"); !ok {
		t.Error("HEQ0128279 must be in DeviceRegistry")
	}
	if got, ok := descs.Get(wireKey(hmenum.InterfaceBidCosRF), "HEQ0128279"); !ok {
		t.Error("HEQ0128279 must be in DescriptionRegistry")
	} else if got.Parent != "" {
		t.Errorf("Parent=%q, want empty for top-level device", got.Parent)
	}
	if len(*created) != 1 || (*created)[0].Address != "HEQ0128279" {
		t.Errorf("DeviceCreatedEvent: %+v, want single HEQ0128279", *created)
	}
}

// ---------------------------------------------------------------------------
// StoreDelayedDeviceDescriptions — parent known, channels stored by Parent
// ---------------------------------------------------------------------------

// TestStoreDelayedDescriptionsParentKnownChannelsMissing verifies the
// factory-reset scenario: when a parent device is already known but channel
// descriptions arrive via newDevices, StoreDelayedDeviceDescriptions keys
// them by Parent so AddNewDevicesManually can retrieve them later under the
// parent address.
//
// Mirrors: test_parent_known_but_channels_missing_factory_reset_scenario.
func TestStoreDelayedDescriptionsParentKnownChannelsMissing(t *testing.T) {
	t.Parallel()
	dc, _, devs, descs, _ := newDCFull(t)

	// Parent device already known (exists in registries).
	descs.Put(wireKey(hmenum.InterfaceBidCosRF), hmproto.DeviceDescription{
		Address: "HEQ0128279",
		Type:    "HM-Sec-SC",
	})
	devs.Put(registry.DeviceEntry{
		Interface: wireKey(hmenum.InterfaceBidCosRF),
		Address:   "HEQ0128279",
		Model:     "HM-Sec-SC",
	})

	// CCU sends newDevices with channel descriptions (factory reset).
	dc.StoreDelayedDeviceDescriptions(wireKey(hmenum.InterfaceBidCosRF), []hmproto.DeviceDescription{
		{Address: "HEQ0128279:0", Parent: "HEQ0128279", Type: "MAINTENANCE"},
		{Address: "HEQ0128279:1", Parent: "HEQ0128279", Type: "SHUTTER_CONTACT"},
	})

	// Both channel descriptions must be retrievable via AddNewDevicesManually
	// using the parent address as key.
	err := dc.AddNewDevicesManually(
		context.Background(),
		wireKey(hmenum.InterfaceBidCosRF),
		map[string]string{"HEQ0128279": ""},
		nil,
	)
	if err != nil {
		t.Fatalf("AddNewDevicesManually: %v", err)
	}

	// The channel descriptions must now be in the description registry.
	if _, ok := descs.Get(wireKey(hmenum.InterfaceBidCosRF), "HEQ0128279:0"); !ok {
		t.Error("channel HEQ0128279:0 must be stored after AddNewDevicesManually")
	}
	if _, ok := descs.Get(wireKey(hmenum.InterfaceBidCosRF), "HEQ0128279:1"); !ok {
		t.Error("channel HEQ0128279:1 must be stored after AddNewDevicesManually")
	}
}

// TestStoreDelayedDescriptionsIsIdempotentPerAddress pins that a repeated
// announcement of the same device replaces its pending descriptions instead
// of stacking a second copy on top of them.
//
// The daemon answers listDevices with an empty array, so the CCU re-announces
// its COMPLETE inventory through newDevices after every reconnect. With an
// append-only inbox each reconnect added another full copy of the fleet that
// nothing ever removes — on a large installation that is thousands of
// descriptions per reconnect, retained for the lifetime of the process.
func TestStoreDelayedDescriptionsIsIdempotentPerAddress(t *testing.T) {
	t.Parallel()
	dc, _, _, _, _ := newDCFull(t)

	batch := []hmproto.DeviceDescription{
		{Address: "HEQ0128279", Type: "HM-Sec-SC"},
		{Address: "HEQ0128279:0", Parent: "HEQ0128279", Type: "MAINTENANCE"},
		{Address: "HEQ0128279:1", Parent: "HEQ0128279", Type: "SHUTTER_CONTACT"},
	}

	dc.StoreDelayedDeviceDescriptions(wireKey(hmenum.InterfaceBidCosRF), batch)
	first := len(dc.delayedDescs[string(wireKey(hmenum.InterfaceBidCosRF))]["HEQ0128279"])
	if first != len(batch) {
		t.Fatalf("first announcement stored %d descriptions, want %d", first, len(batch))
	}

	// The CCU re-announces the same inventory after a reconnect.
	dc.StoreDelayedDeviceDescriptions(wireKey(hmenum.InterfaceBidCosRF), batch)
	if got := len(dc.delayedDescs[string(wireKey(hmenum.InterfaceBidCosRF))]["HEQ0128279"]); got != first {
		t.Fatalf("re-announcement grew the delayed inbox to %d descriptions, want %d — "+
			"the inbox must be keyed by address, not appended to", got, first)
	}

	// A changed description for an already-pending address must win.
	dc.StoreDelayedDeviceDescriptions(wireKey(hmenum.InterfaceBidCosRF), []hmproto.DeviceDescription{
		{Address: "HEQ0128279:1", Parent: "HEQ0128279", Type: "SHUTTER_CONTACT", Firmware: "2.0"},
	})
	stored := dc.delayedDescs[string(wireKey(hmenum.InterfaceBidCosRF))]["HEQ0128279"]
	if len(stored) != first {
		t.Fatalf("update of a pending address grew the inbox to %d, want %d", len(stored), first)
	}
	var found bool
	for _, d := range stored {
		if d.Address == "HEQ0128279:1" {
			found = true
			if d.Firmware != "2.0" {
				t.Errorf("re-announced description was not replaced: firmware=%q, want %q", d.Firmware, "2.0")
			}
		}
	}
	if !found {
		t.Error("HEQ0128279:1 must stay in the delayed inbox after the update")
	}
}

// ---------------------------------------------------------------------------
// Stub helpers (local to this file)
// ---------------------------------------------------------------------------

// fakeFirmwareStateReader implements FirmwareStateReader for these
// edge-case tests. Named distinctly to avoid collision with
// stubFirmwareStateReader in device_coordinator_link_firmware_test.go.
type fakeFirmwareStateReader struct {
	states map[string]hmenum.DeviceFirmwareState
}

func (r *fakeFirmwareStateReader) DeviceFirmwareStates(_ hmtypes.WireInterfaceID) map[string]hmenum.DeviceFirmwareState {
	return r.states
}

// stubListerHooked wraps stubLister (defined in device_pull_test.go) with a
// call-detection hook. This avoids adding a field to the shared stubLister
// type. Used only within this file.
type stubListerHooked struct {
	inner         *stubLister
	onListDevices func()
}

func (s *stubListerHooked) ListDevices(ctx context.Context, iface hmtypes.WireInterfaceID) ([]hmproto.DeviceDescription, error) {
	if s.onListDevices != nil {
		s.onListDevices()
	}
	return s.inner.ListDevices(ctx, iface)
}
