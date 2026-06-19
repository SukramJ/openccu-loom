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
)

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
