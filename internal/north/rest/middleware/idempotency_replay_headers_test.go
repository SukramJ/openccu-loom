// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIdempotencyReplayDoesNotDuplicateOuterHeaders pins that a replayed
// response carries the *current* request's infrastructure headers exactly
// once. The recorder must snapshot only what the wrapped handler itself
// contributed: X-Request-ID and X-Content-Type-Options are stamped by the
// middlewares mounted outside Idempotency, so replaying a cached copy of
// them would append a second, stale value to each.
func TestIdempotencyReplayDoesNotDuplicateOuterHeaders(t *testing.T) {
	chain := RequestID(SecurityHeaders(idempotencyMiddleware(newIdempotencyCache())(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))))

	do := func(reqID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/ABC/paramsets/VALUES", http.NoBody)
		req.Header.Set("Idempotency-Key", "key-1")
		req.Header.Set("X-Request-ID", reqID)
		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, req)
		return rec
	}

	first := do("req-first")
	if got := first.Code; got != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", got)
	}

	second := do("req-second")
	if got := second.Header().Get("Idempotent-Replay"); got != "true" {
		t.Fatalf("Idempotent-Replay = %q, want true", got)
	}
	if got := second.Body.String(); got != `{"ok":true}` {
		t.Fatalf("replayed body = %q", got)
	}
	if got := second.Header().Values("X-Request-ID"); len(got) != 1 || got[0] != "req-second" {
		t.Errorf("X-Request-ID = %v, want exactly [req-second]", got)
	}
	if got := second.Header().Values("X-Content-Type-Options"); len(got) != 1 {
		t.Errorf("X-Content-Type-Options = %v, want exactly one value", got)
	}
	if got := second.Header().Values("Content-Type"); len(got) != 1 || got[0] != "application/json" {
		t.Errorf("Content-Type = %v, want exactly [application/json]", got)
	}
}
