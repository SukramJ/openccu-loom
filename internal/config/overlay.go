// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"fmt"
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
//   - OPENCCU_LOOM_CALLBACK_PUBLIC_HOST      → c.Callback.PublicHost
//   - OPENCCU_LOOM_CALLBACK_PORT             → c.Callback.Port (int)
//   - OPENCCU_LOOM_CALLBACK_BIN_PORT         → c.Callback.BinPort (int)
//   - OPENCCU_LOOM_REST_LISTEN               → c.North.REST.Listen
//   - OPENCCU_LOOM_REST_OPENAPI_VALIDATE     → c.North.REST.OpenAPIValidate (bool)
//   - OPENCCU_LOOM_REST_OPENAPI_SPEC_PATH    → c.North.REST.OpenAPISpecPath
//   - OPENCCU_LOOM_MQTT_BROKER_URL           → c.North.MQTT.BrokerURL
//   - OPENCCU_LOOM_UI_EMBEDDED               → c.North.UI.Embedded (bool)
//   - OPENCCU_LOOM_UI_EMBEDDED_SCOPE         → c.North.UI.EmbeddedScope
//
// A recognised variable that is present but fails to parse (e.g.
// OPENCCU_LOOM_CALLBACK_PORT=abc) is reported as an error naming the
// offending variable rather than being silently dropped — mirroring the
// strict-boot philosophy of [LoadEnvFile]: a typo must surface at boot,
// not vanish into a quietly-kept default.
func (c *Config) OverlayFromEnv(getenv func(string) string) error {
	if getenv == nil {
		getenv = os.Getenv
	}
	overlayString(getenv, "OPENCCU_LOOM_LOCALE", &c.Locale)
	overlayString(getenv, "OPENCCU_LOOM_DATA_DIR", &c.DataDir)
	overlayString(getenv, "OPENCCU_LOOM_LOG_LEVEL", &c.Logging.Level)
	overlayString(getenv, "OPENCCU_LOOM_LOG_FORMAT", &c.Logging.Format)
	overlayString(getenv, "OPENCCU_LOOM_CALLBACK_HOST", &c.Callback.Host)
	overlayString(getenv, "OPENCCU_LOOM_CALLBACK_PUBLIC_HOST", &c.Callback.PublicHost)
	if err := overlayInt(getenv, "OPENCCU_LOOM_CALLBACK_PORT", &c.Callback.Port); err != nil {
		return err
	}
	if err := overlayInt(getenv, "OPENCCU_LOOM_CALLBACK_BIN_PORT", &c.Callback.BinPort); err != nil {
		return err
	}
	overlayString(getenv, "OPENCCU_LOOM_REST_LISTEN", &c.North.REST.Listen)
	overlayBoolPtr(getenv, "OPENCCU_LOOM_REST_OPENAPI_VALIDATE", &c.North.REST.OpenAPIValidate)
	overlayString(getenv, "OPENCCU_LOOM_REST_OPENAPI_SPEC_PATH", &c.North.REST.OpenAPISpecPath)
	overlayString(getenv, "OPENCCU_LOOM_MQTT_BROKER_URL", &c.North.MQTT.BrokerURL)
	overlayBoolPtr(getenv, "OPENCCU_LOOM_UI_EMBEDDED", &c.North.UI.Embedded)
	// Validation rejects an unrecognised value at boot, so a typo here
	// surfaces rather than silently reading as the default.
	if v := strings.TrimSpace(getenv("OPENCCU_LOOM_UI_EMBEDDED_SCOPE")); v != "" {
		c.North.UI.EmbeddedScope = EmbeddedScope(v)
	}
	return nil
}

// DefaultWithEnv returns [Default] with the environment overlay applied.
//
// A daemon started without any config file (no --config, none discovered —
// the standard HA add-on / minimal Docker case) MUST still honour the
// OPENCCU_LOOM_* overrides, above all OPENCCU_LOOM_DATA_DIR: it decides where
// the SQLite database and all writable state live. Without the overlay the
// daemon silently falls back to the "./var" default inside the (ephemeral)
// container, so every restart/update starts on an empty database and loses the
// operator's CCUs, users and config. Always go through this constructor for the
// no-config-file path; never use a bare [Default] there.
//
// Panics when a recognised OPENCCU_LOOM_* variable is present but malformed
// (e.g. OPENCCU_LOOM_CALLBACK_PORT=abc) — this path runs before any config
// file (and therefore any [Config.Validate] call) exists, so there is no
// caller in a position to react to a returned error; a fast, loud boot
// failure beats silently keeping a stale default. Callers that can plumb an
// error through (e.g. the primary --config path) should prefer
// [LoadWithEnv], which propagates the same failure as a normal error.
func DefaultWithEnv() *Config {
	cfg := Default()
	if err := cfg.OverlayFromEnv(nil); err != nil {
		panic(fmt.Sprintf("config: DefaultWithEnv: %v", err))
	}
	return cfg
}

// LoadWithEnv combines [Load] + [Config.OverlayFromEnv] in one call.
// The env layer wins over the YAML; defaults still apply for fields
// neither YAML nor env populates. A malformed recognised env variable
// (e.g. OPENCCU_LOOM_CALLBACK_PORT=abc) aborts the load with an error
// naming the offending variable instead of silently keeping the YAML
// value.
func LoadWithEnv(path string) (*Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	if err := cfg.OverlayFromEnv(nil); err != nil {
		return nil, err
	}
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

// overlayInt applies the int-valued env override named key onto dst.
// An unset/empty variable is a no-op; a present-but-unparsable value is
// reported as an error naming key so a typo surfaces at boot instead of
// silently leaving dst on its previous value.
func overlayInt(getenv func(string) string, key string, dst *int) error {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("config: env %s: invalid integer %q: %w", key, v, err)
	}
	*dst = n
	return nil
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
