// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/internal/wiring"
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
	// remove detaches the registry observer Start installed, together with
	// every per-central subscription it attached — see the field on
	// [SystemStatusSubscriber] for the full ownership rule.
	remove func()
}

// NewOptimisticRollbackSubscriber returns a subscriber bound to reg and hub.
func NewOptimisticRollbackSubscriber(reg *central.Registry, hub *Hub) *OptimisticRollbackSubscriber {
	return &OptimisticRollbackSubscriber{reg: reg, hub: hub}
}

// Start subscribes to every central the registry holds now and to every one
// registered later.
func (s *OptimisticRollbackSubscriber) Start() {
	if s.reg == nil || s.hub == nil {
		return
	}
	s.remove = s.reg.OnRegisterDeclared(wiring.Seam{
		Name:         "ws.optimistic_rollback",
		Collaborator: "*ws.OptimisticRollbackSubscriber",
		Phase:        wiring.PhasePerCentral,
		Why:          "a failed write is never rolled back on the client, so the SPA keeps showing the value the operator asked for rather than the one the device holds",
	}, s.StartCentral)
}

// StartCentral attaches this subscriber to a single central's event bus and
// returns the unwire (nil when there was nothing to attach). It is the
// observer the registry runs per central.
//
// Start used to walk the registry as it stood at boot, so a write that
// never landed on a central adopted at runtime rolled back silently: no
// `datapoint.optimistic_rolled_back` frame reached any client until a restart.
func (s *OptimisticRollbackSubscriber) StartCentral(u *central.Unit) func() {
	if s == nil || s.reg == nil || s.hub == nil || u == nil {
		return nil
	}
	bus := u.EventBus
	if bus == nil {
		return nil
	}
	centralName := u.Name()
	hub := s.hub
	reg := s.reg
	return events.Subscribe(bus, func(e hmevent.DataPointOptimisticRolledBackEvent) {
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

// Stop removes the registry observer and drops every subscription it
// attached — including the ones for centrals adopted after Start.
func (s *OptimisticRollbackSubscriber) Stop() {
	if s.remove != nil {
		s.remove()
		s.remove = nil
	}
}
