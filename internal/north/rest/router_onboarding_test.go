// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

// ---------------------------------------------------------------------------
// Root "/" redirect tests
// ---------------------------------------------------------------------------

// TestRoot_RedirectsToSPA: GET "/" must always redirect 302 to /app/ regardless
// of first-run state or the presence of a credential.
func TestRoot_RedirectsToSPA(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{
		StartedAt:  time.Now(),
		SPAHandler: stubSPA,
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

// TestRootFirstRun_IngressPrefix: when the request carries X-Ingress-Path with
// a valid prefix, GET "/" must redirect 302 to <prefix>/app/.
func TestRootFirstRun_IngressPrefix(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{
		StartedAt:  time.Now(),
		SPAHandler: stubSPA,
	})

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/abc")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status=%d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/api/hassio_ingress/abc/app/" {
		t.Errorf("Location=%q, want /api/hassio_ingress/abc/app/", loc)
	}
}

// ---------------------------------------------------------------------------
// Bootstrap handler mount tests
// ---------------------------------------------------------------------------

// TestBootstrap_AboutReachable: with Bootstrap wired, GET /about must reach
// the Bootstrap handler (200 "BOOT"), not the REST 404 handler.
func TestBootstrap_AboutReachable(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{
		StartedAt:  time.Now(),
		SPAHandler: stubSPA,
		Bootstrap:  stubBootstrap,
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/about", http.NoBody))

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rr.Code)
	}
	if rr.Body.String() != "BOOT" {
		t.Errorf("body=%q, want BOOT", rr.Body.String())
	}
}

// TestBootstrap_HealthReachable: with Bootstrap wired, GET /health must reach
// the Bootstrap handler.
func TestBootstrap_HealthReachable(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{
		StartedAt:  time.Now(),
		SPAHandler: stubSPA,
		Bootstrap:  stubBootstrap,
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", http.NoBody))

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
