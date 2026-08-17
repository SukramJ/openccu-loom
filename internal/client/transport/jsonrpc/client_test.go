// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// newTestServer spins up an httptest server whose handler is driven by
// route (a map method→handler). Requests are decoded into envelope{}.
func newTestServer(t *testing.T, route map[string]func(envelope) any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		handler, ok := route[env.Method]
		if !ok {
			http.Error(w, "unknown method", http.StatusNotFound)
			return
		}
		out := handler(env)
		switch v := out.(type) {
		case int: // HTTP status override
			w.WriteHeader(v)
		case response:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(v)
		case error:
			http.Error(w, v.Error(), http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected handler return: %T", out)
		}
	}))
}

func okResult(v any) response {
	raw, _ := json.Marshal(v)
	return response{Result: raw}
}

func TestClientHappyPath(t *testing.T) {
	srv := newTestServer(t, map[string]func(envelope) any{
		"System.listMethods": func(env envelope) any {
			if _, hasSession := env.Params[sessionParamKey]; hasSession {
				t.Errorf("unauthenticated client should not send session id")
			}
			return okResult([]string{"a", "b"})
		},
	})
	defer srv.Close()

	c, err := New(Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := c.Call(context.Background(), "System.listMethods", nil, &got); err != nil {
		t.Fatalf("Call returned %v", err)
	}
	if len(got) != 2 || got[0] != "a" {
		t.Fatalf("got %v", got)
	}
}

func TestClientLoginInjectsSession(t *testing.T) {
	var sessionSeen atomic.Value
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			if env.Params["username"] != "u" || env.Params["password"] != "p" {
				return okResult("")
			}
			return okResult("sess-abc")
		},
		"System.describe": func(env envelope) any {
			sessionSeen.Store(env.Params[sessionParamKey])
			return okResult(map[string]any{"ok": true})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if c.SessionID() != "sess-abc" {
		t.Fatalf("session id=%q", c.SessionID())
	}
	if err := c.Call(context.Background(), "System.describe", nil, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if sessionSeen.Load() != "sess-abc" {
		t.Fatalf("server saw session=%v", sessionSeen.Load())
	}
}

func TestClient401MapsToAuthFailure(t *testing.T) {
	srv := newTestServer(t, map[string]func(envelope) any{
		"Anything": func(envelope) any { return http.StatusUnauthorized },
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	err := c.Call(context.Background(), "Anything", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("got %v, want ErrAuthFailure", err)
	}
	ctx, ok := hmerr.ErrorContext(err)
	if !ok || ctx.Method != "Anything" || ctx.Protocol != "json-rpc" {
		t.Fatalf("context=%+v ok=%v", ctx, ok)
	}
}

func TestClient500MapsToInternalBackend(t *testing.T) {
	srv := newTestServer(t, map[string]func(envelope) any{
		"Boom": func(envelope) any { return http.StatusInternalServerError },
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	err := c.Call(context.Background(), "Boom", nil, nil)
	if !errors.Is(err, hmerr.ErrInternalBackendException) {
		t.Fatalf("got %v, want ErrInternalBackendException", err)
	}
}

func TestClientJSONRPCErrorInternal(t *testing.T) {
	srv := newTestServer(t, map[string]func(envelope) any{
		"Boom": func(envelope) any {
			return response{Error: &wireError{Code: -32603, Message: "internal"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	err := c.Call(context.Background(), "Boom", nil, nil)
	if !errors.Is(err, hmerr.ErrInternalBackendException) {
		t.Fatalf("got %v", err)
	}
	var rpcErr *hmerr.JSONRPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want *hmerr.JSONRPCError, got %T", err)
	}
	if rpcErr.Code != -32603 {
		t.Fatalf("code=%d", rpcErr.Code)
	}
}

func TestClientJSONRPCErrorClient(t *testing.T) {
	srv := newTestServer(t, map[string]func(envelope) any{
		"Bad": func(envelope) any {
			return response{Error: &wireError{Code: -32600, Message: "invalid request"}}
		},
	})
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL})
	err := c.Call(context.Background(), "Bad", nil, nil)
	if !errors.Is(err, hmerr.ErrClientException) {
		t.Fatalf("got %v, want ErrClientException", err)
	}
}

func TestClientRetriesOn401AfterReLogin(t *testing.T) {
	var calls atomic.Int32
	// Server already accepts only "new"; the client-side "old" is stale.
	var currentSession atomic.Value
	currentSession.Store("new")
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			return okResult(currentSession.Load())
		},
		"Work": func(env envelope) any {
			calls.Add(1)
			session, _ := env.Params[sessionParamKey].(string)
			if session != currentSession.Load() {
				return http.StatusUnauthorized
			}
			return okResult(map[string]any{"ok": true})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	// Prime the client with an expired session without hitting
	// Session.login; mark it recently refreshed so the proactive renew
	// is skipped and the reactive HTTP-401 path is exercised.
	c.mu.Lock()
	c.sessionID = "old"
	c.lastSessionRefresh = time.Now()
	c.mu.Unlock()

	if err := c.Call(context.Background(), "Work", nil, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("Work should have been attempted twice, got %d", calls.Load())
	}
	if c.SessionID() != "new" {
		t.Fatalf("client did not refresh session, got %q", c.SessionID())
	}
}

// accessDenied is the CCU's session-invalid / privilege-mismatch reply:
// HTTP 200 with a JSON-RPC error object, code 400. An expired session and
// an under-privileged user are indistinguishable on the wire.
func accessDenied() response {
	return response{Error: &wireError{Code: 400, Message: "access denied", Data: `"ADMIN" needed 0`}}
}

func TestClientReloginOnJSONRPCAccessDenied(t *testing.T) {
	var workCalls, loginCalls atomic.Int32
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(envelope) any {
			loginCalls.Add(1)
			return okResult("new")
		},
		"Work": func(env envelope) any {
			workCalls.Add(1)
			if env.Params[sessionParamKey] != "new" {
				return accessDenied()
			}
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	// Prime an expired session; mark it recently refreshed so the
	// proactive renew is skipped and the reactive path is exercised.
	c.mu.Lock()
	c.sessionID = "old"
	c.lastSessionRefresh = time.Now()
	c.mu.Unlock()

	if err := c.Call(context.Background(), "Work", nil, nil); err != nil {
		t.Fatalf("Call must self-heal an expired session, got %v", err)
	}
	if workCalls.Load() != 2 {
		t.Fatalf("Work attempts=%d, want 2 (fail + retry)", workCalls.Load())
	}
	if loginCalls.Load() != 1 {
		t.Fatalf("login attempts=%d, want 1", loginCalls.Load())
	}
	if c.SessionID() != "new" {
		t.Fatalf("session=%q, want new", c.SessionID())
	}
}

// TestClientReloginOnJSONRPCSessionExpiredFamily pins that a session
// rejected with the JSON-RPC -32001/-32002 codes (godevccu's shape) is
// treated the same as the CCU's own code 400 — the client used to
// recognise only 400, so a backend using this family never re-logged in
// and kept sending the dead session id forever.
func TestClientReloginOnJSONRPCSessionExpiredFamily(t *testing.T) {
	for _, code := range []int{jsonRPCAuthRequiredCode, jsonRPCSessionExpiredCode} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			var workCalls, loginCalls atomic.Int32
			srv := newTestServer(t, map[string]func(envelope) any{
				"Session.login": func(envelope) any {
					loginCalls.Add(1)
					return okResult("new")
				},
				"Work": func(env envelope) any {
					workCalls.Add(1)
					if env.Params[sessionParamKey] != "new" {
						return response{Error: &wireError{Code: code, Message: "session expired"}}
					}
					return okResult(true)
				},
			})
			defer srv.Close()

			c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
			c.mu.Lock()
			c.sessionID = "old"
			c.lastSessionRefresh = time.Now()
			c.mu.Unlock()

			if err := c.Call(context.Background(), "Work", nil, nil); err != nil {
				t.Fatalf("Call must self-heal a session rejected with code %d, got %v", code, err)
			}
			if workCalls.Load() != 2 {
				t.Fatalf("Work attempts=%d, want 2 (fail + retry)", workCalls.Load())
			}
			if loginCalls.Load() != 1 {
				t.Fatalf("login attempts=%d, want 1", loginCalls.Load())
			}
			if c.SessionID() != "new" {
				t.Fatalf("session=%q, want new", c.SessionID())
			}
		})
	}
}

func TestClientAccessDeniedPersistsAfterRelogin(t *testing.T) {
	var workCalls, loginCalls atomic.Int32
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(envelope) any {
			loginCalls.Add(1)
			return okResult("fresh")
		},
		// Genuine privilege mismatch: denied regardless of session.
		"Work": func(envelope) any {
			workCalls.Add(1)
			return accessDenied()
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	c.mu.Lock()
	c.sessionID = "sess"
	c.lastSessionRefresh = time.Now()
	c.mu.Unlock()

	err := c.Call(context.Background(), "Work", nil, nil)
	if !errors.Is(err, hmerr.ErrPermissionDenied) {
		t.Fatalf("got %v, want ErrPermissionDenied", err)
	}
	if workCalls.Load() != 2 {
		t.Fatalf("Work attempts=%d, want exactly 2 (no retry loop)", workCalls.Load())
	}
	if loginCalls.Load() != 1 {
		t.Fatalf("login attempts=%d, want exactly 1", loginCalls.Load())
	}
	if c.SessionID() != "fresh" {
		t.Fatalf("the freshly obtained session must stay cached, got %q", c.SessionID())
	}
}

func TestClientAccessDeniedUnauthenticatedNoRelogin(t *testing.T) {
	var loginCalls atomic.Int32
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(envelope) any {
			loginCalls.Add(1)
			return okResult("x")
		},
		"Work": func(envelope) any { return accessDenied() },
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL}) // no credentials configured
	err := c.Call(context.Background(), "Work", nil, nil)
	if !errors.Is(err, hmerr.ErrPermissionDenied) {
		t.Fatalf("got %v, want ErrPermissionDenied", err)
	}
	if loginCalls.Load() != 0 {
		t.Fatalf("unauthenticated client must not attempt Session.login, saw %d", loginCalls.Load())
	}
}

func TestClientCallRenewsStaleSessionProactively(t *testing.T) {
	var renewSeen atomic.Value
	var workSeen atomic.Value
	var workCalls atomic.Int32
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.renew": func(env envelope) any {
			renewSeen.Store(env.Params[sessionParamKey])
			return okResult(true) // CCU extends the session in place
		},
		"Work": func(env envelope) any {
			workCalls.Add(1)
			workSeen.Store(env.Params[sessionParamKey])
			if env.Params[sessionParamKey] != "sess-1" {
				return accessDenied()
			}
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	// Session exists but its freshness window has long lapsed.
	c.mu.Lock()
	c.sessionID = "sess-1"
	c.mu.Unlock()

	if err := c.Call(context.Background(), "Work", nil, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if renewSeen.Load() != "sess-1" {
		t.Fatalf("renew saw %v, want sess-1", renewSeen.Load())
	}
	if workSeen.Load() != "sess-1" {
		t.Fatalf("Work saw %v, want sess-1 unchanged — Session.renew extends in place, it does not mint a new id", workSeen.Load())
	}
	if got := c.SessionID(); got != "sess-1" {
		t.Fatalf("SessionID() after renew = %q, want unchanged sess-1", got)
	}
	if workCalls.Load() != 1 {
		t.Fatalf("Work attempts=%d, want 1 (renewed up front, no retry)", workCalls.Load())
	}
}

func TestClientCallFallsBackToLoginWhenRenewRejected(t *testing.T) {
	var workSeen atomic.Value
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.renew": func(envelope) any { return okResult("") }, // CCU dropped the session
		"Session.login": func(envelope) any { return okResult("fresh") },
		"Work": func(env envelope) any {
			workSeen.Store(env.Params[sessionParamKey])
			if env.Params[sessionParamKey] != "fresh" {
				return accessDenied()
			}
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	c.mu.Lock()
	c.sessionID = "dead"
	c.mu.Unlock()

	if err := c.Call(context.Background(), "Work", nil, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if workSeen.Load() != "fresh" {
		t.Fatalf("Work saw %v, want the fresh login session", workSeen.Load())
	}
}

func TestClientCallSkipsRenewRightAfterLogin(t *testing.T) {
	var renewCalls atomic.Int32
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(envelope) any { return okResult("sess-1") },
		"Session.renew": func(envelope) any {
			renewCalls.Add(1)
			return okResult("sess-1")
		},
		"Work": func(envelope) any { return okResult(true) },
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(context.Background(), "Work", nil, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if renewCalls.Load() != 0 {
		t.Fatalf("a just-logged-in session must not be renewed again, saw %d renew calls", renewCalls.Load())
	}
}

func TestClientConnectionFailureMapsToNoConnection(t *testing.T) {
	c, _ := New(Config{Endpoint: "http://127.0.0.1:1"}) // reserved port, connect should fail
	err := c.Call(context.Background(), "x", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, hmerr.ErrNoConnection) {
		t.Fatalf("got %v, want ErrNoConnection", err)
	}
}

func TestClientRespectsContextCancellation(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-block
	}))
	defer srv.Close()
	defer close(block)

	c, _ := New(Config{Endpoint: srv.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.Call(ctx, "x", nil, nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	// context cancellation should surface as ErrNoConnection per our mapping
	// rule ("dial/round-trip could not complete"), not as ErrAuthFailure.
	if errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("cancellation must not classify as ErrAuthFailure, got %v", err)
	}
}

func TestClientConcurrencyCap(t *testing.T) {
	var inflight atomic.Int32
	var peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		inflight.Add(-1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"result":true}`)
	}))
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, MaxConcurrent: 2})
	done := make(chan error, 8)
	for range 8 {
		go func() {
			done <- c.Call(context.Background(), "Ping", nil, nil)
		}()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Fatalf("Call: %v", err)
		}
	}
	if peak.Load() > 2 {
		t.Fatalf("peak concurrency %d exceeded cap 2", peak.Load())
	}
}

func TestNewRejectsMissingEndpoint(t *testing.T) {
	if _, err := New(Config{}); err == nil || !strings.Contains(err.Error(), "Endpoint") {
		t.Fatalf("got %v, want endpoint error", err)
	}
}

func TestClientRenewExtendsSession(t *testing.T) {
	var renewSeen atomic.Value
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(envelope) any {
			return okResult("sess-1")
		},
		"Session.renew": func(env envelope) any {
			renewSeen.Store(env.Params[sessionParamKey])
			return okResult(true) // CCU extends the session in place, no new id
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.SessionID() != "sess-1" {
		t.Fatalf("post-login session=%q", c.SessionID())
	}
	// Age the freshness stamp so Renew performs a real round-trip.
	c.mu.Lock()
	c.lastSessionRefresh = time.Time{}
	c.mu.Unlock()
	if err := c.Renew(context.Background()); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if renewSeen.Load() != "sess-1" {
		t.Fatalf("server saw renew session=%v want sess-1", renewSeen.Load())
	}
	if c.SessionID() != "sess-1" {
		t.Fatalf("post-renew session=%q want unchanged sess-1 — Session.renew does not mint a new id", c.SessionID())
	}
}

func TestClientRenewNoOpWhenLoggedOut(t *testing.T) {
	called := atomic.Bool{}
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.renew": func(envelope) any {
			called.Store(true)
			return okResult("sess-x")
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.Renew(context.Background()); err != nil {
		t.Fatalf("Renew with no session: %v", err)
	}
	if called.Load() {
		t.Fatal("server must not see Session.renew when no session is active")
	}
}

// The CCU signals a dropped session by answering Session.renew with the
// boolean false (never an empty result) — the client must treat that as an
// auth failure and clear the cached session id so the next call re-logs in
// rather than keep sending a session the CCU has already discarded.
func TestClientRenewFalseInvalidatesSession(t *testing.T) {
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(envelope) any { return okResult("sess-1") },
		"Session.renew": func(envelope) any { return okResult(false) },
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Age the freshness stamp so Renew performs a real round-trip.
	c.mu.Lock()
	c.lastSessionRefresh = time.Time{}
	c.mu.Unlock()
	err := c.Renew(context.Background())
	if err == nil {
		t.Fatal("Session.renew=false must surface as auth failure")
	}
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("Renew error = %v, want it to wrap hmerr.ErrAuthFailure", err)
	}
	if c.SessionID() != "" {
		t.Fatalf("session id must be cleared after a rejected renew, got %q", c.SessionID())
	}
}
