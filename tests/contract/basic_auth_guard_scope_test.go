// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
)

// TestBasicAuthGuardDoesNotChargeTheSPAMount pins where the per-IP
// Basic-credential throttle is mounted.
//
// The throttle charges one token per request that carries an HTTP Basic header
// the auth chain could not resolve, out of the same small per-IP bucket
// POST /auth/login draws from. Sharing that bucket across the API is
// deliberate — it is one credential space, and two buckets would hand a
// guessing sweep twice the attempts.
//
// Mounting it on the whole mux was not. Above /api/v1 sit the SPA, /app/*,
// /about, /health and the UI bootstrap, and one page load is dozens of asset
// requests. A browser that once answered a Basic challenge and still holds a
// stale credential would charge the bucket once per asset and lock its own
// source out of logging in before the page finished rendering — no attacker
// involved, just a cached header.
//
// The bucket's burst is 5, so a handful of asset requests is already enough to
// tell the two mountings apart.
func TestBasicAuthGuardDoesNotChargeTheSPAMount(t *testing.T) {
	deps := fullyWiredRouterDeps()
	deps.LoginRateLimit = middleware.NewLoginRateLimiter()
	// A resolve step that resolves nothing: a Basic header then arrives at the
	// guard as "present, unverified", which is exactly the shape that charges.
	// Without a non-nil AuthResolve the guard is not mounted at all and this
	// test would pass whatever the router does — the first version of it did.
	deps.AuthResolve = func(next http.Handler) http.Handler { return next }
	deps.AuthRequire = func(next http.Handler) http.Handler { return next }
	router := rest.NewRouter(deps)

	const from = "203.0.113.7:5555"

	// A page load's worth of non-API requests, each carrying a stale Basic
	// credential the chain cannot resolve.
	for range 12 {
		req := httptest.NewRequest(http.MethodGet, "/app/index.html", http.NoBody)
		req.RemoteAddr = from
		req.SetBasicAuth("stale", "credential")
		router.ServeHTTP(httptest.NewRecorder(), req)
	}

	// The login route must still have budget: nothing an attacker did has
	// happened yet.
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", http.NoBody)
	login.RemoteAddr = from
	login.SetBasicAuth("stale", "credential")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, login)

	if rec.Code == http.StatusTooManyRequests {
		t.Errorf("twelve SPA asset requests carrying a stale Basic header exhausted the\n"+
			"per-IP login budget: POST /auth/login answered %d. The throttle is charging\n"+
			"outside /api/v1, so a browser locks its own source out of login by replaying\n"+
			"a credential it cached long ago.", rec.Code)
	}
}
