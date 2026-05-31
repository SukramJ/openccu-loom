// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

func newTokenStore(t *testing.T) *TokenStore {
	t.Helper()
	return NewTokenStore(openTestDB(t, "tokens.db"))
}

// TestTokenStoreCreateReturnsPlaintextAndFingerprint verifies that
// Create returns a non-empty token and a fingerprint of the form
// "…XXXXXX" (ellipsis + last 6 chars).
func TestTokenStoreCreateReturnsPlaintextAndFingerprint(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	res, err := s.Create(ctx, CreateInput{Subject: "alice", Role: auth.RoleAdmin})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Token == "" {
		t.Fatal("Token is empty")
	}
	if res.Fingerprint == "" {
		t.Fatal("Fingerprint is empty")
	}
	// Fingerprint must start with the ellipsis character.
	if !strings.HasPrefix(res.Fingerprint, "…") {
		t.Errorf("Fingerprint=%q does not start with '…'", res.Fingerprint)
	}
	// The last 6 chars of the token must be the suffix after "…".
	wantSuffix := res.Token[len(res.Token)-6:]
	gotSuffix := res.Fingerprint[len("…"):]
	if gotSuffix != wantSuffix {
		t.Errorf("Fingerprint suffix %q != last 6 of token %q", gotSuffix, wantSuffix)
	}
}

// TestTokenStoreCreateEmptySubject verifies that an empty subject is
// rejected.
func TestTokenStoreCreateEmptySubject(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	_, err := s.Create(ctx, CreateInput{Subject: "", Role: auth.RoleViewer})
	if err == nil {
		t.Error("Create with empty subject: expected error, got nil")
	}
}

// TestTokenStoreCreateEmptyRole verifies that an empty role is rejected.
func TestTokenStoreCreateEmptyRole(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	_, err := s.Create(ctx, CreateInput{Subject: "bob", Role: ""})
	if err == nil {
		t.Error("Create with empty role: expected error, got nil")
	}
}

// TestTokenStoreAuthenticateTokenHappyPath verifies that the freshly
// returned plaintext authenticates successfully.
func TestTokenStoreAuthenticateTokenHappyPath(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	res, err := s.Create(ctx, CreateInput{Subject: "carol", Role: auth.RoleOperator})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id, err := s.AuthenticateToken(ctx, res.Token)
	if err != nil {
		t.Fatalf("AuthenticateToken: %v", err)
	}
	if id.Subject != "carol" {
		t.Errorf("Subject=%q want carol", id.Subject)
	}
	if id.Role != auth.RoleOperator {
		t.Errorf("Role=%q want operator", id.Role)
	}
	if id.Scheme != auth.SchemeBearer {
		t.Errorf("Scheme=%q want bearer", id.Scheme)
	}
	if id.TokenID != res.Fingerprint {
		t.Errorf("TokenID=%q want %q (fingerprint)", id.TokenID, res.Fingerprint)
	}
}

// TestTokenStoreAuthenticateTokenWrongToken verifies ErrUnauthenticated
// when the secret does not match any stored hash.
func TestTokenStoreAuthenticateTokenWrongToken(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	if _, err := s.Create(ctx, CreateInput{Subject: "dave", Role: auth.RoleViewer}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := s.AuthenticateToken(ctx, "totally-wrong-token")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("wrong token: want ErrUnauthenticated, got %v", err)
	}
}

// TestTokenStoreAuthenticateTokenEmpty verifies ErrUnauthenticated for
// an empty secret.
func TestTokenStoreAuthenticateTokenEmpty(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	_, err := s.AuthenticateToken(ctx, "")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("empty token: want ErrUnauthenticated, got %v", err)
	}
}

// TestTokenStoreDeleteHappyPath verifies that Delete by fingerprint
// removes the token.
func TestTokenStoreDeleteHappyPath(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	res, err := s.Create(ctx, CreateInput{Subject: "eve", Role: auth.RoleViewer})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Delete(ctx, res.Fingerprint); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Token must no longer authenticate.
	if _, err := s.AuthenticateToken(ctx, res.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("after Delete: want ErrUnauthenticated, got %v", err)
	}
}

// TestTokenStoreDeleteUnknown verifies ErrTokenNotFound for an absent
// fingerprint.
func TestTokenStoreDeleteUnknown(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	err := s.Delete(ctx, "…nosuch")
	if !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("Delete unknown: want ErrTokenNotFound, got %v", err)
	}
}

// TestTokenStoreListRedacted verifies that List returns a row with no
// plaintext token and the fingerprint is the display-safe value.
func TestTokenStoreListRedacted(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	res, err := s.Create(ctx, CreateInput{Subject: "frank", Role: auth.RoleAdmin})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List len=%d want 1", len(rows))
	}
	r := rows[0]
	if r.Subject != "frank" {
		t.Errorf("Subject=%q want frank", r.Subject)
	}
	if r.Role != auth.RoleAdmin {
		t.Errorf("Role=%q want admin", r.Role)
	}
	// The row struct carries Fingerprint; the plaintext token must NOT
	// appear anywhere in the row.
	if r.Fingerprint != res.Fingerprint {
		t.Errorf("Fingerprint=%q want %q", r.Fingerprint, res.Fingerprint)
	}
	// Defensive: TokenRow has no Token field — ensure the fingerprint
	// is not accidentally the full token.
	if r.Fingerprint == res.Token {
		t.Error("Fingerprint equals plaintext token — row is not redacted")
	}
}

// TestTokenStoreListSortedBySubject verifies ORDER BY subject.
func TestTokenStoreListSortedBySubject(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	for _, sub := range []string{"zoe", "alice", "bob"} {
		if _, err := s.Create(ctx, CreateInput{Subject: sub, Role: auth.RoleViewer}); err != nil {
			t.Fatalf("Create %s: %v", sub, err)
		}
	}
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("List len=%d want 3", len(rows))
	}
	want := []string{"alice", "bob", "zoe"}
	for i, w := range want {
		if rows[i].Subject != w {
			t.Errorf("rows[%d].Subject=%q want %q", i, rows[i].Subject, w)
		}
	}
}
