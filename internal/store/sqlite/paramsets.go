// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmlog"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ErrParamsetNotFound is returned when Get can't find a paramset.
var ErrParamsetNotFound = errors.New("sqlite: paramset not found")

// ParamsetCacheSchemaVersion identifies the on-disk format of cached paramset
// descriptions. Bump this whenever the patching / normalisation pipeline
// changes shape so that previously-cached rows become stale and get refetched
// from the CCU.
//
// Version history:
//
// 0: pre-versioning (rows written before migration 003 are tagged 0 and wiped
// on first run with this binary) 1: initial versioned schema
// 2: HmIP-FWI CODE_ID MAX patch — cached bounds rebuilt from the CCU (#3238)
const ParamsetCacheSchemaVersion = 2

// ParamsetRecord persists a paramset description.
type ParamsetRecord struct {
	CentralName    string
	InterfaceID    string
	ChannelAddress string
	ParamsetKey    hmenum.ParamsetKey
	Hash           string
	Paramset       hmproto.Paramset
}

// ParamsetStore persists paramset descriptions. Every read and write goes
// to SQLite; the store holds no in-memory index of its own.
type ParamsetStore struct {
	db *sql.DB
}

// NewParamsetStore returns a store backed by db.
func NewParamsetStore(db *sql.DB) *ParamsetStore {
	return &ParamsetStore{
		db: db,
	}
}

// Upsert writes or replaces rec. The schema_version column is always written
// as [ParamsetCacheSchemaVersion]; older versions are removed by
// [ParamsetStore.WipeOutdated].
func (s *ParamsetStore) Upsert(ctx context.Context, rec ParamsetRecord) error {
	defer hmlog.WatchSlow(ctx, slog.Default(), "paramsets.upsert", 0)()
	raw, err := json.Marshal(rec.Paramset)
	if err != nil {
		return fmt.Errorf("sqlite: marshal paramset: %w", err)
	}
	const q = `
INSERT INTO paramsets (central_name, interface_id, channel_address, paramset_key, hash, paramset_json, schema_version, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(central_name, interface_id, channel_address, paramset_key) DO UPDATE SET
    hash = excluded.hash,
    paramset_json = excluded.paramset_json,
    schema_version = excluded.schema_version,
    updated_at = CURRENT_TIMESTAMP;
`
	_, err = s.db.ExecContext(
		ctx, q,
		rec.CentralName, rec.InterfaceID, rec.ChannelAddress, string(rec.ParamsetKey),
		rec.Hash, string(raw), ParamsetCacheSchemaVersion,
	)
	if err != nil {
		return fmt.Errorf("sqlite: upsert paramset: %w", err)
	}
	return nil
}

// Get returns the record for the composite key. Rows tagged with a
// schema_version other than [ParamsetCacheSchemaVersion] are treated as
// missing — the bootstrap-time wipe pass removes them on the next
// daemon restart.
func (s *ParamsetStore) Get(ctx context.Context, centralName, ifaceID, channelAddress string, psKey hmenum.ParamsetKey) (ParamsetRecord, error) {
	const q = `
SELECT hash, paramset_json
FROM paramsets WHERE central_name = ? AND interface_id = ? AND channel_address = ? AND paramset_key = ? AND schema_version = ?`
	rec := ParamsetRecord{
		CentralName:    centralName,
		InterfaceID:    ifaceID,
		ChannelAddress: channelAddress,
		ParamsetKey:    psKey,
	}
	var raw string
	err := s.db.QueryRowContext(ctx, q, centralName, ifaceID, channelAddress, string(psKey), ParamsetCacheSchemaVersion).
		Scan(&rec.Hash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ParamsetRecord{}, ErrParamsetNotFound
	}
	if err != nil {
		return ParamsetRecord{}, fmt.Errorf("sqlite: get paramset: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &rec.Paramset); err != nil {
		return ParamsetRecord{}, fmt.Errorf("sqlite: unmarshal paramset: %w", err)
	}
	return rec, nil
}

// ListByCentral returns every paramset record persisted for central
// whose on-disk format matches [ParamsetCacheSchemaVersion]. Used by
// the boot-time registry hydration; stale-versioned rows are skipped
// (the bootstrap wipe pass removes them).
func (s *ParamsetStore) ListByCentral(ctx context.Context, centralName string) ([]ParamsetRecord, error) {
	const q = `
SELECT interface_id, channel_address, paramset_key, hash, paramset_json
FROM paramsets WHERE central_name = ? AND schema_version = ? ORDER BY interface_id, channel_address, paramset_key`
	rows, err := s.db.QueryContext(ctx, q, centralName, ParamsetCacheSchemaVersion)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list paramsets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ParamsetRecord
	for rows.Next() {
		rec := ParamsetRecord{CentralName: centralName}
		var psKey, raw string
		if err := rows.Scan(&rec.InterfaceID, &rec.ChannelAddress, &psKey, &rec.Hash, &raw); err != nil {
			return nil, fmt.Errorf("sqlite: scan paramset: %w", err)
		}
		rec.ParamsetKey = hmenum.ParamsetKey(psKey)
		if err := json.Unmarshal([]byte(raw), &rec.Paramset); err != nil {
			return nil, fmt.Errorf("sqlite: unmarshal paramset: %w", err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// Size returns the total number of paramset records stored for central.
func (s *ParamsetStore) Size(ctx context.Context, centralName string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM paramsets WHERE central_name = ?`, centralName).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sqlite: paramset size: %w", err)
	}
	return n, nil
}

// GetChannelParamsetDescriptions returns all paramsets for (central,
// interface, channelAddress) as a map keyed by ParamsetKey.
func (s *ParamsetStore) GetChannelParamsetDescriptions(ctx context.Context, centralName, ifaceID, channelAddress string) (map[hmenum.ParamsetKey]hmproto.Paramset, error) {
	const q = `
SELECT paramset_key, paramset_json
FROM paramsets WHERE central_name = ? AND interface_id = ? AND channel_address = ? AND schema_version = ?`
	rows, err := s.db.QueryContext(ctx, q, centralName, ifaceID, channelAddress, ParamsetCacheSchemaVersion)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get channel paramsets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[hmenum.ParamsetKey]hmproto.Paramset)
	for rows.Next() {
		var psKeyStr, raw string
		if err := rows.Scan(&psKeyStr, &raw); err != nil {
			return nil, fmt.Errorf("sqlite: scan channel paramset: %w", err)
		}
		var ps hmproto.Paramset
		if err := json.Unmarshal([]byte(raw), &ps); err != nil {
			return nil, fmt.Errorf("sqlite: unmarshal channel paramset: %w", err)
		}
		out[hmenum.ParamsetKey(psKeyStr)] = ps
	}
	return out, rows.Err()
}

// GetParamsetKeys returns all paramset keys available for (central,
// interface, channelAddress).
func (s *ParamsetStore) GetParamsetKeys(ctx context.Context, centralName, ifaceID, channelAddress string) ([]hmenum.ParamsetKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT paramset_key FROM paramsets WHERE central_name = ? AND interface_id = ? AND channel_address = ? AND schema_version = ? ORDER BY paramset_key`,
		centralName, ifaceID, channelAddress, ParamsetCacheSchemaVersion)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get paramset keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []hmenum.ParamsetKey
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("sqlite: scan paramset key: %w", err)
		}
		out = append(out, hmenum.ParamsetKey(k))
	}
	return out, rows.Err()
}

// HasInterfaceID reports whether any paramsets exist for (central,
// interface).
func (s *ParamsetStore) HasInterfaceID(ctx context.Context, centralName, ifaceID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM paramsets WHERE central_name = ? AND interface_id = ? LIMIT 1`,
		centralName, ifaceID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("sqlite: has interface id: %w", err)
	}
	return n > 0, nil
}

// HasParameter reports whether a specific parameter exists for (central,
// interface, channelAddress, paramsetKey).
func (s *ParamsetStore) HasParameter(ctx context.Context, centralName, ifaceID, channelAddress string, psKey hmenum.ParamsetKey, parameter string) (bool, error) {
	rec, err := s.Get(ctx, centralName, ifaceID, channelAddress, psKey)
	if errors.Is(err, ErrParamsetNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, ok := rec.Paramset[parameter]
	return ok, nil
}

// GetParameterData returns the parameter descriptor for a single parameter in
// (central, interface, channelAddress, paramsetKey, parameter). Returns nil
// when not found.
func (s *ParamsetStore) GetParameterData(ctx context.Context, centralName, ifaceID, channelAddress string, psKey hmenum.ParamsetKey, parameter string) (*hmproto.ParameterData, error) {
	rec, err := s.Get(ctx, centralName, ifaceID, channelAddress, psKey)
	if errors.Is(err, ErrParamsetNotFound) {
		return nil, nil //nolint:nilnil // nil,nil is the documented "not found" contract for this query method
	}
	if err != nil {
		return nil, err
	}
	pd, ok := rec.Paramset[parameter]
	if !ok {
		return nil, nil //nolint:nilnil // nil,nil is the documented "not found" contract for this query method
	}
	return &pd, nil
}

// GetChannelAddressesByParamsetKey returns a mapping from paramset key to the
// set of channel addresses that have that paramset for (central, interface,
// deviceAddress).
func (s *ParamsetStore) GetChannelAddressesByParamsetKey(ctx context.Context, centralName, ifaceID, deviceAddress string) (map[hmenum.ParamsetKey][]string, error) {
	const q = `
SELECT channel_address, paramset_key
FROM paramsets
WHERE central_name = ? AND interface_id = ?
  AND (channel_address = ? OR channel_address LIKE ? ESCAPE '\')
  AND schema_version = ?
ORDER BY paramset_key, channel_address`
	like := deviceAddress + ":%"
	rows, err := s.db.QueryContext(ctx, q, centralName, ifaceID, deviceAddress, like, ParamsetCacheSchemaVersion)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get channel addresses by paramset key: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[hmenum.ParamsetKey][]string)
	for rows.Next() {
		var addr, psKeyStr string
		if err := rows.Scan(&addr, &psKeyStr); err != nil {
			return nil, fmt.Errorf("sqlite: scan channel address: %w", err)
		}
		k := hmenum.ParamsetKey(psKeyStr)
		out[k] = append(out[k], addr)
	}
	return out, rows.Err()
}

// ClearForInterface removes all paramset records for (central, ifaceID) from
// the database.
//
// Returns the number of rows deleted.
func (s *ParamsetStore) ClearForInterface(ctx context.Context, centralName, ifaceID string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM paramsets WHERE central_name = ? AND interface_id = ?`,
		centralName, ifaceID)
	if err != nil {
		return 0, fmt.Errorf("sqlite: clear paramsets: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: clear paramsets rows-affected: %w", err)
	}
	return n, nil
}

// DeleteChannel removes every paramset bound to the given channel from
// the database.
func (s *ParamsetStore) DeleteChannel(ctx context.Context, centralName, ifaceID, channelAddress string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM paramsets WHERE central_name = ? AND interface_id = ? AND channel_address = ?`,
		centralName, ifaceID, channelAddress)
	if err != nil {
		return fmt.Errorf("sqlite: delete paramsets: %w", err)
	}

	return nil
}

// DeleteDevice removes every paramset row for every channel of the device
// (channel_address = deviceAddress or deviceAddress:<n>).
func (s *ParamsetStore) DeleteDevice(ctx context.Context, centralName, ifaceID, deviceAddress string) (int64, error) {
	prefix := strings.TrimRight(deviceAddress, ":") + ":"
	res, err := s.db.ExecContext(ctx, `
        DELETE FROM paramsets
         WHERE central_name = ? AND interface_id = ?
           AND (channel_address = ? OR channel_address LIKE ? || '%' ESCAPE '\')
    `, centralName, ifaceID, deviceAddress, prefix)
	if err != nil {
		return 0, fmt.Errorf("sqlite: delete paramsets device: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: delete paramsets device: %w", err)
	}
	return n, nil
}

// WipeOutdated removes every paramset row whose schema_version differs from
// the current [ParamsetCacheSchemaVersion]. Returns the number of rows
// deleted. Idempotent — safe to call on every daemon start.
//
// The intended call site is [Open] right after migrations apply, so a
// freshly-bumped binary always boots with a clean cache and refetches from
// the CCU.
func (s *ParamsetStore) WipeOutdated(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM paramsets WHERE schema_version != ?`,
		ParamsetCacheSchemaVersion)
	if err != nil {
		return 0, fmt.Errorf("sqlite: wipe outdated paramsets: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: wipe outdated paramsets rows-affected: %w", err)
	}
	return n, nil
}
