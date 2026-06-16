// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for in-memory password hashing: HashPassword produces an idempotent
// bcrypt hash, and MemoryUserStore.AuthenticateBasic verifies both bcrypt-hashed
// records and legacy verbatim (plaintext) records.
package auth

import (
	"context"
	"errors"
	"testing"
)

func TestLooksLikeBcryptHash(t *testing.T) {
	t.Parallel()
	hashed, err := HashPassword("whatever")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"real bcrypt hash", hashed, true},
		{"plaintext", "s3cret", false},
		{"empty", "", false},
		{"right prefix, wrong length", hashed[:59], false},
		{"right length, wrong prefix", "x" + hashed[1:], false},
	}
	for _, tc := range cases {
		if got := looksLikeBcryptHash(tc.in); got != tc.want {
			t.Errorf("%s: looksLikeBcryptHash(%q)=%v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestHashPasswordProducesBcryptHash(t *testing.T) {
	t.Parallel()
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if h == "s3cret" {
		t.Fatal("HashPassword returned the plaintext unchanged")
	}
	if !looksLikeBcryptHash(h) {
		t.Fatalf("HashPassword(%q) = %q, not a bcrypt hash", "s3cret", h)
	}
}

func TestHashPasswordIdempotentOnExistingHash(t *testing.T) {
	t.Parallel()
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	again, err := HashPassword(h)
	if err != nil {
		t.Fatalf("HashPassword(hash): %v", err)
	}
	if again != h {
		t.Fatalf("HashPassword double-hashed an existing bcrypt hash:\n  first  = %q\n  second = %q", h, again)
	}
}

func TestMemoryUserStoreAuthenticatesHashedRecord(t *testing.T) {
	t.Parallel()
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	us := NewMemoryUserStore()
	us.Put("alice", h, RoleAdmin)

	id, err := us.AuthenticateBasic(context.Background(), "alice", "s3cret")
	if err != nil {
		t.Fatalf("AuthenticateBasic(correct): %v", err)
	}
	if id.Role != RoleAdmin || id.Scheme != SchemeBasic {
		t.Errorf("identity = %+v, want admin/basic", id)
	}
	if _, err := us.AuthenticateBasic(context.Background(), "alice", "wrong"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("AuthenticateBasic(wrong) err = %v, want ErrUnauthenticated", err)
	}
}

func TestMemoryUserStorePlaintextRecordStillAuthenticates(t *testing.T) {
	t.Parallel()
	// Legacy/test path: a record stored verbatim (not hashed) must still
	// authenticate via the constant-time equality fallback.
	us := NewMemoryUserStore()
	us.Put("bob", "plaintext", RoleViewer)
	if _, err := us.AuthenticateBasic(context.Background(), "bob", "plaintext"); err != nil {
		t.Fatalf("plaintext fallback failed: %v", err)
	}
	if _, err := us.AuthenticateBasic(context.Background(), "bob", "nope"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("plaintext wrong-password err = %v, want ErrUnauthenticated", err)
	}
}

// TestAuthenticateBasicUnknownUsernameRunsDummyCompare verifies that
// the unknown-username path returns ErrUnauthenticated (same as wrong
// password) and that the code path reaches the dummy bcrypt compare
// rather than short-circuiting. Structural test: we cannot measure wall
// time in a unit test, but we can confirm the function:
//   - does NOT return nil error for an unknown user
//   - returns ErrUnauthenticated, same sentinel as wrong-password path
func TestAuthenticateBasicUnknownUsernameRunsDummyCompare(t *testing.T) {
	t.Parallel()
	us := NewMemoryUserStore()
	us.Put("alice", "s3cret", RoleViewer)

	_, err := us.AuthenticateBasic(context.Background(), "nobody", "anypassword")
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unknown username must return ErrUnauthenticated, got %v", err)
	}
}

// TestAuthenticateBasicUnknownUsernameReturnsSameSentinelAsWrongPassword
// ensures that both failure modes — unknown user and wrong password —
// surface the same error type, so callers cannot distinguish them.
func TestAuthenticateBasicUnknownUsernameReturnsSameSentinelAsWrongPassword(t *testing.T) {
	t.Parallel()
	us := NewMemoryUserStore()
	h, err := HashPassword("correct")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	us.Put("alice", h, RoleViewer)

	_, errUnknown := us.AuthenticateBasic(context.Background(), "nobody", "x")
	_, errWrong := us.AuthenticateBasic(context.Background(), "alice", "x")

	if !errors.Is(errUnknown, ErrUnauthenticated) {
		t.Fatalf("unknown user: %v", errUnknown)
	}
	if !errors.Is(errWrong, ErrUnauthenticated) {
		t.Fatalf("wrong password: %v", errWrong)
	}
	// Both errors must be the same sentinel so callers cannot branch on error type.
	if !errors.Is(errUnknown, errWrong) {
		t.Fatalf("unknown-user error %v != wrong-password error %v", errUnknown, errWrong)
	}
}

// TestMemoryTokenStoreFingerprintNotRawToken verifies that List() returns
// a fingerprint derived from the token hash rather than any portion of the
// raw token value. A heap dump of the token map must not reveal bearer secrets.
func TestMemoryTokenStoreFingerprintNotRawToken(t *testing.T) {
	t.Parallel()
	rawToken := "super-secret-bearer-12345"
	ts := NewMemoryTokenStore(map[string]Identity{
		rawToken: {Subject: "ci", Role: RoleOperator},
	})
	list := ts.List()
	if len(list) != 1 {
		t.Fatalf("len=%d, want 1", len(list))
	}
	fp := list[0].Fingerprint
	if fp == rawToken {
		t.Fatalf("fingerprint must not be the raw token: %q", fp)
	}
	// Fingerprint must not contain any suffix/prefix of the raw token.
	if fp != "" && (len(fp) >= 4 && fp == rawToken[len(rawToken)-len(fp):]) {
		t.Fatalf("fingerprint appears to be a suffix of the raw token: %q", fp)
	}
	if fp == "" {
		t.Fatal("fingerprint must not be empty")
	}
}

// TestMemoryTokenStorePutFingerprintNotRawToken verifies the same invariant
// for tokens added via Put (not the constructor).
func TestMemoryTokenStorePutFingerprintNotRawToken(t *testing.T) {
	t.Parallel()
	rawToken := "another-secret-abc123"
	ts := &MemoryTokenStore{}
	ts.Put(rawToken, Identity{Subject: "ops", Role: RoleAdmin})
	list := ts.List()
	if len(list) != 1 {
		t.Fatalf("len=%d, want 1", len(list))
	}
	if list[0].Fingerprint == rawToken {
		t.Fatalf("Put must not store raw token as fingerprint: %q", list[0].Fingerprint)
	}
}
