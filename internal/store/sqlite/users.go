// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	// verified short-circuits the repeat password verification of a
	// credential this store has already checked — see
	// [auth.VerifiedBasicCache] for why that is safe. A nil cache verifies
	// every time.
	verified *auth.VerifiedBasicCache
}

// NewUserStore returns a store backed by db.
func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{db: db, verified: auth.NewVerifiedBasicCache()}
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
// rejected so the table cannot accumulate sentinel rows. Returns
// [ErrLastAdmin] when the write would demote the only remaining admin —
// the same lockout the [UserStore.Delete] guard prevents, reached by
// changing a role instead of removing a row.
func (s *UserStore) Put(ctx context.Context, subject, password string, role auth.Role) error {
	subject = auth.CanonicalSubject(subject)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: users upsert: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// The count and the write share one transaction so two concurrent
	// demotions cannot each observe a second admin that the other removes.
	if role != auth.RoleAdmin {
		var current string
		err = tx.QueryRowContext(ctx, `SELECT role FROM users WHERE subject = ?`, subject).Scan(&current)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("sqlite: users upsert: select: %w", err)
		}
		if current == string(auth.RoleAdmin) {
			var adminCount int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM users WHERE role = ?`, string(auth.RoleAdmin)).Scan(&adminCount); err != nil {
				return fmt.Errorf("sqlite: users upsert: admin count: %w", err)
			}
			if adminCount <= 1 {
				return ErrLastAdmin
			}
		}
	}

	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO users (subject, password_hash, role, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(subject) DO UPDATE SET password_hash=excluded.password_hash,
		     role=excluded.role, updated_at=excluded.updated_at`,
		subject, string(hash), string(role), now, now)
	if err != nil {
		return fmt.Errorf("sqlite: users upsert: %w", err)
	}
	return tx.Commit()
}

// SetRole changes a user's role and leaves the stored password hash
// untouched. It is the write behind a role-only update: [UserStore.Put]
// needs a plaintext password to hash, which an admin who only moves an
// account between roles never has.
//
// Returns [ErrUserNotFound] when the subject has no row and
// [ErrLastAdmin] when the change would leave the table with zero admins —
// the same lockout guard [UserStore.Put] and [UserStore.Delete] apply,
// and for the same reason it lives inside the transaction: two concurrent
// demotions must not each observe the admin the other is removing.
func (s *UserStore) SetRole(ctx context.Context, subject string, role auth.Role) error {
	subject = auth.CanonicalSubject(subject)
	if subject == "" {
		return errors.New("sqlite: user subject required")
	}
	if role == "" {
		return errors.New("sqlite: user role required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: users set role: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var current string
	err = tx.QueryRowContext(ctx, `SELECT role FROM users WHERE subject = ?`, subject).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("sqlite: users set role: select: %w", err)
	}
	if current == string(auth.RoleAdmin) && role != auth.RoleAdmin {
		var adminCount int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM users WHERE role = ?`, string(auth.RoleAdmin)).Scan(&adminCount); err != nil {
			return fmt.Errorf("sqlite: users set role: admin count: %w", err)
		}
		if adminCount <= 1 {
			return ErrLastAdmin
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET role = ?, updated_at = ? WHERE subject = ?`,
		string(role), time.Now().UTC(), subject); err != nil {
		return fmt.Errorf("sqlite: users set role: exec: %w", err)
	}
	return tx.Commit()
}

// Delete removes a user. Refuses to remove the last admin so the
// daemon never locks itself out.
func (s *UserStore) Delete(ctx context.Context, subject string) error {
	subject = auth.CanonicalSubject(subject)
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
	subject := auth.CanonicalSubject(username)
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
	ok := s.verified.Verify(subject, hash, password, func() bool {
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	})
	if !ok {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE users SET last_seen_at = ? WHERE subject = ?`, time.Now().UTC(), subject)
	// Report the subject the row is keyed on, not the casing the caller
	// typed: the session, the audit trail and every revocation hook behind
	// a credential change compare against the stored spelling.
	return auth.Identity{Subject: subject, Scheme: auth.SchemeBasic, Role: auth.Role(role)}, nil
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
