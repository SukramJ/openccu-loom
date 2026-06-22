// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// DeviceTriggerPayload is the WebSocket payload published on the
// `device.<addr>.channels.<no>.trigger` topic when the CCU reports a
// non-state device event (keypress, impulse, device error). Mirrors
// [hmevent.DeviceTriggerEvent]. Home Assistant consumes these via its
// device-trigger automation surface, distinct from value changes.
type DeviceTriggerPayload struct {
	Central       string `json:"central"`
	InterfaceID   string `json:"interface_id"`
	DeviceAddress string `json:"device_address"`
	Channel       int    `json:"channel"`
	EventType     string `json:"event_type"`
	Parameter     string `json:"parameter"`
	// UniqueID is the canonical loom-namespaced routing key for the data
	// point this trigger fires on; it matches the value-changed entity's
	// key (a trigger and a value change on the same DP route to the same
	// HA entity). Always present and non-empty — it resolves from the
	// (gated) central serial plus the channel address + parameter, both
	// always carried by a trigger event. See
	// [DataPointValueChangedPayload.UniqueID].
	UniqueID string `json:"unique_id"`
	Value    any    `json:"value,omitempty"`
}

// DeviceTriggerTopic returns the canonical topic for a device-trigger
// event — one topic per (device, channel). The envelope `type` and the
// payload's parameter / event_type disambiguate which trigger fired.
func DeviceTriggerTopic(deviceAddr string, channel int) string {
	return "device." + deviceAddr + ".channels." + strconv.Itoa(channel) + ".trigger"
}

// DeviceTriggerSubscriber bridges per-central [hmevent.DeviceTriggerEvent]
// from the domain bus to the WebSocket [*Hub]. Mirrors
// [DeviceLifecycleSubscriber] in shape; runs alongside it so each event
// family gets a focused subscriber.
type DeviceTriggerSubscriber struct {
	reg    *central.Registry
	hub    *Hub
	unsubs []func()
}

// NewDeviceTriggerSubscriber returns a subscriber bound to reg and hub.
func NewDeviceTriggerSubscriber(reg *central.Registry, hub *Hub) *DeviceTriggerSubscriber {
	return &DeviceTriggerSubscriber{reg: reg, hub: hub}
}

// Start attaches subscriptions to every registered central's event bus.
func (s *DeviceTriggerSubscriber) Start() {
	if s.reg == nil || s.hub == nil {
		return
	}
	for _, u := range s.reg.List() {
		bus := u.EventBus
		if bus == nil {
			continue
		}
		hub := s.hub
		reg := s.reg
		unsub := events.Subscribe(bus, func(e hmevent.DeviceTriggerEvent) {
			channelAddr := e.DeviceAddress + ":" + strconv.Itoa(e.ChannelNo)
			hub.Publish(Event{
				Topic: DeviceTriggerTopic(e.DeviceAddress, e.ChannelNo),
				Type:  string(hmevent.EventTypeDeviceTrigger),
				When:  e.Timestamp(),
				Payload: DeviceTriggerPayload{
					Central:       e.CentralName,
					InterfaceID:   e.InterfaceID,
					DeviceAddress: e.DeviceAddress,
					Channel:       e.ChannelNo,
					EventType:     string(e.EventType_),
					Parameter:     e.Parameter,
					UniqueID: routingkey.CanonicalUniqueID(
						reg.SerialSuffix(e.CentralName), channelAddr, e.Parameter, "",
					),
					Value: e.Value.Unwrap(),
				},
			})
		})
		s.unsubs = append(s.unsubs, unsub)
	}
}

// Stop drops all event-bus subscriptions.
func (s *DeviceTriggerSubscriber) Stop() {
	for _, u := range s.unsubs {
		u()
	}
	s.unsubs = nil
}
