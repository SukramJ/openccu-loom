// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// SectionLoader is the persistence dependency the Store needs.
// Satisfied by [*sqlite.ConfigSectionStore].
type SectionLoader interface {
	Get(ctx context.Context, section string) (sqlite.SectionRow, error)
	Put(ctx context.Context, section string, valueJSON []byte, updatedBy string) (sqlite.SectionRow, error)
	Delete(ctx context.Context, section string) error
	List(ctx context.Context) ([]sqlite.SectionRow, error)
}

// CentralLoader is the persistence dependency for the centrals
// table. Satisfied by [*sqlite.CentralsStore].
type CentralLoader interface {
	List(ctx context.Context) ([]sqlite.CentralRow, error)
}

// Store is the high-level config facade. It composes a section
// store and a centrals store, plus the static bootstrap config
// captured at process start.
type Store struct {
	bootstrap *config.BootstrapConfig
	sections  SectionLoader
	centrals  CentralLoader

	// envLookup resolves env-var references for secret fields.
	// Tests can swap this with a fake; production wires
	// [os.Getenv].
	envLookup func(string) string
}

// Option configures a Store at construction.
type Option func(*Store)

// WithEnvLookup overrides the env-var resolver. Default uses os.Getenv.
func WithEnvLookup(f func(string) string) Option {
	return func(s *Store) { s.envLookup = f }
}

// New returns a Store. bootstrap must be non-nil; sections and
// centrals may be nil for tests that exercise pure-bootstrap paths.
func New(bootstrap *config.BootstrapConfig, sections SectionLoader, centrals CentralLoader, opts ...Option) *Store {
	s := &Store{bootstrap: bootstrap, sections: sections, centrals: centrals}
	for _, o := range opts {
		o(s)
	}
	if s.envLookup == nil {
		s.envLookup = noopLookup
	}
	return s
}

func noopLookup(string) string { return "" }

// FieldSource records the resolved origin of a config field for the
// SPA's source-attribution UI.
type FieldSource string

const (
	// SourceBootstrap means the value came from BootstrapConfig (YAML).
	SourceBootstrap FieldSource = "bootstrap"
	// SourceDB means the value came from a config_sections /
	// centrals row.
	SourceDB FieldSource = "db"
	// SourceEnv means the value was resolved via an env-var
	// reference (secret fields).
	SourceEnv FieldSource = "env"
	// SourceDefault means the value is the daemon's built-in
	// default (no override registered).
	SourceDefault FieldSource = "default"
)

// EffectiveResult is the output of [Store.Effective]: the assembled
// [config.Config] plus per-field source attribution.
type EffectiveResult struct {
	Config  *config.Config
	Sources map[string]FieldSource
}

// OverlayInto layers the DB-tier sections + centrals on top of the
// caller-supplied [config.Config]. The daemon uses this to merge
// the SPA's live edits over the YAML-loaded base: any section that
// has a DB row replaces the YAML-side values; sections without a
// row leave the YAML untouched.
//
// Mirrors [Effective] but operates on an existing config instead
// of starting from defaults, so the daemon's existing YAML-driven
// wiring continues to work for everything the operator has not yet
// changed in the SPA.
func (s *Store) OverlayInto(ctx context.Context, cfg *config.Config) (map[string]FieldSource, error) {
	srcs := make(map[string]FieldSource)
	if s.sections != nil {
		if err := s.layerSections(ctx, cfg, srcs); err != nil {
			return nil, err
		}
	}
	if s.centrals != nil {
		if err := s.layerCentrals(ctx, cfg, srcs); err != nil {
			return nil, err
		}
	}
	s.resolveEnvSecrets(cfg, srcs)
	// A stored section may omit fields (e.g. north.mqtt without
	// topic_base) that the base config had defaulted before the overlay
	// clobbered the whole sub-tree. Re-fill defaults so a partial section
	// does not leave a required field at its zero value.
	cfg.ApplyDefaults()
	return srcs, nil
}

// Effective assembles the daemon's runtime config by:
//  1. starting from the bootstrap tier (data_dir, listen, logging),
//  2. layering DB-tier section snapshots on top,
//  3. resolving env-var references for secret fields,
//  4. filling in defaults for anything still unset.
//
// Returns an error only on JSON-decode failures of malformed
// section rows; missing sections fall back to defaults silently.
func (s *Store) Effective(ctx context.Context) (*EffectiveResult, error) {
	cfg := config.Default()
	srcs := make(map[string]FieldSource)

	// Bootstrap-tier wins on the fields it owns.
	cfg.DataDir = s.bootstrap.DataDir
	srcs["data_dir"] = SourceBootstrap
	cfg.Logging = s.bootstrap.Logging
	srcs["logging"] = SourceBootstrap
	cfg.North.REST.Listen = s.bootstrap.Listen.REST
	srcs["north.rest.listen"] = SourceBootstrap
	cfg.North.UI.Listen = s.bootstrap.Listen.UI
	srcs["north.ui.listen"] = SourceBootstrap

	if s.sections != nil {
		if err := s.layerSections(ctx, cfg, srcs); err != nil {
			return nil, err
		}
	}
	if s.centrals != nil {
		if err := s.layerCentrals(ctx, cfg, srcs); err != nil {
			return nil, err
		}
	}
	s.resolveEnvSecrets(cfg, srcs)
	cfg.ApplyDefaults()
	return &EffectiveResult{Config: cfg, Sources: srcs}, nil
}

// layerSections walks every known section, reads its JSON row when
// present, and applies it to the runtime config.
func (s *Store) layerSections(ctx context.Context, cfg *config.Config, srcs map[string]FieldSource) error {
	for _, sec := range AllSections() {
		row, err := s.sections.Get(ctx, string(sec))
		if errors.Is(err, sqlite.ErrSectionNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("configstore: layer %s: %w", sec, err)
		}
		if err := applySection(sec, row.ValueJSON, cfg); err != nil {
			return fmt.Errorf("configstore: apply %s: %w", sec, err)
		}
		srcs[string(sec)] = SourceDB
	}
	return nil
}

// applySection routes one section payload to the corresponding
// [config.Config] sub-tree.
func applySection(sec Section, raw []byte, cfg *config.Config) error {
	switch sec {
	case SectionMQTT:
		return json.Unmarshal(raw, &cfg.North.MQTT)
	case SectionMatter:
		return json.Unmarshal(raw, &cfg.North.Matter)
	case SectionDiscovery:
		return json.Unmarshal(raw, &cfg.North.Discovery)
	case SectionREST:
		return json.Unmarshal(raw, &cfg.North.REST)
	case SectionOIDC:
		return json.Unmarshal(raw, &cfg.North.REST.Auth.OIDC)
	case SectionUI:
		return json.Unmarshal(raw, &cfg.North.UI)
	case SectionCallback:
		return json.Unmarshal(raw, &cfg.Callback)
	case SectionCCUData:
		return json.Unmarshal(raw, &cfg.CCUData)
	case SectionReliability:
		return json.Unmarshal(raw, &cfg.Reliability)
	case SectionPersistence:
		return json.Unmarshal(raw, &cfg.Persistence)
	case SectionLocale:
		var v LocaleConfig
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		if v.Locale != "" {
			cfg.Locale = v.Locale
		}
		return nil
	case SectionSecurity:
		// security section has no runtime config.Config target;
		// it is consumed by the central-CRUD handlers directly.
		return nil
	default:
		return fmt.Errorf("configstore: unknown section %q", sec)
	}
}

// layerCentrals materialises the centrals table into
// [config.Config.Centrals]. Disabled rows are silently skipped so
// the operator can park a CCU without removing it.
func (s *Store) layerCentrals(ctx context.Context, cfg *config.Config, srcs map[string]FieldSource) error {
	rows, err := s.centrals.List(ctx)
	if err != nil {
		return fmt.Errorf("configstore: centrals list: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	out := make([]config.CentralConfig, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		if !r.Enabled {
			continue
		}
		// Password resolution per central, env-var wins over the
		// plaintext fallback. PasswordEnv is the *name* of the env
		// variable; the value comes from envLookup at runtime so
		// the plaintext never round-trips through this layer when
		// the operator chose the env path.
		password := r.PasswordPlain
		if r.PasswordEnv != "" {
			if v := s.envLookup(r.PasswordEnv); v != "" {
				password = v
				srcs["centrals."+r.Name+".password"] = SourceEnv
			}
		}
		out = append(out, config.CentralConfig{
			Name:                  r.Name,
			Host:                  r.Host,
			Port:                  r.Port,
			JSONRPCPort:           r.JSONRPCPort,
			Username:              r.Username,
			Password:              password,
			Interfaces:            r.Interfaces,
			Ports:                 r.Ports,
			TLS:                   r.TLS,
			TLSInsecureSkipVerify: r.TLSInsecureSkipVerify,
			PrimaryInterface:      r.PrimaryInterface,
			Visibility:            r.Visibility,
		})
	}
	if len(out) > 0 {
		cfg.Centrals = out
		srcs["centrals"] = SourceDB
	}
	return nil
}

// resolveEnvSecrets walks the in-memory config and overlays env-var
// values for any secret field whose env-name is configured. The
// approach is intentionally narrow: only known secret-bearing
// fields are checked. Adding a new env-resolvable secret requires
// adding it here.
//
// Per-central passwords: looked up via the password_env column;
// the SectionLoader path already populates `Password` with the
// plaintext fallback, env value (when set) takes precedence.
func (s *Store) resolveEnvSecrets(cfg *config.Config, srcs map[string]FieldSource) {
	// MQTT password env-resolver — convention: env var name
	// "OPENCCU_LOOM_MQTT_PASSWORD".
	if v := s.envLookup("OPENCCU_LOOM_MQTT_PASSWORD"); v != "" {
		cfg.North.MQTT.Password = v
		srcs["north.mqtt.password"] = SourceEnv
	}
	// OIDC client_secret env-resolver.
	if v := s.envLookup("OPENCCU_LOOM_OIDC_CLIENT_SECRET"); v != "" {
		cfg.North.REST.Auth.OIDC.ClientSecret = v
		srcs["north.rest.auth.oidc.client_secret"] = SourceEnv
	}
}
