// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
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

// SessionPersistence is the durable backing store for [SessionStore].
// The in-memory store is a save-through cache over an implementation of
// this port: it hydrates from [LoadActiveSessions] on boot and
// best-effort mirrors each Issue/Revoke and the periodic purge to disk.
// A nil persistence keeps the store purely in-memory (the historical
// behaviour). The SQLite implementation lives in
// internal/store/sqlite/auth_sessions.go.
type SessionPersistence interface {
	// SaveSession durably stores (or replaces) sess.
	SaveSession(ctx context.Context, sess *Session) error
	// DeleteSession removes the session with the given id.
	DeleteSession(ctx context.Context, id string) error
	// LoadActiveSessions returns every session not yet expired at now.
	LoadActiveSessions(ctx context.Context, now time.Time) ([]*Session, error)
	// DeleteExpiredSessions deletes every session expired at or before
	// now and returns the count removed.
	DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error)
}

// SessionStore keeps sessions in memory, optionally mirroring them to a
// [SessionPersistence] so they survive a daemon restart. Persisted
// sessions pair with OIDC refresh tokens.
type SessionStore struct {
	TTL     time.Duration
	now     func() time.Time
	mu      sync.RWMutex
	items   map[string]*Session
	persist SessionPersistence
	logger  *slog.Logger
}

// NewSessionStore constructs an in-memory store with no durable backing.
func NewSessionStore() *SessionStore {
	return &SessionStore{TTL: SessionTTL, now: time.Now, items: make(map[string]*Session)}
}

// NewPersistentSessionStore constructs a save-through session store
// backed by persist. It hydrates the in-memory map from the persisted
// active sessions so a daemon restart no longer logs active browsers
// out. A hydration failure is returned (and is fatal to the caller's
// construction step); subsequent persist failures during Issue/Revoke/
// purge are best-effort and logged, never propagated.
func NewPersistentSessionStore(persist SessionPersistence, logger *slog.Logger) (*SessionStore, error) {
	s := &SessionStore{
		TTL:     SessionTTL,
		now:     time.Now,
		items:   make(map[string]*Session),
		persist: persist,
		logger:  logger,
	}
	if persist != nil {
		// Boot-time hydration runs once during composition-root wiring,
		// which has no request ctx to thread; a fresh background context
		// is correct here.
		active, err := persist.LoadActiveSessions(context.Background(), s.now())
		if err != nil {
			return nil, err
		}
		for _, sess := range active {
			s.items[sess.ID] = sess
		}
	}
	return s, nil
}

// persistBest runs a persistence op best-effort: it returns early when
// no durable backing is configured, and on error logs (when a logger is
// set) rather than failing the caller. The in-memory copy is always the
// source of truth for the running process.
func (s *SessionStore) persistBest(op string, fn func(ctx context.Context) error) {
	if s.persist == nil {
		return
	}
	// Issue/Lookup/Revoke carry no ctx (their signatures are part of the
	// stable handler-facing API and cannot change), so the best-effort
	// mirror uses a fresh background context. Persistence here is fire-and-
	// forget — a slow disk must never block a session read or write.
	if err := fn(context.Background()); err != nil && s.logger != nil {
		s.logger.Warn("auth.session.persist", slog.String("op", op), slog.String("err", err.Error()))
	}
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
	// Best-effort durability: a DB hiccup here costs only this session's
	// survival across a restart, not the login itself.
	s.persistBest("save", func(ctx context.Context) error { return s.persist.SaveSession(ctx, session) })
	return session, nil
}

// Lookup returns the session for sid or nil when it is absent or
// expired. Expired sessions are evicted on read.
func (s *SessionStore) Lookup(sid string) *Session {
	s.mu.Lock()
	sess, ok := s.items[sid]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	if s.now().After(sess.Expires) {
		delete(s.items, sid)
		s.mu.Unlock()
		s.persistBest("delete", func(ctx context.Context) error { return s.persist.DeleteSession(ctx, sid) })
		return nil
	}
	s.mu.Unlock()
	return sess
}

// Revoke removes a session id. No-op when absent.
func (s *SessionStore) Revoke(sid string) {
	s.mu.Lock()
	delete(s.items, sid)
	s.mu.Unlock()
	s.persistBest("delete", func(ctx context.Context) error { return s.persist.DeleteSession(ctx, sid) })
}

// PurgeExpired evicts expired sessions from the in-memory map and, when
// a durable backing is configured, deletes the expired rows from it,
// returning the number of rows the persistence reported deleted. It is
// safe with no persistence (memory is still swept; the count is 0).
func (s *SessionStore) PurgeExpired(ctx context.Context) (int, error) {
	now := s.now()
	s.mu.Lock()
	for id, sess := range s.items {
		if now.After(sess.Expires) {
			delete(s.items, id)
		}
	}
	s.mu.Unlock()
	if s.persist == nil {
		return 0, nil
	}
	return s.persist.DeleteExpiredSessions(ctx, now)
}

// SessionMiddleware resolves the session cookie and attaches the
// identity if a valid session is found. Unlike [Middleware.Resolve]
// it never emits a 401 — the downstream `Require` handles gating.
func SessionMiddleware(store *SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if store != nil {
				if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
					if sess := store.Lookup(c.Value); sess != nil { //nolint:contextcheck // Lookup is ctx-free by API contract; its best-effort persist uses a background ctx
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
