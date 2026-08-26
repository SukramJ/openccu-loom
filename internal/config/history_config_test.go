// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import (
	"testing"
	"time"
)

// TestConfig_Validate_ClampsHistoryRetention verifies finding #5: an
// explicit history.retention below the hourly-rollup lag is clamped up to
// HistoryRetentionFloor at config load (so the purge can never delete raw
// rows before the hourly fold folds them), while zero (use-default) and
// any value at or above the floor pass through unchanged.
func TestConfig_Validate_ClampsHistoryRetention(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"below floor is clamped", 30 * time.Minute, HistoryRetentionFloor},
		{"zero stays zero (use default)", 0, 0},
		{"at floor unchanged", HistoryRetentionFloor, HistoryRetentionFloor},
		{"above floor unchanged", 720 * time.Hour, 720 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := Default()
			c.Persistence.History.Retention = tc.in
			if err := c.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if got := c.Persistence.History.Retention; got != tc.want {
				t.Errorf("retention after Validate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHistoryConfig_NilEnabled(t *testing.T) {
	t.Parallel()
	c := HistoryConfig{}
	if c.HistoryFeatureEnabled() {
		t.Error("HistoryFeatureEnabled() = true, want false when Enabled is nil")
	}
	if c.HistoryEnabled("x") {
		t.Error("HistoryEnabled(\"x\") = true, want false when Enabled is nil")
	}
}

func TestHistoryConfig_ExplicitFalse(t *testing.T) {
	t.Parallel()
	boolPtr := func(b bool) *bool { return &b }
	c := HistoryConfig{Enabled: boolPtr(false)}
	if c.HistoryFeatureEnabled() {
		t.Error("HistoryFeatureEnabled() = true, want false when Enabled is *false")
	}
	if c.HistoryEnabled("x") {
		t.Error("HistoryEnabled(\"x\") = true, want false when Enabled is *false")
	}
}

func TestHistoryConfig_EnabledNoCentralsRestriction(t *testing.T) {
	t.Parallel()
	boolPtr := func(b bool) *bool { return &b }
	c := HistoryConfig{Enabled: boolPtr(true)}
	if !c.HistoryFeatureEnabled() {
		t.Error("HistoryFeatureEnabled() = false, want true when Enabled is *true")
	}
	if !c.HistoryEnabled("x") {
		t.Error("HistoryEnabled(\"x\") = false, want true when Enabled is *true and no DisabledCentrals")
	}
}

// TestHistoryConfig_EnergyTariffYAMLRoundTrip verifies that the electricity
// tariff fields decode from persistence.history in the daemon YAML config.
func TestHistoryConfig_EnergyTariffYAMLRoundTrip(t *testing.T) {
	t.Parallel()
	buf := []byte(minimalCentralYAML + `
persistence:
  history:
    energy_price_per_kwh: 0.32
    energy_currency: "$"
`)
	cfg, err := Parse(buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Persistence.History.EnergyPricePerKWh != 0.32 {
		t.Errorf("EnergyPricePerKWh = %v, want 0.32", cfg.Persistence.History.EnergyPricePerKWh)
	}
	if cfg.Persistence.History.EnergyCurrency != "$" {
		t.Errorf("EnergyCurrency = %q, want %q", cfg.Persistence.History.EnergyCurrency, "$")
	}
}

// TestHistoryConfig_EnergyTariffAbsentKeysStayZero verifies that omitting
// the tariff keys leaves both fields at their zero value — this is the
// signal the daemon and the SPA use to mean "no tariff configured", as
// opposed to an explicit free (0.00) tariff.
func TestHistoryConfig_EnergyTariffAbsentKeysStayZero(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(minimalCentralYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Persistence.History.EnergyPricePerKWh != 0 {
		t.Errorf("EnergyPricePerKWh = %v, want 0 (unset)", cfg.Persistence.History.EnergyPricePerKWh)
	}
	if cfg.Persistence.History.EnergyCurrency != "" {
		t.Errorf("EnergyCurrency = %q, want empty (unset)", cfg.Persistence.History.EnergyCurrency)
	}
}

func TestHistoryConfig_DisabledCentralExcludesIt(t *testing.T) {
	t.Parallel()
	boolPtr := func(b bool) *bool { return &b }
	c := HistoryConfig{
		Enabled:          boolPtr(true),
		DisabledCentrals: []string{"b"},
	}
	if !c.HistoryEnabled("a") {
		t.Error("HistoryEnabled(\"a\") = false, want true when \"a\" is not in DisabledCentrals")
	}
	if c.HistoryEnabled("b") {
		t.Error("HistoryEnabled(\"b\") = true, want false when \"b\" is in DisabledCentrals")
	}
}
