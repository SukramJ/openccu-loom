// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// serveWithIdentity mirrors the production mount: the auth chain resolves a
// credential and attaches the identity to the request context, and the
// upgrade handler copies it onto the connection.
func serveWithIdentity(id auth.Identity, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(auth.ContextWithIdentity(r.Context(), id)))
	})
}

// waitForClientCount blocks until the hub holds want connections.
func waitForClientCount(t *testing.T, hub *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.ClientCount() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("hub holds %d connections, want %d", hub.ClientCount(), want)
}

// expectConnectionClosed asserts the server tore the socket down: the next
// read must fail rather than block.
func expectConnectionClosed(t *testing.T, c *wsConn) {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	for {
		// A frame already queued when the close landed (a ping, a pending
		// result) can still arrive, so read until the socket itself dies.
		// A deadline expiry is not a close — it is exactly the "still open"
		// case this must fail on, so the two are told apart.
		_, err := c.conn.Read(buf)
		if err == nil {
			continue
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatal("connection still open after the credential was revoked")
		}
		return
	}
}

// TestRevokedSubjectLosesItsOpenCommandPlane pins the whole point of
// [Hub.CloseBySubject]: a connection gates every command on the identity it
// captured at the upgrade, so a revocation that stops at the HTTP session
// map leaves the old role live on the socket for as long as the client keeps
// answering pings.
func TestRevokedSubjectLosesItsOpenCommandPlane(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	admin := auth.Identity{Subject: "alice", Scheme: auth.SchemeSession, Role: auth.RoleAdmin}
	server := httptest.NewServer(serveWithIdentity(admin, Handler(hub, nil, nil)))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)

	// An admin-tier command proves the captured identity is what the router
	// gates on.
	if res := c.call("r1", "backup.trigger", map[string]any{}); res.Error != nil {
		t.Fatalf("admin command before revocation = %+v, want success", res.Error)
	}

	// The subject arrives from a URL path parameter, in whatever casing the
	// caller typed; the stores fold it, and so must this.
	if n := hub.CloseBySubject("Alice"); n != 1 {
		t.Fatalf("CloseBySubject closed %d connections, want 1", n)
	}
	expectConnectionClosed(t, c)
	waitForClientCount(t, hub, 0)
}

// TestCloseBySubjectLeavesOtherPrincipalsConnected verifies the teardown is
// scoped to the revoked subject — one admin's demotion must not disconnect
// every operator watching the daemon.
func TestCloseBySubjectLeavesOtherPrincipalsConnected(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	aliceSrv := httptest.NewServer(serveWithIdentity(
		auth.Identity{Subject: "alice", Scheme: auth.SchemeSession, Role: auth.RoleAdmin}, Handler(hub, nil, nil),
	))
	t.Cleanup(aliceSrv.Close)
	bobSrv := httptest.NewServer(serveWithIdentity(
		auth.Identity{Subject: "bob", Scheme: auth.SchemeSession, Role: auth.RoleAdmin}, Handler(hub, nil, nil),
	))
	t.Cleanup(bobSrv.Close)

	alice := dialWS(t, aliceSrv)
	bob := dialWS(t, bobSrv)
	waitForClientCount(t, hub, 2)

	if n := hub.CloseBySubject("alice"); n != 1 {
		t.Fatalf("CloseBySubject closed %d connections, want 1", n)
	}
	expectConnectionClosed(t, alice)
	if res := bob.call("r1", "backup.trigger", map[string]any{}); res.Error != nil {
		t.Fatalf("bob's command after alice's revocation = %+v, want success", res.Error)
	}
}

// TestCloseBySubjectClosesFederatedPrincipals pins the one place where the
// hub deliberately does NOT mirror [auth.SessionStore.RevokeBySubject]'s
// federated exemption. Revoking a session takes a credential away, which the
// daemon has no authority to do to a principal an external provider vouched
// for; closing a socket only forces the re-authentication to happen now, and a
// principal whose credential is still valid reconnects on its own backoff.
// While federated connections were skipped, an explicit logout left the
// principal's command plane dispatching under its connect-time role.
func TestCloseBySubjectClosesFederatedPrincipals(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(serveWithIdentity(
		auth.Identity{Subject: "alice", Scheme: auth.SchemeOIDC, Role: auth.RoleAdmin}, Handler(hub, nil, nil),
	))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)

	if n := hub.CloseBySubject("alice"); n != 1 {
		t.Fatalf("CloseBySubject closed %d federated connections, want 1", n)
	}
	expectConnectionClosed(t, c)
	waitForClientCount(t, hub, 0)
}

// TestRevokedTokenLosesItsOpenCommandPlane pins [Hub.CloseByToken]: a bearer
// token deleted from the store is refused by REST on the next request,
// because REST re-resolves the credential every time — the socket resolved it
// once at the upgrade, so without the teardown the revoked token keeps
// dispatching admin-tier commands for as long as it answers pings.
func TestRevokedTokenLosesItsOpenCommandPlane(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(serveWithIdentity(
		auth.Identity{Subject: "svc", Scheme: auth.SchemeBearer, Role: auth.RoleAdmin, TokenID: "fp-1"},
		Handler(hub, nil, nil),
	))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)
	if res := c.call("r1", "backup.trigger", map[string]any{}); res.Error != nil {
		t.Fatalf("admin command before revocation = %+v, want success", res.Error)
	}

	if n := hub.CloseByToken("fp-1"); n != 1 {
		t.Fatalf("CloseByToken closed %d connections, want 1", n)
	}
	expectConnectionClosed(t, c)
	waitForClientCount(t, hub, 0)
}

// TestCloseByTokenLeavesTheSubjectsOtherCredentialsConnected scopes the
// teardown to the revoked credential: a subject may hold several tokens and an
// interactive session, and revoking one of them must not disconnect the rest.
func TestCloseByTokenLeavesTheSubjectsOtherCredentialsConnected(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	revoked := httptest.NewServer(serveWithIdentity(
		auth.Identity{Subject: "svc", Scheme: auth.SchemeBearer, Role: auth.RoleAdmin, TokenID: "fp-1"},
		Handler(hub, nil, nil),
	))
	t.Cleanup(revoked.Close)
	kept := httptest.NewServer(serveWithIdentity(
		auth.Identity{Subject: "svc", Scheme: auth.SchemeBearer, Role: auth.RoleAdmin, TokenID: "fp-2"},
		Handler(hub, nil, nil),
	))
	t.Cleanup(kept.Close)

	gone := dialWS(t, revoked)
	alive := dialWS(t, kept)
	waitForClientCount(t, hub, 2)

	if n := hub.CloseByToken("fp-1"); n != 1 {
		t.Fatalf("CloseByToken closed %d connections, want 1", n)
	}
	expectConnectionClosed(t, gone)
	if res := alive.call("r1", "backup.trigger", map[string]any{}); res.Error != nil {
		t.Fatalf("second token's command after the first was revoked = %+v, want success", res.Error)
	}
}

// recordingSessionRevoker is a [SessionRevoking] that records what it was
// asked to revoke, so the composed revoker can be shown to do both halves
// rather than silently replacing one with the other.
type recordingSessionRevoker struct {
	subjects []string
	keptSIDs []string
}

func (r *recordingSessionRevoker) RevokeBySubject(subject string) int {
	r.subjects = append(r.subjects, subject)
	return 1
}

func (r *recordingSessionRevoker) RevokeBySubjectExcept(subject, keepSID string) int {
	r.subjects = append(r.subjects, subject)
	r.keptSIDs = append(r.keptSIDs, keepSID)
	return 2
}

// TestRevokeWithSocketsDoesBothHalves verifies the composed revoker still
// revokes sessions (and reports their count, which callers log) while also
// closing the subject's sockets.
func TestRevokeWithSocketsDoesBothHalves(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(serveWithIdentity(
		auth.Identity{Subject: "alice", Scheme: auth.SchemeSession, Role: auth.RoleAdmin}, Handler(hub, nil, nil),
	))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)

	sessions := &recordingSessionRevoker{}
	revoker := RevokeWithSockets(sessions, hub)
	if n := revoker.RevokeBySubject("alice"); n != 1 {
		t.Fatalf("RevokeBySubject reported %d sessions, want the session store's own count (1)", n)
	}
	if len(sessions.subjects) != 1 || sessions.subjects[0] != "alice" {
		t.Fatalf("session revocations = %v, want [alice]", sessions.subjects)
	}
	expectConnectionClosed(t, c)
}

// TestRevokeWithSocketsPasswordChangeKeepsSessionButClosesSockets covers the
// self-service password change: it deliberately keeps the caller's own HTTP
// session, but a socket carries no session id, so it goes and the browser
// reconnects on the surviving cookie.
func TestRevokeWithSocketsPasswordChangeKeepsSessionButClosesSockets(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(serveWithIdentity(
		auth.Identity{Subject: "alice", Scheme: auth.SchemeSession, Role: auth.RoleAdmin}, Handler(hub, nil, nil),
	))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)

	sessions := &recordingSessionRevoker{}
	revoker := RevokeWithSockets(sessions, hub)
	if n := revoker.RevokeBySubjectExcept("alice", "sid-1"); n != 2 {
		t.Fatalf("RevokeBySubjectExcept reported %d sessions, want 2", n)
	}
	if len(sessions.keptSIDs) != 1 || sessions.keptSIDs[0] != "sid-1" {
		t.Fatalf("kept SIDs = %v, want [sid-1]", sessions.keptSIDs)
	}
	expectConnectionClosed(t, c)
}

// TestRevokeWithSocketsWithoutHubKeepsPlainSessionBehaviour guards the
// no-WebSocket daemon: the decorator must not swallow the session half.
func TestRevokeWithSocketsWithoutHubKeepsPlainSessionBehaviour(t *testing.T) {
	t.Parallel()
	sessions := &recordingSessionRevoker{}
	revoker := RevokeWithSockets(sessions, nil)
	if n := revoker.RevokeBySubject("alice"); n != 1 {
		t.Fatalf("RevokeBySubject = %d, want 1", n)
	}
	if len(sessions.subjects) != 1 {
		t.Fatalf("session revocations = %v, want one", sessions.subjects)
	}
}
