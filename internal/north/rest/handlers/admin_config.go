// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// ConfigAdminService is the DI surface for the /api/v1/config
// endpoints. The router wires a [*configstore.Store] and the
// [*sqlite.ConfigSectionStore] underneath.
type ConfigAdminService interface {
	// Effective returns the assembled runtime config with per-field
	// source attribution for the SPA's source pill.
	Effective(ctx context.Context) (*configstore.EffectiveResult, error)
	// GetSection returns the persisted section row (used by the SPA
	// to populate the editor with the DB-only view).
	GetSection(ctx context.Context, section configstore.Section) (sqlite.SectionRow, error)
	// PutSection validates and persists a section payload.
	PutSection(ctx context.Context, section configstore.Section, valueJSON []byte, updatedBy string) (sqlite.SectionRow, error)
	// DeleteSection reverts a section to defaults.
	DeleteSection(ctx context.Context, section configstore.Section) error
}

// SchemaField is one entry of `GET /api/v1/config/schema`.
type SchemaField struct {
	Path   string `json:"path"`
	Class  string `json:"class"`
	GoType string `json:"go_type"`
	// RestartRequired is emitted unconditionally: the published schema
	// lists it among SchemaField's required members, so omitting the
	// false case breaks every generated client that enforces them.
	RestartRequired bool `json:"restart_required"`
	// Default carries the daemon's effective default for the
	// field — the value used when neither YAML nor SQLite nor an
	// env-override supplies one. For most fields this is the Go
	// zero value (0 / false / ""), so the SPA renders nothing.
	// Curated entries in [consumerDefaults] surface "implicit"
	// defaults that live in the consuming code, not in
	// config.Default() — e.g. ValuesCache.Enabled defaults to
	// true via a pointer-bool helper, FlushInterval falls back
	// to 60s when the field is zero, etc.
	Default any `json:"default,omitempty"`
}

// consumerDefaults pins the runtime defaults that *do not* show
// up in config.Default()/applyDefaults because the consuming
// code interprets the zero value as "use this hard-coded
// fallback". The SPA reads these as input placeholders so
// operators see what they get if they leave a field empty,
// without having to spelunk into the daemon source.
//
// Keep entries narrow + targeted; only add a path here when the
// zero value is misleading.
var consumerDefaults = map[string]any{
	// Reliability — see internal/config/config.go ReliabilityConfig.
	"reliability.command_retry_initial_delay":          int64(2_000_000_000), // 2 s (hmreliability.RetryInitialBackoff)
	"reliability.command_throttle_inter_command_delay": int64(0),             // disabled (no inter-command pacing)
	// Persistence/ValuesCache — see internal/config/config.go ValuesCacheConfig
	// and internal/central/adapter.DefaultValuesCacheFlushInterval.
	"persistence.values_cache.enabled":        true,
	"persistence.values_cache.flush_interval": int64(60_000_000_000), // 60 s
	// Persistence/History — see internal/config/config.go HistoryConfig
	// and internal/history.Default* constants. Opt-in: the *bool fields
	// default to false and need the explicit value so the SPA renders an
	// unchecked checkbox rather than an indeterminate one.
	"persistence.history.enabled":        false,
	"persistence.history.retention":      int64(2_592_000_000_000_000), // 720 h (30 d)
	"persistence.history.flush_interval": int64(5_000_000_000),         // 5 s
	"persistence.history.export.enabled": false,
	// WS — see internal/config/config.go NorthRESTWS.
	"north.rest.ws.replay_capacity": 1024,
	// Rate-limit — see middleware/ratelimit; zero means "use the
	// fallback" inside the middleware.
	"north.rest.rate_limit.requests_per_second": 10,
	"north.rest.rate_limit.burst":               30,
	// Matter — see internal/north/matter/* defaults. Zero values
	// are misleading because the bridge layers fall back to the
	// IANA test-vendor block and well-known commissioning codes
	// at runtime.
	"north.matter.listen":                   ":5540",
	"north.matter.vendor_id":                0xFFF1,
	"north.matter.product_id":               0x8000,
	"north.matter.node_label":               "openccu-loom",
	"north.matter.discriminator":            0xF00,
	"north.matter.mdns_advertise":           "zeroconf",
	"north.matter.commissioning.iterations": 1000,
	// MCP — see internal/config/config.go NorthMCP.MountPath(): the
	// empty path falls back to "/mcp" at mount time.
	"north.mcp.path": "/mcp",
	// REST / OIDC — see internal/auth/oidc and middleware/openapi.
	// REST and UI surfaces default to ON; *bool fields render as
	// `null` in JSON when unset, so the SPA needs the explicit
	// default to surface a correctly-checked checkbox.
	"north.rest.enabled":              true,
	"north.ui.enabled":                true,
	"north.discovery.mdns.enabled":    true,
	"north.rest.openapi_validate":     true,
	"north.rest.auth.basic_enabled":   true,
	"north.rest.auth.bearer_enabled":  true,
	"north.rest.auth.oidc.role_claim": "role",
	// ccu_data.translations_path / ccu_data.easymode_path deliberately have
	// no entry here: loadTranslations/loadEasymode (cmd/openccu-loom/
	// daemon_ccudata.go) use the configured path only when non-empty and
	// otherwise call the *Embedded loader directly — there is no
	// filesystem fallback path to show as a placeholder. The help text
	// (config.help.ccu_data.translations_path / .easymode_path) already
	// states the real default in words ("the embedded archive bundled
	// with the binary"); a fabricated './var/ccu_data/...' placeholder
	// here led operators to type a path that does not exist on a normal
	// install.
	// Alarm — see internal/config/config.go AlarmConfig / applyDefaults.
	"alarm.default_siren_seconds":             180,
	"alarm.max_acoustic_per_incident_seconds": 900,
	"alarm.stop_verify_seconds":               120,
	"alarm.journal_retention_days":            90,
	"alarm.restart_loop_breaker":              3,
}

// SchemaResponse bundles the field descriptors + the list of known
// sections so the SPA can render tabs.
type SchemaResponse struct {
	Sections []string      `json:"sections"`
	Fields   []SchemaField `json:"fields"`
}

// restartRequiredPaths enumerates the fields whose change cannot be
// hot-reloaded, so the schema editor can badge them. It is derived from
// [config.RestartRules] — the same table [config.RestartRequiredDiff]
// evaluates — rather than maintained here, because the two lists drifted
// while they were independent: the alarm block and the Basic/Bearer auth
// gates carried the badge but were never diffed, so the PUT response
// answered restart_required:false and /restart-pending never flagged the
// staged change. Mirrors §7.1 Q12 of SPECIFICATION.md.
var restartRequiredPaths = config.RestartRequiredFieldPaths()

// dedicatedEditorPaths lists config fields that a purpose-built editor
// owns, so the generic section editor must not render them. Unlike
// configstore.UnmanagedFieldPaths these ARE carried by their section and
// persist through the normal section path — only the SPA form skips
// them, because a nested map has no meaningful generic input.
//
//   - north.ui.profiles is edited under Settings → Navigation & views,
//     which needs the shipped defaults, the floor and the capability
//     gates alongside each value. None of that is expressible in the
//     flat field schema.
var dedicatedEditorPaths = map[string]struct{}{
	"north.ui.profiles": {},
}

// GetConfigSchema renders the typed schema for the SPA editor. No
// secrets leave this endpoint — secret-classed fields are listed as
// `class: "secret"` so the SPA shows a placeholder + env-resolver
// hint instead of an editor.
func GetConfigSchema() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fields := config.ClassifyFields(&config.Config{})
		out := SchemaResponse{
			Sections: make([]string, 0, len(configstore.AllSections())),
			Fields:   make([]SchemaField, 0, len(fields)),
		}
		for _, sec := range configstore.AllSections() {
			out.Sections = append(out.Sections, string(sec))
		}
		unmanaged := configstore.UnmanagedFieldPaths()
		for _, f := range fields {
			// Skip fields that are not editable through the section editor:
			// bootstrap-tier north.rest.listen and the SQLite-managed auth
			// credentials (north.rest.auth.users / .tokens). They are managed
			// by BootstrapConfig and the dedicated user/token CRUD surfaces,
			// so the SPA must not render them as REST-section fields.
			if _, skip := unmanaged[f.Path]; skip {
				continue
			}
			if _, skip := dedicatedEditorPaths[f.Path]; skip {
				continue
			}
			_, restart := restartRequiredPaths[f.Path]
			def := consumerDefaults[f.Path]
			out.Fields = append(out.Fields, SchemaField{
				Path:            f.Path,
				Class:           string(f.Class),
				GoType:          f.GoType,
				RestartRequired: restart,
				Default:         def,
			})
		}
		JSON(w, http.StatusOK, out)
	}
}

// ConfigSnapshotResponse is what `GET /api/v1/config` returns: the
// effective config (secrets masked) plus the per-field source map.
type ConfigSnapshotResponse struct {
	Config  map[string]any                     `json:"config"`
	Sources map[string]configstore.FieldSource `json:"sources"`
}

// GetEffectiveConfig assembles the runtime config and emits the
// SPA-friendly snapshot. Secret fields are replaced with the
// sentinel "***" so the response is safe to log.
func GetEffectiveConfig(svc ConfigAdminService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Config service unavailable", ""))
			return
		}
		res, err := svc.Effective(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Effective config failed", err)
			return
		}
		masked := maskSecrets(res.Config)
		JSON(w, http.StatusOK, ConfigSnapshotResponse{Config: masked, Sources: res.Sources})
	}
}

// maskSecrets returns a JSON-shaped map of cfg with secret-class
// fields replaced by "***". Done by round-tripping through JSON and
// walking the descriptor list — cheap and reflection-free at the
// field-overwrite step.
func maskSecrets(cfg *config.Config) map[string]any {
	raw, _ := json.Marshal(cfg)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	secretPaths := secretPathSet()
	maskPath(out, "", secretPaths)
	return out
}

func secretPathSet() map[string]struct{} {
	set := make(map[string]struct{})
	for path := range secretPathTypes() {
		set[path] = struct{}{}
	}
	return set
}

// secretPathTypes maps every secret-class config path to its Go type name.
// The type decides how an empty payload value is read on save: an empty
// string clears a string secret but is only ever a placeholder for a
// complex one (see [restoreMaskedSecrets]).
func secretPathTypes() map[string]string {
	types := make(map[string]string)
	for _, f := range config.ClassifyFields(&config.Config{}) {
		if f.Class == config.FieldSecret {
			types[f.Path] = f.GoType
		}
	}
	return types
}

// isStringSecret reports whether the secret at path is a plain string
// field, i.e. one the operator can clear by emptying its input.
func isStringSecret(goType string) bool { return goType == "string" }

// secretIsSet reports whether a secret value carries anything worth
// masking. An unset secret masked to "***" is indistinguishable from a
// configured one, which hides exactly the failure this matters for: a
// credential that was silently dropped still reads as "configured" in the
// UI. Empty values are therefore passed through so the SPA can render
// "not set".
func secretIsSet(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case map[string]any:
		return len(t) > 0
	case []any:
		return len(t) > 0
	case float64:
		return t != 0
	case bool:
		return t
	default:
		return true
	}
}

func maskPath(v any, prefix string, set map[string]struct{}) {
	// Slice-of-struct secrets (e.g. centrals[].password) are classified
	// under the singular slice prefix ("centrals.password"), so each element
	// is re-walked under the same prefix — without this, array-nested secrets
	// are never masked and leak in cleartext to every authenticated reader.
	if arr, ok := v.([]any); ok {
		for _, e := range arr {
			maskPath(e, prefix, set)
		}
		return
	}
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	for k, val := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if _, hit := set[path]; hit {
			if secretIsSet(val) {
				m[k] = maskSentinel
			}
			continue
		}
		maskPath(val, path, set)
	}
}

// maskSectionSecrets is the section-scoped counterpart of maskSecrets: it
// replaces every secret-class value inside a single section's stored JSON with
// the "***" sentinel. The section store opens (decrypts) secrets on read, so
// without this the per-section GET would hand the operator's cleartext
// credentials (MQTT password, OIDC client secret, basic-auth hashes) to the
// browser — unlike the snapshot endpoint, which already masks. The SPA round-
// trips the sentinel and [restoreMaskedSecrets] swaps it back on save, so a
// masked load still persists correctly. Returns the input unchanged for a
// section with no secret fields or a non-object payload.
func maskSectionSecrets(section configstore.Section, valueJSON []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(valueJSON, &m); err != nil {
		return valueJSON
	}
	prefix := string(section) + "."
	rel := make(map[string]struct{})
	for full := range secretPathSet() {
		if r, ok := strings.CutPrefix(full, prefix); ok {
			rel[r] = struct{}{}
		}
	}
	if len(rel) == 0 {
		return valueJSON
	}
	maskPath(m, "", rel)
	out, err := json.Marshal(m)
	if err != nil {
		return valueJSON
	}
	return out
}

// maskSentinel is the placeholder maskSecrets writes in place of every
// secret-class value so the GET response is safe to log. The SPA echoes it
// back unchanged for any secret the operator did not edit; restoreMaskedSecrets
// turns it back into the real value before validation and persistence.
const maskSentinel = "***"

// restoreMaskedSecrets reconciles the secret fields of a section PUT against
// the operator's current real values, scoped to one section. A save must never
// destroy a credential the operator did not touch, and must still let them
// clear one deliberately. The payload distinguishes the two:
//
//   - key absent, JSON null, or the masked "***" sentinel → unchanged: the
//     stored value is substituted before validation and persistence.
//   - "" on a string secret → deliberately cleared: persisted as empty.
//   - "" on a complex secret (map[string]string, e.g. north.rest.auth.users)
//     → unchanged: the editor renders no widget for these, so an empty string
//     is only ever its "no value" placeholder. Persisting it verbatim would
//     fail the strict unmarshal with a 400.
//   - anything else → an operator-supplied new secret, left untouched.
//
// Treating an absent key as "unchanged" also means a REST client that omits a
// secret field keeps the stored one instead of silently wiping it.
func restoreMaskedSecrets(current *config.Config, section configstore.Section, raw json.RawMessage) json.RawMessage {
	if current == nil {
		return raw
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return raw // non-object payloads pass through; validateSection rejects them
	}
	curRaw, err := json.Marshal(current)
	if err != nil {
		return raw
	}
	var curMap map[string]any
	if err := json.Unmarshal(curRaw, &curMap); err != nil {
		return raw
	}
	prefix := string(section) + "."
	unmanaged := configstore.UnmanagedFieldPaths()
	changed := false
	for full, goType := range secretPathTypes() {
		rel, ok := strings.CutPrefix(full, prefix)
		if !ok {
			continue
		}
		// Never restore a field the section does not carry: the SQLite-managed
		// credentials (north.rest.auth.users / .tokens) were just stripped from
		// the payload on purpose, and putting them back would re-persist them
		// into the very section they were removed from.
		if _, skip := unmanaged[full]; skip {
			continue
		}
		// Same for a secret a nested section owns — north.rest.auth.oidc's
		// client_secret belongs to the OIDC row, not to north.rest.
		if owningSection(full) != section {
			continue
		}
		if !secretPayloadIsPlaceholder(payload, rel, goType) {
			continue
		}
		setDeepAny(payload, rel, getDeepAny(curMap, full))
		changed = true
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return out
}

// envSourcedSecretPaths returns the section-relative paths of the secrets
// [configstore.Store.Effective] resolved from the process environment and
// that the submitted payload carries no operator-supplied value for.
//
// The attribution comes from Effective itself, which stamps SourceEnv on the
// fields it overlays. A secret the operator did type into the editor is left
// out: that value is theirs, not the environment's, and persisting it is the
// documented behaviour (the env var still wins at runtime).
func envSourcedSecretPaths(section configstore.Section, sources map[string]configstore.FieldSource, submitted json.RawMessage) []string {
	if len(sources) == 0 {
		return nil
	}
	var payload map[string]any
	// A payload that is not a JSON object carries no operator value for any
	// secret, so every env-resolved one counts as untouched.
	_ = json.Unmarshal(submitted, &payload)
	prefix := string(section) + "."
	var out []string
	for full, goType := range secretPathTypes() {
		if sources[full] != configstore.SourceEnv {
			continue
		}
		rel, ok := strings.CutPrefix(full, prefix)
		if !ok || owningSection(full) != section {
			continue
		}
		if payload != nil && !secretPayloadIsPlaceholder(payload, rel, goType) {
			continue
		}
		out = append(out, rel)
	}
	return out
}

// restoreSecretsFromRow rewrites the leaves of persist named by rels back to
// the values the currently stored section row carries, dropping the key
// entirely when the row has none. Used to undo an env-resolved secret that
// the effective-config merge folded into the blob about to be persisted;
// keeping the row's own value means enabling an env var never silently
// deletes a password the operator saved through the editor earlier.
func restoreSecretsFromRow(persist, stored json.RawMessage, rels []string) json.RawMessage {
	var row map[string]any
	if err := json.Unmarshal(persist, &row); err != nil || row == nil {
		return persist
	}
	var storedMap map[string]any
	_ = json.Unmarshal(stored, &storedMap) // absent row → every leaf is dropped
	for _, rel := range rels {
		if v, ok := lookupDeepAny(storedMap, rel); ok {
			setDeepAny(row, rel, v)
			continue
		}
		deleteDeepAny(row, rel)
	}
	out, err := json.Marshal(row)
	if err != nil {
		return persist
	}
	return out
}

// deleteDeepAny removes the leaf at path from m, leaving the parent objects in
// place. A missing intermediate object is a no-op.
func deleteDeepAny(m map[string]any, path string) {
	parts := strings.Split(path, ".")
	cur := m
	for _, part := range parts[:len(parts)-1] {
		next, ok := cur[part].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
	delete(cur, parts[len(parts)-1])
}

// secretPayloadIsPlaceholder reports whether the value the payload carries at
// rel means "unchanged" rather than an operator edit. See
// [restoreMaskedSecrets] for the contract this implements.
func secretPayloadIsPlaceholder(payload map[string]any, rel, goType string) bool {
	v, present := lookupDeepAny(payload, rel)
	if !present || v == nil {
		return true
	}
	s, isStr := v.(string)
	if !isStr {
		return false // a real object/number the operator supplied
	}
	if s == maskSentinel {
		return true
	}
	// An empty string clears a string secret but is only ever the editor's
	// "no value" placeholder for a complex one.
	return s == "" && !isStringSecret(goType)
}

// lookupDeepAny walks a dotted path through nested JSON objects and reports
// whether the leaf key exists at all. The distinction matters for secrets: an
// absent key and an explicit JSON null both mean "unchanged", but a present
// empty string can mean "cleared" — [getDeepAny] alone cannot tell an absent
// key from a null value.
func lookupDeepAny(m map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	cur := m
	for _, part := range parts[:len(parts)-1] {
		next, ok := cur[part].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	v, ok := cur[parts[len(parts)-1]]
	return v, ok
}

// getDeepAny walks a dotted path through nested JSON objects, returning nil
// when any segment is missing or not an object.
func getDeepAny(m map[string]any, path string) any {
	var cur any = m
	for _, part := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[part]
	}
	return cur
}

// setDeepAny sets a dotted path in nested JSON objects, creating intermediate
// objects as needed.
func setDeepAny(m map[string]any, path string, val any) {
	parts := strings.Split(path, ".")
	cur := m
	for _, part := range parts[:len(parts)-1] {
		next, ok := cur[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[part] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = val
}

// GetConfigSection renders one persisted section. Returns 404 when
// the section has never been written so the SPA can show "currently
// defaulted".
func GetConfigSection(svc ConfigAdminService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Config service unavailable", ""))
			return
		}
		section := configstore.Section(chi.URLParam(r, "section"))
		if !validSection(section) {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Unknown section", string(section)))
			return
		}
		row, err := svc.GetSection(r.Context(), section)
		if errors.Is(err, sqlite.ErrSectionNotFound) {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Section not configured", string(section)))
			return
		}
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Section load failed", err)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		// Drop anything the row must not carry before it reaches the editor. A
		// row written before the nested sections owned their own sub-trees may
		// still hold a stale copy of them; serving it would let the SPA display
		// — and echo back — a value the section does not govern.
		stored := configstore.StripForeignSectionFields(section, row.ValueJSON)
		// Mask secrets before they reach the browser — the store opened them on
		// read. The SPA round-trips the sentinel; restoreMaskedSecrets restores
		// it on save.
		_, _ = w.Write(maskSectionSecrets(section, stored))
	}
}

// SectionApplier hands a freshly persisted section to the subsystem it
// configures, so a save takes effect without a restart. It reports
// whether the running daemon actually took the change.
//
// It exists because "persisted" and "in effect" are two different facts
// and the config surface used to report only the first. `north.mqtt`
// carries no restart-required field, so the schema and the PUT response
// both told an operator the change was live — while the running bridge
// kept the topic base and plane toggles it was constructed with, and the
// only path that rebuilds it (the file watcher's hot reload) never fires
// for a section the SPA writes straight into the database.
//
// A section no subsystem can take live is not an error: ApplySection
// returns (false, nil) and the caller is told the value waits for a
// restart.
//
// loom:reachable:reason="the declared type of rest.Deps.SectionApplier, filled at cmd/openccu-loom/daemon_rest_mount.go with *sectionApplier; an interface reached only by assignment"
type SectionApplier interface {
	ApplySection(ctx context.Context, section configstore.Section) (applied bool, err error)
}

// applySaved runs the live-apply step for a section that was just
// persisted, and renders the two fields the response reports it with.
//
// A failure here never fails the save. The value is stored and will
// apply at the next restart either way; what changes is whether the
// operator is told that is what happened.
func applySaved(ctx context.Context, applier SectionApplier, section configstore.Section) (applied bool, applyErr string) {
	if applier == nil {
		return false, ""
	}
	ok, err := applier.ApplySection(ctx, section)
	if err != nil {
		return false, err.Error()
	}
	return ok, ""
}

// PutConfigSection validates and persists a section, then applies it to
// the running daemon when a subsystem can take it live.
func PutConfigSection(svc ConfigAdminService, rec audit.Recorder, applier SectionApplier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Config service unavailable", ""))
			return
		}
		section := configstore.Section(chi.URLParam(r, "section"))
		if !validSection(section) {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Unknown section", string(section)))
			return
		}
		var raw json.RawMessage
		if err := DecodeJSON(r, &raw); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeValidation, r, "Invalid JSON", err.Error()))
			return
		}
		// Drop fields the section must never carry: bootstrap-tier
		// north.rest.listen, the SQLite-managed auth credentials
		// (north.rest.auth.users / .tokens) and every nested section's
		// sub-tree. This is what makes a REST PUT that omits auth unable to
		// wipe an operator's logins — the credentials live only in the
		// user/token stores now — and keeps north.rest from shadowing the
		// north.rest.auth.* rows.
		raw = configstore.StripForeignSectionFields(section, raw)
		// The effective config is a precondition for the save, not an
		// optimisation: without it the masked-secret sentinels stay in the
		// payload, semantic validation is skipped and the restart-required
		// answer is a guess. Persisting anyway was the worst of the three
		// outcomes — a "***" written over a real credential, reported as 200.
		// Refusing keeps the stored section as it was.
		cur, cerr := svc.Effective(r.Context())
		if cur == nil || cur.Config == nil {
			cerr = cmp.Or(cerr, errEffectiveConfigEmpty)
		}
		if cerr != nil {
			writeServerError(w, r, http.StatusServiceUnavailable, problem.TypeServiceUnready,
				"Effective config unavailable — section not saved", cerr)
			return
		}
		// Turn any masked-secret sentinel the SPA echoed back into the real
		// stored value before validation/persistence, so a round-tripped "***"
		// neither fails type-validation nor overwrites the secret.
		current := cur.Config
		submitted := raw
		raw = restoreMaskedSecrets(current, section, raw)
		if err := validateSection(section, raw); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Section validation failed", err.Error()))
			return
		}
		// Semantic validation: a section can be a well-typed instance of its
		// struct yet still be semantically invalid (empty broker_url with mqtt
		// enabled, callback port out of range, ftp:// public_url). Assemble the
		// candidate effective config with the new section overlaid and run
		// config.Validate so such a section is rejected with 400 instead of
		// being persisted and only warned about at the next boot. The same
		// candidate answers the restart-required question per changed field.
		persist := []byte(raw)
		base := cloneConfig(current)
		base.ApplyDefaults()
		candidate := cloneConfig(current)
		if err := configstore.ApplySectionToConfig(section, raw, candidate); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Section validation failed", err.Error()))
			return
		}
		candidate.ApplyDefaults()
		if err := candidate.Validate(); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Section validation failed", err.Error()))
			return
		}
		restartRequired := len(config.RestartRequiredDiff(base, candidate)) > 0
		// Persist what was validated. PutSection replaces the row, so
		// storing the request fragment would silently drop every field the
		// client did not resend — a PUT of {"enabled":true} on north.mqtt
		// validated against the merged candidate (which still had
		// broker_url) but left a row that no longer described a usable
		// broker. Marshalling the candidate's section sub-tree keeps the
		// row a complete description of the section.
		if merged, ok, mErr := configstore.MarshalSection(section, candidate); mErr == nil && ok {
			persist = merged
		}
		// A secret the operator supplied only through the environment must not
		// become a database row. Effective overlays OPENCCU_LOOM_* secrets onto
		// the assembled config as its last step, so both restoreMaskedSecrets
		// and the section marshal above carry that value into the blob — a save
		// of any unrelated field in the same section would turn a deliberately
		// ephemeral credential into durable state that travels in backups and
		// leaves in cleartext through `config export`.
		if rels := envSourcedSecretPaths(section, cur.Sources, submitted); len(rels) > 0 {
			row, gErr := svc.GetSection(r.Context(), section)
			if gErr != nil && !errors.Is(gErr, sqlite.ErrSectionNotFound) {
				writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal,
					"Section save failed", gErr)
				return
			}
			persist = restoreSecretsFromRow(persist, row.ValueJSON, rels)
		}
		updatedBy := identityFromCtx(r.Context())
		row, err := svc.PutSection(r.Context(), section, persist, updatedBy)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Section save failed", err)
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				Timestamp: row.UpdatedAt,
				User:      updatedBy,
				Action:    audit.ActionConfigSectionUpdate,
				Note:      "section=" + string(section) + " version=" + itoa(row.Version),
			})
		}
		applied, applyErr := applySaved(r.Context(), applier, section)
		out := map[string]any{
			"section":          string(section),
			"version":          row.Version,
			"updated_at":       row.UpdatedAt,
			"restart_required": restartRequired,
			"applied":          applied,
		}
		if applyErr != "" {
			out["apply_error"] = applyErr
		}
		JSON(w, http.StatusOK, out)
	}
}

// errEffectiveConfigEmpty stands in when the config service answers
// without an error but with no config at all. A save cannot proceed on
// either shape, and writeServerError needs something to log.
var errEffectiveConfigEmpty = errors.New("config: effective config is empty")

// cloneConfig returns an independent deep copy of c so the REST handler can
// assemble a candidate effective config (current with a section overlaid)
// without mutating the caller's config.
func cloneConfig(c *config.Config) *config.Config { return config.Clone(c) }

// DeleteConfigSection reverts to defaults.
func DeleteConfigSection(svc ConfigAdminService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Config service unavailable", ""))
			return
		}
		section := configstore.Section(chi.URLParam(r, "section"))
		if !validSection(section) {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Unknown section", string(section)))
			return
		}
		err := svc.DeleteSection(r.Context(), section)
		if errors.Is(err, sqlite.ErrSectionNotFound) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Section delete failed", err)
			return
		}
		if rec != nil {
			actor := identityFromCtx(r.Context())
			rec.Record(audit.Entry{
				User:   actor,
				Action: audit.ActionConfigSectionDelete,
				Note:   "section=" + string(section),
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func validSection(s configstore.Section) bool {
	return slices.Contains(configstore.AllSections(), s)
}

// validateSection runs structural validation by unmarshalling into
// the corresponding typed struct. JSON-decode errors surface as 400.
// Field-level invariants (e.g. MQTT.broker_url required when
// enabled) are deferred to [config.Validate] at apply time; for
// individual section PUTs we only enforce that the JSON is a valid
// instance of the section type.
func validateSection(section configstore.Section, raw json.RawMessage) error {
	switch section {
	case configstore.SectionMQTT:
		var v config.NorthMQTT
		return strictUnmarshal(raw, &v)
	case configstore.SectionMatter:
		var v config.NorthMatter
		return strictUnmarshal(raw, &v)
	case configstore.SectionMCP:
		var v config.NorthMCP
		return strictUnmarshal(raw, &v)
	case configstore.SectionDiscovery:
		var v config.NorthDiscovery
		return strictUnmarshal(raw, &v)
	case configstore.SectionWebhook:
		var v config.NorthWebhook
		return strictUnmarshal(raw, &v)
	case configstore.SectionREST:
		var v config.NorthREST
		return strictUnmarshal(raw, &v)
	case configstore.SectionOIDC:
		var v config.OIDCConfig
		return strictUnmarshal(raw, &v)
	case configstore.SectionCCUAuth:
		var v config.CCUAuthConfig
		return strictUnmarshal(raw, &v)
	case configstore.SectionHAIngress:
		var v config.HAIngressConfig
		return strictUnmarshal(raw, &v)
	case configstore.SectionUI:
		var v config.NorthUI
		return strictUnmarshal(raw, &v)
	case configstore.SectionCallback:
		var v config.CallbackConfig
		return strictUnmarshal(raw, &v)
	case configstore.SectionCCUData:
		var v config.CCUDataConfig
		return strictUnmarshal(raw, &v)
	case configstore.SectionReliability:
		var v config.ReliabilityConfig
		return strictUnmarshal(raw, &v)
	case configstore.SectionPersistence:
		var v config.PersistenceConfig
		return strictUnmarshal(raw, &v)
	case configstore.SectionAlarm:
		var v config.AlarmConfig
		return strictUnmarshal(raw, &v)
	case configstore.SectionLocale:
		var v configstore.LocaleConfig
		return strictUnmarshal(raw, &v)
	case configstore.SectionSecurity:
		var v configstore.SecurityConfig
		return strictUnmarshal(raw, &v)
	}
	return errors.New("unknown section")
}

func strictUnmarshal(raw json.RawMessage, target any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	return dec.Decode(target)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// identityFromCtx extracts the resolved [auth.Identity] subject
// from the request context, falling back to "anonymous" so audit
// rows always carry a non-empty actor.
func identityFromCtx(ctx context.Context) string {
	id, ok := auth.IdentityFrom(ctx)
	if !ok || id.Subject == "" {
		return "anonymous"
	}
	return id.Subject
}
