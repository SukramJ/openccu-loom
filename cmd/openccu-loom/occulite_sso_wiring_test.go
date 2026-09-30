// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// liteAddonStampForTest fakes the two host facts the lite-add-on stamp
// keys on: the VARIANT=lite marker in the version manifest and the minted
// token file, both via the same env overrides the onboarding honours.
func liteAddonStampForTest(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	version := filepath.Join(dir, "VERSION")
	if err := os.WriteFile(version, []byte("VERSION=1.0.0\nVARIANT=lite\n"), 0o600); err != nil {
		t.Fatalf("write version: %v", err)
	}
	token := filepath.Join(dir, "addon.api")
	if err := os.WriteFile(token, []byte("olt_test\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	t.Setenv(liteAddonVersionPathEnv, version)
	t.Setenv(liteAddonTokenPathEnv, token)
}

// TestWireREST_OcculiteSSOWiredThroughAuthResolve pins the ADR 0079 wiring
// through the production boot path: on a lite-add-on host with the default
// (nil → stamp) config, a request carrying a well-formed box session id
// resolves — via the live verification against the (faked) box — to a
// SchemeOcculite identity with the mapped role, and a malformed value stays
// unauthenticated without the box ever being asked.
func TestWireREST_OcculiteSSOWiredThroughAuthResolve(t *testing.T) {
	liteAddonStampForTest(t)

	const sid = "ABCDEFGHIJKLMNOPQRST234567" // 26 chars, A-Z2-7
	var stateCalls int
	box := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/v1/state" {
			http.NotFound(w, r)
			return
		}
		stateCalls++
		if r.Header.Get("Authorization") != "Bearer "+sid {
			_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": false})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authenticated": true, "user": "boxadmin", "role": "admin",
		})
	}))
	defer box.Close()
	t.Setenv(liteAddonBaseURLEnv, box.URL)

	cfg := config.Default() // occulite_sso.enabled stays nil: the stamp decides

	passthrough := func(next http.Handler) http.Handler { return next }
	w := wireREST(context.Background(), restWiringDeps{
		cfg:            cfg,
		logger:         slog.New(slog.DiscardHandler),
		authMw:         auth.NewMiddleware(auth.NewMemoryUserStore(), auth.NewMemoryTokenStore(nil)),
		restResolve:    passthrough,
		sessionResolve: passthrough,
	})
	if w.authResolve == nil {
		t.Fatal("wireREST: authResolve is nil")
	}

	var got auth.Identity
	var resolved bool
	h := w.authResolve(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, resolved = auth.IdentityFrom(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	req.Header.Set(auth.OcculiteSessionHeader, sid)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !resolved {
		t.Fatal("no identity resolved: the box-shell SSO passthrough is not wired on this boot path")
	}
	if got.Scheme != auth.SchemeOcculite {
		t.Fatalf("identity scheme = %q, want %q", got.Scheme, auth.SchemeOcculite)
	}
	if got.Role != auth.RoleAdmin || got.Subject != "occulite:boxadmin" {
		t.Fatalf("identity = %+v, want admin occulite:boxadmin", got)
	}
	if stateCalls != 1 {
		t.Fatalf("state calls = %d, want exactly 1", stateCalls)
	}

	// The ten-character legacy alias must not even reach the box.
	resolved = false
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	req2.Header.Set(auth.OcculiteSessionHeader, "ABCDE23456")
	h.ServeHTTP(httptest.NewRecorder(), req2)
	if resolved {
		t.Fatal("legacy alias resolved an identity; it must defer")
	}
	if stateCalls != 1 {
		t.Fatalf("state calls after alias = %d, want still 1", stateCalls)
	}
}
