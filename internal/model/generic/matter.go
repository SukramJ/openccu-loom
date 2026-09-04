// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: Sensor and BinarySensor participate in the
// Matter source surface (ADR 0012) via the
// [interfaces.MatterMeasurementSource] interface. The endpoint
// assembler reads the classifier on materialisation to decide whether
// the DP becomes a standalone Matter sensor endpoint or contributes a
// measurement cluster to an existing host endpoint.
//
// Three concrete instantiations are pinned so changes to the generic
// Sensor[T] surface are caught at compile time across the typical
// Float / Integer / String backings.
var (
	_ interfaces.MatterMeasurementSource      = (*Sensor[float64])(nil)
	_ interfaces.MatterMeasurementSource      = (*Sensor[int32])(nil)
	_ interfaces.MatterMeasurementSource      = (*Sensor[string])(nil)
	_ interfaces.MatterMeasurementSource      = (*BinarySensor)(nil)
	_ interfaces.MatterFloatMeasurementSource = (*Sensor[float64])(nil)
	_ interfaces.MatterBoolMeasurementSource  = (*BinarySensor)(nil)
)

// matterMeasurementForParameter is the central wire-parameter →
// Matter measurement class lookup used by both Sensor[T] and
// BinarySensor. Returns [interfaces.MatterMeasurementNone] for
// parameters that do not project onto a Matter cluster — those stay
// MQTT-only per the ADR 0012 §6 generic-DP table.
//
// The classifier is name-driven (matches
// routing), not signature-driven: a Float Sensor on
// `ACTUAL_TEMPERATURE` and an Integer Sensor on `LEVEL` follow
// different cluster paths regardless of T.
func matterMeasurementForParameter(p hmenum.Parameter) interfaces.MatterMeasurementClass {
	// Button / press events → 0x003B Switch (Generic Switch endpoint). The
	// family is [isPressParameter]'s to name — Button, Action and this
	// classifier all have to agree on it or a press data point is collected
	// for a group that then refuses it.
	if isPressParameter(p) {
		return interfaces.MatterMeasurementMomentarySwitch
	}

	switch p {
	// Temperature sensors → 0x0402 TemperatureMeasurement.
	case hmenum.ParameterActualTemperature, hmenum.ParameterTemperature:
		return interfaces.MatterMeasurementTemperature

	// Humidity sensors → 0x0405 RelativeHumidityMeasurement.
	case hmenum.ParameterActualHumidity, hmenum.ParameterHumidity:
		return interfaces.MatterMeasurementHumidity

	// Illuminance sensors → 0x0400 IlluminanceMeasurement.
	case hmenum.ParameterIllumination, hmenum.ParameterCurrentIllumination:
		return interfaces.MatterMeasurementIlluminance

	// Pressure → 0x0403 PressureMeasurement (P2).
	case hmenum.ParameterAirPressure:
		return interfaces.MatterMeasurementPressure

	// Air-quality sensors. CONCENTRATION on its own is CO2 by HM
	// convention; PM_25 / PM_10 carry their own dedicated parameters.
	case hmenum.ParameterConcentration:
		return interfaces.MatterMeasurementCO2
	case hmenum.ParameterMassConcentrationPM25_24H:
		return interfaces.MatterMeasurementPM25
	case hmenum.ParameterMassConcentrationPM10_24H:
		return interfaces.MatterMeasurementPM10

	// Power / energy on switching plug-ins → 0x0090 / 0x0091 (P2).
	// The host endpoint is the OnOff plug; the bridge attaches these
	// clusters to that endpoint rather than spawning a sensor endpoint.
	case hmenum.ParameterPower, hmenum.ParameterCurrent,
		hmenum.ParameterVoltage, hmenum.ParameterFrequency:
		return interfaces.MatterMeasurementPower
	case hmenum.ParameterEnergyCounter, hmenum.ParameterEnergyCounterFeedIn:
		return interfaces.MatterMeasurementEnergy

	default:
		return interfaces.MatterMeasurementNone
	}
}

// matterMeasurementForBinaryParameter handles the binary-sensor
// classification distinct from the analog one because several
// parameters (MOTION, contact STATE, OPEN, SABOTAGE) only make sense
// as boolean sources. Leak and moisture parameters map to the Leak
// class, which materialises as a ContactSensor endpoint rather than the
// dedicated WaterLeakDetector device type — see
// interfaces.MatterMeasurementClassDeviceType and the pinning test for
// why that divergence is deliberate. Battery alerts (LOWBAT / LOW_BAT)
// also surface here so the bridge can roll them up onto the host
// endpoint's PowerSource cluster.
func matterMeasurementForBinaryParameter(p hmenum.Parameter) interfaces.MatterMeasurementClass {
	switch p {
	case hmenum.ParameterMotion, hmenum.ParameterMotionDetectionActive:
		return interfaces.MatterMeasurementOccupancy
	case hmenum.ParameterState, hmenum.ParameterOpen,
		hmenum.ParameterSabotage,
		hmenum.ParameterSabotageAcceleration,
		hmenum.ParameterSabotageMagneticField,
		hmenum.ParameterSabotageVertical:
		return interfaces.MatterMeasurementContact
	case hmenum.ParameterMoistureDetected, hmenum.ParameterWaterLevelDetected:
		// The HmIP-SWD leak parameters. ALARMSTATE is deliberately not
		// here: it is a device-wide alarm flag, not a leak reading, and
		// on a siren the same name means actuator feedback.
		return interfaces.MatterMeasurementLeak
	case hmenum.ParameterLowBat, hmenum.ParameterLowbat:
		return interfaces.MatterMeasurementBattery
	default:
		return interfaces.MatterMeasurementNone
	}
}

// MatterMeasurementClass implements
// [interfaces.MatterMeasurementSource]. Returns
// [interfaces.MatterMeasurementNone] when the underlying parameter has
// no Matter measurement-cluster equivalent; the bridge skips such DPs
// when assembling endpoints.
func (s *Sensor[T]) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	if s == nil {
		return interfaces.MatterMeasurementNone
	}
	return matterMeasurementForParameter(s.Parameter())
}

// MatterMeasurementClass implements
// [interfaces.MatterMeasurementSource]. The binary-sensor classifier
// is consulted first; falls back to the analog table for parameters
// that share a name across both surfaces (rare).
func (b *BinarySensor) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	if b == nil {
		return interfaces.MatterMeasurementNone
	}
	if class := matterMeasurementForBinaryParameter(b.Parameter()); class != interfaces.MatterMeasurementNone {
		return class
	}
	return matterMeasurementForParameter(b.Parameter())
}

// MatterFloatValue implements
// [interfaces.MatterFloatMeasurementSource] for the float64-typed
// sensor specialisation. The bridge consumes this when it materialises
// a Temperature / Humidity / Illuminance / Pressure measurement
// cluster against a generic float sensor.
func (s *Sensor[T]) MatterFloatValue() (float64, bool) {
	if s == nil {
		return 0, false
	}
	v, ok := s.Value()
	if !ok {
		return 0, false
	}
	// T is one of the [SensorValue] kinds — float64 / int32 / int64 /
	// string. Only the float64 path is meaningful for Matter
	// measurement clusters; the others are rejected at compile time
	// via the typed assertion above (only Sensor[float64] satisfies
	// MatterFloatMeasurementSource), so the type switch only ever sees
	// float64 in production. The defensive `default: 0,false` covers
	// future SensorValue additions.
	switch f := any(v).(type) {
	case float64:
		return f, true
	case int32:
		return float64(f), true
	case int64:
		return float64(f), true
	default:
		return 0, false
	}
}

// MatterBoolValue implements
// [interfaces.MatterBoolMeasurementSource]. The bridge consumes this
// when it materialises a BooleanState / OccupancySensing cluster.
func (b *BinarySensor) MatterBoolValue() (value, observed bool) {
	if b == nil {
		return false, false
	}
	return b.IsOn()
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier]
// for the float-sensor specialisation. Wraps
// DataPoint.OnConfirmedUpdate so the bridge marks the Matter attribute
// path dirty only when the CCU echoes an actual transition — optimistic
// Apply / rollback transitions stay invisible to Apple's Subscribe.
//
// Mirrors matter.js's `events.measuredValue$Changed.on(...)` pattern
// (see ThermostatServer.ts:450 in matter.js): fires on confirmed change,
// not on every write or read. The wrapper discards the typed old/new
// payload because the Matter dispatcher re-reads the current value via
// MatterFloatValue before encoding the report; the notification's job
// is to wake the dirty bucket, not to carry the value.
func (s *Sensor[T]) OnMatterValueChanged(cb func()) func() {
	if s == nil || s.DataPoint == nil || cb == nil {
		return func() {}
	}
	return s.OnConfirmedUpdate(func(_, _ T) { cb() })
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier]
// for the binary-sensor specialisation. See [Sensor.OnMatterValueChanged]
// for the rationale; the bool counterpart wraps OnConfirmedUpdate so
// BooleanState / OccupancySensing reports stay aligned with confirmed
// CCU transitions.
func (b *BinarySensor) OnMatterValueChanged(cb func()) func() {
	if b == nil || b.DataPoint == nil || cb == nil {
		return func() {}
	}
	return b.OnConfirmedUpdate(func(_, _ bool) { cb() })
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier]
// for the switch specialisation. Wraps OnConfirmedUpdate so the OnOff
// cluster's dirty marking only fires when the CCU confirms a state
// transition — Apple Home's HMOutlet projection mirrors the genuine
// CCU value, not the optimistic guess plus its 30 s rollback. Apple
// runs its own optimistic UI between command-send and the next
// Subscribe report, so pushing optimistic state through the Matter
// pipe would only contend with that UI and surface false flips when
// the rollback timer fires after a missed CCU echo.
func (s *Switch) OnMatterValueChanged(cb func()) func() {
	if s == nil || s.DataPoint == nil || cb == nil {
		return func() {}
	}
	return s.OnConfirmedUpdate(func(_, _ bool) { cb() })
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier]
// for the writable Float specialisation (LEVEL / dimmer brightness /
// cover position / setpoint). Custom device types that embed *Float —
// Light, Cover and their variants — inherit this method, so an external
// CCU-confirmed level change dirty-marks the OnOff / LevelControl /
// WindowCovering attributes and Apple's Subscribe sees the new value.
// Without it a bridged dimmer or blind only ever reflected commands
// Apple itself sent, never a change made at the wall switch or via the
// CCU. Wraps OnConfirmedUpdate for the same optimistic-transition
// rationale as [Switch.OnMatterValueChanged].
func (f *Float) OnMatterValueChanged(cb func()) func() {
	if f == nil || f.DataPoint == nil || cb == nil {
		return func() {}
	}
	return f.OnConfirmedUpdate(func(_, _ float64) { cb() })
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier]
// for the writable Integer specialisation (HUE / COLOR index / colour
// temperature Kelvin). Custom device types that hold a *Integer colour
// axis — ColorLight's hue, the RF colour dimmers' single COLOR integer,
// ColorTempLight's / RGBWLight's kelvin — use this so an external
// CCU-confirmed colour change dirty-marks the ColorControl attributes and
// Apple's Subscribe sees the new value, not just the value it wrote
// itself. Wraps OnConfirmedUpdate for the same optimistic-transition
// rationale as [Float.OnMatterValueChanged].
func (i *Integer) OnMatterValueChanged(cb func()) func() {
	if i == nil || i.DataPoint == nil || cb == nil {
		return func() {}
	}
	return i.OnConfirmedUpdate(func(_, _ int32) { cb() })
}
