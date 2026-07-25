// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// stubDeviceSearcher is an inline stub for DeviceSearchPort.
type stubDeviceSearcher struct {
	found int
	err   error

	lastCentral   string
	lastInterface string
}

func (s *stubDeviceSearcher) SearchWiredDevices(_ context.Context, central, interfaceID string) (int, error) {
	s.lastCentral = central
	s.lastInterface = interfaceID
	if s.err != nil {
		return 0, s.err
	}
	return s.found, nil
}

func TestPostInstallModeSearch_HappyPath_Returns200AndRecordsAudit(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceSearcher{found: 2}
	rec := &captureRecorder{}
	body := strings.NewReader(`{"interface":"BidCos-Wired","central":"ccu-01"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	PostInstallModeSearch(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastInterface != "BidCos-Wired" || svc.lastCentral != "ccu-01" {
		t.Fatalf("forwarded call mismatch: interface=%q central=%q", svc.lastInterface, svc.lastCentral)
	}
	var respBody struct {
		Central   string `json:"central"`
		Interface string `json:"interface"`
		Found     int    `json:"found"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &respBody); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if respBody.Central != "ccu-01" || respBody.Interface != "BidCos-Wired" || respBody.Found != 2 {
		t.Fatalf("response body=%+v", respBody)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("expected exactly 1 audit entry, got %d: %+v", len(rec.entries), rec.entries)
	}
	e := rec.entries[0]
	if e.Action != audit.ActionDeviceSearch {
		t.Errorf("expected action=%q, got %q", audit.ActionDeviceSearch, e.Action)
	}
	if !strings.Contains(e.Note, "BidCos-Wired") {
		t.Errorf("expected note to mention the interface BidCos-Wired, got %q", e.Note)
	}
}

func TestPostInstallModeSearch_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"interface":"BidCos-Wired"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	PostInstallModeSearch(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostInstallModeSearch_BadJSON_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceSearcher{}
	body := strings.NewReader(`{"interface":`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	PostInstallModeSearch(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostInstallModeSearch_MissingInterface_Returns422(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceSearcher{}
	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	PostInstallModeSearch(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastInterface != "" {
		t.Errorf("domain layer must not be called without an interface, got %q", svc.lastInterface)
	}
}

func TestPostInstallModeSearch_Unsupported_Returns422(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceSearcher{err: backends.ErrUnsupported}
	body := strings.NewReader(`{"interface":"HmIP-RF"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	PostInstallModeSearch(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestPostInstallModeSearch_ServiceError_Returns502 verifies a non-
// ErrUnsupported failure (e.g. an unknown central, or a wire-level fault)
// falls through to the generic upstream-failure path. The handler does
// not special-case hmerr.ErrUnknownCentral the way PostDeviceReplace
// does — only backends.ErrUnsupported is distinguished — so an unknown
// central here answers 502, not 404.
func TestPostInstallModeSearch_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceSearcher{err: hmerr.ErrUnknownCentral}
	body := strings.NewReader(`{"interface":"BidCos-Wired","central":"nope"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	PostInstallModeSearch(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostInstallModeSearch_GenericBackendError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubDeviceSearcher{err: errors.New("CCU unreachable")}
	body := strings.NewReader(`{"interface":"BidCos-Wired"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	PostInstallModeSearch(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}
