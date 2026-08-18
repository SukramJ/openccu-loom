// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"sync"
	"time"
)

// verifiedBasicTTL bounds how long one successful password verification is
// remembered. The entry is self-invalidating (see [VerifiedBasicCache]), so
// the window is about how long derived material stays resident, not about
// staleness.
const verifiedBasicTTL = time.Minute

// verifiedBasicMax caps the number of remembered verifications. Only a
// verification that SUCCEEDED is remembered, so the table is bounded by the
// number of credentials in use, never by what an unauthenticated caller
// sends. The cap is the backstop.
const verifiedBasicMax = 512

// VerifiedBasicCache remembers recent successful password verifications so a
// client that presents HTTP Basic credentials on every request pays the
// key-derivation cost once per window instead of once per request. The KDF is
// deliberately expensive — hundreds of milliseconds of CPU per compare — which
// otherwise makes every REST call under Basic two to three orders of magnitude
// slower than the same call under a bearer token, and makes the API's
// throughput a function of a password hash rather than of the work requested.
//
// The entry key binds the stored password hash, so the cache invalidates
// itself rather than needing an invalidation hook on every credential-change
// path: a password change rewrites the record's hash, the key changes with it,
// and the old password misses and then fails the fresh verification. A deleted
// account has no record to look up at all, and the role is read from the live
// record on every request — only the verdict is cached, never the identity.
//
// Keys are HMAC-SHA256 under a process-local random key, so neither the
// password nor the stored hash can be recovered from a heap dump of the map.
// The unknown-user path is deliberately never cached: it must keep costing a
// full compare so response latency does not reveal whether a subject exists.
// loom:reachable:reason="the field type of sqlite.UserStore.verified and the return type of NewVerifiedBasicCache, which NewUserStore calls on construction; the analyzer counts a type as reached only through its own methods and cannot see one held as a struct field"
type VerifiedBasicCache struct {
	ttl time.Duration
	max int
	key []byte

	mu   sync.Mutex
	seen map[[sha256.Size]byte]time.Time
	now  func() time.Time
}

// NewVerifiedBasicCache returns a cache with the package defaults. It returns
// nil when the process cannot produce a random key — a nil cache verifies
// every time, which is the behaviour without a cache at all.
func NewVerifiedBasicCache() *VerifiedBasicCache {
	key := make([]byte, sha256.Size)
	if _, err := rand.Read(key); err != nil {
		return nil
	}
	return &VerifiedBasicCache{
		ttl:  verifiedBasicTTL,
		max:  verifiedBasicMax,
		key:  key,
		seen: make(map[[sha256.Size]byte]time.Time, 8),
		now:  time.Now,
	}
}

// Verify reports whether password matches storedHash for subject. It consults
// the cache first and calls verify — which must be the full key-derivation
// compare — only on a miss, remembering a positive result. A nil cache always
// calls verify.
func (c *VerifiedBasicCache) Verify(subject, storedHash, password string, verify func() bool) bool {
	if verify == nil {
		return false
	}
	if c == nil {
		return verify()
	}
	key := c.entryKey(subject, storedHash, password)
	now := c.now()
	c.mu.Lock()
	expires, hit := c.seen[key]
	c.mu.Unlock()
	if hit && now.Before(expires) {
		return true
	}
	if !verify() {
		return false
	}
	c.remember(key, now)
	return true
}

// entryKey derives the cache key. The stored hash is part of it, which is what
// makes a record change invalidate every entry derived from it.
func (c *VerifiedBasicCache) entryKey(subject, storedHash, password string) [sha256.Size]byte {
	mac := hmac.New(sha256.New, c.key)
	// The separator keeps the concatenation unambiguous: without it a
	// subject/hash pair could be re-split so that a different pair produces
	// the same input.
	for _, part := range []string{subject, storedHash, password} {
		_, _ = mac.Write([]byte(part))
		_, _ = mac.Write([]byte{0})
	}
	var out [sha256.Size]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// remember stores key with a fresh deadline, sweeping expired entries and —
// if the table is still at its cap — dropping it wholesale rather than
// growing. A cleared cache costs one extra verification per credential, which
// is exactly the behaviour without a cache.
func (c *VerifiedBasicCache) remember(key [sha256.Size]byte, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.seen) >= c.max {
		for k, exp := range c.seen {
			if !now.Before(exp) {
				delete(c.seen, k)
			}
		}
		if len(c.seen) >= c.max {
			clear(c.seen)
		}
	}
	c.seen[key] = now.Add(c.ttl)
}
