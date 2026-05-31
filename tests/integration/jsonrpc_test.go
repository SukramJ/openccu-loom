// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// newJSONRPCClient constructs a JSON-RPC client pointed at endpoint with
// the given credentials. Auth fields may be empty for unauthenticated access.
func newJSONRPCClient(t *testing.T, endpoint, username, password string) *jsonrpc.Client {
	t.Helper()
	c, err := jsonrpc.New(jsonrpc.Config{
		Endpoint: endpoint,
		Username: username,
		Password: password,
	})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	return c
}

// ctx5sJSON is a helper identical to ctx5s but kept local so the two test
// files compile independently without a name clash.
func ctx5sJSON(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// ─────────────────────────────────────────────────────────────────────────────
// Session lifecycle
// ─────────────────────────────────────────────────────────────────────────────

// TestOpenCCULoginLogout verifies that openccu-loom's JSON-RPC client can
// acquire a session on the godevccu OpenCCU fixture and cleanly release it.
func TestOpenCCULoginLogout(t *testing.T) {
	srv := startMockCCUOpenCCU(t)
	url := srv.JSONRPCURL()
	if url == "" {
		t.Skip("JSONRPCURL empty — godevccu JSON-RPC listener not reachable")
	}

	c := newJSONRPCClient(t, url, "Admin", "")
	ctx, cancel := ctx5sJSON(t)
	defer cancel()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if c.SessionID() == "" {
		t.Fatal("Login succeeded but SessionID is empty")
	}
	t.Logf("session id after Login: %s", c.SessionID())

	if err := c.Logout(ctx); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if c.SessionID() != "" {
		t.Fatalf("SessionID not cleared after Logout: %q", c.SessionID())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Authenticated method round-trip
// ─────────────────────────────────────────────────────────────────────────────

// TestOpenCCUSessionRoundTrip verifies that a logged-in client can call an
// authenticated JSON-RPC method and receive a well-formed response. We use
// Interface.listInterfaces because it is always available and returns a
// predictable shape (slice of interface descriptors with a "port" field).
func TestOpenCCUSessionRoundTrip(t *testing.T) {
	srv := startMockCCUOpenCCU(t)
	url := srv.JSONRPCURL()
	if url == "" {
		t.Skip("JSONRPCURL empty — godevccu JSON-RPC listener not reachable")
	}

	c := newJSONRPCClient(t, url, "Admin", "")
	ctx, cancel := ctx5sJSON(t)
	defer cancel()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer func() { _ = c.Logout(ctx) }()

	var ifaces []map[string]any
	if err := c.Call(ctx, "Interface.listInterfaces", nil, &ifaces); err != nil {
		t.Fatalf("Interface.listInterfaces: %v", err)
	}
	if len(ifaces) == 0 {
		t.Fatal("Interface.listInterfaces: empty response; expected at least one entry")
	}
	for _, iface := range ifaces {
		t.Logf("interface: name=%v port=%v", iface["name"], iface["port"])
		if _, ok := iface["port"]; !ok {
			t.Errorf("interface entry missing 'port' key: %v", iface)
		}
	}
}

// TestOpenCCUProgramsRoundTrip verifies Program.getAll returns a slice (may
// be empty when SetupDefaults populates no programs, but must not error).
func TestOpenCCUProgramsRoundTrip(t *testing.T) {
	srv := startMockCCUOpenCCU(t)
	url := srv.JSONRPCURL()
	if url == "" {
		t.Skip("JSONRPCURL empty — godevccu JSON-RPC listener not reachable")
	}

	c := newJSONRPCClient(t, url, "Admin", "")
	ctx, cancel := ctx5sJSON(t)
	defer cancel()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer func() { _ = c.Logout(ctx) }()

	var programs []map[string]any
	if err := c.Call(ctx, "Program.getAll", nil, &programs); err != nil {
		t.Fatalf("Program.getAll: %v", err)
	}
	t.Logf("Program.getAll: %d programs", len(programs))
}

// TestOpenCCUSysVarRoundTrip verifies SysVar.getAll returns a slice.
func TestOpenCCUSysVarRoundTrip(t *testing.T) {
	srv := startMockCCUOpenCCU(t)
	url := srv.JSONRPCURL()
	if url == "" {
		t.Skip("JSONRPCURL empty — godevccu JSON-RPC listener not reachable")
	}

	c := newJSONRPCClient(t, url, "Admin", "")
	ctx, cancel := ctx5sJSON(t)
	defer cancel()

	if err := c.Login(ctx); err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer func() { _ = c.Logout(ctx) }()

	var sysvars []map[string]any
	if err := c.Call(ctx, "SysVar.getAll", nil, &sysvars); err != nil {
		t.Fatalf("SysVar.getAll: %v", err)
	}
	t.Logf("SysVar.getAll: %d system variables", len(sysvars))
}

// ─────────────────────────────────────────────────────────────────────────────
// Auth failure mapping
// ─────────────────────────────────────────────────────────────────────────────

// TestOpenCCUAuthFailureMapsToErrAuthFailure verifies that calling an
// authenticated method without a valid session ID maps to hmerr.ErrAuthFailure
// on the openccu-loom client side. godevccu returns a JSON-RPC session error;
// the client must translate it correctly.
//
// We construct the client with empty Username/Password so the automatic
// retry-with-login path in jsonrpc.Client is not triggered (Login is a no-op
// when Username is empty). We then call an auth-gated method directly — the
// server rejects with "Session expired or invalid" and the client should map
// that to ErrAuthFailure via the JSONRPCError wrapping path.
//
// Note: godevccu's session error is returned as a JSON-RPC error payload
// (not an HTTP 401), so it arrives via the wireError path in callOnce rather
// than the HTTP-status path. openccu-loom wraps it as *hmerr.JSONRPCError.
// The session error code from godevccu is -32002 (ErrSessionExpired), which
// is not explicitly mapped to ErrAuthFailure in the current client — the call
// will surface as a *hmerr.JSONRPCError rather than ErrAuthFailure directly.
//
// TODO(client): map godevccu's -32001 (ErrAuthRequired) and -32002
// (ErrSessionExpired) codes to ErrAuthFailure in jsonrpc.Client.callOnce so
// this test can assert errors.Is(err, hmerr.ErrAuthFailure) rather than
// unwrapping to JSONRPCError.
func TestOpenCCUAuthFailureMapsToErrAuthFailure(t *testing.T) {
	srv := startMockCCUOpenCCU(t)
	url := srv.JSONRPCURL()
	if url == "" {
		t.Skip("JSONRPCURL empty — godevccu JSON-RPC listener not reachable")
	}

	// Empty Username disables the auto-login retry, so we hit the auth
	// check without a valid session and get the raw server response.
	c := newJSONRPCClient(t, url, "", "")
	ctx, cancel := ctx5sJSON(t)
	defer cancel()

	var result any
	err := c.Call(ctx, "Interface.listInterfaces", nil, &result)
	if err == nil {
		// godevccu AuthEnabled=true — unauthenticated call must fail.
		t.Fatal("expected an error for unauthenticated call, got nil")
	}

	// The server returns a JSON-RPC session error; the client wraps it in
	// hmerr.Context but the inner type is *hmerr.JSONRPCError (code -32000).
	var jrpcErr *hmerr.JSONRPCError
	if !errors.As(err, &jrpcErr) {
		// Also accept ErrAuthFailure directly in case a future client
		// version maps the session code explicitly.
		if !errors.Is(err, hmerr.ErrAuthFailure) {
			t.Fatalf("expected *hmerr.JSONRPCError or ErrAuthFailure, got %T: %v", err, err)
		}
		t.Logf("got ErrAuthFailure (direct mapping): %v", err)
		return
	}
	t.Logf("session rejection surfaced as JSONRPCError code=%d msg=%q", jrpcErr.Code, jrpcErr.Message)
	// Code -32002 is godevccu's ErrSessionExpired sentinel
	// (ErrServerError=-32000, ErrAuthRequired=-32001, ErrSessionExpired=-32002).
	if jrpcErr.Code != -32002 {
		t.Errorf("expected code -32002 (session expired), got %d", jrpcErr.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CCU.getAuthEnabled — public method (no session required)
// ─────────────────────────────────────────────────────────────────────────────

// TestOpenCCUGetAuthEnabledIsPublic verifies that CCU.getAuthEnabled can be
// called without a session and returns true (auth is enabled on the fixture).
func TestOpenCCUGetAuthEnabledIsPublic(t *testing.T) {
	srv := startMockCCUOpenCCU(t)
	url := srv.JSONRPCURL()
	if url == "" {
		t.Skip("JSONRPCURL empty — godevccu JSON-RPC listener not reachable")
	}

	// No username — rely on the method being in the public-methods set.
	c := newJSONRPCClient(t, url, "", "")
	ctx, cancel := ctx5sJSON(t)
	defer cancel()

	var authEnabled bool
	if err := c.Call(ctx, "CCU.getAuthEnabled", nil, &authEnabled); err != nil {
		t.Fatalf("CCU.getAuthEnabled: %v", err)
	}
	if !authEnabled {
		t.Fatal("CCU.getAuthEnabled: expected true (fixture has AuthEnabled=true)")
	}
}
