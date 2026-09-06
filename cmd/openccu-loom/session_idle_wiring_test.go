// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// TestBuildSessionStoreCarriesTheConfiguredIdleTimeout pins the one place
// `north.rest.auth.session_idle_timeout` reaches the session store. The
// store's idle eviction existed as a documented control with no production
// assignment — a stolen-but-idle cookie stayed usable for the full absolute
// TTL, which is exactly what the field's comment claimed to cap. The pin
// asserts the effect: a session left idle beyond the configured timeout is
// gone on the next lookup, and with the timeout at zero it is not.
func TestBuildSessionStoreCarriesTheConfiguredIdleTimeout(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)

	idle := buildSessionStore(nil, logger, auth.SessionStoreOptions{IdleTTL: 30 * time.Millisecond})
	sess, err := idle.Issue(auth.Identity{Subject: "operator", Role: auth.RoleAdmin})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if idle.Lookup(sess.ID) == nil {
		t.Fatal("a fresh session must resolve")
	}
	time.Sleep(60 * time.Millisecond)
	if idle.Lookup(sess.ID) != nil {
		t.Fatal("a session idle beyond the configured timeout still resolved; the configured value did not reach the store")
	}

	never := buildSessionStore(nil, logger, auth.SessionStoreOptions{})
	sess, err = never.Issue(auth.Identity{Subject: "operator", Role: auth.RoleAdmin})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if never.Lookup(sess.ID) == nil {
		t.Fatal("with session_idle_timeout unset a session must not be evicted for idling")
	}
}
