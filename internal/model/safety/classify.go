// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package safety

import (
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Classification is the verdict for one data point.
type Classification struct {
	// Class is the hazard/fault taxonomy bucket.
	Class hmenum.SecurityClass
	// Reason narrows a technical fault. It is the empty string for
	// classes that carry no reason facet.
	Reason hmenum.SecurityFaultReason
	// ActiveValues lists the ENUM value labels that count as active.
	// It is nil for BOOL parameters, where truthiness decides.
	//
	// A non-nil list is exhaustive: any value outside it is inactive,
	// including values the device may add in a later firmware. That is
	// the safe direction — an unknown value must not raise an alarm.
	// [ActiveFromRaw] applies that rule; this field is the fallback
	// label set for a source nobody enrolled.
	ActiveValues []string
	// Preferred marks the data point a consumer should enrol when a
	// device offers several sources for the same class. The calculated
	// SMOKE_ALARM is preferred over the raw ENUM status it derives
	// from.
	Preferred bool
}

// smokeActiveValues are the SMOKE_DETECTOR_ALARM_STATUS labels that
// mean "this detector sensed smoke". The set itself lives in
// [hmenum.SmokeDetectorAlarmStatusSmokeLabels], which carries why
// INTRUSION_ALARM is not one of them; the classifier is one of its two
// consumers, not a second definition.
var smokeActiveValues = hmenum.SmokeDetectorAlarmStatusSmokeLabels()

// waterStateActiveValues are the WATERDETECTIONSENSOR STATE labels that
// mean "water present" — value list [DRY, WET, WATER].
var waterStateActiveValues = []string{"WET", "WATER"}

// byChannelAndParameter classifies a data point by its channel type and
// parameter. This is the primary table: the channel type carries the
// device's function, so it disambiguates parameters that are reused
// across unrelated roles.
var byChannelAndParameter = map[channelParam]Classification{
	// Smoke — HmIP-SWSD and the HM-Sec-SD family both expose channel
	// type SMOKE_DETECTOR.
	{"SMOKE_DETECTOR", hmenum.ParameterSmokeAlarm}: {
		Class:     hmenum.SecurityClassSmoke,
		Preferred: true,
	},
	{"SMOKE_DETECTOR", hmenum.ParameterSmokeDetectorAlarmStatus}: {
		Class:        hmenum.SecurityClassSmoke,
		ActiveValues: smokeActiveValues,
	},
	{"SMOKE_DETECTOR", hmenum.ParameterState}: {
		Class:     hmenum.SecurityClassSmoke,
		Preferred: true,
	},

	// Water — HmIP-SWD exposes three independent boolean sources on
	// WATER_DETECTION_TRANSMITTER; the aggregate ORs them.
	{"WATER_DETECTION_TRANSMITTER", hmenum.ParameterAlarmState}: {
		Class:     hmenum.SecurityClassWater,
		Preferred: true,
	},
	{"WATER_DETECTION_TRANSMITTER", hmenum.ParameterMoistureDetected}: {
		Class: hmenum.SecurityClassWater,
	},
	{"WATER_DETECTION_TRANSMITTER", hmenum.ParameterWaterLevelDetected}: {
		Class: hmenum.SecurityClassWater,
	},
	// HM-Sec-WDS reports a three-step ENUM instead.
	{"WATERDETECTIONSENSOR", hmenum.ParameterState}: {
		Class:        hmenum.SecurityClassWater,
		ActiveValues: waterStateActiveValues,
		Preferred:    true,
	},
}

// byParameter classifies a data point by parameter alone. It applies
// only to parameters whose meaning is device-independent — maintenance
// and fault signals that every model reports the same way.
var byParameter = map[hmenum.Parameter]Classification{
	hmenum.ParameterSabotage:              {Class: hmenum.SecurityClassTamper, Reason: hmenum.SecurityFaultReasonTamper},
	hmenum.ParameterSabotageAcceleration:  {Class: hmenum.SecurityClassTamper, Reason: hmenum.SecurityFaultReasonTamper},
	hmenum.ParameterSabotageBattery:       {Class: hmenum.SecurityClassTamper, Reason: hmenum.SecurityFaultReasonTamper},
	hmenum.ParameterSabotageMagneticField: {Class: hmenum.SecurityClassTamper, Reason: hmenum.SecurityFaultReasonTamper},
	hmenum.ParameterSabotageVertical:      {Class: hmenum.SecurityClassTamper, Reason: hmenum.SecurityFaultReasonTamper},

	hmenum.ParameterLowBat: {Class: hmenum.SecurityClassBattery, Reason: hmenum.SecurityFaultReasonLowBattery},

	hmenum.ParameterUnreach:       {Class: hmenum.SecurityClassTechnical, Reason: hmenum.SecurityFaultReasonUnreachable},
	hmenum.ParameterStickyUnreach: {Class: hmenum.SecurityClassTechnical, Reason: hmenum.SecurityFaultReasonUnreachable},

	hmenum.ParameterBlockedPermanent: {Class: hmenum.SecurityClassTechnical, Reason: hmenum.SecurityFaultReasonBlocked},
	hmenum.ParameterBlockedTemporary: {Class: hmenum.SecurityClassTechnical, Reason: hmenum.SecurityFaultReasonBlocked},

	// ERROR_ALARM_TEST and ERROR_SMOKE_CHAMBER are enumerations on the
	// SMOKE_DETECTOR channel: the CCU firmware string table carries them
	// as `SMOKE_DETECTOR|<PARAM>=<VALUE>`. Their idle value is NO_ERROR,
	// which sits at index 0, so the default rule already reads them
	// correctly without a value narrowing.
	hmenum.ParameterErrorAlarmTest:    {Class: hmenum.SecurityClassTechnical, Reason: hmenum.SecurityFaultReasonDeviceError},
	hmenum.ParameterErrorJammed:       {Class: hmenum.SecurityClassTechnical, Reason: hmenum.SecurityFaultReasonDeviceError},
	hmenum.ParameterErrorSmokeChamber: {Class: hmenum.SecurityClassTechnical, Reason: hmenum.SecurityFaultReasonDeviceError},
}

// lowBatAliases are the spellings of the low-battery signal that the
// CCU uses interchangeably across generations.
var lowBatAliases = map[hmenum.Parameter]struct{}{
	"LOWBAT":        {},
	"LOWBAT_SENSOR": {},
}

// dutyCycleAliases are the spellings of the duty-cycle exhaustion
// signal. It is a technical fault rather than a battery one: the device
// is powered but legally barred from transmitting.
var dutyCycleAliases = map[hmenum.Parameter]struct{}{
	"DUTYCYCLE":  {},
	"DUTY_CYCLE": {},
}

// excludedParameters can never be classified, whatever table would
// otherwise match. Three different reasons put an entry here, and the
// admission test for a new one differs per reason — so each entry says
// which it is:
//
//   - WRITTEN: a parameter the alarm engine or an operator writes.
//     Reading it back as a cause turns the domain's own output into an
//     input and produces self-sustaining alarms.
//   - REPORTED: a device-reported reading that describes an actuator's
//     own state rather than a sensed condition. A siren reporting that
//     it is sounding is not evidence of an intrusion.
//   - INERT: not a parameter identifier on any CCU surface, so the key
//     never matches a lookup.
//
// The CCU decides which of the three an entry is, and it says so in the
// parameter's IO operations: WRITTEN entries are registered write-only
// (IOOperations.WRITE), REPORTED entries read+event, with no write bit.
// Do not infer the reason from the parameter's name — the two ALARM
// families read alike and split across both reasons.
//
// Keep this list ahead of every lookup: an exclusion that is merely
// "not in the tables" silently stops holding the moment someone adds a
// broader rule. No entry below is in [byParameter] or
// [byChannelAndParameter] today, so all of them are belt-and-braces.
var excludedParameters = map[hmenum.Parameter]struct{}{
	// REPORTED — the siren's own feedback that it is sounding/flashing.
	// Registered through HMIPServer
	// de.eq3.cbcs.devicedescription.channelspecification.stateparameter.GeneralStateParameterFactory#createReadEventBooleanParameter,
	// i.e. read+event, never written.
	hmenum.ParameterAcousticAlarmActive: {},
	"OPTICAL_ALARM_ACTIVE":              {},
	// WRITTEN — the siren's command half, write-only per HMIPServer
	// de.eq3.cbcs.devicedescription.channelspecification.stateparameter.GeneralStateParameterFactory#createAcousticAlarmSelectionParameter
	// and #createOpticalAlarmSelectionParameter.
	hmenum.ParameterAcousticAlarmSelection: {},
	"OPTICAL_ALARM_SELECTION":              {},
	// WRITTEN — the command half of the smoke-detector chain: writing it
	// makes every detector in the group sound, which is an output, not a
	// detection. Write-only per HMIPServer
	// de.eq3.cbcs.devicedescription.channelspecification.stateparameter.GeneralStateParameterFactory#createSmokeDetectorCommandParameter.
	hmenum.ParameterSmokeDetectorCommand: {},
	// REPORTED — a fallback mode an actuator enters on command loss; the
	// technical class covers the real cause. Read+event per HMIPServer
	// de.eq3.cbcs.devicedescription.channelspecification.StateParameterFactory#createReadEventBooleanIntegerParameter.
	// Note it belongs to neither family this block is organised around:
	// it is declared on the underfloor-heating actuator channels
	// (CLIMATECONTROL_FLOOR_TRANSCEIVER / _PUMP_TRANSCEIVER /
	// _DIRECT_TRANSMITTER), not on a siren or a smoke detector.
	"EMERGENCY_OPERATION": {},
	// INERT — INTRUSION_ALARM is not a parameter identifier on any CCU
	// surface: it is a VALUE of SMOKE_DETECTOR_ALARM_STATUS and a member
	// of the SmokeDetectorCommand vocabulary (the only INTRUSION_ALARM-
	// prefixed parameter the firmware declares is the statistic
	// INTRUSION_ALARM_COUNTER). So this key can never match a lookup, and
	// the exclusion that actually holds is the value-level one:
	// [hmenum.SmokeDetectorAlarmStatusSmokeLabels] leaves INTRUSION_ALARM
	// out of the active set. Kept as a guard for the day a table keys on
	// the name by mistake.
	"INTRUSION_ALARM": {},
}

// channelParam is the composite key of [byChannelAndParameter].
type channelParam struct {
	channelType string
	parameter   hmenum.Parameter
}

// Classify returns the security classification of a data point.
//
// channelType is the CCU channel type (e.g. "WATER_DETECTION_TRANSMITTER");
// model is the device model and is accepted for future model-specific
// overrides — the current tables key on channel type and parameter,
// which is the more stable pair. It returns ok=false when the data
// point carries no security meaning, which is the common case.
func Classify(model, channelType string, parameter hmenum.Parameter) (Classification, bool) {
	if _, excluded := excludedParameters[parameter]; excluded {
		return Classification{}, false
	}
	if c, ok := byChannelAndParameter[channelParam{channelType, parameter}]; ok {
		return c, true
	}
	if c, ok := byParameter[parameter]; ok {
		return c, true
	}
	if _, ok := lowBatAliases[parameter]; ok {
		return Classification{
			Class:  hmenum.SecurityClassBattery,
			Reason: hmenum.SecurityFaultReasonLowBattery,
		}, true
	}
	if _, ok := dutyCycleAliases[parameter]; ok {
		return Classification{
			Class:  hmenum.SecurityClassTechnical,
			Reason: hmenum.SecurityFaultReasonDutyCycle,
		}, true
	}
	// SABOTAGE carries model-specific suffixes beyond the enumerated
	// ones (SABOTAGE_STICKY and friends). The prefix rule is safe
	// because no unrelated parameter starts with it.
	if strings.HasPrefix(string(parameter), "SABOTAGE") {
		return Classification{
			Class:  hmenum.SecurityClassTamper,
			Reason: hmenum.SecurityFaultReasonTamper,
		}, true
	}
	return Classification{}, false
}

// Excluded reports whether parameter is on the actuator-feedback
// exclusion list. Enrolment validation uses it to reject a source that
// would feed the domain its own output.
func Excluded(parameter hmenum.Parameter) bool {
	_, ok := excludedParameters[parameter]
	return ok
}
