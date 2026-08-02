// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package calculated

import (
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// calculatedKeyName builds the canonical key segment used by the promoted
// [datapoint.BaseDataPointFields.UniqueID] of a calculated data point. The
// returned shape is `CALCULATED/<paramName>` so the daemon surfaces every
// derived sensor under a stable, family-prefixed token across MQTT / REST /
// WS adapters.
func calculatedKeyName(p hmenum.CalculatedParameter) string {
	return "CALCULATED/" + string(p)
}

// climateInputs holds the source values every climate-derived sensor
// reads from. A value becomes "present" after its On* method has been
// called at least once.
type climateInputs struct {
	temperature    float64
	hasTemperature bool
	humidity       float64
	hasHumidity    bool
	windSpeed      float64
	hasWindSpeed   bool
	pressureHPa    float64
	hasPressure    bool
}

// feedSink writes value into sink if the formula yielded ok AND the value is
// different from the previous emission. Dedup lives here so subscribers of
// generic.Sensor.OnUpdate don't receive no-op events.
//
// If the sensor itself has published within the last 500 ms, suppress the
// call so rapid CCU bursts don't produce intermediate values. The
// sources-level guard ([shouldPublishCalcUpdate]) is evaluated separately by
// the per-sensor On* methods via the passed sources slice.
func feedSink(sink *generic.Sensor[float64], value float64, ok bool, prev *float64, hasPrev *bool, sources []SourceDP) {
	if !ok {
		return
	}
	if *hasPrev && *prev == value {
		return
	}
	// Sensor-level guard: don't publish if the sensor just published.
	if sink.PublishedEventRecently() {
		return
	}
	// Sources-level guard: suppress when all (≥2) sources just published.
	if !shouldPublishCalcUpdate(sources) {
		return
	}
	*prev = value
	*hasPrev = true
	sink.OnEvent(value)
}

// --- DewPoint sensor ---

// DewPointSensor emits the dew point derived from TEMPERATURE + HUMIDITY.
//
// The inner [generic.Sensor] embeds [datapoint.BaseDataPointFields] and is
// constructed via [newDerivedFloatSensor] with
// [generic.Spec.KeyNameOverride] = `CALCULATED/DEW_POINT`. The promoted
// [datapoint.BaseDataPointFields.UniqueID] therefore renders as
// `<central>:<channelAddress>:CALCULATED/DEW_POINT` directly from the inner
// sensor — no outer BaseDataPointFields embed (V2 fix: removed in PR-32 to
// avoid the dual-embed pattern where the outer MarkForcedSensor had no effect
// on the inner Usage / Category).
//
// [StateUncertain] aggregates over registered source DPs (temperature and
// humidity) via the embedded [sourceSink].
type DewPointSensor struct {
	*generic.Sensor[float64]
	sourceSink
	in      climateInputs
	last    float64
	hasLast bool
}

// NewDewPointSensor constructs the sensor with no central / channel
// scoping. The promoted [datapoint.BaseDataPointFields.UniqueID]
// renders as `::CALCULATED/DEW_POINT`. Multi-CCU-safe call sites MUST
// use [NewDewPointSensorWithIdentity].
func NewDewPointSensor() *DewPointSensor {
	return NewDewPointSensorWithIdentity("", "")
}

// NewDewPointSensorWithIdentity constructs a DewPointSensor rooted at
// `<central>:<channelAddress>:CALCULATED/DEW_POINT`. ADR 0002 (multi-
// CCU first-class) requires production callers to set both segments.
func NewDewPointSensorWithIdentity(centralName, channelAddress string) *DewPointSensor {
	s := &DewPointSensor{
		Sensor: newDerivedFloatSensor(hmenum.CalculatedParameterDewPoint, centralName, channelAddress),
	}
	installSourceValidityGate(s.Sensor, &s.sourceSink)
	return s
}

// OnTemperature feeds a temperature observation and recomputes.
func (s *DewPointSensor) OnTemperature(v float64) {
	s.in.temperature, s.in.hasTemperature = v, true
	s.recompute()
}

// OnHumidity feeds a humidity observation and recomputes.
func (s *DewPointSensor) OnHumidity(v float64) {
	s.in.humidity, s.in.hasHumidity = v, true
	s.recompute()
}

func (s *DewPointSensor) recompute() {
	if !s.in.hasTemperature || !s.in.hasHumidity {
		return
	}
	v, ok := DewPoint(s.in.temperature, s.in.humidity)
	feedSink(s.Sensor, v, ok, &s.last, &s.hasLast, s.sources)
}

// --- DewPointSpread sensor ---

// DewPointSpreadSensor emits the dew-point spread.
//
// The inner [generic.Sensor] carries the canonical
// `<central>:<channelAddress>:CALCULATED/DEW_POINT_SPREAD` UniqueID
// via [generic.Spec.KeyNameOverride]; no outer BaseDataPointFields
// embed (V2 fix, PR-32).
//
// [StateUncertain] aggregates over registered source DPs via the
// embedded [sourceSink].
type DewPointSpreadSensor struct {
	*generic.Sensor[float64]
	sourceSink
	in      climateInputs
	last    float64
	hasLast bool
}

// NewDewPointSpreadSensor constructs the sensor with no central
// channel scoping. Multi-CCU-safe call sites MUST use
// [NewDewPointSpreadSensorWithIdentity].
func NewDewPointSpreadSensor() *DewPointSpreadSensor {
	return NewDewPointSpreadSensorWithIdentity("", "")
}

// NewDewPointSpreadSensorWithIdentity constructs the sensor rooted at
// `<central>:<channelAddress>:CALCULATED/DEW_POINT_SPREAD`.
func NewDewPointSpreadSensorWithIdentity(centralName, channelAddress string) *DewPointSpreadSensor {
	s := &DewPointSpreadSensor{
		Sensor: newDerivedFloatSensor(hmenum.CalculatedParameterDewPointSpread, centralName, channelAddress),
	}
	installSourceValidityGate(s.Sensor, &s.sourceSink)
	return s
}

// OnTemperature feeds a temperature observation.
func (s *DewPointSpreadSensor) OnTemperature(v float64) {
	s.in.temperature, s.in.hasTemperature = v, true
	s.recompute()
}

// OnHumidity feeds a humidity observation.
func (s *DewPointSpreadSensor) OnHumidity(v float64) {
	s.in.humidity, s.in.hasHumidity = v, true
	s.recompute()
}

func (s *DewPointSpreadSensor) recompute() {
	if !s.in.hasTemperature || !s.in.hasHumidity {
		return
	}
	v, ok := DewPointSpread(s.in.temperature, s.in.humidity)
	feedSink(s.Sensor, v, ok, &s.last, &s.hasLast, s.sources)
}

// --- FrostPoint sensor ---

// FrostPointSensor emits the frost point.
//
// The inner [generic.Sensor] carries the canonical
// `<central>:<channelAddress>:CALCULATED/FROST_POINT` UniqueID via
// [generic.Spec.KeyNameOverride]; no outer BaseDataPointFields
// embed (V2 fix, PR-32).
//
// [StateUncertain] aggregates over registered source DPs via the
// embedded [sourceSink].
type FrostPointSensor struct {
	*generic.Sensor[float64]
	sourceSink
	in      climateInputs
	last    float64
	hasLast bool
}

// NewFrostPointSensor constructs the sensor with no central / channel
// scoping. Multi-CCU-safe call sites MUST use
// [NewFrostPointSensorWithIdentity].
func NewFrostPointSensor() *FrostPointSensor {
	return NewFrostPointSensorWithIdentity("", "")
}

// NewFrostPointSensorWithIdentity constructs the sensor rooted at
// `<central>:<channelAddress>:CALCULATED/FROST_POINT`.
func NewFrostPointSensorWithIdentity(centralName, channelAddress string) *FrostPointSensor {
	s := &FrostPointSensor{
		Sensor: newDerivedFloatSensor(hmenum.CalculatedParameterFrostPoint, centralName, channelAddress),
	}
	installSourceValidityGate(s.Sensor, &s.sourceSink)
	return s
}

// OnTemperature feeds a temperature observation.
func (s *FrostPointSensor) OnTemperature(v float64) {
	s.in.temperature, s.in.hasTemperature = v, true
	s.recompute()
}

// OnHumidity feeds a humidity observation.
func (s *FrostPointSensor) OnHumidity(v float64) {
	s.in.humidity, s.in.hasHumidity = v, true
	s.recompute()
}

func (s *FrostPointSensor) recompute() {
	if !s.in.hasTemperature || !s.in.hasHumidity {
		return
	}
	v, ok := FrostPoint(s.in.temperature, s.in.humidity)
	feedSink(s.Sensor, v, ok, &s.last, &s.hasLast, s.sources)
}

// --- VaporConcentration sensor ---

// VaporConcentrationSensor emits g/m³ water-vapor concentration.
//
// The inner [generic.Sensor] carries the canonical
// `<central>:<channelAddress>:CALCULATED/VAPOR_CONCENTRATION`
// UniqueID via [generic.Spec.KeyNameOverride]; no outer
// BaseDataPointFields embed (V2 fix, PR-32).
//
// [StateUncertain] aggregates over registered source DPs via the
// embedded [sourceSink].
type VaporConcentrationSensor struct {
	*generic.Sensor[float64]
	sourceSink
	in      climateInputs
	last    float64
	hasLast bool
}

// NewVaporConcentrationSensor constructs the sensor with no central
// channel scoping. Multi-CCU-safe call sites MUST use
// [NewVaporConcentrationSensorWithIdentity].
func NewVaporConcentrationSensor() *VaporConcentrationSensor {
	return NewVaporConcentrationSensorWithIdentity("", "")
}

// NewVaporConcentrationSensorWithIdentity constructs the sensor rooted
// at `<central>:<channelAddress>:CALCULATED/VAPOR_CONCENTRATION`.
func NewVaporConcentrationSensorWithIdentity(centralName, channelAddress string) *VaporConcentrationSensor {
	s := &VaporConcentrationSensor{
		Sensor: newDerivedFloatSensor(hmenum.CalculatedParameterVaporConcentration, centralName, channelAddress),
	}
	installSourceValidityGate(s.Sensor, &s.sourceSink)
	return s
}

// OnTemperature feeds a temperature observation.
func (s *VaporConcentrationSensor) OnTemperature(v float64) {
	s.in.temperature, s.in.hasTemperature = v, true
	s.recompute()
}

// OnHumidity feeds a humidity observation.
func (s *VaporConcentrationSensor) OnHumidity(v float64) {
	s.in.humidity, s.in.hasHumidity = v, true
	s.recompute()
}

func (s *VaporConcentrationSensor) recompute() {
	if !s.in.hasTemperature || !s.in.hasHumidity {
		return
	}
	v, ok := VaporConcentration(s.in.temperature, s.in.humidity)
	feedSink(s.Sensor, v, ok, &s.last, &s.hasLast, s.sources)
}

// --- Enthalpy sensor ---

// EnthalpySensor emits the specific enthalpy of humid air.
//
// The inner [generic.Sensor] carries the canonical
// `<central>:<channelAddress>:CALCULATED/ENTHALPY` UniqueID via
// [generic.Spec.KeyNameOverride]; no outer BaseDataPointFields
// embed (V2 fix, PR-32).
//
// [StateUncertain] aggregates over registered source DPs via the
// embedded [sourceSink]. Pressure is optional and also registered
// when present.
type EnthalpySensor struct {
	*generic.Sensor[float64]
	sourceSink
	in      climateInputs
	last    float64
	hasLast bool
}

// NewEnthalpySensor constructs the sensor with no central / channel
// scoping. Multi-CCU-safe call sites MUST use
// [NewEnthalpySensorWithIdentity].
func NewEnthalpySensor() *EnthalpySensor {
	return NewEnthalpySensorWithIdentity("", "")
}

// NewEnthalpySensorWithIdentity constructs the sensor rooted at
// `<central>:<channelAddress>:CALCULATED/ENTHALPY`.
func NewEnthalpySensorWithIdentity(centralName, channelAddress string) *EnthalpySensor {
	s := &EnthalpySensor{
		Sensor: newDerivedFloatSensor(hmenum.CalculatedParameterEnthalpy, centralName, channelAddress),
	}
	installSourceValidityGate(s.Sensor, &s.sourceSink)
	return s
}

// OnTemperature feeds a temperature observation.
func (s *EnthalpySensor) OnTemperature(v float64) {
	s.in.temperature, s.in.hasTemperature = v, true
	s.recompute()
}

// OnHumidity feeds a humidity observation.
func (s *EnthalpySensor) OnHumidity(v float64) {
	s.in.humidity, s.in.hasHumidity = v, true
	s.recompute()
}

// OnPressure feeds a barometric pressure observation (hPa).
func (s *EnthalpySensor) OnPressure(v float64) {
	s.in.pressureHPa, s.in.hasPressure = v, true
	s.recompute()
}

func (s *EnthalpySensor) recompute() {
	if !s.in.hasTemperature || !s.in.hasHumidity {
		return
	}
	p := DefaultPressureHPa
	if s.in.hasPressure {
		p = s.in.pressureHPa
	}
	v, ok := Enthalpy(s.in.temperature, s.in.humidity, p)
	feedSink(s.Sensor, v, ok, &s.last, &s.hasLast, s.sources)
}

// --- ApparentTemperature sensor ---

// ApparentTemperatureSensor emits the NOAA "feels-like" temperature.
//
// The inner [generic.Sensor] carries the canonical
// `<central>:<channelAddress>:CALCULATED/APPARENT_TEMPERATURE`
// UniqueID via [generic.Spec.KeyNameOverride]; no outer
// BaseDataPointFields embed (V2 fix, PR-32).
//
// [StateUncertain] aggregates over registered source DPs via the
// embedded [sourceSink].
type ApparentTemperatureSensor struct {
	*generic.Sensor[float64]
	sourceSink
	in      climateInputs
	last    float64
	hasLast bool
}

// NewApparentTemperatureSensor constructs the sensor with no central
// channel scoping. Multi-CCU-safe call sites MUST use
// [NewApparentTemperatureSensorWithIdentity].
func NewApparentTemperatureSensor() *ApparentTemperatureSensor {
	return NewApparentTemperatureSensorWithIdentity("", "")
}

// NewApparentTemperatureSensorWithIdentity constructs the sensor rooted
// at `<central>:<channelAddress>:CALCULATED/APPARENT_TEMPERATURE`.
func NewApparentTemperatureSensorWithIdentity(centralName, channelAddress string) *ApparentTemperatureSensor {
	s := &ApparentTemperatureSensor{
		Sensor: newDerivedFloatSensor(hmenum.CalculatedParameterApparentTemperature, centralName, channelAddress),
	}
	installSourceValidityGate(s.Sensor, &s.sourceSink)
	return s
}

// OnTemperature feeds a temperature observation.
func (s *ApparentTemperatureSensor) OnTemperature(v float64) {
	s.in.temperature, s.in.hasTemperature = v, true
	s.recompute()
}

// OnHumidity feeds a humidity observation.
func (s *ApparentTemperatureSensor) OnHumidity(v float64) {
	s.in.humidity, s.in.hasHumidity = v, true
	s.recompute()
}

// OnWindSpeed feeds a wind-speed observation (km/h).
func (s *ApparentTemperatureSensor) OnWindSpeed(v float64) {
	s.in.windSpeed, s.in.hasWindSpeed = v, true
	s.recompute()
}

func (s *ApparentTemperatureSensor) recompute() {
	if !s.in.hasTemperature || !s.in.hasHumidity || !s.in.hasWindSpeed {
		return
	}
	v, ok := ApparentTemperature(s.in.temperature, s.in.humidity, s.in.windSpeed)
	feedSink(s.Sensor, v, ok, &s.last, &s.hasLast, s.sources)
}

// DerivedFloatSensorUnit returns the canonical unit
// reports for each calculated climate parameter. Mirrors the
// per-class `_unit` overrides in
func derivedFloatSensorUnit(p hmenum.CalculatedParameter) string {
	switch p { //nolint:exhaustive // only float-bearing parameters have a unit; others return ""
	case hmenum.CalculatedParameterDewPoint,
		hmenum.CalculatedParameterFrostPoint,
		hmenum.CalculatedParameterApparentTemperature:
		return "°C"
	case hmenum.CalculatedParameterDewPointSpread:
		return "K"
	case hmenum.CalculatedParameterVaporConcentration:
		return "g/m³"
	case hmenum.CalculatedParameterEnthalpy:
		return "kJ/kg"
	case hmenum.CalculatedParameterOperatingVoltageLevel:
		return "%"
	}
	return ""
}

// newDerivedFloatSensor constructs a generic.Sensor[float64] for a
// derived parameter. There is no wire channel or Writer, only a
// Parameter tag; the sensor lives purely as an observable surface.
//
// `central` + `channelAddress` scope the embedded
// [datapoint.BaseDataPointFields.UniqueID] (ADR 0002 multi-CCU
// requirement). The keyName is fixed to `CALCULATED/<param>` via
// [generic.Spec.KeyNameOverride] so the inner DataPoint produces
// the family-prefixed UniqueID directly — no outer
// BaseDataPointFields embed needed on the calculated sensor type.
//
// The descriptor's Unit carries the canonical unit so HA Discovery
// emits a matching `unit_of_measurement` and accepts the entity's
// `device_class` (without the unit, HA logs a `device_class …
// expected one of […]` warning per entity).
func newDerivedFloatSensor(p hmenum.CalculatedParameter, centralName, channelAddress string) *generic.Sensor[float64] {
	return generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddress,
			Parameter:      string(p),
		},
		CentralName:     centralName,
		KeyNameOverride: calculatedKeyName(p),
		// Stamp KindSensor so (*DataPoint).Category() returns the
		// proper DataPointCategory.SENSOR — without it the resolver-
		// internal kind defaults to Unknown and the calculated DP
		// surfaces as `category: undefined` instead of `sensor`,
		// breaking the snapshot diff and any downstream MQTT
		// classification.
		Kind: generic.KindSensor,
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			Unit:       derivedFloatSensorUnit(p),
		},
	})
}

// --- Sensor contract: CalculatedParameter / IsRefreshed ---

// CalculatedParameter returns the calculated parameter id this sensor
// emits.
func (s *DewPointSensor) CalculatedParameter() hmenum.CalculatedParameter {
	return hmenum.CalculatedParameterDewPoint
}

// IsRefreshed reports whether the sensor has emitted at least one
// computed value.
func (s *DewPointSensor) IsRefreshed() bool { return s.hasLast }

// CalculatedParameter — DewPointSpread.
func (s *DewPointSpreadSensor) CalculatedParameter() hmenum.CalculatedParameter {
	return hmenum.CalculatedParameterDewPointSpread
}

// IsRefreshed — DewPointSpread.
func (s *DewPointSpreadSensor) IsRefreshed() bool { return s.hasLast }

// CalculatedParameter — FrostPoint.
func (s *FrostPointSensor) CalculatedParameter() hmenum.CalculatedParameter {
	return hmenum.CalculatedParameterFrostPoint
}

// IsRefreshed — FrostPoint.
func (s *FrostPointSensor) IsRefreshed() bool { return s.hasLast }

// CalculatedParameter — VaporConcentration.
func (s *VaporConcentrationSensor) CalculatedParameter() hmenum.CalculatedParameter {
	return hmenum.CalculatedParameterVaporConcentration
}

// IsRefreshed — VaporConcentration.
func (s *VaporConcentrationSensor) IsRefreshed() bool { return s.hasLast }

// CalculatedParameter — Enthalpy.
func (s *EnthalpySensor) CalculatedParameter() hmenum.CalculatedParameter {
	return hmenum.CalculatedParameterEnthalpy
}

// IsRefreshed — Enthalpy.
func (s *EnthalpySensor) IsRefreshed() bool { return s.hasLast }

// CalculatedParameter — ApparentTemperature.
func (s *ApparentTemperatureSensor) CalculatedParameter() hmenum.CalculatedParameter {
	return hmenum.CalculatedParameterApparentTemperature
}

// IsRefreshed — ApparentTemperature.
func (s *ApparentTemperatureSensor) IsRefreshed() bool { return s.hasLast }
