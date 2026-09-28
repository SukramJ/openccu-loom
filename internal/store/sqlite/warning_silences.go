// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// WarningSilenceStore persists per-operator silences on the operator-warnings
// surface. A silence is scoped to (username, warning_id) so two operators
// silencing the same warning do not affect each other, and it expires on its
// own (Until) rather than requiring an explicit unsilence — a stale silence
// never hides a warning forever.
type WarningSilenceStore struct {
	db *sql.DB
}

// NewWarningSilenceStore returns a store backed by db.
func NewWarningSilenceStore(db *sql.DB) *WarningSilenceStore { return &WarningSilenceStore{db: db} }

// Silences returns every silence for username as a map from warning ID to
// the time it expires.
func (s *WarningSilenceStore) Silences(ctx context.Context, username string) (map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT warning_id, until FROM warning_silences WHERE username = ?`, username)
	if err != nil {
		return nil, fmt.Errorf("sqlite: warning silences: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]time.Time)
	for rows.Next() {
		var warningID string
		var until time.Time
		if err := rows.Scan(&warningID, &until); err != nil {
			return nil, fmt.Errorf("sqlite: scan warning silence: %w", err)
		}
		out[warningID] = until
	}
	return out, rows.Err()
}

// Set upserts a silence for (username, warningID), expiring at until.
// created_at is set to now only on first insert; a re-silence of an
// already-silenced warning keeps its original created_at.
func (s *WarningSilenceStore) Set(ctx context.Context, username, warningID string, until, now time.Time) error {
	const q = `
INSERT INTO warning_silences (username, warning_id, until, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(username, warning_id) DO UPDATE SET until = excluded.until`
	if _, err := s.db.ExecContext(ctx, q, username, warningID, until.UTC(), now.UTC()); err != nil {
		return fmt.Errorf("sqlite: silence warning: %w", err)
	}
	return nil
}

// Delete removes a single (username, warningID) silence. Removing a
// silence that does not exist is not an error.
func (s *WarningSilenceStore) Delete(ctx context.Context, username, warningID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM warning_silences WHERE username = ? AND warning_id = ?`, username, warningID); err != nil {
		return fmt.Errorf("sqlite: unsilence warning: %w", err)
	}
	return nil
}

// DeleteExpired removes every silence (across all users) whose until is at
// or before now.
func (s *WarningSilenceStore) DeleteExpired(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM warning_silences WHERE until <= ?`, now.UTC()); err != nil {
		return fmt.Errorf("sqlite: delete expired warning silences: %w", err)
	}
	return nil
}

// DeleteOtherThan removes username's silences whose warning ID is not in
// activeIDs. A silenced warning whose condition has cleared and later
// re-occurs must alert again rather than staying silently suppressed under
// a stale row, so the caller passes the currently-active warning IDs on
// every evaluation pass and this prunes the rest. An empty activeIDs
// deletes every silence username holds.
func (s *WarningSilenceStore) DeleteOtherThan(ctx context.Context, username string, activeIDs []string) error {
	if len(activeIDs) == 0 {
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM warning_silences WHERE username = ?`, username); err != nil {
			return fmt.Errorf("sqlite: delete all warning silences: %w", err)
		}
		return nil
	}
	// The only concatenated fragment comes from repeatPlaceholders, which
	// emits nothing but ", ?" — every warning id travels as a bound
	// argument, so no caller-controlled text reaches the statement.
	//nolint:gosec // G202: placeholders only; ids are bound parameters
	q := `DELETE FROM warning_silences WHERE username = ? AND warning_id NOT IN (?` +
		repeatPlaceholders(len(activeIDs)-1) + `)`
	args := make([]any, 0, len(activeIDs)+1)
	args = append(args, username)
	for _, id := range activeIDs {
		args = append(args, id)
	}
	if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("sqlite: delete other warning silences: %w", err)
	}
	return nil
}
