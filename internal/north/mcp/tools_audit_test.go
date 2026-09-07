// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
)

// serveMCPAs mounts the real Streamable-HTTP handler behind a middleware
// that attaches id to every request — the shape the daemon's resolve
// chain produces at the mount — and returns a connected client session.
// Going through the HTTP transport is the point: it is what proves the
// identity survives into the tool handler at all.
func serveMCPAs(t *testing.T, id auth.Identity, deps mcp.Deps) *mcpsdk.ClientSession {
	t.Helper()

	handler := mcp.Handler(deps)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(auth.ContextWithIdentity(r.Context(), id)))
	}))
	t.Cleanup(srv.Close)

	c := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &mcpsdk.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestListAudit_DeniedForViewer pins the boundary REST draws explicitly:
// GET /audit is mounted With(admin) because the change-log names which
// operator changed which credential-bearing section. The MCP mount gates
// the whole tool set at viewer (operator once writes are allowed), so
// without the tool's own role check a read-only token enumerates the
// entire configuration change history.
func TestListAudit_DeniedForViewer(t *testing.T) {
	buf := audit.NewBuffer(10)
	buf.Record(audit.Entry{Action: audit.ActionDataPointWrite, DeviceAddress: "ADDR001", User: "admin"})
	devs, _, _ := makeDeviceFixture()

	cs := serveMCPAs(t, auth.Identity{Subject: "watcher", Role: auth.RoleViewer, Scheme: auth.SchemeBasic}, mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"ccu1"}},
		Devices:  devs,
		Audit:    buf,
	})

	res := callTool(t, cs, "list_audit", map[string]any{})
	if !res.IsError {
		t.Fatalf("list_audit answered a viewer identity: %+v", res.StructuredContent)
	}
}

// TestListAudit_ReevaluatesTheCallersIdentityPerRequest pins that the
// admin gate answers the identity of the request that calls the tool, not
// the one that opened the connection.
//
// The transport keeps a session alive across HTTP requests, so a handler
// context captured at initialize time would keep reporting the identity that
// established it: an operator reusing an admin's session id, or an admin
// demoted mid-session, would keep reading the change-log. The middleware here
// swaps the identity between the handshake and the call, which is exactly the
// distinction a single-identity test cannot make.
func TestListAudit_ReevaluatesTheCallersIdentityPerRequest(t *testing.T) {
	buf := audit.NewBuffer(10)
	buf.Record(audit.Entry{Action: audit.ActionDataPointWrite, DeviceAddress: "ADDR001", User: "admin"})
	devs, _, _ := makeDeviceFixture()

	var current atomic.Pointer[auth.Identity]
	current.Store(&auth.Identity{Subject: "boss", Role: auth.RoleAdmin, Scheme: auth.SchemeBasic})

	handler := mcp.Handler(mcp.Deps{
		Centrals: &fakeCentrals{names: []string{"ccu1"}},
		Devices:  devs,
		Audit:    buf,
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(auth.ContextWithIdentity(r.Context(), *current.Load())))
	}))
	t.Cleanup(srv.Close)

	c := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &mcpsdk.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	// The connection was established by an admin; the caller is not one.
	current.Store(&auth.Identity{Subject: "watcher", Role: auth.RoleViewer, Scheme: auth.SchemeBasic})

	res := callTool(t, cs, "list_audit", map[string]any{})
	if !res.IsError {
		t.Fatalf("list_audit answered the connection's identity instead of the caller's: %+v", res.StructuredContent)
	}
}

// TestWriteToolsRecordTheCallersIdentity pins that the audit rows the two
// MCP write tools record carry the resolved per-request identity as User.
// The mount resolves it before the tool runs (callerHasRole reads it for
// list_audit), so an empty User here is the producer dropping the actor —
// and "who changed this?" becomes unanswerable for assistant-driven writes.
func TestWriteToolsRecordTheCallersIdentity(t *testing.T) {
	buf := audit.NewBuffer(10)
	devs, _, _ := makeDeviceFixture()
	h, _ := makeHubWithProgram("ccu1", "prog-42")
	hubs := newFakeHubs()
	hubs.add("ccu1", h)

	cs := serveMCPAs(t, auth.Identity{Subject: "assistant-op", Role: auth.RoleOperator, Scheme: auth.SchemeBearer}, mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		Devices:     devs,
		Writer:      &fakeWriter{},
		Hubs:        hubs,
		Audit:       buf,
		AllowWrites: true,
	})

	if res := callTool(t, cs, "set_datapoint", map[string]any{
		"central_name": "ccu1", "address": "ADDR001", "parameter": "STATE", "value": true,
	}); res.IsError {
		t.Fatalf("set_datapoint: %v", res.Content)
	}
	if res := callTool(t, cs, "trigger_program", map[string]any{
		"central_name": "ccu1", "program_id": "prog-42",
	}); res.IsError {
		t.Fatalf("trigger_program: %v", res.Content)
	}

	entries := buf.List(10)
	if len(entries) != 2 {
		t.Fatalf("expected 2 audit rows, got %d", len(entries))
	}
	for _, e := range entries {
		if e.User != "assistant-op" {
			t.Errorf("audit row %s: User=%q, want the resolved caller %q", e.Action, e.User, "assistant-op")
		}
	}
}
