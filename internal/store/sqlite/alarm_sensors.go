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

// AlarmSensorRow is one enrolled binary trigger source (docs/alarm-concept.md
// §14). The data point identity is stored as discrete columns
// (CentralName + interface/channel/parameter) so enrolment lookups by data
// point stay indexable; the mode matrix and behaviour flags live in
// ConfigJSON.
type AlarmSensorRow struct {
	ID             string
	AreaID         string
	CentralName    string
	InterfaceID    string
	ChannelAddress string
	Parameter      string
	SensorType     hmenum.AlarmSensorType
	Name           string
	ConfigJSON     string
	CreatedAtMS    int64
	UpdatedAtMS    int64
}

// AlarmSensorStore persists enrolled alarm sensors.
type AlarmSensorStore struct {
	db *sql.DB
}

// NewAlarmSensorStore returns a store backed by db.
func NewAlarmSensorStore(db *sql.DB) *AlarmSensorStore { return &AlarmSensorStore{db: db} }

// Upsert inserts or updates the sensor row identified by row.ID.
// CreatedAtMS is written on insert only; an update leaves the existing
// created_at_ms untouched.
func (s *AlarmSensorStore) Upsert(ctx context.Context, row AlarmSensorRow) error {
	const q = `
INSERT INTO alarm_sensors (id, area_id, central_name, interface_id, channel_address, parameter,
    sensor_type, name, config_json, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    area_id = excluded.area_id,
    central_name = excluded.central_name,
    interface_id = excluded.interface_id,
    channel_address = excluded.channel_address,
    parameter = excluded.parameter,
    sensor_type = excluded.sensor_type,
    name = excluded.name,
    config_json = excluded.config_json,
    updated_at_ms = excluded.updated_at_ms`
	_, err := s.db.ExecContext(
		ctx, q,
		row.ID, row.AreaID, row.CentralName, row.InterfaceID, row.ChannelAddress, row.Parameter,
		string(row.SensorType), row.Name, row.ConfigJSON, row.CreatedAtMS, row.UpdatedAtMS,
	)
	if err != nil {
		return fmt.Errorf("sqlite: upsert alarm sensor: %w", err)
	}
	return nil
}

// Get returns the sensor with id. The boolean reports whether it exists.
func (s *AlarmSensorStore) Get(ctx context.Context, id string) (AlarmSensorRow, bool, error) {
	row, err := scanAlarmSensorRow(s.db.QueryRowContext(ctx, alarmSensorSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AlarmSensorRow{}, false, nil
	}
	if err != nil {
		return AlarmSensorRow{}, false, fmt.Errorf("sqlite: get alarm sensor: %w", err)
	}
	return row, true, nil
}

// ListByArea returns the sensors enrolled in areaID, ordered by name then
// id.
func (s *AlarmSensorStore) ListByArea(ctx context.Context, areaID string) ([]AlarmSensorRow, error) {
	q := alarmSensorSelect + ` WHERE area_id = ? ORDER BY name, id`
	rows, err := s.db.QueryContext(ctx, q, areaID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list alarm sensors by area: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AlarmSensorRow
	for rows.Next() {
		row, err := scanAlarmSensorRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan alarm sensor: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetAll returns every enrolled alarm sensor, ordered by area, then name,
// then id.
func (s *AlarmSensorStore) GetAll(ctx context.Context) ([]AlarmSensorRow, error) {
	q := alarmSensorSelect + ` ORDER BY area_id, name, id`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get all alarm sensors: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AlarmSensorRow
	for rows.Next() {
		row, err := scanAlarmSensorRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan alarm sensor: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Delete removes the sensor row of id.
func (s *AlarmSensorStore) Delete(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM alarm_sensors WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: delete alarm sensor: %w", err)
	}
	return nil
}

// DeleteByArea removes every sensor enrolled in areaID and returns the
// number of rows deleted (area-deletion cascade).
func (s *AlarmSensorStore) DeleteByArea(ctx context.Context, areaID string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM alarm_sensors WHERE area_id = ?`, areaID)
	if err != nil {
		return 0, fmt.Errorf("sqlite: delete alarm sensors by area: %w", err)
	}
	return res.RowsAffected()
}

const alarmSensorSelect = `
SELECT id, area_id, central_name, interface_id, channel_address, parameter,
    sensor_type, name, config_json, created_at_ms, updated_at_ms
FROM alarm_sensors`

func scanAlarmSensorRow(sc scannable) (AlarmSensorRow, error) {
	var row AlarmSensorRow
	var sensorType string
	if err := sc.Scan(&row.ID, &row.AreaID, &row.CentralName, &row.InterfaceID, &row.ChannelAddress,
		&row.Parameter, &sensorType, &row.Name, &row.ConfigJSON, &row.CreatedAtMS, &row.UpdatedAtMS); err != nil {
		return AlarmSensorRow{}, err
	}
	row.SensorType = hmenum.AlarmSensorType(sensorType)
	return row, nil
}
