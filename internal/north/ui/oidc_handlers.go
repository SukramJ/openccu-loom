// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/auth/oidc"
)

// OIDCDeps wraps the OIDC runtime a UI router needs. When nil, the
// corresponding routes short-circuit with `503 Service Unavailable`.
type OIDCDeps struct {
	Client *oidc.Client
	States *oidcStateStore
}

// NewOIDCDeps constructs the deps from an already-initialised client.
func NewOIDCDeps(client *oidc.Client) *OIDCDeps {
	return &OIDCDeps{Client: client, States: newOIDCStateStore()}
}

// oidcState is one pending login attempt. Every `/login/oidc/start`
// mints one; `/login/oidc/callback` consumes it.
type oidcState struct {
	Verifier string
	Created  time.Time
}

// oidcStateStore holds pending login attempts, keyed by the
// `state` query parameter. It is deliberately in-memory only —
// session state isn't worth persisting across restarts.
type oidcStateStore struct {
	TTL time.Duration
	now func() time.Time

	mu     sync.Mutex
	states map[string]oidcState
}

func newOIDCStateStore() *oidcStateStore {
	return &oidcStateStore{
		TTL:    5 * time.Minute,
		now:    time.Now,
		states: make(map[string]oidcState),
	}
}

// Put records verifier under a freshly-minted state key.
func (s *oidcStateStore) Put(verifier string) (string, error) {
	key, err := randomKey()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.states[key] = oidcState{Verifier: verifier, Created: s.now()}
	s.mu.Unlock()
	return key, nil
}

// Consume returns the verifier for state and deletes the entry.
// Expired entries are dropped silently.
func (s *oidcStateStore) Consume(state string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.states[state]
	if !ok {
		return "", false
	}
	delete(s.states, state)
	if s.now().Sub(e.Created) > s.TTL {
		return "", false
	}
	return e.Verifier, true
}

func randomKey() (string, error) {
	p, err := oidc.NewPKCEPair()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString([]byte(p.Verifier)), nil
}

// handleOIDCStart mints PKCE + state, stores the verifier, and
// redirects the browser to the IdP.
func handleOIDCStart(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OIDC == nil || d.OIDC.Client == nil {
			http.Error(w, "OIDC not configured", http.StatusServiceUnavailable)
			return
		}
		pkce, err := oidc.NewPKCEPair()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		state, err := d.OIDC.States.Put(pkce.Verifier)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, d.OIDC.Client.AuthURL(state, pkce), http.StatusSeeOther)
	}
}

// handleOIDCCallback receives the authorization code, exchanges it
// for tokens, validates the ID token claims, and issues a local
// session identical to the Basic-auth flow.
func handleOIDCCallback(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OIDC == nil || d.OIDC.Client == nil || d.Auth == nil {
			http.Error(w, "OIDC not configured", http.StatusServiceUnavailable)
			return
		}
		q := r.URL.Query()
		if q.Get("error") != "" {
			redirectError(w, r, q.Get("error"))
			return
		}
		code := q.Get("code")
		state := q.Get("state")
		if code == "" || state == "" {
			redirectError(w, r, "missing_code")
			return
		}
		verifier, ok := d.OIDC.States.Consume(state)
		if !ok {
			redirectError(w, r, "bad_state")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		tokens, err := d.OIDC.Client.Exchange(ctx, code, verifier)
		if err != nil {
			ifLogger(d).WarnContext(r.Context(), "auth.oidc.exchange_fail",
				slog.String("remote", r.RemoteAddr),
				slog.String("err", err.Error()))
			redirectError(w, r, "exchange_failed")
			return
		}
		claims, err := d.OIDC.Client.VerifyIDToken(ctx, tokens.IDToken)
		if err != nil {
			ifLogger(d).WarnContext(r.Context(), "auth.oidc.id_token_invalid",
				slog.String("remote", r.RemoteAddr),
				slog.String("err", err.Error()))
			redirectError(w, r, "id_token_invalid")
			return
		}
		identity := d.OIDC.Client.IdentityFrom(claims)
		sess, err := d.Auth.Sessions.Issue(identity) //nolint:contextcheck // session persist detaches from the request ctx by design (best-effort durability); see ADR 0041
		if err != nil {
			ifLogger(d).ErrorContext(r.Context(), "auth.oidc.session_issue_fail",
				slog.String("subject", identity.Subject),
				slog.String("err", err.Error()))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ifLogger(d).InfoContext(r.Context(), "auth.oidc.login_ok",
			slog.String("subject", identity.Subject),
			slog.String("role", string(identity.Role)),
			slog.String("remote", r.RemoteAddr))
		auth.WriteSessionCookie(w, sess, d.Auth.Secure)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func redirectError(w http.ResponseWriter, r *http.Request, reason string) {
	// `reason` is a fixed enum-like error code from this package, not a
	// user-controlled URL. The redirect target is the static
	// same-origin path "/login"; reason is URL-encoded as a query value
	// so it cannot escape to a host segment.
	http.Redirect(w, r, "/login?error="+url.QueryEscape(reason), http.StatusSeeOther)
}

func ifLogger(d Deps) *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

// Compile-time assertions so refactors that break the contract fail
// fast.
var _ = errors.New
