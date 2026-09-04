// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package calculated adds the P2 parity surface for CalculatedDataPoint
// Sensors. The methods mirror
// delegated properties and state_properties
// (calculated/data_point.py:124-241). They are uniform across all
// concrete sensor subtypes so they are declared once here on each
// sensor type via shared helpers.
package calculated

import (
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// calcSignature builds the canonical Signature string for a calculated sensor:
//
//	{category}/{model}/{calculated_parameter}
//
// `category` is the data point category (always "sensor" for calculated DPs),
// `model` is the parent device's CCU model string (may be empty for anonymous
// fixtures), and `calcParam` is the calculated parameter name.
func calcSignature(category hmenum.DataPointCategory, model string, calcParam hmenum.CalculatedParameter) string {
	return string(category) + "/" + model + "/" + string(calcParam)
}

// --- Shared P2 helpers for all CalculatedDataPoint sensors ---

// calcDefault returns the default value for a calculated sensor's inner
// descriptor. Calculated sensors have no meaningful default in the Homematic
// paramset sense; the property always returns nil.
func calcDefault() any { return nil }

// calcIsReadable mirrors _operations = READ|EVENT (calculated/data_point.py:110).
func calcIsReadable() bool { return true }

// calcIsWritable returns false: calculated sensors are derived read-only views
// of source DPs. Mirrors `is_writable` returning false for READ-only operations.
func calcIsWritable() bool { return false }

// calcHasEvents returns true: calculated sensors emit update events when their
// synthesised value changes. Mirrors _operations = READ|EVENT.
func calcHasEvents() bool { return true }

// calcMax returns the maximum value from the inner sensor descriptor.
// Calculated float sensors have no declared max; returns (0, false).
func calcMax() (float64, bool) { return 0, false }

// calcMin is the minimum-value counterpart.
func calcMin() (float64, bool) { return 0, false }

// calcMultiplier returns 1.0.
func calcMultiplier() float64 { return 1.0 }

// calcService returns false.
func calcService() bool { return false }

// calcValues returns nil.
func calcValues() []string { return nil }

// calcDataPointNamePostfix returns "".
func calcDataPointNamePostfix() string { return "" }

// calcTranslationKey lower-cases a calculated parameter name into the
// i18n slug the package's TranslationKey accessors return.
//
// Measured: no plane consumes it. The MQTT discovery bodies take
// translation_key from their own entity-description table
// (north/mqtt/entity_descriptions_apply.go), and the only production
// .TranslationKey() calls in the tree are on event.Group, a different
// type with its own grammar. This is an internal accessor, not a key any
// consumer holds a contract on.
func calcTranslationKey(p hmenum.CalculatedParameter) string {
	s := string(p)
	result := make([]byte, len(s))
	for i := range len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + 32
		} else {
			result[i] = c
		}
	}
	return string(result)
}

// --- DewPointSensor P2 surface ---

// Default returns nil.
func (s *DewPointSensor) Default() any { return calcDefault() }

// Max returns (0, false) — no declared max.
func (s *DewPointSensor) Max() (float64, bool) { return calcMax() }

// Min returns (0, false) — no declared min.
func (s *DewPointSensor) Min() (float64, bool) { return calcMin() }

// Multiplier returns 1.0.
func (s *DewPointSensor) Multiplier() float64 { return calcMultiplier() }

// Service returns false.
func (s *DewPointSensor) Service() bool { return calcService() }

// Values returns nil (float sensor has no enum values).
func (s *DewPointSensor) Values() []string { return calcValues() }

// DataPointNamePostfix returns "".
func (s *DewPointSensor) DataPointNamePostfix() string { return calcDataPointNamePostfix() }

// HasDataPoints reports whether the sensor has at least one registered
// source DP.
func (s *DewPointSensor) HasDataPoints() bool { return s.IsRefreshedFromSources() }

// IsStatusValid reports whether all source DPs have been observed (i.e. none
// is in state-uncertain).
func (s *DewPointSensor) IsStatusValid() bool { return !s.StateUncertain() }

// TranslationKey returns the HA translation key for this sensor.
func (s *DewPointSensor) TranslationKey() string {
	return calcTranslationKey(hmenum.CalculatedParameterDewPoint)
}

// Signature returns the stable cross-stack identifier for this sensor in the
// format "sensor/{model}/DEW_POINT".
func (s *DewPointSensor) Signature() string {
	return calcSignature(hmenum.DataPointCategorySensor, s.DeviceModel, hmenum.CalculatedParameterDewPoint)
}

// ModifiedAt returns the maximum ModifiedAt time across all registered source
// DPs. Returns zero when no sources are registered.
func (s *DewPointSensor) ModifiedAt() time.Time { return s.aggregateModifiedAt() }

// RefreshedAt returns the maximum RefreshedAt time across all source
// DPs.
func (s *DewPointSensor) RefreshedAt() time.Time { return s.aggregateRefreshedAt() }

// IsStateChange reports whether the sensor value represents a meaningful
// state change. For calculated sensors this returns true when the sensor is
// refreshed and not in state-uncertain.
func (s *DewPointSensor) IsStateChange() bool { return s.IsRefreshed() && !s.StateUncertain() }

// ParamsetKey returns "CALCULATED".
func (s *DewPointSensor) ParamsetKey() string { return "CALCULATED" }

// --- DewPointSpreadSensor P2 surface ---

// Default returns nil.
func (s *DewPointSpreadSensor) Default() any { return calcDefault() }

// Max returns (0, false).
func (s *DewPointSpreadSensor) Max() (float64, bool) { return calcMax() }

// Min returns (0, false).
func (s *DewPointSpreadSensor) Min() (float64, bool) { return calcMin() }

// Multiplier returns 1.0.
func (s *DewPointSpreadSensor) Multiplier() float64 { return calcMultiplier() }

// Service returns false.
func (s *DewPointSpreadSensor) Service() bool { return calcService() }

// Values returns nil.
func (s *DewPointSpreadSensor) Values() []string { return calcValues() }

// DataPointNamePostfix returns "".
func (s *DewPointSpreadSensor) DataPointNamePostfix() string { return calcDataPointNamePostfix() }

// HasDataPoints reports whether source DPs are registered.
func (s *DewPointSpreadSensor) HasDataPoints() bool { return s.IsRefreshedFromSources() }

// IsStatusValid reports whether the sensor state is valid.
func (s *DewPointSpreadSensor) IsStatusValid() bool { return !s.StateUncertain() }

// TranslationKey returns the HA translation key.
func (s *DewPointSpreadSensor) TranslationKey() string {
	return calcTranslationKey(hmenum.CalculatedParameterDewPointSpread)
}

// Signature returns "sensor/{model}/DEW_POINT_SPREAD".
func (s *DewPointSpreadSensor) Signature() string {
	return calcSignature(hmenum.DataPointCategorySensor, s.DeviceModel, hmenum.CalculatedParameterDewPointSpread)
}

// ModifiedAt aggregates source DP modification times.
func (s *DewPointSpreadSensor) ModifiedAt() time.Time { return s.aggregateModifiedAt() }

// RefreshedAt aggregates source DP refresh times.
func (s *DewPointSpreadSensor) RefreshedAt() time.Time { return s.aggregateRefreshedAt() }

// IsStateChange reports whether a meaningful state change occurred.
func (s *DewPointSpreadSensor) IsStateChange() bool {
	return s.IsRefreshed() && !s.StateUncertain()
}

// ParamsetKey returns "CALCULATED".
func (s *DewPointSpreadSensor) ParamsetKey() string { return "CALCULATED" }

// --- FrostPointSensor P2 surface ---

// Default returns nil.
func (s *FrostPointSensor) Default() any { return calcDefault() }

// Max returns (0, false).
func (s *FrostPointSensor) Max() (float64, bool) { return calcMax() }

// Min returns (0, false).
func (s *FrostPointSensor) Min() (float64, bool) { return calcMin() }

// Multiplier returns 1.0.
func (s *FrostPointSensor) Multiplier() float64 { return calcMultiplier() }

// Service returns false.
func (s *FrostPointSensor) Service() bool { return calcService() }

// Values returns nil.
func (s *FrostPointSensor) Values() []string { return calcValues() }

// DataPointNamePostfix returns "".
func (s *FrostPointSensor) DataPointNamePostfix() string { return calcDataPointNamePostfix() }

// HasDataPoints reports whether source DPs are registered.
func (s *FrostPointSensor) HasDataPoints() bool { return s.IsRefreshedFromSources() }

// IsStatusValid reports whether the sensor state is valid.
func (s *FrostPointSensor) IsStatusValid() bool { return !s.StateUncertain() }

// TranslationKey returns the HA translation key.
func (s *FrostPointSensor) TranslationKey() string {
	return calcTranslationKey(hmenum.CalculatedParameterFrostPoint)
}

// Signature returns "sensor/{model}/FROST_POINT".
func (s *FrostPointSensor) Signature() string {
	return calcSignature(hmenum.DataPointCategorySensor, s.DeviceModel, hmenum.CalculatedParameterFrostPoint)
}

// ModifiedAt aggregates source DP modification times.
func (s *FrostPointSensor) ModifiedAt() time.Time { return s.aggregateModifiedAt() }

// RefreshedAt aggregates source DP refresh times.
func (s *FrostPointSensor) RefreshedAt() time.Time { return s.aggregateRefreshedAt() }

// IsStateChange reports whether a meaningful state change occurred.
func (s *FrostPointSensor) IsStateChange() bool { return s.IsRefreshed() && !s.StateUncertain() }

// ParamsetKey returns "CALCULATED".
func (s *FrostPointSensor) ParamsetKey() string { return "CALCULATED" }

// --- VaporConcentrationSensor P2 surface ---

// Default returns nil.
func (s *VaporConcentrationSensor) Default() any { return calcDefault() }

// Max returns (0, false).
func (s *VaporConcentrationSensor) Max() (float64, bool) { return calcMax() }

// Min returns (0, false).
func (s *VaporConcentrationSensor) Min() (float64, bool) { return calcMin() }

// Multiplier returns 1.0.
func (s *VaporConcentrationSensor) Multiplier() float64 { return calcMultiplier() }

// Service returns false.
func (s *VaporConcentrationSensor) Service() bool { return calcService() }

// Values returns nil.
func (s *VaporConcentrationSensor) Values() []string { return calcValues() }

// DataPointNamePostfix returns "".
func (s *VaporConcentrationSensor) DataPointNamePostfix() string { return calcDataPointNamePostfix() }

// HasDataPoints reports whether source DPs are registered.
func (s *VaporConcentrationSensor) HasDataPoints() bool { return s.IsRefreshedFromSources() }

// IsStatusValid reports whether the sensor state is valid.
func (s *VaporConcentrationSensor) IsStatusValid() bool { return !s.StateUncertain() }

// TranslationKey returns the HA translation key.
func (s *VaporConcentrationSensor) TranslationKey() string {
	return calcTranslationKey(hmenum.CalculatedParameterVaporConcentration)
}

// Signature returns "sensor/{model}/VAPOR_CONCENTRATION".
func (s *VaporConcentrationSensor) Signature() string {
	return calcSignature(hmenum.DataPointCategorySensor, s.DeviceModel, hmenum.CalculatedParameterVaporConcentration)
}

// ModifiedAt aggregates source DP modification times.
func (s *VaporConcentrationSensor) ModifiedAt() time.Time {
	return s.aggregateModifiedAt()
}

// RefreshedAt aggregates source DP refresh times.
func (s *VaporConcentrationSensor) RefreshedAt() time.Time {
	return s.aggregateRefreshedAt()
}

// IsStateChange reports whether a meaningful state change occurred.
func (s *VaporConcentrationSensor) IsStateChange() bool {
	return s.IsRefreshed() && !s.StateUncertain()
}

// ParamsetKey returns "CALCULATED".
func (s *VaporConcentrationSensor) ParamsetKey() string { return "CALCULATED" }

// --- EnthalpySensor P2 surface ---

// Default returns nil.
func (s *EnthalpySensor) Default() any { return calcDefault() }

// Max returns (0, false).
func (s *EnthalpySensor) Max() (float64, bool) { return calcMax() }

// Min returns (0, false).
func (s *EnthalpySensor) Min() (float64, bool) { return calcMin() }

// Multiplier returns 1.0.
func (s *EnthalpySensor) Multiplier() float64 { return calcMultiplier() }

// Service returns false.
func (s *EnthalpySensor) Service() bool { return calcService() }

// Values returns nil.
func (s *EnthalpySensor) Values() []string { return calcValues() }

// DataPointNamePostfix returns "".
func (s *EnthalpySensor) DataPointNamePostfix() string { return calcDataPointNamePostfix() }

// HasDataPoints reports whether source DPs are registered.
func (s *EnthalpySensor) HasDataPoints() bool { return s.IsRefreshedFromSources() }

// IsStatusValid reports whether the sensor state is valid.
func (s *EnthalpySensor) IsStatusValid() bool { return !s.StateUncertain() }

// TranslationKey returns the HA translation key.
func (s *EnthalpySensor) TranslationKey() string {
	return calcTranslationKey(hmenum.CalculatedParameterEnthalpy)
}

// Signature returns "sensor/{model}/ENTHALPY".
func (s *EnthalpySensor) Signature() string {
	return calcSignature(hmenum.DataPointCategorySensor, s.DeviceModel, hmenum.CalculatedParameterEnthalpy)
}

// ModifiedAt aggregates source DP modification times.
func (s *EnthalpySensor) ModifiedAt() time.Time { return s.aggregateModifiedAt() }

// RefreshedAt aggregates source DP refresh times.
func (s *EnthalpySensor) RefreshedAt() time.Time { return s.aggregateRefreshedAt() }

// IsStateChange reports whether a meaningful state change occurred.
func (s *EnthalpySensor) IsStateChange() bool { return s.IsRefreshed() && !s.StateUncertain() }

// ParamsetKey returns "CALCULATED".
func (s *EnthalpySensor) ParamsetKey() string { return "CALCULATED" }

// --- ApparentTemperatureSensor P2 surface ---

// Default returns nil.
func (s *ApparentTemperatureSensor) Default() any { return calcDefault() }

// Max returns (0, false).
func (s *ApparentTemperatureSensor) Max() (float64, bool) { return calcMax() }

// Min returns (0, false).
func (s *ApparentTemperatureSensor) Min() (float64, bool) { return calcMin() }

// Multiplier returns 1.0.
func (s *ApparentTemperatureSensor) Multiplier() float64 { return calcMultiplier() }

// Service returns false.
func (s *ApparentTemperatureSensor) Service() bool { return calcService() }

// Values returns nil.
func (s *ApparentTemperatureSensor) Values() []string { return calcValues() }

// DataPointNamePostfix returns "".
func (s *ApparentTemperatureSensor) DataPointNamePostfix() string {
	return calcDataPointNamePostfix()
}

// HasDataPoints reports whether source DPs are registered.
func (s *ApparentTemperatureSensor) HasDataPoints() bool { return s.IsRefreshedFromSources() }

// IsStatusValid reports whether the sensor state is valid.
func (s *ApparentTemperatureSensor) IsStatusValid() bool { return !s.StateUncertain() }

// TranslationKey returns the HA translation key.
func (s *ApparentTemperatureSensor) TranslationKey() string {
	return calcTranslationKey(hmenum.CalculatedParameterApparentTemperature)
}

// Signature returns "sensor/{model}/APPARENT_TEMPERATURE".
func (s *ApparentTemperatureSensor) Signature() string {
	return calcSignature(hmenum.DataPointCategorySensor, s.DeviceModel, hmenum.CalculatedParameterApparentTemperature)
}

// ModifiedAt aggregates source DP modification times.
func (s *ApparentTemperatureSensor) ModifiedAt() time.Time {
	return s.aggregateModifiedAt()
}

// RefreshedAt aggregates source DP refresh times.
func (s *ApparentTemperatureSensor) RefreshedAt() time.Time {
	return s.aggregateRefreshedAt()
}

// IsStateChange reports whether a meaningful state change occurred.
func (s *ApparentTemperatureSensor) IsStateChange() bool {
	return s.IsRefreshed() && !s.StateUncertain()
}

// ParamsetKey returns "CALCULATED".
func (s *ApparentTemperatureSensor) ParamsetKey() string { return "CALCULATED" }

// --- OperatingVoltageLevelSensor P2 surface ---

// Default returns nil.
func (s *OperatingVoltageLevelSensor) Default() any { return calcDefault() }

// Max returns (0, false) — max is model-dependent, not a fixed descriptor value.
func (s *OperatingVoltageLevelSensor) Max() (float64, bool) { return calcMax() }

// Min returns (0, false).
func (s *OperatingVoltageLevelSensor) Min() (float64, bool) { return calcMin() }

// Multiplier returns 1.0.
func (s *OperatingVoltageLevelSensor) Multiplier() float64 { return calcMultiplier() }

// Service returns false.
func (s *OperatingVoltageLevelSensor) Service() bool { return calcService() }

// Values returns nil.
func (s *OperatingVoltageLevelSensor) Values() []string { return calcValues() }

// DataPointNamePostfix returns "".
func (s *OperatingVoltageLevelSensor) DataPointNamePostfix() string {
	return calcDataPointNamePostfix()
}

// HasDataPoints reports whether source DPs are registered.
func (s *OperatingVoltageLevelSensor) HasDataPoints() bool { return s.IsRefreshedFromSources() }

// IsStatusValid reports whether the sensor state is valid.
func (s *OperatingVoltageLevelSensor) IsStatusValid() bool { return !s.StateUncertain() }

// TranslationKey returns the HA translation key.
func (s *OperatingVoltageLevelSensor) TranslationKey() string {
	return calcTranslationKey(hmenum.CalculatedParameterOperatingVoltageLevel)
}

// Signature returns "sensor/{model}/OPERATING_VOLTAGE_LEVEL".
func (s *OperatingVoltageLevelSensor) Signature() string {
	return calcSignature(hmenum.DataPointCategorySensor, s.DeviceModel, hmenum.CalculatedParameterOperatingVoltageLevel)
}

// ModifiedAt aggregates source DP modification times.
func (s *OperatingVoltageLevelSensor) ModifiedAt() time.Time {
	return s.aggregateModifiedAt()
}

// RefreshedAt aggregates source DP refresh times.
func (s *OperatingVoltageLevelSensor) RefreshedAt() time.Time {
	return s.aggregateRefreshedAt()
}

// IsStateChange reports whether a meaningful state change occurred.
func (s *OperatingVoltageLevelSensor) IsStateChange() bool {
	return s.IsRefreshed() && !s.StateUncertain()
}

// ParamsetKey returns "CALCULATED".
func (s *OperatingVoltageLevelSensor) ParamsetKey() string { return "CALCULATED" }

// --- DerivedBinarySensor P2 surface ---

// Default returns nil.
func (s *DerivedBinarySensor) Default() any { return calcDefault() }

// Max returns (0, false).
func (s *DerivedBinarySensor) Max() (float64, bool) { return calcMax() }

// Min returns (0, false).
func (s *DerivedBinarySensor) Min() (float64, bool) { return calcMin() }

// Multiplier returns 1.0.
func (s *DerivedBinarySensor) Multiplier() float64 { return calcMultiplier() }

// Service returns false.
func (s *DerivedBinarySensor) Service() bool { return calcService() }

// Values returns nil.
func (s *DerivedBinarySensor) Values() []string { return calcValues() }

// DataPointNamePostfix returns "".
func (s *DerivedBinarySensor) DataPointNamePostfix() string { return calcDataPointNamePostfix() }

// HasDataPoints reports whether source DPs are registered.
func (s *DerivedBinarySensor) HasDataPoints() bool { return s.IsRefreshedFromSources() }

// IsStatusValid reports whether the sensor state is valid.
func (s *DerivedBinarySensor) IsStatusValid() bool { return !s.StateUncertain() }

// TranslationKey returns the HA translation key derived from the
// calculated parameter name.
func (s *DerivedBinarySensor) TranslationKey() string {
	return calcTranslationKey(s.calcParam)
}

// Signature returns "binary_sensor/{model}/{calcParam}".
func (s *DerivedBinarySensor) Signature() string {
	return calcSignature(hmenum.DataPointCategoryBinarySensor, s.DeviceModel, s.calcParam)
}

// ModifiedAt aggregates source DP modification times.
func (s *DerivedBinarySensor) ModifiedAt() time.Time { return s.aggregateModifiedAt() }

// RefreshedAt aggregates source DP refresh times.
func (s *DerivedBinarySensor) RefreshedAt() time.Time {
	return s.aggregateRefreshedAt()
}

// IsStateChange reports whether a meaningful state change occurred.
func (s *DerivedBinarySensor) IsStateChange() bool { return s.IsRefreshed() && !s.StateUncertain() }

// ParamsetKey returns "CALCULATED".
func (s *DerivedBinarySensor) ParamsetKey() string { return "CALCULATED" }

// --- IsReadable / IsWritable / HasEvents for all CalculatedDataPoint sensors ---
//
// Calculated sensors expose _operations = READ|EVENT (calculated/data_point.py:110).
// These three methods give adapters a uniform surface to filter DPs without
// importing the hmenum package just to check operations.

// IsReadable reports whether this sensor's value can be read.
func (s *DewPointSensor) IsReadable() bool { return calcIsReadable() }

// IsWritable reports whether this sensor's value can be written.
func (s *DewPointSensor) IsWritable() bool { return calcIsWritable() }

// HasEvents reports whether this sensor emits push events.
func (s *DewPointSensor) HasEvents() bool { return calcHasEvents() }

// IsReadable reports whether this sensor's value can be read.
func (s *DewPointSpreadSensor) IsReadable() bool { return calcIsReadable() }

// IsWritable reports whether this sensor's value can be written.
func (s *DewPointSpreadSensor) IsWritable() bool { return calcIsWritable() }

// HasEvents reports whether this sensor emits push events.
func (s *DewPointSpreadSensor) HasEvents() bool { return calcHasEvents() }

// IsReadable reports whether this sensor's value can be read.
func (s *FrostPointSensor) IsReadable() bool { return calcIsReadable() }

// IsWritable reports whether this sensor's value can be written.
func (s *FrostPointSensor) IsWritable() bool { return calcIsWritable() }

// HasEvents reports whether this sensor emits push events.
func (s *FrostPointSensor) HasEvents() bool { return calcHasEvents() }

// IsReadable reports whether this sensor's value can be read.
func (s *VaporConcentrationSensor) IsReadable() bool { return calcIsReadable() }

// IsWritable reports whether this sensor's value can be written.
func (s *VaporConcentrationSensor) IsWritable() bool { return calcIsWritable() }

// HasEvents reports whether this sensor emits push events.
func (s *VaporConcentrationSensor) HasEvents() bool { return calcHasEvents() }

// IsReadable reports whether this sensor's value can be read.
func (s *EnthalpySensor) IsReadable() bool { return calcIsReadable() }

// IsWritable reports whether this sensor's value can be written.
func (s *EnthalpySensor) IsWritable() bool { return calcIsWritable() }

// HasEvents reports whether this sensor emits push events.
func (s *EnthalpySensor) HasEvents() bool { return calcHasEvents() }

// IsReadable reports whether this sensor's value can be read.
func (s *ApparentTemperatureSensor) IsReadable() bool { return calcIsReadable() }

// IsWritable reports whether this sensor's value can be written.
func (s *ApparentTemperatureSensor) IsWritable() bool { return calcIsWritable() }

// HasEvents reports whether this sensor emits push events.
func (s *ApparentTemperatureSensor) HasEvents() bool { return calcHasEvents() }

// IsReadable reports whether this sensor's value can be read.
func (s *OperatingVoltageLevelSensor) IsReadable() bool { return calcIsReadable() }

// IsWritable reports whether this sensor's value can be written.
func (s *OperatingVoltageLevelSensor) IsWritable() bool { return calcIsWritable() }

// HasEvents reports whether this sensor emits push events.
func (s *OperatingVoltageLevelSensor) HasEvents() bool { return calcHasEvents() }

// IsReadable reports whether this sensor's value can be read.
func (s *DerivedBinarySensor) IsReadable() bool { return calcIsReadable() }

// IsWritable reports whether this sensor's value can be written.
func (s *DerivedBinarySensor) IsWritable() bool { return calcIsWritable() }

// HasEvents reports whether this sensor emits push events.
func (s *DerivedBinarySensor) HasEvents() bool { return calcHasEvents() }
