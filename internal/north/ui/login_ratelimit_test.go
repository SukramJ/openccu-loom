// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for the per-IP login brute-force speed-bump: burst then throttle,
// per-IP isolation, RemoteAddr keying, and the guard's redirect-on-overflow.
package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginRateLimiterBurstThenBlocks(t *testing.T) {
	t.Parallel()
	rl := newLoginRateLimiter(3) // burst 3, slow (1/s) refill
	ip := "198.51.100.4"
	for i := range 3 {
		if ok, _ := rl.allow(ip); !ok {
			t.Fatalf("request %d within burst was blocked", i+1)
		}
	}
	ok, retry := rl.allow(ip)
	if ok {
		t.Fatal("request past the burst was allowed")
	}
	if retry < 1 {
		t.Errorf("retryAfter=%d, want >=1", retry)
	}
}

func TestLoginRateLimiterPerIPIsolation(t *testing.T) {
	t.Parallel()
	rl := newLoginRateLimiter(1)
	if ok, _ := rl.allow("a"); !ok {
		t.Fatal("first IP-A request must be allowed")
	}
	if ok, _ := rl.allow("a"); ok {
		t.Fatal("second IP-A request should be blocked")
	}
	if ok, _ := rl.allow("b"); !ok {
		t.Fatal("IP-B must have its own independent bucket")
	}
}

func TestClientIP(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"1.2.3.4:5678":      "1.2.3.4",
		"[2001:db8::1]:443": "2001:db8::1",
		"noport":            "noport",
	}
	for in, want := range cases {
		r := httptest.NewRequest(http.MethodPost, "/login", http.NoBody)
		r.RemoteAddr = in
		if got := clientIP(r); got != want {
			t.Errorf("clientIP(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestLoginRateLimiterGuardRedirectsOnOverflow(t *testing.T) {
	t.Parallel()
	rl := newLoginRateLimiter(1) // burst 1 — second request throttled
	var calls int
	h := rl.guard(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})

	fire := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/login", http.NoBody)
		req.RemoteAddr = "203.0.113.7:5000"
		h(rec, req)
		return rec
	}

	if rec := fire(); rec.Code != http.StatusOK {
		t.Fatalf("first request: code=%d, want 200", rec.Code)
	}
	rec := fire()
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("throttled request: code=%d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login?error=1" {
		t.Errorf("redirect Location=%q, want /login?error=1", loc)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("throttled response is missing the Retry-After header")
	}
	if calls != 1 {
		t.Errorf("downstream handler ran %d times, want 1 (second call throttled)", calls)
	}
}
