// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// fakeLogLevelsService is a minimal implementation of LogLevelsService for tests.
type fakeLogLevelsService struct {
	defaultLevel slog.Level
	snapshot     []hmlog.OverrideInfo
	setCalled    []struct {
		path  string
		level slog.Level
		ttl   time.Duration
	}
	resetResult bool
	resetCalled []string
}

func (f *fakeLogLevelsService) Default() slog.Level { return f.defaultLevel }

func (f *fakeLogLevelsService) Set(path string, level slog.Level, ttl time.Duration) {
	f.setCalled = append(f.setCalled, struct {
		path  string
		level slog.Level
		ttl   time.Duration
	}{path, level, ttl})
}

func (f *fakeLogLevelsService) Reset(path string) bool {
	f.resetCalled = append(f.resetCalled, path)
	return f.resetResult
}

func (f *fakeLogLevelsService) Snapshot() []hmlog.OverrideInfo {
	return f.snapshot
}

// --- ListLogLevels ---

func TestListLogLevels_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/log-levels", http.NoBody)
	w := httptest.NewRecorder()
	ListLogLevels(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestListLogLevels_HappyPath_Returns200(t *testing.T) {
	t.Parallel()
	svc := &fakeLogLevelsService{
		defaultLevel: slog.LevelInfo,
		snapshot:     []hmlog.OverrideInfo{},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/log-levels", http.NoBody)
	w := httptest.NewRecorder()
	ListLogLevels(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp LogLevelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Default == "" {
		t.Fatal("default level must not be empty")
	}
}

func TestListLogLevels_WithOverride_IncludesExpiresAt(t *testing.T) {
	t.Parallel()
	expiry := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	svc := &fakeLogLevelsService{
		defaultLevel: slog.LevelInfo,
		snapshot: []hmlog.OverrideInfo{
			{Path: "client", Level: slog.LevelDebug, ExpiresAt: expiry, RemainingMS: 5000},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/log-levels", http.NoBody)
	w := httptest.NewRecorder()
	ListLogLevels(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp LogLevelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Overrides) != 1 {
		t.Fatalf("overrides len=%d, want 1", len(resp.Overrides))
	}
	if resp.Overrides[0].ExpiresAt == "" {
		t.Fatal("expires_at must be set for override with expiry")
	}
}

// --- PutLogLevel ---

func TestPutLogLevel_NilService_Returns503(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	r.Put("/diagnostics/log-levels/{path}", PutLogLevel(nil, nil))
	req := httptest.NewRequest(http.MethodPut, "/diagnostics/log-levels/client", strings.NewReader(`{"level":"debug"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutLogLevel_MissingPathParam_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeLogLevelsService{}
	// Send request without routing through chi so {path} is empty
	req := httptest.NewRequest(http.MethodPut, "/diagnostics/log-levels/", strings.NewReader(`{"level":"debug"}`))
	w := httptest.NewRecorder()
	PutLogLevel(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutLogLevel_InvalidBody_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeLogLevelsService{}
	r := chi.NewRouter()
	r.Put("/diagnostics/log-levels/{path}", PutLogLevel(svc, nil))
	req := httptest.NewRequest(http.MethodPut, "/diagnostics/log-levels/client", strings.NewReader(`{not json}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutLogLevel_InvalidLevel_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeLogLevelsService{}
	r := chi.NewRouter()
	r.Put("/diagnostics/log-levels/{path}", PutLogLevel(svc, nil))
	req := httptest.NewRequest(http.MethodPut, "/diagnostics/log-levels/client", strings.NewReader(`{"level":"superverbose"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutLogLevel_TTLExceedsCap_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeLogLevelsService{}
	r := chi.NewRouter()
	r.Put("/diagnostics/log-levels/{path}", PutLogLevel(svc, nil))
	// 25 hours in seconds > 24h cap
	req := httptest.NewRequest(http.MethodPut, "/diagnostics/log-levels/client", strings.NewReader(`{"level":"debug","ttl_seconds":90001}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutLogLevel_HappyPath_Returns204(t *testing.T) {
	t.Parallel()
	svc := &fakeLogLevelsService{}
	r := chi.NewRouter()
	r.Put("/diagnostics/log-levels/{path}", PutLogLevel(svc, nil))
	req := httptest.NewRequest(http.MethodPut, "/diagnostics/log-levels/client", strings.NewReader(`{"level":"debug"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if len(svc.setCalled) != 1 {
		t.Fatalf("Set called %d times, want 1", len(svc.setCalled))
	}
}

func TestPutLogLevel_AuditRecorderCalled(t *testing.T) {
	t.Parallel()
	svc := &fakeLogLevelsService{}
	rec := audit.NewBuffer(10)
	r := chi.NewRouter()
	r.Put("/diagnostics/log-levels/{path}", PutLogLevel(svc, rec))
	req := httptest.NewRequest(http.MethodPut, "/diagnostics/log-levels/client", strings.NewReader(`{"level":"debug","ttl_seconds":60}`))
	req = withIdentity(req, auth.Identity{Subject: "alice", Role: auth.RoleAdmin})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	entries := rec.List(10)
	if len(entries) != 1 {
		t.Fatalf("audit entries=%d, want 1", len(entries))
	}
	if entries[0].Action != audit.Action("logging.override_set") {
		t.Fatalf("unexpected action: %q", entries[0].Action)
	}
	// The row must name who raised the level, else it cannot be attributed.
	if entries[0].User != "alice" {
		t.Fatalf("audit user=%q, want alice", entries[0].User)
	}
}

// --- DeleteLogLevel ---

func TestDeleteLogLevel_NilService_Returns503(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	r.Delete("/diagnostics/log-levels/{path}", DeleteLogLevel(nil, nil))
	req := httptest.NewRequest(http.MethodDelete, "/diagnostics/log-levels/client", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteLogLevel_MissingPathParam_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeLogLevelsService{}
	// Without chi routing, {path} is empty
	req := httptest.NewRequest(http.MethodDelete, "/diagnostics/log-levels/", http.NoBody)
	w := httptest.NewRecorder()
	DeleteLogLevel(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteLogLevel_HappyPath_Returns204(t *testing.T) {
	t.Parallel()
	svc := &fakeLogLevelsService{resetResult: true}
	r := chi.NewRouter()
	r.Delete("/diagnostics/log-levels/{path}", DeleteLogLevel(svc, nil))
	req := httptest.NewRequest(http.MethodDelete, "/diagnostics/log-levels/client", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteLogLevel_NotFound_Returns204_Idempotent(t *testing.T) {
	t.Parallel()
	svc := &fakeLogLevelsService{resetResult: false}
	r := chi.NewRouter()
	r.Delete("/diagnostics/log-levels/{path}", DeleteLogLevel(svc, nil))
	req := httptest.NewRequest(http.MethodDelete, "/diagnostics/log-levels/nonexistent", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 (idempotent), got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteLogLevel_AuditRecorderCalled_WhenRemoved(t *testing.T) {
	t.Parallel()
	svc := &fakeLogLevelsService{resetResult: true}
	rec := audit.NewBuffer(10)
	r := chi.NewRouter()
	r.Delete("/diagnostics/log-levels/{path}", DeleteLogLevel(svc, rec))
	req := httptest.NewRequest(http.MethodDelete, "/diagnostics/log-levels/client", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	entries := rec.List(10)
	if len(entries) != 1 {
		t.Fatalf("audit entries=%d, want 1", len(entries))
	}
	if entries[0].Action != audit.Action("logging.override_reset") {
		t.Fatalf("unexpected action: %q", entries[0].Action)
	}
}

func TestDeleteLogLevel_AuditRecorderNotCalled_WhenNotRemoved(t *testing.T) {
	t.Parallel()
	svc := &fakeLogLevelsService{resetResult: false}
	rec := audit.NewBuffer(10)
	r := chi.NewRouter()
	r.Delete("/diagnostics/log-levels/{path}", DeleteLogLevel(svc, rec))
	req := httptest.NewRequest(http.MethodDelete, "/diagnostics/log-levels/nonexistent", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	entries := rec.List(10)
	if len(entries) != 0 {
		t.Fatalf("audit entries=%d, want 0 (path was not removed)", len(entries))
	}
}
