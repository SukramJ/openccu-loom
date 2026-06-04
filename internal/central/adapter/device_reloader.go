// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// DeviceReloaderAdapter implements ws.DeviceReloader. It locates the
// device by address across all registered centrals, resolves its
// interface, and delegates to
// DeviceCoordinator.RefreshDeviceDescriptionsAndCreateMissingDevices
// scoped to that interface.
//
// Note: openccu-loom refreshes the whole interface (all devices on the
// same CCU backend) because the single-device refresh path would
// require a `DeviceCoordinator.RefreshSingleDevice` method that does
// not yet exist.
// From an operator perspective the result is identical (the target device
// is refreshed); the performance difference for a large inventory (~50
// devices) is negligible because the JSON-RPC `listDevices` response is
// cached. A future `DeviceCoordinator.RefreshSingleDevice` method using
// `Backend.GetDeviceDescription(addr)` + `GetParamsetDescription`
// iteration for only the affected channels would be the per-device
// optimisation.
type DeviceReloaderAdapter struct {
	registry *central.Registry
	writer   *clientpkg.ValueWriter
}

// NewDeviceReloaderAdapter wires the live adapter.
func NewDeviceReloaderAdapter(r *central.Registry, w *clientpkg.ValueWriter) *DeviceReloaderAdapter {
	return &DeviceReloaderAdapter{registry: r, writer: w}
}

// ReloadDeviceConfig re-fetches the device description from the CCU for
// the interface that owns deviceAddress and recreates any missing
// channels/data-points. Returns an error when the device is unknown or
// no backend can be resolved.
func (a *DeviceReloaderAdapter) ReloadDeviceConfig(ctx context.Context, deviceAddress string) error {
	if a.registry == nil || a.writer == nil {
		return errors.New("device_reloader: adapter not fully wired")
	}
	for _, unit := range a.registry.List() {
		dev, ok := unit.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		b, ok := a.writer.Backend(unit.Name(), dev.InterfaceID)
		if !ok {
			return fmt.Errorf("device_reloader: no backend for %s/%s", unit.Name(), dev.InterfaceID)
		}
		fetcher := &backendDescFetcher{ops: b}
		return unit.Devices.RefreshDeviceDescriptionsAndCreateMissingDevices(ctx, fetcher, dev.Interface)
	}
	return fmt.Errorf("device_reloader: device not found: %s", deviceAddress)
}

// backendDescFetcher wraps a [backends.Operations] as a
// coordinators.DeviceDescriptionFetcher. The backend's ListDevices call
// is already interface-scoped (each backend is wired to one interface),
// so the iface parameter is accepted but not forwarded — it is used
// only by the coordinator for registry writes.
type backendDescFetcher struct {
	ops backends.Operations
}

// ListDevices satisfies coordinators.DeviceDescriptionFetcher.
func (f *backendDescFetcher) ListDevices(ctx context.Context, _ hmenum.Interface) ([]hmproto.DeviceDescription, error) {
	return f.ops.ListDevices(ctx)
}
