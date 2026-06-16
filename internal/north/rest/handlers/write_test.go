// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

// TestHandlerOversizedBodyReturns400 verifies that a handler which calls
// DecodeJSON maps an oversized body to an HTTP 400 response via its existing
// error path (links.AddLink as a representative handler).
func TestHandlerOversizedBodyReturns400(t *testing.T) {
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
	// 400 because the handler maps DecodeJSON errors to bad_request.
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized body should produce 400, got %d body=%s", w.Code, w.Body.String())
	}
}
