// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"context"
	"testing"
)

// TestCircuitBypassesSessionLogin verifies that Session.login executes
// even when the circuit breaker is OPEN.
func TestCircuitBypassesSessionLogin(t *testing.T) {
	t.Parallel()
	cb := newOpenCircuit(t)

	called := false
	err := cb.Do(context.Background(), "Session.login", func(_ context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Session.login should bypass: got err %v", err)
	}
	if !called {
		t.Fatal("Session.login handler was not called")
	}
}

// TestCircuitBypassesSessionLogout verifies that Session.logout executes
// even when the circuit breaker is OPEN.
func TestCircuitBypassesSessionLogout(t *testing.T) {
	t.Parallel()
	cb := newOpenCircuit(t)

	called := false
	err := cb.Do(context.Background(), "Session.logout", func(_ context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Session.logout should bypass: got err %v", err)
	}
	if !called {
		t.Fatal("Session.logout handler was not called")
	}
}

// TestCircuitBypassesSessionRenew verifies that Session.renew executes
// even when the circuit breaker is OPEN.
func TestCircuitBypassesSessionRenew(t *testing.T) {
	t.Parallel()
	cb := newOpenCircuit(t)

	called := false
	err := cb.Do(context.Background(), "Session.renew", func(_ context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Session.renew should bypass: got err %v", err)
	}
	if !called {
		t.Fatal("Session.renew handler was not called")
	}
}
