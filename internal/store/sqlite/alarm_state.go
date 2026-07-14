// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// AlarmStateRow is the continuously persisted arm-state of one alarm
// area. TimersJSON carries the redundant timer tuples (wall deadline,
// remaining duration, persist-time timestamp, boot counter) that let a
// restart restore or expire countdowns deterministically and detect
// implausible clocks. ContextJSON carries per-sensor runtime markers a
// restore must not lose (sensors open at arm completion, the pending
// cause). The engine owns both encodings.
type AlarmStateRow struct {
	AreaID      string
	State       hmenum.AlarmAreaState
	Mode        hmenum.AlarmMode
	BypassJSON  string
	IncidentID  int64
	TimersJSON  string
	ContextJSON string
	UpdatedAtMS int64
}

// AlarmStateStore persists the per-area arm state (one row per area,
// written through on every transition).
type AlarmStateStore struct {
	db *sql.DB
}

// NewAlarmStateStore returns a store backed by db.
func NewAlarmStateStore(db *sql.DB) *AlarmStateStore { return &AlarmStateStore{db: db} }

// Upsert writes the full state row for row.AreaID.
func (s *AlarmStateStore) Upsert(ctx context.Context, row AlarmStateRow) error {
	const q = `
INSERT INTO alarm_state (area_id, state, mode, bypass_json, incident_id, timers_json, context_json, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(area_id) DO UPDATE SET
    state = excluded.state,
    mode = excluded.mode,
    bypass_json = excluded.bypass_json,
    incident_id = excluded.incident_id,
    timers_json = excluded.timers_json,
    context_json = excluded.context_json,
    updated_at_ms = excluded.updated_at_ms`
	_, err := s.db.ExecContext(
		ctx, q,
		row.AreaID, string(row.State), string(row.Mode), row.BypassJSON,
		row.IncidentID, row.TimersJSON, row.ContextJSON, row.UpdatedAtMS,
	)
	if err != nil {
		return fmt.Errorf("sqlite: upsert alarm state: %w", err)
	}
	return nil
}

// Get returns the persisted state of areaID. The boolean reports
// whether a row exists.
func (s *AlarmStateStore) Get(ctx context.Context, areaID string) (AlarmStateRow, bool, error) {
	const q = `
SELECT area_id, state, mode, bypass_json, incident_id, timers_json, context_json, updated_at_ms
FROM alarm_state WHERE area_id = ?`
	row, err := scanAlarmStateRow(s.db.QueryRowContext(ctx, q, areaID))
	if errors.Is(err, sql.ErrNoRows) {
		return AlarmStateRow{}, false, nil
	}
	if err != nil {
		return AlarmStateRow{}, false, fmt.Errorf("sqlite: get alarm state: %w", err)
	}
	return row, true, nil
}

// GetAll returns every persisted area state.
func (s *AlarmStateStore) GetAll(ctx context.Context) ([]AlarmStateRow, error) {
	const q = `
SELECT area_id, state, mode, bypass_json, incident_id, timers_json, context_json, updated_at_ms
FROM alarm_state ORDER BY area_id`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get all alarm states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AlarmStateRow
	for rows.Next() {
		row, err := scanAlarmStateRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan alarm state: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Delete removes the state row of areaID (area deletion).
func (s *AlarmStateStore) Delete(ctx context.Context, areaID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM alarm_state WHERE area_id = ?`, areaID); err != nil {
		return fmt.Errorf("sqlite: delete alarm state: %w", err)
	}
	return nil
}

func scanAlarmStateRow(sc scannable) (AlarmStateRow, error) {
	var row AlarmStateRow
	var state, mode string
	if err := sc.Scan(&row.AreaID, &state, &mode, &row.BypassJSON,
		&row.IncidentID, &row.TimersJSON, &row.ContextJSON, &row.UpdatedAtMS); err != nil {
		return AlarmStateRow{}, err
	}
	row.State = hmenum.AlarmAreaState(state)
	row.Mode = hmenum.AlarmMode(mode)
	return row, nil
}
