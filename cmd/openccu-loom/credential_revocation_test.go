// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// dialUpgradedWS performs the RFC 6455 handshake against server and returns
// the raw connection. The WebSocket client libraries live in the ws package's
// own tests, and this pin only needs a registered connection.
func dialUpgradedWS(t *testing.T, server *httptest.Server) net.Conn {
	t.Helper()
	wsURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	conn, err := net.Dial("tcp", wsURL.Host)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("key: %v", err)
	}
	req := "GET /events HTTP/1.1\r\n" +
		"Host: " + wsURL.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(raw) + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status = %d, want 101", resp.StatusCode)
	}
	return conn
}

// waitForHubClients blocks until the hub holds want connections.
func waitForHubClients(t *testing.T, hub *ws.Hub, want int) {
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

// TestCredentialRevokerRevokesSessionsAndOpenSockets pins the revoker the
// composition root hands to the user-admin and password handlers. A
// revocation that reaches only the session map leaves a demoted or deleted
// principal dispatching commands over its already-open WebSocket under the
// role it captured at the upgrade, for as long as it keeps the socket alive.
func TestCredentialRevokerRevokesSessionsAndOpenSockets(t *testing.T) {
	t.Parallel()

	hub := ws.NewHub()
	sessions := auth.NewSessionStore()
	d := restMountDeps{sessions: sessions, wsHub: hub}

	id := auth.Identity{Subject: "alice", Scheme: auth.SchemeSession, Role: auth.RoleAdmin}
	if _, err := sessions.Issue(id); err != nil {
		t.Fatalf("issue session: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stand in for the auth chain the /events route sits behind: it
		// resolves the credential and attaches the identity, and the upgrade
		// handler copies it onto the connection.
		ws.Handler(hub, nil, nil).ServeHTTP(w, r.WithContext(auth.ContextWithIdentity(r.Context(), id)))
	}))
	t.Cleanup(server.Close)

	dialUpgradedWS(t, server)
	waitForHubClients(t, hub, 1)

	revoker := d.credentialRevoker()
	if revoker == nil {
		t.Fatal("composition root wired no credential revoker")
	}
	if n := revoker.RevokeBySubject("alice"); n != 1 {
		t.Fatalf("revoked %d sessions, want 1", n)
	}
	waitForHubClients(t, hub, 0)
}

// TestCredentialRevokerWithoutSessionsStaysUnwired keeps the pre-existing
// contract: the handlers treat a nil revoker as "no revocation hook", and a
// daemon without a session store must not gain one that only closes sockets.
func TestCredentialRevokerWithoutSessionsStaysUnwired(t *testing.T) {
	t.Parallel()
	d := restMountDeps{wsHub: ws.NewHub()}
	if d.credentialRevoker() != nil {
		t.Fatal("credentialRevoker must stay nil without a session store")
	}
}
