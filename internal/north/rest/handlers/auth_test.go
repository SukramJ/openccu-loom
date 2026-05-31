// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

func newTestAuthDeps(t *testing.T) *AuthDeps {
	t.Helper()
	users := auth.NewMemoryUserStore()
	users.Put("admin", "secret", auth.RoleAdmin)
	users.Put("viewer", "pass", auth.RoleViewer)
	tokens := auth.NewMemoryTokenStore(map[string]auth.Identity{
		"tok-abc-123": {Subject: "api-user", Role: auth.RoleOperator},
	})
	sessions := auth.NewSessionStore()
	return &AuthDeps{
		Users:    users,
		Sessions: sessions,
		Tokens:   tokens,
		Secure:   false,
	}
}

// --- ListUsers ---

func TestListUsers_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestAuthDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/users", http.NoBody)
	w := httptest.NewRecorder()
	ListUsers(d).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []UserListEntry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("expected 2 users, got %d: %+v", len(body), body)
	}
}

func TestListUsers_NilDeps_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/users", http.NoBody)
	w := httptest.NewRecorder()
	ListUsers(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestListUsers_NilUserStore_Returns503(t *testing.T) {
	t.Parallel()
	d := &AuthDeps{Users: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/users", http.NoBody)
	w := httptest.NewRecorder()
	ListUsers(d).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// --- ListTokens ---

func TestListTokens_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestAuthDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/tokens", http.NoBody)
	w := httptest.NewRecorder()
	ListTokens(d).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []TokenListEntry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 token, got %d: %+v", len(body), body)
	}
	if body[0].Subject != "api-user" {
		t.Fatalf("expected subject=api-user, got %q", body[0].Subject)
	}
}

func TestListTokens_NilDeps_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/tokens", http.NoBody)
	w := httptest.NewRecorder()
	ListTokens(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with empty array, got %d", w.Code)
	}
	var body []TokenListEntry
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 0 {
		t.Fatalf("expected empty, got %+v", body)
	}
}

// --- Login ---

func TestLogin_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestAuthDeps(t)
	body := strings.NewReader(`{"username":"admin","password":"secret"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	w := httptest.NewRecorder()
	Login(d).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp meResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Subject != "admin" {
		t.Fatalf("expected subject=admin, got %q", resp.Subject)
	}
}

func TestLogin_WrongPassword_Returns401(t *testing.T) {
	t.Parallel()
	d := newTestAuthDeps(t)
	body := strings.NewReader(`{"username":"admin","password":"wrong"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	w := httptest.NewRecorder()
	Login(d).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestLogin_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	d := newTestAuthDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("NOT JSON"))
	w := httptest.NewRecorder()
	Login(d).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestLogin_NilDeps_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"username":"admin","password":"secret"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	w := httptest.NewRecorder()
	Login(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// --- Logout ---

func TestLogout_NoSession_Returns204(t *testing.T) {
	t.Parallel()
	d := newTestAuthDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", http.NoBody)
	w := httptest.NewRecorder()
	Logout(d).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestLogout_WithSession_ClearsCookie(t *testing.T) {
	t.Parallel()
	d := newTestAuthDeps(t)
	id := auth.Identity{Subject: "admin", Role: auth.RoleAdmin, Scheme: auth.SchemeSession}
	sess, err := d.Sessions.Issue(id)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", http.NoBody)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.ID})
	w := httptest.NewRecorder()
	Logout(d).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestLogout_NilDeps_Returns204(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", http.NoBody)
	w := httptest.NewRecorder()
	Logout(nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

// --- Me ---

func TestMe_WithIdentity_Returns200(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody)
	id := auth.Identity{Subject: "testuser", Role: auth.RoleAdmin, Scheme: auth.SchemeSession}
	req = req.WithContext(auth.ContextWithIdentity(req.Context(), id))
	w := httptest.NewRecorder()
	Me().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp meResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Subject != "testuser" {
		t.Fatalf("expected subject=testuser, got %q", resp.Subject)
	}
}

func TestMe_NoIdentity_Returns401(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody)
	w := httptest.NewRecorder()
	Me().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// --- identitySubject helper ---

func TestIdentitySubject_NonEmpty(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	id := auth.Identity{Subject: "user1", Role: auth.RoleAdmin, Scheme: auth.SchemeSession}
	ctx := auth.ContextWithIdentity(req.Context(), id)
	req = req.WithContext(ctx)
	if got := identitySubject(req.Context()); got != "user1" {
		t.Errorf("expected user1, got %q", got)
	}
}
