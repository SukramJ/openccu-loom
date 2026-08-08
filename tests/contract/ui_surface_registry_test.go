// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/ui/surface"
)

// The Config UI's navigable entry points live in three SPA tables. The
// Go registry in internal/north/ui/surface must carry exactly the same
// set, because three consumers read it and none of them can notice a
// gap on its own: the navigation gate, the profile editor, and the
// write enforcement that scopes the Home Assistant passthrough identity.
//
// A view that lands without a registry entry is not a cosmetic omission.
// In the embedded profile it is either a leaked duplicate of something
// Home Assistant already owns, or a capability nobody can reach — and
// both look exactly like a working release until an operator hits them.

func spaFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", "..", "assets", "ui", "src"}, parts...)...)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// sliceBetween returns the source between the first occurrence of start
// and the next occurrence of end after it.
func sliceBetween(t *testing.T, src, start, end string) string {
	t.Helper()
	i := strings.Index(src, start)
	if i < 0 {
		t.Fatalf("marker %q not found — the SPA table was renamed; update this test with it", start)
	}
	rest := src[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		t.Fatalf("end marker %q not found after %q", end, start)
	}
	return rest[:j]
}

var (
	navHrefRe   = regexp.MustCompile(`href:\s*"#/([a-z-]+)"`)
	tabIDRe     = regexp.MustCompile(`\{\s*id:\s*"([a-z_]+)"`)
	tsUnionRe   = regexp.MustCompile(`"([a-z-]+)"`)
	surfaceKeyE = regexp.MustCompile(`"(surface\.(?:label|desc)\.[^"]+)"\s*:`)
)

// spaSurfaceIDs collects the surface ids the SPA actually renders.
func spaSurfaceIDs(t *testing.T) []surface.ID {
	t.Helper()
	out := make([]surface.ID, 0, len(surface.Registry()))

	nav := spaFile(t, "lib", "nav.ts")
	navBody := sliceBetween(t, nav, "export function navClusters", "\n}\n")
	for _, m := range navHrefRe.FindAllStringSubmatch(navBody, -1) {
		out = append(out, surface.ID("nav."+m[1]))
	}

	settings := spaFile(t, "routes", "Settings.svelte")
	tabs := sliceBetween(t, settings, "const ALL_TABS: Tab[] = [", "];")
	for _, m := range tabIDRe.FindAllStringSubmatch(tabs, -1) {
		out = append(out, surface.ID("settings."+m[1]))
	}

	detail := spaFile(t, "routes", "DeviceDetail.svelte")
	top := sliceBetween(t, detail, "type TopTab =", ";")
	for _, m := range tsUnionRe.FindAllStringSubmatch(top, -1) {
		out = append(out, surface.ID("device."+m[1]))
	}
	sub := sliceBetween(t, detail, "type ConfigSub =", ";")
	for _, m := range tsUnionRe.FindAllStringSubmatch(sub, -1) {
		out = append(out, surface.ID("device.configure."+m[1]))
	}
	return out
}

// TestEverySurfaceIsRegistered fails when the SPA and the Go registry
// disagree in either direction.
func TestEverySurfaceIsRegistered(t *testing.T) {
	t.Parallel()

	inSPA := spaSurfaceIDs(t)
	if len(inSPA) < 40 {
		t.Fatalf("only %d surfaces parsed out of the SPA — the parser lost a table rather than the SPA losing views", len(inSPA))
	}

	registered := map[surface.ID]bool{}
	for _, s := range surface.Registry() {
		registered[s.ID] = true
	}
	spa := map[surface.ID]bool{}
	for _, id := range inSPA {
		spa[id] = true
	}

	var missing, extra []string
	for id := range spa {
		if !registered[id] {
			missing = append(missing, string(id))
		}
	}
	for id := range registered {
		if !spa[id] {
			extra = append(extra, string(id))
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) > 0 {
		t.Errorf("%d SPA surfaces have no entry in internal/north/ui/surface "+
			"(classify each one — shipped default per profile, floor, write-gating):\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(extra) > 0 {
		t.Errorf("%d registered surfaces no longer exist in the SPA "+
			"(drop them from the registry, or a profile keeps carrying a dead id):\n  %s",
			len(extra), strings.Join(extra, "\n  "))
	}
}

// TestSurfaceCopyIsComplete requires a label and a description for every
// surface, in both locales. The description is not decoration: it is the
// only thing that tells an operator what they are about to switch off,
// and in the embedded profile the difference between "Device groups
// (HmIP)" and "User groups" decides whether they hide the right one.
func TestSurfaceCopyIsComplete(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "assets", "ui", "src", "lib", "i18n.ts")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read i18n.ts: %v", err)
	}
	src := string(raw)
	deStart := strings.Index(src, "const DE")
	if deStart < 0 {
		t.Fatal("i18n.ts: 'const DE' catalogue marker not found")
	}
	collect := func(block string) map[string]bool {
		out := map[string]bool{}
		for _, m := range surfaceKeyE.FindAllStringSubmatch(block, -1) {
			out[m[1]] = true
		}
		return out
	}
	en, de := collect(src[:deStart]), collect(src[deStart:])

	var missing []string
	for _, s := range surface.Registry() {
		for _, kind := range []string{"label", "desc"} {
			key := "surface." + kind + "." + string(s.ID)
			if !en[key] {
				missing = append(missing, "EN "+key)
			}
			if !de[key] {
				missing = append(missing, "DE "+key)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%d surface i18n entries missing in assets/ui/src/lib/i18n.ts "+
			"(every surface needs surface.label.<id> AND surface.desc.<id> in EN and DE):\n%s",
			len(missing), strings.Join(missing, "\n"))
	}
}

// TestFloorSurfacesAreTheDocumentedSet pins the floor itself. It is the
// one part of the model an operator cannot repair from the UI once it is
// wrong: hiding Settings or this editor leaves YAML and the REST API as
// the only way back. Changing this set is a decision, so it changes here
// too, deliberately.
func TestFloorSurfacesAreTheDocumentedSet(t *testing.T) {
	t.Parallel()

	wantAlways := []surface.ID{"nav.devices", "nav.settings", "nav.about", "settings.navviews"}
	wantStandalone := []surface.ID{"settings.users", "settings.tokens"}

	var gotAlways, gotStandalone []surface.ID
	for _, s := range surface.Registry() {
		switch s.Floor {
		case surface.FloorAlways:
			gotAlways = append(gotAlways, s.ID)
		case surface.FloorStandalone:
			gotStandalone = append(gotStandalone, s.ID)
		case surface.FloorNone:
		}
	}
	slices.Sort(gotAlways)
	slices.Sort(gotStandalone)
	slices.Sort(wantAlways)
	slices.Sort(wantStandalone)

	if !slices.Equal(gotAlways, wantAlways) {
		t.Errorf("always-floor surfaces = %v, want %v", gotAlways, wantAlways)
	}
	if !slices.Equal(gotStandalone, wantStandalone) {
		t.Errorf("standalone-floor surfaces = %v, want %v", gotStandalone, wantStandalone)
	}
}

// TestE2ESurfaceFixtureMatchesRegistry keeps the Playwright fixture from
// drifting away from the registry it stands in for.
//
// A stale fixture is worse than none: the browser suite would keep
// gating on a view set the daemon no longer serves, and every visual
// baseline would lock in a navigation nobody sees. Regenerate with the
// handler's own renderer rather than editing it by hand.
func TestE2ESurfaceFixtureMatchesRegistry(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "assets", "ui", "tests", "e2e", "fixtures", "ui-surfaces.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture struct {
		Effective map[string]bool `json:"effective"`
		Surfaces  []struct {
			ID string `json:"id"`
		} `json:"surfaces"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	inFixture := map[string]bool{}
	for _, s := range fixture.Surfaces {
		inFixture[s.ID] = true
	}
	var missing []string
	for _, s := range surface.Registry() {
		if !inFixture[string(s.ID)] {
			missing = append(missing, string(s.ID))
		}
		if _, ok := fixture.Effective[string(s.ID)]; !ok {
			missing = append(missing, string(s.ID)+" (effective)")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%d surfaces missing from the e2e fixture "+
			"(regenerate assets/ui/tests/e2e/fixtures/ui-surfaces.json from the registry):\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(fixture.Surfaces) != len(surface.Registry()) {
		t.Errorf("fixture has %d surfaces, registry has %d — it carries entries that no longer exist",
			len(fixture.Surfaces), len(surface.Registry()))
	}
}
