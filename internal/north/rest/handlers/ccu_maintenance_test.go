// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// fakeCCUHostActioner implements CCUHostActionPort. Each of the three
// actions has its own central/error slots so a test can drive one action
// without the others' stubs interfering.
type fakeCCUHostActioner struct {
	poweroffCentral string
	poweroffErr     error

	safeModeCentral string
	safeModeErr     error

	recoveryModeCentral string
	recoveryModeErr     error
}

func (f *fakeCCUHostActioner) PoweroffCCU(_ context.Context, central string) error {
	f.poweroffCentral = central
	return f.poweroffErr
}

func (f *fakeCCUHostActioner) EnterSafeMode(_ context.Context, central string) error {
	f.safeModeCentral = central
	return f.safeModeErr
}

func (f *fakeCCUHostActioner) EnterRecoveryMode(_ context.Context, central string) error {
	f.recoveryModeCentral = central
	return f.recoveryModeErr
}

// ccuHostActionCase describes one of the three host actions for the
// table-driven ladder + audit tests below: which handler constructor to
// exercise, which error field on fakeCCUHostActioner drives it, which
// central field records the call, and which audit.Action it must record.
// Testing all three through one table (rather than three independent copy
// -pasted suites) is what catches a copy-paste error that swaps the
// audit action or the error field between two of the three handlers.
type ccuHostActionCase struct {
	name        string
	handler     func(svc CCUHostActionPort, rec audit.Recorder) http.HandlerFunc
	auditAction audit.Action
	setErr      func(f *fakeCCUHostActioner, err error)
	lastCentral func(f *fakeCCUHostActioner) string
}

func ccuHostActionCases() []ccuHostActionCase {
	return []ccuHostActionCase{
		{
			name:        "Poweroff",
			handler:     PostCCUPoweroff,
			auditAction: audit.ActionSystemCCUPoweroff,
			setErr:      func(f *fakeCCUHostActioner, err error) { f.poweroffErr = err },
			lastCentral: func(f *fakeCCUHostActioner) string { return f.poweroffCentral },
		},
		{
			name:        "SafeMode",
			handler:     PostCCUSafeMode,
			auditAction: audit.ActionSystemCCUSafeMode,
			setErr:      func(f *fakeCCUHostActioner, err error) { f.safeModeErr = err },
			lastCentral: func(f *fakeCCUHostActioner) string { return f.safeModeCentral },
		},
		{
			name:        "RecoveryMode",
			handler:     PostCCURecoveryMode,
			auditAction: audit.ActionSystemCCURecoveryMode,
			setErr:      func(f *fakeCCUHostActioner, err error) { f.recoveryModeErr = err },
			lastCentral: func(f *fakeCCUHostActioner) string { return f.recoveryModeCentral },
		},
	}
}

func ccuHostActionRequest(centralParam string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	return req.WithContext(chiContext(req, map[string]string{"central": centralParam}))
}

// TestCCUHostAction_NilService_Returns503 covers the status ladder shared by
// all three host actions: a nil service must be reported as unwired.
func TestCCUHostAction_NilService_Returns503(t *testing.T) {
	t.Parallel()
	for _, tc := range ccuHostActionCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			tc.handler(nil, nil).ServeHTTP(w, ccuHostActionRequest("home"))
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// TestCCUHostAction_MissingCentral_Returns400 verifies the central path
// parameter is required for every host action.
func TestCCUHostAction_MissingCentral_Returns400(t *testing.T) {
	t.Parallel()
	for _, tc := range ccuHostActionCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &fakeCCUHostActioner{}
			w := httptest.NewRecorder()
			tc.handler(svc, nil).ServeHTTP(w, ccuHostActionRequest(""))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// TestCCUHostAction_UnknownCentral_Returns404 verifies
// hmerr.ErrUnknownCentral maps to 404 for every host action.
func TestCCUHostAction_UnknownCentral_Returns404(t *testing.T) {
	t.Parallel()
	for _, tc := range ccuHostActionCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &fakeCCUHostActioner{}
			tc.setErr(svc, hmerr.ErrUnknownCentral)
			w := httptest.NewRecorder()
			tc.handler(svc, nil).ServeHTTP(w, ccuHostActionRequest("nope"))
			if w.Code != http.StatusNotFound {
				t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// TestCCUHostAction_UnsupportedBackend_Returns422 verifies
// backends.ErrUnsupported (CUxD/Homegear, or a firmware without the
// method) maps to 422 — an operator-fixable request, not a transport
// failure.
func TestCCUHostAction_UnsupportedBackend_Returns422(t *testing.T) {
	t.Parallel()
	for _, tc := range ccuHostActionCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &fakeCCUHostActioner{}
			tc.setErr(svc, backends.ErrUnsupported)
			w := httptest.NewRecorder()
			tc.handler(svc, nil).ServeHTTP(w, ccuHostActionRequest("home"))
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// TestCCUHostAction_UpstreamFailure_Returns502 verifies a generic backend
// error (CCU-side transport failure) maps to 502 for every host action.
func TestCCUHostAction_UpstreamFailure_Returns502(t *testing.T) {
	t.Parallel()
	for _, tc := range ccuHostActionCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &fakeCCUHostActioner{}
			tc.setErr(svc, hmerr.ErrNoConnection)
			w := httptest.NewRecorder()
			tc.handler(svc, nil).ServeHTTP(w, ccuHostActionRequest("home"))
			if w.Code != http.StatusBadGateway {
				t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// TestCCUHostAction_HappyPath_Returns202AndAuditsOwnAction is the case that
// would stay silent under a copy-paste bug: each handler must record ITS
// OWN audit.Action, not one borrowed from a sibling handler that shares the
// same ccuHostActionHandler plumbing.
func TestCCUHostAction_HappyPath_Returns202AndAuditsOwnAction(t *testing.T) {
	t.Parallel()
	for _, tc := range ccuHostActionCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &fakeCCUHostActioner{}
			rec := &captureRecorder{}
			w := httptest.NewRecorder()
			tc.handler(svc, rec).ServeHTTP(w, ccuHostActionRequest("home"))
			if w.Code != http.StatusAccepted {
				t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
			}
			if got := tc.lastCentral(svc); got != "home" {
				t.Fatalf("expected the action to target central=home, got %q", got)
			}
			if len(rec.entries) != 1 {
				t.Fatalf("expected 1 audit entry, got %d", len(rec.entries))
			}
			if got := rec.entries[0]; got.Action != tc.auditAction || got.Note != "home" {
				t.Fatalf("audit entry mismatch: got %+v, want Action=%q Note=home", got, tc.auditAction)
			}
		})
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
	calls       int
	err         error
}

func (f *fakeFirmwareDownloader) DownloadFirmware(_ context.Context, central string) error {
	f.lastCentral = central
	f.calls++
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

// TestPostSystemFirmwareDownload_NoURL_Proceeds pins that a body without a
// url is a complete request. The CCU downloads the firmware matching its own
// version and board serial, so there is nothing for the caller to name; the
// endpoint used to answer 422 for a field it could not have used.
func TestPostSystemFirmwareDownload_NoURL_Proceeds(t *testing.T) {
	t.Parallel()
	svc := &fakeFirmwareDownloader{}
	w := httptest.NewRecorder()
	PostSystemFirmwareDownload(svc, nil).ServeHTTP(w, firmwareDownloadRequest(`{"central":"home"}`))
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 1 || svc.lastCentral != "home" {
		t.Fatalf("expected one download for central home, got calls=%d central=%q", svc.calls, svc.lastCentral)
	}
}

// TestPostSystemFirmwareDownload_URLIsAcceptedAndIgnored pins the
// compatibility half of that decision: a client written against the earlier
// contract still sends a url, and even an unusable one must not turn into a
// validation error for a field the daemon no longer reads.
func TestPostSystemFirmwareDownload_URLIsAcceptedAndIgnored(t *testing.T) {
	t.Parallel()
	svc := &fakeFirmwareDownloader{}
	w := httptest.NewRecorder()
	PostSystemFirmwareDownload(svc, nil).ServeHTTP(w, firmwareDownloadRequest(`{"url":"ftp://x/fw.tgz"}`))
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 1 {
		t.Fatalf("expected the download to proceed, got calls=%d", svc.calls)
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
	if svc.lastCentral != "home" {
		t.Fatalf("backend central mismatch: %q", svc.lastCentral)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(rec.entries))
	}
	// The note names the central, never the ignored url: an audit trail
	// that records an image the CCU never fetched is a false record.
	if got := rec.entries[0]; got.Action != audit.ActionSystemFirmwareDownload || got.Note != "home" {
		t.Fatalf("audit entry mismatch: %+v", got)
	}
}

func TestPostSystemFirmwareDownload_SingleCentral_Audits(t *testing.T) {
	t.Parallel()
	svc := &fakeFirmwareDownloader{}
	rec := &captureRecorder{}
	w := httptest.NewRecorder()
	// No central supplied: the single-CCU convenience the other system
	// endpoints share. The empty body is a valid request now that the url
	// carries no meaning.
	PostSystemFirmwareDownload(svc, rec).ServeHTTP(w, firmwareDownloadRequest(`{}`))
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 1 {
		t.Fatalf("expected the download to be dispatched, got calls=%d", svc.calls)
	}
	if got := rec.entries[0]; got.Note != "" {
		t.Fatalf("expected an empty note when no central was named, got %q", got.Note)
	}
}
