// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
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

// fakeFirmwareDownloader records the arguments of the last download call
// and returns a configurable error.
type fakeFirmwareDownloader struct {
	lastCentral string
	lastURL     string
	err         error
}

func (f *fakeFirmwareDownloader) DownloadFirmware(_ context.Context, central, url string) error {
	f.lastCentral = central
	f.lastURL = url
	return f.err
}

func firmwareDownloadRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/system/firmware/download", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestPostSystemFirmwareDownload_NilService_Returns503(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	PostSystemFirmwareDownload(nil, nil).ServeHTTP(w, firmwareDownloadRequest(`{"url":"https://x/fw.tgz"}`))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostSystemFirmwareDownload_MissingURL_Returns422(t *testing.T) {
	t.Parallel()
	svc := &fakeFirmwareDownloader{}
	w := httptest.NewRecorder()
	PostSystemFirmwareDownload(svc, nil).ServeHTTP(w, firmwareDownloadRequest(`{"central":"home"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastURL != "" {
		t.Fatalf("backend must not be called on missing url, got %q", svc.lastURL)
	}
}

func TestPostSystemFirmwareDownload_NonHTTPScheme_Returns422(t *testing.T) {
	t.Parallel()
	svc := &fakeFirmwareDownloader{}
	w := httptest.NewRecorder()
	PostSystemFirmwareDownload(svc, nil).ServeHTTP(w, firmwareDownloadRequest(`{"url":"ftp://x/fw.tgz"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastURL != "" {
		t.Fatalf("backend must not be called on non-http url, got %q", svc.lastURL)
	}
}

func TestPostSystemFirmwareDownload_MalformedBody_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeFirmwareDownloader{}
	w := httptest.NewRecorder()
	PostSystemFirmwareDownload(svc, nil).ServeHTTP(w, firmwareDownloadRequest(`{`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostSystemFirmwareDownload_UnknownCentral_Returns404(t *testing.T) {
	t.Parallel()
	svc := &fakeFirmwareDownloader{err: hmerr.ErrUnknownCentral}
	w := httptest.NewRecorder()
	PostSystemFirmwareDownload(svc, nil).ServeHTTP(w, firmwareDownloadRequest(`{"url":"https://x/fw.tgz","central":"nope"}`))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostSystemFirmwareDownload_Unsupported_Returns422(t *testing.T) {
	t.Parallel()
	svc := &fakeFirmwareDownloader{err: backends.ErrUnsupported}
	w := httptest.NewRecorder()
	PostSystemFirmwareDownload(svc, nil).ServeHTTP(w, firmwareDownloadRequest(`{"url":"https://x/fw.tgz"}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostSystemFirmwareDownload_UpstreamFailure_Returns502(t *testing.T) {
	t.Parallel()
	svc := &fakeFirmwareDownloader{err: hmerr.ErrNoConnection}
	w := httptest.NewRecorder()
	PostSystemFirmwareDownload(svc, nil).ServeHTTP(w, firmwareDownloadRequest(`{"url":"https://x/fw.tgz"}`))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPostSystemFirmwareDownload_HappyPath_Returns202AndAudits(t *testing.T) {
	t.Parallel()
	svc := &fakeFirmwareDownloader{}
	rec := &captureRecorder{}
	w := httptest.NewRecorder()
	PostSystemFirmwareDownload(svc, rec).ServeHTTP(w, firmwareDownloadRequest(`{"url":"https://x/fw.tgz","central":"home"}`))
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastCentral != "home" || svc.lastURL != "https://x/fw.tgz" {
		t.Fatalf("backend args mismatch: central=%q url=%q", svc.lastCentral, svc.lastURL)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(rec.entries))
	}
	if got := rec.entries[0]; got.Action != audit.ActionSystemFirmwareDownload ||
		got.Note != "home https://x/fw.tgz" {
		t.Fatalf("audit entry mismatch: %+v", got)
	}
}

func TestPostSystemFirmwareDownload_SingleCentral_TrimsURLAndAudits(t *testing.T) {
	t.Parallel()
	svc := &fakeFirmwareDownloader{}
	rec := &captureRecorder{}
	w := httptest.NewRecorder()
	// No central supplied (single-CCU convenience) and surrounding
	// whitespace on the URL is trimmed before dispatch.
	PostSystemFirmwareDownload(svc, rec).ServeHTTP(w, firmwareDownloadRequest(`{"url":"  https://x/fw.tgz  "}`))
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastURL != "https://x/fw.tgz" {
		t.Fatalf("expected trimmed url, got %q", svc.lastURL)
	}
	if got := rec.entries[0]; got.Note != "https://x/fw.tgz" {
		t.Fatalf("expected note without central, got %q", got.Note)
	}
}
