// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SecurityPolicy mirrors Matter §11.2.10 GroupKeySecurityPolicy.
type SecurityPolicy uint8

const (
	// SecurityPolicyTrustFirst (value 0) — trust the most recent
	// epoch first.
	SecurityPolicyTrustFirst SecurityPolicy = 0
	// SecurityPolicyCacheAndSync (value 1) — cache all three epoch
	// keys, sync via timing.
	SecurityPolicyCacheAndSync SecurityPolicy = 1
)

// GroupKeySet mirrors a single (fabric, group_key_set_id) row in
// matter_group_keys. The three (EpochKey, EpochStart) pairs are
// independent: EpochKey1/2 are nil when the slot is empty.
type GroupKeySet struct {
	FabricIndex    uint8
	GroupKeySetID  uint16
	SecurityPolicy SecurityPolicy
	EpochKey0      []byte
	EpochStart0    uint64
	EpochKey1      []byte // nil when slot empty
	EpochStart1    uint64
	EpochKey2      []byte // nil when slot empty
	EpochStart2    uint64
}

// UpsertGroupKeySet inserts or replaces a key-set row. The fabric
// must already exist (FK constraint).
func (s *Store) UpsertGroupKeySet(ctx context.Context, rec GroupKeySet) error {
	if _, err := s.db.ExecContext(
		ctx, `
INSERT INTO matter_group_keys
    (fabric_index, group_key_set_id, security_policy,
     epoch_key_0, epoch_start_0,
     epoch_key_1, epoch_start_1,
     epoch_key_2, epoch_start_2)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(fabric_index, group_key_set_id) DO UPDATE SET
    security_policy = excluded.security_policy,
    epoch_key_0     = excluded.epoch_key_0,
    epoch_start_0   = excluded.epoch_start_0,
    epoch_key_1     = excluded.epoch_key_1,
    epoch_start_1   = excluded.epoch_start_1,
    epoch_key_2     = excluded.epoch_key_2,
    epoch_start_2   = excluded.epoch_start_2`,
		rec.FabricIndex, rec.GroupKeySetID, uint8(rec.SecurityPolicy),
		rec.EpochKey0, int64(rec.EpochStart0), //nolint:gosec // start time fits in int64 in practice; see #20
		nullableBytes(rec.EpochKey1), nullableEpochStart(rec.EpochKey1, rec.EpochStart1),
		nullableBytes(rec.EpochKey2), nullableEpochStart(rec.EpochKey2, rec.EpochStart2),
	); err != nil {
		return fmt.Errorf("matter store: upsert group key set: %w", err)
	}
	return nil
}

// GetGroupKeySet returns one key-set. Returns [ErrGroupKeySetNotFound]
// when no row exists.
func (s *Store) GetGroupKeySet(ctx context.Context, fabricIndex uint8, groupKeySetID uint16) (GroupKeySet, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT fabric_index, group_key_set_id, security_policy,
       epoch_key_0, epoch_start_0,
       epoch_key_1, epoch_start_1,
       epoch_key_2, epoch_start_2
FROM matter_group_keys
WHERE fabric_index = ? AND group_key_set_id = ?`, fabricIndex, groupKeySetID)
	rec, err := scanGroupKeySet(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GroupKeySet{}, ErrGroupKeySetNotFound
	}
	if err != nil {
		return GroupKeySet{}, fmt.Errorf("matter store: get group key set: %w", err)
	}
	return rec, nil
}

// ListGroupKeySets returns every key-set for fabricIndex ordered by
// group_key_set_id ascending.
func (s *Store) ListGroupKeySets(ctx context.Context, fabricIndex uint8) ([]GroupKeySet, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT fabric_index, group_key_set_id, security_policy,
       epoch_key_0, epoch_start_0,
       epoch_key_1, epoch_start_1,
       epoch_key_2, epoch_start_2
FROM matter_group_keys WHERE fabric_index = ?
ORDER BY group_key_set_id ASC`, fabricIndex)
	if err != nil {
		return nil, fmt.Errorf("matter store: list group key sets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []GroupKeySet
	for rows.Next() {
		rec, err := scanGroupKeySet(rows)
		if err != nil {
			return nil, fmt.Errorf("matter store: list group key sets: scan: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("matter store: list group key sets: rows: %w", err)
	}
	return out, nil
}

// RemoveGroupKeySet deletes one key-set. CASCADE wipes any
// matter_group_key_map rows that referenced it.
func (s *Store) RemoveGroupKeySet(ctx context.Context, fabricIndex uint8, groupKeySetID uint16) error {
	if _, err := s.db.ExecContext(ctx, `
DELETE FROM matter_group_keys WHERE fabric_index = ? AND group_key_set_id = ?`,
		fabricIndex, groupKeySetID); err != nil {
		return fmt.Errorf("matter store: remove group key set: %w", err)
	}
	return nil
}

// RemoveGroupKeysByFabric deletes all key-sets for fabricIndex. Called
// in the AddNOC failure rollback path after the fabric row exists but a
// subsequent step fails, so no orphaned key material persists for a
// fabric that never completed commissioning. The group-key-map rows
// cascade-delete when the key-set rows are removed (FK constraint).
func (s *Store) RemoveGroupKeysByFabric(ctx context.Context, fabricIndex uint8) error {
	if _, err := s.db.ExecContext(ctx, `
DELETE FROM matter_group_keys WHERE fabric_index = ?`, fabricIndex); err != nil {
		return fmt.Errorf("matter store: remove group keys by fabric: %w", err)
	}
	return nil
}

// GroupKeyMapping binds a GroupID to a GroupKeySetID inside a fabric
// (Matter §11.2.10.4 GroupKeyMap attribute).
type GroupKeyMapping struct {
	FabricIndex   uint8
	GroupID       uint16
	GroupKeySetID uint16
}

// SetGroupKeyMapping upserts a GroupID → GroupKeySetID binding for
// fabricIndex. The fabric and group-key-set must already exist (FK).
func (s *Store) SetGroupKeyMapping(ctx context.Context, m GroupKeyMapping) error {
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO matter_group_key_map (fabric_index, group_id, group_key_set_id)
VALUES (?, ?, ?)
ON CONFLICT(fabric_index, group_id) DO UPDATE SET
    group_key_set_id = excluded.group_key_set_id`,
		m.FabricIndex, m.GroupID, m.GroupKeySetID); err != nil {
		return fmt.Errorf("matter store: set group key mapping: %w", err)
	}
	return nil
}

// RemoveGroupKeyMapping deletes a (fabric, group_id) binding.
func (s *Store) RemoveGroupKeyMapping(ctx context.Context, fabricIndex uint8, groupID uint16) error {
	if _, err := s.db.ExecContext(ctx, `
DELETE FROM matter_group_key_map WHERE fabric_index = ? AND group_id = ?`,
		fabricIndex, groupID); err != nil {
		return fmt.Errorf("matter store: remove group key mapping: %w", err)
	}
	return nil
}

// ListGroupKeyMappings returns every (group_id → group_key_set_id) row
// for fabricIndex ordered by group_id ascending.
func (s *Store) ListGroupKeyMappings(ctx context.Context, fabricIndex uint8) ([]GroupKeyMapping, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT fabric_index, group_id, group_key_set_id
FROM matter_group_key_map WHERE fabric_index = ?
ORDER BY group_id ASC`, fabricIndex)
	if err != nil {
		return nil, fmt.Errorf("matter store: list group key mappings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []GroupKeyMapping
	for rows.Next() {
		var m GroupKeyMapping
		if err := rows.Scan(&m.FabricIndex, &m.GroupID, &m.GroupKeySetID); err != nil {
			return nil, fmt.Errorf("matter store: list group key mappings: scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("matter store: list group key mappings: rows: %w", err)
	}
	return out, nil
}

func scanGroupKeySet(r scanRow) (GroupKeySet, error) {
	var (
		rec    GroupKeySet
		policy uint8
		key1   sql.NullString
		key2   sql.NullString
		start0 int64
		start1 sql.NullInt64
		start2 sql.NullInt64
	)
	if err := r.Scan(
		&rec.FabricIndex, &rec.GroupKeySetID, &policy,
		&rec.EpochKey0, &start0,
		&key1, &start1,
		&key2, &start2,
	); err != nil {
		return GroupKeySet{}, err
	}
	rec.SecurityPolicy = SecurityPolicy(policy)
	rec.EpochStart0 = uint64(start0) //nolint:gosec // round-trip of stored value; see #20
	if key1.Valid {
		rec.EpochKey1 = []byte(key1.String)
	}
	if start1.Valid {
		rec.EpochStart1 = uint64(start1.Int64) //nolint:gosec // round-trip of stored value; see #20
	}
	if key2.Valid {
		rec.EpochKey2 = []byte(key2.String)
	}
	if start2.Valid {
		rec.EpochStart2 = uint64(start2.Int64) //nolint:gosec // round-trip of stored value; see #20
	}
	return rec, nil
}

// nullableEpochStart pairs an EpochStart with its key — when the key
// is nil, the start is also stored as NULL so the two slots stay
// consistent.
func nullableEpochStart(key []byte, start uint64) any {
	if key == nil {
		return nil
	}
	return int64(start) //nolint:gosec // start time fits in int64 in practice; see #20
}
