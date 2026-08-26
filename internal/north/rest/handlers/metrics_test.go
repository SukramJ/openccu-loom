// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/metrics"
)

func TestMetricsHandler_NilRegistry_Returns200Empty(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", http.NoBody)
	w := httptest.NewRecorder()
	MetricsHandler(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("expected text/plain Content-Type, got %q", ct)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("expected empty body for nil registry, got %q", w.Body.String())
	}
}

func TestMetricsHandler_WithRegistry_Returns200WithMetrics(t *testing.T) {
	t.Parallel()
	reg := metrics.NewRegistry()
	c := reg.Counter("test_counter", "a test counter")
	c.Add(42)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", http.NoBody)
	w := httptest.NewRecorder()
	MetricsHandler(reg).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "test_counter") {
		t.Fatalf("expected metric name in output, body=%q", w.Body.String())
	}
}

func TestMetricsHandler_ContentType(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", http.NoBody)
	w := httptest.NewRecorder()
	MetricsHandler(nil).ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type must start with text/plain, got %q", ct)
	}
}
