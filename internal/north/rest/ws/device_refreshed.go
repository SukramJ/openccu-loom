// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/wiring"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// DeviceRefreshedPayload is the WebSocket payload published on the
// `device.<addr>.lifecycle` topic when a device's VALUES data was observed
// from the CCU — a pushed event or an active read, changed or not.
// Mirrors [hmevent.DeviceRefreshedEvent]. The daemon throttles it to one
// frame per device per short window, so `refreshed_at` may trail the
// device's exact latest observation by up to that window; the REST
// device summary's `availability.LastRefreshed` carries the exact value.
type DeviceRefreshedPayload struct {
	Central       string `json:"central"`
	InterfaceID   string `json:"interface_id"`
	DeviceAddress string `json:"device_address"`
	// RefreshedAt is the observation time in RFC 3339 with nanoseconds,
	// UTC — the same wire form as the REST `LastRefreshed` member.
	RefreshedAt string `json:"refreshed_at"`
}

// DeviceRefreshedSubscriber bridges per-central [hmevent.DeviceRefreshedEvent]
// from the domain bus to the WebSocket [*Hub]. Mirrors
// [DeviceLifecycleSubscriber] in shape and rides the same per-device
// lifecycle topic, so a client already watching a device's lifecycle sees
// its refresh ticks on the subscription it holds; the envelope `type`
// disambiguates.
type DeviceRefreshedSubscriber struct {
	reg *central.Registry
	hub *Hub
	// remove detaches the registry observer Start installed, together with
	// every per-central subscription it attached — see the field on
	// [SystemStatusSubscriber] for the full ownership rule.
	remove func()
}

// NewDeviceRefreshedSubscriber returns a subscriber bound to reg and hub.
func NewDeviceRefreshedSubscriber(reg *central.Registry, hub *Hub) *DeviceRefreshedSubscriber {
	return &DeviceRefreshedSubscriber{reg: reg, hub: hub}
}

// Start subscribes to every central the registry holds now and to every one
// registered later.
func (s *DeviceRefreshedSubscriber) Start() {
	if s.reg == nil || s.hub == nil {
		return
	}
	s.remove = s.reg.OnRegisterDeclared(wiring.Seam{
		Name:         "ws.device_refreshed",
		Collaborator: "*ws.DeviceRefreshedSubscriber",
		Phase:        wiring.PhasePerCentral,
		Why:          "the device list's 'last refreshed' time never moves while the page is open: a periodic sweep whose readings did not change publishes no value-changed frame, so this is the only push that carries it",
	}, s.StartCentral)
}

// StartCentral attaches this subscriber to a single central's event bus and
// returns the unwire (nil when there was nothing to attach). It is the
// observer the registry runs per central.
func (s *DeviceRefreshedSubscriber) StartCentral(u *central.Unit) func() {
	if s == nil || s.hub == nil || u == nil {
		return nil
	}
	bus := u.EventBus
	if bus == nil {
		return nil
	}
	hub := s.hub
	return bus.Subscribe(func(e hmevent.DeviceRefreshedEvent) {
		hub.Publish(Event{
			Topic: DeviceLifecycleTopic(e.DeviceAddress),
			Type:  string(hmevent.EventTypeDeviceRefreshed),
			When:  e.Timestamp(),
			Payload: DeviceRefreshedPayload{
				Central:       e.CentralName,
				InterfaceID:   e.InterfaceID,
				DeviceAddress: e.DeviceAddress,
				RefreshedAt:   e.RefreshedAt.UTC().Format(time.RFC3339Nano),
			},
		})
	})
}

// Stop removes the registry observer and drops every subscription it
// attached — including the ones for centrals adopted after Start.
func (s *DeviceRefreshedSubscriber) Stop() {
	if s.remove != nil {
		s.remove()
		s.remove = nil
	}
}
