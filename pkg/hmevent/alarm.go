// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmevent

import (
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Alarm-engine event tags. The `alarm_panel.` namespace is deliberately
// distinct from the CCU alarm-message surface (`hub.alarm_message`) —
// see docs/alarm-concept.md §4 (naming note). Keep the string form
// stable: metrics and north-bound subscriptions reference it.
const (
	EventTypeAlarmStateChanged     EventType = "alarm_panel.state_changed"
	EventTypeAlarmTriggered        EventType = "alarm_panel.triggered"
	EventTypeAlarmReadinessChanged EventType = "alarm_panel.readiness_changed"
	EventTypeAlarmJournalAppended  EventType = "alarm_panel.journal_appended"
)

// AlarmStateChangedEvent fires on every arm-state-machine transition
// of an alarm area (docs/alarm-concept.md §5). Silence is not a state
// transition; it surfaces via the journal event instead.
type AlarmStateChangedEvent struct {
	Base
	// AreaID identifies the alarm area.
	AreaID string
	// AreaName is the display name at publish time.
	AreaName string
	// From and To are the machine states of the transition.
	From hmenum.AlarmAreaState
	To   hmenum.AlarmAreaState
	// Mode is the active (or target, while arming) protection mode.
	Mode hmenum.AlarmMode
	// ChangedBy attributes the transition (code name, operator
	// account, keypad identity, or an engine-internal actor such as
	// "engine:restore"). Empty when unattributed.
	ChangedBy string
	// Source names the surface the action came from (rest, ws, mqtt,
	// hmcli, keypad, engine).
	Source string
	// IncidentID references the active incident, 0 when none.
	IncidentID int64
}

// Type implements Event.
func (AlarmStateChangedEvent) Type() EventType { return EventTypeAlarmStateChanged }

// AlarmTriggeredEvent fires when an area enters `triggered` and an
// incident is opened (or re-adopted after a restart / reconnect).
type AlarmTriggeredEvent struct {
	Base
	// AreaID identifies the alarm area.
	AreaID string
	// AreaName is the display name at publish time.
	AreaName string
	// IncidentID references the incident this trigger belongs to.
	IncidentID int64
	// SensorID identifies the triggering sensor; empty when the cause
	// is not a sensor (adopted siren, central-loss policy).
	SensorID string
	// SensorName is the triggering sensor's display name.
	SensorName string
	// Cause is a stable machine-readable cause token (sensor,
	// adopted, central_lost, restored).
	Cause string
	// Mode is the protection mode that was active at trigger time.
	Mode hmenum.AlarmMode
}

// Type implements Event.
func (AlarmTriggeredEvent) Type() EventType { return EventTypeAlarmTriggered }

// AlarmModeReadiness is the per-mode ready-to-arm verdict carried by
// [AlarmReadinessChangedEvent].
type AlarmModeReadiness struct {
	// Ready reports whether the mode can be armed without force.
	Ready bool
	// Blockers lists the sensor IDs currently blocking the arm.
	Blockers []string
	// Warnings lists sensor IDs with non-blocking health warnings.
	Warnings []string
}

// AlarmReadinessChangedEvent fires when the ready-to-arm computation
// of an area changes for at least one mode (docs/alarm-concept.md
// §6.3). Consumers get the full per-mode map, not a delta.
type AlarmReadinessChangedEvent struct {
	Base
	// AreaID identifies the alarm area.
	AreaID string
	// Readiness maps each configured mode to its verdict.
	Readiness map[hmenum.AlarmMode]AlarmModeReadiness
}

// Type implements Event.
func (AlarmReadinessChangedEvent) Type() EventType { return EventTypeAlarmReadinessChanged }

// AlarmJournalAppendedEvent fires after an alarm-journal entry has
// been persisted. It carries the entry head, not the full details —
// consumers needing details query the journal store.
type AlarmJournalAppendedEvent struct {
	Base
	// EntryID is the persisted journal row ID.
	EntryID int64
	// AreaID identifies the alarm area; empty for engine-global
	// entries.
	AreaID string
	// Class buckets the entry for filtering.
	Class hmenum.AlarmJournalClass
	// Event is the stable machine-readable event token within the
	// class (e.g. "armed", "force_armed", "silenced").
	Event string
	// Actor attributes the entry; empty when unattributed.
	Actor string
	// IncidentID references the related incident, 0 when none.
	IncidentID int64
}

// Type implements Event.
func (AlarmJournalAppendedEvent) Type() EventType { return EventTypeAlarmJournalAppended }
