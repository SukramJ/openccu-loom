// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

func passthrough(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func TestRateLimit_AllowsWithinBurst(t *testing.T) {
	t.Parallel()
	mw := RateLimit(RateLimitConfig{RequestsPerSecond: 1, Burst: 3})
	h := mw(http.HandlerFunc(passthrough))
	for i := range 3 {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", http.NoBody)
		req = req.WithContext(auth.ContextWithIdentity(req.Context(), auth.Identity{Subject: "alice"}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("burst request %d got %d, want 200", i, w.Code)
		}
	}
}

func TestRateLimit_429AfterBurst(t *testing.T) {
	t.Parallel()
	mw := RateLimit(RateLimitConfig{RequestsPerSecond: 0.001, Burst: 1})
	h := mw(http.HandlerFunc(passthrough))
	identity := auth.Identity{Subject: "alice"}
	// First call consumes the bucket.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", http.NoBody)
	req = req.WithContext(auth.ContextWithIdentity(req.Context(), identity))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first request: %d", w.Code)
	}
	// Second call exceeds → 429.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/devices", http.NoBody)
	req2 = req2.WithContext(auth.ContextWithIdentity(req2.Context(), identity))
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: %d, want 429; body=%s", w2.Code, w2.Body.String())
	}
	if w2.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header missing")
	}
	if n, err := strconv.Atoi(w2.Header().Get("Retry-After")); err != nil || n < 1 {
		t.Fatalf("Retry-After = %q, want >= 1", w2.Header().Get("Retry-After"))
	}
}

func TestRateLimit_PerIdentityIsolation(t *testing.T) {
	t.Parallel()
	mw := RateLimit(RateLimitConfig{RequestsPerSecond: 0.001, Burst: 1})
	h := mw(http.HandlerFunc(passthrough))
	for _, subject := range []string{"alice", "bob", "carol"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", http.NoBody)
		req = req.WithContext(auth.ContextWithIdentity(req.Context(), auth.Identity{Subject: subject}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s got %d, want 200 (each identity has its own bucket)", subject, w.Code)
		}
	}
}

func TestRateLimit_SkipPaths_BypassLimiter(t *testing.T) {
	t.Parallel()
	mw := RateLimit(RateLimitConfig{RequestsPerSecond: 0.001, Burst: 1})
	h := mw(http.HandlerFunc(passthrough))
	// /info bypasses the limiter — any number of requests succeed.
	for i := range 5 {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("/info request %d got %d, want 200", i, w.Code)
		}
	}
}

func TestRateLimit_AnonymousSharesBucket(t *testing.T) {
	t.Parallel()
	mw := RateLimit(RateLimitConfig{RequestsPerSecond: 0.001, Burst: 1})
	h := mw(http.HandlerFunc(passthrough))
	// Two unauthenticated callers share the "anonymous" bucket.
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/api/v1/devices", http.NoBody))
	if w1.Code != http.StatusOK {
		t.Fatalf("first anonymous: %d", w1.Code)
	}
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/v1/devices", http.NoBody))
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second anonymous should 429, got %d", w2.Code)
	}
}

func TestRateLimitConfig_Defaults(t *testing.T) {
	t.Parallel()
	eff := RateLimitConfig{}.Effective()
	if eff.RequestsPerSecond != 10 {
		t.Fatalf("rps default = %v", eff.RequestsPerSecond)
	}
	if eff.Burst != 30 {
		t.Fatalf("burst default = %d", eff.Burst)
	}
	if len(eff.SkipPaths) == 0 {
		t.Fatal("SkipPaths default must include at least one entry")
	}
}

func TestRateLimit_NilRequestKeyedAnonymous(t *testing.T) {
	t.Parallel()
	// identityKey explicitly tolerates nil *http.Request — the path
	// exists for defence-in-depth even though chi never delivers a
	// nil request to a middleware in practice.
	if got := identityKey(nil); got != "anonymous" {
		t.Fatalf("identityKey(nil) = %q, want anonymous", got)
	}
}

func TestLimiterStore_IdleEvictionGCs(t *testing.T) {
	t.Parallel()
	// Fill the table to its capacity so the next get() has to reclaim,
	// then backdate every existing entry past rateLimitIdleTTL so they
	// all qualify for eviction.
	store := newKeyedLimiterStore(rate.Limit(10), 30, rateLimitStoreCap, rateLimitIdleTTL)
	for i := range rateLimitStoreCap {
		key := "id-" + strconv.Itoa(i)
		store.get(key)
	}
	// Backdate all stored entries so the next get's GC sweep evicts
	// them. Direct field access works inside the package.
	store.mu.Lock()
	for _, e := range store.buckets {
		e.lastUse = time.Now().Add(-2 * rateLimitIdleTTL)
	}
	store.mu.Unlock()

	// One more get triggers the reclaim pass — the map should shrink
	// significantly once it removes the backdated entries.
	store.get("fresh")

	store.mu.Lock()
	size := len(store.buckets)
	store.mu.Unlock()
	if size > 5 {
		t.Fatalf("expected GC to evict idle entries; map size = %d", size)
	}
}
