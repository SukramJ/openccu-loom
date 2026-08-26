// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubDeviceIconProxy is a configurable test double for DeviceIconProxy.
type stubDeviceIconProxy struct {
	data        []byte
	contentType string
	ok          bool
}

func (s *stubDeviceIconProxy) Icon(_ context.Context, _ string) (data []byte, contentType string, ok bool) {
	return s.data, s.contentType, s.ok
}

func TestGetDeviceIcon_HappyPath(t *testing.T) {
	t.Parallel()
	imgData := []byte("\x89PNG\r\n\x1a\n") // minimal PNG magic bytes
	proxy := &stubDeviceIconProxy{data: imgData, contentType: "image/png", ok: true}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/AABB0001/icon", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "AABB0001"}))
	w := httptest.NewRecorder()

	GetDeviceIcon(proxy).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want %q", ct, "image/png")
	}
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q, want %q", cc, "public, max-age=86400")
	}
	if got := w.Body.Bytes(); !bytes.Equal(got, imgData) {
		t.Errorf("body = %q, want %q", got, imgData)
	}
}

func TestGetDeviceIcon_ProxyMiss_Returns404(t *testing.T) {
	t.Parallel()
	proxy := &stubDeviceIconProxy{ok: false}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/MISSING/icon", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "MISSING"}))
	w := httptest.NewRecorder()

	GetDeviceIcon(proxy).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetDeviceIcon_EmptyBytes_Returns404(t *testing.T) {
	t.Parallel()
	proxy := &stubDeviceIconProxy{data: []byte{}, contentType: "image/png", ok: true}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/AABB0001/icon", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "AABB0001"}))
	w := httptest.NewRecorder()

	GetDeviceIcon(proxy).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for empty bytes, got %d", w.Code)
	}
}

func TestGetDeviceIcon_NilProxy_Returns404(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/AABB0001/icon", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "AABB0001"}))
	w := httptest.NewRecorder()

	GetDeviceIcon(nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for nil proxy, got %d", w.Code)
	}
}
