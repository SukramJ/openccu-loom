// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	scope := liteAddonGateScope(envOr(liteAddonTokenPathEnv, liteAddonTokenPath))
	logger.Info("auth.occulite_sso.enabled — box-shell sessions and box tokens are accepted after live verification against the local box",
		slog.String("addon_scope", scope))
	return auth.OcculiteSSOTrust{Enabled: true, Verifier: occuliteSessionVerifier{c: client}, AddonScope: scope}
}

// liteAddonGateScope derives the add-on's gate scope ("addon:<id>") from the
// file occulited writes its token to: the box names that file
// /run/occulite/addon-tokens/<id>.api, so the add-on id is read from the
// path the box itself chose rather than assumed. A path of another shape
// yields no scope, which leaves box tokens unaccepted.
func liteAddonGateScope(tokenPath string) string {
	id, ok := strings.CutSuffix(filepath.Base(tokenPath), ".api")
	if !ok || id == "" {
		return ""
	}
	return "addon:" + id
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
// open state call carrying the credential under test (session id or token)
// as its bearer.
type occuliteSessionVerifier struct {
	c *occulited.Client
}

func (v occuliteSessionVerifier) VerifySession(ctx context.Context, credential string) (auth.OcculiteSession, error) {
	st, err := v.c.AuthStateOf(ctx, credential)
	if err != nil {
		return auth.OcculiteSession{}, err
	}
	return auth.OcculiteSession{
		Authenticated: st.Authenticated,
		User:          st.User,
		Role:          st.Role,
		Scopes:        st.Scopes,
		AuthOff:       st.AuthOff,
		Public:        st.Public,
	}, nil
}

// occuliteRevalidateErrorLog names the one log event of the revalidator.
const occuliteRevalidateErrorLog = "auth.occulite_sso.revalidate_failed — keeping the socket"

// occuliteRevalidator builds the WebSocket re-verification for box-shell
// sessions and box tokens from the same trust the request resolver uses, or
// nil when the trust is inert (the socket watch then never asks). It answers
// false only on a definite refusal: the box no longer vouches for the
// credential (the predicate is [auth.OcculiteSession.Grant], the resolver's
// own), or it now maps to a role other than the one the socket holds — a
// demotion on the box, or a token losing Full access, must end an admin
// socket rather than leave it commanding. A verification error
// answers true: the box is on the loopback, and closing every box-shell
// socket on one hiccup would make the daemon's availability hinge on it,
// while a real logout is still caught on the next tick.
func occuliteRevalidator(t auth.OcculiteSSOTrust, logger *slog.Logger) func(ctx context.Context, sessionID string, role auth.Role) bool {
	if !t.Enabled || t.Verifier == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	v := t.Verifier
	return func(ctx context.Context, sessionID string, role auth.Role) bool {
		sess, err := v.VerifySession(ctx, sessionID)
		if err != nil {
			// The session id stays out of the line: it is a live credential.
			logger.DebugContext(ctx, occuliteRevalidateErrorLog, slog.String("error", err.Error()))
			return true
		}
		id, ok := sess.Grant(sessionID, t.AddonScope)
		return ok && id.Role == role
	}
}
