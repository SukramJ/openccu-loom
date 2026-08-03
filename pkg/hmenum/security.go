// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

// SecurityClass is the hazard/fault taxonomy of the Security & Safety
// domain. The string form is wire-stable: it appears in the REST/WS
// API, in MQTT topics (`<base>/security/class/<class>`) and in HA
// entity identifiers, so a value must never be renamed once shipped.
//
// The taxonomy is orthogonal to the alarm engine's sensor types: a
// data point carries a SecurityClass because of what it *measures*,
// independent of whether it is enrolled as an alarm trigger. See
// docs/security-safety-concept.md §3.5.
type SecurityClass string

// SecurityClass values.
const (
	// SecurityClassSmoke covers smoke and fire detection.
	SecurityClassSmoke SecurityClass = "smoke"
	// SecurityClassWater covers leakage, moisture and water level.
	// Rain is deliberately not part of this class — precipitation is
	// weather, not a leak (docs/security-safety-concept.md §6.1).
	SecurityClassWater SecurityClass = "water"
	// SecurityClassGas covers combustible gas detection.
	SecurityClassGas SecurityClass = "gas"
	// SecurityClassCO covers carbon-monoxide detection. It is separate
	// from gas because the escalation differs.
	SecurityClassCO SecurityClass = "co"
	// SecurityClassTamper covers sabotage and physical manipulation.
	SecurityClassTamper SecurityClass = "tamper"
	// SecurityClassBattery covers depleted batteries of
	// security-relevant devices.
	SecurityClassBattery SecurityClass = "battery"
	// SecurityClassTechnical covers technical faults: unreachable
	// devices, blocked actuators, device self-diagnosis errors and the
	// loss of a central.
	SecurityClassTechnical SecurityClass = "technical"
	// SecurityClassIntrusion is the class projection of the alarm
	// engine's intrusion incidents.
	SecurityClassIntrusion SecurityClass = "intrusion"
	// SecurityClassPanic covers panic / hold-up triggers.
	SecurityClassPanic SecurityClass = "panic"
)

// String returns the wire representation.
func (c SecurityClass) String() string { return string(c) }

// Valid reports whether c is one of the defined security classes.
func (c SecurityClass) Valid() bool {
	switch c {
	case SecurityClassSmoke, SecurityClassWater, SecurityClassGas, SecurityClassCO,
		SecurityClassTamper, SecurityClassBattery, SecurityClassTechnical,
		SecurityClassIntrusion, SecurityClassPanic:
		return true
	default:
		return false
	}
}

// Hazard reports whether c is an acute danger to people or property
// rather than a degradation of the installation. Hazard classes drive
// the loud escalation path; the remaining classes feed the fault
// plane.
func (c SecurityClass) Hazard() bool {
	switch c {
	case SecurityClassSmoke, SecurityClassWater, SecurityClassGas, SecurityClassCO,
		SecurityClassIntrusion, SecurityClassPanic:
		return true
	default:
		return false
	}
}

// Diagnostic reports whether c belongs on the fault plane — the
// counterpart of [SecurityClass.Hazard]. Diagnostic classes surface as
// HA entities with `entity_category: diagnostic`.
func (c SecurityClass) Diagnostic() bool { return c.Valid() && !c.Hazard() }

// SecurityClasses returns every defined class in escalation order
// (most severe first). Callers that enumerate classes — discovery,
// REST projections, the aggregator — use this instead of a local slice
// so a new class propagates everywhere.
func SecurityClasses() []SecurityClass {
	return []SecurityClass{
		SecurityClassSmoke, SecurityClassGas, SecurityClassCO,
		SecurityClassIntrusion, SecurityClassPanic, SecurityClassWater,
		SecurityClassTamper, SecurityClassTechnical, SecurityClassBattery,
	}
}

// SecuritySeverity ranks the overall state of the Security & Safety
// domain. The string form is the state of the `security/system/state`
// sensor entity.
type SecuritySeverity string

// SecuritySeverity values, ascending.
const (
	// SecuritySeverityOK means nothing is active and no fault is open.
	SecuritySeverityOK SecuritySeverity = "ok"
	// SecuritySeverityInfo means only informational conditions are
	// present (technical notes, battery warnings).
	SecuritySeverityInfo SecuritySeverity = "info"
	// SecuritySeverityWarning means the installation is degraded:
	// tamper, or the alarm engine reports itself unhealthy.
	SecuritySeverityWarning SecuritySeverity = "warning"
	// SecuritySeverityAlarm means an intrusion, panic or water hazard
	// is active.
	SecuritySeverityAlarm SecuritySeverity = "alarm"
	// SecuritySeverityCritical means a life-safety hazard is active:
	// smoke, gas or carbon monoxide.
	SecuritySeverityCritical SecuritySeverity = "critical"
)

// String returns the wire representation.
func (s SecuritySeverity) String() string { return string(s) }

// Valid reports whether s is one of the defined severities.
func (s SecuritySeverity) Valid() bool { return s.Rank() >= 0 }

// Rank returns the ordinal used to fold many conditions into one
// overall severity; higher wins. An undefined severity ranks -1 so a
// zero value can never outrank a real one.
func (s SecuritySeverity) Rank() int {
	switch s {
	case SecuritySeverityOK:
		return 0
	case SecuritySeverityInfo:
		return 1
	case SecuritySeverityWarning:
		return 2
	case SecuritySeverityAlarm:
		return 3
	case SecuritySeverityCritical:
		return 4
	default:
		return -1
	}
}

// SeverityForClass returns the severity an active source of class c
// contributes. The precedence encoded here is the one the aggregate
// `security/system/state` entity applies
// (docs/security-safety-concept.md §4.1).
func SeverityForClass(c SecurityClass) SecuritySeverity {
	switch c {
	case SecurityClassSmoke, SecurityClassGas, SecurityClassCO:
		return SecuritySeverityCritical
	case SecurityClassIntrusion, SecurityClassPanic, SecurityClassWater:
		return SecuritySeverityAlarm
	case SecurityClassTamper:
		return SecuritySeverityWarning
	case SecurityClassTechnical, SecurityClassBattery:
		return SecuritySeverityInfo
	default:
		return SecuritySeverityOK
	}
}

// SecurityFaultReason narrows why a technical fault is open. It is the
// `reason` facet of a fault entry and of the
// `security/class/technical` attributes.
type SecurityFaultReason string

// SecurityFaultReason values.
const (
	// SecurityFaultReasonUnreachable means the device did not answer
	// within the configured window.
	SecurityFaultReasonUnreachable SecurityFaultReason = "unreachable"
	// SecurityFaultReasonBlocked means an actuator reports itself
	// blocked (BLOCKED_TEMPORARY / BLOCKED_PERMANENT).
	SecurityFaultReasonBlocked SecurityFaultReason = "blocked"
	// SecurityFaultReasonDeviceError means the device reports a
	// self-diagnosis error (a soiled smoke chamber, a failed alarm
	// test, a non-flat mounting position).
	SecurityFaultReasonDeviceError SecurityFaultReason = "device_error"
	// SecurityFaultReasonCentralLost means the central serving this
	// source went away.
	SecurityFaultReasonCentralLost SecurityFaultReason = "central_lost"
	// SecurityFaultReasonDutyCycle means the transmitter exhausted its
	// legally permitted duty cycle and cannot send.
	SecurityFaultReasonDutyCycle SecurityFaultReason = "duty_cycle"
	// SecurityFaultReasonLowBattery means the battery is depleted.
	SecurityFaultReasonLowBattery SecurityFaultReason = "low_battery"
	// SecurityFaultReasonTamper means sabotage was detected.
	SecurityFaultReasonTamper SecurityFaultReason = "tamper"
)

// String returns the wire representation.
func (r SecurityFaultReason) String() string { return string(r) }

// Valid reports whether r is one of the defined fault reasons.
func (r SecurityFaultReason) Valid() bool {
	switch r {
	case SecurityFaultReasonUnreachable, SecurityFaultReasonBlocked,
		SecurityFaultReasonDeviceError, SecurityFaultReasonCentralLost,
		SecurityFaultReasonDutyCycle, SecurityFaultReasonLowBattery,
		SecurityFaultReasonTamper:
		return true
	default:
		return false
	}
}

// SecurityVerb names what happened, independent of the class it
// happened to. It is the second half of a message key
// (`security.message.<class>.<verb>`), so the catalogue needs one entry
// per meaningful pair rather than one per event type.
type SecurityVerb string

// SecurityVerb values.
const (
	// SecurityVerbTriggered means a hazard became active.
	SecurityVerbTriggered SecurityVerb = "triggered"
	// SecurityVerbPreAlarm means a pre-alarm phase started — the alarm
	// is imminent but the full output policy has not fired yet.
	SecurityVerbPreAlarm SecurityVerb = "pre_alarm"
	// SecurityVerbCleared means the condition ended.
	SecurityVerbCleared SecurityVerb = "cleared"
	// SecurityVerbSilenced means an operator silenced a running alarm.
	SecurityVerbSilenced SecurityVerb = "silenced"
	// SecurityVerbFailedToArm means an arm attempt was refused.
	SecurityVerbFailedToArm SecurityVerb = "failed_to_arm"
	// SecurityVerbRaised means a fault opened.
	SecurityVerbRaised SecurityVerb = "raised"
	// SecurityVerbTest is an operator-requested test notification.
	SecurityVerbTest SecurityVerb = "test"
)

// String returns the wire representation.
func (v SecurityVerb) String() string { return string(v) }

// Valid reports whether v is one of the defined verbs.
func (v SecurityVerb) Valid() bool {
	switch v {
	case SecurityVerbTriggered, SecurityVerbPreAlarm, SecurityVerbCleared,
		SecurityVerbSilenced, SecurityVerbFailedToArm, SecurityVerbRaised,
		SecurityVerbTest:
		return true
	default:
		return false
	}
}

// SecurityVerbs returns every defined verb. Callers that enumerate
// verbs — the i18n completeness guard above all — use this so a new
// verb cannot be added without a catalogue entry.
func SecurityVerbs() []SecurityVerb {
	return []SecurityVerb{
		SecurityVerbTriggered, SecurityVerbPreAlarm, SecurityVerbCleared,
		SecurityVerbSilenced, SecurityVerbFailedToArm, SecurityVerbRaised,
		SecurityVerbTest,
	}
}

// DuressVisibility bounds where a duress-code use or a silent panic
// trigger may appear.
//
// The threat model is not that Home Assistant is insecure: it is that
// whoever stands next to you sees the same screen you do. A wall tablet
// in the hallway, or a banner on a lock screen while the attacker
// watches, defeats the covert trigger the feature exists for. But an
// installation that notifies only through Home Assistant and runs no
// webhook would get no duress notification at all under a hidden-only
// policy — a safety function failing silently. The choice therefore
// belongs to the operator, not to the product.
type DuressVisibility string

// DuressVisibility values.
const (
	// DuressVisibilityHidden keeps duress on the webhook and the raw
	// alarm event topic only, reproducing the historical behaviour.
	DuressVisibilityHidden DuressVisibility = "hidden"
	// DuressVisibilityNotifyOnly additionally emits the non-retained
	// notification event, so a phone is reached — but never the
	// retained last-alarm sensor, and never the local screen surfaces.
	// The report reaches the operator without lingering where an
	// attacker could read it.
	DuressVisibilityNotifyOnly DuressVisibility = "notify_only"
	// DuressVisibilityFull treats duress like any other notification,
	// including the retained sensor, the SPA and the WebSocket.
	DuressVisibilityFull DuressVisibility = "full"
)

// String returns the wire representation.
func (d DuressVisibility) String() string { return string(d) }

// Valid reports whether d is one of the defined levels.
func (d DuressVisibility) Valid() bool {
	switch d {
	case DuressVisibilityHidden, DuressVisibilityNotifyOnly, DuressVisibilityFull:
		return true
	default:
		return false
	}
}

// AllowsNotification reports whether the level lets a duress event
// reach the non-retained notification surfaces.
func (d DuressVisibility) AllowsNotification() bool {
	return d == DuressVisibilityNotifyOnly || d == DuressVisibilityFull
}

// AllowsRetained reports whether the level lets a duress event reach
// retained state — the last-alarm sensor and the local screens. Only
// the full level does: a retained value stays readable long after the
// moment has passed.
func (d DuressVisibility) AllowsRetained() bool { return d == DuressVisibilityFull }
