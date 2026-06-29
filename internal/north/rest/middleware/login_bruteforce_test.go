// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// TestLoginRateLimiter_BurstThenBlock verifies that the first loginRateBurst
// requests from a single source IP all pass (HTTP 200) and that the very next
// request is throttled (HTTP 429) with a Retry-After header and a
// problem+json body carrying code "rate_limited".
func TestLoginRateLimiter_BurstThenBlock(t *testing.T) {
	t.Parallel()
	lim := NewLoginRateLimiter()
	h := lim.Middleware()(http.HandlerFunc(passthrough))
	const ip = "1.2.3.4:1234"

	for i := range loginRateBurst {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", http.NoBody)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i+1, w.Code)
		}
	}

	// The (burst+1)-th request must be throttled.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", http.NoBody)
	req.RemoteAddr = ip
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("throttled request %d: got %d body=%s, want 429", loginRateBurst+1, w.Code, w.Body.String())
	}
	if retryAfter := w.Header().Get("Retry-After"); retryAfter == "" {
		t.Error("Retry-After header missing on 429 response")
	} else if n, err := strconv.Atoi(retryAfter); err != nil || n < 1 {
		t.Errorf("Retry-After = %q, want integer >= 1", retryAfter)
	}

	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal 429 body: %v", err)
	}
	if body.Code != "rate_limited" {
		t.Errorf("problem code = %q, want rate_limited", body.Code)
	}
}

// TestLoginRateLimiter_IPBucketIsolation verifies that two different source
// IPs maintain independent rate-limit buckets: exhausting IP A's burst does
// not consume any tokens from IP B's bucket.
func TestLoginRateLimiter_IPBucketIsolation(t *testing.T) {
	t.Parallel()
	lim := NewLoginRateLimiter()
	h := lim.Middleware()(http.HandlerFunc(passthrough))

	const ipA = "1.2.3.4:1111"
	const ipB = "5.6.7.8:2222"

	// Exhaust IP A's burst allowance.
	for i := range loginRateBurst {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", http.NoBody)
		req.RemoteAddr = ipA
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("IP A request %d: got %d, want 200", i+1, w.Code)
		}
	}

	// Confirm IP A is now rate-limited.
	reqA := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", http.NoBody)
	reqA.RemoteAddr = ipA
	wA := httptest.NewRecorder()
	h.ServeHTTP(wA, reqA)
	if wA.Code != http.StatusTooManyRequests {
		t.Fatalf("IP A overflow: got %d, want 429", wA.Code)
	}

	// IP B must still have a full bucket and pass through.
	reqB := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", http.NoBody)
	reqB.RemoteAddr = ipB
	wB := httptest.NewRecorder()
	h.ServeHTTP(wB, reqB)
	if wB.Code != http.StatusOK {
		t.Fatalf("IP B (separate bucket): got %d body=%s, want 200", wB.Code, wB.Body.String())
	}
}
