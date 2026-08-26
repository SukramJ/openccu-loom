// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// PublishPendingDevices mirrors the central's deferred-creation queue —
// the devices announced over newDevices while
// `delay_new_device_creation` is enabled — onto the hub's inbox
// aggregate. That aggregate is the operator's single "waiting for you"
// surface: REST `GET /inbox`, the WS `inbox.list` command and its
// `hub.<central>.inbox` broadcast, the MQTT inbox sensor and the SPA
// inbox view all read it. Without this call the queue is invisible and
// the deferred device exists on the CCU with no data points here.
//
// It is called after every change to the queue (a fresh announcement, an
// accept) and is a no-op on a central whose hub model is not wired.
func PublishPendingDevices(u *central.Unit) {
	if u == nil || u.Devices == nil || u.HubModel == nil || u.HubModel.Inbox == nil {
		return
	}
	pending := u.Devices.PendingDevices()
	out := make([]hub.InboxDevice, 0, len(pending))
	for _, p := range pending {
		out = append(out, hub.InboxDevice{
			Address:         p.Address,
			Model:           p.Model,
			Interface:       string(BareInterfaceFromWireID(u.Name(), string(p.Interface))),
			PendingCreation: true,
		})
	}
	u.HubModel.Inbox.SetPendingCreation(out)
}

// AcceptPendingDevice materialises a device an operator accepted out of
// the deferred-creation queue and reports whether the queue held it.
// The descriptions run through the same materialiser the immediate
// hot-plug path uses, so an accepted device arrives with its channels,
// data points and values instead of a bare registry entry.
//
// A materialisation failure puts the descriptions back so the operator
// can retry; the device stays listed as pending in that case.
func AcceptPendingDevice(ctx context.Context, u *central.Unit, address string) (bool, error) {
	if u == nil || u.Devices == nil || address == "" {
		return false, nil
	}
	iface, ok := pendingInterfaceOf(u, address)
	if !ok {
		return false, nil
	}
	descs := u.Devices.TakeDelayedDeviceDescriptions(iface, address)
	if len(descs) == 0 {
		return false, nil
	}
	if err := u.IngestDevices(ctx, string(iface), descs); err != nil {
		u.Devices.StoreDelayedDeviceDescriptions(iface, descs)
		PublishPendingDevices(u)
		return true, fmt.Errorf("accept pending device %s: %w", address, err)
	}
	// Registry bookkeeping and the DeviceCreatedEvent run after the
	// materialisation so a north-bound subscriber resolves the device in
	// the model when the event fires — the same order the hot-plug path
	// keeps.
	u.Devices.HandleAcceptedDevices(iface, descs)
	PublishPendingDevices(u)
	return true, nil
}

// pendingInterfaceOf resolves the interface a parked device was
// announced on. The queue is keyed per interface, and an operator
// accepting from the inbox only knows the address.
func pendingInterfaceOf(u *central.Unit, address string) (hmtypes.WireInterfaceID, bool) {
	for _, p := range u.Devices.PendingDevices() {
		if p.Address == address {
			return p.Interface, true
		}
	}
	return "", false
}
