// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	// e.g. ":8119" or "0.0.0.0:8119". Empty defaults to ":8119". The
	// server-rendered bootstrap surface (login / setup / about) shares
	// this listener — there is no separate UI bind address.
	REST string `yaml:"rest" cfg:"basic"`
}

// BootstrapSafety bundles startup-only safety toggles. It is carried by
// both config tiers: [BootstrapConfig] reads it before the database is
// open, [Config] carries the same block so the daemon's first-run probe
// — which runs off the full config — can see it.
type BootstrapSafety struct {
	// AllowFirstRunSetup controls whether the unauthenticated
	// `/setup` surface is reachable. The default (true) is the only
	// sensible value on a fresh install — the operator MUST create
	// the first admin user via this surface. Once a user exists the
	// toggle makes no difference: the first-run probe already refuses
	// as soon as any authentication source is configured. Operators
	// with strict hardening requirements can flip it to false to keep
	// `/setup` dormant even on a database with zero users.
	//
	// Deliberate consequence: with the toggle false and no
	// authentication source at all, there is no way into the daemon
	// except editing the YAML back and restarting. That lockout is
	// the point of the flag.
	AllowFirstRunSetup *bool `yaml:"allow_first_run_setup,omitempty" json:"allow_first_run_setup,omitempty" cfg:"basic"`
}

// FirstRunSetupAllowed returns the effective allow_first_run_setup
// value, defaulting to true when unset.
func (s BootstrapSafety) FirstRunSetupAllowed() bool {
	return orDefault(s.AllowFirstRunSetup, true)
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
		b.DataDir = defaultDataDir
	}
	b.Logging.applyDefaults()
	if b.Listen.REST == "" {
		b.Listen.REST = defaultRESTListen
	}
	if b.EnvFile == "" {
		b.EnvFile = DefaultEnvFile
	}
}

// OverlayFromEnv applies the bootstrap-tier subset of the OPENCCU_LOOM_*
// environment overrides, mirroring [Config.OverlayFromEnv] for the fields that
// exist at the bootstrap tier. Any pre-database load path — chiefly the CLI
// subcommands that open the SQLite store directly — must call this so
// OPENCCU_LOOM_DATA_DIR is honoured even without a config file. Otherwise a
// containerised `hmcli` resolves DataDir to the default "./var" and misses the
// real /data store the daemon uses (see [DefaultWithEnv] for the daemon side).
func (b *BootstrapConfig) OverlayFromEnv(getenv func(string) string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	overlayString(getenv, "OPENCCU_LOOM_DATA_DIR", &b.DataDir)
	overlayString(getenv, "OPENCCU_LOOM_LOG_LEVEL", &b.Logging.Level)
	overlayString(getenv, "OPENCCU_LOOM_LOG_FORMAT", &b.Logging.Format)
	overlayString(getenv, "OPENCCU_LOOM_REST_LISTEN", &b.Listen.REST)
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
	return b.Logging.validate()
}

// FirstRunSetupAllowed returns the effective allow_first_run_setup
// value, defaulting to true when unset.
func (b *BootstrapConfig) FirstRunSetupAllowed() bool {
	return b.Bootstrap.FirstRunSetupAllowed()
}

// ErrBootstrapMissing wraps a not-found error from [LoadBootstrap].
var ErrBootstrapMissing = errors.New("config: bootstrap file not found")
