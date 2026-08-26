// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	gosql "database/sql"
	"log/slog"
	"net/http"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// restWiringDeps carries the already-constructed subsystems the REST
// wiring phase reads. They are produced by earlier phases of the
// composition root (audit persistence, shared SQLite stores, the
// in-memory auth stores) and threaded through unchanged.
type restWiringDeps struct {
	cfg      *config.Config
	logger   *slog.Logger
	reg      *central.Registry
	auditBuf *audit.Buffer
	// auditRec is the recorder the durable audit trail is written through
	// (the buffer alone when no database opened). Every admin-grade surface
	// records through it; the raw auditBuf reaches only the in-memory ring,
	// which the audit read path stops consulting the moment SQLite is
	// present.
	auditRec      audit.Recorder
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
	// wsHub is the live WebSocket hub. This phase re-registers the chained
	// token store on it, because the in-band {op:"reauth"} frame resolves
	// through the hub's own store and the boot-time registration knows only
	// the YAML-seeded in-memory tokens.
	wsHub *ws.Hub
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
	statusMetrics *middleware.StatusMetrics
	auth          *handlers.AuthDeps
	configAdmin   handlers.ConfigAdminService
	userAdmin     handlers.UserAdminService
	tokenAdmin    handlers.TokenAdminService
	centralAdmin  handlers.CentralAdminService
	// tokenPurger drops every bearer token bound to a subject, across
	// both the durable and the in-memory token store. The user-delete
	// route reads it so a removed account keeps no live credential in
	// either.
	tokenPurger handlers.TokenPurger
	// authMw and authResolve carry the (possibly swapped) auth
	// middleware + REST resolver back to the caller. They equal the
	// input values when SQLite persistence is unavailable.
	authMw      *auth.Middleware
	authResolve func(next http.Handler) http.Handler
}

// seedLegacyAuthFromConfig performs the one-shot migration of config-file
// (YAML) basic-auth users and API tokens into the SQLite user/token stores.
// Now that credentials live only in SQLite (no longer round-tripped through
// the north.rest config section), this migration is what preserves an
// operator's YAML-pinned logins across an upgrade. It is idempotent: users
// are seeded only while the users table is empty and tokens only while the
// tokens table is empty, so a credential the operator later deletes via the
// CRUD surface is never resurrected on the next boot. A nil store is skipped.
func seedLegacyAuthFromConfig(
	ctx context.Context,
	users *sqlitestore.UserStore,
	tokens *sqlitestore.TokenStore,
	cfg *config.Config,
	logger *slog.Logger,
) {
	if users != nil && len(cfg.North.REST.Auth.Users) > 0 {
		if n, err := users.Count(ctx); err == nil && n == 0 {
			for name, pass := range cfg.North.REST.Auth.Users {
				if perr := users.Put(ctx, name, pass, auth.RoleAdmin); perr != nil {
					logger.Warn("auth.seed.user", slog.String("subject", name), slog.String("err", perr.Error()))
				}
			}
		}
	}
	if tokens != nil && len(cfg.North.REST.Auth.Tokens) > 0 {
		if n, err := tokens.Count(ctx); err == nil && n == 0 {
			for secret, role := range cfg.North.REST.Auth.Tokens {
				// Subject is a fixed non-secret label so the token secret never
				// lands in a plaintext column; the exact bearer value is
				// preserved (only its hash is stored) so existing API clients
				// keep authenticating after the migration.
				if _, perr := tokens.Import(ctx, secret, "config-token", auth.Role(role)); perr != nil {
					logger.Warn("auth.seed.token", slog.String("err", perr.Error()))
				}
			}
		}
	}
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
	// baseResolve is the auth + session chain this phase hands to the
	// router: the boot-time one, or the chained-store one built below
	// when the app database opened.
	baseResolve := d.restResolve

	// Optional CCU authentication provider (ADR 0043). Its position in
	// the login chain is governed by `auth.ccu.primary`: when primary
	// (the default when enabled) the CCU is tried first, otherwise last.
	// Either way local users remain a break-glass fallback because the
	// CCU store maps every failure to "unauthenticated".
	ccuStore := buildCCUAuthStore(cfg, d.reg, d.sqCentrals, logger)
	ccuPrimary := ccuAuthPrimary(cfg.North.REST.Auth.CCU)

	// The chain, not either store alone, is the set of stores a bearer
	// token can authenticate against: the durable one plus the in-memory
	// one the legacy POST /auth/tokens still writes into. It is therefore
	// also what an account deletion has to purge — purging only the
	// durable store leaves the deleted user a live credential in the
	// fallback the bearer chain reaches the moment SQLite misses.
	chainedTokens := auth.ChainedTokenStore{Primary: d.sqTokens, Secondary: d.tokens}

	if d.auditDB != nil && d.healthTracker != nil {
		stopProbe := sqlitestore.StartHealthProbe(ctx, d.auditDB, d.healthTracker, sqlitestore.DefaultProbeInterval)
		_ = stopProbe // daemon shutdown handled by the parent context cancel
	}

	if d.auditDB != nil {
		// One-shot migration: copy legacy config-file (YAML) basic-auth users
		// and API tokens into the SQLite stores. Credentials no longer live in
		// the north.rest config section, so this is what keeps an operator's
		// YAML-pinned logins working across the upgrade. Idempotent — it seeds
		// each table only while it is empty, so a later CRUD delete is not
		// resurrected on the next boot.
		seedLegacyAuthFromConfig(ctx, d.sqUsers, d.sqTokens, cfg, logger)

		// Same idempotent seed for the centrals table: if SQLite is
		// empty AND the YAML lists at least one CCU, copy the list
		// into the centrals table so the SPA's CCUs tab shows the
		// running config from the first boot. After that, edit
		// authoritatively via the SPA.
		if n, err := d.sqCentrals.Count(ctx); err == nil && n == 0 {
			for i := range cfg.Centrals {
				cc := &cfg.Centrals[i]
				row := sqlitestore.CentralRow{
					Name: cc.Name,
					// The southbound bring-up ran before this seed and has
					// already resolved the CCU's serial, but the backfill it
					// performs updates an existing row — and on a first boot
					// there is none yet, so it matched nothing and reported
					// success. Carrying the resolved serial into the seed is
					// what makes the first boot persist it; SSDP then
					// recognises a host-configured CCU (localhost, where a
					// host match can never succeed) by serial instead.
					Serial:                serialForSeed(d.reg, cc.Name),
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
		//
		// The scheme gates apply here exactly as they do to the
		// pre-swap middleware: SQLite is open on every normal boot, so
		// an ungated rebuild would discard `basic_enabled: false` /
		// `bearer_enabled: false` in essentially every deployment and
		// keep serving the scheme the operator switched off.
		authMw = gatedAuthMiddleware(
			cfg,
			loginChainWithCCU(d.sqUsers, d.users, ccuStore, ccuPrimary),
			chainedTokens,
		)
		// The WebSocket hub resolves the in-band {op:"reauth"} token through
		// its own store, and it was handed the in-memory one built from the
		// YAML `auth.tokens` map. Every token the operator mints through the
		// admin surface lives only in SQLite, so it authenticated the upgrade
		// (which goes through the chain above) and then failed reauth, which
		// drops the connection. Register the same chain here.
		if d.wsHub != nil && cfg.North.REST.Auth.BearerAuthEnabled() {
			d.wsHub.SetTokenStore(chainedTokens)
		}
		// Re-bind the chain after swapping the middleware so the REST
		// resolver picks up the chained stores.
		baseResolve = func(next http.Handler) http.Handler {
			return authMw.Resolve(d.sessionResolve(next))
		}
	}

	// The HA Ingress passthrough (ADR 0044) is the INNERMOST resolver so
	// real credentials (bearer/session/basic) always win — it only
	// injects an admin identity when nothing else resolved. It is wired
	// on both paths: gating it on the app database would leave an
	// Ingress-only deployment whose data directory failed to open with
	// no way to authenticate at all, which is exactly the boot the
	// operator needs the UI for.
	ingressMW := auth.IngressPassthrough(buildIngressTrust(cfg, logger), logger)
	restResolve := func(next http.Handler) http.Handler {
		return baseResolve(ingressMW(next))
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

	// Mark the session cookie Secure whenever the deployment terminates
	// TLS — directly (TLSEnabled) or behind a proxy the operator has
	// declared via CSRFSecure or an https public_url. Without this the
	// cookie rides plaintext requests and a downgraded request can leak it.
	secureCookie := cfg.North.REST.TLSEnabled() ||
		cfg.North.REST.CSRFSecure ||
		strings.HasPrefix(strings.ToLower(cfg.North.REST.PublicURL), "https://")
	// The token create/revoke entries go through the durable recorder, not
	// the raw ring: with a database present the audit read path serves
	// exclusively from SQL, so a buffer-only entry is invisible on
	// GET /api/v1/audit and gone entirely after a restart — leaving the
	// issuance and revocation of a credential with no trace at all.
	var auditRec audit.Recorder
	switch {
	case d.auditRec != nil:
		auditRec = d.auditRec
	case d.auditBuf != nil:
		auditRec = d.auditBuf
	}
	restAuth := &handlers.AuthDeps{
		Users:         d.users,
		Sessions:      d.sessions,
		Tokens:        d.tokens,
		Secure:        secureCookie,
		AuditRecorder: auditRec,
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
		statusMetrics: restStatusMetrics,
		auth:          restAuth,
		configAdmin:   configAdminSvc,
		userAdmin:     userAdminSvc,
		tokenAdmin:    tokenAdminSvc,
		centralAdmin:  centralAdminSvc,
		tokenPurger:   chainedTokens,
		authMw:        authMw,
		authResolve:   restResolve,
	}
}

// serialForSeed returns the serial the southbound bring-up resolved for
// centralName, or "" when the central is not registered yet or its CCU has not
// answered. Empty is the documented value for a row whose serial is unknown,
// and the periodic backfill fills it in later.
func serialForSeed(reg *central.Registry, centralName string) string {
	if reg == nil {
		return ""
	}
	u, ok := reg.Get(centralName)
	if !ok || u == nil {
		return ""
	}
	return u.SystemInformation().Serial
}
