// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	centralpkg "github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/cachereset"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/diagnostics"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/discovery/ssdp"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/masterprofile"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// ---------------------------------------------------------------------------
// Minimal fakes covering every optional rest.Deps facade that gates a route
// registration in internal/north/rest/router.go. None of these are ever
// invoked — chi.Walk only visits the routing tree, it never calls a
// handler — so every method body is a bare zero-value return. The point of
// wiring all of them is structural: with every dependency non-nil, every
// conditionally-mounted route in NewRouter is present in the tree, so the
// walk below sees the router's true maximal route surface.
// ---------------------------------------------------------------------------

type fakeConfigReader struct{}

func (fakeConfigReader) SanitizedConfig() handlers.ConfigSnapshot { return handlers.ConfigSnapshot{} }

type fakeSelfPasswordService struct{}

func (fakeSelfPasswordService) AuthenticateBasic(context.Context, string, string) (auth.Identity, error) {
	return auth.Identity{}, nil
}

func (fakeSelfPasswordService) Put(context.Context, string, string, auth.Role) error { return nil }

type fakePreferencesService struct{}

func (fakePreferencesService) Get(context.Context, string, string) (string, error) { return "", nil }
func (fakePreferencesService) Set(context.Context, string, string, string) error   { return nil }
func (fakePreferencesService) Delete(context.Context, string, string) error        { return nil }

type fakeDeviceIndex struct{}

func (fakeDeviceIndex) Devices() []*device.Device            { return nil }
func (fakeDeviceIndex) Device(string) (*device.Device, bool) { return nil, false }
func (fakeDeviceIndex) CentralOf(string) string              { return "" }
func (fakeDeviceIndex) SerialSuffix(string) string           { return "" }

type fakeMasterProfilesService struct{}

func (fakeMasterProfilesService) Profiles(string, string) ([]masterprofile.Profile, error) {
	return nil, nil
}

func (fakeMasterProfilesService) Profile(string, string, int) (masterprofile.Profile, error) {
	return masterprofile.Profile{}, nil
}

func (fakeMasterProfilesService) MatchActiveProfile(string, string, map[string]any) int { return 0 }

type fakeUISchemaService struct{}

func (fakeUISchemaService) UISchema(context.Context, handlers.UISchemaRequest) (*handlers.UISchema, error) {
	return &handlers.UISchema{}, nil
}

type fakeLinksService struct{}

func (fakeLinksService) ListLinks(context.Context, string, string) ([]handlers.Link, error) {
	return nil, nil
}

func (fakeLinksService) AddLink(context.Context, string, string, string, string) error { return nil }

func (fakeLinksService) RemoveLink(context.Context, string, string) error { return nil }

func (fakeLinksService) LinkableChannels(context.Context, string, string, string, string) ([]handlers.LinkableChannel, error) {
	return nil, nil
}

type fakeScheduleService struct{}

func (fakeScheduleService) GetClimateSchedule(context.Context, string, int) (*handlers.ClimateSchedule, error) {
	return &handlers.ClimateSchedule{}, nil
}

func (fakeScheduleService) PutClimateSchedule(context.Context, string, int, *handlers.ClimateSchedule) error {
	return nil
}

func (fakeScheduleService) SetActiveProfile(context.Context, string, int, string) error { return nil }

func (fakeScheduleService) GetClimateScheduleAuto(context.Context, string) (*handlers.ClimateSchedule, error) {
	return &handlers.ClimateSchedule{}, nil
}

func (fakeScheduleService) PutClimateScheduleAuto(context.Context, string, *handlers.ClimateSchedule) error {
	return nil
}

func (fakeScheduleService) SetActiveProfileAuto(context.Context, string, string) error { return nil }

func (fakeScheduleService) FindScheduleChannel(context.Context, string) (int, error) { return 0, nil }

func (fakeScheduleService) CopySchedule(context.Context, string, string) error { return nil }

func (fakeScheduleService) CopyClimateProfile(context.Context, string, int, string, int) error {
	return nil
}

type fakeAuditService struct{}

func (fakeAuditService) List(int) []audit.Entry { return nil }

type fakeHistoryService struct{}

func (fakeHistoryService) Query(context.Context, handlers.HistoryQuery) ([]handlers.HistoryBucket, error) {
	return nil, nil
}

type fakeEnergyService struct{}

func (fakeEnergyService) Energy(context.Context, handlers.EnergyQuery) (handlers.EnergyResponse, error) {
	return handlers.EnergyResponse{}, nil
}

type fakeDeviceAdmin struct{}

// fakeFirmwareRefresher backs the /devices/firmware/refresh route in the
// walked router.
type fakeFirmwareRefresher struct{}

func (fakeFirmwareRefresher) RefreshFirmwareData(context.Context) error { return nil }

func (fakeDeviceAdmin) UnpairDevice(context.Context, string, bool, bool) error   { return nil }
func (fakeDeviceAdmin) RenameDevice(context.Context, string, string, bool) error { return nil }
func (fakeDeviceAdmin) RenameChannel(context.Context, string, int, string) error { return nil }
func (fakeDeviceAdmin) AcceptInboxDevice(context.Context, string) error          { return nil }
func (fakeDeviceAdmin) UpdateFirmware(context.Context, string) error             { return nil }
func (fakeDeviceAdmin) SetRooms(context.Context, string, []string) error         { return nil }
func (fakeDeviceAdmin) SetFunctions(context.Context, string, []string) error     { return nil }

type fakeDeviceInstallMode struct{}

func (fakeDeviceInstallMode) SetInstallMode(context.Context, string, int) error { return nil }

type fakeRoomFunctionAdmin struct{}

func (fakeRoomFunctionAdmin) CreateRoom(context.Context, string, string) (int, error) { return 0, nil }

func (fakeRoomFunctionAdmin) RenameRoom(context.Context, string, string, string) error { return nil }

func (fakeRoomFunctionAdmin) DeleteRoom(context.Context, string, string) error { return nil }

func (fakeRoomFunctionAdmin) CreateFunction(context.Context, string, string) (int, error) {
	return 0, nil
}

func (fakeRoomFunctionAdmin) RenameFunction(context.Context, string, string, string) error {
	return nil
}
func (fakeRoomFunctionAdmin) DeleteFunction(context.Context, string, string) error { return nil }

type fakeRefreshDevicesService struct{}

func (fakeRefreshDevicesService) RefreshDevices(context.Context) error { return nil }

type fakeReloaderService struct{}

func (fakeReloaderService) ReloadDeviceConfig(context.Context, string) error  { return nil }
func (fakeReloaderService) ReloadChannelConfig(context.Context, string) error { return nil }

type fakeCentralLinksService struct{}

func (fakeCentralLinksService) CreateCentralLinks(context.Context, string) (handlers.CentralLinksReport, error) {
	return handlers.CentralLinksReport{}, nil
}

func (fakeCentralLinksService) RemoveCentralLinks(context.Context, string) (handlers.CentralLinksReport, error) {
	return handlers.CentralLinksReport{}, nil
}

func (fakeCentralLinksService) CentralLinksStatus(string) (handlers.CentralLinksStatus, error) {
	return handlers.CentralLinksStatus{}, nil
}

type fakeIncidentsReader struct{}

func (fakeIncidentsReader) Incidents() []handlers.Incident { return nil }

type fakeIncidentsClearer struct{}

func (fakeIncidentsClearer) ClearIncidents(context.Context) error { return nil }

type fakeSystemStatusReader struct{}

func (fakeSystemStatusReader) SystemStatusEntries() []handlers.SystemStatusEntry { return nil }

// fakeLogLevelsService satisfies both handlers.LogLevelsService and
// handlers.LogDefaultLevelService — the two REST facades share the
// Default() method, and *hmlog.LevelRegistry backs both in production.
type fakeLogLevelsService struct{}

func (fakeLogLevelsService) Default() slog.Level                   { return slog.LevelInfo }
func (fakeLogLevelsService) Set(string, slog.Level, time.Duration) {}
func (fakeLogLevelsService) Reset(string) bool                     { return true }
func (fakeLogLevelsService) Snapshot() []hmlog.OverrideInfo        { return nil }
func (fakeLogLevelsService) SetDefault(slog.Level)                 {}

type fakeLogFeedService struct{}

func (fakeLogFeedService) Snapshot(int, slog.Level) []hmlog.LogRecord { return nil }
func (fakeLogFeedService) Since(uint64, slog.Level) []hmlog.LogRecord { return nil }

func (fakeLogFeedService) Subscribe(slog.Level) (records <-chan hmlog.LogRecord, cancel func()) {
	ch := make(chan hmlog.LogRecord)
	close(ch)
	return ch, func() {}
}
func (fakeLogFeedService) LastSeq() uint64 { return 0 }

type fakeIntrospectService struct{}

func (fakeIntrospectService) ReliabilitySnapshot(string) []handlers.ReliabilityState { return nil }
func (fakeIntrospectService) ResolveCentral(string) (string, bool)                   { return "", false }

func (fakeIntrospectService) TapEventBus(context.Context, string, []string, func(handlers.DiagnosticsEvent)) {
}

type fakeRSSIService struct{}

func (fakeRSSIService) RSSIInfo(context.Context) (map[string]any, error) { return nil, nil }

type fakeStartupCaptureService struct{}

func (fakeStartupCaptureService) Load() (diagnostics.StartupCaptureConfig, error) {
	return diagnostics.StartupCaptureConfig{}, nil
}
func (fakeStartupCaptureService) Save(diagnostics.StartupCaptureConfig) error { return nil }

type fakeCaptureService struct{}

func (fakeCaptureService) Start(diagnostics.StartOptions) (diagnostics.Summary, error) {
	return diagnostics.Summary{}, nil
}

func (fakeCaptureService) Stop(string) (diagnostics.Summary, error) {
	return diagnostics.Summary{}, nil
}
func (fakeCaptureService) List() []diagnostics.Summary { return nil }
func (fakeCaptureService) Get(string) (diagnostics.Summary, error) {
	return diagnostics.Summary{}, nil
}
func (fakeCaptureService) OpenArchive(string) ([]byte, error) { return nil, nil }

type fakeRPCRecorderService struct{}

func (fakeRPCRecorderService) Start([]string, int, bool) []handlers.RPCRecordingStatus { return nil }

func (fakeRPCRecorderService) Stop([]string) []handlers.RPCRecordingStatus { return nil }

func (fakeRPCRecorderService) Status() []handlers.RPCRecordingStatus { return nil }

func (fakeRPCRecorderService) Export(string, string) (any, bool) { return nil, false }

type fakeValuesCacheService struct{}

func (fakeValuesCacheService) DeleteAll(context.Context) error { return nil }
func (fakeValuesCacheService) DeleteDevice(context.Context, string, string, string) error {
	return nil
}

func (fakeValuesCacheService) Stats(context.Context) (handlers.ValuesCacheStats, error) {
	return handlers.ValuesCacheStats{}, nil
}

func (fakeValuesCacheService) Metrics() handlers.ValuesCacheMetrics {
	return handlers.ValuesCacheMetrics{}
}

type fakeDeviceLookup struct{}

func (fakeDeviceLookup) LocateDevice(string) (central, address string, found bool) {
	return "", "", false
}

type fakeBackupService struct{}

func (fakeBackupService) TriggerBackup(context.Context) (string, error) { return "", nil }
func (fakeBackupService) List(context.Context) ([]handlers.BackupEntry, error) {
	return nil, nil
}
func (fakeBackupService) Stream(context.Context, string, io.Writer) error { return nil }
func (fakeBackupService) Restore(context.Context, string) (string, error) { return "", nil }
func (fakeBackupService) TriggerBackupForCentral(context.Context, string) (string, error) {
	return "", nil
}
func (fakeBackupService) Prune(context.Context, string, int) error { return nil }

type fakeParamsetService struct{}

func (fakeParamsetService) GetParamset(context.Context, string, hmenum.ParamsetKey) (map[string]any, error) {
	return nil, nil
}

func (fakeParamsetService) PutParamset(context.Context, string, hmenum.ParamsetKey, map[string]any) error {
	return nil
}

func (fakeParamsetService) GetLinkParamset(context.Context, string, string) (map[string]any, error) {
	return nil, nil
}

func (fakeParamsetService) PutLinkParamset(context.Context, string, string, map[string]any) error {
	return nil
}

// fakeHubIndex backs the hub-singleton routes (programs, sysvars,
// system/update, install-mode/interfaces, ...). h may be nil — the
// route registration in router.go depends only on the interface being
// non-nil, never on the wrapped *hub.Hub's content.
type fakeHubIndex struct{ h *hub.Hub }

func (f fakeHubIndex) Hub() *hub.Hub              { return f.h }
func (f fakeHubIndex) Hubs() []handlers.NamedHub  { return nil }
func (f fakeHubIndex) HubFor(string) *hub.Hub     { return f.h }
func (f fakeHubIndex) SerialSuffix(string) string { return "" }

type fakeSysvarRefreshService struct{}

func (fakeSysvarRefreshService) FetchSystemVariables(context.Context, string) error { return nil }

type fakeInterfaceIndex struct{}

func (fakeInterfaceIndex) Interfaces() []handlers.InterfaceState { return nil }
func (fakeInterfaceIndex) Interface(string) (handlers.InterfaceState, bool) {
	return handlers.InterfaceState{}, false
}
func (fakeInterfaceIndex) Reconnect(context.Context, string) error { return nil }

type fakeConfigAdminService struct{}

func (fakeConfigAdminService) Effective(context.Context) (*configstore.EffectiveResult, error) {
	return &configstore.EffectiveResult{}, nil
}

func (fakeConfigAdminService) GetSection(context.Context, configstore.Section) (sqlite.SectionRow, error) {
	return sqlite.SectionRow{}, nil
}

func (fakeConfigAdminService) PutSection(context.Context, configstore.Section, []byte, string) (sqlite.SectionRow, error) {
	return sqlite.SectionRow{}, nil
}

func (fakeConfigAdminService) DeleteSection(context.Context, configstore.Section) error { return nil }

type fakeUserAdminService struct{}

func (fakeUserAdminService) Put(context.Context, string, string, auth.Role) error { return nil }
func (fakeUserAdminService) Delete(context.Context, string) error                 { return nil }
func (fakeUserAdminService) List(context.Context) ([]sqlite.UserRow, error)       { return nil, nil }
func (fakeUserAdminService) Count(context.Context) (int, error)                   { return 0, nil }

type fakeTokenAdminService struct{}

func (fakeTokenAdminService) Create(context.Context, sqlite.CreateInput) (sqlite.CreateResult, error) {
	return sqlite.CreateResult{}, nil
}
func (fakeTokenAdminService) Delete(context.Context, string) error            { return nil }
func (fakeTokenAdminService) List(context.Context) ([]sqlite.TokenRow, error) { return nil, nil }

type fakeCentralAdminService struct{}

func (fakeCentralAdminService) Put(context.Context, sqlite.CentralRow) error { return nil }
func (fakeCentralAdminService) Get(context.Context, string) (sqlite.CentralRow, error) {
	return sqlite.CentralRow{}, nil
}
func (fakeCentralAdminService) Delete(context.Context, string) error { return nil }
func (fakeCentralAdminService) List(context.Context) ([]sqlite.CentralRow, error) {
	return nil, nil
}

type fakeDiscoveredCentralLister struct{}

func (fakeDiscoveredCentralLister) List() []ssdp.DiscoveredCCU { return nil }

type fakeDiscoveryIgnoreStore struct{}

func (fakeDiscoveryIgnoreStore) Add(context.Context, sqlite.IgnoredCCU) error { return nil }
func (fakeDiscoveryIgnoreStore) Remove(context.Context, string) (bool, error) { return false, nil }
func (fakeDiscoveryIgnoreStore) List(context.Context) ([]sqlite.IgnoredCCU, error) {
	return nil, nil
}

func (fakeDiscoveryIgnoreStore) IgnoredSerials(context.Context) (map[string]struct{}, error) {
	return nil, nil
}

type fakeConfiguredCentralLister struct{}

func (fakeConfiguredCentralLister) List(context.Context) ([]sqlite.CentralRow, error) {
	return nil, nil
}

type fakeMQTTReloadService struct{}

func (fakeMQTTReloadService) Reload(context.Context) (time.Duration, error) { return 0, nil }

type fakeCacheResetService struct{}

func (fakeCacheResetService) Clear(context.Context, cachereset.Scope) (cachereset.Report, error) {
	return cachereset.Report{}, nil
}

type fakeDataPointWriter struct{}

func (fakeDataPointWriter) SetValue(context.Context, string, hmenum.Parameter, any, hmenum.CommandPriority) error {
	return nil
}

type fakeCustomDPWriter struct{}

func (fakeCustomDPWriter) InvokeCustomDP(
	context.Context, string, string, string, map[string]any, hmenum.CommandPriority, string,
) error {
	return nil
}

// fullyWiredRouterDeps returns a rest.Deps with every optional facade
// populated by a no-op fake, so rest.NewRouter mounts its maximal route
// surface (every "if d.X != nil" branch taken). Handler bodies are never
// invoked by the walk in TestRESTRouterMatchesOpenAPISpec, only the routing
// tree is inspected, so correctness of the fakes' return values does not
// matter — only their presence (non-nil) does.
// mustAlarmPanel builds a minimal real alarm service on an in-memory
// database — the walk only needs the Deps field non-nil so the alarm
// routes mount; no handler body ever runs.
func mustAlarmPanel() handlers.AlarmPanel {
	db, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		panic(err)
	}
	svc, err := alarm.NewService(alarm.Deps{
		Settings: alarm.Settings{Enabled: true},
		Registry: centralpkg.NewRegistry(),
		Stores:   alarm.NewStores(db),
	})
	if err != nil {
		panic(err)
	}
	return svc
}

func fullyWiredRouterDeps() rest.Deps {
	authDeps := &handlers.AuthDeps{
		Users:    auth.NewMemoryUserStore(),
		Sessions: auth.NewSessionStore(),
		Tokens:   auth.NewMemoryTokenStore(map[string]auth.Identity{}),
	}
	return rest.Deps{
		StartedAt:             time.Now(),
		Config:                fakeConfigReader{},
		SelfPassword:          fakeSelfPasswordService{},
		Preferences:           fakePreferencesService{},
		Devices:               fakeDeviceIndex{},
		MasterProfiles:        fakeMasterProfilesService{},
		UISchema:              fakeUISchemaService{},
		Links:                 fakeLinksService{},
		Schedules:             fakeScheduleService{},
		Audit:                 fakeAuditService{},
		History:               fakeHistoryService{},
		Energy:                fakeEnergyService{},
		DeviceAdmin:           fakeDeviceAdmin{},
		FirmwareRefresher:     fakeFirmwareRefresher{},
		DeviceInstallMode:     fakeDeviceInstallMode{},
		RoomFunctionAdmin:     fakeRoomFunctionAdmin{},
		RefreshDevices:        fakeRefreshDevicesService{},
		Reloader:              fakeReloaderService{},
		CentralLinks:          fakeCentralLinksService{},
		Incidents:             fakeIncidentsReader{},
		Alarm:                 mustAlarmPanel(),
		IncidentsAdmin:        fakeIncidentsClearer{},
		SystemStatus:          fakeSystemStatusReader{},
		LogLevels:             fakeLogLevelsService{},
		LogDefaultLevel:       fakeLogLevelsService{},
		LogFeed:               fakeLogFeedService{},
		Introspect:            fakeIntrospectService{},
		RSSIInfo:              fakeRSSIService{},
		StartupCapture:        fakeStartupCaptureService{},
		EnableRestartEndpoint: true,
		Capture:               fakeCaptureService{},
		RPCRecorder:           fakeRPCRecorderService{},
		Metrics:               metrics.NewRegistry(),
		ValuesCache:           fakeValuesCacheService{},
		DeviceLookup:          fakeDeviceLookup{},
		Backup:                fakeBackupService{},
		Paramsets:             fakeParamsetService{},
		Hub:                   fakeHubIndex{h: hub.NewHub("test")},
		SysvarRefresh:         fakeSysvarRefreshService{},
		Interfaces:            fakeInterfaceIndex{},
		ConfigAdmin:           fakeConfigAdminService{},
		UserAdmin:             fakeUserAdminService{},
		TokenAdmin:            fakeTokenAdminService{},
		CentralAdmin:          fakeCentralAdminService{},
		Discovery: &handlers.DiscoveryDeps{
			Discoverer: fakeDiscoveredCentralLister{},
			Ignore:     fakeDiscoveryIgnoreStore{},
			Centrals:   fakeConfiguredCentralLister{},
		},
		MQTTReload:            fakeMQTTReloadService{},
		CacheReset:            fakeCacheResetService{},
		EditSessions:          handlers.NewEditSessions(),
		WSHandler:             http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		Auth:                  authDeps,
		OIDC:                  &handlers.OIDCDeps{},
		WebhookInboundEnabled: true,
		WebhookInboundToken:   "test-token",
		DPWriter:              fakeDataPointWriter{},
		CustomDPWriter:        fakeCustomDPWriter{},
	}
}

// ---------------------------------------------------------------------------
// The structural walk itself.
// ---------------------------------------------------------------------------

// loadOpenAPISpec loads and validates assets/openapi.yaml through kin-openapi
// so path/operation enumeration reflects the same parser the runtime
// OpenAPIValidator middleware uses (see internal/north/rest/middleware).
func loadOpenAPISpec(t *testing.T) *openapi3.T {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
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
	return doc
}

// routerOpenAPIExemptions lists intentional gaps between the mounted chi
// route tree and the documented OpenAPI contract. Each entry needs its own
// justification — this is not a blanket escape hatch. Keys are "METHOD path"
// (path without the /api/v1 server prefix); a bare path (no method) exempts
// every method on it.
var routerOpenAPIExemptions = map[string]string{
	// /events is mounted with chi's method-agnostic Handle (router.go:
	// `pr.Handle("/events", d.WSHandler)`), not Get, because the RFC 6455
	// upgrade handshake must not be rejected by a method-specific route
	// match. The route therefore appears in the walk under every HTTP
	// method chi knows, while the spec — correctly — documents only the
	// GET operation a WebSocket client actually issues. Every method
	// below except GET is this same, single divergence.
	"CONNECT /events": "chi Handle registers /events for every method; only GET is a real client request",
	"DELETE /events":  "chi Handle registers /events for every method; only GET is a real client request",
	"HEAD /events":    "chi Handle registers /events for every method; only GET is a real client request",
	"OPTIONS /events": "chi Handle registers /events for every method; only GET is a real client request",
	"PATCH /events":   "chi Handle registers /events for every method; only GET is a real client request",
	"POST /events":    "chi Handle registers /events for every method; only GET is a real client request",
	"PUT /events":     "chi Handle registers /events for every method; only GET is a real client request",
	"QUERY /events":   "chi Handle registers /events for every method; only GET is a real client request",
	"TRACE /events":   "chi Handle registers /events for every method; only GET is a real client request",
}

// TestRESTRouterMatchesOpenAPISpec walks the real chi router NewRouter
// assembles (every optional facade wired, so every conditionally-mounted
// route is present) and cross-checks it against assets/openapi.yaml in both
// directions:
//
//  1. every mounted /api/v1/* route has a documented operation — a coded
//     route with no spec entry would otherwise reach production invisibly
//     (TestOpenAPIDeclaresMVPEndpoints only ever checked a ~30-path
//     hand-picked subset against the router's ~150 real paths);
//  2. every documented operation resolves to a mounted route — a path
//     that only exists on paper (removed handler, typo'd router path)
//     would otherwise 404 in production while looking complete in the
//     spec, and the runtime OpenAPIValidator middleware (mounted with
//     assets/openapi.yaml in production, see
//     internal/north/rest/middleware.NewOpenAPIValidator) would reject
//     every request to it as "not found in spec" only for callers that
//     never hit that operation in a manual test.
func TestRESTRouterMatchesOpenAPISpec(t *testing.T) {
	spec := loadOpenAPISpec(t)
	router := rest.NewRouter(fullyWiredRouterDeps())

	// mounted[path][method] — path has the /api/v1 server prefix
	// stripped so it lines up with the spec's path keys directly (chi's
	// "{param}" template syntax is identical to OpenAPI's).
	mounted := map[string]map[string]bool{}
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/v1") {
			// SPA mount, server-rendered /about + /health + /ui bootstrap
			// surface, and the root "/" redirect are not part of the
			// OpenAPI-documented API (servers: /api/v1 in openapi.yaml).
			return nil
		}
		path := strings.TrimPrefix(route, "/api/v1")
		if path == "" {
			path = "/"
		}
		if mounted[path] == nil {
			mounted[path] = map[string]bool{}
		}
		mounted[path][method] = true
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}

	documented := map[string]map[string]bool{}
	for path, item := range spec.Paths.Map() {
		ops := map[string]bool{}
		for method := range item.Operations() {
			ops[method] = true
		}
		documented[path] = ops
	}

	var undocumented []string
	for path, methods := range mounted {
		docOps, isDocumented := documented[path]
		for method := range methods {
			key := method + " " + path
			if _, exempt := routerOpenAPIExemptions[key]; exempt {
				continue
			}
			if _, exempt := routerOpenAPIExemptions[path]; exempt {
				continue
			}
			if !isDocumented {
				undocumented = append(undocumented, key+"  (path missing from openapi.yaml)")
				continue
			}
			// "*" is chi's catch-all method (used by pr.Handle, e.g. the
			// /events WebSocket upgrade route) — any documented operation
			// on the path satisfies it.
			if method == "*" {
				if len(docOps) == 0 {
					undocumented = append(undocumented, key+"  (path documented with zero operations)")
				}
				continue
			}
			if !docOps[method] {
				undocumented = append(undocumented, key+"  (operation missing from openapi.yaml)")
			}
		}
	}

	var unmounted []string
	for path, methods := range documented {
		if _, exempt := routerOpenAPIExemptions[path]; exempt {
			continue
		}
		mountedOps, isMounted := mounted[path]
		if !isMounted {
			unmounted = append(unmounted, path+"  (no route mounted for this path)")
			continue
		}
		for method := range methods {
			key := method + " " + path
			if _, exempt := routerOpenAPIExemptions[key]; exempt {
				continue
			}
			if mountedOps[method] || mountedOps["*"] {
				continue
			}
			unmounted = append(unmounted, key+"  (operation not mounted by NewRouter)")
		}
	}

	sort.Strings(undocumented)
	sort.Strings(unmounted)
	if len(undocumented) > 0 {
		t.Errorf("router mounts %d route(s) with no matching OpenAPI operation:\n  %s",
			len(undocumented), strings.Join(undocumented, "\n  "))
	}
	if len(unmounted) > 0 {
		t.Errorf("openapi.yaml documents %d operation(s) with no mounted router route:\n  %s",
			len(unmounted), strings.Join(unmounted, "\n  "))
	}
}
