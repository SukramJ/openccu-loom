// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
)

// stubIncidentsReader is an inline stub for IncidentsReader. It does NOT
// implement IncidentsQuerier, exercising the in-memory fallback filter
// path in ListIncidents.
type stubIncidentsReader struct {
	incidents []Incident
}

func (s *stubIncidentsReader) Incidents() []Incident {
	return s.incidents
}

// stubIncidentsQuerier additionally implements IncidentsQuerier, exercising
// the durable-query path in ListIncidents. It records the last filter it
// was called with so tests can assert on argument propagation.
type stubIncidentsQuerier struct {
	incidents []Incident

	lastCentral string
	lastSince   time.Time
	lastUntil   time.Time
	lastLimit   int
}

func (s *stubIncidentsQuerier) Incidents() []Incident { return s.incidents }

func (s *stubIncidentsQuerier) IncidentsFiltered(central string, since, until time.Time, limit int) []Incident {
	s.lastCentral, s.lastSince, s.lastUntil, s.lastLimit = central, since, until, limit
	out := applyIncidentsFilter(s.incidents, incidentsFilter{central: central, since: since, until: until, limit: limit})
	return out
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

// TestListIncidents_FallbackFilterByCentral verifies the in-memory
// fallback path (reader implements only IncidentsReader) scopes results
// to ?central=.
func TestListIncidents_FallbackFilterByCentral(t *testing.T) {
	t.Parallel()
	reader := &stubIncidentsReader{
		incidents: []Incident{
			{ID: "1", Component: "ccu-a", When: time.Now(), Summary: "a"},
			{ID: "2", Component: "ccu-a/HmIP-RF", When: time.Now(), Summary: "a-iface"},
			{ID: "3", Component: "ccu-b", When: time.Now(), Summary: "b"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents?central=ccu-a", http.NoBody)
	w := httptest.NewRecorder()
	ListIncidents(reader).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []Incident
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("len=%d want 2 (ccu-a + ccu-a/HmIP-RF), got %+v", len(body), body)
	}
	for _, inc := range body {
		if inc.Component != "ccu-a" && inc.Component != "ccu-a/HmIP-RF" {
			t.Errorf("unexpected component in scoped result: %q", inc.Component)
		}
	}
}

// TestListIncidents_FallbackFilterBySinceUntil verifies the in-memory
// fallback path bounds by since (inclusive) / until (exclusive).
func TestListIncidents_FallbackFilterBySinceUntil(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reader := &stubIncidentsReader{
		incidents: []Incident{
			{ID: "1", Component: "ccu-a", When: t0, Summary: "t0"},
			{ID: "2", Component: "ccu-a", When: t0.Add(time.Hour), Summary: "t0+1h"},
			{ID: "3", Component: "ccu-a", When: t0.Add(2 * time.Hour), Summary: "t0+2h"},
		},
	}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/incidents?since="+t0.Add(time.Hour).Format(time.RFC3339)+
			"&until="+t0.Add(2*time.Hour).Format(time.RFC3339), http.NoBody)
	w := httptest.NewRecorder()
	ListIncidents(reader).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []Incident
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].ID != "2" {
		t.Fatalf("expected only the t0+1h entry, got %+v", body)
	}
}

// TestListIncidents_FallbackFilterLimit verifies the in-memory fallback
// path caps the result count to ?limit=.
func TestListIncidents_FallbackFilterLimit(t *testing.T) {
	t.Parallel()
	reader := &stubIncidentsReader{
		incidents: []Incident{
			{ID: "1", Component: "ccu-a", When: time.Now()},
			{ID: "2", Component: "ccu-a", When: time.Now()},
			{ID: "3", Component: "ccu-a", When: time.Now()},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents?limit=2", http.NoBody)
	w := httptest.NewRecorder()
	ListIncidents(reader).ServeHTTP(w, req)

	var body []Incident
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("len=%d want 2 (limit=2)", len(body))
	}
}

// TestListIncidents_InvalidSinceReturns400 verifies malformed timestamps
// are rejected with 400, mirroring ListAudit's contract.
func TestListIncidents_InvalidSinceReturns400(t *testing.T) {
	t.Parallel()
	reader := &stubIncidentsReader{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents?since=not-a-time", http.NoBody)
	w := httptest.NewRecorder()
	ListIncidents(reader).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestListIncidents_QuerierPathReceivesParsedFilter verifies that when the
// wired reader also implements IncidentsQuerier, ListIncidents dispatches
// to it with the parsed central/since/until/limit instead of using the
// in-memory fallback.
func TestListIncidents_QuerierPathReceivesParsedFilter(t *testing.T) {
	t.Parallel()
	querier := &stubIncidentsQuerier{
		incidents: []Incident{
			{ID: "1", Component: "ccu-a", When: time.Now(), Summary: "a"},
			{ID: "2", Component: "ccu-b", When: time.Now(), Summary: "b"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents?central=ccu-a&limit=10", http.NoBody)
	w := httptest.NewRecorder()
	ListIncidents(querier).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if querier.lastCentral != "ccu-a" {
		t.Errorf("lastCentral=%q want ccu-a", querier.lastCentral)
	}
	if querier.lastLimit != 10 {
		t.Errorf("lastLimit=%d want 10", querier.lastLimit)
	}
	var body []Incident
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].ID != "1" {
		t.Fatalf("expected only ccu-a entry via querier path, got %+v", body)
	}
}

// --- DeleteIncidents ---

// stubIncidentsClearer is an inline stub for IncidentsClearer.
type stubIncidentsClearer struct {
	err    error
	called bool
}

func (s *stubIncidentsClearer) ClearIncidents(context.Context) error {
	s.called = true
	return s.err
}

func TestDeleteIncidents_NilClearerReturns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/incidents", http.NoBody)
	w := httptest.NewRecorder()
	DeleteIncidents(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestDeleteIncidents_HappyPath_Returns204AndRecordsAudit(t *testing.T) {
	t.Parallel()
	clearer := &stubIncidentsClearer{}
	rec := &captureRecorder{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/incidents", http.NoBody)
	w := httptest.NewRecorder()
	DeleteIncidents(clearer, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if !clearer.called {
		t.Error("ClearIncidents was not called")
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionIncidentsClear {
		t.Fatalf("expected one incidents_clear audit entry, got %+v", rec.entries)
	}
}

func TestDeleteIncidents_StoreError_Returns500(t *testing.T) {
	t.Parallel()
	clearer := &stubIncidentsClearer{err: errors.New("db down")}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/incidents", http.NoBody)
	w := httptest.NewRecorder()
	DeleteIncidents(clearer, nil).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}
