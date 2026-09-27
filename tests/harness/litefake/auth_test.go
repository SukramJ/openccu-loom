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

// TestFakeLoginSessionAndLogout pins the account path: login answers a
// 26-character session id with the account's identity, the session
// passes as a bearer credential with the account's scopes, auth state
// reports the account fields, and logout ends the session.
func TestFakeLoginSessionAndLogout(t *testing.T) {
	f := startFake(t, litefake.Options{Accounts: []litefake.Account{{
		Username: "admin", Password: "secret", Role: "admin", Level: "administer",
		AccountID: "1", Scopes: []string{"rpc:read"},
	}}})
	if resp, raw := send(t, f, http.MethodPost, "/api/auth/v1/login", "", `{"username":"admin","password":"wrong"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong password: %d %s", resp.StatusCode, raw)
	}
	resp, raw := send(t, f, http.MethodPost, "/api/auth/v1/login", "", `{"username":"admin","password":"secret"}`)
	login := decode[struct {
		SID   string `json:"sid"`
		User  string `json:"user"`
		Level string `json:"level"`
	}](t, string(raw))
	if resp.StatusCode != http.StatusOK || len(login.SID) != 26 || login.User != "admin" || login.Level != "administer" {
		t.Fatalf("login: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := get(t, f, "/api/rpc/v1/interfaces", login.SID); resp.StatusCode != http.StatusOK {
		t.Errorf("session on lite-rpc: %d", resp.StatusCode)
	}
	if resp, _ := get(t, f, "/api/meta/v1/snapshot", login.SID); resp.StatusCode != http.StatusForbidden {
		t.Errorf("session beyond its scopes: %d", resp.StatusCode)
	}
	_, raw = get(t, f, "/api/auth/v1/state", login.SID)
	for _, want := range []string{`"role":"admin"`, `"level":"administer"`, `"account_id":"1"`, `"sid":"` + login.SID + `"`, `"scopes":["rpc:read"]`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("session state lacks %s: %s", want, raw)
		}
	}
	if resp, _ := send(t, f, http.MethodPost, "/api/auth/v1/logout", login.SID, `{}`); resp.StatusCode != http.StatusOK {
		t.Errorf("logout: %d", resp.StatusCode)
	}
	if resp, _ := get(t, f, "/api/rpc/v1/interfaces", login.SID); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("session after logout: %d", resp.StatusCode)
	}
}
