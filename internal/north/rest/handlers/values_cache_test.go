// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// fakeValuesCacheService is a minimal implementation of ValuesCacheService for tests.
type fakeValuesCacheService struct {
	deleteAllErr    error
	deleteDeviceErr error
	statsResult     ValuesCacheStats
	statsErr        error
	metricsResult   ValuesCacheMetrics
}

func (f *fakeValuesCacheService) DeleteAll(_ context.Context) error {
	return f.deleteAllErr
}

func (f *fakeValuesCacheService) DeleteDevice(_ context.Context, _, _, _ string) error {
	return f.deleteDeviceErr
}

func (f *fakeValuesCacheService) Stats(_ context.Context) (ValuesCacheStats, error) {
	return f.statsResult, f.statsErr
}

func (f *fakeValuesCacheService) Metrics() ValuesCacheMetrics {
	return f.metricsResult
}

// fakeDeviceLookup is a minimal implementation of DeviceLookup for tests.
type fakeDeviceLookup struct {
	centralName string
	iface       string
	found       bool
}

func (f *fakeDeviceLookup) LocateDevice(_ string) (centralName, interfaceID string, ok bool) {
	return f.centralName, f.iface, f.found
}

// --- GetValuesCacheStats ---

func TestGetValuesCacheStats_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/values-cache/stats", http.NoBody)
	w := httptest.NewRecorder()
	GetValuesCacheStats(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetValuesCacheStats_HappyPath_Returns200(t *testing.T) {
	t.Parallel()
	svc := &fakeValuesCacheService{
		statsResult:   ValuesCacheStats{Rows: 42, ValueJSONSize: 1024},
		metricsResult: ValuesCacheMetrics{RestoredRows: 10, FlushBatches: 5},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/values-cache/stats", http.NoBody)
	w := httptest.NewRecorder()
	GetValuesCacheStats(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetValuesCacheStats_StatsError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &fakeValuesCacheService{statsErr: errors.New("db error")}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/values-cache/stats", http.NoBody)
	w := httptest.NewRecorder()
	GetValuesCacheStats(svc).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- ResetValuesCacheGlobal ---

func TestResetValuesCacheGlobal_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/values-cache/reset", http.NoBody)
	w := httptest.NewRecorder()
	ResetValuesCacheGlobal(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestResetValuesCacheGlobal_HappyPath_Returns204(t *testing.T) {
	t.Parallel()
	svc := &fakeValuesCacheService{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/values-cache/reset", http.NoBody)
	w := httptest.NewRecorder()
	ResetValuesCacheGlobal(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestResetValuesCacheGlobal_DeleteError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &fakeValuesCacheService{deleteAllErr: errors.New("db error")}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/values-cache/reset", http.NoBody)
	w := httptest.NewRecorder()
	ResetValuesCacheGlobal(svc).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- ResetValuesCacheDevice ---

func TestResetValuesCacheDevice_NilService_Returns503(t *testing.T) {
	t.Parallel()
	lookup := &fakeDeviceLookup{found: true, centralName: "ccu", iface: "HmIP-RF"}
	r := chi.NewRouter()
	r.Post("/devices/{addr}/values-cache/reset", ResetValuesCacheDevice(nil, lookup))
	req := httptest.NewRequest(http.MethodPost, "/devices/DEV0001/values-cache/reset", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestResetValuesCacheDevice_NilLookup_Returns503(t *testing.T) {
	t.Parallel()
	svc := &fakeValuesCacheService{}
	r := chi.NewRouter()
	r.Post("/devices/{addr}/values-cache/reset", ResetValuesCacheDevice(svc, nil))
	req := httptest.NewRequest(http.MethodPost, "/devices/DEV0001/values-cache/reset", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestResetValuesCacheDevice_MissingAddrParam_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeValuesCacheService{}
	lookup := &fakeDeviceLookup{found: true, centralName: "ccu", iface: "HmIP-RF"}
	// Route without {addr} param means chi.URLParam returns ""
	req := httptest.NewRequest(http.MethodPost, "/devices/values-cache/reset", http.NoBody)
	w := httptest.NewRecorder()
	ResetValuesCacheDevice(svc, lookup).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestResetValuesCacheDevice_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := &fakeValuesCacheService{}
	lookup := &fakeDeviceLookup{found: false}
	r := chi.NewRouter()
	r.Post("/devices/{addr}/values-cache/reset", ResetValuesCacheDevice(svc, lookup))
	req := httptest.NewRequest(http.MethodPost, "/devices/MISSING001/values-cache/reset", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestResetValuesCacheDevice_HappyPath_Returns204(t *testing.T) {
	t.Parallel()
	svc := &fakeValuesCacheService{}
	lookup := &fakeDeviceLookup{found: true, centralName: "ccu", iface: "HmIP-RF"}
	r := chi.NewRouter()
	r.Post("/devices/{addr}/values-cache/reset", ResetValuesCacheDevice(svc, lookup))
	req := httptest.NewRequest(http.MethodPost, "/devices/DEV0001/values-cache/reset", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestResetValuesCacheDevice_DeleteError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &fakeValuesCacheService{deleteDeviceErr: errors.New("db error")}
	lookup := &fakeDeviceLookup{found: true, centralName: "ccu", iface: "HmIP-RF"}
	r := chi.NewRouter()
	r.Post("/devices/{addr}/values-cache/reset", ResetValuesCacheDevice(svc, lookup))
	req := httptest.NewRequest(http.MethodPost, "/devices/DEV0001/values-cache/reset", http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}
