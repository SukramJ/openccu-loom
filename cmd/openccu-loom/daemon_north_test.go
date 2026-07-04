// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

func TestFirstRunNeedsSetup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		localUserCount int
		mutate         func(*config.Config)
		want           bool
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
			name:           "CCU auth explicitly enabled",
			localUserCount: 0,
			mutate: func(c *config.Config) {
				c.North.REST.Auth.CCU.Enabled = ptrBool(true)
			},
			want: false,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &config.Config{}
			if tt.mutate != nil {
				tt.mutate(cfg)
			}
			got := firstRunNeedsSetup(cfg, tt.localUserCount)
			if got != tt.want {
				t.Errorf("firstRunNeedsSetup(..., %d) = %v, want %v", tt.localUserCount, got, tt.want)
			}
		})
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
