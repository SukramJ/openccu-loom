// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/diagnostics"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
	"github.com/SukramJ/openccu-loom/internal/north/ui"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// restMountDeps bundles every live subsystem the REST router needs. The fields
// mirror the variables the composition root builds before the REST phase; the
// helper assembles them into a [rest.Deps] and mounts the server. Splitting the
// REST phase out keeps the composition root's statement count under the funlen
// budget.
type restMountDeps struct {
	reg    *central.Registry
	matter matterWiring
	reload *reloadDeps

	healthAdapter      *adapter.HealthAdapter
	configAdapter      *adapter.ConfigAdapter
	devicesAdapter     *adapter.DevicesAdapter
	deviceAdminDomain  *adapter.DeviceAdminDomain
	dpWriterAdapter    *adapter.DataPointWriterAdapter
	customDPDispatcher *adapter.CustomDPDispatcher
	paramsetsDomain    *adapter.ParamsetsDomain
	hubAdapter         *adapter.HubAdapter
	ifaceAdapter       *adapter.InterfacesAdapter
	sysStatusBuf       *handlers.SystemStatusBuffer
	visFilter          filter.VisibilitySet
	metricsReg         *metrics.Registry
	uiSchemaAdapter    *adapter.UISchemaAdapter
	linksDomain        *adapter.LinksDomain
	schedulesDomain    *adapter.SchedulesDomain
	centralLinksDomain *adapter.CentralLinksDomain
	backupAdapter      *adapter.BackupAdapter

	auditBuf  *audit.Buffer
	auditRec  audit.Recorder
	restAuth  *handlers.AuthDeps
	configSvc handlers.ConfigAdminService
	userSvc   handlers.UserAdminService
	tokenSvc  handlers.TokenAdminService
	centSvc   handlers.CentralAdminService

	translations *ccudata.Translations

	mqttSup       *mqttSupervisor
	mqttAvailable bool

	restResolve func(http.Handler) http.Handler
	authMw      *auth.Middleware
	wsHandler   http.Handler

	levels            *hmlog.LevelRegistry
	liveFeed          *hmlog.LiveLog
	captureManager    *diagnostics.Manager
	restStatusMetrics *middleware.StatusMetrics

	visibilityUnIgnoreStore handlers.VisibilityUnIgnoreStore
	visibilityAdapter       *visibilityAdapter

	valuesCacheStore *sqlitestore.ValuesCacheStore
}

// mountRESTServer stands up the REST router + server (and the optional mDNS
// advertiser) when REST is enabled, and returns a teardown that stops the mDNS
// advertiser at daemon exit. When REST is disabled it is a no-op returning a
// no-op teardown. The returned teardown folds the inline mDNS-stop defer that
// previously lived in the composition root.
func mountRESTServer(ctx context.Context, cfg *config.Config, logger *slog.Logger, servers *serverGroup, d restMountDeps) (teardown func()) { //nolint:funlen // length is dominated by the flat rest.Deps assembly literal, not control flow
	teardown = func() {}
	if !cfg.North.REST.IsEnabled() {
		return teardown
	}

	var openapiValidator *middleware.OpenAPIValidator
	if cfg.North.REST.OpenAPIValidateEnabled() {
		openapiValidator = buildOpenAPIValidator(cfg, logger) //nolint:contextcheck // NewOpenAPIValidator/Validate uses context.Background() internally; non-owned code
	}
	// RPC session recorder (XML/JSON-RPC replay capture). Resume a
	// recording that was running before a restart, then expose it.
	rpcRecorder := adapter.NewRPCRecorderAdapter(d.reg, cfg.DataDir)
	if resumed := rpcRecorder.ResumeFromMarker(ctx); len(resumed) > 0 {
		logger.Info("diagnostics.rpc_recording.resumed", slog.Any("centrals", resumed))
	}
	deps := rest.Deps{
		Logger:         logger,
		StartedAt:      time.Now(),
		Health:         d.healthAdapter,
		Config:         d.configAdapter,
		Devices:        d.devicesAdapter,
		DeviceAdmin:    d.deviceAdminDomain,
		RefreshDevices: d.devicesAdapter,
		DPWriter:       d.dpWriterAdapter,
		CustomDPWriter: d.customDPDispatcher,
		Paramsets:      d.paramsetsDomain,
		Hub:            d.hubAdapter,
		InstallMode:    adapter.NewInstallModeAdapter(),
		Interfaces:     d.ifaceAdapter,
		Incidents:      adapter.NewIncidentsAdapter(),
		SystemStatus:   d.sysStatusBuf,
		Labels:         adapter.NewParameterLabelAdapter(d.translations, cfg.Locale),
		DataPointVis:   d.visFilter,
		Metrics:        d.metricsReg,
		UISchema:       d.uiSchemaAdapter,
		Links:          d.linksDomain,
		Schedules:      d.schedulesDomain,
		CentralLinks:   d.centralLinksDomain,
		Audit:          d.auditBuf,
		Auth:           d.restAuth,
		ConfigAdmin:    d.configSvc,
		UserAdmin:      d.userSvc,
		TokenAdmin:     d.tokenSvc,
		CentralAdmin:   d.centSvc,
		MQTTReload:     newMQTTReloadAdapter(d.mqttSup, d.reload, cfg),
		OIDC:           buildOIDCRest(cfg, logger, d.restAuth), //nolint:contextcheck // test callers outside owned set prevent ctx signature; discovery uses its own timeout
		SPAHandler:     ui.SPAHandler(),
		Backup:         d.backupAdapter,
		EditSessions:   handlers.NewEditSessions(),
		WSHandler:      d.wsHandler,
		AuthResolve:    d.restResolve,
		AuthRequire:    d.authMw.Require,
		RequireOperator: func(next http.Handler) http.Handler {
			return d.authMw.RequireRole(auth.RoleOperator, next)
		},
		RequireAdmin: func(next http.Handler) http.Handler {
			return d.authMw.RequireRole(auth.RoleAdmin, next)
		},
		SystemCCU: newSystemCCUAdapter(d.reg, cfg),
		RateLimit: buildRateLimitConfig(cfg),
		Capabilities: runtimeCapabilityDetector{
			mqtt:              d.mqttAvailable,
			matter:            cfg.North.Matter.Enabled,
			oidc:              cfg.North.REST.Auth.OIDC.Enabled && cfg.North.REST.Auth.OIDC.Issuer != "",
			supervisedRestart: detectSupervisedRestart(),
			mcp:               cfg.North.MCP.Enabled,
			mcpWrite:          cfg.North.MCP.AllowWrites,
		},
		CORS:       buildCORS(cfg),
		Idempotent: true,
		// When the daemon hosts exactly one central, capture its
		// name in the REST request scope so every slog record
		// carries `central_name` automatically. Multi-central
		// setups leave the field empty and rely on per-handler
		// SetCentralName resolution.
		CentralName:      singleCentralName(d.reg),
		OpenAPIValidator: openapiValidator,
		// Matter REST surface — fabric-list reads through to the
		// matter store; setup-payload reflects the bridge's
		// configured discriminator + passcode + vendor + product;
		// the commissioning-window opener routes
		// `POST /api/v1/matter/commissioning/window` through the
		// bridge's [matterbridge.CommissioningWindowOpener] (reuses
		// the configured PASE acceptor; ephemeral verifier
		// generation is a post-0.1.0 follow-up).
		MatterFabricStore: d.matter.fabricStore,
		MatterCommissioning: handlers.MatterCommissioning{
			Discriminator: cfg.North.Matter.Discriminator,
			Passcode:      cfg.North.Matter.Commissioning.Passcode,
			VendorID:      cfg.North.Matter.VendorID,
			ProductID:     cfg.North.Matter.ProductID,
		},
		MatterCommissioningOpener: d.matter.opener,
		MatterStatusReader:        d.matter.statusReader,
		MatterFabricRevoker:       d.matter.fabricRevoker,
		MatterCommissioningCloser: d.matter.closer,
		MatterExposureStore:       d.matter.exposureStore,
		MatterCandidateProvider:   d.matter.candidates,
		MatterEventPublisher:      d.matter.pub,
		MatterTopologyReassembler: d.matter.reassembler,
		MatterAuditRecorder:       d.auditRec,
		// Visibility / un-ignore surface — docs/ui/unignore-concept.md.
		VisibilityUnIgnoreStore:     d.visibilityUnIgnoreStore,
		VisibilityCentralLister:     d.visibilityAdapter,
		VisibilityCandidateProvider: d.visibilityAdapter,
		VisibilityRegistryLoader:    d.visibilityAdapter,
		// Diagnostics surface wiring (ADR 0017).
		LogLevels: d.levels,
		// HealthExtras goes through the multi-tracker adapter
		// (daemon-global + every per-central tracker) so the
		// diagnostics dump sees all client details, gauges, and
		// per-central scores regardless of which tracker the
		// producer wrote into.
		HealthExtras:    d.healthAdapter,
		Capture:         d.captureManager,
		LogFeed:         d.liveFeed,
		LogDefaultLevel: d.levels,
		RPCRecorder:     rpcRecorder,
		AuditRecorder:   d.auditRec,
		StatusMetrics:   d.restStatusMetrics,
		KnownCentrals:   d.reg.Names(),
		HealthGauges:    d.healthAdapter.Gauges,
		StartupCapture:  handlers.NewStartupCaptureFileService(cfg.DataDir),
		// Mount /system/restart only when a supervisor will
		// bring the daemon back up. On bare-metal dev runs the
		// endpoint stays unmounted (404), so the SPA's button
		// — which we also disable client-side via the
		// SupervisedRestart capability — fails closed.
		EnableRestartEndpoint: detectSupervisedRestart(),
		ValuesCache:           newValuesCacheHandlerAdapter(d.valuesCacheStore),
		DeviceLookup:          newDeviceLookupAdapter(d.reg),
		CSRFEnabled:           cfg.North.REST.CSRFIsEnabled(),
		CSRFSecure:            cfg.North.REST.CSRFSecure,
	}
	router := rest.NewRouter(deps)
	var topHandler http.Handler = router
	if cfg.North.MCP.Enabled {
		topHandler = mountMCP(cfg, d, router, logger)
	}
	servers.add("rest", rest.NewServer(cfg.North.REST.Listen, topHandler, logger))

	if cfg.North.Discovery.MDNS.IsEnabled() {
		if adv, err := startMDNSAdvertiser(ctx, cfg, logger); err != nil {
			logger.Warn("discovery.mdns.start_failed", slog.String("err", err.Error()))
		} else if adv != nil {
			teardown = func() {
				if err := adv.Stop(); err != nil {
					logger.Warn("discovery.mdns.stop_failed", slog.String("err", err.Error()))
				}
			}
		}
	}
	return teardown
}

// mountMCP wraps the REST router so the configured MCP path serves the
// Streamable-HTTP MCP handler behind the same auth chain as REST, while
// every other path falls through to the REST router. The MCP server is
// read-only unless North.MCP.AllowWrites is also set. See ADR 0025.
func mountMCP(cfg *config.Config, d restMountDeps, router http.Handler, logger *slog.Logger) http.Handler {
	mcpHandler := d.authMw.Require(mcp.Handler(mcp.Deps{
		Centrals:    d.reg,
		Devices:     d.devicesAdapter,
		Writer:      d.dpWriterAdapter,
		Paramsets:   d.paramsetsDomain,
		Health:      d.healthAdapter,
		Hubs:        d.reg,
		Audit:       d.auditRec,
		AllowWrites: cfg.North.MCP.AllowWrites,
		Version:     build.Version,
	}))
	path := cfg.North.MCP.MountPath()
	mux := http.NewServeMux()
	mux.Handle(path, mcpHandler)
	mux.Handle(path+"/", mcpHandler)
	mux.Handle("/", router)
	logger.Info("north.mcp.enabled",
		slog.String("path", path),
		slog.Bool("allow_writes", cfg.North.MCP.AllowWrites))
	return mux
}
