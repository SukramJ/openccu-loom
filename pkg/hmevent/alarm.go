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
	EventTypeAlarmCountdown        EventType = "alarm_panel.countdown"
	EventTypeAlarmWalkTest         EventType = "alarm_panel.walktest_progress"
	EventTypeAlarmHealthChanged    EventType = "alarm_panel.health_changed"
	EventTypeAlarmPanelChanged     EventType = "alarm_panel.panel_changed"
	EventTypeAlarmDuress           EventType = "alarm_panel.duress"
	EventTypeAlarmReminder         EventType = "alarm_panel.reminder"
	EventTypeAlarmCodesChanged     EventType = "alarm_panel.codes_changed"
	EventTypeAlarmNotification     EventType = "alarm_panel.notification"
)

// AlarmNotificationEvent is one enrolled notification output firing
// for an alarm (docs/alarm-concept.md §7): a deliberate, per-zone,
// mode-filtered notification signal — distinct from the raw state
// events every north-bound plane already receives. It is one-shot at
// fire time and never cancelled by a later silence.
type AlarmNotificationEvent struct {
	Base
	// ZoneID identifies the alarm zone.
	ZoneID string
	// ZoneName is the display name at publish time.
	ZoneName string
	// OutputID / OutputName identify the enrolled notification output.
	OutputID   string
	OutputName string
	// IncidentID references the incident this notification belongs to.
	IncidentID int64
	// Mode is the protection mode that was active at trigger time.
	Mode hmenum.AlarmMode
	// MQTT / Webhook select the delivery planes the output enrolled
	// for (both default true); each consumer honours its own flag.
	MQTT    bool
	Webhook bool
}

// Type implements Event.
func (AlarmNotificationEvent) Type() EventType { return EventTypeAlarmNotification }

// AlarmDuressEvent is the silent fan-out of a duress-code use
// (docs/alarm-concept.md §11). A duress code disarms (or arms /
// silences) normally with no visible UI difference; this event carries
// the alarm to notification targets (MQTT event topic + webhook) out of
// band. The WebSocket surface deliberately does NOT broadcast it — a
// watcher on the panel screen must not learn that duress fired — and the
// SPA never renders it. The visible journal entry is written Hidden.
type AlarmDuressEvent struct {
	Base
	// ZoneID identifies the alarm zone.
	ZoneID string
	// ZoneName is the display name at publish time.
	ZoneName string
	// Verb is the action the duress code accompanied (arm, disarm,
	// silence).
	Verb string
	// By is the resolved code identity (the duress code's display
	// name), when known.
	By string
	// Source names the surface the action came from.
	Source string
	// IncidentID references the active incident, 0 when none.
	IncidentID int64
}

// Type implements Event.
func (AlarmDuressEvent) Type() EventType { return EventTypeAlarmDuress }

// AlarmReminderEvent fires when an arm schedule elapses and the zone is
// not in the scheduled mode while the schedule is a reminder (AutoArm
// off): the engine notifies rather than arming (docs/alarm-concept.md
// §15 row 19). The actual reminder emission is wired by the schedule
// service; this type defines the bus contract.
type AlarmReminderEvent struct {
	Base
	// ZoneID identifies the alarm zone.
	ZoneID string
	// ZoneName is the display name at publish time.
	ZoneName string
	// Mode is the protection mode the schedule expected the zone to be
	// in.
	Mode hmenum.AlarmMode
}

// Type implements Event.
func (AlarmReminderEvent) Type() EventType { return EventTypeAlarmReminder }

// AlarmCodesChangedEvent fires after any alarm-code create / update /
// delete. It carries no code data — it is a reconcile poke: the
// effective code_arm_required / code_disarm_required discovery flags
// depend on whether applicable pin codes exist, so the MQTT plane
// re-derives discovery when the code set changes.
type AlarmCodesChangedEvent struct {
	Base
}

// Type implements Event.
func (AlarmCodesChangedEvent) Type() EventType { return EventTypeAlarmCodesChanged }

// AlarmPanelChangedEvent fires when the alarm-control-panel entity
// projection of an zone (or of the aggregate master panel) changes:
// its HA state token, availability, or identity. REST, WebSocket, and
// MQTT surfaces render this one projection.
type AlarmPanelChangedEvent struct {
	Base
	// UniqueID is the stable entity identifier.
	UniqueID string
	// ZoneID is the alarm zone, or "master" for the aggregate panel.
	ZoneID string
	// Name is the display name at publish time.
	Name string
	// State is the HA state token (disarmed, arming, pending,
	// triggered, armed_home, armed_away, armed_night, armed_vacation,
	// armed_custom_bypass).
	State string
	// Available reports the alarm-health verdict.
	Available bool
	// CodeArmRequired / CodeDisarmRequired carry the zone's effective
	// per-verb code policy (docs/alarm-concept.md §11): the zone-config
	// policy half AND the "an applicable enabled pin code exists" half.
	// The master aggregate carries the any-zone-requires union.
	CodeArmRequired    bool
	CodeDisarmRequired bool
	// Removed marks a panel whose zone was deleted.
	Removed bool
}

// Type implements Event.
func (AlarmPanelChangedEvent) Type() EventType { return EventTypeAlarmPanelChanged }

// AlarmHealthChangedEvent mirrors alarm-health transitions (siren
// stop verified/unverified, service degradations) onto the alarm bus
// so surfaces can render the health state live.
type AlarmHealthChangedEvent struct {
	Base
	// Healthy is the new health verdict.
	Healthy bool
	// Note is the stable, English machine string of the transition.
	Note string
}

// Type implements Event.
func (AlarmHealthChangedEvent) Type() EventType { return EventTypeAlarmHealthChanged }

// AlarmWalkTestEvent ticks when a walk-test session records a sensor
// activation, so the checklist view updates live.
type AlarmWalkTestEvent struct {
	Base
	// ZoneID identifies the alarm zone under test.
	ZoneID string
	// SensorID / SensorName identify the sensor that just tripped.
	SensorID   string
	SensorName string
	// Seen / Total report the session progress.
	Seen  int
	Total int
}

// Type implements Event.
func (AlarmWalkTestEvent) Type() EventType { return EventTypeAlarmWalkTest }

// AlarmCountdownEvent ticks once per second while an exit or entry
// delay runs, so surfaces can render live countdowns and chirp
// drivers can pace their ticks.
type AlarmCountdownEvent struct {
	Base
	// ZoneID identifies the alarm zone.
	ZoneID string
	// Kind is the running countdown: "exit_delay" or "entry_delay"
	// (the persisted timer-kind tokens).
	Kind string
	// RemainingMS and TotalMS describe the countdown position.
	RemainingMS int64
	TotalMS     int64
}

// Type implements Event.
func (AlarmCountdownEvent) Type() EventType { return EventTypeAlarmCountdown }

// AlarmStateChangedEvent fires on every arm-state-machine transition
// of an alarm zone (docs/alarm-concept.md §5). Silence is not a state
// transition; it surfaces via the journal event instead.
type AlarmStateChangedEvent struct {
	Base
	// ZoneID identifies the alarm zone.
	ZoneID string
	// ZoneName is the display name at publish time.
	ZoneName string
	// From and To are the machine states of the transition.
	From hmenum.AlarmZoneState
	To   hmenum.AlarmZoneState
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

// AlarmTriggeredEvent fires when an zone enters `triggered` and an
// incident is opened (or re-adopted after a restart / reconnect).
type AlarmTriggeredEvent struct {
	Base
	// ZoneID identifies the alarm zone.
	ZoneID string
	// ZoneName is the display name at publish time.
	ZoneName string
	// IncidentID references the incident this trigger belongs to.
	IncidentID int64
	// SensorID identifies the triggering sensor; empty when the cause
	// is not a sensor (adopted siren, central-loss policy).
	SensorID string
	// SensorName is the triggering sensor's display name.
	SensorName string
	// Cause is a stable machine-readable cause token (sensor,
	// adopted, central_lost, restored, hazard, panic).
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
// of an zone changes for at least one mode (docs/alarm-concept.md
// §6.3). Consumers get the full per-mode map, not a delta.
type AlarmReadinessChangedEvent struct {
	Base
	// ZoneID identifies the alarm zone.
	ZoneID string
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
	// ZoneID identifies the alarm zone; empty for engine-global
	// entries.
	ZoneID string
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
