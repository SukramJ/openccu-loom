// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
}

func (s *stubBackupService) TriggerBackup(_ context.Context) (string, error) {
	return s.triggerID, s.triggerErr
}

func (s *stubBackupService) List(_ context.Context) ([]BackupEntry, error) {
	return s.listResult, s.listErr
}

func (s *stubBackupService) Restore(_ context.Context, _ string) (string, error) {
	return s.restoreID, s.restoreErr
}

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
