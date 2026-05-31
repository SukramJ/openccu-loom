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
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// fakeTokenAdminService is an in-memory TokenAdminService for tests.
type fakeTokenAdminService struct {
	tokens    []sqlite.TokenRow
	createErr error
	deleteErr error
}

func (f *fakeTokenAdminService) Create(_ context.Context, in sqlite.CreateInput) (sqlite.CreateResult, error) {
	if f.createErr != nil {
		return sqlite.CreateResult{}, f.createErr
	}
	fp := "…TEST1"
	f.tokens = append(f.tokens, sqlite.TokenRow{
		Fingerprint: fp,
		Subject:     in.Subject,
		Role:        in.Role,
		CreatedAt:   time.Now().UTC(),
	})
	return sqlite.CreateResult{Token: "plaintext-token-abc", Fingerprint: fp}, nil
}

func (f *fakeTokenAdminService) Delete(_ context.Context, fingerprint string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	for i, tok := range f.tokens {
		if tok.Fingerprint == fingerprint {
			f.tokens = append(f.tokens[:i], f.tokens[i+1:]...)
			return nil
		}
	}
	return sqlite.ErrTokenNotFound
}

func (f *fakeTokenAdminService) List(_ context.Context) ([]sqlite.TokenRow, error) {
	return f.tokens, nil
}

func newFakeTokenSvc() *fakeTokenAdminService {
	now := time.Now().UTC()
	return &fakeTokenAdminService{
		tokens: []sqlite.TokenRow{
			{Fingerprint: "…AAABBB", Subject: "ci-bot", Role: auth.RoleOperator, CreatedAt: now},
		},
	}
}

// --- CreateTokenAdmin ---

func TestCreateTokenAdmin_Happy(t *testing.T) {
	t.Parallel()
	svc := &fakeTokenAdminService{}
	body := strings.NewReader(`{"subject":"ci-bot","role":"operator"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/auth/tokens", body)
	w := httptest.NewRecorder()
	CreateTokenAdmin(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var resp createTokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The plaintext token must surface in the response exactly once.
	if resp.Token == "" {
		t.Error("expected non-empty token in response")
	}
	if resp.Fingerprint == "" {
		t.Error("expected non-empty fingerprint in response")
	}
	if resp.Subject != "ci-bot" {
		t.Errorf("expected subject=ci-bot, got %q", resp.Subject)
	}
	if resp.Role != auth.RoleOperator {
		t.Errorf("expected role=operator, got %q", resp.Role)
	}
}

func TestCreateTokenAdmin_BadBody_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeTokenAdminService{}
	req := httptest.NewRequest(http.MethodPost, "/admin/auth/tokens", strings.NewReader("NOT JSON"))
	w := httptest.NewRecorder()
	CreateTokenAdmin(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateTokenAdmin_MissingSubject_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeTokenAdminService{}
	body := strings.NewReader(`{"role":"operator"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/auth/tokens", body)
	w := httptest.NewRecorder()
	CreateTokenAdmin(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateTokenAdmin_InvalidRole_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeTokenAdminService{}
	body := strings.NewReader(`{"subject":"ci-bot","role":"superuser"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/auth/tokens", body)
	w := httptest.NewRecorder()
	CreateTokenAdmin(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- DeleteTokenAdmin ---

func TestDeleteTokenAdmin_Happy(t *testing.T) {
	t.Parallel()
	svc := newFakeTokenSvc()
	req := httptest.NewRequest(http.MethodDelete, "/admin/auth/tokens/…AAABBB", http.NoBody)
	req = withChiParam(req, "fingerprint", "…AAABBB")
	w := httptest.NewRecorder()
	DeleteTokenAdmin(svc, audit.NoopRecorder()).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteTokenAdmin_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	svc := newFakeTokenSvc()
	req := httptest.NewRequest(http.MethodDelete, "/admin/auth/tokens/…XXXXXX", http.NoBody)
	req = withChiParam(req, "fingerprint", "…XXXXXX")
	w := httptest.NewRecorder()
	DeleteTokenAdmin(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- ListTokensV2 ---

func TestListTokensV2_RedactedList(t *testing.T) {
	t.Parallel()
	svc := newFakeTokenSvc()
	req := httptest.NewRequest(http.MethodGet, "/admin/auth/tokens", http.NoBody)
	w := httptest.NewRecorder()
	ListTokensV2(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var rows []tokenListEntry
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 token, got %d", len(rows))
	}
	// Verify no plaintext token field — tokenListEntry has no Token field.
	raw := w.Body.String()
	if strings.Contains(raw, `"token"`) {
		t.Error("plaintext token field must not appear in list response")
	}
	if rows[0].Subject != "ci-bot" {
		t.Errorf("expected subject=ci-bot, got %q", rows[0].Subject)
	}
}

func TestListTokensV2_Empty(t *testing.T) {
	t.Parallel()
	svc := &fakeTokenAdminService{}
	req := httptest.NewRequest(http.MethodGet, "/admin/auth/tokens", http.NoBody)
	w := httptest.NewRecorder()
	ListTokensV2(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var rows []tokenListEntry
	_ = json.Unmarshal(w.Body.Bytes(), &rows)
	if len(rows) != 0 {
		t.Errorf("expected empty, got %d", len(rows))
	}
}
