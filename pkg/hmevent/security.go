// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmevent

import (
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// SecuritySourceRef identifies one data point that contributed to a
// security event — the sensor that triggered an alarm, the source that
// blocked an arm, the device that reported a fault.
//
// It is the single identity currency of the Security & Safety domain.
// Before it existed, an alarm carried only a sensor ID and a display
// name, so a consumer could render "Flur EG triggered" but could not
// link back to the device, could not tell two centrals apart, and could
// not group by hazard class.
//
// Every field is populated at publish time from the enrolled sensor
// row, so a later rename or un-enrolment does not rewrite history.
type SecuritySourceRef struct {
	// Ref is the routing key `<central>|<interface_id>|<channel_address>|<parameter>`.
	// It is the deduplication key and matches the alarm input index, so
	// two centrals with identical channel addresses never collide.
	Ref string
	// Central names the CCU the source belongs to. Mandatory: the
	// domain aggregates across centrals, so an unqualified address is
	// ambiguous.
	Central string
	// InterfaceID is the southbound interface (HmIP-RF, BidCos-RF, …).
	InterfaceID string
	// ChannelAddress is the CCU channel address (`ABC0123456:1`).
	ChannelAddress string
	// DeviceAddress is ChannelAddress without the channel suffix — the
	// anchor for a deep link into the device view.
	DeviceAddress string
	// Parameter is the data point name (`STATE`, `MOISTURE_DETECTED`).
	Parameter string
	// SensorID is the enrolled alarm-sensor row ID, empty when the
	// source is not an enrolled alarm sensor (a fault source need not
	// be).
	SensorID string
	// Name is the display name at publish time.
	Name string
	// SensorType is the alarm role, empty for non-enrolled sources.
	SensorType hmenum.AlarmSensorType
	// Class is the hazard/fault classification, empty when the source
	// carries no security classification.
	Class hmenum.SecurityClass
	// AtMS is the observation time in Unix milliseconds.
	AtMS int64
}

// NewSecuritySourceRef builds a reference and derives Ref and
// DeviceAddress from the identity components, so no caller has to
// reproduce the key format.
func NewSecuritySourceRef(central, interfaceID, channelAddress, parameter string) SecuritySourceRef {
	return SecuritySourceRef{
		Ref:            SecurityRefKey(central, interfaceID, channelAddress, parameter),
		Central:        central,
		InterfaceID:    interfaceID,
		ChannelAddress: channelAddress,
		DeviceAddress:  SecurityDeviceAddress(channelAddress),
		Parameter:      parameter,
	}
}

// SecurityRefKey builds the routing key of a data point. The format is
// shared with the alarm input index; changing it in one place without
// the other silently breaks source deduplication.
func SecurityRefKey(central, interfaceID, channelAddress, parameter string) string {
	return central + "|" + interfaceID + "|" + channelAddress + "|" + parameter
}

// SecurityDeviceAddress strips the channel suffix from a channel
// address. An address without a suffix is returned unchanged.
func SecurityDeviceAddress(channelAddress string) string {
	if i := strings.IndexByte(channelAddress, ':'); i >= 0 {
		return channelAddress[:i]
	}
	return channelAddress
}

// Empty reports whether the reference carries no identity at all.
func (r SecuritySourceRef) Empty() bool { return r.Ref == "" }

// AlarmBlockerReason names why a source blocks or warns about arming.
// The string form reaches the north-bound surface, so it is
// wire-stable.
type AlarmBlockerReason string

// AlarmBlockerReason values. They mirror the four blocker policies the
// readiness computation evaluates.
const (
	// AlarmBlockerReasonOpen means the sensor is currently active
	// (a contact stands open, a motion detector sees movement).
	AlarmBlockerReasonOpen AlarmBlockerReason = "open"
	// AlarmBlockerReasonUnreachable means the sensor did not answer.
	AlarmBlockerReasonUnreachable AlarmBlockerReason = "unreachable"
	// AlarmBlockerReasonSabotage means the sensor reports tampering.
	AlarmBlockerReasonSabotage AlarmBlockerReason = "sabotage"
	// AlarmBlockerReasonLowBattery means the sensor's battery is
	// depleted.
	AlarmBlockerReasonLowBattery AlarmBlockerReason = "low_battery"
	// AlarmBlockerReasonBypassed means the sensor is excluded from the
	// arm by operator choice; it warns but never blocks.
	AlarmBlockerReasonBypassed AlarmBlockerReason = "bypassed"
)

// String returns the wire representation.
func (r AlarmBlockerReason) String() string { return string(r) }

// Valid reports whether r is one of the defined reasons.
func (r AlarmBlockerReason) Valid() bool {
	switch r {
	case AlarmBlockerReasonOpen, AlarmBlockerReasonUnreachable, AlarmBlockerReasonSabotage,
		AlarmBlockerReasonLowBattery, AlarmBlockerReasonBypassed:
		return true
	default:
		return false
	}
}

// AlarmBlockerDetail is one reason a sensor blocks or warns about
// arming a mode.
//
// A sensor can contribute several reasons at once — unreachable *and*
// low battery — so the detail list may carry more than one entry per
// sensor. That is the point: the flat sensor-ID list it accompanies
// deduplicates the sensor and drops the reason entirely, which is why
// a client cannot answer "why can I not arm?" from it.
type AlarmBlockerDetail struct {
	// SensorID is the enrolled sensor row ID.
	SensorID string
	// Name is the sensor display name.
	Name string
	// Source carries the full data-point identity, so a client can
	// deep-link to the device that blocks the arm.
	Source SecuritySourceRef
	// Reason names the condition.
	Reason AlarmBlockerReason
	// Blocking reports whether this reason prevents the arm (false
	// means it only warns — a warn policy, an auto-bypass, or an
	// operator bypass).
	Blocking bool
}

// Security & Safety event tags. The namespace is `security.` on both
// the internal bus and the WebSocket, unlike the alarm domain where the
// two diverged historically — one vocabulary from the start costs
// nothing and saves a translation layer.
const (
	EventTypeSecurityStateChanged EventType = "security.state_changed"
	EventTypeSecurityClassChanged EventType = "security.class_changed"
	EventTypeSecurityZoneChanged  EventType = "security.zone_changed"
	EventTypeSecurityFaultChanged EventType = "security.fault_changed"
	EventTypeSecurityNotification EventType = "security.notification"
)

// SecurityStateChangedEvent fires when the folded severity of the
// domain changes. It carries only the fold; consumers that need the
// detail read the snapshot.
type SecurityStateChangedEvent struct {
	Base
	From hmenum.SecuritySeverity
	To   hmenum.SecuritySeverity
	// ActiveClasses names the classes contributing to To.
	ActiveClasses []hmenum.SecurityClass
	// OpenFaults counts the standing faults.
	OpenFaults int
}

// Type implements Event.
func (SecurityStateChangedEvent) Type() EventType { return EventTypeSecurityStateChanged }

// SecurityClassChangedEvent fires when one hazard or fault class
// becomes active or inactive, or when its source set changes while
// active — a second smoke detector joining an existing fire is a
// change worth announcing even though the class was already on.
type SecurityClassChangedEvent struct {
	Base
	Class   hmenum.SecurityClass
	Active  bool
	Sources []SecuritySourceRef
	// Centrals names the centrals contributing active sources.
	Centrals []string
	SinceMS  int64
}

// Type implements Event.
func (SecurityClassChangedEvent) Type() EventType { return EventTypeSecurityClassChanged }

// SecurityZoneChangedEvent fires when a zone's security view changes.
type SecurityZoneChangedEvent struct {
	Base
	ZoneID   string
	ZoneSlug string
	ZoneName string
	State    hmenum.AlarmZoneState
	Mode     hmenum.AlarmMode
	Sources  []SecuritySourceRef
	// ByClass groups active source names per class.
	ByClass    map[hmenum.SecurityClass][]string
	IncidentID int64
}

// Type implements Event.
func (SecurityZoneChangedEvent) Type() EventType { return EventTypeSecurityZoneChanged }

// SecurityFaultChangedEvent fires when a fault opens, clears or is
// acknowledged.
type SecurityFaultChangedEvent struct {
	Base
	FaultID  string
	Class    hmenum.SecurityClass
	Reason   hmenum.SecurityFaultReason
	Severity hmenum.SecuritySeverity
	Source   SecuritySourceRef
	// Open reports the direction: true when raised, false when cleared.
	Open bool
	// Acknowledged marks an acknowledgement rather than a state change.
	Acknowledged bool
	SinceMS      int64
	// OpenCount is the standing fault count after this change.
	OpenCount int
}

// Type implements Event.
func (SecurityFaultChangedEvent) Type() EventType { return EventTypeSecurityFaultChanged }

// SecurityNotificationEvent is one rendered report — the event a
// messenger integration consumes.
//
// It is the only event in the domain that carries prose. Everything
// else carries facts; this carries facts plus a sentence built from
// them, because the alternative is every consumer reinventing German
// and English alarm wording.
type SecurityNotificationEvent struct {
	Base
	Class    hmenum.SecurityClass
	Severity hmenum.SecuritySeverity
	Verb     hmenum.SecurityVerb
	Subject  string
	Message  string
	// I18nKey and Args let a consumer render in its own locale.
	I18nKey    string
	Args       map[string]string
	Sources    []SecuritySourceRef
	ZoneID     string
	ZoneSlug   string
	ZoneName   string
	Mode       hmenum.AlarmMode
	IncidentID int64
	Link       string
	// AtMS is when the report was produced.
	//
	// Both retained report entities declare device_class timestamp, so
	// without it they stay permanently unknown and log a warning on
	// every publish — the attributes arrive, the state never does.
	AtMS int64
	// Fault marks a fault report rather than a hazard report, so a
	// consumer can route the two to different destinations without
	// inspecting the class.
	Fault bool
	// Retainable reports whether the report may be written to retained
	// state. A covert-trigger report is delivered but never retained.
	Retainable bool
}

// Type implements Event.
func (SecurityNotificationEvent) Type() EventType { return EventTypeSecurityNotification }
