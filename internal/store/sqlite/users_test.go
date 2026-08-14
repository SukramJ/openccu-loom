// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

func newUserStore(t *testing.T) *UserStore {
	t.Helper()
	return NewUserStore(openTestDB(t, "users.db"))
}

// TestUserStorePutNew verifies that Put inserts a new user row and
// List reflects it.
func TestUserStorePutNew(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, "alice", "s3cr3t", auth.RoleAdmin); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List len=%d want 1", len(rows))
	}
	if rows[0].Subject != "alice" {
		t.Errorf("Subject=%q want alice", rows[0].Subject)
	}
	if rows[0].Role != auth.RoleAdmin {
		t.Errorf("Role=%q want admin", rows[0].Role)
	}
}

// TestUserStorePutReplace verifies upsert semantics: a second Put on
// the same (lowercased) subject overwrites the role and password.
func TestUserStorePutReplace(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, "bob", "pass1", auth.RoleViewer); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	if err := s.Put(ctx, "bob", "pass2", auth.RoleAdmin); err != nil {
		t.Fatalf("second Put: %v", err)
	}

	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List len=%d want 1 (upsert must not duplicate)", len(rows))
	}
	if rows[0].Role != auth.RoleAdmin {
		t.Errorf("Role=%q want admin after replace", rows[0].Role)
	}
	// Verify the new password is effective.
	id, err := s.AuthenticateBasic(ctx, "bob", "pass2")
	if err != nil {
		t.Fatalf("AuthenticateBasic with new password: %v", err)
	}
	if id.Role != auth.RoleAdmin {
		t.Errorf("authenticated role=%q want admin", id.Role)
	}
	// Old password must no longer work.
	if _, err := s.AuthenticateBasic(ctx, "bob", "pass1"); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("old password: want ErrUnauthenticated got %v", err)
	}
}

// TestUserStorePutEmptySubject verifies that an empty / whitespace-only
// subject is rejected.
func TestUserStorePutEmptySubject(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	for _, sub := range []string{"", "  ", "\t"} {
		if err := s.Put(ctx, sub, "pw", auth.RoleViewer); err == nil {
			t.Errorf("Put(%q): expected error, got nil", sub)
		}
	}
}

// TestUserStorePutEmptyPassword verifies that an empty password is
// rejected before any bcrypt work.
func TestUserStorePutEmptyPassword(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, "carol", "", auth.RoleViewer); err == nil {
		t.Error("Put with empty password: expected error, got nil")
	}
}

// TestUserStoreAuthenticateBasicHappyPath verifies that correct
// credentials return an Identity with the expected fields.
func TestUserStoreAuthenticateBasicHappyPath(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, "dave", "hunter2", auth.RoleOperator); err != nil {
		t.Fatalf("Put: %v", err)
	}
	id, err := s.AuthenticateBasic(ctx, "dave", "hunter2")
	if err != nil {
		t.Fatalf("AuthenticateBasic: %v", err)
	}
	if id.Role != auth.RoleOperator {
		t.Errorf("Role=%q want operator", id.Role)
	}
	if id.Scheme != auth.SchemeBasic {
		t.Errorf("Scheme=%q want basic", id.Scheme)
	}
	// Subject is the original case as supplied to AuthenticateBasic,
	// not the stored lowercase form.
	if id.Subject != "dave" {
		t.Errorf("Subject=%q want dave", id.Subject)
	}
}

// TestUserStoreAuthenticateBasicWrongPassword verifies ErrUnauthenticated
// on a password mismatch.
func TestUserStoreAuthenticateBasicWrongPassword(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, "eve", "correct", auth.RoleViewer); err != nil {
		t.Fatalf("Put: %v", err)
	}
	_, err := s.AuthenticateBasic(ctx, "eve", "wrong")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("wrong password: want ErrUnauthenticated, got %v", err)
	}
}

// TestUserStoreAuthenticateBasicUnknownSubject verifies ErrUnauthenticated
// for a subject that was never inserted. AuthenticateBasic runs a dummy
// bcrypt comparison on this path before returning so the observable
// behavior — ErrUnauthenticated, same as a wrong password — is unchanged
// even though the code now spends comparable wall-clock time on both the
// unknown-user and wrong-password paths (anti user-enumeration).
func TestUserStoreAuthenticateBasicUnknownSubject(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	_, err := s.AuthenticateBasic(ctx, "ghost", "pw")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("unknown subject: want ErrUnauthenticated, got %v", err)
	}
}

// TestUserStoreAuthenticateBasicCaseInsensitive verifies that subjects
// are matched case-insensitively (Put lowercases; auth lowercases too).
func TestUserStoreAuthenticateBasicCaseInsensitive(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	// Insert with mixed case — Put will lowercase to "frank".
	if err := s.Put(ctx, "Frank", "pw", auth.RoleViewer); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Auth with different casing must succeed.
	for _, username := range []string{"frank", "Frank", "FRANK", "fRaNk"} {
		if _, err := s.AuthenticateBasic(ctx, username, "pw"); err != nil {
			t.Errorf("AuthenticateBasic(%q): %v", username, err)
		}
	}
}

// TestUserStoreAuthenticateBasicReturnsStoredSubject verifies that a
// successful login reports the canonical (stored) spelling of the
// subject, not the casing the caller happened to type. Everything that
// keys on an identity — the session map, the audit note, the revocation
// hooks behind a password or role change — compares against the stored
// spelling, so returning the caller's casing makes those lookups miss.
func TestUserStoreAuthenticateBasicReturnsStoredSubject(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, "Frank", "pw", auth.RoleViewer); err != nil {
		t.Fatalf("Put: %v", err)
	}
	for _, username := range []string{"frank", "Frank", "FRANK", " fRaNk "} {
		id, err := s.AuthenticateBasic(ctx, username, "pw")
		if err != nil {
			t.Fatalf("AuthenticateBasic(%q): %v", username, err)
		}
		if id.Subject != "frank" {
			t.Errorf("AuthenticateBasic(%q).Subject=%q want %q", username, id.Subject, "frank")
		}
	}
}

// TestUserStorePutRefusesToDemoteLastAdmin pins the lockout guard on the
// write path: demoting the only admin would leave the daemon with zero
// accounts able to reach any admin route, and no admin-only API to
// recover with. Delete already refuses this; Put must too.
func TestUserStorePutRefusesToDemoteLastAdmin(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, "onlyadmin", "pw", auth.RoleAdmin); err != nil {
		t.Fatalf("Put admin: %v", err)
	}
	if err := s.Put(ctx, "viewer", "pw", auth.RoleViewer); err != nil {
		t.Fatalf("Put viewer: %v", err)
	}

	if err := s.Put(ctx, "OnlyAdmin", "newpw", auth.RoleViewer); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("Put demoting last admin: want ErrLastAdmin, got %v", err)
	}
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, r := range rows {
		if r.Subject == "onlyadmin" && r.Role != auth.RoleAdmin {
			t.Errorf("onlyadmin role=%q want admin (write must not have landed)", r.Role)
		}
	}
	// The same account may still have its password reset while keeping
	// the admin role, and a second admin unblocks the demotion.
	if err := s.Put(ctx, "onlyadmin", "newpw", auth.RoleAdmin); err != nil {
		t.Fatalf("Put same role: %v", err)
	}
	if err := s.Put(ctx, "second", "pw", auth.RoleAdmin); err != nil {
		t.Fatalf("Put second admin: %v", err)
	}
	if err := s.Put(ctx, "onlyadmin", "newpw", auth.RoleViewer); err != nil {
		t.Fatalf("Put demoting with a second admin present: %v", err)
	}
}

// TestUserStoreSetRoleKeepsThePassword pins the operation behind a
// role-only update: the admin who moves an account between roles does not
// know its password, so the stored hash has to survive the change and the
// user must keep signing in with the credentials they already have.
func TestUserStoreSetRoleKeepsThePassword(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, "keeper", "pw", auth.RoleAdmin); err != nil {
		t.Fatalf("Put admin: %v", err)
	}
	if err := s.Put(ctx, "bob", "bobpw", auth.RoleOperator); err != nil {
		t.Fatalf("Put bob: %v", err)
	}

	// Address the account the way a path parameter arrives — the store
	// folds it, so a differently-spelled subject still hits the row.
	if err := s.SetRole(ctx, "Bob", auth.RoleViewer); err != nil {
		t.Fatalf("SetRole: %v", err)
	}

	id, err := s.AuthenticateBasic(ctx, "bob", "bobpw")
	if err != nil {
		t.Fatalf("the old password stopped working after a role change: %v", err)
	}
	if id.Role != auth.RoleViewer {
		t.Errorf("authenticated role=%q want viewer", id.Role)
	}
}

// TestUserStoreSetRoleRefusesToDemoteLastAdmin pins that the role-only
// path runs through the same lockout guard as Put: demoting the only
// admin would leave the daemon with no account able to reach an admin
// route, and no admin-only API to recover with.
func TestUserStoreSetRoleRefusesToDemoteLastAdmin(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, "onlyadmin", "pw", auth.RoleAdmin); err != nil {
		t.Fatalf("Put admin: %v", err)
	}
	if err := s.Put(ctx, "viewer", "pw", auth.RoleViewer); err != nil {
		t.Fatalf("Put viewer: %v", err)
	}

	if err := s.SetRole(ctx, "OnlyAdmin", auth.RoleViewer); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("SetRole demoting last admin: want ErrLastAdmin, got %v", err)
	}
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, r := range rows {
		if r.Subject == "onlyadmin" && r.Role != auth.RoleAdmin {
			t.Errorf("onlyadmin role=%q want admin (write must not have landed)", r.Role)
		}
	}
	// A second admin unblocks the demotion.
	if err := s.Put(ctx, "second", "pw", auth.RoleAdmin); err != nil {
		t.Fatalf("Put second admin: %v", err)
	}
	if err := s.SetRole(ctx, "onlyadmin", auth.RoleViewer); err != nil {
		t.Fatalf("SetRole with a second admin present: %v", err)
	}
}

// TestUserStoreSetRoleUnknownSubject verifies that a role change for an
// account that does not exist reports the miss instead of creating one.
func TestUserStoreSetRoleUnknownSubject(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	if err := s.SetRole(ctx, "ghost", auth.RoleViewer); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("SetRole unknown subject: want ErrUserNotFound, got %v", err)
	}
	n, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 0 {
		t.Errorf("Count=%d want 0 (a miss must not insert a row)", n)
	}
}

// TestUserStoreDeleteHappyPath verifies that Delete removes the user.
func TestUserStoreDeleteHappyPath(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	// Need two admins so deleting one is not "last admin".
	if err := s.Put(ctx, "admin1", "pw", auth.RoleAdmin); err != nil {
		t.Fatalf("Put admin1: %v", err)
	}
	if err := s.Put(ctx, "admin2", "pw", auth.RoleAdmin); err != nil {
		t.Fatalf("Put admin2: %v", err)
	}

	if err := s.Delete(ctx, "admin1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List len=%d want 1 after delete", len(rows))
	}
	if rows[0].Subject != "admin2" {
		t.Errorf("remaining subject=%q want admin2", rows[0].Subject)
	}
}

// TestUserStoreDeleteUnknown verifies ErrUserNotFound for a missing subject.
func TestUserStoreDeleteUnknown(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	err := s.Delete(ctx, "nobody")
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("Delete unknown: want ErrUserNotFound, got %v", err)
	}
}

// TestUserStoreDeleteLastAdmin verifies that deleting the sole admin
// is refused with ErrLastAdmin.
func TestUserStoreDeleteLastAdmin(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, "onlyadmin", "pw", auth.RoleAdmin); err != nil {
		t.Fatalf("Put: %v", err)
	}
	err := s.Delete(ctx, "onlyadmin")
	if !errors.Is(err, ErrLastAdmin) {
		t.Errorf("Delete last admin: want ErrLastAdmin, got %v", err)
	}
}

// TestUserStoreListSortedBySubject verifies the ORDER BY subject clause.
func TestUserStoreListSortedBySubject(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	for _, u := range []string{"zara", "alice", "mike"} {
		if err := s.Put(ctx, u, "pw", auth.RoleViewer); err != nil {
			t.Fatalf("Put %s: %v", u, err)
		}
	}
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"alice", "mike", "zara"}
	for i, w := range want {
		if rows[i].Subject != w {
			t.Errorf("rows[%d].Subject=%q want %q", i, rows[i].Subject, w)
		}
	}
}

// TestUserStoreCount verifies Count returns the right number.
func TestUserStoreCount(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	n, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("Count empty: %v", err)
	}
	if n != 0 {
		t.Fatalf("Count empty=%d want 0", n)
	}

	for _, u := range []string{"u1", "u2", "u3"} {
		if err := s.Put(ctx, u, "pw", auth.RoleViewer); err != nil {
			t.Fatalf("Put %s: %v", u, err)
		}
	}
	n, err = s.Count(ctx)
	if err != nil {
		t.Fatalf("Count after inserts: %v", err)
	}
	if n != 3 {
		t.Errorf("Count=%d want 3", n)
	}
}

// TestUserStoreLastSeenUpdatedOnAuth verifies that a successful
// AuthenticateBasic call updates last_seen_at.
func TestUserStoreLastSeenUpdatedOnAuth(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, "greta", "pw", auth.RoleViewer); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Before any auth, last_seen_at is NULL.
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List before auth: %v", err)
	}
	if rows[0].LastSeenAt != nil {
		t.Errorf("LastSeenAt before auth: want nil, got %v", rows[0].LastSeenAt)
	}

	if _, err := s.AuthenticateBasic(ctx, "greta", "pw"); err != nil {
		t.Fatalf("AuthenticateBasic: %v", err)
	}
	rows, err = s.List(ctx)
	if err != nil {
		t.Fatalf("List after auth: %v", err)
	}
	if rows[0].LastSeenAt == nil {
		t.Error("LastSeenAt after auth: want non-nil, got nil")
	}
}
