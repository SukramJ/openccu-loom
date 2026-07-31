// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// ccuRebooter is the narrow capability a backend exposes when it can reboot
// its CCU host. Only the CCU backend implements it; CUxD / Homegear backends
// do not, so a reboot request routed to one surfaces as unsupported.
type ccuRebooter interface {
	RebootCCU(ctx context.Context) (bool, error)
}

// ccuPositionSetter is the narrow capability a backend exposes when it can
// write its CCU's astro reference position. ReGa-backed only, for the same
// reason as ccuRebooter.
type ccuPositionSetter interface {
	SetCCUPosition(ctx context.Context, longitude, latitude float64) error
}

// CCUMaintenanceDomain runs per-central CCU host-maintenance operations.
// Today that is a reboot; the type is the landing place for further CCU
// maintenance actions (each resolving the target central's primary backend
// from the registry and dispatching there).
type CCUMaintenanceDomain struct {
	registry *central.Registry
	writer   *client.ValueWriter
}

// NewCCUMaintenanceDomain wires the live adapter.
func NewCCUMaintenanceDomain(r *central.Registry, w *client.ValueWriter) *CCUMaintenanceDomain {
	return &CCUMaintenanceDomain{registry: r, writer: w}
}

// RebootCCU reboots the CCU behind the named central via the central's
// primary backend (which runs the reboot_ccu ReGa script). It returns
// [hmerr.ErrUnknownCentral] when the central is not registered and
// [backends.ErrUnsupported] when the resolved backend cannot reboot.
func (a *CCUMaintenanceDomain) RebootCCU(ctx context.Context, centralName string) error {
	if a.registry == nil || a.writer == nil {
		return hmerr.ErrUnknownCentral
	}
	unit, ok := a.registry.Get(centralName)
	if !ok || unit == nil {
		return hmerr.ErrUnknownCentral
	}
	_, backend, err := primaryBackendOf(unit, a.writer)
	if err != nil {
		return err
	}
	rb, ok := backend.(ccuRebooter)
	if !ok {
		return backends.ErrUnsupported
	}
	_, err = rb.RebootCCU(ctx)
	return err
}

// SetCCUPosition writes the astro reference position of the CCU behind the
// named central. It returns [hmerr.ErrUnknownCentral] when the central is
// not registered, [backends.ErrUnsupported] when the resolved backend has
// no ReGa path, and the runner's validation error when a coordinate is out
// of range.
//
// On success the central's cached SystemInfo is patched in place so the
// fleet view reflects the new position without waiting for the next hub
// wiring pass - the values are known-good, since the runner only returns
// nil after the CCU read them back unchanged.
func (a *CCUMaintenanceDomain) SetCCUPosition(ctx context.Context, centralName string, longitude, latitude float64) error {
	if a.registry == nil || a.writer == nil {
		return hmerr.ErrUnknownCentral
	}
	unit, ok := a.registry.Get(centralName)
	if !ok || unit == nil {
		return hmerr.ErrUnknownCentral
	}
	_, backend, err := primaryBackendOf(unit, a.writer)
	if err != nil {
		return err
	}
	ps, ok := backend.(ccuPositionSetter)
	if !ok {
		return backends.ErrUnsupported
	}
	if err := ps.SetCCUPosition(ctx, longitude, latitude); err != nil {
		return err
	}
	unit.PatchSystemPosition(longitude, latitude)
	return nil
}

// DownloadFirmware instructs the CCU behind the named central to fetch a
// firmware image from firmwareURL onto the central (posting to the CCU's
// maintenance CGI via the central's primary backend). When centralName is
// empty and exactly one central is registered, that central is used —
// matching the single-CCU convenience of the other system endpoints.
//
// Returns [hmerr.ErrUnknownCentral] when the central cannot be resolved
// and [backends.ErrUnsupported] when the resolved backend has no
// firmware-download path (CUxD, Homegear) or lacks an active JSON-RPC
// session; the CCU-side transport error is propagated verbatim otherwise.
func (a *CCUMaintenanceDomain) DownloadFirmware(ctx context.Context, centralName, firmwareURL string) error {
	if a.registry == nil || a.writer == nil {
		return hmerr.ErrUnknownCentral
	}
	unit, err := a.resolveCentral(centralName)
	if err != nil {
		return err
	}
	_, backend, err := primaryBackendOf(unit, a.writer)
	if err != nil {
		return err
	}
	return backend.DownloadFirmware(ctx, firmwareURL)
}

// resolveCentral looks up the target central by name, defaulting to the
// sole registered central when name is empty. Returns
// [hmerr.ErrUnknownCentral] when the name is unknown or when no name was
// given but the daemon manages more than one central.
func (a *CCUMaintenanceDomain) resolveCentral(name string) (*central.Unit, error) {
	if name != "" {
		unit, ok := a.registry.Get(name)
		if !ok || unit == nil {
			return nil, hmerr.ErrUnknownCentral
		}
		return unit, nil
	}
	units := a.registry.List()
	if len(units) == 1 && units[0] != nil {
		return units[0], nil
	}
	return nil, hmerr.ErrUnknownCentral
}
