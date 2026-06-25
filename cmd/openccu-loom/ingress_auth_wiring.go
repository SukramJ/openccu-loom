// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"log/slog"
	"net"
	"os"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// defaultSupervisorCIDR is the Home Assistant Supervisor's Docker subnet —
// the network Ingress requests originate from. Used when the operator does
// not pin a TrustedProxyCIDR.
const defaultSupervisorCIDR = "172.30.32.0/23"

// buildIngressTrust resolves the HA Ingress auth-passthrough policy (ADR 0044)
// from config + the build/supervised stamp. The returned value is inert (the
// middleware is a no-op) unless the feature is explicitly enabled AND the
// daemon runs as the supervised add-on AND a valid trusted CIDR resolves.
//
// It emits a one-shot startup log so the security posture is visible: the
// passthrough is only safe while the add-on keeps `panel_admin: true` (the
// daemon cannot verify that at runtime — HA passes no user/role through).
func buildIngressTrust(cfg *config.Config, logger *slog.Logger) auth.IngressTrust {
	if logger == nil {
		logger = slog.Default()
	}
	hc := cfg.North.REST.Auth.HAIngress
	supervised := isSupervised()
	// Tri-state: nil defaults to the supervised stamp — ON in the HA add-on
	// (Ingress is admin-only via panel_admin: true), OFF elsewhere. An explicit
	// value overrides.
	enabled := supervised
	if hc.Enabled != nil {
		enabled = *hc.Enabled
	}
	if !enabled {
		return auth.IngressTrust{}
	}

	cidrStr := hc.TrustedProxyCIDR
	if cidrStr == "" {
		cidrStr = defaultSupervisorCIDR
	}
	_, cidr, err := net.ParseCIDR(cidrStr)
	if err != nil {
		logger.Warn("auth.ingress.bad_cidr — passthrough disabled",
			slog.String("cidr", cidrStr), slog.String("err", err.Error()))
		return auth.IngressTrust{}
	}

	role := ingressRole(hc.Role)
	if !supervised {
		// Enabled but not running as the add-on: the Supervisor-subnet trust
		// assumption does not hold, so the middleware stays inert.
		logger.Warn("auth.ingress.enabled_but_not_supervised — passthrough inert (not the supervised add-on)")
		return auth.IngressTrust{Enabled: true, Supervised: false, TrustedCIDR: cidr, Role: role}
	}
	logger.Warn("auth.ingress.enabled — HA Ingress requests from the trusted subnet are accepted as authenticated; security depends on config.yaml panel_admin: true",
		slog.String("trusted_cidr", cidrStr), slog.String("role", string(role)))
	return auth.IngressTrust{Enabled: true, Supervised: true, TrustedCIDR: cidr, Role: role}
}

// isSupervised reports whether the daemon runs as the supervised HA add-on.
func isSupervised() bool {
	return build.IsAddon() || os.Getenv("OPENCCU_LOOM_SUPERVISOR") == "1"
}

// ingressRole maps the config role string to an [auth.Role]; unknown/empty
// defaults to admin (the common HA-admin case).
func ingressRole(s string) auth.Role {
	switch auth.Role(s) {
	case auth.RoleViewer:
		return auth.RoleViewer
	case auth.RoleOperator:
		return auth.RoleOperator
	default:
		return auth.RoleAdmin
	}
}
