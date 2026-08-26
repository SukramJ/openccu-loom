// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package sqlite — SessionRecorderStore persists and loads session.Recorder
// entries to/from the session_recorder table. This enables production-replay
// diagnosis that survives daemon restarts.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/store/session"
)

// DefaultMaxLoadEntriesSR is the default cap for SessionRecorderStore.Load.
// Mirrors a practical upper bound: 1 000 entries keep memory pressure low
// while still covering a full production session.
//
// Distinct from session.DefaultMaxLoadEntries to avoid a naming collision
// inside the sqlite package itself (the sqlite package defines its own
// constant here for documentation purposes; callers should prefer passing
// session.DefaultMaxLoadEntries via the Recorder.Load method).
const DefaultMaxLoadEntriesSR = session.DefaultMaxLoadEntries

// SessionRecorderStore persists SessionRecorder entries and satisfies the
// session.PersistStore interface.
//
// Usage:
//
//	store := sqlite.NewSessionRecorderStore(db)
//	rec.Persist(ctx, store, "main", "diag-2026")
//	rec.Load(ctx, store, "main", "diag-2026", 0)
type SessionRecorderStore struct {
	db *sql.DB
}

// NewSessionRecorderStore returns a store backed by db.
func NewSessionRecorderStore(db *sql.DB) *SessionRecorderStore {
	return &SessionRecorderStore{db: db}
}

// Compile-time assertion: SessionRecorderStore must satisfy session.PersistStore.
var _ session.PersistStore = (*SessionRecorderStore)(nil)

// PersistAll replaces all rows for (centralName, slug) with the provided
// rows inside a single transaction. Existing rows for that scope are
// deleted first so the table stays in sync with the in-memory recorder.
// Satisfies session.PersistStore.
func (s *SessionRecorderStore) PersistAll(ctx context.Context, centralName, slug string, rows []session.PersistRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: session_recorder: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM session_recorder WHERE central_name = ? AND slug = ?`,
		centralName, slug,
	); err != nil {
		return fmt.Errorf("sqlite: session_recorder: delete old: %w", err)
	}

	const ins = `
INSERT INTO session_recorder
    (central_name, slug, rpc_type, method, frozen_params, response_json, recorded_at, ttl_seconds)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	stmt, err := tx.PrepareContext(ctx, ins)
	if err != nil {
		return fmt.Errorf("sqlite: session_recorder: prepare insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i := range rows {
		if _, err := stmt.ExecContext(
			ctx,
			rows[i].CentralName, rows[i].Slug, rows[i].RPCType, rows[i].Method, rows[i].FrozenParams, rows[i].ResponseJSON,
			rows[i].RecordedAt.UTC().Format(time.RFC3339Nano), rows[i].TTLSeconds,
		); err != nil {
			return fmt.Errorf("sqlite: session_recorder: insert row: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: session_recorder: commit: %w", err)
	}
	return nil
}

// Load returns at most maxEntries rows for (centralName, slug) ordered
// by recorded_at descending (most recent first). If maxEntries <= 0,
// DefaultMaxLoadEntriesSR is used.
// Satisfies session.PersistStore.
func (s *SessionRecorderStore) Load(ctx context.Context, centralName, slug string, maxEntries int) ([]session.LoadRow, error) {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxLoadEntriesSR
	}
	const q = `
SELECT rpc_type, method, frozen_params, response_json, recorded_at, ttl_seconds
FROM session_recorder
WHERE central_name = ? AND slug = ?
ORDER BY recorded_at DESC
LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, centralName, slug, maxEntries)
	if err != nil {
		return nil, fmt.Errorf("sqlite: session_recorder: load: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []session.LoadRow
	for rows.Next() {
		var r session.LoadRow
		var recAt string
		if err := rows.Scan(&r.RPCType, &r.Method, &r.FrozenParams, &r.ResponseJSON, &recAt, &r.TTLSeconds); err != nil {
			return nil, fmt.Errorf("sqlite: session_recorder: scan: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, recAt); err == nil {
			r.RecordedAt = t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteAll removes all rows for (centralName, slug). Used when a
// session is explicitly cleared.
func (s *SessionRecorderStore) DeleteAll(ctx context.Context, centralName, slug string) error {
	_, err := s.db.ExecContext(
		ctx,
		`DELETE FROM session_recorder WHERE central_name = ? AND slug = ?`,
		centralName, slug,
	)
	if err != nil {
		return fmt.Errorf("sqlite: session_recorder: delete all: %w", err)
	}
	return nil
}

// CountEntries returns the number of persisted rows for (centralName, slug).
func (s *SessionRecorderStore) CountEntries(ctx context.Context, centralName, slug string) (int, error) {
	var n int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM session_recorder WHERE central_name = ? AND slug = ?`,
		centralName, slug,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sqlite: session_recorder: count: %w", err)
	}
	return n, nil
}
