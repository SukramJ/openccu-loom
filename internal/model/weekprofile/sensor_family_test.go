// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package weekprofile

import "testing"

// TestSensorFamilyOf pins when a climate schedule is published under a family
// of its own: only next to a second schedule on the same device, the rule of
// the reference stack (notes/parity/by_design.md, "Week schedules"). A
// thermostat with its one schedule keeps the family its entity has always had,
// and a switch schedule never moves.
func TestSensorFamilyOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		scheduleType ScheduleType
		profiles     int
		want         string
	}{
		{"climate schedule alone", ScheduleTypeClimate, 1, SensorFamily},
		{"climate schedule next to a switch schedule", ScheduleTypeClimate, 2, ClimateSensorFamily},
		{"switch schedule alone", ScheduleTypeDefault, 1, SensorFamily},
		{"switch schedule next to a climate schedule", ScheduleTypeDefault, 2, SensorFamily},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SensorFamilyOf(tc.scheduleType, tc.profiles); got != tc.want {
				t.Errorf("SensorFamilyOf(%v, %d) = %q, want %q", tc.scheduleType, tc.profiles, got, tc.want)
			}
		})
	}
}
