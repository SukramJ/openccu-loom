// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/SukramJ/openccu-loom/internal/addonupdate"
	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/channelflags"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/diagnostics"
	"github.com/SukramJ/openccu-loom/internal/history"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	northbridge "github.com/SukramJ/openccu-loom/internal/north/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
	"github.com/SukramJ/openccu-loom/internal/north/ui"
	"github.com/SukramJ/openccu-loom/internal/security"
	"github.com/SukramJ/openccu-loom/internal/store/masterprofile"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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

	// bootstrap is the server-rendered, no-JS diagnostic surface (/health,
	// /about), folded onto the REST listener (ADR 0044/0045). noUsers is the
	// first-run probe that gates the SPA onboarding endpoints. Both may be nil
	// (UI off / no durable store).
	bootstrap http.Handler
	noUsers   func(context.Context) bool

	// First-run onboarding stores, shared with the SPA setup endpoints
	// (GET /api/v1/setup/status, POST /api/v1/setup). May be nil (no durable
	// backend → setup reports not-required).
	sqUsers    *sqlitestore.UserStore
	sqCentrals *sqlitestore.CentralsStore
	sqSections *sqlitestore.ConfigSectionStore
	sqTokens   *sqlitestore.TokenStore
	// sessions backs the credential-change session-revocation hooks
	// (password change / user update / user delete).
	sessions *auth.SessionStore

	healthAdapter        *adapter.HealthAdapter
	configAdapter        *adapter.ConfigAdapter
	devicesAdapter       *adapter.DevicesAdapter
	deviceAdminDomain    *adapter.DeviceAdminDomain
	ccuMaintenanceDomain *adapter.CCUMaintenanceDomain
	// addonUpdater backs the CCU add-on self-update surface (ADR 0057).
	// Nil when the platform capability check failed (see wireAddonUpdate)
	// — the REST GET then reports supported:false and the check/install
	// verbs answer 404, mirroring the SystemCCU-style always-mounted
	// handler pattern rather than an unmounted-route 404.
	addonUpdater   *addonupdate.Updater
	groupsDomain   *adapter.GroupsDomain
	deviceReloader *adapter.DeviceReloaderAdapter
	// firmwareRefresher backs POST /devices/firmware/refresh (the same
	// FirmwareDomain the WS `firmware.refresh` command uses).
	firmwareRefresher *adapter.FirmwareDomain
	// editSessions is the shared edit-lock registry — backs both the
	// `/sessions/edit` endpoints and the strict MASTER/LINK paramset-write
	// gate, and is shared with the WS `paramset.put` enforcement.
	editSessions       *handlers.EditSessions
	dpWriterAdapter    *adapter.DataPointWriterAdapter
	customDPDispatcher *adapter.CustomDPDispatcher
	paramsetsDomain    *adapter.ParamsetsDomain
	// parameterDeterminer backs POST .../paramsets/{key}/determine (the
	// MASTER editor's "Determine" button). Shares the registry-resolved
	// backend path with the WS `paramset.determine` command.
	parameterDeterminer *adapter.ParameterDeterminerAdapter
	hubAdapter          *adapter.HubAdapter
	ifaceAdapter        *adapter.InterfacesAdapter
	incidents           handlers.IncidentsReader
	// alarm is the daemon-level alarm service backing the /alarm surface.
	// It may be a nil *alarm.Service (subsystem disabled or failed to
	// start); alarmPanelFrom converts that to a nil interface so the
	// routes stay unmounted rather than dispatching to a nil pointer.
	alarm *alarm.Service
	// security is the Security & Safety domain backing /security. Nil
	// when the persistence tier is unavailable.
	security *security.Service
	// masterProfiles backs the read-only master-profiles REST routes
	// (GET .../master-profiles[/{id}], POST .../master-profiles/match) —
	// the same *masterprofile.Store instance the WS
	// master_profiles.list/get/match commands are wired against.
	masterProfiles         *masterprofile.Store
	sysStatusBuf           *handlers.SystemStatusBuffer
	visFilter              filter.VisibilitySet
	metricsReg             *metrics.Registry
	uiSchemaAdapter        *adapter.UISchemaAdapter
	linksDomain            *adapter.LinksDomain
	schedulesDomain        *adapter.SchedulesDomain
	centralLinksDomain     *adapter.CentralLinksDomain
	definitionExportDomain *adapter.DefinitionExportDomain
	backupAdapter          *adapter.BackupAdapter
	roomFunctionAdmin      *adapter.RoomFunctionAdminDomain
	cacheResetSvc          handlers.CacheResetService

	auditBuf    *audit.Buffer
	auditRead   handlers.AuditService
	auditRec    audit.Recorder
	restAuth    *handlers.AuthDeps
	configSvc   handlers.ConfigAdminService
	userSvc     handlers.UserAdminService
	passwordSvc handlers.SelfPasswordService
	prefSvc     handlers.UserPreferencesService
	diagramSvc  handlers.DiagramConfigService
	areaSvc     handlers.AreaAdmin
	tokenSvc    handlers.TokenAdminService
	centSvc     handlers.CentralAdminService
	discovery   *handlers.DiscoveryDeps

	translations *ccudata.Translations
	// catalogs is the daemon's own i18n catalogue, backing GET
	// /i18n/entities. The MQTT discovery plane already resolves its entity
	// names from it; this hands the same vocabulary to REST/WS consumers.
	catalogs *i18n.Catalogs

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

	valuesCacheStore    *sqlitestore.ValuesCacheStore
	historyStore        *sqlitestore.MeasurementStore
	recordingOverrides  *history.RecordingOverrides
	channelFlagsStore   *sqlitestore.ChannelFlagsStore
	channelFlagsOverlay *channelflags.Overlay
}

// mountRESTServer stands up the REST router + server (and the optional mDNS
// advertiser) when REST is enabled, and returns a teardown that stops the mDNS
// advertiser at daemon exit. When REST is disabled it is a no-op returning a
// no-op teardown. The returned teardown folds the inline mDNS-stop defer that
// previously lived in the composition root.
func mountRESTServer(ctx context.Context, cfg *config.Config, logger *slog.Logger, northBridges *northbridge.Registry, d restMountDeps) (teardown func()) { //nolint:funlen // length is dominated by the flat rest.Deps assembly literal, not control flow
	teardown = func() {}
	if !cfg.North.REST.IsEnabled() {
		return teardown
	}

	var openapiValidator *middleware.OpenAPIValidator
	if cfg.North.REST.OpenAPIValidateEnabled() {
		openapiValidator = buildOpenAPIValidator(cfg, logger) //nolint:contextcheck // NewOpenAPIValidator/Validate uses context.Background() internally; non-owned code
	}
	// Shared live central-config resolver (see ccu_auth_wiring.go): reads the
	// persisted centrals store — the same table a runtime central-adopt
	// writes to — falling back to the boot-time cfg.Centrals snapshot when
	// no store is available. Both the icon proxy and the system-CCU listing
	// need the same by-name lookup, so a single instance is built here.
	centralResolve := newCCUAuthCentralResolver(cfg, d.sqCentrals)
	// Read-only view on live daemon internals: per-interface reliability,
	// the event-bus tap, and the per-central metrics snapshots the
	// diagnostics dump renders.
	introspect := adapter.NewIntrospectAdapter(d.reg)
	// RPC session recorder (XML/JSON-RPC replay capture). Resume a
	// recording that was running before a restart, then expose it.
	rpcRecorder := adapter.NewRPCRecorderAdapter(d.reg, cfg.DataDir)
	if resumed := rpcRecorder.ResumeFromMarker(ctx); len(resumed) > 0 {
		logger.Info("diagnostics.rpc_recording.resumed", slog.Any("centrals", resumed))
	}
	// One provider snapshots the boot config and serves both the
	// restart-pending banner and the changed-settings overview. The
	// baseline is the *assembled* effective config at boot (not the raw
	// YAML cfg): the overview compares it against the same assembly per
	// request, so YAML-only fields the effective view derives elsewhere
	// (e.g. locale) don't read as spurious changes on a clean start.
	bootBaseline := cfg
	if eff, err := d.configSvc.Effective(ctx); err == nil && eff != nil && eff.Config != nil {
		bootBaseline = eff.Config
	}
	restartState := newRestartPendingProvider(bootBaseline, d.configSvc)
	// The live surface profile. Seeded from the assembled effective
	// config — not the raw YAML — so a profile the operator saved to the
	// database is in force from the first request after a restart, and
	// updated in place by the surfaces write handler so a later change
	// needs no restart at all.
	// How many CCUs this daemon serves, read live rather than captured:
	// a CCU adopted at runtime widens two shipped surface defaults,
	// because Home Assistant addresses one CCU per config entry and
	// therefore cannot own the config surface of the ones it has no
	// entry for.
	centralCounter := func() int {
		if d.visibilityAdapter == nil {
			return 0
		}
		return len(d.visibilityAdapter.Names())
	}
	// TLS: when cert+key are configured, build a hot-reloading cert
	// provider. A load failure logs and falls back to plain HTTP rather
	// than refusing to boot, so a bad cert never locks the operator out.
	var tlsReloader *rest.CertReloader
	var tlsCertSvc handlers.TLSCertService
	if cfg.North.REST.TLSEnabled() {
		if rl, err := rest.NewCertReloader(cfg.North.REST.TLSCertFile, cfg.North.REST.TLSKeyFile, logger); err != nil {
			logger.Error("rest.tls.disabled", slog.String("err", err.Error()))
		} else {
			tlsReloader = rl
			tlsCertSvc = rl
		}
	}
	deps := rest.Deps{
		Logger:                  logger,
		StartedAt:               time.Now(),
		Health:                  d.healthAdapter,
		Config:                  d.configAdapter,
		Devices:                 d.devicesAdapter,
		DeviceAdmin:             d.deviceAdminDomain,
		DeviceReplacer:          d.deviceAdminDomain,
		FirmwareRefresher:       firmwareRefresherFrom(d.firmwareRefresher),
		DeviceInstallMode:       d.deviceAdminDomain,
		InstallModeSearch:       d.deviceAdminDomain,
		DeviceCommunicationTest: d.deviceAdminDomain,
		DeviceTeam:              d.deviceAdminDomain,
		DeviceIcons:             newDeviceIconProxy(d.reg, centralResolve),
		RefreshDevices:          d.devicesAdapter,
		Reloader:                d.deviceReloader,
		DPWriter:                d.dpWriterAdapter,
		CustomDPWriter:          d.customDPDispatcher,
		Paramsets:               d.paramsetsDomain,
		ParameterDeterminer:     d.parameterDeterminer,
		Hub:                     d.hubAdapter,
		WebhookInboundEnabled:   cfg.North.Webhook.Inbound.Enabled,
		WebhookInboundToken:     cfg.North.Webhook.Inbound.Token,
		SysvarRefresh:           adapter.NewSysvarFetchAdapter(d.reg),
		Interfaces:              d.ifaceAdapter,
		Incidents:               d.incidents,
		IncidentsAdmin:          incidentsClearerFrom(d.incidents),
		Alarm:                   alarmPanelFrom(d.alarm),
		Security:                securityDomainFrom(d.security),
		AlarmCodes:              alarmCodeAdminFrom(d.alarm),
		MasterProfiles:          d.masterProfiles,
		SystemStatus:            d.sysStatusBuf,
		Labels:                  adapter.NewParameterLabelAdapter(d.translations, cfg.Locale),
		EntityNames:             entityNameCatalogueFrom(d.catalogs),
		DataPointVis:            d.visFilter,
		Metrics:                 d.metricsReg,
		UISchema:                d.uiSchemaAdapter,
		Links:                   d.linksDomain,
		Schedules:               d.schedulesDomain,
		CentralLinks:            d.centralLinksDomain,
		DefinitionExport:        d.definitionExportDomain,
		Audit:                   d.auditRead,
		Auth:                    d.restAuth,
		ConfigAdmin:             d.configSvc,
		CentralCounter:          centralCounter,
		// Where a browser can reach this daemon's Config UI, for clients
		// that want to link a person there. Same source and same
		// restart-required reasoning as the config.cgi hint file written
		// in daemon.go, so the two can never name different addresses.
		ConfigUIURL:       cfg.North.REST.ConfigUIURL(),
		RestartPending:    restartState,
		ConfigChanges:     restartState,
		UserAdmin:         d.userSvc,
		SelfPassword:      d.passwordSvc,
		SessionRevoker:    d.sessions,
		TokenPurger:       d.sqTokens,
		Preferences:       d.prefSvc,
		Diagrams:          d.diagramSvc,
		Areas:             d.areaSvc,
		RoomFunctionAdmin: d.roomFunctionAdmin,
		TLSCert:           tlsCertSvc,
		TokenAdmin:        d.tokenSvc,
		CentralAdmin:      d.centSvc,
		Discovery:         d.discovery,
		MQTTReload:        newMQTTReloadAdapter(d.mqttSup, d.reload, cfg, logger),
		OIDC:              buildOIDCRest(cfg, logger, d.restAuth), //nolint:contextcheck // test callers outside owned set prevent ctx signature; discovery uses its own timeout
		SPAHandler:        ui.SPAHandler(),
		Bootstrap:         d.bootstrap,
		Setup: &handlers.SetupService{
			Users: d.sqUsers,
			// The live-adopt decorator, not the raw store: the wizard's CCU
			// must come up immediately, exactly as an admin-created one does.
			Centrals: d.centSvc,
			Sections: d.sqSections,
			Required: d.noUsers,
		},
		LoginRateLimit:  middleware.NewLoginRateLimiter(),
		Backup:          d.backupAdapter,
		BackupUpload:    d.backupAdapter,
		PreUpdateBackup: d.backupAdapter,
		CacheReset:      d.cacheResetSvc,
		EditSessions:    d.editSessions,
		WSHandler:       d.wsHandler,
		AuthResolve:     d.restResolve,
		AuthRequire:     d.authMw.Require,
		RequireOperator: func(next http.Handler) http.Handler {
			return d.authMw.RequireRole(auth.RoleOperator, next)
		},
		RequireAdmin: func(next http.Handler) http.Handler {
			return d.authMw.RequireRole(auth.RoleAdmin, next)
		},
		SystemCCU:        newSystemCCUAdapter(d.reg, centralResolve),
		CCUReboot:        d.ccuMaintenanceDomain,
		CCUPosition:      d.ccuMaintenanceDomain,
		CCUHostActions:   d.ccuMaintenanceDomain,
		FirmwareDownload: d.ccuMaintenanceDomain,
		AddonUpdate:      addonUpdateServiceFrom(d.addonUpdater),
		Groups:           newGroupsAdapter(d.groupsDomain),
		GroupsWriter:     newGroupsAdapter(d.groupsDomain),
		RateLimit:        buildRateLimitConfig(cfg),
		Capabilities: runtimeCapabilityDetector{
			mqtt:              d.mqttAvailable,
			matter:            cfg.North.Matter.Enabled,
			oidc:              cfg.North.REST.Auth.OIDC.Enabled && cfg.North.REST.Auth.OIDC.Issuer != "",
			ccuAuth:           ccuAuthEnabled(cfg.North.REST.Auth.CCU),
			supervisedRestart: detectSupervisedRestart(),
			mcp:               cfg.North.MCP.Enabled,
			mcpWrite:          cfg.North.MCP.AllowWrites,
			// Mirrors the Deps.Alarm mount condition below: the token
			// tracks whether the /alarm routes exist, not whether the
			// engine is armed/healthy.
			alarm: d.alarm != nil,
			// History is enabled when the recorder store was wired (the
			// same opt-in flag that mounts /history and /history/recording).
			history: d.historyStore != nil,
			// addonSelfUpdate mirrors wireAddonUpdate's capability gate
			// (add-on build + firmware installer present) — d.addonUpdater
			// is only non-nil when that check passed.
			addonSelfUpdate: d.addonUpdater != nil,
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
		MatterFabricStore:           d.matter.fabricStore,
		MatterSessionLister:         d.matter.sessionLister,
		MatterMdnsReporter:          d.matter.mdnsReporter,
		MatterEndpointInspector:     d.matter.endpointInspector,
		MatterCompatibilityReporter: d.matter.compatReporter,
		// WithDefaults keeps the setup payload aligned with the bridge
		// runtime: QR / manual code must carry the SAME discriminator the
		// mDNS record advertises, or commissioners filter the bridge out.
		MatterCommissioning: func() handlers.MatterCommissioning {
			mcfg := cfg.North.Matter.WithDefaults()
			return handlers.MatterCommissioning{
				Discriminator: mcfg.Discriminator,
				Passcode:      mcfg.Commissioning.Passcode,
				VendorID:      mcfg.VendorID,
				ProductID:     mcfg.ProductID,
			}
		}(),
		MatterCommissioningOpener: d.matter.opener,
		MatterStatusReader:        d.matter.statusReader,
		MatterFabricRevoker:       d.matter.fabricRevoker,
		MatterCommissioningCloser: d.matter.closer,
		MatterExposureStore:       d.matter.exposureStore,
		MatterCandidateProvider:   d.matter.candidates,
		MatterEventPublisher:      d.matter.pub,
		MatterTopologyReassembler: d.matter.reassembler,
		MatterAuditRecorder:       d.auditRec,
		// Visibility / un-ignore surface — notes/concepts/ui/unignore-concept.md.
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
		Introspect:      introspect,
		RSSIInfo:        adapter.NewRSSIInfoDomain(d.reg),
		AuditRecorder:   d.auditRec,
		StatusMetrics:   d.restStatusMetrics,
		KnownCentrals:   d.reg.Names(),
		HealthGauges:    d.healthAdapter.Gauges,
		// The per-CCU aggregators the boot wiring stands up leave the
		// daemon here — the diagnostics dump is their only reader.
		CentralMetrics: introspect.MetricsSnapshots,
		StartupCapture: handlers.NewStartupCaptureFileService(cfg.DataDir),
		// Mount /system/restart only when a supervisor will
		// bring the daemon back up. On bare-metal dev runs the
		// endpoint stays unmounted (404), so the SPA's button
		// — which we also disable client-side via the
		// SupervisedRestart capability — fails closed.
		EnableRestartEndpoint: detectSupervisedRestart(),
		ValuesCache:           newValuesCacheHandlerAdapter(d.valuesCacheStore),
		History:               newHistoryHandlerAdapter(d.historyStore),
		RecordingOverrides:    newRecordingOverrideAdapter(d.recordingOverrides),
		ChannelFlags:          d.channelFlagsStore,
		ChannelFlagsOverlay:   d.channelFlagsOverlay,
		Energy:                newEnergyHandlerAdapter(d.historyStore, d.reg, cfg.Persistence.History.EnergyPricePerKWh, cfg.Persistence.History.EnergyCurrency),
		DeviceLookup:          newDeviceLookupAdapter(d.reg),
		CSRFEnabled:           cfg.North.REST.CSRFIsEnabled(),
		CSRFSecure:            cfg.North.REST.CSRFSecure,
	}
	// Fail fast if the composition root ever stops wiring the auth chain:
	// the router's role shims fall through to an open pass-through when
	// AuthRequire is nil, which must never happen in a served build.
	if err := deps.AssertAuthWired(); err != nil {
		logger.Error("rest.auth_not_wired — refusing to serve", slog.String("err", err.Error()))
		return func() {}
	}
	router := rest.NewRouter(deps)
	var topHandler http.Handler = router
	if cfg.North.MCP.Enabled {
		topHandler = mountMCP(cfg, d, router, logger)
	}
	restServer := rest.NewServer(cfg.North.REST.Listen, topHandler, logger)
	if tlsReloader != nil {
		restServer.EnableTLS(tlsReloader)
	}
	// The REST/HTTP surface is a PhaseLate bridge.Service: it starts last
	// (after the router — incl. the MCP mount — is assembled) and, being
	// registered after the webhook, stops first in the reverse-order StopAll,
	// preserving the graceful-REST-shutdown-before-teardown behaviour the old
	// serverGroup gave. See ADR 0047.
	northBridges.Register(rest.NewService(restServer, logger))

	if cfg.North.Discovery.MDNS.IsEnabled() {
		if adv, err := startMDNSAdvertiser(ctx, cfg, d.reg, logger); err != nil {
			logger.Warn("discovery.mdns.start_failed", slog.String("err", err.Error()))
		} else if adv != nil {
			// Re-announce the TXT bundle when serials resolve or the
			// central set changes (ADR 0058) — the hub-ready pipeline
			// invokes this slot.
			refresh := func() {
				if err := adv.UpdateTXT(mdnsTXT(cfg, len(d.reg.Names()), mdnsCCUSerials(d.reg))); err != nil {
					logger.Debug("discovery.mdns.txt_refresh_failed", slog.String("err", err.Error()))
				}
			}
			d.reload.SetMDNSTXTRefresh(refresh)
			teardown = func() {
				d.reload.SetMDNSTXTRefresh(nil)
				if err := adv.Stop(); err != nil {
					logger.Warn("discovery.mdns.stop_failed", slog.String("err", err.Error()))
				}
			}
		}
	}
	return teardown
}

// incidentsClearerFrom narrows the wired IncidentsReader down to the
// optional handlers.IncidentsClearer surface DELETE /incidents needs.
// *adapter.IncidentsStoreReader (the concrete type behind d.incidents)
// implements both, so REST's bulk clear and the WS `incidents.clear`
// command share the same domain call; a reader that only satisfies
// IncidentsReader (e.g. a future non-SQLite-backed implementation)
// simply leaves the route unmounted (nil → 404).
func incidentsClearerFrom(r handlers.IncidentsReader) handlers.IncidentsClearer {
	c, _ := r.(handlers.IncidentsClearer)
	return c
}

// firmwareRefresherFrom converts the concrete FirmwareDomain into the
// handler port, returning a genuinely nil interface for a nil pointer so
// the router leaves the refresh route unmounted (a non-nil interface
// wrapping a nil pointer would dispatch and panic).
func firmwareRefresherFrom(d *adapter.FirmwareDomain) handlers.FirmwareRefresher {
	if d == nil {
		return nil
	}
	return d
}

// alarmPanelFrom converts the concrete alarm service into the handler
// facade, returning a genuinely nil interface when the service is a nil
// pointer so the router leaves the /alarm routes unmounted (a non-nil
// interface wrapping a nil pointer would dispatch and panic).
func alarmPanelFrom(s *alarm.Service) handlers.AlarmPanel {
	if s == nil {
		return nil
	}
	return s
}

// securityDomainFrom converts *security.Service into the handler
// facade, mapping a nil pointer to a genuinely nil interface so the
// router leaves /security unmounted rather than dispatching into a nil
// receiver.
// entityNameCatalogueFrom converts *i18n.Catalogs into the handler port,
// mapping a nil pointer to a genuinely nil interface so the handler's
// nil check fires instead of a typed-nil method call.
func entityNameCatalogueFrom(c *i18n.Catalogs) handlers.EntityNameCatalogue {
	if c == nil {
		return nil
	}
	return c
}

func securityDomainFrom(s *security.Service) handlers.SecurityDomain {
	if s == nil {
		return nil
	}
	return s
}

// addonUpdateServiceFrom converts *addonupdate.Updater into
// handlers.AddonUpdateService, mapping a nil pointer to a genuinely
// nil interface. Without this a nil *Updater assigned directly to the
// interface field would produce a non-nil interface wrapping a nil
// pointer — GetAddonUpdate's `if svc == nil` guard would then miss it
// and Status() would panic on the nil receiver.
func addonUpdateServiceFrom(u *addonupdate.Updater) handlers.AddonUpdateService {
	if u == nil {
		return nil
	}
	return u
}

// alarmCodeAdminFrom converts the alarm service into the /alarm/codes CRUD
// facade: a store-backed adapter that maps the wire DTOs onto the
// argon2id-hashed alarm-code store (notes/concepts/alarm-concept.md §11). A nil
// service or store yields a genuinely nil interface so the codes routes
// serve 503 rather than panicking.
func alarmCodeAdminFrom(s *alarm.Service) handlers.AlarmCodeAdmin {
	if s == nil || s.Stores() == nil || s.Stores().Codes == nil {
		return nil
	}
	return handlers.NewAlarmCodeStoreAdmin(s.Stores().Codes).OnChange(s.NotifyCodesChanged)
}

// mountMCP wraps the REST router so the configured MCP path serves the
// Streamable-HTTP MCP handler behind the same auth chain as REST, while
// every other path falls through to the REST router. The MCP server is
// read-only unless North.MCP.AllowWrites is also set. See ADR 0025.
func mountMCP(cfg *config.Config, d restMountDeps, router http.Handler, logger *slog.Logger) http.Handler {
	// Resolve must wrap Require: Require only checks the identity the
	// resolve chain put into the context — without it every request,
	// credentialed or not, is rejected with 401 (the MCP mount sits
	// outside the REST router's own middleware stack).
	mcpHandler := d.restResolve(d.authMw.Require(mcp.Handler(mcp.Deps{
		Centrals:     d.reg,
		Devices:      d.devicesAdapter,
		Writer:       d.dpWriterAdapter,
		Paramsets:    d.paramsetsDomain,
		Health:       d.healthAdapter,
		Hubs:         d.reg,
		Audit:        d.auditRec,
		Incidents:    d.incidents,
		Alarm:        mcpAlarmSeam(d),
		AlarmControl: mcpAlarmControlSeam(d),
		Security:     mcpSecuritySeam(d),
		AllowWrites:  cfg.North.MCP.AllowWrites,
		Version:      build.Version,
	})))
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

// appDBServices constructs the main-app-DB-backed REST services
// (per-user preferences, diagram configs, areas). All three return nil
// when persistence is disabled so their routes drop.
func appDBServices(auditDB *sql.DB) (handlers.UserPreferencesService, handlers.DiagramConfigService, handlers.AreaAdmin) {
	if auditDB == nil {
		return nil, nil, nil
	}
	// *sqlitestore.AreaStore satisfies handlers.AreaAdmin directly — no
	// adapter needed.
	return sqlitestore.NewUserPreferencesStore(auditDB),
		newDiagramConfigAdapter(sqlitestore.NewDiagramConfigStore(auditDB)),
		sqlitestore.NewAreaStore(auditDB)
}

// --- MCP alarm / security seams ---------------------------------------

// mcpAlarmSeam projects the alarm service onto the MCP read port. A nil
// service (subsystem disabled) yields a nil port, which leaves the
// alarm tools unregistered rather than advertising tools that answer
// "not configured" to every call.
func mcpAlarmSeam(d restMountDeps) mcp.AlarmReader {
	if d.alarm == nil {
		return nil
	}
	return &mcpAlarmAdapter{svc: d.alarm}
}

// mcpAlarmControlSeam projects the write half. It is only consulted
// when AllowWrites is set; the nil-service rule is the same.
func mcpAlarmControlSeam(d restMountDeps) mcp.AlarmController {
	if d.alarm == nil {
		return nil
	}
	return &mcpAlarmAdapter{svc: d.alarm}
}

// mcpSecuritySeam projects the Security & Safety domain.
func mcpSecuritySeam(d restMountDeps) mcp.SecurityReader {
	if d.security == nil {
		return nil
	}
	return d.security
}

// mcpAlarmAdapter satisfies both MCP alarm ports over the engine.
type mcpAlarmAdapter struct{ svc *alarm.Service }

func (a *mcpAlarmAdapter) Zones() []engine.ZoneSnapshot { return a.svc.Engine().Zones() }

func (a *mcpAlarmAdapter) TriggeredMotionSensors(zoneID string) []engine.TriggeredMotionSensor {
	return a.svc.Engine().TriggeredMotionSensors(zoneID)
}

// alarmSourceMCP tags the journal and audit trail so an operator can
// tell an assistant-driven arm from one a person performed. It
// deliberately does not carry the `-operator` suffix that marks the
// REST session as a break-glass surface: an assistant must not bypass
// a required code.
const alarmSourceMCP = "mcp"

func (a *mcpAlarmAdapter) Arm(ctx context.Context, zoneID string, mode hmenum.AlarmMode) error {
	_, err := a.svc.Engine().Arm(ctx, zoneID, engine.ArmRequest{
		Mode:   mode,
		Source: alarmSourceMCP,
	})
	return err
}

func (a *mcpAlarmAdapter) Disarm(ctx context.Context, zoneID string) error {
	return a.svc.Engine().Disarm(ctx, zoneID, "", alarmSourceMCP)
}

func (a *mcpAlarmAdapter) ResetMotion(ctx context.Context, zoneID string) (reset, failed int) {
	res := a.svc.Engine().ResetTriggeredMotion(ctx, zoneID, "", alarmSourceMCP)
	return res.Reset, res.Failed
}
