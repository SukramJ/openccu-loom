// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ValueSource re-exports [hmenum.ValueSource] for convenience in
// callers that already work with the sqlite package.
type ValueSource = hmenum.ValueSource

// Lifecycle re-exports. See [hmenum.ValueSource] for the full doc.
const (
	SourceUnobserved = hmenum.ValueSourceUnobserved
	SourceCache      = hmenum.ValueSourceCache
	SourceLive       = hmenum.ValueSourceLive
	SourceStale      = hmenum.ValueSourceStale
)

// ValuesCacheSchemaVersion identifies the on-disk format that the Go
// code currently produces. Rows whose schema_version does not match
// are dropped on the next restore (and recreated as new values arrive)
// so old rows cannot ship a value through the cast layer that the
// current code can no longer interpret.
//
// The cached rows are keyed by the canonical two-part interface_id
// (`<ccu_name>-<interface>`, ADR-0024) — the same id stamped on devices
// and DataPointKeys. The host-independent instance name is never part of
// this key.
const ValuesCacheSchemaVersion = 1

// ValueType is the small discriminator stored alongside value_json
// so readers can filter / dispatch without parsing JSON.
type ValueType string

// Known value_type tokens. Any new wire-side type must be added here
// and to TypeOfValue.
const (
	ValueTypeBool   ValueType = "bool"
	ValueTypeInt    ValueType = "int"
	ValueTypeFloat  ValueType = "float"
	ValueTypeString ValueType = "string"
	ValueTypeNull   ValueType = "null"
)

// TypeOfValue returns the [ValueType] discriminator for v. Returns
// [ValueTypeNull] for nil. Used by [ValuesCacheStore.SaveValue] to
// derive the persisted value_type column.
//
// loom:reachable:reason="called internally by ValuesCacheStore.SaveValue and SaveBatch to determine column type"
func TypeOfValue(v any) ValueType {
	if v == nil {
		return ValueTypeNull
	}
	switch v.(type) {
	case bool:
		return ValueTypeBool
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return ValueTypeInt
	case float32, float64:
		return ValueTypeFloat
	case string:
		return ValueTypeString
	}
	// Fallback: encode as JSON and let the consumer figure it out. The
	// only path that should hit this is a complex wire-shape (map,
	// slice) — none of which the wire layer is supposed to emit for
	// scalar VALUES, but we keep the cache permissive.
	return ValueTypeString
}

// CachedValue is one persisted wire-DP value, read by the restore
// pass.
type CachedValue struct {
	Parameter     string
	Value         any
	Type          ValueType
	LastSeenAt    time.Time
	LastChangedAt time.Time
}

// ErrSchemaDrift signals that a cached value was found whose recorded
// [ValueType] no longer matches the current paramset description.
// Callers can attempt a cast (int → float etc.) and proceed, or skip
// the entry.
var ErrSchemaDrift = errors.New("values_cache: schema drift")

// ValuesCacheStore persists wire-DP VALUES across daemon restarts.
// See migration 016_values_cache.sql for the schema rationale and
// ADR 0018 for the lifecycle design.
//
// Multi-CCU safe: one shared instance per *sql.DB, all rows scoped by
// (central_name, interface_id, channel_address, parameter_name).
type ValuesCacheStore struct {
	db *sql.DB

	// Cumulative counters surfaced via [Metrics]. Kept on the store
	// itself so the diagnostics endpoint and the Prometheus gauges
	// see the same numbers without coordinating through a separate
	// metrics registry. The atomic types let callers read them
	// concurrently without a lock around the SQLite path.
	metricRestoredRows   atomic.Int64
	metricCastFailures   atomic.Int64
	metricGCRowsDeleted  atomic.Int64
	metricFlushBatches   atomic.Int64
	metricFlushedEntries atomic.Int64
}

// Metrics returns a point-in-time snapshot of the cumulative counters
// the store tracks since process start. Counters are monotonically
// non-decreasing.
type Metrics struct {
	RestoredRows   int64
	CastFailures   int64
	GCRowsDeleted  int64
	FlushBatches   int64
	FlushedEntries int64
}

// MetricsSnapshot reads the current counter values without locking
// the SQLite path. Safe for use from health gauges that read on
// every scrape.
func (s *ValuesCacheStore) MetricsSnapshot() Metrics {
	if s == nil {
		return Metrics{}
	}
	return Metrics{
		RestoredRows:   s.metricRestoredRows.Load(),
		CastFailures:   s.metricCastFailures.Load(),
		GCRowsDeleted:  s.metricGCRowsDeleted.Load(),
		FlushBatches:   s.metricFlushBatches.Load(),
		FlushedEntries: s.metricFlushedEntries.Load(),
	}
}

// IncRestoredRows bumps the counter for "rows applied at boot". The
// pipeline's restore pass calls this per applied wire-DP value.
func (s *ValuesCacheStore) IncRestoredRows(n int64) {
	if s == nil || n <= 0 {
		return
	}
	s.metricRestoredRows.Add(n)
}

// IncCastFailures bumps the counter for cache rows that could not be
// coerced into the data point's static type. The pipeline calls this
// once per cell-level failure (NOT per row).
func (s *ValuesCacheStore) IncCastFailures(n int64) {
	if s == nil || n <= 0 {
		return
	}
	s.metricCastFailures.Add(n)
}

// NewValuesCacheStore returns a store backed by db. The store holds
// no in-memory cache — the live state lives on the data points
// themselves; SQLite is purely the survival layer.
func NewValuesCacheStore(db *sql.DB) *ValuesCacheStore {
	return &ValuesCacheStore{db: db}
}

// Close releases the underlying database handle. Safe on a nil store or
// nil handle. Callers that opened the DB for this store (daemon wiring,
// unit tests) must Close it so the file is released — Windows refuses to
// delete an open SQLite file at temp-dir cleanup.
func (s *ValuesCacheStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB returns the underlying *sql.DB so callers can wire database-level
// operations (e.g. WAL checkpointing) without importing a separate handle.
// Returns nil when the store is nil or was constructed without a database.
func (s *ValuesCacheStore) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

// LoadChannel returns every cached value for the given channel. The
// caller is responsible for applying each entry to its corresponding
// data point (the channel may have evolved since the row was
// written; entries for unknown parameters must be skipped).
//
// Rows with a different cache_schema_version are filtered out so old
// data cannot leak through a cast layer that no longer expects it.
//
// Returns nil when the channel has no cached values.
func (s *ValuesCacheStore) LoadChannel(
	ctx context.Context, centralName, interfaceID, channelAddress string,
) ([]CachedValue, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT parameter_name, value_json, value_type, last_seen_at, last_changed_at
          FROM values_cache
         WHERE central_name = ?
           AND interface_id = ?
           AND channel_address = ?
           AND cache_schema_version = ?
    `, centralName, interfaceID, channelAddress, ValuesCacheSchemaVersion)
	if err != nil {
		return nil, fmt.Errorf("values_cache.LoadChannel: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []CachedValue
	for rows.Next() {
		var (
			name, raw, typ string
			seen, changed  int64
		)
		if err := rows.Scan(&name, &raw, &typ, &seen, &changed); err != nil {
			return nil, fmt.Errorf("values_cache.LoadChannel scan: %w", err)
		}
		var v any
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				continue
			}
		}
		out = append(out, CachedValue{
			Parameter:     name,
			Value:         v,
			Type:          ValueType(typ),
			LastSeenAt:    time.UnixMilli(seen),
			LastChangedAt: time.UnixMilli(changed),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("values_cache.LoadChannel rows: %w", err)
	}
	return out, nil
}

// LoadAll returns every cached value across every channel of every
// central. Used by the boot-time restore pass to bulk-apply the
// snapshot in a single SQLite read.
//
// Result is keyed by (central, interface, channel) → list of cached
// values, mirroring the restore loop in the device pipeline.
func (s *ValuesCacheStore) LoadAll(ctx context.Context) (
	map[string]map[string]map[string][]CachedValue, error,
) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT central_name, interface_id, channel_address, parameter_name,
               value_json, value_type, last_seen_at, last_changed_at
          FROM values_cache
         WHERE cache_schema_version = ?
    `, ValuesCacheSchemaVersion)
	if err != nil {
		return nil, fmt.Errorf("values_cache.LoadAll: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]map[string]map[string][]CachedValue)
	for rows.Next() {
		var (
			centralName, iface, addr, name, raw, typ string
			seen, changed                            int64
		)
		if err := rows.Scan(&centralName, &iface, &addr, &name, &raw, &typ, &seen, &changed); err != nil {
			return nil, fmt.Errorf("values_cache.LoadAll scan: %w", err)
		}
		var v any
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				continue
			}
		}
		byInterface, ok := out[centralName]
		if !ok {
			byInterface = make(map[string]map[string][]CachedValue)
			out[centralName] = byInterface
		}
		byChannel, ok := byInterface[iface]
		if !ok {
			byChannel = make(map[string][]CachedValue)
			byInterface[iface] = byChannel
		}
		byChannel[addr] = append(byChannel[addr], CachedValue{
			Parameter:     name,
			Value:         v,
			Type:          ValueType(typ),
			LastSeenAt:    time.UnixMilli(seen),
			LastChangedAt: time.UnixMilli(changed),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("values_cache.LoadAll rows: %w", err)
	}
	return out, nil
}

// SaveValue upserts one wire-DP value. nil values are NOT persisted —
// the cache should never resurrect a "null was seen" entry; on next
// restore the parameter stays unobserved instead.
func (s *ValuesCacheStore) SaveValue(
	ctx context.Context,
	centralName, interfaceID, channelAddress, parameterName string,
	value any,
	lastSeen, lastChanged time.Time,
) error {
	if s == nil || s.db == nil {
		return nil
	}
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("values_cache.SaveValue marshal: %w", err)
	}
	_, err = s.db.ExecContext(
		ctx, `
        INSERT INTO values_cache (central_name, interface_id, channel_address, parameter_name,
                                  value_json, value_type, last_seen_at, last_changed_at,
                                  cache_schema_version)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT (central_name, interface_id, channel_address, parameter_name) DO UPDATE
            SET value_json           = excluded.value_json,
                value_type           = excluded.value_type,
                last_seen_at         = excluded.last_seen_at,
                last_changed_at      = excluded.last_changed_at,
                cache_schema_version = excluded.cache_schema_version
    `,
		centralName, interfaceID, channelAddress, parameterName,
		string(raw), string(TypeOfValue(value)),
		lastSeen.UnixMilli(), lastChanged.UnixMilli(),
		ValuesCacheSchemaVersion,
	)
	if err != nil {
		return fmt.Errorf("values_cache.SaveValue: %w", err)
	}
	return nil
}

// SaveBatch upserts many values in one transaction. Used by the
// periodic flusher to push the dirty set in a single SQLite write.
func (s *ValuesCacheStore) SaveBatch(ctx context.Context, entries []SaveEntry) error {
	if s == nil || s.db == nil || len(entries) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("values_cache.SaveBatch begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO values_cache (central_name, interface_id, channel_address, parameter_name,
                                  value_json, value_type, last_seen_at, last_changed_at,
                                  cache_schema_version)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT (central_name, interface_id, channel_address, parameter_name) DO UPDATE
            SET value_json           = excluded.value_json,
                value_type           = excluded.value_type,
                last_seen_at         = excluded.last_seen_at,
                last_changed_at      = excluded.last_changed_at,
                cache_schema_version = excluded.cache_schema_version
    `)
	if err != nil {
		return fmt.Errorf("values_cache.SaveBatch prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i := range entries {
		e := &entries[i]
		if e.Value == nil {
			continue
		}
		raw, err := json.Marshal(e.Value)
		if err != nil {
			continue
		}
		if _, err := stmt.ExecContext(
			ctx,
			e.CentralName, e.InterfaceID, e.ChannelAddress, e.ParameterName,
			string(raw), string(TypeOfValue(e.Value)),
			e.LastSeenAt.UnixMilli(), e.LastChangedAt.UnixMilli(),
			ValuesCacheSchemaVersion,
		); err != nil {
			return fmt.Errorf("values_cache.SaveBatch exec %s.%s: %w",
				e.ChannelAddress, e.ParameterName, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("values_cache.SaveBatch commit: %w", err)
	}
	s.metricFlushBatches.Add(1)
	s.metricFlushedEntries.Add(int64(len(entries)))
	return nil
}

// SaveEntry is one row for [ValuesCacheStore.SaveBatch].
type SaveEntry struct {
	CentralName    string
	InterfaceID    string
	ChannelAddress string
	ParameterName  string
	Value          any
	LastSeenAt     time.Time
	LastChangedAt  time.Time
}

// DeleteDevice removes every cached value for every channel of the
// given device. Used on device-remove / unpair so the cache cannot
// resurrect a stale value after a re-pair on the same address.
func (s *ValuesCacheStore) DeleteDevice(
	ctx context.Context, centralName, interfaceID, deviceAddress string,
) error {
	if s == nil || s.db == nil {
		return nil
	}
	prefix := escapeLikePrefix(strings.TrimRight(deviceAddress, ":")) + ":"
	_, err := s.db.ExecContext(ctx, `
        DELETE FROM values_cache
         WHERE central_name = ?
           AND interface_id = ?
           AND (channel_address = ? OR channel_address LIKE ? || '%' ESCAPE '\')
    `, centralName, interfaceID, deviceAddress, prefix)
	if err != nil {
		return fmt.Errorf("values_cache.DeleteDevice: %w", err)
	}
	return nil
}

// DeleteChannel removes every cached value for the channel. Used when
// a channel disappears from the device profile (rare).
func (s *ValuesCacheStore) DeleteChannel(
	ctx context.Context, centralName, interfaceID, channelAddress string,
) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
        DELETE FROM values_cache
         WHERE central_name = ? AND interface_id = ? AND channel_address = ?
    `, centralName, interfaceID, channelAddress)
	if err != nil {
		return fmt.Errorf("values_cache.DeleteChannel: %w", err)
	}
	return nil
}

// DeleteForInterface removes every cached value for one (central, interface).
func (s *ValuesCacheStore) DeleteForInterface(ctx context.Context, centralName, interfaceID string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `
        DELETE FROM values_cache
         WHERE central_name = ? AND interface_id = ?
    `, centralName, interfaceID)
	if err != nil {
		return 0, fmt.Errorf("values_cache.DeleteForInterface: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("values_cache.DeleteForInterface: %w", err)
	}
	return n, nil
}

// DeleteAll empties the cache. Used by the global Reset endpoint and
// by tests.
func (s *ValuesCacheStore) DeleteAll(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM values_cache`); err != nil {
		return fmt.Errorf("values_cache.DeleteAll: %w", err)
	}
	return nil
}

// GCResult summarises a [ValuesCacheStore.GCDeadRows] run. The store
// reports counts; the caller decides whether to surface them as
// metrics or a log line.
type GCResult struct {
	Scanned int
	Deleted int
}

// GCSweep is what one garbage-collection pass observed: the scopes whose
// live model the caller actually read, and the keys that were alive inside
// them.
//
// Both halves are needed because absence of a key is only evidence when the
// scope it belongs to was read. A CCU that is powered off, or an interface
// whose device ingest failed, contributes an empty model that is byte-for-byte
// indistinguishable from "every device on it disappeared" — deleting on that
// reading throws away the cache the next cold boot depends on, for a CCU that
// is merely rebooting. A scope missing from Scopes is therefore skipped
// entirely rather than treated as all-dead.
type GCSweep struct {
	// Scopes holds one [ScopeKey] per (central, interface) whose model the
	// caller read. Rows outside it are never considered.
	Scopes map[string]struct{}
	// Alive holds one [AliveKey] per data point that still exists. Within a
	// swept scope, a row whose key is absent is deleted.
	Alive map[string]struct{}
}

// GCDeadRows deletes rows whose (central, interface, channel, parameter)
// tuple is not in sweep.Alive, considering only rows whose (central,
// interface) scope is in sweep.Scopes. An empty scope set deletes nothing —
// a caller that observed no model at all has no basis for any deletion.
func (s *ValuesCacheStore) GCDeadRows(
	ctx context.Context, sweep GCSweep,
) (GCResult, error) {
	if s == nil || s.db == nil {
		return GCResult{}, nil
	}
	if len(sweep.Scopes) == 0 {
		return GCResult{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT central_name, interface_id, channel_address, parameter_name
          FROM values_cache
    `)
	if err != nil {
		return GCResult{}, fmt.Errorf("values_cache.GCDeadRows scan: %w", err)
	}
	defer func() { _ = rows.Close() }()
	type tuple struct{ centralName, iface, channel, param string }
	var dead []tuple
	scanned := 0
	for rows.Next() {
		var t tuple
		if err := rows.Scan(&t.centralName, &t.iface, &t.channel, &t.param); err != nil {
			return GCResult{}, fmt.Errorf("values_cache.GCDeadRows row: %w", err)
		}
		scanned++
		if _, swept := sweep.Scopes[ScopeKey(t.centralName, t.iface)]; !swept {
			continue
		}
		if _, live := sweep.Alive[AliveKey(t.centralName, t.iface, t.channel, t.param)]; !live {
			dead = append(dead, t)
		}
	}
	if err := rows.Err(); err != nil {
		return GCResult{Scanned: scanned}, fmt.Errorf("values_cache.GCDeadRows iter: %w", err)
	}
	if len(dead) == 0 {
		return GCResult{Scanned: scanned}, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GCResult{Scanned: scanned}, fmt.Errorf("values_cache.GCDeadRows tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
        DELETE FROM values_cache
         WHERE central_name = ?
           AND interface_id = ?
           AND channel_address = ?
           AND parameter_name = ?
    `)
	if err != nil {
		return GCResult{Scanned: scanned}, fmt.Errorf("values_cache.GCDeadRows prep: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for _, t := range dead {
		if _, err := stmt.ExecContext(ctx, t.centralName, t.iface, t.channel, t.param); err != nil {
			return GCResult{Scanned: scanned}, fmt.Errorf("values_cache.GCDeadRows del: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return GCResult{Scanned: scanned}, fmt.Errorf("values_cache.GCDeadRows commit: %w", err)
	}
	s.metricGCRowsDeleted.Add(int64(len(dead)))
	return GCResult{Scanned: scanned, Deleted: len(dead)}, nil
}

// AliveKey is the deterministic encoding used by GCDeadRows. Callers
// build the alive set with this helper so the format stays in sync
// with the scan comparison. The periodic GC pass driven by
// [ValuesCacheStore.GCDeadRows] (see the adapter package's
// values_cache_flush.go) is the production caller; tests use it
// directly to build expected alive sets.
// loom:reachable:reason="called in production by buildGCSweep on the periodic values-cache GC path (internal/central/adapter/values_cache_flush.go), which runs inside the flusher goroutine closure the static reachability pass does not trace"
func AliveKey(centralName, interfaceID, channelAddress, parameterName string) string {
	return centralName + "|" + interfaceID + "|" + channelAddress + "|" + parameterName
}

// ScopeKey is the deterministic encoding of one [GCSweep] scope — the
// (central, interface) pair whose live model the caller read. It shares the
// separator with [AliveKey] so both sets are built from the same coordinates.
//
// loom:reachable:reason="called in production by buildGCSweep on the periodic values-cache GC path (internal/central/adapter/values_cache_flush.go), which runs inside the flusher goroutine closure the static reachability pass does not trace"
func ScopeKey(centralName, interfaceID string) string {
	return centralName + "|" + interfaceID
}

// Stats reports the current row count and approximate byte size of
// value_json across all rows. Exposed via the diagnostics REST
// endpoint.
type Stats struct {
	Rows          int64
	ValueJSONSize int64
}

// Stats returns the current cache statistics.
func (s *ValuesCacheStore) Stats(ctx context.Context) (Stats, error) {
	if s == nil || s.db == nil {
		return Stats{}, nil
	}
	row := s.db.QueryRowContext(ctx, `
        SELECT COUNT(*), COALESCE(SUM(length(value_json)), 0)
          FROM values_cache
    `)
	var out Stats
	if err := row.Scan(&out.Rows, &out.ValueJSONSize); err != nil {
		return Stats{}, fmt.Errorf("values_cache.Stats: %w", err)
	}
	return out, nil
}
