// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package auth

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// OcculiteSessionHeader carries the box-shell session id the lite gate
// attaches to every request it passes to an add-on. Behind the gate a
// client-sent header of this name is stripped, but the daemon's own port
// and any CCU are reachable without the gate — so the value is only ever a
// claim, confirmed against the box before it authenticates anything.
const OcculiteSessionHeader = "X-Occulite-Session"

// OcculiteSessionVerifier confirms a box-shell session id. The composition
// root implements it over the onboarded local box's client.
type OcculiteSessionVerifier interface {
	// VerifySession confirms a box-shell session id against the local
	// box and returns what the box says about it.
	VerifySession(ctx context.Context, sessionID string) (OcculiteSession, error)
}

// OcculiteSession is the box's answer about one session id.
type OcculiteSession struct {
	Authenticated bool
	User          string
	Role          string // the box's role words: "admin", "user"
}

// OcculiteSSOTrust carries the resolved policy for [OcculiteSSOPassthrough].
// The composition root fills it from config and the add-on stamp so this
// package never imports config.
type OcculiteSSOTrust struct {
	// Enabled is the resolved opt-in (config north.rest.auth.occulite_sso).
	Enabled bool
	// Verifier asks the local box about a session id. nil disables the
	// resolver: without a box to ask, the header proves nothing.
	Verifier OcculiteSessionVerifier
}

const (
	// occuliteSessionIDLen is the length of a box session id: 26 characters
	// of the RFC 4648 base32 alphabet. The shorter legacy alias is refused by
	// the box API, so it never reaches the verifier.
	occuliteSessionIDLen = 26
	// occuliteVerifyTimeout bounds one verification round trip so a slow box
	// cannot stall the request beyond what a login form would cost.
	occuliteVerifyTimeout = 2 * time.Second
	// occulitePositiveTTL is how long a confirmed session is trusted without
	// asking again; a session revoked on the box ends within this window.
	occulitePositiveTTL = 60 * time.Second
	// occuliteNegativeTTL keeps a dead or unverifiable session from turning
	// every request into a round trip to the box.
	occuliteNegativeTTL = 10 * time.Second
	// occuliteCacheCap bounds the cache; the header is attacker-chosen on
	// the directly reachable port, so the map must never grow unbounded.
	occuliteCacheCap = 1024
	// occuliteSubjectPrefix marks the identity's origin so audit rows name
	// the box user and never collide with a local account.
	occuliteSubjectPrefix = "occulite:"
	// occuliteLogPrefixLen is how much of a session id a log line may show.
	occuliteLogPrefixLen = 4
)

// occuliteEntry is one cached verification outcome. ok false is a negative
// entry: the request passes unauthenticated until it expires.
type occuliteEntry struct {
	id      Identity
	ok      bool
	expires time.Time
}

// occuliteSSO is the resolver's state: the policy plus a bounded,
// mutex-guarded verification cache. It owns no goroutine; expired entries
// are pruned on access.
type occuliteSSO struct {
	trust  OcculiteSSOTrust
	logger *slog.Logger
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]occuliteEntry
}

func newOcculiteSSO(t OcculiteSSOTrust, logger *slog.Logger, now func() time.Time) *occuliteSSO {
	if logger == nil {
		logger = slog.Default()
	}
	return &occuliteSSO{trust: t, logger: logger, now: now, cache: make(map[string]occuliteEntry)}
}

// OcculiteSSOPassthrough is a fallback resolver for box-shell single sign-on
// over the lite ingress (ADR 0079). Like [IngressPassthrough] it must be
// wired innermost so every credential resolver runs first, and like it the
// middleware never rejects a request: it either injects an identity or
// passes the request on unchanged, leaving the 401 to the route gates.
//
// It injects a [SchemeOcculite] identity ONLY when every condition holds:
//   - the policy is enabled and a verifier is present, and
//   - no identity is already on the context (real credentials win), and
//   - X-Occulite-Session holds a well-formed box session id, and
//   - the box confirms the session as authenticated, with a role the fixed
//     mapping knows (box "admin" → admin, box "user" → operator).
//
// The identity carries no expiry: the box owns the session's lifetime, and
// the verification cache bounds how long a revoked session keeps working.
func OcculiteSSOPassthrough(t OcculiteSSOTrust, logger *slog.Logger) func(http.Handler) http.Handler {
	return newOcculiteSSO(t, logger, time.Now).middleware
}

func (s *occuliteSSO) middleware(next http.Handler) http.Handler {
	if !s.trust.Enabled || s.trust.Verifier == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := IdentityFrom(r.Context()); ok {
			next.ServeHTTP(w, r) // a real Bearer/session/basic identity wins
			return
		}
		sid := r.Header.Get(OcculiteSessionHeader)
		if !isOcculiteSessionID(sid) {
			next.ServeHTTP(w, r)
			return
		}
		id, ok := s.resolve(r.Context(), sid)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(ContextWithIdentity(r.Context(), id)))
	})
}

// resolve answers from the cache or asks the box, caching the outcome.
func (s *occuliteSSO) resolve(ctx context.Context, sid string) (Identity, bool) {
	if e, hit := s.lookup(sid); hit {
		return e.id, e.ok
	}
	vctx, cancel := context.WithTimeout(ctx, occuliteVerifyTimeout)
	sess, err := s.trust.Verifier.VerifySession(vctx, sid)
	cancel()
	if err != nil {
		// A request that ended on its own says nothing about the session;
		// caching it would lock a live session out for the negative window.
		if ctx.Err() != nil {
			return Identity{}, false
		}
		// The negative entry makes this at most one line per window per id.
		s.logger.DebugContext(ctx, "auth.occulite.verify_failed",
			slog.String("session_prefix", sid[:occuliteLogPrefixLen]),
			slog.String("error", err.Error()))
		s.store(sid, occuliteEntry{}, occuliteNegativeTTL)
		return Identity{}, false
	}
	role, mapped := occuliteRole(sess.Role)
	if !sess.Authenticated || !mapped {
		s.store(sid, occuliteEntry{}, occuliteNegativeTTL)
		return Identity{}, false
	}
	id := Identity{Subject: occuliteSubjectPrefix + sess.User, Scheme: SchemeOcculite, Role: role}
	s.store(sid, occuliteEntry{id: id, ok: true}, occulitePositiveTTL)
	return id, true
}

// lookup returns a live cache entry, dropping an expired one it meets.
func (s *occuliteSSO) lookup(sid string) (occuliteEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, hit := s.cache[sid]
	if !hit {
		return occuliteEntry{}, false
	}
	if !s.now().Before(e.expires) {
		delete(s.cache, sid)
		return occuliteEntry{}, false
	}
	return e, true
}

// store caches an outcome for ttl. A full cache is first swept of expired
// entries; if it is still full the outcome is simply not cached — the next
// request verifies again rather than the map growing.
func (s *occuliteSSO) store(sid string, e occuliteEntry, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	e.expires = now.Add(ttl)
	if _, present := s.cache[sid]; !present && len(s.cache) >= occuliteCacheCap {
		for k, v := range s.cache {
			if !now.Before(v.expires) {
				delete(s.cache, k)
			}
		}
		if len(s.cache) >= occuliteCacheCap {
			return
		}
	}
	s.cache[sid] = e
}

// occuliteRole maps the box's role words onto daemon roles. Anything the
// mapping does not know defers rather than guessing a privilege level.
func occuliteRole(boxRole string) (Role, bool) {
	switch boxRole {
	case "admin":
		return RoleAdmin, true
	case "user":
		return RoleOperator, true
	default:
		return "", false
	}
}

// isOcculiteSessionID reports whether v has the exact shape of a box
// session id: 26 characters of A-Z and 2-7.
func isOcculiteSessionID(v string) bool {
	if len(v) != occuliteSessionIDLen {
		return false
	}
	for i := range len(v) {
		c := v[i]
		if (c < 'A' || c > 'Z') && (c < '2' || c > '7') {
			return false
		}
	}
	return true
}
