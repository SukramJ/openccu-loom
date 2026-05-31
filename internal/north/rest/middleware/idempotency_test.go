// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestIdempotency_FreshKeyFlowsThrough(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	})
	handler := Idempotency()(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	req.Header.Set("Idempotency-Key", "key-001")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rec.Code)
	}
	if calls.Load() != 1 {
		t.Errorf("expected handler called once, got %d", calls.Load())
	}
}

func TestIdempotency_RepeatKeyReturnsCachedResponse(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	})
	handler := Idempotency()(inner)

	// First request — populates cache.
	req1 := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	req1.Header.Set("Idempotency-Key", "key-repeat")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	// Second request with the same key — must NOT call inner again.
	req2 := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	req2.Header.Set("Idempotency-Key", "key-repeat")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if calls.Load() != 1 {
		t.Errorf("expected inner handler called exactly once, got %d", calls.Load())
	}
	if rec2.Code != http.StatusCreated {
		t.Errorf("expected cached status 201, got %d", rec2.Code)
	}
	if rec2.Body.String() != `{"id":"abc"}` {
		t.Errorf("expected cached body, got %q", rec2.Body.String())
	}
	if rec2.Header().Get("Idempotent-Replay") != "true" {
		t.Errorf("expected Idempotent-Replay: true on cached response")
	}
}

func TestIdempotency_DifferentKeysDontCollide(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	counter := atomic.Int32{}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		n := counter.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte{byte('0' + n)}) //nolint:gosec // G115: n is a small sequential counter; '0'+n stays within ASCII digit range for test invocations
	})
	handler := Idempotency()(inner)

	req1 := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	req1.Header.Set("Idempotency-Key", "key-A")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	req2 := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	req2.Header.Set("Idempotency-Key", "key-B")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if calls.Load() != 2 {
		t.Errorf("expected 2 handler invocations for 2 distinct keys, got %d", calls.Load())
	}
	if rec1.Body.String() == rec2.Body.String() {
		t.Errorf("different keys should not share a cached response: both returned %q", rec1.Body.String())
	}
}

func TestIdempotency_NoKeyFlowsThrough(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	handler := Idempotency()(inner)

	// Two requests without any Idempotency-Key header.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	if calls.Load() != 2 {
		t.Errorf("expected 2 handler invocations when no key present, got %d", calls.Load())
	}
}

func TestIdempotency_GetBypassesCache(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	handler := Idempotency()(inner)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/devices", http.NoBody)
		req.Header.Set("Idempotency-Key", "key-get")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	if calls.Load() != 3 {
		t.Errorf("GET requests must always reach the inner handler; got %d calls instead of 3", calls.Load())
	}
}

func TestIdempotency_PutAndPatchAreCached(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		method := method
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusOK)
			})
			handler := Idempotency()(inner)

			for i := 0; i < 2; i++ {
				req := httptest.NewRequest(method, "/api/devices/1", http.NoBody)
				req.Header.Set("Idempotency-Key", "key-"+method)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
			}

			if calls.Load() != 1 {
				t.Errorf("%s: expected 1 handler invocation, got %d", method, calls.Load())
			}
		})
	}
}

func TestIdempotency_DeleteIsCached(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	handler := Idempotency()(inner)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodDelete, "/api/devices/42", http.NoBody)
		req.Header.Set("Idempotency-Key", "key-del")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	if calls.Load() != 1 {
		t.Errorf("DELETE: expected 1 handler invocation, got %d", calls.Load())
	}
}

func TestIdempotency_SameKeyDifferentPathsAreDistinct(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
	})
	handler := Idempotency()(inner)

	paths := []string{"/api/devices", "/api/channels"}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodPost, p, http.NoBody)
		req.Header.Set("Idempotency-Key", "shared-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	if calls.Load() != 2 {
		t.Errorf("same key on different paths should be distinct cache entries, got %d calls", calls.Load())
	}
}

func TestIdempotency_TTLEviction(t *testing.T) {
	t.Parallel()

	// Directly manipulate the cache's clock to simulate TTL expiry.
	now := time.Now()
	cache := newIdempotencyCache()
	cache.now = func() time.Time { return now }

	var calls atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"xyz"}`))
	})

	// Build a middleware that uses the test cache.
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMutating(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			id := cacheKey(r.Method, r.URL.Path, key)
			if entry, ok := cache.get(id); ok {
				w.Header().Set("Idempotent-Replay", "true")
				for k, vs := range entry.header {
					for _, v := range vs {
						w.Header().Add(k, v)
					}
				}
				w.WriteHeader(entry.status)
				_, _ = w.Write(entry.body)
				return
			}
			rec := &recorder{ResponseWriter: w, header: http.Header{}, status: 200}
			next.ServeHTTP(rec, r)
			cache.put(id, rec.snapshot())
		})
	}

	handler := mw(inner)

	// First request at time T.
	req1 := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	req1.Header.Set("Idempotency-Key", "key-ttl")
	handler.ServeHTTP(httptest.NewRecorder(), req1)

	// Second request: still within TTL → cached.
	req2 := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	req2.Header.Set("Idempotency-Key", "key-ttl")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Header().Get("Idempotent-Replay") != "true" {
		t.Error("expected cached hit within TTL")
	}
	if calls.Load() != 1 {
		t.Errorf("expected 1 call within TTL, got %d", calls.Load())
	}

	// Advance clock beyond TTL.
	cache.now = func() time.Time { return now.Add(IdempotencyTTL + time.Second) }

	// Third request: TTL expired → handler must be called again.
	req3 := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	req3.Header.Set("Idempotency-Key", "key-ttl")
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	if rec3.Header().Get("Idempotent-Replay") == "true" {
		t.Error("expected cache miss after TTL expiry")
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 total calls after TTL expiry, got %d", calls.Load())
	}
}

func TestIdempotency_FirstReplyNotTaggedAsReplay(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
	})
	handler := Idempotency()(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	req.Header.Set("Idempotency-Key", "key-first")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Idempotent-Replay"); got != "" {
		t.Errorf("first response must not carry Idempotent-Replay header, got %q", got)
	}
}
