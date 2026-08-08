// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package surface

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// uiWith builds a NorthUI carrying one profile's overrides.
func uiWith(embedded bool, profile string, overrides map[string]config.SurfaceState) config.NorthUI {
	ui := config.NorthUI{Profiles: map[string]map[string]config.SurfaceState{profile: overrides}}
	if embedded {
		v := true
		ui.Embedded = &v
	}
	return ui
}

// TestResolveDefaultsPerProfile pins the two shipped default sets: the
// standalone profile serves everything, the embedded profile hides what
// Home Assistant owns.
func TestResolveDefaultsPerProfile(t *testing.T) {
	t.Parallel()

	standalone := Resolve(config.NorthUI{})
	if standalone.Profile != config.ProfileStandalone {
		t.Fatalf("profile = %q, want %q", standalone.Profile, config.ProfileStandalone)
	}
	for _, s := range Registry() {
		if !standalone.IsVisible(s.ID) {
			t.Errorf("standalone hides %s — the shipped standalone profile must serve every surface", s.ID)
		}
	}

	embedded := Resolve(uiWith(true, config.ProfileEmbedded, nil))
	if embedded.Profile != config.ProfileEmbedded {
		t.Fatalf("profile = %q, want %q", embedded.Profile, config.ProfileEmbedded)
	}
	wantHidden := []ID{
		"nav.overview", "nav.favorites", "nav.energy", "nav.diagrams", "nav.matter",
		"settings.ccus", "settings.oidc", "settings.ccu_auth", "settings.users",
		"settings.groups", "settings.tokens", "settings.matter",
		"device.configure", "device.configure.device-config", "device.configure.channels",
		"device.configure.links", "device.configure.schedule",
	}
	got := embedded.HiddenIDs()
	if len(got) != len(wantHidden) {
		t.Fatalf("embedded hides %v, want %v", got, wantHidden)
	}
	for _, id := range wantHidden {
		if embedded.IsVisible(id) {
			t.Errorf("embedded shows %s, want hidden by default", id)
		}
	}
}

// TestResolveHonoursOverrides checks both directions of an override.
func TestResolveHonoursOverrides(t *testing.T) {
	t.Parallel()

	// Hide a view the standalone default shows.
	hidden := Resolve(uiWith(false, config.ProfileStandalone, map[string]config.SurfaceState{
		"nav.alarm": config.SurfaceHidden,
	}))
	if hidden.IsVisible("nav.alarm") {
		t.Error("nav.alarm still visible after an explicit hide")
	}

	// Show one the embedded default hides — the case that also hands the
	// write back (see enforce_test.go).
	shown := Resolve(uiWith(true, config.ProfileEmbedded, map[string]config.SurfaceState{
		"device.configure": config.SurfaceVisible,
	}))
	if !shown.IsVisible("device.configure") {
		t.Error("device.configure still hidden after an explicit show")
	}
}

// TestResolveRefusesFloorOverride is the server-side half of the floor:
// a hand-edited YAML that hides a floor surface must be ignored, not
// honoured. Without this the floor would be a disabled switch in the UI
// and nothing more.
func TestResolveRefusesFloorOverride(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		embedded bool
		profile  string
		id       ID
	}{
		{"devices always", false, config.ProfileStandalone, "nav.devices"},
		{"settings always", true, config.ProfileEmbedded, "nav.settings"},
		{"editor always", true, config.ProfileEmbedded, "settings.navviews"},
		{"about always", true, config.ProfileEmbedded, "nav.about"},
		{"users in standalone", false, config.ProfileStandalone, "settings.users"},
		{"tokens in standalone", false, config.ProfileStandalone, "settings.tokens"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := Resolve(uiWith(tc.embedded, tc.profile, map[string]config.SurfaceState{
				string(tc.id): config.SurfaceHidden,
			}))
			if !res.IsVisible(tc.id) {
				t.Fatalf("%s was hidden despite being floor in %s", tc.id, tc.profile)
			}
			if !slices.Contains(res.Refused, tc.id) {
				t.Errorf("Refused = %v, want it to name %s", res.Refused, tc.id)
			}
		})
	}
}

// TestFloorIsProfileScoped pins that identity administration is floor in
// standalone only — the embedded profile hides it by default because
// Home Assistant owns identity there.
func TestFloorIsProfileScoped(t *testing.T) {
	t.Parallel()

	users, ok := byID["settings.users"]
	if !ok {
		t.Fatal("settings.users missing from the registry")
	}
	if !users.IsFloor(config.ProfileStandalone) {
		t.Error("settings.users must be floor in standalone")
	}
	if users.IsFloor(config.ProfileEmbedded) {
		t.Error("settings.users must not be floor in embedded")
	}
}

// TestResolveIgnoresUnknownIDs keeps a downgrade bootable: a profile
// written by a newer release names views this binary does not have, and
// that must not fail or hide anything.
func TestResolveIgnoresUnknownIDs(t *testing.T) {
	t.Parallel()

	res := Resolve(uiWith(false, config.ProfileStandalone, map[string]config.SurfaceState{
		"nav.from_the_future": config.SurfaceHidden,
	}))
	if !slices.Contains(res.Ignored, "nav.from_the_future") {
		t.Errorf("Ignored = %v, want it to name the unknown id", res.Ignored)
	}
	if !res.IsVisible("nav.from_the_future") {
		t.Error("an unknown id must read as visible, not hidden")
	}
	for _, s := range Registry() {
		if !res.IsVisible(s.ID) {
			t.Errorf("unknown id changed the resolution of %s", s.ID)
		}
	}
}

// TestResolveProfilePreviewsInactiveProfile pins that the editor can
// resolve the profile that is not live, through the same code path.
func TestResolveProfilePreviewsInactiveProfile(t *testing.T) {
	t.Parallel()

	live := config.NorthUI{} // standalone
	preview := ResolveProfile(live, config.ProfileEmbedded)
	if preview.Profile != config.ProfileEmbedded {
		t.Fatalf("profile = %q, want embedded", preview.Profile)
	}
	if preview.IsVisible("device.configure") {
		t.Error("embedded preview shows device.configure, want the embedded default")
	}
	if !Resolve(live).IsVisible("device.configure") {
		t.Error("previewing embedded must not change the live standalone resolution")
	}
}

// TestNormalizeDropsRedundantEntries pins the sparse-storage rule. A
// stored entry that merely repeats today's default would pin it forever:
// the operator would keep a view visible even after a later release
// decided Home Assistant owns it.
func TestNormalizeDropsRedundantEntries(t *testing.T) {
	t.Parallel()

	in := map[string]config.SurfaceState{
		"nav.devices":      config.SurfaceHidden,  // floor → dropped
		"nav.inbox":        config.SurfaceVisible, // == default → dropped
		"nav.alarm":        config.SurfaceHidden,  // real deviation → kept
		"nav.unknown_view": config.SurfaceHidden,  // unknown → dropped
	}
	got := Normalize(config.ProfileStandalone, in)
	if len(got) != 1 || got["nav.alarm"] != config.SurfaceHidden {
		t.Fatalf("Normalize = %v, want only nav.alarm:hidden", got)
	}
}

// TestFloorViolationsAreReported pins that the write path can tell an
// operator what it refused instead of silently normalising it away.
func TestFloorViolationsAreReported(t *testing.T) {
	t.Parallel()

	got := FloorViolations(config.ProfileStandalone, map[string]config.SurfaceState{
		"nav.settings": config.SurfaceHidden,
		"nav.alarm":    config.SurfaceHidden,
	})
	if len(got) != 1 || got[0] != "nav.settings" {
		t.Fatalf("FloorViolations = %v, want [nav.settings]", got)
	}
}

// TestRegistryIDsAreUniqueAndPrefixed guards the id scheme the profile
// storage and the write mapping both key on.
func TestRegistryIDsAreUniqueAndPrefixed(t *testing.T) {
	t.Parallel()

	seen := map[ID]bool{}
	for _, s := range Registry() {
		if seen[s.ID] {
			t.Errorf("duplicate surface id %q", s.ID)
		}
		seen[s.ID] = true
		switch {
		case len(s.ID) > 4 && s.ID[:4] == "nav.":
		case len(s.ID) > 9 && s.ID[:9] == "settings.":
		case len(s.ID) > 7 && s.ID[:7] == "device.":
		default:
			t.Errorf("surface id %q has no known kind prefix", s.ID)
		}
		if s.Defaults == nil {
			t.Errorf("surface %q has no shipped defaults", s.ID)
		}
	}
}
