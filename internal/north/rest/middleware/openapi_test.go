// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", http.NoBody)
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
