// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// TestWireREST_SessionCookieSecureFlag guards the session-cookie Secure
// derivation in wireREST: the cookie must be marked Secure whenever the
// deployment terminates TLS directly, or the operator has declared a
// TLS-terminating reverse proxy via CSRFSecure or an https PublicURL —
// otherwise the cookie would ride plaintext requests and a downgraded
// request could leak it.
func TestWireREST_SessionCookieSecureFlag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		mutate     func(cfg *config.Config)
		wantSecure bool
	}{
		{
			name:       "plain HTTP, no CSRF secure, no public URL",
			mutate:     func(_ *config.Config) {},
			wantSecure: false,
		},
		{
			name: "TLS cert+key configured",
			mutate: func(cfg *config.Config) {
				cfg.North.REST.TLSCertFile = "cert.pem"
				cfg.North.REST.TLSKeyFile = "key.pem"
			},
			wantSecure: true,
		},
		{
			name: "CSRFSecure set explicitly",
			mutate: func(cfg *config.Config) {
				cfg.North.REST.CSRFSecure = true
			},
			wantSecure: true,
		},
		{
			name: "https public URL",
			mutate: func(cfg *config.Config) {
				cfg.North.REST.PublicURL = "https://loom.example.de"
			},
			wantSecure: true,
		},
		{
			name: "http (non-TLS) public URL does not imply secure",
			mutate: func(cfg *config.Config) {
				cfg.North.REST.PublicURL = "http://loom.example.de"
			},
			wantSecure: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.Default()
			tc.mutate(cfg)

			w := wireREST(context.Background(), restWiringDeps{
				cfg:    cfg,
				logger: slog.New(slog.DiscardHandler),
			})
			if w.auth == nil {
				t.Fatal("wireREST: AuthDeps is nil")
			}
			if w.auth.Secure != tc.wantSecure {
				t.Errorf("AuthDeps.Secure = %v, want %v", w.auth.Secure, tc.wantSecure)
			}
		})
	}
}

// TestWireRESTTokenAuditReachesTheDurableTrail pins that a token issued or
// revoked through the REST surface lands in the audit trail an operator can
// actually read.
//
// The AuthDeps recorder used to be the raw in-memory ring. With a database
// present the audit read path serves exclusively from SQL, so the entries for
// the issuance and the revocation of a credential appeared nowhere in
// GET /api/v1/audit or its CSV export, and a restart erased them entirely.
// The assertion is therefore on the durable store, not on the buffer — a
// buffer-level check passes with the defect in place.
func TestWireRESTTokenAuditReachesTheDurableTrail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(t.TempDir(), "audit.db")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	logger := slog.New(slog.DiscardHandler)
	buf := audit.NewBuffer(500)
	rec, _, stopSink := wireAuditPersistenceWithDB(db, buf, logger)
	t.Cleanup(stopSink)

	w := wireREST(ctx, restWiringDeps{
		cfg:        config.Default(),
		logger:     logger,
		auditBuf:   buf,
		auditRec:   rec,
		auditDB:    db,
		sqUsers:    sqlitestore.NewUserStore(db),
		sqTokens:   sqlitestore.NewTokenStore(db),
		sqCentrals: sqlitestore.NewCentralsStore(db),
		users:      auth.NewMemoryUserStore(),
		tokens:     auth.NewMemoryTokenStore(nil),
	})
	if w.auth == nil {
		t.Fatal("wireREST: AuthDeps is nil")
	}

	body := strings.NewReader(`{"subject":"ci","role":"admin"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens", body)
	res := httptest.NewRecorder()
	handlers.CreateToken(w.auth).ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("CreateToken status = %d, want %d (%s)", res.Code, http.StatusCreated, res.Body.String())
	}

	store := sqlitestore.NewAuditStore(db)
	deadline := time.Now().Add(3 * time.Second)
	for {
		entries, qerr := store.Query(ctx, audit.Query{Limit: 50})
		if qerr != nil {
			t.Fatalf("audit store Query: %v", qerr)
		}
		if slices.ContainsFunc(entries, func(e audit.Entry) bool { return e.Action == audit.ActionTokenCreate }) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q entry in the durable audit trail; got %d rows", audit.ActionTokenCreate, len(entries))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestWireRESTRegistersTheChainedTokenStoreOnTheWSHub pins that the WebSocket
// hub resolves every token the HTTP upgrade accepts.
//
// The hub is handed a token store once at boot, built from the YAML
// `auth.tokens` map alone, and the in-band {op:"reauth"} frame resolves
// exclusively through it. A token minted on the live admin surface exists only
// in SQLite, so it authenticated the upgrade — which goes through the chained
// store — and then failed reauth, which closes the connection: a credential
// valid everywhere else dropped the socket.
func TestWireRESTRegistersTheChainedTokenStoreOnTheWSHub(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(t.TempDir(), "tokens.db")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sqTokens := sqlitestore.NewTokenStore(db)
	created, err := sqTokens.Create(ctx, sqlitestore.CreateInput{Subject: "spa", Role: auth.RoleAdmin})
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}

	cfg := config.Default()
	hub := ws.NewHub()
	// The boot-time registration: the in-memory store built from the YAML
	// token map, which knows nothing about the SQLite-minted credential.
	hub.SetTokenStore(auth.NewMemoryTokenStore(nil))
	if _, err := hub.TokenStore().AuthenticateToken(ctx, created.Token); err == nil {
		t.Fatal("the in-memory store resolved a SQLite-only token; the test cannot prove anything")
	}

	wireREST(ctx, restWiringDeps{
		cfg:        cfg,
		logger:     slog.New(slog.DiscardHandler),
		auditDB:    db,
		sqUsers:    sqlitestore.NewUserStore(db),
		sqTokens:   sqTokens,
		sqCentrals: sqlitestore.NewCentralsStore(db),
		users:      auth.NewMemoryUserStore(),
		tokens:     auth.NewMemoryTokenStore(nil),
		wsHub:      hub,
	})

	id, err := hub.TokenStore().AuthenticateToken(ctx, created.Token)
	if err != nil {
		t.Fatalf("the WebSocket hub rejects a token the REST upgrade accepts: %v", err)
	}
	if id.Subject != "spa" || id.Role != auth.RoleAdmin {
		t.Errorf("identity = %+v, want subject=spa role=admin", id)
	}
}

// TestWireRESTPurgeCoversTheLegacyTokenStore pins that the purger the
// user-delete route is handed drops a subject's tokens from *both* stores
// a bearer credential can authenticate against.
//
// The bearer chain falls back to the in-memory store whenever SQLite
// misses, and the legacy POST /auth/tokens route writes only there. A
// purger that knows the durable table alone therefore leaves a deleted
// account holding a live token — the deletion of its SQLite rows is the
// very condition that sends the next request to the fallback. The subject
// is minted with a different casing on purpose: the account can only be
// addressed by its canonical spelling.
func TestWireRESTPurgeCoversTheLegacyTokenStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(t.TempDir(), "purge.db")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sqTokens := sqlitestore.NewTokenStore(db)
	durable, err := sqTokens.Create(ctx, sqlitestore.CreateInput{Subject: "bob", Role: auth.RoleAdmin})
	if err != nil {
		t.Fatalf("mint durable token: %v", err)
	}

	memTokens := auth.NewMemoryTokenStore(nil)
	w := wireREST(ctx, restWiringDeps{
		cfg:        config.Default(),
		logger:     slog.New(slog.DiscardHandler),
		auditDB:    db,
		sqUsers:    sqlitestore.NewUserStore(db),
		sqTokens:   sqTokens,
		sqCentrals: sqlitestore.NewCentralsStore(db),
		users:      auth.NewMemoryUserStore(),
		tokens:     memTokens,
	})
	if w.tokenPurger == nil {
		t.Fatal("wireREST: no token purger for the user-delete route")
	}

	// Mint through the legacy route, exactly as an operator would.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens",
		strings.NewReader(`{"subject":"Bob","role":"admin"}`))
	res := httptest.NewRecorder()
	handlers.CreateToken(w.auth).ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("CreateToken status = %d, want %d (%s)", res.Code, http.StatusCreated, res.Body.String())
	}
	var legacy handlers.CreateTokenResponse
	if err := json.Unmarshal(res.Body.Bytes(), &legacy); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}

	n, err := w.tokenPurger.DeleteBySubject(ctx, "bob")
	if err != nil {
		t.Fatalf("DeleteBySubject: %v", err)
	}
	if n != 2 {
		t.Errorf("purged %d tokens, want both the durable and the legacy one", n)
	}
	if _, err := memTokens.AuthenticateToken(ctx, legacy.Token); err == nil {
		t.Error("the legacy token still authenticates after its account was purged")
	}
	if _, err := sqTokens.AuthenticateToken(ctx, durable.Token); err == nil {
		t.Error("the durable token still authenticates after its account was purged")
	}
}

// TestWireREST_IngressPassthroughWiredWithoutTheAppDatabase pins ADR
// 0044's passthrough on the boot path that has no app database. That
// path keeps the boot-time auth chain, and an Ingress deployment whose
// data directory failed to open resolves no identity at all when the
// passthrough is skipped — every request 401s and the operator cannot
// reach the UI that would show them why.
func TestWireREST_IngressPassthroughWiredWithoutTheAppDatabase(t *testing.T) {
	t.Setenv("OPENCCU_LOOM_SUPERVISOR", "1")

	cfg := config.Default()
	enabled := true
	cfg.North.REST.Auth.HAIngress.Enabled = &enabled
	cfg.North.REST.Auth.HAIngress.TrustedProxyCIDR = "127.0.0.0/8"

	passthrough := func(next http.Handler) http.Handler { return next }
	w := wireREST(context.Background(), restWiringDeps{
		cfg:            cfg,
		logger:         slog.New(slog.DiscardHandler),
		authMw:         auth.NewMiddleware(auth.NewMemoryUserStore(), auth.NewMemoryTokenStore(nil)),
		restResolve:    passthrough,
		sessionResolve: passthrough,
		// auditDB stays nil: the app database could not be opened.
	})
	if w.authResolve == nil {
		t.Fatal("wireREST: authResolve is nil")
	}

	var got auth.Identity
	var resolved bool
	h := w.authResolve(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, resolved = auth.IdentityFrom(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/abc")
	req.RemoteAddr = "127.0.0.1:41234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !resolved {
		t.Fatal("no identity resolved: the HA Ingress passthrough is not wired on this boot path")
	}
	if got.Scheme != auth.SchemeIngress {
		t.Fatalf("identity scheme = %q, want %q", got.Scheme, auth.SchemeIngress)
	}
}
