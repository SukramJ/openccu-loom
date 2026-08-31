// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

import "strings"

// Parameter is the uppercase wire-level name of a device parameter
// (e.g. "LEVEL", "SET_TEMPERATURE"). The CCU uses these strings in
// every XML-RPC / BIN-RPC / JSON-RPC payload.
type Parameter string

// Parameter constants. Values must match the CCU payload exactly
// code that does `param == "SET_TEMPERATURE"` compares against the
// string form, not the typed constant.
const (
	ParameterAccessAuthorization           Parameter = "ACCESS_AUTHORIZATION"
	ParameterAcousticAlarmActive           Parameter = "ACOUSTIC_ALARM_ACTIVE"
	ParameterAirPressure                   Parameter = "AIR_PRESSURE"
	ParameterAlarmState                    Parameter = "ALARMSTATE"
	ParameterAcousticAlarmSelection        Parameter = "ACOUSTIC_ALARM_SELECTION"
	ParameterAcousticNotificationSelection Parameter = "ACOUSTIC_NOTIFICATION_SELECTION"
	ParameterActiveProfile                 Parameter = "ACTIVE_PROFILE"
	ParameterActivityState                 Parameter = "ACTIVITY_STATE"
	ParameterActualHumidity                Parameter = "ACTUAL_HUMIDITY"
	ParameterActualTemperature             Parameter = "ACTUAL_TEMPERATURE"
	ParameterAutoMode                      Parameter = "AUTO_MODE"
	ParameterAutoRelockState               Parameter = "AUTO_RELOCK_STATE"
	ParameterBatteryState                  Parameter = "BATTERY_STATE"
	ParameterBlockedPermanent              Parameter = "BLOCKED_PERMANENT"
	ParameterBlockedTemporary              Parameter = "BLOCKED_TEMPORARY"
	ParameterBoostMode                     Parameter = "BOOST_MODE"
	ParameterBurstLimitWarning             Parameter = "BURST_LIMIT_WARNING"
	ParameterButtonLock                    Parameter = "BUTTON_LOCK"
	ParameterChannelColor                  Parameter = "CHANNEL_COLOR"
	ParameterChannelLock                   Parameter = "CHANNEL_LOCK"
	ParameterChannelOperationMode          Parameter = "CHANNEL_OPERATION_MODE"
	ParameterCodeID                        Parameter = "CODE_ID"
	ParameterCodeState                     Parameter = "CODE_STATE"
	ParameterColor                         Parameter = "COLOR"
	ParameterColorBehaviour                Parameter = "COLOR_BEHAVIOUR"
	ParameterColorTemperature              Parameter = "COLOR_TEMPERATURE"
	ParameterCombinedParameter             Parameter = "COMBINED_PARAMETER"
	ParameterComfortMode                   Parameter = "COMFORT_MODE"
	ParameterConcentration                 Parameter = "CONCENTRATION"
	ParameterConfigPending                 Parameter = "CONFIG_PENDING"
	ParameterControlMode                   Parameter = "CONTROL_MODE"
	ParameterCurrent                       Parameter = "CURRENT"
	ParameterCurrentIllumination           Parameter = "CURRENT_ILLUMINATION"
	ParameterDeviceOperationMode           Parameter = "DEVICE_OPERATION_MODE"
	ParameterDirection                     Parameter = "DIRECTION"
	ParameterDirtLevel                     Parameter = "DIRT_LEVEL"
	ParameterDisplayDataAlignment          Parameter = "DISPLAY_DATA_ALIGNMENT"
	ParameterDisplayDataBackgroundColor    Parameter = "DISPLAY_DATA_BACKGROUND_COLOR"
	ParameterDisplayDataCommit             Parameter = "DISPLAY_DATA_COMMIT"
	ParameterDisplayDataIcon               Parameter = "DISPLAY_DATA_ICON"
	ParameterDisplayDataID                 Parameter = "DISPLAY_DATA_ID"
	ParameterDisplayDataScrolling          Parameter = "DISPLAY_DATA_SCROLLING"
	ParameterDisplayDataString             Parameter = "DISPLAY_DATA_STRING"
	ParameterDisplayDataTextColor          Parameter = "DISPLAY_DATA_TEXT_COLOR"
	ParameterDoorCommand                   Parameter = "DOOR_COMMAND"
	ParameterDoorState                     Parameter = "DOOR_STATE"
	ParameterDurationUnit                  Parameter = "DURATION_UNIT"
	ParameterDurationValue                 Parameter = "DURATION_VALUE"
	ParameterDutyCycle                     Parameter = "DUTY_CYCLE"
	ParameterDutycycle                     Parameter = "DUTYCYCLE"
	ParameterEffect                        Parameter = "EFFECT"
	ParameterEnergyCounter                 Parameter = "ENERGY_COUNTER"
	ParameterEnergyCounterFeedIn           Parameter = "ENERGY_COUNTER_FEED_IN"
	ParameterError                         Parameter = "ERROR"
	ParameterErrorAlarmTest                Parameter = "ERROR_ALARM_TEST"
	ParameterErrorJammed                   Parameter = "ERROR_JAMMED"
	ParameterErrorSmokeChamber             Parameter = "ERROR_SMOKE_CHAMBER"
	ParameterFrequency                     Parameter = "FREQUENCY"
	ParameterGlobalButtonLock              Parameter = "GLOBAL_BUTTON_LOCK"
	ParameterHeatingCooling                Parameter = "HEATING_COOLING"
	ParameterHeatingValveType              Parameter = "HEATING_VALVE_TYPE"
	ParameterHue                           Parameter = "HUE"
	ParameterHumidity                      Parameter = "HUMIDITY"
	ParameterIllumination                  Parameter = "ILLUMINATION"
	ParameterInhibit                       Parameter = "INHIBIT"
	ParameterInstallTest                   Parameter = "INSTALL_TEST"
	ParameterInterval                      Parameter = "INTERVAL"
	ParameterLEDStatus                     Parameter = "LED_STATUS"
	ParameterLevel                         Parameter = "LEVEL"
	ParameterLevel2                        Parameter = "LEVEL_2"
	ParameterLevelCombined                 Parameter = "LEVEL_COMBINED"
	ParameterLevelReal                     Parameter = "LEVEL_REAL"
	ParameterLevelSlats                    Parameter = "LEVEL_SLATS"
	ParameterLockState                     Parameter = "LOCK_STATE"
	ParameterLockStateReason               Parameter = "LOCK_STATE_REASON"
	ParameterLockTargetLevel               Parameter = "LOCK_TARGET_LEVEL"
	ParameterLowBat                        Parameter = "LOW_BAT"
	ParameterLowBatLimit                   Parameter = "LOW_BAT_LIMIT"
	ParameterLowbat                        Parameter = "LOWBAT"
	ParameterLoweringMode                  Parameter = "LOWERING_MODE"
	ParameterManuMode                      Parameter = "MANU_MODE"
	ParameterMassConcentrationPM10_24H     Parameter = "MASS_CONCENTRATION_PM_10_24H_AVERAGE"
	ParameterMassConcentrationPM1_24H      Parameter = "MASS_CONCENTRATION_PM_1_24H_AVERAGE"
	ParameterMassConcentrationPM25_24H     Parameter = "MASS_CONCENTRATION_PM_2_5_24H_AVERAGE"
	ParameterMinMaxNotRelevantForManuMode  Parameter = "MIN_MAX_VALUE_NOT_RELEVANT_FOR_MANU_MODE"
	ParameterMoistureDetected              Parameter = "MOISTURE_DETECTED"
	ParameterMotion                        Parameter = "MOTION"
	ParameterMotionDetectionActive         Parameter = "MOTION_DETECTION_ACTIVE"
	ParameterOnTime                        Parameter = "ON_TIME"
	ParameterOnTimeList1                   Parameter = "ON_TIME_LIST_1"
	ParameterOnTimeUnit                    Parameter = "ON_TIME_UNIT"
	ParameterOnTimeValue                   Parameter = "ON_TIME_VALUE"
	ParameterOpen                          Parameter = "OPEN"
	ParameterOperatingVoltage              Parameter = "OPERATING_VOLTAGE"
	ParameterOpticalAlarmActive            Parameter = "OPTICAL_ALARM_ACTIVE"
	ParameterOpticalAlarmSelection         Parameter = "OPTICAL_ALARM_SELECTION"
	ParameterOptimumStartStop              Parameter = "OPTIMUM_START_STOP"
	ParameterPartyMode                     Parameter = "PARTY_MODE"
	ParameterPartyModeSubmit               Parameter = "PARTY_MODE_SUBMIT"
	ParameterPartyStartDay                 Parameter = "PARTY_START_DAY"
	ParameterPartyStartTime                Parameter = "PARTY_START_TIME"
	ParameterPartyStopDay                  Parameter = "PARTY_STOP_DAY"
	ParameterPartyStopTime                 Parameter = "PARTY_STOP_TIME"
	ParameterPartyTemperature              Parameter = "PARTY_TEMPERATURE"
	ParameterPartyTimeEnd                  Parameter = "PARTY_TIME_END"
	ParameterPartyTimeStart                Parameter = "PARTY_TIME_START"
	ParameterPermissionState               Parameter = "PERMISSION_STATE"
	ParameterPong                          Parameter = "PONG"
	ParameterPower                         Parameter = "POWER"
	ParameterPresenceDetectionState        Parameter = "PRESENCE_DETECTION_STATE"
	ParameterPress                         Parameter = "PRESS"
	ParameterPressCont                     Parameter = "PRESS_CONT"
	ParameterPressLock                     Parameter = "PRESS_LOCK"
	ParameterPressLong                     Parameter = "PRESS_LONG"
	ParameterPressLongRelease              Parameter = "PRESS_LONG_RELEASE"
	ParameterPressLongStart                Parameter = "PRESS_LONG_START"
	ParameterPressShort                    Parameter = "PRESS_SHORT"
	ParameterPressUnlock                   Parameter = "PRESS_UNLOCK"
	ParameterProgram                       Parameter = "PROGRAM"
	ParameterRampTime                      Parameter = "RAMP_TIME"
	ParameterRampTimeToOffUnit             Parameter = "RAMP_TIME_TO_OFF_UNIT"
	ParameterRampTimeToOffValue            Parameter = "RAMP_TIME_TO_OFF_VALUE"
	ParameterRampTimeUnit                  Parameter = "RAMP_TIME_UNIT"
	ParameterRampTimeValue                 Parameter = "RAMP_TIME_VALUE"
	ParameterRepetitions                   Parameter = "REPETITIONS"
	ParameterResetMotion                   Parameter = "RESET_MOTION"
	ParameterResetPresence                 Parameter = "RESET_PRESENCE"
	ParameterRSSIDevice                    Parameter = "RSSI_DEVICE"
	ParameterRSSIPeer                      Parameter = "RSSI_PEER"
	ParameterRaining                       Parameter = "RAINING"
	ParameterSabotage                      Parameter = "SABOTAGE"
	ParameterSabotageAcceleration          Parameter = "SABOTAGE_ACCELERATION"
	ParameterSabotageBattery               Parameter = "SABOTAGE_BATTERY"
	ParameterSabotageMagneticField         Parameter = "SABOTAGE_MAGNETIC_FIELD"
	ParameterSabotageVertical              Parameter = "SABOTAGE_VERTICAL"
	ParameterSaturation                    Parameter = "SATURATION"
	ParameterSection                       Parameter = "SECTION"
	ParameterSensor                        Parameter = "SENSOR"
	ParameterSensorError                   Parameter = "SENSOR_ERROR"
	ParameterSequenceOK                    Parameter = "SEQUENCE_OK"
	ParameterSetPointMode                  Parameter = "SET_POINT_MODE"
	ParameterSetPointTemperature           Parameter = "SET_POINT_TEMPERATURE"
	ParameterSetTemperature                Parameter = "SET_TEMPERATURE"
	ParameterSetpoint                      Parameter = "SETPOINT"
	ParameterSmokeAlarm                    Parameter = "SMOKE_ALARM"
	ParameterSmokeDetectorAlarmStatus      Parameter = "SMOKE_DETECTOR_ALARM_STATUS"
	ParameterSmokeDetectorCommand          Parameter = "SMOKE_DETECTOR_COMMAND"
	ParameterSmokeLevel                    Parameter = "SMOKE_LEVEL"
	ParameterSoundfile                     Parameter = "SOUNDFILE"
	ParameterState                         Parameter = "STATE"
	ParameterStatusValue                   Parameter = "STATUS"
	ParameterStickyUnreach                 Parameter = "STICKY_UNREACH"
	ParameterStop                          Parameter = "STOP"
	ParameterSunshineDuration              Parameter = "SUNSHINEDURATION"
	ParameterTemperature                   Parameter = "TEMPERATURE"
	ParameterTemperatureMaximum            Parameter = "TEMPERATURE_MAXIMUM"
	ParameterTemperatureMinimum            Parameter = "TEMPERATURE_MINIMUM"
	ParameterTemperatureOffset             Parameter = "TEMPERATURE_OFFSET"
	ParameterTimeOfOperation               Parameter = "TIME_OF_OPERATION"
	ParameterUnreach                       Parameter = "UNREACH"
	ParameterUpdatePending                 Parameter = "UPDATE_PENDING"
	ParameterValveState                    Parameter = "VALVE_STATE"
	ParameterVoltage                       Parameter = "VOLTAGE"
	ParameterWaterFlow                     Parameter = "WATER_FLOW"
	ParameterWaterLevelDetected            Parameter = "WATERLEVEL_DETECTED"
	ParameterWaterVolume                   Parameter = "WATER_VOLUME"
	ParameterWaterVolumeSinceOpen          Parameter = "WATER_VOLUME_SINCE_OPEN"
	ParameterWeekProgramChannelLocks       Parameter = "WEEK_PROGRAM_CHANNEL_LOCKS"
	ParameterWeekProgramPointer            Parameter = "WEEK_PROGRAM_POINTER"
	ParameterWeekProgramTargetChannelLock  Parameter = "WEEK_PROGRAM_TARGET_CHANNEL_LOCK"
	ParameterWeekProgramTargetChannelLocks Parameter = "WEEK_PROGRAM_TARGET_CHANNEL_LOCKS"
	ParameterWindDirection                 Parameter = "WIND_DIRECTION"
	ParameterWindDirectionRange            Parameter = "WIND_DIRECTION_RANGE"
	ParameterWindSpeed                     Parameter = "WIND_SPEED"
	ParameterWorking                       Parameter = "WORKING"
)

// String returns the wire representation.
func (p Parameter) String() string { return string(p) }

// ClickEvents enumerates the parameters that represent a button-press
// event rather than a stateful value. Coordinators route them as
// events instead of value updates.
var ClickEvents = map[Parameter]struct{}{
	ParameterPress:            {},
	ParameterPressCont:        {},
	ParameterPressLock:        {},
	ParameterPressLong:        {},
	ParameterPressLongRelease: {},
	ParameterPressLongStart:   {},
	ParameterPressShort:       {},
	ParameterPressUnlock:      {},
}

// IsClickEvent reports whether p is a click-event parameter.
func (p Parameter) IsClickEvent() bool {
	_, ok := ClickEvents[p]
	return ok
}

// edgeTriggerParameters is the set of parameters whose every emission is
// a discrete edge (a momentary push or an identity token), not a stateful
// value the CCU polls. The CCU frequently re-emits the same wire value for
// these — a keypad user pressing the same key twice, a remote sending
// PRESS_SHORT again — so the event coordinator must NOT collapse an
// unchanged repeat into a no-op: dropping the second identical PRESS_LOCK
// would swallow a real "disarm again" intent. Consumers (the alarm intent
// router) rely on every edge surfacing on the bus.
var edgeTriggerParameters = map[Parameter]struct{}{
	ParameterPress:            {},
	ParameterPressCont:        {},
	ParameterPressLock:        {},
	ParameterPressLong:        {},
	ParameterPressLongRelease: {},
	ParameterPressLongStart:   {},
	ParameterPressShort:       {},
	ParameterPressUnlock:      {},
	ParameterCodeID:           {},
	ParameterCodeState:        {},
}

// secretBearingParameters are the parameters whose VALUE is a credential
// rather than a setting. Their names may be logged and audited freely;
// their values must not be persisted anywhere an operator dump can reach.
//
// CODE_ID carries the access code of a keypad / lock channel. A paramset
// write that sets it therefore puts the code itself into the write
// payload — which is how it reached the append-only audit log in
// cleartext.
var secretBearingParameters = map[Parameter]struct{}{
	ParameterCodeID: {},
}

// IsSecretBearingParameter reports whether p's value is a credential.
// Callers that persist or forward parameter values (audit rows, change
// logs, diagnostics dumps) must record the name and drop the value.
func IsSecretBearingParameter(p Parameter) bool {
	_, ok := secretBearingParameters[p]
	return ok
}

// IsEdgeTriggerParameter reports whether p is an edge-trigger parameter:
// one whose repeated identical emission must still be published as an
// event rather than suppressed by value-unchanged deduplication.
func IsEdgeTriggerParameter(p Parameter) bool {
	_, ok := edgeTriggerParameters[p]
	return ok
}

// DeviceChannel0Parameters lists the parameters the CCU emits only on a
// device's channel 0 (device-level state / diagnostics).
var DeviceChannel0Parameters = map[Parameter]struct{}{
	ParameterUnreach:       {},
	ParameterStickyUnreach: {},
	ParameterLowBat:        {},
	ParameterLowbat:        {},
	ParameterConfigPending: {},
	ParameterUpdatePending: {},
	ParameterRSSIDevice:    {},
	ParameterRSSIPeer:      {},
	ParameterDutyCycle:     {},
	ParameterDutycycle:     {},
}

// RelevantInitParameters lists the channel-0 parameters a bootstrap pass
// must load explicitly, in the order they are fetched. They are a strict
// subset of [DeviceChannel0Parameters] and drive the daemon's availability
// tracking: the bulk device-data fetch does not always include them, so
// without an explicit load the daemon would report "reachable" until the
// first push event ever arrives.
var RelevantInitParameters = []Parameter{
	ParameterConfigPending,
	ParameterStickyUnreach,
	ParameterUnreach,
}

// IsDeviceLevel reports whether p is a channel-0 device-level parameter.
func (p Parameter) IsDeviceLevel() bool {
	_, ok := DeviceChannel0Parameters[p]
	return ok
}

// statusParameterSuffix is the convention the CCU uses to pair a writable
// parameter with its read-back STATUS counterpart. Custom data points use
// this to count optimistic-update confirmations even when the CCU mirrors the
// value back on the status channel.
const statusParameterSuffix = "_STATUS"

// StatusPair returns the wire name of the STATUS counterpart parameter
// (e.g. ParameterLevel → "LEVEL_STATUS"). The boolean reports whether
// p itself looks like a value parameter — STATUS-typed parameters
// (those already ending in "_STATUS") return false because they are
// the *target* of a pairing, not the source.
func (p Parameter) StatusPair() (Parameter, bool) {
	s := string(p)
	if s == "" {
		return "", false
	}
	if hasStatusSuffix(s) {
		return "", false
	}
	return Parameter(s + statusParameterSuffix), true
}

// IsStatusPair reports whether p ends in "_STATUS" — i.e. p is the
// confirmation parameter for some other writable parameter. Use
// [Parameter.BasePair] to recover the source name.
func (p Parameter) IsStatusPair() bool { return hasStatusSuffix(string(p)) }

// BasePair returns the source parameter that p is the STATUS pair of
// (e.g. "LEVEL_STATUS" → ParameterLevel). The boolean reports whether
// p actually had the "_STATUS" suffix; otherwise the call is a no-op
// returning p unchanged.
func (p Parameter) BasePair() (Parameter, bool) {
	s := string(p)
	if !hasStatusSuffix(s) {
		return p, false
	}
	return Parameter(s[:len(s)-len(statusParameterSuffix)]), true
}

// hasStatusSuffix reports whether s ends in "_STATUS". Inlined to avoid
// pulling `strings` into pkg/hmenum just for this check.
func hasStatusSuffix(s string) bool {
	const suf = statusParameterSuffix
	return len(s) > len(suf) && s[len(s)-len(suf):] == suf
}

// OptionalParameters is the set of wire-level parameter names that the CCU
// may legitimately omit or send as an empty value even for numeric types.
//
// Consumers use [Parameter.IsOptional] to decide whether a missing empty wire
// value should be silently dropped (true) or treated as a decode error
// (false). The canonical example is LEVEL_2 for blinds without slats: the CCU
// sends an empty string which must be accepted as "no slat position" rather
// than rejected as malformed data.
var OptionalParameters = map[Parameter]struct{}{
	// Cover / blinds — slat control
	ParameterLevel2: {},
	// Light colour parameters (only on colour-capable lights)
	ParameterColor:            {},
	ParameterColorTemperature: {},
	ParameterEffect:           {},
	ParameterHue:              {},
	ParameterSaturation:       {},
	// Timing / unit selectors (rarely used legacy parameters)
	ParameterDurationUnit:      {},
	ParameterOnTime:            {},
	ParameterOnTimeUnit:        {},
	ParameterRampTime:          {},
	ParameterRampTimeUnit:      {},
	ParameterRampTimeToOffUnit: {},
	// Climate party-mode parameters (only on thermostats that support it)
	ParameterPartyStartDay:    {},
	ParameterPartyStartTime:   {},
	ParameterPartyStopDay:     {},
	ParameterPartyStopTime:    {},
	ParameterPartyTemperature: {},
	// Special features
	ParameterInhibit:     {},
	ParameterInstallTest: {},
}

// IsOptional reports whether the parameter may legitimately carry an
// empty or absent value from the CCU without it being a decode error.
func (p Parameter) IsOptional() bool {
	_, ok := OptionalParameters[p]
	return ok
}

// ignoreOnInitialLoadExact is the exact-match set for parameters that should
// not be fetched on initial device load.
var ignoreOnInitialLoadExact = map[Parameter]struct{}{
	ParameterDutyCycle:        {},
	ParameterDutycycle:        {},
	ParameterLowBat:           {},
	ParameterLowbat:           {},
	ParameterOperatingVoltage: {},
}

// IgnoreOnInitialLoad reports whether fetching this parameter during the
// initial device load should be skipped. Polling these parameters on startup
// wakes battery-powered devices unnecessarily and may exceed duty-cycle
// limits.
//
// Matches when: 1. the parameter is an exact member of
// ignoreOnInitialLoadExact. 2. the parameter name starts with `"ERROR_"` or
// `"RSSI_"`. 3. the parameter name ends with `"_ERROR"`.
func (p Parameter) IgnoreOnInitialLoad() bool {
	if _, ok := ignoreOnInitialLoadExact[p]; ok {
		return true
	}
	s := string(p)
	if strings.HasPrefix(s, "ERROR_") || strings.HasPrefix(s, "RSSI_") {
		return true
	}
	if strings.HasSuffix(s, "_ERROR") {
		return true
	}
	return false
}
