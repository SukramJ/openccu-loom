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

// State returns the central's runtime status: the state-machine bucket
// and the registered-device count.
//
// It exists to satisfy the [payload.Source] contract ADR 0007 makes
// mandatory for the central, not because an adapter reads it — the
// health page, the connectivity badges and REST `/info` each build
// their own projection from the registry and the health tracker. The
// same is true of [Unit.Info] and [Unit.Config].
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

// registerCentralServices wires the central's service methods onto the
// embedded ServiceRegistry. Called once from [New] after the Unit is
// fully constructed.
//
// Nothing dispatches them yet, and that is worth stating rather than
// leaving to be discovered. The MQTT command plane resolves a channel's
// custom data point and invokes that ([adapter.MQTTCommandSink].
// InvokeChannelService); REST and WebSocket do the same. No north-bound
// adapter resolves a *central* as a [payload.Source], so
// [Unit.ServiceMethodNames] and [Unit.Invoke] are reachable only from a
// caller that does not exist. Both operations below are reachable
// through their own live paths — RenameDevice from the device-admin
// domain, CreateBackup from the backup adapter — so what is unused is
// this dispatch surface, not the features.
//
// Should a central-level dispatch path land, ADR 0007 names the set it
// expects here: `restart`, `reload_devices`,
// `start_service_messages_check`. `accept_device_inbox` was registered
// here once and is gone: the hook behind it was never wired by any
// production caller, so the method returned "not wired" on the only
// path that could have reached it, while the live accept runs through
// DeviceAdminDomain.AcceptInboxDevice (REST POST /devices/{addr}/accept
// and the WebSocket sibling).
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
	u.RegisterService("create_backup", func(ctx context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		_, err := u.CreateBackup(ctx)
		return err
	})
}
