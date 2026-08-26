// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

func newAuthSessionStore(t *testing.T) *AuthSessionStore {
	t.Helper()
	return NewAuthSessionStore(openTestDB(t, "auth_sessions.db"))
}

func makeSession(id string, created, expires time.Time) *auth.Session {
	return &auth.Session{
		ID: id,
		Identity: auth.Identity{
			Subject: "alice",
			Scheme:  auth.SchemeSession,
			Role:    auth.RoleOperator,
			TokenID: "tok-" + id,
		},
		Created: created.UTC(),
		Expires: expires.UTC(),
	}
}

// TestAuthSessionSaveLoadRoundTrip verifies that a saved session can be
// loaded back with all Identity fields and times intact.
func TestAuthSessionSaveLoadRoundTrip(t *testing.T) {
	s := newAuthSessionStore(t)
	ctx := context.Background()

	created := time.Now().Truncate(time.Second).UTC()
	expires := created.Add(time.Hour)
	sess := makeSession("sess-roundtrip", created, expires)

	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	active, err := s.LoadActiveSessions(ctx, created.Add(-time.Second))
	if err != nil {
		t.Fatalf("LoadActiveSessions: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("got %d sessions, want 1", len(active))
	}
	got := active[0]
	if got.ID != sess.ID {
		t.Errorf("ID=%q want %q", got.ID, sess.ID)
	}
	if got.Identity.Subject != sess.Identity.Subject {
		t.Errorf("Subject=%q want %q", got.Identity.Subject, sess.Identity.Subject)
	}
	if got.Identity.Scheme != sess.Identity.Scheme {
		t.Errorf("Scheme=%q want %q", got.Identity.Scheme, sess.Identity.Scheme)
	}
	if got.Identity.Role != sess.Identity.Role {
		t.Errorf("Role=%q want %q", got.Identity.Role, sess.Identity.Role)
	}
	if got.Identity.TokenID != sess.Identity.TokenID {
		t.Errorf("TokenID=%q want %q", got.Identity.TokenID, sess.Identity.TokenID)
	}
	if got.Created.Unix() != sess.Created.Unix() {
		t.Errorf("Created=%v want %v", got.Created, sess.Created)
	}
	if got.Expires.Unix() != sess.Expires.Unix() {
		t.Errorf("Expires=%v want %v", got.Expires, sess.Expires)
	}
}

// TestAuthSessionLoadExcludesExpired verifies that LoadActiveSessions
// omits sessions whose expiry is before the supplied now, while still
// returning sessions that have not yet expired.
func TestAuthSessionLoadExcludesExpired(t *testing.T) {
	s := newAuthSessionStore(t)
	ctx := context.Background()

	base := time.Now().Truncate(time.Second).UTC()

	expired := makeSession("sess-expired", base.Add(-2*time.Hour), base.Add(-time.Hour))
	active := makeSession("sess-active", base.Add(-time.Hour), base.Add(time.Hour))
	active.Identity.Subject = "bob"

	for _, sess := range []*auth.Session{expired, active} {
		if err := s.SaveSession(ctx, sess); err != nil {
			t.Fatalf("SaveSession %s: %v", sess.ID, err)
		}
	}

	got, err := s.LoadActiveSessions(ctx, base)
	if err != nil {
		t.Fatalf("LoadActiveSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d sessions, want 1", len(got))
	}
	if got[0].ID != active.ID {
		t.Errorf("loaded ID=%q want %q", got[0].ID, active.ID)
	}
}

// TestAuthSessionDeleteRemoves verifies that DeleteSession causes the
// deleted session to be absent from subsequent LoadActiveSessions.
func TestAuthSessionDeleteRemoves(t *testing.T) {
	s := newAuthSessionStore(t)
	ctx := context.Background()

	base := time.Now().Truncate(time.Second).UTC()
	sess := makeSession("sess-delete", base, base.Add(time.Hour))

	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := s.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	got, err := s.LoadActiveSessions(ctx, base.Add(-time.Second))
	if err != nil {
		t.Fatalf("LoadActiveSessions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d sessions after delete, want 0", len(got))
	}
}

// TestAuthSessionDeleteExpiredReturnsCorrectCount verifies that
// DeleteExpiredSessions deletes only expired rows and returns the
// correct count, leaving active sessions intact.
func TestAuthSessionDeleteExpiredReturnsCorrectCount(t *testing.T) {
	s := newAuthSessionStore(t)
	ctx := context.Background()

	base := time.Now().Truncate(time.Second).UTC()

	for i, id := range []string{"exp-1", "exp-2"} {
		sess := makeSession(id, base.Add(-2*time.Hour), base.Add(-time.Hour))
		sess.Identity.Subject = "exp-user"
		sess.Identity.TokenID = id
		_ = i
		if err := s.SaveSession(ctx, sess); err != nil {
			t.Fatalf("SaveSession %s: %v", id, err)
		}
	}
	active := makeSession("active-1", base.Add(-time.Hour), base.Add(time.Hour))
	if err := s.SaveSession(ctx, active); err != nil {
		t.Fatalf("SaveSession active: %v", err)
	}

	count, err := s.DeleteExpiredSessions(ctx, base)
	if err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if count != 2 {
		t.Errorf("count=%d want 2", count)
	}

	remaining, err := s.LoadActiveSessions(ctx, base.Add(-time.Second))
	if err != nil {
		t.Fatalf("LoadActiveSessions: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("remaining=%d want 1", len(remaining))
	}
	if remaining[0].ID != active.ID {
		t.Errorf("remaining ID=%q want %q", remaining[0].ID, active.ID)
	}
}

// TestAuthSessionInsertOrReplace verifies that saving the same session
// ID twice (with a different Expires) results in a single row holding
// the most recent Expires value.
func TestAuthSessionInsertOrReplace(t *testing.T) {
	s := newAuthSessionStore(t)
	ctx := context.Background()

	base := time.Now().Truncate(time.Second).UTC()
	first := makeSession("sess-replace", base, base.Add(time.Hour))
	second := makeSession("sess-replace", base, base.Add(2*time.Hour))
	second.Identity.Subject = first.Identity.Subject

	if err := s.SaveSession(ctx, first); err != nil {
		t.Fatalf("first SaveSession: %v", err)
	}
	if err := s.SaveSession(ctx, second); err != nil {
		t.Fatalf("second SaveSession: %v", err)
	}

	got, err := s.LoadActiveSessions(ctx, base.Add(-time.Second))
	if err != nil {
		t.Fatalf("LoadActiveSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d sessions, want 1 after replace", len(got))
	}
	if got[0].Expires.Unix() != second.Expires.Unix() {
		t.Errorf("Expires=%v want %v (second save)", got[0].Expires, second.Expires)
	}
}
