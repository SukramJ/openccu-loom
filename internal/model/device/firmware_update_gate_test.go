// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestGatedLatestFirmware locks the rule that a newer firmware is only
// surfaced once it is actually installable: an HmIP-RF image must be delivered
// to the device (READY_FOR_UPDATE family), BidCos reports availability
// directly, and a version the CCU merely knows about (NEW_FIRMWARE_AVAILABLE)
// is NOT yet an update.
func TestGatedLatestFirmware(t *testing.T) {
	t.Parallel()
	const cur, avl = "1.0.0", "1.2.0"

	cases := []struct {
		name  string
		iface hmenum.Interface
		info  FirmwareInfo
		want  string
	}{
		{
			"hmip new-available is not yet installable",
			hmenum.InterfaceHmIPRF,
			FirmwareInfo{Current: cur, Available: avl, UpdateState: hmenum.DeviceFirmwareStateNewFirmwareAvailable},
			cur,
		},
		{
			"hmip ready-for-update surfaces available",
			hmenum.InterfaceHmIPRF,
			FirmwareInfo{Current: cur, Available: avl, UpdateState: hmenum.DeviceFirmwareStateReadyForUpdate},
			avl,
		},
		{
			"hmip up-to-date stays current",
			hmenum.InterfaceHmIPRF,
			FirmwareInfo{Current: cur, Available: avl, UpdateState: hmenum.DeviceFirmwareStateUpToDate},
			cur,
		},
		{
			"bidcos-rf surfaces available directly",
			hmenum.InterfaceBidCosRF,
			FirmwareInfo{Current: cur, Available: avl},
			avl,
		},
		{
			"bidcos-wired surfaces available directly",
			hmenum.InterfaceBidCosWired,
			FirmwareInfo{Current: cur, Available: avl},
			avl,
		},
		{
			"cuxd never surfaces an update",
			hmenum.InterfaceCUxD,
			FirmwareInfo{Current: cur, Available: avl, UpdateState: hmenum.DeviceFirmwareStateReadyForUpdate},
			cur,
		},
		{
			"empty available stays current",
			hmenum.InterfaceHmIPRF,
			FirmwareInfo{Current: cur, Available: "", UpdateState: hmenum.DeviceFirmwareStateReadyForUpdate},
			cur,
		},
		{
			"hmip all-zero placeholder stays current",
			hmenum.InterfaceHmIPRF,
			FirmwareInfo{Current: "4.4.22", Available: "0.0.0", UpdateState: hmenum.DeviceFirmwareStateReadyForUpdate},
			"4.4.22",
		},
		{
			"bidcos all-zero placeholder stays current",
			hmenum.InterfaceBidCosRF,
			FirmwareInfo{Current: "4.4.22", Available: "0.0.0"},
			"4.4.22",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := GatedLatestFirmware(tc.iface, tc.info); got != tc.want {
				t.Fatalf("GatedLatestFirmware = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDeviceUpdateAvailable confirms the device-level flag the REST DTO
// exposes: true only when the gated latest version differs from the installed
// one — so an HmIP-RF device with a known-but-undelivered newer firmware does
// not report an update.
func TestDeviceUpdateAvailable(t *testing.T) {
	t.Parallel()

	newDev := func(iface hmenum.Interface, info FirmwareInfo) *Device {
		return New(Config{Address: "DEV", Model: "m", Interface: iface, Firmware: info})
	}

	if d := newDev(hmenum.InterfaceHmIPRF, FirmwareInfo{
		Current: "1.0.0", Available: "1.2.0", UpdateState: hmenum.DeviceFirmwareStateNewFirmwareAvailable,
	}); d.UpdateAvailable() {
		t.Error("HmIP-RF with undelivered firmware must not report update available")
	}
	if d := newDev(hmenum.InterfaceHmIPRF, FirmwareInfo{
		Current: "1.0.0", Available: "1.2.0", UpdateState: hmenum.DeviceFirmwareStateReadyForUpdate,
	}); !d.UpdateAvailable() {
		t.Error("HmIP-RF with delivered firmware must report update available")
	}
	if d := newDev(hmenum.InterfaceBidCosRF, FirmwareInfo{
		Current: "1.0.0", Available: "1.0.0",
	}); d.UpdateAvailable() {
		t.Error("equal versions must not report update available")
	}
	if d := newDev(hmenum.InterfaceBidCosRF, FirmwareInfo{
		Current: "4.4.22", Available: "0.0.0",
	}); d.UpdateAvailable() {
		t.Error("all-zero placeholder version must not report update available")
	}
}
