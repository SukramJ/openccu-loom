// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
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

// DeviceLifecycleTopic returns the canonical topic for both create
// and remove events on a single device. The envelope `type` field
// disambiguates (`device.created` vs `device.removed`) so the
// subscriber sees both kinds on one subscription pattern
// (`device.<addr>.lifecycle`).
func DeviceLifecycleTopic(deviceAddr string) string {
	return "device." + deviceAddr + ".lifecycle"
}

// DeviceLifecycleSubscriber bridges per-central
// [hmevent.DeviceCreatedEvent] and [hmevent.DeviceRemovedEvent] from
// the domain bus to the WebSocket [*Hub]. Mirrors
// [HubEventsSubscriber] in shape; runs alongside it so each event
// family gets a focused subscriber and failure of one path cannot
// starve the other.
type DeviceLifecycleSubscriber struct {
	reg    *central.Registry
	hub    *Hub
	unsubs []func()
}

// NewDeviceLifecycleSubscriber returns a subscriber bound to reg and hub.
func NewDeviceLifecycleSubscriber(reg *central.Registry, hub *Hub) *DeviceLifecycleSubscriber {
	return &DeviceLifecycleSubscriber{reg: reg, hub: hub}
}

// Start attaches subscriptions to every registered central's event bus.
func (s *DeviceLifecycleSubscriber) Start() {
	if s.reg == nil || s.hub == nil {
		return
	}
	for _, c := range s.reg.List() {
		bus := c.EventBus
		if bus == nil {
			continue
		}
		centralName := c.Name()
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
		s.unsubs = append(s.unsubs, unsubCreated, unsubRemoved)
	}
}

// Stop drops all event-bus subscriptions.
func (s *DeviceLifecycleSubscriber) Stop() {
	for _, u := range s.unsubs {
		u()
	}
	s.unsubs = nil
}
