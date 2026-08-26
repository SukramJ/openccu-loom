// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"context"
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

// TestCreateToken_OversizedBodyReturns413 verifies that CreateToken
// caps its request body the same way every other JSON handler does
// ([DecodeJSON]/[maxRequestBodyBytes]) instead of reading an
// unbounded body into memory before rejecting it.
func TestCreateToken_OversizedBodyReturns413(t *testing.T) {
	t.Parallel()
	d := newAuthDeps()
	oversize := bytes.Repeat([]byte("x"), maxRequestBodyBytes+1)
	body := append([]byte(`{"subject":"`), oversize...)
	body = append(body, []byte(`","role":"viewer"}`)...)
	w := httptest.NewRecorder()
	CreateToken(d).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens", bytes.NewReader(body)))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized body, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestCreateToken_StoresTheCanonicalSubject pins that a token minted
// here is bound to the same spelling of the subject every other
// credential surface is keyed on. Stored raw, a token issued for "Bob"
// is invisible to the purge that runs when the "bob" account is deleted,
// so a deleted user keeps a live bearer credential; the identity it
// resolves to also fails to match sessions and audit rows.
func TestCreateToken_StoresTheCanonicalSubject(t *testing.T) {
	t.Parallel()
	d := newAuthDeps()
	body, _ := json.Marshal(CreateTokenRequest{Subject: "  Bob  ", Role: "operator"})
	w := httptest.NewRecorder()
	CreateToken(d).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens", bytes.NewReader(body)))
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var created CreateTokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Subject != "bob" {
		t.Errorf("response subject=%q, want the stored spelling bob", created.Subject)
	}
	entries := d.Tokens.List()
	if len(entries) != 1 {
		t.Fatalf("List len=%d want 1", len(entries))
	}
	if entries[0].Subject != "bob" {
		t.Errorf("stored subject=%q, want bob", entries[0].Subject)
	}
	// The purge the account deletion triggers must find it.
	n, err := d.Tokens.DeleteBySubject(context.Background(), "bob")
	if err != nil {
		t.Fatalf("DeleteBySubject: %v", err)
	}
	if n != 1 {
		t.Fatalf("DeleteBySubject removed %d tokens, want 1", n)
	}
	if _, err := d.Tokens.AuthenticateToken(context.Background(), created.Token); err == nil {
		t.Error("the token still authenticates after its account was purged")
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
	r.Delete("/auth/tokens/{id}", DeleteToken(d, nil))
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
	r.Delete("/auth/tokens/{id}", DeleteToken(d, nil))
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
