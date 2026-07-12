// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import "testing"

// TestEventBridgeTr_LocalizesDiscoveryLabels guards the fix for German
// HA-discovery entity names leaking to English users: the schedule-switch,
// combined-sensor and combined-timer labels the bridge authors itself must
// resolve from the i18n catalogues in the configured locale (with {ch}/{name}
// substitution), not from hardcoded German/English literals.
func TestEventBridgeTr_LocalizesDiscoveryLabels(t *testing.T) {
	t.Parallel()
	en := NewEventBridge(nil, nil, nil).WithLocale("en")
	de := NewEventBridge(nil, nil, nil).WithLocale("de")
	cases := []struct {
		key    string
		subs   []string
		wantEN string
		wantDE string
	}{
		{"discovery.schedule_channel", []string{"ch", "3"}, "Schedule Channel 3", "Zeitplan Kanal 3"},
		{"discovery.schedule_named", []string{"name", "Terrasse"}, "Schedule Terrasse", "Zeitplan Terrasse"},
		{"discovery.level_combined", nil, "Level Combined", "Level kombiniert"},
		{"discovery.hs_color", nil, "HS Color", "HS-Farbe"},
		{"discovery.duration", nil, "Duration", "Zeitdauer"},
	}
	for _, tc := range cases {
		if got := en.tr(tc.key, tc.subs...); got != tc.wantEN {
			t.Errorf("en tr(%q)=%q, want %q", tc.key, got, tc.wantEN)
		}
		if got := de.tr(tc.key, tc.subs...); got != tc.wantDE {
			t.Errorf("de tr(%q)=%q, want %q", tc.key, got, tc.wantDE)
		}
	}
}
