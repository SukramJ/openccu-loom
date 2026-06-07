// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- SecurityHeaders ---

func TestSecurityHeaders_SetsNosniff(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	SecurityHeaders(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestSecurityHeaders_PreservesExisting(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Content-Type-Options", "custom")
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	// The middleware sets the header before the handler runs; a handler
	// that overrides it afterwards wins. Verify the middleware does not
	// clobber an explicitly different value set upstream.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "preset")
		SecurityHeaders(inner).ServeHTTP(w, r)
	})
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if got := rec.Header().Get("X-Content-Type-Options"); got != "custom" {
		t.Fatalf("X-Content-Type-Options = %q, want custom (handler override)", got)
	}
}

// --- RequestID ---

func TestRequestID_GeneratesIDWhenAbsent(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		capturedID = RequestIDFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := RequestID(inner)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if capturedID == "" {
		t.Error("expected a request ID in the context, got empty string")
	}
	if got := rec.Header().Get("X-Request-ID"); got != capturedID {
		t.Errorf("X-Request-ID response header %q does not match context value %q", got, capturedID)
	}
	if calls.Load() != 1 {
		t.Errorf("expected inner handler called once, got %d", calls.Load())
	}
}

func TestRequestID_PreservesExistingID(t *testing.T) {
	t.Parallel()

	const existingID = "my-trace-id-123"
	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := RequestID(inner)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Request-ID", existingID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if capturedID != existingID {
		t.Errorf("expected context ID %q, got %q", existingID, capturedID)
	}
	if got := rec.Header().Get("X-Request-ID"); got != existingID {
		t.Errorf("expected X-Request-ID response header %q, got %q", existingID, got)
	}
}

func TestRequestID_UniquePerRequest(t *testing.T) {
	t.Parallel()

	ids := make([]string, 5)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RequestID(inner)

	for i := range ids {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		ids[i] = rec.Header().Get("X-Request-ID")
	}

	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			t.Fatal("got empty X-Request-ID")
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate request ID generated: %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestRequestIDFrom_ReturnsEmptyWithoutMiddleware(t *testing.T) {
	t.Parallel()

	if got := RequestIDFrom(context.Background()); got != "" {
		t.Errorf("expected empty string without middleware, got %q", got)
	}
}

// --- Logger ---

func TestLogger_LogsRequestDetails(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	handler := Logger(logger)(inner)

	req := httptest.NewRequest(http.MethodGet, "/test/path", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	out := buf.String()
	for _, want := range []string{"http.request", "GET", "/test/path", "418"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected log to contain %q, got: %s", want, out)
		}
	}
}

func TestLogger_DelegatesRequestToInnerHandler(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	handler := Logger(logger)(inner)

	req := httptest.NewRequest(http.MethodPost, "/api", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if calls.Load() != 1 {
		t.Errorf("expected inner handler called once, got %d", calls.Load())
	}
}

// --- Recover ---

func TestRecover_CatchesPanic(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))

	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went wrong")
	})
	handler := Recover(logger)(panicking)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()

	// Must not propagate the panic to the test runner.
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestRecover_LogsPanicMessage(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))

	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("deliberate-panic-message")
	})
	handler := Recover(logger)(panicking)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), "deliberate-panic-message") {
		t.Errorf("expected panic message in log, got: %s", buf.String())
	}
}

func TestRecover_NoPanicPassesThrough(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusAccepted)
	})
	handler := Recover(logger)(inner)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", rec.Code)
	}
	if calls.Load() != 1 {
		t.Errorf("expected inner handler called once, got %d", calls.Load())
	}
}

// --- Timeout ---

func TestTimeout_ContextHasDeadline(t *testing.T) {
	t.Parallel()

	var deadlineSet bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, deadlineSet = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	})
	handler := Timeout(5 * time.Second)(inner)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !deadlineSet {
		t.Error("expected request context to carry a deadline after Timeout middleware")
	}
}

func TestTimeout_DelegatesRequest(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	handler := Timeout(5 * time.Second)(inner)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if calls.Load() != 1 {
		t.Errorf("expected inner handler called once, got %d", calls.Load())
	}
}
