// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
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
	// UniqueID is the canonical loom-namespaced routing key for the data
	// point that rolled back; it matches the value-changed entity's key
	// (the rollback rides the same DP topic and routes to the same HA
	// entity). Always present and non-empty — it resolves from the (gated)
	// central serial plus the data-point key, both always carried by a
	// rollback event. See [DataPointValueChangedPayload.UniqueID].
	UniqueID string `json:"unique_id"`
	// Sent is the optimistic value that was rolled back; Present is the
	// value the DP reverted to (the last CCU-confirmed value).
	Sent    any `json:"sent,omitempty"`
	Present any `json:"present,omitempty"`
}

// OptimisticRollbackSubscriber bridges per-central
// [hmevent.DataPointOptimisticRolledBackEvent] from the domain bus to the
// WebSocket [*Hub]. Mirrors [DeviceLifecycleSubscriber] in shape.
type OptimisticRollbackSubscriber struct {
	reg *central.Registry
	hub *Hub

	// mu guards unsubs against a Start/Stop overlap. StartCentral does not
	// touch the slice at all - it hands its unwire back to the caller, so
	// the runtime-adopt path owns its own detach.
	mu     sync.Mutex
	unsubs []func()
}

// NewOptimisticRollbackSubscriber returns a subscriber bound to reg and hub.
func NewOptimisticRollbackSubscriber(reg *central.Registry, hub *Hub) *OptimisticRollbackSubscriber {
	return &OptimisticRollbackSubscriber{reg: reg, hub: hub}
}

// Start attaches subscriptions to every central registered right now. A
// central adopted later needs [OptimisticRollbackSubscriber.StartCentral] —
// this walk cannot see it.
func (s *OptimisticRollbackSubscriber) Start() {
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
// the live-adopt hook chain, so a runtime-adopted CCU's rollbacks reach
// WebSocket clients like a boot-time one's.
func (s *OptimisticRollbackSubscriber) StartCentral(u *central.Unit) (unwire func()) {
	if s == nil || s.reg == nil || s.hub == nil || u == nil || u.EventBus == nil {
		return nil
	}
	centralName := u.Name()
	hub := s.hub
	reg := s.reg
	return events.Subscribe(u.EventBus, func(e hmevent.DataPointOptimisticRolledBackEvent) {
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
				UniqueID: routingkey.CanonicalUniqueID(
					reg.SerialSuffix(centralName), e.Key.ChannelAddress, e.Key.Parameter, "",
				),
				Sent:    e.Sent.Unwrap(),
				Present: e.Present.Unwrap(),
			},
		})
	})
}

// Stop drops all event-bus subscriptions.
func (s *OptimisticRollbackSubscriber) Stop() {
	s.mu.Lock()
	unsubs := s.unsubs
	s.unsubs = nil
	s.mu.Unlock()
	for _, u := range unsubs {
		u()
	}
}
