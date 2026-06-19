// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// AuthSessionStore is the SQLite-backed [auth.SessionPersistence].
//
// It durably stores browser auth sessions so the in-memory
// [auth.SessionStore] can hydrate on boot and survive a daemon restart.
// Times are persisted as Unix seconds and reconstructed in UTC.
type AuthSessionStore struct {
	db *sql.DB
}

// NewAuthSessionStore returns a store backed by db.
func NewAuthSessionStore(db *sql.DB) *AuthSessionStore {
	return &AuthSessionStore{db: db}
}

var _ auth.SessionPersistence = (*AuthSessionStore)(nil)

// SaveSession inserts (or replaces) the row for sess.ID. Mirrors the
// in-memory map's last-write-wins semantics so a re-issued id overwrites.
func (s *AuthSessionStore) SaveSession(ctx context.Context, sess *auth.Session) error {
	if sess == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO auth_sessions
		    (id, subject, scheme, role, token_id, created_unix, expires_unix)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sess.ID,
		sess.Identity.Subject,
		string(sess.Identity.Scheme),
		string(sess.Identity.Role),
		sess.Identity.TokenID,
		sess.Created.Unix(),
		sess.Expires.Unix())
	if err != nil {
		return fmt.Errorf("sqlite: auth_session save: %w", err)
	}
	return nil
}

// DeleteSession removes the row for id. No-op when absent.
func (s *AuthSessionStore) DeleteSession(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM auth_sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: auth_session delete: %w", err)
	}
	return nil
}

// LoadActiveSessions returns every session whose expiry is still in the
// future relative to now. Used to hydrate the in-memory store on boot.
func (s *AuthSessionStore) LoadActiveSessions(ctx context.Context, now time.Time) ([]*auth.Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, subject, scheme, role, token_id, created_unix, expires_unix
		 FROM auth_sessions WHERE expires_unix > ?`, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("sqlite: auth_sessions load: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []*auth.Session
	for rows.Next() {
		var (
			id          string
			subject     string
			scheme      string
			role        string
			tokenID     string
			createdUnix int64
			expiresUnix int64
		)
		if err := rows.Scan(&id, &subject, &scheme, &role, &tokenID, &createdUnix, &expiresUnix); err != nil {
			return nil, fmt.Errorf("sqlite: auth_sessions scan: %w", err)
		}
		out = append(out, &auth.Session{
			ID: id,
			Identity: auth.Identity{
				Subject: subject,
				Scheme:  auth.Scheme(scheme),
				Role:    auth.Role(role),
				TokenID: tokenID,
			},
			Created: time.Unix(createdUnix, 0).UTC(),
			Expires: time.Unix(expiresUnix, 0).UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: auth_sessions rows: %w", err)
	}
	return out, nil
}

// DeleteExpiredSessions removes every row whose expiry is at or before
// now and returns the number of rows deleted.
func (s *AuthSessionStore) DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM auth_sessions WHERE expires_unix <= ?`, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("sqlite: auth_sessions purge: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
