// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
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
	for range 2 {
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

	for range 3 {
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
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusOK)
			})
			handler := Idempotency()(inner)

			for range 2 {
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

	for range 2 {
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

	// Build a middleware that shares the test cache (and its
	// controllable clock) instead of Idempotency()'s private one.
	handler := idempotencyMiddleware(cache)(inner)

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

// TestIdempotency_DifferentUsersSameKeyDontLeak reproduces the
// cross-user replay leak: two different resolved identities sending
// the same method + path + Idempotency-Key must each execute the
// handler and receive their own response, never the other user's
// cached body.
func TestIdempotency_DifferentUsersSameKeyDontLeak(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		id, _ := auth.IdentityFrom(r.Context())
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(id.Subject + "-" + string('0'+n)))
	})
	handler := Idempotency()(inner)

	reqA := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	reqA.Header.Set("Idempotency-Key", "shared-key")
	reqA = reqA.WithContext(auth.ContextWithIdentity(reqA.Context(), auth.Identity{Subject: "alice"}))
	recA := httptest.NewRecorder()
	handler.ServeHTTP(recA, reqA)

	reqB := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	reqB.Header.Set("Idempotency-Key", "shared-key")
	reqB = reqB.WithContext(auth.ContextWithIdentity(reqB.Context(), auth.Identity{Subject: "bob"}))
	recB := httptest.NewRecorder()
	handler.ServeHTTP(recB, reqB)

	if calls.Load() != 2 {
		t.Fatalf("expected both users' requests to reach the handler, got %d calls", calls.Load())
	}
	if recA.Body.String() == recB.Body.String() {
		t.Fatalf("different users sharing an Idempotency-Key must not share a cached response: both got %q", recA.Body.String())
	}
	if recB.Header().Get("Idempotent-Replay") == "true" {
		t.Error("bob's request must not be served from alice's cache entry")
	}

	// Replay for the SAME user must still hit the cache.
	reqA2 := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	reqA2.Header.Set("Idempotency-Key", "shared-key")
	reqA2 = reqA2.WithContext(auth.ContextWithIdentity(reqA2.Context(), auth.Identity{Subject: "alice"}))
	recA2 := httptest.NewRecorder()
	handler.ServeHTTP(recA2, reqA2)

	if calls.Load() != 2 {
		t.Fatalf("expected alice's replay to be served from cache without a new handler call, got %d calls", calls.Load())
	}
	if recA2.Body.String() != recA.Body.String() {
		t.Errorf("alice's replay should return her own cached body, got %q want %q", recA2.Body.String(), recA.Body.String())
	}
}

// TestIdempotency_ConcurrentSameKeyRejectsInFlightDuplicate covers
// the in-flight race: two concurrent requests for the same key must
// not both reach the handler. The second arrival (while the first is
// still running) gets 409 Conflict rather than double-executing the
// mutation.
func TestIdempotency_ConcurrentSameKeyRejectsInFlightDuplicate(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	release := make(chan struct{})
	entered := make(chan struct{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		<-release
		w.WriteHeader(http.StatusCreated)
	})
	handler := Idempotency()(inner)

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
		req.Header.Set("Idempotency-Key", "key-inflight")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		statuses[0] = rec.Code
	}()

	<-entered // first request is now blocked inside the handler

	req2 := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	req2.Header.Set("Idempotency-Key", "key-inflight")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	statuses[1] = rec2.Code

	close(release)
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("expected the handler to run exactly once for the in-flight duplicate, got %d calls", calls.Load())
	}
	if statuses[1] != http.StatusConflict {
		t.Errorf("expected the concurrent duplicate to get 409 Conflict, got %d", statuses[1])
	}
	if statuses[0] != http.StatusCreated {
		t.Errorf("expected the original in-flight request to complete normally, got %d", statuses[0])
	}
}

// TestIdempotency_PanicReleasesReservation ensures a handler panic
// does not permanently wedge the key: [Idempotency] recovers nothing
// itself (that's [middleware.Recover]'s job higher in the chain), but
// it must release its pending reservation on unwind so a retry after
// the panic is handled — not permanently 409ed.
func TestIdempotency_PanicReleasesReservation(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		panic("boom")
	})
	handler := Idempotency()(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	req.Header.Set("Idempotency-Key", "key-panic")
	rec := httptest.NewRecorder()

	func() {
		defer func() { _ = recover() }()
		handler.ServeHTTP(rec, req)
	}()

	// Retry after the panic must reach the handler again, not 409.
	req2 := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	req2.Header.Set("Idempotency-Key", "key-panic")
	rec2 := httptest.NewRecorder()
	func() {
		defer func() { _ = recover() }()
		handler.ServeHTTP(rec2, req2)
	}()

	if calls.Load() != 2 {
		t.Fatalf("expected the retry after a panic to reach the handler, got %d calls", calls.Load())
	}
}

// idempotencyKeyRequest builds a mutating request carrying key.
func idempotencyKeyRequest(key string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/devices", http.NoBody)
	req.Header.Set("Idempotency-Key", key)
	return req
}

// TestIdempotency_ExpiredEntriesAreReclaimed pins that a flood of
// distinct keys does not grow the table without limit. The header value
// is client-chosen, so a caller minting a fresh UUID per request never
// re-reads a key — nothing used to delete one either, and every cached
// response body stayed live for the life of the process.
func TestIdempotency_ExpiredEntriesAreReclaimed(t *testing.T) {
	t.Parallel()

	now := time.Now()
	cache := newIdempotencyCache()
	cache.now = func() time.Time { return now }
	handler := idempotencyMiddleware(cache)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"xyz"}`))
	}))

	for i := range idempotencyCacheCap {
		handler.ServeHTTP(httptest.NewRecorder(), idempotencyKeyRequest("key-"+strconv.Itoa(i)))
	}
	if got := len(cache.items); got != idempotencyCacheCap {
		t.Fatalf("cache size = %d, want %d before any entry expires", got, idempotencyCacheCap)
	}

	// Everything cached so far is now unreplayable; the next key must
	// reclaim rather than grow the table.
	cache.now = func() time.Time { return now.Add(IdempotencyTTL + time.Second) }
	handler.ServeHTTP(httptest.NewRecorder(), idempotencyKeyRequest("key-after-ttl"))

	if got := len(cache.items); got != 1 {
		t.Fatalf("cache size = %d, want 1 after the expired entries were reclaimed", got)
	}
}

// TestIdempotency_FullCacheRunsUncached pins the flood case where every
// slot is still fresh: the request executes, no slot is stolen from an
// entry that could still serve a replay, and the table stays at its cap.
func TestIdempotency_FullCacheRunsUncached(t *testing.T) {
	t.Parallel()

	now := time.Now()
	cache := newIdempotencyCache()
	cache.now = func() time.Time { return now }
	var calls atomic.Int32
	handler := idempotencyMiddleware(cache)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))

	for i := range idempotencyCacheCap {
		handler.ServeHTTP(httptest.NewRecorder(), idempotencyKeyRequest("fresh-"+strconv.Itoa(i)))
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, idempotencyKeyRequest("overflow"))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 — an unrecordable request must still run", rec.Code)
	}
	if got := calls.Load(); got != idempotencyCacheCap+1 {
		t.Fatalf("handler calls = %d, want %d", got, idempotencyCacheCap+1)
	}
	if got := len(cache.items); got > idempotencyCacheCap {
		t.Fatalf("cache size = %d, want <= %d", got, idempotencyCacheCap)
	}
}

// TestIdempotency_UnauthorizedIsNotCached pins that a response the
// request never mutated anything for does not claim a slot. Without it a
// pre-auth flood fills the table one 401 at a time.
func TestIdempotency_UnauthorizedIsNotCached(t *testing.T) {
	t.Parallel()

	cache := newIdempotencyCache()
	var calls atomic.Int32
	handler := idempotencyMiddleware(cache)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), idempotencyKeyRequest("denied"))
	if got := len(cache.items); got != 0 {
		t.Fatalf("cache size = %d, want 0 after a 401", got)
	}

	// The retry must re-run rather than 409 against a stale reservation.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, idempotencyKeyRequest("denied"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("retry status = %d, want 401", rec.Code)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}
}

// TestIdempotency_OversizedResponseIsNotCached pins the per-entry size
// ceiling: one big mutation response must not dominate the table.
func TestIdempotency_OversizedResponseIsNotCached(t *testing.T) {
	t.Parallel()

	cache := newIdempotencyCache()
	body := bytes.Repeat([]byte("x"), idempotencyBodyLimit+1)
	handler := idempotencyMiddleware(cache)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), idempotencyKeyRequest("big"))

	if got := len(cache.items); got != 0 {
		t.Fatalf("cache size = %d, want 0 for an oversized response", got)
	}
}

// TestIdempotency_CredentialResponseIsNotCached pins that a response minting
// a cookie never becomes a replayable artefact.
//
// The Idempotency-Key is caller-chosen and the cache key folds in a subject
// that is empty on the anonymous routes — POST /auth/login among them — so a
// stored Set-Cookie would be handed verbatim to whoever presents the same key
// within the TTL, authenticating them as the first caller. The retry must
// re-run the login instead.
func TestIdempotency_CredentialResponseIsNotCached(t *testing.T) {
	t.Parallel()

	cache := newIdempotencyCache()
	var calls atomic.Int32
	handler := idempotencyMiddleware(cache)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "sid-" + strconv.Itoa(int(n)), Path: "/"})
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, idempotencyKeyRequest("login-key"))
	if got := first.Result().Cookies(); len(got) != 1 || got[0].Value != "sid-1" {
		t.Fatalf("first response cookies = %v, want the freshly minted session", got)
	}
	if got := len(cache.items); got != 0 {
		t.Fatalf("cache size = %d, want 0 for a response carrying Set-Cookie", got)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, idempotencyKeyRequest("login-key"))
	if second.Header().Get("Idempotent-Replay") != "" {
		t.Fatal("a second caller replayed the cached credential response")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls = %d, want 2 — the retry must re-authenticate", got)
	}
	if got := second.Result().Cookies(); len(got) != 1 || got[0].Value != "sid-2" {
		t.Fatalf("second response cookies = %v, want its own freshly minted session", got)
	}
}
