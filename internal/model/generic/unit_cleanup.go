// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// FixUnitByParam mirrors
// (model/data_point.py:179). Per-parameter overrides that win over
// whatever raw unit the CCU reports — typically used to enforce a
// canonical unit (`°C` for temperature, `lx` for illuminance, …)
// across firmware variants that disagree on the spelling.
var fixUnitByParam = map[hmenum.Parameter]string{
	hmenum.ParameterActualTemperature:       "°C",
	"CURRENT_ILLUMINATION":                  "lx",
	hmenum.ParameterHumidity:                "%",
	"ILLUMINATION":                          "lx",
	hmenum.ParameterLevel:                   "%",
	"MASS_CONCENTRATION_PM_10_24H_AVERAGE":  "µg/m³",
	"MASS_CONCENTRATION_PM_1_24H_AVERAGE":   "µg/m³",
	"MASS_CONCENTRATION_PM_2_5_24H_AVERAGE": "µg/m³",
	hmenum.ParameterOperatingVoltage:        "V",
	hmenum.ParameterRSSIDevice:              "dBm",
	hmenum.ParameterRSSIPeer:                "dBm",
	"SUNSHINE_DURATION":                     "min",
	"SUNSHINEDURATION":                      "min",
	"WIND_DIRECTION":                        "°",
	"WIND_DIRECTION_RANGE":                  "°",
}

// FixUnitReplace mirrors
// (model/data_point.py:179). Substring-matching replacements that
// transform CCU spellings into HA-friendly canonical units.
var fixUnitReplace = map[string]string{
	`"`:      "",
	"100%":   "%",
	"% rF":   "%",
	"degree": "°C",
	"Lux":    "lx",
	"m3":     "m³",
}

// MultiplierUnit mirrors
// (model/data_point.py:184). When the CCU reports a unit string
// like `100%`, the wire value is a fraction (0.0..1.0) and the UI
// expects a percentage; the multiplier converts the fraction.
var multiplierUnit = map[string]float64{
	"100%": 100.0,
}

// multiplierByParam holds per-parameter multiplier overrides that take
// precedence over the unit-based lookup. These mirror the
// `multiplier` fields in
// that go beyond what the raw CCU unit can encode.
//
// TIME_OF_OPERATION: CCU reports seconds; HA entity-description expects
// days (native_unit_of_measurement=UnitOfTime.DAYS, multiplier=1/86400).
var multiplierByParam = map[hmenum.Parameter]float64{
	hmenum.ParameterTimeOfOperation: 1.0 / 86400.0,
}

// DefaultMultiplier is the multiplier returned when no override
// applies.
const DefaultMultiplier = 1.0

// CleanupUnit applies
// reported by the CCU for a given wire parameter. Resolution order:
//
//  1. Per-parameter override (`fixUnitByParam`) wins outright.
//  2. Empty raw unit → empty string (no canonical unit known).
//  3. Substring match in `fixUnitReplace` → return the replacement.
//  4. Otherwise → return the raw unit unchanged.
//
// Mirrors `model/data_point.py:_cleanup_unit`. Used by the MQTT
// discovery builder and any UI renderer that surfaces the data
// point's unit so HA / operators see a stable string regardless
// of CCU firmware quirks.
func CleanupUnit(parameter hmenum.Parameter, rawUnit string) string {
	if newUnit, ok := fixUnitByParam[parameter]; ok {
		return newUnit
	}
	if rawUnit == "" {
		return ""
	}
	for check, fix := range fixUnitReplace {
		if strings.Contains(rawUnit, check) {
			return fix
		}
	}
	return rawUnit
}

// MultiplierForUnit returns the multiplier that converts a wire value into
// its canonical unit.
func MultiplierForUnit(rawUnit string) float64 {
	if rawUnit == "" {
		return DefaultMultiplier
	}
	if m, ok := multiplierUnit[rawUnit]; ok {
		return m
	}
	return DefaultMultiplier
}

// Unit returns the cleaned-up unit for the data point's parameter.
// Helper for north-bound renderers; equivalent to calling
// [CleanupUnit] on the data point's parameter and the descriptor's
// raw unit.
func (d *DataPoint[T]) Unit() string {
	return CleanupUnit(d.Parameter(), d.Descriptor.Unit)
}

// MultiplierForParam returns the multiplier for a specific parameter
// when a per-parameter override exists; returns DefaultMultiplier
// otherwise. Per-param overrides take precedence over unit-based ones.
func MultiplierForParam(parameter hmenum.Parameter) float64 {
	if m, ok := multiplierByParam[parameter]; ok {
		return m
	}
	return DefaultMultiplier
}

// Multiplier returns the multiplier that converts the data point's
// raw wire value into its canonical unit. Per-parameter overrides
// from multiplierByParam take precedence over the unit-based lookup.
// Returns [DefaultMultiplier] when no override applies.
func (d *DataPoint[T]) Multiplier() float64 {
	if m, ok := multiplierByParam[d.Parameter()]; ok {
		return m
	}
	return MultiplierForUnit(d.Descriptor.Unit)
}
