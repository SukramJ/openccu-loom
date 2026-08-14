// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// TokenStore is the SQLite-backed [auth.TokenStore].
//
// Tokens are stored as SHA-256 hashes; the plaintext is returned exactly
// once at creation and never persisted. The display fingerprint is a
// prefix of the SHA-256 hash — derived from the hash, never from the
// plaintext, so nothing recoverable about the secret is persisted or
// surfaced in list responses / audit notes.
type TokenStore struct {
	db *sql.DB
}

// NewTokenStore returns a store backed by db.
func NewTokenStore(db *sql.DB) *TokenStore {
	return &TokenStore{db: db}
}

var _ auth.TokenStore = (*TokenStore)(nil)

// ErrTokenNotFound is returned when a Delete or lookup misses.
var ErrTokenNotFound = errors.New("sqlite: token not found")

// hashToken is sha256(secret) → hex. Constant-time-compatible at
// the byte level when paired with subtle.ConstantTimeCompare.
func hashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// fingerprintFromHash derives the display fingerprint from a token's
// SHA-256 hash (hex). A 12-hex-char (48-bit) prefix is short enough to
// eyeball yet collision-free in practice, and — unlike a slice of the
// plaintext — reveals nothing usable about the secret. Create returns it
// so the operator can record which handle maps to the token they saved.
func fingerprintFromHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

// CreateInput is the payload for [TokenStore.Create].
type CreateInput struct {
	Subject string
	Role    auth.Role
	// ExpiresAt, when non-nil, bounds the token's lifetime; a nil value
	// creates a token that never expires (the historical behaviour).
	ExpiresAt *time.Time
}

// CreateResult carries the plaintext token (returned exactly once)
// and the persistent fingerprint.
type CreateResult struct {
	Token       string // shown once, never persisted in plaintext
	Fingerprint string // stable identifier for management
}

// Create generates a fresh 32-byte URL-safe token, persists its
// hash + fingerprint + identity, and returns the plaintext token in
// the result. The caller MUST show the token to the operator
// immediately — the daemon cannot recover it from disk.
//
// The subject is canonicalised before it is written: it is free-form
// operator input, while the identity it produces at authentication time is
// compared verbatim by every per-subject store (preferences, private diagram
// ownership) and has to line up with the users row it belongs to.
func (s *TokenStore) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	subject := auth.CanonicalSubject(in.Subject)
	if subject == "" {
		return CreateResult{}, errors.New("sqlite: token subject required")
	}
	if in.Role == "" {
		return CreateResult{}, errors.New("sqlite: token role required")
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return CreateResult{}, fmt.Errorf("sqlite: token rand: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	hash := hashToken(token)
	fp := fingerprintFromHash(hash)
	var expires any
	if in.ExpiresAt != nil {
		expires = in.ExpiresAt.UTC()
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens (fingerprint, token_hash, subject, role, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		fp, hash, subject, string(in.Role), time.Now().UTC(), expires); err != nil {
		return CreateResult{}, fmt.Errorf("sqlite: token insert: %w", err)
	}
	return CreateResult{Token: token, Fingerprint: fp}, nil
}

// Count returns the number of tokens in the table.
func (s *TokenStore) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tokens`).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite: tokens count: %w", err)
	}
	return n, nil
}

// Import inserts a token whose plaintext secret is already known — a
// legacy config-file (YAML) token migrated into the store on upgrade,
// now that API tokens live only in SQLite and no longer round-trip
// through the north.rest config section. Unlike [TokenStore.Create] it
// preserves the operator's exact secret (only its SHA-256 hash is
// persisted), so external clients keep authenticating with the same
// bearer value. Idempotent: a token whose fingerprint already exists is
// left untouched, so re-running the migration is a no-op.
func (s *TokenStore) Import(ctx context.Context, secret, subject string, role auth.Role) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", errors.New("sqlite: token secret required")
	}
	subject = auth.CanonicalSubject(subject)
	if subject == "" {
		return "", errors.New("sqlite: token subject required")
	}
	if role == "" {
		return "", errors.New("sqlite: token role required")
	}
	hash := hashToken(secret)
	fp := fingerprintFromHash(hash)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens (fingerprint, token_hash, subject, role, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(fingerprint) DO NOTHING`,
		fp, hash, subject, string(role), time.Now().UTC()); err != nil {
		return "", fmt.Errorf("sqlite: token import: %w", err)
	}
	return fp, nil
}

// DeleteBySubject removes every token issued to subject and returns the
// count removed. Called when the underlying user account is deleted so a
// bearer token bound to a now-nonexistent subject cannot keep
// authenticating.
//
// Both sides are canonicalised, and the comparison stays case-insensitive on
// top of that: a token that outlives the account it belongs to is a live
// credential for a deleted user, so a row an external writer left in a
// spelling of its own must not escape the purge either.
func (s *TokenStore) DeleteBySubject(ctx context.Context, subject string) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM tokens WHERE subject = ? COLLATE NOCASE`, auth.CanonicalSubject(subject))
	if err != nil {
		return 0, fmt.Errorf("sqlite: tokens delete by subject: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Delete removes a token by fingerprint.
func (s *TokenStore) Delete(ctx context.Context, fp string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tokens WHERE fingerprint = ?`, fp)
	if err != nil {
		return fmt.Errorf("sqlite: token delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrTokenNotFound
	}
	return nil
}

// AuthenticateToken resolves a Bearer token. Returns
// [auth.ErrUnauthenticated] on mismatch / unknown token / empty
// secret so middleware can emit a uniform 401.
func (s *TokenStore) AuthenticateToken(ctx context.Context, secret string) (auth.Identity, error) {
	if secret == "" {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	hash := hashToken(secret)
	var (
		fp        string
		subject   string
		role      string
		expiresAt sql.NullTime
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT fingerprint, subject, role, expires_at FROM tokens WHERE token_hash = ?`,
		hash).Scan(&fp, &subject, &role, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	if err != nil {
		return auth.Identity{}, fmt.Errorf("sqlite: token authn: %w", err)
	}
	// Reject an expired token as if it were unknown — a uniform 401, no
	// distinction that would confirm the secret was once valid.
	if expiresAt.Valid && !time.Now().UTC().Before(expiresAt.Time) {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	_, _ = s.db.ExecContext(ctx,
		`UPDATE tokens SET last_seen_at = ? WHERE fingerprint = ?`,
		time.Now().UTC(), fp)
	return auth.Identity{
		Subject: subject,
		Scheme:  auth.SchemeBearer,
		Role:    auth.Role(role),
		TokenID: fp,
	}, nil
}

// TokenRow is one entry of [TokenStore.List]; the secret is never
// surfaced.
type TokenRow struct {
	Fingerprint string
	Subject     string
	Role        auth.Role
	CreatedAt   time.Time
	LastSeenAt  *time.Time
	ExpiresAt   *time.Time
}

// List returns every token sorted by subject.
func (s *TokenStore) List(ctx context.Context) ([]TokenRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT fingerprint, subject, role, created_at, last_seen_at, expires_at
		 FROM tokens ORDER BY subject, fingerprint`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: tokens list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []TokenRow
	for rows.Next() {
		var r TokenRow
		var lastSeen, expiresAt sql.NullTime
		if err := rows.Scan(&r.Fingerprint, &r.Subject, &r.Role, &r.CreatedAt, &lastSeen, &expiresAt); err != nil {
			return nil, fmt.Errorf("sqlite: tokens list scan: %w", err)
		}
		if lastSeen.Valid {
			r.LastSeenAt = &lastSeen.Time
		}
		if expiresAt.Valid {
			r.ExpiresAt = &expiresAt.Time
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
