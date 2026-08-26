// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ccuauth_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/auth/ccuauth"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// fakeAuthenticator is a test double for [ccuauth.Authenticator].
// validateErr is returned by ValidateCredentials; levels maps usernames to
// their CCU UserLevel; levelErr is returned by UserLevel when set.
// validateCalls counts how often ValidateCredentials was invoked so tests
// can verify it was not called on the guard-rejection paths.
type fakeAuthenticator struct {
	validateErr   error
	levels        map[string]int
	levelErr      error
	validateCalls int
}

func (f *fakeAuthenticator) ValidateCredentials(_ context.Context, _, _, _ string) error {
	f.validateCalls++
	return f.validateErr
}

func (f *fakeAuthenticator) UserLevel(_ context.Context, _, username string) (int, error) {
	if f.levelErr != nil {
		return 0, f.levelErr
	}
	return f.levels[username], nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func newStore(t *testing.T, authn ccuauth.Authenticator, cfg ccuauth.Config) *ccuauth.Store {
	t.Helper()
	return ccuauth.New(authn, cfg, discardLogger())
}

func TestStore_AuthenticateBasic_AdminSuccess(t *testing.T) {
	t.Parallel()
	fake := &fakeAuthenticator{levels: map[string]int{"alice": 8}}
	s := newStore(t, fake, ccuauth.Config{})
	id, err := s.AuthenticateBasic(context.Background(), "alice", "secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Subject != "alice" {
		t.Errorf("subject = %q, want %q", id.Subject, "alice")
	}
	if id.Scheme != auth.SchemeBasic {
		t.Errorf("scheme = %q, want %q", id.Scheme, auth.SchemeBasic)
	}
	if id.Role != auth.RoleAdmin {
		t.Errorf("role = %q, want %q", id.Role, auth.RoleAdmin)
	}
}

// TestStore_AuthenticateBasic_ReportsCanonicalSubject pins that the
// identity carries the canonical subject even though the CCU is asked
// about the name the caller typed: the session, the bearer tokens and the
// per-user records the daemon keeps are all keyed on the canonical form,
// and an admin can only address the account by that form.
func TestStore_AuthenticateBasic_ReportsCanonicalSubject(t *testing.T) {
	t.Parallel()
	fake := &fakeAuthenticator{levels: map[string]int{"Alice": 8}}
	s := newStore(t, fake, ccuauth.Config{})
	id, err := s.AuthenticateBasic(context.Background(), "Alice", "secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Subject != "alice" {
		t.Errorf("subject = %q, want %q", id.Subject, "alice")
	}
	if id.Role != auth.RoleAdmin {
		t.Errorf("role = %q, want %q", id.Role, auth.RoleAdmin)
	}
}

func TestStore_AuthenticateBasic_OperatorSuccess(t *testing.T) {
	t.Parallel()
	fake := &fakeAuthenticator{levels: map[string]int{"bob": 2}}
	s := newStore(t, fake, ccuauth.Config{})
	id, err := s.AuthenticateBasic(context.Background(), "bob", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Role != auth.RoleOperator {
		t.Errorf("role = %q, want %q", id.Role, auth.RoleOperator)
	}
}

func TestStore_AuthenticateBasic_ViewerSuccess(t *testing.T) {
	t.Parallel()
	fake := &fakeAuthenticator{levels: map[string]int{"carol": 1}}
	s := newStore(t, fake, ccuauth.Config{})
	id, err := s.AuthenticateBasic(context.Background(), "carol", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Role != auth.RoleViewer {
		t.Errorf("role = %q, want %q", id.Role, auth.RoleViewer)
	}
}

func TestStore_AuthenticateBasic_LevelZeroDenied(t *testing.T) {
	t.Parallel()
	fake := &fakeAuthenticator{levels: map[string]int{"ghost": 0}}
	s := newStore(t, fake, ccuauth.Config{})
	_, err := s.AuthenticateBasic(context.Background(), "ghost", "pass")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestStore_AuthenticateBasic_EmptyCredentialsBypassAuthenticator(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, user, pass string
	}{
		{"empty_username", "", "pass"},
		{"empty_password", "alice", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeAuthenticator{levels: map[string]int{"alice": 8}}
			s := newStore(t, fake, ccuauth.Config{})
			_, err := s.AuthenticateBasic(context.Background(), tc.user, tc.pass)
			if !errors.Is(err, auth.ErrUnauthenticated) {
				t.Errorf("err = %v, want ErrUnauthenticated", err)
			}
			if fake.validateCalls != 0 {
				t.Errorf("ValidateCredentials called %d times, want 0", fake.validateCalls)
			}
		})
	}
}

func TestStore_AuthenticateBasic_InvalidUsernamePreventsAuthenticatorCall(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, user string
	}{
		{"semicolon_injection", `admin"; x`},
		{"space_in_name", "a b"},
		{"path_traversal", "../x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeAuthenticator{}
			s := newStore(t, fake, ccuauth.Config{})
			_, err := s.AuthenticateBasic(context.Background(), tc.user, "pass")
			if !errors.Is(err, auth.ErrUnauthenticated) {
				t.Errorf("err = %v, want ErrUnauthenticated", err)
			}
			if fake.validateCalls != 0 {
				t.Errorf("ValidateCredentials called %d times, want 0", fake.validateCalls)
			}
		})
	}
}

func TestStore_AuthenticateBasic_AuthFailureError(t *testing.T) {
	t.Parallel()
	fake := &fakeAuthenticator{validateErr: hmerr.ErrAuthFailure}
	s := newStore(t, fake, ccuauth.Config{})
	_, err := s.AuthenticateBasic(context.Background(), "alice", "wrong")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestStore_AuthenticateBasic_TransientValidationError(t *testing.T) {
	t.Parallel()
	fake := &fakeAuthenticator{validateErr: errors.New("ccu down")}
	s := newStore(t, fake, ccuauth.Config{})
	_, err := s.AuthenticateBasic(context.Background(), "alice", "pass")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestStore_AuthenticateBasic_UserLevelLookupError(t *testing.T) {
	t.Parallel()
	fake := &fakeAuthenticator{
		levelErr: errors.New("rega timeout"),
	}
	s := newStore(t, fake, ccuauth.Config{})
	_, err := s.AuthenticateBasic(context.Background(), "alice", "pass")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestStore_AuthenticateBasic_MinUserLevelEnforced(t *testing.T) {
	t.Parallel()
	fake := &fakeAuthenticator{levels: map[string]int{"low": 1, "mid": 2}}
	s := newStore(t, fake, ccuauth.Config{MinUserLevel: 2})

	_, err := s.AuthenticateBasic(context.Background(), "low", "pass")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("level-1 with min=2: err = %v, want ErrUnauthenticated", err)
	}

	id, err := s.AuthenticateBasic(context.Background(), "mid", "pass")
	if err != nil {
		t.Fatalf("level-2 with min=2: unexpected error: %v", err)
	}
	if id.Role != auth.RoleOperator {
		t.Errorf("level-2 with min=2: role = %q, want %q", id.Role, auth.RoleOperator)
	}
}

func TestStore_AuthenticateBasic_RoleMappingOverride(t *testing.T) {
	t.Parallel()
	fake := &fakeAuthenticator{levels: map[string]int{"dave": 2}}
	s := newStore(t, fake, ccuauth.Config{
		RoleMapping: map[int]auth.Role{2: auth.RoleViewer},
	})
	id, err := s.AuthenticateBasic(context.Background(), "dave", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Role != auth.RoleViewer {
		t.Errorf("role = %q, want %q (override)", id.Role, auth.RoleViewer)
	}
}
