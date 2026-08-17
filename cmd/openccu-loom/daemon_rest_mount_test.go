// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
)

// TestMountMCPResolvesCredentialsBeforeRequire pins the fix that wraps
// d.restResolve around d.authMw.Require in mountMCP. Require only checks the
// Identity a previous middleware attached to the request context; it never
// resolves credentials itself. Since the /mcp mount sits outside the REST
// router's own middleware stack, wiring Require alone (the pre-fix state)
// rejected every request — valid Bearer token or not — with 401. Resolve
// must run first so a valid token actually reaches Require as an attached
// Identity.
func TestMountMCPResolvesCredentialsBeforeRequire(t *testing.T) {
	t.Parallel()

	tokens := auth.NewMemoryTokenStore(map[string]auth.Identity{
		"good": {Subject: "test-agent", Role: auth.RoleViewer},
	})
	authMw := auth.NewMiddleware(nil, tokens)

	d := restMountDeps{
		// central.NewRegistry() satisfies both mcp.CentralLister and
		// mcp.HubResolver with an empty, but non-nil, registry — mountMCP
		// wires the same *central.Registry into both Deps fields.
		reg:         central.NewRegistry(),
		authMw:      authMw,
		restResolve: authMw.Resolve,
	}

	cfg := config.Default()
	cfg.North.MCP.Enabled = true

	fallthroughCalled := false
	fallthroughRouter := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallthroughCalled = true
		w.WriteHeader(http.StatusTeapot)
	})

	handler := mountMCP(cfg, d, fallthroughRouter, nil, slog.New(slog.DiscardHandler))

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run("valid bearer token reaches the MCP handler ("+method+")", func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(method, "/mcp", http.NoBody)
			req.Header.Set("Authorization", "Bearer good")
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			// The MCP handler is free to reject a non-protocol request body
			// (e.g. 400/405/415 for a missing Accept/Content-Type header) —
			// what pins the fix is that auth itself does not reject it.
			if rr.Code == http.StatusUnauthorized {
				t.Fatalf("valid bearer token got 401 %s, want auth to pass through to the MCP handler: body=%s",
					method, rr.Body.String())
			}
		})
	}

	t.Run("missing credentials are rejected with 401", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("missing credentials: got status %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})

	t.Run("non-mcp path falls through to the REST router", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusTeapot {
			t.Fatalf("fallthrough: got status %d, want %d", rr.Code, http.StatusTeapot)
		}
		if !fallthroughCalled {
			t.Fatal("fallthrough router was not invoked for a non-/mcp path")
		}
	})
}

// TestMountMCPSurvivesAPersistedUnmountablePath pins that a mount path
// http.ServeMux cannot register does not take the daemon down.
//
// ServeMux reports a bad pattern by panicking during registration, and the
// path is persisted config: the panic would repeat on every start with the
// SPA that could correct the value never coming up. The mount is refused
// instead, and REST — which serves that SPA — keeps working.
func TestMountMCPSurvivesAPersistedUnmountablePath(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/", "/mcp{id}", "/mcp/"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			cfg := config.Default()
			cfg.North.MCP.Enabled = true
			cfg.North.MCP.Path = path

			rest := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
			})
			handler := mountMCP(cfg, restMountDeps{reg: central.NewRegistry()}, rest, nil, slog.New(slog.DiscardHandler))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusTeapot {
				t.Fatalf("REST fallthrough after a refused MCP mount: got %d, want %d", rr.Code, http.StatusTeapot)
			}
		})
	}
}

// TestMountMCPGatesWriteToolsOnOperatorRole pins that the MCP mount enforces
// a role, not merely the presence of an identity.
//
// The mcp package documents that it "holds no privilege path of its own" and
// that authorization comes from the middleware this mount wraps it in — so
// whatever this mount omits is omitted everywhere. With AllowWrites the tool
// set includes set_datapoint and the alarm arm/disarm/silence controls, which
// REST gates on operator (router.go mounts them With(op)). Require alone only
// proves that *someone* authenticated, so a viewer token reached the whole
// write surface: an authorization boundary crossed, not just a missing check.
func TestMountMCPGatesWriteToolsOnOperatorRole(t *testing.T) {
	t.Parallel()

	tokens := auth.NewMemoryTokenStore(map[string]auth.Identity{
		"viewer":   {Subject: "viewer-agent", Role: auth.RoleViewer},
		"operator": {Subject: "operator-agent", Role: auth.RoleOperator},
	})
	authMw := auth.NewMiddleware(nil, tokens)

	newHandler := func(allowWrites bool) http.Handler {
		d := restMountDeps{
			reg:         central.NewRegistry(),
			authMw:      authMw,
			restResolve: authMw.Resolve,
		}
		cfg := config.Default()
		cfg.North.MCP.Enabled = true
		cfg.North.MCP.AllowWrites = allowWrites
		return mountMCP(cfg, d, http.NotFoundHandler(), nil, slog.New(slog.DiscardHandler))
	}

	call := func(h http.Handler, token string) int {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	writable := newHandler(true)
	if got := call(writable, "viewer"); got != http.StatusForbidden {
		t.Errorf("viewer against the write-enabled MCP mount: got %d, want %d", got, http.StatusForbidden)
	}
	if got := call(writable, "operator"); got == http.StatusForbidden || got == http.StatusUnauthorized {
		t.Errorf("operator must reach the write-enabled MCP mount, got %d", got)
	}

	// Read-only is the default posture: a viewer legitimately reaches it.
	readOnly := newHandler(false)
	if got := call(readOnly, "viewer"); got == http.StatusForbidden || got == http.StatusUnauthorized {
		t.Errorf("viewer must reach the read-only MCP mount, got %d", got)
	}
}

// TestMountMCPThrottlesBasicAuthGuessingLikeREST is the regression guard for
// the MCP mount sitting outside rest.NewRouter's r.Use(...) chain and
// therefore skipping auth.GuardBasicAuth: a Resolve-protected route (like
// every REST route) charges one token per failed Basic attempt via
// Middleware.resolve, but only GuardBasicAuth turns a spent budget into a
// 429 — Resolve itself just falls through to Require, which answers 401
// forever no matter how many attempts a source has burned. Repeated failed
// Basic-auth attempts against /mcp must eventually see 429, exactly like
// they would against any other Resolve-protected REST path, and the budget
// is the SAME loginLimiter instance the REST router shares.
func TestMountMCPThrottlesBasicAuthGuessingLikeREST(t *testing.T) {
	t.Parallel()

	users := auth.NewMemoryUserStore() // no Put(): every Basic attempt fails verification
	tokens := auth.NewMemoryTokenStore(nil)
	authMw := auth.NewMiddleware(users, tokens)
	loginLimiter := middleware.NewLoginRateLimiter()
	authMw.BasicThrottle = loginLimiter // mirrors mountRESTServer's own wiring

	d := restMountDeps{
		reg:         central.NewRegistry(),
		authMw:      authMw,
		restResolve: authMw.Resolve,
	}
	cfg := config.Default()
	cfg.North.MCP.Enabled = true

	handler := mountMCP(cfg, d, http.NotFoundHandler(), loginLimiter, slog.New(slog.DiscardHandler))

	const sourceIP = "203.0.113.7:12345"

	// Exhaust the shared bucket directly rather than through repeated slow
	// (bcrypt-costed) HTTP round trips — this is also the more faithful
	// scenario for "SAME loginLimiter instance the REST router shares":
	// the budget could equally have been spent by Basic-auth guessing
	// against a REST route from the same source.
	depleteReq := httptest.NewRequest(http.MethodPost, "/auth/login", http.NoBody)
	depleteReq.RemoteAddr = sourceIP
	for {
		_, ok := loginLimiter.ReserveBasicAttempt(depleteReq)
		if !ok {
			break
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
	req.SetBasicAuth("nobody", "wrong-password")
	req.RemoteAddr = sourceIP
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("Basic-auth attempt against /mcp with an exhausted shared budget: got %d, want 429 — GuardBasicAuth is not wired on this mount", rr.Code)
	}
}

// TestMountMCPAppliesRateLimit is the regression guard for the MCP mount
// skipping middleware.RateLimit: a burst of requests from one source that
// would trip the REST router's rate limiter must trip it here too, or an
// attacker routes abusive volume through /mcp specifically to dodge the
// throttle every other endpoint enforces.
func TestMountMCPAppliesRateLimit(t *testing.T) {
	t.Parallel()

	tokens := auth.NewMemoryTokenStore(map[string]auth.Identity{
		"good": {Subject: "test-agent", Role: auth.RoleViewer},
	})
	authMw := auth.NewMiddleware(nil, tokens)

	d := restMountDeps{
		reg:         central.NewRegistry(),
		authMw:      authMw,
		restResolve: authMw.Resolve,
	}
	cfg := config.Default()
	cfg.North.MCP.Enabled = true
	cfg.North.REST.RateLimit.Enabled = true
	cfg.North.REST.RateLimit.RequestsPerSecond = 1
	cfg.North.REST.RateLimit.Burst = 1

	handler := mountMCP(cfg, d, http.NotFoundHandler(), nil, slog.New(slog.DiscardHandler))

	call := func() int {
		req := httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody)
		req.Header.Set("Authorization", "Bearer good")
		req.RemoteAddr = "203.0.113.9:12345"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr.Code
	}

	saw429 := false
	for i := 0; i < 5; i++ {
		if call() == http.StatusTooManyRequests {
			saw429 = true
			break
		}
	}
	if !saw429 {
		t.Error("a burst against /mcp with burst=1 never got 429 — middleware.RateLimit is not wired on this mount")
	}
}
