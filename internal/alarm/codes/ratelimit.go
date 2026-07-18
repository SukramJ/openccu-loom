// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package codes

import (
	"sync"
	"time"
)

// Rate-limit parameters (docs/alarm-concept.md §11 / slice-6 design):
// five wrong attempts are tolerated before a source is locked out; the
// lockout duration starts at rateLimitBaseLockout and doubles on every
// further violation, capped at rateLimitMaxLockout. State is held
// in-memory only — a daemon restart resets every source's counters,
// which is an accepted, documented trade-off (no persisted brute-force
// ledger).
const (
	rateLimitMaxAttempts  = 5
	rateLimitBaseLockout  = 60 * time.Second
	rateLimitMaxLockout   = 15 * time.Minute
	rateLimitMaxDoublings = 8 // 60s * 2^8 already exceeds the 15 min cap
)

// rateLimiter tracks wrong-code attempts per source key (e.g. "mqtt",
// "keypad:<serial>", an operator-session source string). It is safe
// for concurrent use.
type rateLimiter struct {
	mu    sync.Mutex
	state map[string]*rateLimitEntry
}

// rateLimitEntry is one source's failure/lockout bookkeeping.
type rateLimitEntry struct {
	fails       int
	lockouts    int
	lockedUntil time.Time
}

// newRateLimiter returns an empty limiter.
func newRateLimiter() *rateLimiter {
	return &rateLimiter{state: map[string]*rateLimitEntry{}}
}

// allow reports whether source may attempt a code check right now. When
// locked out it also returns the remaining lockout duration.
func (l *rateLimiter) allow(source string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.state[source]
	if e == nil || !now.Before(e.lockedUntil) {
		return true, 0
	}
	return false, e.lockedUntil.Sub(now)
}

// recordFailure registers a wrong attempt for source. It returns the
// lockout duration just engaged, or 0 if the source is still under the
// attempt threshold.
func (l *rateLimiter) recordFailure(source string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.state[source]
	if e == nil {
		e = &rateLimitEntry{}
		l.state[source] = e
	}
	e.fails++
	if e.fails < rateLimitMaxAttempts {
		return 0
	}
	d := lockoutDuration(e.lockouts)
	e.lockouts++
	e.fails = 0
	e.lockedUntil = now.Add(d)
	return d
}

// recordSuccess clears source's failure/lockout state after a code
// authenticates.
func (l *rateLimiter) recordSuccess(source string) {
	l.mu.Lock()
	delete(l.state, source)
	l.mu.Unlock()
}

// lockoutDuration computes the exponentially growing lockout for the
// step-th lockout of a source (0-based), capped at rateLimitMaxLockout.
func lockoutDuration(step int) time.Duration {
	if step > rateLimitMaxDoublings {
		step = rateLimitMaxDoublings
	}
	d := rateLimitBaseLockout << uint(step) //nolint:gosec // G115: step is bounded above
	if d > rateLimitMaxLockout {
		d = rateLimitMaxLockout
	}
	return d
}
