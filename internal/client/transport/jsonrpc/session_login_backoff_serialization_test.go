// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package jsonrpc

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestLoginBackoffDoesNotSerializeConcurrentCalls reproduces the defect
// where an active login backoff stalled every JSON-RPC call on the
// central, not just the login: Call always routes through loginOrRenew,
// which serializes on sessionLoginMu before calling Login, and Login used
// to sleep out the backoff while holding that lock. A caller arriving
// during the backoff must fail fast on the pending login instead of
// queueing behind that sleep — the schedule between actual login attempts
// stays the same, only who waits for it changes.
func TestLoginBackoffDoesNotSerializeConcurrentCalls(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(envelope) any {
			return response{Error: &wireError{Code: 1, Message: "invalid credentials or too many sessions"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "wrong"})

	// One real rejected login primes the backoff (currentBackoff and
	// nextLoginAttempt) exactly the way a rotated CCU password would.
	if err := c.Login(context.Background()); err == nil {
		t.Fatal("expected the first login to be rejected")
	}
	c.mu.Lock()
	backoff := c.currentBackoff
	c.mu.Unlock()
	if backoff <= 0 {
		t.Fatalf("expected a positive backoff after one rejected login, got %v", backoff)
	}

	const callers = 5
	var wg sync.WaitGroup
	start := time.Now()
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Call(context.Background(), "System.getVersion", nil, nil)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	// Every caller must fail fast on the pending backoff. Before the fix,
	// each of the 5 callers serialized on sessionLoginMu and separately
	// slept out (and re-doubled) the backoff, so the total wall time grew
	// far past a single backoff interval.
	if elapsed >= backoff {
		t.Fatalf("%d concurrent Call()s during an active login backoff took %v, want well under the %v backoff — callers serialized behind Login's sleep instead of failing fast", callers, elapsed, backoff)
	}
}
