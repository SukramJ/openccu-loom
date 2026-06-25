// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import "testing"

// TestOverlayFromEnv verifies that the curated env-overlay picks up
// every recognised variable and ignores everything else.
func TestOverlayFromEnv(t *testing.T) {
	cfg := Default()
	getenv := mapEnv{
		"OPENCCU_LOOM_LOCALE":                 "de",
		"OPENCCU_LOOM_DATA_DIR":               "/var/lib/gohm",
		"OPENCCU_LOOM_LOG_LEVEL":              "debug",
		"OPENCCU_LOOM_LOG_FORMAT":             "text",
		"OPENCCU_LOOM_CALLBACK_HOST":          "127.0.0.1",
		"OPENCCU_LOOM_CALLBACK_PUBLIC_HOST":   "192.0.2.10",
		"OPENCCU_LOOM_CALLBACK_PORT":          "9100",
		"OPENCCU_LOOM_CALLBACK_BIN_PORT":      "9129",
		"OPENCCU_LOOM_REST_LISTEN":            ":18080",
		"OPENCCU_LOOM_REST_OPENAPI_VALIDATE":  "true",
		"OPENCCU_LOOM_REST_OPENAPI_SPEC_PATH": "/etc/gohm/openapi.yaml",
		"OPENCCU_LOOM_UI_LISTEN":              ":18081",
		"OPENCCU_LOOM_MQTT_BROKER_URL":        "tcp://broker:1883",
		// Garbage variable — must be ignored.
		"OPENCCU_LOOM_MADE_UP": "ignored",
	}.Get
	cfg.OverlayFromEnv(getenv)

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"Locale", cfg.Locale, "de"},
		{"DataDir", cfg.DataDir, "/var/lib/gohm"},
		{"LogLevel", cfg.Logging.Level, "debug"},
		{"LogFormat", cfg.Logging.Format, "text"},
		{"CallbackHost", cfg.Callback.Host, "127.0.0.1"},
		{"CallbackPublicHost", cfg.Callback.PublicHost, "192.0.2.10"},
		{"CallbackPort", cfg.Callback.Port, 9100},
		{"CallbackBinPort", cfg.Callback.BinPort, 9129},
		{"REST.Listen", cfg.North.REST.Listen, ":18080"},
		{"REST.OpenAPIValidate", cfg.North.REST.OpenAPIValidateEnabled(), true},
		{"REST.OpenAPISpecPath", cfg.North.REST.OpenAPISpecPath, "/etc/gohm/openapi.yaml"},
		{"UI.Listen", cfg.North.UI.Listen, ":18081"},
		{"MQTT.BrokerURL", cfg.North.MQTT.BrokerURL, "tcp://broker:1883"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestOverlayFromEnvIgnoresEmptyAndInvalid verifies the no-op paths
// — empty strings, malformed integers, malformed booleans.
func TestOverlayFromEnvIgnoresEmptyAndInvalid(t *testing.T) {
	cfg := Default()
	cfg.Callback.Port = 8120
	off := false
	cfg.North.REST.OpenAPIValidate = &off

	cfg.OverlayFromEnv(mapEnv{
		"OPENCCU_LOOM_CALLBACK_PORT":         "not-a-number",
		"OPENCCU_LOOM_REST_OPENAPI_VALIDATE": "maybe",
		"OPENCCU_LOOM_LOCALE":                "   ", // whitespace-only → skip
	}.Get)

	if cfg.Callback.Port != 8120 {
		t.Errorf("CallbackPort=%d want 8120 (invalid input must not clobber)", cfg.Callback.Port)
	}
	if cfg.North.REST.OpenAPIValidateEnabled() {
		t.Error("OpenAPIValidate must stay false on garbage input")
	}
	if cfg.Locale != "en" {
		t.Errorf("Locale=%q want en (whitespace-only must be skipped)", cfg.Locale)
	}
}

type mapEnv map[string]string

func (m mapEnv) Get(k string) string { return m[k] }

// TestDefaultWithEnvAppliesDataDir is the regression guard for the data-loss
// bug: a daemon with no config file must still place its data dir where
// OPENCCU_LOOM_DATA_DIR points (the HA add-on sets it to /data) instead of the
// ephemeral "./var" default. See DefaultWithEnv.
func TestDefaultWithEnvAppliesDataDir(t *testing.T) {
	t.Setenv("OPENCCU_LOOM_DATA_DIR", "/data")
	if got := DefaultWithEnv().DataDir; got != "/data" {
		t.Errorf("DefaultWithEnv().DataDir = %q, want /data (OPENCCU_LOOM_DATA_DIR must apply without a config file)", got)
	}
}

// TestDefaultWithEnvKeepsDefaultWhenUnset confirms the overlay is a no-op for an
// empty/unset variable, so the documented "./var" default still applies.
func TestDefaultWithEnvKeepsDefaultWhenUnset(t *testing.T) {
	t.Setenv("OPENCCU_LOOM_DATA_DIR", "")
	if got := DefaultWithEnv().DataDir; got != "./var" {
		t.Errorf("DefaultWithEnv().DataDir = %q, want ./var (default preserved when env unset)", got)
	}
}

// TestBootstrapOverlayFromEnv guards that the CLI/bootstrap tier honours the
// OPENCCU_LOOM_* overrides — above all OPENCCU_LOOM_DATA_DIR, so a containerised
// hmcli opens the same /data store the daemon uses instead of "./var".
func TestBootstrapOverlayFromEnv(t *testing.T) {
	bc := DefaultBootstrap()
	bc.OverlayFromEnv(mapEnv{
		"OPENCCU_LOOM_DATA_DIR":    "/data",
		"OPENCCU_LOOM_LOG_LEVEL":   "debug",
		"OPENCCU_LOOM_REST_LISTEN": "0.0.0.0:9090",
	}.Get)
	if bc.DataDir != "/data" {
		t.Errorf("DataDir=%q want /data", bc.DataDir)
	}
	if bc.Logging.Level != "debug" {
		t.Errorf("Logging.Level=%q want debug", bc.Logging.Level)
	}
	if bc.Listen.REST != "0.0.0.0:9090" {
		t.Errorf("Listen.REST=%q want 0.0.0.0:9090", bc.Listen.REST)
	}
}

// TestBootstrapOverlayFromEnvKeepsDefaults confirms an unset variable is a
// no-op, so DefaultBootstrap's "./var" default survives.
func TestBootstrapOverlayFromEnvKeepsDefaults(t *testing.T) {
	bc := DefaultBootstrap()
	bc.OverlayFromEnv(mapEnv{}.Get)
	if bc.DataDir != "./var" {
		t.Errorf("DataDir=%q want ./var (default preserved)", bc.DataDir)
	}
}
