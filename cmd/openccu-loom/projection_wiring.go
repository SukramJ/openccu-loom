// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/projection"
)

// deviceProjector resolves an address to its device, its central and that
// central's fleet, and asks the projection service. It backs
// GET /devices/{addr}/projection and the MCP tool of the same name.
type deviceProjector struct {
	devices *adapter.DevicesAdapter
	reg     *central.Registry
	svc     *projection.Service
}

// newDeviceProjector wires the projection over the planes that answer
// today. A plane that is switched off is still passed: it answers
// inactive, which is part of the projection.
func newDeviceProjector(devices *adapter.DevicesAdapter, reg *central.Registry, planes ...projection.Source) *deviceProjector {
	return &deviceProjector{devices: devices, reg: reg, svc: projection.New(planes...)}
}

// ProjectDevice implements handlers.DeviceProjector and mcp.DeviceProjector.
func (p *deviceProjector) ProjectDevice(ctx context.Context, address, plane string) (projection.Device, bool, error) {
	dev, ok := p.devices.Device(address)
	if !ok {
		return projection.Device{}, false, nil
	}
	centralName := p.devices.CentralOf(address)
	var fleet []*device.Device
	if u, found := p.reg.Get(centralName); found && u.ModelRegistry != nil {
		fleet = u.ModelRegistry.List()
	}
	out, err := p.svc.Project(ctx, centralName, fleet, dev, plane)
	if err != nil {
		return projection.Device{}, false, err
	}
	return out, true, nil
}
