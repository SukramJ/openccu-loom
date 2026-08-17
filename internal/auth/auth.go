// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Scheme identifies how a request authenticated.
type Scheme string

// Scheme values.
const (
	SchemeBasic   Scheme = "basic"
	SchemeBearer  Scheme = "bearer"
	SchemeSession Scheme = "session" // session-cookie auth, set by the local login flow
	SchemeOIDC    Scheme = "oidc"    // session-cookie auth for a principal an external provider vouched for
	SchemeIngress Scheme = "ingress" // HA Ingress auth passthrough (ADR 0044)
)

// Federated reports whether the scheme identifies a principal an external
// identity provider vouched for rather than one the daemon's own user store
// owns. Subject-keyed controls over local accounts must not reach a
// federated principal: an external login name that folds to the same string
// as a local account belongs to a different person, and the daemon holds no
// authority over their credentials.
func (s Scheme) Federated() bool { return s == SchemeOIDC }

// Role is the coarse-grained permission level.
type Role string

// Role values. Ordered weakly: Admin ⊇ Operator ⊇ Viewer.
const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

// Identity is the principal the middleware attaches to the request
// context.
type Identity struct {
	Subject string
	Scheme  Scheme
	Role    Role
	TokenID string // set when Scheme == bearer
	// ExpiresAt is the instant the credential behind this identity stops
	// being accepted; the zero value means "no server-side expiry".
	//
	// A request-scoped consumer never needs it — every HTTP request
	// re-resolves its credential, and a resolver only returns an identity
	// for a credential that is still valid. It exists for the consumers
	// that resolve once and then keep the snapshot: a WebSocket captures
	// the identity at the upgrade and gates every later command on it, so
	// without a deadline travelling along an expired session or an expired
	// bearer token would keep full command authority for as long as the
	// connection lives.
	ExpiresAt time.Time
}

// Expired reports whether the credential behind i had already stopped
// being valid at now. An identity without a deadline never expires.
func (i Identity) Expired(now time.Time) bool {
	return !i.ExpiresAt.IsZero() && !now.Before(i.ExpiresAt)
}

// CanonicalSubject folds a subject to the single spelling every store
// and every lookup agrees on: trimmed and lower-cased. Local user
// records are keyed on it, so an [Identity] must carry it rather than
// the casing a caller happened to type — otherwise a session, a bearer
// token or an audit note issued for "Markus" is invisible to the
// revocation that a credential change for "markus" triggers.
func CanonicalSubject(subject string) string {
	return strings.TrimSpace(strings.ToLower(subject))
}

// ErrUnauthenticated marks a request that could not be resolved to
// any identity. Require emits `401 Unauthorized`.
var ErrUnauthenticated = errors.New("auth: unauthenticated")

// ErrForbidden marks a resolved identity that is missing the role.
var ErrForbidden = errors.New("auth: forbidden")

// UserStore validates (user, password) pairs for Basic auth.
type UserStore interface {
	AuthenticateBasic(ctx context.Context, username, password string) (Identity, error)
}

// TokenStore resolves a Bearer token to an identity.
type TokenStore interface {
	AuthenticateToken(ctx context.Context, token string) (Identity, error)
}

// tokenEntry is the map value inside [MemoryTokenStore]. Only the
// short display fingerprint and the full digest (never the raw token)
// are retained after Put so a heap or memory dump cannot reveal active
// bearer secrets. AuthenticateToken looks up by the map key (tokenID
// hash) in O(1) and then verifies the full digest.
type tokenEntry struct {
	fingerprint string // first-8-hex of sha256, for display only
	// digest is the FULL SHA-256 of the token. The map key is a 64-bit
	// prefix of it, which is a lookup index and not a credential: it is
	// published as the token `id` by the management API and written into
	// audit notes, so authorising on a bare map hit would let anyone who
	// reads an id brute-force a colliding string and present it as the
	// bearer token. The durable sibling store compares the full hash too.
	digest   [sha256.Size]byte
	identity Identity
}

// MemoryTokenStore is a pragmatic in-memory token store used by
// tests and the MVP bootstrap. The map is keyed on tokenID(token) so
// AuthenticateToken is O(1). Access is safe for concurrent use via
// the embedded RWMutex.
type MemoryTokenStore struct {
	mu sync.RWMutex
	// Entries are held by pointer: they are replaced wholesale by Put and
	// never mutated in place, so sharing them under the mutex is safe and the
	// read paths do not copy a fixed-size digest per lookup.
	tokens map[string]*tokenEntry // key: tokenID(rawToken)
}

// NewMemoryTokenStore constructs a store pre-populated with tokens.
// The raw token values are hashed immediately; only the display
// fingerprint is retained in memory.
func NewMemoryTokenStore(tokens map[string]Identity) *MemoryTokenStore {
	cp := make(map[string]*tokenEntry, len(tokens))
	for raw, id := range tokens {
		id.Subject = CanonicalSubject(id.Subject)
		cp[tokenID(raw)] = &tokenEntry{
			fingerprint: tokenFingerprint(raw),
			digest:      sha256.Sum256([]byte(raw)),
			identity:    id,
		}
	}
	return &MemoryTokenStore{tokens: cp}
}

// AuthenticateToken resolves token to an identity in O(1) by hashing
// the incoming value and performing a direct map lookup. Wrong tokens
// produce no match and return ErrUnauthenticated.
func (s *MemoryTokenStore) AuthenticateToken(_ context.Context, token string) (Identity, error) {
	if token == "" {
		return Identity{}, ErrUnauthenticated
	}
	// The map is keyed on tokenID(token) — a 64-bit prefix of the SHA-256,
	// which doubles as the publicly visible token id. The map hit therefore
	// only selects a candidate; the full digest is what authorises, compared
	// in constant time so a mismatch cannot be timed byte by byte.
	digest := sha256.Sum256([]byte(token))
	id := hex.EncodeToString(digest[:tokenIDBytes])
	s.mu.RLock()
	entry, ok := s.tokens[id]
	s.mu.RUnlock()
	if !ok {
		return Identity{}, ErrUnauthenticated
	}
	if subtle.ConstantTimeCompare(entry.digest[:], digest[:]) != 1 {
		return Identity{}, ErrUnauthenticated
	}
	out := entry.identity
	out.Scheme = SchemeBearer
	// Stamp the credential's own id, the same value the management API
	// publishes and `DELETE /auth/tokens/{id}` addresses. Without it every
	// identity this store issues carries an empty TokenID, and the
	// per-credential teardown that closes a revoked token's WebSocket
	// connections matches nothing — a revoked token keeps its command plane.
	out.TokenID = id
	return out, nil
}

// TokenSummary describes one entry returned by [MemoryTokenStore.List].
// The token value itself is never exposed — callers see an [ID] for
// programmatic operations (delete) and a human-readable [Fingerprint]
// (last six characters) plus the bound subject and role so they can
// audit the configured token set without leaking secrets.
type TokenSummary struct {
	// ID is a stable hex identifier derived from the SHA-256 of the
	// token. Used as the path segment in `DELETE /auth/tokens/{id}`.
	ID          string
	Fingerprint string
	Subject     string
	Role        Role
}

// tokenIDBytes is how much of the SHA-256 the public token id carries.
const tokenIDBytes = 8

// tokenID returns the stable identifier for a token — first 16 hex
// chars of the SHA-256. Long enough to avoid collisions in any
// realistic token set, short enough to fit cleanly in URLs and audit
// logs. Never returned to clients alongside the raw token in the
// same response (clients keep the token; the ID is for management).
//
// It is an index, not a credential: [MemoryTokenStore.AuthenticateToken]
// verifies the full digest after the lookup.
func tokenID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:tokenIDBytes])
}

// tokenFingerprint derives a short human-readable display value from
// a raw token. It uses the first 8 hex chars of the SHA-256 (matching
// [tokenID]) so the fingerprint never leaks token content and is
// stable across daemon restarts.
func tokenFingerprint(token string) string {
	return tokenID(token)
}

// List returns every registered token with the actual secret elided.
// Sorted by subject for stable rendering.
func (s *MemoryTokenStore) List() []TokenSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]TokenSummary, 0, len(s.tokens))
	for id, entry := range s.tokens {
		out = append(out, TokenSummary{ID: id, Fingerprint: entry.fingerprint, Subject: entry.identity.Subject, Role: entry.identity.Role})
	}
	slices.SortFunc(out, func(a, b TokenSummary) int { return cmp.Compare(a.Subject, b.Subject) })
	return out
}

// Put registers token under the supplied identity. Replaces any
// existing binding for the same token. Returns the stable
// [TokenSummary.ID] so the caller can issue subsequent
// management requests (e.g. delete) without learning the secret
// back from the daemon.
//
// The raw token is hashed immediately; only the short display
// fingerprint is retained so a heap dump cannot reveal active secrets.
//
// The subject is folded to its canonical spelling before it is stored:
// a token bound to "Bob" would be invisible to every subject-keyed
// operation — the purge behind an account deletion above all — that
// addresses the account as "bob".
func (s *MemoryTokenStore) Put(token string, id Identity) string {
	id.Subject = CanonicalSubject(id.Subject)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokens == nil {
		s.tokens = make(map[string]*tokenEntry)
	}
	tid := tokenID(token)
	s.tokens[tid] = &tokenEntry{
		fingerprint: tokenFingerprint(token),
		digest:      sha256.Sum256([]byte(token)),
		identity:    id,
	}
	return tid
}

// DeleteBySubject removes every token bound to subject and returns the
// number removed. It is the in-memory half of the purge an account
// deletion triggers: a token this store still resolves keeps
// authenticating requests for a user who no longer exists, because the
// bearer chain falls back to it when the durable store misses.
func (s *MemoryTokenStore) DeleteBySubject(_ context.Context, subject string) (int, error) {
	subject = CanonicalSubject(subject)
	if s == nil || subject == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, entry := range s.tokens {
		if CanonicalSubject(entry.identity.Subject) == subject {
			delete(s.tokens, id)
			n++
		}
	}
	return n, nil
}

// DeleteByID removes the token whose [TokenSummary.ID] matches id.
// Returns true when a token was removed, false when no token with
// that ID was registered. O(1) because the map is keyed on tokenID.
func (s *MemoryTokenStore) DeleteByID(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tokens[id]; !ok {
		return false
	}
	delete(s.tokens, id)
	return true
}

// MemoryUserStore is the matching MVP UserStore.
type MemoryUserStore struct {
	users map[string]userRecord
	// verified short-circuits the repeat password verification of a
	// credential this store has already checked. See [VerifiedBasicCache]
	// for why that is safe; a nil cache simply verifies every time.
	verified *VerifiedBasicCache
}

type userRecord struct {
	password string
	role     Role
}

// NewMemoryUserStore constructs a user store.
func NewMemoryUserStore() *MemoryUserStore {
	return &MemoryUserStore{users: make(map[string]userRecord), verified: NewVerifiedBasicCache()}
}

// Put stores or replaces a user under its canonical subject.
func (s *MemoryUserStore) Put(username, password string, role Role) {
	s.users[CanonicalSubject(username)] = userRecord{password: password, role: role}
}

// bcryptCost matches the persistent SQLite user store
// (internal/store/sqlite/users.go) so password-hash strength is uniform
// across the in-memory and persistent stores.
const bcryptCost = 12

// dummyBcryptHash is a pre-generated bcrypt hash used on the
// unknown-username path in [MemoryUserStore.AuthenticateBasic] to
// equalise response time between "no such user" and "wrong password".
// Without a dummy compare, response time leaks user existence.
// The hash was generated at cost 12 and is never used as a real
// credential — the call always returns ErrUnauthenticated.
var dummyBcryptHash = []byte("$2a$12$w3j05DkTLbO8bN3FgkOfxuNFDLEzElC42sZuPYO0eACSU6dKRLyFG")

// looksLikeBcryptHash reports whether s is a bcrypt hash string — a 60-byte
// value with a $2a$/$2b$/$2y$ prefix. Used to decide whether a stored record
// is already hashed (verify with bcrypt) or a legacy plaintext value (verify
// with a constant-time equality check), and to keep HashPassword idempotent.
func looksLikeBcryptHash(s string) bool {
	return len(s) == 60 &&
		(strings.HasPrefix(s, "$2a$") || strings.HasPrefix(s, "$2b$") || strings.HasPrefix(s, "$2y$"))
}

// HashPassword returns a bcrypt hash of password. A value that is already a
// bcrypt hash is returned unchanged, so operators may seed pre-hashed
// credentials. Call this when seeding the in-memory [MemoryUserStore] (the
// YAML `auth.users` map, the HTMX setup bootstrap) so a plaintext password is
// never held at rest.
func HashPassword(password string) (string, error) {
	if looksLikeBcryptHash(password) {
		return password, nil
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// AuthenticateBasic checks credentials in constant time. Records stored as a
// bcrypt hash are verified with bcrypt.CompareHashAndPassword; legacy plaintext
// records (test fixtures, or a value seeded before [HashPassword] wiring) fall
// back to a constant-time equality check. Both comparison paths are timing-safe.
//
// When the username is not registered a dummy bcrypt compare is performed
// against [dummyBcryptHash] so the response time is indistinguishable from
// the wrong-password path, preventing user-enumeration via timing analysis.
func (s *MemoryUserStore) AuthenticateBasic(_ context.Context, username, password string) (Identity, error) {
	subject := CanonicalSubject(username)
	rec, ok := s.users[subject]
	if !ok {
		// Consume roughly the same wall-clock as a real bcrypt verify so
		// an attacker cannot distinguish "no such user" from "wrong password"
		// by measuring response latency.
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
		return Identity{}, ErrUnauthenticated
	}
	if looksLikeBcryptHash(rec.password) {
		ok := s.verified.Verify(subject, rec.password, password, func() bool {
			return bcrypt.CompareHashAndPassword([]byte(rec.password), []byte(password)) == nil
		})
		if !ok {
			return Identity{}, ErrUnauthenticated
		}
	} else if subtle.ConstantTimeCompare([]byte(rec.password), []byte(password)) != 1 {
		return Identity{}, ErrUnauthenticated
	}
	// Report the canonical subject, not the caller's spelling: it is what
	// the record is keyed on and what every later lookup compares against.
	return Identity{Subject: subject, Scheme: SchemeBasic, Role: rec.role}, nil
}

// UserSummary is one entry of [MemoryUserStore.List]. Passwords are
// never exposed; the caller renders the username + role.
type UserSummary struct {
	Username string
	Role     Role
}

// List returns every registered user sorted by username.
func (s *MemoryUserStore) List() []UserSummary {
	out := make([]UserSummary, 0, len(s.users))
	for u, r := range s.users {
		out = append(out, UserSummary{Username: u, Role: r.role})
	}
	slices.SortFunc(out, func(a, b UserSummary) int { return cmp.Compare(a.Username, b.Username) })
	return out
}

// HasRole reports whether i is allowed to act as want. Admin covers
// everything; Operator covers Viewer; Viewer is its own level.
func (i Identity) HasRole(want Role) bool {
	switch i.Role {
	case RoleAdmin:
		return true
	case RoleOperator:
		return want == RoleOperator || want == RoleViewer
	case RoleViewer:
		return want == RoleViewer
	}
	return false
}
