// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package parameter

// metadata.go — parameter→quantity/value-behavior lookup tables.
//
// Mechanical port
// Python tuple keys are expanded into one entry per element so that
// all lookups are O(1) map access instead of O(n) tuple iteration.
// Device-and-param overrides use a slice of rules with prefix matching
// because the model-prefix dimension cannot be collapsed into a plain
// map key.

import (
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Metadata holds the semantic classification of a sensor data point.
type Metadata struct {
	Quantity      hmenum.Quantity
	ValueBehavior hmenum.ValueBehavior
}

// ---------------------------------------------------------------------------
// Sensor: parameter → Metadata
// Python tuple keys expanded — each member becomes its own map entry.
// ---------------------------------------------------------------------------

var sensorMetadataByParam = map[string]Metadata{
	// AIR_PRESSURE
	"AIR_PRESSURE": {Quantity: hmenum.QuantityPressure, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// BRIGHTNESS
	"BRIGHTNESS": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// CARRIER_SENSE_LEVEL
	"CARRIER_SENSE_LEVEL": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// CONCENTRATION
	"CONCENTRATION": {Quantity: hmenum.QuantityCO2, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// CURRENT
	"CURRENT": {Quantity: hmenum.QuantityCurrent, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// DEWPOINT
	"DEWPOINT": {Quantity: hmenum.QuantityTemperature, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// ("ACTIVITY_STATE", "DIRECTION")
	"ACTIVITY_STATE": {Quantity: hmenum.QuantityEnum},
	"DIRECTION":      {Quantity: hmenum.QuantityEnum},

	// DOOR_STATE
	"DOOR_STATE": {Quantity: hmenum.QuantityEnum},

	// DUTY_CYCLE_LEVEL
	"DUTY_CYCLE_LEVEL": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// ENERGY_COUNTER
	"ENERGY_COUNTER": {Quantity: hmenum.QuantityEnergy, ValueBehavior: hmenum.ValueBehaviorMonotonic},

	// FILLING_LEVEL
	"FILLING_LEVEL": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// FREQUENCY
	"FREQUENCY": {Quantity: hmenum.QuantityFrequency, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// GAS_ENERGY_COUNTER
	"GAS_ENERGY_COUNTER": {Quantity: hmenum.QuantityGas, ValueBehavior: hmenum.ValueBehaviorMonotonic},

	// GAS_POWER
	"GAS_POWER": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// GAS_FLOW
	"GAS_FLOW": {Quantity: hmenum.QuantityVolumeFlowRate, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// GAS_VOLUME
	"GAS_VOLUME": {Quantity: hmenum.QuantityGas, ValueBehavior: hmenum.ValueBehaviorMonotonic},

	// ("HUMIDITY", "ACTUAL_HUMIDITY")
	"HUMIDITY":        {Quantity: hmenum.QuantityHumidity, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"ACTUAL_HUMIDITY": {Quantity: hmenum.QuantityHumidity, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// IEC_ENERGY_COUNTER
	"IEC_ENERGY_COUNTER": {Quantity: hmenum.QuantityEnergy, ValueBehavior: hmenum.ValueBehaviorMonotonic},

	// IEC_POWER
	"IEC_POWER": {Quantity: hmenum.QuantityPower, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// ("ILLUMINATION", "AVERAGE_ILLUMINATION", "CURRENT_ILLUMINATION",
	//  "HIGHEST_ILLUMINATION", "LOWEST_ILLUMINATION", "LUX")
	"ILLUMINATION":         {Quantity: hmenum.QuantityIlluminance, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"AVERAGE_ILLUMINATION": {Quantity: hmenum.QuantityIlluminance, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"CURRENT_ILLUMINATION": {Quantity: hmenum.QuantityIlluminance, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"HIGHEST_ILLUMINATION": {Quantity: hmenum.QuantityIlluminance, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"LOWEST_ILLUMINATION":  {Quantity: hmenum.QuantityIlluminance, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"LUX":                  {Quantity: hmenum.QuantityIlluminance, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// ("LEVEL", "LEVEL_2")
	"LEVEL":   {ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"LEVEL_2": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// LEVEL_SLATS
	"LEVEL_SLATS": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// LOCK_STATE
	"LOCK_STATE": {Quantity: hmenum.QuantityEnum},

	// ("MASS_CONCENTRATION_PM_1", "MASS_CONCENTRATION_PM_1_24H_AVERAGE")
	"MASS_CONCENTRATION_PM_1":             {Quantity: hmenum.QuantityPM1, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"MASS_CONCENTRATION_PM_1_24H_AVERAGE": {Quantity: hmenum.QuantityPM1, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// ("MASS_CONCENTRATION_PM_10", "MASS_CONCENTRATION_PM_10_24H_AVERAGE")
	"MASS_CONCENTRATION_PM_10":             {Quantity: hmenum.QuantityPM10, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"MASS_CONCENTRATION_PM_10_24H_AVERAGE": {Quantity: hmenum.QuantityPM10, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// ("MASS_CONCENTRATION_PM_2_5", "MASS_CONCENTRATION_PM_2_5_24H_AVERAGE")
	"MASS_CONCENTRATION_PM_2_5":             {Quantity: hmenum.QuantityPM25, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"MASS_CONCENTRATION_PM_2_5_24H_AVERAGE": {Quantity: hmenum.QuantityPM25, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// NUMBER_CONCENTRATION_PM_1
	"NUMBER_CONCENTRATION_PM_1": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// NUMBER_CONCENTRATION_PM_10
	"NUMBER_CONCENTRATION_PM_10": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// NUMBER_CONCENTRATION_PM_2_5
	"NUMBER_CONCENTRATION_PM_2_5": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// TYPICAL_PARTICLE_SIZE
	"TYPICAL_PARTICLE_SIZE": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// ("BATTERY_STATE", "OPERATING_VOLTAGE")
	"BATTERY_STATE":     {Quantity: hmenum.QuantityVoltage, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"OPERATING_VOLTAGE": {Quantity: hmenum.QuantityVoltage, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// OPERATING_VOLTAGE_LEVEL
	"OPERATING_VOLTAGE_LEVEL": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// POWER
	"POWER": {Quantity: hmenum.QuantityPower, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// RAIN_COUNTER
	"RAIN_COUNTER": {ValueBehavior: hmenum.ValueBehaviorMonotonic},

	// ("RSSI_DEVICE", "RSSI_PEER")
	"RSSI_DEVICE": {Quantity: hmenum.QuantitySignalStrength, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"RSSI_PEER":   {Quantity: hmenum.QuantitySignalStrength, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// ("SET_POINT_TEMPERATURE", "SET_TEMPERATURE")
	"SET_POINT_TEMPERATURE": {Quantity: hmenum.QuantityTemperature, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"SET_TEMPERATURE":       {Quantity: hmenum.QuantityTemperature, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// ("ACTUAL_TEMPERATURE", "TEMPERATURE")
	"ACTUAL_TEMPERATURE": {Quantity: hmenum.QuantityTemperature, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"TEMPERATURE":        {Quantity: hmenum.QuantityTemperature, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// SMOKE_DETECTOR_ALARM_STATUS
	"SMOKE_DETECTOR_ALARM_STATUS": {Quantity: hmenum.QuantityEnum},

	// SUNSHINEDURATION
	"SUNSHINEDURATION": {ValueBehavior: hmenum.ValueBehaviorMonotonic},

	// VALUE
	"VALUE": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// VAPOR_CONCENTRATION
	"VAPOR_CONCENTRATION": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// VOLTAGE
	"VOLTAGE": {Quantity: hmenum.QuantityVoltage, ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// ("WIND_DIR", "WIND_DIR_RANGE", "WIND_DIRECTION", "WIND_DIRECTION_RANGE")
	"WIND_DIR":             {ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"WIND_DIR_RANGE":       {ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"WIND_DIRECTION":       {ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"WIND_DIRECTION_RANGE": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},

	// WIND_SPEED
	"WIND_SPEED": {Quantity: hmenum.QuantityWindSpeed, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
}

// ---------------------------------------------------------------------------
// Sensor: (device_model, parameter) → Metadata overrides
// ---------------------------------------------------------------------------

// sensorDeviceParamRule maps a set of model prefixes + a parameter name
// to a Metadata override. Model matching uses strings.HasPrefix
// (case-insensitive) as.
type sensorDeviceParamRule struct {
	ModelPrefixes []string
	Parameter     string
	Metadata      Metadata
}

var sensorMetadataByDeviceAndParam = []sensorDeviceParamRule{
	{
		ModelPrefixes: []string{"HmIP-WKP"},
		Parameter:     "CODE_STATE",
		Metadata:      Metadata{Quantity: hmenum.QuantityEnum},
	},
	{
		ModelPrefixes: []string{"HmIP-SRH", "HM-Sec-RHS", "HM-Sec-xx", "ZEL STG RM FDK"},
		Parameter:     "STATE",
		Metadata:      Metadata{Quantity: hmenum.QuantityEnum},
	},
	{
		ModelPrefixes: []string{"HM-Sec-Win"},
		Parameter:     "STATUS",
		Metadata:      Metadata{Quantity: hmenum.QuantityEnum},
	},
	{
		ModelPrefixes: []string{"HM-Sec-Win"},
		Parameter:     "DIRECTION",
		Metadata:      Metadata{Quantity: hmenum.QuantityEnum},
	},
	{
		ModelPrefixes: []string{"HM-Sec-Win"},
		Parameter:     "ERROR",
		Metadata:      Metadata{Quantity: hmenum.QuantityEnum},
	},
	{
		ModelPrefixes: []string{"HM-Sec-Key"},
		Parameter:     "DIRECTION",
		Metadata:      Metadata{Quantity: hmenum.QuantityEnum},
	},
	{
		ModelPrefixes: []string{"HM-Sec-Key"},
		Parameter:     "ERROR",
		Metadata:      Metadata{Quantity: hmenum.QuantityEnum},
	},
	{
		ModelPrefixes: []string{"HM-CC-RT-DN", "HM-CC-VD"},
		Parameter:     "VALVE_STATE",
		Metadata:      Metadata{ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	},
}

// ---------------------------------------------------------------------------
// Sensor: unit → Metadata fallback
// ---------------------------------------------------------------------------

var sensorMetadataByUnit = map[string]Metadata{
	"%":    {ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"bar":  {Quantity: hmenum.QuantityPressure, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"°C":   {Quantity: hmenum.QuantityTemperature, ValueBehavior: hmenum.ValueBehaviorInstantaneous},
	"g/m³": {ValueBehavior: hmenum.ValueBehaviorInstantaneous},
}

// ---------------------------------------------------------------------------
// Binary sensor: parameter → Quantity
// Python tuple keys expanded.
// ---------------------------------------------------------------------------

var binarySensorQuantityByParam = map[string]hmenum.Quantity{
	// ALARMSTATE
	"ALARMSTATE": hmenum.QuantitySafety,

	// ACOUSTIC_ALARM_ACTIVE
	"ACOUSTIC_ALARM_ACTIVE": hmenum.QuantitySafety,

	// ("BLOCKED_PERMANENT", "BLOCKED_TEMPORARY")
	"BLOCKED_PERMANENT": hmenum.QuantityProblem,
	"BLOCKED_TEMPORARY": hmenum.QuantityProblem,

	// BURST_LIMIT_WARNING
	"BURST_LIMIT_WARNING": hmenum.QuantityProblem,

	// ("DUTYCYCLE", "DUTY_CYCLE")
	"DUTYCYCLE":  hmenum.QuantityProblem,
	"DUTY_CYCLE": hmenum.QuantityProblem,

	// DEW_POINT_ALARM
	"DEW_POINT_ALARM": hmenum.QuantityProblem,

	// EMERGENCY_OPERATION
	"EMERGENCY_OPERATION": hmenum.QuantitySafety,

	// ERROR_JAMMED
	"ERROR_JAMMED": hmenum.QuantityProblem,

	// HEATER_STATE
	"HEATER_STATE": hmenum.QuantityHeat,

	// ("LOWBAT", "LOW_BAT", "LOWBAT_SENSOR")
	"LOWBAT":        hmenum.QuantityBattery,
	"LOW_BAT":       hmenum.QuantityBattery,
	"LOWBAT_SENSOR": hmenum.QuantityBattery,

	// MOISTURE_DETECTED
	"MOISTURE_DETECTED": hmenum.QuantityMoisture,

	// MOTION
	"MOTION": hmenum.QuantityMotion,

	// OPTICAL_ALARM_ACTIVE
	"OPTICAL_ALARM_ACTIVE": hmenum.QuantitySafety,

	// POWER_MAINS_FAILURE
	"POWER_MAINS_FAILURE": hmenum.QuantityProblem,

	// PRESENCE_DETECTION_STATE
	"PRESENCE_DETECTION_STATE": hmenum.QuantityPresence,

	// ("PROCESS", "WORKING")
	"PROCESS": hmenum.QuantityRunning,
	"WORKING": hmenum.QuantityRunning,

	// RAINING
	"RAINING": hmenum.QuantityMoisture,

	// ("SABOTAGE", "SABOTAGE_STICKY")
	"SABOTAGE":        hmenum.QuantityTamper,
	"SABOTAGE_STICKY": hmenum.QuantityTamper,

	// WATERLEVEL_DETECTED
	"WATERLEVEL_DETECTED": hmenum.QuantityMoisture,

	// WINDOW_STATE
	"WINDOW_STATE": hmenum.QuantityWindow,
}

// ---------------------------------------------------------------------------
// Binary sensor: (device_model, parameter) → Quantity overrides
// ---------------------------------------------------------------------------

// binarySensorDeviceParamRule maps a set of model prefixes + a parameter
// name to a Quantity override.
type binarySensorDeviceParamRule struct {
	ModelPrefixes []string
	Parameter     string
	Quantity      hmenum.Quantity
}

var binarySensorQuantityByDeviceAndParam = []binarySensorDeviceParamRule{
	{
		ModelPrefixes: []string{"HmIP-DSD-PCB"},
		Parameter:     "STATE",
		Quantity:      hmenum.QuantityOccupancy,
	},
	{
		ModelPrefixes: []string{"HmIP-SCI", "HmIP-FCI1", "HmIP-FCI6"},
		Parameter:     "STATE",
		Quantity:      hmenum.QuantityOpening,
	},
	{
		ModelPrefixes: []string{"HM-Sec-SD"},
		Parameter:     "STATE",
		Quantity:      hmenum.QuantitySmoke,
	},
	{
		// A door lock's own contact, distinct from a window sensor.
		ModelPrefixes: []string{"HmIP-DLP"},
		Parameter:     "STATE",
		Quantity:      hmenum.QuantityDoor,
	},
	{
		// Rotary handle sensors report the sash through WINDOW_OPEN
		// rather than STATE.
		ModelPrefixes: []string{"HmIP-SRH", "HM-Sec-RHS"},
		Parameter:     "WINDOW_OPEN",
		Quantity:      hmenum.QuantityWindow,
	},
	{
		ModelPrefixes: []string{"HmIP-SWSD"},
		Parameter:     "SMOKE_ALARM",
		Quantity:      hmenum.QuantitySmoke,
	},
	{
		// The siren's intrusion channel is a safety condition, not a
		// smoke one, on the same device.
		ModelPrefixes: []string{"HmIP-SWSD"},
		Parameter:     "INTRUSION_ALARM",
		Quantity:      hmenum.QuantitySafety,
	},
	{
		ModelPrefixes: []string{
			"HmIP-SWD",
			"HmIP-SWDO",
			"HmIP-SWDM",
			"HM-Sec-SC",
			"HM-SCI-3-FM",
			"ZEL STG RM FFK",
		},
		Parameter: "STATE",
		Quantity:  hmenum.QuantityWindow,
	},
	{
		ModelPrefixes: []string{"HM-Sen-RD-O"},
		Parameter:     "STATE",
		Quantity:      hmenum.QuantityMoisture,
	},
	{
		ModelPrefixes: []string{"HM-Sec-Win"},
		Parameter:     "WORKING",
		Quantity:      hmenum.QuantityRunning,
	},
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// modelMatchesAny reports whether deviceModel has any of the given prefixes
// (case-insensitive).
func modelMatchesAny(deviceModel string, prefixes []string) bool {
	upper := strings.ToUpper(deviceModel)
	for _, p := range prefixes {
		if strings.HasPrefix(upper, strings.ToUpper(p)) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Public lookup functions
// ---------------------------------------------------------------------------

// MetadataByParam returns the Metadata for the given parameter name.
// Parameter comparison is case-insensitive. Returns zero Metadata{} if
// no entry is found.
func MetadataByParam(parameter string) Metadata {
	return sensorMetadataByParam[strings.ToUpper(parameter)]
}

// MetadataByDeviceAndParam returns the Metadata override for the given
// (deviceModel, parameter) pair. Model matching uses a case-insensitive
// prefix check; parameter matching is case-insensitive equality.
// Returns zero Metadata{} if no rule matches.
func MetadataByDeviceAndParam(deviceModel, parameter string) Metadata {
	for _, rule := range sensorMetadataByDeviceAndParam {
		if strings.EqualFold(rule.Parameter, parameter) && modelMatchesAny(deviceModel, rule.ModelPrefixes) {
			return rule.Metadata
		}
	}
	return Metadata{}
}

// MetadataByUnit returns the Metadata for the given unit string (exact
// match). Returns zero Metadata{} if no entry is found.
func MetadataByUnit(unit string) Metadata {
	return sensorMetadataByUnit[unit]
}

// MetadataFor resolves Metadata using the three-tier precedence:
//  1. MetadataByDeviceAndParam (device-specific override)
//  2. MetadataByParam (parameter name)
//  3. MetadataByUnit (unit fallback)
//
// Returns zero Metadata{} if nothing matches.
func MetadataFor(deviceModel, parameter, unit string) Metadata {
	if md := MetadataByDeviceAndParam(deviceModel, parameter); md != (Metadata{}) {
		return md
	}
	if md := MetadataByParam(parameter); md != (Metadata{}) {
		return md
	}
	return MetadataByUnit(unit)
}

// BinarySensorQuantityByParam returns the Quantity for the given binary
// sensor parameter. Comparison is case-insensitive. Returns
// hmenum.QuantityNone if no entry is found.
func BinarySensorQuantityByParam(parameter string) hmenum.Quantity {
	if q, ok := binarySensorQuantityByParam[strings.ToUpper(parameter)]; ok {
		return q
	}
	return hmenum.QuantityNone
}

// BinarySensorQuantityByDeviceAndParam returns the Quantity override for
// the given (deviceModel, parameter) pair. Model matching uses a
// case-insensitive prefix check; parameter matching is case-insensitive
// equality. Returns hmenum.QuantityNone if no rule matches.
func BinarySensorQuantityByDeviceAndParam(deviceModel, parameter string) hmenum.Quantity {
	for _, rule := range binarySensorQuantityByDeviceAndParam {
		if strings.EqualFold(rule.Parameter, parameter) && modelMatchesAny(deviceModel, rule.ModelPrefixes) {
			return rule.Quantity
		}
	}
	return hmenum.QuantityNone
}

// BinarySensorQuantityFor resolves a binary sensor Quantity using the
// two-tier precedence:
//  1. BinarySensorQuantityByDeviceAndParam (device-specific override)
//  2. BinarySensorQuantityByParam (parameter name)
//
// Returns hmenum.QuantityNone if nothing matches.
func BinarySensorQuantityFor(deviceModel, parameter string) hmenum.Quantity {
	if q := BinarySensorQuantityByDeviceAndParam(deviceModel, parameter); q != hmenum.QuantityNone {
		return q
	}
	return BinarySensorQuantityByParam(parameter)
}
