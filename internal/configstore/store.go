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

	// base is the YAML/env tier the daemon booted from — the same
	// starting point [Store.OverlayInto] is handed at boot. [Store.Effective]
	// layers the DB tier onto a clone of it. Nil falls back to
	// [config.Default], which is only correct for a daemon that has no
	// config file at all.
	base *config.Config

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

// WithBaseConfig pins the YAML/env tier [Store.Effective] assembles on
// top of. The daemon passes the config it loaded from disk, which is the
// same base its own boot assembly ([Store.OverlayInto]) mutates in place.
//
// Without it Effective started from [config.Default], and the two
// assemblies disagreed on every field an operator had set in YAML and
// never touched in the SPA: GET /api/v1/config reported the built-in
// default, and the restart-pending provider — which diffs the running
// boot config against Effective — read that disagreement as a staged
// change and lit the restart banner permanently. `backup.schedule` was
// the reproducible case: it is restart-required, carried by no editable
// section, so a YAML value could never appear on the Effective side.
//
// The store keeps a clone: a later in-place hot-reload of the daemon's
// config must not move the base that Effective replays from.
func WithBaseConfig(base *config.Config) Option {
	return func(s *Store) {
		if base != nil {
			s.base = config.Clone(base)
		}
	}
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

// sectionUnmanagedPaths lists full config field paths that are NOT carried
// by their nominal editable config section. They are stripped from every
// section payload on both persist (marshalSection / SeedSectionsFromConfig)
// and load (applySection), and filtered out of the SPA's editable schema,
// so a section PUT can neither override nor wipe them:
//
//   - north.rest.listen is a BOOTSTRAP-tier field owned by BootstrapConfig
//     (the OPENCCU_LOOM_REST_LISTEN env / YAML value). Keeping it out of the
//     REST section means the bootstrap value always wins and a stale stored
//     row can never pin an old bind address after a restart.
//   - north.rest.auth.users and north.rest.auth.tokens are credentials
//     managed exclusively by the SQLite user/token stores (the
//     /api/v1/users and /auth/tokens CRUD). Keeping them out of the REST
//     section means a REST PUT that omits them can never wipe an operator's
//     logins.
//   - bootstrap.allow_first_run_setup is a BOOTSTRAP-tier hardening toggle
//     that closes the unauthenticated /setup surface. It must be settable
//     only from the YAML the operator controls — a stored row (or a REST
//     PUT) that re-opened it would defeat the control it names.
var sectionUnmanagedPaths = map[string]struct{}{
	"north.rest.listen":               {},
	"north.rest.auth.users":           {},
	"north.rest.auth.tokens":          {},
	"bootstrap.allow_first_run_setup": {},
}

// UnmanagedFieldPaths returns the set of config field paths that are not
// editable through the SPA section editor (bootstrap-tier fields and
// SQLite-managed credentials). The schema handler filters these out so the
// SPA never renders them as section fields.
func UnmanagedFieldPaths() map[string]struct{} {
	out := make(map[string]struct{}, len(sectionUnmanagedPaths))
	for k := range sectionUnmanagedPaths {
		out[k] = struct{}{}
	}
	return out
}

// StripForeignSectionFields removes everything sec must not carry from a
// section payload before it is validated, persisted or applied. It is the
// exported entry point the REST handler uses; the boot-time overlay uses the
// same logic via applySection / marshalSection.
func StripForeignSectionFields(sec Section, raw []byte) []byte {
	return stripForeignFields(sec, raw)
}

// foreignRelPaths returns the paths — relative to sec — that a sec row must
// never carry. Two disjoint groups:
//
//   - the unmanaged fields listed in sectionUnmanagedPaths (bootstrap-tier
//     listen, SQLite-managed auth credentials), and
//   - the sub-tree of every *nested* section: a section whose name starts with
//     sec's name owns that sub-tree exclusively. north.rest.auth.oidc /
//     .ccu / .ha_ingress all live inside the config.NorthREST struct, so a
//     naive marshal of north.rest would duplicate them into the REST row.
//
// The duplication is not cosmetic: applySection merges (json.Unmarshal into an
// already-populated struct) and the parent section is layered before its nested
// ones, so a value that survives only in the parent row silently reappears at
// the next boot. Resetting north.rest.auth.ha_ingress.enabled or deleting the
// whole nested section would then fail to disable an auth passthrough
// (see docs/adr/0044-single-port-onboarding-and-ha-ingress-auth.md).
func foreignRelPaths(sec Section) []string {
	prefix := string(sec) + "."
	var rels []string
	for full := range sectionUnmanagedPaths {
		if rel, ok := strings.CutPrefix(full, prefix); ok {
			rels = append(rels, rel)
		}
	}
	for _, other := range AllSections() {
		if other == sec {
			continue
		}
		if rel, ok := strings.CutPrefix(string(other), prefix); ok {
			rels = append(rels, rel)
		}
	}
	return rels
}

// stripForeignFields removes every path foreignRelPaths reports for sec from a
// section's JSON payload, returning the payload unchanged when it carries none
// (the common case) or is not a JSON object.
func stripForeignFields(sec Section, raw []byte) []byte {
	rels := foreignRelPaths(sec)
	if len(rels) == 0 {
		return raw
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	changed := false
	for _, rel := range rels {
		if deleteDeep(m, rel) {
			changed = true
		}
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// deleteDeep removes a dotted path from nested JSON objects, reporting
// whether anything was removed. Missing intermediate objects are a no-op.
func deleteDeep(m map[string]any, path string) bool {
	parts := strings.Split(path, ".")
	cur := m
	for _, part := range parts[:len(parts)-1] {
		next, ok := cur[part].(map[string]any)
		if !ok {
			return false
		}
		cur = next
	}
	last := parts[len(parts)-1]
	if _, ok := cur[last]; !ok {
		return false
	}
	delete(cur, last)
	return true
}

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
//
// The merge is ALL-OR-NOTHING: it runs against a scratch copy and is
// committed into cfg only once every section and every central row has been
// layered successfully. On error the caller's config is exactly what it
// handed in — the YAML tier, defaults intact. A section can fail for reasons
// that are not exotic (a sealed row whose master key is gone, a row whose
// JSON no longer matches its struct after a downgrade), and merging in
// AllSections order means an abort in the middle would otherwise leave the
// daemon running on a config that is part DB tier, part YAML tier, and never
// defaulted or validated: an auth scheme the operator disabled in the SPA
// would come back up because its section is layered after the failing one.
func (s *Store) OverlayInto(ctx context.Context, cfg *config.Config) (map[string]FieldSource, error) {
	staged := config.Clone(cfg)
	srcs := make(map[string]FieldSource)
	// north.rest.listen is bootstrap-tier: the value already in cfg came from
	// the YAML/OPENCCU_LOOM_REST_LISTEN bootstrap layer, so attribute it to
	// bootstrap up front. layerSections skips it, so a stored REST row can
	// neither overwrite the value nor flip the source pill to db.
	srcs["north.rest.listen"] = SourceBootstrap
	if s.sections != nil {
		if err := s.layerSections(ctx, staged, srcs); err != nil {
			return nil, err
		}
	}
	if s.centrals != nil {
		if err := s.layerCentrals(ctx, staged, srcs); err != nil {
			return nil, err
		}
	}
	s.resolveEnvSecrets(staged, srcs)
	// A stored section may omit fields (e.g. north.mqtt without
	// topic_base) that the base config had defaulted before the overlay
	// clobbered the whole sub-tree. Re-fill defaults so a partial section
	// does not leave a required field at its zero value.
	staged.ApplyDefaults()
	*cfg = *staged
	return srcs, nil
}

// Effective assembles the daemon's runtime config by:
//  1. starting from the YAML/env tier the daemon booted from (see
//     [WithBaseConfig]; [config.Default] when none was pinned),
//  2. applying the bootstrap tier (data_dir, listen, logging),
//  3. layering DB-tier section snapshots on top,
//  4. resolving env-var references for secret fields,
//  5. filling in defaults for anything still unset.
//
// Step 1 must be the daemon's own starting point, not the built-in
// defaults: the section seed only runs on a database with no sections at
// all, so a field set solely in YAML has no row to overlay and an
// assembly starting from defaults silently drops it. Everything that
// compares this result against the running config — the restart-pending
// provider above all — then reads that gap as a pending change.
//
// Returns an error when a section row cannot be read or applied: a
// malformed payload, a row whose JSON no longer matches its struct, or a
// sealed value the section store cannot open because the master key is
// unavailable. Missing sections fall back to defaults silently.
func (s *Store) Effective(ctx context.Context) (*EffectiveResult, error) {
	cfg := s.baseClone()
	srcs := s.bootstrapTierSources(cfg)

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

// PlaintextSecretsAllowed reports whether the operator opted into storing a
// central's CCU password in the clear, i.e. the `security` section's
// allow_plaintext_secrets flag. It is read live (not cached) so a change the
// operator saves in the SPA governs the very next write.
//
// Everything that persists a central row consults this through the centrals
// store: when no at-rest master key is available (ADR 0027's degraded
// fallback), the password would land in the database as cleartext, and the
// documented default is to refuse that write rather than perform it silently.
//
// Every failure mode — no section store, an unreadable row (a sealed value
// whose key is gone), malformed JSON — reports false: the safe default is the
// documented one, and consent must be positively expressed.
func (s *Store) PlaintextSecretsAllowed(ctx context.Context) bool {
	if s.sections == nil {
		return false
	}
	row, err := s.sections.Get(ctx, string(SectionSecurity))
	if err != nil {
		return false
	}
	var v SecurityConfig
	if err := json.Unmarshal(row.ValueJSON, &v); err != nil {
		return false
	}
	return v.AllowPlaintextSecrets
}

// baseClone returns a fresh copy of the pinned YAML/env base, or the
// built-in defaults when no base was pinned (a daemon booted without a
// config file, and every test that constructs a bare Store).
func (s *Store) baseClone() *config.Config {
	if s.base == nil {
		return config.Default()
	}
	return config.Clone(s.base)
}

// bootstrapTierSources applies the bootstrap-tier fields onto cfg and
// seeds the source attribution. Every field the YAML base carries beyond
// the built-in defaults is attributed to the bootstrap tier as well, so
// the SPA's source pill says "bootstrap" for a value that came out of
// the config file instead of mislabelling it "default". A DB row layered
// afterwards overrides the attribution.
func (s *Store) bootstrapTierSources(cfg *config.Config) map[string]FieldSource {
	srcs := make(map[string]FieldSource)
	for _, path := range config.ChangedFields(config.Default(), cfg) {
		srcs[path] = SourceBootstrap
	}

	// Bootstrap-tier wins on the fields it owns.
	cfg.DataDir = s.bootstrap.DataDir
	cfg.Logging = s.bootstrap.Logging
	cfg.North.REST.Listen = s.bootstrap.Listen.REST
	cfg.Bootstrap = s.bootstrap.Bootstrap
	for path := range bootstrapOwnedPaths {
		srcs[path] = SourceBootstrap
	}
	return srcs
}

// bootstrapOwnedPaths are the config paths BootstrapConfig owns outright:
// their value comes from the process environment / YAML bootstrap tier,
// never from a config_sections row, so a stored row must not be able to
// claim them — see [sectionUnmanagedPaths] for the persistence half of
// the same rule.
var bootstrapOwnedPaths = map[string]struct{}{
	"data_dir":                        {},
	"logging":                         {},
	"north.rest.listen":               {},
	"bootstrap.allow_first_run_setup": {},
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
			// Unmanaged paths are never carried by a section row (they are
			// bootstrap-tier or SQLite-managed), so they must never be
			// attributed to the DB tier even when their owning section is
			// present. north.rest.listen keeps its bootstrap attribution.
			if _, unmanaged := sectionUnmanagedPaths[path]; unmanaged {
				continue
			}
			// Only the paths BootstrapConfig genuinely owns keep their
			// bootstrap attribution here. A blanket "already bootstrap"
			// guard would be wrong now that the YAML tier attributes every
			// field it carries: a section the operator then saved in the SPA
			// would keep rendering its source pill as the file it no longer
			// comes from.
			if _, owned := bootstrapOwnedPaths[path]; owned {
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

// ApplySectionToConfig overlays one section payload onto cfg, mirroring
// the boot-time overlay so callers (e.g. the REST section-PUT handler)
// can assemble the candidate effective config a save would produce and
// validate it before persisting. Foreign fields are stripped exactly
// as at boot, so the candidate reflects what actually lands on disk.
func ApplySectionToConfig(sec Section, raw []byte, cfg *config.Config) error {
	return applySection(sec, raw, cfg)
}

// applySection routes one section payload to the corresponding
// [config.Config] sub-tree. Foreign fields — the unmanaged ones
// (bootstrap-tier listen, SQLite-managed auth credentials) and every
// nested section's sub-tree — are stripped first, so neither a stored
// row written before that rule existed nor a hand-crafted one can pin
// or wipe a value it does not own.
func applySection(sec Section, raw []byte, cfg *config.Config) error {
	raw = stripForeignFields(sec, raw)
	switch sec {
	case SectionMQTT:
		return overlaySection(raw, &cfg.North.MQTT)
	case SectionMatter:
		return overlaySection(raw, &cfg.North.Matter)
	case SectionMCP:
		return overlaySection(raw, &cfg.North.MCP)
	case SectionDiscovery:
		return overlaySection(raw, &cfg.North.Discovery)
	case SectionWebhook:
		return overlaySection(raw, &cfg.North.Webhook)
	case SectionREST:
		return overlaySection(raw, &cfg.North.REST)
	case SectionOIDC:
		return overlaySection(raw, &cfg.North.REST.Auth.OIDC)
	case SectionCCUAuth:
		return overlaySection(raw, &cfg.North.REST.Auth.CCU)
	case SectionHAIngress:
		return overlaySection(raw, &cfg.North.REST.Auth.HAIngress)
	case SectionUI:
		return overlaySection(raw, &cfg.North.UI)
	case SectionCallback:
		return overlaySection(raw, &cfg.Callback)
	case SectionCCUData:
		return overlaySection(raw, &cfg.CCUData)
	case SectionReliability:
		return overlaySection(raw, &cfg.Reliability)
	case SectionPersistence:
		return overlaySection(raw, &cfg.Persistence)
	case SectionAlarm:
		return overlaySection(raw, &cfg.Alarm)
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
		// The security section has no runtime config.Config target: it is
		// read on demand through [Store.PlaintextSecretsAllowed], which the
		// centrals store consults before it persists a cleartext password.
		return nil
	default:
		return fmt.Errorf("configstore: unknown section %q", sec)
	}
}

// MarshalSection serialises the section's sub-tree of cfg into the JSON
// shape stored in config_sections — the inverse of [ApplySectionToConfig].
// ok is false for sections that have no [config.Config] source (currently
// only [SectionSecurity]); a caller must then persist its own payload.
//
// The REST section-PUT handler uses it to persist the very candidate it
// validated, so the stored row always describes the whole section instead of
// only the fragment the client happened to send.
func MarshalSection(sec Section, cfg *config.Config) (raw []byte, ok bool, err error) {
	return marshalSection(sec, cfg)
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
	case SectionWebhook:
		//nolint:gosec // G117: value is sealed by the section store transform before persistence (ADR 0027); see #20
		raw, err = json.Marshal(cfg.North.Webhook)
	case SectionREST:
		raw, err = json.Marshal(cfg.North.REST)
	case SectionOIDC:
		//nolint:gosec // G117: value is sealed by the section store transform before persistence (ADR 0027); see #20
		raw, err = json.Marshal(cfg.North.REST.Auth.OIDC)
	case SectionCCUAuth:
		raw, err = json.Marshal(cfg.North.REST.Auth.CCU)
	case SectionHAIngress:
		raw, err = json.Marshal(cfg.North.REST.Auth.HAIngress)
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
	case SectionAlarm:
		raw, err = json.Marshal(cfg.Alarm)
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
	// Never persist foreign fields into a section row: north.rest.listen is
	// bootstrap-tier, the auth credentials live only in the SQLite user/token
	// stores, and every nested section (north.rest.auth.oidc / .ccu /
	// .ha_ingress) owns its own row. Stripping here keeps the seed and every
	// save free of them.
	return stripForeignFields(sec, raw), true, nil
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
//
// The tier rule keys on the presence of ROWS, not of enabled rows: an empty
// table means the DB tier is unused and the YAML `centrals:` list stays
// authoritative, while a table that holds only parked rows is in use and
// authoritative — it means "no central", not "fall back to the config file".
// Anything else makes disabling the last CCU in the SPA reconnect to it on
// the next restart, because the YAML entry the first-run seed copied into
// the table is still there. Mirrors the auth-side tier rule in
// enabledCentralRows (cmd/openccu-loom/ccu_auth_wiring.go).
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
		cc, usedEnv := RowToCentralConfig(*r, s.envLookup)
		if usedEnv {
			srcs["centrals."+r.Name+".password"] = SourceEnv
		}
		out = append(out, cc)
	}
	cfg.Centrals = out
	srcs["centrals"] = SourceDB
	return nil
}

// RowToCentralConfig converts one persisted [sqlite.CentralRow] into the
// in-memory [config.CentralConfig] shape a [*central.Unit] is built from.
// Mirrors the per-row mapping [layerCentrals] applies at boot, and is the
// shared converter the live-CCU-adopt orchestrator uses to turn a freshly
// PUT admin/centrals row into the config the runtime adopt path needs.
//
// envLookup resolves PasswordEnv (the *name* of an env var); production
// callers pass [os.Getenv]. usedEnv reports whether the env var was applied
// (non-empty), so a caller that tracks field-source attribution (as
// [layerCentrals] does) can record it — the plaintext fallback never
// round-trips through this layer when the operator chose the env path.
func RowToCentralConfig(r sqlite.CentralRow, envLookup func(string) string) (cc config.CentralConfig, usedEnv bool) {
	password := r.PasswordPlain
	if r.PasswordEnv != "" {
		if v := envLookup(r.PasswordEnv); v != "" {
			password = v
			usedEnv = true
		}
	}
	return config.CentralConfig{
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
	}, usedEnv
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

// RedactSectionSecrets returns raw with every cfg:"secret" leaf of section sec
// replaced by the JSON null literal, leaving all other keys untouched.
//
// A section row is not secret or non-secret as a whole: "north.mqtt" carries a
// broker URL next to a password. Callers that hand a stored row to somewhere
// less trusted than the database — the config exporter above all, whose rows
// arrive already decrypted by the store's crypto wiring — need the per-leaf
// distinction that [config.ClassifyFields] already draws.
//
// A payload that is not a JSON object is returned unchanged; there is nothing
// to walk, and refusing to emit it would hide the row instead of redacting it.
func RedactSectionSecrets(sec Section, raw []byte) []byte {
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil || tree == nil {
		return raw
	}
	var redacted bool
	for _, f := range config.ClassifyFields(&config.Config{}) {
		if f.Class != config.FieldSecret {
			continue
		}
		rel := relativeFieldPath(sec, f.Path)
		if rel == "" || rel == f.Path {
			// Not a leaf of this section.
			continue
		}
		if redactPath(tree, rel) {
			redacted = true
		}
	}
	if !redacted {
		return raw
	}
	out, err := json.Marshal(tree)
	if err != nil {
		return raw
	}
	return out
}

// RestoreRedactedSecrets returns incoming with every cfg:"secret" leaf of
// section sec that arrives as JSON null replaced by the value the same leaf
// carries in stored — or dropped entirely when stored has none.
//
// It is the inverse of [RedactSectionSecrets] and the reason a redacted export
// can be imported at all: the document a redacted export produces carries null
// where the credential was, and writing that back verbatim replaces the
// operator's live MQTT / OIDC / Matter credentials with null. The merge is per
// leaf, so everything the operator did edit in the exported file still wins —
// only the leaves the export refused to disclose fall back to the database.
//
// A payload that is not a JSON object is returned unchanged; there is nothing
// to walk.
func RestoreRedactedSecrets(sec Section, incoming, stored []byte) []byte {
	var tree map[string]any
	if err := json.Unmarshal(incoming, &tree); err != nil || tree == nil {
		return incoming
	}
	var storedTree map[string]any
	if len(stored) > 0 {
		if err := json.Unmarshal(stored, &storedTree); err != nil {
			storedTree = nil
		}
	}
	var restored bool
	for _, f := range config.ClassifyFields(&config.Config{}) {
		if f.Class != config.FieldSecret {
			continue
		}
		rel := relativeFieldPath(sec, f.Path)
		if rel == "" || rel == f.Path {
			// Not a leaf of this section.
			continue
		}
		if restorePath(tree, storedTree, rel) {
			restored = true
		}
	}
	if !restored {
		return incoming
	}
	out, err := json.Marshal(tree)
	if err != nil {
		return incoming
	}
	return out
}

// restorePath replaces the null leaf at dotted inside tree with the value
// stored carries at the same path, deleting the key when stored has none, and
// reports whether it touched anything. A leaf that is absent or non-null in
// tree is left alone: only a redaction marker (null) may be overridden, so an
// operator who deliberately cleared a secret in the exported document still
// clears it.
func restorePath(tree, stored map[string]any, dotted string) bool {
	parts := strings.Split(dotted, ".")
	cur := tree
	for i, part := range parts {
		if i == len(parts)-1 {
			v, ok := cur[part]
			if !ok || v != nil {
				return false
			}
			if sv, ok := lookupPath(stored, parts); ok && sv != nil {
				cur[part] = sv
			} else {
				delete(cur, part)
			}
			return true
		}
		next, ok := cur[part].(map[string]any)
		if !ok {
			return false
		}
		cur = next
	}
	return false
}

// lookupPath resolves the dotted path parts inside tree, reporting whether the
// leaf key exists.
func lookupPath(tree map[string]any, parts []string) (any, bool) {
	cur := tree
	for i, part := range parts {
		if i == len(parts)-1 {
			v, ok := cur[part]
			return v, ok
		}
		next, ok := cur[part].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return nil, false
}

// redactPath nulls the leaf at dotted inside tree, reporting whether it was
// present. Nulling rather than deleting keeps the key visible, so a reader can
// tell "withheld" from "never set" — the same reason the audit log masks
// rather than drops a credential value.
func redactPath(tree map[string]any, dotted string) bool {
	parts := strings.Split(dotted, ".")
	cur := tree
	for i, part := range parts {
		if i == len(parts)-1 {
			if _, ok := cur[part]; !ok {
				return false
			}
			cur[part] = nil
			return true
		}
		next, ok := cur[part].(map[string]any)
		if !ok {
			return false
		}
		cur = next
	}
	return false
}
