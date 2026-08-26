// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import (
	"strings"
	"testing"
)

// TestOverlayFromEnv verifies that the curated env-overlay picks up
// every recognised variable and ignores everything else.
func TestOverlayFromEnv(t *testing.T) {
	cfg := Default()
	getenv := mapEnv{
		"OPENCCU_LOOM_LOCALE":                 "de",
		"OPENCCU_LOOM_DATA_DIR":               "/var/lib/gohm",
		"OPENCCU_LOOM_BACKUP_DIR":             "/mnt/backups",
		"OPENCCU_LOOM_LOG_LEVEL":              "debug",
		"OPENCCU_LOOM_LOG_FORMAT":             "text",
		"OPENCCU_LOOM_CALLBACK_HOST":          "127.0.0.1",
		"OPENCCU_LOOM_CALLBACK_PUBLIC_HOST":   "192.0.2.10",
		"OPENCCU_LOOM_CALLBACK_PORT":          "9100",
		"OPENCCU_LOOM_CALLBACK_BIN_PORT":      "9129",
		"OPENCCU_LOOM_REST_LISTEN":            ":18080",
		"OPENCCU_LOOM_REST_OPENAPI_VALIDATE":  "true",
		"OPENCCU_LOOM_REST_OPENAPI_SPEC_PATH": "/etc/gohm/openapi.yaml",
		"OPENCCU_LOOM_MQTT_BROKER_URL":        "tcp://broker:1883",
		// Garbage variable — must be ignored.
		"OPENCCU_LOOM_MADE_UP": "ignored",
	}.Get
	if err := cfg.OverlayFromEnv(getenv); err != nil {
		t.Fatalf("OverlayFromEnv returned unexpected error: %v", err)
	}

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"Locale", cfg.Locale, "de"},
		{"DataDir", cfg.DataDir, "/var/lib/gohm"},
		{"Backup.Dir", cfg.Backup.Dir, "/mnt/backups"},
		{"LogLevel", cfg.Logging.Level, "debug"},
		{"LogFormat", cfg.Logging.Format, "text"},
		{"CallbackHost", cfg.Callback.Host, "127.0.0.1"},
		{"CallbackPublicHost", cfg.Callback.PublicHost, "192.0.2.10"},
		{"CallbackPort", cfg.Callback.Port, 9100},
		{"CallbackBinPort", cfg.Callback.BinPort, 9129},
		{"REST.Listen", cfg.North.REST.Listen, ":18080"},
		{"REST.OpenAPIValidate", cfg.North.REST.OpenAPIValidateEnabled(), true},
		{"REST.OpenAPISpecPath", cfg.North.REST.OpenAPISpecPath, "/etc/gohm/openapi.yaml"},
		{"MQTT.BrokerURL", cfg.North.MQTT.BrokerURL, "tcp://broker:1883"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestOverlayFromEnvIgnoresEmptyAndInvalid verifies the no-op paths
// — empty strings and malformed booleans stay silent no-ops. Malformed
// integers are a hard error (see [TestOverlayFromEnvInvalidIntReturnsError]);
// they are deliberately absent from the getenv map here.
func TestOverlayFromEnvIgnoresEmptyAndInvalid(t *testing.T) {
	cfg := Default()
	off := false
	cfg.North.REST.OpenAPIValidate = &off

	if err := cfg.OverlayFromEnv(mapEnv{
		"OPENCCU_LOOM_REST_OPENAPI_VALIDATE": "maybe",
		"OPENCCU_LOOM_LOCALE":                "   ", // whitespace-only → skip
	}.Get); err != nil {
		t.Fatalf("OverlayFromEnv returned unexpected error: %v", err)
	}

	if cfg.North.REST.OpenAPIValidateEnabled() {
		t.Error("OpenAPIValidate must stay false on garbage input")
	}
	if cfg.Locale != "en" {
		t.Errorf("Locale=%q want en (whitespace-only must be skipped)", cfg.Locale)
	}
}

// TestOverlayFromEnvInvalidIntReturnsError verifies that a non-numeric
// OPENCCU_LOOM_CALLBACK_PORT is reported as an error naming the offending
// variable instead of being silently dropped — matching the strict-boot
// philosophy already applied to the env-file loader (see envfile.go).
func TestOverlayFromEnvInvalidIntReturnsError(t *testing.T) {
	cfg := Default()
	cfg.Callback.Port = 8120

	err := cfg.OverlayFromEnv(mapEnv{
		"OPENCCU_LOOM_CALLBACK_PORT": "abc",
	}.Get)
	if err == nil {
		t.Fatal("want error for non-numeric OPENCCU_LOOM_CALLBACK_PORT, got nil")
	}
	if !strings.Contains(err.Error(), "OPENCCU_LOOM_CALLBACK_PORT") {
		t.Errorf("error %q does not name the offending variable", err.Error())
	}
	if cfg.Callback.Port != 8120 {
		t.Errorf("CallbackPort=%d want 8120 (invalid input must not clobber before erroring)", cfg.Callback.Port)
	}
}

// TestOverlayFromEnvInvalidBinPortReturnsError covers the second int field
// overlaid by [Config.OverlayFromEnv].
func TestOverlayFromEnvInvalidBinPortReturnsError(t *testing.T) {
	cfg := Default()
	err := cfg.OverlayFromEnv(mapEnv{
		"OPENCCU_LOOM_CALLBACK_BIN_PORT": "not-a-number",
	}.Get)
	if err == nil {
		t.Fatal("want error for non-numeric OPENCCU_LOOM_CALLBACK_BIN_PORT, got nil")
	}
	if !strings.Contains(err.Error(), "OPENCCU_LOOM_CALLBACK_BIN_PORT") {
		t.Errorf("error %q does not name the offending variable", err.Error())
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

// TestDefaultWithEnvAppliesBackupDir mirrors [TestDefaultWithEnvAppliesDataDir]
// for OPENCCU_LOOM_BACKUP_DIR: a daemon with no config file must still place
// downloaded CCU archives where the variable points (the CCU add-on's service
// script sets it to the configured backup target) instead of the
// <data_dir>/backups default. See DefaultWithEnv and BackupConfig.Dir.
func TestDefaultWithEnvAppliesBackupDir(t *testing.T) {
	t.Setenv("OPENCCU_LOOM_BACKUP_DIR", "/mnt/backups")
	if got := DefaultWithEnv().Backup.Dir; got != "/mnt/backups" {
		t.Errorf("DefaultWithEnv().Backup.Dir = %q, want /mnt/backups (OPENCCU_LOOM_BACKUP_DIR must apply without a config file)", got)
	}
}

// TestDefaultWithEnvKeepsBackupDirDefaultWhenUnset confirms the overlay is a
// no-op for an empty/unset variable, so BackupConfig.Dir stays empty (which
// resolves to <data_dir>/backups downstream).
func TestDefaultWithEnvKeepsBackupDirDefaultWhenUnset(t *testing.T) {
	t.Setenv("OPENCCU_LOOM_BACKUP_DIR", "")
	if got := DefaultWithEnv().Backup.Dir; got != "" {
		t.Errorf("DefaultWithEnv().Backup.Dir = %q, want \"\" (default preserved when env unset)", got)
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
