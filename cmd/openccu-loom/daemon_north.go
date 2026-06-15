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

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/auth/oidc"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/north/ui"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type serverGroup struct {
	logger  *slog.Logger
	mu      sync.Mutex
	servers map[string]*rest.Server
	errs    chan error
}

func newServerGroup(logger *slog.Logger) *serverGroup {
	return &serverGroup{logger: logger, servers: make(map[string]*rest.Server), errs: make(chan error, 4)}
}

func (g *serverGroup) add(name string, s *rest.Server) {
	g.mu.Lock()
	g.servers[name] = s
	g.mu.Unlock()
}

func (g *serverGroup) startAll() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for name, s := range g.servers {
		go func() {
			if err := s.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				g.logger.Error("server.exit", slog.String("name", name), slog.String("err", err.Error()))
				g.errs <- err
			}
		}()
	}
	// Wait a beat so Listen gets a chance to fail-fast on bind issues.
	time.Sleep(20 * time.Millisecond)
	return nil
}

func (g *serverGroup) stopAll(ctx context.Context) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for name, s := range g.servers {
		if err := s.Shutdown(ctx); err != nil {
			g.logger.Warn("server.shutdown", slog.String("name", name), slog.String("err", err.Error()))
		}
	}
}

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
func buildAuthStores(cfg *config.Config, wsHub *ws.Hub) authStores {
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
	wsHub.SetTokenStore(tokens)
	sessions := auth.NewSessionStore()
	authMw := auth.NewMiddleware(users, tokens)

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

// uiMountDeps bundles the live subsystems the browser-facing UI router needs.
type uiMountDeps struct {
	healthAdapter *adapter.HealthAdapter
	catalogs      *i18n.Catalogs
	users         *auth.MemoryUserStore
	sessions      *auth.SessionStore
	sqUsers       *sqlitestore.UserStore
	sqCentrals    *sqlitestore.CentralsStore
	sqSections    *sqlitestore.ConfigSectionStore
}

// mountUIServer stands up the browser-facing UI router + server when the UI is
// enabled (no-op otherwise). The first-run setup wizard is wired only when the
// SQLite stores are available; otherwise the wizard stays disabled and the UI
// serves the login/SPA surface alone.
func mountUIServer(cfg *config.Config, logger *slog.Logger, servers *serverGroup, d uiMountDeps) {
	if !cfg.North.UI.IsEnabled() {
		return
	}
	var setupDeps *ui.SetupWizardDeps
	if d.sqUsers != nil {
		setupDeps = &ui.SetupWizardDeps{
			Users:    d.sqUsers,
			Centrals: d.sqCentrals,
			Sections: d.sqSections,
			Sessions: ui.NewSetupSessionStore(),
		}
	}
	uiRouter := ui.NewRouter(ui.Deps{
		Logger:      logger,
		Lang:        cfg.Locale,
		Health:      d.healthAdapter,
		Catalogs:    d.catalogs,
		Auth:        &ui.AuthDeps{Users: d.users, Sessions: d.sessions, Secure: false},
		Setup:       setupDeps,
		OIDC:        buildOIDC(cfg, logger),
		AuthResolve: auth.SessionMiddleware(d.sessions),
		AuthRequire: nil, // UI is browser-facing, wizard runs unauthenticated
	})
	servers.add("ui", rest.NewServer(cfg.North.UI.Listen, uiRouter, logger))
}

// awaitShutdown blocks until ctx is cancelled, then runs the graceful
// shutdown sequence: emit the Matter ShutDown event (best-effort) and stop
// every north-bound server with a bounded timeout. Production wires ctx to
// SIGINT/SIGTERM via signal.NotifyContext in main.go; tests pass a
// context.WithCancel ctx so they can drive shutdown without signals.
func awaitShutdown(ctx context.Context, logger *slog.Logger, matter matterWiring, servers *serverGroup) {
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
	servers.stopAll(shutdownCtx) //nolint:contextcheck // shutdown path: shutdownCtx intentionally not derived from daemon ctx
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

// buildOIDC discovers the IdP and constructs the UI OIDC deps.
// Returns nil when OIDC is disabled or discovery fails — the daemon
// then renders the login page without the SSO button.
func buildOIDC(cfg *config.Config, logger *slog.Logger) *ui.OIDCDeps { //nolint:contextcheck // test callers outside owned set prevent ctx signature; buildOIDCClient uses context.Background() with a nolint inside
	client := buildOIDCClient(cfg, logger)
	if client == nil {
		return nil
	}
	return ui.NewOIDCDeps(client)
}

// buildOIDCRest reuses the same OIDC client for the REST mount so
// SPA-driven SSO and the HTMX wizard share state and credentials.
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

func buildMQTT(cfg *config.Config, logger *slog.Logger, collector *metrics.MqttCollector) *mqttStack {
	if !cfg.North.MQTT.Enabled {
		return nil
	}
	var client mqtt.Client
	var connector mqtt.Connector
	if cfg.North.MQTT.BrokerURL == "" {
		// No broker configured but enabled → fall back to the
		// recording no-op client so developers can exercise the
		// wiring without a broker.
		client = mqtt.NewNoopClient()
	} else {
		tcp := mqtt.NewTCPClient(mqtt.TCPConfig{
			BrokerURL:    cfg.North.MQTT.BrokerURL,
			ClientID:     cfg.North.MQTT.ClientID,
			Username:     cfg.North.MQTT.Username,
			Password:     cfg.North.MQTT.Password,
			WillTopic:    buildLWTTopic(cfg),
			WillPayload:  []byte("offline"),
			WillRetain:   true,
			CleanSession: true,
			Logger:       logger,
		})
		client = tcp
		connector = tcp
	}

	startedAt := time.Now().UTC()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               cfg.North.MQTT.TopicBase,
		CentralName:        pickFirstCentral(cfg),
		RawEnabled:         cfg.North.MQTT.RawEnabled,
		HADiscoveryEnabled: cfg.North.MQTT.DiscoveryEnabled,
		SubDevicesEnabled:  cfg.North.MQTT.SubDevicesEnabled,
		HealthSupplier:     bridgeHealthSupplier(cfg, startedAt),
		Collector:          collector,
	}, client)
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

// buildLWTTopic assembles the retained LWT/online topic for the
// bridge. Mirrors mqtt.TopicBuilder.BridgeStatus without requiring
// bridge instantiation first.
func buildLWTTopic(cfg *config.Config) string {
	base := cfg.North.MQTT.TopicBase
	if base == "" {
		base = "openccu-loom"
	}
	return base + "/bridge/status"
}

// bridgeHealthSupplier returns a closure the MQTT bridge invokes on
// every AnnounceOnline to compose the `<base>/bridge/health` payload.
// The body carries operator-visible metadata that is more useful than
// a bare "online" flag — build identity, daemon boot timestamp, and
// the configured centrals.
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
	supervisedRestart bool
	mcp               bool
	mcpWrite          bool
}

func (r runtimeCapabilityDetector) HasMQTTDiscovery() bool     { return r.mqtt }
func (r runtimeCapabilityDetector) HasMatterBridge() bool      { return r.matter }
func (r runtimeCapabilityDetector) HasOIDC() bool              { return r.oidc }
func (r runtimeCapabilityDetector) HasSupervisedRestart() bool { return r.supervisedRestart }
func (r runtimeCapabilityDetector) HasMCP() bool               { return r.mcp }
func (r runtimeCapabilityDetector) HasMCPWrite() bool          { return r.mcp && r.mcpWrite }

// detectSupervisedRestart reports whether the daemon is running
// under a supervisor that will restart it after a clean shutdown.
// The check is cheap + heuristic — we do not try to verify the
// supervisor's restart policy; we look for tight markers that
// imply the daemon's IMMEDIATE parent (or runtime) is a
// supervisor, not just the terminal session.
//
// Signals (any one fires):
//
//   - OPENCCU_LOOM_SUPERVISOR=1 — explicit operator override.
//   - JOURNAL_STREAM set AND getppid()==1 — systemd attached the
//     daemon's stdout/stderr to journald and re-parented it to PID 1
//     (the unambiguous "I am a systemd service" signal; INVOCATION_ID
//     alone is too lax because gnome-terminal inherits it).
//   - getppid()==1 AND /run/systemd/system exists — fallback when
//     JOURNAL_STREAM was suppressed but systemd is still PID 1.
//   - KUBERNETES_SERVICE_HOST set — Kubernetes injects it into
//     every Pod and the kubelet always restarts dead containers.
//   - /.dockerenv exists — Docker / Podman containers; restart
//     policy is operator-chosen but presence is the usual signal.
//
// Missing all of these means the binary is running on bare metal
// from a shell. The SPA disables the Restart-Daemon button in
// that case so an operator does not accidentally take the daemon
// permanently offline.
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

// singleCentralName returns the name of the registered central when
// the registry contains exactly one entry, otherwise the empty
// string. The REST router uses the result to pre-populate
// `central_name` in every request's [reqctx.RequestContext]. Multi-
// central deployments leave the request scope unset and rely on
// per-handler resolution.
