// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
)

// TestRouter_RateLimitIsScopedToTheAPI pins that the per-identity request
// limiter covers /api/v1 only. The SPA mount (/app/*) and the no-JS
// diagnostic surface (/health, /about) are served unauthenticated, so
// every browser and every external monitor draws from the one shared
// "anonymous" bucket; with the limiter on the whole mux a single
// unauthenticated source at >10 rps turns other users' login-page assets
// and the documented /health probe into 429s. The API leg is the negative
// control: the same drained bucket still refuses an API request.
func TestRouter_RateLimitIsScopedToTheAPI(t *testing.T) {
	t.Parallel()
	served := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r := NewRouter(Deps{
		StartedAt:  time.Now(),
		RateLimit:  &middleware.RateLimitConfig{RequestsPerSecond: 1, Burst: 1},
		SPAHandler: served,
		Bootstrap:  served,
	})
	get := func(path string) int {
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		return rr.Code
	}

	// Drain the anonymous bucket through the API.
	get("/api/v1/config")
	if code := get("/api/v1/config"); code != http.StatusTooManyRequests {
		t.Fatalf("second /api/v1/config = %d, want 429 (negative control: the limiter must still bite on the API)", code)
	}
	for _, path := range []string{"/health", "/about", "/app/", "/app/index.html", "/app/assets/index.js"} {
		if code := get(path); code == http.StatusTooManyRequests {
			t.Fatalf("GET %s = 429: the API limiter must not cover the SPA mount or the no-JS diagnostic surface", path)
		}
	}
}
