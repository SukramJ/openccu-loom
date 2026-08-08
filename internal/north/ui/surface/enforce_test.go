// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package surface

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestRefusedByFollowsTheProfile is the core of the write boundary: the
// same request is refused or allowed depending on one profile entry.
func TestRefusedByFollowsTheProfile(t *testing.T) {
	t.Parallel()

	const (
		method = "PUT"
		path   = "/devices/ABC123/paramsets/MASTER"
	)

	// Embedded default: the Configure tab is hidden, so Home Assistant
	// may not write paramsets.
	hidden := Resolve(uiWith(true, config.ProfileEmbedded, nil))
	if got := hidden.RefusedBy(method, path); got != "device.configure.device-config" {
		t.Errorf("RefusedBy = %q, want device.configure.device-config", got)
	}

	// The operator shows it again — and the write comes back with it.
	shown := Resolve(uiWith(true, config.ProfileEmbedded, map[string]config.SurfaceState{
		"device.configure":               config.SurfaceVisible,
		"device.configure.device-config": config.SurfaceVisible,
	}))
	if got := shown.RefusedBy(method, path); got != "" {
		t.Errorf("RefusedBy = %q after showing the surface, want no refusal", got)
	}
}

// TestRefusedByIsEmbeddedOnly pins that standalone never refuses. There
// is no passthrough identity to scope there, so the profile is purely
// navigational — hiding a view must not silently disable an API.
func TestRefusedByIsEmbeddedOnly(t *testing.T) {
	t.Parallel()

	res := Resolve(uiWith(false, config.ProfileStandalone, map[string]config.SurfaceState{
		"device.configure": config.SurfaceHidden,
		"settings.users":   config.SurfaceHidden,
	}))
	for _, tc := range []struct{ method, path string }{
		{"PUT", "/devices/ABC123/paramsets/MASTER"},
		{"POST", "/users"},
	} {
		if got := res.RefusedBy(tc.method, tc.path); got != "" {
			t.Errorf("%s %s refused by %q in standalone, want no refusal", tc.method, tc.path, got)
		}
	}
}

// TestHidingTheParentGatesTheChildren pins the containment rule: hiding
// the Configure tab has to take its sub-tabs — and therefore their
// writes — with it, even though the operator only touched one entry.
func TestHidingTheParentGatesTheChildren(t *testing.T) {
	t.Parallel()

	res := Resolve(uiWith(true, config.ProfileEmbedded, map[string]config.SurfaceState{
		// Sub-tabs explicitly shown, parent left at its hidden default.
		"device.configure.links":    config.SurfaceVisible,
		"device.configure.schedule": config.SurfaceVisible,
	}))
	if res.IsVisible("device.configure.links") {
		t.Error("a sub-tab is visible while its parent is hidden")
	}
	if got := res.RefusedBy("POST", "/devices/ABC123/links"); got != "device.configure.links" {
		t.Errorf("RefusedBy = %q, want the link sub-tab to still refuse", got)
	}
}

// TestRefusedByReadsAreNeverGated pins that only writes are scoped. The
// HA panel reads the same devices it edits, and hiding a view was never
// a statement about reading its data.
func TestRefusedByReadsAreNeverGated(t *testing.T) {
	t.Parallel()

	res := Resolve(uiWith(true, config.ProfileEmbedded, nil))
	for _, path := range []string{
		"/devices/ABC123/paramsets/MASTER",
		"/users",
		"/centrals",
	} {
		if got := res.RefusedBy("GET", path); got != "" {
			t.Errorf("GET %s refused by %q, want reads to pass", path, got)
		}
	}
}

// TestSectionRouteMatchesOnlyItsOwnSection guards the generic
// config-section endpoint. PUT /config/sections/{section} can reach
// every section, so the rule has to discriminate on the section name —
// otherwise hiding the OIDC tab would also freeze the MQTT settings the
// embedded profile deliberately keeps editable.
func TestSectionRouteMatchesOnlyItsOwnSection(t *testing.T) {
	t.Parallel()

	res := Resolve(uiWith(true, config.ProfileEmbedded, nil))

	if got := res.RefusedBy("PUT", "/config/sections/north.rest.auth.oidc"); got != "settings.oidc" {
		t.Errorf("RefusedBy(oidc section) = %q, want settings.oidc", got)
	}
	if got := res.RefusedBy("PUT", "/config/sections/north.mqtt"); got != "" {
		t.Errorf("RefusedBy(mqtt section) = %q, want no refusal — MQTT stays editable in embedded", got)
	}
}

// TestWriteRouteMatching covers the pattern matcher itself.
func TestWriteRouteMatching(t *testing.T) {
	t.Parallel()

	r := route("PUT", "/devices/{addr}/paramsets/{key}")
	cases := []struct {
		method, path string
		want         bool
	}{
		{"PUT", "/devices/ABC/paramsets/MASTER", true},
		{"put", "/devices/ABC/paramsets/MASTER", true},
		{"POST", "/devices/ABC/paramsets/MASTER", false},
		{"PUT", "/devices/ABC/paramsets", false},
		{"PUT", "/devices/ABC/paramsets/MASTER/extra", false},
		{"PUT", "/devices/ABC/link-ps/MASTER", false},
	}
	for _, tc := range cases {
		if got := r.Matches(tc.method, tc.path); got != tc.want {
			t.Errorf("Matches(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

// TestEveryWriteGatedSurfaceHasRoutes closes the gap between the flag
// and the rules. A surface flagged WriteGated without a route set
// promises an enforcement that never happens — the editor would show the
// badge and the API would keep accepting the write.
func TestEveryWriteGatedSurfaceHasRoutes(t *testing.T) {
	t.Parallel()

	for _, s := range Registry() {
		routes := WriteRoutes(s.ID)
		// The parent of a gated sub-tree owns no routes of its own: its
		// children carry them, and hiding it hides them by containment.
		hasGatedChildren := false
		for _, c := range Registry() {
			if c.Parent == s.ID && c.WriteGated {
				hasGatedChildren = true
				break
			}
		}
		if s.WriteGated && len(routes) == 0 && !hasGatedChildren {
			t.Errorf("%s is WriteGated but owns no write routes — the badge would promise an enforcement that never runs", s.ID)
		}
		if !s.WriteGated && len(routes) > 0 {
			t.Errorf("%s owns write routes but is not WriteGated — the rules would never be consulted", s.ID)
		}
	}
}
