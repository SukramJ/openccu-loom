// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/auth/oidc"
)

// OIDCDeps bundles what the OIDC REST endpoints need. Mirrors
// `internal/north/ui.OIDCDeps` but lives in the REST package so the
// REST router can mount it without depending on the UI package.
//
// When nil, the routes 503. The SPA's login page should hide the
// "Login with OIDC"-button in that case.
type OIDCDeps struct {
	Client *oidc.Client
	Auth   *AuthDeps
	Logger *slog.Logger
	// SPARedirectURL is where the browser is sent after a successful
	// OIDC callback. Defaults to "/app/" (the SPA root) when empty.
	SPARedirectURL string

	mu     sync.Mutex
	states map[string]oidcState
	now    func() time.Time
}

type oidcState struct {
	verifier string
	// nonce is the OIDC Core §3.1.2.1 replay-protection value minted by
	// OIDCStart; the callback passes it to VerifyIDToken as
	// expectedNonce so a captured ID token cannot be replayed into a
	// different flow.
	nonce   string
	created time.Time
}

const oidcStateTTL = 5 * time.Minute

// maxOIDCStates caps the in-flight authorization flows the daemon holds.
// /auth/oidc/start is pre-auth, and an entry it mints is only reclaimed by
// the TTL sweep, so without a ceiling the map grows at the caller's request
// rate for the whole [oidcStateTTL] window — and every insert scans it under
// the lock, so genuine logins degrade along with it. The cap is far above
// any real concurrency (a login completes in seconds) and turns an
// unbounded allocation into a bounded one.
const maxOIDCStates = 4096

// oidcStateCookieName carries the flow's state value in the initiating
// browser so the callback can prove it is the same agent that started the
// flow. Without this binding an attacker could complete a flow, capture a
// valid state+code, and replay it into a victim's browser to log the
// victim into the attacker's account (login CSRF).
const oidcStateCookieName = "openccu_loom_oidc_state"

// NewOIDCDeps constructs the deps from an already-initialised client.
func NewOIDCDeps(client *oidc.Client, authDeps *AuthDeps, logger *slog.Logger) *OIDCDeps {
	return &OIDCDeps{
		Client: client,
		Auth:   authDeps,
		Logger: logger,
		states: make(map[string]oidcState),
		now:    time.Now,
	}
}

func (d *OIDCDeps) putState(verifier, nonce string) (string, error) {
	key, err := randomKey()
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	// Sweep expired entries on every insert so states that are minted but
	// never completed (the common abandoned-login case) cannot accumulate
	// unbounded — consumeState alone never reclaims them.
	now := d.now()
	for k, e := range d.states {
		if now.Sub(e.created) > oidcStateTTL {
			delete(d.states, k)
		}
	}
	// A flood of abandoned flows stays inside the TTL, so the sweep alone
	// cannot bound the map. Evict the oldest entries instead: a genuine
	// login completes within seconds, so the entry closest to expiry is
	// the one least likely to still be wanted.
	for len(d.states) >= maxOIDCStates {
		oldest, found := "", false
		for k, e := range d.states {
			if !found || e.created.Before(d.states[oldest].created) {
				oldest, found = k, true
			}
		}
		if !found {
			break
		}
		delete(d.states, oldest)
	}
	d.states[key] = oidcState{verifier: verifier, nonce: nonce, created: now}
	d.mu.Unlock()
	return key, nil
}

func (d *OIDCDeps) consumeState(state string) (verifier, nonce string, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, found := d.states[state]
	if !found {
		return "", "", false
	}
	delete(d.states, state)
	if d.now().Sub(e.created) > oidcStateTTL {
		return "", "", false
	}
	return e.verifier, e.nonce, true
}

// OIDCStart mints a fresh PKCE pair, stores the verifier, and
// redirects the user agent to the IdP's authorize endpoint. Browser-
// driven flow — does not return JSON.
func OIDCStart(d *OIDCDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil || d.Client == nil {
			http.Error(w, "OIDC not configured", http.StatusServiceUnavailable)
			return
		}
		pkce, err := oidc.NewPKCEPair()
		if err != nil {
			d.logServerError("oidc.pkce_generate", err)
			http.Error(w, "OIDC start failed", http.StatusInternalServerError)
			return
		}
		nonce, err := oidc.NewNonce()
		if err != nil {
			d.logServerError("oidc.nonce_generate", err)
			http.Error(w, "OIDC start failed", http.StatusInternalServerError)
			return
		}
		state, err := d.putState(pkce.Verifier, nonce)
		if err != nil {
			d.logServerError("oidc.state_store", err)
			http.Error(w, "OIDC start failed", http.StatusInternalServerError)
			return
		}
		writeOIDCStateCookie(w, state, d.secureCookie())
		http.Redirect(w, r, d.Client.AuthURL(state, pkce, nonce), http.StatusSeeOther)
	}
}

// OIDCCallback exchanges the authorization code for tokens, validates
// the ID token, issues a local session cookie, and bounces back to
// the SPA. Errors are surfaced as ?error=… on the SPA login page.
func OIDCCallback(d *OIDCDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d == nil || d.Client == nil || d.Auth == nil || d.Auth.Sessions == nil {
			http.Error(w, "OIDC not configured", http.StatusServiceUnavailable)
			return
		}
		q := r.URL.Query()
		if q.Get("error") != "" {
			oidcRedirectError(w, r, q.Get("error"), d.SPARedirectURL)
			return
		}
		code := q.Get("code")
		state := q.Get("state")
		if code == "" || state == "" {
			oidcRedirectError(w, r, "missing_code", d.SPARedirectURL)
			return
		}
		// Bind the callback to the browser that started the flow: the state
		// query parameter must match the state cookie set by OIDCStart. A
		// forged callback replayed into a victim's browser carries the
		// attacker's state but not a matching cookie, so it is rejected.
		clearOIDCStateCookie(w)
		cookie, cerr := r.Cookie(oidcStateCookieName)
		if cerr != nil || cookie.Value == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
			oidcRedirectError(w, r, "bad_state", d.SPARedirectURL)
			return
		}
		verifier, nonce, ok := d.consumeState(state)
		if !ok {
			oidcRedirectError(w, r, "bad_state", d.SPARedirectURL)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		tokens, err := d.Client.Exchange(ctx, code, verifier)
		if err != nil {
			if d.Logger != nil {
				d.Logger.Warn("oidc.exchange", slog.String("err", err.Error()))
			}
			oidcRedirectError(w, r, "exchange_failed", d.SPARedirectURL)
			return
		}
		claims, err := d.Client.VerifyIDToken(ctx, tokens.IDToken, nonce)
		if err != nil {
			if d.Logger != nil {
				d.Logger.Warn("oidc.id_token_invalid", slog.String("err", err.Error()))
			}
			oidcRedirectError(w, r, "id_token_invalid", d.SPARedirectURL)
			return
		}
		identity := d.Client.IdentityFrom(claims)
		sess, err := d.Auth.Sessions.Issue(identity) //nolint:contextcheck // session persist detaches from the request ctx by design (best-effort durability); see ADR 0041
		if err != nil {
			d.logServerError("oidc.session_issue", err)
			http.Error(w, "OIDC session issue failed", http.StatusInternalServerError)
			return
		}
		auth.WriteSessionCookie(w, sess, d.Auth.Secure)
		target := d.SPARedirectURL
		if target == "" {
			target = "/app/"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	}
}

func oidcRedirectError(w http.ResponseWriter, r *http.Request, reason, target string) {
	// Open-redirect mitigation: only honour relative same-origin targets.
	// Anything that looks like a scheme, an absolute URL, or a
	// protocol-relative reference falls back to the canonical app path
	// so an attacker cannot funnel error redirects to a foreign host.
	if !isSafeRelativeTarget(target) {
		target = "/app/"
	}
	// `target` was vetted by isSafeRelativeTarget above (path-only,
	// same-origin); `reason` is a fixed error code from this package
	// and is URL-encoded as a query value.
	http.Redirect(w, r, target+"?error="+url.QueryEscape(reason), http.StatusSeeOther) //nolint:gosec // target validated, reason encoded; see #20
}

// isSafeRelativeTarget returns true when target is a path-only
// reference rooted at the daemon ("/path/...") — never a scheme-bearing
// or protocol-relative URL.
func isSafeRelativeTarget(target string) bool {
	if target == "" {
		return false
	}
	if !strings.HasPrefix(target, "/") {
		return false
	}
	// Reject protocol-relative references. Browsers treat both `//host`
	// and `/\host` (and `/\\host`) as absolute URLs pointing off-host,
	// so the second character must not be a slash or backslash.
	if len(target) > 1 && (target[1] == '/' || target[1] == '\\') {
		return false
	}
	if strings.Contains(target, ":") {
		return false // schemes embed a colon ("javascript:", "https:")
	}
	return true
}

// secureCookie reports whether the OIDC state cookie should carry the
// Secure flag, mirroring the session cookie's runtime TLS binding.
func (d *OIDCDeps) secureCookie() bool {
	return d != nil && d.Auth != nil && d.Auth.Secure
}

// logServerError records a 500-class OIDC failure via the configured
// logger (falling back to slog.Default() when none is wired) so the
// operator-facing http.Error body can stay a generic message instead
// of echoing err's text — the underlying error may carry IdP-internal
// detail that should not reach the browser.
func (d *OIDCDeps) logServerError(msg string, err error) {
	logger := slog.Default()
	if d != nil && d.Logger != nil {
		logger = d.Logger
	}
	logger.Error(msg, slog.String("err", err.Error()))
}

// writeOIDCStateCookie sets the short-lived, HttpOnly state cookie that
// binds the callback to the initiating browser.
func writeOIDCStateCookie(w http.ResponseWriter, state string, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is runtime-bound to TLS; HttpOnly + SameSite=Lax always set
		Name:     oidcStateCookieName,
		Value:    state,
		Path:     "/",
		MaxAge:   int(oidcStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearOIDCStateCookie invalidates the state cookie once the callback has
// read it (single use). Secure=true (mirroring ClearSessionCookie) so an
// active reverse-proxy terminator strips it correctly across redirects.
func clearOIDCStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func randomKey() (string, error) {
	pkce, err := oidc.NewPKCEPair()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString([]byte(pkce.Verifier)), nil
}
