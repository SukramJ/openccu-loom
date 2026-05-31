// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/diagnostics"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// --------------------------------------------------------------------------
// fakeCaptureService
// --------------------------------------------------------------------------

type fakeCaptureService struct {
	startResult diagnostics.Summary
	startErr    error
	stopResult  diagnostics.Summary
	stopErr     error
	listResult  []diagnostics.Summary
	getResult   diagnostics.Summary
	getErr      error
	archiveData []byte
	archiveErr  error
	started     int
}

func (f *fakeCaptureService) Start(_ diagnostics.StartOptions) (diagnostics.Summary, error) {
	f.started++
	if f.started > 1 {
		return diagnostics.Summary{}, diagnostics.ErrCaptureBusy
	}
	return f.startResult, f.startErr
}

func (f *fakeCaptureService) Stop(_ string) (diagnostics.Summary, error) {
	return f.stopResult, f.stopErr
}

func (f *fakeCaptureService) List() []diagnostics.Summary {
	return f.listResult
}

func (f *fakeCaptureService) Get(_ string) (diagnostics.Summary, error) {
	return f.getResult, f.getErr
}

func (f *fakeCaptureService) OpenArchive(_ string) ([]byte, error) {
	return f.archiveData, f.archiveErr
}

// newCaptureRouter builds a chi router that wires the capture handlers to
// the same paths used by the real REST router.
func newCaptureRouter(svc handlers.CaptureService) chi.Router {
	r := chi.NewRouter()
	r.Post("/api/v1/diagnostics/capture", handlers.StartCapture(svc, nil))
	r.Delete("/api/v1/diagnostics/capture/{id}", handlers.StopCapture(svc, nil))
	r.Get("/api/v1/diagnostics/captures", handlers.ListCaptures(svc))
	r.Get("/api/v1/diagnostics/capture/{id}", handlers.GetCapture(svc))
	r.Get("/api/v1/diagnostics/capture/{id}/download", handlers.DownloadCapture(svc))
	return r
}

// --------------------------------------------------------------------------
// StartCapture
// --------------------------------------------------------------------------

func TestStartCapture_NilService_Returns503(t *testing.T) {
	t.Parallel()
	r := newCaptureRouter(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/capture", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestStartCapture_ValidBody_Returns202(t *testing.T) {
	t.Parallel()
	svc := &fakeCaptureService{
		startResult: diagnostics.Summary{
			ID:     "cap_aabbccdd",
			Status: diagnostics.StatusRunning,
		},
	}
	body := `{"duration_seconds": 60, "anonymise": true}`
	r := newCaptureRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/capture", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var sum diagnostics.Summary
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sum.ID != "cap_aabbccdd" {
		t.Errorf("id = %q, want cap_aabbccdd", sum.ID)
	}
}

func TestStartCapture_Twice_Returns409(t *testing.T) {
	t.Parallel()
	svc := &fakeCaptureService{
		startResult: diagnostics.Summary{ID: "cap_first", Status: diagnostics.StatusRunning},
	}
	r := newCaptureRouter(svc)

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/capture", http.NoBody)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	if w := do(); w.Code != http.StatusAccepted {
		t.Fatalf("first start: expected 202, got %d", w.Code)
	}
	if w := do(); w.Code != http.StatusConflict {
		t.Fatalf("second start: expected 409, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestStartCapture_DurationTooLong_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeCaptureService{
		startErr: diagnostics.ErrCaptureDurationTooLong,
	}
	body := `{"duration_seconds": 99999}`
	r := newCaptureRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/capture", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// --------------------------------------------------------------------------
// StopCapture
// --------------------------------------------------------------------------

func TestStopCapture_NoActive_Returns409(t *testing.T) {
	t.Parallel()
	svc := &fakeCaptureService{
		stopErr: diagnostics.ErrCaptureNotActive,
	}
	r := newCaptureRouter(svc)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/diagnostics/capture/cap_any", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}
}

// --------------------------------------------------------------------------
// ListCaptures
// --------------------------------------------------------------------------

func TestListCaptures_Returns200_EmptyList(t *testing.T) {
	t.Parallel()
	svc := &fakeCaptureService{listResult: []diagnostics.Summary{}}
	r := newCaptureRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/captures", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var list []diagnostics.Summary
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d entries", len(list))
	}
}

// --------------------------------------------------------------------------
// GetCapture
// --------------------------------------------------------------------------

func TestGetCapture_UnknownID_Returns404(t *testing.T) {
	t.Parallel()
	svc := &fakeCaptureService{
		getErr: diagnostics.ErrCaptureNotFound,
	}
	r := newCaptureRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/capture/cap_unknown", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// --------------------------------------------------------------------------
// DownloadCapture
// --------------------------------------------------------------------------

func TestDownloadCapture_ActiveCapture_Returns409(t *testing.T) {
	t.Parallel()
	svc := &fakeCaptureService{
		archiveErr: diagnostics.ErrCaptureNotActive,
	}
	r := newCaptureRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/capture/cap_running/download", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDownloadCapture_StoppedCapture_Returns200WithGzip(t *testing.T) {
	t.Parallel()
	fakeGzip := []byte("\x1f\x8b\x08\x00somefakedata")
	svc := &fakeCaptureService{
		archiveData: fakeGzip,
	}
	r := newCaptureRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/capture/cap_done/download", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "gzip") {
		t.Errorf("Content-Type = %q, want application/gzip", ct)
	}
	if !bytes.Equal(w.Body.Bytes(), fakeGzip) {
		t.Errorf("body mismatch: got %d bytes, want %d bytes", w.Body.Len(), len(fakeGzip))
	}
}

func TestDownloadCapture_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := &fakeCaptureService{
		archiveErr: diagnostics.ErrCaptureNotFound,
	}
	r := newCaptureRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/capture/cap_gone/download", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDownloadCapture_InternalError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &fakeCaptureService{
		archiveErr: errors.New("disk full"),
	}
	r := newCaptureRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/capture/cap_broken/download", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}
