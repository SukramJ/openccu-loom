// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fixUnitByParam holds per-parameter unit overrides that win over
// whatever raw unit the CCU reports — used to enforce a canonical unit
// (`°C` for temperature, `lx` for illuminance, …) across firmware
// variants that disagree on the spelling. The CCU has the same shape of
// escape hatch, keyed the same way: `getUnit` overrides the declared
// unit for METER_CONSTANT_VOLUME, METER_CONSTANT_ENERGY and ALTITUDE by
// parameter name
// (../OpenCCU-Base/www/config/easymodes/etc/uiElements.tcl:205-215).
// The individual entries below are ours, from the Python port, and are
// unverified against the firmware except where noted.
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
	// The CCU counts seconds; multiplierByParam converts to days, so the
	// unit has to say days. Without this the parameter reported a factor
	// that lands in one unit next to a label naming another, and every
	// consumer that applied the factor mislabelled the reading. The MQTT
	// entity description already declared `d` for it, which is why only
	// the REST-facing pair was wrong.
	hmenum.ParameterTimeOfOperation: "d",
	"SUNSHINEDURATION":              "min",
	"WIND_DIRECTION":                "°",
	"WIND_DIRECTION_RANGE":          "°",
}

// fixUnitReplace maps a CCU unit spelling to the canonical one, matched
// on the WHOLE unit string.
//
// Whole-string is the CCU's own rule, not a simplification of ours: its
// unit normaliser is a chain of equality tests with no substring or glob
// anywhere — ../OpenCCU-Base/www/config/easymodes/etc/uiElements.tcl:174-216
// (`proc getUnit`, `==` and `string equal` only) and
// ../OpenCCU-Base/www/rega/esp/programs.fn:606-614. Matching on a
// substring instead cost the suffix of every compound unit the corpus
// declares: `m3/Imp.` on METER_CONSTANT_GAS
// (../OpenCCU-Base/src/devicetypes/rftypes/rf_es_tx_wm.xml:282, also
// rf_es_tx_wm_le_v1_0.xml:171) and `m3/h` on
// ENERGIE_METER_TRANSMITTER.GAS_FLOW
// (../OpenCCU-Base/opt/HmIP/legacy-parameter-definition.config:352) both came
// out as a plain `m³`. The CCU normalises neither, so they now pass
// through verbatim.
//
// `degree` is an angle: programs.fn:611-614 maps it to `&deg;` and
// getUnit maps HmIP's `_Grad_` to `&#176;`. The only parameters that
// declare it are WIND_DIRECTION and WIND_DIRECTION_RANGE
// (rf_hm-wds100-c6-o-2.xml:155,163 and rf_ks550.xml:148,156).
//
// `Lux` → `lx`, `m3` → `m³` and `% rF` → `%` are ours, from the Python
// port: the CCU renders all three verbatim (`% rF` is declared on eight
// HmIP humidity parameters and matches no getUnit branch). They are
// canonicalisations for HA, not CCU rules, and are unverified against
// the firmware.
//
// Two firmware mappings are deliberately NOT carried here. getUnit maps
// `minutes` to a localized label, which is a UI concern and not a unit;
// and it maps `K` to `°C` except on UNIVERSAL_LIGHT_RECEIVER, where `K`
// stays Kelvin — a per-channel-type condition this unit-keyed table
// cannot express, so adding it would mislabel every colour-temperature
// reading.
var fixUnitReplace = map[string]string{
	"100%":   "%",
	"% rF":   "%",
	"_Grad_": "°",
	"degree": "°",
	"Lux":    "lx",
	"m3":     "m³",
}

// multiplierUnit converts a fractional wire value into the unit its
// label claims. A CCU unit of `100%` means the value is a fraction
// 0.0..1.0 that has to be scaled by 100 — which is the CCU's own rule,
// not an inference: ../OpenCCU-Base/www/rega/esp/programs.fn:606-610
// does `value = value * 100` and rewrites the unit to `%` in the same
// branch.
var multiplierUnit = map[string]float64{
	"100%": 100.0,
}

// multiplierByParam holds per-parameter multiplier overrides that take
// precedence over the unit-based lookup, for conversions the raw CCU
// unit cannot encode.
//
// TIME_OF_OPERATION: the CCU counts seconds, and we report days — a
// presentation choice of ours, not a CCU rule.
var multiplierByParam = map[hmenum.Parameter]float64{
	hmenum.ParameterTimeOfOperation: 1.0 / 86400.0,
}

// DefaultMultiplier is the multiplier returned when no override
// applies.
const DefaultMultiplier = 1.0

// CleanupUnit canonicalises the unit the CCU reports for a wire
// parameter. Resolution order:
//
//  1. Per-parameter override (`fixUnitByParam`) wins outright.
//  2. Quote characters are stripped. The HmIP legacy parameter
//     definition declares two parameters whose `.Unit=` value is the
//     literal two-character string `""`
//     (../OpenCCU-Base/opt/HmIP/legacy-parameter-definition.config:333
//     and :653), so
//     this has to be a character-level strip rather than a table entry.
//  3. Empty raw unit → empty string (no canonical unit known).
//  4. WHOLE-string match in `fixUnitReplace` → the replacement.
//  5. Otherwise → the raw unit unchanged, which is what the CCU's own
//     `getUnit` does for every spelling it does not name.
//
// Used by the MQTT discovery builder, the config-UI schema adapter, and
// any UI renderer that surfaces the unit; it is also the lookup key for
// the unit fallback in [DataPoint.Quantity] and [DataPoint.ValueBehavior].
func CleanupUnit(parameter hmenum.Parameter, rawUnit string) string {
	if newUnit, ok := fixUnitByParam[parameter]; ok {
		return newUnit
	}
	rawUnit = strings.ReplaceAll(rawUnit, `"`, "")
	if rawUnit == "" {
		return ""
	}
	if fix, ok := fixUnitReplace[rawUnit]; ok {
		return fix
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

// DisplayValue projects a raw wire value into the unit the data point
// reports, by applying multiplier. It returns ok=false when there is
// nothing to project — a trivial multiplier, or a value that is not a
// number — so the caller leaves its field unset rather than repeating
// the wire value under a second name.
//
// It exists so the projection happens once, in one place, instead of in
// every north-bound renderer. Every consumer that got this wrong got it
// wrong the same way: a LEVEL reads 0.42 on the wire and the unit says
// `%`, so anything that prints the pair without multiplying shows a
// dimmer at 0.42 % instead of 42 %.
//
// It deliberately does NOT change what the domain stores or writes. The
// wire value is what the CCU sends and expects back, what the custom
// data points compute on, and what the Matter mappings convert from —
// scaling it at the source would need every one of those to un-scale it
// again, silently and per site.
//
// An integer parameter projects to a float (TIME_OF_OPERATION counts
// seconds and reports days): the display value describes a quantity,
// not a wire encoding.
func DisplayValue(raw any, multiplier float64) (any, bool) {
	if multiplier == 0 || multiplier == DefaultMultiplier {
		return nil, false
	}
	switch v := raw.(type) {
	case float64:
		return v * multiplier, true
	case float32:
		return float64(v) * multiplier, true
	case int:
		return float64(v) * multiplier, true
	case int32:
		return float64(v) * multiplier, true
	case int64:
		return float64(v) * multiplier, true
	default:
		return nil, false
	}
}
