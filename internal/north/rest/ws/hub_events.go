// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	hubmodel "github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// InstallModeChangedPayload is the WebSocket payload published on the
// `hub.<central>.install_mode` topic whenever a
// [hmevent.InstallModeChangedEvent] arrives.
type InstallModeChangedPayload struct {
	Central    string `json:"central"`
	Enabled    bool   `json:"enabled"`
	RemainingS int    `json:"remaining_s"`
}

// InstallModeTopic returns the canonical topic for a hub-level install-mode
// change event:
//
//	hub.<central>.install_mode
func InstallModeTopic(centralName string) string {
	return "hub." + centralName + ".install_mode"
}

// SysvarChangedPayload is the WebSocket payload published on the
// `hub.<central>.sysvars.<name>` topic whenever a
// [hmevent.SysvarChangedEvent] arrives. ValueType is omitted when
// the producer leaves it unset.
type SysvarChangedPayload struct {
	Central   string              `json:"central"`
	Name      string              `json:"name"`
	ValueType hmenum.HubValueType `json:"value_type,omitempty"`
	// UniqueID is the canonical loom-namespaced routing key for this
	// sysvar (loom_<serial10>_sysvar_<hub-slug>). Always present and
	// non-empty — it resolves from the (gated) central serial plus the
	// sysvar name, both always carried by the change event. See
	// [DataPointValueChangedPayload.UniqueID].
	UniqueID string `json:"unique_id"`
	Value    any    `json:"value"`
	Previous any    `json:"previous,omitempty"`
}

// ProgramExecutedPayload is the WebSocket payload published on the
// `hub.<central>.programs.<id>` topic whenever a
// [hmevent.ProgramExecutedEvent] arrives.
type ProgramExecutedPayload struct {
	Central   string                `json:"central"`
	ProgramID string                `json:"program_id"`
	Trigger   hmenum.ProgramTrigger `json:"trigger,omitempty"`
	Success   bool                  `json:"success"`
	// UniqueID is the canonical loom-namespaced routing key for this
	// program (loom_<serial10>_program_<hub-slug(name)>). Deliberately
	// optional — unlike the sysvar / value-changed payloads it keys on the
	// program *name*, but the execution event carries only the program id,
	// so the name is resolved from the hub model (programUniqueID) and the
	// key is empty for a program not yet loaded. The REST ProgramSummary,
	// which iterates resolved Program objects, always carries it.
	UniqueID string `json:"unique_id,omitempty"`
}

// SysvarTopic returns the canonical topic for a sysvar-change event:
//
//	hub.<central>.sysvars.<name>
func SysvarTopic(centralName, name string) string {
	return "hub." + centralName + ".sysvars." + name
}

// ProgramTopic returns the canonical topic for a program-execution event:
//
//	hub.<central>.programs.<id>
func ProgramTopic(centralName, programID string) string {
	return "hub." + centralName + ".programs." + programID
}

// programUniqueID builds the canonical loom routing key for a program.
// The routing key keys on the program *name* slug, but the execution
// event carries only the program id, so the name is resolved from the
// central's hub model. Returns "" when the central or program is not
// known yet (the field is optional). See
// docs/external-clients/ha-unique-id-migration.md.
func programUniqueID(reg *central.Registry, centralName, programID string) string {
	if reg == nil {
		return ""
	}
	h := reg.HubFor(centralName)
	if h == nil {
		return ""
	}
	p, ok := h.Program(programID)
	if !ok || p == nil {
		return ""
	}
	return p.CanonicalUniqueID(reg.SerialSuffix(centralName))
}

// HubEventsSubscriber bridges per-central [hmevent.SysvarChangedEvent]
// and [hmevent.ProgramExecutedEvent] from the domain bus to the
// WebSocket [*Hub]. Mirrors [SystemStatusSubscriber] in shape; runs
// alongside it so each event type gets its own focused subscriber and
// failure of one path cannot starve the other.
type HubEventsSubscriber struct {
	reg    *central.Registry
	hub    *Hub
	unsubs []func()
}

// NewHubEventsSubscriber returns a subscriber bound to reg and hub.
func NewHubEventsSubscriber(reg *central.Registry, hub *Hub) *HubEventsSubscriber {
	return &HubEventsSubscriber{reg: reg, hub: hub}
}

// Start attaches subscriptions to every registered central's event bus.
func (s *HubEventsSubscriber) Start() {
	if s.reg == nil || s.hub == nil {
		return
	}
	for _, u := range s.reg.List() {
		centralName := u.Name()
		// Hub singletons (alarm / service messages, inbox, metrics,
		// connectivity) reach the WebSocket via the hub model's own change
		// hooks — the same OnUpdate surface the MQTT publisher fans on — so
		// clients can drop their hub-refresh poll loop. This path does not
		// depend on the event bus, so it runs before the bus guard below.
		s.subscribeHubModel(centralName, u.HubModel)
		bus := u.EventBus
		if bus == nil {
			continue
		}
		hub := s.hub
		reg := s.reg
		unsubSv := events.Subscribe(bus, func(e hmevent.SysvarChangedEvent) {
			hub.Publish(Event{
				Topic: SysvarTopic(centralName, e.Name),
				Type:  string(hmevent.EventTypeSysvarChanged),
				When:  e.Timestamp(),
				Payload: SysvarChangedPayload{
					Central:   centralName,
					Name:      e.Name,
					ValueType: e.ValueType,
					UniqueID: routingkey.CanonicalUniqueID(
						reg.SerialSuffix(centralName), "sysvar", routingkey.HubSlug(e.Name), "",
					),
					Value:    e.NewValue.Unwrap(),
					Previous: e.OldValue.Unwrap(),
				},
			})
		})
		unsubPg := events.Subscribe(bus, func(e hmevent.ProgramExecutedEvent) {
			hub.Publish(Event{
				Topic: ProgramTopic(centralName, e.ProgramID),
				Type:  string(hmevent.EventTypeProgramExecuted),
				When:  e.Timestamp(),
				Payload: ProgramExecutedPayload{
					Central:   centralName,
					ProgramID: e.ProgramID,
					Trigger:   e.Trigger,
					Success:   e.Success,
					UniqueID:  programUniqueID(reg, centralName, e.ProgramID),
				},
			})
		})
		unsubIM := events.Subscribe(bus, func(e hmevent.InstallModeChangedEvent) {
			hub.Publish(Event{
				Topic: InstallModeTopic(centralName),
				Type:  string(hmevent.EventTypeInstallModeChanged),
				When:  e.Timestamp(),
				Payload: InstallModeChangedPayload{
					Central:    centralName,
					Enabled:    e.Enabled,
					RemainingS: e.RemainingS,
				},
			})
		})
		// Per-interface reachability: pushed by the reconciler and the CCU
		// callback path as ConnectivityChangedEvent. Bus-driven (not a model
		// hook) because the connectivity tracker is attached lazily — see
		// subscribeHubModel.
		unsubConn := events.Subscribe(bus, func(e hmevent.ConnectivityChangedEvent) {
			hub.Publish(Event{
				Topic: ConnectivityTopic(centralName, e.InterfaceID),
				Type:  string(hmevent.EventTypeConnectivityChanged),
				When:  e.Timestamp(),
				Payload: HubConnectivityChangedPayload{
					Central:     centralName,
					InterfaceID: e.InterfaceID,
					Reachable:   e.Reachable,
					LatencyMs:   e.LatencyMs,
				},
			})
		})
		s.unsubs = append(s.unsubs, unsubSv, unsubPg, unsubIM, unsubConn)
	}
}

// Envelope Type labels for the hub-singleton singletons that have no event-bus
// counterpart. The alarm / service / connectivity flavours reuse the catalogued
// [hmevent.EventType] strings; inbox, metrics, and system-update are model-only.
const (
	eventTypeInboxChanged        = "hub.inbox_changed"
	eventTypeMetricsChanged      = "hub.metrics_changed"
	eventTypeSystemUpdateChanged = "hub.system_update_changed"
)

// HubCountChangedPayload is the broadcast payload for the count-valued hub
// singletons (alarm / service messages, inbox). Count is the current entry
// count; clients refresh the full list via REST when they need the entries.
type HubCountChangedPayload struct {
	Central string `json:"central"`
	Count   int    `json:"count"`
}

// HubMetricChangedPayload is the broadcast payload for a single hub metric
// observation.
type HubMetricChangedPayload struct {
	Central string  `json:"central"`
	Metric  string  `json:"metric"`
	Value   float64 `json:"value"`
	Unit    string  `json:"unit,omitempty"`
}

// HubConnectivityChangedPayload is the broadcast payload for a per-interface
// reachability change. LatencyMs carries the probe round-trip when measured.
type HubConnectivityChangedPayload struct {
	Central     string  `json:"central"`
	InterfaceID string  `json:"interface_id"`
	Reachable   bool    `json:"reachable"`
	LatencyMs   float64 `json:"latency_ms,omitempty"`
}

// HubSystemUpdateChangedPayload is the broadcast payload for a CCU
// firmware-update state change. It mirrors the REST `GET /system/update`
// entry so a client can drop its update-status poll loop and consume the
// same fields off the push. CurrentFirmware / AvailableFirmware are omitted
// while empty so an unobserved tracker stays compact.
type HubSystemUpdateChangedPayload struct {
	Central           string `json:"central"`
	CurrentFirmware   string `json:"current_firmware,omitempty"`
	AvailableFirmware string `json:"available_firmware,omitempty"`
	UpdateAvailable   bool   `json:"update_available"`
	InProgress        bool   `json:"in_progress"`
}

// AlarmMessagesTopic returns the canonical topic for alarm-message changes:
//
//	hub.<central>.alarm_messages
func AlarmMessagesTopic(centralName string) string {
	return "hub." + centralName + ".alarm_messages"
}

// ServiceMessagesTopic returns the canonical topic for service-message changes:
//
//	hub.<central>.service_messages
func ServiceMessagesTopic(centralName string) string {
	return "hub." + centralName + ".service_messages"
}

// InboxTopic returns the canonical topic for inbox changes:
//
//	hub.<central>.inbox
func InboxTopic(centralName string) string {
	return "hub." + centralName + ".inbox"
}

// MetricsTopic returns the canonical topic for hub-metric changes:
//
//	hub.<central>.metrics
func MetricsTopic(centralName string) string {
	return "hub." + centralName + ".metrics"
}

// ConnectivityTopic returns the canonical topic for a per-interface
// reachability change:
//
//	hub.<central>.connectivity.<interface_id>
func ConnectivityTopic(centralName, interfaceID string) string {
	return "hub." + centralName + ".connectivity." + interfaceID
}

// SystemUpdateTopic returns the canonical topic for a CCU firmware-update
// state change:
//
//	hub.<central>.system_update
func SystemUpdateTopic(centralName string) string {
	return "hub." + centralName + ".system_update"
}

// subscribeHubModel wires the hub model's change hooks to WebSocket broadcasts
// so clients receive push updates for the singletons that otherwise force a
// poll loop. No-op when the model is nil.
func (s *HubEventsSubscriber) subscribeHubModel(centralName string, hm *hubmodel.Hub) {
	if hm == nil {
		return
	}
	if hm.Messages != nil {
		s.unsubs = append(s.unsubs, hm.Messages.OnUpdate(func(msgs []hubmodel.AlarmMessage) {
			s.hub.Publish(Event{
				Topic:   AlarmMessagesTopic(centralName),
				Type:    string(hmevent.EventTypeAlarmMessage),
				When:    time.Now().UTC(),
				Payload: HubCountChangedPayload{Central: centralName, Count: len(msgs)},
			})
		}))
	}
	if hm.ServiceMessages != nil {
		s.unsubs = append(s.unsubs, hm.ServiceMessages.OnUpdate(func(msgs []hubmodel.ServiceMessage) {
			s.hub.Publish(Event{
				Topic:   ServiceMessagesTopic(centralName),
				Type:    string(hmevent.EventTypeServiceMessage),
				When:    time.Now().UTC(),
				Payload: HubCountChangedPayload{Central: centralName, Count: len(msgs)},
			})
		}))
	}
	if hm.Inbox != nil {
		s.unsubs = append(s.unsubs, hm.Inbox.OnUpdate(func(devices []hubmodel.InboxDevice) {
			s.hub.Publish(Event{
				Topic:   InboxTopic(centralName),
				Type:    eventTypeInboxChanged,
				When:    time.Now().UTC(),
				Payload: HubCountChangedPayload{Central: centralName, Count: len(devices)},
			})
		}))
	}
	if hm.Metrics != nil {
		s.unsubs = append(s.unsubs, hm.Metrics.OnAny(func(sample hubmodel.MetricSample) {
			s.hub.Publish(Event{
				Topic: MetricsTopic(centralName),
				Type:  eventTypeMetricsChanged,
				When:  time.Now().UTC(),
				Payload: HubMetricChangedPayload{
					Central: centralName,
					Metric:  string(sample.Kind),
					Value:   sample.Value,
					Unit:    hubmodel.MetricSensorUnit(sample.Kind),
				},
			})
		}))
	}
	if hm.Update != nil {
		s.unsubs = append(s.unsubs, hm.Update.OnUpdate(func(info hubmodel.UpdateInfo) {
			s.hub.Publish(Event{
				Topic: SystemUpdateTopic(centralName),
				Type:  eventTypeSystemUpdateChanged,
				When:  time.Now().UTC(),
				Payload: HubSystemUpdateChangedPayload{
					Central:           centralName,
					CurrentFirmware:   info.CurrentFirmware,
					AvailableFirmware: info.AvailableFirmware,
					UpdateAvailable:   info.UpdateAvailable,
					InProgress:        hm.Update.InProgress(),
				},
			})
		}))
	}
	// Connectivity is NOT wired here: the per-interface tracker is attached
	// lazily via Hub.SetConnectivity during readiness-gated central bring-up,
	// which can run after this subscriber has already started. Reading the
	// tracker at wire-time would miss it. Connectivity broadcasts ride the
	// event bus instead (see the ConnectivityChangedEvent subscription in
	// Start), mirroring the MQTT publisher's choice.
}

// Stop drops all event-bus subscriptions.
func (s *HubEventsSubscriber) Stop() {
	for _, u := range s.unsubs {
		u()
	}
	s.unsubs = nil
}
