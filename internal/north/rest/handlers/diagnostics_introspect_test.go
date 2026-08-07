// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// --------------------------------------------------------------------------
// fakeIntrospectService
// --------------------------------------------------------------------------

type fakeIntrospectService struct {
	reliabilityResult []handlers.ReliabilityState
	resolvedName      string
	resolveOK         bool
	// capturedCentral is set by ReliabilitySnapshot so the test can verify
	// the central query argument was forwarded correctly.
	capturedCentral string
}

func (f *fakeIntrospectService) ReliabilitySnapshot(centralName string) []handlers.ReliabilityState {
	f.capturedCentral = centralName
	return f.reliabilityResult
}

func (f *fakeIntrospectService) ResolveCentral(_ string) (string, bool) {
	return f.resolvedName, f.resolveOK
}

func (f *fakeIntrospectService) TapEventBus(ctx context.Context, _ string, _ []string, emit func(handlers.DiagnosticsEvent)) {
	emit(handlers.DiagnosticsEvent{TS: "t1", Type: "TestEvent", Event: map[string]string{"a": "1"}})
	emit(handlers.DiagnosticsEvent{TS: "t2", Type: "TestEvent", Event: map[string]string{"a": "2"}})
	<-ctx.Done()
}

// --------------------------------------------------------------------------
// DiagnosticsReliability
// --------------------------------------------------------------------------

func TestDiagnosticsReliability_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/reliability", http.NoBody)
	w := httptest.NewRecorder()
	handlers.DiagnosticsReliability(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDiagnosticsReliability_ReturnsBothRows(t *testing.T) {
	t.Parallel()
	svc := &fakeIntrospectService{
		reliabilityResult: []handlers.ReliabilityState{
			{Central: "ccu1", Interface: "HmIP-RF", CircuitState: 0},
			{Central: "ccu1", Interface: "BidCos-RF", CircuitState: 1},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/reliability", http.NoBody)
	w := httptest.NewRecorder()
	handlers.DiagnosticsReliability(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var rows []handlers.ReliabilityState
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Interface != "HmIP-RF" {
		t.Errorf("rows[0].Interface = %q, want HmIP-RF", rows[0].Interface)
	}
	if rows[1].Interface != "BidCos-RF" {
		t.Errorf("rows[1].Interface = %q, want BidCos-RF", rows[1].Interface)
	}
}

func TestDiagnosticsReliability_CentralQueryForwarded(t *testing.T) {
	t.Parallel()
	svc := &fakeIntrospectService{
		reliabilityResult: []handlers.ReliabilityState{},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/reliability?central=myCCU", http.NoBody)
	w := httptest.NewRecorder()
	handlers.DiagnosticsReliability(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if svc.capturedCentral != "myCCU" {
		t.Errorf("capturedCentral = %q, want myCCU", svc.capturedCentral)
	}
}

// --------------------------------------------------------------------------
// DiagnosticsEventBusTap — nil service and resolve failure
// --------------------------------------------------------------------------

func TestDiagnosticsEventBusTap_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/eventbus/tap", http.NoBody)
	w := httptest.NewRecorder()
	handlers.DiagnosticsEventBusTap(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDiagnosticsEventBusTap_ResolveFails_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeIntrospectService{
		resolvedName: "",
		resolveOK:    false,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/eventbus/tap?central=unknown", http.NoBody)
	w := httptest.NewRecorder()
	handlers.DiagnosticsEventBusTap(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// --------------------------------------------------------------------------
// DiagnosticsEventBusTap — streaming happy path
// --------------------------------------------------------------------------

func TestDiagnosticsEventBusTap_StreamsStartedAndTwoEvents(t *testing.T) {
	t.Parallel()
	svc := &fakeIntrospectService{
		resolvedName: "ccu1",
		resolveOK:    true,
	}

	// Use httptest.NewServer so the response writer supports Flusher.
	srv := httptest.NewServer(handlers.DiagnosticsEventBusTap(svc))
	defer srv.Close()

	// The tap streams for the requested window and then closes, so the
	// request blocks for its whole length. One second is the shortest window
	// tapWindow accepts, and the assertions below are about which lines
	// arrive, not how long the tap stayed open.
	resp, err := http.Get(srv.URL + "?seconds=1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/x-ndjson") {
		t.Errorf("Content-Type = %q, want application/x-ndjson", ct)
	}

	sc := bufio.NewScanner(resp.Body)
	var lines []string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	// Expect: _tap_started + 2 emitted events = 3 lines minimum.
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 NDJSON lines, got %d: %v", len(lines), lines)
	}

	// First line must be _tap_started.
	var first handlers.DiagnosticsEvent
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decode first line: %v", err)
	}
	if first.Type != "_tap_started" {
		t.Errorf("first event type = %q, want _tap_started", first.Type)
	}

	// Lines 1 and 2 must be the two emitted TestEvents.
	for i, idx := range []int{1, 2} {
		var e handlers.DiagnosticsEvent
		if err := json.Unmarshal([]byte(lines[idx]), &e); err != nil {
			t.Fatalf("decode line %d: %v", idx, err)
		}
		if e.Type != "TestEvent" {
			t.Errorf("line[%d].Type = %q, want TestEvent", i, e.Type)
		}
	}
}

// --------------------------------------------------------------------------
// helpers shared with other test files in this package
// --------------------------------------------------------------------------
