// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/health"
)

// stubHealthReader is an inline stub for HealthReader.
type stubHealthReader struct {
	overall    health.Status
	components []health.Component
}

func (s *stubHealthReader) Overall() health.Status {
	return s.overall
}

func (s *stubHealthReader) Snapshot() []health.Component {
	return s.components
}

func TestHealth_HappyPath_Returns200(t *testing.T) {
	t.Parallel()
	tracker := &stubHealthReader{
		overall: health.StatusHealthy,
		components: []health.Component{
			{
				Name:   "xmlrpc",
				Status: health.StatusHealthy,
				LastSample: health.Sample{
					Healthy:   true,
					Timestamp: time.Now(),
				},
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
	w := httptest.NewRecorder()
	Health(tracker).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Status != "healthy" {
		t.Fatalf("expected status=healthy, got %q", body.Status)
	}
	if len(body.Components) != 1 || body.Components[0].Name != "xmlrpc" {
		t.Fatalf("unexpected components: %+v", body.Components)
	}
}

func TestHealth_CriticalDepDown_Returns503(t *testing.T) {
	t.Parallel()
	// A failed persistence layer is fatal — the daemon cannot serve.
	tracker := &stubHealthReader{
		components: []health.Component{
			{Name: "OttoGo-HmIP-RF", Status: health.StatusHealthy},
			{Name: "sqlite", Status: health.StatusUnhealthy},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
	w := httptest.NewRecorder()
	Health(tracker).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHealth_SingleInterfaceDown_Returns200Degraded(t *testing.T) {
	t.Parallel()
	// One south-bound interface down while others (and core deps) are
	// healthy degrades service but keeps the daemon serving → 200.
	tracker := &stubHealthReader{
		components: []health.Component{
			{Name: "OttoGo-HmIP-RF", Status: health.StatusHealthy},
			{Name: "KearneyGo-CUxD", Status: health.StatusUnhealthy},
			{Name: "sqlite", Status: health.StatusHealthy},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
	w := httptest.NewRecorder()
	Health(tracker).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Status != "degraded" {
		t.Fatalf("expected status=degraded, got %q", body.Status)
	}
}

func TestHealth_AllInterfacesDown_Returns503(t *testing.T) {
	t.Parallel()
	// No reachable south-bound interface anywhere → genuine outage.
	tracker := &stubHealthReader{
		components: []health.Component{
			{Name: "OttoGo-HmIP-RF", Status: health.StatusUnhealthy},
			{Name: "KearneyGo-CUxD", Status: health.StatusUnhealthy},
			{Name: "sqlite", Status: health.StatusHealthy},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
	w := httptest.NewRecorder()
	Health(tracker).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHealth_Degraded_Returns200(t *testing.T) {
	t.Parallel()
	tracker := &stubHealthReader{
		overall: health.StatusDegraded,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
	w := httptest.NewRecorder()
	Health(tracker).ServeHTTP(w, req)

	// degraded != unhealthy → should still be 200
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for degraded, got %d", w.Code)
	}
}

func TestHealth_EmptyComponents_Returns200(t *testing.T) {
	t.Parallel()
	tracker := &stubHealthReader{
		overall: health.StatusHealthy,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
	w := httptest.NewRecorder()
	Health(tracker).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body HealthResponse
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Components) != 0 {
		t.Fatalf("expected empty components, got %+v", body.Components)
	}
}
