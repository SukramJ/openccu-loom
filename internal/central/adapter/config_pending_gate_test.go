// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// configPendingGateDevice builds a one-channel device carrying a
// CONFIG_PENDING data point, with its ProductGroup resolved the way the
// device-ingest pipeline resolves it (model prefix wins over interface,
// see [hmenum.ProductGroupForModel] and ADR 0023).
func configPendingGateDevice(addr, model, wireID string, iface hmenum.Interface) *device.Device {
	dev := device.New(device.Config{
		InterfaceID:  wireID,
		Interface:    iface,
		Address:      addr,
		Model:        model,
		ProductGroup: hmenum.ProductGroupForModel(model, iface),
	})
	ch := dev.AddChannel(addr+":0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	ch.Put(newConfigPendingDP(wireID, addr+":0"))
	return dev
}

// TestConfigPendingSettleFollowsDeviceProductGroup pins which devices the
// CONFIG_PENDING True→False settle leg runs for, driven through the
// production wiring (wireConfigPendingHook + CallbackHandlers.Event) rather
// than through a hand-built closure.
//
// The rule is the device-level one: a device whose product group is HmIP or
// HmIP-Wired pushes a reliable CONFIG_PENDING regardless of the interface it
// is hosted on — an HmIP-HEATING group lives on VirtualDevices — and the REST
// device surface already advertises exactly that verdict to the SPA as
// `master_pushes_config_pending`. An interface that pushes CONFIG_PENDING on
// its own keeps the settle leg for every device it serves, so no device that
// is gated in today loses it.
//
// The expectations below are stated per (interface, model) pair rather than
// re-derived from hmenum: which families get a settle-driven MASTER read is a
// product decision about CCU radio load, not a restatement of the predicate
// the production code calls.
func TestConfigPendingSettleFollowsDeviceProductGroup(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		iface      hmenum.Interface
		model      string
		wantSettle bool
	}{
		// An HmIP-flavoured group on the VirtualDevices interface: the case
		// the interface-only gate dropped while REST told the SPA to wait.
		{"virtualdevices_hmip_group", hmenum.InterfaceVirtualDevices, "HmIP-HEATING", true},
		{"hmiprf_hmip_device", hmenum.InterfaceHmIPRF, "HmIP-eTRV-2", true},
		// Product group wins over the hosting interface.
		{"bidcosrf_hmip_device", hmenum.InterfaceBidCosRF, "HmIP-eTRV-2", true},
		// The interface pushes CONFIG_PENDING even when the model name
		// resolves to a classic product group — nothing loses its hook.
		{"hmiprf_classic_model", hmenum.InterfaceHmIPRF, "HM-CC-RT-DN", true},
		{"bidcosrf_classic_device", hmenum.InterfaceBidCosRF, "HM-LC-Sw1-Pl", false},
		{"cuxd_device", hmenum.InterfaceCUxD, "CUX9002001", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const centralName = "cfgpend"
			c, err := central.New(central.Config{Name: centralName})
			if err != nil {
				t.Fatalf("central.New: %v", err)
			}
			wireID := WireInterfaceID(centralName, tc.iface)
			const addr = "000CFGP01"
			c.ModelRegistry.Put(configPendingGateDevice(addr, tc.model, wireID, tc.iface))

			resolved := make(chan string, 4)
			getterFor := func(interfaceID string) backends.MasterGetter {
				resolved <- interfaceID
				return &perChannelGetter{}
			}
			wireConfigPendingHook(context.Background(), c, openAdapterTestDB(t), centralName, getterFor, nil)

			handlers := NewCallbackHandlers(c, nil)
			t.Cleanup(handlers.Stop)
			initID := InitInterfaceID(c.InstanceName(), centralName, tc.iface)
			for _, v := range []bool{true, false} {
				if err := handlers.Event(context.Background(), initID, addr+":0",
					string(hmenum.ParameterConfigPending), xmlrpc.BoolValue(v)); err != nil {
					t.Fatalf("Event(CONFIG_PENDING=%v): %v", v, err)
				}
			}

			if tc.wantSettle {
				select {
				case got := <-resolved:
					if got != wireID {
						t.Fatalf("settle leg resolved the MASTER getter for %q, want %q", got, wireID)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("settle leg did not run for %s on %s, which REST advertises as CONFIG_PENDING-pushing", tc.model, tc.iface)
				}
				return
			}
			select {
			case got := <-resolved:
				t.Fatalf("settle leg ran for %s on %s (getter %q); this family is covered by the MasterPoller instead", tc.model, tc.iface, got)
			case <-time.After(200 * time.Millisecond):
			}
		})
	}
}
