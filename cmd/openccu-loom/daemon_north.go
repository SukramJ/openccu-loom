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
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
	"github.com/SukramJ/openccu-loom/internal/north/ui"
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
}

func (r runtimeCapabilityDetector) HasMQTTDiscovery() bool     { return r.mqtt }
func (r runtimeCapabilityDetector) HasMatterBridge() bool      { return r.matter }
func (r runtimeCapabilityDetector) HasOIDC() bool              { return r.oidc }
func (r runtimeCapabilityDetector) HasSupervisedRestart() bool { return r.supervisedRestart }

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
