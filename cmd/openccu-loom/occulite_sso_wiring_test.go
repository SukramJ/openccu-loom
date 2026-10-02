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
	"strings"
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
	// Named as occulited names it, /run/occulite/addon-tokens/<id>.api: the
	// gate scope the daemon accepts box tokens for derives from this name.
	token := filepath.Join(dir, "openccu-loom.api")
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

// fakeBoxState serves GET /api/auth/v1/state with a fixed answer per bearer
// session id, and 500 for the id named broken; any other id reads as not
// authenticated, as the open route answers an unknown credential.
func fakeBoxState(t *testing.T, answers map[string]map[string]any, broken string) *httptest.Server {
	t.Helper()
	box := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/v1/state" {
			http.NotFound(w, r)
			return
		}
		sid := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if sid == broken {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if a, ok := answers[sid]; ok {
			_ = json.NewEncoder(w).Encode(a)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": false})
	}))
	t.Cleanup(box.Close)
	return box
}

// Session ids for the fake box: 26 characters of A-Z2-7 each.
const (
	sidConfirmed = "AAAAAAAAAAAAAAAAAAAAAAAAAA"
	sidAuthOff   = "BBBBBBBBBBBBBBBBBBBBBBBBBB"
	sidPublic    = "CCCCCCCCCCCCCCCCCCCCCCCCCC"
	sidBroken    = "DDDDDDDDDDDDDDDDDDDDDDDDDD"
	sidUnknown   = "EEEEEEEEEEEEEEEEEEEEEEEEEE"
	sidDemoted   = "FFFFFFFFFFFFFFFFFFFFFFFFFF"
)

func occuliteBoxAnswers() map[string]map[string]any {
	return map[string]map[string]any{
		sidConfirmed: {"authenticated": true, "user": "alice", "role": "admin"},
		// An auth-off box confirms any bearer as admin; a public-mode box
		// answers an unknown session with its kiosk principal.
		sidAuthOff: {"authenticated": true, "user": "admin", "role": "admin", "auth_off": true},
		sidPublic:  {"authenticated": true, "user": "public", "role": "user", "public": true},
		sidDemoted: {"authenticated": true, "user": "bob", "role": "user"},
	}
}

// TestWireREST_OcculiteSSORefusesNonSessionAnswers drives the production
// boot path against a box that answers auth-off and public-principal: the
// verifier adapter must carry both markers through so the resolver refuses
// them, while a genuine session on the same box still resolves.
func TestWireREST_OcculiteSSORefusesNonSessionAnswers(t *testing.T) {
	liteAddonStampForTest(t)
	box := fakeBoxState(t, occuliteBoxAnswers(), "")
	t.Setenv(liteAddonBaseURLEnv, box.URL)

	passthrough := func(next http.Handler) http.Handler { return next }
	w := wireREST(context.Background(), restWiringDeps{
		cfg:            config.Default(),
		logger:         slog.New(slog.DiscardHandler),
		authMw:         auth.NewMiddleware(auth.NewMemoryUserStore(), auth.NewMemoryTokenStore(nil)),
		restResolve:    passthrough,
		sessionResolve: passthrough,
	})
	for _, tc := range []struct {
		sid  string
		want bool
	}{
		{sidConfirmed, true},
		{sidAuthOff, false},
		{sidPublic, false},
	} {
		var resolved bool
		h := w.authResolve(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, resolved = auth.IdentityFrom(r.Context())
		}))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
		req.Header.Set(auth.OcculiteSessionHeader, tc.sid)
		h.ServeHTTP(httptest.NewRecorder(), req)
		if resolved != tc.want {
			t.Errorf("session %s…: identity resolved = %v, want %v", tc.sid[:4], resolved, tc.want)
		}
	}
}

// TestOcculiteRevalidator pins the socket re-verification's policy: true
// only for a confirmed session, false for a definite refusal (logout,
// auth-off, public principal), and true on a verification error so a
// loopback hiccup never ends every box-shell socket.
func TestOcculiteRevalidator(t *testing.T) {
	liteAddonStampForTest(t)
	box := fakeBoxState(t, occuliteBoxAnswers(), sidBroken)
	t.Setenv(liteAddonBaseURLEnv, box.URL)

	logger := slog.New(slog.DiscardHandler)
	rv := occuliteRevalidator(buildOcculiteSSOTrust(config.Default(), logger), logger)
	if rv == nil {
		t.Fatal("revalidator is nil on a lite-add-on host with SSO enabled")
	}
	for _, tc := range []struct {
		name, sid string
		role      auth.Role
		want      bool
	}{
		{"confirmed", sidConfirmed, auth.RoleAdmin, true},
		{"logged out", sidUnknown, auth.RoleAdmin, false},
		{"auth off", sidAuthOff, auth.RoleAdmin, false},
		{"public", sidPublic, auth.RoleOperator, false},
		{"verify error", sidBroken, auth.RoleAdmin, true},
		// The box now maps the session to operator ("user"); an admin
		// socket must close, an operator socket keeps going.
		{"demoted", sidDemoted, auth.RoleAdmin, false},
		{"demoted, socket already operator", sidDemoted, auth.RoleOperator, true},
		// A promotion is a change too: the socket reconnects to pick it up.
		{"promoted", sidConfirmed, auth.RoleOperator, false},
	} {
		if got := rv(context.Background(), tc.sid, tc.role); got != tc.want {
			t.Errorf("%s: revalidate = %v, want %v", tc.name, got, tc.want)
		}
	}

	// Inert trust wires no revalidator: sockets stay unchecked.
	if occuliteRevalidator(auth.OcculiteSSOTrust{Enabled: true}, logger) != nil {
		t.Error("revalidator built without a verifier")
	}
	if occuliteRevalidator(auth.OcculiteSSOTrust{}, logger) != nil {
		t.Error("revalidator built for a disabled trust")
	}
}
