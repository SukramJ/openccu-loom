// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package auth

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// OcculiteSessionHeader carries the credential the lite gate accepted for a
// request it passes to an add-on: the box-shell session id, or — when the
// caller presented a box API token as `Authorization: Bearer` — the token
// itself. Behind the gate a client-sent header of this name is stripped, but
// the daemon's own port and any CCU are reachable without the gate — so the
// value is only ever a claim, confirmed against the box before it
// authenticates anything.
const OcculiteSessionHeader = "X-Occulite-Session"

// OcculiteSessionVerifier confirms a box credential. The composition root
// implements it over the onboarded local box's client.
type OcculiteSessionVerifier interface {
	// VerifySession confirms a box-shell session id or box API token against
	// the local box and returns what the box says about it.
	VerifySession(ctx context.Context, credential string) (OcculiteSession, error)
}

// OcculiteSession is the box's answer about one credential.
type OcculiteSession struct {
	Authenticated bool
	// User is the account behind a session, or "token:<name>" for a token.
	User string
	// Role is the box's role word behind a session ("admin", "user"); the
	// box answers none for a token.
	Role string
	// Scopes is a token's stored scope list, implications not expanded.
	Scopes []string
	// AuthOff reports a box whose authentication is switched off: it then
	// confirms every bearer as an admin, whatever the presented id.
	AuthOff bool
	// Public reports that the box substituted its public (kiosk) principal
	// for a session it does not know.
	Public bool
}

// Grant returns the identity the box's answer vouches for, given the
// credential it answered about and the add-on's own gate scope. It is the
// single predicate both the request resolver and the socket revalidator
// apply, so the two can never drift apart.
//
// Neither an auth-off nor a public-principal answer vouches for anything:
// both are not about the presented credential at all. Beyond that the answer
// must be authenticated and match the kind of credential presented:
//   - a session id needs a role the fixed mapping knows (box "admin" →
//     admin, box "user" → operator) and a user of sane shape;
//   - a token needs no role, a "token:<name>" user of sane name, and either
//     Full access (admin) or this add-on's own gate scope (operator). The
//     add-on scope is checked here although the gate already did: the
//     daemon's port is reachable without the gate, and a token that opens
//     a different add-on's pages must not open this daemon.
func (s OcculiteSession) Grant(credential, addonScope string) (Identity, bool) {
	if s.AuthOff || s.Public || !s.Authenticated {
		return Identity{}, false
	}
	switch {
	case isOcculiteSessionID(credential):
		role, mapped := occuliteRole(s.Role)
		if !mapped || !isOcculiteUser(s.User) {
			return Identity{}, false
		}
		return Identity{Subject: occuliteSubjectPrefix + s.User, Scheme: SchemeOcculite, Role: role}, true
	case isOcculiteToken(credential):
		name, isToken := strings.CutPrefix(s.User, occuliteTokenUserPrefix)
		if !isToken || s.Role != "" || !isOcculiteUser(name) {
			return Identity{}, false
		}
		role, granted := occuliteTokenRole(s.Scopes, addonScope)
		if !granted {
			return Identity{}, false
		}
		return Identity{Subject: occuliteTokenSubjectPrefix + name, Scheme: SchemeOcculiteToken, Role: role}, true
	}
	return Identity{}, false
}

// OcculiteSSOTrust carries the resolved policy for [OcculiteSSOPassthrough].
// The composition root fills it from config and the add-on stamp so this
// package never imports config.
type OcculiteSSOTrust struct {
	// Enabled is the resolved opt-in (config north.rest.auth.occulite_sso).
	Enabled bool
	// Verifier asks the local box about a credential. nil disables the
	// resolver: without a box to ask, the header proves nothing.
	Verifier OcculiteSessionVerifier
	// AddonScope is this add-on's gate scope on the box ("addon:<id>"). Empty
	// leaves box tokens unaccepted: without the add-on's own id the daemon
	// cannot tell a token for its pages from one for another add-on's.
	AddonScope string
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
	// occuliteUserMaxLen bounds the box user folded into the subject, so a
	// hostile answer cannot bloat audit rows and logs.
	occuliteUserMaxLen = 64
	// occuliteTokenPrefix and occuliteTokenHexLen are the shape of a box API
	// token: "olt_" followed by 32 lower-case hex digits.
	occuliteTokenPrefix = "olt_"
	occuliteTokenHexLen = 32
	// occuliteTokenUserPrefix is how the box names a token's principal in
	// its auth-state answer: "token:<name>".
	occuliteTokenUserPrefix = "token:"
	// occuliteTokenSubjectPrefix marks a token identity in audit rows,
	// distinct from a box user of the same name.
	occuliteTokenSubjectPrefix = "occulite-token:" //nolint:gosec // a subject prefix, not a credential
	// occuliteScopeAll is the box's Full-access scope; it opens every
	// add-on's pages at the gate.
	occuliteScopeAll = "*"
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

	mu    sync.Mutex
	cache map[string]occuliteEntry
}

func newOcculiteSSO(t OcculiteSSOTrust, logger *slog.Logger) *occuliteSSO {
	if logger == nil {
		logger = slog.Default()
	}
	return &occuliteSSO{trust: t, logger: logger, cache: make(map[string]occuliteEntry)}
}

// Active reports whether box-shell sessions are accepted: enabled, with a
// box to verify them against. The middleware and the daemon's
// auth.occulite_sso.v1 capability read this one condition.
func (t OcculiteSSOTrust) Active() bool { return t.Enabled && t.Verifier != nil }

// AcceptsTokens reports whether box API tokens are accepted too, which
// needs the add-on's own gate scope to check them against.
func (t OcculiteSSOTrust) AcceptsTokens() bool { return t.Active() && t.AddonScope != "" }

// OcculiteSSOPassthrough is a fallback resolver for box-shell single sign-on
// over the lite ingress (ADR 0079). Like [IngressPassthrough] it must be
// wired innermost so every credential resolver runs first, and like it the
// middleware never rejects a request: it either injects an identity or
// passes the request on unchanged, leaving the 401 to the route gates.
//
// It injects a [SchemeOcculite] or [SchemeOcculiteToken] identity ONLY when
// every condition holds:
//   - the policy is enabled and a verifier is present, and
//   - no identity is already on the context (real credentials win), and
//   - X-Occulite-Session holds a well-formed box session id, or a
//     well-formed box token while the trust names the add-on's scope, and
//   - the box's answer about it passes [OcculiteSession.Grant].
//
// The identity carries no expiry: the box owns the credential's lifetime, and
// the verification cache bounds how long a revoked one keeps working.
func OcculiteSSOPassthrough(t OcculiteSSOTrust, logger *slog.Logger) func(http.Handler) http.Handler {
	return newOcculiteSSO(t, logger).middleware
}

func (s *occuliteSSO) middleware(next http.Handler) http.Handler {
	if !s.trust.Active() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := IdentityFrom(r.Context()); ok {
			next.ServeHTTP(w, r) // a real Bearer/session/basic identity wins
			return
		}
		sid := r.Header.Get(OcculiteSessionHeader)
		if !isOcculiteSessionID(sid) && (!s.trust.AcceptsTokens() || !isOcculiteToken(sid)) {
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
	// An auth-off box confirms every bearer as admin, and a public-mode box
	// answers an unknown session with its kiosk principal; Grant refuses
	// both, because honouring either would turn any well-formed header on
	// the directly reachable port into a privileged identity.
	id, ok := sess.Grant(sid, s.trust.AddonScope)
	if !ok {
		// The answer's values stay out of the log: they are box-supplied and
		// already failed the checks.
		s.logger.DebugContext(ctx, "auth.occulite.verify_failed",
			slog.String("session_prefix", sid[:occuliteLogPrefixLen]),
			slog.String("error", "the box does not vouch for this credential"))
		s.store(sid, occuliteEntry{}, occuliteNegativeTTL)
		return Identity{}, false
	}
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
	if !time.Now().Before(e.expires) {
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
	now := time.Now()
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

// occuliteTokenRole maps a box token's scopes onto a daemon role: Full
// access is admin, this add-on's own gate scope operator. The token's other
// scopes count for nothing here, as at the gate; an empty addonScope grants
// nothing at all.
func occuliteTokenRole(scopes []string, addonScope string) (Role, bool) {
	if addonScope == "" {
		return "", false
	}
	if slices.Contains(scopes, occuliteScopeAll) {
		return RoleAdmin, true
	}
	if slices.Contains(scopes, addonScope) {
		return RoleOperator, true
	}
	return "", false
}

// isOcculiteToken reports whether v has the exact shape of a box API token:
// "olt_" followed by 32 lower-case hex digits.
func isOcculiteToken(v string) bool {
	hex, ok := strings.CutPrefix(v, occuliteTokenPrefix)
	if !ok || len(hex) != occuliteTokenHexLen {
		return false
	}
	for i := range len(hex) {
		c := hex[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
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

// isOcculiteUser reports whether a box-supplied user name is safe to fold
// into an identity subject. It fails closed: non-empty, at most
// occuliteUserMaxLen bytes, printable ASCII or space, and none of the
// characters that structure subjects, log lines and key=value records
// ('=', ':', '"'). A box that answers otherwise is not trusted to name
// anyone.
func isOcculiteUser(v string) bool {
	if v == "" || len(v) > occuliteUserMaxLen {
		return false
	}
	for i := range len(v) {
		c := v[i]
		if c < 0x20 || c > 0x7E {
			return false
		}
		switch c {
		case '=', ':', '"':
			return false
		}
	}
	return true
}
