// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// writeMinimalConfig writes a config file carrying only the given body and
// returns its path.
func writeMinimalConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestLoadWithEnvRejectsZeroCallbackPorts pins the invariant that neither
// callback.port nor callback.bin_port has a "port 0 = pick any free port" mode.
// The env overlay runs AFTER applyDefaults, so a zero coming from
// OPENCCU_LOOM_CALLBACK_PORT / _BIN_PORT is never rewritten to the default
// and would reach the listener, which then binds an ephemeral port no CCU is
// ever told about.
func TestLoadWithEnvRejectsZeroCallbackPorts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		envKey string
		field  string
	}{
		{"xmlrpc", "OPENCCU_LOOM_CALLBACK_PORT", "callback.port"},
		{"binrpc", "OPENCCU_LOOM_CALLBACK_BIN_PORT", "callback.bin_port"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeMinimalConfig(t, "locale: en\n")
			t.Setenv(tc.envKey, "0")
			cfg, err := LoadWithEnv(path)
			if err == nil {
				t.Fatalf("LoadWithEnv accepted %s=0 (port %d/%d)",
					tc.envKey, cfg.Callback.Port, cfg.Callback.BinPort)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("error must name %s, got: %v", tc.field, err)
			}
		})
	}
}

// TestApplyDefaultsStillRewritesZeroCallbackPortsFromYAML guards the other
// direction: a YAML `callback.port: 0` is defaulting syntax, rewritten by
// applyDefaults before Validate ever sees it. The bound above must not turn
// that documented shorthand into a boot failure.
func TestApplyDefaultsStillRewritesZeroCallbackPortsFromYAML(t *testing.T) {
	cfg, err := Parse([]byte("callback:\n  port: 0\n  bin_port: 0\n"))
	if err != nil {
		t.Fatalf("Parse rejected the YAML zero shorthand: %v", err)
	}
	if cfg.Callback.Port != hmenum.DefaultXMLRPCCallbackPort {
		t.Fatalf("callback.port = %d, want %d", cfg.Callback.Port, hmenum.DefaultXMLRPCCallbackPort)
	}
	if cfg.Callback.BinPort != hmenum.DefaultBINRPCCallbackPort {
		t.Fatalf("callback.bin_port = %d, want %d", cfg.Callback.BinPort, hmenum.DefaultBINRPCCallbackPort)
	}
}
