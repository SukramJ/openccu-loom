// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"strconv"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// Typed event payloads
//
// The WebSocket envelope is `{topic, type, ts, payload}`. Until P3-6
// landed, every publisher inlined the `payload` as a `map[string]any`,
// which made the wire shape easy to drift between producers. The
// dedicated payload structs below pin the field names — anyone calling
// the typed Publish* helpers gets a compile-time guarantee that the
// emitted JSON has the right keys, in the right shape.
//
// Adding a new event:
//   1. Define a new payload struct here with `json:` tags.
//   2. Add a typed publisher in this file (or in hub.go for fan-outs
//      that need access to internal hub state).
//   3. Replace the `map[string]any` payload at the call site.

// DataPointValueChangedPayload mirrors `EventTypeDataPointValueChanged`.
// Fields cover what every consumer of the value-changed stream needs:
// the (central, interface, device, channel, parameter) coordinate, the
// new value, the previous value, and the wall-clock timestamp the CCU
// observed the change at.
type DataPointValueChangedPayload struct {
	Central       string `json:"central"`
	Interface     string `json:"interface,omitempty"`
	DeviceAddress string `json:"device_address"`
	Channel       int    `json:"channel"`
	Parameter     string `json:"parameter"`
	ParamsetKey   string `json:"paramset_key"`
	Value         any    `json:"value"`
	Previous      any    `json:"previous,omitempty"`
	ModifiedAt    string `json:"modified_at"`
}

// CustomDataPointStateChangedPayload carries the aggregated CDP-state
// snapshot emitted whenever a wire-DP change on a CDP-bound channel
// would alter the CDP's State(). SPA tiles subscribe to one
// CDP-scoped topic per tile rather than reassembling state from N
// per-DP events.
type CustomDataPointStateChangedPayload struct {
	Central       string         `json:"central"`
	DeviceAddress string         `json:"device_address"`
	Channel       int            `json:"channel"`
	Name          string         `json:"name"`
	Kind          string         `json:"kind,omitempty"`
	State         map[string]any `json:"state"`
}

// CentralStateChangedPayload mirrors `EventTypeCentralStateChanged`.
type CentralStateChangedPayload struct {
	Central  string `json:"central"`
	OldState string `json:"old_state"`
	NewState string `json:"new_state"`
}

// PublishDataPointValueChanged emits a typed value-changed envelope
// with the default envelope-kind ("change"). For initial-snapshot
// emissions use [Hub.PublishDataPointValueChangedKind] with
// [KindInitial].
// Topic follows the spec convention
// `device.<addr>.channels.<no>.data_points.<parameter>`.
func (h *Hub) PublishDataPointValueChanged(
	central, iface, deviceAddr string,
	channel int,
	parameter, paramsetKey string,
	value, previous any,
	when time.Time,
) {
	h.PublishDataPointValueChangedKind(KindChange, central, iface, deviceAddr, channel,
		parameter, paramsetKey, value, previous, when)
}

// PublishDataPointValueChangedKind is the kind-aware variant of
// [Hub.PublishDataPointValueChanged]. Callers that distinguish
// initial-snapshot pushes from incremental changes pass [KindInitial]
// or [KindRefresh] here; the default [KindChange] is what
// [Hub.PublishDataPointValueChanged] uses.
func (h *Hub) PublishDataPointValueChangedKind(
	envKind, central, iface, deviceAddr string,
	channel int,
	parameter, paramsetKey string,
	value, previous any,
	when time.Time,
) {
	h.Publish(Event{
		Kind:  envKind,
		Topic: DataPointTopic(deviceAddr, channel, parameter),
		Type:  string(hmevent.EventTypeDataPointValueChanged),
		When:  when,
		Payload: DataPointValueChangedPayload{
			Central:       central,
			Interface:     iface,
			DeviceAddress: deviceAddr,
			Channel:       channel,
			Parameter:     parameter,
			ParamsetKey:   paramsetKey,
			Value:         value,
			Previous:      previous,
			ModifiedAt:    when.UTC().Format(time.RFC3339Nano),
		},
	})
}

// PublishCustomDataPointStateChanged emits a typed CDP-state snapshot
// with the default envelope-kind ("change"). For initial-snapshot
// emissions use [Hub.PublishCustomDataPointStateChangedKind].
// Topic follows the convention `device.<addr>.cdps.<name>`. The
// `kind` parameter here is the CDP widget hint (light, cover_blind,
// …), not the envelope kind — those are separate axes.
func (h *Hub) PublishCustomDataPointStateChanged(
	central, deviceAddr string,
	channel int,
	name, kind string,
	state map[string]any,
	when time.Time,
) {
	h.PublishCustomDataPointStateChangedKind(KindChange, central, deviceAddr, channel,
		name, kind, state, when)
}

// PublishCustomDataPointStateChangedKind is the envelope-kind-aware
// variant of [Hub.PublishCustomDataPointStateChanged]. The first
// argument is the envelope kind ([KindInitial] / [KindChange] /
// [KindRefresh]); the per-CDP widget `kind` keeps its name on the
// payload.
func (h *Hub) PublishCustomDataPointStateChangedKind(
	envKind, central, deviceAddr string,
	channel int,
	name, kind string,
	state map[string]any,
	when time.Time,
) {
	h.Publish(Event{
		Kind:  envKind,
		Topic: CustomDataPointTopic(deviceAddr, name),
		Type:  string(hmevent.EventTypeCustomDataPointStateChanged),
		When:  when,
		Payload: CustomDataPointStateChangedPayload{
			Central:       central,
			DeviceAddress: deviceAddr,
			Channel:       channel,
			Name:          name,
			Kind:          kind,
			State:         state,
		},
	})
}

// PublishCentralStateChanged emits a typed central-state envelope.
func (h *Hub) PublishCentralStateChanged(central, oldState, newState string, when time.Time) {
	h.Publish(Event{
		Topic: CentralStateTopic(central),
		Type:  string(hmevent.EventTypeCentralStateChanged),
		When:  when,
		Payload: CentralStateChangedPayload{
			Central:  central,
			OldState: oldState,
			NewState: newState,
		},
	})
}

// DataPointTopic builds the canonical topic for a data-point event.
func DataPointTopic(deviceAddr string, channel int, parameter string) string {
	return "device." + deviceAddr + ".channels." + strconv.Itoa(channel) +
		".data_points." + parameter
}

// CustomDataPointTopic builds the canonical topic for a Custom-DP
// state event — one topic per (device, CDP name).
func CustomDataPointTopic(deviceAddr, name string) string {
	return "device." + deviceAddr + ".cdps." + name
}

// CentralStateTopic builds the canonical topic for a central-state
// event.
func CentralStateTopic(central string) string {
	return "central." + central + ".state"
}
