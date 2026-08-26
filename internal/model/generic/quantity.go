// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// QuantityMetadata bundles a parameter's semantic [hmenum.Quantity] with its
// [hmenum.ValueBehavior].
//
// `Quantity` is `QuantityNone` when the parameter has no semantic
// classification (e.g. raw `LEVEL` is just a fraction). `Behavior` is the
// most useful field for HA `state_class` derivation — MONOTONIC counters
// surface as `total_increasing`, INSTANTANEOUS readings as `measurement`, the
// rest stay unset.
type QuantityMetadata struct {
	Quantity hmenum.Quantity
	Behavior hmenum.ValueBehavior
}

// SensorMetadataByParam mirrors
// `_SENSOR_METADATA_BY_PARAM` map (model/data_point_metadata.py:37).
// Keys are the wire parameter names; the entries cover every sensor
// Parameter
// (DEW_POINT, ENTHALPY, …) the daemon also surfaces.
var sensorMetadataByParam = map[hmenum.Parameter]QuantityMetadata{
	"AIR_PRESSURE":                          {Quantity: hmenum.QuantityPressure, Behavior: hmenum.ValueBehaviorInstantaneous},
	"BRIGHTNESS":                            {Behavior: hmenum.ValueBehaviorInstantaneous},
	"CARRIER_SENSE_LEVEL":                   {Behavior: hmenum.ValueBehaviorInstantaneous},
	"CONCENTRATION":                         {Quantity: hmenum.QuantityCO2, Behavior: hmenum.ValueBehaviorInstantaneous},
	"CURRENT":                               {Quantity: hmenum.QuantityCurrent, Behavior: hmenum.ValueBehaviorInstantaneous},
	"DEWPOINT":                              {Quantity: hmenum.QuantityTemperature, Behavior: hmenum.ValueBehaviorInstantaneous},
	"ACTIVITY_STATE":                        {Quantity: hmenum.QuantityEnum},
	"DIRECTION":                             {Quantity: hmenum.QuantityEnum},
	"DOOR_STATE":                            {Quantity: hmenum.QuantityEnum},
	"DUTY_CYCLE_LEVEL":                      {Behavior: hmenum.ValueBehaviorInstantaneous},
	"ENERGY_COUNTER":                        {Quantity: hmenum.QuantityEnergy, Behavior: hmenum.ValueBehaviorMonotonic},
	"FILLING_LEVEL":                         {Behavior: hmenum.ValueBehaviorInstantaneous},
	"FREQUENCY":                             {Quantity: hmenum.QuantityFrequency, Behavior: hmenum.ValueBehaviorInstantaneous},
	"GAS_ENERGY_COUNTER":                    {Quantity: hmenum.QuantityGas, Behavior: hmenum.ValueBehaviorMonotonic},
	"GAS_FLOW":                              {Quantity: hmenum.QuantityVolumeFlowRate, Behavior: hmenum.ValueBehaviorInstantaneous},
	"GAS_VOLUME":                            {Quantity: hmenum.QuantityGas, Behavior: hmenum.ValueBehaviorMonotonic},
	"HUMIDITY":                              {Quantity: hmenum.QuantityHumidity, Behavior: hmenum.ValueBehaviorInstantaneous},
	"ACTUAL_HUMIDITY":                       {Quantity: hmenum.QuantityHumidity, Behavior: hmenum.ValueBehaviorInstantaneous},
	"IEC_ENERGY_COUNTER":                    {Quantity: hmenum.QuantityEnergy, Behavior: hmenum.ValueBehaviorMonotonic},
	"IEC_POWER":                             {Quantity: hmenum.QuantityPower, Behavior: hmenum.ValueBehaviorInstantaneous},
	"ILLUMINATION":                          {Quantity: hmenum.QuantityIlluminance, Behavior: hmenum.ValueBehaviorInstantaneous},
	"AVERAGE_ILLUMINATION":                  {Quantity: hmenum.QuantityIlluminance, Behavior: hmenum.ValueBehaviorInstantaneous},
	"CURRENT_ILLUMINATION":                  {Quantity: hmenum.QuantityIlluminance, Behavior: hmenum.ValueBehaviorInstantaneous},
	"HIGHEST_ILLUMINATION":                  {Quantity: hmenum.QuantityIlluminance, Behavior: hmenum.ValueBehaviorInstantaneous},
	"LOWEST_ILLUMINATION":                   {Quantity: hmenum.QuantityIlluminance, Behavior: hmenum.ValueBehaviorInstantaneous},
	"LUX":                                   {Quantity: hmenum.QuantityIlluminance, Behavior: hmenum.ValueBehaviorInstantaneous},
	"LEVEL":                                 {Behavior: hmenum.ValueBehaviorInstantaneous},
	"LEVEL_2":                               {Behavior: hmenum.ValueBehaviorInstantaneous},
	"LOCK_STATE":                            {Quantity: hmenum.QuantityEnum},
	"MASS_CONCENTRATION_PM_1":               {Quantity: hmenum.QuantityPM1, Behavior: hmenum.ValueBehaviorInstantaneous},
	"MASS_CONCENTRATION_PM_1_24H_AVERAGE":   {Quantity: hmenum.QuantityPM1, Behavior: hmenum.ValueBehaviorInstantaneous},
	"MASS_CONCENTRATION_PM_10":              {Quantity: hmenum.QuantityPM10, Behavior: hmenum.ValueBehaviorInstantaneous},
	"MASS_CONCENTRATION_PM_10_24H_AVERAGE":  {Quantity: hmenum.QuantityPM10, Behavior: hmenum.ValueBehaviorInstantaneous},
	"MASS_CONCENTRATION_PM_2_5":             {Quantity: hmenum.QuantityPM25, Behavior: hmenum.ValueBehaviorInstantaneous},
	"MASS_CONCENTRATION_PM_2_5_24H_AVERAGE": {Quantity: hmenum.QuantityPM25, Behavior: hmenum.ValueBehaviorInstantaneous},
	"NUMBER_CONCENTRATION_PM_1":             {Behavior: hmenum.ValueBehaviorInstantaneous},
	"NUMBER_CONCENTRATION_PM_10":            {Behavior: hmenum.ValueBehaviorInstantaneous},
	"NUMBER_CONCENTRATION_PM_2_5":           {Behavior: hmenum.ValueBehaviorInstantaneous},
	"TYPICAL_PARTICLE_SIZE":                 {Behavior: hmenum.ValueBehaviorInstantaneous},
	"BATTERY_STATE":                         {Quantity: hmenum.QuantityVoltage, Behavior: hmenum.ValueBehaviorInstantaneous},
	"OPERATING_VOLTAGE":                     {Quantity: hmenum.QuantityVoltage, Behavior: hmenum.ValueBehaviorInstantaneous},
	"POWER":                                 {Quantity: hmenum.QuantityPower, Behavior: hmenum.ValueBehaviorInstantaneous},
	"RAIN_COUNTER":                          {Behavior: hmenum.ValueBehaviorMonotonic},
	"RSSI_DEVICE":                           {Quantity: hmenum.QuantitySignalStrength, Behavior: hmenum.ValueBehaviorInstantaneous},
	"RSSI_PEER":                             {Quantity: hmenum.QuantitySignalStrength, Behavior: hmenum.ValueBehaviorInstantaneous},
	"ACTUAL_TEMPERATURE":                    {Quantity: hmenum.QuantityTemperature, Behavior: hmenum.ValueBehaviorInstantaneous},
	"TEMPERATURE":                           {Quantity: hmenum.QuantityTemperature, Behavior: hmenum.ValueBehaviorInstantaneous},
	"SET_POINT_TEMPERATURE":                 {Quantity: hmenum.QuantityTemperature, Behavior: hmenum.ValueBehaviorInstantaneous},
	"SET_TEMPERATURE":                       {Quantity: hmenum.QuantityTemperature, Behavior: hmenum.ValueBehaviorInstantaneous},
	"PARTY_TEMPERATURE":                     {Quantity: hmenum.QuantityTemperature, Behavior: hmenum.ValueBehaviorInstantaneous},
	"SMOKE_DETECTOR_ALARM_STATUS":           {Quantity: hmenum.QuantityEnum},
	"SUNSHINEDURATION":                      {Behavior: hmenum.ValueBehaviorMonotonic},
	"VALUE":                                 {Behavior: hmenum.ValueBehaviorInstantaneous},
	"VAPOR_CONCENTRATION":                   {Behavior: hmenum.ValueBehaviorInstantaneous},
	"VOLTAGE":                               {Quantity: hmenum.QuantityVoltage, Behavior: hmenum.ValueBehaviorInstantaneous},
	"WIND_DIR":                              {Behavior: hmenum.ValueBehaviorInstantaneous},
	"WIND_DIR_RANGE":                        {Behavior: hmenum.ValueBehaviorInstantaneous},
	"WIND_DIRECTION":                        {Behavior: hmenum.ValueBehaviorInstantaneous},
	"WIND_DIRECTION_RANGE":                  {Behavior: hmenum.ValueBehaviorInstantaneous},
	"WIND_SPEED":                            {Quantity: hmenum.QuantityWindSpeed, Behavior: hmenum.ValueBehaviorInstantaneous},
	// Calculated derivations the daemon synthesises and exposes as
	// Regular sensors. These have no
	// `_SENSOR_METADATA_BY_PARAM` (Python looks them up under the
	// CalculatedParameter enum), but the lookup shape is the same.
	"DEW_POINT":               {Quantity: hmenum.QuantityTemperature, Behavior: hmenum.ValueBehaviorInstantaneous},
	"DEW_POINT_SPREAD":        {Behavior: hmenum.ValueBehaviorInstantaneous},
	"FROST_POINT":             {Quantity: hmenum.QuantityTemperature, Behavior: hmenum.ValueBehaviorInstantaneous},
	"APPARENT_TEMPERATURE":    {Quantity: hmenum.QuantityTemperature, Behavior: hmenum.ValueBehaviorInstantaneous},
	"ENTHALPY":                {Behavior: hmenum.ValueBehaviorInstantaneous},
	"OPERATING_VOLTAGE_LEVEL": {Quantity: hmenum.QuantityBattery, Behavior: hmenum.ValueBehaviorInstantaneous},
}

// SensorMetadataByUnit mirrors
// `_SENSOR_METADATA_BY_UNIT` (data_point_metadata.py:241). Used as a
// last-resort fallback when neither the param-keyed map nor the
// device-keyed overrides match.
var sensorMetadataByUnit = map[string]QuantityMetadata{
	"%":    {Behavior: hmenum.ValueBehaviorInstantaneous},
	"bar":  {Quantity: hmenum.QuantityPressure, Behavior: hmenum.ValueBehaviorInstantaneous},
	"°C":   {Quantity: hmenum.QuantityTemperature, Behavior: hmenum.ValueBehaviorInstantaneous},
	"g/m³": {Behavior: hmenum.ValueBehaviorInstantaneous},
}

// sensorMetadataByDeviceAndParam holds device-model-prefix overrides. The
// outer key is a model prefix (case-insensitive), the inner key is the
// parameter name.
var sensorMetadataByDeviceAndParam = map[string]map[hmenum.Parameter]QuantityMetadata{
	"HmIP-WKP":       {"CODE_STATE": {Quantity: hmenum.QuantityEnum}},
	"HmIP-SRH":       {"STATE": {Quantity: hmenum.QuantityEnum}},
	"HM-Sec-RHS":     {"STATE": {Quantity: hmenum.QuantityEnum}},
	"HM-Sec-xx":      {"STATE": {Quantity: hmenum.QuantityEnum}},
	"ZEL STG RM FDK": {"STATE": {Quantity: hmenum.QuantityEnum}},
	"HM-Sec-Win": {
		"STATUS":    {Quantity: hmenum.QuantityEnum},
		"DIRECTION": {Quantity: hmenum.QuantityEnum},
		"ERROR":     {Quantity: hmenum.QuantityEnum},
	},
	"HM-Sec-Key": {
		"DIRECTION": {Quantity: hmenum.QuantityEnum},
		"ERROR":     {Quantity: hmenum.QuantityEnum},
	},
	"HM-CC-RT-DN": {"VALVE_STATE": {Behavior: hmenum.ValueBehaviorInstantaneous}},
	"HM-CC-VD":    {"VALVE_STATE": {Behavior: hmenum.ValueBehaviorInstantaneous}},
}

// BinarySensorQuantityByParam mirrors
// `_BINARY_SENSOR_QUANTITY_BY_PARAM` (data_point_metadata.py:262) —
// per-parameter binary-sensor classification used to derive HA's
// `device_class` on `binary_sensor` entities.
var binarySensorQuantityByParam = map[hmenum.Parameter]hmenum.Quantity{
	"ALARMSTATE":               hmenum.QuantitySafety,
	"ACOUSTIC_ALARM_ACTIVE":    hmenum.QuantitySafety,
	"BLOCKED_PERMANENT":        hmenum.QuantityProblem,
	"BLOCKED_TEMPORARY":        hmenum.QuantityProblem,
	"BURST_LIMIT_WARNING":      hmenum.QuantityProblem,
	"DUTYCYCLE":                hmenum.QuantityProblem,
	"DUTY_CYCLE":               hmenum.QuantityProblem,
	"DEW_POINT_ALARM":          hmenum.QuantityProblem,
	"EMERGENCY_OPERATION":      hmenum.QuantitySafety,
	"ERROR_JAMMED":             hmenum.QuantityProblem,
	"HEATER_STATE":             hmenum.QuantityHeat,
	"LOWBAT":                   hmenum.QuantityBattery,
	"LOW_BAT":                  hmenum.QuantityBattery,
	"LOWBAT_SENSOR":            hmenum.QuantityBattery,
	"MOISTURE_DETECTED":        hmenum.QuantityMoisture,
	"MOTION":                   hmenum.QuantityMotion,
	"OPTICAL_ALARM_ACTIVE":     hmenum.QuantitySafety,
	"POWER_MAINS_FAILURE":      hmenum.QuantityProblem,
	"PRESENCE_DETECTION_STATE": hmenum.QuantityPresence,
	"PROCESS":                  hmenum.QuantityRunning,
	"WORKING":                  hmenum.QuantityRunning,
	"RAINING":                  hmenum.QuantityMoisture,
	"SABOTAGE":                 hmenum.QuantityTamper,
	"SABOTAGE_STICKY":          hmenum.QuantityTamper,
	"WATERLEVEL_DETECTED":      hmenum.QuantityMoisture,
	"WINDOW_STATE":             hmenum.QuantityWindow,
}

// BinarySensorQuantityByDeviceAndParam mirrors
// `_BINARY_SENSOR_QUANTITY_BY_DEVICE_AND_PARAM`
// (data_point_metadata.py:289).
var binarySensorQuantityByDeviceAndParam = map[string]map[hmenum.Parameter]hmenum.Quantity{
	"HmIP-DSD-PCB":   {"STATE": hmenum.QuantityOccupancy},
	"HmIP-SCI":       {"STATE": hmenum.QuantityOpening},
	"HmIP-FCI1":      {"STATE": hmenum.QuantityOpening},
	"HmIP-FCI6":      {"STATE": hmenum.QuantityOpening},
	"HM-Sec-SD":      {"STATE": hmenum.QuantitySmoke},
	"HmIP-SWDO":      {"STATE": hmenum.QuantityWindow},
	"HmIP-SWDM":      {"STATE": hmenum.QuantityWindow},
	"HM-Sec-SC":      {"STATE": hmenum.QuantityWindow},
	"HM-SCI-3-FM":    {"STATE": hmenum.QuantityWindow},
	"ZEL STG RM FFK": {"STATE": hmenum.QuantityWindow},
	"HM-Sen-RD-O":    {"STATE": hmenum.QuantityMoisture},
	"HM-Sec-Win":     {"WORKING": hmenum.QuantityRunning},
}

// SensorQuantityMetadataForParameter looks up per-parameter sensor
// metadata.
// Returns the zero-value metadata + false when the parameter has
// no entry.
func SensorQuantityMetadataForParameter(p hmenum.Parameter) (QuantityMetadata, bool) {
	m, ok := sensorMetadataByParam[p]
	return m, ok
}

// SensorQuantityMetadataForDeviceParameter looks up device+parameter
// Sensor overrides.
// `get_quantity_metadata_by_device_and_param`. The model is matched
// Case-insensitively via prefix to honour
// `_model_matches`.
func SensorQuantityMetadataForDeviceParameter(model string, p hmenum.Parameter) (QuantityMetadata, bool) {
	if model == "" {
		return QuantityMetadata{}, false
	}
	for k, params := range sensorMetadataByDeviceAndParam {
		if !hasPrefixFold(model, k) {
			continue
		}
		if m, ok := params[p]; ok {
			return m, true
		}
	}
	return QuantityMetadata{}, false
}

// SensorQuantityMetadataForUnit looks up a unit-based fallback.
func SensorQuantityMetadataForUnit(unit string) (QuantityMetadata, bool) {
	m, ok := sensorMetadataByUnit[unit]
	return m, ok
}

// BinarySensorQuantityForParameter looks up the binary-sensor quantity for a
// parameter.
func BinarySensorQuantityForParameter(p hmenum.Parameter) (hmenum.Quantity, bool) {
	q, ok := binarySensorQuantityByParam[p]
	return q, ok
}

// BinarySensorQuantityForDeviceParameter looks up the device-specific
// binary-sensor quantity override.
func BinarySensorQuantityForDeviceParameter(model string, p hmenum.Parameter) (hmenum.Quantity, bool) {
	if model == "" {
		return hmenum.QuantityNone, false
	}
	for k, params := range binarySensorQuantityByDeviceAndParam {
		if !hasPrefixFold(model, k) {
			continue
		}
		if q, ok := params[p]; ok {
			return q, true
		}
	}
	return hmenum.QuantityNone, false
}

// QuantityForParameter returns the canonical [hmenum.Quantity] a
// given wire parameter reports. Resolution order mirrors
//
//  1. Sensor parameter map → return its Quantity (may be empty).
//  2. Binary-sensor parameter map → return its Quantity.
//  3. Unknown parameter → [hmenum.QuantityNone].
//
// Unit-fallback and device+param overrides are exposed through the
// dedicated helpers above; this convenience wrapper covers the
// param-only case to keep existing callers compiling. North-bound
// adapters that need unit / device overrides should call the
// matching helper directly.
func QuantityForParameter(p hmenum.Parameter) hmenum.Quantity {
	if m, ok := sensorMetadataByParam[p]; ok && m.Quantity != hmenum.QuantityNone {
		return m.Quantity
	}
	if q, ok := binarySensorQuantityByParam[p]; ok {
		return q
	}
	return hmenum.QuantityNone
}

// QuantityForDeviceParameter is the device-aware variant. Resolution
// Order mirrors
//
//  1. Sensor device+param override (wins for HmIP-WKP.CODE_STATE,
//     HM-Sec-RHS.STATE, HM-Sec-Win.STATUS, …).
//  2. Binary-sensor device+param override (wins for HmIP-SWDO.STATE
//     → window, HmIP-SCI.STATE → opening, HM-Sec-SD.STATE → smoke …).
//  3. Sensor param-only metadata.
//  4. Binary-sensor param-only metadata.
//  5. Empty model → fall through to [QuantityForParameter] only.
//
// Used by north-bound adapters (REST, WS, MQTT discovery) when
// the device model is in scope and a binary sensor's HA
// `device_class` should reflect the per-device override (e.g. an
// HmIP-SWDO.STATE has Quantity.WINDOW, not the generic STATE
// reading).
func QuantityForDeviceParameter(model string, p hmenum.Parameter) hmenum.Quantity {
	if model != "" {
		if m, ok := SensorQuantityMetadataForDeviceParameter(model, p); ok && m.Quantity != hmenum.QuantityNone {
			return m.Quantity
		}
		if q, ok := BinarySensorQuantityForDeviceParameter(model, p); ok {
			return q
		}
	}
	return QuantityForParameter(p)
}

// ValueBehaviorForParameter returns the parameter's
// [hmenum.ValueBehavior] (INSTANTANEOUS / MONOTONIC / NONE). Used
// by the MQTT discovery builder to derive HA's `state_class`:
// MONOTONIC → `total_increasing`, INSTANTANEOUS → `measurement`.
func ValueBehaviorForParameter(p hmenum.Parameter) hmenum.ValueBehavior {
	if m, ok := sensorMetadataByParam[p]; ok {
		return m.Behavior
	}
	return hmenum.ValueBehaviorNone
}

// Quantity returns the semantic [hmenum.Quantity] this data point reports.
//
// 1. `Category() == BinarySensor` → device+param binary-sensor override →
// param-only binary-sensor. 2. Sensor device+param override (per-model
// overlay). 3. Sensor param-only metadata. 4. Unit fallback (raw
// `Descriptor.Unit`, after CleanupUnit).
//
// The device-aware lookups consult `Config.DeviceModel` set by the pipeline
// at construction time. When `DeviceModel` is empty (test fixtures, virtual
// DPs) the chain degrades to the param-only path.
func (d *DataPoint[T]) Quantity() hmenum.Quantity {
	param := d.Parameter()
	model := d.DeviceModel

	if d.Category() == hmenum.DataPointCategoryBinarySensor {
		if model != "" {
			if q, ok := BinarySensorQuantityForDeviceParameter(model, param); ok {
				return q
			}
		}
		if q, ok := BinarySensorQuantityForParameter(param); ok {
			return q
		}
		return hmenum.QuantityNone
	}

	if model != "" {
		if m, ok := SensorQuantityMetadataForDeviceParameter(model, param); ok && m.Quantity != hmenum.QuantityNone {
			return m.Quantity
		}
	}
	if m, ok := SensorQuantityMetadataForParameter(param); ok && m.Quantity != hmenum.QuantityNone {
		return m.Quantity
	}
	// Unit-fallback as last resort. Use the cleaned unit so spelling
	// quirks ("Lux" vs "lx") don't break the lookup.
	if cleaned := CleanupUnit(param, d.Descriptor.Unit); cleaned != "" {
		if m, ok := SensorQuantityMetadataForUnit(cleaned); ok && m.Quantity != hmenum.QuantityNone {
			return m.Quantity
		}
	}
	return hmenum.QuantityNone
}

// ValueBehavior returns the value-behavior classification for this data point
// using a three-stage read chain:
//
//  1. Device+parameter override (sensorMetadataByDeviceAndParam) — per-model
//     specialisation, e.g. HM-CC-RT-DN.VALVE_STATE → INSTANTANEOUS.
//  2. Parameter-only map (sensorMetadataByParam).
//  3. Unit fallback (sensorMetadataByUnit) — last resort using the
//     descriptor's unit string after [CleanupUnit].
//
// Returns [hmenum.ValueBehaviorNone] when none of the stages match.
// Used by MQTT discovery to derive HA's state_class: MONOTONIC →
// total_increasing, INSTANTANEOUS → measurement.
func (d *DataPoint[T]) ValueBehavior() hmenum.ValueBehavior {
	param := d.Parameter()
	model := d.DeviceModel

	if model != "" {
		if m, ok := SensorQuantityMetadataForDeviceParameter(model, param); ok && m.Behavior != hmenum.ValueBehaviorNone {
			return m.Behavior
		}
	}
	if m, ok := SensorQuantityMetadataForParameter(param); ok {
		return m.Behavior
	}
	if cleaned := CleanupUnit(param, d.Descriptor.Unit); cleaned != "" {
		if m, ok := SensorQuantityMetadataForUnit(cleaned); ok && m.Behavior != hmenum.ValueBehaviorNone {
			return m.Behavior
		}
	}
	return hmenum.ValueBehaviorNone
}

// hasPrefixFold reports whether s starts with prefix (case-fold).
// Inlined to avoid a strings.EqualFold per loop iteration during
// the lookup.
func hasPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return strings.EqualFold(s[:len(prefix)], prefix)
}
