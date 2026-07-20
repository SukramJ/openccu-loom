// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package naming

import "testing"

func TestTitleCaseParameter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"STATE", "State"},
		{"RSSI_DEVICE", "Rssi Device"},
		{"SET_POINT_TEMPERATURE", "Set Point Temperature"},
		{"LEVEL_2", "Level 2"},
		{"x", "X"},
		{"LOW__BAT", "Low  Bat"}, // empty segment from a double underscore is preserved
	}
	for _, tc := range cases {
		if got := TitleCaseParameter(tc.in); got != tc.want {
			t.Errorf("TitleCaseParameter(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEntityDisplayName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		label       string
		labelOmit   bool
		parameter   string
		wantName    string
		wantOmitted bool
	}{
		{"primary omitted", "", true, "STATE", "", true},
		{"primary omitted wins over label", "Status", true, "STATE", "", true},
		{"locale label", "Helligkeit", false, "LEVEL", "Helligkeit", false},
		{"title-case fallback", "", false, "RSSI_DEVICE", "Rssi Device", false},
		{"single token fallback", "", false, "x", "X", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotName, gotOmitted := EntityDisplayName(tc.label, tc.labelOmit, tc.parameter)
			if gotName != tc.wantName || gotOmitted != tc.wantOmitted {
				t.Errorf("EntityDisplayName(%q, %v, %q) = (%q, %v), want (%q, %v)",
					tc.label, tc.labelOmit, tc.parameter, gotName, gotOmitted, tc.wantName, tc.wantOmitted)
			}
		})
	}
}

func TestComposedEntityName(t *testing.T) {
	t.Parallel()
	nd := NameData{DeviceName: "Wohnzimmer", ChannelName: "Relais Status", TranslatedParameterName: "Status"}

	t.Run("label present behaves like EntityDisplayName", func(t *testing.T) {
		t.Parallel()
		name, omitted := ComposedEntityName(nd, false, "STATE")
		if name != "Relais Status Status" || omitted {
			t.Fatalf("got (%q, %v), want (%q, false)", name, omitted, "Relais Status Status")
		}
	})

	t.Run("omitted label ships the collapsed name", func(t *testing.T) {
		t.Parallel()
		name, omitted := ComposedEntityName(nd, true, "STATE")
		if name != "Relais Status" || !omitted {
			t.Fatalf("got (%q, %v), want (%q, true)", name, omitted, "Relais Status")
		}
	})

	t.Run("omitted label with device-name collapse yields empty", func(t *testing.T) {
		t.Parallel()
		derived := NameData{DeviceName: "Wohnzimmer", ChannelName: "Wohnzimmer"}
		name, omitted := ComposedEntityName(derived, true, "STATE")
		if name != "" || !omitted {
			t.Fatalf("got (%q, %v), want (%q, true)", name, omitted, "")
		}
	})
}
