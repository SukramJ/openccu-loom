// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// BootstrapConfig is the minimal config slice the daemon needs before
// the SQLite store is open. Anything in this struct must be settable
// from a tiny YAML file (read-only-mount-safe) plus env-overlay.
// Everything else lives in the database — see [ConfigStore] (Wave B).
//
// Design intent: Docker deployments mount config.yaml read-only (a
// common GitOps pattern). Live-Edit of the full config from the SPA
// would fail with EROFS. The split keeps YAML small and stable while
// runtime-mutable config moves into the writable data volume's
// SQLite database.
//
// Fields in this struct are intentionally limited to:
//   - what the daemon needs to find the database (data_dir)
//   - what must be reachable before any user can edit anything via
//     the SPA (REST/UI bind addresses, basic logging)
//   - the safety toggle that gates the first-run setup wizard
type BootstrapConfig struct {
	// DataDir is the writable directory that holds the SQLite
	// database and filesystem state (sessions, patches, caches).
	// Required at runtime; defaults to "./var" when empty.
	DataDir string `yaml:"data_dir" cfg:"basic"`

	// Logging configures structured logging. The level and format
	// must be known before the database is open because slog is
	// initialised at process start.
	Logging LoggingConfig `yaml:"logging" cfg:"basic"`

	// Listen carries the bind addresses for the REST and UI
	// surfaces. They must be reachable before the SPA can edit
	// anything via REST.
	Listen BootstrapListen `yaml:"listen" cfg:"basic"`

	// Bootstrap groups safety toggles that gate the first-run
	// flows. Once an initial admin user exists, the operator can
	// flip `allow_first_run_setup` to false to disable the open
	// `/setup` surface for the rest of the daemon's lifetime.
	Bootstrap BootstrapSafety `yaml:"bootstrap" cfg:"basic"`

	// EnvFile points at a KEY=VALUE-formatted file the daemon
	// loads at startup, populating any env variable not already
	// present in the process environment. Use it to pin CCU
	// passwords, MQTT credentials and OIDC client secrets without
	// committing them to config.yaml. See README → Secrets for
	// the syntax. Empty defaults to ".env" — the file is optional
	// (its absence is not an error). Set to "-" or "/dev/null" to
	// disable env-file loading entirely.
	EnvFile string `yaml:"env_file,omitempty" cfg:"basic"`
}

// BootstrapListen carries the bind addresses needed before the SPA
// can serve any configuration UI.
type BootstrapListen struct {
	// REST is the bind address of the REST + WebSocket server,
	// e.g. ":8080" or "0.0.0.0:8080". Empty defaults to ":8080".
	REST string `yaml:"rest" cfg:"basic"`
	// UI is the bind address of the SPA + bootstrap HTMX server,
	// e.g. ":8081". Empty defaults to ":8081".
	UI string `yaml:"ui" cfg:"basic"`
}

// BootstrapSafety bundles startup-only safety toggles.
type BootstrapSafety struct {
	// AllowFirstRunSetup controls whether the unauthenticated
	// `/setup` surface is reachable. The default (true) is the only
	// sensible value on a fresh install — the operator MUST create
	// the first admin user via this surface. Once that user exists
	// the toggle stays effectively a no-op until the SPA's "factory
	// reset" path explicitly clears the users table. Operators with
	// strict hardening requirements can flip this to false to keep
	// `/setup` dormant even on a database with zero users.
	AllowFirstRunSetup *bool `yaml:"allow_first_run_setup,omitempty" cfg:"basic"`
}

// DefaultBootstrap returns a BootstrapConfig populated with safe
// defaults. The daemon uses this when no --config flag is given.
func DefaultBootstrap() *BootstrapConfig {
	bc := &BootstrapConfig{}
	bc.applyDefaults()
	return bc
}

// LoadBootstrap parses path as a bootstrap-tier YAML. Missing fields
// fall back to defaults. Extra fields are silently ignored — the
// daemon does not enforce a strict-field policy at the bootstrap
// stage so a single operator-typed YAML can carry comments or
// scaffolding the loader does not yet know about.
func LoadBootstrap(path string) (*BootstrapConfig, error) {
	if path == "" {
		return nil, ErrNoConfig
	}
	buf, err := os.ReadFile(path) //nolint:gosec // path comes from operator-supplied CLI arg; see #20
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return ParseBootstrap(buf)
}

// ParseBootstrap ingests raw YAML bytes into a BootstrapConfig.
func ParseBootstrap(buf []byte) (*BootstrapConfig, error) {
	bc := &BootstrapConfig{}
	if err := yaml.Unmarshal(buf, bc); err != nil {
		return nil, fmt.Errorf("config: parse bootstrap: %w", err)
	}
	bc.applyDefaults()
	if err := bc.Validate(); err != nil {
		return nil, err
	}
	return bc, nil
}

func (b *BootstrapConfig) applyDefaults() {
	if b.DataDir == "" {
		b.DataDir = "./var"
	}
	if b.Logging.Level == "" {
		b.Logging.Level = "info"
	}
	if b.Logging.Format == "" {
		b.Logging.Format = "json"
	}
	if b.Listen.REST == "" {
		b.Listen.REST = ":8080"
	}
	if b.Listen.UI == "" {
		b.Listen.UI = ":8081"
	}
	if b.EnvFile == "" {
		b.EnvFile = DefaultEnvFile
	}
}

// EnvFileEnabled reports whether the env-file loader should run.
// The sentinel paths "-" and "/dev/null" disable loading entirely
// so an operator can hard-pin the behaviour from YAML without
// relying on the file's absence. After applyDefaults the EnvFile
// is never empty, so the empty-string case is paranoia for callers
// who construct a BootstrapConfig by hand.
func (b *BootstrapConfig) EnvFileEnabled() bool {
	switch b.EnvFile {
	case "", "-", "/dev/null":
		return false
	}
	return true
}

// Validate enforces invariants on the bootstrap tier.
func (b *BootstrapConfig) Validate() error {
	if b.Logging.Level != "debug" && b.Logging.Level != "info" &&
		b.Logging.Level != "warn" && b.Logging.Level != "error" {
		return fmt.Errorf("config: invalid logging.level %q", b.Logging.Level)
	}
	if b.Logging.Format != "json" && b.Logging.Format != "text" && b.Logging.Format != "text-color" {
		return fmt.Errorf("config: invalid logging.format %q", b.Logging.Format)
	}
	return nil
}

// FirstRunSetupAllowed returns the effective allow_first_run_setup
// value, defaulting to true when unset.
func (b *BootstrapConfig) FirstRunSetupAllowed() bool {
	if b.Bootstrap.AllowFirstRunSetup == nil {
		return true
	}
	return *b.Bootstrap.AllowFirstRunSetup
}

// ErrBootstrapMissing wraps a not-found error from [LoadBootstrap].
var ErrBootstrapMissing = errors.New("config: bootstrap file not found")
