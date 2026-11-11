// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

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

	standalone := resolveSingle(config.NorthUI{})
	if standalone.Profile != config.ProfileStandalone {
		t.Fatalf("profile = %q, want %q", standalone.Profile, config.ProfileStandalone)
	}
	for _, s := range Registry() {
		if !standalone.IsVisible(s.ID) {
			t.Errorf("standalone hides %s — the shipped standalone profile must serve every surface", s.ID)
		}
	}

	embedded := resolveSingle(uiWith(true, config.ProfileEmbedded, nil))
	if embedded.Profile != config.ProfileEmbedded {
		t.Fatalf("profile = %q, want %q", embedded.Profile, config.ProfileEmbedded)
	}
	wantHidden := []ID{
		"nav.matter",
		"settings.ccus", "settings.oidc", "settings.ccu_auth", "settings.users",
		"settings.groups", "settings.tokens", "settings.matter",
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
	hidden := resolveSingle(uiWith(false, config.ProfileStandalone, map[string]config.SurfaceState{
		"nav.alarm": config.SurfaceHidden,
	}))
	if hidden.IsVisible("nav.alarm") {
		t.Error("nav.alarm still visible after an explicit hide")
	}

	// Show one the embedded default hides — the case that also hands the
	// write back (see enforce_test.go).
	shown := resolveSingle(uiWith(true, config.ProfileEmbedded, map[string]config.SurfaceState{
		"settings.oidc": config.SurfaceVisible,
	}))
	if !shown.IsVisible("settings.oidc") {
		t.Error("settings.oidc still hidden after an explicit show")
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
			res := resolveSingle(uiWith(tc.embedded, tc.profile, map[string]config.SurfaceState{
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

	res := resolveSingle(uiWith(false, config.ProfileStandalone, map[string]config.SurfaceState{
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
	preview := resolveProfile(live, config.ProfileEmbedded, Fleet{})
	if preview.Profile != config.ProfileEmbedded {
		t.Fatalf("profile = %q, want embedded", preview.Profile)
	}
	if preview.IsVisible("settings.oidc") {
		t.Error("embedded preview shows settings.oidc, want the embedded default")
	}
	if !resolveSingle(live).IsVisible("settings.oidc") {
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
	got := Normalize(config.ProfileStandalone, in, Fleet{})
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

// TestEmbeddedKeepsDeviceConfiguration pins that the embedded profile
// serves the device editor on every fleet size.
//
// The embedded profile hides what Home Assistant provides itself. The
// device editor is not among it: the Home Assistant integration registers
// no configuration panel of its own for an entry served by this daemon
// and leaves paramsets, links and schedules to this UI. Hiding the tabs
// here would leave such an installation with no editor at all.
func TestEmbeddedKeepsDeviceConfiguration(t *testing.T) {
	t.Parallel()

	editor := []ID{
		"device.configure",
		"device.configure.channels",
		"device.configure.links",
		"device.configure.schedule",
	}
	for _, centrals := range []int{0, 1, 3} {
		res := ResolveFleet(uiWith(true, config.ProfileEmbedded, nil), config.UIOriginHomeAssistant, Fleet{Centrals: centrals})
		for _, id := range editor {
			if !res.IsVisible(id) {
				t.Errorf("%s hidden in the embedded profile with %d CCU(s) — it is the only editor Home Assistant users have", id, centrals)
			}
		}
	}
	for _, id := range editor {
		if byID[id].HAOwns {
			t.Errorf("%s is marked as provided by Home Assistant; the editor row would explain a default that no longer exists", id)
		}
	}
}

// TestMultiCentralWidensTheEmbeddedDefaults is the fleet rule.
//
// A Home Assistant config entry addresses ONE CCU — the loom backend
// passes a serial — while the embedded switch is daemon-wide. Bind one
// of three CCUs into HA and the single-CCU default would hide the CCU
// settings for the other two in the only UI that can edit them: Home
// Assistant has no entry for them.
func TestMultiCentralWidensTheEmbeddedDefaults(t *testing.T) {
	t.Parallel()

	single := ResolveFleet(uiWith(true, config.ProfileEmbedded, nil), config.UIOriginHomeAssistant, Fleet{Centrals: 1})
	fleet := ResolveFleet(uiWith(true, config.ProfileEmbedded, nil), config.UIOriginHomeAssistant, Fleet{Centrals: 3})

	widened := []ID{"settings.ccus"}
	for _, id := range widened {
		if single.IsVisible(id) {
			t.Errorf("%s visible on a single-CCU daemon, want the shipped embedded default", id)
		}
		if !fleet.IsVisible(id) {
			t.Errorf("%s hidden on a 3-CCU daemon — the CCUs Home Assistant has no entry for lose their only editor", id)
		}
	}

	// Everything else keeps its default: identity belongs to Home
	// Assistant however many CCUs there are.
	for _, id := range []ID{"settings.users", "settings.oidc"} {
		if fleet.IsVisible(id) {
			t.Errorf("%s visible on a multi-CCU daemon — the fleet size says nothing about identity", id)
		}
	}
}

// TestMultiCentralLeavesStandaloneAlone pins that the rule is scoped to
// the embedded profile. Standalone already shows everything; a fleet
// rule there could only ever un-hide what an operator chose to hide.
func TestMultiCentralLeavesStandaloneAlone(t *testing.T) {
	t.Parallel()

	res := ResolveFleet(uiWith(false, config.ProfileStandalone, map[string]config.SurfaceState{
		"settings.ccus": config.SurfaceHidden,
	}), config.UIOriginDirect, Fleet{Centrals: 4})
	if res.IsVisible("settings.ccus") {
		t.Error("the fleet rule overrode an explicit standalone hide")
	}
}

// TestMultiCentralDefaultIsStillOverridable pins that this moves the
// default, not the ceiling: an operator who wants the surface hidden on
// a multi-CCU daemon can still say so.
func TestMultiCentralDefaultIsStillOverridable(t *testing.T) {
	t.Parallel()

	res := ResolveFleet(uiWith(true, config.ProfileEmbedded, map[string]config.SurfaceState{
		"settings.ccus": config.SurfaceHidden,
	}), config.UIOriginHomeAssistant, Fleet{Centrals: 3})
	if res.IsVisible("settings.ccus") {
		t.Error("an explicit hide was ignored on a multi-CCU daemon")
	}
}

// TestNormalizeFollowsTheFleetDefault pins the sparse-storage rule
// against the moved default. On a multi-CCU daemon "visible" for
// settings.ccus IS the default, so storing it would pin today's fleet
// size into the profile — and keep the surface visible after the
// operator removes the extra CCUs.
func TestNormalizeFollowsTheFleetDefault(t *testing.T) {
	t.Parallel()

	in := map[string]config.SurfaceState{"settings.ccus": config.SurfaceVisible}

	single := Normalize(config.ProfileEmbedded, in, Fleet{Centrals: 1})
	if single["settings.ccus"] != config.SurfaceVisible {
		t.Errorf("single-CCU: %v, want the deviation kept", single)
	}

	fleet := Normalize(config.ProfileEmbedded, in, Fleet{Centrals: 3})
	if len(fleet) != 0 {
		t.Errorf("multi-CCU: %v, want the entry dropped as redundant", fleet)
	}
}

// resolveSingle is the single-CCU shorthand these tests read best with.
// Production always knows its fleet size and calls ResolveFleet, so the
// shorthand lives here rather than in the package's surface.
func resolveSingle(ui config.NorthUI) Resolution {
	return ResolveFleet(ui, config.UIOriginHomeAssistant, Fleet{})
}

// TestNewDevicesViewIsOfferedOnEverySystem pins that the view listing
// devices waiting to be accepted or released carries no capability gate.
//
// It lists the daemon's own onboarding hold, which exists on every system
// type. Gated on the CCU inbox feature it vanished from the navigation of
// an openccu-lite fleet — the system that has no other place to accept a
// held device. The programs view stays gated, so a registry that dropped
// every gate would not pass the second half.
func TestNewDevicesViewIsOfferedOnEverySystem(t *testing.T) {
	t.Parallel()

	gates := map[ID]Gate{}
	for _, s := range Registry() {
		gates[s.ID] = s.Gate
	}
	got, ok := gates["nav.inbox"]
	if !ok {
		t.Fatal("nav.inbox is not registered; operator profiles and links reference that id")
	}
	if got != GateNone {
		t.Errorf("nav.inbox gate = %q, want none: the onboarding hold exists on every system", got)
	}
	if gates["nav.programs"] != GateFeaturePrograms {
		t.Errorf("nav.programs gate = %q, want %q (control: per-central gates still apply)",
			gates["nav.programs"], GateFeaturePrograms)
	}
}

// TestEverySurfaceCarriesEveryProfileColumn fails on a surface without a
// shipped default for one of the profiles. A missing column is not an
// error at run time — an unknown profile reads as visible — so without
// this guard a new profile would silently show whatever nobody reviewed.
func TestEverySurfaceCarriesEveryProfileColumn(t *testing.T) {
	t.Parallel()

	for _, s := range Registry() {
		for _, profile := range config.SurfaceProfiles() {
			if _, ok := s.Defaults[profile]; !ok {
				t.Errorf("surface %s has no shipped default for profile %q", s.ID, profile)
			}
		}
		if len(s.Defaults) != len(config.SurfaceProfiles()) {
			t.Errorf("surface %s carries %d default columns, want %d", s.ID, len(s.Defaults), len(config.SurfaceProfiles()))
		}
	}
}

// TestLiteProfileHidesWhatTheBoxProvides pins the lite column: the
// surfaces the openccu-lite shell provides are hidden, and the ones only
// Home Assistant provides are not carried over from the embedded column.
func TestLiteProfileHidesWhatTheBoxProvides(t *testing.T) {
	t.Parallel()

	res := ResolveFleet(config.NorthUI{}, config.UIOriginLiteShell, Fleet{Centrals: 1})
	if res.Profile != config.ProfileLite {
		t.Fatalf("profile = %q, want %q", res.Profile, config.ProfileLite)
	}
	for _, id := range []ID{"settings.users", "settings.oidc", "settings.ccu_auth", "settings.ccus", "nav.firmware"} {
		if res.IsVisible(id) {
			t.Errorf("%s is visible in the lite profile; the box provides it", id)
		}
		if !byID[id].BoxOwns {
			t.Errorf("%s is hidden in the lite profile without the box-owns reason", id)
		}
	}
	// Hidden for Home Assistant, which has its own; the box has none.
	for _, id := range []ID{"nav.matter", "settings.matter", "settings.tokens", "settings.groups"} {
		if !res.IsVisible(id) {
			t.Errorf("%s is hidden in the lite profile; the box does not provide it", id)
		}
	}
	// The device editor is the reason the UI exists on the box.
	for _, id := range []ID{"device.configure", "device.configure.channels", "device.configure.links", "device.configure.schedule"} {
		if !res.IsVisible(id) {
			t.Errorf("%s is hidden in the lite profile", id)
		}
	}
}

// TestLiteProfileShowsCCUSettingsOnAFleet pins that the fleet rule
// reaches the lite column: the daemon onboards its own box, and a second
// CCU is one the box cannot administer.
func TestLiteProfileShowsCCUSettingsOnAFleet(t *testing.T) {
	t.Parallel()

	single := ResolveFleet(config.NorthUI{}, config.UIOriginLiteShell, Fleet{Centrals: 1})
	fleet := ResolveFleet(config.NorthUI{}, config.UIOriginLiteShell, Fleet{Centrals: 2})
	if single.IsVisible("settings.ccus") {
		t.Error("settings.ccus visible on a single-box daemon")
	}
	if !fleet.IsVisible("settings.ccus") {
		t.Error("settings.ccus hidden although the daemon serves a second CCU")
	}
}

// TestLiteProfileMayHideTheAccountTabs pins the floor: users and tokens
// are the way back in only where the daemon's own accounts are the login.
func TestLiteProfileMayHideTheAccountTabs(t *testing.T) {
	t.Parallel()

	for _, id := range []ID{"settings.users", "settings.tokens"} {
		if byID[id].IsFloor(config.ProfileLite) {
			t.Errorf("%s is floor in the lite profile", id)
		}
		if !byID[id].IsFloor(config.ProfileStandalone) {
			t.Errorf("%s lost its standalone floor", id)
		}
	}
}
