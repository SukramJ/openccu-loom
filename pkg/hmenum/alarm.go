// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

// AlarmMode is a named protection level of an alarm zone. The string
// form is wire-stable: it appears in the REST/WS API, MQTT payloads,
// and the persisted alarm state. "disarmed" doubles as the active mode
// of a disarmed zone so every surface can render mode without a
// nullable field. See docs/alarm-concept.md §4 for the mode-naming
// mapping (UI localization, HA vocabulary).
type AlarmMode string

// AlarmMode values.
const (
	// AlarmModeDisarmed is the mode of an zone that is not armed.
	AlarmModeDisarmed AlarmMode = "disarmed"
	// AlarmModePerimeter arms the user-curated perimeter subset
	// (Hüllschutz): typically door/window contacts and rotary handles.
	AlarmModePerimeter AlarmMode = "perimeter"
	// AlarmModeFull arms every sensor assigned to the zone
	// (Vollschutz).
	AlarmModeFull AlarmMode = "full"
	// AlarmModeNight arms the night subset.
	AlarmModeNight AlarmMode = "night"
	// AlarmModeVacation arms the vacation subset.
	AlarmModeVacation AlarmMode = "vacation"
	// AlarmModeCustom arms a user-defined subset.
	AlarmModeCustom AlarmMode = "custom"
)

// String returns the wire representation.
func (m AlarmMode) String() string { return string(m) }

// Valid reports whether m is one of the defined alarm modes.
func (m AlarmMode) Valid() bool {
	switch m {
	case AlarmModeDisarmed, AlarmModePerimeter, AlarmModeFull, AlarmModeNight, AlarmModeVacation, AlarmModeCustom:
		return true
	default:
		return false
	}
}

// Armed reports whether m is an armed protection level (anything but
// disarmed).
func (m AlarmMode) Armed() bool { return m.Valid() && m != AlarmModeDisarmed }

// AlarmZoneState is the arm-state-machine state of one alarm zone.
// The vocabulary follows the HA alarm_control_panel model so every
// integration maps 1:1 (docs/alarm-concept.md §5).
type AlarmZoneState string

// AlarmZoneState values.
const (
	// AlarmZoneStateDisarmed means no protection is active.
	AlarmZoneStateDisarmed AlarmZoneState = "disarmed"
	// AlarmZoneStateArming means the exit delay is running.
	AlarmZoneStateArming AlarmZoneState = "arming"
	// AlarmZoneStateArmed means the zone is armed in its active mode.
	AlarmZoneStateArmed AlarmZoneState = "armed"
	// AlarmZoneStatePending means a delayed sensor tripped and the
	// entry delay is running; a valid disarm here produces no alarm.
	AlarmZoneStatePending AlarmZoneState = "pending"
	// AlarmZoneStateTriggered means an alarm incident is active.
	AlarmZoneStateTriggered AlarmZoneState = "triggered"
)

// String returns the wire representation.
func (s AlarmZoneState) String() string { return string(s) }

// Valid reports whether s is one of the defined zone states.
func (s AlarmZoneState) Valid() bool {
	switch s {
	case AlarmZoneStateDisarmed, AlarmZoneStateArming, AlarmZoneStateArmed, AlarmZoneStatePending, AlarmZoneStateTriggered:
		return true
	default:
		return false
	}
}

// AlarmSensorType classifies an enrolled alarm sensor. Types are
// derived from the device model / channel type and preset the mode
// matrix and behaviour flags; every assignment stays overridable per
// sensor (docs/alarm-concept.md §6.1).
type AlarmSensorType string

// AlarmSensorType values.
const (
	// AlarmSensorTypeDoor is a door-class contact (entry delay,
	// arm-after-closing presets).
	AlarmSensorTypeDoor AlarmSensorType = "door"
	// AlarmSensorTypeWindow is a window contact or rotary handle.
	AlarmSensorTypeWindow AlarmSensorType = "window"
	// AlarmSensorTypeMotion is a motion / presence detector.
	AlarmSensorTypeMotion AlarmSensorType = "motion"
	// AlarmSensorTypeTamper is a sabotage flag of an enrolled device;
	// it participates in all modes and warns while disarmed.
	AlarmSensorTypeTamper AlarmSensorType = "tamper"
	// AlarmSensorTypeHazard is an always-on hazard sensor (smoke,
	// water, gas/CO) that bypasses the arm-state machine.
	AlarmSensorTypeHazard AlarmSensorType = "hazard"
	// AlarmSensorTypePanic is an always-on panic input (keypad key,
	// remote key, wall button).
	AlarmSensorTypePanic AlarmSensorType = "panic"
)

// String returns the wire representation.
func (t AlarmSensorType) String() string { return string(t) }

// Valid reports whether t is one of the defined sensor types.
func (t AlarmSensorType) Valid() bool {
	switch t {
	case AlarmSensorTypeDoor, AlarmSensorTypeWindow, AlarmSensorTypeMotion, AlarmSensorTypeTamper, AlarmSensorTypeHazard, AlarmSensorTypePanic:
		return true
	default:
		return false
	}
}

// AlarmOutputClass declares what an enrolled alarm output is. The
// class — not the backing device type — decides which safety
// invariants apply: acoustic classes get the full bounded-duration
// siren treatment, visual classes follow the alarm-light lifecycle
// (docs/alarm-concept.md §7).
type AlarmOutputClass string

// AlarmOutputClass values.
const (
	// AlarmOutputClassAcousticSiren is a native siren's acoustic
	// channel (bounded per S1, stop-verified per S2).
	AlarmOutputClassAcousticSiren AlarmOutputClass = "acoustic_siren"
	// AlarmOutputClassSwitchedSiren is a plug-in siren behind a
	// switch actuator; activation writes ON_TIME atomically with the
	// switch-on so the device auto-offs.
	AlarmOutputClassSwitchedSiren AlarmOutputClass = "switched_siren"
	// AlarmOutputClassSmokeSounder enrolls smoke detectors as
	// additional intrusion sounders (engine-watchdogged only until
	// the device-side bound is confirmed).
	AlarmOutputClassSmokeSounder AlarmOutputClass = "smoke_sounder"
	// AlarmOutputClassOpticalSiren is a siren's optical channel; may
	// run longer than acoustic but stays bounded.
	AlarmOutputClassOpticalSiren AlarmOutputClass = "optical_siren"
	// AlarmOutputClassAlarmLight is a switch/dimmer used as alarm
	// light: on at trigger, off at silence/disarm.
	AlarmOutputClassAlarmLight AlarmOutputClass = "alarm_light"
	// AlarmOutputClassChirp emits confirmation tones / countdown
	// ticks; degrades first under duty-cycle pressure.
	AlarmOutputClassChirp AlarmOutputClass = "chirp"
	// AlarmOutputClassNotification is an MQTT/webhook/WS notification
	// target; never cancelled by silence.
	AlarmOutputClassNotification AlarmOutputClass = "notification"
	// AlarmOutputClassSysvarMirror mirrors zone state into a CCU
	// system variable for interop with CCU programs.
	AlarmOutputClassSysvarMirror AlarmOutputClass = "sysvar_mirror"
)

// String returns the wire representation.
func (c AlarmOutputClass) String() string { return string(c) }

// Valid reports whether c is one of the defined output classes.
func (c AlarmOutputClass) Valid() bool {
	switch c {
	case AlarmOutputClassAcousticSiren, AlarmOutputClassSwitchedSiren, AlarmOutputClassSmokeSounder,
		AlarmOutputClassOpticalSiren, AlarmOutputClassAlarmLight, AlarmOutputClassChirp,
		AlarmOutputClassNotification, AlarmOutputClassSysvarMirror:
		return true
	default:
		return false
	}
}

// Acoustic reports whether the class is an acoustic output that
// counts against the incident's acoustic-seconds ledger and must obey
// the bounded-activation invariant.
func (c AlarmOutputClass) Acoustic() bool {
	switch c {
	case AlarmOutputClassAcousticSiren, AlarmOutputClassSwitchedSiren, AlarmOutputClassSmokeSounder, AlarmOutputClassChirp:
		return true
	default:
		return false
	}
}

// AlarmBlockerPolicy decides how a sensor-health class (open,
// unreachable, sabotage, low battery) affects arming readiness
// (docs/alarm-concept.md §5, §6.3).
type AlarmBlockerPolicy string

// AlarmBlockerPolicy values.
const (
	// AlarmBlockerPolicyBlock fails the arm unless bypassed.
	AlarmBlockerPolicyBlock AlarmBlockerPolicy = "block"
	// AlarmBlockerPolicyWarn arms but raises a warning.
	AlarmBlockerPolicyWarn AlarmBlockerPolicy = "warn"
	// AlarmBlockerPolicyIgnore arms silently.
	AlarmBlockerPolicyIgnore AlarmBlockerPolicy = "ignore"
)

// String returns the wire representation.
func (p AlarmBlockerPolicy) String() string { return string(p) }

// Valid reports whether p is one of the defined blocker policies.
func (p AlarmBlockerPolicy) Valid() bool {
	switch p {
	case AlarmBlockerPolicyBlock, AlarmBlockerPolicyWarn, AlarmBlockerPolicyIgnore:
		return true
	default:
		return false
	}
}

// AlarmJournalClass buckets alarm-journal entries for filtering
// (docs/alarm-concept.md §12.5).
type AlarmJournalClass string

// AlarmJournalClass values.
const (
	// AlarmJournalClassArm covers arm attempts and completions.
	AlarmJournalClassArm AlarmJournalClass = "arm"
	// AlarmJournalClassDisarm covers disarm actions.
	AlarmJournalClassDisarm AlarmJournalClass = "disarm"
	// AlarmJournalClassTrigger covers trigger episodes and sensor
	// events during them.
	AlarmJournalClassTrigger AlarmJournalClass = "trigger"
	// AlarmJournalClassSilence covers silence/acknowledge actions.
	AlarmJournalClassSilence AlarmJournalClass = "silence"
	// AlarmJournalClassBypass covers bypass decisions.
	AlarmJournalClassBypass AlarmJournalClass = "bypass"
	// AlarmJournalClassFault covers health degradations, failed
	// outputs, and recovery anomalies (fail-visible per S7).
	AlarmJournalClassFault AlarmJournalClass = "fault"
	// AlarmJournalClassTest covers test fires and walk tests.
	AlarmJournalClassTest AlarmJournalClass = "test"
	// AlarmJournalClassConfig covers configuration changes.
	AlarmJournalClassConfig AlarmJournalClass = "config"
)

// String returns the wire representation.
func (c AlarmJournalClass) String() string { return string(c) }

// Valid reports whether c is one of the defined journal classes.
func (c AlarmJournalClass) Valid() bool {
	switch c {
	case AlarmJournalClassArm, AlarmJournalClassDisarm, AlarmJournalClassTrigger, AlarmJournalClassSilence,
		AlarmJournalClassBypass, AlarmJournalClassFault, AlarmJournalClassTest, AlarmJournalClassConfig:
		return true
	default:
		return false
	}
}

// AlarmPostTriggerPolicy decides what happens when the trigger time
// of an incident elapses (docs/alarm-concept.md §5).
type AlarmPostTriggerPolicy string

// AlarmPostTriggerPolicy values.
const (
	// AlarmPostTriggerReturnToArmed returns to the armed mode
	// (default; outputs already stopped by their bounds).
	AlarmPostTriggerReturnToArmed AlarmPostTriggerPolicy = "return_to_armed"
	// AlarmPostTriggerDisarm disarms after the trigger time
	// (opt-in "disarm after trigger").
	AlarmPostTriggerDisarm AlarmPostTriggerPolicy = "disarm"
)

// String returns the wire representation.
func (p AlarmPostTriggerPolicy) String() string { return string(p) }

// Valid reports whether p is one of the defined post-trigger policies.
func (p AlarmPostTriggerPolicy) Valid() bool {
	switch p {
	case AlarmPostTriggerReturnToArmed, AlarmPostTriggerDisarm:
		return true
	default:
		return false
	}
}

// AlarmCentralLossPolicy decides how an armed zone reacts when a
// whole central is lost (docs/alarm-concept.md §10.1) — never
// silently.
type AlarmCentralLossPolicy string

// AlarmCentralLossPolicy values.
const (
	// AlarmCentralLossAlert keeps the zone armed, shows degraded
	// coverage, and notifies loudly (default).
	AlarmCentralLossAlert AlarmCentralLossPolicy = "alert"
	// AlarmCentralLossTrigger treats the loss as an activation
	// (paranoid).
	AlarmCentralLossTrigger AlarmCentralLossPolicy = "trigger"
)

// String returns the wire representation.
func (p AlarmCentralLossPolicy) String() string { return string(p) }

// Valid reports whether p is one of the defined central-loss policies.
func (p AlarmCentralLossPolicy) Valid() bool {
	switch p {
	case AlarmCentralLossAlert, AlarmCentralLossTrigger:
		return true
	default:
		return false
	}
}
