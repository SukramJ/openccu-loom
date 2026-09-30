// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// validSID is a well-formed box session id: 26 characters of A-Z2-7.
const validSID = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

// fakeOcculiteVerifier answers every session with one fixed outcome and
// counts how often it was asked.
type fakeOcculiteVerifier struct {
	mu    sync.Mutex
	calls int
	sess  OcculiteSession
	err   error
}

func (f *fakeOcculiteVerifier) VerifySession(_ context.Context, _ string) (OcculiteSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.sess, f.err
}

func (f *fakeOcculiteVerifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// occuliteRecorder is the terminal handler: it records the identity and the
// exact request it was handed.
type occuliteRecorder struct {
	hasID bool
	id    Identity
	req   *http.Request
}

func (o *occuliteRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	o.id, o.hasID = IdentityFrom(r.Context())
	o.req = r
	w.WriteHeader(http.StatusOK)
}

func occuliteRequest(sid string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/devices", http.NoBody)
	if sid != "" {
		r.Header.Set(OcculiteSessionHeader, sid)
	}
	return r
}

// serveOcculite runs one request through h and fails the test on any status
// other than 200: the resolver must never reject.
func serveOcculite(t *testing.T, h http.Handler, r *http.Request) {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the resolver must never reject", rr.Code)
	}
}

// TestOcculiteSSOPassthroughInert: without the opt-in or without a verifier
// the resolver is the identity middleware and never asks the box.
func TestOcculiteSSOPassthroughInert(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		trust func(*fakeOcculiteVerifier) OcculiteSSOTrust
	}{
		{"disabled", func(v *fakeOcculiteVerifier) OcculiteSSOTrust { return OcculiteSSOTrust{Verifier: v} }},
		{"nil verifier", func(*fakeOcculiteVerifier) OcculiteSSOTrust { return OcculiteSSOTrust{Enabled: true} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: "alice", Role: "admin"}}
			rec := &occuliteRecorder{}
			h := OcculiteSSOPassthrough(tc.trust(v), nil)(rec)
			if h != http.Handler(rec) {
				t.Error("an inert resolver must return next itself")
			}
			serveOcculite(t, h, occuliteRequest(validSID))
			if rec.hasID {
				t.Errorf("inert resolver injected %+v", rec.id)
			}
			if n := v.count(); n != 0 {
				t.Errorf("verifier calls = %d, want 0", n)
			}
		})
	}
}

// TestOcculiteSSOPassthroughExistingIdentityWins: a request already resolved
// by a real credential passes untouched and the box is never asked.
func TestOcculiteSSOPassthroughExistingIdentityWins(t *testing.T) {
	t.Parallel()
	v := &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: "alice", Role: "admin"}}
	rec := &occuliteRecorder{}
	h := OcculiteSSOPassthrough(OcculiteSSOTrust{Enabled: true, Verifier: v}, nil)(rec)

	prior := Identity{Subject: "bob", Scheme: SchemeSession, Role: RoleViewer}
	in := occuliteRequest(validSID)
	in = in.WithContext(ContextWithIdentity(in.Context(), prior))
	serveOcculite(t, h, in)

	if rec.req != in {
		t.Error("the request was replaced; it must pass untouched")
	}
	if !rec.hasID || rec.id != prior {
		t.Errorf("identity = %+v, want the prior %+v", rec.id, prior)
	}
	if n := v.count(); n != 0 {
		t.Errorf("verifier calls = %d, want 0", n)
	}
}

// TestOcculiteSSOPassthroughMalformedHeaderNeverVerifies: anything but an
// exact 26-character A-Z2-7 value defers without asking the box.
func TestOcculiteSSOPassthroughMalformedHeaderNeverVerifies(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, sid string }{
		{"missing", ""},
		{"25 chars", validSID[:25]},
		{"27 chars", validSID + "A"},
		{"lowercase", "abcdefghijklmnopqrstuvwxyz"},
		{"digit outside alphabet", "ABCDEFGHIJKLMNOPQRSTUVWXY1"},
		{"ten-char legacy alias", "ABCDEFGHIJ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: "alice", Role: "admin"}}
			rec := &occuliteRecorder{}
			h := OcculiteSSOPassthrough(OcculiteSSOTrust{Enabled: true, Verifier: v}, nil)(rec)
			in := occuliteRequest(tc.sid)
			serveOcculite(t, h, in)
			if rec.hasID {
				t.Errorf("identity injected for %q: %+v", tc.sid, rec.id)
			}
			if rec.req != in {
				t.Error("the request was replaced; it must pass untouched")
			}
			if n := v.count(); n != 0 {
				t.Errorf("verifier calls = %d, want 0", n)
			}
		})
	}
}

// TestOcculiteSSOPassthroughDefersOnEveryNonConfirmingAnswer: a verification
// error, an unauthenticated session, and an unmapped role all pass the
// request unauthenticated — and are negatively cached, so a second request
// inside the window does not ask the box again.
func TestOcculiteSSOPassthroughDefersOnEveryNonConfirmingAnswer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		v    *fakeOcculiteVerifier
	}{
		{"verify error", &fakeOcculiteVerifier{err: errors.New("box unreachable")}},
		{"unauthenticated", &fakeOcculiteVerifier{sess: OcculiteSession{User: "alice", Role: "admin"}}},
		{"role viewer", &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: "alice", Role: "viewer"}}},
		{"role empty", &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: "alice"}}},
		// An auth-off box confirms any bearer as admin; a public-mode box
		// substitutes its kiosk principal. Neither answer is about the id.
		{"auth off admin", &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: "admin", Role: "admin", AuthOff: true}}},
		{"public user", &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: "public", Role: "user", Public: true}}},
		{"user empty", &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, Role: "admin"}}},
		{"user 65 bytes", &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: strings.Repeat("a", 65), Role: "admin"}}},
		{"user with equals", &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: "a=b", Role: "admin"}}},
		{"user with colon", &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: "a:b", Role: "admin"}}},
		{"user with quote", &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: `a"b`, Role: "admin"}}},
		{"user with control char", &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: "a\nb", Role: "admin"}}},
		{"user non-ascii", &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: "b\u00fcro", Role: "admin"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &occuliteRecorder{}
			h := OcculiteSSOPassthrough(OcculiteSSOTrust{Enabled: true, Verifier: tc.v}, nil)(rec)
			for range 2 {
				serveOcculite(t, h, occuliteRequest(validSID))
				if rec.hasID {
					t.Fatalf("identity injected: %+v", rec.id)
				}
			}
			if n := tc.v.count(); n != 1 {
				t.Errorf("verifier calls = %d, want 1 (negative cache)", n)
			}
		})
	}
}

// TestOcculiteSSOPassthroughMapsTheBoxRole: a confirmed session yields a
// SchemeOcculite identity with the origin-prefixed subject, no expiry, and
// the fixed role mapping; a second request is served from the cache.
func TestOcculiteSSOPassthroughMapsTheBoxRole(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		boxRole string
		want    Role
	}{
		{"admin", RoleAdmin},
		{"user", RoleOperator},
	} {
		t.Run(tc.boxRole, func(t *testing.T) {
			t.Parallel()
			v := &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: "alice", Role: tc.boxRole}}
			rec := &occuliteRecorder{}
			h := OcculiteSSOPassthrough(OcculiteSSOTrust{Enabled: true, Verifier: v}, nil)(rec)
			for range 2 {
				serveOcculite(t, h, occuliteRequest(validSID))
				want := Identity{Subject: "occulite:alice", Scheme: SchemeOcculite, Role: tc.want}
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

// TestOcculiteUserShapeAcceptsTheBoundary: the shape check is not so tight
// that it refuses ordinary names; 64 bytes and an inner space pass.
func TestOcculiteUserShapeAcceptsTheBoundary(t *testing.T) {
	t.Parallel()
	for _, u := range []string{"alice", "Alice Smith", strings.Repeat("a", 64), "a.b-c_d@e"} {
		if !isOcculiteUser(u) {
			t.Errorf("isOcculiteUser(%q) = false, want true", u)
		}
	}
}

// TestOcculiteSSOCacheExpires: the positive entry lives 60s, the negative
// one 10s; past either window the box is asked again.
func TestOcculiteSSOCacheExpires(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		sess OcculiteSession
		ttl  time.Duration
	}{
		{"positive", OcculiteSession{Authenticated: true, User: "alice", Role: "admin"}, occulitePositiveTTL},
		{"negative", OcculiteSession{}, occuliteNegativeTTL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			now := time.Unix(1_000_000, 0)
			v := &fakeOcculiteVerifier{sess: tc.sess}
			s := newOcculiteSSO(OcculiteSSOTrust{Enabled: true, Verifier: v}, nil, func() time.Time { return now })
			h := s.middleware(&occuliteRecorder{})

			serveOcculite(t, h, occuliteRequest(validSID))
			now = now.Add(tc.ttl - time.Second)
			serveOcculite(t, h, occuliteRequest(validSID))
			if n := v.count(); n != 1 {
				t.Fatalf("verifier calls inside the window = %d, want 1", n)
			}
			now = now.Add(time.Second)
			serveOcculite(t, h, occuliteRequest(validSID))
			if n := v.count(); n != 2 {
				t.Fatalf("verifier calls past the window = %d, want 2", n)
			}
		})
	}
}

// TestOcculiteSSOCacheIsBounded: more distinct session ids than the cap
// never grow the cache past it, and a full cache still resolves.
func TestOcculiteSSOCacheIsBounded(t *testing.T) {
	t.Parallel()
	v := &fakeOcculiteVerifier{sess: OcculiteSession{Authenticated: true, User: "alice", Role: "admin"}}
	s := newOcculiteSSO(OcculiteSSOTrust{Enabled: true, Verifier: v}, nil, time.Now)
	rec := &occuliteRecorder{}
	h := s.middleware(rec)

	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	n := occuliteCacheCap + 1
	for i := range n {
		sid := []byte(validSID)
		sid[0], sid[1], sid[2] = alphabet[i%32], alphabet[(i/32)%32], alphabet[(i/1024)%32]
		serveOcculite(t, h, occuliteRequest(string(sid)))
		if !rec.hasID {
			t.Fatalf("request %d did not resolve", i)
		}
	}
	s.mu.Lock()
	size := len(s.cache)
	s.mu.Unlock()
	if size > occuliteCacheCap {
		t.Errorf("cache size = %d, want <= %d", size, occuliteCacheCap)
	}
	if got := v.count(); got != n {
		t.Errorf("verifier calls = %d, want %d (every id distinct)", got, n)
	}
}

// TestOcculiteSchemeIsFederated: the box vouches for the principal, so
// subject-keyed controls over local accounts must not reach it.
func TestOcculiteSchemeIsFederated(t *testing.T) {
	t.Parallel()
	if !SchemeOcculite.Federated() {
		t.Error("SchemeOcculite must be federated")
	}
}

// TestOcculiteSchemeNotCSRFExempt: the gate attaches the session header to
// every request the browser's box cookie authorizes, cross-site ones
// included, so the double-submit defence must apply.
func TestOcculiteSchemeNotCSRFExempt(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/something", http.NoBody)
	if csrfExempt(r, SchemeOcculite) {
		t.Error("SchemeOcculite is browser-ambient and must not be CSRF-exempt")
	}
	rr, called := applyCSRFAs(r, Identity{Subject: "occulite:alice", Scheme: SchemeOcculite, Role: RoleAdmin})
	if called || rr.Code != http.StatusForbidden {
		t.Errorf("tokenless write as SchemeOcculite: status %d, handler called %v; want 403, not called", rr.Code, called)
	}
}
