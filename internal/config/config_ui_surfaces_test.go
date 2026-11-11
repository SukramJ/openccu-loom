// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package config

import "testing"

// TestNorthUIEmbeddedDefaultsOff pins the tri-state default. Unlike
// Enabled (nil → true) an unset Embedded must read false: a daemon that
// was never told Home Assistant owns its config surface has to serve
// everything, or an upgrade would silently amputate the UI.
func TestNorthUIEmbeddedDefaultsOff(t *testing.T) {
	t.Parallel()

	var ui NorthUI
	if ui.IsEmbedded() {
		t.Error("unset embedded reads true, want false")
	}
	if got := ui.ActiveProfile(); got != ProfileStandalone {
		t.Errorf("ActiveProfile = %q, want %q", got, ProfileStandalone)
	}

	on := true
	ui.Embedded = &on
	if !ui.IsEmbedded() {
		t.Error("embedded:true reads false")
	}
	if got := ui.ActiveProfile(); got != ProfileEmbedded {
		t.Errorf("ActiveProfile = %q, want %q", got, ProfileEmbedded)
	}

	off := false
	ui.Embedded = &off
	if ui.IsEmbedded() {
		t.Error("embedded:false reads true — the pointer must distinguish unset from explicit false")
	}
}

// TestSurfaceOverridesNeverNil lets callers range over the result of a
// profile that was never configured.
func TestSurfaceOverridesNeverNil(t *testing.T) {
	t.Parallel()

	var ui NorthUI
	if got := ui.SurfaceOverrides(ProfileEmbedded); got == nil {
		t.Fatal("SurfaceOverrides returned nil for an unconfigured profile")
	}

	ui.Profiles = map[string]map[string]SurfaceState{
		ProfileStandalone: {"nav.alarm": SurfaceHidden},
	}
	got := ui.SurfaceOverrides(ProfileStandalone)
	if got["nav.alarm"] != SurfaceHidden {
		t.Errorf("override lost: %v", got)
	}
	// The copy must not alias the stored map, or a handler mutating its
	// working copy would rewrite the live config.
	got["nav.alarm"] = SurfaceVisible
	if ui.Profiles[ProfileStandalone]["nav.alarm"] != SurfaceHidden {
		t.Error("SurfaceOverrides aliases the stored map")
	}
}

// TestValidateSurfaceProfiles covers the two things config validation
// owns: profile names and state values. Surface ids are deliberately not
// validated here — see the doc comment on validateSurfaceProfiles.
func TestValidateSurfaceProfiles(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		profiles map[string]map[string]SurfaceState
		wantErr  bool
	}{
		{"empty", nil, false},
		{"valid standalone", map[string]map[string]SurfaceState{
			ProfileStandalone: {"nav.alarm": SurfaceHidden},
		}, false},
		{"valid embedded", map[string]map[string]SurfaceState{
			ProfileEmbedded: {"nav.matter": SurfaceVisible},
		}, false},
		{"unknown profile", map[string]map[string]SurfaceState{
			"kiosk": {"nav.alarm": SurfaceHidden},
		}, true},
		{"unknown state", map[string]map[string]SurfaceState{
			ProfileStandalone: {"nav.alarm": SurfaceState("off")},
		}, true},
		{"id from a newer release is accepted", map[string]map[string]SurfaceState{
			ProfileStandalone: {"nav.not_in_this_binary": SurfaceHidden},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateSurfaceProfiles(&NorthUI{Profiles: tc.profiles})
			if tc.wantErr && err == nil {
				t.Fatal("want an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}

// TestOverlayUIEmbedded pins the env override the HA add-on maps its
// option onto.
func TestOverlayUIEmbedded(t *testing.T) {
	t.Parallel()

	c := Default()
	if err := c.OverlayFromEnv(func(k string) string {
		if k == "OPENCCU_LOOM_UI_EMBEDDED" {
			return "true"
		}
		return ""
	}); err != nil {
		t.Fatalf("OverlayFromEnv: %v", err)
	}
	if !c.North.UI.IsEmbedded() {
		t.Error("OPENCCU_LOOM_UI_EMBEDDED=true did not reach north.ui.embedded")
	}

	c2 := Default()
	if err := c2.OverlayFromEnv(func(string) string { return "" }); err != nil {
		t.Fatalf("OverlayFromEnv: %v", err)
	}
	if c2.North.UI.Embedded != nil {
		t.Error("absent env var must leave the pointer nil, not stamp a value")
	}
}

// TestActiveProfileForEveryDoor pins profile selection per request
// origin, for each declaration an operator can make.
func TestActiveProfileForEveryDoor(t *testing.T) {
	t.Parallel()

	on := true
	cases := []struct {
		name   string
		ui     NorthUI
		origin UIOrigin
		want   string
	}{
		{"undeclared, direct", NorthUI{}, UIOriginDirect, ProfileStandalone},
		{"undeclared, Home Assistant", NorthUI{}, UIOriginHomeAssistant, ProfileStandalone},
		{"undeclared, box shell", NorthUI{}, UIOriginLiteShell, ProfileLite},
		{"embedded, direct", NorthUI{Embedded: &on}, UIOriginDirect, ProfileStandalone},
		{"embedded, Home Assistant", NorthUI{Embedded: &on}, UIOriginHomeAssistant, ProfileEmbedded},
		{"embedded, box shell", NorthUI{Embedded: &on}, UIOriginLiteShell, ProfileLite},
		{"embedded always, direct", NorthUI{Embedded: &on, EmbeddedScope: EmbeddedScopeAlways}, UIOriginDirect, ProfileEmbedded},
		{"embedded always, box shell", NorthUI{Embedded: &on, EmbeddedScope: EmbeddedScopeAlways}, UIOriginLiteShell, ProfileEmbedded},
		{"scope always without the declaration, box shell", NorthUI{EmbeddedScope: EmbeddedScopeAlways}, UIOriginLiteShell, ProfileLite},
		{"unknown origin", NorthUI{Embedded: &on}, UIOrigin("carrier-pigeon"), ProfileStandalone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.ui.ActiveProfileFor(tc.origin); got != tc.want {
				t.Errorf("ActiveProfileFor(%q) = %q, want %q", tc.origin, got, tc.want)
			}
		})
	}
}

// TestValidateAcceptsEverySurfaceProfile pins that validation and the
// profile listing are one set: a stored lite profile must not stop a boot.
func TestValidateAcceptsEverySurfaceProfile(t *testing.T) {
	t.Parallel()

	for _, profile := range SurfaceProfiles() {
		ui := NorthUI{Profiles: map[string]map[string]SurfaceState{profile: {"nav.matter": SurfaceHidden}}}
		if err := validateSurfaceProfiles(&ui); err != nil {
			t.Errorf("profile %q rejected: %v", profile, err)
		}
	}
	ui := NorthUI{Profiles: map[string]map[string]SurfaceState{"kiosk": {}}}
	if err := validateSurfaceProfiles(&ui); err == nil {
		t.Error("unknown profile accepted")
	}
}
