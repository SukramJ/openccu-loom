// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

// TestFakeAuthStateReportsStoredScopes pins /api/auth/v1/state: open,
// scopes exactly as stored (rpc:operate is not expanded to rpc:read),
// and only the two base fields without a valid credential.
func TestFakeAuthStateReportsStoredScopes(t *testing.T) {
	f := startFake(t, litefake.Options{Tokens: map[string][]string{operateToken: {"rpc:operate"}}})

	resp, body := get(t, f, "/api/auth/v1/state", operateToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("auth state: %d", resp.StatusCode)
	}
	st := decode[struct {
		SetupRequired bool     `json:"setup_required"`
		Authenticated bool     `json:"authenticated"`
		User          string   `json:"user"`
		Scopes        []string `json:"scopes"`
	}](t, string(body))
	if !st.Authenticated || st.User != "token:"+litefake.TokenName(operateToken) {
		t.Errorf("state %s", body)
	}
	if strings.Join(st.Scopes, ",") != "rpc:operate" {
		t.Errorf("scopes %v, want the stored [rpc:operate] unexpanded", st.Scopes)
	}

	_, body = get(t, f, "/api/auth/v1/state", "")
	if string(body) != `{"setup_required":false,"authenticated":false}` {
		t.Errorf("anonymous state %s", body)
	}
}

// TestFakeEnforcesRouteScopeOnLiteRPC pins the route check: 401 without
// a credential, 403 naming rpc:read for a token without it, 400 for a
// query-string credential; the implied rpc:read of rpc:operate passes.
func TestFakeEnforcesRouteScopeOnLiteRPC(t *testing.T) {
	f := startFake(t, litefake.Options{Tokens: map[string][]string{
		metaToken:    {"meta:read"},
		operateToken: {"rpc:operate"},
	}})

	resp, body := get(t, f, "/api/rpc/v1/interfaces", "")
	if resp.StatusCode != http.StatusUnauthorized || errorCode(t, body) != "unauthenticated" {
		t.Errorf("no credential: %d %s", resp.StatusCode, body)
	}
	resp, body = get(t, f, "/api/rpc/v1/interfaces", metaToken)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), `"scope":"rpc:read"`) {
		t.Errorf("meta token: %d %s", resp.StatusCode, body)
	}
	resp, body = get(t, f, "/api/rpc/v1/events?sid=abcdefghij", "")
	if resp.StatusCode != http.StatusBadRequest || errorCode(t, body) != "bad-request" {
		t.Errorf("query credential: %d %s", resp.StatusCode, body)
	}
	if resp, body := get(t, f, "/api/rpc/v1/interfaces", operateToken); resp.StatusCode != http.StatusOK {
		t.Errorf("rpc:operate implies rpc:read: %d %s", resp.StatusCode, body)
	}
}
