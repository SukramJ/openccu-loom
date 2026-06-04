// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMemoryTokenStoreListSorted verifies that List returns entries sorted by
// subject and elides the token secret.
func TestMemoryTokenStoreListSorted(t *testing.T) {
	ts := NewMemoryTokenStore(map[string]Identity{
		"tok-z": {Subject: "zeta", Role: RoleAdmin},
		"tok-a": {Subject: "alpha", Role: RoleViewer},
		"tok-m": {Subject: "mu", Role: RoleOperator},
	})
	list := ts.List()
	if len(list) != 3 {
		t.Fatalf("List len=%d want 3", len(list))
	}
	if list[0].Subject != "alpha" || list[1].Subject != "mu" || list[2].Subject != "zeta" {
		t.Fatalf("List order wrong: %+v", list)
	}
	for _, s := range list {
		if s.Fingerprint == "" {
			t.Fatal("fingerprint should not be empty")
		}
	}
}

// TestMemoryTokenStoreListShortToken exercises the branch where the token is
// ≤6 chars (no truncation).
func TestMemoryTokenStoreListShortToken(t *testing.T) {
	ts := NewMemoryTokenStore(map[string]Identity{
		"abc": {Subject: "s", Role: RoleViewer},
	})
	list := ts.List()
	if len(list) != 1 {
		t.Fatalf("len=%d", len(list))
	}
	// Short token: fingerprint == the token itself (no leading "…").
	if strings.HasPrefix(list[0].Fingerprint, "…") {
		t.Fatalf("short token should not be truncated: %q", list[0].Fingerprint)
	}
}

// TestMemoryTokenStoreEmptyToken exercises the empty-token path.
func TestMemoryTokenStoreEmptyToken(t *testing.T) {
	ts := NewMemoryTokenStore(map[string]Identity{
		"tok": {Subject: "s", Role: RoleViewer},
	})
	_, err := ts.AuthenticateToken(context.Background(), "")
	if err == nil {
		t.Fatal("empty token should return ErrUnauthenticated")
	}
}

// TestMemoryUserStoreListSorted verifies List order for user store.
func TestMemoryUserStoreListSorted(t *testing.T) {
	us := NewMemoryUserStore()
	us.Put("charlie", "p", RoleViewer)
	us.Put("alice", "p", RoleAdmin)
	us.Put("bob", "p", RoleOperator)
	list := us.List()
	if len(list) != 3 {
		t.Fatalf("len=%d", len(list))
	}
	if list[0].Username != "alice" || list[1].Username != "bob" || list[2].Username != "charlie" {
		t.Fatalf("order wrong: %+v", list)
	}
}

// TestContextWithIdentity exercises the ContextWithIdentity helper.
func TestContextWithIdentity(t *testing.T) {
	id := Identity{Subject: "test-user", Role: RoleAdmin, Scheme: SchemeBearer}
	ctx := ContextWithIdentity(context.Background(), id)
	got, ok := IdentityFrom(ctx)
	if !ok {
		t.Fatal("IdentityFrom should return ok after ContextWithIdentity")
	}
	if got.Subject != "test-user" || got.Role != RoleAdmin {
		t.Fatalf("got=%+v", got)
	}
}

// TestSessionRevoke verifies that Revoke removes a valid session.
func TestSessionRevoke(t *testing.T) {
	store := NewSessionStore()
	sess, err := store.Issue(Identity{Subject: "alice", Role: RoleViewer})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	store.Revoke(sess.ID)
	if got := store.Lookup(sess.ID); got != nil {
		t.Fatal("Lookup should return nil after Revoke")
	}
	// Revoking a non-existent session is a no-op.
	store.Revoke("does-not-exist")
}

// TestWriteAndClearSessionCookie exercises the cookie-writing helpers via
// httptest.ResponseRecorder.
func TestWriteAndClearSessionCookie(t *testing.T) {
	store := NewSessionStore()
	sess, _ := store.Issue(Identity{Subject: "u", Role: RoleViewer})

	rr := httptest.NewRecorder()
	WriteSessionCookie(rr, sess, false)
	cookies := rr.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == SessionCookieName && c.Value == sess.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("WriteSessionCookie did not set the expected cookie")
	}

	rr2 := httptest.NewRecorder()
	ClearSessionCookie(rr2)
	cleared := rr2.Result().Cookies()
	clearedFound := false
	for _, c := range cleared {
		if c.Name == SessionCookieName && c.MaxAge < 0 {
			clearedFound = true
		}
	}
	if !clearedFound {
		t.Fatal("ClearSessionCookie did not set MaxAge=-1 cookie")
	}
}

// TestCSRFTokenFromContext exercises CSRFToken when the context carries a token.
func TestCSRFTokenFromContext(t *testing.T) {
	// The CSRF middleware stores the token in context via csrfCtxKey{}. We can
	// verify this by going through the middleware and reading the token in the
	// next handler.
	expectedToken := ""
	mw := CSRFMiddleware(false)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		expectedToken = CSRFToken(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if expectedToken == "" {
		t.Fatal("CSRFToken returned empty from context")
	}
}

// TestCSRFMiddlewareExemptBasicAuth verifies that an authenticated Basic-auth
// request bypasses the double-submit token check. Browsers do not auto-include
// Authorization headers on cross-origin requests, so per-request credentials
// are not CSRF-vulnerable. The chip-tool harness, curl scripts, and ops
// automation rely on this exemption.
func TestCSRFMiddlewareExemptBasicAuth(t *testing.T) {
	hit := 0
	mw := CSRFMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit++
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	// Stamp Basic identity in the context (as Middleware.Resolve would).
	req = req.WithContext(ContextWithIdentity(req.Context(), Identity{
		Subject: "ops",
		Scheme:  SchemeBasic,
		Role:    RoleAdmin,
	}))
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != 204 || hit != 1 {
		t.Fatalf("Basic-auth POST should bypass CSRF: status=%d hit=%d", rr.Code, hit)
	}
}

// TestCSRFMiddlewareExemptBearerAuth verifies the same exemption for Bearer-
// token (API token) auth — the second per-request credential scheme.
func TestCSRFMiddlewareExemptBearerAuth(t *testing.T) {
	hit := 0
	mw := CSRFMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit++
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(ContextWithIdentity(req.Context(), Identity{
		Subject: "api-token-1",
		Scheme:  SchemeBearer,
		Role:    RoleOperator,
	}))
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != 204 || hit != 1 {
		t.Fatalf("Bearer-auth POST should bypass CSRF: status=%d hit=%d", rr.Code, hit)
	}
}

// TestCSRFMiddlewareEnforcesOnSession verifies that the bypass does NOT extend
// to session-cookie identities — those carry ambient credentials and remain
// CSRF-vulnerable.
func TestCSRFMiddlewareEnforcesOnSession(t *testing.T) {
	mw := CSRFMiddleware(false)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("session POST without CSRF token must not reach handler")
	}))
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(ContextWithIdentity(req.Context(), Identity{
		Subject: "alice",
		Scheme:  SchemeSession,
		Role:    RoleAdmin,
	}))
	// No CSRF cookie + no token → must 403.
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("session POST without token: status=%d, want 403", rr.Code)
	}
}

// TestCSRFMiddlewareEnforcesOnAnonymous verifies that the bypass does NOT
// extend to unauthenticated requests — those still go through the
// double-submit gate so a CSRF on a login-style endpoint is caught.
func TestCSRFMiddlewareEnforcesOnAnonymous(t *testing.T) {
	mw := CSRFMiddleware(false)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("anonymous POST without CSRF token must not reach handler")
	}))
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	// No identity → must 403.
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("anonymous POST without token: status=%d, want 403", rr.Code)
	}
}

// TestCSRFMiddlewareFormField exercises the form-field branch of the CSRF check.
func TestCSRFMiddlewareFormField(t *testing.T) {
	hit := 0
	mw := CSRFMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit++
		w.WriteHeader(http.StatusNoContent)
	}))
	// Submit via form field instead of header.
	body := strings.NewReader(CSRFFormField + "=match-form")
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "match-form"})
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != 204 || hit != 1 {
		t.Fatalf("status=%d hit=%d; want 204/1", rr.Code, hit)
	}
}

// TestMiddlewareRequireRolePassesForAdmin exercises that an admin passes any
// RequireRole gate.
func TestMiddlewareRequireRolePassesForAdmin(t *testing.T) {
	us := NewMemoryUserStore()
	us.Put("admin", "s", RoleAdmin)
	mw := NewMiddleware(us, nil)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := mw.Resolve(mw.RequireRole(RoleAdmin, next))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.SetBasicAuth("admin", "s")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("admin should pass RoleAdmin gate: status=%d", rr.Code)
	}
}

// TestMiddlewareRequireRoleUnauthenticated exercises the 401 path in RequireRole.
func TestMiddlewareRequireRoleUnauthenticated(t *testing.T) {
	mw := NewMiddleware(nil, nil)
	h := mw.RequireRole(RoleViewer, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next should not run")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("RequireRole without identity should return 401: status=%d", rr.Code)
	}
}

// TestSessionMiddlewareNilStore verifies that a nil store is a no-op.
func TestSessionMiddlewareNilStore(t *testing.T) {
	hit := 0
	h := SessionMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit++
		// no identity attached
		_, ok := IdentityFrom(r.Context())
		if ok {
			t.Fatal("nil store should not attach identity")
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if hit != 1 {
		t.Fatalf("hit=%d", hit)
	}
}
