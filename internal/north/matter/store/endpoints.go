// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// DPKind classifies a Matter endpoint's source within the model.
// Persisted as a TEXT column with CHECK constraint — the database
// rejects unknown kinds at insert time.
type DPKind string

// DPKind values. The string forms match the dp_kind CHECK constraint
// in migration 007.
const (
	DPKindCustom      DPKind = "custom"
	DPKindGeneric     DPKind = "generic"
	DPKindCalculated  DPKind = "calculated"
	DPKindCombined    DPKind = "combined"
	DPKindMeasurement DPKind = "measurement"
)

// EndpointKey uniquely identifies the model-side source of a Matter
// endpoint. The 5-tuple is the primary key of matter_endpoints.
type EndpointKey struct {
	CentralName   string
	DeviceAddress string
	ChannelNo     int
	DPKind        DPKind
	DPKey         string
}

// EndpointRecord is the on-disk shape of a (source → endpoint_id)
// mapping. EndpointID is in [1, 65534] (endpoint 0 is the root bridge
// endpoint and is not stored).
type EndpointRecord struct {
	Key        EndpointKey
	EndpointID uint16
	DeviceType uint16
}

// Sentinel errors specific to the endpoint table.
var (
	// ErrEndpointNotFound is returned when a key lookup misses.
	ErrEndpointNotFound = errors.New("matter store: endpoint not found")
	// ErrEndpointIDExhausted is returned when [Store.AssignEndpointID]
	// runs out of free 1..65534 slots.
	ErrEndpointIDExhausted = errors.New("matter store: endpoint id exhausted")
)

// GetEndpoint returns the record for key. Returns
// [ErrEndpointNotFound] when no row exists.
func (s *Store) GetEndpoint(ctx context.Context, key EndpointKey) (EndpointRecord, error) {
	rec := EndpointRecord{Key: key}
	err := s.db.QueryRowContext(
		ctx, `
SELECT endpoint_id, device_type FROM matter_endpoints
WHERE central_name = ? AND device_address = ? AND channel_no = ? AND dp_kind = ? AND dp_key = ?`,
		key.CentralName, key.DeviceAddress, key.ChannelNo, string(key.DPKind), key.DPKey,
	).Scan(&rec.EndpointID, &rec.DeviceType)
	if errors.Is(err, sql.ErrNoRows) {
		return EndpointRecord{}, ErrEndpointNotFound
	}
	if err != nil {
		return EndpointRecord{}, fmt.Errorf("matter store: get endpoint: %w", err)
	}
	return rec, nil
}

// ListEndpoints returns every record ordered by endpoint_id ascending.
// Optionally filtered by central_name (empty string = no filter).
func (s *Store) ListEndpoints(ctx context.Context, centralName string) ([]EndpointRecord, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if centralName == "" {
		rows, err = s.db.QueryContext(ctx, `
SELECT central_name, device_address, channel_no, dp_kind, dp_key, endpoint_id, device_type
FROM matter_endpoints ORDER BY endpoint_id ASC`)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT central_name, device_address, channel_no, dp_kind, dp_key, endpoint_id, device_type
FROM matter_endpoints WHERE central_name = ? ORDER BY endpoint_id ASC`, centralName)
	}
	if err != nil {
		return nil, fmt.Errorf("matter store: list endpoints: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []EndpointRecord
	for rows.Next() {
		var (
			rec  EndpointRecord
			kind string
		)
		if err := rows.Scan(&rec.Key.CentralName, &rec.Key.DeviceAddress, &rec.Key.ChannelNo,
			&kind, &rec.Key.DPKey, &rec.EndpointID, &rec.DeviceType); err != nil {
			return nil, fmt.Errorf("matter store: list endpoints: scan: %w", err)
		}
		rec.Key.DPKind = DPKind(kind)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("matter store: list endpoints: rows: %w", err)
	}
	return out, nil
}

// UpsertEndpoint inserts or updates a record. The (5-tuple) primary
// key controls identity; updates rewrite endpoint_id / device_type
// and bump updated_at. Use [Store.AssignEndpointID] to allocate a
// fresh endpoint_id for an unmapped key.
func (s *Store) UpsertEndpoint(ctx context.Context, rec EndpointRecord) error {
	if _, err := s.db.ExecContext(
		ctx, `
INSERT INTO matter_endpoints
    (central_name, device_address, channel_no, dp_kind, dp_key, endpoint_id, device_type)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(central_name, device_address, channel_no, dp_kind, dp_key) DO UPDATE SET
    endpoint_id = excluded.endpoint_id,
    device_type = excluded.device_type,
    updated_at  = CURRENT_TIMESTAMP`,
		rec.Key.CentralName, rec.Key.DeviceAddress, rec.Key.ChannelNo,
		string(rec.Key.DPKind), rec.Key.DPKey, rec.EndpointID, rec.DeviceType,
	); err != nil {
		return fmt.Errorf("matter store: upsert endpoint: %w", err)
	}
	return nil
}

// RemoveEndpoint deletes the row for key. No error when the row is
// absent (idempotent — the assembler may call this for vanished
// devices without coordinating).
func (s *Store) RemoveEndpoint(ctx context.Context, key EndpointKey) error {
	if _, err := s.db.ExecContext(
		ctx, `
DELETE FROM matter_endpoints
WHERE central_name = ? AND device_address = ? AND channel_no = ? AND dp_kind = ? AND dp_key = ?`,
		key.CentralName, key.DeviceAddress, key.ChannelNo, string(key.DPKind), key.DPKey,
	); err != nil {
		return fmt.Errorf("matter store: remove endpoint: %w", err)
	}
	return nil
}

// AssignEndpointID returns the next free endpoint_id in [1, 65534].
// The lookup happens against the live table; the caller is responsible
// for inserting the assigned value before calling AssignEndpointID
// again, otherwise the same ID is returned twice.
//
// Typical use is inside a transaction:
//
//	tx, _ := db.Begin()
//	id := AssignEndpointID(ctx, tx)
//	tx.ExecContext(ctx, "INSERT INTO matter_endpoints ... endpoint_id=?", id)
//	tx.Commit()
//
// For convenience, [Store.UpsertEndpointAssigning] does both in one
// transaction.
func (s *Store) AssignEndpointID(ctx context.Context) (uint16, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("matter store: assign endpoint id: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	id, err := nextFreeEndpointID(ctx, tx)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("matter store: assign endpoint id: commit: %w", err)
	}
	return id, nil
}

// UpsertEndpointAssigning is the common-case helper: if rec.EndpointID
// is 0, allocate a fresh ID under a transaction and insert; otherwise
// upsert with the caller's ID. Returns the effective ID. The fresh-ID
// path holds an exclusive lock briefly to keep two concurrent
// assemblers from picking the same ID.
func (s *Store) UpsertEndpointAssigning(ctx context.Context, rec EndpointRecord) (uint16, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("matter store: upsert endpoint assigning: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	id := rec.EndpointID
	if id == 0 {
		next, err := nextFreeEndpointID(ctx, tx)
		if err != nil {
			return 0, err
		}
		id = next
	}

	if _, err := tx.ExecContext(
		ctx, `
INSERT INTO matter_endpoints
    (central_name, device_address, channel_no, dp_kind, dp_key, endpoint_id, device_type)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(central_name, device_address, channel_no, dp_kind, dp_key) DO UPDATE SET
    endpoint_id = excluded.endpoint_id,
    device_type = excluded.device_type,
    updated_at  = CURRENT_TIMESTAMP`,
		rec.Key.CentralName, rec.Key.DeviceAddress, rec.Key.ChannelNo,
		string(rec.Key.DPKind), rec.Key.DPKey, id, rec.DeviceType,
	); err != nil {
		return 0, fmt.Errorf("matter store: upsert endpoint assigning: insert: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("matter store: upsert endpoint assigning: commit: %w", err)
	}
	return id, nil
}

// nextFreeEndpointID walks the existing endpoint_id values in tx and
// returns the smallest unused 1..65534. SQLite locks the table at
// COMMIT, not at SELECT, so callers must wrap the SELECT-then-INSERT
// in the same transaction (see [Store.UpsertEndpointAssigning]).
func nextFreeEndpointID(ctx context.Context, tx *sql.Tx) (uint16, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT endpoint_id FROM matter_endpoints ORDER BY endpoint_id ASC`)
	if err != nil {
		return 0, fmt.Errorf("matter store: next endpoint id: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Endpoint IDs 0 + 1 are structural (RootNode + Aggregator,
	// Apple-bridge topology). Bridged endpoints start at 2; mirrors
	// matter.js's `ServerNode.create(...).add(aggregator).add(child)`
	// which always lands bridged endpoints at 2..N.
	want := uint16(2)
	for rows.Next() {
		var got int
		if err := rows.Scan(&got); err != nil {
			return 0, fmt.Errorf("matter store: next endpoint id: scan: %w", err)
		}
		if got < 2 || got > 65534 {
			return 0, fmt.Errorf("matter store: next endpoint id: stored value %d out of range", got)
		}
		if uint16(got) != want { //nolint:gosec // bounded by the range check above
			return want, nil
		}
		if want == 65534 {
			return 0, ErrEndpointIDExhausted
		}
		want++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("matter store: next endpoint id: rows: %w", err)
	}
	return want, nil
}
