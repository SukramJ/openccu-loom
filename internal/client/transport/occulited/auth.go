// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"context"
	"net/http"
	"sort"
)

// Scopes the box grants. A credential's stored list names only what was
// granted explicitly; [ExpandScopes] adds what each scope implies.
const (
	ScopeAll          = "*"
	ScopeMetaRead     = "meta:read"
	ScopeMetaWrite    = "meta:write"
	ScopeSystemRead   = "system:read"
	ScopeSystemWrite  = "system:write"
	ScopeLogsRead     = "logs:read"
	ScopeAddonsWrite  = "addons:write"
	ScopePower        = "power"
	ScopeBackup       = "backup"
	ScopeLED          = "led"
	ScopeRadioKeys    = "radio:keys"
	ScopeAuthAdmin    = "auth:admin"
	ScopeSelf         = "self"
	ScopeRPCRead      = "rpc:read"
	ScopeRPCOperate   = "rpc:operate"
	ScopeRPCConfigure = "rpc:configure"
	ScopeRPCAdmin     = "rpc:admin"
)

// allScopes is every scope `*` stands for.
var allScopes = []string{
	ScopeMetaRead, ScopeMetaWrite, ScopeSystemRead, ScopeSystemWrite, ScopeLogsRead,
	ScopeAddonsWrite, ScopePower, ScopeBackup, ScopeLED, ScopeRadioKeys, ScopeAuthAdmin,
	ScopeSelf, ScopeRPCRead, ScopeRPCOperate, ScopeRPCConfigure, ScopeRPCAdmin,
}

// scopeImplies lists what a scope grants beyond itself: the box's
// documented implications, applied transitively by [ExpandScopes].
var scopeImplies = map[string][]string{
	ScopeMetaWrite:    {ScopeMetaRead},
	ScopeSystemWrite:  {ScopeSystemRead, ScopeLED},
	ScopeAddonsWrite:  {ScopeSystemRead},
	ScopeAuthAdmin:    {ScopeSelf},
	ScopeRPCOperate:   {ScopeRPCRead},
	ScopeRPCConfigure: {ScopeRPCOperate},
	ScopeRPCAdmin:     {ScopeRPCConfigure},
}

// ExpandScopes returns the set of scopes a stored list grants: each scope,
// everything it implies (transitively), and every scope for `*`. The box
// reports the stored list; deciding what a credential may do needs the
// expansion.
func ExpandScopes(stored []string) map[string]bool {
	out := make(map[string]bool, len(stored))
	var add func(string)
	add = func(s string) {
		if out[s] {
			return
		}
		out[s] = true
		for _, implied := range scopeImplies[s] {
			add(implied)
		}
	}
	for _, s := range stored {
		if s == ScopeAll {
			out[ScopeAll] = true
			for _, each := range allScopes {
				add(each)
			}
			continue
		}
		add(s)
	}
	return out
}

// sortedScopes renders an expanded set in a stable order, for logs.
func sortedScopes(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// AuthState is GET /api/auth/v1/state for the client's own credential.
type AuthState struct {
	SetupRequired bool `json:"setup_required"`
	Authenticated bool `json:"authenticated"`
	// AuthOff reports a box whose authentication is switched off: every
	// request is allowed and Scopes is empty.
	AuthOff bool `json:"auth_off"`
	// Public reports that the box answered with its public (kiosk)
	// principal in place of a session it does not know.
	Public bool `json:"public"`
	// User is "token:<name>" for an API token.
	User string `json:"user"`
	// Scopes is the credential's stored list, implications not expanded.
	Scopes             []string `json:"scopes"`
	MustChangePassword bool     `json:"must_change_password"`
	// Role is the account role behind an interactive session ("admin",
	// "user"); empty for an API token.
	Role string `json:"role"`
}

// AuthState reads the auth state of the client's credential. The route is
// open: an invalid token answers with Authenticated false rather than 401.
func (c *Client) AuthState(ctx context.Context) (AuthState, error) {
	return get[AuthState](ctx, c, "/api/auth/v1/state", nil)
}

// AuthStateOf reads the auth state of a foreign credential — typically a
// box session id a browser presented — instead of the client's own. The
// credential rides this one request only: it replaces the client's token
// rather than accompanying it, and it is never stored, logged, or quoted
// in an error. The route is open: an invalid credential answers with
// Authenticated false rather than an error.
func (c *Client) AuthStateOf(ctx context.Context, credential string) (AuthState, error) {
	return call[AuthState](withoutCredential(ctx), c, request{
		method: http.MethodGet, path: "/api/auth/v1/state",
		header: http.Header{"Authorization": {"Bearer " + credential}},
	})
}

// Granted returns what the credential may do: the expanded scope set, or
// every scope on a box whose authentication is off.
func (s AuthState) Granted() map[string]bool {
	if s.AuthOff {
		return ExpandScopes([]string{ScopeAll})
	}
	if !s.Authenticated {
		return map[string]bool{}
	}
	return ExpandScopes(s.Scopes)
}

// LoginResult is the session a successful account login opens.
type LoginResult struct {
	SID       string `json:"sid"`
	User      string `json:"user"`
	Role      string `json:"role"`
	Level     string `json:"level"`
	AccountID string `json:"account_id"`
	// MustChangePassword is set for an account whose password has to be
	// changed on the box before it may do anything else.
	MustChangePassword bool `json:"must_change_password"`
}

type loginBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login checks an account's username and password against the box and
// opens a session there. The request carries no API token: the credential
// under test is the account's own. A wrong password is an *APIError that
// matches hmerr.ErrAuthFailure.
func (c *Client) Login(ctx context.Context, username, password string) (LoginResult, error) {
	body, err := marshal(loginBody{Username: username, Password: password})
	if err != nil {
		return LoginResult{}, err
	}
	return call[LoginResult](withoutCredential(ctx), c, request{
		method: http.MethodPost, path: "/api/auth/v1/login", body: body,
	})
}

// Logout closes a session opened by [Client.Login]. Loom opens a session
// only to verify an account, so it closes it again straight away.
func (c *Client) Logout(ctx context.Context, sid string) error {
	resp, err := c.send(withoutCredential(ctx), c.calls, request{
		method: http.MethodPost, path: "/api/auth/v1/logout", body: []byte("{}"),
		header: http.Header{"Authorization": {"Bearer " + sid}},
	})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
