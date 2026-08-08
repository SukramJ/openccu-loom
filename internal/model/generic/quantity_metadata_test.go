// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSensorQuantityMetadataParameterCoverage spot-checks the
// expansion of the sensor parameter map. Every parameter listed here
// was missing in the pre-expansion catalogue — pinning each one keeps
// the MQTT discovery payload's HA `device_class` / `state_class`
// derivation faithful.
func TestSensorQuantityMetadataParameterCoverage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		param hmenum.Parameter
		want  generic.QuantityMetadata
	}{
		// Energy / gas — MONOTONIC.
		{"ENERGY_COUNTER", generic.QuantityMetadata{Quantity: hmenum.QuantityEnergy, Behavior: hmenum.ValueBehaviorMonotonic}},
		{"GAS_ENERGY_COUNTER", generic.QuantityMetadata{Quantity: hmenum.QuantityGas, Behavior: hmenum.ValueBehaviorMonotonic}},
		{"GAS_VOLUME", generic.QuantityMetadata{Quantity: hmenum.QuantityGas, Behavior: hmenum.ValueBehaviorMonotonic}},
		{"IEC_ENERGY_COUNTER", generic.QuantityMetadata{Quantity: hmenum.QuantityEnergy, Behavior: hmenum.ValueBehaviorMonotonic}},
		{"RAIN_COUNTER", generic.QuantityMetadata{Behavior: hmenum.ValueBehaviorMonotonic}},
		{"SUNSHINEDURATION", generic.QuantityMetadata{Behavior: hmenum.ValueBehaviorMonotonic}},
		// Particulate matter — by quantity.
		{"MASS_CONCENTRATION_PM_1", generic.QuantityMetadata{Quantity: hmenum.QuantityPM1, Behavior: hmenum.ValueBehaviorInstantaneous}},
		{"MASS_CONCENTRATION_PM_10_24H_AVERAGE", generic.QuantityMetadata{Quantity: hmenum.QuantityPM10, Behavior: hmenum.ValueBehaviorInstantaneous}},
		{"MASS_CONCENTRATION_PM_2_5", generic.QuantityMetadata{Quantity: hmenum.QuantityPM25, Behavior: hmenum.ValueBehaviorInstantaneous}},
		// Calculated derivations the daemon synthesises.
		{"DEW_POINT", generic.QuantityMetadata{Quantity: hmenum.QuantityTemperature, Behavior: hmenum.ValueBehaviorInstantaneous}},
		{"OPERATING_VOLTAGE_LEVEL", generic.QuantityMetadata{Quantity: hmenum.QuantityBattery, Behavior: hmenum.ValueBehaviorInstantaneous}},
		{"VAPOR_CONCENTRATION", generic.QuantityMetadata{Behavior: hmenum.ValueBehaviorInstantaneous}},
		// Concentration / Air pressure — quantity by parameter.
		{"CONCENTRATION", generic.QuantityMetadata{Quantity: hmenum.QuantityCO2, Behavior: hmenum.ValueBehaviorInstantaneous}},
		{"AIR_PRESSURE", generic.QuantityMetadata{Quantity: hmenum.QuantityPressure, Behavior: hmenum.ValueBehaviorInstantaneous}},
		// RSSI — signal_strength.
		{"RSSI_DEVICE", generic.QuantityMetadata{Quantity: hmenum.QuantitySignalStrength, Behavior: hmenum.ValueBehaviorInstantaneous}},
		{"RSSI_PEER", generic.QuantityMetadata{Quantity: hmenum.QuantitySignalStrength, Behavior: hmenum.ValueBehaviorInstantaneous}},
		// LOCK_STATE / SMOKE_DETECTOR_ALARM_STATUS — enum, no behavior.
		{"LOCK_STATE", generic.QuantityMetadata{Quantity: hmenum.QuantityEnum}},
		{"SMOKE_DETECTOR_ALARM_STATUS", generic.QuantityMetadata{Quantity: hmenum.QuantityEnum}},
	}
	for _, tc := range cases {
		got, ok := generic.SensorQuantityMetadataForParameter(tc.param)
		if !ok {
			t.Errorf("%q not classified as a sensor parameter", tc.param)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %+v want %+v", tc.param, got, tc.want)
		}
	}
}

// TestBinarySensorQuantityCoverage pins the binary-sensor
// Classification map: every parameter
// produce the same Quantity in Go.
func TestBinarySensorQuantityCoverage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		param hmenum.Parameter
		want  hmenum.Quantity
	}{
		{"MOTION", hmenum.QuantityMotion},
		{"PRESENCE_DETECTION_STATE", hmenum.QuantityPresence},
		{"WINDOW_STATE", hmenum.QuantityWindow},
		{"RAINING", hmenum.QuantityMoisture},
		{"MOISTURE_DETECTED", hmenum.QuantityMoisture},
		{"WATERLEVEL_DETECTED", hmenum.QuantityMoisture},
		{"SABOTAGE", hmenum.QuantityTamper},
		{"SABOTAGE_STICKY", hmenum.QuantityTamper},
		{"LOWBAT", hmenum.QuantityBattery},
		{"LOW_BAT", hmenum.QuantityBattery},
		{"HEATER_STATE", hmenum.QuantityHeat},
		{"PROCESS", hmenum.QuantityRunning},
		{"WORKING", hmenum.QuantityRunning},
		{"ALARMSTATE", hmenum.QuantitySafety},
		{"ACOUSTIC_ALARM_ACTIVE", hmenum.QuantitySafety},
		{"OPTICAL_ALARM_ACTIVE", hmenum.QuantitySafety},
		{"BLOCKED_PERMANENT", hmenum.QuantityProblem},
		{"BURST_LIMIT_WARNING", hmenum.QuantityProblem},
		{"DEW_POINT_ALARM", hmenum.QuantityProblem},
		{"DUTY_CYCLE", hmenum.QuantityProblem},
		{"ERROR_JAMMED", hmenum.QuantityProblem},
		{"POWER_MAINS_FAILURE", hmenum.QuantityProblem},
	}
	for _, tc := range cases {
		got, ok := generic.BinarySensorQuantityForParameter(tc.param)
		if !ok {
			t.Errorf("%q not classified as a binary-sensor parameter", tc.param)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %q want %q", tc.param, got, tc.want)
		}
	}
}

// TestDeviceParamSensorOverrides pins the device+param override map
// (HmIP-WKP / HM-Sec-RHS / HM-Sec-Win etc.).
func TestDeviceParamSensorOverrides(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model, param string
		want         generic.QuantityMetadata
	}{
		{"HmIP-WKP", "CODE_STATE", generic.QuantityMetadata{Quantity: hmenum.QuantityEnum}},
		{"HmIP-WKP-1", "CODE_STATE", generic.QuantityMetadata{Quantity: hmenum.QuantityEnum}}, // prefix match
		{"HM-Sec-RHS", "STATE", generic.QuantityMetadata{Quantity: hmenum.QuantityEnum}},
		{"HM-Sec-Win", "STATUS", generic.QuantityMetadata{Quantity: hmenum.QuantityEnum}},
		{"HM-Sec-Key", "DIRECTION", generic.QuantityMetadata{Quantity: hmenum.QuantityEnum}},
		{"HM-CC-RT-DN", "VALVE_STATE", generic.QuantityMetadata{Behavior: hmenum.ValueBehaviorInstantaneous}},
	}
	for _, tc := range cases {
		got, ok := generic.SensorQuantityMetadataForDeviceParameter(tc.model, hmenum.Parameter(tc.param))
		if !ok {
			t.Errorf("(%q,%q) not classified through the device-override path", tc.model, tc.param)
			continue
		}
		if got != tc.want {
			t.Errorf("(%q,%q): got %+v want %+v", tc.model, tc.param, got, tc.want)
		}
	}
}

// TestDeviceParamBinarySensorOverrides pins the binary-sensor
// override map (SWDO → window, SCI/FCI → opening, Sec-SD → smoke …).
func TestDeviceParamBinarySensorOverrides(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model, param string
		want         hmenum.Quantity
	}{
		{"HmIP-SWDO", "STATE", hmenum.QuantityWindow},
		{"HmIP-SWDO-PL", "STATE", hmenum.QuantityWindow}, // prefix
		{"HmIP-SCI", "STATE", hmenum.QuantityOpening},
		{"HmIP-FCI1", "STATE", hmenum.QuantityOpening},
		{"HM-Sec-SD", "STATE", hmenum.QuantitySmoke},
		{"HmIP-DSD-PCB", "STATE", hmenum.QuantityOccupancy},
		{"HM-Sen-RD-O", "STATE", hmenum.QuantityMoisture},
		{"HM-Sec-Win", "WORKING", hmenum.QuantityRunning},
	}
	for _, tc := range cases {
		got, ok := generic.BinarySensorQuantityForDeviceParameter(tc.model, hmenum.Parameter(tc.param))
		if !ok {
			t.Errorf("(%q,%q) not classified through the binary-sensor device-override path", tc.model, tc.param)
			continue
		}
		if got != tc.want {
			t.Errorf("(%q,%q): got %q want %q", tc.model, tc.param, got, tc.want)
		}
	}
}

// TestDeviceParamBinarySensorOverridesExcludesHmIPSWD pins
// BD-Safety-SWDWindowRuleDropped (notes/parity/by_design.md):
// HmIP-SWD is the water sensor, not a window contact. Its ported
// grouping with the window-contact family assigned STATE the window
// quantity, but HmIP-SWD carries no STATE parameter and the mapping
// would invert the classification the Security & Safety domain
// (internal/model/safety) must derive from it, so the entry is
// deliberately not carried into this table.
func TestDeviceParamBinarySensorOverridesExcludesHmIPSWD(t *testing.T) {
	t.Parallel()

	if q, ok := generic.BinarySensorQuantityForDeviceParameter("HmIP-SWD", "STATE"); ok {
		t.Errorf("BinarySensorQuantityForDeviceParameter(HmIP-SWD, STATE) = (%q, true), want ok=false", q)
	}
}

// TestUnitFallback pins the unit-keyed fallback.
func TestUnitFallback(t *testing.T) {
	t.Parallel()
	cases := []struct {
		unit string
		want generic.QuantityMetadata
	}{
		{"%", generic.QuantityMetadata{Behavior: hmenum.ValueBehaviorInstantaneous}},
		{"bar", generic.QuantityMetadata{Quantity: hmenum.QuantityPressure, Behavior: hmenum.ValueBehaviorInstantaneous}},
		{"°C", generic.QuantityMetadata{Quantity: hmenum.QuantityTemperature, Behavior: hmenum.ValueBehaviorInstantaneous}},
		{"g/m³", generic.QuantityMetadata{Behavior: hmenum.ValueBehaviorInstantaneous}},
	}
	for _, tc := range cases {
		got, ok := generic.SensorQuantityMetadataForUnit(tc.unit)
		if !ok {
			t.Errorf("unit %q not classified through the fallback map", tc.unit)
			continue
		}
		if got != tc.want {
			t.Errorf("unit %q: got %+v want %+v", tc.unit, got, tc.want)
		}
	}
}
