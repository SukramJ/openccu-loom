// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import "testing"

// TestFirstRunSetupAllowedDefaultsToTrue pins the tri-state semantics of
// the hardening toggle: unset means "wizard reachable" (the only sensible
// value on a fresh install), an explicit false keeps it dormant.
func TestFirstRunSetupAllowedDefaultsToTrue(t *testing.T) {
	t.Parallel()
	if !DefaultBootstrap().FirstRunSetupAllowed() {
		t.Error("unset allow_first_run_setup must default to true")
	}
	bc, err := ParseBootstrap([]byte("bootstrap:\n  allow_first_run_setup: false\n"))
	if err != nil {
		t.Fatalf("parse bootstrap: %v", err)
	}
	if bc.FirstRunSetupAllowed() {
		t.Error("allow_first_run_setup: false must disable the first-run surface")
	}
}

// TestConfigCarriesBootstrapSafetyToggle pins that the daemon-tier config
// parses the `bootstrap:` block too. The first-run probe runs off
// [Config], so a toggle that only [BootstrapConfig] can see never reaches
// the gate it names — the documented hardening control silently does
// nothing.
func TestConfigCarriesBootstrapSafetyToggle(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte("bootstrap:\n  allow_first_run_setup: false\n" + minimalCentralYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Bootstrap.FirstRunSetupAllowed() {
		t.Error("config: bootstrap.allow_first_run_setup: false must reach Config")
	}
	def, err := Parse([]byte(minimalCentralYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !def.Bootstrap.FirstRunSetupAllowed() {
		t.Error("config: an absent bootstrap block must leave the first-run surface reachable")
	}
}
