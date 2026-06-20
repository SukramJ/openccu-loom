// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubReloaderService is an inline stub for ReloaderService that
// records the address / channel-address passed to it.
type stubReloaderService struct {
	deviceAddr  string
	channelAddr string
	deviceErr   error
	channelErr  error
}

func (s *stubReloaderService) ReloadDeviceConfig(_ context.Context, address string) error {
	s.deviceAddr = address
	return s.deviceErr
}

func (s *stubReloaderService) ReloadChannelConfig(_ context.Context, channelAddress string) error {
	s.channelAddr = channelAddress
	return s.channelErr
}

func TestReloadDevice_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubReloaderService{}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	ReloadDevice(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.deviceAddr != "DEV001" {
		t.Fatalf("expected address DEV001, got %q", svc.deviceAddr)
	}
}

func TestReloadDevice_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	ReloadDevice(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestReloadDevice_MissingAddress_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubReloaderService{}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{}))
	w := httptest.NewRecorder()
	ReloadDevice(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestReloadDevice_ReloadError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubReloaderService{deviceErr: errors.New("device not found")}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	ReloadDevice(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestReloadChannel_HappyPath_ComposesChannelAddress(t *testing.T) {
	t.Parallel()
	svc := &stubReloaderService{}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "channel": "3"}))
	w := httptest.NewRecorder()
	ReloadChannel(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.channelAddr != "DEV001:3" {
		t.Fatalf("expected channel address DEV001:3, got %q", svc.channelAddr)
	}
}

func TestReloadChannel_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "channel": "3"}))
	w := httptest.NewRecorder()
	ReloadChannel(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestReloadChannel_MissingChannel_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubReloaderService{}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001"}))
	w := httptest.NewRecorder()
	ReloadChannel(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestReloadChannel_ReloadError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubReloaderService{channelErr: errors.New("channel not found")}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV001", "channel": "3"}))
	w := httptest.NewRecorder()
	ReloadChannel(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}
