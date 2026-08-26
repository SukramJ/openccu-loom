// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// fakeCCU mimics the CCU's JSON-RPC session handlers on the wire, including
// the return types that matter:
//
//	Session.login  -> "<sid>"  (string)
//	Session.renew  -> true     (bool)   — extends in place, no new ID
//	Session.logout -> true     (bool)
//
// It tracks which sessions are still open so a test can assert that the
// client does not strand any on the CCU.
type fakeCCU struct {
	mu sync.Mutex

	logins  int
	renews  int
	logouts int

	sidSeq int
	open   map[string]bool

	// renewResult is what Session.renew answers with. The CCU says true;
	// false models a session the CCU has dropped.
	renewResult bool
	// loginError, when non-zero, makes Session.login fail with this
	// JSON-RPC error code instead of returning a session.
	loginErrCode int
	loginErrMsg  string
}

func newFakeCCU() *fakeCCU {
	return &fakeCCU{open: map[string]bool{}, renewResult: true}
}

func (f *fakeCCU) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env envelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")

		switch env.Method {
		case "Session.login":
			f.logins++
			if f.loginErrCode != 0 {
				_, _ = fmt.Fprintf(w, `{"result":null,"error":{"code":%d,"message":%q}}`,
					f.loginErrCode, f.loginErrMsg)
				return
			}
			f.sidSeq++
			sid := fmt.Sprintf("sid-%d", f.sidSeq)
			f.open[sid] = true
			_, _ = fmt.Fprintf(w, `{"result":%q,"error":null}`, sid)

		case "Session.renew":
			f.renews++
			_, _ = fmt.Fprintf(w, `{"result":%t,"error":null}`, f.renewResult)

		case "Session.logout":
			f.logouts++
			if sid, ok := env.Params[sessionParamKey].(string); ok {
				delete(f.open, sid)
			}
			_, _ = fmt.Fprint(w, `{"result":true,"error":null}`)

		default:
			_, _ = fmt.Fprint(w, `{"result":null,"error":null}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeCCU) counts() (logins, renews, logouts, openSessions int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins, f.renews, f.logouts, len(f.open)
}

// expireFreshness rewinds the client's freshness stamp so the next Call runs
// the renew ladder instead of taking the fast path — the test equivalent of
// jsonSessionAge elapsing.
func expireFreshness(c *Client) {
	c.mu.Lock()
	c.lastSessionRefresh = time.Now().Add(-2 * jsonSessionAge)
	c.mu.Unlock()
}

func newTestClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	c, err := New(Config{Endpoint: endpoint, Username: "Admin", Password: "secret"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// The CCU answers Session.renew with the boolean true and keeps the existing
// session ID. A long-lived client that keeps polling must therefore hold
// exactly ONE session for its whole life, however many freshness windows
// elapse. Decoding the reply into the wrong Go type used to turn every
// renewal into a decode error, which re-logged-in and stranded the old
// session on the CCU — 40 leaked sessions per hour per central.
func TestRenewKeepsSingleSessionAcrossFreshnessWindows(t *testing.T) {
	t.Parallel()
	ccu := newFakeCCU()
	c := newTestClient(t, ccu.start(t).URL)
	ctx := context.Background()

	if err := c.Call(ctx, "Interface.listInterfaces", nil, nil); err != nil {
		t.Fatalf("cold call: %v", err)
	}
	first := c.SessionID()

	for i := range 5 {
		expireFreshness(c)
		if err := c.Call(ctx, "Interface.listInterfaces", nil, nil); err != nil {
			t.Fatalf("call %d: %v", i+2, err)
		}
	}

	logins, renews, _, open := ccu.counts()
	if logins != 1 {
		t.Errorf("Session.login called %d times, want 1 — the client is leaking a session per renewal", logins)
	}
	if renews != 5 {
		t.Errorf("Session.renew called %d times, want 5", renews)
	}
	if open != 1 {
		t.Errorf("%d sessions left open on the CCU, want 1", open)
	}
	if got := c.SessionID(); got != first {
		t.Errorf("session ID changed from %q to %q; Session.renew extends in place", first, got)
	}
}

// Within the freshness window neither a renew nor a login round-trip is made.
func TestRenewSkippedWithinFreshnessWindow(t *testing.T) {
	t.Parallel()
	ccu := newFakeCCU()
	c := newTestClient(t, ccu.start(t).URL)
	ctx := context.Background()

	for range 3 {
		if err := c.Call(ctx, "Interface.listInterfaces", nil, nil); err != nil {
			t.Fatalf("call: %v", err)
		}
	}

	logins, renews, _, _ := ccu.counts()
	if logins != 1 || renews != 0 {
		t.Errorf("logins=%d renews=%d, want logins=1 renews=0 inside the freshness window", logins, renews)
	}
}

// A CCU that rejects the renewal (session dropped after a reboot) must cost
// exactly one replacement session — and the client must hand the old slot
// back rather than let it idle out.
func TestRenewRejectedReleasesStaleSessionBeforeRelogin(t *testing.T) {
	t.Parallel()
	ccu := newFakeCCU()
	ccu.renewResult = false
	c := newTestClient(t, ccu.start(t).URL)
	ctx := context.Background()

	if err := c.Call(ctx, "Interface.listInterfaces", nil, nil); err != nil {
		t.Fatalf("cold call: %v", err)
	}
	stale := c.SessionID()

	expireFreshness(c)
	if err := c.Call(ctx, "Interface.listInterfaces", nil, nil); err != nil {
		t.Fatalf("call after renew rejection: %v", err)
	}

	logins, _, logouts, open := ccu.counts()
	if logins != 2 {
		t.Errorf("Session.login called %d times, want 2 (cold + replacement)", logins)
	}
	if logouts != 1 {
		t.Errorf("Session.logout called %d times, want 1 — the stale session must be released", logouts)
	}
	if open != 1 {
		t.Errorf("%d sessions open on the CCU, want 1", open)
	}
	if c.SessionID() == stale || c.SessionID() == "" {
		t.Errorf("expected a fresh session, got %q (stale was %q)", c.SessionID(), stale)
	}
}

// The CCU reports an expired session as HTTP 200 + JSON-RPC error 400
// ("access denied"). The client re-logs-in and retries once — and releases
// the session it abandoned.
func TestAccessDeniedReleasesStaleSessionBeforeRelogin(t *testing.T) {
	t.Parallel()
	var (
		mu             sync.Mutex
		logins, logout int
		denyOnce       = true
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env envelope
		_ = json.NewDecoder(r.Body).Decode(&env)
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch env.Method {
		case "Session.login":
			logins++
			_, _ = fmt.Fprintf(w, `{"result":"sid-%d","error":null}`, logins)
		case "Session.logout":
			logout++
			_, _ = fmt.Fprint(w, `{"result":true,"error":null}`)
		default:
			if denyOnce {
				denyOnce = false
				_, _ = fmt.Fprint(w, `{"result":null,"error":{"code":400,"message":"access denied"}}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"result":"ok","error":null}`)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.Call(context.Background(), "Interface.listInterfaces", nil, nil); err != nil {
		t.Fatalf("call: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if logins != 2 {
		t.Errorf("Session.login called %d times, want 2 (cold + after access-denied)", logins)
	}
	if logout != 1 {
		t.Errorf("Session.logout called %d times, want 1 — the denied session must be released", logout)
	}
}

// "invalid credentials or too many sessions" arrives as a JSON-RPC error, not
// as an empty result. It must surface as an auth failure AND engage the login
// backoff — hammering a CCU whose session pool is full is what keeps it full.
func TestLoginRejectionEngagesBackoff(t *testing.T) {
	t.Parallel()
	ccu := newFakeCCU()
	ccu.loginErrCode = 501
	ccu.loginErrMsg = "invalid credentials or too many sessions"
	c := newTestClient(t, ccu.start(t).URL)

	err := c.Login(context.Background())
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Errorf("Login error = %v, want it to wrap hmerr.ErrAuthFailure", err)
	}

	c.mu.Lock()
	attempts, backoff := c.failedLoginAttempts, c.currentBackoff
	c.mu.Unlock()
	if attempts != 1 {
		t.Errorf("failedLoginAttempts = %d, want 1", attempts)
	}
	if backoff != loginBaseBackoff {
		t.Errorf("currentBackoff = %v, want %v", backoff, loginBaseBackoff)
	}
}

// A transport error is not the credentials' fault and must not penalise the
// login backoff — otherwise a brief CCU outage slows every later reconnect.
func TestLoginTransportErrorDoesNotEngageBackoff(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if err := c.Login(context.Background()); err == nil {
		t.Fatal("Login: want error on HTTP 500")
	}

	c.mu.Lock()
	attempts := c.failedLoginAttempts
	c.mu.Unlock()
	if attempts != 0 {
		t.Errorf("failedLoginAttempts = %d, want 0 for a transport error", attempts)
	}
}

// Login() opens a NEW session by definition. When it displaces a live one,
// that slot must go back to the CCU rather than idle out — otherwise any
// caller reaching for Login directly (the backup/firmware download renewer
// did) leaks a session every time it runs.
func TestLoginReleasesDisplacedSession(t *testing.T) {
	t.Parallel()
	ccu := newFakeCCU()
	c := newTestClient(t, ccu.start(t).URL)
	ctx := context.Background()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("first login: %v", err)
	}
	first := c.SessionID()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("second login: %v", err)
	}

	logins, _, logouts, open := ccu.counts()
	if logins != 2 {
		t.Errorf("Session.login called %d times, want 2", logins)
	}
	if logouts != 1 {
		t.Errorf("Session.logout called %d times, want 1 — the displaced session must be released", logouts)
	}
	if open != 1 {
		t.Errorf("%d sessions open on the CCU, want 1", open)
	}
	if c.SessionID() == first {
		t.Error("Login must mint a new session id")
	}
}

// EnsureSession is the ladder the backup/firmware download uses to make sure
// its session id is usable. On a healthy session it must renew in place — a
// forced login there would displace the session the whole central is using
// and burn a CCU slot on every backup.
func TestEnsureSessionRenewsInsteadOfReplacing(t *testing.T) {
	t.Parallel()
	ccu := newFakeCCU()
	c := newTestClient(t, ccu.start(t).URL)
	ctx := context.Background()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("login: %v", err)
	}
	sid := c.SessionID()

	// Fresh session: no round-trip at all.
	if err := c.EnsureSession(ctx); err != nil {
		t.Fatalf("EnsureSession (fresh): %v", err)
	}
	// Past the freshness window: a renew, not a login.
	expireFreshness(c)
	if err := c.EnsureSession(ctx); err != nil {
		t.Fatalf("EnsureSession (stale): %v", err)
	}

	logins, renews, _, open := ccu.counts()
	if logins != 1 {
		t.Errorf("Session.login called %d times, want 1 — EnsureSession must not displace a healthy session", logins)
	}
	if renews != 1 {
		t.Errorf("Session.renew called %d times, want 1", renews)
	}
	if got := c.SessionID(); got != sid {
		t.Errorf("session id changed from %q to %q", sid, got)
	}
	if open != 1 {
		t.Errorf("%d sessions open on the CCU, want 1", open)
	}
}

// A burst of concurrent callers on a cold client must open exactly one
// session, not one per goroutine.
func TestConcurrentColdStartOpensSingleSession(t *testing.T) {
	t.Parallel()
	ccu := newFakeCCU()
	c := newTestClient(t, ccu.start(t).URL)
	ctx := context.Background()

	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Call(ctx, "Interface.listInterfaces", nil, nil); err != nil {
				t.Errorf("concurrent call: %v", err)
			}
		}()
	}
	wg.Wait()

	logins, _, _, open := ccu.counts()
	if logins != 1 {
		t.Errorf("Session.login called %d times, want 1", logins)
	}
	if open != 1 {
		t.Errorf("%d sessions open on the CCU, want 1", open)
	}
}
