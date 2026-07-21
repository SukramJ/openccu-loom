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
