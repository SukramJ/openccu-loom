// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// MasterValuesStore persists MASTER paramset values per channel so that
// the daemon can rehydrate them at startup without issuing
// getParamset(MASTER) against the CCU. See migration
// 015_master_values.sql for the schema rationale.
//
// Multi-CCU safe: one shared instance per *sql.DB, all rows scoped by
// (central_name, interface_id).
type MasterValuesStore struct {
	db *sql.DB
}

// NewMasterValuesStore returns a store backed by db. The store itself
// holds no in-memory cache; the per-channel cache is the SQLite row
// itself.
func NewMasterValuesStore(db *sql.DB) *MasterValuesStore {
	return &MasterValuesStore{db: db}
}

// Close releases the underlying database handle. Safe on a nil store or
// nil handle. Callers that opened the DB for this store (daemon wiring,
// unit tests) must Close it so the file is released — Windows refuses to
// delete an open SQLite file at temp-dir cleanup.
func (s *MasterValuesStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// LoadChannel returns the cached MASTER values for the given channel as
// a parameter→value map. ok=false signals a cache miss: callers must
// then fetch from the CCU and persist the result via [SaveChannel].
//
// A row whose value_json fails to decode is treated as missing for the
// affected parameter; the remaining parameters still populate the
// returned map.
func (s *MasterValuesStore) LoadChannel(
	ctx context.Context, centralName, interfaceID, channelAddress string,
) (values map[string]any, found bool, err error) {
	if s == nil || s.db == nil {
		return nil, false, nil
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT parameter_name, value_json
          FROM master_values
         WHERE central_name = ? AND interface_id = ? AND channel_address = ?
    `, centralName, interfaceID, channelAddress)
	if err != nil {
		return nil, false, fmt.Errorf("master_values.LoadChannel: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]any)
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			return nil, false, fmt.Errorf("master_values.LoadChannel scan: %w", err)
		}
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			continue
		}
		out[name] = v
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("master_values.LoadChannel rows: %w", err)
	}
	if len(out) == 0 {
		return nil, false, nil
	}
	return out, true, nil
}

// SaveChannel upserts the provided values for the channel. An empty
// values map is a no-op; nil entries are skipped (a parameter the CCU
// returned as nil should not overwrite a previously cached value).
func (s *MasterValuesStore) SaveChannel(
	ctx context.Context, centralName, interfaceID, channelAddress string, values map[string]any,
) error {
	if s == nil || s.db == nil || len(values) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("master_values.SaveChannel begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO master_values (central_name, interface_id, channel_address, parameter_name, value_json)
        VALUES (?, ?, ?, ?, ?)
        ON CONFLICT (central_name, interface_id, channel_address, parameter_name) DO UPDATE
            SET value_json = excluded.value_json,
                updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
    `)
	if err != nil {
		return fmt.Errorf("master_values.SaveChannel prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for name, v := range values {
		if v == nil {
			continue
		}
		raw, err := json.Marshal(v)
		if err != nil {
			continue
		}
		if _, err := stmt.ExecContext(ctx, centralName, interfaceID, channelAddress, name, string(raw)); err != nil {
			return fmt.Errorf("master_values.SaveChannel exec %s.%s: %w", channelAddress, name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("master_values.SaveChannel commit: %w", err)
	}
	return nil
}

// SaveParameter upserts a single parameter value. Used by the
// operator-write path where Channel.WriteParamset commits one parameter
// at a time.
func (s *MasterValuesStore) SaveParameter(
	ctx context.Context, centralName, interfaceID, channelAddress, parameterName string, value any,
) error {
	if s == nil || s.db == nil || value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("master_values.SaveParameter marshal: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
        INSERT INTO master_values (central_name, interface_id, channel_address, parameter_name, value_json)
        VALUES (?, ?, ?, ?, ?)
        ON CONFLICT (central_name, interface_id, channel_address, parameter_name) DO UPDATE
            SET value_json = excluded.value_json,
                updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
    `, centralName, interfaceID, channelAddress, parameterName, string(raw))
	if err != nil {
		return fmt.Errorf("master_values.SaveParameter: %w", err)
	}
	return nil
}

// DeleteChannel removes all cached MASTER values for the channel. Used
// when a channel disappears from the device profile (rare).
func (s *MasterValuesStore) DeleteChannel(
	ctx context.Context, centralName, interfaceID, channelAddress string,
) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
        DELETE FROM master_values
         WHERE central_name = ? AND interface_id = ? AND channel_address = ?
    `, centralName, interfaceID, channelAddress)
	if err != nil {
		return fmt.Errorf("master_values.DeleteChannel: %w", err)
	}
	return nil
}

// DeleteDevice removes all cached MASTER values for every channel of
// the given device. Used on device removal / unpair. The device
// address is the prefix shared by every channel address
// ("00021BE9957782" matches "00021BE9957782:0", ...:1, ...).
func (s *MasterValuesStore) DeleteDevice(
	ctx context.Context, centralName, interfaceID, deviceAddress string,
) error {
	if s == nil || s.db == nil {
		return nil
	}
	prefix := strings.TrimRight(deviceAddress, ":") + ":"
	_, err := s.db.ExecContext(ctx, `
        DELETE FROM master_values
         WHERE central_name = ? AND interface_id = ?
           AND (channel_address = ? OR channel_address LIKE ? || '%' ESCAPE '\')
    `, centralName, interfaceID, deviceAddress, prefix)
	if err != nil {
		return fmt.Errorf("master_values.DeleteDevice: %w", err)
	}
	return nil
}

// DeleteForInterface removes every cached MASTER value for one (central, interface).
func (s *MasterValuesStore) DeleteForInterface(ctx context.Context, centralName, interfaceID string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `
        DELETE FROM master_values
         WHERE central_name = ? AND interface_id = ?
    `, centralName, interfaceID)
	if err != nil {
		return 0, fmt.Errorf("master_values.DeleteForInterface: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("master_values.DeleteForInterface: %w", err)
	}
	return n, nil
}
