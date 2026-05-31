// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
)

func newAuthDeps() *AuthDeps {
	return &AuthDeps{
		Tokens: auth.NewMemoryTokenStore(map[string]auth.Identity{}),
	}
}

func TestCreateToken_HappyPath(t *testing.T) {
	t.Parallel()
	d := newAuthDeps()
	body, _ := json.Marshal(CreateTokenRequest{Subject: "homeassistant", Role: "operator"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens", bytes.NewReader(body))
	w := httptest.NewRecorder()
	CreateToken(d).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got CreateTokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Token == "" || len(got.Token) < 40 {
		t.Fatalf("token too short: %q", got.Token)
	}
	if got.ID == "" {
		t.Fatal("id must not be empty")
	}
	if !strings.HasPrefix(got.Fingerprint, "…") {
		t.Fatalf("fingerprint format off: %q", got.Fingerprint)
	}
	if got.Subject != "homeassistant" || got.Role != "operator" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestCreateToken_RejectInvalidRole(t *testing.T) {
	t.Parallel()
	d := newAuthDeps()
	body, _ := json.Marshal(CreateTokenRequest{Subject: "x", Role: "root"})
	w := httptest.NewRecorder()
	CreateToken(d).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens", bytes.NewReader(body)))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}
}

func TestCreateToken_RejectEmptySubject(t *testing.T) {
	t.Parallel()
	d := newAuthDeps()
	body, _ := json.Marshal(CreateTokenRequest{Subject: "  ", Role: "viewer"})
	w := httptest.NewRecorder()
	CreateToken(d).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens", bytes.NewReader(body)))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}
}

func TestCreateToken_NilStore503(t *testing.T) {
	t.Parallel()
	body, _ := json.Marshal(CreateTokenRequest{Subject: "x", Role: "viewer"})
	w := httptest.NewRecorder()
	CreateToken(nil).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens", bytes.NewReader(body)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestDeleteToken_HappyPath(t *testing.T) {
	t.Parallel()
	d := newAuthDeps()
	body, _ := json.Marshal(CreateTokenRequest{Subject: "ci", Role: "operator"})
	w := httptest.NewRecorder()
	CreateToken(d).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens", bytes.NewReader(body)))
	var created CreateTokenResponse
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	r := chi.NewRouter()
	r.Delete("/auth/tokens/{id}", DeleteToken(d))
	req := httptest.NewRequest(http.MethodDelete, "/auth/tokens/"+created.ID, http.NoBody)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req)
	if w2.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", w2.Code, w2.Body.String())
	}

	// Second delete = 404
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, httptest.NewRequest(http.MethodDelete, "/auth/tokens/"+created.ID, http.NoBody))
	if w3.Code != http.StatusNotFound {
		t.Fatalf("idempotent delete should 404 second time, got %d", w3.Code)
	}
}

func TestCreateToken_EmitsAuditEntry(t *testing.T) {
	t.Parallel()
	rec := audit.NewBuffer(10)
	d := newAuthDeps()
	d.AuditRecorder = rec
	body, _ := json.Marshal(CreateTokenRequest{Subject: "ci", Role: "admin"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens", bytes.NewReader(body))
	req = req.WithContext(auth.ContextWithIdentity(req.Context(), auth.Identity{Subject: "alice", Role: auth.RoleAdmin}))
	w := httptest.NewRecorder()
	CreateToken(d).ServeHTTP(w, req)

	entries := rec.List(10)
	if len(entries) != 1 {
		t.Fatalf("audit entries=%d, want 1", len(entries))
	}
	e := entries[0]
	if e.Action != audit.ActionTokenCreate {
		t.Fatalf("Action = %q", e.Action)
	}
	if e.User != "alice" {
		t.Fatalf("User = %q, want alice", e.User)
	}
	if !strings.Contains(e.Note, "subject=ci") || !strings.Contains(e.Note, "role=admin") {
		t.Fatalf("Note missing expected fields: %q", e.Note)
	}
}

func TestDeleteToken_EmitsAuditEntry(t *testing.T) {
	t.Parallel()
	rec := audit.NewBuffer(10)
	d := newAuthDeps()
	d.AuditRecorder = rec
	id := d.Tokens.Put("some-token-1234567890", auth.Identity{Subject: "ci", Role: auth.RoleViewer})

	r := chi.NewRouter()
	r.Delete("/auth/tokens/{id}", DeleteToken(d))
	req := httptest.NewRequest(http.MethodDelete, "/auth/tokens/"+id, http.NoBody)
	req = req.WithContext(auth.ContextWithIdentity(req.Context(), auth.Identity{Subject: "alice", Role: auth.RoleAdmin}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", w.Code)
	}
	entries := rec.List(10)
	if len(entries) != 1 {
		t.Fatalf("audit entries=%d, want 1", len(entries))
	}
	if entries[0].Action != audit.ActionTokenRevoke {
		t.Fatalf("Action = %q", entries[0].Action)
	}
	if entries[0].User != "alice" {
		t.Fatalf("User = %q", entries[0].User)
	}
}

func TestListTokens_IncludesID(t *testing.T) {
	t.Parallel()
	d := newAuthDeps()
	d.Tokens.Put("super-secret-token-12345678", auth.Identity{Subject: "alice", Role: auth.RoleViewer})
	w := httptest.NewRecorder()
	ListTokens(d).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/tokens", http.NoBody))
	var got []TokenListEntry
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].ID == "" {
		t.Fatal("id field must be populated")
	}
	if got[0].Fingerprint == "" || got[0].Subject != "alice" {
		t.Fatalf("list mismatch: %+v", got[0])
	}
}
