// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
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

// PublishAwaitingRelease mirrors the devices that are materialised but
// still withheld from the ecosystems onto the hub's inbox aggregate.
//
// They belong on that surface for the same reason the deferred queue
// does: both mean "this is waiting for you". They carry a different flag
// because the ask is different — a parked device needs a decision about
// whether it exists at all, this one needs its last configuration step
// and a release.
func PublishAwaitingRelease(u *central.Unit) {
	if u == nil || u.HubModel == nil || u.Devices == nil || u.ModelRegistry == nil {
		return
	}
	var out []hub.InboxDevice
	seen := map[string]struct{}{}
	for _, d := range u.ModelRegistry.List() {
		if d == nil {
			continue
		}
		if u.Devices.IsReleased(hmtypes.ParseWireInterfaceID(d.InterfaceID), d.Address) {
			continue
		}
		if _, dup := seen[d.Address]; dup {
			continue
		}
		seen[d.Address] = struct{}{}
		out = append(out, hub.InboxDevice{
			Address:         d.Address,
			Name:            d.Name(),
			Model:           d.Model,
			Interface:       string(BareInterfaceFromWireID(u.Name(), d.InterfaceID)),
			AwaitingRelease: true,
		})
	}
	u.HubModel.Inbox.SetAwaitingRelease(out)
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
	descs := u.Devices.TakeDelayedDeviceDescriptions(ctx, iface, address)
	if len(descs) == 0 {
		return false, nil
	}
	if err := u.IngestDevices(ctx, string(iface), descs); err != nil {
		u.Devices.StoreDelayedDeviceDescriptions(ctx, iface, descs)
		PublishPendingDevices(u)
		return true, fmt.Errorf("accept pending device %s: %w", address, err)
	}
	// Registry bookkeeping and the DeviceCreatedEvent run after the
	// materialisation so a north-bound subscriber resolves the device in
	// the model when the event fires — the same order the hot-plug path
	// keeps.
	u.Devices.HandleAcceptedDevices(iface, descs)
	PublishPendingDevices(u)
	// The accept moved it from "decide whether this exists" to "configure
	// it and publish it". Without this the device leaves the inbox
	// surface entirely and the operator has no way back to the last step.
	PublishAwaitingRelease(u)
	return true, nil
}

// ReleaseDevice ends the onboarding hold on a materialised device and
// reports whether it was held. This is the wizard's last step: up to
// here the device exists, is configurable and is visible in this
// daemon's own surfaces, but MQTT, Matter and the outbound webhook have
// been withholding it.
//
// The event is published only for a device that was actually held, so a
// double-click does not make three ecosystems re-publish a device that
// was never withheld.
func ReleaseDevice(ctx context.Context, u *central.Unit, address string) bool {
	if u == nil || u.Devices == nil || address == "" {
		return false
	}
	d, ok := u.ModelRegistry.Get(address)
	if !ok || d == nil {
		return false
	}
	iface := hmtypes.ParseWireInterfaceID(d.InterfaceID)
	if !u.Devices.ReleaseDevice(ctx, iface, address) {
		return false
	}
	events.Publish(u.EventBus, hmevent.DeviceReleasedEvent{
		Base:        hmevent.NewBase(),
		CentralName: u.Name(),
		InterfaceID: d.InterfaceID,
		Address:     address,
	})
	PublishAwaitingRelease(u)
	return true
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
