// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/middleware"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/tests/contract"
)

// countingUserStore records how often a password verification was actually
// requested. The password KDF is what the per-source budget has to bound, so
// "how many times did the store get asked" is the only observable that
// distinguishes a guard in front of the KDF from one behind it.
type countingUserStore struct {
	calls atomic.Int64
	inner auth.UserStore
}

func (s *countingUserStore) AuthenticateBasic(ctx context.Context, username, password string) (auth.Identity, error) {
	s.calls.Add(1)
	return s.inner.AuthenticateBasic(ctx, username, password)
}

// credentialRig assembles the REST router the way the daemon's composition
// root does for the credential surfaces: the auth middleware and the session
// middleware chained into one AuthResolve, the session store composed with
// the WebSocket hub into the revoker the logout / user-admin handlers get,
// and the hub itself as the token-socket teardown.
type credentialRig struct {
	server   *httptest.Server
	sessions *auth.SessionStore
	tokens   *auth.MemoryTokenStore
	users    *countingUserStore
	hub      *ws.Hub
}

func newCredentialRig(t *testing.T) *credentialRig {
	t.Helper()
	memUsers := auth.NewMemoryUserStore()
	// A plaintext record, so the assertions are about how often the store is
	// consulted rather than about how long a key derivation takes. The
	// throttle sits in front of the store either way.
	memUsers.Put("alice", "correct-horse", auth.RoleAdmin)
	users := &countingUserStore{inner: memUsers}

	tokens := auth.NewMemoryTokenStore(nil)
	sessions := auth.NewSessionStore()
	hub := ws.NewHub()

	authMw := auth.NewMiddleware(users, tokens)
	limiter := middleware.NewLoginRateLimiter()
	// The resolver holds the throttle, because the resolver is where the
	// password KDF runs. See the composition root in
	// cmd/openccu-loom/daemon_rest_mount.go.
	authMw.BasicThrottle = limiter
	sessionResolve := auth.SessionMiddleware(sessions)
	resolve := func(next http.Handler) http.Handler { return authMw.Resolve(sessionResolve(next)) }

	router := rest.NewRouter(rest.Deps{
		Auth: &handlers.AuthDeps{
			Users:    memUsers,
			Sessions: sessions,
			Tokens:   tokens,
		},
		SessionRevoker: ws.RevokeWithSockets(sessions, hub),
		TokenSockets:   hub,
		WSHandler:      ws.Handler(hub, nil, nil),
		AuthResolve:    resolve,
		AuthRequire:    authMw.Require,
		RequireAdmin: func(next http.Handler) http.Handler {
			return authMw.RequireRole(auth.RoleAdmin, next)
		},
		LoginRateLimit: limiter,
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return &credentialRig{server: server, sessions: sessions, tokens: tokens, users: users, hub: hub}
}

// dialEvents completes the WebSocket handshake against /api/v1/events with
// the supplied credential header. Only the connection matters here — the
// assertions are about whether the daemon keeps it, so no frames are read.
func (rig *credentialRig) dialEvents(t *testing.T, header, value string) net.Conn {
	t.Helper()
	u, _ := url.Parse(rig.server.URL)
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("websocket key: %v", err)
	}
	req := "GET /api/v1/events HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		header + ": " + value + "\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(key[:]) + "\r\n" +
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
		t.Fatalf("handshake status=%d, want 101", resp.StatusCode)
	}
	return conn
}

// waitForClients blocks until the hub holds want connections.
func (rig *credentialRig) waitForClients(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rig.hub.ClientCount() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("hub holds %d connections, want %d", rig.hub.ClientCount(), want)
}

// TestLogoutRevokesTheSessionItWasPresentedWith pins the one revocation
// logout cannot delegate. A by-subject sweep spares federated principals by
// design — the daemon does not own an external provider's credentials — so
// for an OIDC login it evicts nothing, and the presented cookie stayed a
// valid credential for the rest of its 12h TTL after the operator clicked
// Logout, with no other surface able to end it.
func TestLogoutRevokesTheSessionItWasPresentedWith(t *testing.T) {
	t.Parallel()
	for _, scheme := range []auth.Scheme{auth.SchemeSession, auth.SchemeOIDC} {
		t.Run(string(scheme), func(t *testing.T) {
			t.Parallel()
			rig := newCredentialRig(t)
			sess, err := rig.sessions.Issue(auth.Identity{Subject: "alice", Scheme: scheme, Role: auth.RoleAdmin})
			if err != nil {
				t.Fatalf("issue session: %v", err)
			}
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, rig.server.URL+"/api/v1/auth/logout", http.NoBody)
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.ID})
			resp, err := rig.server.Client().Do(req)
			if err != nil {
				t.Fatalf("logout: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("logout status=%d, want 204", resp.StatusCode)
			}
			if rig.sessions.Lookup(sess.ID) != nil {
				t.Fatal("session still resolvable after logout")
			}
		})
	}
}

// TestLogoutClosesTheCallersOpenSocket pins the second half: the connection
// gates every later command on the identity it captured at the upgrade, so a
// logout that stops at the session map leaves the logged-out principal's
// command plane running. Both schemes go, because closing a socket only
// forces a re-authentication — it takes no credential away.
func TestLogoutClosesTheCallersOpenSocket(t *testing.T) {
	t.Parallel()
	for _, scheme := range []auth.Scheme{auth.SchemeSession, auth.SchemeOIDC} {
		t.Run(string(scheme), func(t *testing.T) {
			t.Parallel()
			rig := newCredentialRig(t)
			sess, err := rig.sessions.Issue(auth.Identity{Subject: "alice", Scheme: scheme, Role: auth.RoleAdmin})
			if err != nil {
				t.Fatalf("issue session: %v", err)
			}
			rig.dialEvents(t, "Cookie", auth.SessionCookieName+"="+sess.ID)
			rig.waitForClients(t, 1)

			req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, rig.server.URL+"/api/v1/auth/logout", http.NoBody)
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.ID})
			resp, err := rig.server.Client().Do(req)
			if err != nil {
				t.Fatalf("logout: %v", err)
			}
			_ = resp.Body.Close()
			rig.waitForClients(t, 0)
		})
	}
}

// TestLegacyTokenRevocationClosesTheTokensSocket pins the deprecated
// DELETE /auth/tokens/{id} to the same teardown its v2 sibling performs.
// It also pins the identity plumbing the teardown needs: the in-memory store
// has to stamp the token's own id onto the identities it issues, or the
// per-credential close matches nothing and the route's teardown is
// decoration.
func TestLegacyTokenRevocationClosesTheTokensSocket(t *testing.T) {
	t.Parallel()
	rig := newCredentialRig(t)
	victim := "victim-token-value-0123456789"
	victimID := rig.tokens.Put(victim, auth.Identity{Subject: "svc", Role: auth.RoleOperator})
	adminToken := "admin-token-value-0123456789"
	rig.tokens.Put(adminToken, auth.Identity{Subject: "root", Role: auth.RoleAdmin})

	rig.dialEvents(t, "Authorization", "Bearer "+victim)
	rig.waitForClients(t, 1)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete,
		rig.server.URL+"/api/v1/auth/tokens/"+victimID, http.NoBody)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := rig.server.Client().Do(req)
	if err != nil {
		t.Fatalf("delete token: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d, want 204", resp.StatusCode)
	}
	rig.waitForClients(t, 0)
}

// TestExpiredSessionSocketIsClosed pins the credential deadline all the way
// through the mounted router: an established connection used to outlive the
// session it was opened with, keeping admin command authority while every
// REST call from the same cookie answered 401.
func TestExpiredSessionSocketIsClosed(t *testing.T) {
	t.Parallel()
	rig := newCredentialRig(t)
	rig.sessions.TTL = 300 * time.Millisecond
	sess, err := rig.sessions.Issue(auth.Identity{Subject: "alice", Scheme: auth.SchemeSession, Role: auth.RoleAdmin})
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	rig.dialEvents(t, "Cookie", auth.SessionCookieName+"="+sess.ID)
	rig.waitForClients(t, 1)
	rig.waitForClients(t, 0)

	// The control: REST refuses the same credential, which is the state the
	// socket had been diverging from.
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, rig.server.URL+"/api/v1/auth/me", http.NoBody)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.ID})
	resp, err := rig.server.Client().Do(req)
	if err != nil {
		t.Fatalf("auth/me: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("auth/me with an expired session = %d, want 401", resp.StatusCode)
	}
}

// TestBasicAuthBudgetGatesThePasswordVerification pins the ordering that
// makes the per-source budget mean anything: the password KDF is deliberately
// expensive, so a throttle consulted after it has already run bounds nothing.
// An unauthenticated caller could spend a CPU core per request on any route
// the router resolves credentials for — including /info, which needs no
// credential at all.
func TestBasicAuthBudgetGatesThePasswordVerification(t *testing.T) {
	rig := newCredentialRig(t)
	// Concurrent, because that is the shape that defeats a throttle placed
	// after the verification: every request passes the same "is there budget
	// left" read before any of them has charged, and they all run the key
	// derivation in parallel.
	const attempts = 24
	var throttled atomic.Int64
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, rig.server.URL+"/api/v1/info", http.NoBody)
			req.SetBasicAuth("alice", "wrong-password")
			resp, err := rig.server.Client().Do(req)
			if err != nil {
				return
			}
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusTooManyRequests {
				throttled.Add(1)
			}
		}()
	}
	wg.Wait()

	// The burst is the whole allowance, plus whatever the bucket refilled
	// while the sweep ran (one per second).
	if got := rig.users.calls.Load(); got > 8 {
		t.Fatalf("password verifications = %d over %d concurrent guesses, want the per-source budget to bound them", got, attempts)
	}
	if throttled.Load() == 0 {
		t.Fatal("no attempt was answered 429 — the spent budget is not surfaced to the caller")
	}
}

// TestDaemonGivesTheResolverTheBasicThrottle pins the composition root itself.
// The rig above wires the throttle onto the middleware the way the daemon
// does, which proves the mechanism works — not that the daemon still does it.
// Without that assignment the accounting falls back to the guard behind the
// key derivation, which bounds the sweep but no longer bounds its cost.
func TestDaemonGivesTheResolverTheBasicThrottle(t *testing.T) {
	t.Parallel()
	contract.MustFindCallerInFile(
		t,
		"cmd/openccu-loom/daemon_rest_mount.go",
		"internal/auth",
		"BasicThrottle",
	)
}

// TestVerifiedBasicCredentialIsNotReThrottled is the counterpart: a valid
// credential must cost nothing, or an operator using HTTP Basic for
// automation would be rate-limited by their own successful requests.
func TestVerifiedBasicCredentialIsNotReThrottled(t *testing.T) {
	rig := newCredentialRig(t)
	for i := range 20 {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, rig.server.URL+"/api/v1/auth/me", http.NoBody)
		req.SetBasicAuth("alice", "correct-horse")
		resp, err := rig.server.Client().Do(req)
		if err != nil {
			t.Fatalf("auth/me: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d with valid credentials = %d, want 200", i, resp.StatusCode)
		}
	}
}
