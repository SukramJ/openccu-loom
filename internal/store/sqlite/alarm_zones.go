// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AlarmZoneRow is one independently armable partition (docs/alarm-concept.md
// §14). ConfigJSON carries the bounded per-mode configuration document
// (delays, output policy, post-trigger policy, central-loss policy, blocker
// policies); it is always loaded and saved as a whole and never queried
// relationally.
type AlarmZoneRow struct {
	ID   string
	Name string
	// Slug is the stable, human-readable external identifier. It is
	// derived from the name once and then frozen: it reaches consumer
	// entity ids and MQTT topics, so letting it follow a rename would
	// orphan every entity of that zone on every rename.
	//
	// Empty on read means a row written before the column existed; the
	// caller derives one rather than falling back to the UUID, which is
	// unusable in an entity id.
	Slug        string
	Position    int
	ConfigJSON  string
	CreatedAtMS int64
	UpdatedAtMS int64
}

// AlarmZoneStore persists alarm zones.
type AlarmZoneStore struct {
	db *sql.DB
}

// NewAlarmZoneStore returns a store backed by db.
func NewAlarmZoneStore(db *sql.DB) *AlarmZoneStore { return &AlarmZoneStore{db: db} }

// Upsert inserts or updates the zone row identified by row.ID. CreatedAtMS
// is written on insert only; an update leaves the existing created_at_ms
// untouched.
// The slug is deliberately absent from the conflict-update list: it is
// frozen at creation. Letting a rename move it would orphan every
// consumer entity of that zone, which is the whole reason it exists
// beside the UUID.
func (s *AlarmZoneStore) Upsert(ctx context.Context, row AlarmZoneRow) error {
	const q = `
INSERT INTO alarm_zones (id, name, slug, position, config_json, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name,
    position = excluded.position,
    config_json = excluded.config_json,
    updated_at_ms = excluded.updated_at_ms`
	_, err := s.db.ExecContext(
		ctx, q,
		row.ID, row.Name, row.Slug, row.Position, row.ConfigJSON, row.CreatedAtMS, row.UpdatedAtMS,
	)
	if err != nil {
		return fmt.Errorf("sqlite: upsert alarm zone: %w", err)
	}
	return nil
}

// Get returns the zone with id. The boolean reports whether it exists.
func (s *AlarmZoneStore) Get(ctx context.Context, id string) (AlarmZoneRow, bool, error) {
	const q = `
SELECT id, name, slug, position, config_json, created_at_ms, updated_at_ms
FROM alarm_zones WHERE id = ?`
	row, err := scanAlarmZoneRow(s.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AlarmZoneRow{}, false, nil
	}
	if err != nil {
		return AlarmZoneRow{}, false, fmt.Errorf("sqlite: get alarm zone: %w", err)
	}
	return row, true, nil
}

// GetAll returns every alarm zone ordered by position, then name.
func (s *AlarmZoneStore) GetAll(ctx context.Context) ([]AlarmZoneRow, error) {
	const q = `
SELECT id, name, slug, position, config_json, created_at_ms, updated_at_ms
FROM alarm_zones ORDER BY position, name`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get all alarm zones: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AlarmZoneRow
	for rows.Next() {
		row, err := scanAlarmZoneRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan alarm zone: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Delete removes the zone row of id.
func (s *AlarmZoneStore) Delete(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM alarm_zones WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: delete alarm zone: %w", err)
	}
	return nil
}

func scanAlarmZoneRow(sc scannable) (AlarmZoneRow, error) {
	var row AlarmZoneRow
	if err := sc.Scan(&row.ID, &row.Name, &row.Slug, &row.Position, &row.ConfigJSON, &row.CreatedAtMS, &row.UpdatedAtMS); err != nil {
		return AlarmZoneRow{}, err
	}
	return row, nil
}
