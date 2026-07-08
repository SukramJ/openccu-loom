// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package jsonrpc

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestLoginOrRenewColdStartBurstLogsInOnce verifies that a cold-start burst
// of concurrent callers into loginOrRenew performs exactly one Session.login
// round-trip, not one per caller. sessionLoginMu serializes the
// establish-a-session critical section, and the freshness re-check inside
// the lock lets every goroutine after the first observe the session the
// first goroutine just installed and skip its own login. Without that
// serialization, a burst like this would each race past the pre-lock
// freshness check and open N separate CCU sessions at once — which trips
// the CCU's "too many sessions" limit.
func TestLoginOrRenewColdStartBurstLogsInOnce(t *testing.T) {
	t.Parallel()

	var loginCalls atomic.Int64
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			loginCalls.Add(1)
			// Widen the race window: while this one login is in flight,
			// every other goroutine has time to pile into loginOrRenew and
			// either block on sessionLoginMu or race the pre-lock fast path.
			time.Sleep(20 * time.Millisecond)
			return okResult("session-1")
		},
	})
	defer srv.Close()

	c, err := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = c.loginOrRenew(context.Background())
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("loginOrRenew[%d]: %v", i, err)
		}
	}
	if got := c.SessionID(); got != "session-1" {
		t.Fatalf("SessionID() = %q, want %q", got, "session-1")
	}
	if got := loginCalls.Load(); got != 1 {
		t.Fatalf("Session.login called %d times, want exactly 1 — a cold-start burst of %d "+
			"concurrent loginOrRenew callers must serialize into a single login, not one per caller", got, n)
	}
}

// TestReloginLockedConcurrentAuthFailuresLogInOnce verifies that when N
// concurrent callers each observe the same stale session and call
// reloginLocked, only one of them performs the actual Session.login — the
// rest see the fresh session another goroutine already installed and reuse
// it as a no-op. This is the auth-failure retry path (a 401/403 or a
// JSON-RPC 400 "access denied"): a burst of simultaneous auth failures on
// the same stale session id must not each open their own CCU session.
func TestReloginLockedConcurrentAuthFailuresLogInOnce(t *testing.T) {
	t.Parallel()

	var totalCalls atomic.Int64      // keys the response: 1st call is the setup login, rest are "concurrent"
	var concurrentCalls atomic.Int64 // reset after setup; must land at exactly 1
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			n := totalCalls.Add(1)
			concurrentCalls.Add(1)
			// Widen the race window the same way as the cold-start test.
			time.Sleep(20 * time.Millisecond)
			if n == 1 {
				return okResult("stale-session")
			}
			return okResult("fresh-session")
		},
	})
	defer srv.Close()

	c, err := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Establish a known "stale" session id via a single setup login. The
	// handler above is keyed so this first call returns a different id
	// ("stale-session") than every subsequent call ("fresh-session"),
	// which keeps the two phases unambiguous.
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("setup Login: %v", err)
	}
	staleID := c.SessionID()
	if staleID != "stale-session" {
		t.Fatalf("setup SessionID() = %q, want %q", staleID, "stale-session")
	}
	// Only count Session.login calls from the concurrent phase below.
	concurrentCalls.Store(0)

	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = c.reloginLocked(context.Background(), staleID)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("reloginLocked[%d]: %v", i, err)
		}
	}
	if got := concurrentCalls.Load(); got != 1 {
		t.Fatalf("Session.login called %d times during the concurrent phase, want exactly 1 — %d "+
			"concurrent reloginLocked callers sharing the same stale session id must serialize into a "+
			"single relogin, the rest reusing the session the first one installed", got, n)
	}
}
