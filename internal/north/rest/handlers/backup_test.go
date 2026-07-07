// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// stubBackupService is an inline stub for BackupService.
type stubBackupService struct {
	triggerID  string
	triggerErr error
	listResult []BackupEntry
	listErr    error
	restoreID  string
	restoreErr error
	streamData string
	streamErr  error

	// unscopedTriggerCalls / forCentralCalls record which trigger method
	// the handler invoked and, for the scoped call, which central name it
	// was given — so tests can assert the handler routed to the right one.
	unscopedTriggerCalls int
	forCentralCalls      []string
}

func (s *stubBackupService) TriggerBackup(_ context.Context) (string, error) {
	s.unscopedTriggerCalls++
	return s.triggerID, s.triggerErr
}

func (s *stubBackupService) List(_ context.Context) ([]BackupEntry, error) {
	return s.listResult, s.listErr
}

func (s *stubBackupService) Restore(_ context.Context, _ string) (string, error) {
	return s.restoreID, s.restoreErr
}

func (s *stubBackupService) TriggerBackupForCentral(_ context.Context, centralName string) (string, error) {
	s.forCentralCalls = append(s.forCentralCalls, centralName)
	return s.triggerID, s.triggerErr
}

func (s *stubBackupService) Prune(_ context.Context, _ string, _ int) error { return nil }

func (s *stubBackupService) Stream(_ context.Context, _ string, w io.Writer) error {
	if s.streamErr != nil {
		return s.streamErr
	}
	_, _ = w.Write([]byte(s.streamData))
	return nil
}

// chiContext returns a context with chi URL params attached.
// Shared by all handler tests in this package.
func chiContext(r *http.Request, params map[string]string) context.Context {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
}

func TestTriggerBackup_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{triggerID: "backup-001"}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups", http.NoBody)
	w := httptest.NewRecorder()
	TriggerBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/api/v1/backups/backup-001" {
		t.Fatalf("expected Location header, got %q", loc)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["id"] != "backup-001" {
		t.Fatalf("expected id=backup-001, got %q", body["id"])
	}
}

func TestTriggerBackup_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups", http.NoBody)
	w := httptest.NewRecorder()
	TriggerBackup(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestTriggerBackup_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{triggerErr: errors.New("CCU offline")}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups", http.NoBody)
	w := httptest.NewRecorder()
	TriggerBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

// TestTriggerBackup_NoBody_CallsUnscopedTrigger locks in the
// backward-compatible default: a bare `POST /backups` (no body) still
// backs up the first registered central via TriggerBackup, not
// TriggerBackupForCentral.
func TestTriggerBackup_NoBody_CallsUnscopedTrigger(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{triggerID: "backup-001"}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups", http.NoBody)
	w := httptest.NewRecorder()
	TriggerBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.unscopedTriggerCalls != 1 {
		t.Errorf("unscopedTriggerCalls = %d, want 1", svc.unscopedTriggerCalls)
	}
	if len(svc.forCentralCalls) != 0 {
		t.Errorf("forCentralCalls = %v, want none", svc.forCentralCalls)
	}
}

// TestTriggerBackup_WithCentralName_CallsTriggerBackupForCentral is the
// REST-side half of the B2 multi-CCU fix: a body carrying central_name
// must route to TriggerBackupForCentral with exactly that name, not the
// unscoped (first-central) trigger.
func TestTriggerBackup_WithCentralName_CallsTriggerBackupForCentral(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{triggerID: "beta-20260101-000000"}
	body := strings.NewReader(`{"central_name":"beta"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	TriggerBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.unscopedTriggerCalls != 0 {
		t.Errorf("unscopedTriggerCalls = %d, want 0", svc.unscopedTriggerCalls)
	}
	if want := []string{"beta"}; len(svc.forCentralCalls) != 1 || svc.forCentralCalls[0] != want[0] {
		t.Errorf("forCentralCalls = %v, want %v", svc.forCentralCalls, want)
	}
}

// TestTriggerBackup_MalformedBody_Returns400 checks that an unparsable
// body is rejected as a client error, not silently ignored (which would
// mask an operator typo as an unscoped, wrong-central trigger).
func TestTriggerBackup_MalformedBody_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{triggerID: "backup-001"}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups", strings.NewReader(`{not json`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	TriggerBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.unscopedTriggerCalls != 0 || len(svc.forCentralCalls) != 0 {
		t.Error("service must not be invoked when the body fails to decode")
	}
}

func TestListBackups_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{
		listResult: []BackupEntry{
			{ID: "b1", Central: "ccu-01", Bytes: 1024},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups", http.NoBody)
	w := httptest.NewRecorder()
	ListBackups(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []BackupEntry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].ID != "b1" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestListBackups_ServiceNil_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups", http.NoBody)
	w := httptest.NewRecorder()
	ListBackups(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []BackupEntry
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 0 {
		t.Fatalf("expected empty list, got %+v", body)
	}
}

func TestListBackups_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{listErr: errors.New("DB error")}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups", http.NoBody)
	w := httptest.NewRecorder()
	ListBackups(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestRestoreBackup_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{restoreID: "b1"}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups/b1/restore", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "b1"}))
	w := httptest.NewRecorder()
	RestoreBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRestoreBackup_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups/b1/restore", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "b1"}))
	w := httptest.NewRecorder()
	RestoreBackup(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestRestoreBackup_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{restoreErr: errors.New("restore failed")}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups/b1/restore", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "b1"}))
	w := httptest.NewRecorder()
	RestoreBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestDownloadBackup_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{streamData: "SBKDATA"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups/b1/download", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "b1"}))
	w := httptest.NewRecorder()
	DownloadBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("expected octet-stream, got %q", ct)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "b1.sbk") {
		t.Fatalf("unexpected Content-Disposition: %q", w.Header().Get("Content-Disposition"))
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("SBKDATA")) {
		t.Fatalf("body does not contain stream data")
	}
}

// TestDownloadBackup_IDWithQuoteIsEscapedInContentDisposition verifies
// that a backup id containing a double quote cannot break out of the
// filename parameter and inject extra Content-Disposition directives
// — the header must stay parseable, and the parsed filename value
// must round-trip the id verbatim (quote included) rather than being
// silently truncated at the injected quote.
func TestDownloadBackup_IDWithQuoteIsEscapedInContentDisposition(t *testing.T) {
	t.Parallel()
	trickyID := `evil"; filename="pwned.sh`
	svc := &stubBackupService{streamData: "SBKDATA"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups/x/download", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": trickyID}))
	w := httptest.NewRecorder()
	DownloadBackup(svc).ServeHTTP(w, req)

	cd := w.Header().Get("Content-Disposition")
	_, params, err := mime.ParseMediaType(cd)
	if err != nil {
		t.Fatalf("Content-Disposition is not parseable: %q: %v", cd, err)
	}
	wantFilename := trickyID + ".sbk"
	if params["filename"] != wantFilename {
		t.Errorf("filename param = %q, want %q (header: %q)", params["filename"], wantFilename, cd)
	}
}

func TestDownloadBackup_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups/b1/download", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "b1"}))
	w := httptest.NewRecorder()
	DownloadBackup(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}
