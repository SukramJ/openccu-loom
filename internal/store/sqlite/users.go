// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// UserStore is the SQLite-backed [auth.UserStore]. Replaces the
// in-memory + YAML bootstrap path on production daemons; tests can
// keep using [auth.MemoryUserStore].
//
// Passwords are persisted as bcrypt hashes (cost 12). The plaintext
// is only ever in transit during [UserStore.Put] / authentication.
type UserStore struct {
	db *sql.DB
}

// NewUserStore returns a store backed by db.
func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{db: db}
}

// Compile-time assertion.
var _ auth.UserStore = (*UserStore)(nil)

// ErrUserNotFound is returned when a lookup misses.
var ErrUserNotFound = errors.New("sqlite: user not found")

// ErrLastAdmin is returned when a Delete or role-change would leave
// the table with zero admins. Callers translate this into a 409.
var ErrLastAdmin = errors.New("sqlite: refusing to remove the last admin")

const bcryptCost = 12

// Put creates or replaces a user. Empty username / password are
// rejected so the table cannot accumulate sentinel rows.
func (s *UserStore) Put(ctx context.Context, subject, password string, role auth.Role) error {
	subject = strings.TrimSpace(strings.ToLower(subject))
	if subject == "" {
		return errors.New("sqlite: user subject required")
	}
	if password == "" {
		return errors.New("sqlite: user password required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("sqlite: bcrypt: %w", err)
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO users (subject, password_hash, role, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(subject) DO UPDATE SET password_hash=excluded.password_hash,
		     role=excluded.role, updated_at=excluded.updated_at`,
		subject, string(hash), string(role), now, now)
	if err != nil {
		return fmt.Errorf("sqlite: users upsert: %w", err)
	}
	return nil
}

// Delete removes a user. Refuses to remove the last admin so the
// daemon never locks itself out.
func (s *UserStore) Delete(ctx context.Context, subject string) error {
	subject = strings.TrimSpace(strings.ToLower(subject))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: users delete: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var role string
	err = tx.QueryRowContext(ctx, `SELECT role FROM users WHERE subject = ?`, subject).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("sqlite: users delete: select: %w", err)
	}
	if role == string(auth.RoleAdmin) {
		var adminCount int
		err = tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM users WHERE role = ?`, string(auth.RoleAdmin)).Scan(&adminCount)
		if err != nil {
			return fmt.Errorf("sqlite: users delete: admin count: %w", err)
		}
		if adminCount <= 1 {
			return ErrLastAdmin
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE subject = ?`, subject); err != nil {
		return fmt.Errorf("sqlite: users delete: exec: %w", err)
	}
	return tx.Commit()
}

// dummyBcryptHash is a pre-generated bcrypt hash compared against on the
// unknown-user path so the login latency does not leak whether a subject
// exists. It never matches any real password.
var dummyBcryptHash = []byte("$2a$12$w3j05DkTLbO8bN3FgkOfxuNFDLEzElC42sZuPYO0eACSU6dKRLyFG")

// AuthenticateBasic resolves credentials. Uses bcrypt.CompareHashAndPassword
// for constant-time comparison.
func (s *UserStore) AuthenticateBasic(ctx context.Context, username, password string) (auth.Identity, error) {
	subject := strings.TrimSpace(strings.ToLower(username))
	if subject == "" || password == "" {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	var hash, role string
	err := s.db.QueryRowContext(ctx,
		`SELECT password_hash, role FROM users WHERE subject = ?`, subject).Scan(&hash, &role)
	if errors.Is(err, sql.ErrNoRows) {
		// Consume roughly the same wall-clock as a real bcrypt verify so an
		// attacker cannot distinguish "no such user" from "wrong password"
		// by measuring response latency (user enumeration via timing).
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	if err != nil {
		return auth.Identity{}, fmt.Errorf("sqlite: users authn: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE users SET last_seen_at = ? WHERE subject = ?`, time.Now().UTC(), subject)
	return auth.Identity{Subject: username, Scheme: auth.SchemeBasic, Role: auth.Role(role)}, nil
}

// UserRow is one entry from [UserStore.List].
type UserRow struct {
	Subject    string
	Role       auth.Role
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastSeenAt *time.Time
}

// List returns every user sorted by subject.
func (s *UserStore) List(ctx context.Context) ([]UserRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT subject, role, created_at, updated_at, last_seen_at
		 FROM users ORDER BY subject`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: users list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []UserRow
	for rows.Next() {
		var r UserRow
		var lastSeen sql.NullTime
		if err := rows.Scan(&r.Subject, &r.Role, &r.CreatedAt, &r.UpdatedAt, &lastSeen); err != nil {
			return nil, fmt.Errorf("sqlite: users list scan: %w", err)
		}
		if lastSeen.Valid {
			r.LastSeenAt = &lastSeen.Time
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Count returns the number of users in the table.
func (s *UserStore) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sqlite: users count: %w", err)
	}
	return n, nil
}
