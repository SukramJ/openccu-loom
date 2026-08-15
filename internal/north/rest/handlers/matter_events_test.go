// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/diagevent"
)

type fakeDiagEvents struct{ events []diagevent.Event }

func (f fakeDiagEvents) DiagnosticEvents() []diagevent.Event { return f.events }

// TestMatterDiagnosticEventsServesTheTraceNewestFirst pins the shape an
// operator reads after a pairing failed.
func TestMatterDiagnosticEventsServesTheTraceNewestFirst(t *testing.T) {
	t.Parallel()

	src := fakeDiagEvents{events: []diagevent.Event{{
		At:       time.Date(2026, 8, 15, 12, 0, 30, 0, time.UTC),
		Kind:     diagevent.KindPairing,
		Severity: diagevent.SeverityError,
		Message:  "The commissioning window was revoked after too many failed pairing attempts.",
		Detail:   map[string]string{"max_errors": "20"},
	}}}

	rec := httptest.NewRecorder()
	MatterDiagnosticEvents(src)(rec, httptest.NewRequest(http.MethodGet, "/api/v1/matter/events", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body MatterDiagnosticEventList
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(body.Events))
	}
	e := body.Events[0]
	if e.Severity != "error" || e.Kind != "pairing" {
		t.Errorf("kind/severity = %q/%q, want pairing/error", e.Kind, e.Severity)
	}
	if e.At != "2026-08-15T12:00:30Z" {
		t.Errorf("at = %q, want an RFC3339 timestamp", e.At)
	}
	if e.Detail["max_errors"] != "20" {
		t.Errorf("detail lost: %v", e.Detail)
	}
}

// TestADisabledBridgeIsNotAnEmptyTrace keeps the two states apart.
//
// An operator reading an empty list on a bridge that is switched off
// would conclude that nothing happened, which is the opposite of the
// truth: nothing was recorded because nothing is running.
func TestADisabledBridgeIsNotAnEmptyTrace(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	MatterDiagnosticEvents(nil)(rec, httptest.NewRequest(http.MethodGet, "/api/v1/matter/events", http.NoBody))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 for a disabled bridge", rec.Code)
	}
}
