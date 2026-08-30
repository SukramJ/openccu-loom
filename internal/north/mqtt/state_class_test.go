// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import "testing"

// TestSensorStateClassComesFromTheDomain pins every parameter that a
// discovery payload publishes a state_class for to the domain's own value
// behaviour. The adapter used to carry a second, hand-maintained
// parameter-name table beside this path; it drifted (it called RAIN_COUNTER
// an instantaneous measurement while the domain calls it a monotonic
// counter) and it capped the classified set at whatever its author had
// written down. A parameter dropping out of the domain tables must fail
// here rather than silently lose its state_class in Home Assistant, where
// the consequence is a wrong long-term statistic and no error anywhere.
func TestSensorStateClassComesFromTheDomain(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"ENERGY_COUNTER":          "total_increasing",
		"GAS_ENERGY_COUNTER":      "total_increasing",
		"IEC_ENERGY_COUNTER":      "total_increasing",
		"RAIN_COUNTER":            "total_increasing",
		"ACTUAL_TEMPERATURE":      "measurement",
		"TEMPERATURE":             "measurement",
		"SET_POINT_TEMPERATURE":   "measurement",
		"SET_TEMPERATURE":         "measurement",
		"HUMIDITY":                "measurement",
		"POWER":                   "measurement",
		"GAS_POWER":               "measurement",
		"IEC_POWER":               "measurement",
		"VOLTAGE":                 "measurement",
		"OPERATING_VOLTAGE":       "measurement",
		"OPERATING_VOLTAGE_LEVEL": "measurement",
		"CURRENT":                 "measurement",
		"FREQUENCY":               "measurement",
		"AIR_PRESSURE":            "measurement",
		"BRIGHTNESS":              "measurement",
		"ILLUMINATION":            "measurement",
		"WIND_SPEED":              "measurement",
		"WIND_DIRECTION":          "measurement",
		"RSSI_DEVICE":             "measurement",
		"RSSI_PEER":               "measurement",
		"BATTERY_STATE":           "measurement",
		"LEVEL":                   "measurement",
		"LEVEL_2":                 "measurement",
		"LEVEL_SLATS":             "measurement",
	}
	for param, want := range cases {
		if got := resolveSensorStateClass("", param, ""); got != want {
			t.Errorf("%s: domain answers %q, want %q", param, got, want)
		}
	}
}

// TestSensorStateClassAbsentForNonQuantities keeps the negative half: a
// boolean or unknown parameter must yield no state_class at all, so the
// guard above cannot pass by classifying everything.
func TestSensorStateClassAbsentForNonQuantities(t *testing.T) {
	t.Parallel()
	for _, param := range []string{"STATE", "WINDOW_STATE", "UNKNOWN_PARAM"} {
		if got := resolveSensorStateClass("", param, ""); got != "" {
			t.Errorf("%s: expected no state_class, got %q", param, got)
		}
	}
}

// TestSensorStateClassAcceptsWireCasing pins case-insensitivity, which the
// removed adapter table provided explicitly and the domain lookup must keep.
func TestSensorStateClassAcceptsWireCasing(t *testing.T) {
	t.Parallel()
	if got := resolveSensorStateClass("", "actual_temperature", ""); got != "measurement" {
		t.Fatalf("lowercase wire form: got %q, want measurement", got)
	}
}
