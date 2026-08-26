// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	hubmodel "github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/internal/wiring"
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
	// Channel is the canonical channel address ("ADDR:idx") of the device
	// channel this sysvar is associated with (explicit CCU assignment or
	// name match — the same value the REST SysvarSummary carries). Omitted
	// when the sysvar belongs to no device: clients then attach the entity
	// to the central hub device.
	Channel string `json:"channel,omitempty"`
	// DeviceAddress is the device part of Channel (before the ":");
	// omitted together with Channel.
	DeviceAddress string `json:"device_address,omitempty"`
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
	// Channel is the canonical channel address ("ADDR:idx") of the device
	// channel this program is associated with (name match — the same value
	// the REST ProgramSummary carries). Omitted when the program belongs to
	// no device or is not yet loaded in the hub model.
	Channel string `json:"channel,omitempty"`
	// DeviceAddress is the device part of Channel (before the ":");
	// omitted together with Channel.
	DeviceAddress string `json:"device_address,omitempty"`
}

// ProgramChangedPayload is the WebSocket payload published on the
// `hub.<central>.programs.<id>` topic whenever a program's activity flag
// changes — the operator toggled it in the CCU WebUI, or a client wrote it.
//
// A CCU program is two controls: the activity flag decides whether it reacts
// at all, and the execution runs it once. A deactivated program refuses the
// execution, so a client offering "run now" needs the transition to render
// that control unavailable. `execute_available` carries the daemon's answer
// so the rule is not re-derived per consumer — the same field the REST
// ProgramSummary and the MQTT availability topic carry.
type ProgramChangedPayload struct {
	Central   string `json:"central"`
	ProgramID string `json:"program_id"`
	// UniqueID is the canonical loom-namespaced routing key for this
	// program. Optional for the same reason as on
	// [ProgramExecutedPayload]: it keys on the program *name*, resolved
	// from the hub model, and is empty for a program not yet loaded.
	UniqueID string `json:"unique_id,omitempty"`
	// Active is the program's activity flag as the CCU reports it.
	Active bool `json:"active"`
	// ExecuteAvailable reports whether running the program would do
	// anything. False exactly while the program is deactivated.
	ExecuteAvailable bool `json:"execute_available"`
	// Channel / DeviceAddress mirror [ProgramExecutedPayload]; omitted when
	// the program belongs to no device.
	Channel       string `json:"channel,omitempty"`
	DeviceAddress string `json:"device_address,omitempty"`
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

// sysvarDeviceLink resolves the current device association of the named
// sysvar from the central's hub model: the channel address plus the derived
// device address. Both are empty when the central, the sysvar, or the
// association is unknown — the payload fields are optional and clients fall
// back to the hub card.
func sysvarDeviceLink(reg *central.Registry, centralName, name string) (channel, deviceAddress string) {
	if reg == nil {
		return "", ""
	}
	h := reg.HubFor(centralName)
	if h == nil {
		return "", ""
	}
	sv, ok := h.Sysvar(name)
	if !ok || sv == nil {
		return "", ""
	}
	return sv.Channel(), sv.DeviceAddress()
}

// programDeviceLink is the program counterpart of [sysvarDeviceLink], keyed
// by program id.
func programDeviceLink(reg *central.Registry, centralName, programID string) (channel, deviceAddress string) {
	if reg == nil {
		return "", ""
	}
	h := reg.HubFor(centralName)
	if h == nil {
		return "", ""
	}
	p, ok := h.Program(programID)
	if !ok || p == nil {
		return "", ""
	}
	return p.Channel(), p.DeviceAddress()
}

// HubEventsSubscriber bridges per-central [hmevent.SysvarChangedEvent]
// and [hmevent.ProgramExecutedEvent] from the domain bus to the
// WebSocket [*Hub]. Mirrors [SystemStatusSubscriber] in shape; runs
// alongside it so each event type gets its own focused subscriber and
// failure of one path cannot starve the other.
type HubEventsSubscriber struct {
	reg *central.Registry
	hub *Hub

	// remove detaches the registry observer Start installed, together with
	// every per-central subscription it attached — see the field on
	// [SystemStatusSubscriber] for the full ownership rule. A lock here
	// would guard a Start/Stop overlap that cannot occur: the composition
	// root runs Start before any server listens and Stop after every server
	// has stopped.
	remove func()
}

// NewHubEventsSubscriber returns a subscriber bound to reg and hub.
func NewHubEventsSubscriber(reg *central.Registry, hub *Hub) *HubEventsSubscriber {
	return &HubEventsSubscriber{reg: reg, hub: hub}
}

// Start subscribes to every central the registry holds now and to every one
// registered later.
func (s *HubEventsSubscriber) Start() {
	if s.reg == nil || s.hub == nil {
		return
	}
	s.remove = s.reg.OnRegisterDeclared(wiring.Seam{
		Name:         "ws.hub_events",
		Collaborator: "*ws.HubEventSubscriber",
		Phase:        wiring.PhasePerCentral,
		Why:          "sysvar, program and service-message changes are never broadcast, so the SPA's hub views only update on a manual reload",
	}, s.StartCentral)
}

// StartCentral attaches this subscriber's hub-model and event-bus
// subscriptions to a single central and returns an unwire func that
// detaches them again (nil when there was nothing to attach).
//
// It exists because Start only ever walked the registry as it stood at
// boot: a central adopted at runtime got no subscriptions at all, so none
// of its hub singletons — alarm and service message counts, inbox,
// connectivity — ever reached a WebSocket client. That was invisible while
// nothing consumed the broadcasts; the sidebar's message badge made it a
// visible defect, because the adopted central's counter would sit at its
// seed value forever.
func (s *HubEventsSubscriber) StartCentral(u *central.Unit) func() {
	if s == nil || s.reg == nil || s.hub == nil || u == nil {
		return nil
	}
	var unsubs []func()
	{
		centralName := u.Name()
		// Hub singletons (alarm / service messages, inbox, metrics,
		// connectivity) reach the WebSocket via the hub model's own change
		// hooks — the same OnUpdate surface the MQTT publisher fans on — so
		// clients can drop their hub-refresh poll loop. This path does not
		// depend on the event bus, so it runs before the bus guard below.
		unsubs = append(unsubs, s.hubModelSubscriptions(centralName, u.HubModel)...)
		bus := u.EventBus
		if bus == nil {
			return unwireAll(unsubs)
		}
		hub := s.hub
		reg := s.reg
		unsubSv := events.Subscribe(bus, func(e hmevent.SysvarChangedEvent) {
			channel, deviceAddress := sysvarDeviceLink(reg, centralName, e.Name)
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
					Value:         e.NewValue.Unwrap(),
					Previous:      e.OldValue.Unwrap(),
					Channel:       channel,
					DeviceAddress: deviceAddress,
				},
			})
		})
		unsubPg := events.Subscribe(bus, func(e hmevent.ProgramExecutedEvent) {
			channel, deviceAddress := programDeviceLink(reg, centralName, e.ProgramID)
			hub.Publish(Event{
				Topic: ProgramTopic(centralName, e.ProgramID),
				Type:  string(hmevent.EventTypeProgramExecuted),
				When:  e.Timestamp(),
				Payload: ProgramExecutedPayload{
					Central:       centralName,
					ProgramID:     e.ProgramID,
					Trigger:       e.Trigger,
					Success:       e.Success,
					UniqueID:      programUniqueID(reg, centralName, e.ProgramID),
					Channel:       channel,
					DeviceAddress: deviceAddress,
				},
			})
		})
		unsubPc := events.Subscribe(bus, func(e hmevent.ProgramChangedEvent) {
			channel, deviceAddress := programDeviceLink(reg, centralName, e.ProgramID)
			hub.Publish(Event{
				Topic: ProgramTopic(centralName, e.ProgramID),
				Type:  string(hmevent.EventTypeProgramChanged),
				When:  e.Timestamp(),
				Payload: ProgramChangedPayload{
					Central:   centralName,
					ProgramID: e.ProgramID,
					UniqueID:  programUniqueID(reg, centralName, e.ProgramID),
					Active:    e.Active,
					// The CCU refuses to run a deactivated program, so the two
					// travel together: a client re-renders both controls off one
					// message instead of re-deriving the rule.
					ExecuteAvailable: e.Active,
					Channel:          channel,
					DeviceAddress:    deviceAddress,
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
		unsubs = append(unsubs, unsubSv, unsubPg, unsubPc, unsubIM, unsubConn)
	}
	return unwireAll(unsubs)
}

// unwireAll folds a slice of unsubscribe funcs into one, returning nil
// when there is nothing to detach so callers can store a plain nil.
func unwireAll(unsubs []func()) func() {
	if len(unsubs) == 0 {
		return nil
	}
	return func() {
		for _, u := range unsubs {
			if u != nil {
				u()
			}
		}
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

// hubModelSubscriptions wires the hub model's change hooks to WebSocket
// broadcasts so clients receive push updates for the singletons that
// otherwise force a poll loop, returning the unsubscribe funcs. Empty when
// the model is nil.
func (s *HubEventsSubscriber) hubModelSubscriptions(centralName string, hm *hubmodel.Hub) []func() {
	if hm == nil {
		return nil
	}
	var unsubs []func()
	if hm.Messages != nil {
		unsubs = append(unsubs, hm.Messages.OnUpdate(func(msgs []hubmodel.AlarmMessage) {
			s.hub.Publish(Event{
				Topic:   AlarmMessagesTopic(centralName),
				Type:    string(hmevent.EventTypeAlarmMessage),
				When:    time.Now().UTC(),
				Payload: HubCountChangedPayload{Central: centralName, Count: len(msgs)},
			})
		}))
	}
	if hm.ServiceMessages != nil {
		unsubs = append(unsubs, hm.ServiceMessages.OnUpdate(func(msgs []hubmodel.ServiceMessage) {
			s.hub.Publish(Event{
				Topic:   ServiceMessagesTopic(centralName),
				Type:    string(hmevent.EventTypeServiceMessage),
				When:    time.Now().UTC(),
				Payload: HubCountChangedPayload{Central: centralName, Count: len(msgs)},
			})
		}))
	}
	if hm.Inbox != nil {
		unsubs = append(unsubs, hm.Inbox.OnUpdate(func(devices []hubmodel.InboxDevice) {
			s.hub.Publish(Event{
				Topic:   InboxTopic(centralName),
				Type:    eventTypeInboxChanged,
				When:    time.Now().UTC(),
				Payload: HubCountChangedPayload{Central: centralName, Count: len(devices)},
			})
		}))
	}
	if hm.Metrics != nil {
		unsubs = append(unsubs, hm.Metrics.OnAny(func(sample hubmodel.MetricSample) {
			// A negative system_health is the not-ready sentinel
			// (hubmodel.MetricSystemHealthUnknown): the central is FAILED, so
			// the score is unknown, not a real percentage. REST's config
			// snapshot (system_hub.go) omits the field and the hub data-point
			// projection (hub_data_points.go) skips the metric entirely for
			// the same reason; this broadcast must match or a client mirroring
			// hub.metrics_changed onto a gauge renders "-1 %" during an outage
			// instead of "unknown".
			if sample.Kind == hubmodel.MetricSystemHealth && sample.Value < 0 {
				return
			}
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
		unsubs = append(unsubs, hm.Update.OnUpdate(func(info hubmodel.UpdateInfo) {
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
	// StartCentral), mirroring the MQTT publisher's choice.
	return unsubs
}

// Stop removes the registry observer and drops every subscription it
// attached — both the event-bus handlers and the hub-model change callbacks,
// for boot-time and adopted centrals alike.
func (s *HubEventsSubscriber) Stop() {
	if s.remove != nil {
		s.remove()
		s.remove = nil
	}
}
