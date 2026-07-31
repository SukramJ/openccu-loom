// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/channelflags"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// Deps bundles every collaborator the REST router needs.
//
// Nil-field semantics — three distinct behaviours depending on the field:
//
//  1. Nil → route not mounted (HTTP 404 from the chi.NotFound handler).
//     The route is entirely absent from the mux tree, so clients get a
//     clean 404 rather than a method-specific error. Examples:
//     Devices (omits all /devices/* routes), Links (omits /devices/{addr}/links),
//     Backup (omits all /backups routes).
//
//  2. Nil → 503 service_unready. The route IS mounted but the handler
//     inspects the field internally and writes a 503 Problem+JSON body.
//     Used where the OpenAPI spec documents the endpoint as always-present
//     but optionally wired. Examples: MatterStatusReader (GET /matter/status),
//     ConfigExport (GET and POST /config/export|import),
//     SystemStatus (GET /system/status).
//
//  3. Nil → silent no-op / fallback. The field modifies cross-cutting
//     behaviour rather than gating a route. A nil value activates a safe
//     default so existing callers keep working. Examples:
//     DataPointVis (nil means "expose everything"),
//     ConfigChannelMeta (nil causes the export handler to omit model/channel_type),
//     MatterAuditRecorder (nil silently skips the audit append).
type Deps struct {
	Logger      *slog.Logger
	StartedAt   time.Time
	Health      handlers.HealthReader
	Config      handlers.ConfigReader
	Devices     handlers.DeviceIndex
	DeviceAdmin handlers.DeviceAdmin
	// DeviceReplacer backs the guided device-replace workflow
	// (GET /devices/{addr}/replace-candidates + POST
	// /devices/{addr}/replace). Nil serves those routes as 503.
	DeviceReplacer handlers.DeviceReplacePort
	// FirmwareRefresher backs POST /devices/firmware/refresh (force
	// re-read of per-device firmware data from every CCU). Nil leaves
	// the route unmounted.
	FirmwareRefresher handlers.FirmwareRefresher
	// DeviceInstallMode opens a targeted (per-device / serial) pairing
	// window at POST /devices/{addr}/install-mode. Nil disables the route.
	DeviceInstallMode handlers.DeviceInstallModePort
	// InstallModeSearch triggers a wired-bus device scan at POST
	// /install-mode/search. Nil serves the route as 503.
	InstallModeSearch handlers.DeviceSearchPort
	// DeviceCommunicationTest runs the per-device communication test at
	// POST /devices/{addr}/test. Nil serves the route as 503.
	DeviceCommunicationTest handlers.DeviceCommunicationTestPort
	// DeviceTeam backs channel team assignment
	// (GET .../channels/{no}/team-candidates + PUT .../channels/{no}/team).
	// Nil serves those routes as 503.
	DeviceTeam handlers.DeviceTeamPort
	// DeviceIcons proxies device-type icon images from the CCU for the
	// device list. Optional — nil answers 404 (SPA uses a glyph).
	DeviceIcons handlers.DeviceIconProxy
	// CustomDPWriter drives POST .../cdps/{name}/{operation}.
	// Nil disables the mutating endpoint (list/get remain available).
	CustomDPWriter handlers.CustomDPWriter
	// RefreshDevices triggers a fresh ListDevices sweep across every
	// backend on demand. Optional — nil disables `POST /devices/refresh`.
	RefreshDevices handlers.RefreshDevicesService
	// Reloader re-pulls a single device's or channel's config from its
	// CCU. Optional — nil disables `POST /devices/{addr}/reload` and
	// `POST /devices/{addr}/channels/{no}/reload`. The same adapter
	// backs the WS reload commands.
	Reloader  handlers.ReloaderService
	DPWriter  handlers.DataPointWriter
	Paramsets handlers.ParamsetService
	// ParameterDeterminer backs
	// POST /devices/{addr}/channels/{no}/paramsets/{key}/determine — the
	// MASTER editor's "Determine" button, which reads one parameter's live
	// value straight from the device. Nil disables the route (404); shares
	// the domain call with the WS `paramset.determine` command.
	ParameterDeterminer handlers.ParameterDeterminer
	// WebhookInboundEnabled mounts the inbound webhook routes
	// (POST /webhook/value, POST /webhook/program) when true; off means the
	// routes are not mounted (404). Restart-required (mirrors north.mcp).
	WebhookInboundEnabled bool
	// WebhookInboundToken is the optional inbound bearer token accepted in
	// addition to the normal operator auth chain on the inbound webhook routes.
	WebhookInboundToken string
	// DataPointVis is the outbound visibility filter for the
	// GET .../data-points endpoint. Nil means "expose everything"
	// (backward-compatible with un-wired call sites). See ADR 0005.
	DataPointVis filter.VisibilitySet
	Hub          handlers.HubIndex
	// SysvarRefresh backs POST /sysvars/fetch — force re-pull the CCU
	// sysvar catalogue into the hub model. Nil disables the route (404).
	SysvarRefresh handlers.SysvarRefreshService
	Interfaces    handlers.InterfaceIndex
	Incidents     handlers.IncidentsReader
	// IncidentsAdmin backs DELETE /incidents (admin-only bulk clear across
	// every registered central). Nil disables the route (404); shares the
	// domain call with the WS `incidents.clear` command.
	IncidentsAdmin handlers.IncidentsClearer
	// Alarm backs the /alarm surface (the alarm-panel engine + output
	// drivers + config stores). Nil leaves every /alarm route unmounted
	// (the alarm subsystem is disabled or failed to come up).
	Alarm handlers.AlarmPanel
	// AlarmCodes backs the /alarm/codes CRUD surface (the argon2id-hashed
	// alarm-code store, docs/alarm-concept.md §11). The routes mount
	// whenever Alarm is set; a nil AlarmCodes serves them as 503 so the
	// contract is present even before the codes facade is wired.
	AlarmCodes handlers.AlarmCodeAdmin
	Labels     handlers.ParameterLabeler
	Metrics    *metrics.Registry
	// MasterProfiles backs the read-only master-profile discovery routes:
	//   GET  /api/v1/devices/{addr}/channels/{no}/master-profiles
	//   GET  /api/v1/devices/{addr}/channels/{no}/master-profiles/{id}
	//   POST /api/v1/devices/{addr}/channels/{no}/master-profiles/match
	// Nil disables all three (404). Shares the domain call with the WS
	// `master_profiles.list/get/match` commands.
	MasterProfiles handlers.MasterProfilesService
	// UISchema produces the data-driven rendering descriptor the SPA
	// consumes at /api/v1/devices/{addr}/channels/{no}/ui-schema.
	UISchema handlers.UISchemaService
	// Links backs the direct-link (Direktverknüpfung) endpoints:
	//   GET    /api/v1/devices/{addr}/links
	//   POST   /api/v1/devices/{addr}/links
	//   PATCH  /api/v1/devices/{addr}/links
	//   DELETE /api/v1/devices/{addr}/links?sender=…&receiver=…
	//   GET    /api/v1/devices/{addr}/channels/{no}/linkable-channels
	Links handlers.LinksService
	// Schedules drives the climate-schedule endpoints:
	//   GET  /api/v1/devices/{addr}/channels/{no}/schedule
	//   PUT  /api/v1/devices/{addr}/channels/{no}/schedule
	//   POST /api/v1/devices/{addr}/channels/{no}/schedule/active-profile
	Schedules handlers.ScheduleService
	// SystemStatus backs GET /api/v1/system/status — a bounded ring
	// buffer of recent SystemStatusChangedEvents. Nil disables the
	// endpoint (returns 503). Wire via
	// [handlers.SystemStatusBuffer.Subscribe] after the event bus is
	// live.
	SystemStatus handlers.SystemStatusReader
	// Audit feeds the change-history view: GET /api/v1/audit.
	Audit handlers.AuditService
	// History feeds the measurement-history chart: GET /api/v1/history.
	// Nil when the opt-in history feature is disabled (the default).
	History handlers.HistoryService
	// RecordingOverrides feeds the per-datapoint recording toggle:
	// GET/PUT /api/v1/history/recording. Nil when the opt-in history
	// feature is disabled (the same flag /history depends on).
	RecordingOverrides handlers.RecordingOverrideService
	// ChannelFlags + ChannelFlagsOverlay back the per-channel operator
	// override endpoints (G12): GET/PUT
	// /api/v1/devices/{addr}/channels/{no}/flags. Both nil when there is no
	// durable DB (the routes are then not mounted).
	ChannelFlags        handlers.ChannelFlagsWriter
	ChannelFlagsOverlay *channelflags.Overlay
	// Energy feeds the energy view's per-device power/energy breakdown:
	// GET /api/v1/energy. Nil when the opt-in history feature is
	// disabled (the same feature flag /history depends on).
	Energy handlers.EnergyService
	// Auth exposes login/logout/me endpoints at /api/v1/auth so the
	// SPA can authenticate without the HTMX pages. Nil disables the
	// endpoints (they 503 on request).
	Auth *handlers.AuthDeps

	// ConfigAdmin backs the live-edit config endpoints
	// (`GET /config/schema`, `GET|PUT|DELETE /config/{section}`).
	// Nil disables all of them with 503.
	ConfigAdmin handlers.ConfigAdminService
	// RestartPending backs GET /system/restart-pending — whether a saved
	// restart-required config change is staged but not yet active.
	RestartPending handlers.RestartPendingProvider
	// ConfigChanges backs GET /system/config-changes — config fields that
	// differ from the running boot config (what changed since start).
	ConfigChanges handlers.ConfigChangesProvider

	// UserAdmin backs the SQLite-backed `/users` CRUD. Nil keeps
	// the legacy in-memory `/auth/users` read-only path active.
	UserAdmin handlers.UserAdminService

	// SelfPassword backs the self-service password change at
	// PATCH /auth/me/password. Nil disables the route.
	SelfPassword handlers.SelfPasswordService

	// SessionRevoker invalidates a subject's server-side sessions when a
	// credential changes (password reset, role change, deletion). Nil skips
	// the revocation hook; wired to the *auth.SessionStore in production.
	SessionRevoker handlers.SessionRevoker

	// TokenPurger deletes a subject's bearer tokens on account deletion.
	// Nil skips the purge; wired to the *sqlite.TokenStore in production.
	TokenPurger handlers.TokenPurger

	// Preferences backs per-user UI state (favorites, dashboard) at
	// /me/preferences/{key}. Nil disables those routes.
	Preferences handlers.UserPreferencesService
	// Diagrams backs the named multi-series diagram CRUD at /diagrams.
	// Nil disables those routes.
	Diagrams handlers.DiagramConfigService

	// Areas backs the operator-defined area (room grouping) CRUD at
	// /areas, incl. full-set room-assignment replace at
	// /areas/{id}/rooms. Nil disables those routes. Distinct from the
	// alarm engine's own "area" partitions (Alarm field above).
	Areas handlers.AreaAdmin

	// RoomFunctionAdmin backs room/function entity CRUD at /rooms and
	// /functions (create/rename/delete). Nil disables those routes;
	// the read-only GET /rooms + GET /functions stay available.
	RoomFunctionAdmin handlers.RoomFunctionAdmin

	// TLSCert backs the runtime certificate upload at
	// POST /admin/tls/certificate. Nil when TLS is not enabled.
	TLSCert handlers.TLSCertService

	// TokenAdmin backs the SQLite-backed `/auth/tokens` CRUD.
	TokenAdmin handlers.TokenAdminService

	// CentralAdmin backs the SQLite-backed `/centrals` CRUD.
	CentralAdmin handlers.CentralAdminService
	// Discovery backs the SSDP CCU-discovery endpoints
	// (`/api/v1/centrals/discovered*`). Nil disables them (list returns empty).
	Discovery *handlers.DiscoveryDeps

	// MQTTReload backs POST /admin/mqtt/reload. Nil disables the
	// route — operators on builds without the supervisor (tests,
	// MQTT-disabled deployments) get a clean 404 rather than a 500.
	MQTTReload handlers.MQTTReloadService
	// CacheReset backs POST /admin/cache/clear — the scoped
	// clear-caches + readiness-gated re-pull operation (ADR 0042).
	// Nil disables the route (404) when south-bound never came up.
	CacheReset handlers.CacheResetService
	// OIDC mounts /api/v1/auth/oidc/{start,callback} when configured.
	// The flow drops the same session cookie as Login(), so the rest
	// of the SPA needs no further wiring. Nil = OIDC disabled.
	OIDC *handlers.OIDCDeps
	// SPAHandler serves the Svelte SPA under /app/*. Same-origin with
	// /api/v1/* is essential — the SPA relies on session cookies, and
	// cross-origin requests would need CORS gymnastics. Mounting here
	// keeps the auth boundary simple.
	SPAHandler http.Handler
	// Bootstrap serves the server-rendered HTMX bootstrap surface (login,
	// first-run /setup wizard, /about, OIDC HTMX flow) on the SAME listener
	// as the SPA, so the whole onboarding works through one port / HA Ingress
	// (ADR 0044). Nil disables those routes. Folded in from the former
	// stand-alone :8081 UI listener.
	Bootstrap http.Handler
	// Setup backs the first-run onboarding endpoints
	// (`GET /api/v1/setup/status`, `POST /api/v1/setup`). Nil disables them
	// (status reports not-required, finalize returns 503).
	Setup *handlers.SetupService
	// LoginRateLimit guards POST /api/v1/auth/login against per-IP brute-force
	// sweeps. Nil disables the speed-bump (used by test fixtures).
	LoginRateLimit *middleware.LoginRateLimiter
	Backup         handlers.BackupService
	// CentralLinks toggles CCU click-event forwarding for press-event
	// channels. Nil disables the endpoint trio
	// (`GET|POST|DELETE /api/v1/devices/{addr}/central-links`).
	CentralLinks handlers.CentralLinksService
	// DefinitionExport backs the device-definition export endpoint:
	//   GET /api/v1/devices/{addr}/export-definition
	// It produces an anonymised zip whose JSON members are byte-compatible
	// with the Python reference's export_device_definition. Nil disables the endpoint
	// (returns 503).
	DefinitionExport handlers.DeviceDefinitionExportService
	// ConfigExport backs the channel configuration export/import endpoints:
	//   GET  /api/v1/devices/{addr}/channels/{no}/config/export
	//   POST /api/v1/devices/{addr}/channels/{no}/config/import
	// Both fields may be nil independently; nil disables the respective
	// endpoint (returns 503).
	ConfigExport handlers.ConfigExportService
	// ConfigChannelMeta resolves device/channel metadata for the export
	// endpoint. Nil causes the handler to fall back to the URL params only
	// (model and channel_type fields will be empty in the snapshot).
	ConfigChannelMeta handlers.ChannelInfoReader
	// EditSessions backs the per-resource edit-lock endpoints
	// (`POST/DELETE /api/v1/sessions/edit`). Nil falls back to the
	// previous, fully optimistic behaviour.
	EditSessions *handlers.EditSessions
	WSHandler    http.Handler
	AuthResolve  func(http.Handler) http.Handler
	AuthRequire  func(http.Handler) http.Handler
	// Capabilities lets `GET /info` surface the runtime feature set
	// (MQTT discovery on, Matter bridge wired, OIDC configured).
	// Nil emits the always-on capabilities only.
	Capabilities handlers.CapabilityDetector
	// SystemCCU backs `GET /api/v1/system/ccu` — per-central CCU
	// metadata (model, version, serial, configured interfaces). Nil
	// returns an empty entries array.
	SystemCCU handlers.SystemCCUReader
	// CCUReboot backs `POST /api/v1/system/ccu/{central}/reboot` — an
	// admin-only reboot of one CCU host. Nil serves the route as 503.
	CCUReboot handlers.CCURebootPort
	// CCUPosition backs `PUT /api/v1/system/ccu/{central}/position` — an
	// admin-only write of a CCU's astro reference position. Nil serves
	// the route as 503.
	CCUPosition handlers.CCUPositionPort
	// CCUHostActions backs the admin-only host power and boot-mode routes
	// under `/api/v1/system/ccu/{central}/`. Nil serves them as 503.
	CCUHostActions handlers.CCUHostActionPort
	// BackupUpload backs `POST /api/v1/backups/upload` — importing an
	// externally-supplied .sbk. Nil serves the route as 503.
	BackupUpload handlers.BackupUploader
	// PreUpdateBackup backs the optional `backup_first` flag on
	// `POST /api/v1/system/update/install`. Nil rejects that flag with
	// 503 rather than silently updating without a backup.
	PreUpdateBackup handlers.PreUpdateBackupPort
	// FirmwareDownload backs `POST /api/v1/system/firmware/download` — an
	// admin-only trigger that has one CCU fetch a firmware image onto the
	// central. Nil serves the route as 503.
	FirmwareDownload handlers.FirmwareDownloadPort
	// AddonUpdate backs the CCU add-on self-update surface (ADR 0057):
	// `GET /api/v1/system/addon-update` (always 200; nil reports a
	// minimal supported:false body) plus the operator-gated
	// `POST …/check` and `POST …/install` (nil/unsupported answers 404).
	// Wired only on platforms where the capability probe passed (add-on
	// build + firmware installer present).
	AddonUpdate handlers.AddonUpdateService
	// Groups backs `GET /api/v1/groups` — the read-only heating-group
	// listing (one entry per central; `?central=` scopes to one). Nil
	// serves the route as 503.
	Groups handlers.GroupsReader
	// GroupsWriter backs heating-group administration (GR02): create / edit
	// / delete plus the suitable-members and types read helpers, via the CCU
	// jpages proxy. Nil serves those routes as 503.
	GroupsWriter handlers.GroupsWriter
	// RateLimit, when non-nil, installs the per-identity REST
	// rate limiter before the auth-require gate. Nil disables it.
	RateLimit *middleware.RateLimitConfig
	// RequireOperator wraps mutations that an operator-grade user is
	// allowed to perform (paramset writes, link CRUD, schedule edits,
	// sysvar writes, device pairing / install mode, firmware update).
	// Nil falls back to AuthRequire (any authenticated user); when
	// AuthRequire is also nil the shim fails closed (401).
	RequireOperator func(http.Handler) http.Handler
	// RequireAdmin gates dangerous operations (delete device, backup
	// trigger, cache clear, interface reconnect, user/token/central CRUD).
	// Nil falls back to AuthRequire; when that is also nil it fails closed.
	RequireAdmin func(http.Handler) http.Handler
	CORS         *middleware.CORSConfig
	Idempotent   bool
	// WriteTimeout bounds every request via [middleware.Timeout] (a
	// context deadline the handler chain observes), not the raw
	// [http.Server.WriteTimeout] — [NewServer] takes only the handler,
	// so the TCP-level timeouts are fixed constants in server.go.
	WriteTimeout time.Duration
	// CentralName is the scope this REST router is bound to. When set,
	// every request enriches its [reqctx.RequestContext] with the name
	// so log records and domain calls carry the central tag. Leave
	// empty in multi-central setups where handlers resolve the central
	// per-request.
	CentralName string
	// OpenAPIValidator, when non-nil, gates every request through the
	// kin-openapi validator. Mounted right after [middleware.Logger]
	// so the rejection path still produces a logged record. Build via
	// [middleware.NewOpenAPIValidator] in the composition root.
	OpenAPIValidator *middleware.OpenAPIValidator
	// MatterFabricStore backs GET /api/v1/matter/fabrics. Nil disables
	// the endpoint (returns 503 service_unready).
	MatterFabricStore handlers.MatterFabricStore
	// MatterCommissioning carries the bridge's discriminator / passcode /
	// vendor / product so GET /api/v1/matter/setup-payload can emit
	// QR + manual codes. Zero-value disables the endpoint (503).
	MatterCommissioning handlers.MatterCommissioning
	// MatterCommissioningOpener backs POST /api/v1/matter/commissioning/window.
	// Nil disables the endpoint (503).
	MatterCommissioningOpener handlers.MatterCommissioningOpener
	// MatterExposureStore backs the allowlist endpoints
	// (`/matter/exposable*`). Nil disables them (503).
	MatterExposureStore handlers.MatterExposureStore
	// MatterCandidateProvider yields the per-source classification
	// list the operator sees in the allowlist UI. Nil disables the
	// `/matter/exposable` GET endpoint.
	MatterCandidateProvider handlers.MatterCandidateProvider
	// MatterStatusReader backs `/matter/status`. Nil → status returns
	// `{"enabled":false}` (clean default for SPA probing).
	MatterStatusReader handlers.MatterStatusReader
	// MatterFabricRevoker backs `DELETE /matter/fabrics/{id}`.
	MatterFabricRevoker handlers.MatterFabricRevoker
	// MatterCommissioningCloser backs `POST /matter/commissioning/window/close`.
	MatterCommissioningCloser handlers.MatterCommissioningCloser
	// MatterEventPublisher routes server-pushed Matter events
	// (`matter.exposable_changed`, `matter.fabric_added`, …) to the
	// WebSocket hub. Nil = events are dropped (test convenience).
	MatterEventPublisher handlers.MatterEventPublisher
	// MatterTopologyReassembler triggers a bridge re-assemble after
	// `/matter/exposable[/bulk]` writes so the allowlist takes effect
	// immediately. Nil = the persisted change only surfaces on the
	// next daemon restart (test convenience).
	MatterTopologyReassembler handlers.MatterTopologyReassembler
	// MatterAuditRecorder is the write side of the audit ledger the
	// Matter mutation handlers append to (per
	// `docs/matter-ui-concept.md` §6). Same buffer as the read-side
	// [Deps.Audit] in production; nil = audit-disabled (test).
	MatterAuditRecorder audit.Recorder

	// VisibilityUnIgnoreStore backs the `/visibility/unignore*` REST
	// surface (see docs/ui/unignore-concept.md). Nil disables the
	// endpoints with 503 service_unready.
	VisibilityUnIgnoreStore handlers.VisibilityUnIgnoreStore
	// VisibilityCentralLister returns the names of every central the
	// daemon manages; the visibility handlers iterate over it to
	// gather per-central candidate lists and persisted patterns.
	VisibilityCentralLister handlers.VisibilityCentralLister
	// VisibilityCandidateProvider yields the per-central candidate set
	// of hidden parameter names. Backed by an adapter that wraps each
	// central's [*central.QueryFacade].
	VisibilityCandidateProvider handlers.VisibilityCandidateProvider
	// VisibilityRegistryLoader applies fresh un-ignore lists to the
	// live visibility decider + re-runs the suppression marks on every
	// affected device. Nil disables PUT (503).
	VisibilityRegistryLoader handlers.VisibilityRegistryLoader

	// LogLevels backs the diagnostics log-levels endpoint trio
	// (`GET|PUT|DELETE /api/v1/diagnostics/log-levels`). Wire with the
	// daemon's [*hmlog.LevelRegistry]. Nil disables the endpoints
	// (returns 503).
	LogLevels handlers.LogLevelsService
	// HealthExtras complements [Deps.Health] with the numeric Score
	// and the IsAvailable/IsDegraded/IsFailed flags used by the
	// diagnostics dump. The same *health.Tracker satisfies both
	// interfaces; passing nil leaves the diagnostics block to derive
	// flags from the overall status alone.
	HealthExtras handlers.HealthExtras
	// Capture backs the `/diagnostics/capture/*` endpoints. Wire with
	// the daemon's [*diagnostics.Manager]. Nil disables the endpoints
	// (returns 503).
	Capture handlers.CaptureService
	// LogFeed backs the log-viewer endpoints (`/diagnostics/logs` backfill/
	// download + `/diagnostics/logs/stream` SSE tail). Nil disables them.
	LogFeed handlers.LogFeedService
	// LogDefaultLevel backs the log-viewer level dropdown
	// (`/diagnostics/log-level`). Nil disables it.
	LogDefaultLevel handlers.LogDefaultLevelService
	// RPCRecorder backs the `/diagnostics/rpc-recording*` endpoints — the
	// XML/JSON-RPC session recorder for deterministic replay. Nil disables
	// them (returns 503 / empty list).
	RPCRecorder handlers.RPCRecorderService
	// Introspect backs the live-introspection diagnostics endpoints
	// (`GET /diagnostics/reliability`, `GET /diagnostics/eventbus/tap`).
	// Read-only; nil disables them.
	Introspect handlers.DiagnosticsIntrospectService
	// RSSIInfo backs `GET /diagnostics/rssi` — the CCU's pairwise RF
	// reception matrix. Read-only; nil disables the endpoint.
	RSSIInfo handlers.RSSIMatrixService
	// AuditRecorder is the daemon-wide audit sink the diagnostics
	// endpoints append override / capture events to. Same buffer as
	// [Deps.MatterAuditRecorder] in production wiring; the separate
	// field documents the call-site intent without coupling the two
	// audit domains in code.
	AuditRecorder audit.Recorder
	// StartupCapture persists the next-boot capture config that the
	// Settings UI exposes as a toggle. Wired in production by the
	// daemon's composition root via
	// [handlers.NewStartupCaptureFileService]. Nil disables both the
	// GET and PUT endpoints.
	StartupCapture handlers.StartupCaptureService
	// EnableRestartEndpoint, when true, mounts
	// `POST /api/v1/system/restart` and lets an admin trigger a
	// graceful shutdown via the SPA. False keeps the endpoint
	// unmounted so deployments without a supervisor (no
	// systemd / Docker restart-policy) cannot accidentally take
	// themselves offline.
	EnableRestartEndpoint bool
	// StatusMetrics receives the per-response status-code counts the
	// `rest.5xx` / `rest.4xx` health gauges read. Nil disables the
	// middleware (no metrics emitted).
	StatusMetrics *middleware.StatusMetrics
	// ValuesCache backs `/admin/values-cache/stats` and the reset
	// endpoints. Wired in production with the daemon's persistent
	// VALUES cache store. Nil leaves the endpoints returning 503.
	ValuesCache handlers.ValuesCacheService
	// DeviceLookup resolves a bare device address to its (central,
	// interface) tuple so the per-device values-cache reset works in
	// multi-CCU deployments. Nil disables that one endpoint; the
	// global reset still works.
	DeviceLookup handlers.DeviceLookup
	// KnownCentrals is the list of CCU scope names whose
	// per-central health score the diagnostics dump should
	// publish. Daemon composition root fills this from the central
	// registry; tests may pass an explicit list. Empty disables the
	// `central_scores` block in the diagnostics envelope.
	KnownCentrals []string
	// HealthGauges, when set, returns the daemon's current
	// pull-gauge readings (event_bus / audit / scheduler / rest /
	// ws). `(*health.Tracker).Gauges` satisfies this directly.
	HealthGauges func() map[string]float64
	// CSRFEnabled mounts auth.CSRFMiddleware in the chain when true.
	// The double-submit cookie/header guard protects mutating REST
	// endpoints against cross-site request forgery. Disabled by
	// default for backward compatibility.
	CSRFEnabled bool
	// CSRFSecure passes the Secure flag to the CSRF cookie. Set true
	// behind HTTPS/TLS terminators.
	CSRFSecure bool
}

// NewRouter builds the `/api/v1` router from d.
func NewRouter(d Deps) *chi.Mux { //nolint:gocognit,gocyclo,funlen // composition/wiring: long sequential setup
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.StartedAt.IsZero() {
		d.StartedAt = time.Now()
	}
	if d.WriteTimeout == 0 {
		d.WriteTimeout = 30 * time.Second
	}

	r := chi.NewRouter()
	// First middleware by contract: route on the percent-decoded path so
	// every chi.URLParam yields decoded values (see decodedPathRouting).
	r.Use(decodedPathRouting)
	r.Use(middleware.RequestID)
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.ReqContextWithCentral(d.CentralName))
	r.Use(middleware.Logger(d.Logger))
	r.Use(middleware.Recover(d.Logger))
	r.Use(middleware.Timeout(d.WriteTimeout))
	if d.StatusMetrics != nil {
		r.Use(middleware.StatusCounter(d.StatusMetrics))
	}
	if d.CORS != nil {
		r.Use(middleware.CORS(*d.CORS))
	}
	if d.AuthResolve != nil {
		r.Use(d.AuthResolve)
	}
	if d.Idempotent {
		// Mounted after AuthResolve so the cache key can incorporate the
		// resolved identity (see middleware.Idempotency doc comment) —
		// otherwise two different users sharing an Idempotency-Key value
		// on the same method+path would replay each other's response.
		r.Use(middleware.Idempotency())
	}
	if d.CSRFEnabled {
		r.Use(auth.CSRFMiddleware(d.CSRFSecure))
	}
	if d.RateLimit != nil {
		r.Use(middleware.RateLimit(*d.RateLimit))
	}

	// Mount the SPA before the NotFound handler so an unknown /app/*
	// path falls through to SPAHandler's index.html (client-side
	// routing) rather than triggering the REST 404.
	if d.SPAHandler != nil {
		// Root → SPA. Behind Home Assistant Ingress the Supervisor strips
		// its proxy prefix before the request reaches us and passes it in
		// X-Ingress-Path; echo it back so the browser lands on
		// <prefix>/app/. A bare "/app/" would resolve against the Home
		// Assistant origin and bypass the Ingress proxy.
		r.Get("/", func(w http.ResponseWriter, req *http.Request) {
			prefix := safeIngressPrefix(req)
			// Root → SPA. The SPA probes /api/v1/setup/status on boot and
			// renders the onboarding wizard itself when the daemon has no
			// authentication source yet, so first-run no longer needs a
			// server-side redirect here.
			target := "/app/"
			if prefix != "" {
				target = prefix + "/app/"
			}
			// target is either the constant "/app/" or a validated local
			// absolute path (safeIngressPrefix rejects scheme/host forms), so
			// this cannot redirect to a foreign origin.
			http.Redirect(w, req, target, http.StatusFound) //nolint:gosec // G710: target validated to a local path
		})
		r.Handle("/app", http.RedirectHandler("/app/", http.StatusMovedPermanently))
		r.Handle("/app/*", http.StripPrefix("/app/", d.SPAHandler))
	}

	// Server-rendered diagnostic surface (/about + a no-JS /health for the
	// case where the SPA bundle fails to load, plus its /ui/assets CSS),
	// folded onto this listener from the former :8081 UI server so SPA-down
	// diagnosis works through one port (HA Ingress). Login, onboarding, and
	// OIDC now live in the SPA. None of these overlap with /api/v1, /app or
	// the root /, and they run unauthenticated by design.
	if d.Bootstrap != nil {
		for _, p := range []string{"/about", "/health", "/ui"} {
			r.Handle(p, d.Bootstrap)
			r.Handle(p+"/*", d.Bootstrap)
		}
	}

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, req, "Route not found", req.URL.Path))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		problem.Write(w, http.StatusMethodNotAllowed,
			problem.New(problem.TypeBadRequest, req, "Method not allowed", req.Method))
	})

	r.Route("/api/v1", func(r chi.Router) {
		// OpenAPI request validation runs only inside the /api/v1
		// subtree — the SPA mount, /app/*, and any future static-
		// asset routes are not described by the spec and would
		// otherwise be rejected with "Route not found in OpenAPI spec".
		if d.OpenAPIValidator != nil {
			r.Use(d.OpenAPIValidator.Middleware())
		}
		r.Get("/info", handlers.Info(d.StartedAt, d.Capabilities))
		r.Get("/health", handlers.Health(d.Health))

		// Device-type icon proxy. Unauthenticated like /health: it
		// serves only non-sensitive device model artwork and must
		// resolve from an <img> tag regardless of auth scheme. Nil
		// proxy → 404 (SPA falls back to a generic glyph).
		r.Get("/devices/{addr}/icon", handlers.GetDeviceIcon(d.DeviceIcons))

		// Auth endpoints stay outside the AuthRequire group — a logged-
		// out SPA must be able to POST credentials to /auth/login.
		// `/auth/me` is the probe the SPA uses on startup to decide
		// whether to show the login page; it returns 401 when there
		// is no active session.
		if d.Auth != nil {
			// The per-IP brute-force speed-bump wraps only the login POST — the
			// SPA is the sole login surface, so this is the one place a sweep can
			// guess credentials. Nil limiter ⇒ plain handler (test fixtures).
			login := handlers.Login(d.Auth)
			if d.LoginRateLimit != nil {
				r.With(d.LoginRateLimit.Middleware()).Post("/auth/login", login)
			} else {
				r.Post("/auth/login", login)
			}
			r.Post("/auth/logout", handlers.Logout(d.Auth))
			r.Get("/auth/me", handlers.Me())
		}
		// First-run onboarding. Unauthenticated: no admin exists yet when the
		// wizard runs. POST /setup hard-gates itself on the first-run probe.
		r.Get("/setup/status", handlers.SetupStatus(d.Setup))
		r.Post("/setup", handlers.Setup(d.Setup))
		if d.OIDC != nil {
			r.Get("/auth/oidc/start", handlers.OIDCStart(d.OIDC))
			r.Get("/auth/oidc/callback", handlers.OIDCCallback(d.OIDC))
		}

		// Permission shorthands. When a specific role middleware isn't
		// wired it falls back to the blanket AuthRequire (any authenticated
		// user). The final pass-through fallback exists only for tests that
		// build a router without an auth chain; production is protected by
		// [AssertAuthWired], called from the composition root, which refuses
		// to start a router whose auth middleware is nil.
		op := d.RequireOperator
		if op == nil {
			op = d.AuthRequire
		}
		if op == nil {
			op = func(h http.Handler) http.Handler { return h }
		}
		admin := d.RequireAdmin
		if admin == nil {
			admin = d.AuthRequire
		}
		if admin == nil {
			admin = func(h http.Handler) http.Handler { return h }
		}

		// Protected routes require auth when the dependency is wired.
		if d.Auth != nil {
			r.With(admin).Get("/auth/users", handlers.ListUsers(d.Auth))
			r.With(admin).Get("/auth/tokens", handlers.ListTokens(d.Auth))
			r.With(admin).Post("/auth/tokens", handlers.CreateToken(d.Auth))
			r.With(admin).Delete("/auth/tokens/{id}", handlers.DeleteToken(d.Auth))
		}
		// Inbound webhook: external systems POST to set a value or run a
		// program. Mounted OUTSIDE the AuthRequire group (like /auth/login)
		// because the InboundWebhookAuth middleware is the full gate — it
		// admits an operator identity (resolved by the global AuthResolve) OR
		// the configured inbound bearer token, so a header-only caller is not
		// pre-rejected by the blanket AuthRequire. Only mounted when enabled
		// (restart-required). These are real device writes / program runs.
		if d.WebhookInboundEnabled {
			inboundAuth := handlers.InboundWebhookAuth(d.WebhookInboundToken)
			r.With(inboundAuth).Post("/webhook/value", handlers.WebhookInboundValue(d.DPWriter))
			r.With(inboundAuth).Post("/webhook/program", handlers.WebhookInboundProgram(d.Hub))
		}

		r.Group(func(pr chi.Router) {
			if d.AuthRequire != nil {
				pr.Use(d.AuthRequire)
			}
			if d.Config != nil {
				pr.Get("/config", handlers.Config(d.Config))
			}
			// UI telemetry — anonymous fire-and-forget endpoint the
			// SPA uses to log toggle / view-selector events (ADR 0016).
			pr.Post("/ui/event", handlers.PostUIEvent())
			// Self-service password change for the logged-in local user.
			// Any authenticated role; the handler verifies the current
			// password and preserves the role.
			if d.SelfPassword != nil {
				pr.Patch("/auth/me/password", handlers.ChangeOwnPassword(d.SelfPassword, d.AuditRecorder, d.SessionRevoker))
			}
			// Per-user preferences (favorites / dashboard). Any
			// authenticated role; scoped to the caller's subject.
			if d.Preferences != nil {
				pr.Get("/me/preferences/{key}", handlers.GetPreference(d.Preferences))
				pr.Put("/me/preferences/{key}", handlers.PutPreference(d.Preferences))
				pr.Delete("/me/preferences/{key}", handlers.DeletePreference(d.Preferences))
			}
			if d.Diagrams != nil {
				pr.Get("/diagrams", handlers.ListDiagrams(d.Diagrams))
				pr.Get("/diagrams/{id}", handlers.GetDiagram(d.Diagrams))
				pr.With(op).Post("/diagrams", handlers.CreateDiagram(d.Diagrams, d.AuditRecorder))
				pr.With(op).Put("/diagrams/{id}", handlers.UpdateDiagram(d.Diagrams, d.AuditRecorder))
				pr.With(op).Delete("/diagrams/{id}", handlers.DeleteDiagram(d.Diagrams, d.AuditRecorder))
			}
			if d.Areas != nil {
				// Read-only list is available to any authenticated caller;
				// the write routes are operator-gated further down
				// alongside the /rooms admin block.
				pr.Get("/areas", handlers.ListAreas(d.Areas))
			}
			if d.Devices != nil {
				pr.Post("/devices/values:batch", handlers.ValuesBatch(d.Devices, d.Labels, d.DataPointVis))
				pr.Get("/rooms", handlers.ListRooms(d.Devices))
				pr.Get("/functions", handlers.ListFunctions(d.Devices))
				pr.Get("/devices", handlers.ListDevices(d.Devices))
				pr.Get("/devices/{addr}", handlers.GetDevice(d.Devices, d.Labels))
				pr.Get("/devices/{addr}/channels", handlers.ListChannels(d.Devices, d.Labels))
				pr.Get("/devices/{addr}/channels/{no}", handlers.GetChannel(d.Devices, d.Labels))
				pr.Get("/devices/{addr}/channels/{no}/event-groups", handlers.ListEventGroups(d.Devices))
				pr.Get("/devices/{addr}/channels/{no}/data-points", handlers.ListDataPoints(d.Devices, d.Labels, d.DataPointVis))
				pr.Get("/devices/{addr}/channels/{no}/data-points/{param}", handlers.GetDataPoint(d.Devices, d.Labels))
				pr.With(op).Put("/devices/{addr}/channels/{no}/data-points/{param}/value",
					handlers.PutDataPointValue(d.Devices, d.DPWriter))
				// Per-channel operator overrides (G12): hidden / locked. Always
				// mounted; PutChannelFlags returns 503 when there is no durable
				// store/overlay, GetChannelFlags reads the live channel.
				pr.Get("/devices/{addr}/channels/{no}/flags",
					handlers.GetChannelFlags(d.Devices))
				pr.With(op).Put("/devices/{addr}/channels/{no}/flags",
					handlers.PutChannelFlags(d.Devices, d.ChannelFlags, d.ChannelFlagsOverlay, d.AuditRecorder))
				// Custom data points (Phase C).
				pr.Get("/devices/{addr}/cdps",
					handlers.ListCustomDataPoints(d.Devices, d.Labels))
				pr.Get("/devices/{addr}/cdps/{name}",
					handlers.GetCustomDataPoint(d.Devices))
				pr.With(op).Post("/devices/{addr}/cdps/{name}/{operation}",
					handlers.InvokeCustomDataPoint(d.Devices, d.CustomDPWriter))
				// Calculated data points (Phase C).
				pr.Get("/devices/{addr}/channels/{no}/calc-dps",
					handlers.ListCalculatedDataPoints(d.Devices, d.Labels))
				pr.Get("/devices/{addr}/channels/{no}/calc-dps/{name}",
					handlers.GetCalculatedDataPoint(d.Devices, d.Labels))
				// Week-profile metadata (read-only; full schedule data via schedule routes).
				pr.Get("/devices/{addr}/channels/{no}/week_profile",
					handlers.GetWeekProfile(d.Devices))
				// Write half of the schedule_enabled map: toggle one target
				// channel's week-program participation.
				pr.With(op).Put("/devices/{addr}/channels/{no}/week_profile/channel-locks/{key}",
					handlers.PutWeekProfileChannelLock(d.Devices))
				if d.MasterProfiles != nil {
					// list/get/match are viewer-accessible, mirroring the WS
					// master_profiles.list/get/match classification (readOnlyCommands
					// in internal/north/rest/ws/commands.go) — no role gate. Nested
					// under d.Devices != nil because the handlers resolve
					// device_type/channel_type from the channel's owning device.
					pr.Get("/devices/{addr}/channels/{no}/master-profiles",
						handlers.ListMasterProfiles(d.Devices, d.MasterProfiles))
					pr.Get("/devices/{addr}/channels/{no}/master-profiles/{id}",
						handlers.GetMasterProfile(d.Devices, d.MasterProfiles))
					pr.Post("/devices/{addr}/channels/{no}/master-profiles/match",
						handlers.MatchMasterProfile(d.Devices, d.MasterProfiles))
				}
			}
			if d.ParameterDeterminer != nil {
				// Read one parameter's live value from the device (the MASTER
				// editor's "Determine" button). A read, so no edit-lock token —
				// but it triggers a device round-trip like master-profiles/match.
				pr.Post("/devices/{addr}/channels/{no}/paramsets/{key}/determine",
					handlers.DetermineParameter(d.ParameterDeterminer))
			}
			if d.UISchema != nil {
				pr.Get("/devices/{addr}/channels/{no}/ui-schema", handlers.UISchemaHandler(d.UISchema))
			}
			// Export and import are always mounted so the spec-documented
			// 503 service_unready response is reachable when the backend
			// is not wired. A nil ConfigExport makes both handlers return
			// 503 via their internal nil-guard.
			pr.Get("/devices/{addr}/channels/{no}/config/export",
				handlers.ExportChannelConfig(d.ConfigExport, d.ConfigChannelMeta))
			pr.With(op).Post("/devices/{addr}/channels/{no}/config/import",
				handlers.ImportChannelConfig(d.ConfigExport))
			// A nil DefinitionExport makes the handler return 503 via its
			// internal nil-guard.
			pr.Get("/devices/{addr}/export-definition",
				handlers.ExportDeviceDefinition(d.DefinitionExport))
			if d.Links != nil {
				pr.Get("/links", handlers.ListAllLinks(d.Links))
				pr.Get("/devices/{addr}/links", handlers.ListLinks(d.Links))
				pr.With(op).Post("/devices/{addr}/links/test", handlers.TestLinkAtDevice(d.Links))
				pr.With(op).Post("/devices/{addr}/links", handlers.AddLink(d.Links))
				pr.With(op).Patch("/devices/{addr}/links", handlers.UpdateLink(d.Links))
				pr.With(op).Delete("/devices/{addr}/links", handlers.RemoveLink(d.Links))
				pr.Get("/devices/{addr}/channels/{no}/linkable-channels",
					handlers.LinkableChannels(d.Links))
			}
			if d.Schedules != nil {
				pr.Get("/devices/{addr}/channels/{no}/schedule",
					handlers.GetSchedule(d.Schedules))
				pr.With(op).Put("/devices/{addr}/channels/{no}/schedule",
					handlers.PutSchedule(d.Schedules))
				pr.With(op).Post("/devices/{addr}/channels/{no}/schedule/active-profile",
					handlers.PostActiveProfile(d.Schedules))
				// Device-level convenience routes resolve the schedule
				// Channel automatically (mirrors 's
				// _resolve_climate_schedule_channel).
				pr.Get("/devices/{addr}/schedule",
					handlers.GetScheduleAuto(d.Schedules))
				pr.With(op).Put("/devices/{addr}/schedule",
					handlers.PutScheduleAuto(d.Schedules))
				pr.With(op).Post("/devices/{addr}/schedule/active-profile",
					handlers.PostActiveProfileAuto(d.Schedules))
				// Copy the whole device schedule to another device
				// (channels auto-resolved on both sides).
				pr.With(op).Post("/devices/{addr}/schedules/copy",
					handlers.PostCopySchedule(d.Schedules))
				// Copy a single climate profile from the source channel /
				// profile to a target channel / profile.
				pr.With(op).Post("/devices/{addr}/channels/{no}/week_profile/copy",
					handlers.PostCopyProfile(d.Schedules))
			}
			if d.Audit != nil {
				// The change-log can expose subjects, device addresses and
				// operator actions across every central, so it is admin-only —
				// a viewer / operator identity must not read it.
				pr.With(admin).Get("/audit", handlers.ListAudit(d.Audit, d.Devices))
			}
			if d.History != nil {
				pr.Get("/history", handlers.GetHistory(d.History))
			}
			if d.RecordingOverrides != nil {
				pr.Get("/history/recording", handlers.GetRecordingOverride(d.RecordingOverrides))
				pr.With(op).Put("/history/recording", handlers.PutRecordingOverride(d.RecordingOverrides, d.AuditRecorder))
			}
			if d.Energy != nil {
				pr.Get("/energy", handlers.GetEnergy(d.Energy))
			}
			if d.DeviceAdmin != nil {
				pr.With(admin).Delete("/devices/{addr}", handlers.DeleteDevice(d.DeviceAdmin))
				pr.With(op).Patch("/devices/{addr}", handlers.PatchDevice(d.DeviceAdmin, d.AuditRecorder))
				pr.With(op).Patch("/devices/{addr}/channels/{no}", handlers.PatchChannel(d.DeviceAdmin, d.AuditRecorder))
				pr.With(op).Post("/devices/{addr}/accept", handlers.AcceptInboxDevice(d.DeviceAdmin))
				pr.With(op).Post("/devices/{addr}/firmware/update", handlers.UpdateDeviceFirmware(d.DeviceAdmin))
				pr.With(admin).Post("/devices/{addr}/config/restore", handlers.RestoreDeviceConfig(d.DeviceAdmin, d.AuditRecorder))
			}
			if d.DeviceReplacer != nil {
				pr.Get("/devices/{addr}/replace-candidates", handlers.GetDeviceReplaceCandidates(d.DeviceReplacer))
				pr.With(admin).Post("/devices/{addr}/replace", handlers.PostDeviceReplace(d.DeviceReplacer, d.AuditRecorder))
			}
			if d.DeviceCommunicationTest != nil {
				pr.With(op).Post("/devices/{addr}/test", handlers.TestDeviceCommunication(d.DeviceCommunicationTest, d.AuditRecorder))
			}
			if d.DeviceTeam != nil {
				pr.Get("/devices/{addr}/channels/{no}/team-candidates", handlers.GetDeviceTeamCandidates(d.DeviceTeam))
				pr.With(op).Put("/devices/{addr}/channels/{no}/team", handlers.SetDeviceChannelTeam(d.DeviceTeam, d.AuditRecorder))
			}
			if d.FirmwareRefresher != nil {
				pr.With(op).Post("/devices/firmware/refresh", handlers.RefreshFirmwareData(d.FirmwareRefresher))
			}
			if d.DeviceInstallMode != nil {
				pr.With(op).Post("/devices/{addr}/install-mode", handlers.PostDeviceInstallMode(d.DeviceInstallMode, d.AuditRecorder))
			}
			// Always mounted (admin): when TLS is not configured the
			// handler returns the documented 503 "TLS not enabled" via its
			// nil-service guard, rather than a 404 from an absent route.
			pr.With(admin).Post("/admin/tls/certificate", handlers.UploadTLSCertificate(d.TLSCert, d.AuditRecorder))
			// Room / function (Gewerk) entity CRUD. CCU writes, so
			// operator-gated; the read-only GET /rooms + /functions
			// stay open above.
			if d.RoomFunctionAdmin != nil {
				pr.With(op).Post("/rooms", handlers.CreateRoom(d.RoomFunctionAdmin, d.AuditRecorder))
				pr.With(op).Patch("/rooms/{name}", handlers.RenameRoom(d.RoomFunctionAdmin, d.AuditRecorder))
				pr.With(op).Delete("/rooms/{name}", handlers.DeleteRoom(d.RoomFunctionAdmin, d.AuditRecorder))
				pr.With(op).Post("/functions", handlers.CreateFunction(d.RoomFunctionAdmin, d.AuditRecorder))
				pr.With(op).Patch("/functions/{name}", handlers.RenameFunction(d.RoomFunctionAdmin, d.AuditRecorder))
				pr.With(op).Delete("/functions/{name}", handlers.DeleteFunction(d.RoomFunctionAdmin, d.AuditRecorder))
			}
			// Area (room grouping) entity CRUD + room-assignment replace.
			// Local SQLite writes, so operator-gated; the read-only GET
			// /areas stays open above.
			if d.Areas != nil {
				pr.With(op).Post("/areas", handlers.CreateArea(d.Areas, d.AuditRecorder))
				pr.With(op).Put("/areas/{id}", handlers.PutArea(d.Areas, d.AuditRecorder))
				pr.With(op).Delete("/areas/{id}", handlers.DeleteArea(d.Areas, d.AuditRecorder))
				pr.With(op).Put("/areas/{id}/rooms", handlers.PutAreaRooms(d.Areas, d.AuditRecorder))
			}
			if d.RefreshDevices != nil {
				pr.With(op).Post("/devices/refresh", handlers.RefreshDevices(d.RefreshDevices))
			}
			if d.Reloader != nil {
				pr.With(op).Post("/devices/{addr}/reload", handlers.ReloadDevice(d.Reloader))
				pr.With(op).Post("/devices/{addr}/channels/{no}/reload",
					handlers.ReloadChannel(d.Reloader))
			}
			if d.CentralLinks != nil {
				pr.Get("/devices/{addr}/central-links", handlers.GetCentralLinksStatus(d.CentralLinks))
				pr.With(op).Post("/devices/{addr}/central-links", handlers.CreateCentralLinks(d.CentralLinks))
				pr.With(op).Delete("/devices/{addr}/central-links", handlers.DeleteCentralLinks(d.CentralLinks))
			}
			if d.Incidents != nil {
				pr.Get("/incidents", handlers.ListIncidents(d.Incidents))
			}
			if d.IncidentsAdmin != nil {
				// Mirrors the WS `incidents.clear` role (auth.RoleOperator in
				// internal/north/rest/ws/commands.go's writeCommandRoles).
				pr.With(op).Delete("/incidents", handlers.DeleteIncidents(d.IncidentsAdmin, d.AuditRecorder))
			}
			if d.Alarm != nil {
				// Alarm-panel surface: reads authenticate, every mutation
				// gates on the operator role (mirrors the alarm_panel WS
				// command roles). Nil Alarm leaves the whole tree unmounted.
				pr.Get("/alarm/state", handlers.AlarmState(d.Alarm))
				pr.Get("/alarm/zones", handlers.ListAlarmZones(d.Alarm))
				pr.With(op).Post("/alarm/zones", handlers.CreateAlarmZone(d.Alarm, d.AuditRecorder))
				pr.Get("/alarm/zones/{id}", handlers.GetAlarmZone(d.Alarm))
				pr.With(op).Put("/alarm/zones/{id}", handlers.PutAlarmZone(d.Alarm, d.AuditRecorder))
				pr.With(op).Delete("/alarm/zones/{id}", handlers.DeleteAlarmZone(d.Alarm, d.AuditRecorder))
				pr.Get("/alarm/zones/{id}/sensors", handlers.ListAlarmZoneSensors(d.Alarm))
				pr.With(op).Put("/alarm/zones/{id}/sensors", handlers.PutAlarmZoneSensors(d.Alarm, d.AuditRecorder))
				pr.Get("/alarm/zones/{id}/outputs", handlers.ListAlarmZoneOutputs(d.Alarm))
				pr.With(op).Put("/alarm/zones/{id}/outputs", handlers.PutAlarmZoneOutputs(d.Alarm, d.AuditRecorder))
				pr.With(op).Post("/alarm/zones/{id}/arm", handlers.ArmAlarmZone(d.Alarm, d.AuditRecorder))
				pr.With(op).Post("/alarm/zones/{id}/disarm", handlers.DisarmAlarmZone(d.Alarm, d.AuditRecorder))
				pr.With(op).Post("/alarm/zones/{id}/silence", handlers.SilenceAlarmZone(d.Alarm, d.AuditRecorder))
				pr.With(op).Post("/alarm/zones/{id}/acknowledge", handlers.AcknowledgeAlarmZone(d.Alarm, d.AuditRecorder))
				pr.With(op).Post("/alarm/silence-all", handlers.SilenceAllAlarmZones(d.Alarm, d.AuditRecorder))
				pr.Get("/alarm/zones/{id}/readiness", handlers.GetAlarmZoneReadiness(d.Alarm))
				pr.Get("/alarm/journal", handlers.ListAlarmJournal(d.Alarm))
				pr.Get("/alarm/panels", handlers.ListAlarmPanels(d.Alarm))
				pr.With(op).Post("/alarm/zones/{id}/walktest/start", handlers.StartAlarmWalkTest(d.Alarm, d.AuditRecorder))
				pr.With(op).Post("/alarm/zones/{id}/walktest/stop", handlers.StopAlarmWalkTest(d.Alarm, d.AuditRecorder))
				pr.Get("/alarm/zones/{id}/walktest", handlers.GetAlarmWalkTestStatus(d.Alarm))
				pr.Get("/alarm/output-candidates", handlers.ListAlarmOutputCandidates(d.Alarm, d.Labels))
				pr.Get("/alarm/remote-key-candidates", handlers.ListAlarmRemoteKeyCandidates(d.Alarm))
				pr.With(op).Post("/alarm/outputs/{id}/test", handlers.TestAlarmOutput(d.Alarm, d.AuditRecorder))
				// Alarm codes: reads and writes both require the operator
				// role — codes are security material, so even the list is
				// not viewer-open (docs/alarm-concept.md §11/§16). A nil
				// AlarmCodes serves these as 503.
				pr.With(op).Get("/alarm/codes", handlers.ListAlarmCodes(d.AlarmCodes))
				pr.With(op).Post("/alarm/codes", handlers.CreateAlarmCode(d.AlarmCodes, d.AuditRecorder))
				pr.With(op).Get("/alarm/codes/{id}", handlers.GetAlarmCode(d.AlarmCodes))
				pr.With(op).Put("/alarm/codes/{id}", handlers.PutAlarmCode(d.AlarmCodes, d.AuditRecorder))
				pr.With(op).Delete("/alarm/codes/{id}", handlers.DeleteAlarmCode(d.AlarmCodes, d.AuditRecorder))
			}
			if d.SystemStatus != nil {
				pr.Get("/system/status", handlers.ListSystemStatus(d.SystemStatus))
			}
			pr.Get("/system/ccu", handlers.SystemCCU(d.SystemCCU))
			// Reboot one CCU host. Admin-gated like DELETE /devices; the
			// handler serves 503 when d.CCUReboot is nil (bridge unwired).
			pr.With(admin).Post("/system/ccu/{central}/reboot", handlers.PostCCUReboot(d.CCUReboot, d.AuditRecorder))
			// Astro reference position. Admin-gated: it moves every
			// sunrise/sunset time the CCU computes, for its own programs
			// as well as the weekly profiles this daemon edits.
			pr.With(admin).Put("/system/ccu/{central}/position", handlers.PutCCUPosition(d.CCUPosition, d.AuditRecorder))
			// Host power and boot mode. All three take the CCU out of
			// normal service, so they sit next to the reboot: admin-gated,
			// audited, and confirmed in the UI before dispatch.
			pr.With(admin).Post("/system/ccu/{central}/poweroff", handlers.PostCCUPoweroff(d.CCUHostActions, d.AuditRecorder))
			pr.With(admin).Post("/system/ccu/{central}/safe-mode", handlers.PostCCUSafeMode(d.CCUHostActions, d.AuditRecorder))
			pr.With(admin).Post("/system/ccu/{central}/recovery-mode", handlers.PostCCURecoveryMode(d.CCUHostActions, d.AuditRecorder))
			pr.With(admin).Post("/system/firmware/download", handlers.PostSystemFirmwareDownload(d.FirmwareDownload, d.AuditRecorder))
			// Add-on self-update (ADR 0057). GET always answers 200 (a nil
			// d.AddonUpdate reports supported:false) — mounted unconditionally
			// so the SPA can distinguish "unsupported platform" from "old
			// daemon / misrouted proxy" without a 404. The check/install
			// verbs answer 404 themselves when unsupported.
			pr.Get("/system/addon-update", handlers.GetAddonUpdate(d.AddonUpdate))
			pr.With(op).Post("/system/addon-update/check", handlers.PostAddonUpdateCheck(d.AddonUpdate))
			pr.With(op).Post("/system/addon-update/install", handlers.PostAddonUpdateInstall(d.AddonUpdate, d.AuditRecorder))
			// Read-only heating-group listing (one entry per central).
			pr.Get("/groups", handlers.ListGroups(d.Groups))
			// Heating-group administration (GR02) via the CCU jpages proxy.
			// The type / suitable-member helpers are reads; create / edit /
			// delete are admin-gated and audited.
			pr.Get("/groups/types", handlers.ListGroupTypes(d.GroupsWriter))
			pr.Get("/groups/suitable-members", handlers.ListSuitableMembers(d.GroupsWriter))
			pr.With(admin).Post("/groups", handlers.CreateGroup(d.GroupsWriter, d.AuditRecorder))
			pr.With(admin).Put("/groups/{id}", handlers.UpdateGroup(d.GroupsWriter, d.AuditRecorder))
			pr.With(admin).Delete("/groups/{id}", handlers.DeleteGroup(d.GroupsWriter, d.AuditRecorder))
			// Persistent "restart required" status for the SPA banner.
			pr.Get("/system/restart-pending", handlers.GetRestartPending(d.RestartPending))
			// Config fields changed since the daemon started.
			pr.Get("/system/config-changes", handlers.GetConfigChanges(d.ConfigChanges))
			if d.LogLevels != nil {
				pr.Get("/diagnostics/log-levels", handlers.ListLogLevels(d.LogLevels))
				pr.With(admin).Put("/diagnostics/log-levels/{path}", handlers.PutLogLevel(d.LogLevels, d.AuditRecorder))
				pr.With(admin).Delete("/diagnostics/log-levels/{path}", handlers.DeleteLogLevel(d.LogLevels, d.AuditRecorder))
			}
			if d.LogDefaultLevel != nil {
				pr.Get("/diagnostics/log-level", handlers.GetDefaultLogLevel(d.LogDefaultLevel))
				pr.With(admin).Put("/diagnostics/log-level", handlers.PutDefaultLogLevel(d.LogDefaultLevel, d.AuditRecorder))
			}
			if d.LogFeed != nil {
				pr.With(admin).Get("/diagnostics/logs", handlers.ListLogs(d.LogFeed))
				pr.With(admin).Get("/diagnostics/logs/stream", handlers.StreamLogs(d.LogFeed))
			}
			if d.Introspect != nil {
				pr.With(admin).Get("/diagnostics/reliability", handlers.DiagnosticsReliability(d.Introspect))
				pr.With(admin).Get("/diagnostics/eventbus/tap", handlers.DiagnosticsEventBusTap(d.Introspect))
			}
			if d.RSSIInfo != nil {
				pr.With(admin).Get("/diagnostics/rssi", handlers.DiagnosticsRSSI(d.RSSIInfo))
			}
			// Composite diagnostics dump — single artefact for support /
			// agent escalation. Anonymises by default; pass
			// ?anonymize=0 explicitly for raw output.
			diagDeps := handlers.DiagnosticsDeps{
				Health:        d.Health,
				HealthExt:     d.HealthExtras,
				Interfaces:    d.Interfaces,
				Incidents:     d.Incidents,
				SystemStatus:  d.SystemStatus,
				LogLevels:     d.LogLevels,
				KnownCentrals: d.KnownCentrals,
				HealthGauges:  d.HealthGauges,
			}
			pr.With(admin).Get("/diagnostics", handlers.Diagnostics(diagDeps))
			if d.StartupCapture != nil {
				pr.With(admin).Get("/system/startup-capture", handlers.GetStartupCapture(d.StartupCapture))
				pr.With(admin).Put("/system/startup-capture", handlers.PutStartupCapture(d.StartupCapture, d.AuditRecorder))
			}
			if d.EnableRestartEndpoint {
				pr.With(admin).Post("/system/restart", handlers.Restart(d.AuditRecorder))
			}
			if d.Capture != nil {
				pr.With(admin).Post("/diagnostics/capture", handlers.StartCapture(d.Capture, d.AuditRecorder))
				pr.With(admin).Get("/diagnostics/capture", handlers.ListCaptures(d.Capture))
				pr.With(admin).Get("/diagnostics/capture/{id}", handlers.GetCapture(d.Capture))
				pr.With(admin).Post("/diagnostics/capture/{id}/stop", handlers.StopCapture(d.Capture, d.AuditRecorder))
				pr.With(admin).Get("/diagnostics/capture/{id}/download", handlers.DownloadCapture(d.Capture))
			}
			if d.RPCRecorder != nil {
				pr.With(admin).Post("/diagnostics/rpc-recording", handlers.StartRPCRecording(d.RPCRecorder, d.AuditRecorder))
				pr.With(admin).Post("/diagnostics/rpc-recording/stop", handlers.StopRPCRecording(d.RPCRecorder, d.AuditRecorder))
				pr.Get("/diagnostics/rpc-recording", handlers.ListRPCRecordings(d.RPCRecorder))
				pr.With(admin).Get("/diagnostics/rpc-recording/{central}/download", handlers.DownloadRPCRecording(d.RPCRecorder))
			}
			if d.Metrics != nil {
				pr.Get("/metrics", handlers.MetricsHandler(d.Metrics))
			}
			if d.ValuesCache != nil {
				pr.With(admin).Get("/admin/values-cache/stats", handlers.GetValuesCacheStats(d.ValuesCache))
				pr.With(admin).Post("/admin/values-cache/reset", handlers.ResetValuesCacheGlobal(d.ValuesCache))
				if d.DeviceLookup != nil {
					pr.With(admin).Post("/devices/{addr}/values-cache/reset", handlers.ResetValuesCacheDevice(d.ValuesCache, d.DeviceLookup))
				}
			}

			// Matter endpoints — each sub-route returns 503 service_unready
			// when its dependency is nil, so the bridge being disabled
			// surfaces cleanly rather than as 404. Mutating routes
			// (PUT / POST / DELETE) gate on admin role.
			pr.Get("/matter/status", handlers.MatterStatus(d.MatterStatusReader))
			pr.Get("/matter/fabrics", handlers.MatterFabrics(d.MatterFabricStore))
			pr.With(admin).Delete("/matter/fabrics/{id}", handlers.MatterFabricRevoke(d.MatterFabricRevoker, d.MatterEventPublisher, d.MatterAuditRecorder))
			pr.Get("/matter/setup-payload", handlers.MatterSetupPayload(d.MatterCommissioning))
			pr.Get("/matter/exposable", handlers.MatterExposable(d.MatterCandidateProvider, d.MatterExposureStore, d.Labels))
			pr.With(admin).Put("/matter/exposable", handlers.MatterExposeUpdate(d.MatterExposureStore, d.MatterEventPublisher, d.MatterAuditRecorder, d.MatterTopologyReassembler))
			pr.With(admin).Post("/matter/exposable/bulk", handlers.MatterExposeBulk(d.MatterExposureStore, d.MatterEventPublisher, d.MatterAuditRecorder, d.MatterTopologyReassembler))
			pr.With(admin).Post("/matter/commissioning/window", handlers.MatterCommissioningWindow(d.MatterCommissioningOpener, d.MatterEventPublisher))
			pr.With(admin).Post("/matter/commissioning/window/close", handlers.MatterCommissioningClose(d.MatterCommissioningCloser, d.MatterEventPublisher, d.MatterAuditRecorder))
			pr.With(admin).Post("/matter/share", handlers.MatterShare(d.MatterCommissioningOpener, d.MatterEventPublisher))

			// Visibility / un-ignore endpoints — power-user surface that
			// promotes otherwise-hidden parameters to first-class data
			// points. See docs/ui/unignore-concept.md.
			pr.Get("/visibility/unignore", handlers.ListVisibilityUnIgnore(d.VisibilityCentralLister, d.VisibilityUnIgnoreStore))
			pr.With(admin).Put("/visibility/unignore", handlers.UpdateVisibilityUnIgnore(d.VisibilityUnIgnoreStore, d.VisibilityRegistryLoader, d.MatterAuditRecorder))
			pr.Get("/visibility/unignore/candidates", handlers.ListVisibilityUnIgnoreCandidates(d.VisibilityCentralLister, d.VisibilityCandidateProvider))
			if d.Backup != nil {
				pr.With(admin).Post("/backups", handlers.TriggerBackup(d.Backup))
				pr.Get("/backups", handlers.ListBackups(d.Backup))
				// Importing an operator-supplied archive. Admin-gated like
				// the trigger: what is imported here can later overwrite a
				// CCU's entire configuration.
				pr.With(admin).Post("/backups/upload", handlers.UploadBackup(d.BackupUpload, d.AuditRecorder))
				pr.With(admin).Get("/backups/{id}/download", handlers.DownloadBackup(d.Backup))
				pr.With(admin).Post("/backups/{id}/restore", handlers.RestoreBackup(d.Backup))
			}
			if d.Paramsets != nil {
				pr.Get("/devices/{addr}/paramsets/{key}", handlers.GetParamset(d.Paramsets))
				pr.With(op).Put("/devices/{addr}/paramsets/{key}", handlers.PutParamset(d.Paramsets, d.EditSessions))
				pr.Get("/devices/{addr}/link-ps/{peer}", handlers.GetLinkParamset(d.Paramsets))
				pr.With(op).Put("/devices/{addr}/link-ps/{peer}", handlers.PutLinkParamset(d.Paramsets, d.EditSessions))
			}
			if d.Hub != nil {
				pr.Get("/programs", handlers.ListPrograms(d.Hub))
				pr.Get("/programs/{id}", handlers.GetProgram(d.Hub))
				pr.With(op).Post("/programs/{id}/execute", handlers.ExecuteProgram(d.Hub))
				pr.With(op).Patch("/programs/{id}", handlers.SetProgramEnabled(d.Hub))
				pr.With(admin).Delete("/programs/{id}", handlers.DeleteProgram(d.Hub, d.AuditRecorder))
				pr.Get("/sysvars", handlers.ListSysvars(d.Hub))
				pr.With(op).Post("/sysvars", handlers.CreateSysvar(d.Hub))
				pr.Get("/sysvars/{name}", handlers.GetSysvar(d.Hub))
				pr.Get("/sysvars/{name}/usage", handlers.GetSysvarUsage(d.Hub))
				pr.With(op).Put("/sysvars/{name}", handlers.PutSysvar(d.Hub))
				pr.With(op).Patch("/sysvars/{name}", handlers.PatchSysvar(d.Hub))
				pr.With(op).Delete("/sysvars/{name}", handlers.DeleteSysvar(d.Hub))
				if d.SysvarRefresh != nil {
					// Force re-pull the sysvar catalogue from the CCU.
					pr.With(op).Post("/sysvars/fetch", handlers.FetchSysvars(d.SysvarRefresh))
				}
				pr.Get("/inbox", handlers.ListInbox(d.Hub))
				// Aggregated hub-singleton snapshot so a client hub coordinator
				// can be built from a single fetch (alarm/service messages,
				// inbox, update, metrics, connectivity, install-mode).
				pr.Get("/hub/data-points", handlers.GetHubDataPoints(d.Hub))
				pr.Get("/alarm-messages", handlers.ListAlarmMessages(d.Hub))
				pr.With(op).Post("/alarm-messages/ack-all", handlers.AckAllAlarmMessages(d.Hub))
				pr.With(op).Post("/alarm-messages/{id}/ack", handlers.AckAlarmMessage(d.Hub))
				pr.Get("/service-messages", handlers.ListServiceMessages(d.Hub))
				pr.Get("/service-messages/suppressed", handlers.ListSuppressedServiceMessages(d.Hub))
				pr.With(op).Post("/service-messages/ack-all", handlers.AckAllServiceMessages(d.Hub))
				pr.With(op).Post("/service-messages/unsuppress", handlers.UnsuppressServiceMessage(d.Hub))
				pr.With(op).Post("/service-messages/{id}/ack", handlers.AckServiceMessage(d.Hub))
				pr.With(op).Post("/service-messages/{id}/disable", handlers.DisableServiceMessage(d.Hub))
			}
			if d.Hub != nil {
				// Hub singletons for external clients: system-update info,
				// hub metrics, per-interface install mode.
				pr.Get("/system/update", handlers.GetSystemUpdate(d.Hub))
				pr.With(admin).Post("/system/update/install", handlers.PostSystemUpdateInstall(d.Hub, d.PreUpdateBackup, d.AuditRecorder))
				pr.Get("/system/metrics", handlers.GetHubMetrics(d.Hub))
				pr.Get("/install-mode/interfaces", handlers.GetInstallModeInterfaces(d.Hub))
				pr.With(op).Post("/install-mode/interfaces", handlers.PostInstallModeInterface(d.Hub, d.AuditRecorder))
				pr.With(op).Post("/install-mode/search", handlers.PostInstallModeSearch(d.InstallModeSearch, d.AuditRecorder))
			}
			if d.Interfaces != nil {
				pr.Get("/interfaces", handlers.ListInterfaces(d.Interfaces))
				pr.Get("/interfaces/{id}", handlers.GetInterface(d.Interfaces))
				pr.With(admin).Post("/interfaces/{id}/reconnect", handlers.ReconnectInterface(d.Interfaces))
			}
			pr.Get("/snapshot", handlers.Snapshot(handlers.SnapshotDeps{
				Devices:    d.Devices,
				Hub:        d.Hub,
				Interfaces: d.Interfaces,
				Labels:     d.Labels,
			}))

			// Live-edit config endpoints. ConfigAdmin / UserAdmin /
			// TokenAdmin / CentralAdmin gate independently — the SPA
			// disables individual editors when the matching service
			// is nil (the schema endpoint is always available so the
			// Settings page can render at least the read-only view).
			pr.Get("/config/schema", handlers.GetConfigSchema())
			if d.ConfigAdmin != nil {
				pr.Get("/config/effective", handlers.GetEffectiveConfig(d.ConfigAdmin))
				pr.Get("/config/sections/{section}", handlers.GetConfigSection(d.ConfigAdmin))
				pr.With(admin).Put("/config/sections/{section}", handlers.PutConfigSection(d.ConfigAdmin, d.AuditRecorder))
				pr.With(admin).Delete("/config/sections/{section}", handlers.DeleteConfigSection(d.ConfigAdmin, d.AuditRecorder))
				// Per-field reset: revert a single config field to its default.
				pr.With(admin).Delete("/config/fields/{path}", handlers.ResetConfigField(d.ConfigAdmin, d.AuditRecorder))
			}
			if d.UserAdmin != nil {
				pr.With(admin).Get("/users", handlers.ListUsersV2(d.UserAdmin))
				pr.With(admin).Post("/users", handlers.CreateUser(d.UserAdmin, d.AuditRecorder))
				pr.With(admin).Patch("/users/{subject}", handlers.UpdateUser(d.UserAdmin, d.AuditRecorder, d.SessionRevoker))
				pr.With(admin).Delete("/users/{subject}", handlers.DeleteUser(d.UserAdmin, d.AuditRecorder, d.SessionRevoker, d.TokenPurger))
			}
			if d.TokenAdmin != nil {
				pr.With(admin).Get("/auth/tokens/v2", handlers.ListTokensV2(d.TokenAdmin))
				pr.With(admin).Post("/auth/tokens/v2", handlers.CreateTokenAdmin(d.TokenAdmin, d.AuditRecorder))
				pr.With(admin).Delete("/auth/tokens/v2/{fingerprint}", handlers.DeleteTokenAdmin(d.TokenAdmin, d.AuditRecorder))
			}
			if d.CentralAdmin != nil {
				pr.Get("/centrals", handlers.ListCentrals(d.CentralAdmin))
				// Discovery routes register the static `discovered` segment
				// before the `{name}` param so chi resolves them unambiguously.
				if d.Discovery != nil {
					pr.Get("/centrals/discovered", handlers.ListDiscoveredCCUs(d.Discovery))
					pr.Get("/centrals/discovered/ignored", handlers.ListIgnoredCCUs(d.Discovery))
					pr.With(admin).Post("/centrals/discovered/{serial}/ignore", handlers.IgnoreDiscoveredCCU(d.Discovery))
					pr.With(admin).Delete("/centrals/discovered/{serial}/ignore", handlers.UnignoreDiscoveredCCU(d.Discovery))
				}
				pr.Get("/centrals/{name}", handlers.GetCentral(d.CentralAdmin))
				pr.With(admin).Post("/centrals", handlers.CreateCentral(d.CentralAdmin, d.AuditRecorder))
				pr.With(admin).Put("/centrals/{name}", handlers.UpdateCentral(d.CentralAdmin, d.AuditRecorder))
				pr.With(admin).Delete("/centrals/{name}", handlers.DeleteCentral(d.CentralAdmin, d.AuditRecorder))
			}
			if d.MQTTReload != nil {
				pr.With(admin).Post("/admin/mqtt/reload", handlers.MQTTReload(d.MQTTReload, d.AuditRecorder))
			}
			if d.CacheReset != nil {
				pr.With(admin).Post("/admin/cache/clear", handlers.ClearCache(d.CacheReset))
			}
			if d.EditSessions != nil {
				pr.With(op).Post("/sessions/edit", handlers.OpenEditSession(d.EditSessions))
				pr.With(op).Post("/sessions/edit/heartbeat", handlers.HeartbeatEditSession(d.EditSessions))
				pr.With(op).Post("/sessions/edit/take-over", handlers.ForceCloseEditSession(d.EditSessions))
				pr.With(op).Delete("/sessions/edit", handlers.CloseEditSession(d.EditSessions))
			}
			if d.WSHandler != nil {
				pr.Handle("/events", d.WSHandler)
			}
		})
	})
	return r
}

// safeIngressPrefix returns the Home Assistant Ingress proxy prefix from the
// X-Ingress-Path header (e.g. "/api/hassio_ingress/<token>"), or "" when the
// header is absent or not a local absolute path. It rejects scheme-relative
// ("//host") and backslash ("/\") forms so the value can be used to build a
// redirect target without becoming an open redirect to a foreign origin.
func safeIngressPrefix(r *http.Request) string {
	p := r.Header.Get("X-Ingress-Path")
	if p == "" || !strings.HasPrefix(p, "/") {
		return ""
	}
	if strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return ""
	}
	return strings.TrimRight(p, "/")
}

// AssertAuthWired reports an error when d has no auth enforcement wired —
// AuthRequire is the blanket gate every protected route depends on, and a
// nil value would leave the router's role shims falling through to an
// open pass-through. The composition root calls this before serving so a
// mis-wired production build fails fast instead of silently exposing write
// routes to anonymous callers (the router's own pass-through fallback
// exists only for auth-less unit tests).
func (d Deps) AssertAuthWired() error {
	if d.AuthRequire == nil {
		return errors.New("rest: AuthRequire middleware is not wired — refusing to serve with no auth gate")
	}
	return nil
}
