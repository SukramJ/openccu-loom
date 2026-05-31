// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/i18n"
)

func newAuthUI(t *testing.T) (http.Handler, *auth.MemoryUserStore, *auth.SessionStore) {
	t.Helper()
	cats, _ := i18n.NewCatalogs()
	users := auth.NewMemoryUserStore()
	users.Put("alice", "s3cret", auth.RoleAdmin)
	sessions := auth.NewSessionStore()
	r := NewRouter(Deps{
		Lang: "en", Catalogs: cats,
		Auth: &AuthDeps{Users: users, Sessions: sessions},
	})
	return r, users, sessions
}

func TestLoginPostSuccessIssuesCookie(t *testing.T) {
	h, _, _ := newAuthUI(t)
	body := strings.NewReader("username=alice&password=s3cret")
	req := httptest.NewRequest("POST", "/login", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d", rr.Code)
	}
	if !strings.Contains(rr.Header().Get("Set-Cookie"), auth.SessionCookieName) {
		t.Fatalf("missing session cookie: %s", rr.Header().Get("Set-Cookie"))
	}
}

func TestLoginPostFailureRedirects(t *testing.T) {
	h, _, _ := newAuthUI(t)
	req := httptest.NewRequest("POST", "/login", strings.NewReader("username=alice&password=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther || !strings.HasSuffix(rr.Header().Get("Location"), "error=1") {
		t.Fatalf("status=%d loc=%s", rr.Code, rr.Header().Get("Location"))
	}
}

func TestLogoutPostClearsCookie(t *testing.T) {
	h, _, sessions := newAuthUI(t)
	sess, _ := sessions.Issue(auth.Identity{Subject: "alice", Role: auth.RoleAdmin})
	req := httptest.NewRequest("POST", "/logout", http.NoBody)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.ID})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d", rr.Code)
	}
	if sessions.Lookup(sess.ID) != nil {
		t.Fatal("session must be revoked")
	}
}

func TestSetupPostCreatesAdmin(t *testing.T) {
	cats, _ := i18n.NewCatalogs()
	users := auth.NewMemoryUserStore()
	h := NewRouter(Deps{
		Lang: "en", Catalogs: cats,
		Auth: &AuthDeps{Users: users, Sessions: auth.NewSessionStore()},
	})
	req := httptest.NewRequest("POST", "/setup",
		strings.NewReader("username=root&password=supersecret&confirm=supersecret"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("status=%d loc=%s", rr.Code, rr.Header().Get("Location"))
	}
	if _, err := users.AuthenticateBasic(req.Context(), "root", "supersecret"); err != nil {
		t.Fatalf("admin not created: %v", err)
	}
}

func TestSetupPostPasswordMismatchFails(t *testing.T) {
	cats, _ := i18n.NewCatalogs()
	users := auth.NewMemoryUserStore()
	h := NewRouter(Deps{
		Lang: "en", Catalogs: cats,
		Auth: &AuthDeps{Users: users, Sessions: auth.NewSessionStore()},
	})
	req := httptest.NewRequest("POST", "/setup",
		strings.NewReader("username=root&password=abc&confirm=xyz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !strings.HasSuffix(rr.Header().Get("Location"), "error=1") {
		t.Fatalf("loc=%s", rr.Header().Get("Location"))
	}
}

// ---------------------------------------------------------------------------
// handleLoginPost — oversized body returns 400
// ---------------------------------------------------------------------------

func TestHandleLoginPostOversizedBodyReturns400(t *testing.T) {
	t.Parallel()
	cats, _ := i18n.NewCatalogs()
	users := auth.NewMemoryUserStore()
	sessions := auth.NewSessionStore()
	h := NewRouter(Deps{
		Lang: "en", Catalogs: cats,
		Auth: &AuthDeps{Users: users, Sessions: sessions},
	})
	// Body larger than 64 KiB — triggers MaxBytesReader + ParseForm error.
	bigBody := strings.Repeat("x", 65*1024+1)
	req := httptest.NewRequest("POST", "/login", strings.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized body, got %d", rr.Code)
	}
}

// TestHandleSetupPostOversizedBodyReturns400 exercises the ParseForm error
// path in handleSetupPost.
func TestHandleSetupPostOversizedBodyReturns400(t *testing.T) {
	t.Parallel()
	cats, _ := i18n.NewCatalogs()
	users := auth.NewMemoryUserStore()
	sessions := auth.NewSessionStore()
	h := NewRouter(Deps{
		Lang: "en", Catalogs: cats,
		Auth: &AuthDeps{Users: users, Sessions: sessions},
	})
	bigBody := strings.Repeat("x", 65*1024+1)
	req := httptest.NewRequest("POST", "/setup", strings.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized body, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// handleLoginPost — success path (cookie issued + redirect to /)
// ---------------------------------------------------------------------------

func TestHandleLoginPostSuccessRedirectsToRoot(t *testing.T) {
	t.Parallel()
	cats, _ := i18n.NewCatalogs()
	users := auth.NewMemoryUserStore()
	users.Put("testuser", "testpass", auth.RoleAdmin)
	sessions := auth.NewSessionStore()
	h := NewRouter(Deps{
		Lang: "en", Catalogs: cats,
		Auth: &AuthDeps{Users: users, Sessions: sessions},
	})
	body := strings.NewReader("username=testuser&password=testpass")
	req := httptest.NewRequest("POST", "/login", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d body=%s", rr.Code, rr.Body.String())
	}
	if loc := rr.Header().Get("Location"); loc != "/" {
		t.Fatalf("expected redirect to /, got %q", loc)
	}
	if !strings.Contains(rr.Header().Get("Set-Cookie"), auth.SessionCookieName) {
		t.Fatalf("missing session cookie: %s", rr.Header().Get("Set-Cookie"))
	}
}

// ---------------------------------------------------------------------------
// handleLoginPost — auth not configured (nil AuthDeps) returns 503
// ---------------------------------------------------------------------------

func TestHandleLoginPostNilAuthReturns503(t *testing.T) {
	cats, _ := i18n.NewCatalogs()
	// Auth: nil → handleLoginPost should return 503
	h := NewRouter(Deps{Lang: "en", Catalogs: cats, Auth: nil})
	body := strings.NewReader("username=alice&password=s3cret")
	req := httptest.NewRequest("POST", "/login", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when Auth is nil, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// handleSetupPost — valid form creates user and redirects to /login
// ---------------------------------------------------------------------------

func TestHandleSetupPostCreatesUserAndRedirects(t *testing.T) {
	cats, _ := i18n.NewCatalogs()
	users := auth.NewMemoryUserStore()
	sessions := auth.NewSessionStore()
	h := NewRouter(Deps{
		Lang:     "en",
		Catalogs: cats,
		Auth:     &AuthDeps{Users: users, Sessions: sessions},
	})
	body := strings.NewReader("username=admin&password=secret1&confirm=secret1")
	req := httptest.NewRequest("POST", "/setup", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d body=%s", rr.Code, rr.Body.String())
	}
	if loc := rr.Header().Get("Location"); !strings.Contains(loc, "/login") {
		t.Fatalf("expected redirect to /login, got %q", loc)
	}
}

func TestHandleSetupPostPasswordMismatchRedirectsWithError(t *testing.T) {
	cats, _ := i18n.NewCatalogs()
	users := auth.NewMemoryUserStore()
	sessions := auth.NewSessionStore()
	h := NewRouter(Deps{
		Lang:     "en",
		Catalogs: cats,
		Auth:     &AuthDeps{Users: users, Sessions: sessions},
	})
	body := strings.NewReader("username=admin&password=secret1&confirm=different")
	req := httptest.NewRequest("POST", "/setup", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.Contains(loc, "error=1") {
		t.Fatalf("expected error in redirect, got %q", loc)
	}
}

func TestHandleSetupPostNilAuthReturns503(t *testing.T) {
	cats, _ := i18n.NewCatalogs()
	h := NewRouter(Deps{Lang: "en", Catalogs: cats, Auth: nil})
	body := strings.NewReader("username=admin&password=secret1&confirm=secret1")
	req := httptest.NewRequest("POST", "/setup", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when Auth is nil, got %d", rr.Code)
	}
}

func TestHandleLoginPostParsesFormValues(t *testing.T) {
	cats, _ := i18n.NewCatalogs()
	users := auth.NewMemoryUserStore()
	users.Put("bob", "pass123", auth.RoleAdmin)
	sessions := auth.NewSessionStore()
	h := NewRouter(Deps{
		Lang:     "en",
		Catalogs: cats,
		Auth:     &AuthDeps{Users: users, Sessions: sessions},
	})
	// Wrong password → redirect with error
	body := strings.NewReader("username=bob&password=wrongpass")
	req := httptest.NewRequest("POST", "/login", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.Contains(loc, "error=1") {
		t.Fatalf("expected error in redirect, got %q", loc)
	}
}
