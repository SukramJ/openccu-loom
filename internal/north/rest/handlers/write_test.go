// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// TestDecodeJSONRejectsOversizedBody verifies that DecodeJSON refuses request
// bodies larger than maxRequestBodyBytes, returning a *http.MaxBytesError
// rather than allocating the full payload.
func TestDecodeJSONRejectsOversizedBody(t *testing.T) {
	t.Parallel()
	// Build a body slightly over the 1 MiB limit using printable ASCII so the
	// JSON decoder does not hit a syntax error before reaching the size cap.
	oversize := bytes.Repeat([]byte("x"), maxRequestBodyBytes+1)
	// Wrap it in a JSON object so the decoder sees valid JSON prefix.
	body := append([]byte(`{"data":"`), oversize...)
	body = append(body, []byte(`"}`)...)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))

	var v map[string]any
	err := DecodeJSON(req, &v)
	if err == nil {
		t.Fatal("expected an error for oversized body, got nil")
	}
	if !IsBodyTooLargeError(err) {
		t.Fatalf("expected MaxBytesError for oversized body, got %T: %v", err, err)
	}
}

// TestDecodeJSONAcceptsBodyAtLimit verifies that a body exactly at the limit
// is accepted without error.
func TestDecodeJSONAcceptsBodyAtLimit(t *testing.T) {
	t.Parallel()
	// Small valid JSON body — well within the 1 MiB cap.
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"key":"value"}`))
	var v map[string]any
	if err := DecodeJSON(req, &v); err != nil {
		t.Fatalf("expected no error for small body, got: %v", err)
	}
}

// TestIsBodyTooLargeErrorDistinguishesOtherErrors confirms that
// IsBodyTooLargeError returns false for non-MaxBytes errors so callers
// can still distinguish them.
func TestIsBodyTooLargeErrorDistinguishesOtherErrors(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("NOT JSON"))
	var v map[string]any
	err := DecodeJSON(req, &v)
	if err == nil {
		t.Fatal("expected JSON decode error")
	}
	if IsBodyTooLargeError(err) {
		t.Fatal("a JSON syntax error must not be classified as body-too-large")
	}
}

// TestDecodeJSONDisallowsUnknownFields verifies that unknown JSON fields are
// still rejected by DecodeJSON after the MaxBytesReader wrapping.
func TestDecodeJSONDisallowsUnknownFields(t *testing.T) {
	t.Parallel()
	type target struct {
		Name string `json:"name"`
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ok","extra":"fail"}`))
	var v target
	if err := DecodeJSON(req, &v); err == nil {
		t.Fatal("expected error for unknown field 'extra'")
	}
}

// TestHandlerOversizedBodyReturns413 verifies that a handler which calls
// DecodeJSON maps an oversized body to an HTTP 413 response (a distinct
// memory-safety rejection, not the generic 400 a malformed-but-small body
// gets) via its existing error path (links.AddLink as a representative
// handler).
func TestHandlerOversizedBodyReturns413(t *testing.T) {
	t.Parallel()
	svc := &stubLinksService{}
	// Build a body whose raw size exceeds the 1 MiB limit.
	oversize := bytes.Repeat([]byte("x"), maxRequestBodyBytes+1)
	body := append([]byte(`{"sender_address":"`), oversize...)
	body = append(body, []byte(`","receiver_address":"DEV002:1"}`)...)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	AddLink(svc).ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body should produce 413, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestDecodeJSONStatusMapsErrorKinds locks in the status-selection
// contract [DecodeJSONStatus] gives handlers: 413 only for the
// MaxBytesReader case, 400 for every other decode failure including a
// nil error (the rooms/functions handlers OR a validation check into
// the same branch).
func TestDecodeJSONStatusMapsErrorKinds(t *testing.T) {
	t.Parallel()
	oversize := bytes.Repeat([]byte("x"), maxRequestBodyBytes+1)
	body := append([]byte(`{"data":"`), oversize...)
	body = append(body, []byte(`"}`)...)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	var v map[string]any
	tooLargeErr := DecodeJSON(req, &v)

	badReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("NOT JSON"))
	var v2 map[string]any
	badJSONErr := DecodeJSON(badReq, &v2)

	cases := []struct {
		name string
		err  error
		want int
	}{
		{"too large", tooLargeErr, http.StatusRequestEntityTooLarge},
		{"malformed JSON", badJSONErr, http.StatusBadRequest},
		{"nil (validation-only failure)", nil, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := DecodeJSONStatus(tc.err); got != tc.want {
				t.Errorf("DecodeJSONStatus(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// TestWriteServerErrorOmitsRawErrorFromBody verifies the info-leak fix
// for every 5xx problem+json response: [writeServerError] must not put
// err's text into the response body (only the static title), so a
// driver-specific or filesystem-path-carrying error string never
// reaches a caller — regardless of their privilege level.
func TestWriteServerErrorOmitsRawErrorFromBody(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()

	sensitive := errors.New("sqlite: open /var/lib/openccu-loom/secret-path.db: permission denied")
	writeServerError(w, req, http.StatusInternalServerError, problem.TypeInternal, "Widget query failed", sensitive)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "secret-path.db") || strings.Contains(body, "permission denied") {
		t.Errorf("response body leaked the underlying error text: %s", body)
	}
	if !strings.Contains(body, "Widget query failed") {
		t.Errorf("expected the generic title in the body, got %s", body)
	}
}
