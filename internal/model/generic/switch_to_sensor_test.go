// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestIsForceSensorParameter pins the _SWITCH_DP_TO_SENSOR override.
// HmIP-eTRV / HmIP-HEATING with parameter LEVEL must classify as
// sensor-forced, every other (model, param) pair must not.
func TestIsForceSensorParameter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model string
		param hmenum.Parameter
		want  bool
	}{
		{"HmIP-eTRV", hmenum.ParameterLevel, true},
		{"HmIP-eTRV-2", hmenum.ParameterLevel, true},  // prefix match
		{"hmip-etrv", hmenum.ParameterLevel, true},    // case-insensitive
		{"HmIP-HEATING", hmenum.ParameterLevel, true}, // group thermostat
		{"HmIP-eTRV", hmenum.ParameterState, false},   // wrong parameter
		{"HmIP-FSM", hmenum.ParameterLevel, false},    // unrelated model
		{"", hmenum.ParameterLevel, false},            // empty model
	}
	for _, tc := range cases {
		got := generic.IsForceSensorParameter(tc.model, tc.param)
		if got != tc.want {
			t.Errorf("IsForceSensorParameter(%q, %q) = %v, want %v",
				tc.model, tc.param, got, tc.want)
		}
	}
}
