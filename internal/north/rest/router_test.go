// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/diagnostics"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/store/masterprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// ---------------------------------------------------------------------------
// Fakes for router-level tests (device admin, incidents, backup, etc.)
// ---------------------------------------------------------------------------

type fakeAdmin struct {
	unpairs int
	renames int
	accepts int
}

func (f *fakeAdmin) UnpairDevice(_ context.Context, _ string) error { f.unpairs++; return nil }
func (f *fakeAdmin) RenameDevice(_ context.Context, _, _ string, _ bool) error {
	f.renames++
	return nil
}

func (f *fakeAdmin) RenameChannel(_ context.Context, _ string, _ int, _ string) error { return nil }
func (f *fakeAdmin) AcceptInboxDevice(_ context.Context, _ string) error {
	f.accepts++
	return nil
}
func (f *fakeAdmin) UpdateFirmware(_ context.Context, _ string) error           { return nil }
func (f *fakeAdmin) SetRooms(_ context.Context, _ string, _ []string) error     { return nil }
func (f *fakeAdmin) SetFunctions(_ context.Context, _ string, _ []string) error { return nil }

type fakeIncidents struct{ items []handlers.Incident }

func (f *fakeIncidents) Incidents() []handlers.Incident { return f.items }

type fakeBackup struct {
	jobs []handlers.BackupEntry
}

func (f *fakeBackup) TriggerBackup(context.Context) (string, error) { return "job-1", nil }
func (f *fakeBackup) List(context.Context) ([]handlers.BackupEntry, error) {
	return f.jobs, nil
}

func (f *fakeBackup) Stream(_ context.Context, _ string, w io.Writer) error {
	_, err := w.Write([]byte("payload"))
	return err
}

func (f *fakeBackup) Restore(_ context.Context, id string) (string, error) {
	return id, nil
}

func (f *fakeBackup) TriggerBackupForCentral(context.Context, string) (string, error) {
	return "job-1", nil
}
func (f *fakeBackup) Prune(context.Context, string, int) error { return nil }

// ---------------------------------------------------------------------------
// Fakes for router branch-coverage tests (dep-guarded routes).
// ---------------------------------------------------------------------------

type fakeAuditService struct{}

func (fakeAuditService) List(_ int) []audit.Entry { return nil }

type fakeLinksService struct{}

func (fakeLinksService) ListLinks(_ context.Context, _, _ string) ([]handlers.Link, error) {
	return nil, nil
}
func (fakeLinksService) AddLink(_ context.Context, _, _, _, _ string) error { return nil }
func (fakeLinksService) RemoveLink(_ context.Context, _, _ string) error    { return nil }
func (fakeLinksService) LinkableChannels(_ context.Context, _, _, _, _ string) ([]handlers.LinkableChannel, error) {
	return nil, nil
}

type fakeScheduleService struct{}

func (fakeScheduleService) GetClimateSchedule(_ context.Context, _ string, _ int) (*handlers.ClimateSchedule, error) {
	return &handlers.ClimateSchedule{}, nil
}

func (fakeScheduleService) PutClimateSchedule(_ context.Context, _ string, _ int, _ *handlers.ClimateSchedule) error {
	return nil
}

func (fakeScheduleService) SetActiveProfile(_ context.Context, _ string, _ int, _ string) error {
	return nil
}

func (fakeScheduleService) GetClimateScheduleAuto(_ context.Context, _ string) (*handlers.ClimateSchedule, error) {
	return &handlers.ClimateSchedule{}, nil
}

func (fakeScheduleService) PutClimateScheduleAuto(_ context.Context, _ string, _ *handlers.ClimateSchedule) error {
	return nil
}
func (fakeScheduleService) SetActiveProfileAuto(_ context.Context, _, _ string) error { return nil }
func (fakeScheduleService) FindScheduleChannel(_ context.Context, _ string) (int, error) {
	return 1, nil
}

func (fakeScheduleService) CopySchedule(_ context.Context, _, _ string) error { return nil }

func (fakeScheduleService) CopyClimateProfile(_ context.Context, _ string, _ int, _ string, _ int) error {
	return nil
}

type fakeCentralLinksService struct{}

func (fakeCentralLinksService) CreateCentralLinks(_ context.Context, _ string) (handlers.CentralLinksReport, error) {
	return handlers.CentralLinksReport{}, nil
}

func (fakeCentralLinksService) RemoveCentralLinks(_ context.Context, _ string) (handlers.CentralLinksReport, error) {
	return handlers.CentralLinksReport{}, nil
}

func (fakeCentralLinksService) CentralLinksStatus(_ string) (handlers.CentralLinksStatus, error) {
	return handlers.CentralLinksStatus{}, nil
}

type fakeIncidentsReader struct{}

func (fakeIncidentsReader) Incidents() []handlers.Incident { return nil }

// stubDeviceIndexForRouter is a minimal handlers.DeviceIndex for
// router-level master-profiles route-mounting tests: one device "DEV1"
// with one channel "DEV1:1".
type stubDeviceIndexForRouter struct{}

func (stubDeviceIndexForRouter) Devices() []*device.Device { return nil }

func (stubDeviceIndexForRouter) Device(address string) (*device.Device, bool) {
	if address != "DEV1" {
		return nil, false
	}
	d := device.New(device.Config{
		Address:     "DEV1",
		Model:       "HmIP-eTRV",
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: "HmIP-RF@CCU",
		Name:        "Test",
	})
	d.AddChannel("DEV1:1", 1, "CLIMATECONTROL", hmenum.ParamsetKeyMaster)
	return d, true
}

func (stubDeviceIndexForRouter) CentralOf(string) string    { return "" }
func (stubDeviceIndexForRouter) SerialSuffix(string) string { return "" }

// fakeIncidentsClearer is a minimal handlers.IncidentsClearer for
// router-level route-mounting tests.
type fakeIncidentsClearer struct{ calls int }

func (f *fakeIncidentsClearer) ClearIncidents(context.Context) error {
	f.calls++
	return nil
}

// fakeMasterProfilesService is a minimal handlers.MasterProfilesService
// for router-level route-mounting tests.
type fakeMasterProfilesService struct{}

func (fakeMasterProfilesService) Profiles(_, _ string) ([]masterprofile.Profile, error) {
	return []masterprofile.Profile{{ID: 1, Name: map[string]string{"en": "Eco"}}}, nil
}

func (fakeMasterProfilesService) Profile(_, _ string, id int) (masterprofile.Profile, error) {
	return masterprofile.Profile{ID: id}, nil
}

func (fakeMasterProfilesService) MatchActiveProfile(_, _ string, _ map[string]any) int { return 0 }

type fakeSystemStatusReader struct{}

func (fakeSystemStatusReader) SystemStatusEntries() []handlers.SystemStatusEntry { return nil }

type fakeLogLevelsService struct{}

func (fakeLogLevelsService) Default() slog.Level                         { return slog.LevelInfo }
func (fakeLogLevelsService) Set(_ string, _ slog.Level, _ time.Duration) {}
func (fakeLogLevelsService) Reset(_ string) bool                         { return true }
func (fakeLogLevelsService) Snapshot() []hmlog.OverrideInfo              { return nil }

type fakeStartupCaptureService struct{}

func (fakeStartupCaptureService) Load() (diagnostics.StartupCaptureConfig, error) {
	return diagnostics.StartupCaptureConfig{}, nil
}
func (fakeStartupCaptureService) Save(_ diagnostics.StartupCaptureConfig) error { return nil }

type fakeCaptureService struct{}

func (fakeCaptureService) Start(_ diagnostics.StartOptions) (diagnostics.Summary, error) {
	return diagnostics.Summary{ID: "x"}, nil
}

func (fakeCaptureService) Stop(_ string) (diagnostics.Summary, error) {
	return diagnostics.Summary{}, nil
}
func (fakeCaptureService) List() []diagnostics.Summary { return nil }
func (fakeCaptureService) Get(_ string) (diagnostics.Summary, error) {
	return diagnostics.Summary{}, nil
}
func (fakeCaptureService) OpenArchive(_ string) ([]byte, error) { return nil, nil }

type fakeValuesCacheService struct{}

func (fakeValuesCacheService) DeleteAll(_ context.Context) error                    { return nil }
func (fakeValuesCacheService) DeleteDevice(_ context.Context, _, _, _ string) error { return nil }
func (fakeValuesCacheService) Stats(_ context.Context) (handlers.ValuesCacheStats, error) {
	return handlers.ValuesCacheStats{}, nil
}

func (fakeValuesCacheService) Metrics() handlers.ValuesCacheMetrics {
	return handlers.ValuesCacheMetrics{}
}

type fakeDeviceLookup struct{}

func (fakeDeviceLookup) LocateDevice(addr string) (centralName, interfaceID string, ok bool) {
	if addr == "A" {
		return "home", "HmIP-RF", true
	}
	return "", "", false
}

type fakeParamsetService struct{}

func (fakeParamsetService) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	return map[string]any{}, nil
}

func (fakeParamsetService) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any) error {
	return nil
}

func (fakeParamsetService) GetLinkParamset(_ context.Context, _, _ string) (map[string]any, error) {
	return map[string]any{}, nil
}

func (fakeParamsetService) PutLinkParamset(_ context.Context, _, _ string, _ map[string]any) error {
	return nil
}

type fakeRefreshDevicesService struct{}

func (fakeRefreshDevicesService) RefreshDevices(_ context.Context) error { return nil }

type fakeUISchemaService struct{}

func (fakeUISchemaService) UISchema(_ context.Context, _ handlers.UISchemaRequest) (*handlers.UISchema, error) {
	return &handlers.UISchema{}, nil
}

type fakeConfigExportService struct{}

func (fakeConfigExportService) ReadParamset(_ context.Context, _, _, _ string) (map[string]any, error) {
	return map[string]any{}, nil
}

func (fakeConfigExportService) WriteParamset(_ context.Context, _, _, _ string, _ map[string]any) error {
	return nil
}

type fakeDeviceAdmin struct{}

func (fakeDeviceAdmin) UnpairDevice(_ context.Context, _ string) error            { return nil }
func (fakeDeviceAdmin) RenameDevice(_ context.Context, _, _ string, _ bool) error { return nil }
func (fakeDeviceAdmin) RenameChannel(_ context.Context, _ string, _ int, _ string) error {
	return nil
}
func (fakeDeviceAdmin) AcceptInboxDevice(_ context.Context, _ string) error        { return nil }
func (fakeDeviceAdmin) UpdateFirmware(_ context.Context, _ string) error           { return nil }
func (fakeDeviceAdmin) SetRooms(_ context.Context, _ string, _ []string) error     { return nil }
func (fakeDeviceAdmin) SetFunctions(_ context.Context, _ string, _ []string) error { return nil }

// fakeSystemCCUReader is a minimal SystemCCUReader for router-level
// integration tests; the daemon adapter is exercised elsewhere.
type fakeSystemCCUReader struct{ entries []handlers.SystemCCUEntry }

func (f fakeSystemCCUReader) List(_ context.Context) []handlers.SystemCCUEntry { return f.entries }

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

type fakeConfig struct{ snap handlers.ConfigSnapshot }

func (f *fakeConfig) SanitizedConfig() handlers.ConfigSnapshot { return f.snap }

func newTestRouter(t *testing.T) *handlerHarness {
	t.Helper()
	tr := health.NewTracker()
	tr.Record("central", health.Sample{Healthy: true, Note: "ok"})
	cfg := &fakeConfig{snap: handlers.ConfigSnapshot{Locale: "de"}}
	r := NewRouter(Deps{
		StartedAt: time.Now().Add(-2 * time.Minute),
		Health:    tr,
		Config:    cfg,
	})
	return &handlerHarness{t: t, h: r}
}

type handlerHarness struct {
	t *testing.T
	h http.Handler
}

func (h *handlerHarness) do(method, path string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	rr := httptest.NewRecorder()
	h.h.ServeHTTP(rr, req)
	return rr
}

// routerGET issues a GET request against the supplied router and returns the status code.
func routerGET(r http.Handler, path string) int {
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, http.NoBody))
	return rr.Code
}

// passthroughMiddleware is a no-op auth wrapper for tests that
// exercise route wiring without exercising the auth middleware.
func passthroughMiddleware(next http.Handler) http.Handler { return next }

// ---------------------------------------------------------------------------
// Core router tests (info, health, config, error shapes, OpenAPI validator)
// ---------------------------------------------------------------------------

func TestInfoEndpoint(t *testing.T) {
	h := newTestRouter(t)
	rr := h.do(http.MethodGet, "/api/v1/info", nil)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var info handlers.InfoResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.Uptime == "" || info.StartedAt == "" {
		t.Fatalf("info=%+v", info)
	}
	if rr.Header().Get("X-Request-ID") == "" {
		t.Fatal("request id header missing")
	}
}

func TestHealthEndpointHealthy(t *testing.T) {
	h := newTestRouter(t)
	rr := h.do(http.MethodGet, "/api/v1/health", nil)
	if rr.Code != 200 {
		t.Fatalf("status=%d", rr.Code)
	}
	var body handlers.HealthResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Status != "healthy" {
		t.Fatalf("status=%s", body.Status)
	}
	if len(body.Components) != 1 || body.Components[0].Name != "central" {
		t.Fatalf("components=%+v", body.Components)
	}
}

func TestHealthUnhealthyReturns503(t *testing.T) {
	tr := health.NewTracker()
	tr.Record("central", health.Sample{Healthy: false})
	tr.Record("central", health.Sample{Healthy: false})
	r := NewRouter(Deps{Health: tr})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
}

func TestRouterUnknownEndpointProblemJSON(t *testing.T) {
	h := newTestRouter(t)
	rr := h.do(http.MethodGet, "/api/v1/does-not-exist", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != problem.ContentType {
		t.Fatalf("content-type=%s", ct)
	}
	if !strings.Contains(rr.Body.String(), "not_found") {
		t.Fatalf("body=%s", rr.Body.String())
	}
}

func TestRouterMethodNotAllowed(t *testing.T) {
	h := newTestRouter(t)
	rr := h.do(http.MethodPost, "/api/v1/info", strings.NewReader("{}"))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestConfigEndpoint(t *testing.T) {
	h := newTestRouter(t)
	rr := h.do(http.MethodGet, "/api/v1/config", nil)
	if rr.Code != 200 {
		t.Fatalf("status=%d", rr.Code)
	}
	var snap handlers.ConfigSnapshot
	_ = json.Unmarshal(rr.Body.Bytes(), &snap)
	if snap.Locale != "de" {
		t.Fatalf("snap=%+v", snap)
	}
}

// TestOpenAPIValidatorScopedToAPI verifies that the OpenAPI
// validator runs only for /api/v1/* routes — the SPA mount and any
// other root-level handlers must not be rejected as "Route not found
// in OpenAPI spec". Regression test for the bug where /app/ was
// returning 404 problem+json instead of serving the SPA.
func TestOpenAPIValidatorScopedToAPI(t *testing.T) {
	const spec = `
openapi: 3.1.0
info:
  title: t
  version: "1"
servers:
  - url: /api/v1
paths:
  /info:
    get:
      operationId: getInfo
      responses:
        "200":
          description: ok
`
	v, err := middleware.NewOpenAPIValidator(middleware.OpenAPIValidatorConfig{
		Spec: []byte(spec),
	})
	if err != nil {
		t.Fatalf("NewOpenAPIValidator: %v", err)
	}

	spaCalled := false
	spa := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		spaCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>spa</html>"))
	})

	tr := health.NewTracker()
	tr.Record("central", health.Sample{Healthy: true, Note: "ok"})
	r := NewRouter(Deps{
		StartedAt:        time.Now().Add(-time.Minute),
		Health:           tr,
		SPAHandler:       spa,
		OpenAPIValidator: v,
	})

	// /app/ → must reach the SPA handler, not the validator.
	req := httptest.NewRequest(http.MethodGet, "/app/", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if !spaCalled {
		t.Fatalf("SPA handler not invoked; status=%d body=%q", rr.Code, rr.Body.String())
	}
	if rr.Code != http.StatusOK {
		t.Errorf("/app/ status=%d, want 200", rr.Code)
	}

	// /api/v1/info → must be allowed by the validator (it is in the spec).
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody))
	if rr.Code != http.StatusOK {
		t.Errorf("/api/v1/info status=%d, want 200", rr.Code)
	}

	// /api/v1/not-in-spec → must be rejected by the validator with 404.
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/not-in-spec", http.NoBody))
	if rr.Code != http.StatusNotFound {
		t.Errorf("/api/v1/not-in-spec status=%d, want 404", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/problem+json") {
		t.Errorf("rejection body content-type=%q, want problem+json", ct)
	}
}

// TestRootRedirectsToSPA covers the root handler that lands a browser on the
// SPA, including the Home Assistant Ingress case where the Supervisor strips
// its proxy prefix and passes it back in X-Ingress-Path.
func TestRootRedirectsToSPA(t *testing.T) {
	spa := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r := NewRouter(Deps{StartedAt: time.Now(), SPAHandler: spa})

	cases := []struct {
		name        string
		ingressPath string
		wantLoc     string
	}{
		{"direct (no ingress)", "", "/app/"},
		{"ingress prefix", "/api/hassio_ingress/tok3n", "/api/hassio_ingress/tok3n/app/"},
		{"ingress prefix with trailing slash", "/api/hassio_ingress/tok3n/", "/api/hassio_ingress/tok3n/app/"},
		// Open-redirect guard: a hostile X-Ingress-Path must never steer the
		// redirect to a foreign origin — it falls back to the local SPA.
		{"reject scheme-relative", "//evil.example", "/app/"},
		{"reject absolute URL", "https://evil.example", "/app/"},
		{"reject backslash trick", "/\\evil.example", "/app/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			if tc.ingressPath != "" {
				req.Header.Set("X-Ingress-Path", tc.ingressPath)
			}
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != http.StatusFound {
				t.Fatalf("status=%d, want 302", rr.Code)
			}
			if loc := rr.Header().Get("Location"); loc != tc.wantLoc {
				t.Errorf("Location=%q, want %q", loc, tc.wantLoc)
			}
		})
	}
}

// TestRootHasNoRedirectWithoutSPA confirms the root redirect is only wired
// when an SPA handler is present (a headless daemon keeps root as a 404).
func TestRootHasNoRedirectWithoutSPA(t *testing.T) {
	r := NewRouter(Deps{StartedAt: time.Now()})
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if rr.Code == http.StatusFound {
		t.Fatalf("root unexpectedly redirected without an SPA handler")
	}
}

// ---------------------------------------------------------------------------
// Router dep-guarded route tests (nil/non-nil dep branches)
// ---------------------------------------------------------------------------

// TestRouter_StatusMetrics exercises the d.StatusMetrics != nil branch.
func TestRouter_StatusMetrics(t *testing.T) {
	t.Parallel()
	sm := middleware.NewStatusMetrics()
	r := NewRouter(Deps{
		StartedAt:     time.Now(),
		StatusMetrics: sm,
	})
	if code := routerGET(r, "/api/v1/info"); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
}

// TestRouter_CORS exercises the d.CORS != nil branch.
func TestRouter_CORS(t *testing.T) {
	t.Parallel()
	cors := &middleware.CORSConfig{Origins: []string{"https://example.com"}}
	r := NewRouter(Deps{StartedAt: time.Now(), CORS: cors})
	if code := routerGET(r, "/api/v1/info"); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
}

// TestRouter_Idempotent exercises the d.Idempotent branch.
func TestRouter_Idempotent(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{StartedAt: time.Now(), Idempotent: true})
	if code := routerGET(r, "/api/v1/info"); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
}

// TestRouter_AuthResolve exercises the d.AuthResolve != nil branch.
func TestRouter_AuthResolve(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{StartedAt: time.Now(), AuthResolve: passthroughMiddleware})
	if code := routerGET(r, "/api/v1/info"); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
}

// TestRouter_RateLimit exercises the d.RateLimit != nil branch.
func TestRouter_RateLimit(t *testing.T) {
	t.Parallel()
	rl := &middleware.RateLimitConfig{RequestsPerSecond: 100, Burst: 100}
	r := NewRouter(Deps{StartedAt: time.Now(), RateLimit: rl})
	if code := routerGET(r, "/api/v1/info"); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
}

// TestRouter_OIDC_branch exercises the d.OIDC != nil guard. We don't test
// the handler itself — just that the routes are registered (200 / 40x, not 404).
func TestRouter_OIDC_branch(t *testing.T) {
	t.Parallel()
	oidcDeps := &handlers.OIDCDeps{} // empty but non-nil → routes mounted
	r := NewRouter(Deps{StartedAt: time.Now(), OIDC: oidcDeps})
	// Route must exist (even if it returns an error — not 404).
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/start", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatalf("OIDC start route not mounted, got 404")
	}
}

// TestRouter_UISchema_route confirms the ui-schema route is registered when
// the dep is wired and absent when it is nil.
func TestRouter_UISchema_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{StartedAt: time.Now(), UISchema: fakeUISchemaService{}})
	rr := httptest.NewRecorder()
	withDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/devices/A/channels/0/ui-schema", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("UISchema route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	rr = httptest.NewRecorder()
	withoutDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/devices/A/channels/0/ui-schema", http.NoBody))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without UISchema dep, got %d", rr.Code)
	}
}

// TestRouter_ConfigExport_route confirms the config/export and config/import
// routes are always mounted. When ConfigExport is nil the handlers return
// 503 service_unready so the spec-documented response is reachable by the
// E2E walker instead of falling through to 404.
func TestRouter_ConfigExport_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{StartedAt: time.Now(), ConfigExport: fakeConfigExportService{}})
	rr := httptest.NewRecorder()
	withDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/devices/A/channels/0/config/export", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("config/export route not mounted")
	}

	// Routes are always mounted; nil dep → 503 service_unready (not 404).
	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	rr = httptest.NewRecorder()
	withoutDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/devices/A/channels/0/config/export", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatalf("config/export route must always be mounted; got 404 (want 503 when dep is nil)")
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without ConfigExport dep, got %d", rr.Code)
	}
}

// TestRouter_Links_route verifies link routes are registered only when wired.
func TestRouter_Links_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{StartedAt: time.Now(), Links: fakeLinksService{}})
	rr := httptest.NewRecorder()
	withDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/devices/A/links", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("links route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	rr = httptest.NewRecorder()
	withoutDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/devices/A/links", http.NoBody))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without Links dep, got %d", rr.Code)
	}
}

// TestRouter_Schedules_route verifies schedule routes are registered only when wired.
func TestRouter_Schedules_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{StartedAt: time.Now(), Schedules: fakeScheduleService{}})
	rr := httptest.NewRecorder()
	withDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/devices/A/channels/1/schedule", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("schedule route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	rr = httptest.NewRecorder()
	withoutDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/devices/A/channels/1/schedule", http.NoBody))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without Schedules dep, got %d", rr.Code)
	}
}

// TestRouter_Audit_route verifies audit route is mounted only when dep is wired.
func TestRouter_Audit_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{StartedAt: time.Now(), Audit: fakeAuditService{}})
	if code := routerGET(withDep, "/api/v1/audit"); code == http.StatusNotFound {
		t.Fatal("audit route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	if code := routerGET(withoutDep, "/api/v1/audit"); code != http.StatusNotFound {
		t.Fatalf("expected 404 without Audit dep, got %d", code)
	}
}

// TestRouter_Audit_RequiresAdmin locks the change-log endpoint behind the
// admin role: a viewer identity is rejected with 403, while an admin
// reaches the handler (200). The audit log can expose subjects, device
// addresses and operator actions, so any-authenticated-user access is a
// regression.
func TestRouter_Audit_RequiresAdmin(t *testing.T) {
	t.Parallel()
	mw := auth.NewMiddleware(nil, nil)
	build := func(role auth.Role) http.Handler {
		return NewRouter(Deps{
			StartedAt: time.Now(),
			Audit:     fakeAuditService{},
			AuthResolve: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ctx := auth.ContextWithIdentity(r.Context(), auth.Identity{Subject: "u", Role: role})
					next.ServeHTTP(w, r.WithContext(ctx))
				})
			},
			AuthRequire:  mw.Require,
			RequireAdmin: func(next http.Handler) http.Handler { return mw.RequireRole(auth.RoleAdmin, next) },
		})
	}

	rr := httptest.NewRecorder()
	build(auth.RoleViewer).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/audit", http.NoBody))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer must be forbidden from /audit, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	build(auth.RoleAdmin).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/audit", http.NoBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("admin must reach /audit, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestRouter_RefreshDevices_route confirms the refresh route is guarded by dep.
func TestRouter_RefreshDevices_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{StartedAt: time.Now(), RefreshDevices: fakeRefreshDevicesService{}})
	rr := httptest.NewRecorder()
	withDep.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/devices/refresh", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("refresh route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	rr = httptest.NewRecorder()
	withoutDep.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/devices/refresh", http.NoBody))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without RefreshDevices dep, got %d", rr.Code)
	}
}

// TestRouter_CentralLinks_route verifies central-links routes are guarded.
func TestRouter_CentralLinks_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{StartedAt: time.Now(), CentralLinks: fakeCentralLinksService{}})
	rr := httptest.NewRecorder()
	withDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/devices/A/central-links", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("central-links route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	rr = httptest.NewRecorder()
	withoutDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/devices/A/central-links", http.NoBody))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without CentralLinks dep, got %d", rr.Code)
	}
}

// TestRouter_Incidents_route verifies incidents route is guarded by dep.
func TestRouter_Incidents_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{StartedAt: time.Now(), Incidents: fakeIncidentsReader{}})
	if code := routerGET(withDep, "/api/v1/incidents"); code == http.StatusNotFound {
		t.Fatal("incidents route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	if code := routerGET(withoutDep, "/api/v1/incidents"); code != http.StatusNotFound {
		t.Fatalf("expected 404 without Incidents dep, got %d", code)
	}
}

// TestRouter_IncidentsAdmin_route verifies DELETE /incidents is guarded by
// IncidentsAdmin and actually invokes ClearIncidents when mounted.
func TestRouter_IncidentsAdmin_route(t *testing.T) {
	t.Parallel()
	clearer := &fakeIncidentsClearer{}
	r := NewRouter(Deps{StartedAt: time.Now(), IncidentsAdmin: clearer})
	h := &handlerHarness{t: t, h: r}
	rr := h.do(http.MethodDelete, "/api/v1/incidents", nil)
	if rr.Code == http.StatusNotFound {
		t.Fatal("DELETE /incidents route not mounted")
	}
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rr.Code, rr.Body.String())
	}
	if clearer.calls != 1 {
		t.Fatalf("ClearIncidents calls=%d want 1", clearer.calls)
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	if code := (&handlerHarness{t: t, h: withoutDep}).do(http.MethodDelete, "/api/v1/incidents", nil).Code; code != http.StatusNotFound {
		t.Fatalf("expected 404 without IncidentsAdmin dep, got %d", code)
	}
}

// TestRouter_MasterProfiles_route verifies the master-profiles routes are
// guarded by both Devices and MasterProfiles (nested gating — the
// handlers resolve device_type/channel_type from the channel's device).
func TestRouter_MasterProfiles_route(t *testing.T) {
	t.Parallel()
	devIdx := &stubDeviceIndexForRouter{}
	r := NewRouter(Deps{StartedAt: time.Now(), Devices: devIdx, MasterProfiles: fakeMasterProfilesService{}})
	h := &handlerHarness{t: t, h: r}

	if code := h.do(http.MethodGet, "/api/v1/devices/DEV1/channels/1/master-profiles", nil).Code; code == http.StatusNotFound {
		t.Fatal("GET master-profiles route not mounted")
	}
	if code := h.do(http.MethodGet, "/api/v1/devices/DEV1/channels/1/master-profiles/1", nil).Code; code == http.StatusNotFound {
		t.Fatal("GET master-profiles/{id} route not mounted")
	}
	if code := h.do(http.MethodPost, "/api/v1/devices/DEV1/channels/1/master-profiles/match", bytes.NewBufferString("{}")).Code; code == http.StatusNotFound {
		t.Fatal("POST master-profiles/match route not mounted")
	}

	// MasterProfiles set but Devices nil: routes stay unmounted (nested gate).
	withoutDevices := NewRouter(Deps{StartedAt: time.Now(), MasterProfiles: fakeMasterProfilesService{}})
	if code := (&handlerHarness{t: t, h: withoutDevices}).do(http.MethodGet, "/api/v1/devices/DEV1/channels/1/master-profiles", nil).Code; code != http.StatusNotFound {
		t.Fatalf("expected 404 without Devices dep, got %d", code)
	}

	// Devices set but MasterProfiles nil: routes stay unmounted.
	withoutMP := NewRouter(Deps{StartedAt: time.Now(), Devices: devIdx})
	if code := (&handlerHarness{t: t, h: withoutMP}).do(http.MethodGet, "/api/v1/devices/DEV1/channels/1/master-profiles", nil).Code; code != http.StatusNotFound {
		t.Fatalf("expected 404 without MasterProfiles dep, got %d", code)
	}
}

// TestRouter_SystemStatus_route verifies system/status route is guarded.
func TestRouter_SystemStatus_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{StartedAt: time.Now(), SystemStatus: fakeSystemStatusReader{}})
	if code := routerGET(withDep, "/api/v1/system/status"); code == http.StatusNotFound {
		t.Fatal("system/status route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	if code := routerGET(withoutDep, "/api/v1/system/status"); code != http.StatusNotFound {
		t.Fatalf("expected 404 without SystemStatus dep, got %d", code)
	}
}

// TestRouter_LogLevels_route verifies diagnostics/log-levels route is guarded.
func TestRouter_LogLevels_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{StartedAt: time.Now(), LogLevels: fakeLogLevelsService{}})
	if code := routerGET(withDep, "/api/v1/diagnostics/log-levels"); code == http.StatusNotFound {
		t.Fatal("log-levels route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	if code := routerGET(withoutDep, "/api/v1/diagnostics/log-levels"); code != http.StatusNotFound {
		t.Fatalf("expected 404 without LogLevels dep, got %d", code)
	}
}

// TestRouter_StartupCapture_route verifies system/startup-capture route is guarded.
func TestRouter_StartupCapture_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{StartedAt: time.Now(), StartupCapture: fakeStartupCaptureService{}})
	if code := routerGET(withDep, "/api/v1/system/startup-capture"); code == http.StatusNotFound {
		t.Fatal("startup-capture route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	if code := routerGET(withoutDep, "/api/v1/system/startup-capture"); code != http.StatusNotFound {
		t.Fatalf("expected 404 without StartupCapture dep, got %d", code)
	}
}

// TestRouter_EnableRestartEndpoint exercises the bool-guarded restart route.
// Verifies route presence via GET (chi distinguishes 405 "route exists but
// wrong method" from 404 "no such route"). POSTing the endpoint is
// out of bounds — the handler launches a goroutine that sleeps 100 ms
// then sends SIGTERM to the test process, terminating the test run.
func TestRouter_EnableRestartEndpoint(t *testing.T) {
	t.Parallel()
	withEnabled := NewRouter(Deps{StartedAt: time.Now(), EnableRestartEndpoint: true})
	rr := httptest.NewRecorder()
	withEnabled.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/system/restart", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("restart route not mounted when enabled (got 404, expected 405 for wrong method)")
	}

	withDisabled := NewRouter(Deps{StartedAt: time.Now(), EnableRestartEndpoint: false})
	rr = httptest.NewRecorder()
	withDisabled.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/system/restart", http.NoBody))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when restart endpoint disabled, got %d", rr.Code)
	}
}

// TestRouter_Capture_route verifies diagnostics/capture route is guarded.
func TestRouter_Capture_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{StartedAt: time.Now(), Capture: fakeCaptureService{}})
	rr := httptest.NewRecorder()
	withDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/capture", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("capture route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	rr = httptest.NewRecorder()
	withoutDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/capture", http.NoBody))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without Capture dep, got %d", rr.Code)
	}
}

// TestRouter_ValuesCache_route verifies admin/values-cache routes are guarded.
func TestRouter_ValuesCache_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{StartedAt: time.Now(), ValuesCache: fakeValuesCacheService{}})
	if code := routerGET(withDep, "/api/v1/admin/values-cache/stats"); code == http.StatusNotFound {
		t.Fatal("values-cache/stats route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	if code := routerGET(withoutDep, "/api/v1/admin/values-cache/stats"); code != http.StatusNotFound {
		t.Fatalf("expected 404 without ValuesCache dep, got %d", code)
	}
}

// TestRouter_DeviceLookup_route verifies per-device reset is guarded by DeviceLookup.
func TestRouter_DeviceLookup_route(t *testing.T) {
	t.Parallel()
	withBoth := NewRouter(Deps{
		StartedAt:    time.Now(),
		ValuesCache:  fakeValuesCacheService{},
		DeviceLookup: fakeDeviceLookup{},
	})
	rr := httptest.NewRecorder()
	withBoth.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/devices/A/values-cache/reset", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("device values-cache reset route not mounted")
	}

	withoutLookup := NewRouter(Deps{StartedAt: time.Now(), ValuesCache: fakeValuesCacheService{}})
	rr = httptest.NewRecorder()
	withoutLookup.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/devices/A/values-cache/reset", http.NoBody))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without DeviceLookup dep, got %d", rr.Code)
	}
}

// TestRouter_Paramsets_route verifies paramset routes are guarded.
func TestRouter_Paramsets_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{StartedAt: time.Now(), Paramsets: fakeParamsetService{}})
	rr := httptest.NewRecorder()
	withDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/devices/A/paramsets/MASTER", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("paramsets route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	rr = httptest.NewRecorder()
	withoutDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/devices/A/paramsets/MASTER", http.NoBody))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without Paramsets dep, got %d", rr.Code)
	}
}

// TestRouter_EditSessions_route verifies sessions/edit routes are guarded.
func TestRouter_EditSessions_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{
		StartedAt:    time.Now(),
		EditSessions: handlers.NewEditSessions(),
	})
	rr := httptest.NewRecorder()
	withDep.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/edit", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("sessions/edit route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	rr = httptest.NewRecorder()
	withoutDep.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/edit", http.NoBody))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without EditSessions dep, got %d", rr.Code)
	}
}

// TestRouter_WSHandler_route verifies /events is mounted only when WSHandler is wired.
func TestRouter_WSHandler_route(t *testing.T) {
	t.Parallel()
	wsCalled := false
	wsHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		wsCalled = true
		w.WriteHeader(http.StatusOK)
	})
	withDep := NewRouter(Deps{StartedAt: time.Now(), WSHandler: wsHandler})
	rr := httptest.NewRecorder()
	withDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/events", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("/events route not mounted")
	}
	if !wsCalled {
		t.Fatal("ws handler not called")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	rr = httptest.NewRecorder()
	withoutDep.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/events", http.NoBody))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without WSHandler dep, got %d", rr.Code)
	}
}

// TestRouter_DeviceAdmin_route verifies DeviceAdmin routes are guarded.
func TestRouter_DeviceAdmin_route(t *testing.T) {
	t.Parallel()
	withDep := NewRouter(Deps{
		StartedAt:   time.Now(),
		DeviceAdmin: fakeDeviceAdmin{},
	})
	rr := httptest.NewRecorder()
	withDep.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/devices/A", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("delete device route not mounted")
	}

	withoutDep := NewRouter(Deps{StartedAt: time.Now()})
	rr = httptest.NewRecorder()
	withoutDep.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/devices/A", http.NoBody))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without DeviceAdmin dep, got %d", rr.Code)
	}
}

// TestRouter_RequireOperator_fallback covers the op == nil → AuthRequire branch
// and the op == nil AND AuthRequire == nil → identity wrapper branch.
func TestRouter_RequireOperator_fallback(t *testing.T) {
	t.Parallel()
	// op=nil, AuthRequire=nil → identity wrapper — just confirm the router builds.
	r := NewRouter(Deps{
		StartedAt:   time.Now(),
		DeviceAdmin: fakeDeviceAdmin{},
	})
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/devices/A", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("route must be mounted regardless of op fallback")
	}
}

// TestRouter_RequireAdmin_fallback covers the admin == nil → AuthRequire → identity wrapper path.
func TestRouter_RequireAdmin_fallback(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{
		StartedAt:   time.Now(),
		DeviceAdmin: fakeDeviceAdmin{},
		// Neither RequireAdmin nor AuthRequire wired.
	})
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/devices/A", http.NoBody))
	if rr.Code == http.StatusNotFound {
		t.Fatal("delete route must be mounted with admin fallback")
	}
}

// ---------------------------------------------------------------------------
// Router endpoint tests (system/ccu, auth tokens, snapshot NDJSON)
// ---------------------------------------------------------------------------

func TestRouter_SystemCCU(t *testing.T) {
	t.Parallel()
	deps := Deps{
		StartedAt: time.Now(),
		SystemCCU: fakeSystemCCUReader{entries: []handlers.SystemCCUEntry{
			{
				Name:                 "home",
				Host:                 "172.18.4.29",
				Available:            true,
				Model:                "RaspberryMatic",
				Version:              "3.79.6.20240803",
				ConfiguredInterfaces: []string{"HmIP-RF"},
			},
		}},
	}
	rr := httptest.NewRecorder()
	r := NewRouter(deps)
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/system/ccu", http.NoBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Entries []handlers.SystemCCUEntry `json:"entries"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Name != "home" {
		t.Fatalf("entries=%+v", got.Entries)
	}
}

func TestRouter_AuthTokensCreate(t *testing.T) {
	t.Parallel()
	tokens := auth.NewMemoryTokenStore(map[string]auth.Identity{})
	authDeps := &handlers.AuthDeps{Tokens: tokens}
	// Router with admin wrapper that lets every request through (no
	// auth required at the router level for this test — we exercise
	// the handler's create-flow shape, not the gate).
	r := NewRouter(Deps{
		StartedAt:    time.Now(),
		Auth:         authDeps,
		AuthRequire:  passthroughMiddleware,
		RequireAdmin: passthroughMiddleware,
	})
	body, _ := json.Marshal(handlers.CreateTokenRequest{Subject: "homeassistant", Role: "operator"})
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens", bytes.NewReader(body)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp handlers.CreateTokenResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Token == "" || resp.ID == "" {
		t.Fatalf("missing token/id in response: %+v", resp)
	}
}

func TestRouter_AuthTokensDelete(t *testing.T) {
	t.Parallel()
	tokens := auth.NewMemoryTokenStore(map[string]auth.Identity{})
	id := tokens.Put("some-token-1234567890", auth.Identity{Subject: "ci", Role: auth.RoleViewer})
	r := NewRouter(Deps{
		StartedAt:    time.Now(),
		Auth:         &handlers.AuthDeps{Tokens: tokens},
		AuthRequire:  passthroughMiddleware,
		RequireAdmin: passthroughMiddleware,
	})
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/auth/tokens/"+id, http.NoBody))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestRouter_SnapshotNDJSON_ContentType(t *testing.T) {
	t.Parallel()
	r := NewRouter(Deps{StartedAt: time.Now()})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot", http.NoBody)
	req.Header.Set("Accept", "application/x-ndjson")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content-type=%q want application/x-ndjson", ct)
	}
	// First line should be the meta record.
	first, _, _ := strings.Cut(rr.Body.String(), "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(first), &m); err != nil {
		t.Fatalf("first line not JSON: %v", err)
	}
	if m["kind"] != "meta" {
		t.Fatalf("first line kind=%v, want meta", m["kind"])
	}
}

// ---------------------------------------------------------------------------
// Device admin, incidents, metrics, backup, CORS, idempotency endpoint tests
// ---------------------------------------------------------------------------

func TestDeleteDevice(t *testing.T) {
	admin := &fakeAdmin{}
	r := NewRouter(Deps{DeviceAdmin: admin})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/devices/0001ABCD", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted || admin.unpairs != 1 {
		t.Fatalf("code=%d unpairs=%d", rr.Code, admin.unpairs)
	}
}

func TestPatchDeviceRename(t *testing.T) {
	admin := &fakeAdmin{}
	r := NewRouter(Deps{DeviceAdmin: admin})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/devices/0001ABCD",
		strings.NewReader(`{"name":"Flur Licht"}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted || admin.renames != 1 {
		t.Fatalf("code=%d renames=%d", rr.Code, admin.renames)
	}
}

func TestPatchDeviceChannelRename(t *testing.T) {
	admin := &fakeAdmin{}
	r := NewRouter(Deps{DeviceAdmin: admin})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/devices/0001ABCD/channels/1",
		strings.NewReader(`{"name":"Kitchen Light"}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d", rr.Code)
	}
}

func TestAcceptInboxDevice(t *testing.T) {
	admin := &fakeAdmin{}
	r := NewRouter(Deps{DeviceAdmin: admin})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/0001/accept", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted || admin.accepts != 1 {
		t.Fatalf("code=%d accepts=%d", rr.Code, admin.accepts)
	}
}

func TestListIncidents(t *testing.T) {
	reader := &fakeIncidents{items: []handlers.Incident{{ID: "inc-1", Severity: "warn"}}}
	r := NewRouter(Deps{Incidents: reader})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	var out []handlers.Incident
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out) != 1 || out[0].ID != "inc-1" {
		t.Fatalf("incidents=%+v", out)
	}
}

func TestMetricsEndpointRenders(t *testing.T) {
	reg := metrics.NewRegistry()
	reg.Counter("foo_total", "doc").Inc()
	r := NewRouter(Deps{Metrics: reg})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), "foo_total 1") {
		t.Fatalf("body=%s", rr.Body.String())
	}
}

func TestBackupEndpoints(t *testing.T) {
	bak := &fakeBackup{jobs: []handlers.BackupEntry{{ID: "b1", Bytes: 7}}}
	r := NewRouter(Deps{Backup: bak})

	// POST /backups
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted || rr.Header().Get("Location") == "" {
		t.Fatalf("code=%d loc=%s", rr.Code, rr.Header().Get("Location"))
	}

	// GET /backups
	req = httptest.NewRequest(http.MethodGet, "/api/v1/backups", http.NoBody)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	var list []handlers.BackupEntry
	_ = json.Unmarshal(rr.Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != "b1" {
		t.Fatalf("list=%+v", list)
	}

	// GET /backups/{id}/download
	req = httptest.NewRequest(http.MethodGet, "/api/v1/backups/b1/download", http.NoBody)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Body.String() != "payload" {
		t.Fatalf("payload=%s", rr.Body.String())
	}
}

func TestCORSPreflight(t *testing.T) {
	r := NewRouter(Deps{
		CORS: &middleware.CORSConfig{Origins: []string{"https://app.example.com"}},
	})
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/info", http.NoBody)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("allow-origin=%s", rr.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestIdempotencyReplays(t *testing.T) {
	trig := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		trig++
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	})
	wrapped := middleware.Idempotency()(handler)

	first := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("b"))
	first.Header.Set("Idempotency-Key", "abc")
	rr1 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr1, first)

	second := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("b"))
	second.Header.Set("Idempotency-Key", "abc")
	rr2 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr2, second)

	if trig != 1 {
		t.Fatalf("handler ran %d times, want 1", trig)
	}
	if rr2.Header().Get("Idempotent-Replay") != "true" {
		t.Fatalf("replay header missing")
	}
	if rr2.Body.String() != "ok" || rr2.Code != http.StatusAccepted {
		t.Fatalf("replay body=%q code=%d", rr2.Body.String(), rr2.Code)
	}
	_ = time.Now
}

// ---------------------------------------------------------------------------
// Response-schema conformance. middleware.OpenAPIValidator (see
// middleware/openapi.go) only ever validates the REQUEST side against
// assets/openapi.yaml — a handler that drops or renames a response field
// previously had no test surface that would catch it. These tests replay a
// representative response through the same kin-openapi machinery so a DTO
// drift fails a test instead of shipping silently to external clients.
// ---------------------------------------------------------------------------

// openAPIResponseRouter loads and validates assets/openapi.yaml and builds
// the gorillamux router kin-openapi needs to resolve a request to its
// documented operation, mirroring middleware.NewOpenAPIValidator's own
// construction path so response checks exercise the identical schema the
// production request validator enforces.
func openAPIResponseRouter(t *testing.T) routers.Router {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	specPath := filepath.Join(repoRoot, "assets", "openapi.yaml")

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromFile(specPath)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate spec: %v", err)
	}
	rtr, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	return rtr
}

// assertResponseMatchesOpenAPISchema resolves req to its documented
// operation in rtr and validates rr's status/body against that operation's
// declared response schema. Fails the test on any mismatch: a status code
// the spec doesn't document for the path, a missing required property, or a
// property whose JSON type disagrees with the schema.
func assertResponseMatchesOpenAPISchema(t *testing.T, rtr routers.Router, req *http.Request, rr *httptest.ResponseRecorder) {
	t.Helper()
	route, pathParams, err := rtr.FindRoute(req)
	if err != nil {
		t.Fatalf("FindRoute(%s %s): %v", req.Method, req.URL.Path, err)
	}
	respInput := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: pathParams,
			Route:      route,
		},
		Status: rr.Code,
		Header: rr.Header(),
	}
	respInput.SetBodyBytes(rr.Body.Bytes())
	if err := openapi3filter.ValidateResponse(context.Background(), respInput); err != nil {
		t.Fatalf("response for %s %s (status %d) does not match assets/openapi.yaml: %v\nbody=%s",
			req.Method, req.URL.Path, rr.Code, err, rr.Body.String())
	}
}

// TestRESTResponsesMatchOpenAPISchema exercises a representative slice of
// endpoints across the core/devices/hub/incidents/backup surfaces and checks
// each response against its documented schema. Not exhaustive — the goal is
// a standing regression guard against dropped/renamed response fields on the
// most heavily used surfaces, not full endpoint coverage (which the
// per-handler unit tests already provide for behavior).
func TestRESTResponsesMatchOpenAPISchema(t *testing.T) {
	rtr := openAPIResponseRouter(t)

	t.Run("info", func(t *testing.T) {
		h := newTestRouter(t)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
		rr := h.do(http.MethodGet, "/api/v1/info", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		assertResponseMatchesOpenAPISchema(t, rtr, req, rr)
	})

	t.Run("health healthy", func(t *testing.T) {
		h := newTestRouter(t)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
		rr := h.do(http.MethodGet, "/api/v1/health", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		assertResponseMatchesOpenAPISchema(t, rtr, req, rr)
	})

	t.Run("health unhealthy", func(t *testing.T) {
		tr := health.NewTracker()
		tr.Record("central", health.Sample{Healthy: false})
		tr.Record("central", health.Sample{Healthy: false})
		r := NewRouter(Deps{Health: tr})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d", rr.Code)
		}
		assertResponseMatchesOpenAPISchema(t, rtr, req, rr)
	})

	t.Run("config", func(t *testing.T) {
		h := newTestRouter(t)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/config", http.NoBody)
		rr := h.do(http.MethodGet, "/api/v1/config", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		assertResponseMatchesOpenAPISchema(t, rtr, req, rr)
	})

	t.Run("devices list", func(t *testing.T) {
		r := newDeviceRouter(t, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", http.NoBody)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		assertResponseMatchesOpenAPISchema(t, rtr, req, rr)
	})

	t.Run("device detail", func(t *testing.T) {
		r := newDeviceRouter(t, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/0001ABCD", http.NoBody)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		assertResponseMatchesOpenAPISchema(t, rtr, req, rr)
	})

	t.Run("incidents", func(t *testing.T) {
		reader := &fakeIncidents{items: []handlers.Incident{{ID: "inc-1", Severity: "warn"}}}
		r := NewRouter(Deps{Incidents: reader})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", http.NoBody)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		assertResponseMatchesOpenAPISchema(t, rtr, req, rr)
	})

	t.Run("backups list", func(t *testing.T) {
		bak := &fakeBackup{jobs: []handlers.BackupEntry{{ID: "b1", Bytes: 7}}}
		r := NewRouter(Deps{Backup: bak})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/backups", http.NoBody)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		assertResponseMatchesOpenAPISchema(t, rtr, req, rr)
	})

	t.Run("programs list", func(t *testing.T) {
		h := newHubRouter(t)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/programs", http.NoBody)
		rr := httptest.NewRecorder()
		h.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		assertResponseMatchesOpenAPISchema(t, rtr, req, rr)
	})
}
