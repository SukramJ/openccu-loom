// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

func newTokenStore(t *testing.T) *TokenStore {
	t.Helper()
	return NewTokenStore(openTestDB(t, "tokens.db"))
}

// TestTokenStoreCreateReturnsPlaintextAndFingerprint verifies that Create
// returns a non-empty token and a fingerprint that is the 12-lowercase-hex
// prefix of the token's SHA-256 hash — never a slice of the plaintext.
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
	if len(res.Fingerprint) != 12 {
		t.Fatalf("Fingerprint=%q length=%d want 12", res.Fingerprint, len(res.Fingerprint))
	}
	for _, c := range res.Fingerprint {
		isLowerHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isLowerHex {
			t.Fatalf("Fingerprint=%q contains non-lowercase-hex char %q", res.Fingerprint, c)
		}
	}
	sum := sha256.Sum256([]byte(res.Token))
	wantFingerprint := hex.EncodeToString(sum[:])[:12]
	if res.Fingerprint != wantFingerprint {
		t.Errorf("Fingerprint=%q want %q (sha256(token) prefix)", res.Fingerprint, wantFingerprint)
	}
	// Defensive: the fingerprint must not be recoverable as a slice of the
	// plaintext token — it is derived from the hash, not the secret.
	if len(res.Token) >= 12 && res.Fingerprint == res.Token[len(res.Token)-12:] {
		t.Error("Fingerprint equals a plaintext token slice — must be hash-derived")
	}
}

// TestTokenStoreCreateCanonicalisesSubject pins that a token issued with the
// operator's own spelling is persisted under the canonical subject. The users
// table is keyed on that spelling, and the bearer identity flows straight into
// stores that compare it verbatim — per-user preferences and private diagram
// ownership — so a token row keyed on "Admin" splits one account into two
// identities and survives the purge of its own user.
func TestTokenStoreCreateCanonicalisesSubject(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	res, err := s.Create(ctx, CreateInput{Subject: "  Mallory  ", Role: auth.RoleOperator})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id, err := s.AuthenticateToken(ctx, res.Token)
	if err != nil {
		t.Fatalf("AuthenticateToken: %v", err)
	}
	if id.Subject != "mallory" {
		t.Errorf("Identity.Subject=%q want %q", id.Subject, "mallory")
	}
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List returned %d rows, want 1", len(rows))
	}
	if rows[0].Subject != "mallory" {
		t.Errorf("row Subject=%q want %q", rows[0].Subject, "mallory")
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

// TestTokenStoreAuthenticateTokenExpiredRejected verifies that a token
// created with an ExpiresAt in the past fails authentication with
// [auth.ErrUnauthenticated], the same uniform outcome as an unknown token.
func TestTokenStoreAuthenticateTokenExpiredRejected(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	past := time.Now().UTC().Add(-time.Hour)
	res, err := s.Create(ctx, CreateInput{Subject: "grace", Role: auth.RoleViewer, ExpiresAt: &past})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.AuthenticateToken(ctx, res.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("expired token: want ErrUnauthenticated, got %v", err)
	}
}

// TestTokenStoreAuthenticateTokenFutureExpiryAccepted verifies that a
// token with an ExpiresAt still in the future authenticates normally.
func TestTokenStoreAuthenticateTokenFutureExpiryAccepted(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	future := time.Now().UTC().Add(time.Hour)
	res, err := s.Create(ctx, CreateInput{Subject: "heidi", Role: auth.RoleOperator, ExpiresAt: &future})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id, err := s.AuthenticateToken(ctx, res.Token)
	if err != nil {
		t.Fatalf("AuthenticateToken with future ExpiresAt: %v", err)
	}
	if id.Subject != "heidi" {
		t.Errorf("Subject=%q want heidi", id.Subject)
	}
}

// TestTokenStoreAuthenticateTokenNilExpiryNeverExpires verifies that a
// token created without ExpiresAt (nil) authenticates without a time bound
// — the historical behaviour is preserved for tokens that opt out of
// expiry.
func TestTokenStoreAuthenticateTokenNilExpiryNeverExpires(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	res, err := s.Create(ctx, CreateInput{Subject: "ivan", Role: auth.RoleOperator})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.AuthenticateToken(ctx, res.Token); err != nil {
		t.Fatalf("AuthenticateToken with nil ExpiresAt: %v", err)
	}
}

// TestTokenStoreDeleteBySubjectRemovesAllForSubject verifies that
// DeleteBySubject removes every token issued to a subject, returns the
// count removed, and leaves other subjects' tokens authenticating.
func TestTokenStoreDeleteBySubjectRemovesAllForSubject(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	res1, err := s.Create(ctx, CreateInput{Subject: "judy", Role: auth.RoleViewer})
	if err != nil {
		t.Fatalf("Create judy 1: %v", err)
	}
	res2, err := s.Create(ctx, CreateInput{Subject: "judy", Role: auth.RoleViewer})
	if err != nil {
		t.Fatalf("Create judy 2: %v", err)
	}
	other, err := s.Create(ctx, CreateInput{Subject: "mallory", Role: auth.RoleViewer})
	if err != nil {
		t.Fatalf("Create mallory: %v", err)
	}

	n, err := s.DeleteBySubject(ctx, "judy")
	if err != nil {
		t.Fatalf("DeleteBySubject: %v", err)
	}
	if n != 2 {
		t.Fatalf("DeleteBySubject count=%d want 2", n)
	}
	if _, err := s.AuthenticateToken(ctx, res1.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Error("judy token 1 still authenticates after DeleteBySubject")
	}
	if _, err := s.AuthenticateToken(ctx, res2.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Error("judy token 2 still authenticates after DeleteBySubject")
	}
	if _, err := s.AuthenticateToken(ctx, other.Token); err != nil {
		t.Errorf("mallory token no longer authenticates after DeleteBySubject(judy): %v", err)
	}
}

// TestTokenStoreDeleteBySubjectIgnoresSubjectCasing verifies that the
// purge behind a deleted user account matches the subject regardless of
// the casing a token was issued with. The users table is canonicalised
// to lower case, so a token created from a mixed-case identity would
// otherwise outlive the account it belongs to.
func TestTokenStoreDeleteBySubjectIgnoresSubjectCasing(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	res, err := s.Create(ctx, CreateInput{Subject: "Markus", Role: auth.RoleAdmin})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	n, err := s.DeleteBySubject(ctx, "markus")
	if err != nil {
		t.Fatalf("DeleteBySubject: %v", err)
	}
	if n != 1 {
		t.Fatalf("DeleteBySubject count=%d want 1", n)
	}
	if _, err := s.AuthenticateToken(ctx, res.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Error("token still authenticates after the account was deleted")
	}
}

// TestTokenStoreDeleteBySubjectUnknownSubjectIsZero verifies that deleting
// tokens for a subject with none returns a zero count and no error.
func TestTokenStoreDeleteBySubjectUnknownSubjectIsZero(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	n, err := s.DeleteBySubject(ctx, "nobody")
	if err != nil {
		t.Fatalf("DeleteBySubject: %v", err)
	}
	if n != 0 {
		t.Errorf("DeleteBySubject count=%d want 0", n)
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

// TestTokenStoreListSurfacesExpiresAt verifies that List reports the
// stored ExpiresAt for a token created with one, and nil for a token
// created without.
func TestTokenStoreListSurfacesExpiresAt(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	future := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	if _, err := s.Create(ctx, CreateInput{Subject: "niaj", Role: auth.RoleViewer, ExpiresAt: &future}); err != nil {
		t.Fatalf("Create niaj: %v", err)
	}
	if _, err := s.Create(ctx, CreateInput{Subject: "oscar", Role: auth.RoleViewer}); err != nil {
		t.Fatalf("Create oscar: %v", err)
	}

	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var niaj, oscar *TokenRow
	for i := range rows {
		switch rows[i].Subject {
		case "niaj":
			niaj = &rows[i]
		case "oscar":
			oscar = &rows[i]
		}
	}
	if niaj == nil || oscar == nil {
		t.Fatalf("expected rows for both niaj and oscar, got %+v", rows)
	}
	if niaj.ExpiresAt == nil {
		t.Fatal("niaj.ExpiresAt is nil, want the stored expiry")
	}
	if !niaj.ExpiresAt.Equal(future) {
		t.Errorf("niaj.ExpiresAt=%v want %v", niaj.ExpiresAt, future)
	}
	if oscar.ExpiresAt != nil {
		t.Errorf("oscar.ExpiresAt=%v want nil (no expiry set)", oscar.ExpiresAt)
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

// TestTokenStoreImportPreservesSecret verifies the legacy-token migration path:
// Import stores a token whose plaintext is already known (a config-file token)
// so the exact bearer value keeps authenticating, and Count reflects it.
func TestTokenStoreImportPreservesSecret(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	const secret = "legacy-config-token-value"
	fp, err := s.Import(ctx, secret, "config-token", auth.RoleOperator)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(fp) != 12 {
		t.Errorf("fingerprint=%q length=%d want 12", fp, len(fp))
	}

	// The exact secret must authenticate to the imported identity.
	id, err := s.AuthenticateToken(ctx, secret)
	if err != nil {
		t.Fatalf("AuthenticateToken(imported secret): %v", err)
	}
	if id.Role != auth.RoleOperator {
		t.Errorf("role=%q want operator", id.Role)
	}
	if id.Subject != "config-token" {
		t.Errorf("subject=%q want config-token", id.Subject)
	}

	n, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Errorf("Count=%d want 1", n)
	}
}

// TestTokenStoreImportCanonicalisesSubject pins that a migrated legacy token
// lands under the canonical subject too, so an upgraded daemon does not
// resurrect the split identity the import path was meant to preserve.
func TestTokenStoreImportCanonicalisesSubject(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	const secret = "legacy-mixed-case-token"
	if _, err := s.Import(ctx, secret, "  Config-Token  ", auth.RoleOperator); err != nil {
		t.Fatalf("Import: %v", err)
	}
	id, err := s.AuthenticateToken(ctx, secret)
	if err != nil {
		t.Fatalf("AuthenticateToken: %v", err)
	}
	if id.Subject != "config-token" {
		t.Errorf("Identity.Subject=%q want %q", id.Subject, "config-token")
	}
}

// TestTokenStoreImportIsIdempotent verifies a re-run of the migration does not
// duplicate an already-imported token (ON CONFLICT DO NOTHING on fingerprint).
func TestTokenStoreImportIsIdempotent(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	const secret = "same-secret"
	if _, err := s.Import(ctx, secret, "config-token", auth.RoleAdmin); err != nil {
		t.Fatalf("Import #1: %v", err)
	}
	if _, err := s.Import(ctx, secret, "config-token", auth.RoleAdmin); err != nil {
		t.Fatalf("Import #2: %v", err)
	}
	n, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Errorf("Count=%d want 1 (import must be idempotent)", n)
	}
}

// TestTokenStoreImportRejectsEmpty verifies the guards on the migration path.
func TestTokenStoreImportRejectsEmpty(t *testing.T) {
	s := newTokenStore(t)
	ctx := context.Background()

	if _, err := s.Import(ctx, "", "config-token", auth.RoleAdmin); err == nil {
		t.Error("Import with empty secret must fail")
	}
	if _, err := s.Import(ctx, "secret", "", auth.RoleAdmin); err == nil {
		t.Error("Import with empty subject must fail")
	}
	if _, err := s.Import(ctx, "secret", "config-token", ""); err == nil {
		t.Error("Import with empty role must fail")
	}
}
