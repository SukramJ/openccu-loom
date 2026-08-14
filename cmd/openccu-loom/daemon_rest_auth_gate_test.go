// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// TestWireRESTHonoursAuthSchemeGates pins that the SQLite-backed middleware
// swap keeps the `basic_enabled` / `bearer_enabled` policy gates.
//
// The swap runs inside `if d.auditDB != nil`, and the app database is opened
// on every normal boot — so an ungated rebuild silently re-enabled a header
// scheme the operator had switched off, in essentially every deployment, with
// the daemon reporting nothing. The assertion is on the effect: a request
// carrying credentials for a disabled scheme must reach the handler with no
// identity attached.
func TestWireRESTHonoursAuthSchemeGates(t *testing.T) {
	t.Parallel()

	const (
		user   = "admin"
		pass   = "s3cret-pass"
		secret = "tok-secret"
	)

	cases := []struct {
		name          string
		basic, bearer bool
	}{
		{name: "both schemes on", basic: true, bearer: true},
		{name: "basic disabled", basic: false, bearer: true},
		{name: "bearer disabled", basic: true, bearer: false},
		{name: "both disabled", basic: false, bearer: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(t.TempDir(), "auth.db")))
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })

			sqUsers := sqlitestore.NewUserStore(db)
			if perr := sqUsers.Put(ctx, user, pass, auth.RoleAdmin); perr != nil {
				t.Fatalf("seed user: %v", perr)
			}
			sqTokens := sqlitestore.NewTokenStore(db)
			if _, perr := sqTokens.Import(ctx, secret, "test-token", auth.RoleAdmin); perr != nil {
				t.Fatalf("seed token: %v", perr)
			}

			cfg := config.Default()
			basic, bearer := tc.basic, tc.bearer
			cfg.North.REST.Auth.BasicEnabled = &basic
			cfg.North.REST.Auth.BearerEnabled = &bearer

			w := wireREST(ctx, restWiringDeps{
				cfg:        cfg,
				logger:     slog.New(slog.DiscardHandler),
				auditDB:    db,
				sqUsers:    sqUsers,
				sqTokens:   sqTokens,
				sqCentrals: sqlitestore.NewCentralsStore(db),
				users:      auth.NewMemoryUserStore(),
				tokens:     auth.NewMemoryTokenStore(nil),
			})
			if w.authMw == nil {
				t.Fatal("wireREST returned a nil auth middleware")
			}

			resolved := func(setCreds func(*http.Request)) bool {
				var got bool
				h := w.authMw.Resolve(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					_, got = auth.IdentityFrom(r.Context())
				}))
				req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody)
				setCreds(req)
				h.ServeHTTP(httptest.NewRecorder(), req)
				return got
			}

			gotBasic := resolved(func(r *http.Request) { r.SetBasicAuth(user, pass) })
			if gotBasic != tc.basic {
				t.Errorf("Basic credentials resolved = %v, want %v", gotBasic, tc.basic)
			}
			gotBearer := resolved(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+secret) })
			if gotBearer != tc.bearer {
				t.Errorf("Bearer credentials resolved = %v, want %v", gotBearer, tc.bearer)
			}
		})
	}
}
