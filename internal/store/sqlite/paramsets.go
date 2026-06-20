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
	"sync"

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
const ParamsetCacheSchemaVersion = 1

// ParamsetRecord persists a paramset description.
type ParamsetRecord struct {
	CentralName    string
	InterfaceID    string
	ChannelAddress string
	ParamsetKey    hmenum.ParamsetKey
	Hash           string
	Paramset       hmproto.Paramset
}

// addrParamSet is the in-memory set of channel numbers (the integer
// portion of a channel address like "VCU001:3" → channel 3) that
// contain a given (deviceAddress, parameter) pair.
//
// The zero value is usable — the set is empty.
type addrParamSet = map[int]struct{}

// ParamsetStore persists paramset descriptions.
//
// # Address-parameter cache
//
// [IsInMultipleChannels] uses an in-memory cache that mirrors Python's
// `_address_parameter_cache: dict[(device_addr, parameter)] → set[channelNo]`
//
// Layout: cacheMu guards cache.
//
//	cache[deviceAddress][parameter][channelNo] = struct{}{}
//
// The cache is populated eagerly in [Upsert] and invalidated in
// [ClearForInterface] / [DeleteChannel]. On a fresh daemon start
// (before any [Upsert] calls) the cache is cold — [IsInMultipleChannels]
// falls back to the SQL json_extract query for cold entries. Call
// [WarmCache] once at daemon startup to load the full index from the
// existing database so cold-start lookups also hit the cache.
//
// Cache scope: per-(central, interface) lookup. The cache key uses the
// raw device address (not scoped by central/interface) to match the
// Python implementation which also does not scope by interface. This is
// safe because device addresses are globally unique across interfaces
// for a given central. Multi-CCU safety: distinct ParamsetStore
// instances are used per daemon (one per db connection); no cross-
// central sharing occurs.
type ParamsetStore struct {
	db      *sql.DB
	cacheMu sync.RWMutex
	// cache[deviceAddress][parameter][channelNo]
	cache map[string]map[string]addrParamSet
}

// NewParamsetStore returns a store backed by db with an empty
// address-parameter cache.
func NewParamsetStore(db *sql.DB) *ParamsetStore {
	return &ParamsetStore{
		db:    db,
		cache: make(map[string]map[string]addrParamSet),
	}
}

// cacheAdd records (deviceAddress, parameter, channelNo) in the
// in-memory address-parameter cache. Must be called with cacheMu held
// for writing.
func (s *ParamsetStore) cacheAdd(deviceAddress, parameter string, channelNo int) {
	byParam, ok := s.cache[deviceAddress]
	if !ok {
		byParam = make(map[string]addrParamSet)
		s.cache[deviceAddress] = byParam
	}
	if byParam[parameter] == nil {
		byParam[parameter] = make(addrParamSet)
	}
	byParam[parameter][channelNo] = struct{}{}
}

// splitChannelAddress splits "VCU001:3" into ("VCU001", 3, true).
// Returns ("", 0, false) when addr contains no ":" separator.
func splitChannelAddress(addr string) (device string, channelNo int, ok bool) {
	before, after, ok := strings.Cut(addr, ":")
	if !ok {
		return "", 0, false
	}
	dev := before
	rest := after
	ch := 0
	for _, r := range rest {
		if r < '0' || r > '9' {
			// Non-numeric suffix — treat channel as 0 (still valid; the set
			// will contain a single entry distinguishing it from others).
			break
		}
		ch = ch*10 + int(r-'0')
	}
	return dev, ch, true
}

// cacheIsInMultipleChannels checks the in-memory cache.
// Returns (result, hit): hit=false means the cache has no entry for
// this (deviceAddress, parameter) — the caller should fall back to SQL.
func (s *ParamsetStore) cacheIsInMultipleChannels(deviceAddress, parameter string) (result, hit bool) {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	byParam, ok := s.cache[deviceAddress]
	if !ok {
		return false, false
	}
	channels, ok := byParam[parameter]
	if !ok {
		return false, false
	}
	return len(channels) > 1, true
}

// WarmCache loads the full address-parameter index from the database
// into the in-memory cache. Call once at daemon start after migrations
// apply so that cold-start [IsInMultipleChannels] lookups hit the cache
// instead of falling back to the json_extract SQL query.
//
// which is called from `_process_loaded_content` on storage load.
//
// Concurrent calls are safe — the write lock is held only while the
// cache is being updated, not during the DB scan.
func (s *ParamsetStore) WarmCache(ctx context.Context) error {
	defer hmlog.WatchSlow(ctx, slog.Default(), "paramsets.warm_cache", 0)()
	const q = `SELECT channel_address, paramset_json FROM paramsets WHERE schema_version = ?`
	rows, err := s.db.QueryContext(ctx, q, ParamsetCacheSchemaVersion)
	if err != nil {
		return fmt.Errorf("sqlite: warm address-param cache: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Build a local index first, then swap under the write lock.
	type entry struct {
		device    string
		channelNo int
		parameter string
	}
	var entries []entry
	for rows.Next() {
		var chAddr, raw string
		if err := rows.Scan(&chAddr, &raw); err != nil {
			return fmt.Errorf("sqlite: warm cache scan: %w", err)
		}
		dev, chNo, ok := splitChannelAddress(chAddr)
		if !ok {
			continue // device-level address — skip
		}
		var ps hmproto.Paramset
		if err := json.Unmarshal([]byte(raw), &ps); err != nil {
			continue // corrupt row — skip silently
		}
		for param := range ps {
			entries = append(entries, entry{device: dev, channelNo: chNo, parameter: param})
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite: warm cache rows: %w", err)
	}

	s.cacheMu.Lock()
	s.cache = make(map[string]map[string]addrParamSet, len(entries)/4+1)
	for _, e := range entries {
		s.cacheAdd(e.device, e.parameter, e.channelNo)
	}
	s.cacheMu.Unlock()
	return nil
}

// Upsert writes or replaces rec. The schema_version column is always written
// as [ParamsetCacheSchemaVersion]; older versions are removed by
// [ParamsetStore.WipeOutdated].
//
// After a successful DB write, the address-parameter cache is updated so that
// subsequent [IsInMultipleChannels] calls for the same (deviceAddress,
// parameter) pair reflect the new channel without a SQL round-trip.
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
	// Update the address-parameter cache for the channel, but only if
	// the address contains a channel separator (device-level addresses
	// have no channel number and are not tracked in the cache).
	if dev, chNo, ok := splitChannelAddress(rec.ChannelAddress); ok {
		s.cacheMu.Lock()
		for param := range rec.Paramset {
			s.cacheAdd(dev, param, chNo)
		}
		s.cacheMu.Unlock()
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

// IsInMultipleChannels reports whether the given parameter name appears in more
// than one channel of the same device (identified by the device-address prefix
// of channelAddress, e.g. "VCU0000001" from "VCU0000001:2").
//
// This is used by the model layer to decide whether a combined data point
// (e.g. a climate entity) should be created for the device. The check is
// semantically equivalent to len(_address_parameter_cache[(device_addr, param)]) > 1
// In.
//
// # Performance
//
// The method consults the in-memory address-parameter cache first
// (O(1) map lookup). The cache is populated by [Upsert] and warmed at
// daemon startup by [WarmCache]. A cache miss (cold start, cache not yet
// warmed) falls back to the SQL json_extract query used in the original
// implementation.
//
// Is_in_multiple_channels
// (store/persistent/paramset.py:195) and the _address_parameter_cache
// pattern (paramset.py:75).
func (s *ParamsetStore) IsInMultipleChannels(ctx context.Context, centralName, ifaceID, channelAddress, parameter string) (bool, error) {
	// Extract the device address (the part before the first ':').
	dev, _, ok := splitChannelAddress(channelAddress)
	if !ok {
		// No separator — this is a device address, not a channel address.
		return false, nil
	}

	// Fast path: check in-memory cache.
	if result, hit := s.cacheIsInMultipleChannels(dev, parameter); hit {
		return result, nil
	}

	// Slow path (cache miss): SQL json_extract query — used when the
	// cache has not been warmed yet (first daemon start before WarmCache
	// runs, or for a central/interface whose paramsets were never Upserted
	// in the current process lifetime).
	const q = `
SELECT COUNT(DISTINCT channel_address) FROM (
  SELECT channel_address, paramset_json FROM paramsets
  WHERE central_name = ? AND interface_id = ?
    AND (channel_address = ? OR channel_address LIKE ? ESCAPE '\')
    AND schema_version = ?
) sub
WHERE json_extract(paramset_json, '$.' || ?) IS NOT NULL`
	like := dev + ":%"
	var count int
	err := s.db.QueryRowContext(ctx, q, centralName, ifaceID, dev, like, ParamsetCacheSchemaVersion, parameter).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("sqlite: is_in_multiple_channels: %w", err)
	}
	return count > 1, nil
}

// RegisterAdditionalParameter registers (channelAddress, parameter) in the
// in-memory address-parameter cache so that subsequent [IsInMultipleChannels]
// calls reflect this channel without a SQL round-trip.
//
// This method is now a real cache-write (not a no-op) since added the
// in-memory [addressParamSet] cache. Callers that synthesise calculated or
// custom data-point parameters outside the normal [Upsert] path use this to
// ensure the cache stays consistent.
func (s *ParamsetStore) RegisterAdditionalParameter(_ context.Context, channelAddress, parameter string) {
	dev, chNo, ok := splitChannelAddress(channelAddress)
	if !ok {
		return // device-level address — no channel to register
	}
	s.cacheMu.Lock()
	s.cacheAdd(dev, parameter, chNo)
	s.cacheMu.Unlock()
}

// ClearForInterface removes all paramset records for (central, ifaceID) from
// the database AND drops the entire address-parameter cache.
//
// The cache is cleared completely (not just for this interface) because the
// cache keys are device addresses, which are not scoped by interface — a full
// clear is the safe conservative choice. The cache will be repopulated by
// subsequent [Upsert] calls or a new [WarmCache] call.
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
	if n > 0 {
		// Drop the full cache — it is cheaper than finding which device
		// addresses belonged to this interface.
		s.cacheMu.Lock()
		s.cache = make(map[string]map[string]addrParamSet)
		s.cacheMu.Unlock()
	}
	return n, nil
}

// DeleteChannel removes every paramset bound to the given channel from
// the database AND removes the channel's contribution to the
// address-parameter cache (decrements the channel set for each
// parameter that was in the channel's paramset).
func (s *ParamsetStore) DeleteChannel(ctx context.Context, centralName, ifaceID, channelAddress string) error {
	// Read the paramset before deleting so we know which parameters to
	// remove from the cache. We may have multiple paramset keys per
	// channel; use GetChannelParamsetDescriptions for a single round-trip.
	existing, _ := s.GetChannelParamsetDescriptions(ctx, centralName, ifaceID, channelAddress)

	_, err := s.db.ExecContext(ctx,
		`DELETE FROM paramsets WHERE central_name = ? AND interface_id = ? AND channel_address = ?`,
		centralName, ifaceID, channelAddress)
	if err != nil {
		return fmt.Errorf("sqlite: delete paramsets: %w", err)
	}

	// Update the cache by removing the channel's channel-number from
	// each (deviceAddress, parameter) set.
	if dev, chNo, ok := splitChannelAddress(channelAddress); ok && len(existing) > 0 {
		s.cacheMu.Lock()
		if byParam, ok2 := s.cache[dev]; ok2 {
			for _, ps := range existing {
				for param := range ps {
					if channels, ok3 := byParam[param]; ok3 {
						delete(channels, chNo)
						if len(channels) == 0 {
							delete(byParam, param)
						}
					}
				}
			}
			if len(byParam) == 0 {
				delete(s.cache, dev)
			}
		}
		s.cacheMu.Unlock()
	}
	return nil
}

// DeleteDevice removes every paramset row for every channel of the device
// (channel_address = deviceAddress or deviceAddress:<n>) and drops the
// device from the in-memory address-parameter cache.
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
	if n > 0 {
		s.cacheMu.Lock()
		delete(s.cache, deviceAddress)
		s.cacheMu.Unlock()
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
