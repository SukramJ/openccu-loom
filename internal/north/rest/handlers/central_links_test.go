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
)

// stubCentralLinksService is an inline stub for CentralLinksService.
type stubCentralLinksService struct {
	createReport CentralLinksReport
	createErr    error
	removeReport CentralLinksReport
	removeErr    error
	statusResult CentralLinksStatus
	statusErr    error
}

func (s *stubCentralLinksService) CreateCentralLinks(_ context.Context, _ string) (CentralLinksReport, error) {
	return s.createReport, s.createErr
}

func (s *stubCentralLinksService) RemoveCentralLinks(_ context.Context, _ string) (CentralLinksReport, error) {
	return s.removeReport, s.removeErr
}

func (s *stubCentralLinksService) CentralLinksStatus(_ string) (CentralLinksStatus, error) {
	return s.statusResult, s.statusErr
}

func TestGetCentralLinksStatus_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubCentralLinksService{
		statusResult: CentralLinksStatus{Supported: true, EligibleChannels: 2},
	}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	GetCentralLinksStatus(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body CentralLinksStatus
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.Supported {
		t.Fatal("expected supported=true")
	}
}

func TestGetCentralLinksStatus_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	GetCentralLinksStatus(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGetCentralLinksStatus_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubCentralLinksService{statusErr: errors.New("lookup failed")}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	GetCentralLinksStatus(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestCreateCentralLinks_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubCentralLinksService{
		createReport: CentralLinksReport{Touched: 3, Skipped: 1},
	}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	CreateCentralLinks(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var body CentralLinksReport
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Touched != 3 {
		t.Fatalf("expected touched=3, got %d", body.Touched)
	}
}

func TestCreateCentralLinks_UnsupportedError_Returns422(t *testing.T) {
	t.Parallel()
	svc := &stubCentralLinksService{createErr: ErrCentralLinksUnsupported}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	CreateCentralLinks(svc).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteCentralLinks_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubCentralLinksService{
		removeReport: CentralLinksReport{Touched: 2},
	}
	req := httptest.NewRequest(http.MethodDelete, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	DeleteCentralLinks(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- centralLinksError error-type routing ---

func TestCentralLinksError_UnsupportedError(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	w := httptest.NewRecorder()
	centralLinksError(w, req, ErrCentralLinksUnsupported)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCentralLinksError_Generic(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	w := httptest.NewRecorder()
	centralLinksError(w, req, errors.New("some internal failure"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- DeleteCentralLinks error paths ---

func TestDeleteCentralLinks_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	DeleteCentralLinks(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteCentralLinks_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubCentralLinksService{removeErr: errors.New("remove failed")}
	req := httptest.NewRequest(http.MethodDelete, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	DeleteCentralLinks(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteCentralLinks_HappyPath_Returns202(t *testing.T) {
	t.Parallel()
	svc := &stubCentralLinksService{removeReport: CentralLinksReport{Touched: 1}}
	req := httptest.NewRequest(http.MethodDelete, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	DeleteCentralLinks(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- CreateCentralLinks error paths ---

func TestCreateCentralLinks_NilSvc_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001:1"}))
	w := httptest.NewRecorder()
	CreateCentralLinks(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestCreateCentralLinks_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubCentralLinksService{createErr: errors.New("links fail")}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001:1"}))
	w := httptest.NewRecorder()
	CreateCentralLinks(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateCentralLinks_HappyPath_Returns202(t *testing.T) {
	t.Parallel()
	svc := &stubCentralLinksService{createReport: CentralLinksReport{Touched: 1}}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001:1"}))
	w := httptest.NewRecorder()
	CreateCentralLinks(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}
