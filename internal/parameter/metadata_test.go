// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package parameter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// MetadataByParam
// ---------------------------------------------------------------------------

func TestMetadataByParam(t *testing.T) {
	t.Parallel()

	tests := []struct {
		param        string
		wantQuantity hmenum.Quantity
		wantBehavior hmenum.ValueBehavior
	}{
		// Simple entries
		{"ACTUAL_TEMPERATURE", hmenum.QuantityTemperature, hmenum.ValueBehaviorInstantaneous},
		{"AIR_PRESSURE", hmenum.QuantityPressure, hmenum.ValueBehaviorInstantaneous},
		{"BRIGHTNESS", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"CARRIER_SENSE_LEVEL", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"CONCENTRATION", hmenum.QuantityCO2, hmenum.ValueBehaviorInstantaneous},
		{"CURRENT", hmenum.QuantityCurrent, hmenum.ValueBehaviorInstantaneous},
		{"DEWPOINT", hmenum.QuantityTemperature, hmenum.ValueBehaviorInstantaneous},
		{"DOOR_STATE", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},
		{"DUTY_CYCLE_LEVEL", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"ENERGY_COUNTER", hmenum.QuantityEnergy, hmenum.ValueBehaviorMonotonic},
		{"FILLING_LEVEL", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"FREQUENCY", hmenum.QuantityFrequency, hmenum.ValueBehaviorInstantaneous},
		{"GAS_ENERGY_COUNTER", hmenum.QuantityGas, hmenum.ValueBehaviorMonotonic},
		{"GAS_FLOW", hmenum.QuantityVolumeFlowRate, hmenum.ValueBehaviorInstantaneous},
		{"GAS_VOLUME", hmenum.QuantityGas, hmenum.ValueBehaviorMonotonic},
		{"IEC_ENERGY_COUNTER", hmenum.QuantityEnergy, hmenum.ValueBehaviorMonotonic},
		{"IEC_POWER", hmenum.QuantityPower, hmenum.ValueBehaviorInstantaneous},
		{"LOCK_STATE", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},
		{"POWER", hmenum.QuantityPower, hmenum.ValueBehaviorInstantaneous},
		{"RAIN_COUNTER", hmenum.QuantityNone, hmenum.ValueBehaviorMonotonic},
		{"SMOKE_DETECTOR_ALARM_STATUS", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},
		{"SUNSHINEDURATION", hmenum.QuantityNone, hmenum.ValueBehaviorMonotonic},
		{"TEMPERATURE", hmenum.QuantityTemperature, hmenum.ValueBehaviorInstantaneous},
		{"VALUE", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"VAPOR_CONCENTRATION", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"VOLTAGE", hmenum.QuantityVoltage, hmenum.ValueBehaviorInstantaneous},
		{"WIND_SPEED", hmenum.QuantityWindSpeed, hmenum.ValueBehaviorInstantaneous},

		// Expanded tuple: ("ACTIVITY_STATE", "DIRECTION")
		{"ACTIVITY_STATE", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},
		{"DIRECTION", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},

		// Expanded tuple: ("HUMIDITY", "ACTUAL_HUMIDITY")
		{"HUMIDITY", hmenum.QuantityHumidity, hmenum.ValueBehaviorInstantaneous},
		{"ACTUAL_HUMIDITY", hmenum.QuantityHumidity, hmenum.ValueBehaviorInstantaneous},

		// Expanded tuple: illumination group
		{"ILLUMINATION", hmenum.QuantityIlluminance, hmenum.ValueBehaviorInstantaneous},
		{"AVERAGE_ILLUMINATION", hmenum.QuantityIlluminance, hmenum.ValueBehaviorInstantaneous},
		{"CURRENT_ILLUMINATION", hmenum.QuantityIlluminance, hmenum.ValueBehaviorInstantaneous},
		{"HIGHEST_ILLUMINATION", hmenum.QuantityIlluminance, hmenum.ValueBehaviorInstantaneous},
		{"LOWEST_ILLUMINATION", hmenum.QuantityIlluminance, hmenum.ValueBehaviorInstantaneous},
		{"LUX", hmenum.QuantityIlluminance, hmenum.ValueBehaviorInstantaneous},

		// Expanded tuple: ("LEVEL", "LEVEL_2")
		{"LEVEL", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"LEVEL_2", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},

		// Expanded tuple: PM1
		{"MASS_CONCENTRATION_PM_1", hmenum.QuantityPM1, hmenum.ValueBehaviorInstantaneous},
		{"MASS_CONCENTRATION_PM_1_24H_AVERAGE", hmenum.QuantityPM1, hmenum.ValueBehaviorInstantaneous},

		// Expanded tuple: PM10
		{"MASS_CONCENTRATION_PM_10", hmenum.QuantityPM10, hmenum.ValueBehaviorInstantaneous},
		{"MASS_CONCENTRATION_PM_10_24H_AVERAGE", hmenum.QuantityPM10, hmenum.ValueBehaviorInstantaneous},

		// Expanded tuple: PM2.5
		{"MASS_CONCENTRATION_PM_2_5", hmenum.QuantityPM25, hmenum.ValueBehaviorInstantaneous},
		{"MASS_CONCENTRATION_PM_2_5_24H_AVERAGE", hmenum.QuantityPM25, hmenum.ValueBehaviorInstantaneous},

		// Number concentration (no quantity)
		{"NUMBER_CONCENTRATION_PM_1", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"NUMBER_CONCENTRATION_PM_10", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"NUMBER_CONCENTRATION_PM_2_5", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"TYPICAL_PARTICLE_SIZE", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},

		// Expanded tuple: ("BATTERY_STATE", "OPERATING_VOLTAGE")
		{"BATTERY_STATE", hmenum.QuantityVoltage, hmenum.ValueBehaviorInstantaneous},
		{"OPERATING_VOLTAGE", hmenum.QuantityVoltage, hmenum.ValueBehaviorInstantaneous},

		// Expanded tuple: ("RSSI_DEVICE", "RSSI_PEER")
		{"RSSI_DEVICE", hmenum.QuantitySignalStrength, hmenum.ValueBehaviorInstantaneous},
		{"RSSI_PEER", hmenum.QuantitySignalStrength, hmenum.ValueBehaviorInstantaneous},

		// Expanded tuple: wind direction group
		{"WIND_DIR", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"WIND_DIR_RANGE", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"WIND_DIRECTION", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"WIND_DIRECTION_RANGE", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.param, func(t *testing.T) {
			t.Parallel()
			md := MetadataByParam(tc.param)
			if md.Quantity != tc.wantQuantity {
				t.Errorf("MetadataByParam(%q).Quantity = %q, want %q", tc.param, md.Quantity, tc.wantQuantity)
			}
			if md.ValueBehavior != tc.wantBehavior {
				t.Errorf("MetadataByParam(%q).ValueBehavior = %q, want %q", tc.param, md.ValueBehavior, tc.wantBehavior)
			}
		})
	}
}

// MetadataByParam lookup is case-insensitive.
func TestMetadataByParamCaseInsensitive(t *testing.T) {
	t.Parallel()
	md := MetadataByParam("actual_temperature")
	if md.Quantity != hmenum.QuantityTemperature {
		t.Errorf("case-insensitive lookup failed: got %q", md.Quantity)
	}
}

// Unknown parameter returns zero Metadata.
func TestMetadataByParamUnknown(t *testing.T) {
	t.Parallel()
	md := MetadataByParam("DOES_NOT_EXIST")
	if md != (Metadata{}) {
		t.Errorf("expected zero Metadata for unknown param, got %+v", md)
	}
}

// ---------------------------------------------------------------------------
// MetadataByDeviceAndParam
// ---------------------------------------------------------------------------

func TestMetadataByDeviceAndParam(t *testing.T) {
	t.Parallel()

	tests := []struct {
		deviceModel  string
		parameter    string
		wantQuantity hmenum.Quantity
		wantBehavior hmenum.ValueBehavior
	}{
		// Single-model prefix rules
		{"HmIP-WKP", "CODE_STATE", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},
		{"HmIP-WKP-X", "CODE_STATE", hmenum.QuantityEnum, hmenum.ValueBehaviorNone}, // prefix match
		{"HM-Sec-Win", "STATUS", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},
		{"HM-Sec-Win", "DIRECTION", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},
		{"HM-Sec-Win", "ERROR", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},
		{"HM-Sec-Key", "DIRECTION", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},
		{"HM-Sec-Key", "ERROR", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},

		// Multi-model prefix rule: ("HmIP-SRH", "HM-Sec-RHS", "HM-Sec-xx", "ZEL STG RM FDK"), "STATE"
		{"HmIP-SRH", "STATE", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},
		{"HM-Sec-RHS", "STATE", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},
		{"HM-Sec-xx", "STATE", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},
		{"ZEL STG RM FDK", "STATE", hmenum.QuantityEnum, hmenum.ValueBehaviorNone},

		// Multi-model prefix rule: ("HM-CC-RT-DN", "HM-CC-VD"), "VALVE_STATE"
		{"HM-CC-RT-DN", "VALVE_STATE", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"HM-CC-VD", "VALVE_STATE", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
	}

	for _, tc := range tests {
		tc := tc
		name := tc.deviceModel + "/" + tc.parameter
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			md := MetadataByDeviceAndParam(tc.deviceModel, tc.parameter)
			if md.Quantity != tc.wantQuantity {
				t.Errorf("MetadataByDeviceAndParam(%q, %q).Quantity = %q, want %q",
					tc.deviceModel, tc.parameter, md.Quantity, tc.wantQuantity)
			}
			if md.ValueBehavior != tc.wantBehavior {
				t.Errorf("MetadataByDeviceAndParam(%q, %q).ValueBehavior = %q, want %q",
					tc.deviceModel, tc.parameter, md.ValueBehavior, tc.wantBehavior)
			}
		})
	}
}

// Non-matching device+param returns zero Metadata.
func TestMetadataByDeviceAndParamNoMatch(t *testing.T) {
	t.Parallel()
	md := MetadataByDeviceAndParam("HM-Unknown", "STATE")
	if md != (Metadata{}) {
		t.Errorf("expected zero Metadata, got %+v", md)
	}
}

// ---------------------------------------------------------------------------
// MetadataByUnit
// ---------------------------------------------------------------------------

func TestMetadataByUnit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		unit         string
		wantQuantity hmenum.Quantity
		wantBehavior hmenum.ValueBehavior
	}{
		{"°C", hmenum.QuantityTemperature, hmenum.ValueBehaviorInstantaneous},
		{"%", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
		{"bar", hmenum.QuantityPressure, hmenum.ValueBehaviorInstantaneous},
		{"g/m³", hmenum.QuantityNone, hmenum.ValueBehaviorInstantaneous},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.unit, func(t *testing.T) {
			t.Parallel()
			md := MetadataByUnit(tc.unit)
			if md.Quantity != tc.wantQuantity {
				t.Errorf("MetadataByUnit(%q).Quantity = %q, want %q", tc.unit, md.Quantity, tc.wantQuantity)
			}
			if md.ValueBehavior != tc.wantBehavior {
				t.Errorf("MetadataByUnit(%q).ValueBehavior = %q, want %q", tc.unit, md.ValueBehavior, tc.wantBehavior)
			}
		})
	}
}

func TestMetadataByUnitUnknown(t *testing.T) {
	t.Parallel()
	md := MetadataByUnit("unknown_unit")
	if md != (Metadata{}) {
		t.Errorf("expected zero Metadata for unknown unit, got %+v", md)
	}
}

// ---------------------------------------------------------------------------
// MetadataFor — precedence
// ---------------------------------------------------------------------------

func TestMetadataForPrecedence(t *testing.T) {
	t.Parallel()

	// Step 1: device override wins over param and unit.
	// HM-Sec-Win/STATUS → Enum (override), param "STATUS" has no entry,
	// unit "°C" would give Temperature — override must win.
	md := MetadataFor("HM-Sec-Win", "STATUS", "°C")
	if md.Quantity != hmenum.QuantityEnum {
		t.Errorf("device override should win: got quantity %q, want %q", md.Quantity, hmenum.QuantityEnum)
	}

	// Step 2: param wins over unit when no device override.
	// No device override for "Unknown-Device/ACTUAL_TEMPERATURE",
	// but ACTUAL_TEMPERATURE→Temperature from param table.
	// Unit "bar" would give Pressure — param must win.
	md = MetadataFor("Unknown-Device", "ACTUAL_TEMPERATURE", "bar")
	if md.Quantity != hmenum.QuantityTemperature {
		t.Errorf("param should beat unit: got quantity %q, want %q", md.Quantity, hmenum.QuantityTemperature)
	}

	// Step 3: unit fallback when neither device nor param match.
	md = MetadataFor("Unknown-Device", "UNKNOWN_PARAM", "°C")
	if md.Quantity != hmenum.QuantityTemperature {
		t.Errorf("unit fallback failed: got quantity %q, want %q", md.Quantity, hmenum.QuantityTemperature)
	}

	// Step 4: nothing matches → zero Metadata.
	md = MetadataFor("Unknown-Device", "UNKNOWN_PARAM", "unknown_unit")
	if md != (Metadata{}) {
		t.Errorf("expected zero Metadata, got %+v", md)
	}
}

// ---------------------------------------------------------------------------
// BinarySensorQuantityByParam
// ---------------------------------------------------------------------------

func TestBinarySensorQuantityByParam(t *testing.T) {
	t.Parallel()

	tests := []struct {
		param string
		want  hmenum.Quantity
	}{
		// Simple entries
		{"ALARMSTATE", hmenum.QuantitySafety},
		{"ACOUSTIC_ALARM_ACTIVE", hmenum.QuantitySafety},
		{"BURST_LIMIT_WARNING", hmenum.QuantityProblem},
		{"DEW_POINT_ALARM", hmenum.QuantityProblem},
		{"EMERGENCY_OPERATION", hmenum.QuantitySafety},
		{"ERROR_JAMMED", hmenum.QuantityProblem},
		{"HEATER_STATE", hmenum.QuantityHeat},
		{"MOISTURE_DETECTED", hmenum.QuantityMoisture},
		{"MOTION", hmenum.QuantityMotion},
		{"OPTICAL_ALARM_ACTIVE", hmenum.QuantitySafety},
		{"POWER_MAINS_FAILURE", hmenum.QuantityProblem},
		{"PRESENCE_DETECTION_STATE", hmenum.QuantityPresence},
		{"RAINING", hmenum.QuantityMoisture},
		{"WATERLEVEL_DETECTED", hmenum.QuantityMoisture},
		{"WINDOW_STATE", hmenum.QuantityWindow},

		// Expanded tuple: ("BLOCKED_PERMANENT", "BLOCKED_TEMPORARY")
		{"BLOCKED_PERMANENT", hmenum.QuantityProblem},
		{"BLOCKED_TEMPORARY", hmenum.QuantityProblem},

		// Expanded tuple: ("DUTYCYCLE", "DUTY_CYCLE")
		{"DUTYCYCLE", hmenum.QuantityProblem},
		{"DUTY_CYCLE", hmenum.QuantityProblem},

		// Expanded tuple: ("LOWBAT", "LOW_BAT", "LOWBAT_SENSOR")
		{"LOWBAT", hmenum.QuantityBattery},
		{"LOW_BAT", hmenum.QuantityBattery},
		{"LOWBAT_SENSOR", hmenum.QuantityBattery},

		// Expanded tuple: ("PROCESS", "WORKING")
		{"PROCESS", hmenum.QuantityRunning},
		{"WORKING", hmenum.QuantityRunning},

		// Expanded tuple: ("SABOTAGE", "SABOTAGE_STICKY")
		{"SABOTAGE", hmenum.QuantityTamper},
		{"SABOTAGE_STICKY", hmenum.QuantityTamper},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.param, func(t *testing.T) {
			t.Parallel()
			got := BinarySensorQuantityByParam(tc.param)
			if got != tc.want {
				t.Errorf("BinarySensorQuantityByParam(%q) = %q, want %q", tc.param, got, tc.want)
			}
		})
	}
}

func TestBinarySensorQuantityByParamUnknown(t *testing.T) {
	t.Parallel()
	got := BinarySensorQuantityByParam("DOES_NOT_EXIST")
	if got != hmenum.QuantityNone {
		t.Errorf("expected QuantityNone for unknown param, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// BinarySensorQuantityByDeviceAndParam
// ---------------------------------------------------------------------------

func TestBinarySensorQuantityByDeviceAndParam(t *testing.T) {
	t.Parallel()

	tests := []struct {
		deviceModel string
		parameter   string
		want        hmenum.Quantity
	}{
		{"HmIP-DSD-PCB", "STATE", hmenum.QuantityOccupancy},
		{"HmIP-SCI", "STATE", hmenum.QuantityOpening},
		{"HmIP-FCI1", "STATE", hmenum.QuantityOpening},
		{"HmIP-FCI6", "STATE", hmenum.QuantityOpening},
		{"HM-Sec-SD", "STATE", hmenum.QuantitySmoke},
		{"HM-Sen-RD-O", "STATE", hmenum.QuantityMoisture},
		{"HM-Sec-Win", "WORKING", hmenum.QuantityRunning},

		// Window-group multi-model
		{"HmIP-SWD", "STATE", hmenum.QuantityWindow},
		{"HmIP-SWDO", "STATE", hmenum.QuantityWindow},
		{"HmIP-SWDM", "STATE", hmenum.QuantityWindow},
		{"HM-Sec-SC", "STATE", hmenum.QuantityWindow},
		{"HM-SCI-3-FM", "STATE", hmenum.QuantityWindow},
		{"ZEL STG RM FFK", "STATE", hmenum.QuantityWindow},
	}

	for _, tc := range tests {
		tc := tc
		name := tc.deviceModel + "/" + tc.parameter
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := BinarySensorQuantityByDeviceAndParam(tc.deviceModel, tc.parameter)
			if got != tc.want {
				t.Errorf("BinarySensorQuantityByDeviceAndParam(%q, %q) = %q, want %q",
					tc.deviceModel, tc.parameter, got, tc.want)
			}
		})
	}
}

func TestBinarySensorQuantityByDeviceAndParamNoMatch(t *testing.T) {
	t.Parallel()
	got := BinarySensorQuantityByDeviceAndParam("HM-Unknown", "STATE")
	if got != hmenum.QuantityNone {
		t.Errorf("expected QuantityNone, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// BinarySensorQuantityFor — precedence
// ---------------------------------------------------------------------------

func TestBinarySensorQuantityForPrecedence(t *testing.T) {
	t.Parallel()

	// Device override: HmIP-DSD-PCB/STATE → Occupancy
	// (param "STATE" has no entry in the by-param table, but the device wins anyway)
	got := BinarySensorQuantityFor("HmIP-DSD-PCB", "STATE")
	if got != hmenum.QuantityOccupancy {
		t.Errorf("device override should win: got %q, want %q", got, hmenum.QuantityOccupancy)
	}

	// Param fallback: LOWBAT → Battery (no device rule for "Unknown-Device")
	got = BinarySensorQuantityFor("Unknown-Device", "LOWBAT")
	if got != hmenum.QuantityBattery {
		t.Errorf("param fallback failed: got %q, want %q", got, hmenum.QuantityBattery)
	}

	// Nothing matches → QuantityNone
	got = BinarySensorQuantityFor("Unknown-Device", "UNKNOWN_PARAM")
	if got != hmenum.QuantityNone {
		t.Errorf("expected QuantityNone, got %q", got)
	}
}

// Verify a device-override that shadows the generic param behavior.
// HM-Sec-SD/STATE → Smoke; plain STATE has no param-level entry,
// so the override is the only reason this resolves.
func TestBinarySensorQuantityForHMSecSDSmoke(t *testing.T) {
	t.Parallel()
	got := BinarySensorQuantityFor("HM-Sec-SD", "STATE")
	if got != hmenum.QuantitySmoke {
		t.Errorf("HM-Sec-SD/STATE: got %q, want %q", got, hmenum.QuantitySmoke)
	}
}
