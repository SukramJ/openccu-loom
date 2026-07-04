// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

// End-to-end test for the admin lifecycle over the REST surface: walks
// the first-run onboarding endpoints the SPA wizard drives (ADR 0045 —
// `GET /api/v1/setup/status`, `POST /api/v1/setup`), logs in with the
// onboarding-created admin, and exercises the /users, /auth/tokens/v2,
// /centrals and /config/sections endpoints against the same SQLite
// stores the daemon would use in production. Validates that the
// chained user store resolves onboarding-created admins and that the
// admin handlers persist + read back via the SQLite-backed services.
//
// Does NOT exercise the central/CCU stack — the onboarding payload
// omits the CCU + MQTT steps so the test runs in-process with zero
// external dependencies (no godevccu, no Mosquitto).

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// adminE2EHarness wires up just enough of the production stack
// to walk the admin lifecycle end-to-end.
type adminE2EHarness struct {
	t        *testing.T
	api      *httptest.Server // REST listener
	users    *sqlitestore.UserStore
	tokens   *sqlitestore.TokenStore
	centrals *sqlitestore.CentralsStore
	sections *sqlitestore.ConfigSectionStore
	audit    *audit.Buffer
}

// newAdminE2EHarness opens a fresh SQLite DB in a t.TempDir() and
// builds the minimal Deps the REST router needs.
func newAdminE2EHarness(t *testing.T) *adminE2EHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dbPath := filepath.Join(t.TempDir(), "openccu-loom.db")
	dsn := "file:" + dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(2000)"
	db, err := sqlitestore.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("sqlitestore.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	users := sqlitestore.NewUserStore(db)
	tokens := sqlitestore.NewTokenStore(db)
	centrals := sqlitestore.NewCentralsStore(db)
	sections := sqlitestore.NewConfigSectionStore(db)
	auditBuf := audit.NewBuffer(500)

	bootstrap := &config.BootstrapConfig{
		DataDir: t.TempDir(),
		Logging: config.LoggingConfig{Level: "info", Format: "json"},
		Listen:  config.BootstrapListen{REST: ":0"},
	}
	store := configstore.New(bootstrap, sections, centrals)

	// authMw layered: SQLite-backed primary + Memory fallback
	// (empty in this test). Mirrors the daemon-wiring contract.
	memUsers := auth.NewMemoryUserStore()
	memTokens := auth.NewMemoryTokenStore(nil)
	authMw := auth.NewMiddleware(
		auth.ChainedUserStore{Primary: users, Secondary: memUsers},
		auth.ChainedTokenStore{Primary: tokens, Secondary: memTokens},
	)
	sessions := auth.NewSessionStore()
	sessionResolve := auth.SessionMiddleware(sessions)
	restResolve := func(next http.Handler) http.Handler {
		return authMw.Resolve(sessionResolve(next))
	}

	tr := health.NewTracker()
	tr.Record("central", health.Sample{Healthy: true, Note: "ok"})

	restRouter := rest.NewRouter(rest.Deps{
		Logger:    slog.New(slog.DiscardHandler),
		StartedAt: time.Now(),
		Health:    tr,
		Audit:     auditBuf,
		Auth: &handlers.AuthDeps{
			Users:         memUsers, // legacy /auth/users read path
			Sessions:      sessions,
			Tokens:        memTokens,
			Secure:        false,
			AuditRecorder: auditBuf,
			LoginUsers: auth.ChainedUserStore{
				Primary:   users,
				Secondary: memUsers,
			},
		},
		Setup: &handlers.SetupService{
			Users:    users,
			Centrals: centrals,
			Sections: sections,
			// Mirrors the daemon's first-run probe: onboarding is
			// required only while no local admin exists. YAML users,
			// CCU-delegated login, and OIDC are not configured in
			// this harness, so the SQLite count is the whole truth.
			Required: func(ctx context.Context) bool {
				n, err := users.Count(ctx)
				return err == nil && n == 0
			},
		},
		AuthResolve:     restResolve,
		AuthRequire:     authMw.Require,
		RequireAdmin:    func(next http.Handler) http.Handler { return authMw.RequireRole(auth.RoleAdmin, next) },
		RequireOperator: func(next http.Handler) http.Handler { return authMw.RequireRole(auth.RoleOperator, next) },
		AuditRecorder:   auditBuf,
		ConfigAdmin:     adminE2EConfigSvc{store: store, sections: sections},
		UserAdmin:       users,
		TokenAdmin:      tokens,
		CentralAdmin:    centrals,
		// The OpenAPI validator middleware stays unwired so the test
		// focuses on the behavioural roundtrip; spec coverage is
		// pinned by the contract suite under tests/contract/.
	})

	api := httptest.NewServer(restRouter)
	t.Cleanup(api.Close)

	return &adminE2EHarness{
		t:        t,
		api:      api,
		users:    users,
		tokens:   tokens,
		centrals: centrals,
		sections: sections,
		audit:    auditBuf,
	}
}

// adminE2EConfigSvc is the tiny adapter the daemon also uses in
// production (cmd/openccu-loom/config_admin_wiring.go); we copy
// it here so the integration test does not import from the
// command package.
type adminE2EConfigSvc struct {
	store    *configstore.Store
	sections *sqlitestore.ConfigSectionStore
}

func (a adminE2EConfigSvc) Effective(ctx context.Context) (*configstore.EffectiveResult, error) {
	return a.store.Effective(ctx)
}

func (a adminE2EConfigSvc) GetSection(ctx context.Context, section configstore.Section) (sqlitestore.SectionRow, error) {
	return a.sections.Get(ctx, string(section))
}

func (a adminE2EConfigSvc) PutSection(ctx context.Context, section configstore.Section, valueJSON []byte, updatedBy string) (sqlitestore.SectionRow, error) {
	return a.sections.Put(ctx, string(section), valueJSON, updatedBy)
}

func (a adminE2EConfigSvc) DeleteSection(ctx context.Context, section configstore.Section) error {
	return a.sections.Delete(ctx, string(section))
}

// newCookieClient builds an http.Client with a cookie jar so the
// session cookie from /api/v1/auth/login sticks to subsequent calls.
func newCookieClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
	}
}

// apiJSON is the helper used for all JSON REST calls.
func (h *adminE2EHarness) apiJSON(client *http.Client, method, path string, body any, headers map[string]string) (*http.Response, []byte) {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, _ := http.NewRequest(method, h.api.URL+path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	buf, _ := io.ReadAll(res.Body)
	return res, buf
}

// setupStatus fetches GET /api/v1/setup/status and returns the
// `required` flag.
func (h *adminE2EHarness) setupStatus(client *http.Client) bool {
	h.t.Helper()
	res, buf := h.apiJSON(client, http.MethodGet, "/api/v1/setup/status", nil, nil)
	if res.StatusCode != 200 {
		h.t.Fatalf("setup/status status=%d body=%s", res.StatusCode, buf)
	}
	var status struct {
		Required bool `json:"required"`
	}
	if err := json.Unmarshal(buf, &status); err != nil {
		h.t.Fatalf("setup/status unmarshal: %v body=%s", err, buf)
	}
	return status.Required
}

// TestAdminE2E walks the complete admin lifecycle:
//
//  1. GET /api/v1/setup/status → onboarding required
//  2. POST /api/v1/setup (admin + locale, CCU/MQTT skipped) → 204
//  3. GET /api/v1/setup/status → no longer required
//  4. POST /api/v1/setup again → 409 (single-shot gate)
//  5. Verify SQLite has one admin user; locale section is set
//  6. Login via /api/v1/auth/login → session cookie
//  7. GET /api/v1/config/schema → contains north.mqtt.broker_url
//  8. GET /api/v1/config/effective → returns config + sources
//  9. POST /api/v1/users → second user created
//  10. POST /api/v1/auth/tokens/v2 → plaintext + fingerprint
//  11. Bearer-auth with that token reaches /api/v1/info
//  12. PUT /api/v1/config/sections/north.mqtt → 200, version=1
//  13. GET /api/v1/config/sections/north.mqtt → matches PUT body
//  14. PUT same section again → version=2
//  15. POST /api/v1/centrals → 201
//  16. GET /api/v1/centrals → contains created row
//  17. GET /api/v1/config/effective → now reports the central
//  18. DELETE /api/v1/centrals/{name} → 204
//  19. DELETE /api/v1/users/{subject} on the second user → 204
//  20. DELETE the last admin → 409 (last-admin protection)
//  21. Audit buffer carries entries for the section / user /
//     central mutations.
func TestAdminE2E(t *testing.T) {
	h := newAdminE2EHarness(t)
	client := newCookieClient(t)
	ctx := context.Background()

	// --- First-run probe: onboarding required ---
	if !h.setupStatus(client) {
		t.Fatal("setup/status reported required=false on a fresh DB, want true")
	}

	// --- Finalize onboarding (CCU + MQTT steps skipped) ---
	setupBody := map[string]any{
		"admin":  map[string]string{"username": "alice", "password": "correcthorse"},
		"locale": map[string]string{"locale": "de", "theme": "system"},
	}
	res, buf := h.apiJSON(client, http.MethodPost, "/api/v1/setup", setupBody, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("setup finalize status=%d body=%s", res.StatusCode, buf)
	}

	// --- Probe flips to not-required ---
	if h.setupStatus(client) {
		t.Fatal("setup/status still reports required=true after finalize")
	}

	// --- Single-shot gate: a second finalize must be refused ---
	res, buf = h.apiJSON(client, http.MethodPost, "/api/v1/setup", setupBody, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("second setup finalize status=%d body=%s (want 409)", res.StatusCode, buf)
	}

	// --- Verify SQLite persistence ---
	if n, err := h.users.Count(ctx); err != nil || n != 1 {
		t.Fatalf("post-onboarding users.Count = %d (err=%v), want 1", n, err)
	}
	locale, err := h.sections.Get(ctx, "locale")
	if err != nil {
		t.Fatalf("locale section missing: %v", err)
	}
	if !strings.Contains(string(locale.ValueJSON), "de") {
		t.Fatalf("locale section payload = %s, want contains \"de\"", locale.ValueJSON)
	}

	// --- API login ---
	apiClient := newCookieClient(t)
	res, buf = h.apiJSON(apiClient, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "alice", "password": "correcthorse"}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("login status=%d body=%s", res.StatusCode, buf)
	}

	// --- GET /config/schema ---
	res, buf = h.apiJSON(apiClient, http.MethodGet, "/api/v1/config/schema", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("config/schema status=%d body=%s", res.StatusCode, buf)
	}
	var schema handlers.SchemaResponse
	if err := json.Unmarshal(buf, &schema); err != nil {
		t.Fatalf("schema unmarshal: %v body=%s", err, buf)
	}
	foundBrokerURL := false
	for _, f := range schema.Fields {
		if f.Path == "north.mqtt.broker_url" {
			foundBrokerURL = true
			break
		}
	}
	if !foundBrokerURL {
		t.Fatalf("schema missing north.mqtt.broker_url field")
	}

	// --- GET /config/effective ---
	res, buf = h.apiJSON(apiClient, http.MethodGet, "/api/v1/config/effective", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("config/effective status=%d body=%s", res.StatusCode, buf)
	}
	var snap handlers.ConfigSnapshotResponse
	if err := json.Unmarshal(buf, &snap); err != nil {
		t.Fatalf("effective unmarshal: %v body=%s", err, buf)
	}
	if _, ok := snap.Sources["data_dir"]; !ok {
		t.Fatalf("expected data_dir source attribution, got sources=%v", snap.Sources)
	}

	// --- Create a second user ---
	res, buf = h.apiJSON(apiClient, http.MethodPost, "/api/v1/users", map[string]string{
		"username": "bob",
		"password": "anotherpw1",
		"role":     "operator",
	}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("users create status=%d body=%s", res.StatusCode, buf)
	}
	if n, _ := h.users.Count(ctx); n != 2 {
		t.Fatalf("users count after create = %d, want 2", n)
	}

	// --- Create a bearer token ---
	res, buf = h.apiJSON(apiClient, http.MethodPost, "/api/v1/auth/tokens/v2", map[string]string{
		"subject": "ci-job",
		"role":    "operator",
	}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("token create status=%d body=%s", res.StatusCode, buf)
	}
	var tokRes struct {
		Token       string `json:"token"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(buf, &tokRes); err != nil {
		t.Fatalf("token unmarshal: %v body=%s", err, buf)
	}
	if tokRes.Token == "" || tokRes.Fingerprint == "" {
		t.Fatalf("token response missing fields: %+v", tokRes)
	}

	// --- Use the bearer token on a different client (no cookies) ---
	bareClient := &http.Client{Timeout: 5 * time.Second}
	res, buf = h.apiJSON(bareClient, http.MethodGet, "/api/v1/info", nil,
		map[string]string{"Authorization": "Bearer " + tokRes.Token})
	if res.StatusCode != 200 {
		t.Fatalf("bearer GET /info status=%d body=%s", res.StatusCode, buf)
	}

	// --- PUT a config section ---
	mqttSection := map[string]any{
		"enabled":     true,
		"broker_url":  "tcp://broker.example:1883",
		"topic_base":  "openccu-loom",
		"client_id":   "openccu-loom-test",
		"raw_enabled": true,
	}
	res, buf = h.apiJSON(apiClient, http.MethodPut, "/api/v1/config/sections/north.mqtt", mqttSection, nil)
	if res.StatusCode != 200 {
		t.Fatalf("section put status=%d body=%s", res.StatusCode, buf)
	}
	var putRes struct {
		Section string `json:"section"`
		Version int    `json:"version"`
	}
	_ = json.Unmarshal(buf, &putRes)
	if putRes.Section != "north.mqtt" || putRes.Version != 1 {
		t.Fatalf("section put response = %+v", putRes)
	}

	// --- GET it back ---
	res, buf = h.apiJSON(apiClient, http.MethodGet, "/api/v1/config/sections/north.mqtt", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("section get status=%d body=%s", res.StatusCode, buf)
	}
	if !strings.Contains(string(buf), "broker.example:1883") {
		t.Fatalf("section roundtrip lost broker_url: %s", buf)
	}

	// --- PUT again, version bumps ---
	mqttSection["topic_base"] = "openccu-loom-2"
	res, buf = h.apiJSON(apiClient, http.MethodPut, "/api/v1/config/sections/north.mqtt", mqttSection, nil)
	if res.StatusCode != 200 {
		t.Fatalf("section put2 status=%d body=%s", res.StatusCode, buf)
	}
	_ = json.Unmarshal(buf, &putRes)
	if putRes.Version != 2 {
		t.Fatalf("section version after second PUT = %d, want 2", putRes.Version)
	}

	// --- Create a CCU ---
	central := map[string]any{
		"name":       "home",
		"host":       "192.168.1.10",
		"interfaces": []map[string]any{{"name": "HmIP-RF"}},
		"enabled":    true,
	}
	res, buf = h.apiJSON(apiClient, http.MethodPost, "/api/v1/centrals", central, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("central create status=%d body=%s", res.StatusCode, buf)
	}

	res, buf = h.apiJSON(apiClient, http.MethodGet, "/api/v1/centrals", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("centrals list status=%d body=%s", res.StatusCode, buf)
	}
	if !strings.Contains(string(buf), "192.168.1.10") {
		t.Fatalf("centrals list missing new entry: %s", buf)
	}

	// --- Effective config now shows the central ---
	res, buf = h.apiJSON(apiClient, http.MethodGet, "/api/v1/config/effective", nil, nil)
	if !strings.Contains(string(buf), "192.168.1.10") {
		t.Fatalf("effective config missing central: %s", buf)
	}

	// --- Delete the CCU ---
	res, buf = h.apiJSON(apiClient, http.MethodDelete, "/api/v1/centrals/home", nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("central delete status=%d body=%s", res.StatusCode, buf)
	}

	// --- Delete the second user ---
	res, buf = h.apiJSON(apiClient, http.MethodDelete, "/api/v1/users/bob", nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete user status=%d body=%s", res.StatusCode, buf)
	}

	// --- Last-admin protection ---
	res, buf = h.apiJSON(apiClient, http.MethodDelete, "/api/v1/users/alice", nil, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("delete last admin status=%d body=%s (want 409)", res.StatusCode, buf)
	}

	// --- Audit buffer recorded our mutations ---
	entries := h.audit.List(50)
	wantActions := map[audit.Action]int{
		audit.ActionConfigSectionUpdate: 0,
		audit.ActionUserCreate:          0,
		audit.ActionTokenCreate:         0,
		audit.ActionCentralCreate:       0,
		audit.ActionCentralDelete:       0,
		audit.ActionUserDelete:          0,
	}
	for _, e := range entries {
		if _, ok := wantActions[e.Action]; ok {
			wantActions[e.Action]++
		}
	}
	for act, n := range wantActions {
		if n == 0 {
			t.Errorf("expected audit entry for %s, got 0", act)
		}
	}
}
