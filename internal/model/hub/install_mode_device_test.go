// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"context"
	"testing"
	"time"
)

// stubDeviceInstallWriter implements [DeviceInstallModeWriter] so
// EnableForDevice tests can assert device address forwarding.
type stubDeviceInstallWriter struct {
	stubInstall // satisfies base InstallModeWriter
	deviceAddr  string
}

func (s *stubDeviceInstallWriter) SetInstallModeForDevice(_ context.Context, _ string, _ time.Duration, deviceAddress string) error {
	s.deviceAddr = deviceAddress
	return nil
}

// TestEnableForDeviceForwardsAddressWhenWriterSupports verifies that
// EnableForDevice calls SetInstallModeForDevice when the writer also
// implements [DeviceInstallModeWriter] and a non-empty address is given.
func TestEnableForDeviceForwardsAddressWhenWriterSupports(t *testing.T) {
	t.Parallel()
	w := &stubDeviceInstallWriter{}
	m := NewInstallMode("HmIP-RF", w)
	const wantAddr = "00012A:1"
	if err := m.EnableForDevice(context.Background(), 30*time.Second, wantAddr); err != nil {
		t.Fatalf("EnableForDevice() unexpected error: %v", err)
	}
	if w.deviceAddr != wantAddr {
		t.Fatalf("deviceAddr=%q, want %q", w.deviceAddr, wantAddr)
	}
	// OnState must have been called → install mode is active.
	enabled, _, _ := m.InstallState()
	if !enabled {
		t.Fatal("install mode must be active after EnableForDevice")
	}
}

// TestEnableForDeviceEmptyAddressDelegatesToEnable verifies that an empty
// device address degrades to a broadcast Enable call without attempting
// SetInstallModeForDevice.
func TestEnableForDeviceEmptyAddressDelegatesToEnable(t *testing.T) {
	t.Parallel()
	w := &stubDeviceInstallWriter{}
	m := NewInstallMode("HmIP-RF", w)
	if err := m.EnableForDevice(context.Background(), 30*time.Second, ""); err != nil {
		t.Fatalf("EnableForDevice() empty addr unexpected error: %v", err)
	}
	// Broadcast path used → device-specific address must remain empty.
	if w.deviceAddr != "" {
		t.Fatalf("deviceAddr=%q, want empty for broadcast path", w.deviceAddr)
	}
	enabled, _, _ := m.InstallState()
	if !enabled {
		t.Fatal("install mode must be active after broadcast Enable")
	}
}

// TestEnableForDeviceFallsBackWhenWriterLacksInterface verifies that
// EnableForDevice falls back to the broadcast Enable when the writer
// does not implement [DeviceInstallModeWriter].
func TestEnableForDeviceFallsBackWhenWriterLacksInterface(t *testing.T) {
	t.Parallel()
	w := &stubInstall{} // plain InstallModeWriter, no device-address support
	m := NewInstallMode("HmIP-RF", w)
	if err := m.EnableForDevice(context.Background(), 30*time.Second, "00012A:1"); err != nil {
		t.Fatalf("EnableForDevice() fallback error: %v", err)
	}
	enabled, _, _ := m.InstallState()
	if !enabled {
		t.Fatal("install mode must be active after fallback Enable")
	}
}

// TestEnableForDeviceRejectsZeroDuration verifies the duration guard.
func TestEnableForDeviceRejectsZeroDuration(t *testing.T) {
	t.Parallel()
	w := &stubDeviceInstallWriter{}
	m := NewInstallMode("HmIP-RF", w)
	if err := m.EnableForDevice(context.Background(), 0, "00012A:1"); err == nil {
		t.Fatal("EnableForDevice with zero duration must return error")
	}
}
