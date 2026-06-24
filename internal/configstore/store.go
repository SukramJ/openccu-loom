// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
	owned := sectionFieldPaths()
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
		// Attribute each field that is *actually present* in the stored
		// section JSON to the DB tier. The SPA's source pill keys on the
		// full field path (e.g. "north.mqtt.enabled"), not the bare
		// section name, so attributing only the section key would leave
		// every field rendering as "default". Conversely, a field that
		// was pruned from the row by a per-field revert must NOT read as
		// db — it has fallen back to its built-in default, so it stays
		// at whatever attribution it already had (default). Bootstrap-
		// owned paths live under a section's struct but are sourced from
		// BootstrapConfig, not the section row, so they keep their
		// bootstrap attribution.
		tree := sectionTree(sec, row.ValueJSON)
		for _, path := range owned[sec] {
			if existing, ok := srcs[path]; ok && existing == SourceBootstrap {
				continue
			}
			if !pathPresent(tree, relativeFieldPath(sec, path)) {
				continue
			}
			srcs[path] = SourceDB
		}
		// Keep the bare section-key attribution too, so callers that
		// reason about whole sections (and the existing section-keyed
		// tests) keep working.
		srcs[string(sec)] = SourceDB
	}
	return nil
}

// sectionTree decodes a stored section payload into a generic JSON tree
// so present-field probing can run without per-section reflection. The
// OIDC section persists the OIDCConfig sub-tree directly (it is rooted at
// "north.rest.auth.oidc"), so its keys are already relative to the
// section. Returns nil on a non-object payload, which makes every
// pathPresent probe report false.
func sectionTree(_ Section, raw []byte) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// relativeFieldPath strips the section prefix from a full field path so
// it can be walked against the section's own JSON tree. e.g. for section
// "north.mqtt" and path "north.mqtt.broker_url" it returns "broker_url".
func relativeFieldPath(sec Section, path string) string {
	ss := string(sec)
	if path == ss {
		return ""
	}
	return strings.TrimPrefix(path, ss+".")
}

// pathPresent reports whether a dotted path resolves to a key that
// exists in the decoded section tree (the value may be null/zero — what
// matters is that the operator's stored payload carries the key, which
// is what marks it as a DB-tier override).
func pathPresent(tree map[string]any, dotted string) bool {
	if tree == nil || dotted == "" {
		return false
	}
	var cur any = tree
	parts := strings.Split(dotted, ".")
	for i, part := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		v, ok := m[part]
		if !ok {
			return false
		}
		if i == len(parts)-1 {
			return true
		}
		cur = v
	}
	return false
}

// sectionFieldPaths buckets every classified config field path under
// the section that owns it (longest matching section prefix wins, so
// "north.rest.auth.oidc.*" attaches to the OIDC section rather than
// REST). Sections with no config.Config-backed fields (e.g. the
// security/locale pseudo-sections) simply get no entries.
func sectionFieldPaths() map[Section][]string {
	all := AllSections()
	out := make(map[Section][]string, len(all))
	for _, f := range config.ClassifyFields(&config.Config{}) {
		var best Section
		for _, sec := range all {
			ss := string(sec)
			if f.Path == ss || strings.HasPrefix(f.Path, ss+".") {
				if len(ss) > len(string(best)) {
					best = sec
				}
			}
		}
		if best != "" {
			out[best] = append(out[best], f.Path)
		}
	}
	return out
}

// applySection routes one section payload to the corresponding
// [config.Config] sub-tree.
func applySection(sec Section, raw []byte, cfg *config.Config) error {
	switch sec {
	case SectionMQTT:
		return json.Unmarshal(raw, &cfg.North.MQTT)
	case SectionMatter:
		return json.Unmarshal(raw, &cfg.North.Matter)
	case SectionMCP:
		return json.Unmarshal(raw, &cfg.North.MCP)
	case SectionDiscovery:
		return json.Unmarshal(raw, &cfg.North.Discovery)
	case SectionREST:
		return json.Unmarshal(raw, &cfg.North.REST)
	case SectionOIDC:
		return json.Unmarshal(raw, &cfg.North.REST.Auth.OIDC)
	case SectionCCUAuth:
		return json.Unmarshal(raw, &cfg.North.REST.Auth.CCU)
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

// marshalSection is the inverse of [applySection]: it serialises the
// section's sub-tree of cfg into the JSON shape stored in
// config_sections. ok is false for sections that have no config.Config
// source (currently only [SectionSecurity]), which the seed skips.
func marshalSection(sec Section, cfg *config.Config) (raw []byte, ok bool, err error) {
	switch sec {
	case SectionMQTT:
		//nolint:gosec // G117: value is sealed by the section store transform before persistence (ADR 0027); see #20
		raw, err = json.Marshal(cfg.North.MQTT)
	case SectionMatter:
		raw, err = json.Marshal(cfg.North.Matter)
	case SectionMCP:
		raw, err = json.Marshal(cfg.North.MCP)
	case SectionDiscovery:
		raw, err = json.Marshal(cfg.North.Discovery)
	case SectionREST:
		raw, err = json.Marshal(cfg.North.REST)
	case SectionOIDC:
		//nolint:gosec // G117: value is sealed by the section store transform before persistence (ADR 0027); see #20
		raw, err = json.Marshal(cfg.North.REST.Auth.OIDC)
	case SectionCCUAuth:
		raw, err = json.Marshal(cfg.North.REST.Auth.CCU)
	case SectionUI:
		raw, err = json.Marshal(cfg.North.UI)
	case SectionCallback:
		raw, err = json.Marshal(cfg.Callback)
	case SectionCCUData:
		raw, err = json.Marshal(cfg.CCUData)
	case SectionReliability:
		raw, err = json.Marshal(cfg.Reliability)
	case SectionPersistence:
		raw, err = json.Marshal(cfg.Persistence)
	case SectionLocale:
		raw, err = json.Marshal(LocaleConfig{Locale: cfg.Locale})
	case SectionSecurity:
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("configstore: marshal: unknown section %q", sec)
	}
	if err != nil {
		return nil, false, fmt.Errorf("configstore: marshal %s: %w", sec, err)
	}
	return raw, true, nil
}

// SeedSectionsFromConfig performs a one-shot copy of the YAML-loaded
// config sections into the config_sections table on first run. It is a
// no-op when any section row already exists, so subsequent boots — and
// any operator SPA edit — keep the DB authoritative. Mirrors the
// one-shot users/centrals seed, giving operators a "write a full
// config.yaml, start once, then manage in the SPA" workflow.
//
// Secrets are seeded exactly as written in the YAML (matching how the
// SPA persists a section verbatim). A secret left empty in the YAML
// stays empty in the DB and is resolved from its env var at load time,
// so env-only secrets never land in the database.
//
// Returns the number of sections written.
func (s *Store) SeedSectionsFromConfig(ctx context.Context, cfg *config.Config, updatedBy string) (int, error) {
	if s.sections == nil || cfg == nil {
		return 0, nil
	}
	existing, err := s.sections.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("configstore: seed: list sections: %w", err)
	}
	if len(existing) > 0 {
		return 0, nil // already seeded or SPA-edited — DB is authoritative
	}
	n := 0
	for _, sec := range AllSections() {
		raw, ok, mErr := marshalSection(sec, cfg)
		if mErr != nil {
			return n, mErr
		}
		if !ok {
			continue
		}
		if _, err := s.sections.Put(ctx, string(sec), raw, updatedBy); err != nil {
			return n, fmt.Errorf("configstore: seed %s: %w", sec, err)
		}
		n++
	}
	return n, nil
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
			Behavior:              r.Behavior,
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
