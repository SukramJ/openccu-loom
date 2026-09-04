// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// hmAdpAdminFixture holds one central with one device on the given interface,
// plus the fake backend the admin domain resolves for it.
type hmAdpAdminFixture struct {
	reg    *central.Registry
	writer *client.ValueWriter
	fake   *configFakeOperations
}

// hmAdpBuildAdminFixture registers a device on iface and wires a fake CCU
// backend under the same wire interface id.
func hmAdpBuildAdminFixture(t *testing.T, iface hmenum.Interface, addr string) *hmAdpAdminFixture {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-cap"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	wireID := string(iface)
	dev := device.New(device.Config{
		InterfaceID: wireID,
		Interface:   iface,
		Address:     addr,
		Model:       "TestDevice",
	})
	dev.AddChannel(addr+":0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)
	c.DeviceRegistry.Put(registry.DeviceEntry{
		Interface: hmtypes.ParseWireInterfaceID(wireID),
		Address:   addr,
		Model:     "TestDevice",
	})
	c.DescRegistry.Put(hmtypes.ParseWireInterfaceID(wireID), hmproto.DeviceDescription{
		Address: addr,
		Type:    "TestDevice",
	})
	fake := &configFakeOperations{kind: backends.KindCCU}
	w := client.NewValueWriter()
	w.Register("ccu-cap", hmtypes.ParseWireInterfaceID(wireID), fake)
	return &hmAdpAdminFixture{reg: reg, writer: w, fake: fake}
}

// TestHmAdpInstallModeConsultsTheInterfaceCapability pins that SetInstallMode
// answers from the same hmenum gate every sibling admin op consults.
//
// The gate is a property of the backend call, not only of the data-point
// wiring: hmenum documents setInstallMode as meaningful for HmIP-RF and
// BidCos-* but not for VirtualDevices (aggregated groups, no radio) or CUxD
// (synchronous, returns ErrUnsupported). Without the gate a VirtualDevices
// address put a real setInstallMode on the wire against a radio-less daemon.
func TestHmAdpInstallModeConsultsTheInterfaceCapability(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		iface       hmenum.Interface
		wantRefused bool
	}{
		{hmenum.InterfaceHmIPRF, false},
		{hmenum.InterfaceBidCosRF, false},
		{hmenum.InterfaceVirtualDevices, true},
		{hmenum.InterfaceCUxD, true},
	} {
		f := hmAdpBuildAdminFixture(t, tc.iface, "CAP001")
		admin := NewDeviceAdminDomain(f.reg, f.writer)
		err := admin.SetInstallMode(context.Background(), "CAP001", 60)
		refused := errors.Is(err, backends.ErrUnsupported)
		if refused != tc.wantRefused {
			t.Fatalf("SetInstallMode on %s: refused=%v (err=%v), want refused=%v",
				tc.iface, refused, err, tc.wantRefused)
		}
	}
}

// TestHmAdpFirmwareUpdateConsultsTheInterfaceCapability is the same gate for
// UpdateFirmware, whose set hmenum keeps separately.
func TestHmAdpFirmwareUpdateConsultsTheInterfaceCapability(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		iface       hmenum.Interface
		wantRefused bool
	}{
		{hmenum.InterfaceHmIPRF, false},
		{hmenum.InterfaceBidCosWired, false},
		{hmenum.InterfaceVirtualDevices, true},
		{hmenum.InterfaceCUxD, true},
	} {
		f := hmAdpBuildAdminFixture(t, tc.iface, "CAP002")
		admin := NewDeviceAdminDomain(f.reg, f.writer)
		err := admin.UpdateFirmware(context.Background(), "CAP002")
		refused := errors.Is(err, backends.ErrUnsupported)
		if refused != tc.wantRefused {
			t.Fatalf("UpdateFirmware on %s: refused=%v (err=%v), want refused=%v",
				tc.iface, refused, err, tc.wantRefused)
		}
	}
}
