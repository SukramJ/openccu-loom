// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

import "slices"

// SmokeDetectorAlarmStatus is the ENUM vocabulary of the
// SMOKE_DETECTOR_ALARM_STATUS parameter on the SMOKE_DETECTOR channel.
//
// The four constants below are the VALUE_LIST the HmIP-SWSD paramset
// description declares, in its order — read off a captured descriptor
// (channel VCU2822385:1, VALUES / SMOKE_DETECTOR_ALARM_STATUS: TYPE
// ENUM, DEFAULT "IDLE_OFF", VALUE_LIST ["IDLE_OFF", "PRIMARY_ALARM",
// "INTRUSION_ALARM", "SECONDARY_ALARM"]), not inferred. The device's
// own value list is the only authority on this vocabulary; a label no
// paramset carries does not get a constant here, however plausible it
// looks. IDLE_ON is exactly such a label: it appears in no captured
// descriptor, only in hand-written fixtures.
// loom:reachable:reason="the type of the four SMOKE_DETECTOR_ALARM_STATUS constants whose string values build SmokeDetectorAlarmStatusSmokeLabels, read in production by the smoke active-value set in internal/model/safety and by the derived SMOKE_ALARM mapping in internal/model/calculated; a string type whose methods production never calls, which the analyzer's type heuristic cannot see used"
type SmokeDetectorAlarmStatus string

// SmokeDetectorAlarmStatus values, in VALUE_LIST order.
const (
	// SmokeDetectorAlarmStatusIdleOff is the idle state and the
	// parameter's DEFAULT.
	SmokeDetectorAlarmStatusIdleOff SmokeDetectorAlarmStatus = "IDLE_OFF"
	// SmokeDetectorAlarmStatusPrimaryAlarm means this detector itself
	// sensed smoke.
	SmokeDetectorAlarmStatusPrimaryAlarm SmokeDetectorAlarmStatus = "PRIMARY_ALARM"
	// SmokeDetectorAlarmStatusIntrusionAlarm means the installation
	// drove this detector as a siren for an intrusion alarm. It is a
	// command the domain sent, not a detection the device made.
	SmokeDetectorAlarmStatusIntrusionAlarm SmokeDetectorAlarmStatus = "INTRUSION_ALARM"
	// SmokeDetectorAlarmStatusSecondaryAlarm means the detector relays
	// a peer detector's primary alarm.
	SmokeDetectorAlarmStatusSecondaryAlarm SmokeDetectorAlarmStatus = "SECONDARY_ALARM"
)

// smokeDetectorAlarmStatusSmokeLabels is the backing array of
// [SmokeDetectorAlarmStatusSmokeLabels]. It is never handed out
// directly: consumers store the returned slice by reference into
// long-lived state, so a shared backing array would be package-level
// mutable state reachable from every layer.
var smokeDetectorAlarmStatusSmokeLabels = []string{
	string(SmokeDetectorAlarmStatusPrimaryAlarm),
	string(SmokeDetectorAlarmStatusSecondaryAlarm),
}

// SmokeDetectorAlarmStatusSmokeLabels returns the
// SMOKE_DETECTOR_ALARM_STATUS labels that mean "this detector sensed
// smoke", in VALUE_LIST order. Every consumer that decides smoke from
// this parameter reads it here — the derived SMOKE_ALARM binary sensor
// and the safety classifier both do, and they must not disagree about
// the same wire label on the same device.
//
// INTRUSION_ALARM is deliberately absent although it sits at index 2 of
// the VALUE_LIST the HmIP-SWSD paramset declares ([IDLE_OFF,
// PRIMARY_ALARM, INTRUSION_ALARM, SECONDARY_ALARM]) and would therefore
// pass a naive "index != 0" test. It means the opposite of a fire: the
// installation drove this smoke detector as a *siren* for an intrusion
// alarm. Treating it as smoke makes the domain report its own siren
// command as the cause of a fire.
//
// The returned list is also a published and persisted identity — it is
// pre-filled into the operator's sensor enrolment and read back from
// the stored alarm configuration — so its content and order are pinned
// by contract test, not free to be reordered.
func SmokeDetectorAlarmStatusSmokeLabels() []string {
	return slices.Clone(smokeDetectorAlarmStatusSmokeLabels)
}
