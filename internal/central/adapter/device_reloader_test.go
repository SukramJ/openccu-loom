// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

// Tests for DeviceReloaderAdapter — both ReloadDeviceConfig and
// ReloadChannelConfig.
//
// Strategy: build a central with a device seeded into ModelRegistry,
// register a fake backends.Operations that records ListDevices calls,
// then call the reload methods and assert delegation reaches the
// DeviceCoordinator refresh path (verified via the ListDevices call on the
// backend). For ReloadChannelConfig, fakeOperations.GetParamsetDescription
// returns (nil, nil), so fetched == 3 and ReloadChannelConfig on the
// coordinator succeeds before the device-level refresh runs.

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// listDevicesOps is a fake backends.Operations that records ListDevices
// calls. Embeds fakeOperations for all no-op stub methods.
type listDevicesOps struct {
	fakeOperations

	listCalls   int
	returnDescs []hmproto.DeviceDescription
	returnErr   error
}

func (f *listDevicesOps) ListDevices(_ context.Context) ([]hmproto.DeviceDescription, error) {
	f.listCalls++
	return f.returnDescs, f.returnErr
}

// buildReloaderFixture creates a central with one device at address
// deviceAddr registered, a fake backend wired via a ValueWriter, and
// returns the DeviceReloaderAdapter and the fake backend for inspection.
func buildReloaderFixture(
	t *testing.T,
	deviceAddr string,
	descs []hmproto.DeviceDescription,
	backendErr error,
) (*DeviceReloaderAdapter, *listDevicesOps) {
	t.Helper()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     deviceAddr,
		Model:       "HmIP-STH",
		Name:        "Sensor",
	})
	c.ModelRegistry.Put(dev)

	fake := &listDevicesOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		returnDescs:    descs,
		returnErr:      backendErr,
	}
	w := clientpkg.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", fake)

	return NewDeviceReloaderAdapter(reg, w), fake
}

func TestReloadDeviceConfigCallsListDevices(t *testing.T) {
	t.Parallel()

	// The backend returns one description for the device.
	descs := []hmproto.DeviceDescription{
		{Address: "0001ABCD", Type: "HmIP-STH", Children: []string{"0001ABCD:0", "0001ABCD:1"}},
	}
	adapter, fake := buildReloaderFixture(t, "0001ABCD", descs, nil)

	if err := adapter.ReloadDeviceConfig(context.Background(), "0001ABCD"); err != nil {
		t.Fatalf("ReloadDeviceConfig: %v", err)
	}
	if fake.listCalls != 1 {
		t.Errorf("expected 1 ListDevices call, got %d", fake.listCalls)
	}
}

func TestReloadDeviceConfigUnknownDeviceReturnsError(t *testing.T) {
	t.Parallel()

	adapter, _ := buildReloaderFixture(t, "0001ABCD", nil, nil)

	err := adapter.ReloadDeviceConfig(context.Background(), "DEADBEEF")
	if err == nil {
		t.Fatal("expected error for unknown device address")
	}
}

func TestReloadDeviceConfigNilRegistryReturnsError(t *testing.T) {
	t.Parallel()

	a := NewDeviceReloaderAdapter(nil, clientpkg.NewValueWriter())
	if err := a.ReloadDeviceConfig(context.Background(), "0001ABCD"); err == nil {
		t.Fatal("expected error when registry is nil")
	}
}

func TestReloadDeviceConfigNilWriterReturnsError(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	a := NewDeviceReloaderAdapter(reg, nil)
	if err := a.ReloadDeviceConfig(context.Background(), "0001ABCD"); err == nil {
		t.Fatal("expected error when writer is nil")
	}
}

func TestReloadDeviceConfigNoBackendReturnsError(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
	})
	c.ModelRegistry.Put(dev)

	// Writer has no backend registered for this central/interface.
	w := clientpkg.NewValueWriter()
	a := NewDeviceReloaderAdapter(reg, w)

	if err := a.ReloadDeviceConfig(context.Background(), "0001ABCD"); err == nil {
		t.Fatal("expected error when backend not registered")
	}
}

// ─── ReloadChannelConfig ──────────────────────────────────────────────────────

func TestReloadChannelConfigCallsBackend(t *testing.T) {
	t.Parallel()

	// The backend returns one description so RefreshDeviceDescriptionsAndCreateMissingDevices
	// has something to work with after the channel paramset re-pull.
	descs := []hmproto.DeviceDescription{
		{Address: "0001ABCD", Type: "HmIP-STH", Children: []string{"0001ABCD:0", "0001ABCD:1"}},
	}
	adapter, fake := buildReloaderFixture(t, "0001ABCD", descs, nil)

	if err := adapter.ReloadChannelConfig(context.Background(), "0001ABCD:1"); err != nil {
		t.Fatalf("ReloadChannelConfig: %v", err)
	}
	// The device-level refresh fires after the channel paramset re-pull.
	if fake.listCalls != 1 {
		t.Errorf("expected 1 ListDevices call after channel reload, got %d", fake.listCalls)
	}
}

func TestReloadChannelConfigUnknownChannelReturnsError(t *testing.T) {
	t.Parallel()

	adapter, _ := buildReloaderFixture(t, "0001ABCD", nil, nil)

	err := adapter.ReloadChannelConfig(context.Background(), "DEADBEEF:1")
	if err == nil {
		t.Fatal("expected error for unknown channel address")
	}
}

func TestReloadChannelConfigEmptyAddressReturnsError(t *testing.T) {
	t.Parallel()

	adapter, _ := buildReloaderFixture(t, "0001ABCD", nil, nil)

	if err := adapter.ReloadChannelConfig(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty channel address")
	}
}

func TestReloadChannelConfigNilRegistryReturnsError(t *testing.T) {
	t.Parallel()

	a := NewDeviceReloaderAdapter(nil, clientpkg.NewValueWriter())
	if err := a.ReloadChannelConfig(context.Background(), "0001ABCD:1"); err == nil {
		t.Fatal("expected error when registry is nil")
	}
}

func TestReloadChannelConfigNoBackendReturnsError(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
		Model:       "HmIP-STH",
	})
	c.ModelRegistry.Put(dev)

	// Writer has no backend registered for this central/interface.
	w := clientpkg.NewValueWriter()
	a := NewDeviceReloaderAdapter(reg, w)

	if err := a.ReloadChannelConfig(context.Background(), "0001ABCD:1"); err == nil {
		t.Fatal("expected error when backend not registered")
	}
}
