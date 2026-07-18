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

	handler := mountMCP(cfg, d, fallthroughRouter, slog.New(slog.DiscardHandler))

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
