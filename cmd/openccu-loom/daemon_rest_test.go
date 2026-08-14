// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
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
