// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// stubIncidentsReader is an inline stub for IncidentsReader.
type stubIncidentsReader struct {
	incidents []Incident
}

func (s *stubIncidentsReader) Incidents() []Incident {
	return s.incidents
}

func TestListIncidents_HappyPath(t *testing.T) {
	t.Parallel()
	reader := &stubIncidentsReader{
		incidents: []Incident{
			{
				ID:        "inc-001",
				When:      time.Now(),
				Component: "xmlrpc",
				Severity:  "warn",
				Summary:   "reconnect loop",
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", http.NoBody)
	w := httptest.NewRecorder()
	ListIncidents(reader).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []Incident
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].ID != "inc-001" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestListIncidents_ReaderNil_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", http.NoBody)
	w := httptest.NewRecorder()
	ListIncidents(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []Incident
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 0 {
		t.Fatalf("expected empty list, got %+v", body)
	}
}

func TestListIncidents_EmptyList(t *testing.T) {
	t.Parallel()
	reader := &stubIncidentsReader{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", http.NoBody)
	w := httptest.NewRecorder()
	ListIncidents(reader).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
