// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import "testing"

func TestStateClassMapping(t *testing.T) {
	cases := []struct {
		param string
		want  string
	}{
		{"ACTUAL_TEMPERATURE", "measurement"},
		{"TEMPERATURE", "measurement"},
		{"HUMIDITY", "measurement"},
		{"POWER", "measurement"},
		{"VOLTAGE", "measurement"},
		{"CURRENT", "measurement"},
		{"BRIGHTNESS", "measurement"},
		{"WIND_SPEED", "measurement"},
		{"LEVEL", "measurement"},
		{"ENERGY_COUNTER", "total_increasing"},
		{"GAS_ENERGY_COUNTER", "total_increasing"},
		{"IEC_ENERGY_COUNTER", "total_increasing"},
		{"STATE", ""}, // boolean, no state_class
		{"WINDOW_STATE", ""},
		{"UNKNOWN_PARAM", ""},
	}
	for _, c := range cases {
		t.Run(c.param, func(t *testing.T) {
			if got := stateClassFor(c.param); got != c.want {
				t.Fatalf("stateClassFor(%q) = %q, want %q", c.param, got, c.want)
			}
		})
	}
}

func TestStateClassCaseInsensitive(t *testing.T) {
	if stateClassFor("actual_temperature") != "measurement" {
		t.Fatal("must accept lowercase wire form")
	}
}
