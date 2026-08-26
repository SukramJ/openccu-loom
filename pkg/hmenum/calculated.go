// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

// CalculatedParameter identifies virtual parameters computed inside
// openccu-loom from raw CCU values — they are never transmitted to or
// from the CCU itself.
type CalculatedParameter string

// CalculatedParameter values.
const (
	// CalculatedParameterApparentTemperature is the "feels-like"
	// temperature derived from air temperature, humidity, and wind.
	CalculatedParameterApparentTemperature CalculatedParameter = "APPARENT_TEMPERATURE"
	// CalculatedParameterDewPoint is the temperature at which
	// moisture begins to condense.
	CalculatedParameterDewPoint CalculatedParameter = "DEW_POINT"
	// CalculatedParameterDewPointSpread is the difference between
	// the current temperature and the dew-point temperature.
	CalculatedParameterDewPointSpread CalculatedParameter = "DEW_POINT_SPREAD"
	// CalculatedParameterEnthalpy is the thermodynamic enthalpy of
	// moist air.
	CalculatedParameterEnthalpy CalculatedParameter = "ENTHALPY"
	// CalculatedParameterFrostPoint is the temperature at which frost
	// forms (subset of dew point for sub-zero air temperatures).
	CalculatedParameterFrostPoint CalculatedParameter = "FROST_POINT"
	// CalculatedParameterIntrusionAlarm is a virtual alarm synthesised
	// from window/door sensors and motion detectors.
	CalculatedParameterIntrusionAlarm CalculatedParameter = "INTRUSION_ALARM"
	// CalculatedParameterOperatingVoltageLevel is the battery-voltage
	// level expressed as a fraction (0.0–1.0).
	CalculatedParameterOperatingVoltageLevel CalculatedParameter = "OPERATING_VOLTAGE_LEVEL"
	// CalculatedParameterSmokeAlarm is a virtual alarm synthesised
	// from smoke-detector channels.
	CalculatedParameterSmokeAlarm CalculatedParameter = "SMOKE_ALARM"
	// CalculatedParameterVaporConcentration is the absolute humidity
	// in g/m³.
	CalculatedParameterVaporConcentration CalculatedParameter = "VAPOR_CONCENTRATION"
	// CalculatedParameterWindowOpen is derived from window-contact
	// sensors to expose a boolean open/closed state.
	CalculatedParameterWindowOpen CalculatedParameter = "WINDOW_OPEN"
)

// String returns the wire representation.
func (c CalculatedParameter) String() string { return string(c) }
