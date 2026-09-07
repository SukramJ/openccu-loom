// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package auth

import (
	"net/http/httptest"
	"testing"
	"time"
)

// TestHydratedSessionSurvivesRestartWithIdleTimeout pins that a
// persisted session is not evicted on its first post-restart lookup
// just because it was issued longer ago than the idle window. The
// idle clock measures inactivity, and a restart observes none — the
// absolute Expires window is what bounds a hydrated session.
func TestHydratedSessionSurvivesRestartWithIdleTimeout(t *testing.T) {
	fake := newFakePersist()
	now := time.Now()
	preloaded := &Session{
		ID:       "hydrated-idle",
		Identity: Identity{Subject: "carol", Role: RoleAdmin},
		Created:  now.Add(-time.Hour),
		Expires:  now.Add(time.Hour),
	}
	fake.preloaded = []*Session{preloaded}

	store, err := NewPersistentSessionStoreWithOptions(fake, discardLogger(), SessionStoreOptions{IdleTTL: 30 * time.Minute})
	if err != nil {
		t.Fatalf("NewPersistentSessionStore: %v", err)
	}
	store.now = func() time.Time { return now }

	if got := store.Lookup(preloaded.ID); got == nil {
		t.Fatal("Lookup evicted a hydrated session that was merely older than the idle window")
	}
	// The idle clock still bites on real inactivity after the restart.
	store.now = func() time.Time { return now.Add(31 * time.Minute) }
	if got := store.Lookup(preloaded.ID); got != nil {
		t.Fatal("Lookup kept a session idle past IdleTTL after hydration")
	}
}

// TestClearSessionCookieIsAcceptedOverPlainHTTP pins that the logout
// cookie can actually be cleared on a plain-HTTP deployment: browsers
// ignore a Set-Cookie carrying Secure when it arrives from an
// insecure origin, so a hard-coded Secure left the revoked cookie in
// the browser (RFC 6265bis "Leave Secure Cookies Alone").
func TestClearSessionCookieIsAcceptedOverPlainHTTP(t *testing.T) {
	rr := httptest.NewRecorder()
	ClearSessionCookie(rr)
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Set-Cookie count = %d, want 1", len(cookies))
	}
	if cookies[0].Secure {
		t.Fatal("ClearSessionCookie sets Secure, so a plain-HTTP origin cannot clear the cookie")
	}
	if cookies[0].MaxAge != -1 {
		t.Fatalf("MaxAge = %d, want -1", cookies[0].MaxAge)
	}
}
