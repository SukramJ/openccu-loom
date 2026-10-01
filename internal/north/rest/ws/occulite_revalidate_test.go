// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// testOcculiteSID is a well-formed box session id: 26 characters of A-Z2-7.
const testOcculiteSID = "QRSTUVWXYZ234567ABCDEFGHIJ"

// testRevalidateInterval stands in for the one-minute cadence.
const testRevalidateInterval = 20 * time.Millisecond

// withOcculiteRevalidateInterval shortens the re-verification cadence so a
// test does not wait a minute.
func withOcculiteRevalidateInterval(d time.Duration) HandlerOption {
	return func(hc *handlerConfig) { hc.occuliteInterval = d }
}

// fakeRevalidator answers every re-verification with a switchable verdict
// and records the session ids and roles it was asked about. When boxRole is
// set it also plays the role check: the verdict is false once the socket's
// role differs from what the box now says.
type fakeRevalidator struct {
	holds   atomic.Bool
	boxRole atomic.Value // auth.Role
	mu      sync.Mutex
	asked   []string
	roles   []auth.Role
}

func (f *fakeRevalidator) revalidate(_ context.Context, sid string, role auth.Role) bool {
	f.mu.Lock()
	f.asked = append(f.asked, sid)
	f.roles = append(f.roles, role)
	f.mu.Unlock()
	if r, ok := f.boxRole.Load().(auth.Role); ok && r != role {
		return false
	}
	return f.holds.Load()
}

func (f *fakeRevalidator) seenRoles() []auth.Role {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]auth.Role(nil), f.roles...)
}

func (f *fakeRevalidator) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

// serveOcculiteUpgrade stands in for the lite gate plus the SSO resolver:
// the upgrade request carries the box session header and an identity of
// the given scheme.
func serveOcculiteUpgrade(scheme auth.Scheme, next http.Handler) http.Handler {
	id := auth.Identity{Subject: "occulite:alice", Scheme: scheme, Role: auth.RoleAdmin}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set(auth.OcculiteSessionHeader, testOcculiteSID)
		next.ServeHTTP(w, r.WithContext(auth.ContextWithIdentity(r.Context(), id)))
	})
}

// lockedBuffer is a log sink safe for the watch goroutine to write to.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestOcculiteSocketClosedOnceTheBoxStopsConfirming: a box-shell identity
// carries no expiry, so without re-verification a session logged out on the
// box kept its command plane for as long as the TCP connection lived. The
// socket must close once the revalidator answers false — and the log line
// that records it must not carry the session id.
func TestOcculiteSocketClosedOnceTheBoxStopsConfirming(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	rv := &fakeRevalidator{}
	rv.holds.Store(true)
	logs := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	server := httptest.NewServer(serveOcculiteUpgrade(auth.SchemeOcculite, Handler(hub, logger, nil,
		WithOcculiteRevalidate(rv.revalidate), withOcculiteRevalidateInterval(testRevalidateInterval))))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)

	// While the box confirms, the socket survives several checks.
	waitForAsked(t, rv, 2)
	if res := c.call("r1", "backup.trigger", map[string]any{}); res.Error != nil {
		t.Fatalf("admin command while the box confirms = %+v, want success", res.Error)
	}
	for _, sid := range rv.calls() {
		if sid != testOcculiteSID {
			t.Fatalf("revalidator asked about %q, want the upgrade's session id", sid)
		}
	}

	rv.holds.Store(false)
	expectConnectionClosed(t, c)
	waitForClientCount(t, hub, 0)

	out := logs.String()
	if !strings.Contains(out, "ws.credential.revoked") {
		t.Fatalf("no revocation log line; logs:\n%s", out)
	}
	if strings.Contains(out, testOcculiteSID) {
		t.Fatalf("a log line carries the session id:\n%s", out)
	}
}

// TestOcculiteSocketStaysOpenWhileTheBoxConfirms guards the other direction:
// a confirmed session keeps its socket across many re-verifications.
func TestOcculiteSocketStaysOpenWhileTheBoxConfirms(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	rv := &fakeRevalidator{}
	rv.holds.Store(true)
	server := httptest.NewServer(serveOcculiteUpgrade(auth.SchemeOcculite, Handler(hub, nil, nil,
		WithOcculiteRevalidate(rv.revalidate), withOcculiteRevalidateInterval(testRevalidateInterval))))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)
	waitForAsked(t, rv, 5)
	if res := c.call("r1", "backup.trigger", map[string]any{}); res.Error != nil {
		t.Fatalf("command after re-verifications = %+v, want success", res.Error)
	}
	if n := hub.ClientCount(); n != 1 {
		t.Fatalf("hub holds %d connections, want the confirmed one to survive", n)
	}
}

// TestNonOcculiteSocketNeverRevalidates: only a box-shell identity is the
// box's to end. A session or bearer identity on a request that happens to
// carry the header must never trigger a box round trip.
func TestNonOcculiteSocketNeverRevalidates(t *testing.T) {
	t.Parallel()
	for _, scheme := range []auth.Scheme{auth.SchemeSession, auth.SchemeBearer} {
		t.Run(string(scheme), func(t *testing.T) {
			t.Parallel()
			hub, _, _, _, _, _ := newTestHub(t)
			rv := &fakeRevalidator{} // answers false: a call would close the socket
			server := httptest.NewServer(serveOcculiteUpgrade(scheme, Handler(hub, nil, nil,
				WithOcculiteRevalidate(rv.revalidate), withOcculiteRevalidateInterval(testRevalidateInterval))))
			t.Cleanup(server.Close)

			c := dialWS(t, server)
			waitForClientCount(t, hub, 1)
			time.Sleep(10 * testRevalidateInterval)
			if n := len(rv.calls()); n != 0 {
				t.Fatalf("revalidator called %d times for a %s identity, want 0", n, scheme)
			}
			if res := c.call("r1", "backup.trigger", map[string]any{}); res.Error != nil {
				t.Fatalf("command = %+v, want success", res.Error)
			}
		})
	}
}

// TestOcculiteSocketCheckedImmediatelyAtUpgrade: the identity may come from
// the resolver's cache, minted before a logout the box has since recorded.
// The first re-verification runs at the upgrade, so a session the box
// already refuses loses its socket without waiting an interval — the
// default one-minute cadence stays in force here.
func TestOcculiteSocketCheckedImmediatelyAtUpgrade(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	rv := &fakeRevalidator{} // refuses from the start
	server := httptest.NewServer(serveOcculiteUpgrade(auth.SchemeOcculite, Handler(hub, nil, nil,
		WithOcculiteRevalidate(rv.revalidate))))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	expectConnectionClosed(t, c)
	waitForClientCount(t, hub, 0)
	if n := len(rv.calls()); n != 1 {
		t.Fatalf("revalidator calls = %d, want exactly the upgrade-time one", n)
	}
}

// TestOcculiteSocketClosedOnDemotion: the socket hands the revalidator the
// role it captured at the upgrade, so a box that has since demoted the
// session from admin to user ends the admin socket.
func TestOcculiteSocketClosedOnDemotion(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	rv := &fakeRevalidator{}
	rv.holds.Store(true)
	rv.boxRole.Store(auth.RoleAdmin)
	server := httptest.NewServer(serveOcculiteUpgrade(auth.SchemeOcculite, Handler(hub, nil, nil,
		WithOcculiteRevalidate(rv.revalidate), withOcculiteRevalidateInterval(testRevalidateInterval))))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)
	waitForAsked(t, rv, 2)
	for _, r := range rv.seenRoles() {
		if r != auth.RoleAdmin {
			t.Fatalf("revalidator saw role %q, want the socket's admin role", r)
		}
	}

	rv.boxRole.Store(auth.RoleOperator) // the box now says "user"
	expectConnectionClosed(t, c)
	waitForClientCount(t, hub, 0)
}

// waitForAsked blocks until the revalidator has been asked at least n times.
func waitForAsked(t *testing.T, rv *fakeRevalidator, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(rv.calls()) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("revalidator asked %d times, want at least %d", len(rv.calls()), n)
}
