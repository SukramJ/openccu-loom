// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

// auth_subject_canonicalisation_test.go — one naming authority for subjects
//
// Every login path mints an [auth.Identity] whose Subject becomes the key
// that sessions, bearer tokens, per-user preferences and the audit trail
// are filed under. Those consumers are addressed by the admin surface with
// the canonical spelling, so a store that echoes the casing the caller
// happened to type splits one principal into two: a revocation triggered
// by a password reset, a role change or an account deletion then evicts
// nothing while the stale credential keeps working.

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/auth/ccuauth"
	"github.com/SukramJ/openccu-loom/internal/auth/oidc"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// ccuAuthenticatorStub answers every validation positively and reports a
// fixed user level, so the store under test reaches its identity-building
// tail. It records the username it was handed to prove the CCU-side calls
// still carry the caller's own spelling — the CCU owns that namespace.
type ccuAuthenticatorStub struct {
	level    int
	seenUser []string
}

func (s *ccuAuthenticatorStub) ValidateCredentials(_ context.Context, _, username, _ string) error {
	s.seenUser = append(s.seenUser, username)
	return nil
}

func (s *ccuAuthenticatorStub) UserLevel(_ context.Context, _, username string) (int, error) {
	s.seenUser = append(s.seenUser, username)
	return s.level, nil
}

// TestEveryUserStoreReportsCanonicalSubject pins that every
// [auth.UserStore] in the daemon reports the canonical subject regardless
// of the casing the caller typed.
func TestEveryUserStoreReportsCanonicalSubject(t *testing.T) {
	ctx := context.Background()

	memory := auth.NewMemoryUserStore()
	memory.Put("Frank", "s3cr3t", auth.RoleAdmin)

	db, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	persistent := sqlite.NewUserStore(db)
	if err := persistent.Put(ctx, "Frank", "s3cr3t", auth.RoleAdmin); err != nil {
		t.Fatalf("put user: %v", err)
	}

	ccu := ccuauth.New(&ccuAuthenticatorStub{level: 8}, ccuauth.Config{Central: "ccu"},
		slog.New(slog.DiscardHandler))

	cases := []struct {
		name  string
		store auth.UserStore
		// typed spellings this store accepts; the CCU store rejects
		// padding up front to keep its ReGa lookup injection-safe.
		spellings []string
	}{
		{"memory", memory, []string{"Frank", "FRANK", "fRaNk", " frank "}},
		{"sqlite", persistent, []string{"Frank", "FRANK", "fRaNk", " frank "}},
		{"ccu", ccu, []string{"Frank", "FRANK", "fRaNk"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, typed := range tc.spellings {
				id, err := tc.store.AuthenticateBasic(ctx, typed, "s3cr3t")
				if err != nil {
					t.Fatalf("AuthenticateBasic(%q): %v", typed, err)
				}
				if id.Subject != "frank" {
					t.Errorf("AuthenticateBasic(%q) subject = %q, want %q",
						typed, id.Subject, "frank")
				}
			}
		})
	}
}

// TestOIDCLoginReportsCanonicalSubject pins the remaining login path. The
// external provider names the principal with "preferred_username", a claim
// OpenID Connect Core §5.1 declares neither stable nor unique — directories
// hand back whatever casing they hold, so one person signing in twice would
// otherwise be filed as two. The "sub" fallback is exempt on purpose: it is
// opaque and case-sensitive (§2), so folding it could merge two principals
// that differ in casing alone.
func TestOIDCLoginReportsCanonicalSubject(t *testing.T) {
	client := &oidc.Client{}

	for _, typed := range []string{"Frank", "FRANK", "fRaNk", " frank "} {
		id := client.IdentityFrom(&oidc.IDClaims{PreferredUser: typed})
		if id.Subject != "frank" {
			t.Errorf("IdentityFrom(preferred_username=%q) subject = %q, want %q",
				typed, id.Subject, "frank")
		}
		if id.Scheme != auth.SchemeOIDC {
			t.Errorf("IdentityFrom(preferred_username=%q) scheme = %q, want %q",
				typed, id.Scheme, auth.SchemeOIDC)
		}
	}

	if id := client.IdentityFrom(&oidc.IDClaims{Subject: "AbC123"}); id.Subject != "AbC123" {
		t.Errorf("IdentityFrom(sub=%q) subject = %q, want it verbatim", "AbC123", id.Subject)
	}
}

// TestCCUUserStoreQueriesTheCCUWithTheTypedSpelling guards the other half
// of the canonicalisation: the CCU's user database is its own namespace,
// so folding the subject for Loom-side bookkeeping must not change the
// name the credential validation and the user-level lookup ask about.
func TestCCUUserStoreQueriesTheCCUWithTheTypedSpelling(t *testing.T) {
	stub := &ccuAuthenticatorStub{level: 8}
	store := ccuauth.New(stub, ccuauth.Config{Central: "ccu"}, slog.New(slog.DiscardHandler))

	if _, err := store.AuthenticateBasic(context.Background(), "Frank", "s3cr3t"); err != nil {
		t.Fatalf("AuthenticateBasic: %v", err)
	}
	if len(stub.seenUser) == 0 {
		t.Fatal("authenticator was never called")
	}
	for _, seen := range stub.seenUser {
		if seen != "Frank" {
			t.Errorf("CCU call carried username %q, want %q", seen, "Frank")
		}
	}
}
