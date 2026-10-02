// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// Box API tokens for the fake box: "olt_" and 32 lower-case hex each.
const (
	tokAddon    = "olt_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tokFull     = "olt_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tokOther    = "olt_cccccccccccccccccccccccccccccccc"
	tokAPIOnly  = "olt_dddddddddddddddddddddddddddddddd"
	tokBroken   = "olt_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	tokUnknown  = "olt_ffffffffffffffffffffffffffffffff"
	tokRedmatic = "olt_1111111111111111111111111111111a"
)

// occuliteTokenAnswers are the box's auth-state answers for tokens, in the
// form the box documents: a "token:<name>" user, the stored scopes, no role.
func occuliteTokenAnswers() map[string]map[string]any {
	return map[string]map[string]any{
		tokAddon:    {"authenticated": true, "user": "token:homeassistant", "scopes": []string{"addon:openccu-loom"}},
		tokFull:     {"authenticated": true, "user": "token:ops", "scopes": []string{"*"}},
		tokOther:    {"authenticated": true, "user": "token:nodered", "scopes": []string{"addon:redmatic"}},
		tokAPIOnly:  {"authenticated": true, "user": "token:script", "scopes": []string{"system:write", "rpc:admin"}},
		tokRedmatic: {"authenticated": true, "user": "token:flows", "scopes": []string{"addon:redmatic"}},
	}
}

// resolveThrough runs one request carrying the gate's identity header (and,
// when bearer is set, the Authorization header the gate may pass on)
// through the wired resolver chain and reports the identity it produced.
func resolveThrough(resolve func(http.Handler) http.Handler, credential string, bearer bool) (auth.Identity, bool) {
	var got auth.Identity
	var resolved bool
	h := resolve(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, resolved = auth.IdentityFrom(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	req.Header.Set(auth.OcculiteSessionHeader, credential)
	if bearer {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got, resolved
}

// TestWireREST_OcculiteTokenWiredThroughAuthResolve pins the ADR 0080
// wiring through the production boot path, with the daemon's own bearer
// resolution in front as it runs in production: on a lite-add-on host a
// box token the gate accepted resolves — via live verification against the
// (faked) box — to a SchemeOcculiteToken identity, operator for the
// add-on's own scope and admin for Full access; a token for another add-on
// or for the APIs only stays unauthenticated. That holds whether or not
// the gate passes the Authorization header on: the daemon's bearer
// resolution misses on a box token and defers.
func TestWireREST_OcculiteTokenWiredThroughAuthResolve(t *testing.T) {
	liteAddonStampForTest(t)
	box := fakeBoxState(t, occuliteTokenAnswers(), "")
	t.Setenv(liteAddonBaseURLEnv, box.URL)

	authMw := auth.NewMiddleware(auth.NewMemoryUserStore(), auth.NewMemoryTokenStore(nil))
	passthrough := func(next http.Handler) http.Handler { return next }
	w := wireREST(context.Background(), restWiringDeps{
		cfg:            config.Default(), // occulite_sso.enabled stays nil: the stamp decides
		logger:         slog.New(slog.DiscardHandler),
		authMw:         authMw,
		restResolve:    authMw.Resolve,
		sessionResolve: passthrough,
	})

	for _, bearer := range []bool{false, true} {
		for _, tc := range []struct {
			name, tok string
			want      *auth.Identity
		}{
			{"addon scope", tokAddon, &auth.Identity{Subject: "occulite-token:homeassistant", Scheme: auth.SchemeOcculiteToken, Role: auth.RoleOperator}},
			{"full access", tokFull, &auth.Identity{Subject: "occulite-token:ops", Scheme: auth.SchemeOcculiteToken, Role: auth.RoleAdmin}},
			{"another add-on", tokOther, nil},
			{"API scopes only", tokAPIOnly, nil},
			{"unknown to the box", tokUnknown, nil},
		} {
			got, resolved := resolveThrough(w.authResolve, tc.tok, bearer)
			switch {
			case tc.want == nil && resolved:
				t.Errorf("%s (bearer passed on: %v): identity %+v resolved, want none", tc.name, bearer, got)
			case tc.want != nil && (!resolved || got != *tc.want):
				t.Errorf("%s (bearer passed on: %v): identity = %+v (resolved %v), want %+v", tc.name, bearer, got, resolved, *tc.want)
			}
		}
	}
}

// TestWireREST_OcculiteTokenScopeComesFromTheTokenFile pins where the
// add-on's gate scope comes from: the name occulited gives the add-on's
// token file. With the file named for another add-on, that add-on's token
// resolves and this daemon's own does not — so the scope is read from the
// box's file, not assumed.
func TestWireREST_OcculiteTokenScopeComesFromTheTokenFile(t *testing.T) {
	dir := t.TempDir()
	version := filepath.Join(dir, "VERSION")
	if err := os.WriteFile(version, []byte("VERSION=1.0.0\nVARIANT=lite\n"), 0o600); err != nil {
		t.Fatalf("write version: %v", err)
	}
	token := filepath.Join(dir, "redmatic.api")
	if err := os.WriteFile(token, []byte("olt_test\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	t.Setenv(liteAddonVersionPathEnv, version)
	t.Setenv(liteAddonTokenPathEnv, token)
	box := fakeBoxState(t, occuliteTokenAnswers(), "")
	t.Setenv(liteAddonBaseURLEnv, box.URL)

	passthrough := func(next http.Handler) http.Handler { return next }
	w := wireREST(context.Background(), restWiringDeps{
		cfg:            config.Default(),
		logger:         slog.New(slog.DiscardHandler),
		authMw:         auth.NewMiddleware(auth.NewMemoryUserStore(), auth.NewMemoryTokenStore(nil)),
		restResolve:    passthrough,
		sessionResolve: passthrough,
	})
	if _, resolved := resolveThrough(w.authResolve, tokAddon, false); resolved {
		t.Error("an addon:openccu-loom token resolved although the token file names redmatic")
	}
	got, resolved := resolveThrough(w.authResolve, tokRedmatic, false)
	if !resolved || got.Role != auth.RoleOperator {
		t.Errorf("addon:redmatic token = %+v (resolved %v), want operator", got, resolved)
	}
}

// TestLiteAddonGateScope pins the derivation itself: <id>.api names the
// add-on; any other file name yields no scope, which leaves tokens out.
func TestLiteAddonGateScope(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ path, want string }{
		{"/run/occulite/addon-tokens/openccu-loom.api", "addon:openccu-loom"},
		{"openccu-loom.api", "addon:openccu-loom"},
		{"/run/occulite/addon-tokens/.api", ""},
		{"/run/occulite/addon-tokens/openccu-loom", ""},
		{"/run/occulite/addon-tokens/openccu-loom.api.bak", ""},
		{"", ""},
	} {
		if got := liteAddonGateScope(tc.path); got != tc.want {
			t.Errorf("liteAddonGateScope(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// TestOcculiteRevalidatorTokens pins the socket re-verification for box
// tokens: a token that keeps its scope keeps its socket; one the box no
// longer knows, or that lost Full access, closes an admin socket; a
// verification error keeps it, as for sessions.
func TestOcculiteRevalidatorTokens(t *testing.T) {
	liteAddonStampForTest(t)
	box := fakeBoxState(t, occuliteTokenAnswers(), tokBroken)
	t.Setenv(liteAddonBaseURLEnv, box.URL)

	logger := slog.New(slog.DiscardHandler)
	rv := occuliteRevalidator(buildOcculiteSSOTrust(config.Default(), logger), logger)
	if rv == nil {
		t.Fatal("revalidator is nil on a lite-add-on host with SSO enabled")
	}
	for _, tc := range []struct {
		name, tok string
		role      auth.Role
		want      bool
	}{
		{"addon scope held", tokAddon, auth.RoleOperator, true},
		{"full access held", tokFull, auth.RoleAdmin, true},
		{"revoked", tokUnknown, auth.RoleOperator, false},
		// The token now holds only the add-on scope: an admin socket closes.
		{"lost full access", tokAddon, auth.RoleAdmin, false},
		{"scope moved to another add-on", tokOther, auth.RoleOperator, false},
		{"verify error", tokBroken, auth.RoleOperator, true},
	} {
		if got := rv(context.Background(), tc.tok, tc.role); got != tc.want {
			t.Errorf("%s: revalidate = %v, want %v", tc.name, got, tc.want)
		}
	}
}
