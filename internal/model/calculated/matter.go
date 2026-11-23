// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package calculated

import (
	contract "thingfabric.dev/go-fabric/contract"

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

// MatterMeasurementClass for OperatingVoltageLevelSensor: the derived
// battery percentage is a battery reading. The Matter bridge serves no
// endpoint for it; the device's PowerSource (0x002F) is fed from its
// generic battery data point.
func (s *OperatingVoltageLevelSensor) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	return interfaces.MatterMeasurementBattery
}

// MatterPowerSourceSelection implements [contract.PowerSourceSelectionSource]
// for the derived battery percentage.
// The device model carries no battery replaceability, and the daemon keeps
// no static device database to look it up, so the source says it does not
// know (BatteryReplaceabilityUnspecified) — the value matter.js serves for a
// battery power source that states none (matter.js
// packages/node/src/behaviors/power-source/PowerSourceServer.ts:48-54).
// Without it the node serves the selection's zero value, a battery the
// user replaces, which nothing here established. Only a source of class
// MeasurementBattery is asked for it.
func (s *OperatingVoltageLevelSensor) MatterPowerSourceSelection() contract.PowerSourceSelection {
	return contract.PowerSourceSelection{BatteryReplaceability: contract.BatteryReplaceabilityUnspecified}
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
