// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	gosql "database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// setupOpenMu serialises sqlite.Open calls to avoid a data race in the
// goose library's package-level embed pointer when tests run in parallel.
var setupOpenMu sync.Mutex

// migratedSchemaTemplate applies the migration set once per test binary and
// returns the path of a closed, fully migrated database file.
//
// A full goose run costs about a second and cannot run concurrently (see
// setupOpenMu), so every test that opened its own database queued that second
// behind the same lock — across this package's handler fixtures that was the
// bulk of its runtime, all of it re-deriving a schema that never varies.
// Deriving it once and copying the file gives each test a private database
// for the price of a file copy.
var migratedSchemaTemplate = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "rest-handlers-schema-template")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "template.db")
	db, err := sqlite.Open(context.Background(), sqlite.FileDSN(path))
	if err != nil {
		return "", err
	}
	// Closing checkpoints the write-ahead log back into the main file, so
	// the single file copied below carries the whole schema.
	if err := db.Close(); err != nil {
		return "", err
	}
	return path, nil
})

// openMigratedTestDB opens a private, already-migrated SQLite database in t's
// temp directory. Tests share the schema, never the data.
func openMigratedTestDB(t *testing.T, name string) *gosql.DB {
	t.Helper()
	templatePath, err := migratedSchemaTemplate()
	if err != nil {
		t.Fatalf("build migrated schema template: %v", err)
	}
	schema, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read schema template: %v", err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, schema, 0o600); err != nil {
		t.Fatalf("seed test db: %v", err)
	}
	// Open still runs goose; it finds the version table already at the
	// latest revision and applies nothing. The lock stays because that
	// check is goose-internal too.
	setupOpenMu.Lock()
	db, err := sqlite.Open(context.Background(), sqlite.FileDSN(path))
	setupOpenMu.Unlock()
	if err != nil {
		t.Fatalf("open test db %s: %v", name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// openSetupStores opens a fresh, fully-migrated SQLite database in t's temp
// directory and returns all three stores the setup handler needs.
func openSetupStores(t *testing.T) (*sqlite.UserStore, *sqlite.CentralsStore, *sqlite.ConfigSectionStore) {
	t.Helper()
	db := openMigratedTestDB(t, "setup.db")
	return sqlite.NewUserStore(db), sqlite.NewCentralsStore(db), sqlite.NewConfigSectionStore(db)
}

// newFullSetupService builds a SetupService backed by a real SQLite database.
// Required always returns true so the finalize gate is open.
func newFullSetupService(t *testing.T) *SetupService {
	t.Helper()
	users, centrals, sections := openSetupStores(t)
	return &SetupService{
		Users:    users,
		Centrals: centrals,
		Sections: sections,
		Required: func(context.Context) bool { return true },
	}
}

// nilBackedSetupService returns a SetupService whose stores are non-nil
// pointers backed by a nil *sql.DB. No DB method is ever called on these
// stores — they satisfy the nil-pointer guards in the handler and are safe
// as long as no actual DB operation is reached (i.e. validation or the
// required-gate fires first).
func nilBackedSetupService(required bool) *SetupService {
	return &SetupService{
		Users:    sqlite.NewUserStore(nil),
		Sections: sqlite.NewConfigSectionStore(nil),
		Required: func(context.Context) bool { return required },
	}
}

// --- SetupStatus ---

func TestSetupStatus_RequiredTrue(t *testing.T) {
	t.Parallel()
	svc := &SetupService{
		Users:    sqlite.NewUserStore(nil),
		Sections: sqlite.NewConfigSectionStore(nil),
		Required: func(context.Context) bool { return true },
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", http.NoBody)
	w := httptest.NewRecorder()
	SetupStatus(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}
	var body setupStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.Required {
		t.Error("expected required=true")
	}
}

func TestSetupStatus_RequiredFalse(t *testing.T) {
	t.Parallel()
	svc := &SetupService{
		Users:    sqlite.NewUserStore(nil),
		Sections: sqlite.NewConfigSectionStore(nil),
		Required: func(context.Context) bool { return false },
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", http.NoBody)
	w := httptest.NewRecorder()
	SetupStatus(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}
	var body setupStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Required {
		t.Error("expected required=false")
	}
}

// TestSetupStatus_AuthenticatedIdentity_NotRequired pins the ADR-0044 fix: a
// request that already carries an authenticated identity (e.g. injected by the
// HA Ingress passthrough) must report required=false even when the first-run
// probe would otherwise say true — otherwise an already-logged-in admin is
// trapped in the onboarding wizard.
func TestSetupStatus_AuthenticatedIdentity_NotRequired(t *testing.T) {
	t.Parallel()
	svc := &SetupService{
		Users:    sqlite.NewUserStore(nil),
		Sections: sqlite.NewConfigSectionStore(nil),
		Required: func(context.Context) bool { return true }, // would normally trap the wizard
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", http.NoBody)
	req = req.WithContext(auth.ContextWithIdentity(req.Context(),
		auth.Identity{Subject: "ha-ingress", Role: auth.RoleAdmin, Scheme: auth.SchemeIngress}))
	w := httptest.NewRecorder()
	SetupStatus(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}
	var body setupStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Required {
		t.Error("expected required=false for an already-authenticated caller")
	}
}

func TestSetupStatus_NilService(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", http.NoBody)
	w := httptest.NewRecorder()
	SetupStatus(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}
	var body setupStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Required {
		t.Error("nil service must report required=false")
	}
}

// --- Setup success paths ---

// TestSetup_AdminAndLocale_204 finalises setup with only admin + locale and
// verifies that the user row is persisted and can authenticate.
// Note: bcrypt at the production cost makes this test intentionally slower
// than a typical unit test (~0.3 s).
func TestSetup_AdminAndLocale_204(t *testing.T) {
	svc := newFullSetupService(t)
	ctx := context.Background()

	body := strings.NewReader(`{
		"admin":  {"username":"admin","password":"password123"},
		"locale": {"locale":"de","theme":"light"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", body)
	w := httptest.NewRecorder()
	Setup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("got %d body=%s, want 204", w.Code, w.Body.String())
	}

	n, err := svc.Users.Count(ctx)
	if err != nil {
		t.Fatalf("Users.Count: %v", err)
	}
	if n != 1 {
		t.Errorf("user count = %d, want 1", n)
	}

	if _, err := svc.Users.AuthenticateBasic(ctx, "admin", "password123"); err != nil {
		t.Errorf("AuthenticateBasic with correct credentials: %v", err)
	}

	if _, err := svc.Sections.Get(ctx, "locale"); err != nil {
		t.Errorf("locale section not persisted: %v", err)
	}
}

// TestSetup_WithCCUAndMQTT_204 finalises setup with all optional fields and
// verifies that the CCU row and the north.mqtt section are also persisted.
func TestSetup_WithCCUAndMQTT_204(t *testing.T) {
	svc := newFullSetupService(t)
	ctx := context.Background()

	body := strings.NewReader(`{
		"admin":  {"username":"admin","password":"password123"},
		"locale": {"locale":"en","theme":"dark"},
		"ccu":    {"name":"ccu1","host":"192.168.1.1","interfaces":["HmIP-RF"]},
		"mqtt":   {"broker_url":"tcp://localhost:1883"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", body)
	w := httptest.NewRecorder()
	Setup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("got %d body=%s, want 204", w.Code, w.Body.String())
	}

	if _, err := svc.Centrals.Get(ctx, "ccu1"); err != nil {
		t.Errorf("centrals.Get(ccu1): %v", err)
	}

	if _, err := svc.Sections.Get(ctx, "north.mqtt"); err != nil {
		t.Errorf("north.mqtt section not persisted: %v", err)
	}
}

// TestSetup_MQTTStepEnablesTheBridge pins that the wizard's MQTT step
// switches the bridge on rather than only recording where the broker is.
//
// The SPA sends the mqtt object only when the operator flipped "Enable
// MQTT", and the persisted section is overlaid sparsely onto the config, so
// a section without `enabled` leaves North.MQTT.Enabled at its false zero
// value: the supervisor never starts the bridge and the step the operator
// completed publishes nothing.
func TestSetup_MQTTStepEnablesTheBridge(t *testing.T) {
	svc := newFullSetupService(t)
	ctx := context.Background()

	body := strings.NewReader(`{
		"admin":  {"username":"admin","password":"password123"},
		"locale": {"locale":"en","theme":"dark"},
		"mqtt":   {"broker_url":"tcp://broker.local:1883","username":"mq","password":"secret"}
	}`)
	w := httptest.NewRecorder()
	Setup(svc).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/setup", body))
	if w.Code != http.StatusNoContent {
		t.Fatalf("got %d body=%s, want 204", w.Code, w.Body.String())
	}

	row, err := svc.Sections.Get(ctx, "north.mqtt")
	if err != nil {
		t.Fatalf("north.mqtt section not persisted: %v", err)
	}
	// Decode into the real config type: the overlay the daemon applies is
	// exactly this unmarshal, so a key the section omits stays at its zero
	// value here too.
	var sec config.NorthMQTT
	if err := json.Unmarshal(row.ValueJSON, &sec); err != nil {
		t.Fatalf("unmarshal north.mqtt: %v", err)
	}
	if !sec.Enabled {
		t.Errorf("north.mqtt.enabled = false; the wizard step that enabled MQTT left the bridge off: %s", row.ValueJSON)
	}
	if sec.BrokerURL != "tcp://broker.local:1883" {
		t.Errorf("broker_url = %q, want tcp://broker.local:1883", sec.BrokerURL)
	}
}

// recordingCentralAdmin is a [CentralAdminService] that records what the
// wizard writes. Production wires the live-adopt decorator here, so the
// recorded Put stands in for "the CCU was adopted, not just persisted".
type recordingCentralAdmin struct {
	*sqlite.CentralsStore
	puts []sqlite.CentralRow
}

func (r *recordingCentralAdmin) Put(ctx context.Context, row sqlite.CentralRow) error {
	r.puts = append(r.puts, row)
	return r.CentralsStore.Put(ctx, row)
}

// TestSetup_CCUGoesThroughCentralAdminService pins that the wizard's optional
// CCU step writes through the injected [CentralAdminService] rather than a
// raw store. Production injects the live-adopt decorator there, which is what
// brings the CCU up immediately — without it a freshly onboarded add-on stays
// dark (and CCU-delegated login keeps failing) until the next restart.
func TestSetup_CCUGoesThroughCentralAdminService(t *testing.T) {
	users, centrals, sections := openSetupStores(t)
	rec := &recordingCentralAdmin{CentralsStore: centrals}
	svc := &SetupService{
		Users:    users,
		Centrals: rec,
		Sections: sections,
		Required: func(context.Context) bool { return true },
	}

	body := strings.NewReader(`{
		"admin":  {"username":"admin","password":"password123"},
		"locale": {"locale":"de","theme":"system"},
		"ccu":    {"name":"ccu1","host":"192.168.1.1","username":"Admin","password":"ccu-secret","interfaces":["HmIP-RF","BidCos-RF"]}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", body)
	w := httptest.NewRecorder()
	Setup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("got %d body=%s, want 204", w.Code, w.Body.String())
	}
	if len(rec.puts) != 1 {
		t.Fatalf("expected exactly one Put through the central-admin service, got %d", len(rec.puts))
	}
	got := rec.puts[0]
	if got.Name != "ccu1" || got.Host != "192.168.1.1" || !got.Enabled {
		t.Errorf("row = %+v, want name=ccu1 host=192.168.1.1 enabled=true", got)
	}
	if got.PasswordPlain != "ccu-secret" || got.Username != "Admin" {
		t.Errorf("credentials not forwarded: user=%q password set=%v", got.Username, got.PasswordPlain != "")
	}
	if len(got.Interfaces) != 2 {
		t.Errorf("interfaces = %+v, want 2 entries", got.Interfaces)
	}
}

// --- Setup failure paths ---

func TestSetup_AlreadyCompleted_409(t *testing.T) {
	t.Parallel()
	svc := nilBackedSetupService(false)
	body := strings.NewReader(`{
		"admin":  {"username":"admin","password":"password123"},
		"locale": {"locale":"de","theme":"light"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", body)
	w := httptest.NewRecorder()
	Setup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("got %d body=%s, want 409", w.Code, w.Body.String())
	}
}

// TestSetup_FirstRunDisabled_403 pins the refusal an operator gets after
// closing the onboarding surface with bootstrap.allow_first_run_setup: false.
// It must not read as "already completed" — the users table is empty, so a
// 409 would send the operator looking for an account nobody ever created.
func TestSetup_FirstRunDisabled_403(t *testing.T) {
	t.Parallel()
	svc := nilBackedSetupService(true)
	svc.FirstRunAllowed = func() bool { return false }
	body := strings.NewReader(`{
		"admin":  {"username":"admin","password":"password123"},
		"locale": {"locale":"de","theme":"light"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", body)
	w := httptest.NewRecorder()
	Setup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d body=%s, want 403", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "allow_first_run_setup") {
		t.Errorf("refusal must name the toggle that closed the surface: %s", w.Body.String())
	}
}

func TestSetup_BadJSON_400(t *testing.T) {
	t.Parallel()
	svc := nilBackedSetupService(true)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader("NOT JSON"))
	w := httptest.NewRecorder()
	Setup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", w.Code)
	}
}

// TestSetup_InvalidPayload_422 covers all validation branches that produce
// HTTP 422 before any database write is attempted.
func TestSetup_InvalidPayload_422(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
	}{
		{
			name: "password_too_short",
			body: `{"admin":{"username":"admin","password":"short"},"locale":{"locale":"de","theme":"light"}}`,
		},
		{
			name: "locale_invalid",
			body: `{"admin":{"username":"admin","password":"password123"},"locale":{"locale":"fr","theme":"light"}}`,
		},
		{
			name: "theme_invalid",
			body: `{"admin":{"username":"admin","password":"password123"},"locale":{"locale":"de","theme":"custom"}}`,
		},
		{
			name: "ccu_empty_interfaces",
			body: `{"admin":{"username":"admin","password":"password123"},"locale":{"locale":"de","theme":"light"},"ccu":{"name":"ccu1","host":"192.168.1.1","interfaces":[]}}`,
		},
		{
			name: "mqtt_empty_broker_url",
			body: `{"admin":{"username":"admin","password":"password123"},"locale":{"locale":"de","theme":"light"},"mqtt":{"broker_url":""}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := nilBackedSetupService(true)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			Setup(svc).ServeHTTP(w, req)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("got %d body=%s, want 422", w.Code, w.Body.String())
			}
		})
	}
}

func TestSetup_NilUsers_503(t *testing.T) {
	t.Parallel()
	svc := &SetupService{
		Users:    nil,
		Sections: sqlite.NewConfigSectionStore(nil),
		Required: func(context.Context) bool { return true },
	}
	body := strings.NewReader(`{
		"admin":  {"username":"admin","password":"password123"},
		"locale": {"locale":"de","theme":"light"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", body)
	w := httptest.NewRecorder()
	Setup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", w.Code)
	}
}
