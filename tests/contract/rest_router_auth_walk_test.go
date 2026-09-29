// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
)

// publicAPIRoutes is the complete, justified set of /api/v1 routes an
// UNAUTHENTICATED caller may reach. Keys are "METHOD /path" as chi mounts
// them. Every other mounted route must answer 401 or 403 to an anonymous
// request — a new route is protected by default, and making one public
// means adding it here with a reason.
var publicAPIRoutes = map[string]string{
	"POST /api/v1/auth/login":        "the login itself — credentials are the body, not a header",
	"POST /api/v1/auth/logout":       "logout clears the session cookie; an anonymous logout is a no-op",
	"GET /api/v1/auth/oidc/start":    "OIDC entry point — the browser arrives unauthenticated by definition",
	"GET /api/v1/auth/oidc/callback": "OIDC return leg — the code exchange is the authentication",
	"GET /api/v1/health":             "liveness probe for load balancers and the no-JS anchor",
	// Client token pairing (ADR 0076): the asking client has no
	// credential yet by definition. The POST carries the protocol's own
	// limits plus the login rate limit; poll and withdraw authenticate
	// with the request's poll secret, and nothing becomes a credential
	// without the code an administrator types.
	"POST /api/v1/pairing":        "pairing ask — the client has no credential yet; approval is the typed code",
	"GET /api/v1/pairing/{id}":    "pairing poll — authenticated by the request's own poll secret",
	"DELETE /api/v1/pairing/{id}": "pairing withdraw — authenticated by the request's own poll secret",
	"GET /api/v1/info":            "version + API-version banner the SPA reads before login",
	// The first-run wizard runs before any user exists, so its routes are
	// mounted outside the auth group and gate themselves: POST /setup
	// hard-gates on the first-run probe and the FirstRunAllowed switch
	// (internal/north/rest/handlers/setup.go), and the pairing routes are
	// only meaningful during that same window.
	"GET /api/v1/setup/status":          "first-run probe the SPA reads before login",
	"POST /api/v1/setup":                "first-run wizard — self-gated on the first-run probe",
	"POST /api/v1/setup/probe":          "first-run wizard — CCU reachability probe, self-gated",
	"POST /api/v1/setup/pairing":        "first-run wizard — openccu-lite pairing, self-gated",
	"GET /api/v1/setup/pairing/{id}":    "first-run wizard — pairing poll, self-gated",
	"DELETE /api/v1/setup/pairing/{id}": "first-run wizard — pairing abort, self-gated",
}

// fillChiPattern turns a chi route pattern into a concrete request path:
// "{param}" and "{param:regex}" become "1" (matches the numeric patterns in
// use and is a harmless literal elsewhere), a trailing "/*" becomes "/x".
func fillChiPattern(route string) string {
	re := regexp.MustCompile(`\{[^}]+\}`)
	path := re.ReplaceAllString(route, "1")
	path = strings.ReplaceAll(path, "/*", "/x")
	return path
}

// TestEveryAPIRouteRequiresAuthOrIsListedPublic walks the fully-wired
// router with the PRODUCTION auth middleware chain (auth.Middleware's
// Resolve/Require/RequireRole, exactly as cmd/openccu-loom wires them) and
// fires one anonymous request at every mounted /api/v1 route. A route that
// answers anything but 401/403 is publicly reachable and must be in
// publicAPIRoutes; a listed route that has become protected must leave the
// list. This is the behavioural twin of an occulited-style route→scope
// table: the public set is pinned, so no route slips into production
// unauthenticated because a handler was mounted outside the guarded group.
func TestEveryAPIRouteRequiresAuthOrIsListedPublic(t *testing.T) {
	deps := fullyWiredRouterDeps()
	mw := auth.NewMiddleware(auth.NewMemoryUserStore(), auth.NewMemoryTokenStore(map[string]auth.Identity{}))
	deps.AuthResolve = mw.Resolve
	deps.AuthRequire = mw.Require
	deps.RequireOperator = func(next http.Handler) http.Handler {
		return mw.RequireRole(auth.RoleOperator, next)
	}
	deps.RequireAdmin = func(next http.Handler) http.Handler {
		return mw.RequireRole(auth.RoleAdmin, next)
	}
	router := rest.NewRouter(deps)

	type probe struct{ method, route string }
	var probes []probe
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/api/v1") {
			probes = append(probes, probe{method, route})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	if len(probes) < 100 {
		t.Fatalf("walked only %d /api/v1 routes — the router shrank or the walk broke", len(probes))
	}

	var leaks, stale []string
	seenPublic := map[string]bool{}
	for _, p := range probes {
		req := httptest.NewRequest(p.method, fillChiPattern(p.route), http.NoBody)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		key := p.method + " " + p.route
		protected := rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden
		if _, listed := publicAPIRoutes[key]; listed {
			seenPublic[key] = true
			if protected {
				stale = append(stale, key+" is listed public but answered "+http.StatusText(rec.Code))
			}
			continue
		}
		if !protected {
			leaks = append(leaks, key+" answered "+rec.Result().Status+" to an anonymous request")
		}
	}
	for key := range publicAPIRoutes {
		if !seenPublic[key] {
			stale = append(stale, key+" is listed public but not mounted")
		}
	}

	if len(leaks)+len(stale) > 0 {
		sort.Strings(leaks)
		sort.Strings(stale)
		t.Fatalf("public surface drifted (%d unlisted reachable, %d stale list entries):\n%s",
			len(leaks), len(stale), strings.Join(append(leaks, stale...), "\n"))
	}
}
