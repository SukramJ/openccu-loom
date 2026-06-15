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
