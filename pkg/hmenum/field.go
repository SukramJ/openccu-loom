// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

// Field is the semantic-role name of a custom data-point field.
// Values are lowercase strings that mirror the
// Exactly — including the few cases
// where name and value diverge (e.g. COLOR_LEVEL = "color_temp",
// ON_TIME_LIST = "on_time_list_1", OPERATION_MODE = "channel_operation_mode",
// SWITCH_V1 = "vswitch_1", SWITCH_V2 = "vswitch_2").
type Field string

// Field constants. Values match the
const (
	FieldAcousticAlarmActive               Field = "acoustic_alarm_active"
	FieldAcousticAlarmSelection            Field = "acoustic_alarm_selection"
	FieldAcousticNotificationSelection     Field = "acoustic_notification_selection"
	FieldActiveProfile                     Field = "active_profile"
	FieldActivityState                     Field = "activity_state"
	FieldAutoMode                          Field = "auto_mode"
	FieldBoostMode                         Field = "boost_mode"
	FieldBurstLimitWarning                 Field = "burst_limit_warning"
	FieldButtonLock                        Field = "button_lock"
	FieldChannelColor                      Field = "channel_color"
	FieldColor                             Field = "color"
	FieldColorBehaviour                    Field = "color_behaviour"
	FieldColorLevel                        Field = "color_temp" // Python name: COLOR_LEVEL; value deviates
	FieldColorTemperature                  Field = "color_temperature"
	FieldCombinedParameter                 Field = "combined_parameter"
	FieldComfortMode                       Field = "comfort_mode"
	FieldConcentration                     Field = "concentration"
	FieldControlMode                       Field = "control_mode"
	FieldCurrent                           Field = "current"
	FieldDeviceOperationMode               Field = "device_operation_mode"
	FieldDirection                         Field = "direction"
	FieldDisplayDataAlignment              Field = "display_data_alignment"
	FieldDisplayDataBackgroundColor        Field = "display_data_background_color"
	FieldDisplayDataCommit                 Field = "display_data_commit"
	FieldDisplayDataIcon                   Field = "display_data_icon"
	FieldDisplayDataID                     Field = "display_data_id"
	FieldDisplayDataString                 Field = "display_data_string"
	FieldDisplayDataTextColor              Field = "display_data_text_color"
	FieldDoorCommand                       Field = "door_command"
	FieldDoorState                         Field = "door_state"
	FieldDuration                          Field = "duration"
	FieldDurationUnit                      Field = "duration_unit"
	FieldDurationValue                     Field = "duration_value"
	FieldDutycycle                         Field = "dutycycle"
	FieldDutyCycle                         Field = "duty_cycle"
	FieldEffect                            Field = "effect"
	FieldEnergyCounter                     Field = "energy_counter"
	FieldError                             Field = "error"
	FieldFrequency                         Field = "frequency"
	FieldGroupLevel                        Field = "group_level"
	FieldGroupLevel2                       Field = "group_level_2"
	FieldGroupState                        Field = "group_state"
	FieldHeatingCooling                    Field = "heating_cooling"
	FieldHeatingValveType                  Field = "heating_valve_type"
	FieldHue                               Field = "hue"
	FieldHumidity                          Field = "humidity"
	FieldInhibit                           Field = "inhibit"
	FieldInterval                          Field = "interval"
	FieldLevel                             Field = "level"
	FieldLevel2                            Field = "level_2"
	FieldLevelCombined                     Field = "level_combined"
	FieldLockState                         Field = "lock_state"
	FieldLockTargetLevel                   Field = "lock_target_level"
	FieldLowbat                            Field = "lowbat"
	FieldLowBat                            Field = "low_bat"
	FieldLowBatLimit                       Field = "low_bat_limit"
	FieldLoweringMode                      Field = "lowering_mode"
	FieldManuMode                          Field = "manu_mode"
	FieldMinMaxValueNotRelevantForManuMode Field = "min_max_value_not_relevant_for_manu_mode"
	FieldOnTimeList                        Field = "on_time_list_1" // Python name: ON_TIME_LIST; value deviates
	FieldOnTimeUnit                        Field = "on_time_unit"
	FieldOnTimeValue                       Field = "on_time_value"
	FieldOpen                              Field = "open"
	FieldOperatingVoltage                  Field = "operating_voltage"
	FieldOperationMode                     Field = "channel_operation_mode" // Python name: OPERATION_MODE; value deviates
	FieldOpticalAlarmActive                Field = "optical_alarm_active"
	FieldOpticalAlarmSelection             Field = "optical_alarm_selection"
	FieldOptimumStartStop                  Field = "optimum_start_stop"
	FieldPartyMode                         Field = "party_mode"
	FieldPower                             Field = "power"
	FieldProgram                           Field = "program"
	FieldRampTimeToOffUnit                 Field = "ramp_time_to_off_unit"
	FieldRampTimeToOffValue                Field = "ramp_time_to_off_value"
	FieldRampTimeUnit                      Field = "ramp_time_unit"
	FieldRampTimeValue                     Field = "ramp_time_value"
	FieldRepetitions                       Field = "repetitions"
	FieldRssiDevice                        Field = "rssi_device"
	FieldRssiPeer                          Field = "rssi_peer"
	FieldSabotage                          Field = "sabotage"
	FieldSaturation                        Field = "saturation"
	FieldSection                           Field = "section"
	FieldSetpoint                          Field = "setpoint"
	FieldSetPointMode                      Field = "set_point_mode"
	FieldSmokeDetectorAlarmStatus          Field = "smoke_detector_alarm_status"
	FieldSmokeDetectorCommand              Field = "smoke_detector_command"
	FieldSoundfile                         Field = "soundfile"
	FieldState                             Field = "state"
	FieldStop                              Field = "stop"
	FieldSwitchMain                        Field = "switch_main"
	FieldSwitchV1                          Field = "vswitch_1" // Python name: SWITCH_V1; value deviates
	FieldSwitchV2                          Field = "vswitch_2" // Python name: SWITCH_V2; value deviates
	FieldTemperature                       Field = "temperature"
	FieldTemperatureMaximum                Field = "temperature_maximum"
	FieldTemperatureMinimum                Field = "temperature_minimum"
	FieldTemperatureOffset                 Field = "temperature_offset"
	FieldValveState                        Field = "valve_state"
	FieldVoltage                           Field = "voltage"
	FieldWeekProgramPointer                Field = "week_program_pointer"
)

// allFields is the canonical list of every Field constant.
// Used by AllFields() and coverage tests.
var allFields = []Field{
	FieldAcousticAlarmActive,
	FieldAcousticAlarmSelection,
	FieldAcousticNotificationSelection,
	FieldActiveProfile,
	FieldActivityState,
	FieldAutoMode,
	FieldBoostMode,
	FieldBurstLimitWarning,
	FieldButtonLock,
	FieldChannelColor,
	FieldColor,
	FieldColorBehaviour,
	FieldColorLevel,
	FieldColorTemperature,
	FieldCombinedParameter,
	FieldComfortMode,
	FieldConcentration,
	FieldControlMode,
	FieldCurrent,
	FieldDeviceOperationMode,
	FieldDirection,
	FieldDisplayDataAlignment,
	FieldDisplayDataBackgroundColor,
	FieldDisplayDataCommit,
	FieldDisplayDataIcon,
	FieldDisplayDataID,
	FieldDisplayDataString,
	FieldDisplayDataTextColor,
	FieldDoorCommand,
	FieldDoorState,
	FieldDuration,
	FieldDurationUnit,
	FieldDurationValue,
	FieldDutycycle,
	FieldDutyCycle,
	FieldEffect,
	FieldEnergyCounter,
	FieldError,
	FieldFrequency,
	FieldGroupLevel,
	FieldGroupLevel2,
	FieldGroupState,
	FieldHeatingCooling,
	FieldHeatingValveType,
	FieldHue,
	FieldHumidity,
	FieldInhibit,
	FieldInterval,
	FieldLevel,
	FieldLevel2,
	FieldLevelCombined,
	FieldLockState,
	FieldLockTargetLevel,
	FieldLowbat,
	FieldLowBat,
	FieldLowBatLimit,
	FieldLoweringMode,
	FieldManuMode,
	FieldMinMaxValueNotRelevantForManuMode,
	FieldOnTimeList,
	FieldOnTimeUnit,
	FieldOnTimeValue,
	FieldOpen,
	FieldOperatingVoltage,
	FieldOperationMode,
	FieldOpticalAlarmActive,
	FieldOpticalAlarmSelection,
	FieldOptimumStartStop,
	FieldPartyMode,
	FieldPower,
	FieldProgram,
	FieldRampTimeToOffUnit,
	FieldRampTimeToOffValue,
	FieldRampTimeUnit,
	FieldRampTimeValue,
	FieldRepetitions,
	FieldRssiDevice,
	FieldRssiPeer,
	FieldSabotage,
	FieldSaturation,
	FieldSection,
	FieldSetpoint,
	FieldSetPointMode,
	FieldSmokeDetectorAlarmStatus,
	FieldSmokeDetectorCommand,
	FieldSoundfile,
	FieldState,
	FieldStop,
	FieldSwitchMain,
	FieldSwitchV1,
	FieldSwitchV2,
	FieldTemperature,
	FieldTemperatureMaximum,
	FieldTemperatureMinimum,
	FieldTemperatureOffset,
	FieldValveState,
	FieldVoltage,
	FieldWeekProgramPointer,
}

// AllFields returns a slice of every defined Field constant.
// Callers must not mutate the returned slice.
func AllFields() []Field {
	return allFields
}
