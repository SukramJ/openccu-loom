// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake

import (
	"net/http"
	"strings"
)

// DefaultToken is the API token a fake accepts when [Options.Tokens] is
// nil. It holds the wildcard scope, so it passes every check.
const DefaultToken = "olt_0123456789abcdef0123456789abcdef"

// Scope names from the occulited auth API.
const (
	scopeAll          = "*"
	scopeRPCRead      = "rpc:read"
	scopeRPCOperate   = "rpc:operate"
	scopeRPCConfigure = "rpc:configure"
	scopeRPCAdmin     = "rpc:admin"
)

// tokenEntry is one API token as the box stores it: a name and the
// scopes it was granted, unexpanded.
type tokenEntry struct {
	name   string
	scopes []string
}

// tokenName derives the stored name of a test token. The box names
// tokens explicitly; the fake keeps the knob a plain secret→scopes map
// and derives a stable name from the secret instead, so the subject a
// stream limit counts ("token:<name>") and the user /api/auth/v1/state
// reports are predictable from the secret alone.
func tokenName(secret string) string {
	body := strings.TrimPrefix(secret, "olt_")
	if len(body) > 8 {
		body = body[:8]
	}
	return "tok-" + body
}

// TokenName returns the name the fake reports for secret, as it appears
// in /api/auth/v1/state ("token:<name>").
func TokenName(secret string) string { return tokenName(secret) }

// implied lists the scopes a stored scope grants in addition to itself.
// The table is already closed under implication, so one lookup suffices.
func implied(scope string) []string {
	switch scope {
	case "meta:write":
		return []string{"meta:read"}
	case "system:write":
		return []string{"system:read", "led"}
	case "addons:write":
		return []string{"system:read"}
	case "auth:admin":
		return []string{"self"}
	case scopeRPCOperate:
		return []string{scopeRPCRead}
	case scopeRPCConfigure:
		return []string{scopeRPCOperate, scopeRPCRead}
	case scopeRPCAdmin:
		return []string{scopeRPCConfigure, scopeRPCOperate, scopeRPCRead}
	default:
		return nil
	}
}

// hasScope reports whether the stored scopes grant want, following the
// implications. Checks expand; /api/auth/v1/state does not.
func hasScope(stored []string, want string) bool {
	for _, s := range stored {
		if s == scopeAll || s == want {
			return true
		}
		for _, imp := range implied(s) {
			if imp == want {
				return true
			}
		}
	}
	return false
}

// credential extracts the API token a request presents: a bearer token,
// or the password of Basic auth (the user name is ignored). It returns
// "" when the request carries neither.
func credential(r *http.Request) string {
	if _, pass, ok := r.BasicAuth(); ok {
		return pass
	}
	scheme, rest, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if ok && strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(rest)
	}
	return ""
}

// lookupToken resolves a presented secret against the token table.
func (f *Fake) lookupToken(secret string) (tokenEntry, bool) {
	if secret == "" {
		return tokenEntry{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.tokens[secret]
	return e, ok
}

// authorize runs the route check: a valid credential holding scope. On
// failure it writes the error answer and returns false. rpcRoute
// selects the lite-rpc rule that a ?sid= query credential is refused
// with 400 rather than considered; a valid header credential is
// consulted first, since the first valid credential wins.
//
//nolint:unparam // the route scope is per route; every lite-rpc route needs rpc:read.
func (f *Fake) authorize(w http.ResponseWriter, r *http.Request, scope string, rpcRoute bool) (entry tokenEntry, secret string, ok bool) {
	secret = credential(r)
	entry, ok = f.lookupToken(secret)
	if !ok {
		if rpcRoute && r.URL.Query().Has("sid") {
			writeError(w, http.StatusBadRequest, "bad-request",
				"credentials are not accepted in the query string here: use the Authorization header")
			return tokenEntry{}, "", false
		}
		writeError(w, http.StatusUnauthorized, "unauthenticated", "login required")
		return tokenEntry{}, "", false
	}
	if !hasScope(entry.scopes, scope) {
		writeJSON(w, http.StatusForbidden, forbiddenBody{
			Error:   "forbidden",
			Message: "the scope " + scope + " is required",
			Scope:   scope,
		})
		return tokenEntry{}, "", false
	}
	recordSubject(r, "token:"+entry.name)
	return entry, secret, true
}

// forbiddenBody is the 403 answer; it names the missing scope.
type forbiddenBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Scope   string `json:"scope"`
}

// authStateAnon is the /api/auth/v1/state answer without a valid
// credential.
type authStateAnon struct {
	SetupRequired bool `json:"setup_required"`
	Authenticated bool `json:"authenticated"`
}

// authStateToken is the /api/auth/v1/state answer for an API token.
type authStateToken struct {
	SetupRequired      bool     `json:"setup_required"`
	Authenticated      bool     `json:"authenticated"`
	User               string   `json:"user"`
	Scopes             []string `json:"scopes"`
	MustChangePassword bool     `json:"must_change_password"`
}

// handleAuthState answers GET /api/auth/v1/state. It is open; with a
// token it reports the scopes exactly as stored, implications not
// expanded, so a client has to expand them itself.
func (f *Fake) handleAuthState(w http.ResponseWriter, r *http.Request) {
	secret := credential(r)
	entry, ok := f.lookupToken(secret)
	if !ok {
		writeJSON(w, http.StatusOK, authStateAnon{})
		return
	}
	recordSubject(r, "token:"+entry.name)
	writeJSON(w, http.StatusOK, authStateToken{
		Authenticated: true,
		User:          "token:" + entry.name,
		Scopes:        append([]string{}, entry.scopes...),
	})
}
