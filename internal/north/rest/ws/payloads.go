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
	// UniqueID is the canonical loom-namespaced routing key for this
	// data point (loom_<routing-key>), supplied so a client consumes it
	// directly instead of rebuilding it from the raw fields. Always present:
	// the daemon is the sole owner of the key, so clients consume it
	// unconditionally (an empty string signals an unresolved central serial,
	// not "field absent"). See docs/external-clients/ha-unique-id-migration.md.
	UniqueID string `json:"unique_id"`
	Value    any    `json:"value"`
	// DisplayValue is Value expressed in the data point's reported unit
	// (Value × multiplier), present only when that projection is
	// non-trivial. It carries the same meaning as the field of the same
	// name on the REST data-point summary, and must agree with it: a
	// client seeds from REST and updates from here, so a value scaled on
	// one plane and raw on the other makes the reading jump on the first
	// push.
	DisplayValue any `json:"display_value,omitempty"`
	Previous     any `json:"previous,omitempty"`
	// Available reports whether the new value is a confirmed reading:
	// observed AND valid (refreshed, paired STATUS acceptable, value type as
	// declared, within the declared bounds). For a calculated data point it
	// additionally folds in the validity of every source it derives from.
	//
	// Carried on the push because it can flip without the value changing —
	// a paired `<param>_STATUS` fault does not move the reading — and
	// because the transition *into* a fault usually arrives as a value
	// change, so a consumer reading availability only at catalogue-refresh
	// time renders the faulted value as confirmed. MASTER-paramset entries
	// are always reported available: configuration is not a runtime reading.
	Available  bool   `json:"available"`
	ModifiedAt string `json:"modified_at"`
	// Category and DataPointType classify the DP inline so a client that
	// reconnects mid-stream can route the event without a prior catalogue
	// lookup. They are quasi-static, so they ride a high-frequency message
	// only when a client opts in with `classify` on its subscribe frame;
	// the per-client write pump strips them otherwise (default off).
	Category      string `json:"category,omitempty"`
	DataPointType string `json:"data_point_type,omitempty"`
}

// CustomDataPointStateChangedPayload carries the aggregated CDP-state
// snapshot emitted whenever a wire-DP change on a CDP-bound channel
// would alter the CDP's State(). SPA tiles subscribe to one
// CDP-scoped topic per tile rather than reassembling state from N
// per-DP events.
type CustomDataPointStateChangedPayload struct {
	Central       string `json:"central"`
	DeviceAddress string `json:"device_address"`
	Channel       int    `json:"channel"`
	Name          string `json:"name"`
	Kind          string `json:"kind,omitempty"`
	// UniqueID is the canonical loom-namespaced routing key for the
	// custom data point this snapshot describes. Always present; see
	// [DataPointValueChangedPayload.UniqueID].
	UniqueID string         `json:"unique_id"`
	State    map[string]any `json:"state"`
}

// CentralStateChangedPayload mirrors `EventTypeCentralStateChanged`.
type CentralStateChangedPayload struct {
	Central  string `json:"central"`
	OldState string `json:"old_state"`
	NewState string `json:"new_state"`
}

// ValueChange describes one `datapoint.value_changed` emission.
//
// A struct rather than a parameter list: the emission carries four
// same-typed identity strings, two untyped values and two flags, and every
// extension appended another positional argument to a call nobody could
// read. Named fields also make the zero value safe to reason about —
// EnvelopeKind falls back to [KindChange], and the optional classification
// fields simply stay empty.
type ValueChange struct {
	// EnvelopeKind distinguishes initial-snapshot pushes ([KindInitial])
	// and source-token re-emits ([KindRefresh]) from incremental changes.
	// Empty means [KindChange].
	EnvelopeKind  string
	Central       string
	Interface     string
	DeviceAddress string
	Channel       int
	Parameter     string
	ParamsetKey   string
	Value         any
	// DisplayValue mirrors [DataPointValueChangedPayload.DisplayValue].
	// The caller resolves it (it needs the data point's Multiplier(),
	// which this package has no access to) and passes it through
	// unchanged; empty/nil leaves the payload field absent.
	DisplayValue any
	Previous     any
	When         time.Time
	// Category / DataPointType classify the data point. They are always
	// carried on the buffered payload (so a replay keeps them) and stripped
	// per-client at write time unless the client opted into `classify` —
	// see [client.writePump]. Empty omits them.
	Category      string
	DataPointType string
	// UniqueID is the canonical loom routing key; empty omits it.
	UniqueID string
	// Available reports whether the value is a confirmed reading. See
	// [DataPointValueChangedPayload.Available].
	Available bool
}

// PublishDataPointValueChanged emits a typed value-changed envelope.
// Topic follows the spec convention
// `device.<addr>.channels.<no>.data_points.<parameter>`.
func (h *Hub) PublishDataPointValueChanged(ev ValueChange) {
	envKind := ev.EnvelopeKind
	if envKind == "" {
		envKind = KindChange
	}
	h.Publish(Event{
		Kind:  envKind,
		Topic: DataPointTopic(ev.DeviceAddress, ev.Channel, ev.Parameter),
		Type:  string(hmevent.EventTypeDataPointValueChanged),
		When:  ev.When,
		Payload: DataPointValueChangedPayload{
			Central:       ev.Central,
			Interface:     ev.Interface,
			DeviceAddress: ev.DeviceAddress,
			Channel:       ev.Channel,
			Parameter:     ev.Parameter,
			ParamsetKey:   ev.ParamsetKey,
			UniqueID:      ev.UniqueID,
			Value:         ev.Value,
			DisplayValue:  ev.DisplayValue,
			Previous:      ev.Previous,
			Available:     ev.Available,
			ModifiedAt:    ev.When.UTC().Format(time.RFC3339Nano),
			Category:      ev.Category,
			DataPointType: ev.DataPointType,
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
	centralName, deviceAddr string,
	channel int,
	name, kind string,
	state map[string]any,
	when time.Time,
) {
	h.PublishCustomDataPointStateChangedKind(KindChange, centralName, deviceAddr, channel,
		name, kind, state, when, "")
}

// PublishCustomDataPointStateChangedKind is the envelope-kind-aware
// variant of [Hub.PublishCustomDataPointStateChanged]. The first
// argument is the envelope kind ([KindInitial] / [KindChange] /
// [KindRefresh]); the per-CDP widget `kind` keeps its name on the
// payload. uniqueID is the canonical loom routing key; pass "" to omit.
func (h *Hub) PublishCustomDataPointStateChangedKind(
	envKind, centralName, deviceAddr string,
	channel int,
	name, kind string,
	state map[string]any,
	when time.Time,
	uniqueID string,
) {
	h.Publish(Event{
		Kind:  envKind,
		Topic: CustomDataPointTopic(deviceAddr, name),
		Type:  string(hmevent.EventTypeCustomDataPointStateChanged),
		When:  when,
		Payload: CustomDataPointStateChangedPayload{
			Central:       centralName,
			DeviceAddress: deviceAddr,
			Channel:       channel,
			Name:          name,
			Kind:          kind,
			UniqueID:      uniqueID,
			State:         state,
		},
	})
}

// CentralReadinessChangedPayload mirrors `EventTypeCentralReadinessChanged`.
type CentralReadinessChangedPayload struct {
	Central          string `json:"central"`
	Phase            string `json:"phase"`
	Ready            bool   `json:"ready"`
	InterfacesLoaded int    `json:"interfaces_loaded"`
	InterfacesTotal  int    `json:"interfaces_total"`
}

// PublishCentralReadinessChanged emits a typed central-readiness envelope so
// north-bound consumers can distinguish a central still in southbound bring-up
// from one that is offline.
func (h *Hub) PublishCentralReadinessChanged(centralName, phase string, ready bool, loaded, total int, when time.Time) {
	h.Publish(Event{
		Topic: CentralReadinessTopic(centralName),
		Type:  string(hmevent.EventTypeCentralReadinessChanged),
		When:  when,
		Payload: CentralReadinessChangedPayload{
			Central:          centralName,
			Phase:            phase,
			Ready:            ready,
			InterfacesLoaded: loaded,
			InterfacesTotal:  total,
		},
	})
}

// PublishCentralStateChanged emits a typed central-state envelope.
func (h *Hub) PublishCentralStateChanged(centralName, oldState, newState string, when time.Time) {
	h.Publish(Event{
		Topic: CentralStateTopic(centralName),
		Type:  string(hmevent.EventTypeCentralStateChanged),
		When:  when,
		Payload: CentralStateChangedPayload{
			Central:  centralName,
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
func CentralStateTopic(centralName string) string {
	return "central." + centralName + ".state"
}

// CentralReadinessTopic builds the canonical topic for a central-readiness
// event.
func CentralReadinessTopic(centralName string) string {
	return "central." + centralName + ".readiness"
}
