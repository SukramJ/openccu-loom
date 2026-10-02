// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// validToken is a well-formed box API token: "olt_" and 32 lower-case hex.
const validToken = "olt_0123456789abcdef0123456789abcdef"

// loomScope is the gate scope the tests give the add-on.
const loomScope = "addon:openccu-loom"

func tokenTrust(v *fakeOcculiteVerifier) OcculiteSSOTrust {
	return OcculiteSSOTrust{Enabled: true, Verifier: v, AddonScope: loomScope}
}

// tokenAnswer is the box's auth-state answer for a token: a "token:<name>"
// user, the stored scopes, and no role.
func tokenAnswer(name string, scopes ...string) OcculiteSession {
	return OcculiteSession{Authenticated: true, User: "token:" + name, Scopes: scopes}
}

// TestOcculiteTokenMapsScopesToRoles: the add-on's own gate scope grants
// operator, Full access admin — also when both are present — with a subject
// that names the token and a scheme of its own; a second request is served
// from the cache.
func TestOcculiteTokenMapsScopesToRoles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		scopes []string
		want   Role
	}{
		{"addon scope", []string{loomScope}, RoleOperator},
		{"addon scope beside others", []string{"system:read", loomScope, "logs:read"}, RoleOperator},
		{"full access", []string{"*"}, RoleAdmin},
		{"full access and addon scope", []string{loomScope, "*"}, RoleAdmin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := &fakeOcculiteVerifier{sess: tokenAnswer("homeassistant", tc.scopes...)}
			rec := &occuliteRecorder{}
			h := OcculiteSSOPassthrough(tokenTrust(v), nil)(rec)
			want := Identity{Subject: "occulite-token:homeassistant", Scheme: SchemeOcculiteToken, Role: tc.want}
			for range 2 {
				serveOcculite(t, h, occuliteRequest(validToken))
				if !rec.hasID || rec.id != want {
					t.Fatalf("identity = %+v (present %v), want %+v", rec.id, rec.hasID, want)
				}
			}
			if n := v.count(); n != 1 {
				t.Errorf("verifier calls = %d, want 1 (positive cache)", n)
			}
		})
	}
}

// TestOcculiteTokenRefusesEveryAnswerThatDoesNotVouch: a token whose scopes
// open another add-on, or only the APIs, grants nothing — the gate checked
// the scope, but the daemon's port is reachable without the gate. Neither
// does an answer of the wrong kind or shape, nor an auth-off or public one.
// Every refusal is negatively cached.
func TestOcculiteTokenRefusesEveryAnswerThatDoesNotVouch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		sess OcculiteSession
	}{
		{"another add-on's scope", tokenAnswer("ha", "addon:redmatic")},
		{"scope differing only in case", tokenAnswer("ha", "addon:OpenCCU-Loom")},
		{"scope prefix only", tokenAnswer("ha", "addon:openccu")},
		{"API scopes only", tokenAnswer("ha", "system:write", "rpc:admin", "addons:write")},
		{"no scopes", tokenAnswer("ha")},
		{"unauthenticated", OcculiteSession{User: "token:ha", Scopes: []string{"*"}}},
		{"auth off", OcculiteSession{Authenticated: true, User: "token:ha", Scopes: []string{"*"}, AuthOff: true}},
		{"public", OcculiteSession{Authenticated: true, User: "token:ha", Scopes: []string{loomScope}, Public: true}},
		// A token's answer carries no role; one that does is not about a token.
		{"answer with a role", OcculiteSession{Authenticated: true, User: "token:ha", Role: "admin", Scopes: []string{"*"}}},
		{"session-shaped answer", OcculiteSession{Authenticated: true, User: "alice", Role: "admin"}},
		{"user without token prefix", OcculiteSession{Authenticated: true, User: "ha", Scopes: []string{"*"}}},
		{"empty token name", tokenAnswer("", "*")},
		{"token name with colon", tokenAnswer("a:b", "*")},
		{"token name with equals", tokenAnswer("a=b", "*")},
		{"token name 65 bytes", tokenAnswer(strings.Repeat("a", 65), "*")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := &fakeOcculiteVerifier{sess: tc.sess}
			rec := &occuliteRecorder{}
			h := OcculiteSSOPassthrough(tokenTrust(v), nil)(rec)
			for range 2 {
				serveOcculite(t, h, occuliteRequest(validToken))
				if rec.hasID {
					t.Fatalf("identity injected: %+v", rec.id)
				}
			}
			if n := v.count(); n != 1 {
				t.Errorf("verifier calls = %d, want 1 (negative cache)", n)
			}
		})
	}
}

// TestOcculiteTokenNeedsTheAddonScope: without the add-on's own scope in the
// trust the daemon cannot tell its tokens from another add-on's, so a token
// is never even sent to the box — a Full-access answer included.
func TestOcculiteTokenNeedsTheAddonScope(t *testing.T) {
	t.Parallel()
	v := &fakeOcculiteVerifier{sess: tokenAnswer("ha", "*")}
	rec := &occuliteRecorder{}
	h := OcculiteSSOPassthrough(OcculiteSSOTrust{Enabled: true, Verifier: v}, nil)(rec)
	serveOcculite(t, h, occuliteRequest(validToken))
	if rec.hasID {
		t.Errorf("identity injected without an add-on scope: %+v", rec.id)
	}
	if n := v.count(); n != 0 {
		t.Errorf("verifier calls = %d, want 0", n)
	}
	if _, ok := tokenAnswer("ha", "*").Grant(validToken, ""); ok {
		t.Error("Grant vouched for a token without an add-on scope")
	}
}

// TestOcculiteTokenMalformedNeverVerifies: anything but "olt_" and exactly
// 32 lower-case hex digits defers without asking the box.
func TestOcculiteTokenMalformedNeverVerifies(t *testing.T) {
	t.Parallel()
	hex := strings.TrimPrefix(validToken, "olt_")
	for _, tc := range []struct{ name, tok string }{
		{"31 hex", "olt_" + hex[:31]},
		{"33 hex", "olt_" + hex + "0"},
		{"upper-case hex", "olt_" + strings.ToUpper(hex)},
		{"non-hex digit", "olt_" + hex[:31] + "g"},
		{"other prefix", "olx_" + hex},
		{"upper-case prefix", "OLT_" + hex},
		{"prefix only", "olt_"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := &fakeOcculiteVerifier{sess: tokenAnswer("ha", "*")}
			rec := &occuliteRecorder{}
			h := OcculiteSSOPassthrough(tokenTrust(v), nil)(rec)
			serveOcculite(t, h, occuliteRequest(tc.tok))
			if rec.hasID {
				t.Errorf("identity injected for %q: %+v", tc.tok, rec.id)
			}
			if n := v.count(); n != 0 {
				t.Errorf("verifier calls = %d, want 0", n)
			}
		})
	}
}

// TestOcculiteGrantMatchesTheCredentialKind: the answer must be about the
// kind of credential presented. A token-shaped answer for a session id, or
// a session-shaped answer for a token, vouches for nothing.
func TestOcculiteGrantMatchesTheCredentialKind(t *testing.T) {
	t.Parallel()
	session := OcculiteSession{Authenticated: true, User: "alice", Role: "admin"}
	token := tokenAnswer("ha", "*")
	if _, ok := token.Grant(validSID, loomScope); ok {
		t.Error("a token answer vouched for a session id")
	}
	if _, ok := session.Grant(validToken, loomScope); ok {
		t.Error("a session answer vouched for a token")
	}
	if id, ok := session.Grant(validSID, loomScope); !ok || id.Scheme != SchemeOcculite || id.Role != RoleAdmin {
		t.Errorf("session answer for a session id = %+v, %v; want an admin SchemeOcculite identity", id, ok)
	}
	if id, ok := token.Grant(validToken, loomScope); !ok || id.Scheme != SchemeOcculiteToken || id.Role != RoleAdmin {
		t.Errorf("token answer for a token = %+v, %v; want an admin SchemeOcculiteToken identity", id, ok)
	}
	// Grant is called by the socket revalidator too, outside the middleware's
	// shape filter: a credential of neither shape vouches for nothing, even
	// with an answer that would grant a well-formed one.
	for _, cred := range []string{"", "not-a-credential", strings.ToUpper(validToken), validSID[:25]} {
		if _, ok := token.Grant(cred, loomScope); ok {
			t.Errorf("a token answer vouched for the malformed credential %q", cred)
		}
		if _, ok := session.Grant(cred, loomScope); ok {
			t.Errorf("a session answer vouched for the malformed credential %q", cred)
		}
	}
}

// TestOcculiteTokenSchemeIsFederatedBoxVouchedAndCSRFExempt: the box vouches
// for the token, so local-account controls must not reach it and sockets
// must re-ask the box; and the gate takes the token only from a request's own
// header, so — like a bearer — no browser attaches it by itself.
func TestOcculiteTokenSchemeIsFederatedBoxVouchedAndCSRFExempt(t *testing.T) {
	t.Parallel()
	if !SchemeOcculiteToken.Federated() {
		t.Error("SchemeOcculiteToken must be federated")
	}
	for _, s := range []Scheme{SchemeOcculite, SchemeOcculiteToken} {
		if !s.BoxVouched() {
			t.Errorf("%s must be box-vouched", s)
		}
	}
	for _, s := range []Scheme{SchemeBasic, SchemeBearer, SchemeSession, SchemeOIDC, SchemeIngress} {
		if s.BoxVouched() {
			t.Errorf("%s must not be box-vouched", s)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/something", http.NoBody)
	rr, called := applyCSRFAs(r, Identity{Subject: "occulite-token:ha", Scheme: SchemeOcculiteToken, Role: RoleOperator})
	if !called || rr.Code != http.StatusOK {
		t.Errorf("tokenless write as SchemeOcculiteToken: status %d, handler called %v; want 200, called", rr.Code, called)
	}
}
