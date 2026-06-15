// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"maps"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// Scheme identifies how a request authenticated.
type Scheme string

// Scheme values.
const (
	SchemeBasic   Scheme = "basic"
	SchemeBearer  Scheme = "bearer"
	SchemeSession Scheme = "session" // reserved for future session auth
)

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

// MemoryTokenStore is a pragmatic in-memory token store used by
// tests and the MVP bootstrap. It compares in constant time and is
// safe for concurrent access via the embedded RWMutex.
type MemoryTokenStore struct {
	mu     sync.RWMutex
	tokens map[string]Identity
}

// NewMemoryTokenStore constructs a store pre-populated with tokens.
func NewMemoryTokenStore(tokens map[string]Identity) *MemoryTokenStore {
	cp := make(map[string]Identity, len(tokens))
	maps.Copy(cp, tokens)
	return &MemoryTokenStore{tokens: cp}
}

// AuthenticateToken matches token against every registered entry in
// constant time, falling through to ErrUnauthenticated on a miss.
func (s *MemoryTokenStore) AuthenticateToken(_ context.Context, token string) (Identity, error) {
	if token == "" {
		return Identity{}, ErrUnauthenticated
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for k, id := range s.tokens {
		if subtle.ConstantTimeCompare([]byte(k), []byte(token)) == 1 {
			id.Scheme = SchemeBearer
			return id, nil
		}
	}
	return Identity{}, ErrUnauthenticated
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

// tokenID returns the stable identifier for a token — first 16 hex
// chars of the SHA-256. Long enough to avoid collisions in any
// realistic token set, short enough to fit cleanly in URLs and audit
// logs. Never returned to clients alongside the raw token in the
// same response (clients keep the token; the ID is for management).
func tokenID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

// List returns every registered token with the actual secret elided.
// Sorted by subject for stable rendering.
func (s *MemoryTokenStore) List() []TokenSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]TokenSummary, 0, len(s.tokens))
	for token, id := range s.tokens {
		fp := token
		if len(fp) > 6 {
			fp = "…" + fp[len(fp)-6:]
		}
		out = append(out, TokenSummary{ID: tokenID(token), Fingerprint: fp, Subject: id.Subject, Role: id.Role})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Subject > out[j].Subject; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Put registers token under the supplied identity. Replaces any
// existing binding for the same token verbatim. Returns the stable
// [TokenSummary.ID] so the caller can issue subsequent
// management requests (e.g. delete) without learning the secret
// back from the daemon.
func (s *MemoryTokenStore) Put(token string, id Identity) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokens == nil {
		s.tokens = make(map[string]Identity)
	}
	s.tokens[token] = id
	return tokenID(token)
}

// DeleteByID removes the token whose [TokenSummary.ID] matches id.
// Returns true when a token was removed, false when no token with
// that ID was registered.
func (s *MemoryTokenStore) DeleteByID(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token := range s.tokens {
		if tokenID(token) == id {
			delete(s.tokens, token)
			return true
		}
	}
	return false
}

// MemoryUserStore is the matching MVP UserStore.
type MemoryUserStore struct {
	users map[string]userRecord
}

type userRecord struct {
	password string
	role     Role
}

// NewMemoryUserStore constructs a user store.
func NewMemoryUserStore() *MemoryUserStore {
	return &MemoryUserStore{users: make(map[string]userRecord)}
}

// Put stores or replaces a user.
func (s *MemoryUserStore) Put(username, password string, role Role) {
	s.users[strings.ToLower(username)] = userRecord{password: password, role: role}
}

// bcryptCost matches the persistent SQLite user store
// (internal/store/sqlite/users.go) so password-hash strength is uniform
// across the in-memory and persistent stores.
const bcryptCost = 12

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
func (s *MemoryUserStore) AuthenticateBasic(_ context.Context, username, password string) (Identity, error) {
	rec, ok := s.users[strings.ToLower(username)]
	if !ok {
		return Identity{}, ErrUnauthenticated
	}
	if looksLikeBcryptHash(rec.password) {
		if bcrypt.CompareHashAndPassword([]byte(rec.password), []byte(password)) != nil {
			return Identity{}, ErrUnauthenticated
		}
	} else if subtle.ConstantTimeCompare([]byte(rec.password), []byte(password)) != 1 {
		return Identity{}, ErrUnauthenticated
	}
	return Identity{Subject: username, Scheme: SchemeBasic, Role: rec.role}, nil
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
	// stable order
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Username > out[j].Username; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
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
