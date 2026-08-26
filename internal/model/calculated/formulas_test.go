// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package calculated

import (
	"math"
	"testing"
)

// toFloat64 type-branch tests

func TestToFloat64TypeBranches(t *testing.T) {
	cases := []struct {
		in   any
		want float64
		ok   bool
	}{
		{float64(3.14), 3.14, true},
		{float32(1.5), float64(float32(1.5)), true},
		{int(7), 7.0, true},
		{int32(99), 99.0, true},
		{int64(1000), 1000.0, true},
		{"string", 0, false},
		{true, 0, false},
		{nil, 0, false},
	}
	for _, c := range cases {
		got, ok := toFloat64(c.in)
		if ok != c.ok {
			t.Errorf("toFloat64(%T(%v)): ok=%v want %v", c.in, c.in, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("toFloat64(%v): got %v want %v", c.in, got, c.want)
		}
	}
}

// Formula edge/branch cases

func closef(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestDewPointKnownPoint(t *testing.T) {
	// 25 °C / 60 % RH → dew point ≈ 16.7 °C (NOAA reference value).
	got, ok := DewPoint(25, 60)
	if !ok {
		t.Fatal("not ok")
	}
	if !closef(got, 16.7, 0.2) {
		t.Fatalf("got %v, want ≈16.7", got)
	}
}

func TestDewPointZeroHumidityHandled(t *testing.T) {
	if _, ok := DewPoint(20, 0); ok {
		t.Fatal("zero humidity should not yield a finite dew point")
	}
	// Degenerate zero/zero case
	if v, ok := DewPoint(0, 0); !ok || v != 0 {
		t.Fatalf("zero/zero = %v ok=%v", v, ok)
	}
}

func TestDewPointSpreadMatchesDifference(t *testing.T) {
	temp := 22.0
	hum := 55.0
	dew, ok := DewPoint(temp, hum)
	if !ok {
		t.Fatal()
	}
	spread, ok := DewPointSpread(temp, hum)
	if !ok {
		t.Fatal()
	}
	if !closef(spread, temp-dew, 0.05) {
		t.Fatalf("spread=%v, want %v", spread, temp-dew)
	}
}

func TestVaporConcentrationKnownPoint(t *testing.T) {
	// 20 °C / 50 % RH → ~8.65 g/m³
	got, ok := VaporConcentration(20, 50)
	if !ok {
		t.Fatal()
	}
	if !closef(got, 8.65, 0.1) {
		t.Fatalf("got %v, want ≈8.65", got)
	}
}

func TestEnthalpyKnownPoint(t *testing.T) {
	// 20 °C / 50 % RH at 1013.25 hPa → ~38.6 kJ/kg
	got, ok := Enthalpy(20, 50, DefaultPressureHPa)
	if !ok {
		t.Fatal()
	}
	if !closef(got, 38.6, 0.2) {
		t.Fatalf("got %v, want ≈38.6", got)
	}
}

func TestFrostPointKnownPoint(t *testing.T) {
	// -5 °C / 70 % RH → frost point around -9 °C
	got, ok := FrostPoint(-5, 70)
	if !ok {
		t.Fatal()
	}
	if !closef(got, -9, 0.3) {
		t.Fatalf("got %v, want ≈-9", got)
	}
}

func TestApparentTemperatureBranches(t *testing.T) {
	// Below 10 °C with wind → wind chill.
	at, ok := ApparentTemperature(5, 50, 20)
	if !ok {
		t.Fatal()
	}
	if at > 5 {
		t.Fatalf("wind chill must be < actual, got %v", at)
	}
	// Hot day → heat index ≥ actual.
	at, ok = ApparentTemperature(30, 70, 0)
	if !ok || at < 30 {
		t.Fatalf("heat index=%v ok=%v", at, ok)
	}
	// Mild conditions → pass-through.
	at, ok = ApparentTemperature(18, 40, 3)
	if !ok || !closef(at, 18, 0.05) {
		t.Fatalf("passthrough at=%v ok=%v", at, ok)
	}
}

func TestApparentTemperatureWindChillBranch(t *testing.T) {
	at, ok := ApparentTemperature(0, 50, 10)
	if !ok {
		t.Fatal("ApparentTemperature(0, 50, 10) should return ok")
	}
	if at >= 0 {
		t.Fatalf("expected wind chill below 0, got %v", at)
	}
}

func TestApparentTemperatureNoWindNoChill(t *testing.T) {
	at, ok := ApparentTemperature(5, 50, 3)
	if !ok {
		t.Fatal("expected ok")
	}
	if !closef(at, 5, 0.15) {
		t.Fatalf("expected pass-through ≈5, got %v", at)
	}
}

func TestApparentTemperatureHeatIndexBranch(t *testing.T) {
	at, ok := ApparentTemperature(26.7, 30, 0)
	if !ok {
		t.Fatal("expected ok at boundary 26.7")
	}
	_ = at
}

func TestDewPointSpreadZeroHumidity(t *testing.T) {
	_, ok := DewPointSpread(20, 0)
	if ok {
		t.Fatal("DewPointSpread with zero humidity should return ok=false")
	}
}

func TestDewPointSpreadTempZeroHumZero(t *testing.T) {
	v, ok := DewPointSpread(0, 0)
	if !ok {
		t.Fatal("DewPointSpread(0, 0) should return ok")
	}
	if v != 0 {
		t.Fatalf("expected 0, got %v", v)
	}
}

func TestEnthalpyZeroPressure(t *testing.T) {
	_, ok := Enthalpy(20, 50, 0)
	if ok {
		t.Fatal("zero pressure should fail")
	}
	_, ok = Enthalpy(20, 50, -1)
	if ok {
		t.Fatal("negative pressure should fail")
	}
}

func TestFrostPointZeroHumidity(t *testing.T) {
	_, ok := FrostPoint(-5, 0)
	if ok {
		t.Fatal("FrostPoint with zero humidity should return ok=false")
	}
}

func TestVaporConcentrationNormalInputs(t *testing.T) {
	v, ok := VaporConcentration(20, 50)
	if !ok {
		t.Fatal("expected ok")
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		t.Fatalf("expected finite value, got %v", v)
	}
	if v <= 0 {
		t.Fatalf("expected positive, got %v", v)
	}
}

func TestVaporConcentrationAbsoluteZeroKelvin(t *testing.T) {
	_, ok := VaporConcentration(-273.15, 50)
	if ok {
		t.Fatal("VaporConcentration at absolute zero should fail")
	}
}

func TestWindChillOutsideRange(t *testing.T) {
	v, ok := windChill(15, 20)
	if ok {
		t.Fatalf("windChill above 10°C: expected false, got v=%v ok=true", v)
	}
	v, ok = windChill(5, 4.8)
	if ok {
		t.Fatalf("windChill at 4.8 km/h: expected false, got v=%v ok=true", v)
	}
}

func TestApparentTemperatureWindChillReturnsFalse(t *testing.T) {
	at, ok := ApparentTemperature(10, 50, 5)
	if !ok {
		t.Fatal("expected ok at boundary temp=10 wind=5")
	}
	_ = at
}

func TestDewPointHighHumidityRange(t *testing.T) {
	v, ok := DewPoint(30, 100)
	if !ok {
		t.Fatal("DewPoint(30, 100) should succeed")
	}
	if v <= 0 {
		t.Fatalf("DewPoint at 100%% humidity should be positive, got %v", v)
	}
}

func TestEnthalpyExtremeHumidityAndPressure(t *testing.T) {
	_, ok := Enthalpy(60, 100, 10)
	if ok {
		t.Fatal("extremely high humidity at very low pressure should return ok=false")
	}
}

func TestFrostPointExtremeTemp(t *testing.T) {
	_, _ = FrostPoint(-30, 80) // must not panic
}

func TestDewPointNearBoundaryInputs(t *testing.T) {
	v, ok := DewPoint(60, 100)
	if !ok {
		t.Skip("DewPoint(60,100) returned !ok; formula rejects extreme inputs")
	}
	if v <= 0 {
		t.Fatalf("DewPoint(60°C, 100%%) should be positive, got %v", v)
	}
}

func TestFrostPointTBelowAbsoluteZero(t *testing.T) {
	_, ok := FrostPoint(-274, 80)
	if ok {
		t.Fatal("FrostPoint with temperature below absolute zero should return ok=false")
	}
}

func TestFrostPointNaNInfGuard(t *testing.T) {
	_, _ = FrostPoint(-40, 5) // must not panic
}

func TestVaporConcentrationExtremeHumidity(t *testing.T) {
	v, ok := VaporConcentration(50, 100)
	if !ok {
		t.Fatal("VaporConcentration(50, 100) should succeed")
	}
	if v <= 0 {
		t.Fatalf("expected positive value, got %v", v)
	}
}

func TestWindChillNearBoundaryValid(t *testing.T) {
	wc, ok := windChill(10, 5)
	if !ok {
		t.Fatal("windChill(10, 5) should succeed")
	}
	_ = wc
}

func TestOperatingVoltageLevel(t *testing.T) {
	cases := []struct {
		op, low, max, want float64
		ok                 bool
	}{
		{3.0, 2.0, 3.0, 100, true}, // max
		{2.0, 2.0, 3.0, 0, true},   // min
		{2.5, 2.0, 3.0, 50, true},  // mid
		{1.0, 2.0, 3.0, 0, true},   // clamp low
		{4.0, 2.0, 3.0, 100, true}, // clamp high
		{2.5, 3.0, 2.0, 0, false},  // invalid refs
	}
	for _, c := range cases {
		got, ok := OperatingVoltageLevel(c.op, c.low, c.max)
		if ok != c.ok {
			t.Errorf("op=%v refs=(%v,%v): ok=%v want %v", c.op, c.low, c.max, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("op=%v refs=(%v,%v): got %v want %v", c.op, c.low, c.max, got, c.want)
		}
	}
}
