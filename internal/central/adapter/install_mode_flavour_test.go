// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestBothInstallModePathsSendTheSameFlavour compares the two production
// paths that open a device-scoped pairing window — the hub writer and the
// device-admin domain — against one another rather than against a constant.
//
// The CCU distinguishes the normal install mode from the re-pairing one by
// this argument, so the two surfaces disagreeing means the operator gets a
// different pairing window depending on which one they used. A test that
// compared each path against installModeNormal separately would stay green
// while they drifted, because the drifting site would simply not read the
// constant.
func TestBothInstallModePathsSendTheSameFlavour(t *testing.T) {
	t.Parallel()

	const centralName = "ccu-flavour"
	iface := hmenum.InterfaceBidCosRF
	wireID := hmtypes.NewWireInterfaceID(centralName, iface)

	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: wireID.String(),
		Interface:   iface,
		Address:     "ABC123",
		Model:       "HM-LC-Sw1-Pl",
		Name:        "Lampe",
	})
	c.ModelRegistry.Put(dev)

	fake := &installFakeOps{}
	w := clientpkg.NewValueWriter()
	w.Register(centralName, wireID, fake)

	admin := NewDeviceAdminDomain(reg, w)
	if err := admin.SetInstallMode(t.Context(), "ABC123", 60); err != nil {
		t.Fatalf("DeviceAdminDomain.SetInstallMode: %v", err)
	}

	writer := &installModeWriter{unit: c, writer: w}
	if err := writer.SetInstallModeForDevice(t.Context(), string(iface), 60*time.Second, "ABC123"); err != nil {
		t.Fatalf("installModeWriter.SetInstallModeForDevice: %v", err)
	}

	if len(fake.calls) != 2 {
		t.Fatalf("expected 2 backend calls, got %d", len(fake.calls))
	}
	if fake.calls[0].mode != fake.calls[1].mode {
		t.Errorf("install-mode flavour differs between the two paths: device-admin sent %d, hub writer sent %d",
			fake.calls[0].mode, fake.calls[1].mode)
	}
}
