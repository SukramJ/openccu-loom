// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

import "testing"

func TestParameterStatusPair(t *testing.T) {
	cases := []struct {
		name string
		in   Parameter
		want Parameter
		ok   bool
	}{
		{"value param", ParameterLevel, "LEVEL_STATUS", true},
		{"another value param", ParameterState, "STATE_STATUS", true},
		{"already status — no double-suffix", "LEVEL_STATUS", "", false},
		{"empty", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := c.in.StatusPair()
			if got != c.want || ok != c.ok {
				t.Fatalf("StatusPair(%q) = (%q, %v) want (%q, %v)", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestParameterIsStatusPair(t *testing.T) {
	if !Parameter("LEVEL_STATUS").IsStatusPair() {
		t.Fatal("LEVEL_STATUS must be a status pair")
	}
	if ParameterLevel.IsStatusPair() {
		t.Fatal("LEVEL must not report as status pair")
	}
	if Parameter("_STATUS").IsStatusPair() {
		// "_STATUS" alone has nothing in front; not a real CCU name.
		t.Fatal("\"_STATUS\" alone must not be a status pair")
	}
	if Parameter("").IsStatusPair() {
		t.Fatal("empty must not be a status pair")
	}
}

func TestParameterBasePair(t *testing.T) {
	if got, ok := Parameter("LEVEL_STATUS").BasePair(); !ok || got != ParameterLevel {
		t.Fatalf("BasePair(LEVEL_STATUS) = (%q, %v)", got, ok)
	}
	if got, ok := ParameterLevel.BasePair(); ok || got != ParameterLevel {
		t.Fatalf("BasePair(LEVEL) = (%q, %v) want (LEVEL, false)", got, ok)
	}
}

func TestParameterStatusPairRoundTrip(t *testing.T) {
	for _, p := range []Parameter{ParameterLevel, ParameterState, ParameterColor} {
		pair, ok := p.StatusPair()
		if !ok {
			t.Fatalf("%s should produce a pair", p)
		}
		base, baseOk := pair.BasePair()
		if !baseOk || base != p {
			t.Fatalf("round-trip %s → %s → (%s, %v) failed", p, pair, base, baseOk)
		}
	}
}

// TestParameterIsOptional verifies the M14 optional-parameter set.
// used in _allows_none_value (data_point.py:1309).
func TestParameterIsOptional(t *testing.T) {
	t.Parallel()
	optional := []Parameter{
		ParameterLevel2,
		ParameterColor,
		ParameterColorTemperature,
		ParameterEffect,
		ParameterHue,
		ParameterSaturation,
		ParameterDurationUnit,
		ParameterOnTime,
		ParameterOnTimeUnit,
		ParameterRampTime,
		ParameterRampTimeUnit,
		ParameterRampTimeToOffUnit,
		ParameterPartyStartDay,
		ParameterPartyStartTime,
		ParameterPartyStopDay,
		ParameterPartyStopTime,
		ParameterPartyTemperature,
		ParameterInhibit,
		ParameterInstallTest,
	}
	for _, p := range optional {
		if !p.IsOptional() {
			t.Errorf("%s: expected IsOptional()=true", p)
		}
	}
	nonOptional := []Parameter{ParameterLevel, ParameterState, ParameterActualTemperature}
	for _, p := range nonOptional {
		if p.IsOptional() {
			t.Errorf("%s: expected IsOptional()=false", p)
		}
	}
}

func TestParameterIsClickEvent(t *testing.T) {
	clickEvents := []Parameter{
		ParameterPressShort,
		ParameterPressLong,
		ParameterPressLongStart,
		ParameterPressLongRelease,
	}
	for _, p := range clickEvents {
		if !p.IsClickEvent() {
			t.Errorf("%s.IsClickEvent() = false, want true", p)
		}
	}
	nonClick := []Parameter{
		ParameterLevel,
		ParameterState,
		ParameterUnreach,
	}
	for _, p := range nonClick {
		if p.IsClickEvent() {
			t.Errorf("%s.IsClickEvent() = true, want false", p)
		}
	}
}

func TestParameterIsDeviceLevel(t *testing.T) {
	deviceLevel := []Parameter{
		ParameterUnreach,
		ParameterStickyUnreach,
		ParameterLowBat,
		ParameterConfigPending,
	}
	for _, p := range deviceLevel {
		if !p.IsDeviceLevel() {
			t.Errorf("%s.IsDeviceLevel() = false, want true", p)
		}
	}
	notDeviceLevel := []Parameter{
		ParameterLevel,
		ParameterState,
		ParameterPressShort,
	}
	for _, p := range notDeviceLevel {
		if p.IsDeviceLevel() {
			t.Errorf("%s.IsDeviceLevel() = true, want false", p)
		}
	}
}

// TestParameterIgnoreOnInitialLoad verifies the ignore-on-initial-load logic.
func TestParameterIgnoreOnInitialLoad(t *testing.T) {
	t.Parallel()
	ignored := []Parameter{
		// Exact set
		ParameterDutyCycle,
		ParameterDutycycle,
		ParameterLowBat,
		ParameterLowbat,
		ParameterOperatingVoltage,
		// ERROR_ prefix
		"ERROR_OVERHEAT",
		"ERROR_REDUCED",
		// RSSI_ prefix
		ParameterRSSIDevice,
		ParameterRSSIPeer,
		// _ERROR suffix
		"SENSOR_ERROR",
		"HUMIDITY_ERROR",
	}
	for _, p := range ignored {
		if !p.IgnoreOnInitialLoad() {
			t.Errorf("%s: expected IgnoreOnInitialLoad()=true", p)
		}
	}
	notIgnored := []Parameter{
		ParameterLevel,
		ParameterActualTemperature,
		"NOTERROR",
	}
	for _, p := range notIgnored {
		if p.IgnoreOnInitialLoad() {
			t.Errorf("%s: expected IgnoreOnInitialLoad()=false", p)
		}
	}
}
