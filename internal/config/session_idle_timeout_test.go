// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import (
	"strings"
	"testing"
	"time"
)

// TestSessionIdleTimeoutRoundTrip pins that north.rest.auth.session_idle_timeout
// survives YAML ingestion, and that the default is zero — the historical
// behaviour, where only the absolute session lifetime bounds a login.
func TestSessionIdleTimeoutRoundTrip(t *testing.T) {
	if got := Default().North.REST.Auth.SessionIdleTimeout; got != 0 {
		t.Fatalf("default session_idle_timeout = %s, want 0 (idle check disabled)", got)
	}
	cfg, err := Parse([]byte("north:\n  rest:\n    auth:\n      session_idle_timeout: 30m\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cfg.North.REST.Auth.SessionIdleTimeout; got != 30*time.Minute {
		t.Fatalf("session_idle_timeout = %s, want 30m", got)
	}
}

// TestSessionIdleTimeoutRejectsNegative keeps a negative idle window out: it
// would evict every session on its first lookup, locking the operator out of
// the surface that could fix the value.
func TestSessionIdleTimeoutRejectsNegative(t *testing.T) {
	_, err := Parse([]byte("north:\n  rest:\n    auth:\n      session_idle_timeout: -1s\n"))
	if err == nil {
		t.Fatal("Parse accepted a negative session_idle_timeout")
	}
	if !strings.Contains(err.Error(), "session_idle_timeout") {
		t.Fatalf("error must name the field, got: %v", err)
	}
}
