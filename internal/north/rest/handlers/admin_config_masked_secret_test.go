// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// getSection drives GetConfigSection for the given section.
func getSection(svc ConfigAdminService, section string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/"+section, http.NoBody)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("section", section)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	GetConfigSection(svc).ServeHTTP(w, req)
	return w
}

// TestGetConfigSection_MasksSecrets verifies the per-section GET masks secret
// values to "***" instead of handing the operator's cleartext credential to
// the browser. The section store opens (decrypts) secrets on read, so without
// masking the GET would leak them — unlike the snapshot endpoint, which masks.
func TestGetConfigSection_MasksSecrets(t *testing.T) {
	t.Parallel()

	fake := &fakeConfigAdminSvc{
		getSectionRow: sqlitestore.SectionRow{
			Section:   "north.mqtt",
			ValueJSON: []byte(`{"enabled":true,"password":"hunter2"}`),
		},
	}
	w := getSection(fake, "north.mqtt")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "hunter2") {
		t.Fatalf("cleartext secret leaked to the GET response: %s", w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if got["password"] != "***" {
		t.Errorf("password should be masked to ***, got %v", got["password"])
	}
	if got["enabled"] != true {
		t.Errorf("non-secret field must pass through unchanged, got %v", got["enabled"])
	}
}

// putSection drives PutConfigSection with the given section + JSON body.
func putSection(svc ConfigAdminService, section, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/"+section, strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("section", section)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	PutConfigSection(svc, nil).ServeHTTP(w, req)
	return w
}

// TestPutConfigSection_RestoresMaskedSecrets reproduces the operator's report:
// editing north.rest.public_url and saving did nothing because the section
// carries the complex secret fields auth.users / auth.tokens (map[string]string).
// The GET masks them to "***"; the SPA echoes the sentinel back; without
// restoration the strict unmarshal of "***" into a map fails (so the save
// 400s) — or, for a string secret, the sentinel would overwrite the real value.
// The fix restores every masked sentinel to the current real value before
// validation + persistence, so the edited field saves and existing secrets
// are preserved.
func TestPutConfigSection_RestoresMaskedSecrets(t *testing.T) {
	t.Parallel()

	current := &config.Config{
		North: config.NorthConfig{
			REST: config.NorthREST{
				PublicURL: "https://old.example",
				Auth: config.AuthConfig{
					Users:  map[string]string{"admin": "$2a$10$realhashvalue"},
					Tokens: map[string]string{"tok": "admin"},
				},
			},
		},
	}
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	// The operator changed public_url; the SPA round-trips the masked sentinel
	// for the two secret maps it never received in cleartext.
	body := `{"public_url":"https://loom-rc.toonlan.de/","auth":{"users":"***","tokens":"***"}}`
	w := putSection(fake, "north.rest", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	if !fake.putCalled {
		t.Fatal("PutSection was never called — the save silently aborted")
	}

	var saved config.NorthREST
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON is invalid: %v", err)
	}
	if saved.PublicURL != "https://loom-rc.toonlan.de/" {
		t.Errorf("edited public_url not persisted: %q", saved.PublicURL)
	}
	if got := saved.Auth.Users["admin"]; got != "$2a$10$realhashvalue" {
		t.Errorf("masked secret map auth.users not restored: %#v", saved.Auth.Users)
	}
	if got := saved.Auth.Tokens["tok"]; got != "admin" {
		t.Errorf("masked secret map auth.tokens not restored: %#v", saved.Auth.Tokens)
	}
}

// TestGetConfigSection_MasksWebhookSecret verifies the outbound-webhook signing
// secret is masked on GET — a string secret (north.webhook.secret) that, if
// leaked, would let anyone forge signed webhook deliveries.
func TestGetConfigSection_MasksWebhookSecret(t *testing.T) {
	t.Parallel()

	fake := &fakeConfigAdminSvc{
		getSectionRow: sqlitestore.SectionRow{
			Section:   "north.webhook",
			ValueJSON: []byte(`{"enabled":true,"url":"https://hook.example","secret":"s3cr3t-key"}`),
		},
	}
	w := getSection(fake, "north.webhook")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "s3cr3t-key") {
		t.Fatalf("cleartext webhook secret leaked to the GET response: %s", w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if got["secret"] != "***" {
		t.Errorf("secret should be masked to ***, got %v", got["secret"])
	}
	if got["url"] != "https://hook.example" {
		t.Errorf("non-secret field must pass through unchanged, got %v", got["url"])
	}
}

// TestPutConfigSection_RestoresWebhookSecret verifies the masked webhook secret
// is restored to the operator's stored value on save, so editing an unrelated
// webhook field (e.g. the URL) does not overwrite the real signing secret with
// the "***" sentinel.
func TestPutConfigSection_RestoresWebhookSecret(t *testing.T) {
	t.Parallel()

	current := &config.Config{
		North: config.NorthConfig{
			Webhook: config.NorthWebhook{
				Enabled: true,
				URL:     "https://hook.example",
				Secret:  "real-signing-key",
			},
		},
	}
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	// The operator changed the URL; the SPA echoes the masked secret unchanged.
	body := `{"enabled":true,"url":"https://hook.example/v2","secret":"***"}`
	w := putSection(fake, "north.webhook", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	if !fake.putCalled {
		t.Fatal("PutSection was never called — the save silently aborted")
	}

	var saved config.NorthWebhook
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON is invalid: %v", err)
	}
	if saved.URL != "https://hook.example/v2" {
		t.Errorf("edited url not persisted: %q", saved.URL)
	}
	if saved.Secret != "real-signing-key" {
		t.Errorf("masked webhook secret not restored: %q", saved.Secret)
	}
}

// TestPutConfigSection_KeepsOperatorSuppliedSecret verifies a genuinely changed
// secret (a non-sentinel value) is persisted verbatim — restoration only
// touches the "***" sentinel, never operator-supplied new secrets.
func TestPutConfigSection_KeepsOperatorSuppliedSecret(t *testing.T) {
	t.Parallel()

	current := &config.Config{
		North: config.NorthConfig{
			REST: config.NorthREST{
				Auth: config.AuthConfig{Users: map[string]string{"admin": "$2a$10$old"}},
			},
		},
	}
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	body := `{"auth":{"users":{"admin":"$2a$10$brandnew"}}}`
	w := putSection(fake, "north.rest", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	var saved config.NorthREST
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON is invalid: %v", err)
	}
	if got := saved.Auth.Users["admin"]; got != "$2a$10$brandnew" {
		t.Errorf("operator-supplied new secret was not persisted: %#v", saved.Auth.Users)
	}
}

// TestPutConfigSection_EmptyComplexSecretPlaceholder reproduces the operator's
// actual 400: when no HTTP-basic users are configured, the section load
// returns north.rest WITHOUT auth.users, so the SPA's parseValue yields an
// empty string "" for that map[string]string field and the editor echoes it
// back. Strict unmarshal of "" into a map fails with
// `cannot unmarshal string into ... AuthConfig.auth.users of type map[string]string`.
// The reconcile must replace the empty placeholder (no stored value) so the
// edited public_url still saves.
func TestPutConfigSection_EmptyComplexSecretPlaceholder(t *testing.T) {
	t.Parallel()

	// Current config has NO basic-auth users/tokens.
	current := &config.Config{North: config.NorthConfig{REST: config.NorthREST{}}}
	fake := &fakeConfigAdminSvc{effectiveResult: &configstore.EffectiveResult{Config: current}}

	// What the SPA sends: a changed public_url plus the empty-string
	// placeholders for the unset secret maps.
	body := `{"public_url":"https://loom-rc.toonlan.de/","auth":{"users":"","tokens":""}}`
	w := putSection(fake, "north.rest", body)

	if w.Code != http.StatusOK {
		t.Fatalf("save should succeed; got %d: %s", w.Code, w.Body.String())
	}
	if !fake.putCalled {
		t.Fatal("PutSection was never called")
	}
	var saved config.NorthREST
	if err := json.Unmarshal(fake.putJSON, &saved); err != nil {
		t.Fatalf("persisted section JSON is invalid: %v", err)
	}
	if saved.PublicURL != "https://loom-rc.toonlan.de/" {
		t.Errorf("edited public_url not persisted: %q", saved.PublicURL)
	}
	if len(saved.Auth.Users) != 0 {
		t.Errorf("auth.users should stay empty, got: %#v", saved.Auth.Users)
	}
}
