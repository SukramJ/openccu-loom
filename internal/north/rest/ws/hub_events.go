// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
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
	// sysvar (loom_<serial10>_sysvar_<hub-slug>). Optional; see
	// [DataPointValueChangedPayload.UniqueID].
	UniqueID string `json:"unique_id,omitempty"`
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
	// program (loom_<serial10>_program_<hub-slug(name)>). Optional and
	// empty when the program name cannot be resolved; see
	// [DataPointValueChangedPayload.UniqueID].
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
		bus := u.EventBus
		if bus == nil {
			continue
		}
		centralName := u.Name()
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
		s.unsubs = append(s.unsubs, unsubSv, unsubPg, unsubIM)
	}
}

// Stop drops all event-bus subscriptions.
func (s *HubEventsSubscriber) Stop() {
	for _, u := range s.unsubs {
		u()
	}
	s.unsubs = nil
}
