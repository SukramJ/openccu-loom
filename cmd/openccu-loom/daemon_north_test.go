// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

func TestFirstRunNeedsSetup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		localUserCount  int
		persistedTokens bool
		hasCentral      bool
		mutate          func(*config.Config)
		want            bool
	}{
		{
			name:           "local user present",
			localUserCount: 1,
			want:           false,
		},
		{
			name:           "YAML user present",
			localUserCount: 0,
			mutate: func(c *config.Config) {
				c.North.REST.Auth.Users = map[string]string{"admin": "x"}
			},
			want: false,
		},
		{
			// A token-only deployment authenticates through the bearer
			// resolver, which runs first in the chain. Reporting first run
			// here leaves the unauthenticated POST /api/v1/setup open to
			// anyone on the network.
			name:           "YAML bearer token present",
			localUserCount: 0,
			mutate: func(c *config.Config) {
				c.North.REST.Auth.Tokens = map[string]string{"s3cr3t": "admin"}
			},
			want: false,
		},
		{
			// Same credential after the one-shot migration into SQLite, and
			// the shape an SPA-minted token has from the start.
			name:            "persisted API token present",
			localUserCount:  0,
			persistedTokens: true,
			want:            false,
		},
		{
			// A token whose scheme is switched off resolves nobody, but it
			// is still a credential the operator configured — and switching
			// the scheme back on is a config edit, while POST /api/v1/setup
			// is reachable by anyone who reaches the listener. Reporting
			// first run here trades a dormant scheme for anonymous admin
			// creation; the state is logged instead (auth.scheme.dormant).
			name:           "YAML bearer token present but the bearer scheme is off",
			localUserCount: 0,
			mutate: func(c *config.Config) {
				c.North.REST.Auth.Tokens = map[string]string{"s3cr3t": "admin"}
				c.North.REST.Auth.BearerEnabled = ptrBool(false)
			},
			want: false,
		},
		{
			name:            "persisted API token present but the bearer scheme is off",
			localUserCount:  0,
			persistedTokens: true,
			mutate: func(c *config.Config) {
				c.North.REST.Auth.BearerEnabled = ptrBool(false)
			},
			want: false,
		},
		{
			// The scheme gate only removes the token sources; a local admin
			// still logs in through the SPA session route.
			name:            "bearer scheme off but a local user exists",
			localUserCount:  1,
			persistedTokens: true,
			mutate: func(c *config.Config) {
				c.North.REST.Auth.BearerEnabled = ptrBool(false)
			},
			want: false,
		},
		{
			name:           "CCU auth explicitly enabled with a configured central",
			localUserCount: 0,
			hasCentral:     true,
			mutate: func(c *config.Config) {
				c.North.REST.Auth.CCU.Enabled = ptrBool(true)
			},
			want: false,
		},
		{
			// The add-on's lockout: CCU-delegated login is enabled by
			// default there, but it cannot authenticate anyone until a
			// central exists — and adding one requires being logged in.
			// Onboarding must stay reachable.
			name:           "CCU auth enabled but no central configured",
			localUserCount: 0,
			hasCentral:     false,
			mutate: func(c *config.Config) {
				c.North.REST.Auth.CCU.Enabled = ptrBool(true)
			},
			want: true,
		},
		{
			name:           "OIDC enabled",
			localUserCount: 0,
			mutate: func(c *config.Config) {
				c.North.REST.Auth.OIDC.Enabled = true
			},
			want: false,
		},
		{
			name:           "genuine first run: nothing configured",
			localUserCount: 0,
			// CCU.Enabled nil → build.IsAddon() == false in a normal test build.
			want: true,
		},
		{
			name:           "CCU auth explicitly disabled, nothing else",
			localUserCount: 0,
			mutate: func(c *config.Config) {
				c.North.REST.Auth.CCU.Enabled = ptrBool(false)
			},
			want: true,
		},
		{
			// The hardening toggle wins over the genuine-first-run state:
			// an operator who closed the surface keeps it closed even on a
			// database with zero users (wiped volume, restored blank DB).
			name:           "onboarding closed by bootstrap.allow_first_run_setup",
			localUserCount: 0,
			mutate: func(c *config.Config) {
				c.Bootstrap.AllowFirstRunSetup = ptrBool(false)
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &config.Config{}
			if tt.mutate != nil {
				tt.mutate(cfg)
			}
			src := authSources{
				localUsers:      tt.localUserCount,
				persistedTokens: tt.persistedTokens,
				hasCentral:      tt.hasCentral,
			}
			got := firstRunNeedsSetup(cfg, src)
			if got != tt.want {
				t.Errorf("firstRunNeedsSetup(..., %+v) = %v, want %v", src, got, tt.want)
			}
		})
	}
}

// TestFirstRunProbeClosesSetupOnceATokenExists drives the probe the REST
// setup routes actually mount against real stores: a daemon whose only
// credential is an API token must NOT report first run, or POST /api/v1/setup
// stays open for any unauthenticated peer to create an admin account.
func TestFirstRunProbeClosesSetupOnceATokenExists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(t.TempDir(), "firstrun.db")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sqUsers := sqlitestore.NewUserStore(db)
	sqTokens := sqlitestore.NewTokenStore(db)
	cfg := config.Default()
	cfg.North.REST.Auth.CCU.Enabled = ptrBool(false)

	probe := firstRunProbe(cfg, sqUsers, sqTokens, nil)
	if !probe(ctx) {
		t.Fatal("probe on an empty database = false, want true (genuine first run)")
	}

	if _, err := sqTokens.Import(ctx, "s3cr3t-token-value", "headless", auth.RoleAdmin); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if probe(ctx) {
		t.Error("probe with a persisted API token = true; the daemon can authenticate, " +
			"so the anonymous setup endpoint must be closed")
	}
}

// TestDormantBearerSchemeIsLoggedInsteadOfOpeningSetup covers the state the
// first-run gate deliberately does not answer with the onboarding wizard: an
// API token as the only credential while `bearer_enabled` is false.
//
// Opening anonymous admin creation there would hand the daemon to anyone who
// can reach the listener, so the gate stays closed and the boot log is the
// only place that can name the cause. Without the record the operator sees a
// login that rejects a token the config clearly contains, and nothing
// anywhere says why.
func TestDormantBearerSchemeIsLoggedInsteadOfOpeningSetup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(t.TempDir(), "dormant.db")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := config.Default()
	cfg.North.REST.Auth.CCU.Enabled = ptrBool(false)
	cfg.North.REST.Auth.BearerEnabled = ptrBool(false)
	cfg.North.REST.Auth.Tokens = map[string]string{"s3cr3t": "admin"}

	src := authSources{}
	if firstRunNeedsSetup(cfg, src) {
		t.Error("firstRunNeedsSetup = true with an API token configured; " +
			"POST /api/v1/setup is then open to anyone on the network")
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	warnOnDormantOnboarding(ctx, cfg, sqlitestore.NewUserStore(db), sqlitestore.NewTokenStore(db), nil, logger)
	if !strings.Contains(buf.String(), "auth.scheme.dormant") {
		t.Errorf("boot log does not report the dormant bearer scheme; got %q", buf.String())
	}

	// With the scheme on, the same configuration is an ordinary token-only
	// deployment and must stay silent.
	buf.Reset()
	cfg.North.REST.Auth.BearerEnabled = ptrBool(true)
	warnOnDormantOnboarding(ctx, cfg, sqlitestore.NewUserStore(db), sqlitestore.NewTokenStore(db), nil, logger)
	if buf.Len() != 0 {
		t.Errorf("a working token-only deployment logged %q, want nothing", buf.String())
	}
}

// resolveWithHeaders runs one request through the store's restResolve
// chain and reports whether an Identity was attached to the context.
func resolveWithHeaders(t *testing.T, st authStores, decorate func(*http.Request)) bool {
	t.Helper()
	var got bool
	h := st.restResolve(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, got = auth.IdentityFrom(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/probe", http.NoBody)
	decorate(req)
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestBuildAuthStores_SchemeGates(t *testing.T) {
	t.Parallel()
	off := false
	base := func() *config.Config {
		cfg := config.Default()
		cfg.North.REST.Auth.Users = map[string]string{"admin": "secret"}
		cfg.North.REST.Auth.Tokens = map[string]string{"tok-1": "admin"}
		return cfg
	}
	logger := slog.New(slog.DiscardHandler)

	t.Run("defaults resolve basic and bearer", func(t *testing.T) {
		t.Parallel()
		st := buildAuthStores(base(), ws.NewHub(), nil, logger)
		if !resolveWithHeaders(t, st, func(r *http.Request) { r.SetBasicAuth("admin", "secret") }) {
			t.Fatal("basic auth must resolve when the gate is default-on")
		}
		if !resolveWithHeaders(t, st, func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok-1") }) {
			t.Fatal("bearer must resolve when the gate is default-on")
		}
	})

	t.Run("basic disabled rejects basic, keeps bearer and sessions", func(t *testing.T) {
		t.Parallel()
		cfg := base()
		cfg.North.REST.Auth.BasicEnabled = &off
		st := buildAuthStores(cfg, ws.NewHub(), nil, logger)
		if resolveWithHeaders(t, st, func(r *http.Request) { r.SetBasicAuth("admin", "secret") }) {
			t.Fatal("basic auth must NOT resolve when disabled")
		}
		if !resolveWithHeaders(t, st, func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok-1") }) {
			t.Fatal("bearer must stay usable when only basic is disabled")
		}
		sess, err := st.sessions.Issue(auth.Identity{Subject: "admin", Role: auth.RoleAdmin})
		if err != nil {
			t.Fatalf("issue session: %v", err)
		}
		if !resolveWithHeaders(t, st, func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.ID})
		}) {
			t.Fatal("session login must keep working with basic disabled")
		}
	})

	t.Run("bearer disabled rejects bearer, keeps basic", func(t *testing.T) {
		t.Parallel()
		cfg := base()
		cfg.North.REST.Auth.BearerEnabled = &off
		st := buildAuthStores(cfg, ws.NewHub(), nil, logger)
		if resolveWithHeaders(t, st, func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok-1") }) {
			t.Fatal("bearer must NOT resolve when disabled")
		}
		if !resolveWithHeaders(t, st, func(r *http.Request) { r.SetBasicAuth("admin", "secret") }) {
			t.Fatal("basic must stay usable when only bearer is disabled")
		}
	})
}
