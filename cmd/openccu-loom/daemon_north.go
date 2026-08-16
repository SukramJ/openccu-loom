// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/auth/oidc"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	northbridge "github.com/SukramJ/openccu-loom/internal/north/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/north/ui"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// The REST/HTTP server lifecycle is owned by the north-bound bridge.Registry
// as a PhaseLate rest.Service (ADR 0047); the former serverGroup helper was
// retired when REST moved onto the registry.

// authStores bundles the in-memory auth state the composition root builds
// before the REST phase. The SQLite-backed stores are layered on top inside
// wireREST; these remain as the secondary fallback so YAML-pinned legacy users
// keep working.
type authStores struct {
	users          *auth.MemoryUserStore
	tokens         *auth.MemoryTokenStore
	sessions       *auth.SessionStore
	authMw         *auth.Middleware
	sessionResolve func(http.Handler) http.Handler
	restResolve    func(http.Handler) http.Handler
}

// buildAuthStores constructs the in-memory user/token/session stores from
// cfg.Users, registers the token store on the WS hub, and chains the token +
// session resolvers. The SPA authenticates via the session cookie the HTMX
// login page sets; since the browser sends cookies across ports to the same
// hostname, both tokens (Bearer, Basic) AND sessions must resolve on the REST
// listener, so the two resolvers are chained.
//
// When sessionPersist is non-nil the session store is durable
// (SQLite-backed, save-through): it hydrates active sessions on boot and
// mirrors Issue/Revoke so a daemon restart no longer logs every browser
// out. A nil persistence (no DB — tests, dev-loopback) keeps the
// historical in-memory-only behaviour, and a hydration failure falls
// back to in-memory rather than crashing boot.
// gatedAuthMiddleware builds the header-auth middleware from the two stores,
// honouring the tri-state scheme gates: the middleware only receives the store
// whose scheme is enabled, and a nil store disables that scheme (see
// [auth.NewMiddleware]). The concrete stores stay available to their callers
// regardless — SPA login and the user/token admin endpoints work on them
// independently of the header-based schemes.
//
// It is shared by every construction site (boot-time in-memory stores, and the
// SQLite-chained rebuild in wireREST) because a site that skips the gate
// re-enables a scheme the operator switched off, with no signal that the
// setting did nothing.
func gatedAuthMiddleware(cfg *config.Config, users auth.UserStore, tokens auth.TokenStore) *auth.Middleware {
	var mwUsers auth.UserStore
	if cfg.North.REST.Auth.BasicAuthEnabled() {
		mwUsers = users
	}
	var mwTokens auth.TokenStore
	if cfg.North.REST.Auth.BearerAuthEnabled() {
		mwTokens = tokens
	}
	return auth.NewMiddleware(mwUsers, mwTokens)
}

func buildAuthStores(cfg *config.Config, wsHub *ws.Hub, sessionPersist auth.SessionPersistence, logger *slog.Logger) authStores {
	users := auth.NewMemoryUserStore()
	for name, pass := range cfg.North.REST.Auth.Users {
		hashed, err := auth.HashPassword(pass)
		if err != nil {
			slog.Warn("auth: skipping YAML-seeded user with unhashable password",
				slog.String("user", name), slog.Any("error", err))
			continue
		}
		users.Put(name, hashed, auth.RoleAdmin)
	}
	tokens := auth.NewMemoryTokenStore(buildTokenMap(cfg))
	sessions := buildSessionStore(sessionPersist, logger)

	if cfg.North.REST.Auth.BearerAuthEnabled() {
		// WS upgrades authenticate via the same Bearer tokens; the hub
		// only learns the store when the scheme is on.
		wsHub.SetTokenStore(tokens)
	}
	authMw := gatedAuthMiddleware(cfg, users, tokens)

	sessionResolve := auth.SessionMiddleware(sessions)
	restResolve := func(next http.Handler) http.Handler {
		return authMw.Resolve(sessionResolve(next))
	}
	return authStores{
		users:          users,
		tokens:         tokens,
		sessions:       sessions,
		authMw:         authMw,
		sessionResolve: sessionResolve,
		restResolve:    restResolve,
	}
}

// buildSessionStore returns a save-through session store when a durable
// backing is available, else the in-memory store. A persistence
// hydration error degrades to in-memory (login still works) rather than
// aborting boot.
func buildSessionStore(persist auth.SessionPersistence, logger *slog.Logger) *auth.SessionStore {
	if persist == nil {
		return auth.NewSessionStore()
	}
	sessions, err := auth.NewPersistentSessionStore(persist, logger)
	if err != nil {
		logger.Warn("auth.session.persist.hydrate",
			slog.String("err", err.Error()),
			slog.String("effect", "sessions in-memory only this run"))
		return auth.NewSessionStore()
	}
	return sessions
}

// uiMountDeps bundles the live subsystems the server-rendered diagnostic UI
// router needs. Since the SPA owns login + onboarding, this is now just the
// health reader and translation catalogues behind /health and /about.
type uiMountDeps struct {
	healthAdapter *adapter.HealthAdapter
	catalogs      *i18n.Catalogs
}

// buildBootstrapRouter builds the server-rendered diagnostic surface
// (/health, /about). Since ADR 0044 it is folded onto the REST listener
// (:8119) instead of a stand-alone :8081 server, so it works through one port /
// HA Ingress. Login, logout, OIDC, and first-run onboarding now live in the
// Svelte SPA — this surface exists only as a no-JS fallback for when the SPA
// bundle cannot load. Returns nil when the UI is disabled.
func buildBootstrapRouter(cfg *config.Config, logger *slog.Logger, d uiMountDeps) http.Handler {
	if !cfg.North.UI.IsEnabled() {
		return nil
	}
	return ui.NewRouter(ui.Deps{
		Logger:   logger,
		Lang:     cfg.Locale,
		Health:   d.healthAdapter,
		Catalogs: d.catalogs,
	})
}

// firstRunProbe returns a probe reporting first-run state — no way to
// authenticate yet — that gates the SPA onboarding endpoints
// (GET /api/v1/setup/status, POST /api/v1/setup). nil sqUsers (no durable
// store) reports "not first run" so the probe never traps an operator on a
// backend that cannot persist the onboarding result.
//
// sqTokens and sqCentrals are read live for the same reason the user count
// is: both back an authentication source an operator can add after boot (an
// API token minted through the SPA, a CCU adopted at runtime), and the probe
// gates an endpoint that creates an admin account without any credential.
func firstRunProbe(
	cfg *config.Config,
	sqUsers *sqlitestore.UserStore,
	sqTokens *sqlitestore.TokenStore,
	sqCentrals *sqlitestore.CentralsStore,
) func(context.Context) bool {
	return func(ctx context.Context) bool {
		if sqUsers == nil {
			return false
		}
		n, err := sqUsers.Count(ctx)
		if err != nil {
			return false
		}
		return firstRunNeedsSetup(cfg, authSources{
			localUsers:      n,
			persistedTokens: hasPersistedToken(ctx, sqTokens),
			hasCentral:      hasConfiguredCentral(ctx, cfg, sqCentrals),
		})
	}
}

// hasPersistedToken reports whether the API-token store holds at least one
// token — a live bearer credential that authenticates through
// [auth.Middleware] exactly like a local user does.
//
// A store error reports true. The probe this feeds opens anonymous admin
// creation, so an unreadable store must not be read as "nobody can
// authenticate"; the user count fails in the same direction.
func hasPersistedToken(ctx context.Context, sqTokens *sqlitestore.TokenStore) bool {
	if sqTokens == nil {
		return false
	}
	n, err := sqTokens.Count(ctx)
	if err != nil {
		return true
	}
	return n > 0
}

// warnOnDormantOnboarding logs once at boot when the operator has closed the
// first-run surface on a daemon that cannot authenticate anyone. The lockout
// is the point of `bootstrap.allow_first_run_setup: false`, but from the SPA
// it is indistinguishable from a broken login — the log record is the only
// place that can name the cause and the way out.
func warnOnDormantOnboarding(ctx context.Context, cfg *config.Config, sqUsers *sqlitestore.UserStore, sqTokens *sqlitestore.TokenStore, sqCentrals *sqlitestore.CentralsStore, logger *slog.Logger) {
	if cfg.Bootstrap.FirstRunSetupAllowed() || sqUsers == nil {
		return
	}
	n, err := sqUsers.Count(ctx)
	if err != nil {
		return
	}
	if !noAuthSourceConfigured(cfg, authSources{
		localUsers:      n,
		persistedTokens: hasPersistedToken(ctx, sqTokens),
		hasCentral:      hasConfiguredCentral(ctx, cfg, sqCentrals),
	}) {
		return
	}
	logger.Warn("setup.onboarding.dormant",
		slog.String("reason", "bootstrap.allow_first_run_setup is false and no authentication source is configured"),
		slog.String("effect", "nobody can log in and the onboarding wizard stays closed"),
		slog.String("remedy", "set bootstrap.allow_first_run_setup: true in the config file and restart"))
}

// hasConfiguredCentral reports whether the operator has a central in either
// tier: the boot-time cfg.Centrals snapshot (the YAML tier, plus the DB rows
// layered in at boot) or an enabled row added to the SQLite centrals table
// since — the table is consulted live so a runtime adopt counts immediately.
// A store error falls back to the snapshot alone.
func hasConfiguredCentral(ctx context.Context, cfg *config.Config, sqCentrals *sqlitestore.CentralsStore) bool {
	if len(cfg.Centrals) > 0 {
		return true
	}
	if sqCentrals == nil {
		return false
	}
	rows, err := sqCentrals.List(ctx)
	if err != nil {
		return false
	}
	for i := range rows {
		if rows[i].Enabled {
			return true
		}
	}
	return false
}

// firstRunNeedsSetup reports whether the operator still needs onboarding: ONLY
// when there is no way to authenticate yet — no local admin AND no alternative
// provider. In the add-on CCU auth is on by default, so most operators log in
// with their CCU account and never create a local admin; treating that as
// "first run" would trap them on the wizard (regression fixed in 0.14.1).
//
// CCU-delegated login only counts once at least one central is configured:
// it authenticates against a CCU's user database, and with no central there
// is nothing to ask (ADR 0043). Counting it regardless locked a fresh CCU
// add-on install out completely — the wizard was suppressed AND every CCU
// login was rejected, while adding the central needs an authenticated
// session.
//
// The operator can close the surface outright with
// `bootstrap.allow_first_run_setup: false`, which wins over everything else:
// it is a hardening control for deployments that must never expose anonymous
// admin creation, including on a database whose users table was emptied
// (restored volume, blank DB). Its documented consequence is a lockout — with
// the toggle false and no authentication source configured the only way in is
// editing the YAML back and restarting. [warnOnDormantOnboarding] names that
// state in the log so it is diagnosable.
func firstRunNeedsSetup(cfg *config.Config, src authSources) bool {
	return cfg.Bootstrap.FirstRunSetupAllowed() && noAuthSourceConfigured(cfg, src)
}

// authSources carries the facts [noAuthSourceConfigured] can only learn from
// the persistent stores — everything else comes out of cfg. Grouping them
// keeps every call site naming what it passes: the decision opens or closes
// anonymous admin creation, and a silently-swapped positional bool there is
// a remote-privilege bug.
type authSources struct {
	// localUsers is the row count of the SQLite users table.
	localUsers int
	// persistedTokens reports whether the SQLite token store holds at
	// least one API token.
	persistedTokens bool
	// hasCentral reports whether any central is configured, which is what
	// makes CCU-delegated login usable.
	hasCentral bool
}

// noAuthSourceConfigured reports whether the daemon has no way to
// authenticate anyone yet.
//
// Every credential the auth middleware resolves has to be listed here.
// Bearer tokens were missing, and they are resolved FIRST in the chain: a
// token-only deployment — the documented headless setup, since CCU-delegated
// login is off outside the add-on — authenticated its operator perfectly
// while still reporting "first run", which leaves the unauthenticated
// POST /api/v1/setup open for anyone on the network to mint an admin.
//
// A credential only counts while the scheme that resolves it is on. Tokens
// reach the middleware exclusively through the Bearer scheme — [gatedAuthMiddleware]
// hands it a nil token store when `bearer_enabled` is false — so with the
// scheme off a stored token authenticates nobody. Counting it anyway closed
// the onboarding wizard AND silenced [warnOnDormantOnboarding] on a daemon
// whose users table is empty (fresh volume, restored DB), leaving no way in
// at all. Local users keep counting regardless of `basic_enabled`: SPA session
// login resolves them without the header scheme.
func noAuthSourceConfigured(cfg *config.Config, src authSources) bool {
	bearer := cfg.North.REST.Auth.BearerAuthEnabled()
	switch {
	case src.localUsers > 0:
		return false // a persisted local admin exists
	case bearer && src.persistedTokens:
		return false // a persisted API token authenticates as its role
	case len(cfg.North.REST.Auth.Users) > 0:
		return false // a YAML-pinned local user exists
	case bearer && len(cfg.North.REST.Auth.Tokens) > 0:
		return false // a YAML-pinned bearer token (seeded into the store on upgrade)
	case ccuAuthEnabled(cfg.North.REST.Auth.CCU) && src.hasCentral:
		return false // CCU-delegated login is available (ADR 0043)
	case cfg.North.REST.Auth.OIDC.Enabled:
		return false // OIDC SSO is available
	default:
		return true
	}
}

// awaitShutdown blocks until ctx is cancelled, then runs the graceful
// shutdown sequence: emit the Matter ShutDown event (best-effort) and stop
// every north-bound server with a bounded timeout. Production wires ctx to
// SIGINT/SIGTERM via signal.NotifyContext in main.go; tests pass a
// context.WithCancel ctx so they can drive shutdown without signals.
func awaitShutdown(ctx context.Context, logger *slog.Logger, matter matterWiring, northBridges *northbridge.Registry) {
	logger.Info("daemon.ready")
	<-ctx.Done()
	// context.Cause is non-nil once ctx is cancelled (guaranteed here by the
	// receive above), but guard anyway so a future caller can't trip a nil
	// dereference.
	cause := "canceled"
	if c := context.Cause(ctx); c != nil {
		cause = c.Error()
	}
	logger.Info("daemon.shutdown", slog.String("cause", cause))

	// Matter ShutDown event: spec §11.1.6.2 mandates the cluster fires this
	// event when the bridge is about to terminate so commissioners can detach
	// gracefully. Best-effort — failure to emit is not fatal. matter.bi is nil
	// when matter is disabled.
	if matter.bi != nil {
		matter.bi.EmitShutDown()
	}

	//nolint:contextcheck // shutdown path must not inherit the cancelled daemon ctx
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Reverse-order StopAll: the REST/HTTP surface (registered last) stops
	// first — a graceful drain before the rest of the daemon tears down,
	// matching the old serverGroup.stopAll placement — then the webhook.
	northBridges.StopAll(shutdownCtx) //nolint:contextcheck // shutdown path: shutdownCtx intentionally not derived from daemon ctx
}

func buildTokenMap(cfg *config.Config) map[string]auth.Identity {
	out := make(map[string]auth.Identity, len(cfg.North.REST.Auth.Tokens))
	for token, role := range cfg.North.REST.Auth.Tokens {
		out[token] = auth.Identity{Subject: token, Role: auth.Role(role)}
	}
	return out
}

func buildCORS(cfg *config.Config) *middleware.CORSConfig {
	if len(cfg.North.REST.CORS) == 0 {
		return nil
	}
	return &middleware.CORSConfig{Origins: cfg.North.REST.CORS}
}

// mqttStack bundles every MQTT component the composition root builds
// and tears down as one unit.
type mqttStack struct {
	wiring    *mqtt.Wiring
	lifecycle *mqtt.Lifecycle
	client    mqtt.Client
}

// scheduleWeekProfileSink adapts [adapter.SchedulesDomain] to the
// [mqtt.WeekProfileSink] contract. The schedules domain resolves the
// device through the central registry passed at construction time, so
// the central + iface arguments from the MQTT topic are intentionally
// dropped here. Priority is also dropped: SchedulesDomain enforces the
// canonical per-DP priority via PutParamset.
type scheduleWeekProfileSink struct {
	sd *adapter.SchedulesDomain
}

func (a scheduleWeekProfileSink) SetActiveProfile(
	ctx context.Context, _, _, deviceAddress string, channelIdx int,
	profileKey string, _ hmenum.CommandPriority,
) error {
	return a.sd.SetActiveProfile(ctx, deviceAddress, channelIdx, profileKey)
}

// alarmSourceMQTT tags every alarm verb the raw MQTT command plane issues
// so the journal / audit trail attributes it correctly.
const alarmSourceMQTT = "mqtt"

// alarmMQTTSink adapts the daemon-level alarm engine onto the
// [mqtt.AlarmSink] contract. Zones are daemon-level, so the sink drives
// the engine directly without central scoping. The master verbs fan out
// best-effort over every zone; a per-zone arm failure is collected and
// also surfaced as a FAILED_TO_ARM event through onArmFailure, wired to
// the MQTT alarm publisher once it exists (see wireSystemStatusSubscribers).
type alarmMQTTSink struct {
	svc *alarm.Service

	mu           sync.RWMutex
	onArmFailure func(zoneID, zoneName string, mode hmenum.AlarmMode, blockers []hmevent.AlarmBlockerDetail)
}

// Compile-time proof the sink satisfies the command-subscriber contract.
var _ mqtt.AlarmSink = (*alarmMQTTSink)(nil)

// newAlarmMQTTSink returns a sink over svc, or nil when the alarm service
// is disabled — the command subscriber then leaves the alarm plane unwired.
func newAlarmMQTTSink(svc *alarm.Service) *alarmMQTTSink {
	if svc == nil {
		return nil
	}
	return &alarmMQTTSink{svc: svc}
}

// setArmFailureHook installs the FAILED_TO_ARM publisher. A nil hook
// disables the per-zone failure event.
func (s *alarmMQTTSink) setArmFailureHook(fn func(zoneID, zoneName string, mode hmenum.AlarmMode, blockers []hmevent.AlarmBlockerDetail)) {
	s.mu.Lock()
	s.onArmFailure = fn
	s.mu.Unlock()
}

func (s *alarmMQTTSink) Arm(ctx context.Context, zoneID string, mode hmenum.AlarmMode, code string) error {
	_, err := s.svc.Engine().Arm(ctx, zoneID, engine.ArmRequest{Mode: mode, Code: code, Source: alarmSourceMQTT})
	return err
}

func (s *alarmMQTTSink) Disarm(ctx context.Context, zoneID, code string) error {
	return s.svc.Engine().DisarmWithCode(ctx, zoneID, "", alarmSourceMQTT, code)
}

func (s *alarmMQTTSink) Silence(ctx context.Context, zoneID, code string) error {
	return s.svc.Engine().SilenceWithCode(ctx, zoneID, "", alarmSourceMQTT, code)
}

// Panic fires the engine's loud panic path (silent=false) for the zone —
// the HA TRIGGER command routes here (notes/concepts/alarm-concept.md §7).
func (s *alarmMQTTSink) Panic(ctx context.Context, zoneID string) error {
	return s.svc.Engine().PanicTrigger(ctx, zoneID, false, "", alarmSourceMQTT)
}

// MasterArm arms every zone best-effort. An zone that does not configure
// the requested mode is skipped silently (not a failure); any other arm
// error is collected and surfaces a FAILED_TO_ARM event with the blocking
// sensors.
func (s *alarmMQTTSink) MasterArm(ctx context.Context, mode hmenum.AlarmMode) error {
	eng := s.svc.Engine()
	var errs []error
	zones := eng.Zones()
	for i := range zones {
		a := &zones[i]
		if _, err := eng.Arm(ctx, a.ID, engine.ArmRequest{Mode: mode, Source: alarmSourceMQTT}); err != nil {
			if errors.Is(err, engine.ErrUnknownMode) {
				continue
			}
			errs = append(errs, err)
			s.emitArmFailure(a.ID, a.Name, mode, err)
		}
	}
	return errors.Join(errs...)
}

// ResetMotion clears the latched motion detectors of one zone.
func (s *alarmMQTTSink) ResetMotion(ctx context.Context, zoneID string) error {
	s.svc.Engine().ResetTriggeredMotion(ctx, zoneID, "", alarmSourceMQTT)
	return nil
}

// MasterResetMotion clears the latched motion detectors of every zone.
//
// Neither form returns an error: a per-device failure is counted in the
// engine result and journalled, and there is nothing an MQTT publisher
// could do with it that the journal and the triggered-count topic do
// not already say.
func (s *alarmMQTTSink) MasterResetMotion(ctx context.Context) error {
	s.svc.Engine().ResetTriggeredMotion(ctx, "", "", alarmSourceMQTT)
	return nil
}

// MasterDisarm disarms every zone best-effort.
func (s *alarmMQTTSink) MasterDisarm(ctx context.Context) error {
	eng := s.svc.Engine()
	var errs []error
	zones := eng.Zones()
	for i := range zones {
		a := &zones[i]
		if err := eng.Disarm(ctx, a.ID, "", alarmSourceMQTT); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// emitArmFailure publishes a FAILED_TO_ARM event, extracting the blocking
// sensors from a not-ready refusal.
func (s *alarmMQTTSink) emitArmFailure(zoneID, zoneName string, mode hmenum.AlarmMode, cause error) {
	s.mu.RLock()
	hook := s.onArmFailure
	s.mu.RUnlock()
	if hook == nil {
		return
	}
	var blockers []hmevent.AlarmBlockerDetail
	var nre *engine.NotReadyError
	if errors.As(cause, &nre) {
		blockers = nre.Details
	}
	hook(zoneID, zoneName, mode, blockers)
}

// buildOIDCRest discovers the IdP and constructs the REST OIDC deps backing
// the SPA's SSO flow (`/api/v1/auth/oidc/{start,callback}`). Returns nil when
// OIDC is disabled or discovery fails — the SPA then hides the SSO button.
func buildOIDCRest(cfg *config.Config, logger *slog.Logger, authDeps *handlers.AuthDeps) *handlers.OIDCDeps { //nolint:contextcheck // test callers outside owned set prevent ctx signature; buildOIDCClient uses context.Background() with a nolint inside
	client := buildOIDCClient(cfg, logger)
	if client == nil {
		return nil
	}
	return handlers.NewOIDCDeps(client, authDeps, logger)
}

func buildOIDCClient(cfg *config.Config, logger *slog.Logger) *oidc.Client {
	oc := cfg.North.REST.Auth.OIDC
	if !oc.Enabled || oc.Issuer == "" {
		return nil
	}
	//nolint:contextcheck // test callers outside owned set prevent threading ctx through here; discovery uses a short independent timeout
	discoCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := oidc.New(discoCtx, oidc.Config{
		Issuer:       oc.Issuer,
		ClientID:     oc.ClientID,
		ClientSecret: oc.ClientSecret,
		RedirectURL:  oc.RedirectURL,
		RoleClaim:    oc.RoleClaim,
	}, nil)
	if err != nil {
		logger.Warn("oidc.discovery", slog.String("err", err.Error()))
		return nil
	}
	return client
}

// buildMQTT assembles one MQTT stack generation (client, bridge, lifecycle)
// from cfg. Returns nil when MQTT is disabled.
//
// centralNames resolves the centrals the daemon currently serves. It is a
// function, not a slice, because a CCU adopted at runtime never reaches
// cfg.Centrals: the retained `bridge/health` payload is rebuilt on every
// AnnounceOnline and must name the live fleet. A nil func falls back to the
// boot config.
func buildMQTT(cfg *config.Config, logger *slog.Logger, collector *metrics.MqttCollector, channelHidden func(central, channelAddress string) bool, centralNames func() []string) *mqttStack {
	if !cfg.North.MQTT.Enabled {
		return nil
	}
	if centralNames == nil {
		centralNames = func() []string { return configuredCentralNames(cfg) }
	}
	var client mqtt.Client
	var connector mqtt.Connector
	if cfg.North.MQTT.BrokerURL == "" {
		// No broker configured but enabled → fall back to the
		// recording no-op client so developers can exercise the
		// wiring without a broker.
		client = mqtt.NewNoopClient()
	} else {
		// MQTT 5.0 is the transport default; operators pin
		// north.mqtt.protocol_version to "3.1.1" for brokers without
		// v5 support (no silent downgrade on the wire).
		var protoVersion mqtt.ProtocolVersion
		switch cfg.North.MQTT.ProtocolVersion {
		case "", "5":
			protoVersion = mqtt.ProtocolV50
		case "3.1.1":
			protoVersion = mqtt.ProtocolV311
		default:
			logger.Warn("mqtt.protocol_version.unknown",
				slog.String("value", cfg.North.MQTT.ProtocolVersion),
				slog.String("effect", "using MQTT 5.0"))
			protoVersion = mqtt.ProtocolV50
		}
		tcp := mqtt.NewTCPClient(mqtt.TCPConfig{
			BrokerURL: cfg.North.MQTT.BrokerURL,
			ClientID:  cfg.North.MQTT.ClientID,
			Username:  cfg.North.MQTT.Username,
			Password:  cfg.North.MQTT.Password,
			Will: &mqtt.Will{
				Topic:   buildLWTTopic(cfg),
				Payload: []byte("offline"),
				Retain:  true,
			},
			CleanStart:      true,
			ProtocolVersion: protoVersion,
			Logger:          logger,
		})
		client = tcp
		connector = tcp
	}

	startedAt := time.Now().UTC()
	// Circuit breaker between the bridge and the broker: during a
	// degraded-broker phase (link up, acks missing) publishes fail
	// fast with ErrCircuitOpen instead of each stalling on the
	// AckTimeout. Open-transitions feed the CircuitBreakerOpened
	// counter; the reconnect loop stays in charge of the link itself.
	pub := mqtt.NewBreaker(client, mqtt.BreakerConfig{
		OnStateChange: func(from, to mqtt.BreakerState) {
			logger.Warn("mqtt.breaker.state",
				slog.String("from", from.String()),
				slog.String("to", to.String()))
			if to == mqtt.BreakerOpen && collector != nil && collector.CircuitBreakerOpened != nil {
				collector.CircuitBreakerOpened.Inc()
			}
		},
	})
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        cfg.North.MQTT.TopicBase,
		CentralName: pickFirstCentral(cfg),
		// The retained sweeps compare candidate topics against the live
		// topics of every configured CCU, not just the default one — a
		// retired spelling of one CCU's name can be another CCU's live
		// topic, and clearing that would blank a value in use.
		CentralNames:       centralNames(),
		RawEnabled:         cfg.North.MQTT.RawEnabled,
		HADiscoveryEnabled: cfg.North.MQTT.DiscoveryEnabled,
		SubDevicesEnabled:  cfg.North.MQTT.SubDevicesEnabled,
		Locale:             cfg.Locale,
		HealthSupplier:     bridgeHealthSupplier(centralNames, startedAt),
		Collector:          collector,
		ChannelHidden:      channelHidden,
	}, pub).WithSubscriber(client)
	wiring := mqtt.NewWiring(bridge, logger)

	stack := &mqttStack{wiring: wiring, client: client}
	if connector != nil {
		lc := mqtt.NewLifecycle(mqtt.DefaultLifecycle(), connector)
		lc.OnConnect(func(ctx context.Context) {
			_ = bridge.AnnounceOnline(ctx)
			// Re-publish every cached Discovery config so HA recovers
			// all entities after a broker restart or clean-session
			// reconnect that wiped the retained store.
			_ = bridge.RepublishDiscovery(ctx)
		})
		stack.lifecycle = lc
	}
	return stack
}

// buildLWTTopic returns the retained LWT/online topic for the bridge,
// before a bridge exists to ask. It delegates to the same builder the online
// publish and every discovery availability entry use rather than re-assembling
// the string: a second assembly drifts from the declared topic the moment the
// base needs normalising, and the will is the one topic whose divergence stays
// invisible until the daemon is already gone.
func buildLWTTopic(cfg *config.Config) string {
	return mqtt.NewTopicBuilder(cfg.North.MQTT.TopicBase).BridgeStatus()
}

// buildRateLimitConfig projects the YAML config into the middleware
// shape, returning nil when rate limiting is disabled so the router
// skips the middleware wiring entirely.
func buildRateLimitConfig(cfg *config.Config) *middleware.RateLimitConfig {
	rl := cfg.North.REST.RateLimit
	if !rl.Enabled {
		return nil
	}
	return &middleware.RateLimitConfig{
		RequestsPerSecond: rl.RequestsPerSecond,
		Burst:             rl.Burst,
	}
}

// runtimeCapabilityDetector implements handlers.CapabilityDetector with
// the runtime state captured at daemon-Deps construction time.
type runtimeCapabilityDetector struct {
	mqtt              bool
	matter            bool
	oidc              bool
	ccuAuth           bool
	supervisedRestart bool
	mcp               bool
	mcpWrite          bool
	alarm             bool
	history           bool
	addonSelfUpdate   bool
}

func (r runtimeCapabilityDetector) HasMQTTDiscovery() bool     { return r.mqtt }
func (r runtimeCapabilityDetector) HasMatterBridge() bool      { return r.matter }
func (r runtimeCapabilityDetector) HasOIDC() bool              { return r.oidc }
func (r runtimeCapabilityDetector) HasCCUAuth() bool           { return r.ccuAuth }
func (r runtimeCapabilityDetector) HasSupervisedRestart() bool { return r.supervisedRestart }
func (r runtimeCapabilityDetector) HasMCP() bool               { return r.mcp }
func (r runtimeCapabilityDetector) HasMCPWrite() bool          { return r.mcp && r.mcpWrite }
func (r runtimeCapabilityDetector) HasAlarm() bool             { return r.alarm }
func (r runtimeCapabilityDetector) HasHistory() bool           { return r.history }
func (r runtimeCapabilityDetector) HasAddonSelfUpdate() bool   { return r.addonSelfUpdate }

// splitListenPort returns the TCP port from a Go net.Listen-style
// address (":8119", "0.0.0.0:8119", "[::]:8119"). Reports ok=false
// for addresses without a numeric port (e.g. Unix sockets, malformed
// strings) so the caller can degrade gracefully.
func splitListenPort(addr string) (int, bool) {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil || portStr == "" {
		return 0, false
	}
	var p int
	if _, err := fmt.Sscanf(portStr, "%d", &p); err != nil {
		return 0, false
	}
	if p <= 0 || p > 65535 {
		return 0, false
	}
	return p, true
}

// wsAllowedOrigins returns the list of origins the WebSocket handler
// should accept when CSRF protection is active. When CSRF is disabled
// (pure API-token setups without browser sessions) the list is empty
// and the handler skips the Origin check entirely. When CSRF is
// enabled the daemon derives the self-origin from its REST listen
// address so the embedded SPA — served on the same host:port — can
// always connect, and any CORS allowed-origins are appended as well.
func wsAllowedOrigins(cfg *config.Config) []string {
	if !cfg.North.REST.CSRFIsEnabled() {
		return nil
	}
	origins := make([]string, 0, 2)
	if port, ok := splitListenPort(cfg.North.REST.Listen); ok {
		origins = append(
			origins,
			fmt.Sprintf("http://localhost:%d", port),
			fmt.Sprintf("https://localhost:%d", port),
		)
	}
	origins = append(origins, cfg.North.REST.CORS...)
	return origins
}

// buildOpenAPIValidator loads the OpenAPI spec from the configured
// path and returns a ready validator, or nil when the file is missing
// or fails to parse. Failures are logged but never abort the daemon —
// a missing spec must not take the REST surface offline; operators
// see the warning and can either supply the spec or flip
// OpenAPIValidate off.
func buildOpenAPIValidator(cfg *config.Config, logger *slog.Logger) *middleware.OpenAPIValidator {
	path := cfg.North.REST.OpenAPISpecPath
	if path == "" {
		path = "assets/openapi.yaml"
	}
	specBytes, err := os.ReadFile(path)
	if err != nil {
		logger.Warn("openapi.spec.read",
			slog.String("path", path),
			slog.String("err", err.Error()),
			slog.String("hint", "set north.rest.openapi_spec_path to the deployed location"))
		return nil
	}
	v, err := middleware.NewOpenAPIValidator(middleware.OpenAPIValidatorConfig{
		Spec:     specBytes,
		Logger:   logger.With(slog.String("component", "openapi")),
		FailOpen: false,
	})
	if err != nil {
		logger.Warn("openapi.spec.parse",
			slog.String("path", path),
			slog.String("err", err.Error()))
		return nil
	}
	logger.Info("openapi.spec.loaded",
		slog.String("path", path))
	return v
}
