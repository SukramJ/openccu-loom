// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// VisibilityUnIgnoreStore persists the per-central un_ignore patterns
// — `MODEL:CHANNEL:PARAMETER` strings (with `*` wildcards) that promote
// otherwise-hidden parameters into the visible data-point surface.
//
// Reads happen at daemon start (one query per central) and after every
// REST PUT (read-back to confirm what was applied). Writes replace the
// whole list for a central in one transaction so the table never carries
// a half-applied edit.
type VisibilityUnIgnoreStore struct {
	db *sql.DB
}

// NewVisibilityUnIgnoreStore returns a store backed by db.
func NewVisibilityUnIgnoreStore(db *sql.DB) *VisibilityUnIgnoreStore {
	return &VisibilityUnIgnoreStore{db: db}
}

// Close releases the underlying database handle. Safe on a nil store or
// nil handle. Callers that opened the DB for this store (daemon wiring,
// unit tests) must Close it so the file is released — Windows refuses to
// delete an open SQLite file at temp-dir cleanup.
func (s *VisibilityUnIgnoreStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// UnIgnoreEntry is one persisted row.
type UnIgnoreEntry struct {
	Pattern   string    `json:"pattern"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by,omitempty"`
}

// List returns every persisted pattern for centralName, sorted by
// pattern. Returns an empty slice when the central has no rows.
func (s *VisibilityUnIgnoreStore) List(ctx context.Context, centralName string) ([]UnIgnoreEntry, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	const q = `SELECT pattern, updated_at, updated_by
               FROM visibility_unignore
               WHERE central_name = ?
               ORDER BY pattern`
	rows, err := s.db.QueryContext(ctx, q, centralName)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list visibility_unignore: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []UnIgnoreEntry
	for rows.Next() {
		var e UnIgnoreEntry
		var tsRaw string
		if err := rows.Scan(&e.Pattern, &tsRaw, &e.UpdatedBy); err != nil {
			return nil, fmt.Errorf("sqlite: scan visibility_unignore: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, tsRaw); err == nil {
			e.UpdatedAt = t
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate visibility_unignore: %w", err)
	}
	return out, nil
}

// Patterns is the convenience accessor for callers that only need the
// pattern strings (e.g. the Registry.LoadUnIgnore feed at daemon start).
func (s *VisibilityUnIgnoreStore) Patterns(ctx context.Context, centralName string) ([]string, error) {
	entries, err := s.List(ctx, centralName)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Pattern)
	}
	return out, nil
}

// DeleteForCentral removes every persisted un_ignore pattern for
// centralName. Called on live central removal, so a re-adopted central
// under the same name (or a different CCU later given that name) starts
// from an empty pattern set instead of the previous incarnation's rows
// silently reviving through the [central.Registry.OnRegister] observer
// that replays whatever SQLite still holds.
func (s *VisibilityUnIgnoreStore) DeleteForCentral(ctx context.Context, centralName string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM visibility_unignore WHERE central_name = ?`, centralName); err != nil {
		return fmt.Errorf("visibility_unignore.DeleteForCentral: %w", err)
	}
	return nil
}

// Replace swaps the full pattern set for centralName atomically. Empty
// `patterns` clears the list. updatedBy is stamped onto every retained
// pattern. Duplicate / blank patterns in the input are deduped.
func (s *VisibilityUnIgnoreStore) Replace(ctx context.Context, centralName string, patterns []string, updatedBy string) error {
	if s == nil || s.db == nil {
		return nil
	}
	dedup := make(map[string]struct{}, len(patterns))
	for _, p := range patterns {
		if p == "" {
			continue
		}
		dedup[p] = struct{}{}
	}
	cleaned := make([]string, 0, len(dedup))
	for p := range dedup {
		cleaned = append(cleaned, p)
	}
	sort.Strings(cleaned)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin visibility_unignore tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM visibility_unignore WHERE central_name = ?`,
		centralName,
	); err != nil {
		return fmt.Errorf("sqlite: clear visibility_unignore: %w", err)
	}
	if len(cleaned) > 0 {
		stamp := time.Now().UTC().Format(time.RFC3339Nano)
		const ins = `INSERT INTO visibility_unignore (central_name, pattern, updated_at, updated_by)
                     VALUES (?, ?, ?, ?)`
		stmt, err := tx.PrepareContext(ctx, ins)
		if err != nil {
			return fmt.Errorf("sqlite: prepare visibility_unignore insert: %w", err)
		}
		defer func() { _ = stmt.Close() }()
		for _, p := range cleaned {
			if _, err := stmt.ExecContext(ctx, centralName, p, stamp, updatedBy); err != nil {
				return fmt.Errorf("sqlite: insert visibility_unignore %q: %w", p, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit visibility_unignore tx: %w", err)
	}
	return nil
}

// SeedIfEmpty inserts patterns for centralName only when the table has
// zero rows for that central. Used by the daemon bootstrap to apply the
// config.yaml `centrals[].visibility.un_ignore` seed without overriding
// runtime edits.
func (s *VisibilityUnIgnoreStore) SeedIfEmpty(ctx context.Context, centralName string, patterns []string) error {
	if s == nil || s.db == nil || len(patterns) == 0 {
		return nil
	}
	var count int
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM visibility_unignore WHERE central_name = ?`,
		centralName,
	).Scan(&count); err != nil {
		return fmt.Errorf("sqlite: count visibility_unignore: %w", err)
	}
	if count > 0 {
		return nil
	}
	return s.Replace(ctx, centralName, patterns, "config_yaml")
}
