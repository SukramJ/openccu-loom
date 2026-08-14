// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"sync"

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
	reg *central.Registry
	hub *Hub

	// mu guards unsubs against a Start/Stop overlap. StartCentral does not
	// touch the slice at all - it hands its unwire back to the caller, so
	// the runtime-adopt path owns its own detach.
	mu     sync.Mutex
	unsubs []func()
}

// NewDeviceLifecycleSubscriber returns a subscriber bound to reg and hub.
func NewDeviceLifecycleSubscriber(reg *central.Registry, hub *Hub) *DeviceLifecycleSubscriber {
	return &DeviceLifecycleSubscriber{reg: reg, hub: hub}
}

// Start attaches subscriptions to every central registered right now. A
// central adopted later needs [DeviceLifecycleSubscriber.StartCentral] —
// this walk cannot see it.
func (s *DeviceLifecycleSubscriber) Start() {
	if s.reg == nil || s.hub == nil {
		return
	}
	for _, u := range s.reg.List() {
		if unwire := s.StartCentral(u); unwire != nil {
			s.mu.Lock()
			s.unsubs = append(s.unsubs, unwire)
			s.mu.Unlock()
		}
	}
}

// StartCentral subscribes exactly one central and returns its unwire, or nil
// when there is nothing to attach. The composition root routes this through
// the live-adopt hook chain, so a runtime-adopted CCU's device create/remove
// frames reach WebSocket clients like a boot-time one's.
func (s *DeviceLifecycleSubscriber) StartCentral(u *central.Unit) (unwire func()) {
	if s == nil || s.reg == nil || s.hub == nil || u == nil || u.EventBus == nil {
		return nil
	}
	centralName := u.Name()
	hub := s.hub
	unsubCreated := events.Subscribe(u.EventBus, func(e hmevent.DeviceCreatedEvent) {
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
	unsubRemoved := events.Subscribe(u.EventBus, func(e hmevent.DeviceRemovedEvent) {
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
	return unwireAll([]func(){unsubCreated, unsubRemoved})
}

// Stop drops all event-bus subscriptions.
func (s *DeviceLifecycleSubscriber) Stop() {
	s.mu.Lock()
	unsubs := s.unsubs
	s.unsubs = nil
	s.mu.Unlock()
	for _, u := range unsubs {
		u()
	}
}
