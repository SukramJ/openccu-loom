// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// WireDeviceAvailability subscribes to [hmevent.ClientStateChangedEvent] and
// propagates the connection state of an InterfaceClient onto every device
// that lives on that interface.
//
// Without this wiring HmIP / BidCos / CUxD devices that the CCU has silently
// dropped continue to render as `online` in HA, REST and the SPA. The override
// is a hard third state (NotSet / ForceTrue / ForceFalse) that wins over the
// regular UNREACH / STICKY_UNREACH gating; clearing it on reconnect (NotSet)
// restores the wire-truth path.
//
// When the effective availability of a device changes, a
// [hmevent.DeviceLifecycleEvent] with subtype
// [hmenum.DeviceLifecycleSubtypeAvailabilityChanged] is published so
// north-bound adapters (MQTT, REST) can react without polling.
//
// Returns a closer that drops the subscription and clears the matching
// `MarkAllDevicesForced` flag on every InterfaceClient — safe to call during
// shutdown so a restart starts from a clean slate.
func WireDeviceAvailability(unit *central.CentralUnit) func() {
	if unit == nil || unit.EventBus == nil || unit.ModelRegistry == nil {
		return func() {}
	}
	bus := unit.EventBus
	centralName := unit.Name()

	apply := func(interfaceID string, mode hmenum.ForcedDeviceAvailability, clientMode client.ForcedAvailability) {
		// Mark every device on this interface and emit a lifecycle event when
		// the effective availability actually flipped.
		for _, d := range unit.ModelRegistry.List() {
			if d.InterfaceID != interfaceID {
				continue
			}
			changed := d.SetForcedAvailability(mode)
			if changed {
				events.Publish(bus, hmevent.DeviceLifecycleEvent{
					Base:        hmevent.NewBase(),
					CentralName: centralName,
					InterfaceID: interfaceID,
					Address:     d.Address,
					Subtype:     hmenum.DeviceLifecycleSubtypeAvailabilityChanged,
					Available:   d.AvailabilityInfo().IsReachable,
				})
			}
		}
		// Mirror the override on the InterfaceClient so coordinators
		// querying ForcedAvailabilityMode see the same state.
		if unit.Clients != nil {
			if entry, ok := unit.Clients.Get(interfaceID); ok && entry.Client != nil {
				entry.Client.MarkAllDevicesForced(clientMode)
			}
		}
	}

	unsub := events.Subscribe(bus, func(e hmevent.ClientStateChangedEvent) {
		switch e.To { //nolint:exhaustive // Created/Initializing/Initialized/Stopping are transient — no override needed
		case hmenum.ClientStateConnected:
			// Connection (re-)established: lift the forced override and
			// hand control back to each device's own availability tracker.
			apply(e.InterfaceID, hmenum.ForcedDeviceAvailabilityNotSet, client.ForcedAvailabilityNone)
		case hmenum.ClientStateDisconnected,
			hmenum.ClientStateReconnecting,
			hmenum.ClientStateFailed,
			hmenum.ClientStateStopped:
			// Connection lost: force every device unavailable so HA /
			// REST / SPA stop showing stale state. Reconnecting is
			// included because the interface cannot deliver fresh
			// values during the recovery window.
			apply(e.InterfaceID, hmenum.ForcedDeviceAvailabilityForceFalse, client.ForcedAvailabilityFalse)
		}
	})

	return unsub
}
