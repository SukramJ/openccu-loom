// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	gosql "database/sql"
	"log/slog"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// restWiringDeps carries the already-constructed subsystems the REST
// wiring phase reads. They are produced by earlier phases of the
// composition root (audit persistence, shared SQLite stores, the
// in-memory auth stores) and threaded through unchanged.
type restWiringDeps struct {
	cfg           *config.Config
	logger        *slog.Logger
	reg           *central.Registry
	auditBuf      *audit.Buffer
	auditDB       *gosql.DB
	healthTracker *health.Tracker
	configStore   *configstore.Store
	sqUsers       *sqlitestore.UserStore
	sqCentrals    *sqlitestore.CentralsStore
	sqTokens      *sqlitestore.TokenStore
	sqSections    *sqlitestore.ConfigSectionStore
	users         *auth.MemoryUserStore
	tokens        *auth.MemoryTokenStore
	sessions      *auth.SessionStore
	// authMw and sessionResolve are the auth middleware + session
	// resolver built by the auth phase. When SQLite persistence is
	// available this phase swaps authMw for the chained stores and
	// re-binds the REST resolver; the updated values come back via
	// restWiring.
	authMw         *auth.Middleware
	restResolve    func(next http.Handler) http.Handler
	sessionResolve func(next http.Handler) http.Handler
}

// restWiring is the result of the REST wiring phase. It surfaces the
// values the REST router/server build (and the shutdown path) read
// later in the composition root.
type restWiring struct {
	servers       *serverGroup
	statusMetrics *middleware.StatusMetrics
	auth          *handlers.AuthDeps
	configAdmin   handlers.ConfigAdminService
	userAdmin     handlers.UserAdminService
	tokenAdmin    handlers.TokenAdminService
	centralAdmin  handlers.CentralAdminService
	// authMw and authResolve carry the (possibly swapped) auth
	// middleware + REST resolver back to the caller. They equal the
	// input values when SQLite persistence is unavailable.
	authMw      *auth.Middleware
	authResolve func(next http.Handler) http.Handler
}

// wireREST performs the REST wiring phase of the composition root: it
// builds the server group, starts the SQLite health probe, runs the
// idempotent one-shot seeds for the users and centrals tables, layers
// the SQLite stores on top of the in-memory auth stores, wires the REST
// status metrics into the health tracker, and assembles the REST auth
// deps + admin services.
//
// It is a behavior-preserving extraction: same operations, order and
// nil-handling as the inline phase. There are no inline defers in the
// phase, so it returns no teardown.
//
//nolint:funlen,gocognit // composition/wiring: REST handler + router assembly
func wireREST(ctx context.Context, d restWiringDeps) restWiring {
	cfg := d.cfg
	logger := d.logger

	authMw := d.authMw
	restResolve := d.restResolve

	// Optional CCU authentication provider (ADR 0043). Its position in
	// the login chain is governed by `auth.ccu.primary`: when primary
	// (the default when enabled) the CCU is tried first, otherwise last.
	// Either way local users remain a break-glass fallback because the
	// CCU store maps every failure to "unauthenticated".
	ccuStore := buildCCUAuthStore(cfg, d.reg, logger)
	ccuPrimary := ccuAuthPrimary(cfg.North.REST.Auth.CCU)

	servers := newServerGroup(logger)
	if d.auditDB != nil && d.healthTracker != nil {
		stopProbe := sqlitestore.StartHealthProbe(ctx, d.auditDB, d.healthTracker, sqlitestore.DefaultProbeInterval)
		_ = stopProbe // daemon shutdown handled by the parent context cancel
	}

	if d.auditDB != nil {
		// One-shot seed: if SQLite users table is empty AND the
		// YAML carries legacy auth.users, copy them in so the
		// admin-edit surface starts pre-populated. Idempotent —
		// subsequent boots see Count() > 0 and skip the seed.
		if n, err := d.sqUsers.Count(ctx); err == nil && n == 0 {
			for name, pass := range cfg.North.REST.Auth.Users {
				if perr := d.sqUsers.Put(ctx, name, pass, auth.RoleAdmin); perr != nil {
					logger.Warn("auth.seed.user", slog.String("subject", name), slog.String("err", perr.Error()))
				}
			}
		}

		// Same idempotent seed for the centrals table: if SQLite is
		// empty AND the YAML lists at least one CCU, copy the list
		// into the centrals table so the SPA's CCUs tab shows the
		// running config from the first boot. After that, edit
		// authoritatively via the SPA.
		if n, err := d.sqCentrals.Count(ctx); err == nil && n == 0 {
			for i := range cfg.Centrals {
				cc := &cfg.Centrals[i]
				row := sqlitestore.CentralRow{
					Name:                  cc.Name,
					Host:                  cc.Host,
					Port:                  cc.Port,
					JSONRPCPort:           cc.JSONRPCPort,
					Username:              cc.Username,
					PasswordPlain:         cc.Password, // YAML password becomes the SQLite default
					TLS:                   cc.TLS,
					TLSInsecureSkipVerify: cc.TLSInsecureSkipVerify,
					PrimaryInterface:      cc.PrimaryInterface,
					Interfaces:            cc.Interfaces,
					Ports:                 cc.Ports,
					Visibility:            cc.Visibility,
					Enabled:               true,
				}
				if perr := d.sqCentrals.Put(ctx, row); perr != nil {
					logger.Warn("centrals.seed", slog.String("name", cc.Name), slog.String("err", perr.Error()))
				}
			}
		}

		// Layer SQLite stores on top of the Memory stores for
		// authentication so wizard-created users + YAML-pinned
		// users both resolve. The chain prefers SQLite; falls back
		// to Memory only on a clean "unauthenticated" miss.
		authMw = auth.NewMiddleware(
			loginChainWithCCU(d.sqUsers, d.users, ccuStore, ccuPrimary),
			auth.ChainedTokenStore{Primary: d.sqTokens, Secondary: d.tokens},
		)
		// Re-bind the resolver after swapping the middleware so the
		// REST chain picks up the chained stores. The HA Ingress
		// passthrough (ADR 0044) is the INNERMOST resolver so real
		// credentials (bearer/session/basic) always win — it only
		// injects an admin identity when nothing else resolved.
		ingressMW := auth.IngressPassthrough(buildIngressTrust(cfg, logger), logger)
		restResolve = func(next http.Handler) http.Handler {
			return authMw.Resolve(d.sessionResolve(ingressMW(next)))
		}
	}

	// REST status metrics — 5xx/4xx counters surfaced as health
	// gauges. Wired into the chi middleware chain via Deps.StatusMetrics
	// and read back through RegisterGauge so the diagnostics dump and
	// the SPA's Diagnostics view can render the values.
	restStatusMetrics := middleware.NewStatusMetrics()
	if d.healthTracker != nil {
		sm := restStatusMetrics
		d.healthTracker.RegisterGauge("rest.5xx",
			func() float64 { return float64(sm.ServerErrors()) })
		d.healthTracker.RegisterGauge("rest.4xx",
			func() float64 { return float64(sm.ClientErrors()) })
		d.healthTracker.RegisterGauge("rest.requests_total",
			func() float64 { return float64(sm.TotalRequests()) })
	}

	restAuth := &handlers.AuthDeps{
		Users:         d.users,
		Sessions:      d.sessions,
		Tokens:        d.tokens,
		Secure:        false, // dev/plain HTTP; flip when TLS is wired
		AuditRecorder: d.auditBuf,
	}
	// When SQLite-backed user persistence is available, route the
	// /auth/login resolver through the chained store so
	// wizard-created admins and YAML-pinned users both
	// authenticate. The legacy /auth/users read path continues to
	// hit the in-memory snapshot via AuthDeps.Users.
	if d.sqUsers != nil {
		restAuth.LoginUsers = loginChainWithCCU(d.sqUsers, d.users, ccuStore, ccuPrimary)
	}

	// Wave-C admin services backed by the SQLite stores opened
	// above. Each handler-side interface (ConfigAdminService /
	// UserAdminService / TokenAdminService / CentralAdminService)
	// is satisfied directly by the corresponding *sqlite.Store —
	// no extra adapter required.
	var (
		configAdminSvc  handlers.ConfigAdminService
		userAdminSvc    handlers.UserAdminService
		tokenAdminSvc   handlers.TokenAdminService
		centralAdminSvc handlers.CentralAdminService
	)
	if d.configStore != nil {
		configAdminSvc = configAdminAdapter{store: d.configStore, sections: d.sqSections}
		userAdminSvc = d.sqUsers
		tokenAdminSvc = d.sqTokens
		centralAdminSvc = d.sqCentrals
	}

	return restWiring{
		servers:       servers,
		statusMetrics: restStatusMetrics,
		auth:          restAuth,
		configAdmin:   configAdminSvc,
		userAdmin:     userAdminSvc,
		tokenAdmin:    tokenAdminSvc,
		centralAdmin:  centralAdminSvc,
		authMw:        authMw,
		authResolve:   restResolve,
	}
}
