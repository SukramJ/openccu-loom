// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// fakeCCURebooter records the central it was asked to reboot and returns a
// configurable error.
type fakeCCURebooter struct {
	lastCentral string
	err         error
}

func (f *fakeCCURebooter) RebootCCU(_ context.Context, central string) error {
	f.lastCentral = central
	return f.err
}

func TestPostCCUReboot_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"central": "home"}))
	w := httptest.NewRecorder()
	PostCCUReboot(nil, nil).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostCCUReboot_MissingCentral_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeCCURebooter{}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"central": ""}))
	w := httptest.NewRecorder()
	PostCCUReboot(svc, nil).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostCCUReboot_UnknownCentral_Returns404(t *testing.T) {
	t.Parallel()
	svc := &fakeCCURebooter{err: hmerr.ErrUnknownCentral}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"central": "nope"}))
	w := httptest.NewRecorder()
	PostCCUReboot(svc, nil).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostCCUReboot_UpstreamFailure_Returns502(t *testing.T) {
	t.Parallel()
	svc := &fakeCCURebooter{err: hmerr.ErrNoConnection}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"central": "home"}))
	w := httptest.NewRecorder()
	PostCCUReboot(svc, nil).ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostCCUReboot_HappyPath_Returns202AndAudits(t *testing.T) {
	t.Parallel()
	svc := &fakeCCURebooter{}
	rec := &captureRecorder{}
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"central": "home"}))
	w := httptest.NewRecorder()
	PostCCUReboot(svc, rec).ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastCentral != "home" {
		t.Fatalf("expected reboot of central=home, got %q", svc.lastCentral)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(rec.entries))
	}
	if got := rec.entries[0]; got.Action != audit.ActionSystemCCUReboot || got.Note != "home" {
		t.Fatalf("audit entry mismatch: %+v", got)
	}
}
