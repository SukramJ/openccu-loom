// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/deployment"
)

// TestBuildIngressTrustTriState pins the tri-state resolution of
// north.rest.auth.ha_ingress.enabled: unset defaults to the deployment (on
// in the Home Assistant add-on), an explicit value overrides, and the
// result is inert outside that add-on. ptrBool is declared in
// ccu_auth_wiring_test.go.
func TestBuildIngressTrustTriState(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	cfgWith := func(enabled *bool) *config.Config {
		c := &config.Config{}
		c.North.REST.Auth.HAIngress.Enabled = enabled
		return c
	}

	t.Run("supervised + unset → active admin", func(t *testing.T) {
		t.Setenv(deployment.EnvVar, "ha-addon")
		got := buildIngressTrust(cfgWith(nil), logger)
		if !got.Enabled || !got.Supervised || got.TrustedCIDR == nil {
			t.Fatalf("want active trust, got %+v", got)
		}
		if got.Role != auth.RoleAdmin {
			t.Errorf("Role=%q, want admin", got.Role)
		}
	})

	t.Run("supervised + explicit false → inert", func(t *testing.T) {
		t.Setenv(deployment.EnvVar, "ha-addon")
		got := buildIngressTrust(cfgWith(new(false)), logger)
		if got.Enabled || got.TrustedCIDR != nil {
			t.Fatalf("want inert trust, got %+v", got)
		}
	})

	// A restart supervisor is not the Home Assistant Supervisor: the CCU
	// add-on sets OPENCCU_LOOM_SUPERVISOR too, and no Ingress exists there.
	t.Run("ccu add-on with a restart supervisor + unset → inert", func(t *testing.T) {
		t.Setenv("OPENCCU_LOOM_SUPERVISOR", "1")
		t.Setenv(deployment.EnvVar, "ccu-addon")
		got := buildIngressTrust(cfgWith(nil), logger)
		if got.Active() {
			t.Fatalf("want inert trust outside the HA add-on, got %+v", got)
		}
	})

	t.Run("not supervised + unset → inert", func(t *testing.T) {
		t.Setenv(deployment.EnvVar, "standalone")
		got := buildIngressTrust(cfgWith(nil), logger)
		if got.Enabled {
			t.Fatalf("want inert trust when not supervised, got %+v", got)
		}
	})

	t.Run("explicit true but not supervised → inert middleware", func(t *testing.T) {
		t.Setenv(deployment.EnvVar, "standalone")
		got := buildIngressTrust(cfgWith(new(true)), logger)
		// enabled is honoured, but Supervised is false so the middleware no-ops.
		if got.Supervised {
			t.Fatalf("Supervised must be false when not supervised, got %+v", got)
		}
	})
}
