// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// testOcculiteToken is a well-formed box API token.
const testOcculiteToken = "olt_0123456789abcdef0123456789abcdef"

// serveOcculiteTokenUpgrade stands in for the lite gate plus the resolver
// for a caller that presented a box token: the gate hands the token itself
// on in the identity header, and the resolver produced a token identity.
func serveOcculiteTokenUpgrade(next http.Handler) http.Handler {
	id := auth.Identity{Subject: "occulite-token:homeassistant", Scheme: auth.SchemeOcculiteToken, Role: auth.RoleOperator}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set(auth.OcculiteSessionHeader, testOcculiteToken)
		next.ServeHTTP(w, r.WithContext(auth.ContextWithIdentity(r.Context(), id)))
	})
}

// TestOcculiteTokenSocketClosedOnceTheBoxStopsVouching: a box-token identity
// carries no expiry either, so a token revoked on the box must end the
// socket it opened — asked about with the token and the socket's role, and
// logged without the token.
func TestOcculiteTokenSocketClosedOnceTheBoxStopsVouching(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	rv := &fakeRevalidator{}
	rv.holds.Store(true)
	logs := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	server := httptest.NewServer(serveOcculiteTokenUpgrade(Handler(hub, logger, nil,
		WithOcculiteRevalidate(rv.revalidate), withOcculiteRevalidateInterval(testRevalidateInterval))))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)
	waitForAsked(t, rv, 2)
	for _, cred := range rv.calls() {
		if cred != testOcculiteToken {
			t.Fatalf("revalidator asked about %q, want the upgrade's token", cred)
		}
	}
	for _, role := range rv.seenRoles() {
		if role != auth.RoleOperator {
			t.Fatalf("revalidator handed role %q, want the socket's operator", role)
		}
	}

	rv.holds.Store(false)
	expectConnectionClosed(t, c)
	waitForClientCount(t, hub, 0)

	out := logs.String()
	if !strings.Contains(out, "ws.credential.revoked") {
		t.Fatalf("no revocation log line; logs:\n%s", out)
	}
	if strings.Contains(out, testOcculiteToken) {
		t.Fatalf("a log line carries the token:\n%s", out)
	}
}
