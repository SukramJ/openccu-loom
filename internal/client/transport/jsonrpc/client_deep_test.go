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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestJSONRPCLoginSucceedsAndSetsSessionID verifies that a successful
// Session.login response is stored in SessionID().
func TestJSONRPCLoginSucceedsAndSetsSessionID(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			return okResult("abc123")
		},
	})
	defer srv.Close()

	c, err := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := c.SessionID(); got != "abc123" {
		t.Fatalf("SessionID() = %q, want %q", got, "abc123")
	}
}

// TestJSONRPCLogoutClearsSession verifies that after a successful Logout
// the session ID is empty.
func TestJSONRPCLogoutClearsSession(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			return okResult("tok-42")
		},
		"Session.logout": func(env envelope) any {
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if c.SessionID() == "" {
		t.Fatal("expected non-empty session after login")
	}
	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if got := c.SessionID(); got != "" {
		t.Fatalf("SessionID() after logout = %q, want empty", got)
	}
}

// TestJSONRPCLogoutWithoutLoginIsNoop verifies that calling Logout with no
// active session neither errors nor contacts the server.
func TestJSONRPCLogoutWithoutLoginIsNoop(t *testing.T) {
	t.Parallel()
	var called atomic.Bool
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.logout": func(env envelope) any {
			called.Store(true)
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("Logout without session returned error: %v", err)
	}
	if called.Load() {
		t.Fatal("server must not be contacted when no session is active")
	}
}

// TestJSONRPCRenewExtendsSession verifies that Renew keeps SessionID()
// unchanged when the CCU answers with the boolean true — Session.renew
// extends the session in place, it never mints a new id.
func TestJSONRPCRenewExtendsSession(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			return okResult("initial-session")
		},
		"Session.renew": func(env envelope) any {
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	// Age the freshness stamp so Renew performs a real round-trip.
	c.mu.Lock()
	c.lastSessionRefresh = time.Time{}
	c.mu.Unlock()
	if err := c.Renew(context.Background()); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if got := c.SessionID(); got != "initial-session" {
		t.Fatalf("SessionID() after renew = %q, want unchanged %q", got, "initial-session")
	}
}

// TestJSONRPCCallInjectsSessionParam verifies that after login, the
// session key (_session_id_) is present in the request params.
func TestJSONRPCCallInjectsSessionParam(t *testing.T) {
	t.Parallel()
	var capturedSession atomic.Value
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			return okResult("session-xyz")
		},
		"Device.list": func(env envelope) any {
			capturedSession.Store(env.Params[sessionParamKey])
			return okResult([]string{"device1"})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := c.Call(context.Background(), "Device.list", nil, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	got, _ := capturedSession.Load().(string)
	if got != "session-xyz" {
		t.Fatalf("server saw session param %q, want %q", got, "session-xyz")
	}
}

// TestJSONRPCCallWithoutLoginOmitsSession verifies that an unauthenticated
// client does not inject a session key into request params.
func TestJSONRPCCallWithoutLoginOmitsSession(t *testing.T) {
	t.Parallel()
	var sessionKeyPresent atomic.Bool
	srv := newTestServer(t, map[string]func(envelope) any{
		"Device.list": func(env envelope) any {
			_, ok := env.Params[sessionParamKey]
			sessionKeyPresent.Store(ok)
			return okResult([]string{})
		},
	})
	defer srv.Close()

	// No Username — session-less client.
	c, _ := New(Config{Endpoint: srv.URL})
	if err := c.Call(context.Background(), "Device.list", nil, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if sessionKeyPresent.Load() {
		t.Fatal("unauthenticated call must not include session param")
	}
}

// TestJSONRPCCallReauthOnAuthFailure verifies that the client transparently
// re-logs-in and retries once when the server returns HTTP 401 on the first
// attempt.
func TestJSONRPCCallReauthOnAuthFailure(t *testing.T) {
	t.Parallel()
	var workCalls atomic.Int32
	var currentSession atomic.Value
	currentSession.Store("fresh")

	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			return okResult(currentSession.Load())
		},
		"Work.do": func(env envelope) any {
			n := workCalls.Add(1)
			if n == 1 {
				// First attempt: reject with 401
				return http.StatusUnauthorized
			}
			// Second attempt: succeed
			return okResult("done")
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	// Pre-seed a stale session so the first Work.do hits the 401 branch.
	c.mu.Lock()
	c.sessionID = "stale"
	c.mu.Unlock()

	var result string
	if err := c.Call(context.Background(), "Work.do", nil, &result); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if workCalls.Load() != 2 {
		t.Fatalf("Work.do should be called twice (original + retry), got %d", workCalls.Load())
	}
	if result != "done" {
		t.Fatalf("result = %q, want %q", result, "done")
	}
}

// TestJSONRPCCallSecondAuthFailureDoesNotLoopForever verifies that when the
// server returns 401 on both the original call and the retry after re-login,
// the client surfaces an error without looping.
func TestJSONRPCCallSecondAuthFailureDoesNotLoopForever(t *testing.T) {
	t.Parallel()
	var workCalls atomic.Int32

	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			return okResult("new-sess")
		},
		"Work.do": func(env envelope) any {
			workCalls.Add(1)
			return http.StatusUnauthorized
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	err := c.Call(context.Background(), "Work.do", nil, nil)
	if err == nil {
		t.Fatal("expected error on persistent 401")
	}
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("got %v, want ErrAuthFailure", err)
	}
	// Must be called at most twice: original attempt + one retry.
	if got := workCalls.Load(); got > 2 {
		t.Fatalf("Work.do called %d times, expected at most 2 (no infinite loop)", got)
	}
}

// TestJSONRPCCallNon2xxStatusReturnsError verifies that a 500 response
// surfaces as an error whose message contains the method name.
func TestJSONRPCCallNon2xxStatusReturnsError(t *testing.T) {
	t.Parallel()
	const method = "Boom.explode"
	srv := newTestServer(t, map[string]func(envelope) any{
		method: func(env envelope) any {
			return http.StatusInternalServerError
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	err := c.Call(context.Background(), method, nil, nil)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), method) {
		t.Fatalf("error %q does not contain method name %q", err.Error(), method)
	}
}

// TestJSONRPCCallContextCancelAborts verifies that a request cancelled via
// context is surfaced as a context-related error (not auth failure).
func TestJSONRPCCallContextCancelAborts(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer srv.Close()
	defer close(block)

	c, _ := New(Config{Endpoint: srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the request aborts.
	cancel()

	err := c.Call(ctx, "Slow.method", nil, nil)
	if err == nil {
		t.Fatal("expected error after context cancel")
	}
	// Must NOT look like an auth failure.
	if errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("cancelled request must not be ErrAuthFailure, got %v", err)
	}
	// The error should indicate context cancellation somewhere in the chain.
	if !errors.Is(err, context.Canceled) && !errors.Is(err, hmerr.ErrNoConnection) {
		t.Fatalf("expected Canceled or ErrNoConnection, got %v", err)
	}
}

// TestJSONRPCCallMalformedJSONReturnsError verifies that a non-JSON body
// from the server surfaces as a non-nil error.
func TestJSONRPCCallMalformedJSONReturnsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "not-json-at-all")
	}))
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	err := c.Call(context.Background(), "Broken.method", nil, nil)
	if err == nil {
		t.Fatal("expected error for malformed JSON response")
	}
}

// TestJSONRPCCallServerErrorObjectIsReturned verifies that a JSON-RPC
// error object (non-nil "error" field) surfaces as a non-nil error
// whose message contains the server error message.
func TestJSONRPCCallServerErrorObjectIsReturned(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Crash.now": func(env envelope) any {
			return response{Error: &wireError{Code: 1, Message: "boom"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	err := c.Call(context.Background(), "Crash.now", nil, nil)
	if err == nil {
		t.Fatal("expected error from server error object")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error %q does not contain server message %q", err.Error(), "boom")
	}
}

// TestJSONRPCSemaphoreSerializesConcurrentCalls verifies that with
// MaxConcurrent=1 at most one request is in-flight at any point.
func TestJSONRPCSemaphoreSerializesConcurrentCalls(t *testing.T) {
	t.Parallel()
	var inflight atomic.Int32
	var violation atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inflight.Add(1)
		if n > 1 {
			violation.Store(true)
		}
		time.Sleep(5 * time.Millisecond)
		inflight.Add(-1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"result":"ok"}`)
	}))
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, MaxConcurrent: 1})

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan error, goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			errs <- c.Call(context.Background(), "Ping", nil, nil)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
	}
	if violation.Load() {
		t.Fatal("more than 1 concurrent request observed with MaxConcurrent=1")
	}
}

// TestJSONRPCInvalidateSessionForcesReloginOnNextCall verifies that after
// the session is invalidated (via auth-fail path), the next Call triggers
// a fresh login.
func TestJSONRPCInvalidateSessionForcesReloginOnNextCall(t *testing.T) {
	t.Parallel()
	var loginCalls atomic.Int32
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			loginCalls.Add(1)
			return okResult("fresh-token")
		},
		"Work.do": func(env envelope) any {
			return okResult("result")
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	// Explicitly invalidate the session.
	c.invalidateSession()
	if c.SessionID() != "" {
		t.Fatal("expected empty session after invalidateSession()")
	}

	// Now seed a stale session and invalidate, then call — relogin must happen
	// via the allowRetry path when a 401 is returned.
	// For this test: just do a clean call after invalidation with no pre-existing
	// session (Username set, so Login() is a no-op only when username is empty).
	// The client will not auto-login unless it hits a 401, so let's trigger that.
	c.mu.Lock()
	c.sessionID = "stale-to-invalidate"
	c.mu.Unlock()

	// Force a 401 on first Work.do, then success after re-login.
	var workN atomic.Int32
	srv2 := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			loginCalls.Add(1)
			return okResult("fresh-token-2")
		},
		"Work.do": func(env envelope) any {
			n := workN.Add(1)
			if n == 1 {
				return http.StatusUnauthorized
			}
			return okResult("done")
		},
	})
	defer srv2.Close()

	c2, _ := New(Config{Endpoint: srv2.URL, Username: "u", Password: "p"})
	c2.mu.Lock()
	c2.sessionID = "stale"
	c2.mu.Unlock()

	if err := c2.Call(context.Background(), "Work.do", nil, nil); err != nil {
		t.Fatalf("Call after stale session: %v", err)
	}
	if c2.SessionID() != "fresh-token-2" {
		t.Fatalf("SessionID() = %q, want %q", c2.SessionID(), "fresh-token-2")
	}
}

// TestJSONRPCCallOutNilWorks verifies that passing nil as the out parameter
// does not panic and does not return an error on a successful call.
func TestJSONRPCCallOutNilWorks(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Ping": func(env envelope) any {
			return okResult(map[string]any{"pong": true})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	// Must not panic and must return nil error.
	if err := c.Call(context.Background(), "Ping", nil, nil); err != nil {
		t.Fatalf("Call with nil out returned error: %v", err)
	}
}

// TestJSONRPCWrapsErrorWithMethodContext verifies that every error returned
// by Call includes the method name somewhere in its string representation.
func TestJSONRPCWrapsErrorWithMethodContext(t *testing.T) {
	t.Parallel()
	const method = "My.SpecialMethod"
	srv := newTestServer(t, map[string]func(envelope) any{
		method: func(env envelope) any {
			return http.StatusInternalServerError
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	err := c.Call(context.Background(), method, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), method) {
		t.Fatalf("error %q does not contain method name %q", err.Error(), method)
	}
	// Validate via hmerr.ErrorContext that the Context.Method is also set.
	ctx, ok := hmerr.ErrorContext(err)
	if !ok {
		t.Fatalf("hmerr.ErrorContext returned false for error: %v", err)
	}
	if ctx.Method != method {
		t.Fatalf("ErrorContext.Method = %q, want %q", ctx.Method, method)
	}
	if ctx.Protocol != "json-rpc" {
		t.Fatalf("ErrorContext.Protocol = %q, want %q", ctx.Protocol, "json-rpc")
	}
}

// TestJSONRPCRequestBodyIsValidJSON verifies that the request body sent
// by the client is valid JSON with the expected structure.
func TestJSONRPCRequestBodyIsValidJSON(t *testing.T) {
	t.Parallel()
	var capturedBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody.Store(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"result":"ok"}`)
	}))
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	params := map[string]any{"key": "value", "num": 42}
	if err := c.Call(context.Background(), "Test.method", params, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}

	rawBody, _ := capturedBody.Load().([]byte)
	var parsed map[string]any
	if err := json.Unmarshal(rawBody, &parsed); err != nil {
		t.Fatalf("request body is not valid JSON: %v — body: %s", err, rawBody)
	}
	if parsed["method"] != "Test.method" {
		t.Fatalf("request method = %v, want %q", parsed["method"], "Test.method")
	}
	bodyParams, _ := parsed["params"].(map[string]any)
	if bodyParams["key"] != "value" {
		t.Fatalf("request param key = %v, want %q", bodyParams["key"], "value")
	}
}

// TestJSONRPCLoginEmptyUsernameIsNoop verifies that Login with an empty
// username is a no-op (no session ID is set, no server call is made).
func TestJSONRPCLoginEmptyUsernameIsNoop(t *testing.T) {
	t.Parallel()
	var loginCalled atomic.Bool
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			loginCalled.Store(true)
			return okResult("should-not-be-set")
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL}) // no Username
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login with empty username: %v", err)
	}
	if loginCalled.Load() {
		t.Fatal("Session.login must not be called when username is empty")
	}
	if c.SessionID() != "" {
		t.Fatalf("SessionID() = %q, expected empty when username is blank", c.SessionID())
	}
}

// TestJSONRPCLoginEmptySessionIDFromServerFails verifies that when the
// server returns an empty string for Session.login, Login returns ErrAuthFailure.
func TestJSONRPCLoginEmptySessionIDFromServerFails(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			return okResult("") // server refuses — empty session
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "wrong"})
	err := c.Login(context.Background())
	if err == nil {
		t.Fatal("expected error when server returns empty session ID")
	}
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("got %v, want ErrAuthFailure", err)
	}
}

// TestJSONRPCCallParamsPassedThrough verifies that custom params given to
// Call arrive at the server.
func TestJSONRPCCallParamsPassedThrough(t *testing.T) {
	t.Parallel()
	var receivedParams atomic.Value
	srv := newTestServer(t, map[string]func(envelope) any{
		"Echo": func(env envelope) any {
			receivedParams.Store(env.Params)
			return okResult("echoed")
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	params := map[string]any{"hello": "world", "answer": float64(42)}
	if err := c.Call(context.Background(), "Echo", params, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	got, _ := receivedParams.Load().(map[string]any)
	if got["hello"] != "world" {
		t.Fatalf("param hello = %v, want %q", got["hello"], "world")
	}
	if got["answer"] != float64(42) {
		t.Fatalf("param answer = %v, want 42", got["answer"])
	}
}

// TestJSONRPCCallDecodeResultIntoOut verifies that the result from the
// server is correctly decoded into the out pointer.
func TestJSONRPCCallDecodeResultIntoOut(t *testing.T) {
	t.Parallel()
	type sysInfo struct {
		Version string `json:"version"`
		Build   int    `json:"build"`
	}
	srv := newTestServer(t, map[string]func(envelope) any{
		"System.getInfo": func(env envelope) any {
			return okResult(sysInfo{Version: "3.65.10", Build: 1234})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	var out sysInfo
	if err := c.Call(context.Background(), "System.getInfo", nil, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.Version != "3.65.10" {
		t.Fatalf("Version = %q, want %q", out.Version, "3.65.10")
	}
	if out.Build != 1234 {
		t.Fatalf("Build = %d, want %d", out.Build, 1234)
	}
}

// TestJSONRPCCallForbiddenTriggersAuthFailure verifies that HTTP 403
// maps to ErrAuthFailure (same as 401).
func TestJSONRPCCallForbiddenTriggersAuthFailure(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Admin.op": func(env envelope) any {
			return http.StatusForbidden
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	err := c.Call(context.Background(), "Admin.op", nil, nil)
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("got %v, want ErrAuthFailure", err)
	}
}

// TestJSONRPCHostDefaultsToEndpoint verifies that when Host is empty in
// Config, the client uses Endpoint as the host in error contexts.
func TestJSONRPCHostDefaultsToEndpoint(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Fail": func(env envelope) any {
			return http.StatusInternalServerError
		},
	})
	defer srv.Close()

	// No Host set — should fall back to srv.URL.
	c, _ := New(Config{Endpoint: srv.URL})
	err := c.Call(context.Background(), "Fail", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	errCtx, ok := hmerr.ErrorContext(err)
	if !ok {
		t.Fatalf("expected ContextualError, got %v", err)
	}
	if errCtx.Host != srv.URL {
		t.Fatalf("Host = %q, want %q (Endpoint fallback)", errCtx.Host, srv.URL)
	}
}

// TestJSONRPCCustomHostAppearsInErrorContext verifies that a custom Host
// in Config appears in error context rather than the endpoint URL.
func TestJSONRPCCustomHostAppearsInErrorContext(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Op": func(env envelope) any {
			return http.StatusInternalServerError
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Host: "my-ccu.local"})
	err := c.Call(context.Background(), "Op", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	errCtx, ok := hmerr.ErrorContext(err)
	if !ok {
		t.Fatalf("expected ContextualError, got %v", err)
	}
	if errCtx.Host != "my-ccu.local" {
		t.Fatalf("Host = %q, want %q", errCtx.Host, "my-ccu.local")
	}
}

// ---------------------------------------------------------------------------
// IsActivated
// ---------------------------------------------------------------------------

// TestIsActivatedFalseWhenNoSession verifies that a fresh, unauthenticated
// Client reports IsActivated == false (mirrors property returning false when
// session_id == "").
func TestIsActivatedFalseWhenNoSession(t *testing.T) {
	t.Parallel()
	c, _ := New(Config{Endpoint: "http://localhost:9999"})
	if c.IsActivated() {
		t.Error("IsActivated() = true for unauthenticated client; want false")
	}
}

// TestIsActivatedTrueAfterSuccessfulLogin verifies that IsActivated becomes
// true after Login succeeds (session ID is stored).
func TestIsActivatedTrueAfterSuccessfulLogin(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(env envelope) any {
			return okResult("abc123session")
		},
	})
	defer srv.Close()

	c, _ := New(Config{
		Endpoint: srv.URL,
		Username: "admin",
		Password: "secret",
	})
	if c.IsActivated() {
		t.Error("IsActivated() = true before Login; want false")
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login() unexpected error: %v", err)
	}
	if !c.IsActivated() {
		t.Error("IsActivated() = false after successful Login; want true")
	}
}

// ---------------------------------------------------------------------------
// CheckSupportedMethods / IsMethodSupported
// ---------------------------------------------------------------------------

// TestClientCheckSupportedMethodsObjectShape verifies that
// CheckSupportedMethods parses a CCU-style []map[string]any result (each
// entry carries a "name" field).
func TestClientCheckSupportedMethodsObjectShape(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"system.listMethods": func(_ envelope) any {
			return okResult([]map[string]any{
				{"name": "Interface.getValue"},
				{"name": "Interface.putParamset"},
				{"name": "Session.login"},
			})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.CheckSupportedMethods(context.Background())
	if err != nil {
		t.Fatalf("CheckSupportedMethods() unexpected error: %v", err)
	}
	for _, want := range []string{"Interface.getValue", "Interface.putParamset", "Session.login"} {
		if !got[want] {
			t.Errorf("CheckSupportedMethods(): method %q missing from result", want)
		}
	}
}

// TestClientCheckSupportedMethodsStringShape verifies that
// CheckSupportedMethods also handles a plain []string result (some
// gateways return bare strings, not objects).
func TestClientCheckSupportedMethodsStringShape(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"system.listMethods": func(_ envelope) any {
			return okResult([]string{"Session.login", "Interface.getValue"})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	got, err := c.CheckSupportedMethods(context.Background())
	if err != nil {
		t.Fatalf("CheckSupportedMethods() unexpected error: %v", err)
	}
	if !got["Session.login"] {
		t.Error("CheckSupportedMethods(): Session.login missing")
	}
}

// TestClientCheckSupportedMethodsCachesResult verifies that a second call
// to CheckSupportedMethods does not issue another HTTP request (result is
// cached after the first probe).
func TestClientCheckSupportedMethodsCachesResult(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := newTestServer(t, map[string]func(envelope) any{
		"system.listMethods": func(_ envelope) any {
			calls.Add(1)
			return okResult([]string{"Interface.getValue"})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.CheckSupportedMethods(context.Background()); err != nil {
		t.Fatalf("first CheckSupportedMethods: %v", err)
	}
	if _, err := c.CheckSupportedMethods(context.Background()); err != nil {
		t.Fatalf("second CheckSupportedMethods: %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("system.listMethods called %d times; want 1 (cached)", n)
	}
}

// TestCheckSupportedMethodsError verifies that when system.listMethods fails,
// CheckSupportedMethods returns an error AND caches an empty set so subsequent
// calls skip the HTTP round-trip.
func TestCheckSupportedMethodsError(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := newTestServer(t, map[string]func(envelope) any{
		"system.listMethods": func(_ envelope) any {
			calls.Add(1)
			return response{Error: &wireError{Code: -32603, Message: "not supported"}}
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	m, err := c.CheckSupportedMethods(context.Background())
	if err == nil {
		t.Fatal("expected error from CheckSupportedMethods when listMethods fails")
	}
	if len(m) != 0 {
		t.Fatalf("expected empty map on error, got %v", m)
	}
	// Second call must use the cache.
	_, _ = c.CheckSupportedMethods(context.Background())
	if n := calls.Load(); n != 1 {
		t.Fatalf("system.listMethods called %d times, want 1 (cached after error)", n)
	}
}

// TestIsMethodSupportedOptimisticBeforeProbe verifies that IsMethodSupported
// returns true (optimistic) before CheckSupportedMethods has run.
func TestIsMethodSupportedOptimisticBeforeProbe(t *testing.T) {
	t.Parallel()
	c, _ := New(Config{Endpoint: "http://localhost:9999"})
	if !c.IsMethodSupported("anything") {
		t.Error("IsMethodSupported() = false before probe; want true (optimistic)")
	}
}

// TestIsMethodSupportedFalseWhenAbsent verifies that after
// CheckSupportedMethods has populated the cache, IsMethodSupported returns
// false for a method not in the list.
func TestIsMethodSupportedFalseWhenAbsent(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"system.listMethods": func(_ envelope) any {
			return okResult([]string{"Session.login"})
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	if _, err := c.CheckSupportedMethods(context.Background()); err != nil {
		t.Fatalf("CheckSupportedMethods: %v", err)
	}
	if c.IsMethodSupported("Interface.deleteDevice") {
		t.Error("IsMethodSupported(Interface.deleteDevice) = true; want false (not in list)")
	}
	if !c.IsMethodSupported("Session.login") {
		t.Error("IsMethodSupported(Session.login) = false; want true")
	}
}

// ---------------------------------------------------------------------------
// Renew session freshness guard
// ---------------------------------------------------------------------------

// TestRenewSkipsWhenRecentlyRefreshed verifies that a second Renew call
// within jsonSessionAge does NOT issue an HTTP request to the CCU.
func TestRenewSkipsWhenRecentlyRefreshed(t *testing.T) {
	t.Parallel()
	var renewCalls atomic.Int32
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(_ envelope) any {
			return okResult("sess-fresh")
		},
		"Session.renew": func(_ envelope) any {
			renewCalls.Add(1)
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	// Login stamps freshness; age it so the first Renew is a real round-trip.
	c.mu.Lock()
	c.lastSessionRefresh = time.Time{}
	c.mu.Unlock()

	// First Renew — must hit the server.
	if err := c.Renew(context.Background()); err != nil {
		t.Fatalf("first Renew: %v", err)
	}
	if n := renewCalls.Load(); n != 1 {
		t.Fatalf("after first Renew: server calls = %d, want 1", n)
	}

	// Second Renew within jsonSessionAge — must be skipped.
	if err := c.Renew(context.Background()); err != nil {
		t.Fatalf("second Renew: %v", err)
	}
	if n := renewCalls.Load(); n != 1 {
		t.Errorf("after second Renew: server calls = %d, want still 1 (freshness guard)", n)
	}
}

// TestRenewIssuedAfterSessionAgeExpiry verifies that once the freshness
// window has elapsed, Renew sends another HTTP request. We inject a
// tiny artificial age to avoid real time.Sleep in the test.
func TestRenewIssuedAfterSessionAgeExpiry(t *testing.T) {
	t.Parallel()
	var renewCalls atomic.Int32
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(_ envelope) any {
			return okResult("sess-abc")
		},
		"Session.renew": func(_ envelope) any {
			renewCalls.Add(1)
			return okResult(true)
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	// Login stamps freshness; age it so the first Renew is a real round-trip.
	c.mu.Lock()
	c.lastSessionRefresh = time.Time{}
	c.mu.Unlock()

	// First Renew populates lastSessionRefresh.
	if err := c.Renew(context.Background()); err != nil {
		t.Fatalf("first Renew: %v", err)
	}

	// Back-date lastSessionRefresh beyond jsonSessionAge so the next
	// Renew call is forced to hit the server.
	c.mu.Lock()
	c.lastSessionRefresh = time.Now().Add(-(jsonSessionAge + time.Second))
	c.mu.Unlock()

	// Second Renew — freshness guard should NOT fire; server must be hit.
	if err := c.Renew(context.Background()); err != nil {
		t.Fatalf("second Renew after expiry: %v", err)
	}
	if n := renewCalls.Load(); n != 2 {
		t.Errorf("after expiry Renew: server calls = %d, want 2", n)
	}
}

// TestRenewCallOnceError verifies that when Session.renew itself returns an
// error (e.g. HTTP 500), Renew propagates the error to the caller.
func TestRenewCallOnceError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(_ envelope) any { return okResult("sess-1") },
		"Session.renew": func(_ envelope) any {
			return http.StatusInternalServerError
		},
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	// Login stamps freshness; age it so Renew is a real round-trip.
	c.mu.Lock()
	c.lastSessionRefresh = time.Time{}
	c.mu.Unlock()
	if err := c.Renew(context.Background()); err == nil {
		t.Fatal("expected error when Session.renew returns 500")
	}
}

// ---------------------------------------------------------------------------
// callOnce error branches
// ---------------------------------------------------------------------------

// TestCallOnceClientErrorStatus verifies that a 4xx response that is neither
// 401 nor 403 surfaces as ErrClientException.
func TestCallOnceClientErrorStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest) // 400
	}))
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	err := c.Call(context.Background(), "Any.method", nil, nil)
	if err == nil {
		t.Fatal("expected error for 400")
	}
	if !errors.Is(err, hmerr.ErrClientException) {
		t.Fatalf("got %v, want ErrClientException", err)
	}
}

// TestCallOnceDecodeResultError verifies the "decode result" branch: the CCU
// returns HTTP 200 with a valid JSON-RPC envelope whose "result" field cannot
// be decoded into the target type.
func TestCallOnceDecodeResultError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// result is a JSON string but we will ask the client to decode it
		// into an integer — that mismatch forces a decode error.
		_, _ = io.WriteString(w, `{"result":"not-an-int"}`)
	}))
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	var out int
	err := c.Call(context.Background(), "Any.method", nil, &out)
	if err == nil {
		t.Fatal("expected decode-result error")
	}
}

// TestCallOnceReadBodyError verifies the "read response" branch: the handler
// abruptly closes the connection after writing the status line so
// io.ReadAll fails.
func TestCallOnceReadBodyError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Declare a huge Content-Length but then hijack and close the connection
		// so io.ReadAll sees an unexpected EOF.
		w.Header().Set("Content-Length", "999999")
		w.WriteHeader(http.StatusOK)
		// Flush & immediately close (the hijack path).
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		// Close the underlying connection by panicking with ErrAbortHandler.
		panic(http.ErrAbortHandler)
	}))
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL})
	var out string
	err := c.Call(context.Background(), "Any.method", nil, &out)
	// The error may surface as ErrNoConnection (transport reset) or as a
	// generic I/O error; what matters is that it is non-nil.
	if err == nil {
		t.Fatal("expected error when body read fails")
	}
}

// ---------------------------------------------------------------------------
// Login backoff and error paths
// ---------------------------------------------------------------------------

// TestLoginBackoffContextCancelledDuringWait verifies that Login honours an
// already-cancelled context even while a backoff from a prior failure is
// pending — it must surface the context error, not silently fail-fast with
// an auth error that could be mistaken for a fresh rejection.
func TestLoginBackoffContextCancelledDuringWait(t *testing.T) {
	t.Parallel()
	// We need a server that the client will never reach in this test (the
	// backoff check runs before any HTTP round-trip).
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(_ envelope) any { return okResult("session") },
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})

	// Manually set the client into the "too many failures" state with a long
	// backoff so the pending-backoff path is exercised.
	c.mu.Lock()
	c.failedLoginAttempts = loginMaxFailedAttempts
	c.currentBackoff = 10 * time.Second
	c.nextLoginAttempt = time.Now().Add(c.currentBackoff)
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.Login(ctx)
	if err == nil {
		t.Fatal("expected error when context is already cancelled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected a context-derived error, got %v", err)
	}
}

// TestLoginBackoffAppliesFromTheFirstFailure verifies that a pending
// backoff makes the next Login call fail fast even when far fewer than
// loginMaxFailedAttempts have accumulated — the very first rejected login
// must already gate the second one, not just the eleventh. Gating the
// wait on loginMaxFailedAttempts let the first ten rejected logins fire
// back-to-back with no delay at all, which is what keeps an exhausted CCU
// session pool exhausted. The gate itself must never block a caller —
// Login is reached under sessionLoginMu, and blocking here would
// serialize every other JSON-RPC call on the central behind the wait.
func TestLoginBackoffAppliesFromTheFirstFailure(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(_ envelope) any { return okResult("session") },
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "p"})

	const backoff = 80 * time.Millisecond
	c.mu.Lock()
	c.failedLoginAttempts = 1 // one failure — nowhere near loginMaxFailedAttempts
	c.currentBackoff = backoff
	c.nextLoginAttempt = time.Now().Add(backoff)
	c.mu.Unlock()

	start := time.Now()
	err := c.Login(context.Background())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected Login to fail fast on the pending backoff")
	}
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("expected ErrAuthFailure for a pending backoff, got %v", err)
	}
	if elapsed >= backoff {
		t.Fatalf("Login blocked for %v waiting out the pending backoff of %v — it must fail fast instead", elapsed, backoff)
	}
}

// TestLoginNetworkErrorResetsBackoff verifies that a network error during
// Session.login (callOnce fails) does NOT increment the failed-login counter
// (it's a transient outage, not a credentials failure).
func TestLoginNetworkErrorResetsBackoff(t *testing.T) {
	t.Parallel()
	// Port 1 is reserved; connection will fail immediately.
	c, _ := New(Config{Endpoint: "http://127.0.0.1:1", Username: "u", Password: "p"})
	err := c.Login(context.Background())
	if err == nil {
		t.Fatal("expected network error")
	}
	// Must NOT be ErrAuthFailure.
	if errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("network error must not surface as ErrAuthFailure, got %v", err)
	}
	// Failed-login counter must not have been incremented.
	c.mu.Lock()
	count := c.failedLoginAttempts
	c.mu.Unlock()
	if count != 0 {
		t.Fatalf("failedLoginAttempts=%d after network error, want 0", count)
	}
}

// TestLoginBackoffDoublesOnRepeatedFailure verifies that currentBackoff doubles
// after each empty-session-id response.
func TestLoginBackoffDoublesOnRepeatedFailure(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, map[string]func(envelope) any{
		"Session.login": func(_ envelope) any { return okResult("") }, // always refuse
	})
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, Username: "u", Password: "wrong"})

	// First failure: backoff goes from 0 → loginBaseBackoff.
	_ = c.Login(context.Background())
	c.mu.Lock()
	b1 := c.currentBackoff
	c.mu.Unlock()
	if b1 != loginBaseBackoff {
		t.Fatalf("after 1st failure: backoff=%v, want %v", b1, loginBaseBackoff)
	}

	// The pending-backoff deadline from the first failure would otherwise
	// make this second call fail fast without ever reaching the server —
	// clear it to simulate the deadline having elapsed, the same as a
	// caller arriving later in real time would see.
	c.mu.Lock()
	c.nextLoginAttempt = time.Time{}
	c.mu.Unlock()

	// Second failure: backoff doubles.
	_ = c.Login(context.Background())
	c.mu.Lock()
	b2 := c.currentBackoff
	c.mu.Unlock()
	want2 := time.Duration(float64(loginBaseBackoff) * loginBackoffMultiplier)
	if b2 != want2 {
		t.Fatalf("after 2nd failure: backoff=%v, want %v", b2, want2)
	}
}
