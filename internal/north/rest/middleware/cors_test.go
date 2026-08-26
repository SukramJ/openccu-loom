// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package middleware

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
)

// newCountingHandler returns a handler that increments calls on each invocation
// and writes the given status code + body.
func newCountingHandler(t *testing.T, calls *atomic.Int32, status int, body string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func TestCORS_AllowedOriginSetsHeader(t *testing.T) {
	t.Parallel()

	cfg := CORSConfig{Origins: []string{"https://example.com"}}
	var calls atomic.Int32
	handler := CORS(cfg)(newCountingHandler(t, &calls, http.StatusOK, "ok"))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("expected Access-Control-Allow-Origin = %q, got %q", "https://example.com", got)
	}
	if calls.Load() != 1 {
		t.Errorf("expected inner handler called once, got %d", calls.Load())
	}
}

func TestCORS_NonMatchingOriginNoHeader(t *testing.T) {
	t.Parallel()

	cfg := CORSConfig{Origins: []string{"https://example.com"}}
	var calls atomic.Int32
	handler := CORS(cfg)(newCountingHandler(t, &calls, http.StatusOK, "ok"))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Origin", "https://evil.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no Access-Control-Allow-Origin for unlisted origin, got %q", got)
	}
	// Inner handler must still be called (we only block the CORS headers, not the request).
	if calls.Load() != 1 {
		t.Errorf("expected inner handler called once, got %d", calls.Load())
	}
}

func TestCORS_NoOriginHeaderPassThrough(t *testing.T) {
	t.Parallel()

	cfg := CORSConfig{Origins: []string{"https://example.com"}}
	var calls atomic.Int32
	handler := CORS(cfg)(newCountingHandler(t, &calls, http.StatusOK, "ok"))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	// No Origin header at all.
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no CORS header without Origin, got %q", got)
	}
	if calls.Load() != 1 {
		t.Errorf("expected inner handler called once, got %d", calls.Load())
	}
}

func TestCORS_WildcardOriginAllowsAny(t *testing.T) {
	t.Parallel()

	cfg := CORSConfig{Origins: []string{"*"}}
	var calls atomic.Int32
	handler := CORS(cfg)(newCountingHandler(t, &calls, http.StatusOK, "ok"))

	for _, origin := range []string{"https://foo.example", "https://bar.io", "http://localhost:3000"} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.Header.Set("Origin", origin)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			// With wildcard and no credentials, the spec demands "Access-Control-Allow-Origin: *"
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Errorf("origin %s: expected *, got %q", origin, got)
			}
		})
	}
}

func TestCORS_WildcardWithCredentialsReflectsOrigin(t *testing.T) {
	t.Parallel()

	// When AllowCredentials is true, "*" must not be sent; the actual origin is reflected.
	cfg := CORSConfig{Origins: []string{"*"}, AllowCredentials: true}
	var calls atomic.Int32
	handler := CORS(cfg)(newCountingHandler(t, &calls, http.StatusOK, "ok"))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Origin", "https://myapp.example")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://myapp.example" {
		t.Errorf("expected reflected origin, got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("expected Access-Control-Allow-Credentials: true, got %q", got)
	}
}

func TestCORS_AllowCredentialsHeader(t *testing.T) {
	t.Parallel()

	cfg := CORSConfig{Origins: []string{"https://example.com"}, AllowCredentials: true}
	var calls atomic.Int32
	handler := CORS(cfg)(newCountingHandler(t, &calls, http.StatusOK, "ok"))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("expected Access-Control-Allow-Credentials: true, got %q", got)
	}
	// When reflecting the origin the Vary: Origin header must be present.
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("expected Vary: Origin, got %q", got)
	}
}

func TestCORS_VaryOriginHeader(t *testing.T) {
	t.Parallel()

	// Whitelisted specific origin → must add Vary: Origin so caches
	// know the response differs by origin.
	cfg := CORSConfig{Origins: []string{"https://example.com"}}
	var calls atomic.Int32
	handler := CORS(cfg)(newCountingHandler(t, &calls, http.StatusOK, "ok"))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("expected Vary: Origin, got %q", got)
	}
}

func TestCORS_PreflightReturns204(t *testing.T) {
	t.Parallel()

	cfg := CORSConfig{Origins: []string{"https://example.com"}}
	var calls atomic.Int32
	handler := CORS(cfg)(newCountingHandler(t, &calls, http.StatusOK, "ok"))

	req := httptest.NewRequest(http.MethodOptions, "/api/devices", http.NoBody)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 NoContent, got %d", rec.Code)
	}
	// Inner handler must NOT be invoked during preflight.
	if calls.Load() != 0 {
		t.Errorf("expected inner handler NOT called during preflight, got %d calls", calls.Load())
	}
}

func TestCORS_PreflightAllowedMethods(t *testing.T) {
	t.Parallel()

	cfg := CORSConfig{Origins: []string{"https://example.com"}}
	var calls atomic.Int32
	handler := CORS(cfg)(newCountingHandler(t, &calls, http.StatusOK, "ok"))

	req := httptest.NewRequest(http.MethodOptions, "/api/devices", http.NoBody)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	allowMethods := rec.Header().Get("Access-Control-Allow-Methods")
	if allowMethods == "" {
		t.Fatal("expected Access-Control-Allow-Methods to be set in preflight response")
	}
	// Check a few essential methods are listed.
	for _, m := range []string{"GET", "POST", "PUT", "DELETE"} {
		if !containsToken(allowMethods, m) {
			t.Errorf("expected %q in Access-Control-Allow-Methods, got %q", m, allowMethods)
		}
	}
}

func TestCORS_PreflightReflectsRequestHeaders(t *testing.T) {
	t.Parallel()

	cfg := CORSConfig{Origins: []string{"https://example.com"}}
	var calls atomic.Int32
	handler := CORS(cfg)(newCountingHandler(t, &calls, http.StatusOK, "ok"))

	req := httptest.NewRequest(http.MethodOptions, "/api/devices", http.NoBody)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, Authorization")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, Authorization" {
		t.Errorf("expected Access-Control-Allow-Headers to reflect request, got %q", got)
	}
}

func TestCORS_PreflightMaxAge(t *testing.T) {
	t.Parallel()

	cfg := CORSConfig{Origins: []string{"https://example.com"}, MaxAgeSeconds: 600}
	var calls atomic.Int32
	handler := CORS(cfg)(newCountingHandler(t, &calls, http.StatusOK, "ok"))

	req := httptest.NewRequest(http.MethodOptions, "/api/devices", http.NoBody)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("expected Access-Control-Max-Age = 600, got %q", got)
	}
}

func TestCORS_OptionsWithoutACRequestMethodIsNotPreflight(t *testing.T) {
	t.Parallel()

	// An OPTIONS request without Access-Control-Request-Method is not a
	// preflight; the inner handler must be invoked.
	cfg := CORSConfig{Origins: []string{"https://example.com"}}
	var calls atomic.Int32
	handler := CORS(cfg)(newCountingHandler(t, &calls, http.StatusOK, "ok"))

	req := httptest.NewRequest(http.MethodOptions, "/api/devices", http.NoBody)
	req.Header.Set("Origin", "https://example.com")
	// No Access-Control-Request-Method header.
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if calls.Load() != 1 {
		t.Errorf("expected inner handler called for non-preflight OPTIONS, got %d", calls.Load())
	}
}

func TestCORS_CaseInsensitiveOriginMatching(t *testing.T) {
	t.Parallel()

	cfg := CORSConfig{Origins: []string{"https://Example.COM"}}
	var calls atomic.Int32
	handler := CORS(cfg)(newCountingHandler(t, &calls, http.StatusOK, "ok"))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "" {
		t.Error("expected origin match to be case-insensitive, but header was not set")
	}
}

// TestCORS_ConfiguredOriginWithTrailingSlashMatches pins the operator-facing
// half of the whitelist: an allowed origin copied out of a browser address bar
// carries a trailing slash, while the Origin header never does. The WebSocket
// handshake gate trims it, so a mismatch here presented as "the live socket
// connects but every REST call is blocked" with nothing logged.
func TestCORS_ConfiguredOriginWithTrailingSlashMatches(t *testing.T) {
	t.Parallel()

	cfg := CORSConfig{Origins: []string{"https://ha.example.com/", "  https://Second.Example.com  "}}
	var calls atomic.Int32
	handler := CORS(cfg)(newCountingHandler(t, &calls, http.StatusOK, "ok"))

	for _, origin := range []string{"https://ha.example.com", "https://second.example.com"} {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("Origin %q: Access-Control-Allow-Origin = %q, want %q", origin, got, origin)
		}
	}
}

// containsToken reports whether token appears as a word in the comma-separated
// header value s.
func containsToken(s, token string) bool {
	return slices.Contains(splitComma(s), token)
}

func splitComma(s string) []string {
	var out []string
	for _, part := range splitStr(s, ',') {
		trimmed := trimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func splitStr(s string, sep byte) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}
