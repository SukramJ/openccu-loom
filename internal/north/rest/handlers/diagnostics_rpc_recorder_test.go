// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// --------------------------------------------------------------------------
// fakeRPCRecorderService
// --------------------------------------------------------------------------

type fakeRPCRecorderService struct {
	startResult  []handlers.RPCRecordingStatus
	stopResult   []handlers.RPCRecordingStatus
	statusResult []handlers.RPCRecordingStatus
	exportResult any
	exportOK     bool
	startCalled  []string
	stopCalled   []string
}

func (f *fakeRPCRecorderService) Start(centrals []string, _ int, _ bool) []handlers.RPCRecordingStatus {
	f.startCalled = centrals
	return f.startResult
}

func (f *fakeRPCRecorderService) Stop(centrals []string) []handlers.RPCRecordingStatus {
	f.stopCalled = centrals
	return f.stopResult
}

func (f *fakeRPCRecorderService) Status() []handlers.RPCRecordingStatus {
	return f.statusResult
}

func (f *fakeRPCRecorderService) Export(_, _ string) (any, bool) {
	return f.exportResult, f.exportOK
}

// newRPCRecorderRouter builds a chi router mirroring the real REST paths.
func newRPCRecorderRouter(svc handlers.RPCRecorderService) chi.Router {
	r := chi.NewRouter()
	r.Post("/api/v1/diagnostics/rpc-recording", handlers.StartRPCRecording(svc, nil))
	r.Delete("/api/v1/diagnostics/rpc-recording", handlers.StopRPCRecording(svc, nil))
	r.Get("/api/v1/diagnostics/rpc-recordings", handlers.ListRPCRecordings(svc))
	r.Get("/api/v1/diagnostics/rpc-recording/{central}", handlers.DownloadRPCRecording(svc))
	return r
}

// --------------------------------------------------------------------------
// StartRPCRecording
// --------------------------------------------------------------------------

func TestStartRPCRecording_NilService_Returns503(t *testing.T) {
	t.Parallel()
	r := newRPCRecorderRouter(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/rpc-recording", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestStartRPCRecording_ValidBody_Returns202WithStatusJSON(t *testing.T) {
	t.Parallel()
	svc := &fakeRPCRecorderService{
		startResult: []handlers.RPCRecordingStatus{
			{Central: "ccu-01", Active: true, Entries: 0},
		},
	}
	body := `{"centrals":["ccu-01"]}`
	r := newRPCRecorderRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/rpc-recording", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var out []handlers.RPCRecordingStatus
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0].Central != "ccu-01" || !out[0].Active {
		t.Errorf("unexpected status: %+v", out)
	}
}

func TestStartRPCRecording_CallsSvcWithBodyCentrals(t *testing.T) {
	t.Parallel()
	svc := &fakeRPCRecorderService{
		startResult: []handlers.RPCRecordingStatus{
			{Central: "alpha", Active: true},
			{Central: "beta", Active: true},
		},
	}
	body := `{"centrals":["alpha","beta"]}`
	r := newRPCRecorderRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/rpc-recording", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}
	if len(svc.startCalled) != 2 || svc.startCalled[0] != "alpha" || svc.startCalled[1] != "beta" {
		t.Errorf("Start called with wrong centrals: %v", svc.startCalled)
	}
}

func TestStartRPCRecording_EmptyBody_CallsSvcWithNilCentrals(t *testing.T) {
	t.Parallel()
	svc := &fakeRPCRecorderService{
		startResult: []handlers.RPCRecordingStatus{
			{Central: "ccu-01", Active: true},
		},
	}
	r := newRPCRecorderRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/rpc-recording", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	// empty body → req.Centrals is nil; Start receives nil
	if svc.startCalled != nil {
		t.Errorf("expected nil centrals, got %v", svc.startCalled)
	}
}

// --------------------------------------------------------------------------
// StopRPCRecording
// --------------------------------------------------------------------------

func TestStopRPCRecording_NilService_Returns503(t *testing.T) {
	t.Parallel()
	r := newRPCRecorderRouter(nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/diagnostics/rpc-recording", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestStopRPCRecording_Returns200WithStatusJSON(t *testing.T) {
	t.Parallel()
	svc := &fakeRPCRecorderService{
		stopResult: []handlers.RPCRecordingStatus{
			{Central: "ccu-01", Active: false, Entries: 3},
		},
	}
	r := newRPCRecorderRouter(svc)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/diagnostics/rpc-recording", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out []handlers.RPCRecordingStatus
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0].Active {
		t.Errorf("unexpected status after stop: %+v", out)
	}
}

// --------------------------------------------------------------------------
// ListRPCRecordings
// --------------------------------------------------------------------------

func TestListRPCRecordings_NilService_Returns200EmptyArray(t *testing.T) {
	t.Parallel()
	r := newRPCRecorderRouter(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/rpc-recordings", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out []handlers.RPCRecordingStatus
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty array, got %d entries", len(out))
	}
}

func TestListRPCRecordings_Returns200WithStatusArray(t *testing.T) {
	t.Parallel()
	svc := &fakeRPCRecorderService{
		statusResult: []handlers.RPCRecordingStatus{
			{Central: "ccu-01", Active: true, Entries: 5},
			{Central: "ccu-02", Active: false, Entries: 0},
		},
	}
	r := newRPCRecorderRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/rpc-recordings", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out []handlers.RPCRecordingStatus
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
	if out[0].Central != "ccu-01" || !out[0].Active || out[0].Entries != 5 {
		t.Errorf("unexpected first entry: %+v", out[0])
	}
	if out[1].Central != "ccu-02" || out[1].Active {
		t.Errorf("unexpected second entry: %+v", out[1])
	}
}

// --------------------------------------------------------------------------
// DownloadRPCRecording
// --------------------------------------------------------------------------

func TestDownloadRPCRecording_NilService_Returns503(t *testing.T) {
	t.Parallel()
	r := newRPCRecorderRouter(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/rpc-recording/ccu-01", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDownloadRPCRecording_KnownCentral_Returns200WithAttachment(t *testing.T) {
	t.Parallel()
	svc := &fakeRPCRecorderService{
		exportResult: map[string]any{
			"xml/listDevices|[]": map[string]any{
				"rpc_type": "xml",
				"method":   "listDevices",
			},
		},
		exportOK: true,
	}
	r := newRPCRecorderRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/rpc-recording/ccu-01", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
	if !strings.Contains(cd, "ccu-01") {
		t.Errorf("Content-Disposition = %q, want filename with ccu-01", cd)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(out) == 0 {
		t.Error("expected non-empty body")
	}
}

func TestDownloadRPCRecording_UnknownCentral_Returns404(t *testing.T) {
	t.Parallel()
	svc := &fakeRPCRecorderService{
		exportResult: nil,
		exportOK:     false,
	}
	r := newRPCRecorderRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/rpc-recording/ghost", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestStartRPCRecording_AuditEntryNamesTheOperator verifies the recorder
// lifecycle audit row carries the requesting admin — an RPC trace holds
// CCU traffic, so the row has to say who started it.
func TestStartRPCRecording_AuditEntryNamesTheOperator(t *testing.T) {
	t.Parallel()
	svc := &fakeRPCRecorderService{}
	rec := audit.NewBuffer(10)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/rpc-recording",
		strings.NewReader(`{"centrals":["ccu01"]}`))
	req = req.WithContext(auth.ContextWithIdentity(req.Context(),
		auth.Identity{Subject: "alice", Role: auth.RoleAdmin}))
	w := httptest.NewRecorder()
	handlers.StartRPCRecording(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	entries := rec.List(10)
	if len(entries) != 1 {
		t.Fatalf("audit entries=%d, want 1", len(entries))
	}
	if entries[0].User != "alice" {
		t.Fatalf("audit user=%q, want alice", entries[0].User)
	}
}
