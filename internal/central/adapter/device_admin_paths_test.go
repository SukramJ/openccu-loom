// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// device_admin_paths_test.go covers the device-found paths in
// DeviceAdminDomain: RenameDevice success, resolve with device-found
// (no backend), SetRooms/SetFunctions with device found but nil HubModel,
// plus the AcceptInboxDevice device-found-nil-HubModel path.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// registerDeviceInRegistry creates a central, registers it, and adds
// a device to its ModelRegistry.
func registerDeviceInRegistry(t *testing.T, name, devAddr, interfaceID string) *central.Registry {
	t.Helper()
	c, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dev := device.New(device.Config{Address: devAddr, InterfaceID: interfaceID, Model: "TestModel"})
	c.ModelRegistry.Put(dev)
	return reg
}

// ============================================================
// RenameDevice — device found success path
// ============================================================

func TestDeviceAdminRenameDeviceFound(t *testing.T) {
	t.Parallel()
	reg := registerDeviceInRegistry(t, "ccu-rename", "DEV001", "HmIP-RF")
	a := NewDeviceAdminDomain(reg, nil)
	err := a.RenameDevice(context.Background(), "DEV001", "MyNewName")
	if err != nil {
		t.Fatalf("RenameDevice found device: %v", err)
	}
}

// ============================================================
// SetRooms — device found but HubModel nil
// ============================================================

func TestDeviceAdminSetRoomsDeviceFoundHubNil(t *testing.T) {
	t.Parallel()
	reg := registerDeviceInRegistry(t, "ccu-rooms", "DEV002", "HmIP-RF")
	a := NewDeviceAdminDomain(reg, nil)
	err := a.SetRooms(context.Background(), "DEV002", []string{"Room1"})
	// HubModel is nil → ErrNoDeviceBackend
	if err == nil {
		t.Fatal("SetRooms with nil HubModel must return error")
	}
}

// ============================================================
// SetFunctions — device found but HubModel nil
// ============================================================

func TestDeviceAdminSetFunctionsDeviceFoundHubNil(t *testing.T) {
	t.Parallel()
	reg := registerDeviceInRegistry(t, "ccu-funcs", "DEV003", "HmIP-RF")
	a := NewDeviceAdminDomain(reg, nil)
	err := a.SetFunctions(context.Background(), "DEV003", []string{"Lights"})
	if err == nil {
		t.Fatal("SetFunctions with nil HubModel must return error")
	}
}

// ============================================================
// resolve — device found but no backend (nil writer case)
// ============================================================

func TestDeviceAdminResolveDeviceFoundNilWriter(t *testing.T) {
	t.Parallel()
	reg := registerDeviceInRegistry(t, "ccu-resolve", "DEV004", "HmIP-RF")
	a := NewDeviceAdminDomain(reg, nil)
	// writer is nil → resolve returns ErrNoDeviceBackend
	_, err := a.resolve("DEV004")
	if err == nil {
		t.Fatal("resolve with nil writer must return error")
	}
}

// ============================================================
// AcceptInboxDevice — empty registry walk (central without device)
// ============================================================

func TestDeviceAdminAcceptInboxDeviceNotInModelRegistry(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-accept"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	// No device in registry → final "not found" error
	a := NewDeviceAdminDomain(reg, nil)
	err = a.AcceptInboxDevice(context.Background(), "NOSUCHDEV")
	if err == nil {
		t.Fatal("AcceptInboxDevice not found must error")
	}
}

// ============================================================
// AcceptInboxDevice — central found, HubModel nil (no-op central path)
// ============================================================

func TestDeviceAdminAcceptInboxDeviceHubModelNil(t *testing.T) {
	t.Parallel()
	reg := registerDeviceInRegistry(t, "ccu-accept2", "DEV005", "HmIP-RF")
	a := NewDeviceAdminDomain(reg, nil)
	// Device exists in ModelRegistry but HubModel is nil → AcceptInboxDeviceRemote
	// will fail because HubModel is nil, so the central is skipped, and we
	// eventually reach the "not found" error.
	err := a.AcceptInboxDevice(context.Background(), "DEV005")
	if err == nil {
		t.Fatal("AcceptInboxDevice nil HubModel must error (no AcceptInboxDeviceRemote)")
	}
}

// ============================================================
// DeviceAdminDomain nil-registry guards
// ============================================================

func TestDeviceAdminResolveNilRegistry(t *testing.T) {
	t.Parallel()
	a := NewDeviceAdminDomain(nil, nil)
	_, err := a.resolve("DEV001")
	if err == nil {
		t.Fatal("expected error from nil registry")
	}
}

func TestDeviceAdminRenameDeviceNilRegistry(t *testing.T) {
	t.Parallel()
	a := NewDeviceAdminDomain(nil, nil)
	err := a.RenameDevice(context.Background(), "DEV001", "NewName")
	if err == nil {
		t.Fatal("expected error from nil registry")
	}
}

func TestDeviceAdminAcceptInboxNilRegistry(t *testing.T) {
	t.Parallel()
	a := NewDeviceAdminDomain(nil, nil)
	err := a.AcceptInboxDevice(context.Background(), "DEV001")
	if err == nil {
		t.Fatal("expected error from nil registry")
	}
}

func TestDeviceAdminUpdateFirmwareNilRegistry(t *testing.T) {
	t.Parallel()
	a := NewDeviceAdminDomain(nil, nil)
	err := a.UpdateFirmware(context.Background(), "DEV001")
	if err == nil {
		t.Fatal("expected error from nil registry")
	}
}

func TestDeviceAdminSetInstallModeNilRegistry(t *testing.T) {
	t.Parallel()
	a := NewDeviceAdminDomain(nil, nil)
	err := a.SetInstallMode(context.Background(), "DEV001", 60)
	if err == nil {
		t.Fatal("expected error from nil registry")
	}
}

func TestDeviceAdminSetRoomsNilRegistry(t *testing.T) {
	t.Parallel()
	a := NewDeviceAdminDomain(nil, nil)
	err := a.SetRooms(context.Background(), "DEV001", []string{"Room1"})
	if err == nil {
		t.Fatal("expected error from nil registry")
	}
}

func TestDeviceAdminSetFunctionsNilRegistry(t *testing.T) {
	t.Parallel()
	a := NewDeviceAdminDomain(nil, nil)
	err := a.SetFunctions(context.Background(), "DEV001", []string{"Lights"})
	if err == nil {
		t.Fatal("expected error from nil registry")
	}
}

// Non-nil registry but device not found.
func TestDeviceAdminRenameDeviceNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := NewDeviceAdminDomain(reg, nil)
	err := a.RenameDevice(context.Background(), "NOSUCHDEV", "NewName")
	if err == nil {
		t.Fatal("expected error when device not found")
	}
}

func TestDeviceAdminAcceptInboxNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := NewDeviceAdminDomain(reg, nil)
	err := a.AcceptInboxDevice(context.Background(), "NOSUCHDEV")
	if err == nil {
		t.Fatal("expected error when device not found")
	}
}

func TestDeviceAdminSetRoomsNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := NewDeviceAdminDomain(reg, nil)
	err := a.SetRooms(context.Background(), "NOSUCHDEV", []string{"Room1"})
	if err == nil {
		t.Fatal("expected error when device not found")
	}
}

func TestDeviceAdminSetFunctionsNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := NewDeviceAdminDomain(reg, nil)
	err := a.SetFunctions(context.Background(), "NOSUCHDEV", []string{"Lights"})
	if err == nil {
		t.Fatal("expected error when device not found")
	}
}

// ============================================================
// DevicesAdapter nil-registry guards
// ============================================================

func TestDevicesAdapterDevicesNilRegistry(t *testing.T) {
	t.Parallel()
	a := NewDevicesAdapter(nil)
	if got := a.Devices(); got != nil {
		t.Errorf("Devices() nil registry = %v, want nil", got)
	}
}

func TestDevicesAdapterDeviceNilRegistry(t *testing.T) {
	t.Parallel()
	a := NewDevicesAdapter(nil)
	_, ok := a.Device("DEV001")
	if ok {
		t.Error("Device() nil registry must return false")
	}
}

func TestDevicesAdapterRefreshDevicesNilRegistry(t *testing.T) {
	t.Parallel()
	a := NewDevicesAdapter(nil)
	err := a.RefreshDevices(context.Background())
	if err == nil {
		t.Fatal("expected error for nil registry")
	}
}

func TestDevicesAdapterRefreshDevicesNilWriter(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := NewDevicesAdapter(reg)
	// nil writer → returns nil (best-effort)
	if err := a.RefreshDevices(context.Background()); err != nil {
		t.Fatalf("nil writer → expected nil error, got %v", err)
	}
}

func TestDevicesAdapterCentralOfNilRegistry(t *testing.T) {
	t.Parallel()
	a := NewDevicesAdapter(nil)
	if got := a.CentralOf("DEV001"); got != "" {
		t.Errorf("CentralOf nil registry = %q, want empty", got)
	}
}

func TestDevicesAdapterCentralOfNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := NewDevicesAdapter(reg)
	if got := a.CentralOf("NOSUCHDEV"); got != "" {
		t.Errorf("CentralOf not found = %q, want empty", got)
	}
}

func TestDevicesAdapterWithWriter(t *testing.T) {
	t.Parallel()
	a := NewDevicesAdapter(nil).WithWriter(nil)
	if a == nil {
		t.Fatal("WithWriter must return non-nil adapter")
	}
}

// ============================================================
// deviceAddressOf tests
// ============================================================

func TestDeviceAddressOfNormal(t *testing.T) {
	t.Parallel()
	if got := deviceAddressOf("DEV001:1"); got != "DEV001" {
		t.Errorf("deviceAddressOf(DEV001:1) = %q, want DEV001", got)
	}
}

func TestDeviceAddressOfNoColon(t *testing.T) {
	t.Parallel()
	if got := deviceAddressOf("DEV001"); got != "DEV001" {
		t.Errorf("deviceAddressOf(DEV001) = %q, want DEV001", got)
	}
}

func TestDeviceAddressOfMultipleColons(t *testing.T) {
	t.Parallel()
	if got := deviceAddressOf("CCU:DEV001:1"); got != "CCU:DEV001" {
		t.Errorf("deviceAddressOf(CCU:DEV001:1) = %q, want CCU:DEV001", got)
	}
}

// ============================================================
// DataPointWriterAdapter nil-writer guard
// ============================================================

func TestDataPointWriterAdapterNilWriter(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	a := NewDataPointWriterAdapter(reg, nil)
	err := a.SetValue(context.Background(), "DEV:1", "STATE", true, 0)
	if err == nil {
		t.Fatal("expected ErrNoWriter for nil writer")
	}
}
