// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
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
	Path            string `json:"path"`
	Class           string `json:"class"`
	GoType          string `json:"go_type"`
	RestartRequired bool   `json:"restart_required,omitempty"`
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
	"reliability.command_retry_initial_delay":          int64(250_000_000), // 250 ms
	"reliability.command_throttle_inter_command_delay": int64(0),           // disabled (no inter-command pacing)
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
	"north.rest.auth.oidc.role_claim": "role",
	// CCU metadata archives — paths the embedded extracts fall back
	// to when the FS-side bundle is absent.
	"ccu_data.translations_path": "./var/ccu_data/translation_extract.json.gz",
	"ccu_data.easymode_path":     "./var/ccu_data/easymode_extract.json.gz",
}

// SchemaResponse bundles the field descriptors + the list of known
// sections so the SPA can render tabs.
type SchemaResponse struct {
	Sections []string      `json:"sections"`
	Fields   []SchemaField `json:"fields"`
}

// restartRequiredPaths enumerates fields whose change cannot be
// hot-reloaded. Mirrors §7.1 Q12 of SPECIFICATION.md.
var restartRequiredPaths = map[string]struct{}{
	"data_dir":              {},
	"north.rest.listen":     {},
	"north.rest.public_url": {},
	"north.ui.listen":       {},
	"callback.host":         {},
	"callback.port":         {},
	"callback.bin_port":     {},
	"callback.port_range":   {},
	"north.matter.enabled":  {},
	"north.matter.listen":   {},
	// MCP route is mounted once at boot (cmd/openccu-loom/daemon_rest_mount.go),
	// so toggling any MCP field takes effect only after a restart.
	"north.mcp.enabled":      {},
	"north.mcp.allow_writes": {},
	"north.mcp.path":         {},
	"centrals":               {},
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
		for _, f := range fields {
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
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "Effective config failed", err.Error()))
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
	for _, f := range config.ClassifyFields(&config.Config{}) {
		if f.Class == config.FieldSecret {
			set[f.Path] = struct{}{}
		}
	}
	return set
}

func maskPath(v any, prefix string, set map[string]struct{}) {
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
			m[k] = maskSentinel
			continue
		}
		maskPath(val, path, set)
	}
}

// maskSentinel is the placeholder maskSecrets writes in place of every
// secret-class value so the GET response is safe to log. The SPA echoes it
// back unchanged for any secret the operator did not edit; restoreMaskedSecrets
// turns it back into the real value before validation and persistence.
const maskSentinel = "***"

// restoreMaskedSecrets is the inverse of maskSecrets, scoped to one section.
// A section PUT round-trips the masked sentinel for every secret the operator
// did not change (the SPA never receives the cleartext). Left as-is that
// sentinel would either fail strict type-validation — a map[string]string
// secret like north.rest.auth.users would arrive as the bare string "***" —
// or overwrite the stored secret with "***". This walks every secret path
// under the section and, wherever the payload still carries the sentinel,
// substitutes the operator's current real value (a map, string, number — any
// shape). Secret fields the operator actually changed carry a non-sentinel
// value and are left untouched, so new secrets still persist.
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
	changed := false
	for full := range secretPathSet() {
		rel, ok := strings.CutPrefix(full, prefix)
		if !ok {
			continue
		}
		if getDeepAny(payload, rel) != maskSentinel {
			continue // absent, cleared, or an operator-supplied new secret
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
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "Section load failed", err.Error()))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(row.ValueJSON)
	}
}

// PutConfigSection validates and persists a section.
func PutConfigSection(svc ConfigAdminService, rec audit.Recorder) http.HandlerFunc {
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
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid JSON", err.Error()))
			return
		}
		// Turn any masked-secret sentinel the SPA echoed back into the real
		// stored value before validation/persistence, so a round-tripped "***"
		// neither fails type-validation nor overwrites the secret. Best-effort:
		// if the current config is unavailable the raw payload validates as-is.
		if cur, cerr := svc.Effective(r.Context()); cerr == nil && cur != nil {
			raw = restoreMaskedSecrets(cur.Config, section, raw)
		}
		if err := validateSection(section, raw); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Section validation failed", err.Error()))
			return
		}
		updatedBy := identityFromCtx(r.Context())
		row, err := svc.PutSection(r.Context(), section, raw, updatedBy)
		if err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "Section save failed", err.Error()))
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
		JSON(w, http.StatusOK, map[string]any{
			"section":          string(section),
			"version":          row.Version,
			"updated_at":       row.UpdatedAt,
			"restart_required": sectionRestartRequired(section),
		})
	}
}

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
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "Section delete failed", err.Error()))
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
	case configstore.SectionREST:
		var v config.NorthREST
		return strictUnmarshal(raw, &v)
	case configstore.SectionOIDC:
		var v config.OIDCConfig
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

// sectionRestartRequired returns true for sections whose changes
// cannot be hot-reloaded. Every other section is hot-applied; the
// default-false return is intentional so adding a new
// [configstore.Section] does not silently mark it restart-required.
//
//nolint:exhaustive // hot-reload is the default; explicit cases only for restart-required sections.
func sectionRestartRequired(section configstore.Section) bool {
	switch section {
	case configstore.SectionCallback,
		configstore.SectionMatter,
		configstore.SectionMCP:
		return true
	}
	return false
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
