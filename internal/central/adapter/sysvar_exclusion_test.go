// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import "testing"

// TestSysvarIsExcluded pins the fetch-time sysvar filter: OldVal/pcCCUID
// scratch values and the fixed alarm/service-message IDs (40/41) never
// enter the hub model, so REST, MQTT discovery, Matter and external
// clients all see the same catalogue as the reference stack.
func TestSysvarIsExcluded(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, id string
		want     bool
	}{
		{"svEnergyCounterOldVal_14179", "1234", true},
		{"svCounterOldVal_51323", "1235", true},
		{"pcCCUID", "1236", true},
		{"Alarmmeldungen", "40", true},
		{"Servicemeldungen", "41", true},
		{"svEnergyCounter_14179", "1237", false},
		{"Temperatur Garten", "401", false}, // "401" must not match "40"
		{"CCU-Reboot", "1238", false},
	} {
		if got := sysvarIsExcluded(tc.name, tc.id); got != tc.want {
			t.Errorf("sysvarIsExcluded(%q, %q) = %v, want %v", tc.name, tc.id, got, tc.want)
		}
	}
}
