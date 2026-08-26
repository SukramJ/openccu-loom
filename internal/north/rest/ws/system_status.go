// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/wiring"
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
	reg *central.Registry
	hub *Hub
	// remove detaches the registry observer Start installed, together with
	// every per-central subscription it attached. It needs no lock because
	// exactly one goroutine ever touches it: the composition root sets it in
	// Start before any server is listening and runs it in Stop after every
	// server has stopped.
	remove func()
}

// NewSystemStatusSubscriber returns a subscriber bound to reg and hub.
func NewSystemStatusSubscriber(reg *central.Registry, hub *Hub) *SystemStatusSubscriber {
	return &SystemStatusSubscriber{reg: reg, hub: hub}
}

// Start subscribes to every central the registry holds now and to every one
// registered later. Safe to call from the daemon composition root after the
// bus is ready.
func (s *SystemStatusSubscriber) Start() {
	if s.reg == nil || s.hub == nil {
		return
	}
	s.remove = s.reg.OnRegisterDeclared(wiring.Seam{
		Name:         "ws.system_status",
		Collaborator: "*ws.SystemStatusSubscriber",
		Phase:        wiring.PhasePerCentral,
		Why:          "system-status changes are never broadcast, so a connected client keeps the status it had when it subscribed",
	}, s.StartCentral)
}

// StartCentral attaches this subscriber to a single central's event bus and
// returns the unwire (nil when there was nothing to attach). It is the
// observer the registry runs per central.
//
// Start used to walk the registry as it stood at boot, so a central adopted at
// runtime emitted no `system.<central>.status` frame at all: its interface
// up/down transitions never reached a WebSocket client until the daemon was
// restarted.
func (s *SystemStatusSubscriber) StartCentral(u *central.Unit) func() {
	if s == nil || s.hub == nil || u == nil {
		return nil
	}
	bus := u.EventBus
	if bus == nil {
		return nil
	}
	centralName := u.Name()
	hub := s.hub
	return events.Subscribe(bus, func(e hmevent.SystemStatusChangedEvent) {
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
}

// Stop removes the registry observer and drops every subscription it
// attached — including the ones for centrals adopted after Start.
func (s *SystemStatusSubscriber) Stop() {
	if s.remove != nil {
		s.remove()
		s.remove = nil
	}
}
