// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

	// lastSeen tracks the most recent successful Lookup for the idle
	// timeout. It is in-memory only (not persisted) — a restart resets the
	// idle clock to the absolute Expires window, which is acceptable.
	lastSeen time.Time
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
	TTL time.Duration
	// IdleTTL, when > 0, evicts a session that has not been looked up
	// within the window even if its absolute TTL has not elapsed — capping
	// how long a stolen-but-idle cookie stays usable. Zero disables the
	// idle check (absolute TTL only), preserving the historical behaviour.
	IdleTTL time.Duration
	now     func() time.Time
	mu      sync.RWMutex
	items   map[string]*Session
	persist SessionPersistence
	logger  *slog.Logger
}

// SessionStoreOptions carries the tunables a composition root passes to a
// session store. The zero value reproduces the historical behaviour, so a
// caller that has nothing to configure keeps using the plain constructors.
type SessionStoreOptions struct {
	// IdleTTL is the inactivity window after which a session is evicted on
	// its next lookup. Zero disables the idle check. Mirrors
	// north.rest.auth.session_idle_timeout.
	IdleTTL time.Duration
}

// NewSessionStore constructs an in-memory store with no durable backing.
func NewSessionStore() *SessionStore {
	return NewSessionStoreWithOptions(SessionStoreOptions{})
}

// NewSessionStoreWithOptions is [NewSessionStore] with the tunables of opts
// applied.
func NewSessionStoreWithOptions(opts SessionStoreOptions) *SessionStore {
	return &SessionStore{TTL: SessionTTL, IdleTTL: opts.IdleTTL, now: time.Now, items: make(map[string]*Session)}
}

// NewPersistentSessionStoreWithOptions constructs a save-through session
// store backed by persist, with the tunables of opts applied. It hydrates
// the in-memory map from the persisted active sessions so a daemon restart
// no longer logs active browsers out. A hydration failure is returned (and
// is fatal to the caller's construction step); subsequent persist failures
// during Issue/Revoke/purge are best-effort and logged, never propagated.
func NewPersistentSessionStoreWithOptions(persist SessionPersistence, logger *slog.Logger, opts SessionStoreOptions) (*SessionStore, error) {
	s := &SessionStore{
		TTL:     SessionTTL,
		IdleTTL: opts.IdleTTL,
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
			if sess.lastSeen.IsZero() {
				sess.lastSeen = sess.Created
			}
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
	session := &Session{ID: raw, Identity: id, Created: now, Expires: now.Add(s.TTL), lastSeen: now}
	s.mu.Lock()
	s.items[raw] = session
	s.mu.Unlock()
	// Best-effort durability: a DB hiccup here costs only this session's
	// survival across a restart, not the login itself.
	s.persistBest("save", func(ctx context.Context) error { return s.persist.SaveSession(ctx, session) })
	return session, nil
}

// Lookup returns the session for sid or nil when it is absent, past its
// absolute expiry, or idle beyond IdleTTL. Evicted sessions are removed
// on read; a live one has its idle clock refreshed.
func (s *SessionStore) Lookup(sid string) *Session {
	now := s.now()
	s.mu.Lock()
	sess, ok := s.items[sid]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	idle := s.IdleTTL > 0 && !sess.lastSeen.IsZero() && now.Sub(sess.lastSeen) > s.IdleTTL
	if now.After(sess.Expires) || idle {
		delete(s.items, sid)
		s.mu.Unlock()
		s.persistBest("delete", func(ctx context.Context) error { return s.persist.DeleteSession(ctx, sid) })
		return nil
	}
	sess.lastSeen = now
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

// RevokeBySubject removes every session of the local account subject and
// returns the number evicted. It is the invalidation hook for credential
// changes: a password change, role change, or account deletion must not
// leave a stolen or stale session usable for the remainder of its TTL.
// Sessions of federated principals are left alone — see [Scheme.Federated].
// No-op for an empty subject.
func (s *SessionStore) RevokeBySubject(subject string) int {
	return s.revokeBySubject(subject, "")
}

// RevokeBySubjectExcept is RevokeBySubject but preserves the session with
// id keepSID — used by a self-service password change so the caller's own
// session survives while every *other* session for that subject is killed.
func (s *SessionStore) RevokeBySubjectExcept(subject, keepSID string) int {
	return s.revokeBySubject(subject, keepSID)
}

func (s *SessionStore) revokeBySubject(subject, keepSID string) int {
	subject = CanonicalSubject(subject)
	if subject == "" {
		return 0
	}
	s.mu.Lock()
	ids := make([]string, 0)
	for id, sess := range s.items {
		// A federated principal is not the local account of the same name:
		// the external provider owns their credentials, so a local password
		// reset or account deletion has no say over their session.
		if sess.Identity.Scheme.Federated() {
			continue
		}
		// Compare canonically: a session issued before the identity
		// boundary folded the subject (or by a provider that reports its
		// own spelling) must still be reachable by the canonical name the
		// admin surface uses, or a credential change silently evicts
		// nothing.
		if CanonicalSubject(sess.Identity.Subject) == subject && id != keepSID {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		delete(s.items, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.persistBest("delete", func(ctx context.Context) error { return s.persist.DeleteSession(ctx, id) })
	}
	return len(ids)
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
			// A Bearer/Basic identity resolved by an earlier resolver wins: the
			// session only fills an otherwise-unauthenticated request. Mirrors
			// IngressPassthrough's precedence guard and the "a genuine Bearer
			// token always wins" contract. Without this guard a lower-role
			// session cookie riding alongside an injected admin Bearer (the
			// remote-proxy path) would silently overwrite the admin identity and
			// 403 admin/operator actions with "insufficient role".
			if _, ok := IdentityFrom(r.Context()); ok {
				next.ServeHTTP(w, r)
				return
			}
			if store != nil {
				if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
					if sess := store.Lookup(c.Value); sess != nil { //nolint:contextcheck // Lookup is ctx-free by API contract; its best-effort persist uses a background ctx
						id := sess.Identity
						// Carry the session's absolute expiry with the identity.
						// Lookup enforces it on every HTTP request, but a
						// consumer that resolves once and keeps the snapshot —
						// the WebSocket upgrade — has no second resolve to
						// enforce it at, and would otherwise hold the session's
						// role indefinitely past its TTL.
						id.ExpiresAt = sess.Expires
						if !id.Scheme.Federated() {
							// The stored scheme records the credential the
							// session was minted from; the request presented a
							// cookie. A federated session keeps its own scheme
							// so subject-keyed controls downstream can tell an
							// external principal from the local account of the
							// same name.
							id.Scheme = SchemeSession
						}
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
