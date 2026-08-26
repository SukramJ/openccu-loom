// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestCalculatedParameter_StringValues(t *testing.T) {
	cases := []struct {
		param hmenum.CalculatedParameter
		want  string
	}{
		{hmenum.CalculatedParameterApparentTemperature, "APPARENT_TEMPERATURE"},
		{hmenum.CalculatedParameterDewPoint, "DEW_POINT"},
		{hmenum.CalculatedParameterDewPointSpread, "DEW_POINT_SPREAD"},
		{hmenum.CalculatedParameterEnthalpy, "ENTHALPY"},
		{hmenum.CalculatedParameterFrostPoint, "FROST_POINT"},
		{hmenum.CalculatedParameterIntrusionAlarm, "INTRUSION_ALARM"},
		{hmenum.CalculatedParameterOperatingVoltageLevel, "OPERATING_VOLTAGE_LEVEL"},
		{hmenum.CalculatedParameterSmokeAlarm, "SMOKE_ALARM"},
		{hmenum.CalculatedParameterVaporConcentration, "VAPOR_CONCENTRATION"},
		{hmenum.CalculatedParameterWindowOpen, "WINDOW_OPEN"},
	}
	for _, tc := range cases {
		if got := tc.param.String(); got != tc.want {
			t.Errorf("CalculatedParameter %q: String() = %q, want %q", tc.param, got, tc.want)
		}
	}
}

func TestCalculatedParameter_Count(t *testing.T) {
	want := 10
	all := []hmenum.CalculatedParameter{
		hmenum.CalculatedParameterApparentTemperature,
		hmenum.CalculatedParameterDewPoint,
		hmenum.CalculatedParameterDewPointSpread,
		hmenum.CalculatedParameterEnthalpy,
		hmenum.CalculatedParameterFrostPoint,
		hmenum.CalculatedParameterIntrusionAlarm,
		hmenum.CalculatedParameterOperatingVoltageLevel,
		hmenum.CalculatedParameterSmokeAlarm,
		hmenum.CalculatedParameterVaporConcentration,
		hmenum.CalculatedParameterWindowOpen,
	}
	if len(all) != want {
		t.Errorf("CalculatedParameter has %d members, want %d", len(all), want)
	}
}
