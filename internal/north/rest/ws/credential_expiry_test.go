// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// dialWithSessionCookie performs the RFC 6455 handshake carrying a session
// cookie, so the identity the connection captures comes from the real
// [auth.SessionMiddleware] rather than from a test-injected value.
func dialWithSessionCookie(t *testing.T, server *httptest.Server, sid string) *wsConn {
	t.Helper()
	wsURL, _ := url.Parse(server.URL)
	conn, err := net.Dial("tcp", wsURL.Host)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	req := "GET /ws HTTP/1.1\r\n" +
		"Host: " + wsURL.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Cookie: " + auth.SessionCookieName + "=" + sid + "\r\n" +
		"Sec-WebSocket-Key: " + genWSKey(t) + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status=%d", resp.StatusCode)
	}
	return &wsConn{conn: conn, br: br, t: t}
}

// TestExpiredSessionClosesTheSocket pins the deadline travelling from the
// session store, through the real session middleware, onto the connection:
// an established socket used to hold the role it captured at the upgrade for
// as long as the client answered pings, because a session's TTL is only ever
// enforced where a credential is resolved — on an HTTP request. The socket is
// never re-resolved, so REST answered 401 while the same principal kept
// dispatching operator/admin commands over the open connection.
func TestExpiredSessionClosesTheSocket(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	sessions := auth.NewSessionStore()
	sessions.TTL = 300 * time.Millisecond
	sess, err := sessions.Issue(auth.Identity{Subject: "alice", Scheme: auth.SchemeSession, Role: auth.RoleAdmin})
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	server := httptest.NewServer(auth.SessionMiddleware(sessions)(Handler(hub, nil, nil)))
	t.Cleanup(server.Close)

	c := dialWithSessionCookie(t, server, sess.ID)
	waitForClientCount(t, hub, 1)

	// While the session is live the captured identity carries its full role.
	if res := c.call("r1", "backup.trigger", map[string]any{}); res.Error != nil {
		t.Fatalf("admin command before expiry = %+v, want success", res.Error)
	}
	// REST refuses the same credential once the TTL elapses; the socket has
	// to follow it.
	expectConnectionClosed(t, c)
	if sessions.Lookup(sess.ID) != nil {
		t.Fatal("session still resolvable after its TTL elapsed")
	}
	waitForClientCount(t, hub, 0)
}

// TestExpiredIdentityRefusesCommands covers the window between the deadline
// and the connection teardown — and the in-band reauth that installs an
// already-expired credential. No command may be dispatched on an expired
// snapshot, whichever of the two closes the socket first.
func TestExpiredIdentityRefusesCommands(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(serveWithIdentity(
		auth.Identity{Subject: "alice", Scheme: auth.SchemeSession, Role: auth.RoleAdmin}, Handler(hub, nil, nil),
	))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)
	if res := c.call("r1", "backup.trigger", map[string]any{}); res.Error != nil {
		t.Fatalf("admin command before expiry = %+v, want success", res.Error)
	}

	grantIdentity(hub, auth.Identity{
		Subject:   "alice",
		Scheme:    auth.SchemeSession,
		Role:      auth.RoleAdmin,
		ExpiresAt: time.Now().Add(-time.Minute),
	})
	res := c.call("r2", "backup.trigger", map[string]any{})
	if res.Error == nil || res.Error.Code != CommandErrorUnauthorized {
		t.Fatalf("command on an expired credential = %+v, want unauthorized", res.Error)
	}
}

// TestUnexpiringIdentityKeepsItsSocket guards the other direction: the
// overwhelming majority of credentials carry no server-side deadline
// (a YAML-seeded bearer token, a Basic user), and the expiry watch must
// leave those connections alone.
func TestUnexpiringIdentityKeepsItsSocket(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(serveWithIdentity(
		auth.Identity{Subject: "svc", Scheme: auth.SchemeBearer, Role: auth.RoleAdmin, TokenID: "fp-1"},
		Handler(hub, nil, nil),
	))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)
	time.Sleep(300 * time.Millisecond)
	if res := c.call("r1", "backup.trigger", map[string]any{}); res.Error != nil {
		t.Fatalf("command on a credential without a deadline = %+v, want success", res.Error)
	}
	if n := hub.ClientCount(); n != 1 {
		t.Fatalf("hub holds %d connections, want the unexpiring one to survive", n)
	}
}

// TestSessionMiddlewareCarriesTheSessionDeadline pins the producer half in
// isolation: without the deadline on the identity, every consumer that keeps
// the snapshot (the WebSocket upgrade) has nothing to enforce.
func TestSessionMiddlewareCarriesTheSessionDeadline(t *testing.T) {
	t.Parallel()
	sessions := auth.NewSessionStore()
	sess, err := sessions.Issue(auth.Identity{Subject: "alice", Scheme: auth.SchemeSession, Role: auth.RoleAdmin})
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	var got auth.Identity
	h := auth.SessionMiddleware(sessions)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = auth.IdentityFrom(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.ID})
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !got.ExpiresAt.Equal(sess.Expires) {
		t.Fatalf("identity deadline = %v, want the session's own expiry %v", got.ExpiresAt, sess.Expires)
	}
	if got.Expired(sess.Expires.Add(time.Second)) != true {
		t.Fatal("identity does not report itself expired past the session expiry")
	}
}
