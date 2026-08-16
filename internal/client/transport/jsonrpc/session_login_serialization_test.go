// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package jsonrpc

import (
	"context"
	"fmt"
	"net/http"
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

// TestCallOnceAuthRetryInvalidatesTheSessionTheRequestUsed pins which session
// an auth failure is allowed to discard: the one the failed request carried,
// never whatever the client holds when the reply arrives.
//
// Several callers share one client, so a concurrent caller can log in again
// between send and reply. Judging the reply against the current session then
// declares that brand-new session stale — the client invalidates it and logs
// it out on the CCU, killing a live session and pushing every concurrent
// caller through its own login, which is exactly what exhausts the CCU's
// small session pool after a reboot.
//
// The interleaving is produced deterministically: the CCU handler for the
// call logs the client in again (standing in for the concurrent caller) and
// only then answers 401.
func TestCallOnceAuthRetryInvalidatesTheSessionTheRequestUsed(t *testing.T) {
	t.Parallel()

	var (
		mu           sync.Mutex
		logins       int
		loggedOut    []string
		callSessions []string
	)
	var c *Client
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(envelope) any {
			mu.Lock()
			logins++
			id := fmt.Sprintf("session-%d", logins)
			mu.Unlock()
			return okResult(id)
		},
		"Session.logout": func(env envelope) any {
			mu.Lock()
			loggedOut = append(loggedOut, sessionOf(env.Params))
			mu.Unlock()
			return okResult(true)
		},
		"Foo": func(env envelope) any {
			session := sessionOf(env.Params)
			mu.Lock()
			callSessions = append(callSessions, session)
			mu.Unlock()
			if session != "session-1" {
				return okResult("ok")
			}
			// A concurrent caller notices the dead session first and
			// establishes a new one while this request is in flight.
			if err := c.Login(context.Background()); err != nil {
				t.Errorf("concurrent relogin: %v", err)
			}
			return http.StatusUnauthorized
		},
	})
	defer srv.Close()

	var err error
	c, err = New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("setup Login: %v", err)
	}

	if err := c.Call(context.Background(), "Foo", nil, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, s := range loggedOut {
		if s == "session-2" {
			t.Fatalf("the session established by the concurrent caller was logged out on the CCU "+
				"(logouts=%v) — only the session the failed request carried may be discarded", loggedOut)
		}
	}
	if logins != 2 {
		t.Fatalf("Session.login calls = %d, want 2 (setup + the concurrent caller's) — the retry must "+
			"reuse the session that already exists, not open another one", logins)
	}
	if got := callSessions[len(callSessions)-1]; got != "session-2" {
		t.Fatalf("retried call carried session %q, want %q", got, "session-2")
	}
	if got := c.SessionID(); got != "session-2" {
		t.Fatalf("SessionID() = %q, want %q", got, "session-2")
	}
}
