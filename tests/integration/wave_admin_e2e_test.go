// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

// End-to-end test for the Wave-A..G admin path: walks the
// multi-step setup wizard via HTTP, logs in with the wizard-
// created admin, exercises the new /users / /auth/tokens/v2 /
// /centrals / /config/sections endpoints against the same SQLite
// stores the daemon would use in production. Validates that the
// chained user store (Wave-wiring) resolves wizard-created
// admins and that the new admin handlers persist+read back via
// the SQLite-backed services.
//
// Does NOT exercise the central/CCU stack — the wizard skips the
// CCU + MQTT steps so the test runs in-process with zero
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
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/ui"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// adminE2EHarness wires up just enough of the production stack
// to walk the admin lifecycle end-to-end.
type adminE2EHarness struct {
	t        *testing.T
	api      *httptest.Server // REST listener
	wizard   *httptest.Server // UI listener (setup + login form)
	users    *sqlitestore.UserStore
	tokens   *sqlitestore.TokenStore
	centrals *sqlitestore.CentralsStore
	sections *sqlitestore.ConfigSectionStore
	audit    *audit.Buffer
}

// newAdminE2EHarness opens a fresh SQLite DB in a t.TempDir() and
// builds the minimal Deps both routers need.
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
		Listen:  config.BootstrapListen{REST: ":0", UI: ":0"},
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
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
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
		AuthResolve:     restResolve,
		AuthRequire:     authMw.Require,
		RequireAdmin:    func(next http.Handler) http.Handler { return authMw.RequireRole(auth.RoleAdmin, next) },
		RequireOperator: func(next http.Handler) http.Handler { return authMw.RequireRole(auth.RoleOperator, next) },
		AuditRecorder:   auditBuf,
		ConfigAdmin:     adminE2EConfigSvc{store: store, sections: sections},
		UserAdmin:       users,
		TokenAdmin:      tokens,
		CentralAdmin:    centrals,
		// Disable the OpenAPI validator middleware so the test
		// focuses on behavioural roundtrip; spec coverage is
		// pinned by tests/contract/openapi_wave_c_paths_test.go.
	})

	catalogs, _ := i18n.NewCatalogs()
	uiRouter := ui.NewRouter(ui.Deps{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Lang:     "en",
		Health:   tr,
		Catalogs: catalogs,
		Auth:     &ui.AuthDeps{Users: memUsers, Sessions: sessions, Secure: false},
		Setup: &ui.SetupWizardDeps{
			Users:    users,
			Centrals: centrals,
			Sections: sections,
			Sessions: ui.NewSetupSessionStore(),
		},
		AuthResolve: sessionResolve,
		AuthRequire: nil,
	})

	api := httptest.NewServer(restRouter)
	t.Cleanup(api.Close)
	wizard := httptest.NewServer(uiRouter)
	t.Cleanup(wizard.Close)

	return &adminE2EHarness{
		t:        t,
		api:      api,
		wizard:   wizard,
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

// newCookieClient builds an http.Client that follows redirects
// but stops at "303 See Other" boundaries so the wizard's
// per-step redirects don't auto-collapse to /login.
func newCookieClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// postForm posts form values; helper around the wizard's
// HTML form-style POSTs.
func (h *adminE2EHarness) postForm(client *http.Client, path string, form url.Values) *http.Response {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, h.wizard.URL+path,
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("POST %s: %v", path, err)
	}
	return res
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

// findCSRFToken pulls the CSRF token out of an HTML form. The
// wizard templates render <input type="hidden" name="_csrf"
// value="...">.
func findCSRFToken(t *testing.T, html string) string {
	t.Helper()
	const marker = `name="_csrf" value="`
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatalf("CSRF token marker not found in HTML")
	}
	rest := html[i+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		t.Fatalf("CSRF token value not closed")
	}
	return rest[:end]
}

// TestAdminE2E walks the complete admin lifecycle:
//
//  1. GET /setup → wizard renders step 1
//  2. POST /setup/admin → advances to step 2
//  3. POST /setup/locale → step 3
//  4. POST /setup/ccu with skip=1 → step 4
//  5. POST /setup/mqtt with skip=1 → finalize + redirect to /login
//  6. Verify SQLite has one admin user; locale section is set
//  7. Login via /api/v1/auth/login → session cookie
//  8. GET /api/v1/config/schema → contains data_dir + north.mqtt.broker_url
//  9. GET /api/v1/config/effective → returns config + sources
//  10. POST /api/v1/users → second user created
//  11. POST /api/v1/auth/tokens/v2 → plaintext + fingerprint
//  12. Bearer-auth with that token reaches /api/v1/info
//  13. PUT /api/v1/config/sections/north.mqtt → 200, version=1
//  14. GET /api/v1/config/sections/north.mqtt → matches PUT body
//  15. PUT same section again → version=2
//  16. POST /api/v1/centrals → 201
//  17. GET /api/v1/centrals → contains created row
//  18. GET /api/v1/config/effective → now reports the central
//  19. DELETE /api/v1/centrals/{name} → 204
//  20. DELETE /api/v1/users/{subject} on the second user → 204
//  21. DELETE the last admin → 409 (last-admin protection)
//  22. Audit buffer carries entries for the section / user /
//     central mutations.
func TestAdminE2E(t *testing.T) {
	h := newAdminE2EHarness(t)
	client := newCookieClient(t)
	ctx := context.Background()

	// --- Step 1: GET /setup ---
	res, err := client.Get(h.wizard.URL + "/setup")
	if err != nil {
		t.Fatalf("GET /setup: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("step1 status=%d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	csrf := findCSRFToken(t, string(body))

	// --- Step 1 POST: admin ---
	res = h.postForm(client, "/setup/admin", url.Values{
		"_csrf":    {csrf},
		"username": {"alice"},
		"password": {"correcthorse"},
		"confirm":  {"correcthorse"},
	})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("setup/admin status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	// --- Step 2 GET → locale form, step 2 POST ---
	res, _ = client.Get(h.wizard.URL + "/setup")
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	csrf = findCSRFToken(t, string(body))
	res = h.postForm(client, "/setup/locale", url.Values{
		"_csrf":  {csrf},
		"locale": {"de"},
		"theme":  {"system"},
	})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("setup/locale status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	// --- Step 3 SKIP ---
	res, _ = client.Get(h.wizard.URL + "/setup")
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	csrf = findCSRFToken(t, string(body))
	res = h.postForm(client, "/setup/ccu", url.Values{
		"_csrf": {csrf},
		"skip":  {"1"},
	})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("setup/ccu status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	// --- Step 4 SKIP → finalize ---
	res, _ = client.Get(h.wizard.URL + "/setup")
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	csrf = findCSRFToken(t, string(body))
	res = h.postForm(client, "/setup/mqtt", url.Values{
		"_csrf": {csrf},
		"skip":  {"1"},
	})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("setup/mqtt status=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	// --- Verify SQLite persistence ---
	if n, err := h.users.Count(ctx); err != nil || n != 1 {
		t.Fatalf("post-wizard users.Count = %d (err=%v), want 1", n, err)
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
	res, buf := h.apiJSON(apiClient, http.MethodPost, "/api/v1/auth/login",
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
