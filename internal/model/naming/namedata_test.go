// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package naming

import (
	"strings"
	"testing"
)

func TestNameDataComposition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                   string
		nd                     NameData
		wantName               string
		wantTranslatedName     string
		wantFullName           string
		wantTranslatedFullName string
	}{
		{
			name:                   "channel-prefix duplicates device gets stripped",
			nd:                     NameData{DeviceName: "Wohnzimmer", ChannelName: "Wohnzimmer", ParameterName: "State"},
			wantName:               "State",
			wantTranslatedName:     "State",
			wantFullName:           "Wohnzimmer State",
			wantTranslatedFullName: "Wohnzimmer State",
		},
		{
			name:                   "translated parameter name used when set",
			nd:                     NameData{DeviceName: "Wohnzimmer", ChannelName: "Wohnzimmer", ParameterName: "State", TranslatedParameterName: "Schalter"},
			wantName:               "State",
			wantTranslatedName:     "Schalter",
			wantFullName:           "Wohnzimmer State",
			wantTranslatedFullName: "Wohnzimmer Schalter",
		},
		{
			name:                   "empty channel name, parameter only",
			nd:                     NameData{DeviceName: "Wohnzimmer", ChannelName: "", ParameterName: "State"},
			wantName:               "State",
			wantTranslatedName:     "State",
			wantFullName:           "Wohnzimmer State",
			wantTranslatedFullName: "Wohnzimmer State",
		},
		{
			name:                   "empty device name, channel and parameter",
			nd:                     NameData{DeviceName: "", ChannelName: "Buero", ParameterName: "Temperatur"},
			wantName:               "Buero Temperatur",
			wantTranslatedName:     "Buero Temperatur",
			wantFullName:           "Buero Temperatur",
			wantTranslatedFullName: "Buero Temperatur",
		},
		{
			name:                   "empty parameter, channel-level entity",
			nd:                     NameData{DeviceName: "X", ChannelName: "X", ParameterName: ""},
			wantName:               "",
			wantTranslatedName:     "",
			wantFullName:           "X",
			wantTranslatedFullName: "X",
		},
		{
			name:                   "translated name falls back to Name when TranslatedParameterName empty",
			nd:                     NameData{DeviceName: "D", ChannelName: "D", ParameterName: "State", TranslatedParameterName: ""},
			wantName:               "State",
			wantTranslatedName:     "State",
			wantFullName:           "D State",
			wantTranslatedFullName: "D State",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.nd.Name(); got != tc.wantName {
				t.Errorf("Name() = %q, want %q", got, tc.wantName)
			}
			if got := tc.nd.TranslatedName(); got != tc.wantTranslatedName {
				t.Errorf("TranslatedName() = %q, want %q", got, tc.wantTranslatedName)
			}
			if got := tc.nd.FullName(); got != tc.wantFullName {
				t.Errorf("FullName() = %q, want %q", got, tc.wantFullName)
			}
			if got := tc.nd.TranslatedFullName(); got != tc.wantTranslatedFullName {
				t.Errorf("TranslatedFullName() = %q, want %q", got, tc.wantTranslatedFullName)
			}
		})
	}
}

func TestNameData_IsZero(t *testing.T) {
	t.Parallel()
	if !EmptyNameData.IsZero() {
		t.Fatal("EmptyNameData.IsZero() = false, want true")
	}
	nd := NameData{DeviceName: "X"}
	if nd.IsZero() {
		t.Fatal("non-empty NameData.IsZero() = true, want false")
	}
}

func TestNameData_DevicePrefixStripped(t *testing.T) {
	t.Parallel()
	nd := NameData{DeviceName: "Wohnzimmer Licht", ChannelName: "Wohnzimmer Licht Kanal 1", ParameterName: "STATE"}
	// The channel name starts with the device name, so "Wohnzimmer Licht " must be stripped.
	name := nd.Name()
	if strings.HasPrefix(name, "Wohnzimmer Licht") {
		t.Errorf("Name() should strip device prefix, got %q", name)
	}
}

func TestNameData_NoPrefixStrippingWhenDifferent(t *testing.T) {
	t.Parallel()
	nd := NameData{DeviceName: "Device A", ChannelName: "Switch Channel", ParameterName: "STATE"}
	name := nd.Name()
	// Channel name does not start with device name → both are preserved.
	if !strings.Contains(name, "Switch Channel") {
		t.Errorf("Name() = %q; expected to contain channel name", name)
	}
}

func TestNameData_FullNameIncludesDevice(t *testing.T) {
	t.Parallel()
	nd := NameData{DeviceName: "Küche", ChannelName: "Schalter", ParameterName: "STATE"}
	full := nd.FullName()
	if !strings.HasPrefix(full, "Küche") {
		t.Errorf("FullName() = %q; should start with device name", full)
	}
}

func TestNameData_EmptyChannelName(t *testing.T) {
	t.Parallel()
	nd := NameData{DeviceName: "Device", ChannelName: "", ParameterName: ""}
	if nd.FullName() != "Device" {
		t.Errorf("FullName() = %q; want %q", nd.FullName(), "Device")
	}
}

func TestNameData_TranslatedNameFallsBackWhenEmpty(t *testing.T) {
	t.Parallel()
	nd := NameData{DeviceName: "D", ChannelName: "D", ParameterName: "State", TranslatedParameterName: ""}
	if nd.TranslatedName() != nd.Name() {
		t.Errorf("TranslatedName() = %q, want %q (fallback to Name)", nd.TranslatedName(), nd.Name())
	}
}

func TestNameData_UnicodePreserved(t *testing.T) {
	t.Parallel()
	nd := NameData{DeviceName: "日本語", ChannelName: "チャンネル", ParameterName: "パラメータ"}
	full := nd.FullName()
	if !strings.Contains(full, "日本語") {
		t.Errorf("FullName() = %q; unicode device name must be preserved", full)
	}
}

func TestNameData_CollapsedName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		nd   NameData
		want string
	}{
		{
			name: "custom channel name without marker",
			nd:   NameData{DeviceName: "Wohnzimmer", ChannelName: "Relais Status"},
			want: "Relais Status",
		},
		{
			name: "derived channel name reduces to empty",
			nd:   NameData{DeviceName: "Wohnzimmer", ChannelName: "Wohnzimmer"},
			want: "",
		},
		{
			name: "derived channel name keeps the marker",
			nd:   NameData{DeviceName: "Wohnzimmer", ChannelName: "Wohnzimmer", ChannelPostfix: "ch2"},
			want: "ch2",
		},
		{
			name: "custom channel name keeps the marker",
			nd:   NameData{DeviceName: "Wohnzimmer", ChannelName: "Status", ChannelPostfix: "ch4"},
			want: "Status ch4",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.nd.CollapsedName(); got != tc.want {
				t.Fatalf("CollapsedName() = %q, want %q", got, tc.want)
			}
		})
	}
}
