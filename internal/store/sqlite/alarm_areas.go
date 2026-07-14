// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AlarmAreaRow is one independently armable partition (docs/alarm-concept.md
// §14). ConfigJSON carries the bounded per-mode configuration document
// (delays, output policy, post-trigger policy, central-loss policy, blocker
// policies); it is always loaded and saved as a whole and never queried
// relationally.
type AlarmAreaRow struct {
	ID          string
	Name        string
	Position    int
	ConfigJSON  string
	CreatedAtMS int64
	UpdatedAtMS int64
}

// AlarmAreaStore persists alarm areas.
type AlarmAreaStore struct {
	db *sql.DB
}

// NewAlarmAreaStore returns a store backed by db.
func NewAlarmAreaStore(db *sql.DB) *AlarmAreaStore { return &AlarmAreaStore{db: db} }

// Upsert inserts or updates the area row identified by row.ID. CreatedAtMS
// is written on insert only; an update leaves the existing created_at_ms
// untouched.
func (s *AlarmAreaStore) Upsert(ctx context.Context, row AlarmAreaRow) error {
	const q = `
INSERT INTO alarm_areas (id, name, position, config_json, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name,
    position = excluded.position,
    config_json = excluded.config_json,
    updated_at_ms = excluded.updated_at_ms`
	_, err := s.db.ExecContext(
		ctx, q,
		row.ID, row.Name, row.Position, row.ConfigJSON, row.CreatedAtMS, row.UpdatedAtMS,
	)
	if err != nil {
		return fmt.Errorf("sqlite: upsert alarm area: %w", err)
	}
	return nil
}

// Get returns the area with id. The boolean reports whether it exists.
func (s *AlarmAreaStore) Get(ctx context.Context, id string) (AlarmAreaRow, bool, error) {
	const q = `
SELECT id, name, position, config_json, created_at_ms, updated_at_ms
FROM alarm_areas WHERE id = ?`
	row, err := scanAlarmAreaRow(s.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AlarmAreaRow{}, false, nil
	}
	if err != nil {
		return AlarmAreaRow{}, false, fmt.Errorf("sqlite: get alarm area: %w", err)
	}
	return row, true, nil
}

// GetAll returns every alarm area ordered by position, then name.
func (s *AlarmAreaStore) GetAll(ctx context.Context) ([]AlarmAreaRow, error) {
	const q = `
SELECT id, name, position, config_json, created_at_ms, updated_at_ms
FROM alarm_areas ORDER BY position, name`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get all alarm areas: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AlarmAreaRow
	for rows.Next() {
		row, err := scanAlarmAreaRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan alarm area: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Delete removes the area row of id.
func (s *AlarmAreaStore) Delete(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM alarm_areas WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: delete alarm area: %w", err)
	}
	return nil
}

func scanAlarmAreaRow(sc scannable) (AlarmAreaRow, error) {
	var row AlarmAreaRow
	if err := sc.Scan(&row.ID, &row.Name, &row.Position, &row.ConfigJSON, &row.CreatedAtMS, &row.UpdatedAtMS); err != nil {
		return AlarmAreaRow{}, err
	}
	return row, nil
}
