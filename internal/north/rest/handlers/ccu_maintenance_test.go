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

// fakeCCUPositionSetter records the arguments of the last position write
// and returns a configurable error.
type fakeCCUPositionSetter struct {
	lastCentral string
	lastLon     float64
	lastLat     float64
	calls       int
	err         error
}

func (f *fakeCCUPositionSetter) SetCCUPosition(_ context.Context, central string, longitude, latitude float64) error {
	f.calls++
	f.lastCentral = central
	f.lastLon = longitude
	f.lastLat = latitude
	return f.err
}

func ccuPositionRequestBody(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(chiContext(req, map[string]string{"central": "home"}))
}

func TestPutCCUPosition_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := ccuPositionRequestBody(`{"longitude":10,"latitude":50}`)
	w := httptest.NewRecorder()
	PutCCUPosition(nil, nil).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutCCUPosition_MissingCentral_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeCCUPositionSetter{}
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"longitude":10,"latitude":50}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(chiContext(req, map[string]string{"central": ""}))
	w := httptest.NewRecorder()
	PutCCUPosition(svc, nil).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 0 {
		t.Fatalf("backend must not be called on a missing central")
	}
}

func TestPutCCUPosition_MalformedBody_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeCCUPositionSetter{}
	w := httptest.NewRecorder()
	PutCCUPosition(svc, nil).ServeHTTP(w, ccuPositionRequestBody(`{`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 0 {
		t.Fatalf("backend must not be called on a malformed body")
	}
}

// TestPutCCUPosition_NoCoordinates_Returns400 verifies that a well-formed
// but empty body — neither coordinate present — is rejected rather than
// silently defaulting to 0/0, which would be a real (wrong) place.
func TestPutCCUPosition_NoCoordinates_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeCCUPositionSetter{}
	w := httptest.NewRecorder()
	PutCCUPosition(svc, nil).ServeHTTP(w, ccuPositionRequestBody(`{}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 0 {
		t.Fatalf("backend must not be called when both coordinates are missing")
	}
}

// TestPutCCUPosition_OnlyLongitude_Returns400 verifies that a half-supplied
// body (only one coordinate) is rejected — writing just one would leave the
// CCU with a position that is half old and half new.
func TestPutCCUPosition_OnlyLongitude_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeCCUPositionSetter{}
	w := httptest.NewRecorder()
	PutCCUPosition(svc, nil).ServeHTTP(w, ccuPositionRequestBody(`{"longitude":10}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 0 {
		t.Fatalf("backend must not be called with only one coordinate")
	}
}

func TestPutCCUPosition_UnknownCentral_Returns404(t *testing.T) {
	t.Parallel()
	svc := &fakeCCUPositionSetter{err: hmerr.ErrUnknownCentral}
	w := httptest.NewRecorder()
	PutCCUPosition(svc, nil).ServeHTTP(w, ccuPositionRequestBody(`{"longitude":10,"latitude":50}`))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutCCUPosition_ValidationError_Returns422(t *testing.T) {
	t.Parallel()
	svc := &fakeCCUPositionSetter{err: hmerr.ErrValidation}
	w := httptest.NewRecorder()
	PutCCUPosition(svc, nil).ServeHTTP(w, ccuPositionRequestBody(`{"longitude":200,"latitude":50}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutCCUPosition_UnsupportedBackend_Returns422(t *testing.T) {
	t.Parallel()
	svc := &fakeCCUPositionSetter{err: backends.ErrUnsupported}
	w := httptest.NewRecorder()
	PutCCUPosition(svc, nil).ServeHTTP(w, ccuPositionRequestBody(`{"longitude":10,"latitude":50}`))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutCCUPosition_UpstreamFailure_Returns502(t *testing.T) {
	t.Parallel()
	svc := &fakeCCUPositionSetter{err: hmerr.ErrNoConnection}
	w := httptest.NewRecorder()
	PutCCUPosition(svc, nil).ServeHTTP(w, ccuPositionRequestBody(`{"longitude":10,"latitude":50}`))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutCCUPosition_HappyPath_Returns204AndAudits(t *testing.T) {
	t.Parallel()
	svc := &fakeCCUPositionSetter{}
	rec := &captureRecorder{}
	w := httptest.NewRecorder()
	PutCCUPosition(svc, rec).ServeHTTP(w, ccuPositionRequestBody(`{"longitude":10.222946,"latitude":53.551086}`))
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.lastCentral != "home" || svc.lastLon != 10.222946 || svc.lastLat != 53.551086 {
		t.Fatalf("backend args mismatch: central=%q lon=%g lat=%g", svc.lastCentral, svc.lastLon, svc.lastLat)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(rec.entries))
	}
	if got := rec.entries[0]; got.Action != audit.ActionSystemCCUPosition || got.Note != "home" {
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
