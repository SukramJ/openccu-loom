// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"context"
	"testing"
	"time"
)

// TestVerifiedBasicCacheSkipsTheRepeatVerification is the point of the
// cache: a client that presents Basic credentials on every request must not
// pay the key derivation every time.
func TestVerifiedBasicCacheSkipsTheRepeatVerification(t *testing.T) {
	t.Parallel()
	c := NewVerifiedBasicCache()
	calls := 0
	verify := func() bool { calls++; return true }
	for range 5 {
		if !c.Verify("alice", "$2a$12$storedhash", "secret", verify) {
			t.Fatal("cached verification reported failure")
		}
	}
	if calls != 1 {
		t.Fatalf("verifications = %d, want 1", calls)
	}
}

// TestVerifiedBasicCacheInvalidatesOnStoredHashChange is why the cache needs
// no invalidation hook on the credential-change paths: the stored hash is
// part of the key, so a password change makes every entry derived from the
// old record unreachable and the old password faces a fresh verification.
func TestVerifiedBasicCacheInvalidatesOnStoredHashChange(t *testing.T) {
	t.Parallel()
	c := NewVerifiedBasicCache()
	calls := 0
	if !c.Verify("alice", "$2a$12$oldhash", "old-password", func() bool { calls++; return true }) {
		t.Fatal("first verification reported failure")
	}
	// The record was rewritten; the old password must not be admitted from
	// the cache, and the fresh verification refuses it.
	if c.Verify("alice", "$2a$12$newhash", "old-password", func() bool { calls++; return false }) {
		t.Fatal("the old password was admitted after the record changed")
	}
	if calls != 2 {
		t.Fatalf("verifications = %d, want 2 — the changed record must not hit the cache", calls)
	}
}

// TestVerifiedBasicCacheNeverRemembersAFailure keeps a wrong password out of
// the table, so the entries stay bounded by the credentials actually in use
// rather than by what an unauthenticated caller sends.
func TestVerifiedBasicCacheNeverRemembersAFailure(t *testing.T) {
	t.Parallel()
	c := NewVerifiedBasicCache()
	calls := 0
	for range 3 {
		if c.Verify("alice", "$2a$12$storedhash", "wrong", func() bool { calls++; return false }) {
			t.Fatal("a failed verification was reported as success")
		}
	}
	if calls != 3 {
		t.Fatalf("verifications = %d, want one per attempt", calls)
	}
	if len(c.seen) != 0 {
		t.Fatalf("cache holds %d entries after only failures", len(c.seen))
	}
}

// TestVerifiedBasicCacheDistinguishesSubjectsAndPasswords guards the key
// derivation itself: two different credentials must never collapse onto one
// entry.
func TestVerifiedBasicCacheDistinguishesSubjectsAndPasswords(t *testing.T) {
	t.Parallel()
	c := NewVerifiedBasicCache()
	calls := 0
	verify := func() bool { calls++; return true }
	c.Verify("alice", "$2a$12$h", "secret", verify)
	c.Verify("bob", "$2a$12$h", "secret", verify)
	c.Verify("alice", "$2a$12$h", "other", verify)
	// The concatenation must not be re-splittable into a different triple.
	c.Verify("ali", "ce$2a$12$h", "secret", verify)
	if calls != 4 {
		t.Fatalf("verifications = %d, want one per distinct credential", calls)
	}
}

// TestVerifiedBasicCacheExpires pins that an entry stops being served once
// its window elapses.
func TestVerifiedBasicCacheExpires(t *testing.T) {
	t.Parallel()
	now := time.Now()
	c := NewVerifiedBasicCache()
	c.ttl = time.Minute
	c.now = func() time.Time { return now }
	calls := 0
	verify := func() bool { calls++; return true }
	c.Verify("alice", "$2a$12$h", "secret", verify)
	now = now.Add(2 * time.Minute)
	c.Verify("alice", "$2a$12$h", "secret", verify)
	if calls != 2 {
		t.Fatalf("verifications = %d, want the expired entry to be re-verified", calls)
	}
}

// TestVerifiedBasicCacheStaysBounded pins the ceiling: only successful
// verifications are remembered, but the table must not grow without limit
// even so.
func TestVerifiedBasicCacheStaysBounded(t *testing.T) {
	t.Parallel()
	c := NewVerifiedBasicCache()
	for i := range c.max * 2 {
		c.Verify(string(rune('a'+i%26))+string(rune(i)), "$2a$12$h", "secret", func() bool { return true })
	}
	if len(c.seen) > c.max {
		t.Fatalf("cache holds %d entries, cap is %d", len(c.seen), c.max)
	}
}

// TestNilVerifiedBasicCacheAlwaysVerifies keeps the degraded path honest: a
// store constructed without a cache must still check the password.
func TestNilVerifiedBasicCacheAlwaysVerifies(t *testing.T) {
	t.Parallel()
	var c *VerifiedBasicCache
	calls := 0
	verify := func() bool { calls++; return true }
	c.Verify("alice", "$2a$12$h", "secret", verify)
	c.Verify("alice", "$2a$12$h", "secret", verify)
	if calls != 2 {
		t.Fatalf("verifications = %d, want one per call without a cache", calls)
	}
}

// TestMemoryUserStoreCachesTheVerdictButNotTheRecord pins the store-level
// behaviour: repeat authentications skip the key derivation, while the role
// keeps coming from the live record and a changed password is refused at
// once rather than after the cache window.
func TestMemoryUserStoreCachesTheVerdictButNotTheRecord(t *testing.T) {
	t.Parallel()
	store := NewMemoryUserStore()
	hashed, err := HashPassword("correct-horse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store.Put("alice", hashed, RoleViewer)
	ctx := context.Background()
	if _, err := store.AuthenticateBasic(ctx, "alice", "correct-horse"); err != nil {
		t.Fatalf("first authentication: %v", err)
	}
	// The role is read from the record, never from the cache.
	store.Put("alice", hashed, RoleAdmin)
	id, err := store.AuthenticateBasic(ctx, "alice", "correct-horse")
	if err != nil {
		t.Fatalf("second authentication: %v", err)
	}
	if id.Role != RoleAdmin {
		t.Fatalf("Role=%q, want the live record's admin", id.Role)
	}
	// A password change takes effect immediately: the new record's hash is
	// part of the key, so the old password cannot be served from the cache.
	rotated, err := HashPassword("new-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store.Put("alice", rotated, RoleAdmin)
	if _, err := store.AuthenticateBasic(ctx, "alice", "correct-horse"); err == nil {
		t.Fatal("the previous password still authenticates after the password changed")
	}
}
