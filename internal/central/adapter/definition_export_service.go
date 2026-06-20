// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device/definitionexport"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// DefinitionExportDomain produces a device-definition export (byte-compatible
// with the Python reference's export_device_definition) for any device across the
// configured centrals. It resolves the owning central + interface from the
// device address, then drives the export over that interface's
// order-preserving RPC caller.
type DefinitionExportDomain struct {
	reg      *central.Registry
	randomID definitionexport.RandomIDFunc
}

// NewDefinitionExportDomain wires the export domain to the central registry.
// The anonymisation id generator defaults to [definitionexport.DefaultRandomID]
// (a fresh random "VCU<7-digit>" per export, matching the Python reference).
func NewDefinitionExportDomain(reg *central.Registry) *DefinitionExportDomain {
	return &DefinitionExportDomain{reg: reg, randomID: definitionexport.DefaultRandomID}
}

// ExportDefinition resolves deviceAddress to its central + interface client and
// returns the device model plus the zip archive. Returns
// [definitionexport.ErrDeviceNotFound] when no central knows the device, which
// callers map to a 404.
func (d *DefinitionExportDomain) ExportDefinition(ctx context.Context, deviceAddress string) (model string, zip []byte, err error) {
	if d == nil || d.reg == nil {
		return "", nil, errors.New("definition export: registry not wired")
	}
	for _, name := range d.reg.Names() {
		unit, ok := d.reg.Get(name)
		if !ok || unit == nil || unit.ModelRegistry == nil {
			continue
		}
		dev, ok := unit.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		ic := clientForInterface(unit, dev.Interface)
		if ic == nil {
			return "", nil, fmt.Errorf("definition export: no client for interface %s on central %s", dev.Interface, name)
		}
		res, err := definitionexport.Export(ctx, ic, deviceAddress, d.randomID)
		if err != nil {
			return "", nil, err
		}
		return res.Model, res.Zip, nil
	}
	return "", nil, definitionexport.ErrDeviceNotFound
}

// clientForInterface returns the InterfaceClient serving iface on unit, or nil.
func clientForInterface(unit *central.Unit, iface hmenum.Interface) definitionexport.OrderedRPC {
	if unit == nil || unit.Clients == nil {
		return nil
	}
	for _, e := range unit.Clients.List() {
		if e != nil && e.Interface == iface && e.Client != nil {
			return e.Client
		}
	}
	return nil
}
