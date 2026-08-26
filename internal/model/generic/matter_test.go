// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: both concrete sensor types must satisfy
// [interfaces.MatterChangeNotifier]. Placed in the _test.go so they
// never inflate the production binary.
var (
	_ interfaces.MatterChangeNotifier = (*Sensor[float64])(nil)
	_ interfaces.MatterChangeNotifier = (*BinarySensor)(nil)
)

// TestSensorMeasurementClassTemperatureFamily covers every parameter
// in the temperature-sensor → 0x0402 lane.
func TestSensorMeasurementClassTemperatureFamily(t *testing.T) {
	for _, p := range []hmenum.Parameter{hmenum.ParameterActualTemperature, hmenum.ParameterTemperature} {
		s := NewFloatSensor(baseCfg(p, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
		if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementTemperature {
			t.Errorf("%s: got %v, want MatterMeasurementTemperature", p, got)
		}
	}
}

// TestSensorMeasurementClassHumidityFamily covers HUMIDITY +
// ACTUAL_HUMIDITY → 0x0405.
func TestSensorMeasurementClassHumidityFamily(t *testing.T) {
	for _, p := range []hmenum.Parameter{hmenum.ParameterActualHumidity, hmenum.ParameterHumidity} {
		s := NewFloatSensor(baseCfg(p, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
		if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementHumidity {
			t.Errorf("%s: got %v, want MatterMeasurementHumidity", p, got)
		}
	}
}

// TestSensorMeasurementClassIlluminanceFamily covers ILLUMINATION +
// CURRENT_ILLUMINATION → 0x0400.
func TestSensorMeasurementClassIlluminanceFamily(t *testing.T) {
	for _, p := range []hmenum.Parameter{hmenum.ParameterIllumination, hmenum.ParameterCurrentIllumination} {
		s := NewFloatSensor(baseCfg(p, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
		if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementIlluminance {
			t.Errorf("%s: got %v, want MatterMeasurementIlluminance", p, got)
		}
	}
}

// TestSensorMeasurementClassPressure → 0x0403 (P2).
func TestSensorMeasurementClassPressure(t *testing.T) {
	s := NewFloatSensor(baseCfg(hmenum.ParameterAirPressure, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
	if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementPressure {
		t.Fatalf("AIR_PRESSURE: got %v, want MatterMeasurementPressure", got)
	}
}

// TestSensorMeasurementClassConcentrationCO2 → 0x040D.
func TestSensorMeasurementClassConcentrationCO2(t *testing.T) {
	s := NewFloatSensor(baseCfg(hmenum.ParameterConcentration, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
	if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementCO2 {
		t.Fatalf("CONCENTRATION: got %v, want MatterMeasurementCO2", got)
	}
}

// TestSensorMeasurementClassPM25And10 covers PM2.5 + PM10 24h-average
// parameters.
func TestSensorMeasurementClassPM25And10(t *testing.T) {
	s25 := NewFloatSensor(baseCfg(hmenum.ParameterMassConcentrationPM25_24H, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
	if got := s25.MatterMeasurementClass(); got != interfaces.MatterMeasurementPM25 {
		t.Errorf("PM_2_5: got %v, want MatterMeasurementPM25", got)
	}
	s10 := NewFloatSensor(baseCfg(hmenum.ParameterMassConcentrationPM10_24H, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
	if got := s10.MatterMeasurementClass(); got != interfaces.MatterMeasurementPM10 {
		t.Errorf("PM_10: got %v, want MatterMeasurementPM10", got)
	}
}

// TestSensorMeasurementClassPowerFamily routes POWER/CURRENT/VOLTAGE/
// FREQUENCY through the ElectricalPower lane (host-endpoint cluster).
func TestSensorMeasurementClassPowerFamily(t *testing.T) {
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterPower, hmenum.ParameterCurrent,
		hmenum.ParameterVoltage, hmenum.ParameterFrequency,
	} {
		s := NewFloatSensor(baseCfg(p, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
		if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementPower {
			t.Errorf("%s: got %v, want MatterMeasurementPower", p, got)
		}
	}
}

// TestSensorMeasurementClassEnergyFamily routes ENERGY_COUNTER +
// ENERGY_COUNTER_FEED_IN through ElectricalEnergy.
func TestSensorMeasurementClassEnergyFamily(t *testing.T) {
	for _, p := range []hmenum.Parameter{hmenum.ParameterEnergyCounter, hmenum.ParameterEnergyCounterFeedIn} {
		s := NewFloatSensor(baseCfg(p, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
		if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementEnergy {
			t.Errorf("%s: got %v, want MatterMeasurementEnergy", p, got)
		}
	}
}

// TestSensorMeasurementClassButtonFamily covers PRESS / PRESS_SHORT /
// PRESS_LONG → GenericSwitch (0x003B).
func TestSensorMeasurementClassButtonFamily(t *testing.T) {
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterPress, hmenum.ParameterPressShort, hmenum.ParameterPressLong,
	} {
		s := NewFloatSensor(baseCfg(p, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
		if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementMomentarySwitch {
			t.Errorf("%s: got %v, want MatterMeasurementMomentarySwitch", p, got)
		}
	}
}

// TestSensorMeasurementClassUnmappedReturnsNone confirms that
// parameters without a Matter cluster — wind, opaque, dummies — map
// to None (the bridge will then skip them on the Matter surface).
func TestSensorMeasurementClassUnmappedReturnsNone(t *testing.T) {
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterWindSpeed, hmenum.ParameterWindDirection,
		hmenum.ParameterSunshineDuration, hmenum.ParameterRSSIDevice,
	} {
		s := NewFloatSensor(baseCfg(p, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
		if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementNone {
			t.Errorf("%s: got %v, want MatterMeasurementNone", p, got)
		}
	}
}

// TestStringSensorMeasurementClassReturnsNone — opaque strings have no
// Matter cluster.
func TestStringSensorMeasurementClassReturnsNone(t *testing.T) {
	s := NewStringSensor(baseCfg(hmenum.ParameterError, hmenum.ParameterTypeString, hmenum.OperationsRead|hmenum.OperationsEvent))
	if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementNone {
		t.Fatalf("string sensor: got %v, want MatterMeasurementNone", got)
	}
}

// --- BinarySensor ---

// TestBinarySensorMeasurementMotionMapsToOccupancy routes MOTION /
// MOTION_DETECTION_ACTIVE through the OccupancySensing lane (0x0406).
func TestBinarySensorMeasurementMotionMapsToOccupancy(t *testing.T) {
	for _, p := range []hmenum.Parameter{hmenum.ParameterMotion, hmenum.ParameterMotionDetectionActive} {
		b := NewBinarySensor(baseCfg(p, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
		if got := b.MatterMeasurementClass(); got != interfaces.MatterMeasurementOccupancy {
			t.Errorf("%s: got %v, want MatterMeasurementOccupancy", p, got)
		}
	}
}

// TestBinarySensorMeasurementContactFamily covers door / window /
// sabotage parameters that share the BooleanState (0x0045) lane.
func TestBinarySensorMeasurementContactFamily(t *testing.T) {
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterState, hmenum.ParameterOpen,
		hmenum.ParameterSabotage, hmenum.ParameterSabotageMagneticField,
	} {
		b := NewBinarySensor(baseCfg(p, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
		if got := b.MatterMeasurementClass(); got != interfaces.MatterMeasurementContact {
			t.Errorf("%s: got %v, want MatterMeasurementContact", p, got)
		}
	}
}

// TestBinarySensorMeasurementBatteryAlertFamily covers LOWBAT +
// LOW_BAT — both spellings exist in the wire surface.
func TestBinarySensorMeasurementBatteryAlertFamily(t *testing.T) {
	for _, p := range []hmenum.Parameter{hmenum.ParameterLowBat, hmenum.ParameterLowbat} {
		b := NewBinarySensor(baseCfg(p, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
		if got := b.MatterMeasurementClass(); got != interfaces.MatterMeasurementBattery {
			t.Errorf("%s: got %v, want MatterMeasurementBattery", p, got)
		}
	}
}

// TestBinarySensorMeasurementUnreachReturnsNone — UNREACH is surfaced
// via BridgedDeviceBasicInformation.Reachable, not as a measurement
// cluster.
func TestBinarySensorMeasurementUnreachReturnsNone(t *testing.T) {
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterUnreach, hmenum.ParameterStickyUnreach,
		hmenum.ParameterConfigPending, hmenum.ParameterUpdatePending,
	} {
		b := NewBinarySensor(baseCfg(p, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
		if got := b.MatterMeasurementClass(); got != interfaces.MatterMeasurementNone {
			t.Errorf("%s: got %v, want MatterMeasurementNone", p, got)
		}
	}
}

// TestSensorNilSafeMatterMeasurementClass — defensive null check on
// the typed-nil receiver.
func TestSensorNilSafeMatterMeasurementClass(t *testing.T) {
	var s *Sensor[float64]
	if got := s.MatterMeasurementClass(); got != interfaces.MatterMeasurementNone {
		t.Fatalf("nil sensor: got %v, want MatterMeasurementNone", got)
	}
	var b *BinarySensor
	if got := b.MatterMeasurementClass(); got != interfaces.MatterMeasurementNone {
		t.Fatalf("nil binary sensor: got %v, want MatterMeasurementNone", got)
	}
}

// --- OnMatterValueChanged (MatterChangeNotifier) ---

// TestSensor_OnMatterValueChanged_FiresOnValueUpdate verifies that a
// registered callback is invoked whenever a fresh value arrives via
// OnEvent.
func TestSensor_OnMatterValueChanged_FiresOnValueUpdate(t *testing.T) {
	s := NewFloatSensor(baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
	var count int
	_ = s.OnMatterValueChanged(func() { count++ })
	s.OnEvent(21.5)
	s.OnEvent(22.0)
	if count != 2 {
		t.Fatalf("expected 2 callback invocations, got %d", count)
	}
}

// TestSensor_OnMatterValueChanged_UnsubscribeStopsCallback verifies
// that calling the returned closure detaches the callback so subsequent
// value pushes do not fire it.
func TestSensor_OnMatterValueChanged_UnsubscribeStopsCallback(t *testing.T) {
	s := NewFloatSensor(baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
	var count int
	unsub := s.OnMatterValueChanged(func() { count++ })
	s.OnEvent(21.5)
	unsub()
	s.OnEvent(22.0)
	if count != 1 {
		t.Fatalf("expected 1 callback invocation after unsub, got %d", count)
	}
}

// TestSensor_OnMatterValueChanged_NilSensorSafe verifies that calling
// OnMatterValueChanged on a nil *Sensor does not panic and returns a
// non-nil, safe-to-call unsubscribe closure.
func TestSensor_OnMatterValueChanged_NilSensorSafe(t *testing.T) {
	var s *Sensor[float64]
	var unsub func()
	if panicked := func() (p bool) {
		defer func() {
			if r := recover(); r != nil {
				p = true
			}
		}()
		unsub = s.OnMatterValueChanged(func() {})
		return false
	}(); panicked {
		t.Fatal("nil sensor: OnMatterValueChanged must not panic")
	}
	if unsub == nil {
		t.Fatal("nil sensor: OnMatterValueChanged must return non-nil unsub")
	}
	// Calling unsub on nil-sensor result must not panic.
	if panicked := func() (p bool) {
		defer func() {
			if r := recover(); r != nil {
				p = true
			}
		}()
		unsub()
		return false
	}(); panicked {
		t.Fatal("nil sensor: unsub closure must not panic")
	}
}

// TestSensor_OnMatterValueChanged_NilCallbackSafe verifies that a nil
// callback is accepted without panic and returns a callable unsub.
func TestSensor_OnMatterValueChanged_NilCallbackSafe(t *testing.T) {
	s := NewFloatSensor(baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))
	var unsub func()
	if panicked := func() (p bool) {
		defer func() {
			if r := recover(); r != nil {
				p = true
			}
		}()
		unsub = s.OnMatterValueChanged(nil)
		return false
	}(); panicked {
		t.Fatal("nil callback: OnMatterValueChanged must not panic")
	}
	if unsub == nil {
		t.Fatal("nil callback: OnMatterValueChanged must return non-nil unsub")
	}
	// OnEvent must not panic when callback is nil (no subscriber stored).
	s.OnEvent(5.0)
}

// --- BinarySensor OnMatterValueChanged ---

// TestBinarySensor_OnMatterValueChanged_FiresOnValueUpdate verifies
// that callbacks fire for both true and false events.
func TestBinarySensor_OnMatterValueChanged_FiresOnValueUpdate(t *testing.T) {
	b := NewBinarySensor(baseCfg(hmenum.ParameterMotion, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
	var count int
	_ = b.OnMatterValueChanged(func() { count++ })
	b.OnEvent(true)
	b.OnEvent(false)
	if count != 2 {
		t.Fatalf("expected 2 callback invocations, got %d", count)
	}
}

// TestBinarySensor_OnMatterValueChanged_UnsubscribeStopsCallback
// verifies the unsubscribe contract for BinarySensor.
func TestBinarySensor_OnMatterValueChanged_UnsubscribeStopsCallback(t *testing.T) {
	b := NewBinarySensor(baseCfg(hmenum.ParameterMotion, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
	var count int
	unsub := b.OnMatterValueChanged(func() { count++ })
	b.OnEvent(true)
	unsub()
	b.OnEvent(false)
	if count != 1 {
		t.Fatalf("expected 1 invocation after unsub, got %d", count)
	}
}

// TestBinarySensor_OnMatterValueChanged_NilSensorSafe checks nil
// receiver safety for BinarySensor.OnMatterValueChanged.
func TestBinarySensor_OnMatterValueChanged_NilSensorSafe(t *testing.T) {
	var b *BinarySensor
	var unsub func()
	if panicked := func() (p bool) {
		defer func() {
			if r := recover(); r != nil {
				p = true
			}
		}()
		unsub = b.OnMatterValueChanged(func() {})
		return false
	}(); panicked {
		t.Fatal("nil BinarySensor: OnMatterValueChanged must not panic")
	}
	if unsub == nil {
		t.Fatal("nil BinarySensor: OnMatterValueChanged must return non-nil unsub")
	}
	unsub() // must not panic
}

// TestBinarySensor_OnMatterValueChanged_NilCallbackSafe checks that a
// nil callback is safe for BinarySensor.
func TestBinarySensor_OnMatterValueChanged_NilCallbackSafe(t *testing.T) {
	b := NewBinarySensor(baseCfg(hmenum.ParameterMotion, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
	var unsub func()
	if panicked := func() (p bool) {
		defer func() {
			if r := recover(); r != nil {
				p = true
			}
		}()
		unsub = b.OnMatterValueChanged(nil)
		return false
	}(); panicked {
		t.Fatal("nil callback: BinarySensor.OnMatterValueChanged must not panic")
	}
	if unsub == nil {
		t.Fatal("nil callback: BinarySensor.OnMatterValueChanged must return non-nil unsub")
	}
	b.OnEvent(true) // must not panic with no subscriber
}

// --- Confirmed-only semantics (N01 regression tripwire) ---
//
// These tests lock the invariant that OnMatterValueChanged subscribes
// on the confirmed-update slot, not on the regular OnUpdate slot —
// the Matter Subscribe pipeline only reports CCU-confirmed transitions
// so optimistic Apply / rollback / idempotent echoes stay invisible to
// Apple Home.

// TestSwitch_OnMatterValueChanged_IgnoresOptimisticWrite verifies the
// optimistic-Apply path (WriteUnconfirmedValue) fires the regular
// OnUpdate slot but NOT OnMatterValueChanged. Without this gating the
// Matter Subscribe engine would push the optimistic guess to Apple and
// then push the rollback 30 s later, producing a visible false flip.
func TestSwitch_OnMatterValueChanged_IgnoresOptimisticWrite(t *testing.T) {
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent|hmenum.OperationsWrite))

	var matterCount, updateCount int
	_ = s.OnMatterValueChanged(func() { matterCount++ })
	_ = s.OnUpdate(func(_, _ bool) { updateCount++ })

	// Optimistic Apply path — fires updateCallbacks only.
	s.WriteUnconfirmedValue(true, time.Time{})

	if updateCount != 1 {
		t.Fatalf("expected 1 OnUpdate invocation for optimistic write, got %d", updateCount)
	}
	if matterCount != 0 {
		t.Fatalf("OnMatterValueChanged must NOT fire on optimistic write, got %d", matterCount)
	}
}

// TestSwitch_OnMatterValueChanged_FiresOnConfirmedEvent verifies that
// the CCU-confirmed OnEvent path fires both the regular OnUpdate and
// the OnMatterValueChanged callback.
func TestSwitch_OnMatterValueChanged_FiresOnConfirmedEvent(t *testing.T) {
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent|hmenum.OperationsWrite))

	var matterCount, updateCount int
	_ = s.OnMatterValueChanged(func() { matterCount++ })
	_ = s.OnUpdate(func(_, _ bool) { updateCount++ })

	s.OnEvent(true)

	if updateCount != 1 {
		t.Fatalf("expected 1 OnUpdate invocation, got %d", updateCount)
	}
	if matterCount != 1 {
		t.Fatalf("expected 1 OnMatterValueChanged invocation on confirmed event, got %d", matterCount)
	}
}

// TestSwitch_OnMatterValueChanged_SkipsIdempotentEcho verifies that a
// CCU echo confirming the same value as the previous confirmed observation
// fires OnUpdate (the broad observer surface still cares) but NOT
// OnMatterValueChanged — matches matter.js's `measuredValue$Changed`
// semantics: report on transition, not on every read.
func TestSwitch_OnMatterValueChanged_SkipsIdempotentEcho(t *testing.T) {
	s := NewSwitch(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent|hmenum.OperationsWrite))

	var matterCount, updateCount int
	_ = s.OnMatterValueChanged(func() { matterCount++ })
	_ = s.OnUpdate(func(_, _ bool) { updateCount++ })

	s.OnEvent(true) // first observation — fires
	s.OnEvent(true) // idempotent echo — must NOT fire matter callback

	if updateCount != 2 {
		t.Fatalf("expected 2 OnUpdate invocations, got %d", updateCount)
	}
	if matterCount != 1 {
		t.Fatalf("idempotent echo must not re-fire OnMatterValueChanged: want 1, got %d", matterCount)
	}
}

// TestBinarySensor_OnMatterValueChanged_IgnoresOptimisticWrite mirrors
// the Switch test for the BinarySensor surface — optimistic
// WriteUnconfirmedValue must not bubble to the Matter dirty-marker.
func TestBinarySensor_OnMatterValueChanged_IgnoresOptimisticWrite(t *testing.T) {
	b := NewBinarySensor(baseCfg(hmenum.ParameterMotion, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))

	var matterCount int
	_ = b.OnMatterValueChanged(func() { matterCount++ })

	b.WriteUnconfirmedValue(true, time.Time{})

	if matterCount != 0 {
		t.Fatalf("BinarySensor.OnMatterValueChanged must NOT fire on optimistic write, got %d", matterCount)
	}
}

// TestSensor_OnMatterValueChanged_SkipsIdempotentEcho locks the
// idempotent-echo rule for the float-sensor surface.
func TestSensor_OnMatterValueChanged_SkipsIdempotentEcho(t *testing.T) {
	s := NewFloatSensor(baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))

	var matterCount int
	_ = s.OnMatterValueChanged(func() { matterCount++ })

	s.OnEvent(21.5)
	s.OnEvent(21.5) // identical confirmed value — no Matter dirty.

	if matterCount != 1 {
		t.Fatalf("idempotent confirmed echo must not re-fire: want 1, got %d", matterCount)
	}
}

// --- Float OnMatterValueChanged ---
//
// Float backs LEVEL / dimmer brightness / cover position / setpoint on
// every custom type that embeds it (Cover, Blind, Light, Climate's
// setpoint field). These tests lock the same confirmed-only contract
// the Sensor/BinarySensor/Switch specializations already carry above.

// TestFloat_OnMatterValueChanged_FiresOnConfirmedChange verifies that a
// registered callback is invoked whenever a fresh confirmed value
// arrives via OnEvent.
func TestFloat_OnMatterValueChanged_FiresOnConfirmedChange(t *testing.T) {
	f := NewFloat(baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	var count int
	_ = f.OnMatterValueChanged(func() { count++ })
	f.OnEvent(0.5)
	f.OnEvent(0.75)
	if count != 2 {
		t.Fatalf("expected 2 callback invocations, got %d", count)
	}
}

// TestFloat_OnMatterValueChanged_UnsubscribeStopsCallback verifies that
// calling the returned closure detaches the callback so subsequent
// value pushes do not fire it.
func TestFloat_OnMatterValueChanged_UnsubscribeStopsCallback(t *testing.T) {
	f := NewFloat(baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	var count int
	unsub := f.OnMatterValueChanged(func() { count++ })
	f.OnEvent(0.5)
	unsub()
	f.OnEvent(0.75)
	if count != 1 {
		t.Fatalf("expected 1 callback invocation after unsub, got %d", count)
	}
}

// TestFloat_OnMatterValueChanged_NilFloatSafe verifies that calling
// OnMatterValueChanged on a nil *Float does not panic and returns a
// non-nil, safe-to-call unsubscribe closure.
func TestFloat_OnMatterValueChanged_NilFloatSafe(t *testing.T) {
	var f *Float
	unsub := f.OnMatterValueChanged(func() {})
	if unsub == nil {
		t.Fatal("nil Float: OnMatterValueChanged must return non-nil unsub")
	}
	unsub() // must not panic
}

// TestFloat_OnMatterValueChanged_NilCallbackSafe verifies that a nil
// callback is accepted without panic and returns a callable unsub.
func TestFloat_OnMatterValueChanged_NilCallbackSafe(t *testing.T) {
	f := NewFloat(baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	unsub := f.OnMatterValueChanged(nil)
	if unsub == nil {
		t.Fatal("nil callback: OnMatterValueChanged must return non-nil unsub")
	}
	// OnEvent must not panic when callback is nil (no subscriber stored).
	f.OnEvent(0.5)
}

// ---------------------------------------------------------------------------
// Integer.OnMatterValueChanged
//
// Integer backs HUE / the RF colour dimmers' single COLOR integer /
// colour-temperature Kelvin on every custom light type that holds one.
// These tests lock the same confirmed-only contract the Float
// specialisation carries above — without it, a colour changed outside
// Matter never dirty-marks the ColorControl cluster.

// TestInteger_OnMatterValueChanged_FiresOnConfirmedChange verifies that a
// registered callback is invoked whenever a fresh confirmed value arrives
// via OnEvent.
func TestInteger_OnMatterValueChanged_FiresOnConfirmedChange(t *testing.T) {
	i := NewInteger(baseCfg(hmenum.ParameterHue, hmenum.ParameterTypeInteger, hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	var count int
	_ = i.OnMatterValueChanged(func() { count++ })
	i.OnEvent(30)
	i.OnEvent(60)
	if count != 2 {
		t.Fatalf("expected 2 callback invocations, got %d", count)
	}
}

// TestInteger_OnMatterValueChanged_UnsubscribeStopsCallback verifies that
// calling the returned closure detaches the callback so subsequent value
// pushes do not fire it.
func TestInteger_OnMatterValueChanged_UnsubscribeStopsCallback(t *testing.T) {
	i := NewInteger(baseCfg(hmenum.ParameterHue, hmenum.ParameterTypeInteger, hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	var count int
	unsub := i.OnMatterValueChanged(func() { count++ })
	i.OnEvent(30)
	unsub()
	i.OnEvent(60)
	if count != 1 {
		t.Fatalf("expected 1 callback invocation after unsub, got %d", count)
	}
}

// TestInteger_OnMatterValueChanged_NilIntegerSafe verifies that calling
// OnMatterValueChanged on a nil *Integer does not panic and returns a
// non-nil, safe-to-call unsubscribe closure.
func TestInteger_OnMatterValueChanged_NilIntegerSafe(t *testing.T) {
	var i *Integer
	unsub := i.OnMatterValueChanged(func() {})
	if unsub == nil {
		t.Fatal("nil Integer: OnMatterValueChanged must return non-nil unsub")
	}
	unsub() // must not panic
}

// TestInteger_OnMatterValueChanged_NilCallbackSafe verifies that a nil
// callback is accepted without panic and returns a callable unsub.
func TestInteger_OnMatterValueChanged_NilCallbackSafe(t *testing.T) {
	i := NewInteger(baseCfg(hmenum.ParameterHue, hmenum.ParameterTypeInteger, hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	unsub := i.OnMatterValueChanged(nil)
	if unsub == nil {
		t.Fatal("nil callback: OnMatterValueChanged must return non-nil unsub")
	}
	// OnEvent must not panic when callback is nil (no subscriber stored).
	i.OnEvent(30)
}
