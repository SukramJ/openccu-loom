// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// OptimisticRollbackPayload is the WebSocket payload published when an
// optimistic write is rolled back (the CCU never confirmed it within the
// TTL, or rejected it). Mirrors [hmevent.DataPointOptimisticRolledBackEvent].
// It rides the same `device.<addr>.channels.<no>.data_points.<param>`
// topic as the value-changed stream — subscribers route by envelope
// `type` — so a client learns the DP reverted without synthesising the
// rollback from set_value failures itself.
type OptimisticRollbackPayload struct {
	Central       string `json:"central"`
	DeviceAddress string `json:"device_address"`
	Channel       int    `json:"channel"`
	Parameter     string `json:"parameter"`
	ParamsetKey   string `json:"paramset_key"`
	Reason        string `json:"reason"`
	// Sent is the optimistic value that was rolled back; Present is the
	// value the DP reverted to (the last CCU-confirmed value).
	Sent    any `json:"sent,omitempty"`
	Present any `json:"present,omitempty"`
}

// OptimisticRollbackSubscriber bridges per-central
// [hmevent.DataPointOptimisticRolledBackEvent] from the domain bus to the
// WebSocket [*Hub]. Mirrors [DeviceLifecycleSubscriber] in shape.
type OptimisticRollbackSubscriber struct {
	reg    *central.Registry
	hub    *Hub
	unsubs []func()
}

// NewOptimisticRollbackSubscriber returns a subscriber bound to reg and hub.
func NewOptimisticRollbackSubscriber(reg *central.Registry, hub *Hub) *OptimisticRollbackSubscriber {
	return &OptimisticRollbackSubscriber{reg: reg, hub: hub}
}

// Start attaches subscriptions to every registered central's event bus.
func (s *OptimisticRollbackSubscriber) Start() {
	if s.reg == nil || s.hub == nil {
		return
	}
	for _, u := range s.reg.List() {
		bus := u.EventBus
		if bus == nil {
			continue
		}
		centralName := u.Name()
		hub := s.hub
		unsub := events.Subscribe(bus, func(e hmevent.DataPointOptimisticRolledBackEvent) {
			channel, _ := e.Key.ChannelNo()
			hub.Publish(Event{
				Topic: DataPointTopic(e.Key.DeviceAddress(), channel, e.Key.Parameter),
				Type:  string(hmevent.EventTypeDataPointOptimisticRolled),
				When:  e.Timestamp(),
				Payload: OptimisticRollbackPayload{
					Central:       centralName,
					DeviceAddress: e.Key.DeviceAddress(),
					Channel:       channel,
					Parameter:     e.Key.Parameter,
					ParamsetKey:   string(e.Key.ParamsetKey),
					Reason:        string(e.Reason),
					Sent:          e.Sent.Unwrap(),
					Present:       e.Present.Unwrap(),
				},
			})
		})
		s.unsubs = append(s.unsubs, unsub)
	}
}

// Stop drops all event-bus subscriptions.
func (s *OptimisticRollbackSubscriber) Stop() {
	for _, u := range s.unsubs {
		u()
	}
	s.unsubs = nil
}
