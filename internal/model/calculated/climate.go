// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package calculated

import (
	"sync"

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

// emitState holds the value a calculated float sensor last published, together
// with the lock that guards it.
//
// Every calculated sensor is driven by more than one upstream data point, and
// each of those fires its update handler on whichever goroutine delivered the
// CCU event — the callback servers dispatch one per connection and nothing
// serialises them per channel. Without a lock the compare-and-set below tears:
// a value can be recorded as published while the emission is lost, or the same
// value can be emitted twice.
type emitState struct {
	mu      sync.Mutex
	last    float64
	hasLast bool
}

// refreshed reports whether the sensor has emitted at least one computed value.
func (es *emitState) refreshed() bool {
	es.mu.Lock()
	defer es.mu.Unlock()
	return es.hasLast
}

// feed writes value into sink if the formula yielded ok AND the value is
// different from the previous emission. Dedup lives here so subscribers of
// generic.Sensor.OnUpdate don't receive no-op events.
//
// If the sensor itself has published within the last 500 ms, suppress the
// call so rapid CCU bursts don't produce intermediate values. The
// sources-level guard ([shouldPublishCalcUpdate]) is evaluated from the
// snapshot the calling sensor passes in.
//
// Only the dedup compare-and-set runs under the lock: sink.OnEvent fans out to
// arbitrary subscribers, and a sensor lock held across that fan-out would
// invite re-entrancy through a subscriber that reads the sensor back.
func (es *emitState) feed(sink *generic.Sensor[float64], value float64, ok bool, sources []SourceDP) {
	if !ok {
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
	es.mu.Lock()
	if es.hasLast && es.last == value {
		es.mu.Unlock()
		return
	}
	es.last, es.hasLast = value, true
	es.mu.Unlock()
	sink.OnEvent(value)
}

// climateState guards the composite inputs a climate-derived sensor computes
// from, plus the value it last emitted.
//
// Temperature, humidity, wind speed and pressure each arrive on their own
// upstream data point, so two of them are routinely written at the same time.
// Recomputing from the unguarded struct could mix a fresh humidity with a
// temperature that is being overwritten, publishing a number that was never
// measured.
type climateState struct {
	mu   sync.Mutex
	in   climateInputs
	emit emitState
}

// update applies mut to the guarded inputs and returns the consistent snapshot
// the caller's formula must compute from. Recomputing from the live struct
// after releasing the lock would reintroduce the torn read.
func (st *climateState) update(mut func(*climateInputs)) climateInputs {
	st.mu.Lock()
	defer st.mu.Unlock()
	mut(&st.in)
	return st.in
}

func (st *climateState) setTemperature(v float64) climateInputs {
	return st.update(func(in *climateInputs) { in.temperature, in.hasTemperature = v, true })
}

func (st *climateState) setHumidity(v float64) climateInputs {
	return st.update(func(in *climateInputs) { in.humidity, in.hasHumidity = v, true })
}

func (st *climateState) setWindSpeed(v float64) climateInputs {
	return st.update(func(in *climateInputs) { in.windSpeed, in.hasWindSpeed = v, true })
}

func (st *climateState) setPressure(v float64) climateInputs {
	return st.update(func(in *climateInputs) { in.pressureHPa, in.hasPressure = v, true })
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
	climateState
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
	s.recompute(s.setTemperature(v))
}

// OnHumidity feeds a humidity observation and recomputes.
func (s *DewPointSensor) OnHumidity(v float64) {
	s.recompute(s.setHumidity(v))
}

func (s *DewPointSensor) recompute(in climateInputs) {
	if !in.hasTemperature || !in.hasHumidity {
		return
	}
	v, ok := DewPoint(in.temperature, in.humidity)
	s.emit.feed(s.Sensor, v, ok, s.snapshotSources())
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
	climateState
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
	s.recompute(s.setTemperature(v))
}

// OnHumidity feeds a humidity observation.
func (s *DewPointSpreadSensor) OnHumidity(v float64) {
	s.recompute(s.setHumidity(v))
}

func (s *DewPointSpreadSensor) recompute(in climateInputs) {
	if !in.hasTemperature || !in.hasHumidity {
		return
	}
	v, ok := DewPointSpread(in.temperature, in.humidity)
	s.emit.feed(s.Sensor, v, ok, s.snapshotSources())
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
	climateState
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
	s.recompute(s.setTemperature(v))
}

// OnHumidity feeds a humidity observation.
func (s *FrostPointSensor) OnHumidity(v float64) {
	s.recompute(s.setHumidity(v))
}

func (s *FrostPointSensor) recompute(in climateInputs) {
	if !in.hasTemperature || !in.hasHumidity {
		return
	}
	v, ok := FrostPoint(in.temperature, in.humidity)
	s.emit.feed(s.Sensor, v, ok, s.snapshotSources())
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
	climateState
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
	s.recompute(s.setTemperature(v))
}

// OnHumidity feeds a humidity observation.
func (s *VaporConcentrationSensor) OnHumidity(v float64) {
	s.recompute(s.setHumidity(v))
}

func (s *VaporConcentrationSensor) recompute(in climateInputs) {
	if !in.hasTemperature || !in.hasHumidity {
		return
	}
	v, ok := VaporConcentration(in.temperature, in.humidity)
	s.emit.feed(s.Sensor, v, ok, s.snapshotSources())
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
	climateState
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
	s.recompute(s.setTemperature(v))
}

// OnHumidity feeds a humidity observation.
func (s *EnthalpySensor) OnHumidity(v float64) {
	s.recompute(s.setHumidity(v))
}

// OnPressure feeds a barometric pressure observation (hPa).
func (s *EnthalpySensor) OnPressure(v float64) {
	s.recompute(s.setPressure(v))
}

func (s *EnthalpySensor) recompute(in climateInputs) {
	if !in.hasTemperature || !in.hasHumidity {
		return
	}
	p := DefaultPressureHPa
	if in.hasPressure {
		p = in.pressureHPa
	}
	v, ok := Enthalpy(in.temperature, in.humidity, p)
	s.emit.feed(s.Sensor, v, ok, s.snapshotSources())
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
	climateState
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
	s.recompute(s.setTemperature(v))
}

// OnHumidity feeds a humidity observation.
func (s *ApparentTemperatureSensor) OnHumidity(v float64) {
	s.recompute(s.setHumidity(v))
}

// OnWindSpeed feeds a wind-speed observation (km/h).
func (s *ApparentTemperatureSensor) OnWindSpeed(v float64) {
	s.recompute(s.setWindSpeed(v))
}

func (s *ApparentTemperatureSensor) recompute(in climateInputs) {
	if !in.hasTemperature || !in.hasHumidity || !in.hasWindSpeed {
		return
	}
	v, ok := ApparentTemperature(in.temperature, in.humidity, in.windSpeed)
	s.emit.feed(s.Sensor, v, ok, s.snapshotSources())
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
func (s *DewPointSensor) IsRefreshed() bool { return s.emit.refreshed() }

// CalculatedParameter — DewPointSpread.
func (s *DewPointSpreadSensor) CalculatedParameter() hmenum.CalculatedParameter {
	return hmenum.CalculatedParameterDewPointSpread
}

// IsRefreshed — DewPointSpread.
func (s *DewPointSpreadSensor) IsRefreshed() bool { return s.emit.refreshed() }

// CalculatedParameter — FrostPoint.
func (s *FrostPointSensor) CalculatedParameter() hmenum.CalculatedParameter {
	return hmenum.CalculatedParameterFrostPoint
}

// IsRefreshed — FrostPoint.
func (s *FrostPointSensor) IsRefreshed() bool { return s.emit.refreshed() }

// CalculatedParameter — VaporConcentration.
func (s *VaporConcentrationSensor) CalculatedParameter() hmenum.CalculatedParameter {
	return hmenum.CalculatedParameterVaporConcentration
}

// IsRefreshed — VaporConcentration.
func (s *VaporConcentrationSensor) IsRefreshed() bool { return s.emit.refreshed() }

// CalculatedParameter — Enthalpy.
func (s *EnthalpySensor) CalculatedParameter() hmenum.CalculatedParameter {
	return hmenum.CalculatedParameterEnthalpy
}

// IsRefreshed — Enthalpy.
func (s *EnthalpySensor) IsRefreshed() bool { return s.emit.refreshed() }

// CalculatedParameter — ApparentTemperature.
func (s *ApparentTemperatureSensor) CalculatedParameter() hmenum.CalculatedParameter {
	return hmenum.CalculatedParameterApparentTemperature
}

// IsRefreshed — ApparentTemperature.
func (s *ApparentTemperatureSensor) IsRefreshed() bool { return s.emit.refreshed() }
