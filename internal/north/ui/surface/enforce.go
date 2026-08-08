// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package surface

import (
	"strings"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// In the embedded profile a surface entry carries more than navigation:
// a surface that is hidden there also refuses its writes for the Home
// Assistant Ingress passthrough identity, and one the operator shows
// accepts them again.
//
// The scope is deliberately narrow, and each boundary answers a way this
// could otherwise become a hole:
//
//   - Only the passthrough identity (auth.SchemeIngress). An API token
//     or a Loom account carries the rights it was granted; a navigation
//     switch must never widen or narrow those, or an operator could
//     grant a machine client access by un-hiding a sidebar entry.
//   - Only the embedded profile. Standalone has no passthrough identity
//     to scope, so there the profile is purely navigational.
//   - Only surfaces with a declared route set below. Hiding a view with
//     no write path gates nothing.
//   - Reads are never refused. The HA panel needs them, and hiding a
//     view was never a statement about reading its data.

// WriteRoute is one write endpoint a surface owns, matched by method
// and path pattern. Patterns are written against the API path WITHOUT
// the /api/v1 prefix; a `{}` segment matches exactly one path segment.
type WriteRoute struct {
	// Method is the uppercase HTTP method.
	Method string
	// Pattern is the slash-separated path pattern, e.g.
	// "/devices/{addr}/paramsets/{key}".
	Pattern string
	// ParamIndex, when >= 0, restricts the rule to requests whose path
	// segment at that index is one of ParamValues. It exists because the
	// generic config-section endpoint carries the section name in the
	// path: PUT /config/sections/north.rest.auth.oidc must follow the
	// OIDC surface, while the same endpoint for north.mqtt must not.
	ParamIndex int
	// ParamValues lists the accepted values for ParamIndex.
	ParamValues []string
}

// route builds a rule with no parameter restriction.
func route(method, pattern string) WriteRoute {
	return WriteRoute{Method: method, Pattern: pattern, ParamIndex: -1}
}

// section builds a rule for the generic config-section endpoints,
// restricted to the named sections.
func section(method string, sections ...string) WriteRoute {
	return WriteRoute{
		Method: method, Pattern: "/config/sections/{section}",
		ParamIndex: 2, ParamValues: sections,
	}
}

// writeRoutes maps each write-gated surface to the endpoints it owns.
//
// Every pattern here must exist in the router — TestSurfaceWriteRoutesExist
// fails on a rule that names a route no longer served, because a stale
// rule is a refusal that silently stopped happening.
var writeRoutes = map[ID][]WriteRoute{
	"device.configure.device-config": {
		route("PUT", "/devices/{addr}/paramsets/{key}"),
		route("POST", "/devices/{addr}/channels/{no}/config/import"),
		route("POST", "/devices/{addr}/config/restore"),
	},
	"device.configure.links": {
		route("POST", "/devices/{addr}/links"),
		route("PATCH", "/devices/{addr}/links"),
		route("DELETE", "/devices/{addr}/links"),
		route("POST", "/devices/{addr}/links/test"),
		route("PUT", "/devices/{addr}/link-ps/{peer}"),
	},
	"device.configure.schedule": {
		route("PUT", "/devices/{addr}/schedule"),
		route("POST", "/devices/{addr}/schedule/active-profile"),
		route("POST", "/devices/{addr}/schedules/copy"),
		route("PUT", "/devices/{addr}/channels/{no}/schedule"),
		route("POST", "/devices/{addr}/channels/{no}/schedule/active-profile"),
		route("POST", "/devices/{addr}/channels/{no}/week_profile/copy"),
		route("PUT", "/devices/{addr}/channels/{no}/week_profile/channel-locks/{key}"),
	},
	"settings.ccus": {
		route("POST", "/centrals"),
		route("PUT", "/centrals/{name}"),
		route("DELETE", "/centrals/{name}"),
		route("POST", "/centrals/discovered/{serial}/ignore"),
		route("DELETE", "/centrals/discovered/{serial}/ignore"),
	},
	"settings.users": {
		route("POST", "/users"),
		route("PATCH", "/users/{subject}"),
		route("DELETE", "/users/{subject}"),
	},
	"settings.tokens": {
		route("POST", "/auth/tokens"),
		route("DELETE", "/auth/tokens/{id}"),
		route("POST", "/auth/tokens/v2"),
		route("DELETE", "/auth/tokens/v2/{fingerprint}"),
	},
	"settings.oidc": {
		section("PUT", "north.rest.auth.oidc"),
		section("DELETE", "north.rest.auth.oidc"),
	},
	"settings.ccu_auth": {
		section("PUT", "north.rest.auth.ccu"),
		section("DELETE", "north.rest.auth.ccu"),
	},
	"settings.groups": {
		route("POST", "/rooms"),
		route("PATCH", "/rooms/{name}"),
		route("DELETE", "/rooms/{name}"),
		route("POST", "/functions"),
		route("PATCH", "/functions/{name}"),
		route("DELETE", "/functions/{name}"),
		route("POST", "/areas"),
		route("PUT", "/areas/{id}"),
		route("DELETE", "/areas/{id}"),
		route("PUT", "/areas/{id}/rooms"),
	},
	"settings.matter": {
		section("PUT", "north.matter"),
		section("DELETE", "north.matter"),
	},
	"nav.matter": {
		route("PUT", "/matter/exposable"),
		route("POST", "/matter/exposable/bulk"),
		route("POST", "/matter/commissioning/window"),
		route("POST", "/matter/commissioning/window/close"),
		route("POST", "/matter/share"),
		route("DELETE", "/matter/fabrics/{id}"),
	},
}

// WriteRoutes returns the endpoints owned by id.
func WriteRoutes(id ID) []WriteRoute {
	return writeRoutes[id]
}

// AllWriteRoutes returns the full mapping, for the guards that compare
// it against the router.
func AllWriteRoutes() map[ID][]WriteRoute {
	out := make(map[ID][]WriteRoute, len(writeRoutes))
	for id, rules := range writeRoutes {
		out[id] = append([]WriteRoute(nil), rules...)
	}
	return out
}

// Matches reports whether method+path selects this rule. path is the
// request path with the API prefix already stripped.
func (w WriteRoute) Matches(method, path string) bool {
	if !strings.EqualFold(method, w.Method) {
		return false
	}
	want := splitPath(w.Pattern)
	got := splitPath(path)
	if len(want) != len(got) {
		return false
	}
	for i, seg := range want {
		if strings.HasPrefix(seg, "{") {
			continue
		}
		if seg != got[i] {
			return false
		}
	}
	if w.ParamIndex >= 0 {
		if w.ParamIndex >= len(got) {
			return false
		}
		for _, v := range w.ParamValues {
			if got[w.ParamIndex] == v {
				return true
			}
		}
		return false
	}
	return true
}

func splitPath(p string) []string {
	return strings.Split(strings.Trim(p, "/"), "/")
}

// RefusedBy returns the surface whose hidden state refuses method+path,
// or "" when the request is not gated.
//
// It consults the resolution rather than the raw config so the answer
// always agrees with what the navigation shows — the two must never be
// derived separately, or a surface could be hidden while its writes stay
// open (or the reverse, an editor that renders and then fails to save).
func (r Resolution) RefusedBy(method, path string) ID {
	if r.Profile != config.ProfileEmbedded {
		return ""
	}
	for i := range registry {
		s := &registry[i]
		if !s.WriteGated || r.IsVisible(s.ID) {
			continue
		}
		for _, rule := range writeRoutes[s.ID] {
			if rule.Matches(method, path) {
				return s.ID
			}
		}
	}
	return ""
}
