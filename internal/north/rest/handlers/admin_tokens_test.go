// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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
		// Record the expiry the handler computed — a fake that drops it
		// would stay green against a token that is already expired.
		ExpiresAt: in.ExpiresAt,
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

// TestCreateTokenAdmin_CanonicalisesSubject pins that the handler hands the
// store — and reports back — the canonical subject rather than the operator's
// spelling, so the token list and the audit note name the identity the token
// will actually authenticate as.
func TestCreateTokenAdmin_CanonicalisesSubject(t *testing.T) {
	t.Parallel()
	svc := &fakeTokenAdminService{}
	body := strings.NewReader(`{"subject":"  CI-Bot  ","role":"operator"}`)
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
	if resp.Subject != "ci-bot" {
		t.Errorf("response subject=%q want %q", resp.Subject, "ci-bot")
	}
	if len(svc.tokens) != 1 {
		t.Fatalf("service holds %d tokens, want 1", len(svc.tokens))
	}
	if svc.tokens[0].Subject != "ci-bot" {
		t.Errorf("stored subject=%q want %q", svc.tokens[0].Subject, "ci-bot")
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

func TestCreateTokenAdmin_ExpiryAlwaysLandsInTheFuture(t *testing.T) {
	t.Parallel()
	// expires_in_days is multiplied into nanoseconds; past the
	// representable range the int64 product wraps negative and time.Add
	// moves the expiry backwards, minting a credential the bearer
	// resolver rejects on first use. The endpoint must refuse instead.
	cases := []struct {
		name string
		days int
		want int
	}{
		{name: "one day", days: 1, want: http.StatusCreated},
		{name: "largest representable", days: maxTokenExpiryDays, want: http.StatusCreated},
		{name: "one past representable", days: maxTokenExpiryDays + 1, want: http.StatusBadRequest},
		{name: "years mistaken for days", days: 200000, want: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &fakeTokenAdminService{}
			body := strings.NewReader(
				`{"subject":"svc","role":"operator","expires_in_days":` + strconv.Itoa(tc.days) + `}`,
			)
			req := httptest.NewRequest(http.MethodPost, "/admin/auth/tokens", body)
			w := httptest.NewRecorder()
			CreateTokenAdmin(svc, nil).ServeHTTP(w, req)

			if w.Code != tc.want {
				t.Fatalf("expected %d, got %d body=%s", tc.want, w.Code, w.Body.String())
			}
			if tc.want != http.StatusCreated {
				if len(svc.tokens) != 0 {
					t.Fatalf("expected no token to be minted, got %d", len(svc.tokens))
				}
				return
			}
			if len(svc.tokens) != 1 || svc.tokens[0].ExpiresAt == nil {
				t.Fatalf("expected one token with an expiry, got %+v", svc.tokens)
			}
			if !svc.tokens[0].ExpiresAt.After(time.Now().UTC()) {
				t.Errorf("stored expiry %s is not in the future", svc.tokens[0].ExpiresAt)
			}
		})
	}
}

// --- DeleteTokenAdmin ---

func TestDeleteTokenAdmin_Happy(t *testing.T) {
	t.Parallel()
	svc := newFakeTokenSvc()
	req := httptest.NewRequest(http.MethodDelete, "/admin/auth/tokens/…AAABBB", http.NoBody)
	req = withChiParam(req, "fingerprint", "…AAABBB")
	w := httptest.NewRecorder()
	DeleteTokenAdmin(svc, audit.NoopRecorder(), nil).ServeHTTP(w, req)

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
	DeleteTokenAdmin(svc, nil, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// recordingTokenSockets is a [TokenSocketRevoker] that records which
// fingerprints it was asked to disconnect.
type recordingTokenSockets struct {
	closed []string
}

func (r *recordingTokenSockets) CloseByToken(fingerprint string) int {
	r.closed = append(r.closed, fingerprint)
	return 1
}

// TestDeleteTokenAdmin_ClosesTheTokensSockets pins that revoking a bearer
// token reaches the WebSocket plane. REST re-resolves the credential on every
// request and refuses immediately; a socket resolved it once at the upgrade
// and gates every later command on that snapshot, so a revocation that stops
// at the token table leaves the leaked credential dispatching writes for as
// long as its connection answers pings.
func TestDeleteTokenAdmin_ClosesTheTokensSockets(t *testing.T) {
	t.Parallel()
	svc := newFakeTokenSvc()
	sockets := &recordingTokenSockets{}
	req := httptest.NewRequest(http.MethodDelete, "/admin/auth/tokens/…AAABBB", http.NoBody)
	req = withChiParam(req, "fingerprint", "…AAABBB")
	w := httptest.NewRecorder()
	DeleteTokenAdmin(svc, audit.NoopRecorder(), sockets).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if len(sockets.closed) != 1 || sockets.closed[0] != "…AAABBB" {
		t.Fatalf("closed sockets for %v, want […AAABBB]", sockets.closed)
	}
}

// TestDeleteTokenAdmin_UnknownFingerprintLeavesSocketsAlone keeps the
// teardown tied to a revocation that really happened: a 404 must not
// disconnect anybody.
func TestDeleteTokenAdmin_UnknownFingerprintLeavesSocketsAlone(t *testing.T) {
	t.Parallel()
	svc := newFakeTokenSvc()
	sockets := &recordingTokenSockets{}
	req := httptest.NewRequest(http.MethodDelete, "/admin/auth/tokens/…XXXXXX", http.NoBody)
	req = withChiParam(req, "fingerprint", "…XXXXXX")
	w := httptest.NewRecorder()
	DeleteTokenAdmin(svc, audit.NoopRecorder(), sockets).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if len(sockets.closed) != 0 {
		t.Fatalf("closed sockets %v on an unknown fingerprint, want none", sockets.closed)
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
