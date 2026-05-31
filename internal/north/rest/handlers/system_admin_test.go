// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/diagnostics"
)

// fakeStartupCaptureService is a minimal implementation of StartupCaptureService for tests.
type fakeStartupCaptureService struct {
	loadResult diagnostics.StartupCaptureConfig
	loadErr    error
	saveErr    error
	savedCfg   *diagnostics.StartupCaptureConfig
}

func (f *fakeStartupCaptureService) Load() (diagnostics.StartupCaptureConfig, error) {
	return f.loadResult, f.loadErr
}

func (f *fakeStartupCaptureService) Save(cfg diagnostics.StartupCaptureConfig) error {
	f.savedCfg = &cfg
	return f.saveErr
}

// --- GetStartupCapture ---

func TestGetStartupCapture_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/startup-capture", http.NoBody)
	w := httptest.NewRecorder()
	GetStartupCapture(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetStartupCapture_HappyPath_Returns200(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{
		loadResult: diagnostics.StartupCaptureConfig{Enabled: true, DurationS: 60, Anonymise: true},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/startup-capture", http.NoBody)
	w := httptest.NewRecorder()
	GetStartupCapture(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetStartupCapture_LoadError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{
		loadErr: errors.New("disk error"),
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/startup-capture", http.NoBody)
	w := httptest.NewRecorder()
	GetStartupCapture(svc).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- PutStartupCapture ---

func TestPutStartupCapture_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := `{"enabled":true,"duration_seconds":30,"anonymise":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutStartupCapture(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutStartupCapture_MalformedJSON_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", bytes.NewBufferString(`{not valid`))
	w := httptest.NewRecorder()
	PutStartupCapture(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutStartupCapture_NegativeDuration_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{}
	body := `{"enabled":false,"duration_seconds":-1}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutStartupCapture(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutStartupCapture_SaveError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{saveErr: errors.New("write failed")}
	body := `{"enabled":true,"duration_seconds":30}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutStartupCapture(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutStartupCapture_HappyPath_Returns200(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{}
	body := `{"enabled":true,"duration_seconds":30,"anonymise":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutStartupCapture(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.savedCfg == nil {
		t.Fatal("expected Save to be called")
	}
	if !svc.savedCfg.Enabled {
		t.Fatal("expected Enabled=true in saved config")
	}
}

func TestPutStartupCapture_AuditRecorderCalled_OnEnable(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{}
	rec := audit.NewBuffer(10)
	body := `{"enabled":true,"duration_seconds":30}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutStartupCapture(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	entries := rec.List(10)
	if len(entries) != 1 {
		t.Fatalf("audit entries=%d, want 1", len(entries))
	}
	if entries[0].Action != audit.Action("diagnostics.startup_capture_enabled") {
		t.Fatalf("unexpected action: %q", entries[0].Action)
	}
}

func TestPutStartupCapture_AuditRecorderCalled_OnDisable(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{}
	rec := audit.NewBuffer(10)
	body := `{"enabled":false,"duration_seconds":30}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutStartupCapture(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	entries := rec.List(10)
	if len(entries) != 1 {
		t.Fatalf("audit entries=%d, want 1", len(entries))
	}
	if entries[0].Action != audit.Action("diagnostics.startup_capture_disabled") {
		t.Fatalf("unexpected action: %q", entries[0].Action)
	}
}
