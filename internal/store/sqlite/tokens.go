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
// Tokens are stored as bcrypt-style salted SHA-256 hashes — we keep
// the SHA-256 fingerprint (last 6 chars of the URL-safe base64) for
// UI display and the full hash for authentication. Plaintext is
// returned exactly once at creation.
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

// fingerprint derives the last-six-chars display fingerprint of a
// token. Stable for a given token, never collides for cleanly random
// 32-byte tokens.
func fingerprint(secret string) string {
	if len(secret) <= 6 {
		return secret
	}
	return "…" + secret[len(secret)-6:]
}

// CreateInput is the payload for [TokenStore.Create].
type CreateInput struct {
	Subject string
	Role    auth.Role
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
func (s *TokenStore) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	subject := strings.TrimSpace(in.Subject)
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
	fp := fingerprint(token)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens (fingerprint, token_hash, subject, role, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		fp, hashToken(token), subject, string(in.Role), time.Now().UTC()); err != nil {
		return CreateResult{}, fmt.Errorf("sqlite: token insert: %w", err)
	}
	return CreateResult{Token: token, Fingerprint: fp}, nil
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
		fp      string
		subject string
		role    string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT fingerprint, subject, role FROM tokens WHERE token_hash = ?`,
		hash).Scan(&fp, &subject, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	if err != nil {
		return auth.Identity{}, fmt.Errorf("sqlite: token authn: %w", err)
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
}

// List returns every token sorted by subject.
func (s *TokenStore) List(ctx context.Context) ([]TokenRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT fingerprint, subject, role, created_at, last_seen_at
		 FROM tokens ORDER BY subject, fingerprint`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: tokens list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []TokenRow
	for rows.Next() {
		var r TokenRow
		var lastSeen sql.NullTime
		if err := rows.Scan(&r.Fingerprint, &r.Subject, &r.Role, &r.CreatedAt, &lastSeen); err != nil {
			return nil, fmt.Errorf("sqlite: tokens list scan: %w", err)
		}
		if lastSeen.Valid {
			r.LastSeenAt = &lastSeen.Time
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
