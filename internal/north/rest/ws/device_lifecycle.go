// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/wiring"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// DeviceCreatedPayload is the WebSocket payload published on the
// `device.<addr>.lifecycle` topic when the registry observes a new
// device. Mirrors [hmevent.DeviceCreatedEvent].
type DeviceCreatedPayload struct {
	Central       string                        `json:"central"`
	InterfaceID   string                        `json:"interface_id"`
	DeviceAddress string                        `json:"device_address"`
	Model         string                        `json:"model"`
	Source        hmenum.SourceOfDeviceCreation `json:"source,omitempty"`
}

// DeviceRemovedPayload is the WebSocket payload published on the
// `device.<addr>.lifecycle` topic when the CCU reports a device
// deletion. Mirrors [hmevent.DeviceRemovedEvent].
type DeviceRemovedPayload struct {
	Central       string `json:"central"`
	InterfaceID   string `json:"interface_id"`
	DeviceAddress string `json:"device_address"`
}

// DeviceAvailabilityChangedPayload is the WebSocket payload published
// on the `device.<addr>.lifecycle` topic when a device's effective
// reachability flips. Mirrors the
// [hmenum.DeviceLifecycleSubtypeAvailabilityChanged] variant of
// [hmevent.DeviceLifecycleEvent].
type DeviceAvailabilityChangedPayload struct {
	Central       string `json:"central"`
	InterfaceID   string `json:"interface_id"`
	DeviceAddress string `json:"device_address"`
	Available     bool   `json:"available"`
}

// broadcastDeviceAvailabilityChanged is the envelope `type` of the
// availability frame. It has no [hmevent.EventType] of its own: the
// domain carries every device lifecycle transition on one event with a
// sub-type discriminator, while WebSocket consumers route on `type`
// alone and must be able to tell an availability flip from a creation.
const broadcastDeviceAvailabilityChanged = "device.availability_changed"

// DeviceLifecycleTopic returns the canonical topic for the create,
// remove and availability events on a single device. The envelope
// `type` field disambiguates (`device.created` vs `device.removed` vs
// `device.availability_changed`) so the subscriber sees every kind on
// one subscription pattern (`device.<addr>.lifecycle`).
func DeviceLifecycleTopic(deviceAddr string) string {
	return "device." + deviceAddr + ".lifecycle"
}

// DeviceLifecycleSubscriber bridges per-central
// [hmevent.DeviceCreatedEvent], [hmevent.DeviceRemovedEvent] and the
// availability sub-type of [hmevent.DeviceLifecycleEvent] from the
// domain bus to the WebSocket [*Hub]. Mirrors
// [HubEventsSubscriber] in shape; runs alongside it so each event
// family gets a focused subscriber and failure of one path cannot
// starve the other.
type DeviceLifecycleSubscriber struct {
	reg *central.Registry
	hub *Hub
	// remove detaches the registry observer Start installed, together with
	// every per-central subscription it attached — see the field on
	// [SystemStatusSubscriber] for the full ownership rule.
	remove func()
}

// NewDeviceLifecycleSubscriber returns a subscriber bound to reg and hub.
func NewDeviceLifecycleSubscriber(reg *central.Registry, hub *Hub) *DeviceLifecycleSubscriber {
	return &DeviceLifecycleSubscriber{reg: reg, hub: hub}
}

// Start subscribes to every central the registry holds now and to every one
// registered later.
func (s *DeviceLifecycleSubscriber) Start() {
	if s.reg == nil || s.hub == nil {
		return
	}
	s.remove = s.reg.OnRegisterDeclared(wiring.Seam{
		Name:         "ws.device_lifecycle",
		Collaborator: "*ws.DeviceLifecycleSubscriber",
		Phase:        wiring.PhasePerCentral,
		Why:          "no device add, removal or availability change reaches a WebSocket client, so an open SPA never learns a device appeared or went offline",
	}, s.StartCentral)
}

// StartCentral attaches this subscriber to a single central's event bus and
// returns the unwire (nil when there was nothing to attach). It is the
// observer the registry runs per central.
//
// Start used to walk the registry as it stood at boot, so a central
// adopted at runtime emitted no `device.created` / `device.removed` frame for
// any of its devices until the daemon was restarted.
func (s *DeviceLifecycleSubscriber) StartCentral(u *central.Unit) func() {
	if s == nil || s.hub == nil || u == nil {
		return nil
	}
	bus := u.EventBus
	if bus == nil {
		return nil
	}
	centralName := u.Name()
	hub := s.hub
	unsubCreated := events.Subscribe(bus, func(e hmevent.DeviceCreatedEvent) {
		hub.Publish(Event{
			Topic: DeviceLifecycleTopic(e.Address),
			Type:  string(hmevent.EventTypeDeviceCreated),
			When:  e.Timestamp(),
			Payload: DeviceCreatedPayload{
				Central:       centralName,
				InterfaceID:   e.InterfaceID,
				DeviceAddress: e.Address,
				Model:         e.Model,
				Source:        e.Source,
			},
		})
	})
	unsubRemoved := events.Subscribe(bus, func(e hmevent.DeviceRemovedEvent) {
		hub.Publish(Event{
			Topic: DeviceLifecycleTopic(e.Address),
			Type:  string(hmevent.EventTypeDeviceRemoved),
			When:  e.Timestamp(),
			Payload: DeviceRemovedPayload{
				Central:       centralName,
				InterfaceID:   e.InterfaceID,
				DeviceAddress: e.Address,
			},
		})
	})
	// Availability rides the same topic as create/remove. The domain
	// publishes every lifecycle transition on one event; only the
	// availability sub-type needs a north-bound frame here, because the
	// creation and deletion sub-types have their own dedicated events
	// (subscribed above) and relaying them would double each frame.
	unsubAvailability := events.Subscribe(bus, func(e hmevent.DeviceLifecycleEvent) {
		if e.Subtype != hmenum.DeviceLifecycleSubtypeAvailabilityChanged {
			return
		}
		hub.Publish(Event{
			Topic: DeviceLifecycleTopic(e.Address),
			Type:  broadcastDeviceAvailabilityChanged,
			When:  e.Timestamp(),
			Payload: DeviceAvailabilityChangedPayload{
				Central:       centralName,
				InterfaceID:   e.InterfaceID,
				DeviceAddress: e.Address,
				Available:     e.Available,
			},
		})
	})
	return unwireAll([]func(){unsubCreated, unsubRemoved, unsubAvailability})
}

// Stop removes the registry observer and drops every subscription it
// attached — including the ones for centrals adopted after Start.
func (s *DeviceLifecycleSubscriber) Stop() {
	if s.remove != nil {
		s.remove()
		s.remove = nil
	}
}
