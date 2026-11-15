// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The device half of the deferred MQTT retraction across a model reload.
//
// A restart of a central and the cache-reset re-pull remove every device from
// the model and load it again. Retracting each device's configs as it leaves
// deletes its entities in Home Assistant, and the rediscovery that follows
// creates new registry entries: a renamed entity id falls back to its default
// and a helper built on the entity is deleted. So while the central's model
// generation is incomplete ([central.Unit.ModelGeneration]) a removal holds
// the device instead: its discovery configs are held on the bridge, its raw
// topics stay, and its availability reads offline until the model is back.
//
// The first snapshot of the completed generation reconciles the held set:
//   - a device the snapshot published again retracts only the configs it no
//     longer declares;
//   - a device still in the model but not published (not released by the
//     onboarding wizard) keeps its configs;
//   - a device that is gone is retracted as a removal always was — when its
//     interface has no client on the reloaded central (dropped, or failed to
//     wire) or loaded devices in the new generation. An interface with a
//     client that loaded nothing failed its ingest; its devices stay held,
//     and the next boot's orphan sweep is the reconciler for them.
//
// With the CCU unreachable the generation never completes, nothing is
// reconciled and nothing is retracted. A removal of the central, or a restart
// whose adopt failed, releases the hold: everything held is retracted.

// deviceHold is one held device.
type deviceHold struct {
	interfaceID string
	// touched is set once a snapshot published the device again, and
	// touchedGen is the model generation it was published in: a touch from
	// a generation a later teardown replaced proves nothing about the next.
	touched    bool
	touchedGen uint64
}

// centralModelHold is the held device set of one central, keyed by device
// address. It is keyed by central name, not by unit, because a restart
// replaces the unit and the hold has to outlive the old one.
type centralModelHold struct {
	devices map[string]*deviceHold
}

// holdState is the EventBridge's hold bookkeeping, guarded by its own mutex
// so no handler holds a lifecycle lock while reading it.
type holdState struct {
	holds map[string]*centralModelHold
	// released names the centrals whose hold was released by a removal: their
	// removals retract at once, whatever the unit's generation says.
	released map[string]bool
}

// holdRemovedDevice defers the retraction of a device removed during a model
// reload and reports whether it did; a central whose hold was released is
// retracted at once.
func (b *EventBridge) holdRemovedDevice(ctx context.Context, bridge *mqtt.Bridge, e hmevent.DeviceRemovedEvent) bool {
	b.holdMu.Lock()
	if b.hold.released[e.CentralName] {
		b.holdMu.Unlock()
		return false
	}
	if b.hold.holds == nil {
		b.hold.holds = make(map[string]*centralModelHold)
	}
	ch := b.hold.holds[e.CentralName]
	if ch == nil {
		ch = &centralModelHold{devices: make(map[string]*deviceHold)}
		b.hold.holds[e.CentralName] = ch
	}
	if _, already := ch.devices[e.Address]; !already {
		ch.devices[e.Address] = &deviceHold{interfaceID: e.InterfaceID}
	}
	b.holdMu.Unlock()

	bridge.HoldDiscoveryForCentralDevice(e.CentralName, e.Address)
	// The entities stay declared, but nothing serves them until the model
	// is back. The snapshot that publishes the device again flips the topic
	// back through the same transition gate. Only a device whose
	// availability was published is written: one the onboarding wizard never
	// released has no topic, and a hold must not create one.
	if _, published := b.availabilityCache.Load(availabilityCacheKey(e.CentralName, e.InterfaceID, e.Address)); published {
		b.markAvailability(ctx, e.CentralName, e.InterfaceID, e.Address, false)
	}
	return true
}

// touchHeldDevice records that a snapshot published a held device again, in
// the central's current model generation. Only the device object the model
// holds now counts: a snapshot pass walking a list taken before a removal
// publishes a device the model no longer has, and that proves nothing.
func (b *EventBridge) touchHeldDevice(centralName string, d *device.Device) {
	if b.registry == nil {
		return
	}
	u, ok := b.registry.Get(centralName)
	if !ok || u == nil || u.ModelRegistry == nil {
		return
	}
	if cur, ok := u.ModelRegistry.Get(d.Address); !ok || cur != d {
		return
	}
	gen, _ := u.ModelGeneration()
	b.holdMu.Lock()
	defer b.holdMu.Unlock()
	if ch := b.hold.holds[centralName]; ch != nil {
		if h := ch.devices[d.Address]; h != nil {
			h.touched, h.touchedGen = true, gen
		}
	}
}

// reconcileModelHold settles the held devices of u's central once a snapshot
// of the completed generation gen has been published. A teardown begun since
// the snapshot read gen leaves the hold for the next generation's snapshot.
func (b *EventBridge) reconcileModelHold(ctx context.Context, u *central.Unit, gen uint64) {
	centralName := u.Name()
	b.holdMu.Lock()
	ch := b.hold.holds[centralName]
	if ch == nil {
		b.holdMu.Unlock()
		return
	}
	if now, complete := u.ModelGeneration(); !complete || now != gen {
		b.holdMu.Unlock()
		return
	}
	held := ch.devices
	ch.devices = make(map[string]*deviceHold)
	b.holdMu.Unlock()

	bridge := b.bridgeOrNil()
	kept := map[string]*deviceHold{}
	loaded, configured := interfaceLoadState(u)
	for addr, d := range held {
		group := mqtt.DeviceHoldGroup(addr)
		switch {
		case d.touched && d.touchedGen == gen:
			if bridge != nil {
				bridge.RetractHeldDiscovery(ctx, centralName, group)
			}
		case u.ModelRegistry != nil && modelHas(u, addr):
			if bridge != nil {
				bridge.DropHeldDiscovery(centralName, group)
			}
		default:
			iface := BareInterfaceFromWireID(centralName, d.interfaceID)
			if configured[iface] && !loaded[iface] {
				kept[addr] = d
				continue
			}
			if bridge != nil {
				bridge.DropHeldDiscovery(centralName, group)
				b.retractRemovedDevice(ctx, bridge, centralName, d.interfaceID, addr)
			} else {
				b.forgetAvailability(centralName, d.interfaceID, addr)
			}
		}
	}

	b.holdMu.Lock()
	defer b.holdMu.Unlock()
	if len(kept) > 0 {
		for addr, d := range kept {
			if _, again := ch.devices[addr]; !again {
				ch.devices[addr] = d
			}
		}
		slog.Default().Warn("mqtt.model_hold.kept",
			slog.String("central", centralName),
			slog.Int("devices", len(kept)),
			slog.String("detail", "devices of an interface that loaded nothing stay declared; the next boot's orphan sweep settles them"))
	}
	if len(ch.devices) == 0 && b.hold.holds[centralName] == ch {
		delete(b.hold.holds, centralName)
	}
}

// ReleaseModelHold ends the central's hold for good: the central is being
// removed, or its restart failed to adopt it again. It takes effect for every
// removal handled from now on, and everything held is retracted on the
// fan-out worker behind the removals already queued.
func (b *EventBridge) ReleaseModelHold(centralName string) {
	if b == nil {
		return
	}
	b.holdMu.Lock()
	if b.hold.released == nil {
		b.hold.released = make(map[string]bool)
	}
	b.hold.released[centralName] = true
	b.holdMu.Unlock()
	b.enqueueDurable(func(ctx context.Context) {
		b.holdMu.Lock()
		ch := b.hold.holds[centralName]
		delete(b.hold.holds, centralName)
		b.holdMu.Unlock()
		if ch == nil {
			return
		}
		bridge := b.bridgeOrNil()
		for addr, d := range ch.devices {
			if bridge == nil {
				b.forgetAvailability(centralName, d.interfaceID, addr)
				continue
			}
			bridge.DropHeldDiscovery(centralName, mqtt.DeviceHoldGroup(addr))
			b.retractRemovedDevice(ctx, bridge, centralName, d.interfaceID, addr)
		}
	})
}

// clearModelHoldRelease lets a central adopted under a released name defer
// its removals again.
func (b *EventBridge) clearModelHoldRelease(centralName string) {
	b.holdMu.Lock()
	delete(b.hold.released, centralName)
	b.holdMu.Unlock()
}

// bridgeOrNil returns the MQTT bridge, or nil when no broker is wired.
func (b *EventBridge) bridgeOrNil() *mqtt.Bridge {
	if b.mqtt == nil {
		return nil
	}
	return b.mqtt.Bridge()
}

// modelHas reports whether the device is in u's model.
func modelHas(u *central.Unit, address string) bool {
	_, ok := u.ModelRegistry.Get(address)
	return ok
}

// interfaceLoadState reports per interface whether the central has a client
// for it and whether it carries at least one device in the model — the
// loaded half is the rule countLoadedInterfaces applies for the readiness
// tally. "Configured" is read from the client registry because the bridge
// holds no configuration: an interface whose client failed to wire counts as
// dropped, and its held devices are retracted as for a dropped interface.
func interfaceLoadState(u *central.Unit) (loaded, configured map[hmenum.Interface]bool) {
	loaded = map[hmenum.Interface]bool{}
	configured = map[hmenum.Interface]bool{}
	if u.Clients == nil {
		return loaded, configured
	}
	for _, entry := range u.Clients.List() {
		if entry == nil {
			continue
		}
		iface := entry.Interface
		if iface == "" {
			iface = BareInterfaceFromWireID(u.Name(), entry.InterfaceID)
		}
		if iface == "" {
			continue
		}
		configured[iface] = true
		if u.DeviceRegistry != nil {
			wireID := hmtypes.ParseWireInterfaceID(WireInterfaceID(u.Name(), iface))
			loaded[iface] = len(u.DeviceRegistry.Addresses(wireID)) > 0
		}
	}
	return loaded, configured
}
