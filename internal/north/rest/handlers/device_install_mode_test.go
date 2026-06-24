// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeInstallMode records calls to SetInstallMode and returns a
// configurable error.
type fakeInstallMode struct {
	lastAddress string
	lastSeconds int
	err         error
}

func (f *fakeInstallMode) SetInstallMode(_ context.Context, address string, durationSecs int) error {
	f.lastAddress = address
	f.lastSeconds = durationSecs
	return f.err
}

func TestPostDeviceInstallMode_ExplicitSeconds_Returns202(t *testing.T) {
	t.Parallel()
	svc := &fakeInstallMode{}
	body := strings.NewReader(`{"seconds":120}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PostDeviceInstallMode(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastAddress != "DEV001" {
		t.Fatalf("expected lastAddress=DEV001, got %q", svc.lastAddress)
	}
	if svc.lastSeconds != 120 {
		t.Fatalf("expected lastSeconds=120, got %d", svc.lastSeconds)
	}
}

func TestPostDeviceInstallMode_OmittedSeconds_DefaultsTo60(t *testing.T) {
	t.Parallel()
	svc := &fakeInstallMode{}
	// An empty JSON object omits "seconds"; the handler must default to 60.
	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV002"}))
	w := httptest.NewRecorder()
	PostDeviceInstallMode(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastSeconds != 60 {
		t.Fatalf("expected lastSeconds=60 (default), got %d", svc.lastSeconds)
	}
}

func TestPostDeviceInstallMode_ZeroSeconds_DefaultsTo60(t *testing.T) {
	t.Parallel()
	svc := &fakeInstallMode{}
	body := strings.NewReader(`{"seconds":0}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV003"}))
	w := httptest.NewRecorder()
	PostDeviceInstallMode(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastSeconds != 60 {
		t.Fatalf("expected lastSeconds=60 for zero input, got %d", svc.lastSeconds)
	}
}

func TestPostDeviceInstallMode_MissingAddr_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeInstallMode{}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": ""}))
	w := httptest.NewRecorder()
	PostDeviceInstallMode(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostDeviceInstallMode_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PostDeviceInstallMode(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostDeviceInstallMode_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &fakeInstallMode{err: errors.New("CCU unreachable")}
	body := strings.NewReader(`{"seconds":30}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PostDeviceInstallMode(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostDeviceInstallMode_InvalidJSON_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeInstallMode{}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("NOT JSON"))
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	PostDeviceInstallMode(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}
