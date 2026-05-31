// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ExposureRecord is the on-disk shape of one row in the
// `matter_exposures` allowlist (migration 009). The 5-tuple key matches
// [EndpointKey] / `matter_endpoints.PrimaryKey` so allowlist entries
// can be JOIN-correlated with assigned endpoint IDs.
//
// Default state of any row is `Enabled = false`: the assembler must
// see an explicit toggle before bridging the source. An empty
// `matter_exposures` table therefore yields an empty topology except
// for the root endpoint — the §1 "Allowlist instead of Denylist"
// guarantee from `docs/matter-ui-concept.md`.
type ExposureRecord struct {
	Key          EndpointKey
	Enabled      bool
	FriendlyName string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Actor        string
}

// Sentinel errors specific to the matter_exposures table.
var (
	// ErrExposureNotFound is returned when a key lookup misses.
	ErrExposureNotFound = errors.New("matter store: exposure not found")
)

// GetExposure returns the allowlist row for key. Returns
// [ErrExposureNotFound] when no row exists — the caller treats that
// as "not exposed" (the default).
func (s *Store) GetExposure(ctx context.Context, key EndpointKey) (ExposureRecord, error) {
	rec := ExposureRecord{Key: key}
	var enabledInt int
	err := s.db.QueryRowContext(
		ctx, `
SELECT enabled, friendly_name, created_at, updated_at, actor FROM matter_exposures
WHERE central_name = ? AND device_address = ? AND channel_no = ? AND dp_kind = ? AND dp_key = ?`,
		key.CentralName, key.DeviceAddress, key.ChannelNo, string(key.DPKind), key.DPKey,
	).Scan(&enabledInt, &rec.FriendlyName, &rec.CreatedAt, &rec.UpdatedAt, &rec.Actor)
	if errors.Is(err, sql.ErrNoRows) {
		return ExposureRecord{}, ErrExposureNotFound
	}
	if err != nil {
		return ExposureRecord{}, fmt.Errorf("matter store: get exposure: %w", err)
	}
	rec.Enabled = enabledInt == 1
	return rec, nil
}

// IsExposed is the assembler's hot-path probe: returns true when the
// row exists AND `enabled=1`. Missing rows are treated as not exposed.
func (s *Store) IsExposed(ctx context.Context, key EndpointKey) (bool, error) {
	var enabledInt int
	err := s.db.QueryRowContext(
		ctx, `
SELECT enabled FROM matter_exposures
WHERE central_name = ? AND device_address = ? AND channel_no = ? AND dp_kind = ? AND dp_key = ?`,
		key.CentralName, key.DeviceAddress, key.ChannelNo, string(key.DPKind), key.DPKey,
	).Scan(&enabledInt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("matter store: is exposed: %w", err)
	}
	return enabledInt == 1, nil
}

// EnabledKeys returns every (central_name, …, dp_key) tuple with
// `enabled=1`, optionally filtered by central name (empty = all).
// Used by the assembler when it walks the model and needs the
// allowlist as a set.
func (s *Store) EnabledKeys(ctx context.Context, centralName string) (map[EndpointKey]struct{}, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if centralName == "" {
		rows, err = s.db.QueryContext(ctx, `
SELECT central_name, device_address, channel_no, dp_kind, dp_key FROM matter_exposures
WHERE enabled = 1`)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT central_name, device_address, channel_no, dp_kind, dp_key FROM matter_exposures
WHERE enabled = 1 AND central_name = ?`, centralName)
	}
	if err != nil {
		return nil, fmt.Errorf("matter store: enabled keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[EndpointKey]struct{})
	for rows.Next() {
		var (
			k    EndpointKey
			kind string
		)
		if err := rows.Scan(&k.CentralName, &k.DeviceAddress, &k.ChannelNo, &kind, &k.DPKey); err != nil {
			return nil, fmt.Errorf("matter store: enabled keys scan: %w", err)
		}
		k.DPKind = DPKind(kind)
		out[k] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("matter store: enabled keys rows: %w", err)
	}
	return out, nil
}

// ListExposures returns every row ordered by central + address +
// channel + kind + key. Empty centralName → no filter.
func (s *Store) ListExposures(ctx context.Context, centralName string) ([]ExposureRecord, error) {
	var (
		rows *sql.Rows
		err  error
	)
	const orderBy = `ORDER BY central_name, device_address, channel_no, dp_kind, dp_key`
	if centralName == "" {
		rows, err = s.db.QueryContext(ctx, `
SELECT central_name, device_address, channel_no, dp_kind, dp_key, enabled,
       friendly_name, created_at, updated_at, actor
FROM matter_exposures `+orderBy)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT central_name, device_address, channel_no, dp_kind, dp_key, enabled,
       friendly_name, created_at, updated_at, actor
FROM matter_exposures WHERE central_name = ? `+orderBy, centralName)
	}
	if err != nil {
		return nil, fmt.Errorf("matter store: list exposures: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ExposureRecord
	for rows.Next() {
		var (
			rec        ExposureRecord
			kind       string
			enabledInt int
		)
		if err := rows.Scan(
			&rec.Key.CentralName, &rec.Key.DeviceAddress, &rec.Key.ChannelNo, &kind, &rec.Key.DPKey,
			&enabledInt, &rec.FriendlyName, &rec.CreatedAt, &rec.UpdatedAt, &rec.Actor,
		); err != nil {
			return nil, fmt.Errorf("matter store: list exposures scan: %w", err)
		}
		rec.Key.DPKind = DPKind(kind)
		rec.Enabled = enabledInt == 1
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("matter store: list exposures rows: %w", err)
	}
	return out, nil
}

// UpsertExposure inserts or updates a row. The 5-tuple key + actor
// are required; FriendlyName may be empty (cluster servers fall back
// to the device name).
func (s *Store) UpsertExposure(ctx context.Context, rec ExposureRecord) error {
	enabledInt := 0
	if rec.Enabled {
		enabledInt = 1
	}
	_, err := s.db.ExecContext(
		ctx, `
INSERT INTO matter_exposures
    (central_name, device_address, channel_no, dp_kind, dp_key, enabled, friendly_name, actor)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(central_name, device_address, channel_no, dp_kind, dp_key) DO UPDATE
SET enabled       = excluded.enabled,
    friendly_name = excluded.friendly_name,
    updated_at    = CURRENT_TIMESTAMP,
    actor         = excluded.actor`,
		rec.Key.CentralName, rec.Key.DeviceAddress, rec.Key.ChannelNo,
		string(rec.Key.DPKind), rec.Key.DPKey,
		enabledInt, rec.FriendlyName, rec.Actor,
	)
	if err != nil {
		return fmt.Errorf("matter store: upsert exposure: %w", err)
	}
	return nil
}

// DeleteExposure removes a row by key. Idempotent: missing keys
// return nil.
func (s *Store) DeleteExposure(ctx context.Context, key EndpointKey) error {
	_, err := s.db.ExecContext(
		ctx, `
DELETE FROM matter_exposures
WHERE central_name = ? AND device_address = ? AND channel_no = ? AND dp_kind = ? AND dp_key = ?`,
		key.CentralName, key.DeviceAddress, key.ChannelNo, string(key.DPKind), key.DPKey,
	)
	if err != nil {
		return fmt.Errorf("matter store: delete exposure: %w", err)
	}
	return nil
}

// CountEnabled returns the number of enabled rows for a central
// (empty central = global). Used by `/matter/status` for the
// dashboard summary.
func (s *Store) CountEnabled(ctx context.Context, centralName string) (int, error) {
	var n int
	var err error
	if centralName == "" {
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM matter_exposures WHERE enabled = 1`).Scan(&n)
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM matter_exposures WHERE enabled = 1 AND central_name = ?`, centralName).Scan(&n)
	}
	if err != nil {
		return 0, fmt.Errorf("matter store: count enabled: %w", err)
	}
	return n, nil
}
