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
