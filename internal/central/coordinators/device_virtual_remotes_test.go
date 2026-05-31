// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// device_virtual_remotes_test.go covers DeviceCoordinator public-API scenarios
// not exercised by device_deep_test.go, device_pull_test.go, or
// coordinators_test.go.
//
// Covered:
//   - GetVirtualRemotes: empty result, single-device match, multiple devices
//   - IdentifyChannel: not-found, success, empty-text guard
//   - DeleteDevice: removes device + channels, not-found is a no-op
//   - CheckForNewDeviceAddresses: all-present, mixed-known/unknown channels
package coordinators

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// newDCWithDescs populates a DeviceCoordinator with the given descriptions.
func newDCWithDescs(t *testing.T, iface hmenum.Interface, descs ...hmproto.DeviceDescription) *DeviceCoordinator {
	t.Helper()
	bus := events.NewBus()
	devs := registry.NewDeviceRegistry()
	descReg := registry.NewDeviceDescriptionRegistry()
	psets := registry.NewParamsetRegistry()
	dc := NewDeviceCoordinator("c1", bus, devs, descReg, psets, nil)
	for i := range descs {
		descReg.Put(iface, descs[i])
	}
	return dc
}

// ---------------------------------------------------------------------------
// GetVirtualRemotes
// ---------------------------------------------------------------------------

func TestGetVirtualRemotesNonVrcTypeIsExcluded(t *testing.T) {
	t.Parallel()
	dc := newDCWithDescs(
		t, hmenum.InterfaceHmIPRF,
		hmproto.DeviceDescription{Address: "AA", Type: "HmIP-SW"},
		hmproto.DeviceDescription{Address: "AA:0", Parent: "AA", Type: "MAINTENANCE"},
	)
	remotes := dc.GetVirtualRemotes(hmenum.InterfaceHmIPRF)
	if len(remotes) != 0 {
		t.Fatalf("GetVirtualRemotes must return empty for non-VRC devices, got %d", len(remotes))
	}
}

func TestGetVirtualRemotesFound(t *testing.T) {
	t.Parallel()
	dc := newDCWithDescs(
		t, hmenum.InterfaceHmIPRF,
		hmproto.DeviceDescription{
			Address:  "VRT0001",
			Type:     "HM-RCV-50",
			Children: []string{"VRT0001:1", "VRT0001:2"},
		},
		hmproto.DeviceDescription{Address: "VRT0001:1", Parent: "VRT0001", Type: "VRC"},
		hmproto.DeviceDescription{Address: "VRT0001:2", Parent: "VRT0001", Type: "VRC"},
	)
	remotes := dc.GetVirtualRemotes(hmenum.InterfaceHmIPRF)
	if len(remotes) != 1 {
		t.Fatalf("GetVirtualRemotes must return 1 entry for VRC device, got %d", len(remotes))
	}
	if remotes[0].Address != "VRT0001" {
		t.Fatalf("Address=%q, want VRT0001", remotes[0].Address)
	}
	if remotes[0].DeviceType != "HM-RCV-50" {
		t.Fatalf("DeviceType=%q, want HM-RCV-50", remotes[0].DeviceType)
	}
	if len(remotes[0].ChannelAddresses) != 2 {
		t.Fatalf("ChannelAddresses=%v, want 2 entries", remotes[0].ChannelAddresses)
	}
}

func TestGetVirtualRemotesMultipleDevices(t *testing.T) {
	t.Parallel()
	dc := newDCWithDescs(
		t, hmenum.InterfaceHmIPRF,
		hmproto.DeviceDescription{Address: "VRT0001", Type: "HM-RCV-50"},
		hmproto.DeviceDescription{Address: "VRT0002", Type: "HM-RCV-50"},
		hmproto.DeviceDescription{Address: "NOTVRV", Type: "HmIP-SW"},
	)
	remotes := dc.GetVirtualRemotes(hmenum.InterfaceHmIPRF)
	if len(remotes) != 2 {
		t.Fatalf("GetVirtualRemotes must return 2, got %d", len(remotes))
	}
}

// ---------------------------------------------------------------------------
// IdentifyChannel
// ---------------------------------------------------------------------------

func TestIdentifyChannelBySubstringNotFound(t *testing.T) {
	t.Parallel()
	dc := newDCWithDescs(
		t, hmenum.InterfaceHmIPRF,
		hmproto.DeviceDescription{Address: "AA:1", Parent: "AA", Type: "SWITCH"},
	)
	addr, ok := dc.IdentifyChannel(hmenum.InterfaceHmIPRF, "ZZ")
	if ok {
		t.Fatalf("IdentifyChannel with no match must return false, got %q", addr)
	}
}

func TestIdentifyChannelBySubstringMatch(t *testing.T) {
	t.Parallel()
	dc := newDCWithDescs(
		t, hmenum.InterfaceHmIPRF,
		hmproto.DeviceDescription{Address: "HEQ0128279:1", Parent: "HEQ0128279", Type: "SENSOR"},
	)
	addr, ok := dc.IdentifyChannel(hmenum.InterfaceHmIPRF, "HEQ0128279")
	if !ok {
		t.Fatal("IdentifyChannel must match substring")
	}
	if addr != "HEQ0128279:1" {
		t.Fatalf("addr=%q, want HEQ0128279:1", addr)
	}
}

func TestIdentifyChannelBySubstringEmptyText(t *testing.T) {
	t.Parallel()
	dc := newDCWithDescs(
		t, hmenum.InterfaceHmIPRF,
		hmproto.DeviceDescription{Address: "AA:1", Parent: "AA", Type: "SWITCH"},
	)
	addr, ok := dc.IdentifyChannel(hmenum.InterfaceHmIPRF, "")
	if ok || addr != "" {
		t.Fatalf("empty text must return ('', false), got (%q, %v)", addr, ok)
	}
}

// ---------------------------------------------------------------------------
// DeleteDevice
// ---------------------------------------------------------------------------

func TestDeleteDeviceIncludesChannels(t *testing.T) {
	t.Parallel()
	dc, bus, devs, descs, _ := newDCFull(t)
	removed := collectRemoved(bus)
	ctx := context.Background()

	dc.HandleNewDevices(ctx, hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
		device("AA", "HmIP-X", "1.0", "AA:0", "AA:1"),
		channel("AA:0", "AA", "MAINTENANCE"),
		channel("AA:1", "AA", "SENSOR"),
	})
	if devs.Len() != 1 {
		t.Fatalf("seeded devs=%d, want 1", devs.Len())
	}

	dc.DeleteDevice(ctx, hmenum.InterfaceHmIPRF, "AA")

	if devs.Len() != 0 {
		t.Fatalf("devs after delete=%d, want 0", devs.Len())
	}
	// Channels must also be gone from descriptions.
	if _, ok := descs.Get(hmenum.InterfaceHmIPRF, "AA:0"); ok {
		t.Fatal("AA:0 should have been deleted")
	}
	if _, ok := descs.Get(hmenum.InterfaceHmIPRF, "AA:1"); ok {
		t.Fatal("AA:1 should have been deleted")
	}
	// Exactly one DeviceRemovedEvent for the top-level device.
	if len(*removed) != 1 || (*removed)[0].Address != "AA" {
		t.Fatalf("removed events=%v, want single AA", sortedRemovedAddrs(*removed))
	}
}

func TestDeleteDeviceNotFoundIsNoop(t *testing.T) {
	t.Parallel()
	dc, _, devs, _, _ := newDCFull(t)
	ctx := context.Background()

	// Empty registry — delete must not panic or return error.
	dc.DeleteDevice(ctx, hmenum.InterfaceHmIPRF, "GHOST")
	if devs.Len() != 0 {
		t.Fatalf("devs=%d, want 0", devs.Len())
	}
}

// ---------------------------------------------------------------------------
// CheckForNewDeviceAddresses — factory-reset / missing-descriptions scenarios.
// ---------------------------------------------------------------------------

func TestCheckForNewDeviceAddressesAllDescriptionsPresent(t *testing.T) {
	t.Parallel()
	dc := newDCWithDescs(
		t, hmenum.InterfaceHmIPRF,
		hmproto.DeviceDescription{Address: "HEQ0128279", Type: "HM-Sec-SC"},
		hmproto.DeviceDescription{Address: "HEQ0128279:0", Parent: "HEQ0128279", Type: "MAINTENANCE"},
		hmproto.DeviceDescription{Address: "HEQ0128279:1", Parent: "HEQ0128279", Type: "SHUTTER_CONTACT"},
	)
	snapshot := []hmproto.DeviceDescription{
		{Address: "HEQ0128279", Type: "HM-Sec-SC"},
		{Address: "HEQ0128279:0", Parent: "HEQ0128279", Type: "MAINTENANCE"},
		{Address: "HEQ0128279:1", Parent: "HEQ0128279", Type: "SHUTTER_CONTACT"},
	}
	got := dc.CheckForNewDeviceAddresses(hmenum.InterfaceHmIPRF, snapshot)
	if len(got) != 0 {
		t.Fatalf("all present → want empty, got %v", got)
	}
}

func TestCheckForNewDeviceAddressesMixedChannels(t *testing.T) {
	t.Parallel()
	// DEV001 channels present; DEV002 channels missing.
	dc := newDCWithDescs(
		t, hmenum.InterfaceHmIPRF,
		hmproto.DeviceDescription{Address: "DEV001", Type: "HM-A"},
		hmproto.DeviceDescription{Address: "DEV001:0", Parent: "DEV001", Type: "MAINTENANCE"},
		hmproto.DeviceDescription{Address: "DEV001:1", Parent: "DEV001", Type: "SENSOR"},
		hmproto.DeviceDescription{Address: "DEV002", Type: "HM-B"},
	)
	snapshot := []hmproto.DeviceDescription{
		{Address: "DEV001:0", Parent: "DEV001", Type: "MAINTENANCE"},
		{Address: "DEV001:1", Parent: "DEV001", Type: "SENSOR"},
		{Address: "DEV002:0", Parent: "DEV002", Type: "MAINTENANCE"},
		{Address: "DEV002:1", Parent: "DEV002", Type: "SENSOR"},
	}
	got := dc.CheckForNewDeviceAddresses(hmenum.InterfaceHmIPRF, snapshot)
	// Only DEV002 channels are unknown.
	if len(got) != 2 {
		t.Fatalf("want 2 new addresses, got %v", got)
	}
	for _, addr := range got {
		if addr != "DEV002:0" && addr != "DEV002:1" {
			t.Fatalf("unexpected address %q in result", addr)
		}
	}
}
