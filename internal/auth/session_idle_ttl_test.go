// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package auth

import (
	"testing"
	"time"
)

// TestSessionStoreIdleTTLEvictsIdleSession pins the documented security
// control: a session unused for longer than IdleTTL is gone on its next
// lookup, even though its absolute TTL is still running.
func TestSessionStoreIdleTTLEvictsIdleSession(t *testing.T) {
	store := NewSessionStoreWithOptions(SessionStoreOptions{IdleTTL: 15 * time.Minute})
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	sess, err := store.Issue(Identity{Subject: "alice"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	now = now.Add(10 * time.Minute)
	if store.Lookup(sess.ID) == nil {
		t.Fatal("session evicted while inside the idle window")
	}
	// The successful lookup above refreshed the idle clock, so the eviction
	// below is genuinely about inactivity, not about elapsed absolute time.
	now = now.Add(16 * time.Minute)
	if got := store.Lookup(sess.ID); got != nil {
		t.Fatal("session survived idle beyond IdleTTL")
	}
	if store.Lookup(sess.ID) != nil {
		t.Fatal("evicted session still present on a second lookup")
	}
}

// TestSessionStoreZeroIdleTTLKeepsSession is the negative control: with the
// idle check disabled (the default), the same inactivity must NOT evict.
func TestSessionStoreZeroIdleTTLKeepsSession(t *testing.T) {
	store := NewSessionStoreWithOptions(SessionStoreOptions{})
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	sess, err := store.Issue(Identity{Subject: "alice"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	now = now.Add(6 * time.Hour) // idle, but inside the 12 h absolute TTL
	if store.Lookup(sess.ID) == nil {
		t.Fatal("session evicted with the idle check disabled")
	}
}
