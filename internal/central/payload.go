// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Compile-time guarantee that *Unit satisfies the universal
// Source contract. ADR 0007 step 8 — top-level service.
var _ payload.Source = (*Unit)(nil)

// Info returns the typed identity payload of the central.
func (u *Unit) Info() payload.InfoPayload {
	if u == nil {
		return nil
	}
	si := u.SystemInformation()
	return &payload.CentralInfo{
		Name:             u.cfg.Name,
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
func (u *Unit) Config() payload.ConfigPayload {
	if u == nil {
		return nil
	}
	return &payload.CentralConfig{Name: u.cfg.Name}
}

// State returns the central's runtime status. The state-machine
// bucket and the registered-device count are the two metrics
// northbound adapters consume — health page, connectivity badges,
// REST `/info` endpoint.
func (u *Unit) State() payload.StatePayload {
	if u == nil {
		return nil
	}
	out := &payload.CentralState{}
	if u.StateMachine != nil {
		out.State = string(u.StateMachine.State())
	}
	if u.DeviceRegistry != nil {
		out.DeviceCount = u.DeviceRegistry.Len()
	}
	return out
}

// registerCentralServices wires the externally invocable operations
// onto the embedded ServiceRegistry. Called once from [New] after the
// Unit is fully constructed.
func (u *Unit) registerCentralServices() {
	u.RegisterService("rename_device", func(ctx context.Context, params map[string]any, _ hmenum.CommandPriority) error {
		address, err := payload.ParamString(params, "address")
		if err != nil {
			return err
		}
		name, err := payload.ParamString(params, "name")
		if err != nil {
			return err
		}
		return u.RenameDevice(ctx, address, name)
	})
	u.RegisterService("accept_device_inbox", func(ctx context.Context, params map[string]any, _ hmenum.CommandPriority) error {
		address, err := payload.ParamString(params, "address")
		if err != nil {
			return err
		}
		return u.AcceptDeviceInbox(ctx, address)
	})
	u.RegisterService("create_backup", func(ctx context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		_, err := u.CreateBackup(ctx)
		return err
	})
}
