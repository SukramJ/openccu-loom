// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
)

// TestRestoreDeviceConfig_HappyPath_Returns202AndRecordsAudit verifies a
// successful restore returns 202 Accepted (the CCU runs the re-transmit
// asynchronously) and records exactly one audit entry tagged
// audit.ActionDeviceConfigRestore against the device address.
func TestRestoreDeviceConfig_HappyPath_Returns202AndRecordsAudit(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceAdmin{}
	rec := &captureRecorder{}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
	w := httptest.NewRecorder()
	RestoreDeviceConfig(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastAddress != "0001ABCD" {
		t.Fatalf("expected lastAddress=0001ABCD, got %q", svc.lastAddress)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("expected exactly 1 audit entry, got %d: %+v", len(rec.entries), rec.entries)
	}
	e := rec.entries[0]
	if e.Action != audit.ActionDeviceConfigRestore {
		t.Errorf("expected action=%q, got %q", audit.ActionDeviceConfigRestore, e.Action)
	}
	if e.DeviceAddress != "0001ABCD" {
		t.Errorf("expected device_address=%q, got %q", "0001ABCD", e.DeviceAddress)
	}
	if e.Note != "restore stored config to device" {
		t.Errorf("expected note=%q, got %q", "restore stored config to device", e.Note)
	}
}

// TestRestoreDeviceConfig_Unsupported_Returns422 verifies an interface
// that does not expose restoreConfigToDevice (BidCos-Wired, CUxD,
// VirtualDevices) surfaces as 422, not a generic 502 — the operator
// should read this as "not supported here", not "CCU unreachable".
func TestRestoreDeviceConfig_Unsupported_Returns422(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceAdmin{restoreErr: backends.ErrUnsupported}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
	w := httptest.NewRecorder()
	RestoreDeviceConfig(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestRestoreDeviceConfig_ServiceError_Returns502 verifies a non-capability
// failure (e.g. the CCU is unreachable) surfaces as a 502 upstream error.
func TestRestoreDeviceConfig_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceAdmin{restoreErr: errors.New("CCU unreachable")}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
	w := httptest.NewRecorder()
	RestoreDeviceConfig(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestRestoreDeviceConfig_NilService_Returns503 verifies the handler
// degrades gracefully (503) rather than panicking when the daemon has not
// wired a DeviceConfigRestorePort.
func TestRestoreDeviceConfig_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD"}))
	w := httptest.NewRecorder()
	RestoreDeviceConfig(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestRestoreDeviceConfig_MissingAddr_Returns400 verifies an empty {addr}
// path parameter is rejected before the domain layer is ever called.
func TestRestoreDeviceConfig_MissingAddr_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceAdmin{}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": ""}))
	w := httptest.NewRecorder()
	RestoreDeviceConfig(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastAddress != "" {
		t.Errorf("domain layer must not be called on a missing address, got lastAddress=%q", svc.lastAddress)
	}
}
