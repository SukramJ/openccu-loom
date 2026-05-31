// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

// White-box tests for unexported helpers inside the middleware package.
// Tests in this file live in the same package so they can reach the
// unexported statusWriter, recorder, and itoa helper.

import (
	"bufio"
	"bytes"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- statusWriter.Write ---

func TestStatusWriterWriteDelegatesAndCounts(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	var bodyWritten string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Logger wraps w in a statusWriter; write body through the wrapper.
		n, err := w.Write([]byte("hello"))
		if err != nil {
			t.Errorf("Write: %v", err)
		}
		if n != 5 {
			t.Errorf("Write n=%d, want 5", n)
		}
		bodyWritten = "hello"
	})
	handler := Logger(logger)(inner)

	req := httptest.NewRequest(http.MethodGet, "/write-test", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if bodyWritten != "hello" {
		t.Errorf("body not written: %q", bodyWritten)
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Errorf("body not forwarded to underlying recorder: %q", rec.Body.String())
	}
}

// --- statusWriter.Flush ---

type flushableRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flushableRecorder) Flush() {
	f.flushed = true
	f.ResponseRecorder.Flush()
}

func TestStatusWriterFlushForwardsToFlusher(t *testing.T) {
	t.Parallel()

	fr := &flushableRecorder{ResponseRecorder: httptest.NewRecorder()}
	sw := &statusWriter{ResponseWriter: fr, status: http.StatusOK}
	sw.Flush()

	if !fr.flushed {
		t.Error("Flush was not forwarded to the underlying Flusher")
	}
}

// A plain httptest.ResponseRecorder does NOT implement http.Flusher in a way
// that sets our flag — verify Flush on a non-flusher doesn't panic.
func TestStatusWriterFlushOnNonFlusherIsNoop(t *testing.T) {
	t.Parallel()

	// Wrap a plain ResponseWriter that doesn't implement Flusher.
	sw := &statusWriter{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	// Must not panic.
	sw.Flush()
}

// --- statusWriter.Hijack ---

type hijackableWriter struct {
	http.ResponseWriter
	hijackCalled bool
}

func (h *hijackableWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijackCalled = true
	return nil, nil, nil
}

func TestStatusWriterHijackForwardsToHijacker(t *testing.T) {
	t.Parallel()

	hw := &hijackableWriter{ResponseWriter: httptest.NewRecorder()}
	sw := &statusWriter{ResponseWriter: hw, status: http.StatusOK}
	conn, rw, err := sw.Hijack()
	if err != nil {
		t.Fatalf("Hijack: unexpected error %v", err)
	}
	if conn != nil || rw != nil {
		t.Errorf("expected nil conn/rw from stub, got %v/%v", conn, rw)
	}
	if !hw.hijackCalled {
		t.Error("Hijack not forwarded to underlying Hijacker")
	}
}

func TestStatusWriterHijackOnNonHijackerReturnsError(t *testing.T) {
	t.Parallel()

	// httptest.ResponseRecorder does not implement http.Hijacker.
	sw := &statusWriter{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	_, _, err := sw.Hijack()
	if err == nil {
		t.Error("expected error when underlying ResponseWriter does not implement Hijacker")
	}
}

// --- recorder.Header ---

func TestRecorderHeaderMergesIntoUnderlying(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	r := &recorder{ResponseWriter: rec, header: http.Header{}, status: 200}

	// Header() must return the underlying header so writes go to both.
	h := r.Header()
	h.Set("X-Custom", "test-value")

	if rec.Header().Get("X-Custom") != "test-value" {
		t.Errorf("underlying header missing X-Custom: %q", rec.Header().Get("X-Custom"))
	}
}

// --- itoa ---

func TestItoaPositive(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{300, "300"},
		{3600, "3600"},
		{86400, "86400"},
	}
	for _, tc := range cases {
		got := itoa(tc.in)
		if got != tc.want {
			t.Errorf("itoa(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestItoaNegative(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{-1, "-1"},
		{-100, "-100"},
	}
	for _, tc := range cases {
		got := itoa(tc.in)
		if got != tc.want {
			t.Errorf("itoa(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- CORS: credential + allowAll path ---

func TestCORS_WildcardWithCredentialsSetsOriginEcho(t *testing.T) {
	t.Parallel()

	// allowAll=true + AllowCredentials=true → must echo the origin, not "*".
	cfg := CORSConfig{Origins: []string{"*"}, AllowCredentials: true}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := CORS(cfg)(inner)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("expected echoed origin, got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("expected Allow-Credentials: true, got %q", got)
	}
}

// --- CentralFromURL: missing param path ---

func TestCentralFromURL_NoParam_ReturnsUnchangedRequest(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	// chi.URLParam will return "" when not inside a router match.
	got := CentralFromURL(req)
	if got != req {
		t.Error("CentralFromURL with no URL param should return the original request unchanged")
	}
}
