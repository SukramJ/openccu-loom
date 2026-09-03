// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RecordingOverride is one per-datapoint recording override. A present
// row forces recording on/off for exactly one (central, interface,
// channel, parameter) tuple, overriding the parameter-name glob policy.
type RecordingOverride struct {
	CentralName    string
	InterfaceID    string
	ChannelAddress string
	Parameter      string
	Record         bool
	UpdatedBy      string
	UpdatedAt      string
}

// RecordingOverrideStore persists per-datapoint recording overrides in
// the history database (shared handle with [MeasurementStore]; the
// measurement store owns closing it).
type RecordingOverrideStore struct {
	db *sql.DB
}

// NewRecordingOverrideStore returns a store backed by the history
// database handle.
func NewRecordingOverrideStore(db *sql.DB) *RecordingOverrideStore {
	return &RecordingOverrideStore{db: db}
}

// List returns every recording override across all centrals. Used once
// at wire time to populate the in-memory overlay.
func (s *RecordingOverrideStore) List(ctx context.Context) ([]RecordingOverride, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT central_name, interface_id, channel_address, parameter, record, updated_by, updated_at
          FROM measurement_recording_overrides
    `)
	if err != nil {
		return nil, fmt.Errorf("recording_overrides.List: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []RecordingOverride
	for rows.Next() {
		var o RecordingOverride
		var rec int
		if err := rows.Scan(&o.CentralName, &o.InterfaceID, &o.ChannelAddress,
			&o.Parameter, &rec, &o.UpdatedBy, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("recording_overrides.List scan: %w", err)
		}
		o.Record = rec != 0
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recording_overrides.List rows: %w", err)
	}
	return out, nil
}

// Set upserts a recording override for one data point.
func (s *RecordingOverrideStore) Set(
	ctx context.Context,
	centralName, interfaceID, channelAddress, parameter string,
	record bool, updatedBy string,
) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO measurement_recording_overrides
            (central_name, interface_id, channel_address, parameter, record, updated_by, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(central_name, interface_id, channel_address, parameter)
        DO UPDATE SET record = excluded.record,
                      updated_by = excluded.updated_by,
                      updated_at = excluded.updated_at
    `, centralName, interfaceID, channelAddress, parameter, boolToInt(record),
		updatedBy, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("recording_overrides.Set: %w", err)
	}
	return nil
}

// Clear removes a recording override, reverting the data point to the
// glob policy. Deleting an absent row is a no-op.
func (s *RecordingOverrideStore) Clear(
	ctx context.Context,
	centralName, interfaceID, channelAddress, parameter string,
) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
        DELETE FROM measurement_recording_overrides
         WHERE central_name = ? AND interface_id = ? AND channel_address = ? AND parameter = ?
    `, centralName, interfaceID, channelAddress, parameter)
	if err != nil {
		return fmt.Errorf("recording_overrides.Clear: %w", err)
	}
	return nil
}

// DeleteDevice removes every override for every channel of the given device.
// Prefix-safe ("DEVICE" never matches "DEVICE2:0").
//
// Reached on device-remove / unpair alongside the measurement purge, through
// [adapter.WireMeasurementEviction] and pinned with it. Like the measurement
// purge it had no caller until that seam existed.
func (s *RecordingOverrideStore) DeleteDevice(
	ctx context.Context, centralName, interfaceID, deviceAddress string,
) error {
	if s == nil || s.db == nil {
		return nil
	}
	prefix := deviceAddress + ":"
	_, err := s.db.ExecContext(ctx, `
        DELETE FROM measurement_recording_overrides
         WHERE central_name = ?
           AND interface_id = ?
           AND (channel_address = ? OR channel_address LIKE ? || '%' ESCAPE '\')
    `, centralName, interfaceID, deviceAddress, prefix)
	if err != nil {
		return fmt.Errorf("recording_overrides.DeleteDevice: %w", err)
	}
	return nil
}

// DeleteForCentral removes every override for a central, across every
// interface and device. Called on live central removal.
func (s *RecordingOverrideStore) DeleteForCentral(ctx context.Context, centralName string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM measurement_recording_overrides WHERE central_name = ?`, centralName); err != nil {
		return fmt.Errorf("recording_overrides.DeleteForCentral: %w", err)
	}
	return nil
}
