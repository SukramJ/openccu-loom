// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// SystemStatusChangedPayload is the WebSocket payload published on the
// `system.<central>.status` topic whenever a
// [hmevent.SystemStatusChangedEvent] arrives.
//
// The payload mirrors the event fields verbatim so north-bound
// consumers (SPA, Node-RED, external monitors) do not need to know
// the internal hmevent catalogue layout.
type SystemStatusChangedPayload struct {
	Central            string              `json:"central"`
	Component          string              `json:"component"`
	Healthy            bool                `json:"healthy"`
	Reason             string              `json:"reason,omitempty"`
	InterfaceID        string              `json:"interface_id,omitempty"`
	ErrorCode          int                 `json:"error_code,omitempty"`
	CentralState       hmenum.CentralState `json:"central_state,omitempty"`
	ConnectionState    string              `json:"connection_state,omitempty"`
	ClientState        hmenum.ClientState  `json:"client_state,omitempty"`
	CallbackState      string              `json:"callback_state,omitempty"`
	DegradedInterfaces []string            `json:"degraded_interfaces,omitempty"`
	Issues             []string            `json:"issues,omitempty"`
	EventAt            time.Time           `json:"event_at"`
}

// SystemStatusTopic returns the canonical topic for central-scoped
// system-status events:
//
//	system.<central>.status
func SystemStatusTopic(centralName string) string {
	return "system." + centralName + ".status"
}

// SystemStatusSubscriber attaches one subscription per registered
// central and fans [hmevent.SystemStatusChangedEvent] payloads out to
// every WebSocket client that subscribed to the matching topic.
//
// Start/Stop manage the subscription lifetime.
type SystemStatusSubscriber struct {
	reg    *central.Registry
	hub    *Hub
	unsubs []func()
}

// NewSystemStatusSubscriber returns a subscriber bound to reg and hub.
func NewSystemStatusSubscriber(reg *central.Registry, hub *Hub) *SystemStatusSubscriber {
	return &SystemStatusSubscriber{reg: reg, hub: hub}
}

// Start attaches subscriptions to every registered central's event bus.
// Safe to call from the daemon composition root after the bus is ready.
func (s *SystemStatusSubscriber) Start() {
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
		unsub := events.Subscribe(bus, func(e hmevent.SystemStatusChangedEvent) {
			hub.Publish(Event{
				Topic: SystemStatusTopic(centralName),
				Type:  string(hmevent.EventTypeSystemStatusChanged),
				When:  e.Timestamp(),
				Payload: SystemStatusChangedPayload{
					Central:            centralName,
					Component:          e.Component,
					Healthy:            e.Healthy,
					Reason:             e.Reason,
					InterfaceID:        e.InterfaceID,
					ErrorCode:          e.ErrorCode,
					CentralState:       e.CentralState,
					ConnectionState:    e.ConnectionState,
					ClientState:        e.ClientState,
					CallbackState:      e.CallbackState,
					DegradedInterfaces: e.DegradedInterfaces,
					Issues:             e.Issues,
					EventAt:            e.Timestamp(),
				},
			})
		})
		s.unsubs = append(s.unsubs, unsub)
	}
}

// Stop drops all event-bus subscriptions.
func (s *SystemStatusSubscriber) Stop() {
	for _, u := range s.unsubs {
		u()
	}
	s.unsubs = nil
}
