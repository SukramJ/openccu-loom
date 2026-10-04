// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// TestDeviceAdminOpsReportAnUnknownDeviceAsNotFound pins the classification
// every device-admin operation gives an address no central's model holds:
// it is the caller's stale or mistyped input, not an unreachable backend, so
// the error wraps interfaces.ErrDeviceNotFound — which the north-bound
// surfaces answer as "not found" — and not ErrNoDeviceBackend, which they
// answer as an upstream failure. The fixture holds a real device on a wired
// backend, so the only thing the unknown address lacks is the device itself.
func TestDeviceAdminOpsReportAnUnknownDeviceAsNotFound(t *testing.T) {
	t.Parallel()
	const unknown = "UNKNOWN0000001"
	ctx := context.Background()
	ops := []struct {
		name string
		call func(d *DeviceAdminDomain) error
	}{
		{"unpair", func(d *DeviceAdminDomain) error { return d.UnpairDevice(ctx, unknown, true, false) }},
		{"rename device", func(d *DeviceAdminDomain) error { return d.RenameDevice(ctx, unknown, "x", false) }},
		{"rename channel", func(d *DeviceAdminDomain) error { return d.RenameChannel(ctx, unknown, 1, "x") }},
		{"firmware update", func(d *DeviceAdminDomain) error { return d.UpdateFirmware(ctx, unknown) }},
		{"restore config", func(d *DeviceAdminDomain) error { return d.RestoreDeviceConfig(ctx, unknown) }},
		{"clear config cache", func(d *DeviceAdminDomain) error { return d.ClearConfigCache(ctx, unknown) }},
		{"assign rf interface", func(d *DeviceAdminDomain) error { return d.AssignRFInterface(ctx, unknown, "GW1", false) }},
		{"install mode", func(d *DeviceAdminDomain) error { return d.SetInstallMode(ctx, unknown, 60) }},
		{"set rooms", func(d *DeviceAdminDomain) error { return d.SetRooms(ctx, unknown, []string{"Flur"}) }},
		{"set functions", func(d *DeviceAdminDomain) error { return d.SetFunctions(ctx, unknown, []string{"Licht"}) }},
		{"set channel rooms", func(d *DeviceAdminDomain) error { return d.SetChannelRooms(ctx, unknown, 1, []string{"Flur"}) }},
		{"set channel functions", func(d *DeviceAdminDomain) error {
			return d.SetChannelFunctions(ctx, unknown, 1, []string{"Licht"})
		}},
		{"set taxonomy paths", func(d *DeviceAdminDomain) error {
			return d.SetTaxonomyPaths(ctx, unknown, "room", []string{"room/flur"})
		}},
		{"set team", func(d *DeviceAdminDomain) error { return d.SetChannelTeam(ctx, unknown, 1, "") }},
		{"team candidates", func(d *DeviceAdminDomain) error {
			_, err := d.TeamCandidates(ctx, unknown, 1)
			return err
		}},
		{"communication test", func(d *DeviceAdminDomain) error {
			_, err := d.TestDeviceCommunication(ctx, unknown)
			return err
		}},
		{"replace (old device)", func(d *DeviceAdminDomain) error { return d.ReplaceDevice(ctx, "", unknown, "NEW0000000001") }},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()
			domain, _, _, fake := buildUnpairFixture(t, nil)

			err := op.call(domain)
			if !errors.Is(err, interfaces.ErrDeviceNotFound) {
				t.Fatalf("expected interfaces.ErrDeviceNotFound, got %v", err)
			}
			if errors.Is(err, ErrNoDeviceBackend) {
				t.Errorf("an unknown device must not also read as a missing backend: %v", err)
			}
			if !strings.Contains(err.Error(), unknown) {
				t.Errorf("error %q does not name the address", err)
			}
			if len(fake.deleteDeviceCalls) != 0 {
				t.Errorf("a backend call was made for an unknown device: %v", fake.deleteDeviceCalls)
			}
		})
	}
}

// TestDeviceAdminOpsKeepNoBackendForAnUnwiredDomain is the other half of
// the split: a domain without a registry or writer cannot resolve anything,
// which stays ErrNoDeviceBackend and must not be mistaken for an unknown
// device.
func TestDeviceAdminOpsKeepNoBackendForAnUnwiredDomain(t *testing.T) {
	t.Parallel()
	err := NewDeviceAdminDomain(nil, nil).UnpairDevice(context.Background(), "0001ABCD", false, false)
	if !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("expected ErrNoDeviceBackend, got %v", err)
	}
	if errors.Is(err, interfaces.ErrDeviceNotFound) {
		t.Fatalf("an unwired domain must not read as an unknown device: %v", err)
	}
}
