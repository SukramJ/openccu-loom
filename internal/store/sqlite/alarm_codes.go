// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AlarmCodeRow is one alarm-system user code or hardware-identity
// binding (notes/concepts/alarm-concept.md §11, migration 028). Kind is one of
// "pin" (argon2id-hashed in Hash), "keypad_slot" or "remote_key" (Hash
// empty; the binding lives in BindingJSON). PermsJSON, ZonesJSON and
// BindingJSON are always loaded and saved as a whole and never queried
// relationally; the domain facade in internal/alarm/codes owns their
// shape.
type AlarmCodeRow struct {
	ID           string
	Name         string
	Kind         string
	Hash         string
	Duress       bool
	PermsJSON    string
	ZonesJSON    string
	BindingJSON  string
	ValidFromMS  int64
	ValidUntilMS int64
	Enabled      bool
	CreatedAtMS  int64
	UpdatedAtMS  int64
}

// AlarmCodeStore persists alarm codes and hardware-identity bindings.
type AlarmCodeStore struct {
	db *sql.DB
}

// NewAlarmCodeStore returns a store backed by db.
func NewAlarmCodeStore(db *sql.DB) *AlarmCodeStore { return &AlarmCodeStore{db: db} }

// Upsert inserts or updates the code row identified by row.ID.
// CreatedAtMS is written on insert only; an update leaves the existing
// created_at_ms untouched.
func (s *AlarmCodeStore) Upsert(ctx context.Context, row AlarmCodeRow) error {
	const q = `
INSERT INTO alarm_codes (id, name, kind, hash, duress, perms_json, zones_json, binding_json,
    valid_from_ms, valid_until_ms, enabled, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name,
    kind = excluded.kind,
    hash = excluded.hash,
    duress = excluded.duress,
    perms_json = excluded.perms_json,
    zones_json = excluded.zones_json,
    binding_json = excluded.binding_json,
    valid_from_ms = excluded.valid_from_ms,
    valid_until_ms = excluded.valid_until_ms,
    enabled = excluded.enabled,
    updated_at_ms = excluded.updated_at_ms`
	_, err := s.db.ExecContext(
		ctx, q,
		row.ID, row.Name, row.Kind, row.Hash, boolToInt(row.Duress), row.PermsJSON, row.ZonesJSON, row.BindingJSON,
		row.ValidFromMS, row.ValidUntilMS, boolToInt(row.Enabled), row.CreatedAtMS, row.UpdatedAtMS,
	)
	if err != nil {
		return fmt.Errorf("sqlite: upsert alarm code: %w", err)
	}
	return nil
}

// Get returns the code with id. The boolean reports whether it exists.
func (s *AlarmCodeStore) Get(ctx context.Context, id string) (AlarmCodeRow, bool, error) {
	row, err := scanAlarmCodeRow(s.db.QueryRowContext(ctx, alarmCodeSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AlarmCodeRow{}, false, nil
	}
	if err != nil {
		return AlarmCodeRow{}, false, fmt.Errorf("sqlite: get alarm code: %w", err)
	}
	return row, true, nil
}

// GetAll returns every alarm code, ordered by name then id.
func (s *AlarmCodeStore) GetAll(ctx context.Context) ([]AlarmCodeRow, error) {
	q := alarmCodeSelect + ` ORDER BY name, id`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get all alarm codes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AlarmCodeRow
	for rows.Next() {
		row, err := scanAlarmCodeRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan alarm code: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Delete removes the code row of id.
func (s *AlarmCodeStore) Delete(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM alarm_codes WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: delete alarm code: %w", err)
	}
	return nil
}

const alarmCodeSelect = `
SELECT id, name, kind, hash, duress, perms_json, zones_json, binding_json,
    valid_from_ms, valid_until_ms, enabled, created_at_ms, updated_at_ms
FROM alarm_codes`

func scanAlarmCodeRow(sc scannable) (AlarmCodeRow, error) {
	var row AlarmCodeRow
	var duress, enabled int
	if err := sc.Scan(
		&row.ID, &row.Name, &row.Kind, &row.Hash, &duress, &row.PermsJSON, &row.ZonesJSON, &row.BindingJSON,
		&row.ValidFromMS, &row.ValidUntilMS, &enabled, &row.CreatedAtMS, &row.UpdatedAtMS,
	); err != nil {
		return AlarmCodeRow{}, err
	}
	row.Duress = duress != 0
	row.Enabled = enabled != 0
	return row, nil
}
