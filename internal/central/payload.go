// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Compile-time guarantee that *CentralUnit satisfies the universal
// Source contract. ADR 0007 step 8 — top-level service.
var _ payload.Source = (*CentralUnit)(nil)

// Info returns the typed identity payload of the central.
func (c *CentralUnit) Info() payload.InfoPayload {
	if c == nil {
		return nil
	}
	si := c.SystemInformation()
	return &payload.CentralUnitInfo{
		Name:             c.cfg.Name,
		Model:            si.Model,
		SWVersion:        si.Version,
		Hostname:         si.Hostname,
		SerialNumber:     si.Serial,
		ConfigurationURL: si.URL,
		IsHaApp:          si.IsHaApp,
	}
}

// Config returns the operator-tunable configuration of the central.
// Today the central exposes few runtime-tunable knobs; the typed
// shape lets adapters refer to the bucket without special-casing.
func (c *CentralUnit) Config() payload.ConfigPayload {
	if c == nil {
		return nil
	}
	return &payload.CentralUnitConfig{Name: c.cfg.Name}
}

// State returns the central's runtime status. The state-machine
// bucket and the registered-device count are the two metrics
// northbound adapters consume — health page, connectivity badges,
// REST `/info` endpoint.
func (c *CentralUnit) State() payload.StatePayload {
	if c == nil {
		return nil
	}
	out := &payload.CentralUnitState{}
	if c.StateMachine != nil {
		out.State = string(c.StateMachine.State())
	}
	if c.DeviceRegistry != nil {
		out.DeviceCount = c.DeviceRegistry.Len()
	}
	return out
}

// registerCentralServices wires the externally invocable operations
// onto the embedded ServiceRegistry. Called once from [New] after the
// CentralUnit is fully constructed.
func (c *CentralUnit) registerCentralServices() {
	c.RegisterService("set_install_mode", func(ctx context.Context, params map[string]any, _ hmenum.CommandPriority) error {
		on, err := payload.ParamBool(params, "on")
		if err != nil {
			return err
		}
		seconds := 60
		if v, ok := params["time"]; ok {
			if f, ok := v.(float64); ok {
				seconds = int(f)
			}
		}
		return c.SetInstallMode(ctx, on, seconds)
	})
	c.RegisterService("rename_device", func(ctx context.Context, params map[string]any, _ hmenum.CommandPriority) error {
		address, err := payload.ParamString(params, "address")
		if err != nil {
			return err
		}
		name, err := payload.ParamString(params, "name")
		if err != nil {
			return err
		}
		return c.RenameDevice(ctx, address, name)
	})
	c.RegisterService("accept_device_inbox", func(ctx context.Context, params map[string]any, _ hmenum.CommandPriority) error {
		address, err := payload.ParamString(params, "address")
		if err != nil {
			return err
		}
		return c.AcceptDeviceInbox(ctx, address)
	})
	c.RegisterService("create_backup", func(ctx context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		_, err := c.CreateBackup(ctx)
		return err
	})
}
