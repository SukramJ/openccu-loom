// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/SukramJ/openccu-loom/internal/addonupdate"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// buildOcculiteSSOTrust resolves the box-shell SSO policy (ADR 0079) from
// config plus the lite-add-on stamp. The returned value is inert (the
// middleware is a no-op) unless the feature resolves enabled AND the host is
// an openccu-lite box that minted this add-on's token — the same two facts
// the add-on onboarding keys on — because only there does a local box exist
// to verify session ids against.
//
// The verifier talks to the box over the loopback with no credential of its
// own: the state route is open, and the session id under test is the bearer
// of each call. That keeps the SSO path independent of the RPC client's
// token and of any configured central.
func buildOcculiteSSOTrust(cfg *config.Config, logger *slog.Logger) auth.OcculiteSSOTrust {
	if logger == nil {
		logger = slog.Default()
	}
	oc := cfg.North.REST.Auth.OcculiteSSO
	liteAddon := isLiteAddonHost()
	// Tri-state: nil defaults to the lite-add-on stamp — ON beside the box
	// whose gate strips the header, OFF everywhere else. An explicit value
	// overrides.
	enabled := liteAddon
	if oc.Enabled != nil {
		enabled = *oc.Enabled
	}
	if !enabled {
		return auth.OcculiteSSOTrust{}
	}
	if !liteAddon {
		// Enabled explicitly but not beside a box: there is nothing to
		// verify against, so the middleware stays inert rather than
		// trusting an unverifiable header.
		logger.Warn("auth.occulite_sso.enabled_but_no_local_box — SSO inert (not the openccu-lite add-on)")
		return auth.OcculiteSSOTrust{Enabled: true}
	}
	client, err := occulited.New(occulited.Config{
		BaseURL: envOr(liteAddonBaseURLEnv, liteAddonBaseURL),
		Logger:  logger,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		logger.Warn("auth.occulite_sso.client_failed — SSO inert",
			slog.String("err", err.Error()))
		return auth.OcculiteSSOTrust{Enabled: true}
	}
	logger.Info("auth.occulite_sso.enabled — box-shell sessions are accepted after live verification against the local box")
	return auth.OcculiteSSOTrust{Enabled: true, Verifier: occuliteSessionVerifier{c: client}}
}

// isLiteAddonHost reports whether the daemon runs as the openccu-lite
// add-on beside a box that minted its token — the onboarding's own two
// predicates, env-overridable the same way for the e2e harness.
func isLiteAddonHost() bool {
	if !addonupdate.HostIsLiteVariant(envOr(liteAddonVersionPathEnv, addonupdate.HostVersionPath)) {
		return false
	}
	_, err := os.Stat(envOr(liteAddonTokenPathEnv, liteAddonTokenPath))
	return err == nil
}

// occuliteSessionVerifier adapts the occulited client to the auth port: one
// open state call carrying the session id under test as its bearer.
type occuliteSessionVerifier struct {
	c *occulited.Client
}

func (v occuliteSessionVerifier) VerifySession(ctx context.Context, sessionID string) (auth.OcculiteSession, error) {
	st, err := v.c.AuthStateOf(ctx, sessionID)
	if err != nil {
		return auth.OcculiteSession{}, err
	}
	return auth.OcculiteSession{
		Authenticated: st.Authenticated,
		User:          st.User,
		Role:          st.Role,
	}, nil
}
