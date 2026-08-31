// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestVirtualRemoteModelClassificationIsSingleSourced pins every consumer of
// the virtual-remote model rule to one set.
//
// The classification existed twice with two different behaviours: an exact
// three-model set in the device aggregate and a prefix match in the device
// coordinator. The false rows below are exactly the strings the two spellings
// disagreed on, so they are the tie rather than a restatement of the set.
//
// The coordinator half drives the effect through a real central unit. It pins
// a rule on an API with no production caller today, which is the reason the
// divergence went unnoticed — a rule pin, not a shipped behaviour.
func TestVirtualRemoteModelClassificationIsSingleSourced(t *testing.T) {
	t.Parallel()

	models := []struct {
		model string
		want  bool
	}{
		{"HM-RCV-50", true},
		{"HMW-RCV-50", true},
		{"HmIP-RCV-50", true},
		{"HM-RCV-51", false},
		{"HmIP-RCV-2", false},
		{"HM-RCV-", false},
		{"HM-PB-2-WM55", false},
	}
	for _, tc := range models {
		if got := hmenum.IsVirtualRemoteModel(tc.model); got != tc.want {
			t.Errorf("hmenum.IsVirtualRemoteModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
		dev := device.New(device.Config{Address: "VRT0001", Model: tc.model})
		if got := dev.IsVirtualRemote(); got != tc.want {
			t.Errorf("device.IsVirtualRemote() for model %q = %v, want %v", tc.model, got, tc.want)
		}
	}

	const centralName = "ccu-virtual-remotes"
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	iface := hmtypes.NewWireInterfaceID(centralName, hmenum.InterfaceHmIPRF)
	// Top-level addresses (no ':' suffix) — a channel address is filtered out
	// before the model rule is ever consulted.
	c.DescRegistry.Put(iface, hmproto.DeviceDescription{Address: "VRT0001", Type: "HM-RCV-50"})
	c.DescRegistry.Put(iface, hmproto.DeviceDescription{Address: "VRT0002", Type: "HM-RCV-51"})

	remotes := c.Devices.GetVirtualRemotes(iface)
	if len(remotes) != 1 {
		t.Fatalf("GetVirtualRemotes = %v, want exactly one entry (VRT0001)", remotes)
	}
	if remotes[0].Address != "VRT0001" {
		t.Errorf("GetVirtualRemotes()[0].Address = %q, want VRT0001", remotes[0].Address)
	}
	addrs := c.Devices.GetVirtualRemoteAddresses(iface)
	if len(addrs) != 1 || addrs[0] != "VRT0001" {
		t.Errorf("GetVirtualRemoteAddresses = %v, want [VRT0001]", addrs)
	}
}
