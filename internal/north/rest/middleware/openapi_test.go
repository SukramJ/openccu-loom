// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// minimalSpec is a self-contained OpenAPI 3.1 document used for the
// middleware tests. The shape — including the `servers: [/api/v1]`
// prefix — mirrors `assets/openapi.yaml` so tests catch the same
// path-resolution semantics as production.
const minimalSpec = `
openapi: 3.1.0
info:
  title: test
  version: "1"
servers:
  - url: /api/v1
paths:
  /devices:
    get:
      operationId: listDevices
      responses:
        "200":
          description: ok
    post:
      operationId: createDevice
      requestBody:
        content:
          application/json:
            schema: { type: object }
      responses:
        "201":
          description: created
  /devices/{addr}:
    get:
      operationId: getDevice
      parameters:
        - name: addr
          in: path
          required: true
          schema: { type: string }
      responses:
        "200":
          description: ok
`

func newValidatorOrFail(t *testing.T) *OpenAPIValidator {
	t.Helper()
	v, err := NewOpenAPIValidator(OpenAPIValidatorConfig{
		Spec: []byte(minimalSpec),
	})
	if err != nil {
		t.Fatalf("NewOpenAPIValidator: %v", err)
	}
	return v
}

// TestOpenAPIValidatorAllowsSpeccedPath verifies that requests
// matching a documented path + method pass through untouched.
func TestOpenAPIValidatorAllowsSpeccedPath(t *testing.T) {
	v := newValidatorOrFail(t)
	called := false
	leaf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	mw := v.Middleware()(leaf)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", http.NoBody)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if !called {
		t.Fatal("downstream handler was not invoked")
	}
	if rr.Code != http.StatusNoContent {
		t.Errorf("status=%d want 204", rr.Code)
	}
}

// TestOpenAPIValidatorRejectsUnknownPath verifies that a request to
// an unspecced route is rejected with 404 + problem+json instead of
// reaching the handler.
func TestOpenAPIValidatorRejectsUnknownPath(t *testing.T) {
	v := newValidatorOrFail(t)
	leaf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not be invoked for unspecced route")
	})
	mw := v.Middleware()(leaf)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/not-in-spec", http.NoBody)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status=%d want 404", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/problem+json") {
		t.Errorf("content-type=%q want problem+json", ct)
	}
}

// TestOpenAPIValidatorRejectsBadMethod verifies that a method
// mismatch yields 405.
func TestOpenAPIValidatorRejectsBadMethod(t *testing.T) {
	v := newValidatorOrFail(t)
	mw := v.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler must not be invoked")
	}))

	// /devices/{addr} only defines GET in minimalSpec (unlike /devices,
	// which also defines POST for the body-size test below).
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/ABC1234567", http.NoBody)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status=%d want 405", rr.Code)
	}
}

// TestOpenAPIValidatorFailOpenLetsThroughUnknownPaths verifies the
// FailOpen escape hatch — useful while the spec is being backfilled.
func TestOpenAPIValidatorFailOpenLetsThroughUnknownPaths(t *testing.T) {
	v, err := NewOpenAPIValidator(OpenAPIValidatorConfig{
		Spec:     []byte(minimalSpec),
		FailOpen: true,
	})
	if err != nil {
		t.Fatalf("NewOpenAPIValidator: %v", err)
	}
	called := false
	mw := v.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/not-in-spec", http.NoBody)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if !called {
		t.Fatal("FailOpen mode should let unknown paths through")
	}
	_ = rr
}

// TestOpenAPIValidatorRejectsOversizedBody verifies that a request
// body larger than [openAPIBodyLimit] is rejected with 413 before the
// validator buffers it in full via io.ReadAll — otherwise an
// unauthenticated, unbounded POST could pin a goroutine allocating
// unlimited heap ahead of any handler-level size cap.
func TestOpenAPIValidatorRejectsOversizedBody(t *testing.T) {
	v := newValidatorOrFail(t)
	mw := v.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler must not be invoked for an oversized body")
	}))

	oversize := bytes.Repeat([]byte("x"), openAPIBodyLimit+1)
	body := append([]byte(`{"data":"`), oversize...)
	body = append(body, []byte(`"}`)...)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413, body=%s", rr.Code, rr.Body.String())
	}
}

// TestOpenAPIValidatorRejectsOversizedChunkedBody pins the same cap for a
// body whose length the transport does not announce. net/http reports
// ContentLength -1 for `Transfer-Encoding: chunked`, so a guard keyed on
// a positive length lets the request reach openapi3filter, which reads
// the whole body into memory with an unbounded io.ReadAll — before auth,
// CSRF or any per-handler limit sees the request.
func TestOpenAPIValidatorRejectsOversizedChunkedBody(t *testing.T) {
	v := newValidatorOrFail(t)
	mw := v.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler must not be invoked for an oversized body")
	}))

	oversize := bytes.Repeat([]byte("x"), openAPIBodyLimit+1)
	body := append([]byte(`{"data":"`), oversize...)
	body = append(body, []byte(`"}`)...)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// What the server hands the chain for a chunked upload.
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413, body=%s", rr.Code, rr.Body.String())
	}
}
