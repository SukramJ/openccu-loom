// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// stubSPA is a minimal http.Handler that writes 200 so the router mounts the
// root "/" redirect block (SPAHandler != nil gate).
var stubSPA = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// stubBootstrap writes 200 "BOOT" so tests can confirm Bootstrap is reached.
var stubBootstrap = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("BOOT"))
})

// injectIdentityMiddleware wraps a handler so every request carries the given
// Identity in its context — simulates a resolved credential (Bearer/session)
// upstream of the router.
func injectIdentityMiddleware(id auth.Identity) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.ContextWithIdentity(r.Context(), id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// noUsersTrue is a NoUsers func that always reports first-run.
func noUsersTrue(_ context.Context) bool { return true }

// noUsersFalse is a NoUsers func that always reports users exist.
func noUsersFalse(_ context.Context) bool { return false }

// ---------------------------------------------------------------------------
// NoUsers / first-run redirect tests
// ---------------------------------------------------------------------------

// TestRootFirstRun_NoUsers_Unauthenticated: when NoUsers → true and the
// request carries no identity, GET "/" must redirect 303 to /setup.
func TestRootFirstRun_NoUsers_Unauthenticated(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{
		StartedAt:  time.Now(),
		SPAHandler: stubSPA,
		NoUsers:    noUsersTrue,
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d, want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/setup" {
		t.Errorf("Location=%q, want /setup", loc)
	}
}

// TestRootFirstRun_NoUsers_Authenticated: when NoUsers → true BUT the request
// carries a resolved identity, GET "/" must redirect 302 to /app/ (setup
// already done or admin bypassing it via Ingress).
func TestRootFirstRun_NoUsers_Authenticated(t *testing.T) {
	t.Parallel()
	resolvedID := auth.Identity{Subject: "admin", Scheme: auth.SchemeBearer, Role: auth.RoleAdmin}
	r := NewRouter(Deps{
		StartedAt:   time.Now(),
		SPAHandler:  stubSPA,
		NoUsers:     noUsersTrue,
		AuthResolve: injectIdentityMiddleware(resolvedID),
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if rr.Code != http.StatusFound {
		t.Fatalf("status=%d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/app/" {
		t.Errorf("Location=%q, want /app/", loc)
	}
}

// TestRootFirstRun_UsersExist: when NoUsers → false, GET "/" must redirect
// 302 to /app/ regardless of identity.
func TestRootFirstRun_UsersExist(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{
		StartedAt:  time.Now(),
		SPAHandler: stubSPA,
		NoUsers:    noUsersFalse,
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if rr.Code != http.StatusFound {
		t.Fatalf("status=%d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/app/" {
		t.Errorf("Location=%q, want /app/", loc)
	}
}

// ---------------------------------------------------------------------------
// Bootstrap handler mount tests
// ---------------------------------------------------------------------------

// TestBootstrap_SetupReachable: with Bootstrap wired, GET /setup must reach
// the Bootstrap handler (200 "BOOT"), not the REST 404 handler.
func TestBootstrap_SetupReachable(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{
		StartedAt:  time.Now(),
		SPAHandler: stubSPA,
		Bootstrap:  stubBootstrap,
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/setup", http.NoBody))

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rr.Code)
	}
	if rr.Body.String() != "BOOT" {
		t.Errorf("body=%q, want BOOT", rr.Body.String())
	}
}

// TestBootstrap_LoginReachable: with Bootstrap wired, GET /login must reach
// the Bootstrap handler.
func TestBootstrap_LoginReachable(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{
		StartedAt:  time.Now(),
		SPAHandler: stubSPA,
		Bootstrap:  stubBootstrap,
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/login", http.NoBody))

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rr.Code)
	}
	if rr.Body.String() != "BOOT" {
		t.Errorf("body=%q, want BOOT", rr.Body.String())
	}
}

// TestBootstrap_SetupSubpathReachable: with Bootstrap wired, POST /setup/admin
// (a sub-path) must reach the Bootstrap handler.
func TestBootstrap_SetupSubpathReachable(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{
		StartedAt:  time.Now(),
		SPAHandler: stubSPA,
		Bootstrap:  stubBootstrap,
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/setup/admin", http.NoBody))

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rr.Code)
	}
	if rr.Body.String() != "BOOT" {
		t.Errorf("body=%q, want BOOT", rr.Body.String())
	}
}

// TestBootstrap_NilNotMounted: when Bootstrap is nil, GET /setup returns 404
// (not found from the chi router).
func TestBootstrap_NilNotMounted(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{
		StartedAt:  time.Now(),
		SPAHandler: stubSPA,
		// Bootstrap nil — routes not mounted
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/setup", http.NoBody))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404 when Bootstrap is nil", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Ingress-prefix-aware first-run redirect
// ---------------------------------------------------------------------------

// TestRootFirstRun_IngressPrefix: when NoUsers → true and the request carries
// X-Ingress-Path with a valid prefix, GET "/" must redirect 303 to
// <prefix>/setup.
func TestRootFirstRun_IngressPrefix(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{
		StartedAt:  time.Now(),
		SPAHandler: stubSPA,
		NoUsers:    noUsersTrue,
	})

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/abc")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d, want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/api/hassio_ingress/abc/setup" {
		t.Errorf("Location=%q, want /api/hassio_ingress/abc/setup", loc)
	}
}
