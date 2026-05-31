// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package calculated

import (
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: every calculated-DP concrete type
// participates in the Matter source surface (ADR 0012) via the
// [interfaces.MatterMeasurementSource] interface. Each sensor declares
// its target Matter measurement cluster on the type rather than via
// the bridge — calculated parameters that have no Matter equivalent
// (DEW_POINT_SPREAD, ENTHALPY, VAPOR_CONCENTRATION, SMOKE_ALARM)
// return [interfaces.MatterMeasurementNone] and stay MQTT-only.
//
// SMOKE_ALARM is intentionally None at this layer: the SmokeCOAlarm
// cluster surface lives on `siren.SmokeSiren` (custom DP) which carries
// the full alarm state. The calculated SMOKE_ALARM derives from the
// same wire data and does not need a duplicate Matter projection.
var (
	_ interfaces.MatterMeasurementSource = (*ApparentTemperatureSensor)(nil)
	_ interfaces.MatterMeasurementSource = (*DewPointSensor)(nil)
	_ interfaces.MatterMeasurementSource = (*DewPointSpreadSensor)(nil)
	_ interfaces.MatterMeasurementSource = (*EnthalpySensor)(nil)
	_ interfaces.MatterMeasurementSource = (*FrostPointSensor)(nil)
	_ interfaces.MatterMeasurementSource = (*VaporConcentrationSensor)(nil)
	_ interfaces.MatterMeasurementSource = (*OperatingVoltageLevelSensor)(nil)
	_ interfaces.MatterMeasurementSource = (*DerivedBinarySensor)(nil)
)

// MatterMeasurementClass for ApparentTemperatureSensor:
// "feels-like" temperature → TemperatureMeasurement (0x0402).
func (s *ApparentTemperatureSensor) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return interfaces.MatterMeasurementTemperature
}

// MatterMeasurementClass for DewPointSensor → TemperatureMeasurement.
func (s *DewPointSensor) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return interfaces.MatterMeasurementTemperature
}

// MatterMeasurementClass for DewPointSpreadSensor: a delta has no
// clean Matter cluster; the bridge skips it and the spread surfaces
// on MQTT only.
func (s *DewPointSpreadSensor) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return interfaces.MatterMeasurementNone
}

// MatterMeasurementClass for EnthalpySensor: J/kg has no Matter unit.
func (s *EnthalpySensor) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return interfaces.MatterMeasurementNone
}

// MatterMeasurementClass for FrostPointSensor → TemperatureMeasurement.
func (s *FrostPointSensor) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return interfaces.MatterMeasurementTemperature
}

// MatterMeasurementClass for VaporConcentrationSensor: absolute
// humidity (g/m³) has no Matter cluster; relative humidity is a
// distinct concept and not equivalent.
func (s *VaporConcentrationSensor) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return interfaces.MatterMeasurementNone
}

// MatterMeasurementClass for OperatingVoltageLevelSensor: derived
// battery percentage → PowerSource (0x002F) BatPercentRemaining (P1).
func (s *OperatingVoltageLevelSensor) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return interfaces.MatterMeasurementBattery
}

// MatterMeasurementClass for DerivedBinarySensor dispatches on the
// per-instance CalculatedParameter — the same struct type carries
// IntrusionAlarm, SmokeAlarm, and WindowOpen.
func (s *DerivedBinarySensor) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	switch s.CalculatedParameter() {
	case hmenum.CalculatedParameterIntrusionAlarm,
		hmenum.CalculatedParameterWindowOpen:
		return interfaces.MatterMeasurementContact
	case hmenum.CalculatedParameterSmokeAlarm:
		// Smoke is surfaced by siren.SmokeSiren via the SmokeCOAlarm
		// cluster — the calculated derivation is redundant for Matter.
		return interfaces.MatterMeasurementNone
	default:
		return interfaces.MatterMeasurementNone
	}
}
