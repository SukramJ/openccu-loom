// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestDeleteDevicesClearsTheDeferredCreationQueue pins that a device the
// CCU reports as deleted also leaves the deferred-creation queue.
//
// With delay_new_device_creation on, a freshly announced device is
// parked instead of materialised. If the operator then unpairs it on the
// CCU before accepting it, the deleteDevices callback used to clear the
// model and the registries but not the parked set, so the address stayed
// on the inbox surface naming a device that no longer exists — and
// accepting it materialised a ghost with no data points. The full pull
// sweeps stale parked rows, but that only runs at boot.
func TestDeleteDevicesClearsTheDeferredCreationQueue(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	const iface = "ccu-01-HmIP-RF"
	const addr = "PARKED001"
	wire := hmtypes.WireInterfaceID(iface)

	c.Devices.StoreDelayedDeviceDescriptions(t.Context(), wire, []hmproto.DeviceDescription{
		{Address: addr, Type: "HmIP-BSM", Children: []string{addr + ":1"}},
		{Address: addr + ":1", Parent: addr, Type: "SWITCH_VIRTUAL_RECEIVER"},
	})
	if !c.Devices.IsParked(wire, addr) {
		t.Fatalf("fixture: %s was not parked", addr)
	}

	h := NewCallbackHandlers(c, nil)
	if err := h.DeleteDevices(t.Context(), iface, []string{addr}); err != nil {
		t.Fatalf("DeleteDevices: %v", err)
	}

	if c.Devices.IsParked(wire, addr) {
		t.Errorf("%s is still parked after the CCU deleted it", addr)
	}
	for _, p := range c.Devices.PendingDevices() {
		if p.Address == addr {
			t.Errorf("%s is still listed as pending: %+v", addr, p)
		}
	}
}
