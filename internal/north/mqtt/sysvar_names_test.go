// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import "testing"

// TestClassifyAutoSysvar_LongestMatch pins the classification of
// CCU-auto-generated sysvar names, including the longest-match precedence that
// keeps the FeedIn / Today / Yesterday variants from collapsing onto their base
// token. The friendly names themselves live in the i18n catalogues and are
// asserted end-to-end in the discovery test; here we pin the key + metadata.
func TestClassifyAutoSysvar_LongestMatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		wantOK     bool
		wantKey    string
		wantDevCls string
		wantUnit   string
		wantState  string
	}{
		{
			name: "svEnergyCounter_14007_0001DBE9915BE4:6", wantOK: true,
			wantKey:    "discovery.energy_counter_total",
			wantDevCls: "energy", wantUnit: "Wh", wantState: "total_increasing",
		},
		{
			// Must win over the shorter "svEnergyCounter" substring.
			name: "svEnergyCounterFeedIn_14100_0001DBE9915BE4:6", wantOK: true,
			wantKey:    "discovery.energy_counter_feed_in_total",
			wantDevCls: "energy", wantUnit: "Wh", wantState: "total_increasing",
		},
		{
			name: "svHmIPRainCounter_1234_000A:1", wantOK: true,
			wantKey:    "discovery.rain_counter_total",
			wantDevCls: "", wantUnit: "mm", wantState: "total_increasing",
		},
		{
			// Must win over the shorter "svHmIPRainCounter" substring.
			name: "svHmIPRainCounterToday_1234_000A:1", wantOK: true,
			wantKey:    "discovery.rain_counter_today",
			wantDevCls: "", wantUnit: "mm", wantState: "total_increasing",
		},
		{
			name: "svHmIPRainCounterYesterday_1234_000A:1", wantOK: true,
			wantKey: "discovery.rain_counter_yesterday",
		},
		{
			name: "svHmIPSunshineCounter_9_000B:2", wantOK: true,
			wantKey:    "discovery.sunshine_counter_total",
			wantDevCls: "duration", wantUnit: "min", wantState: "total_increasing",
		},
		{
			name: "svHmIPSunshineCounterYesterday_9_000B:2", wantOK: true,
			wantKey:    "discovery.sunshine_counter_yesterday",
			wantDevCls: "duration", wantUnit: "min", wantState: "total_increasing",
		},
		{name: "Anwesenheit", wantOK: false},
		{name: "svCustomOperatorVariable", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cls, ok := classifyAutoSysvar(tc.name)
			if ok != tc.wantOK {
				t.Fatalf("classifyAutoSysvar(%q) ok=%v, want %v", tc.name, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if cls.translationKey != tc.wantKey {
				t.Errorf("translationKey = %q, want %q", cls.translationKey, tc.wantKey)
			}
			if tc.wantDevCls != "" && cls.deviceClass != tc.wantDevCls {
				t.Errorf("device_class = %q, want %q", cls.deviceClass, tc.wantDevCls)
			}
			if tc.wantUnit != "" && cls.unit != tc.wantUnit {
				t.Errorf("unit = %q, want %q", cls.unit, tc.wantUnit)
			}
			if tc.wantState != "" && cls.stateClass != tc.wantState {
				t.Errorf("state_class = %q, want %q", cls.stateClass, tc.wantState)
			}
		})
	}
}
