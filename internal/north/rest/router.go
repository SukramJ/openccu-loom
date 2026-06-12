// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// Deps bundles every collaborator the REST router needs.
type Deps struct {
	Logger      *slog.Logger
	StartedAt   time.Time
	Health      handlers.HealthReader
	Config      handlers.ConfigReader
	Devices     handlers.DeviceIndex
	DeviceAdmin handlers.DeviceAdmin
	// CustomDPWriter drives POST .../cdps/{name}/{operation}.
	// Nil disables the mutating endpoint (list/get remain available).
	CustomDPWriter handlers.CustomDPWriter
	// RefreshDevices triggers a fresh ListDevices sweep across every
	// backend on demand. Optional — nil disables `POST /devices/refresh`.
	RefreshDevices handlers.RefreshDevicesService
	DPWriter       handlers.DataPointWriter
	Paramsets      handlers.ParamsetService
	// DataPointVis is the outbound visibility filter for the
	// GET .../data-points endpoint. Nil means "expose everything"
	// (backward-compatible with un-wired call sites). See ADR 0005.
	DataPointVis filter.VisibilitySet
	Hub          handlers.HubIndex
	InstallMode  handlers.InstallModeController
	Interfaces   handlers.InterfaceIndex
	Incidents    handlers.IncidentsReader
	Labels       handlers.ParameterLabeler
	Metrics      *metrics.Registry
	// UISchema produces the data-driven rendering descriptor the SPA
	// consumes at /api/v1/devices/{addr}/channels/{no}/ui-schema.
	UISchema handlers.UISchemaService
	// Links backs the direct-link (Direktverknüpfung) endpoints:
	//   GET    /api/v1/devices/{addr}/links
	//   POST   /api/v1/devices/{addr}/links
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
	// Auth exposes login/logout/me endpoints at /api/v1/auth so the
	// SPA can authenticate without the HTMX pages. Nil disables the
	// endpoints (they 503 on request).
	Auth *handlers.AuthDeps

	// ConfigAdmin backs the live-edit config endpoints
	// (`GET /config/schema`, `GET|PUT|DELETE /config/{section}`).
	// Nil disables all of them with 503.
	ConfigAdmin handlers.ConfigAdminService

	// UserAdmin backs the SQLite-backed `/users` CRUD. Nil keeps
	// the legacy in-memory `/auth/users` read-only path active.
	UserAdmin handlers.UserAdminService

	// TokenAdmin backs the SQLite-backed `/auth/tokens` CRUD.
	TokenAdmin handlers.TokenAdminService

	// CentralAdmin backs the SQLite-backed `/centrals` CRUD.
	CentralAdmin handlers.CentralAdminService

	// MQTTReload backs POST /admin/mqtt/reload. Nil disables the
	// route — operators on builds without the supervisor (tests,
	// MQTT-disabled deployments) get a clean 404 rather than a 500.
	MQTTReload handlers.MQTTReloadService
	// OIDC mounts /api/v1/auth/oidc/{start,callback} when configured.
	// The flow drops the same session cookie as Login(), so the rest
	// of the SPA needs no further wiring. Nil = OIDC disabled.
	OIDC *handlers.OIDCDeps
	// SPAHandler serves the Svelte SPA under /app/*. Same-origin with
	// /api/v1/* is essential — the SPA relies on session cookies, and
	// cross-origin requests would need CORS gymnastics. Mounting here
	// keeps the auth boundary simple.
	SPAHandler http.Handler
	Backup     handlers.BackupService
	// CentralLinks toggles CCU click-event forwarding for press-event
	// channels. Nil disables the endpoint trio
	// (`GET|POST|DELETE /api/v1/devices/{addr}/central-links`).
	CentralLinks handlers.CentralLinksService
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
	// RateLimit, when non-nil, installs the per-identity REST
	// rate limiter before the auth-require gate. Nil disables it.
	RateLimit *middleware.RateLimitConfig
	// RequireOperator wraps mutations that an operator-grade user is
	// allowed to perform (paramset writes, link CRUD, schedule edits,
	// sysvar writes). Nil falls back to AuthRequire — i.e. any
	// authenticated user is allowed (current behaviour).
	RequireOperator func(http.Handler) http.Handler
	// RequireAdmin gates dangerous operations (delete device, install
	// mode, backup trigger, interface reconnect). Nil → AuthRequire.
	RequireAdmin func(http.Handler) http.Handler
	CORS         *middleware.CORSConfig
	Idempotent   bool
	ReadTimeout  time.Duration
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
	if d.Idempotent {
		r.Use(middleware.Idempotency())
	}
	if d.AuthResolve != nil {
		r.Use(d.AuthResolve)
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
			target := "/app/"
			if prefix := safeIngressPrefix(req); prefix != "" {
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

		// Auth endpoints stay outside the AuthRequire group — a logged-
		// out SPA must be able to POST credentials to /auth/login.
		// `/auth/me` is the probe the SPA uses on startup to decide
		// whether to show the login page; it returns 401 when there
		// is no active session.
		if d.Auth != nil {
			r.Post("/auth/login", handlers.Login(d.Auth))
			r.Post("/auth/logout", handlers.Logout(d.Auth))
			r.Get("/auth/me", handlers.Me())
		}
		if d.OIDC != nil {
			r.Get("/auth/oidc/start", handlers.OIDCStart(d.OIDC))
			r.Get("/auth/oidc/callback", handlers.OIDCCallback(d.OIDC))
		}

		// Permission shorthands. When a role middleware isn't wired
		// (legacy single-tier setups), all authenticated users pass —
		// matches the previous behaviour so upgrades don't lock
		// anyone out.
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
			if d.Devices != nil {
				pr.Post("/devices/values:batch", handlers.ValuesBatch(d.Devices, d.Labels, d.DataPointVis))
				pr.Get("/rooms", handlers.ListRooms(d.Devices))
				pr.Get("/functions", handlers.ListFunctions(d.Devices))
				pr.Get("/devices", handlers.ListDevices(d.Devices))
				pr.Get("/devices/{addr}", handlers.GetDevice(d.Devices, d.Labels))
				pr.Get("/devices/{addr}/channels", handlers.ListChannels(d.Devices, d.Labels))
				pr.Get("/devices/{addr}/channels/{no}", handlers.GetChannel(d.Devices, d.Labels))
				pr.Get("/devices/{addr}/channels/{no}/data-points", handlers.ListDataPoints(d.Devices, d.Labels, d.DataPointVis))
				pr.Get("/devices/{addr}/channels/{no}/data-points/{param}", handlers.GetDataPoint(d.Devices, d.Labels))
				pr.With(op).Put("/devices/{addr}/channels/{no}/data-points/{param}/value",
					handlers.PutDataPointValue(d.Devices, d.DPWriter))
				// Custom data points (Phase C).
				pr.Get("/devices/{addr}/cdps",
					handlers.ListCustomDataPoints(d.Devices))
				pr.Get("/devices/{addr}/cdps/{name}",
					handlers.GetCustomDataPoint(d.Devices))
				pr.With(op).Post("/devices/{addr}/cdps/{name}/{operation}",
					handlers.InvokeCustomDataPoint(d.Devices, d.CustomDPWriter))
				// Calculated data points (Phase C).
				pr.Get("/devices/{addr}/channels/{no}/calc-dps",
					handlers.ListCalculatedDataPoints(d.Devices))
				pr.Get("/devices/{addr}/channels/{no}/calc-dps/{name}",
					handlers.GetCalculatedDataPoint(d.Devices))
				// Week-profile metadata (read-only; full schedule data via schedule routes).
				pr.Get("/devices/{addr}/channels/{no}/week_profile",
					handlers.GetWeekProfile(d.Devices))
				// Write half of the schedule_enabled map: toggle one target
				// channel's week-program participation.
				pr.With(op).Put("/devices/{addr}/channels/{no}/week_profile/channel-locks/{key}",
					handlers.PutWeekProfileChannelLock(d.Devices))
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
			if d.Links != nil {
				pr.Get("/devices/{addr}/links", handlers.ListLinks(d.Links))
				pr.With(op).Post("/devices/{addr}/links", handlers.AddLink(d.Links))
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
			}
			if d.Audit != nil {
				pr.Get("/audit", handlers.ListAudit(d.Audit, d.Devices))
			}
			if d.DeviceAdmin != nil {
				pr.With(admin).Delete("/devices/{addr}", handlers.DeleteDevice(d.DeviceAdmin))
				pr.With(op).Patch("/devices/{addr}", handlers.PatchDevice(d.DeviceAdmin))
				pr.With(op).Post("/devices/{addr}/accept", handlers.AcceptInboxDevice(d.DeviceAdmin))
				pr.With(op).Post("/devices/{addr}/firmware/update", handlers.UpdateDeviceFirmware(d.DeviceAdmin))
			}
			if d.RefreshDevices != nil {
				pr.With(op).Post("/devices/refresh", handlers.RefreshDevices(d.RefreshDevices))
			}
			if d.CentralLinks != nil {
				pr.Get("/devices/{addr}/central-links", handlers.GetCentralLinksStatus(d.CentralLinks))
				pr.With(op).Post("/devices/{addr}/central-links", handlers.CreateCentralLinks(d.CentralLinks))
				pr.With(op).Delete("/devices/{addr}/central-links", handlers.DeleteCentralLinks(d.CentralLinks))
			}
			if d.Incidents != nil {
				pr.Get("/incidents", handlers.ListIncidents(d.Incidents))
			}
			if d.SystemStatus != nil {
				pr.Get("/system/status", handlers.ListSystemStatus(d.SystemStatus))
			}
			pr.Get("/system/ccu", handlers.SystemCCU(d.SystemCCU))
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
				pr.With(admin).Get("/backups/{id}/download", handlers.DownloadBackup(d.Backup))
				pr.With(admin).Post("/backups/{id}/restore", handlers.RestoreBackup(d.Backup))
			}
			if d.Paramsets != nil {
				pr.Get("/devices/{addr}/paramsets/{key}", handlers.GetParamset(d.Paramsets))
				pr.With(op).Put("/devices/{addr}/paramsets/{key}", handlers.PutParamset(d.Paramsets))
				pr.Get("/devices/{addr}/link-ps/{peer}", handlers.GetLinkParamset(d.Paramsets))
				pr.With(op).Put("/devices/{addr}/link-ps/{peer}", handlers.PutLinkParamset(d.Paramsets))
			}
			if d.Hub != nil {
				pr.Get("/programs", handlers.ListPrograms(d.Hub))
				pr.With(op).Post("/programs/{id}/execute", handlers.ExecuteProgram(d.Hub))
				pr.With(op).Patch("/programs/{id}", handlers.SetProgramEnabled(d.Hub))
				pr.Get("/sysvars", handlers.ListSysvars(d.Hub))
				pr.With(op).Post("/sysvars", handlers.CreateSysvar(d.Hub))
				pr.Get("/sysvars/{name}", handlers.GetSysvar(d.Hub))
				pr.With(op).Put("/sysvars/{name}", handlers.PutSysvar(d.Hub))
				pr.With(op).Patch("/sysvars/{name}", handlers.PatchSysvar(d.Hub))
				pr.With(op).Delete("/sysvars/{name}", handlers.DeleteSysvar(d.Hub))
				pr.Get("/inbox", handlers.ListInbox(d.Hub))
				pr.Get("/alarm-messages", handlers.ListAlarmMessages(d.Hub))
				pr.With(op).Post("/alarm-messages/{id}/ack", handlers.AckAlarmMessage(d.Hub))
				pr.Get("/service-messages", handlers.ListServiceMessages(d.Hub))
				pr.With(op).Post("/service-messages/{id}/ack", handlers.AckServiceMessage(d.Hub))
			}
			if d.Hub != nil {
				// Hub singletons for external clients: system-update info,
				// hub metrics, per-interface install mode.
				pr.Get("/system/update", handlers.GetSystemUpdate(d.Hub))
				pr.With(admin).Post("/system/update/install", handlers.PostSystemUpdateInstall(d.Hub))
				pr.Get("/system/metrics", handlers.GetHubMetrics(d.Hub))
				pr.Get("/install-mode/interfaces", handlers.GetInstallModeInterfaces(d.Hub))
				pr.With(op).Post("/install-mode/interfaces", handlers.PostInstallModeInterface(d.Hub))
			}
			if d.InstallMode != nil {
				pr.Get("/install-mode", handlers.GetInstallMode(d.InstallMode))
				pr.With(admin).Post("/install-mode", handlers.PostInstallMode(d.InstallMode))
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
			}
			if d.UserAdmin != nil {
				pr.With(admin).Get("/users", handlers.ListUsersV2(d.UserAdmin))
				pr.With(admin).Post("/users", handlers.CreateUser(d.UserAdmin, d.AuditRecorder))
				pr.With(admin).Patch("/users/{subject}", handlers.UpdateUser(d.UserAdmin, d.AuditRecorder))
				pr.With(admin).Delete("/users/{subject}", handlers.DeleteUser(d.UserAdmin, d.AuditRecorder))
			}
			if d.TokenAdmin != nil {
				pr.With(admin).Get("/auth/tokens/v2", handlers.ListTokensV2(d.TokenAdmin))
				pr.With(admin).Post("/auth/tokens/v2", handlers.CreateTokenAdmin(d.TokenAdmin, d.AuditRecorder))
				pr.With(admin).Delete("/auth/tokens/v2/{fingerprint}", handlers.DeleteTokenAdmin(d.TokenAdmin, d.AuditRecorder))
			}
			if d.CentralAdmin != nil {
				pr.Get("/centrals", handlers.ListCentrals(d.CentralAdmin))
				pr.Get("/centrals/{name}", handlers.GetCentral(d.CentralAdmin))
				pr.With(admin).Post("/centrals", handlers.CreateCentral(d.CentralAdmin, d.AuditRecorder))
				pr.With(admin).Put("/centrals/{name}", handlers.UpdateCentral(d.CentralAdmin, d.AuditRecorder))
				pr.With(admin).Delete("/centrals/{name}", handlers.DeleteCentral(d.CentralAdmin, d.AuditRecorder))
			}
			if d.MQTTReload != nil {
				pr.With(admin).Post("/admin/mqtt/reload", handlers.MQTTReload(d.MQTTReload, d.AuditRecorder))
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
