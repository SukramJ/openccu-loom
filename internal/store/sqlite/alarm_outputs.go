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

// AlarmOutputRow is one enrolled alarm consequence (docs/alarm-concept.md
// §14). Class is the user-declared output class (acoustic_siren,
// switched_siren, smoke_sounder, optical_siren, alarm_light, chirp,
// notification, sysvar_mirror) — the class, not the device type, decides
// which safety invariants apply. Notification targets carry no data-point
// identity, so CentralName/InterfaceID/ChannelAddress may be empty.
// Durations, tones, indoor/outdoor and per-mode assignment live in
// ConfigJSON.
type AlarmOutputRow struct {
	ID             string
	ZoneID         string
	Class          hmenum.AlarmOutputClass
	CentralName    string
	InterfaceID    string
	ChannelAddress string
	Name           string
	ConfigJSON     string
	CreatedAtMS    int64
	UpdatedAtMS    int64
}

// AlarmOutputStore persists enrolled alarm outputs.
type AlarmOutputStore struct {
	db *sql.DB
}

// NewAlarmOutputStore returns a store backed by db.
func NewAlarmOutputStore(db *sql.DB) *AlarmOutputStore { return &AlarmOutputStore{db: db} }

// Upsert inserts or updates the output row identified by row.ID.
// CreatedAtMS is written on insert only; an update leaves the existing
// created_at_ms untouched.
func (s *AlarmOutputStore) Upsert(ctx context.Context, row AlarmOutputRow) error {
	const q = `
INSERT INTO alarm_outputs (id, zone_id, class, central_name, interface_id, channel_address,
    name, config_json, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    zone_id = excluded.zone_id,
    class = excluded.class,
    central_name = excluded.central_name,
    interface_id = excluded.interface_id,
    channel_address = excluded.channel_address,
    name = excluded.name,
    config_json = excluded.config_json,
    updated_at_ms = excluded.updated_at_ms`
	_, err := s.db.ExecContext(
		ctx, q,
		row.ID, row.ZoneID, string(row.Class), row.CentralName, row.InterfaceID, row.ChannelAddress,
		row.Name, row.ConfigJSON, row.CreatedAtMS, row.UpdatedAtMS,
	)
	if err != nil {
		return fmt.Errorf("sqlite: upsert alarm output: %w", err)
	}
	return nil
}

// Get returns the output with id. The boolean reports whether it exists.
func (s *AlarmOutputStore) Get(ctx context.Context, id string) (AlarmOutputRow, bool, error) {
	row, err := scanAlarmOutputRow(s.db.QueryRowContext(ctx, alarmOutputSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AlarmOutputRow{}, false, nil
	}
	if err != nil {
		return AlarmOutputRow{}, false, fmt.Errorf("sqlite: get alarm output: %w", err)
	}
	return row, true, nil
}

// ListByZone returns the outputs enrolled in zoneID, ordered by name then
// id.
func (s *AlarmOutputStore) ListByZone(ctx context.Context, zoneID string) ([]AlarmOutputRow, error) {
	q := alarmOutputSelect + ` WHERE zone_id = ? ORDER BY name, id`
	rows, err := s.db.QueryContext(ctx, q, zoneID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list alarm outputs by zone: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AlarmOutputRow
	for rows.Next() {
		row, err := scanAlarmOutputRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan alarm output: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetAll returns every enrolled alarm output, ordered by zone, then name,
// then id.
func (s *AlarmOutputStore) GetAll(ctx context.Context) ([]AlarmOutputRow, error) {
	q := alarmOutputSelect + ` ORDER BY zone_id, name, id`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get all alarm outputs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AlarmOutputRow
	for rows.Next() {
		row, err := scanAlarmOutputRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan alarm output: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Delete removes the output row of id.
func (s *AlarmOutputStore) Delete(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM alarm_outputs WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: delete alarm output: %w", err)
	}
	return nil
}

// DeleteByZone removes every output enrolled in zoneID and returns the
// number of rows deleted (zone-deletion cascade).
func (s *AlarmOutputStore) DeleteByZone(ctx context.Context, zoneID string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM alarm_outputs WHERE zone_id = ?`, zoneID)
	if err != nil {
		return 0, fmt.Errorf("sqlite: delete alarm outputs by zone: %w", err)
	}
	return res.RowsAffected()
}

const alarmOutputSelect = `
SELECT id, zone_id, class, central_name, interface_id, channel_address,
    name, config_json, created_at_ms, updated_at_ms
FROM alarm_outputs`

func scanAlarmOutputRow(sc scannable) (AlarmOutputRow, error) {
	var row AlarmOutputRow
	var class string
	if err := sc.Scan(&row.ID, &row.ZoneID, &class, &row.CentralName, &row.InterfaceID, &row.ChannelAddress,
		&row.Name, &row.ConfigJSON, &row.CreatedAtMS, &row.UpdatedAtMS); err != nil {
		return AlarmOutputRow{}, err
	}
	row.Class = hmenum.AlarmOutputClass(class)
	return row, nil
}

// ReplaceByZone atomically replaces the output set of an zone — one
// transaction, no partial sets on failure (mirrors the sensor store).
func (s *AlarmOutputStore) ReplaceByZone(ctx context.Context, zoneID string, rows []AlarmOutputRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: replace alarm outputs: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM alarm_outputs WHERE zone_id = ?`, zoneID); err != nil {
		return fmt.Errorf("sqlite: replace alarm outputs: %w", err)
	}
	const q = `
INSERT INTO alarm_outputs (id, zone_id, class, central_name, interface_id, channel_address,
    name, config_json, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for i := range rows {
		row := &rows[i]
		if _, err := tx.ExecContext(
			ctx, q,
			row.ID, zoneID, string(row.Class), row.CentralName, row.InterfaceID, row.ChannelAddress,
			row.Name, row.ConfigJSON, row.CreatedAtMS, row.UpdatedAtMS,
		); err != nil {
			return fmt.Errorf("sqlite: replace alarm outputs: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: replace alarm outputs: %w", err)
	}
	return nil
}
