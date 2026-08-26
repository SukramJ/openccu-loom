// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/diagnostics"
)

// fakeStartupCaptureService is a minimal implementation of StartupCaptureService for tests.
type fakeStartupCaptureService struct {
	loadResult diagnostics.StartupCaptureConfig
	loadErr    error
	saveErr    error
	savedCfg   *diagnostics.StartupCaptureConfig
}

func (f *fakeStartupCaptureService) Load() (diagnostics.StartupCaptureConfig, error) {
	return f.loadResult, f.loadErr
}

func (f *fakeStartupCaptureService) Save(cfg diagnostics.StartupCaptureConfig) error {
	f.savedCfg = &cfg
	return f.saveErr
}

// --- GetStartupCapture ---

func TestGetStartupCapture_NilService_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/startup-capture", http.NoBody)
	w := httptest.NewRecorder()
	GetStartupCapture(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetStartupCapture_HappyPath_Returns200(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{
		loadResult: diagnostics.StartupCaptureConfig{Enabled: true, DurationS: 60, Anonymise: true},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/startup-capture", http.NoBody)
	w := httptest.NewRecorder()
	GetStartupCapture(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetStartupCapture_LoadError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{
		loadErr: errors.New("disk error"),
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/startup-capture", http.NoBody)
	w := httptest.NewRecorder()
	GetStartupCapture(svc).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- PutStartupCapture ---

func TestPutStartupCapture_NilService_Returns503(t *testing.T) {
	t.Parallel()
	body := `{"enabled":true,"duration_seconds":30,"anonymise":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutStartupCapture(nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutStartupCapture_MalformedJSON_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", bytes.NewBufferString(`{not valid`))
	w := httptest.NewRecorder()
	PutStartupCapture(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutStartupCapture_NegativeDuration_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{}
	body := `{"enabled":false,"duration_seconds":-1}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutStartupCapture(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutStartupCapture_SaveError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{saveErr: errors.New("write failed")}
	body := `{"enabled":true,"duration_seconds":30}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutStartupCapture(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutStartupCapture_HappyPath_Returns200(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{}
	body := `{"enabled":true,"duration_seconds":30,"anonymise":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutStartupCapture(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.savedCfg == nil {
		t.Fatal("expected Save to be called")
	}
	if !svc.savedCfg.Enabled {
		t.Fatal("expected Enabled=true in saved config")
	}
}

// TestPutStartupCapture_OmittedAnonymise_PersistsTrue crosses the handler's
// decode seam: a body that enables the boot capture without naming `anonymise`
// must persist the privacy default rather than a zero-valued false, or the next
// boot archives raw device addresses nobody opted into.
func TestPutStartupCapture_OmittedAnonymise_PersistsTrue(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{}
	body := `{"enabled":true,"duration_seconds":30}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutStartupCapture(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.savedCfg == nil {
		t.Fatal("expected Save to be called")
	}
	if !svc.savedCfg.Anonymise {
		t.Error("saved Anonymise = false for a body that omitted the key, want true")
	}

	// An explicit opt-out is still honoured.
	svc2 := &fakeStartupCaptureService{}
	req2 := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture",
		strings.NewReader(`{"enabled":true,"anonymise":false}`))
	w2 := httptest.NewRecorder()
	PutStartupCapture(svc2, nil).ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w2.Code, w2.Body.String())
	}
	if svc2.savedCfg == nil || svc2.savedCfg.Anonymise {
		t.Errorf("explicit anonymise=false was not persisted: %+v", svc2.savedCfg)
	}
}

func TestPutStartupCapture_AuditRecorderCalled_OnEnable(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{}
	rec := audit.NewBuffer(10)
	body := `{"enabled":true,"duration_seconds":30}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", strings.NewReader(body))
	req = withIdentity(req, auth.Identity{Subject: "alice", Role: auth.RoleAdmin})
	w := httptest.NewRecorder()
	PutStartupCapture(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	entries := rec.List(10)
	if len(entries) != 1 {
		t.Fatalf("audit entries=%d, want 1", len(entries))
	}
	if entries[0].Action != audit.Action("diagnostics.startup_capture_enabled") {
		t.Fatalf("unexpected action: %q", entries[0].Action)
	}
	// The row must name the admin who changed the capture configuration.
	if entries[0].User != "alice" {
		t.Fatalf("audit user=%q, want alice", entries[0].User)
	}
}

func TestPutStartupCapture_AuditRecorderCalled_OnDisable(t *testing.T) {
	t.Parallel()
	svc := &fakeStartupCaptureService{}
	rec := audit.NewBuffer(10)
	body := `{"enabled":false,"duration_seconds":30}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/startup-capture", strings.NewReader(body))
	w := httptest.NewRecorder()
	PutStartupCapture(svc, rec).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	entries := rec.List(10)
	if len(entries) != 1 {
		t.Fatalf("audit entries=%d, want 1", len(entries))
	}
	if entries[0].Action != audit.Action("diagnostics.startup_capture_disabled") {
		t.Fatalf("unexpected action: %q", entries[0].Action)
	}
}

// restartTestEnv takes over the process-global restart state for one
// test: it counts the signals instead of terminating the test binary and
// drives the grace window from a settable clock, so the retry path can be
// exercised without waiting out [restartGrace].
type restartTestEnv struct {
	signals atomic.Int32
	nowNS   atomic.Int64
}

func newRestartTestEnv(t *testing.T) *restartTestEnv {
	t.Helper()
	env := &restartTestEnv{}
	env.nowNS.Store(time.Now().UnixNano())
	origSignal, origNow := restartSignal, restartNow
	restartSignal = func() { env.signals.Add(1) }
	restartNow = func() time.Time { return time.Unix(0, env.nowNS.Load()) }
	restartSignalledAt.Store(0)
	t.Cleanup(func() {
		restartSignal = origSignal
		restartNow = origNow
		restartSignalledAt.Store(0)
	})
	return env
}

func (e *restartTestEnv) advance(d time.Duration) { e.nowNS.Add(int64(d)) }

// post issues one restart request and returns the `status` field the
// handler reported.
func (e *restartTestEnv) post(t *testing.T, h http.Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/restart", http.NoBody)
	req = withIdentity(req, auth.Identity{Subject: "alice", Role: auth.RoleAdmin})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode restart response: %v (body=%s)", err, w.Body.String())
	}
	return body.Status
}

// awaitSignals waits for the detached signal goroutines to land — they
// sleep 100 ms before firing — and asserts the total.
func (e *restartTestEnv) awaitSignals(t *testing.T, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && e.signals.Load() < want {
		time.Sleep(10 * time.Millisecond)
	}
	if got := e.signals.Load(); got != want {
		t.Fatalf("shutdown signals=%d, want exactly %d", got, want)
	}
}

// TestRestart_DoubleRequest_SignalsOnce verifies the double-fire guard:
// the SIGTERM is sent from a detached goroutine after the handler has
// returned, so two rapid requests must still produce exactly one signal
// while both are acknowledged and audited. The second response says so
// rather than reporting the same success as the first.
func TestRestart_DoubleRequest_SignalsOnce(t *testing.T) {
	env := newRestartTestEnv(t)
	rec := audit.NewBuffer(10)
	h := Restart(rec)

	if got := env.post(t, h); got != "shutdown_signalled" {
		t.Fatalf("first status=%q, want shutdown_signalled", got)
	}
	if got := env.post(t, h); got != "shutdown_in_progress" {
		t.Fatalf("second status=%q, want shutdown_in_progress", got)
	}

	env.awaitSignals(t, 1)
	entries := rec.List(10)
	if len(entries) != 2 {
		t.Fatalf("audit entries=%d, want 2 (both requests are recorded)", len(entries))
	}
	if entries[0].User != "alice" {
		t.Fatalf("audit user=%q, want alice", entries[0].User)
	}
}

// TestRestart_AfterGraceWindow_SignalsAgain pins the retry path. A
// graceful shutdown that never terminates the process (a south-bound
// call that will not unblock, an aborted sequence) leaves the daemon
// running, and the operator's second attempt has to send a real signal
// instead of collecting another success line for a no-op.
func TestRestart_AfterGraceWindow_SignalsAgain(t *testing.T) {
	env := newRestartTestEnv(t)
	h := Restart(nil)

	if got := env.post(t, h); got != "shutdown_signalled" {
		t.Fatalf("first status=%q, want shutdown_signalled", got)
	}
	env.awaitSignals(t, 1)

	// The process is still alive well past the shutdown budget.
	env.advance(restartGrace + time.Second)
	if got := env.post(t, h); got != "shutdown_signalled" {
		t.Fatalf("retry status=%q, want shutdown_signalled", got)
	}
	env.awaitSignals(t, 2)
}

// TestRestart_ClockStepBackDoesNotWedgeTheEndpoint pins the wall-clock
// caveat: the last-signal stamp has no monotonic reading, so a clock
// stepped backwards (NTP on a box without an RTC) must not turn the
// grace window into an open-ended block.
func TestRestart_ClockStepBackDoesNotWedgeTheEndpoint(t *testing.T) {
	env := newRestartTestEnv(t)
	h := Restart(nil)

	if got := env.post(t, h); got != "shutdown_signalled" {
		t.Fatalf("first status=%q, want shutdown_signalled", got)
	}
	env.awaitSignals(t, 1)

	env.advance(-2 * time.Hour)
	if got := env.post(t, h); got != "shutdown_signalled" {
		t.Fatalf("status after a backwards clock step=%q, want shutdown_signalled", got)
	}
	env.awaitSignals(t, 2)
}

// TestRestart_AuditNoteCarriesOutcome pins that the trail distinguishes
// the signalled request from the suppressed duplicate — otherwise the
// last rows before the daemon goes quiet cannot be told apart.
func TestRestart_AuditNoteCarriesOutcome(t *testing.T) {
	env := newRestartTestEnv(t)
	rec := audit.NewBuffer(10)
	h := Restart(rec)

	env.post(t, h)
	env.post(t, h)
	env.awaitSignals(t, 1)

	entries := rec.List(10)
	if len(entries) != 2 {
		t.Fatalf("audit entries=%d, want 2", len(entries))
	}
	notes := []string{entries[0].Note, entries[1].Note}
	if !slices.Contains(notes, "shutdown_signalled") || !slices.Contains(notes, "shutdown_in_progress") {
		t.Fatalf("audit notes=%v, want one of each outcome", notes)
	}
	for _, e := range entries {
		if e.Action != audit.ActionSystemRestartRequested {
			t.Fatalf("audit action=%q, want %q", e.Action, audit.ActionSystemRestartRequested)
		}
	}
}
