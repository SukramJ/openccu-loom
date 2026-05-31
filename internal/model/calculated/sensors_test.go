// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package calculated

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestDewPointSensorFiresOnceFullyObserved(t *testing.T) {
	s := NewDewPointSensor()
	var fired int
	s.OnUpdate(func(_, _ float64) { fired++ })

	s.OnTemperature(25)
	if v, ok := s.Value(); ok {
		t.Fatalf("value before humidity observed: %v", v)
	}
	s.OnHumidity(60)
	if _, ok := s.Value(); !ok {
		t.Fatal("value should be observed after both inputs")
	}
	if fired != 1 {
		t.Fatalf("fired=%d, want 1", fired)
	}
	// Same inputs → no second callback.
	s.OnHumidity(60)
	if fired != 1 {
		t.Fatalf("no-change fired=%d", fired)
	}
	// Changed input → callback fires again.
	s.OnHumidity(55)
	if fired != 2 {
		t.Fatalf("change fired=%d", fired)
	}
}

func TestDewPointSpreadSensorMatchesFormula(t *testing.T) {
	s := NewDewPointSpreadSensor()
	s.OnTemperature(22)
	s.OnHumidity(55)
	v, _ := s.Value()
	want, _ := DewPointSpread(22, 55)
	if v != want {
		t.Fatalf("got %v want %v", v, want)
	}
}

func TestEnthalpySensorDefaultPressure(t *testing.T) {
	s := NewEnthalpySensor()
	s.OnTemperature(20)
	s.OnHumidity(50)
	v, ok := s.Value()
	if !ok {
		t.Fatal("sensor should compute with default pressure")
	}
	ref, _ := Enthalpy(20, 50, DefaultPressureHPa)
	if v != ref {
		t.Fatalf("got %v, want %v", v, ref)
	}
}

func TestEnthalpySensorExplicitPressure(t *testing.T) {
	s := NewEnthalpySensor()
	s.OnTemperature(20)
	s.OnHumidity(50)
	s.OnPressure(950)
	v, _ := s.Value()
	ref, _ := Enthalpy(20, 50, 950)
	if v != ref {
		t.Fatalf("got %v, want %v (pressure override)", v, ref)
	}
}

func TestApparentTemperatureSensorRequiresWind(t *testing.T) {
	s := NewApparentTemperatureSensor()
	s.OnTemperature(30)
	s.OnHumidity(70)
	if _, ok := s.Value(); ok {
		t.Fatal("value should not be observed without wind")
	}
	s.OnWindSpeed(0)
	if _, ok := s.Value(); !ok {
		t.Fatal("value should be observed with wind")
	}
}

func TestOperatingVoltageLevelSensor(t *testing.T) {
	s := NewOperatingVoltageLevelSensor()
	s.OnOperatingVoltage(2.5)
	if _, ok := s.Value(); ok {
		t.Fatal("sensor should wait for references")
	}
	s.SetReferences(2.0, 3.0)
	v, ok := s.Value()
	if !ok || v != 50 {
		t.Fatalf("got %v ok=%v, want 50", v, ok)
	}
	// Invalid references are rejected and clear the ready flag.
	s.SetReferences(3.0, 2.0)
	s.OnOperatingVoltage(2.7)
	// Still 50 because the last good compute sticks; no new update.
	v, _ = s.Value()
	if v != 50 {
		t.Fatalf("invalid refs must not overwrite value: got %v", v)
	}
}

func TestDerivedBinarySensor(t *testing.T) {
	s := NewWindowOpenSensor()
	s.OnLabel("CLOSED")
	v, ok := s.Value()
	if !ok || v {
		t.Fatalf("CLOSED → %v ok=%v", v, ok)
	}
	s.OnLabel("OPEN")
	v, _ = s.Value()
	if !v {
		t.Fatal("OPEN should be true")
	}
	s.OnLabel("TILTED")
	v, _ = s.Value()
	if !v {
		t.Fatal("TILTED should be true")
	}
}

func TestDerivedBinarySensorUnknownLabelHoldsValue(t *testing.T) {
	s := NewWindowOpenSensor()
	s.OnLabel("OPEN")
	s.OnLabel("UNKNOWN_STATE")
	v, ok := s.Value()
	if !ok || !v {
		t.Fatalf("unknown label should hold previous value, got %v ok=%v", v, ok)
	}
}

// CalculatedParameter, IsRefreshed, dedup — per sensor type

func TestFrostPointSensorCalculatedParameter(t *testing.T) {
	s := NewFrostPointSensor()
	if s.CalculatedParameter() != hmenum.CalculatedParameterFrostPoint {
		t.Fatalf("wrong calculated parameter: %v", s.CalculatedParameter())
	}
}

func TestFrostPointSensorIsRefreshed(t *testing.T) {
	s := NewFrostPointSensor()
	if s.IsRefreshed() {
		t.Fatal("sensor must not be refreshed before inputs")
	}
	s.OnTemperature(-5)
	if s.IsRefreshed() {
		t.Fatal("sensor must not be refreshed with only temperature")
	}
	s.OnHumidity(70)
	if !s.IsRefreshed() {
		t.Fatal("sensor must be refreshed after both inputs")
	}
}

func TestFrostPointSensorDedup(t *testing.T) {
	s := NewFrostPointSensor()
	var fired int
	s.OnUpdate(func(_, _ float64) { fired++ })
	s.OnTemperature(-5)
	s.OnHumidity(70)
	if fired != 1 {
		t.Fatalf("expected 1 fire, got %d", fired)
	}
	s.OnHumidity(70)
	if fired != 1 {
		t.Fatalf("no-change dedup failed: fired=%d", fired)
	}
	s.OnHumidity(60)
	if fired != 2 {
		t.Fatalf("expected 2 fires after change, got %d", fired)
	}
}

func TestVaporConcentrationSensorCalculatedParameter(t *testing.T) {
	s := NewVaporConcentrationSensor()
	if s.CalculatedParameter() != hmenum.CalculatedParameterVaporConcentration {
		t.Fatalf("wrong calculated parameter: %v", s.CalculatedParameter())
	}
}

func TestVaporConcentrationSensorIsRefreshed(t *testing.T) {
	s := NewVaporConcentrationSensor()
	if s.IsRefreshed() {
		t.Fatal("not refreshed before inputs")
	}
	s.OnTemperature(20)
	if s.IsRefreshed() {
		t.Fatal("not refreshed with only temperature")
	}
	s.OnHumidity(50)
	if !s.IsRefreshed() {
		t.Fatal("must be refreshed after both inputs")
	}
}

func TestVaporConcentrationSensorDedup(t *testing.T) {
	s := NewVaporConcentrationSensor()
	var fired int
	s.OnUpdate(func(_, _ float64) { fired++ })
	s.OnTemperature(20)
	s.OnHumidity(50)
	if fired != 1 {
		t.Fatalf("expected 1, got %d", fired)
	}
	s.OnHumidity(50)
	if fired != 1 {
		t.Fatalf("dedup failed: %d", fired)
	}
	s.OnHumidity(60)
	if fired != 2 {
		t.Fatalf("expected 2, got %d", fired)
	}
}

func TestDewPointSpreadSensorCalculatedParameter(t *testing.T) {
	s := NewDewPointSpreadSensor()
	if s.CalculatedParameter() != hmenum.CalculatedParameterDewPointSpread {
		t.Fatalf("wrong calculated parameter: %v", s.CalculatedParameter())
	}
}

func TestDewPointSpreadSensorIsRefreshed(t *testing.T) {
	s := NewDewPointSpreadSensor()
	if s.IsRefreshed() {
		t.Fatal("not refreshed before inputs")
	}
	s.OnTemperature(22)
	if s.IsRefreshed() {
		t.Fatal("not refreshed with only temperature")
	}
	s.OnHumidity(55)
	if !s.IsRefreshed() {
		t.Fatal("must be refreshed after both inputs")
	}
}

func TestEnthalpySensorCalculatedParameter(t *testing.T) {
	s := NewEnthalpySensor()
	if s.CalculatedParameter() != hmenum.CalculatedParameterEnthalpy {
		t.Fatalf("wrong calculated parameter: %v", s.CalculatedParameter())
	}
}

func TestEnthalpySensorIsRefreshed(t *testing.T) {
	s := NewEnthalpySensor()
	if s.IsRefreshed() {
		t.Fatal("not refreshed before inputs")
	}
	s.OnTemperature(20)
	s.OnHumidity(50)
	if !s.IsRefreshed() {
		t.Fatal("must be refreshed after temp+humidity")
	}
}

func TestApparentTemperatureSensorCalculatedParameter(t *testing.T) {
	s := NewApparentTemperatureSensor()
	if s.CalculatedParameter() != hmenum.CalculatedParameterApparentTemperature {
		t.Fatalf("wrong calculated parameter: %v", s.CalculatedParameter())
	}
}

func TestApparentTemperatureSensorIsRefreshed(t *testing.T) {
	s := NewApparentTemperatureSensor()
	if s.IsRefreshed() {
		t.Fatal("not refreshed before inputs")
	}
	s.OnTemperature(30)
	s.OnHumidity(70)
	if s.IsRefreshed() {
		t.Fatal("not refreshed without wind")
	}
	s.OnWindSpeed(10)
	if !s.IsRefreshed() {
		t.Fatal("must be refreshed after all three inputs")
	}
}

func TestDewPointSensorCalculatedParameter(t *testing.T) {
	s := NewDewPointSensor()
	if s.CalculatedParameter() != hmenum.CalculatedParameterDewPoint {
		t.Fatalf("wrong calculated parameter: %v", s.CalculatedParameter())
	}
}

func TestDewPointSensorIsRefreshed(t *testing.T) {
	s := NewDewPointSensor()
	if s.IsRefreshed() {
		t.Fatal("not refreshed before inputs")
	}
	s.OnTemperature(25)
	s.OnHumidity(60)
	if !s.IsRefreshed() {
		t.Fatal("must be refreshed after both inputs")
	}
}

func TestFeedSinkDedup(t *testing.T) {
	s := NewDewPointSensor()
	var fired int
	s.OnUpdate(func(_, _ float64) { fired++ })
	s.OnTemperature(25)
	s.OnHumidity(60)
	if fired != 1 {
		t.Fatalf("expected 1 fire, got %d", fired)
	}
	s.OnTemperature(25)
	if fired != 1 {
		t.Fatalf("dedup: expected 1, got %d", fired)
	}
}

func TestIntrusionAlarmAndSmokeAlarmSensors(t *testing.T) {
	ia := NewIntrusionAlarmSensor()
	ia.OnLabel("INTRUSION_ALARM")
	if v, _ := ia.Value(); !v {
		t.Fatal("intrusion alarm")
	}
	sa := NewSmokeAlarmSensor()
	sa.OnLabel("IDLE_OFF")
	if v, ok := sa.Value(); !ok || v {
		t.Fatalf("idle off → %v ok=%v", v, ok)
	}
	sa.OnLabel("PRIMARY_ALARM")
	if v, _ := sa.Value(); !v {
		t.Fatal("primary alarm should be on")
	}
}
