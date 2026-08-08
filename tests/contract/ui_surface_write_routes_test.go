// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/ui/surface"
)

// In the embedded profile a hidden surface also refuses its writes for
// the Home Assistant passthrough identity. That refusal is expressed as
// a table of (method, path pattern) rules next to the registry — and a
// table of route patterns is exactly the kind of declaration that keeps
// looking correct after the routes it names have moved.
//
// This is the declared-vs-served shape CLAUDE.md names: a rule for a
// route the router no longer serves is a refusal that silently stopped
// happening, and nothing else in the build would notice.

var routeDeclRe = regexp.MustCompile(`\.(Put|Post|Delete|Patch)\("([^"]+)"`)

// routerWritePatterns collects every write route the REST router serves,
// as "METHOD /pattern" with chi's {param} placeholders normalised to {}.
func routerWritePatterns(t *testing.T) map[string]bool {
	t.Helper()
	path := filepath.Join("..", "..", "internal", "north", "rest", "router.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	out := map[string]bool{}
	for _, m := range routeDeclRe.FindAllStringSubmatch(string(raw), -1) {
		out[strings.ToUpper(m[1])+" "+normalizePattern(m[2])] = true
	}
	return out
}

// normalizePattern replaces every {name} segment with {} so a rename of
// a path parameter does not read as a moved route.
func normalizePattern(p string) string {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range segs {
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			segs[i] = "{}"
		}
	}
	return "/" + strings.Join(segs, "/")
}

// TestSurfaceWriteRoutesExist fails when a declared rule names a route
// the router does not serve.
func TestSurfaceWriteRoutesExist(t *testing.T) {
	t.Parallel()

	served := routerWritePatterns(t)
	if len(served) < 50 {
		t.Fatalf("only %d write routes parsed from router.go — the parser broke, not the router", len(served))
	}

	var stale []string
	for id, rules := range surface.AllWriteRoutes() {
		for _, r := range rules {
			key := strings.ToUpper(r.Method) + " " + normalizePattern(r.Pattern)
			if !served[key] {
				stale = append(stale, string(id)+": "+key)
			}
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("%d surface write rules name routes the router no longer serves "+
			"(each one is a refusal that silently stopped happening):\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// TestHAOwnedWriteSurfacesAreGated is the other direction, and the one
// that actually protects the boundary: a surface the embedded profile
// hides *because Home Assistant owns it* has to carry write rules, or
// the hidden UI is bypassable by calling the API the panel's iframe can
// reach anyway.
//
// The exceptions are surfaces with no write path at all — hiding them
// gates nothing because there is nothing to gate.
func TestHAOwnedWriteSurfacesAreGated(t *testing.T) {
	t.Parallel()

	// Read-only or preference-only surfaces: nothing an operator can
	// write through them, so they are navigation and nothing more.
	noWritePath := map[surface.ID]string{
		"nav.overview":                   "device tiles write through the data-point endpoint, which the Overview tab of the device detail owns",
		"nav.favorites":                  "favorites are a per-user preference, not a daemon-side write",
		"nav.energy":                     "read-only aggregation of recorded history",
		"nav.diagrams":                   "read-only aggregation of recorded history",
		"device.configure":               "a container: its sub-tabs carry the routes and inherit its hidden state",
		"device.configure.channels":      "a selector; its writes belong to the device-config sub-tab",
		"settings.matter":                "covered by the config-section rule on north.matter",
		"settings.groups":                "covered by its own room/function/area rules",
		"settings.ccus":                  "covered by its own centrals rules",
		"settings.users":                 "covered by its own user rules",
		"settings.tokens":                "covered by its own token rules",
		"settings.oidc":                  "covered by the config-section rule on north.rest.auth.oidc",
		"settings.ccu_auth":              "covered by the config-section rule on north.rest.auth.ccu",
		"nav.matter":                     "covered by its own matter rules",
		"device.configure.device-config": "covered by its own paramset rules",
		"device.configure.links":         "covered by its own link rules",
		"device.configure.schedule":      "covered by its own schedule rules",
	}

	var ungated []string
	for _, s := range surface.Registry() {
		if !s.HAOwns {
			continue
		}
		if _, known := noWritePath[s.ID]; known {
			continue
		}
		if len(surface.WriteRoutes(s.ID)) == 0 {
			ungated = append(ungated, string(s.ID))
		}
	}
	if len(ungated) > 0 {
		sort.Strings(ungated)
		t.Fatalf("%d surfaces are hidden in embedded because Home Assistant owns them, "+
			"but declare no write rules — hiding them is then cosmetic:\n  %s\n"+
			"Either add the routes they own, or record why they have no write path.",
			len(ungated), strings.Join(ungated, "\n  "))
	}
}
