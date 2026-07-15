// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// alarmPanelTopic is the single WebSocket topic every alarm_panel
// broadcast rides. Areas are daemon-level (not per-central), so the
// alarm surface publishes on one flat topic and the payload carries the
// area_id — mirrors the wsapi.json `alarm.panel` topic.
const alarmPanelTopic = "alarm.panel"

// Broadcast Type labels for the alarm_panel family. These are the
// wire-level `type` strings the SPA switches on; they mirror the
// wsapi.json broadcast names. Deliberately distinct from the
// hmevent.EventTypeAlarm* engine tags (which use the `alarm_panel.`
// prefix) — the north-bound broadcast namespace is `alarm.`.
const (
	broadcastAlarmStateChanged     = "alarm.state_changed"
	broadcastAlarmCountdown        = "alarm.countdown"
	broadcastAlarmReadinessChanged = "alarm.readiness_changed"
	broadcastAlarmTriggered        = "alarm.triggered"
	broadcastAlarmJournalAppended  = "alarm.journal_appended"
	broadcastAlarmWalkTestProgress = "alarm.walktest_progress"
	broadcastAlarmHealthChanged    = "alarm.health_changed"
)

// AlarmStateChangedPayload is the broadcast payload for an arm-state
// transition (alarm.state_changed).
type AlarmStateChangedPayload struct {
	AreaID     string `json:"area_id"`
	AreaName   string `json:"area_name"`
	OldState   string `json:"old_state"`
	NewState   string `json:"new_state"`
	Mode       string `json:"mode,omitempty"`
	ChangedBy  string `json:"changed_by,omitempty"`
	Source     string `json:"source,omitempty"`
	IncidentID int64  `json:"incident_id,omitempty"`
}

// AlarmCountdownPayload is the broadcast payload for a running exit/entry
// delay tick (alarm.countdown). Remaining/total are carried in both
// milliseconds (source fidelity) and whole seconds (display parity with
// the panel status DTO's countdown).
type AlarmCountdownPayload struct {
	AreaID      string `json:"area_id"`
	Kind        string `json:"kind"`
	RemainingMS int64  `json:"remaining_ms"`
	TotalMS     int64  `json:"total_ms"`
	RemainingS  int    `json:"remaining_s"`
	TotalS      int    `json:"total_s"`
}

// AlarmReadinessChangedPayload is the broadcast payload for a
// ready-to-arm recomputation (alarm.readiness_changed). The map is keyed
// by mode name, reusing the REST readiness verdict shape.
type AlarmReadinessChangedPayload struct {
	AreaID    string                              `json:"area_id"`
	Readiness map[string]hmapi.AlarmModeReadiness `json:"readiness"`
}

// AlarmTriggeredPayload is the broadcast payload for an area entering
// triggered (alarm.triggered).
type AlarmTriggeredPayload struct {
	AreaID     string `json:"area_id"`
	AreaName   string `json:"area_name"`
	IncidentID int64  `json:"incident_id"`
	SensorID   string `json:"sensor_id,omitempty"`
	SensorName string `json:"sensor_name,omitempty"`
	Cause      string `json:"cause"`
	Mode       string `json:"mode,omitempty"`
}

// AlarmJournalAppendedPayload is the broadcast payload for a persisted
// journal head (alarm.journal_appended). It carries the entry head, not
// the details — consumers query the journal for the full row.
type AlarmJournalAppendedPayload struct {
	EntryID    int64  `json:"entry_id"`
	AreaID     string `json:"area_id,omitempty"`
	Class      string `json:"class"`
	Event      string `json:"event"`
	Actor      string `json:"actor,omitempty"`
	IncidentID int64  `json:"incident_id,omitempty"`
}

// AlarmWalkTestProgressPayload is the broadcast payload for a walk-test
// sensor activation (alarm.walktest_progress).
type AlarmWalkTestProgressPayload struct {
	AreaID     string `json:"area_id"`
	SensorID   string `json:"sensor_id"`
	SensorName string `json:"sensor_name,omitempty"`
	Seen       int    `json:"seen"`
	Total      int    `json:"total"`
}

// AlarmHealthChangedPayload is the broadcast payload for an
// alarm-service health transition (alarm.health_changed). It is
// service-global, so it carries no area_id.
type AlarmHealthChangedPayload struct {
	Healthy bool   `json:"healthy"`
	Note    string `json:"note"`
}

// AlarmPanelSubscriber bridges the daemon-level alarm event bus onto the
// WebSocket [*Hub]. Unlike [HubEventsSubscriber] it subscribes to one
// shared bus (areas are daemon-level, not per-central), so there is no
// per-central fan-out — one handler per alarm event type.
type AlarmPanelSubscriber struct {
	bus    *events.Bus
	hub    *Hub
	unsubs []func()
}

// NewAlarmPanelSubscriber returns a subscriber bound to the alarm bus
// and the WebSocket hub.
func NewAlarmPanelSubscriber(bus *events.Bus, hub *Hub) *AlarmPanelSubscriber {
	return &AlarmPanelSubscriber{bus: bus, hub: hub}
}

// Start attaches one subscription per alarm event type to the bus.
func (s *AlarmPanelSubscriber) Start() {
	if s.bus == nil || s.hub == nil {
		return
	}
	s.unsubs = append(
		s.unsubs,
		events.Subscribe(s.bus, s.onStateChanged),
		events.Subscribe(s.bus, s.onCountdown),
		events.Subscribe(s.bus, s.onReadinessChanged),
		events.Subscribe(s.bus, s.onTriggered),
		events.Subscribe(s.bus, s.onJournalAppended),
		events.Subscribe(s.bus, s.onWalkTest),
		events.Subscribe(s.bus, s.onHealthChanged),
	)
}

// Stop drops all subscriptions.
func (s *AlarmPanelSubscriber) Stop() {
	for _, u := range s.unsubs {
		u()
	}
	s.unsubs = nil
}

func (s *AlarmPanelSubscriber) onStateChanged(e hmevent.AlarmStateChangedEvent) {
	s.hub.Publish(Event{
		Topic: alarmPanelTopic,
		Type:  broadcastAlarmStateChanged,
		When:  e.Timestamp(),
		Payload: AlarmStateChangedPayload{
			AreaID:     e.AreaID,
			AreaName:   e.AreaName,
			OldState:   string(e.From),
			NewState:   string(e.To),
			Mode:       string(e.Mode),
			ChangedBy:  e.ChangedBy,
			Source:     e.Source,
			IncidentID: e.IncidentID,
		},
	})
}

func (s *AlarmPanelSubscriber) onCountdown(e hmevent.AlarmCountdownEvent) {
	s.hub.Publish(Event{
		Topic: alarmPanelTopic,
		Type:  broadcastAlarmCountdown,
		When:  e.Timestamp(),
		Payload: AlarmCountdownPayload{
			AreaID:      e.AreaID,
			Kind:        e.Kind,
			RemainingMS: e.RemainingMS,
			TotalMS:     e.TotalMS,
			RemainingS:  int(e.RemainingMS / 1000),
			TotalS:      int(e.TotalMS / 1000),
		},
	})
}

func (s *AlarmPanelSubscriber) onReadinessChanged(e hmevent.AlarmReadinessChangedEvent) {
	s.hub.Publish(Event{
		Topic: alarmPanelTopic,
		Type:  broadcastAlarmReadinessChanged,
		When:  e.Timestamp(),
		Payload: AlarmReadinessChangedPayload{
			AreaID:    e.AreaID,
			Readiness: alarmReadinessDTO(e.Readiness),
		},
	})
}

func (s *AlarmPanelSubscriber) onTriggered(e hmevent.AlarmTriggeredEvent) {
	s.hub.Publish(Event{
		Topic: alarmPanelTopic,
		Type:  broadcastAlarmTriggered,
		When:  e.Timestamp(),
		Payload: AlarmTriggeredPayload{
			AreaID:     e.AreaID,
			AreaName:   e.AreaName,
			IncidentID: e.IncidentID,
			SensorID:   e.SensorID,
			SensorName: e.SensorName,
			Cause:      e.Cause,
			Mode:       string(e.Mode),
		},
	})
}

func (s *AlarmPanelSubscriber) onJournalAppended(e hmevent.AlarmJournalAppendedEvent) {
	s.hub.Publish(Event{
		Topic: alarmPanelTopic,
		Type:  broadcastAlarmJournalAppended,
		When:  e.Timestamp(),
		Payload: AlarmJournalAppendedPayload{
			EntryID:    e.EntryID,
			AreaID:     e.AreaID,
			Class:      string(e.Class),
			Event:      e.Event,
			Actor:      e.Actor,
			IncidentID: e.IncidentID,
		},
	})
}

func (s *AlarmPanelSubscriber) onWalkTest(e hmevent.AlarmWalkTestEvent) {
	s.hub.Publish(Event{
		Topic: alarmPanelTopic,
		Type:  broadcastAlarmWalkTestProgress,
		When:  e.Timestamp(),
		Payload: AlarmWalkTestProgressPayload{
			AreaID:     e.AreaID,
			SensorID:   e.SensorID,
			SensorName: e.SensorName,
			Seen:       e.Seen,
			Total:      e.Total,
		},
	})
}

func (s *AlarmPanelSubscriber) onHealthChanged(e hmevent.AlarmHealthChangedEvent) {
	s.hub.Publish(Event{
		Topic: alarmPanelTopic,
		Type:  broadcastAlarmHealthChanged,
		When:  e.Timestamp(),
		Payload: AlarmHealthChangedPayload{
			Healthy: e.Healthy,
			Note:    e.Note,
		},
	})
}
