// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewStatusMetrics_StartsZero(t *testing.T) {
	t.Parallel()
	m := NewStatusMetrics()
	if m.TotalRequests() != 0 || m.ServerErrors() != 0 || m.ClientErrors() != 0 {
		t.Fatalf("fresh metrics not zeroed: total=%d server=%d client=%d",
			m.TotalRequests(), m.ServerErrors(), m.ClientErrors())
	}
}

func TestStatusCounter_NilMetricsIsPassThrough(t *testing.T) {
	t.Parallel()
	called := false
	h := StatusCounter(nil)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if !called {
		t.Fatal("nil-metrics wrapper must still call next")
	}
}

func TestStatusCounter_ClassifiesByStatusCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		status     int
		wantServer uint64
		wantClient uint64
	}{
		{"200 OK", http.StatusOK, 0, 0},
		{"201 Created", http.StatusCreated, 0, 0},
		{"302 Redirect", http.StatusFound, 0, 0},
		{"400 BadRequest", http.StatusBadRequest, 0, 1},
		{"404 NotFound", http.StatusNotFound, 0, 1},
		{"422 Unprocessable", http.StatusUnprocessableEntity, 0, 1},
		{"500 Internal", http.StatusInternalServerError, 1, 0},
		{"502 BadGateway", http.StatusBadGateway, 1, 0},
		{"503 ServiceUnavailable", http.StatusServiceUnavailable, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := NewStatusMetrics()
			h := StatusCounter(m)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
			if m.TotalRequests() != 1 {
				t.Errorf("total=%d, want 1", m.TotalRequests())
			}
			if m.ServerErrors() != tc.wantServer {
				t.Errorf("server=%d, want %d", m.ServerErrors(), tc.wantServer)
			}
			if m.ClientErrors() != tc.wantClient {
				t.Errorf("client=%d, want %d", m.ClientErrors(), tc.wantClient)
			}
		})
	}
}

func TestStatusCounter_DefaultStatusIsOK(t *testing.T) {
	t.Parallel()
	// Handler that doesn't call WriteHeader explicitly — statusWriter
	// must record the implicit 200 OK that net/http writes.
	m := NewStatusMetrics()
	h := StatusCounter(m)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if m.TotalRequests() != 1 || m.ServerErrors() != 0 || m.ClientErrors() != 0 {
		t.Fatalf("implicit-200 path total=%d server=%d client=%d",
			m.TotalRequests(), m.ServerErrors(), m.ClientErrors())
	}
}

func TestStatusCounter_AccumulatesAcrossRequests(t *testing.T) {
	t.Parallel()
	m := NewStatusMetrics()
	h := StatusCounter(m)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
		case "/bad":
			w.WriteHeader(http.StatusBadRequest)
		case "/oops":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	for _, p := range []string{"/ok", "/ok", "/bad", "/bad", "/bad", "/oops"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, http.NoBody))
	}
	if m.TotalRequests() != 6 {
		t.Errorf("total=%d, want 6", m.TotalRequests())
	}
	if m.ClientErrors() != 3 {
		t.Errorf("client=%d, want 3", m.ClientErrors())
	}
	if m.ServerErrors() != 1 {
		t.Errorf("server=%d, want 1", m.ServerErrors())
	}
}
