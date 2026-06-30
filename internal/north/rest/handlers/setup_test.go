// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// setupOpenMu serialises sqlite.Open calls to avoid a data race in the
// goose library's package-level embed pointer when tests run in parallel.
var setupOpenMu sync.Mutex

// openSetupStores opens a fresh, fully-migrated SQLite database in t's temp
// directory and returns all three stores the setup handler needs.
func openSetupStores(t *testing.T) (*sqlite.UserStore, *sqlite.CentralsStore, *sqlite.ConfigSectionStore) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "setup.db") + "?_pragma=journal_mode(WAL)"
	setupOpenMu.Lock()
	db, err := sqlite.Open(context.Background(), dsn)
	setupOpenMu.Unlock()
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
