// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AreaRow is one operator-defined room grouping (a floor, a shed, a
// terrace roof) sitting one level above the CCU's flat, per-central
// room list. Distinct from alarm zones (the armable alarm partitions,
// docs/alarm-concept.md §14) — same "area" word, unrelated concept.
type AreaRow struct {
	ID          string
	Name        string
	Position    int
	CreatedAtMS int64
	UpdatedAtMS int64
}

// RoomAreaRow is one (central, room) assignment to an area. The pair
// is the primary key, so a room belongs to at most one area at a
// time — assigning it elsewhere moves it.
type RoomAreaRow struct {
	CentralName string
	RoomName    string
	AreaID      string
}

// AreaStore persists areas and their room assignments.
type AreaStore struct {
	db *sql.DB
}

// NewAreaStore returns a store backed by db.
func NewAreaStore(db *sql.DB) *AreaStore { return &AreaStore{db: db} }

// Upsert inserts or updates the area row identified by row.ID.
// CreatedAtMS is written on insert only; an update leaves the
// existing created_at_ms untouched.
func (s *AreaStore) Upsert(ctx context.Context, row AreaRow) error {
	const q = `
INSERT INTO areas (id, name, position, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name,
    position = excluded.position,
    updated_at_ms = excluded.updated_at_ms`
	_, err := s.db.ExecContext(ctx, q, row.ID, row.Name, row.Position, row.CreatedAtMS, row.UpdatedAtMS)
	if err != nil {
		return fmt.Errorf("sqlite: upsert area: %w", err)
	}
	return nil
}

// Get returns the area with id. The boolean reports whether it exists.
func (s *AreaStore) Get(ctx context.Context, id string) (AreaRow, bool, error) {
	const q = `
SELECT id, name, position, created_at_ms, updated_at_ms
FROM areas WHERE id = ?`
	row, err := scanAreaRow(s.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AreaRow{}, false, nil
	}
	if err != nil {
		return AreaRow{}, false, fmt.Errorf("sqlite: get area: %w", err)
	}
	return row, true, nil
}

// GetAll returns every area ordered by position, then name.
func (s *AreaStore) GetAll(ctx context.Context) ([]AreaRow, error) {
	const q = `
SELECT id, name, position, created_at_ms, updated_at_ms
FROM areas ORDER BY position, name`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get all areas: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AreaRow
	for rows.Next() {
		row, err := scanAreaRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan area: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Delete removes the area row of id and every room_areas row assigned
// to it, in one transaction — a deleted area never leaves orphaned
// assignments behind for the next ListAssignments to surface.
func (s *AreaStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: delete area: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM room_areas WHERE area_id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: delete area: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM areas WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: delete area: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: delete area: %w", err)
	}
	return nil
}

// ListAssignments returns every (central, room) -> area assignment
// across all areas, ordered by central then room. Callers group by
// AreaID to assemble each area's room list without one query per area.
func (s *AreaStore) ListAssignments(ctx context.Context) ([]RoomAreaRow, error) {
	const q = `
SELECT central_name, room_name, area_id
FROM room_areas ORDER BY central_name, room_name`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list room-area assignments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []RoomAreaRow
	for rows.Next() {
		var row RoomAreaRow
		if err := rows.Scan(&row.CentralName, &row.RoomName, &row.AreaID); err != nil {
			return nil, fmt.Errorf("sqlite: scan room-area assignment: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ReplaceRooms atomically replaces the full room set of areaID with
// refs — one transaction: delete every row this area currently owns,
// then insert the new set with INSERT OR REPLACE. Because the primary
// key is (central_name, room_name), a room already assigned to
// ANOTHER area is silently moved rather than rejected — one area per
// room is enforced by the key itself, not by a pre-check.
func (s *AreaStore) ReplaceRooms(ctx context.Context, areaID string, refs []RoomAreaRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: replace area rooms: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM room_areas WHERE area_id = ?`, areaID); err != nil {
		return fmt.Errorf("sqlite: replace area rooms: %w", err)
	}
	const q = `
INSERT OR REPLACE INTO room_areas (central_name, room_name, area_id)
VALUES (?, ?, ?)`
	for i := range refs {
		r := &refs[i]
		if _, err := tx.ExecContext(ctx, q, r.CentralName, r.RoomName, areaID); err != nil {
			return fmt.Errorf("sqlite: replace area rooms: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: replace area rooms: %w", err)
	}
	return nil
}

func scanAreaRow(sc scannable) (AreaRow, error) {
	var row AreaRow
	if err := sc.Scan(&row.ID, &row.Name, &row.Position, &row.CreatedAtMS, &row.UpdatedAtMS); err != nil {
		return AreaRow{}, err
	}
	return row, nil
}
