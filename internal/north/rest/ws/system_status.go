// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"sync"
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
	reg *central.Registry
	hub *Hub

	// mu guards unsubs against a Start/Stop overlap. StartCentral does not
	// touch the slice at all - it hands its unwire back to the caller, so
	// the runtime-adopt path owns its own detach.
	mu     sync.Mutex
	unsubs []func()
}

// NewSystemStatusSubscriber returns a subscriber bound to reg and hub.
func NewSystemStatusSubscriber(reg *central.Registry, hub *Hub) *SystemStatusSubscriber {
	return &SystemStatusSubscriber{reg: reg, hub: hub}
}

// Start attaches subscriptions to every central registered right now. A
// central adopted later needs [SystemStatusSubscriber.StartCentral] — this
// walk cannot see it.
func (s *SystemStatusSubscriber) Start() {
	if s.reg == nil {
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
// the live-adopt hook chain, so a runtime-adopted CCU's status changes reach
// WebSocket clients like a boot-time one's.
func (s *SystemStatusSubscriber) StartCentral(u *central.Unit) (unwire func()) {
	if s == nil || s.hub == nil || u == nil || u.EventBus == nil {
		return nil
	}
	centralName := u.Name()
	hub := s.hub
	unsub := events.Subscribe(u.EventBus, func(e hmevent.SystemStatusChangedEvent) {
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
	return unsub
}

// Stop drops all event-bus subscriptions.
func (s *SystemStatusSubscriber) Stop() {
	s.mu.Lock()
	unsubs := s.unsubs
	s.unsubs = nil
	s.mu.Unlock()
	for _, u := range unsubs {
		u()
	}
}
