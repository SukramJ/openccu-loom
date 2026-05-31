// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package problem_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// --- helpers -----------------------------------------------------------------

func doWrite(status int, d problem.Details) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	problem.Write(rr, status, d)
	return rr
}

func decode(t *testing.T, rr *httptest.ResponseRecorder) problem.Details {
	t.Helper()
	var d problem.Details
	if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode problem: %v (body=%s)", err, rr.Body.String())
	}
	return d
}

func fakeRequest(path string) *http.Request {
	return httptest.NewRequest(http.MethodGet, path, http.NoBody)
}

// --- ContentType constant -----------------------------------------------------

func TestContentType(t *testing.T) {
	if problem.ContentType != "application/problem+json" {
		t.Fatalf("ContentType=%q", problem.ContentType)
	}
}

// --- Write tests -------------------------------------------------------------

func TestWriteSetsContentType(t *testing.T) {
	rr := doWrite(http.StatusNotFound, problem.Details{Type: "x", Status: 404})
	if rr.Header().Get("Content-Type") != problem.ContentType {
		t.Fatalf("Content-Type=%q", rr.Header().Get("Content-Type"))
	}
}

func TestWriteStatusFromArgWhenDetailsZero(t *testing.T) {
	rr := doWrite(http.StatusNotFound, problem.Details{Type: "not_found"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rr.Code)
	}
	d := decode(t, rr)
	if d.Status != http.StatusNotFound {
		t.Fatalf("body.status=%d", d.Status)
	}
}

func TestWriteStatusFromDetailsWhenNonZero(t *testing.T) {
	rr := doWrite(http.StatusInternalServerError, problem.Details{
		Type:   "conflict",
		Status: http.StatusConflict,
	})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestWriteEmitsProblemCodeHeader(t *testing.T) {
	d := problem.Details{Type: "x", Code: "my_code"}
	rr := doWrite(http.StatusBadRequest, d)
	if rr.Header().Get("X-Problem-Code") != "my_code" {
		t.Fatalf("X-Problem-Code=%q", rr.Header().Get("X-Problem-Code"))
	}
}

func TestWriteNoCodeHeaderWhenCodeEmpty(t *testing.T) {
	rr := doWrite(http.StatusBadRequest, problem.Details{Type: "x"})
	if rr.Header().Get("X-Problem-Code") != "" {
		t.Fatalf("X-Problem-Code must be absent")
	}
}

func TestWriteBodyIsJSON(t *testing.T) {
	rr := doWrite(http.StatusUnprocessableEntity, problem.Details{
		Type:   "validation",
		Title:  "Validation failed",
		Detail: "field x required",
		Errors: []problem.FieldError{{Field: "x", Reason: "required"}},
	})
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
}

func TestWriteFieldErrors(t *testing.T) {
	rr := doWrite(http.StatusUnprocessableEntity, problem.Details{
		Type: "validation",
		Errors: []problem.FieldError{
			{Field: "temp", Reason: "too_high", Max: 30},
			{Field: "power", Reason: "too_low", Min: 0},
		},
	})
	var out struct {
		Errors []struct {
			Field  string `json:"field"`
			Reason string `json:"reason"`
			Min    any    `json:"min,omitempty"`
			Max    any    `json:"max,omitempty"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Errors) != 2 {
		t.Fatalf("errors=%d", len(out.Errors))
	}
	if out.Errors[0].Field != "temp" || out.Errors[0].Reason != "too_high" {
		t.Fatalf("errors[0]=%+v", out.Errors[0])
	}
}

// --- New tests ---------------------------------------------------------------

func TestNewWithRequest(t *testing.T) {
	req := fakeRequest("/api/v1/devices/ABC")
	d := problem.New(problem.TypeNotFound, req, "Not found", "device ABC not found")
	if d.Instance != "/api/v1/devices/ABC" {
		t.Fatalf("instance=%q", d.Instance)
	}
	if d.Code != string(problem.TypeNotFound) {
		t.Fatalf("code=%q", d.Code)
	}
	if d.Title != "Not found" {
		t.Fatalf("title=%q", d.Title)
	}
	if d.Detail != "device ABC not found" {
		t.Fatalf("detail=%q", d.Detail)
	}
	if d.Type == "" {
		t.Fatal("type must not be empty")
	}
}

func TestNewNilRequest(t *testing.T) {
	d := problem.New(problem.TypeInternal, nil, "Internal error", "boom")
	if d.Instance != "" {
		t.Fatalf("instance must be empty for nil request, got %q", d.Instance)
	}
}

func TestNewTypeBaseURL(t *testing.T) {
	d := problem.New(problem.TypeValidation, nil, "", "")
	// Type must begin with the base URL.
	if d.Type == string(problem.TypeValidation) {
		t.Fatal("Type must be a URL, not the bare enum string")
	}
	if len(d.Type) < len(string(problem.TypeValidation)) {
		t.Fatalf("type too short: %q", d.Type)
	}
}

func TestNewAllCanonicalTypes(t *testing.T) {
	types := []problem.Type{
		problem.TypeValidation,
		problem.TypeNotFound,
		problem.TypeConflict,
		problem.TypeUnauthorized,
		problem.TypeForbidden,
		problem.TypeUnsupported,
		problem.TypeRateLimited,
		problem.TypeInternal,
		problem.TypeBadRequest,
		problem.TypeServiceUnready,
		problem.TypeUpstreamUnavailable,
	}
	for _, pt := range types {
		d := problem.New(pt, nil, "t", "d")
		if d.Code != string(pt) {
			t.Errorf("type %q: code=%q", pt, d.Code)
		}
	}
}

// --- WriteFromError tests ----------------------------------------------------

func TestWriteFromErrorNotFound(t *testing.T) {
	rr := httptest.NewRecorder()
	req := fakeRequest("/x")
	problem.WriteFromError(rr, req, problem.ErrNotFound)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rr.Code)
	}
	d := decode(t, rr)
	if d.Code != string(problem.TypeNotFound) {
		t.Fatalf("code=%q", d.Code)
	}
}

func TestWriteFromErrorWrappedNotFound(t *testing.T) {
	err := fmt.Errorf("layer: %w", problem.ErrNotFound)
	rr := httptest.NewRecorder()
	problem.WriteFromError(rr, fakeRequest("/y"), err)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestWriteFromErrorUnauthorized(t *testing.T) {
	rr := httptest.NewRecorder()
	problem.WriteFromError(rr, fakeRequest("/u"), problem.ErrUnauthorized)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rr.Code)
	}
	d := decode(t, rr)
	if d.Code != string(problem.TypeUnauthorized) {
		t.Fatalf("code=%q", d.Code)
	}
}

func TestWriteFromErrorForbidden(t *testing.T) {
	rr := httptest.NewRecorder()
	problem.WriteFromError(rr, fakeRequest("/f"), problem.ErrForbidden)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestWriteFromErrorValidation(t *testing.T) {
	err := fmt.Errorf("bad input: %w", hmerr.ErrValidation)
	rr := httptest.NewRecorder()
	problem.WriteFromError(rr, fakeRequest("/v"), err)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d", rr.Code)
	}
	d := decode(t, rr)
	if d.Code != string(problem.TypeValidation) {
		t.Fatalf("code=%q", d.Code)
	}
}

func TestWriteFromErrorCircuitBreakerOpen(t *testing.T) {
	err := fmt.Errorf("cb: %w", hmerr.ErrCircuitBreakerOpen)
	rr := httptest.NewRecorder()
	problem.WriteFromError(rr, fakeRequest("/cb"), err)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status=%d", rr.Code)
	}
	d := decode(t, rr)
	if d.Code != string(problem.TypeUpstreamUnavailable) {
		t.Fatalf("code=%q", d.Code)
	}
}

func TestWriteFromErrorAuthFailure(t *testing.T) {
	err := fmt.Errorf("upstream: %w", hmerr.ErrAuthFailure)
	rr := httptest.NewRecorder()
	problem.WriteFromError(rr, fakeRequest("/af"), err)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestWriteFromErrorInternal(t *testing.T) {
	err := errors.New("unexpected")
	rr := httptest.NewRecorder()
	problem.WriteFromError(rr, fakeRequest("/i"), err)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rr.Code)
	}
	d := decode(t, rr)
	if d.Code != string(problem.TypeInternal) {
		t.Fatalf("code=%q", d.Code)
	}
}

// --- IsUpstreamUnavailable tests ---------------------------------------------

func TestIsUpstreamUnavailableNil(t *testing.T) {
	if problem.IsUpstreamUnavailable(nil) {
		t.Fatal("nil must not be upstream unavailable")
	}
}

func TestIsUpstreamUnavailableCircuitBreaker(t *testing.T) {
	if !problem.IsUpstreamUnavailable(hmerr.ErrCircuitBreakerOpen) {
		t.Fatal("ErrCircuitBreakerOpen must be upstream unavailable")
	}
}

func TestIsUpstreamUnavailableWrappedCircuitBreaker(t *testing.T) {
	err := fmt.Errorf("w: %w", hmerr.ErrCircuitBreakerOpen)
	if !problem.IsUpstreamUnavailable(err) {
		t.Fatal("wrapped ErrCircuitBreakerOpen must be upstream unavailable")
	}
}

func TestIsUpstreamUnavailableAuthFailure(t *testing.T) {
	if !problem.IsUpstreamUnavailable(hmerr.ErrAuthFailure) {
		t.Fatal("ErrAuthFailure must be upstream unavailable")
	}
}

func TestIsUpstreamUnavailableOtherError(t *testing.T) {
	if problem.IsUpstreamUnavailable(errors.New("other")) {
		t.Fatal("generic error must not be upstream unavailable")
	}
}

// --- Sentinel errors ---------------------------------------------------------

func TestSentinelErrNotFound(t *testing.T) {
	if problem.ErrNotFound == nil {
		t.Fatal("ErrNotFound must not be nil")
	}
}

func TestSentinelErrUnauthorized(t *testing.T) {
	if problem.ErrUnauthorized == nil {
		t.Fatal("ErrUnauthorized must not be nil")
	}
}

func TestSentinelErrForbidden(t *testing.T) {
	if problem.ErrForbidden == nil {
		t.Fatal("ErrForbidden must not be nil")
	}
}

func TestSentinelErrValidationAliasesHmerr(t *testing.T) {
	if !errors.Is(problem.ErrValidation, hmerr.ErrValidation) {
		t.Fatal("problem.ErrValidation must be errors.Is(hmerr.ErrValidation)")
	}
}

// --- FieldError omitempty ----------------------------------------------------

func TestFieldErrorMinMaxOmitEmpty(t *testing.T) {
	fe := problem.FieldError{Field: "x", Reason: "bad"}
	b, err := json.Marshal(fe)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("empty marshal")
	}
	// min/max must not appear when zero.
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if _, has := out["min"]; has {
		t.Fatal("min must be omitted when zero")
	}
	if _, has := out["max"]; has {
		t.Fatal("max must be omitted when zero")
	}
}
