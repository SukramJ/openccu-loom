// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package calculated

import "math"

// DefaultPressureHPa is the fallback air pressure used by [Enthalpy]
// when the caller has no barometric measurement.
const DefaultPressureHPa = 1013.25

// ApparentTemperature returns the "feels like" temperature in °C.
// Uses the Wind Chill model below 10 °C with wind > 4.8 km/h, the
// NOAA Heat Index model at ≥ 26.7 °C, and a pass-through in between.
// Returns (0, false) when the inputs cannot produce a finite result.
func ApparentTemperature(temperature, humidity, windSpeed float64) (float64, bool) {
	switch {
	case temperature <= 10 && windSpeed > 4.8:
		f, ok := windChill(temperature, windSpeed)
		if !ok {
			return 0, false
		}
		return round1(f), true
	case temperature >= 26.7:
		return round1(heatIndex(temperature, humidity)), true
	default:
		return round1(temperature), true
	}
}

// DewPoint returns the dew-point temperature in °C. Implementation
// mirrors the NOAA closed-form approximation.
func DewPoint(temperature, humidity float64) (float64, bool) {
	if humidity <= 0 {
		// log(0 / 0.61078) is undefined; formula has no meaningful
		// output for zero humidity.
		if temperature == 0 {
			return 0, true
		}
		return 0, false
	}
	a0 := 373.15 / (273.15 + temperature)
	s := -7.90298 * (a0 - 1)
	s += 5.02808 * math.Log10(a0)
	s += -1.3816e-7 * (math.Pow(10, 11.344*(1-1/a0)) - 1)
	s += 8.1328e-3 * (math.Pow(10, -3.49149*(a0-1)) - 1)
	s += math.Log10(1013.246)
	vp := math.Pow(10, s-3) * humidity
	if vp <= 0 || 0.61078 <= 0 {
		return 0, false
	}
	td := math.Log(vp / 0.61078)
	divisor := 17.558 - td
	if divisor == 0 {
		return 0, false
	}
	res := (241.88 * td) / divisor
	if math.IsNaN(res) || math.IsInf(res, 0) {
		return 0, false
	}
	return round1(res), true
}

// DewPointSpread returns the difference between the air temperature
// and the dew point (i.e. the safety margin against condensation).
func DewPointSpread(temperature, humidity float64) (float64, bool) {
	dew, ok := DewPoint(temperature, humidity)
	if !ok {
		return 0, false
	}
	return round2(temperature - dew), true
}

// Enthalpy returns the specific enthalpy of humid air in kJ/kg
// (relative to dry air) at the given pressure. Pass
// [DefaultPressureHPa] when no measurement is available.
func Enthalpy(temperature, humidity, pressureHPa float64) (float64, bool) {
	if pressureHPa <= 0 {
		return 0, false
	}
	eS := 6.112 * math.Exp((17.62*temperature)/(243.12+temperature))
	e := humidity / 100 * eS
	if pressureHPa-e <= 0 {
		return 0, false
	}
	r := 622 * e / (pressureHPa - e)
	h := 1.006*temperature + r*(2501+1.86*temperature)/1000
	if math.IsNaN(h) || math.IsInf(h, 0) {
		return 0, false
	}
	return round2(h), true
}

// FrostPoint returns the frost-point temperature in °C.
func FrostPoint(temperature, humidity float64) (float64, bool) {
	dew, ok := DewPoint(temperature, humidity)
	if !ok {
		return 0, false
	}
	t := temperature + 273.15
	td := dew + 273.15
	if t <= 0 || 2954.61/t+2.193665*math.Log(t)-13.3448 == 0 {
		return 0, false
	}
	denom := 2954.61/t + 2.193665*math.Log(t) - 13.3448
	if denom == 0 {
		return 0, false
	}
	res := (td + 2671.02/denom - t) - 273.15
	if math.IsNaN(res) || math.IsInf(res, 0) {
		return 0, false
	}
	return round1(res), true
}

// VaporConcentration returns the absolute water-vapor concentration in
// g/m³.
func VaporConcentration(temperature, humidity float64) (float64, bool) {
	absT := temperature + 273.15
	if absT == 0 {
		return 0, false
	}
	v := 6.112 *
		math.Exp((17.67*temperature)/(243.5+temperature)) *
		humidity * 2.1674 / absT
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return round2(v), true
}

// OperatingVoltageLevel returns the battery-level percentage (0-100)
// derived from the current operating voltage and the two reference
// points lowBatLimit and voltageMax.
//
// The rounding and clamping order mirrors the Python reference:
// round first, then clamp — so that a value like 100.05 rounds to
// 100.1 before the upper clamp brings it back to 100.0 rather than
// being clamped to 100.0 first and rounding to 100.0 regardless.
func OperatingVoltageLevel(operatingVoltage, lowBatLimit, voltageMax float64) (float64, bool) {
	if voltageMax <= lowBatLimit {
		return 0, false
	}
	pct := (operatingVoltage - lowBatLimit) / (voltageMax - lowBatLimit) * 100
	// Round first (mirrors Python: round(..., 1) inside max/min).
	pct = round1(pct)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct, true
}

// ---- private helpers ----

// heatIndex implements the NOAA closed-form heat-index in °C. The
// low-humidity / low-temperature branch uses the simple Rothfusz
// approximation; the full polynomial kicks in above ~27 °C and
// ~40 % RH.
func heatIndex(temperature, humidity float64) float64 {
	tempF := temperature*9/5 + 32
	hiF := 0.5 * (tempF + 61.0 + (tempF-68)*1.2 + humidity*0.094)
	if (hiF+tempF)/2 >= 80 {
		const (
			c1 = -8.78469475556
			c2 = 1.61139411
			c3 = 2.33854883889
			c4 = -0.14611605
			c5 = -0.012308094
			c6 = -0.0164248277778
			c7 = 0.002211732
			c8 = 0.00072546
			c9 = -0.000003582
		)
		return c1 +
			c2*temperature +
			c3*humidity +
			c4*temperature*humidity +
			c5*temperature*temperature +
			c6*humidity*humidity +
			c7*temperature*temperature*humidity +
			c8*temperature*humidity*humidity +
			c9*temperature*temperature*humidity*humidity
	}
	return (hiF - 32) * 5 / 9
}

// windChill returns the NOAA wind-chill temperature in °C, or ok=false
// when outside the formula's applicable range (≤ 10 °C and wind-speed
// > 4.8 km/h).
func windChill(temperature, windSpeed float64) (float64, bool) {
	if temperature > 10 || windSpeed <= 4.8 {
		return 0, false
	}
	wc := 13.12 +
		0.6215*temperature -
		11.37*math.Pow(windSpeed, 0.16) +
		0.3965*temperature*math.Pow(windSpeed, 0.16)
	if math.IsNaN(wc) || math.IsInf(wc, 0) {
		return 0, false
	}
	return wc, true
}

// round1 rounds v to one decimal place using round-half-to-even (banker's
// rounding), matching Python's built-in round(v, 1) behaviour.
func round1(v float64) float64 { return roundHalfEven(v, 10) }

// round2 rounds v to two decimal places using round-half-to-even (banker's
// rounding), matching Python's built-in round(v, 2) behaviour.
func round2(v float64) float64 { return roundHalfEven(v, 100) }

// roundHalfEven rounds v to 1/scale resolution using the round-half-to-even
// (banker's) rule. scale must be a positive power of ten (10 for 1 dp, 100
// for 2 dp, …). Half-way cases (e.g. 0.5 * scale) are rounded to the nearest
// even integer multiple of 1/scale. All other cases round to the nearest
// representable value, identical to math.Round.
//
// Python's built-in round() uses the same IEEE 754 round-half-to-even rule,
// so this function produces matching output for the sensor formulas in this
// package.
func roundHalfEven(v, scale float64) float64 {
	scaled := v * scale
	floor := math.Floor(scaled)
	diff := scaled - floor
	const half = 0.5
	switch {
	case diff < half:
		return floor / scale
	case diff > half:
		return (floor + 1) / scale
	default:
		// Exactly half: round to even.
		if math.Mod(floor, 2) == 0 {
			return floor / scale
		}
		return (floor + 1) / scale
	}
}
