// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"os"
	"strconv"
	"strings"
)

// OverlayFromEnv applies environment-variable overrides to the
// loaded [Config]. Operators set `OPENCCU_LOOM_<KEY>` to override a
// curated set of common knobs without rewriting the YAML — useful
// for Kubernetes / docker deployments where most settings come from
// the image and a handful are tenant-specific.
//
// Unrecognised variables are ignored (silent no-op). The recognised
// set is intentionally narrow; extending it is a deliberate design
// decision per CLAUDE.md "no koanf without an ADR" — full koanf
// would pull in a heavy dependency tree we have not budgeted for.
//
// Recognised variables:
//
//   - OPENCCU_LOOM_LOCALE                    → c.Locale
//   - OPENCCU_LOOM_DATA_DIR                  → c.DataDir
//   - OPENCCU_LOOM_LOG_LEVEL                 → c.Logging.Level
//   - OPENCCU_LOOM_LOG_FORMAT                → c.Logging.Format
//   - OPENCCU_LOOM_CALLBACK_HOST             → c.Callback.Host
//   - OPENCCU_LOOM_CALLBACK_PORT             → c.Callback.Port (int)
//   - OPENCCU_LOOM_CALLBACK_BIN_PORT         → c.Callback.BinPort (int)
//   - OPENCCU_LOOM_REST_LISTEN               → c.North.REST.Listen
//   - OPENCCU_LOOM_REST_OPENAPI_VALIDATE     → c.North.REST.OpenAPIValidate (bool)
//   - OPENCCU_LOOM_REST_OPENAPI_SPEC_PATH    → c.North.REST.OpenAPISpecPath
//   - OPENCCU_LOOM_UI_LISTEN                 → c.North.UI.Listen
//   - OPENCCU_LOOM_MQTT_BROKER_URL           → c.North.MQTT.Listen
func (c *Config) OverlayFromEnv(getenv func(string) string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	overlayString(getenv, "OPENCCU_LOOM_LOCALE", &c.Locale)
	overlayString(getenv, "OPENCCU_LOOM_DATA_DIR", &c.DataDir)
	overlayString(getenv, "OPENCCU_LOOM_LOG_LEVEL", &c.Logging.Level)
	overlayString(getenv, "OPENCCU_LOOM_LOG_FORMAT", &c.Logging.Format)
	overlayString(getenv, "OPENCCU_LOOM_CALLBACK_HOST", &c.Callback.Host)
	overlayInt(getenv, "OPENCCU_LOOM_CALLBACK_PORT", &c.Callback.Port)
	overlayInt(getenv, "OPENCCU_LOOM_CALLBACK_BIN_PORT", &c.Callback.BinPort)
	overlayString(getenv, "OPENCCU_LOOM_REST_LISTEN", &c.North.REST.Listen)
	overlayBoolPtr(getenv, "OPENCCU_LOOM_REST_OPENAPI_VALIDATE", &c.North.REST.OpenAPIValidate)
	overlayString(getenv, "OPENCCU_LOOM_REST_OPENAPI_SPEC_PATH", &c.North.REST.OpenAPISpecPath)
	overlayString(getenv, "OPENCCU_LOOM_UI_LISTEN", &c.North.UI.Listen)
}

// LoadWithEnv combines [Load] + [Config.OverlayFromEnv] in one call.
// The env layer wins over the YAML; defaults still apply for fields
// neither YAML nor env populates.
func LoadWithEnv(path string) (*Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	cfg.OverlayFromEnv(nil)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func overlayString(getenv func(string) string, key string, dst *string) {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		*dst = v
	}
}

func overlayInt(getenv func(string) string, key string, dst *int) {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return
	}
	if n, err := strconv.Atoi(v); err == nil {
		*dst = n
	}
}

// overlayBoolPtr writes into a tri-state
// `*bool` field — leaving the pointer nil when the env var is absent
// or unparseable. Used by config knobs whose default depends on the
// pointer being nil (see [NorthREST.OpenAPIValidate]).
func overlayBoolPtr(getenv func(string) string, key string, dst **bool) {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return
	}
	var b bool
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		b = true
	case "0", "false", "no", "off":
		b = false
	default:
		return
	}
	*dst = &b
}
