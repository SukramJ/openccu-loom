// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

// SessionCookieName is the HTTP cookie carrying the session id.
const SessionCookieName = "openccu_loom_session"

// SessionTTL is the default session lifetime.
const SessionTTL = 12 * time.Hour

// Session is one server-side session record.
type Session struct {
	ID       string
	Identity Identity
	Created  time.Time
	Expires  time.Time
}

// SessionStore keeps sessions in memory. Persisted sessions pair with
// OIDC refresh tokens.
type SessionStore struct {
	TTL   time.Duration
	now   func() time.Time
	mu    sync.RWMutex
	items map[string]*Session
}

// NewSessionStore constructs an in-memory store.
func NewSessionStore() *SessionStore {
	return &SessionStore{TTL: SessionTTL, now: time.Now, items: make(map[string]*Session)}
}

// Issue mints a new session for id.
func (s *SessionStore) Issue(id Identity) (*Session, error) {
	raw, err := randomID()
	if err != nil {
		return nil, err
	}
	now := s.now()
	session := &Session{ID: raw, Identity: id, Created: now, Expires: now.Add(s.TTL)}
	s.mu.Lock()
	s.items[raw] = session
	s.mu.Unlock()
	return session, nil
}

// Lookup returns the session for sid or nil when it is absent or
// expired. Expired sessions are evicted on read.
func (s *SessionStore) Lookup(sid string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.items[sid]
	if !ok {
		return nil
	}
	if s.now().After(sess.Expires) {
		delete(s.items, sid)
		return nil
	}
	return sess
}

// Revoke removes a session id. No-op when absent.
func (s *SessionStore) Revoke(sid string) {
	s.mu.Lock()
	delete(s.items, sid)
	s.mu.Unlock()
}

// SessionMiddleware resolves the session cookie and attaches the
// identity if a valid session is found. Unlike [Middleware.Resolve]
// it never emits a 401 — the downstream `Require` handles gating.
func SessionMiddleware(store *SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if store != nil {
				if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
					if sess := store.Lookup(c.Value); sess != nil {
						id := sess.Identity
						id.Scheme = SchemeSession
						ctx := context.WithValue(r.Context(), keyIdentity, id)
						r = r.WithContext(ctx)
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WriteSessionCookie sets the cookie on w. `secure` is the runtime
// flag that the daemon sets based on TLS termination — gosec's static
// analyser cannot prove the value, so the linter is suppressed.
func WriteSessionCookie(w http.ResponseWriter, sess *Session, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is runtime-bound; HttpOnly + SameSite=Lax already set; see #20
		Name:     SessionCookieName,
		Value:    sess.ID,
		Path:     "/",
		Expires:  sess.Expires,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie invalidates the cookie on w. The empty MaxAge<0
// cookie carries SameSite=Lax + Secure=true so an active reverse-proxy
// terminator strips it correctly across the redirect chain.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func randomID() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}
