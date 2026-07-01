// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/pressly/goose/v3"
)

//go:embed migrations_history/*.sql
var historyMigrationsFS embed.FS

// OpenHistory initialises the dedicated measurement-history database at
// dsn and applies the history migration series. The history store lives
// in its own file (its own WAL) so the append-heavy recorder never
// contends with the config/session writer. See ADR 0040.
//
// Use ":memory:" (or "file::memory:?cache=shared") for tests.
func OpenHistory(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open(DriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open history %q: %w", dsn, err)
	}
	db.SetMaxOpenConns(DefaultMaxOpenConns)
	db.SetMaxIdleConns(DefaultMaxIdleConns)
	db.SetConnMaxLifetime(DefaultConnMaxLifetime)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping history: %w", err)
	}
	if err := applyPragmas(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrateHistory(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// migrateHistory applies the history migration series. It shares
// [migrateMu] with [Migrate] because goose writes package-level globals
// (dialect store, base filesystem) that are not safe for concurrent use;
// the two migration sets must never interleave. Each database carries its
// own goose_db_version table, so applying a different series against a
// different file is otherwise independent.
func migrateHistory(ctx context.Context, db *sql.DB) error {
	migrateMu.Lock()
	defer migrateMu.Unlock()
	goose.SetBaseFS(historyMigrationsFS)
	if err := goose.SetDialect(string(goose.DialectSQLite3)); err != nil {
		return fmt.Errorf("sqlite: history set dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "migrations_history"); err != nil {
		return fmt.Errorf("sqlite: history migrate: %w", err)
	}
	return nil
}

// MeasurementSample is one numeric measurement row. The recorder builds
// these from genuine live wire observations (ADR 0040 provenance guard);
// the store does not re-validate provenance — that is the recorder's job.
type MeasurementSample struct {
	CentralName    string
	InterfaceID    string
	ChannelAddress string
	Parameter      string
	TS             time.Time
	Value          float64
}

// MeasurementBucket is one aggregated point returned by
// [MeasurementStore.QueryBuckets]. TS is the bucket's start time.
type MeasurementBucket struct {
	TS    time.Time
	Avg   float64
	Min   float64
	Max   float64
	Count int64
}

// MeasurementMetrics is a point-in-time snapshot of the recorder/store
// counters, surfaced via health gauges and the diagnostics dump.
type MeasurementMetrics struct {
	RowsWritten      int64
	Batches          int64
	RetentionDeleted int64
}

// MeasurementStore persists numeric measurement history in the dedicated
// history database. Multi-CCU safe: every row is scoped by
// (central_name, interface_id, channel_address, parameter) per ADR 0002.
//
// The store takes the history DB handle returned by [OpenHistory], NOT
// the main config/session handle — keep the two apart so a wedged history
// write cannot stall config persistence.
type MeasurementStore struct {
	db *sql.DB

	metricRowsWritten      atomic.Int64
	metricBatches          atomic.Int64
	metricRetentionDeleted atomic.Int64
}

// NewMeasurementStore returns a store backed by the history database.
func NewMeasurementStore(db *sql.DB) *MeasurementStore {
	return &MeasurementStore{db: db}
}

// Close releases the underlying history database handle. Safe on a nil
// store or nil handle.
func (s *MeasurementStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB returns the underlying *sql.DB so callers can wire database-level
// operations (e.g. WAL checkpointing) without a separate handle.
func (s *MeasurementStore) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

// MetricsSnapshot reads the cumulative counters without locking the
// SQLite path. Safe for health gauges that read on every scrape.
func (s *MeasurementStore) MetricsSnapshot() MeasurementMetrics {
	if s == nil {
		return MeasurementMetrics{}
	}
	return MeasurementMetrics{
		RowsWritten:      s.metricRowsWritten.Load(),
		Batches:          s.metricBatches.Load(),
		RetentionDeleted: s.metricRetentionDeleted.Load(),
	}
}

// SaveBatch inserts many samples in one transaction. Two samples for the
// same data point in the same millisecond collide on the primary key;
// the later value wins (last-write-wins within a millisecond is fine for
// a chart). Used by the recorder's periodic flusher.
func (s *MeasurementStore) SaveBatch(ctx context.Context, samples []MeasurementSample) error {
	if s == nil || s.db == nil || len(samples) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("measurements.SaveBatch begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO measurements (central_name, interface_id, channel_address, parameter, ts, value)
        VALUES (?, ?, ?, ?, ?, ?)
        ON CONFLICT (central_name, interface_id, channel_address, parameter, ts) DO UPDATE
            SET value = excluded.value
    `)
	if err != nil {
		return fmt.Errorf("measurements.SaveBatch prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i := range samples {
		m := &samples[i]
		if _, err := stmt.ExecContext(
			ctx,
			m.CentralName, m.InterfaceID, m.ChannelAddress, m.Parameter,
			m.TS.UnixMilli(), m.Value,
		); err != nil {
			return fmt.Errorf("measurements.SaveBatch exec %s.%s: %w",
				m.ChannelAddress, m.Parameter, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("measurements.SaveBatch commit: %w", err)
	}
	s.metricBatches.Add(1)
	s.metricRowsWritten.Add(int64(len(samples)))
	return nil
}

// QueryBuckets aggregates the rows for one data point in [from, to) into
// at most buckets evenly spaced buckets, returning avg/min/max/count per
// non-empty bucket in chronological order. The server-side aggregate
// bounds the response regardless of how many raw rows back the range, so
// the SPA never pulls the raw series.
//
// buckets must be > 0 and to must be after from.
func (s *MeasurementStore) QueryBuckets(
	ctx context.Context,
	centralName, interfaceID, channelAddress, parameter string,
	from, to time.Time,
	buckets int,
) ([]MeasurementBucket, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if buckets <= 0 {
		return nil, errors.New("measurements.QueryBuckets: buckets must be > 0")
	}
	fromMs, toMs := from.UnixMilli(), to.UnixMilli()
	if toMs <= fromMs {
		return nil, errors.New("measurements.QueryBuckets: to must be after from")
	}
	// Bucket width in ms, at least 1 so very short ranges still group.
	width := (toMs - fromMs) / int64(buckets)
	if width < 1 {
		width = 1
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT CAST((ts - ?) / ? AS INTEGER) AS bucket,
               AVG(value), MIN(value), MAX(value), COUNT(*)
          FROM measurements
         WHERE central_name = ?
           AND interface_id = ?
           AND channel_address = ?
           AND parameter = ?
           AND ts >= ?
           AND ts < ?
         GROUP BY bucket
         ORDER BY bucket
    `, fromMs, width, centralName, interfaceID, channelAddress, parameter, fromMs, toMs)
	if err != nil {
		return nil, fmt.Errorf("measurements.QueryBuckets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []MeasurementBucket
	for rows.Next() {
		var (
			bucket          int64
			avg, minV, maxV float64
			count           int64
		)
		if err := rows.Scan(&bucket, &avg, &minV, &maxV, &count); err != nil {
			return nil, fmt.Errorf("measurements.QueryBuckets scan: %w", err)
		}
		out = append(out, MeasurementBucket{
			TS:    time.UnixMilli(fromMs + bucket*width),
			Avg:   avg,
			Min:   minV,
			Max:   maxV,
			Count: count,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("measurements.QueryBuckets rows: %w", err)
	}
	return out, nil
}

// DeleteOlderThan drops every row whose timestamp is before cutoff. The
// retention scheduler job calls this with now-retention. Returns the
// number of rows removed.
func (s *MeasurementStore) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM measurements WHERE ts < ?`, cutoff.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("measurements.DeleteOlderThan: %w", err)
	}
	n, _ := res.RowsAffected()
	s.metricRetentionDeleted.Add(n)
	return n, nil
}

// DeleteDevice removes every measurement for every channel of the given
// device. Used on device-remove / unpair so history cannot keep growing
// for an address that no longer exists. Prefix-safe ("DEVICE" never
// matches "DEVICE2:0").
func (s *MeasurementStore) DeleteDevice(
	ctx context.Context, centralName, interfaceID, deviceAddress string,
) error {
	if s == nil || s.db == nil {
		return nil
	}
	prefix := deviceAddress + ":"
	_, err := s.db.ExecContext(ctx, `
        DELETE FROM measurements
         WHERE central_name = ?
           AND interface_id = ?
           AND (channel_address = ? OR channel_address LIKE ? || '%' ESCAPE '\')
    `, centralName, interfaceID, deviceAddress, prefix)
	if err != nil {
		return fmt.Errorf("measurements.DeleteDevice: %w", err)
	}
	return nil
}

// DeleteForCentral removes every measurement recorded for the given central,
// across every interface and device. Used on live central removal (see
// docs/plans/L-live-ccu-adopt.md PR3) so a removed CCU's history does not
// linger under a name that could later be reused by an unrelated CCU.
func (s *MeasurementStore) DeleteForCentral(ctx context.Context, centralName string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM measurements WHERE central_name = ?`, centralName); err != nil {
		return fmt.Errorf("measurements.DeleteForCentral: %w", err)
	}
	return nil
}

// DeleteAll empties the history. Used by the global reset endpoint and by
// tests.
func (s *MeasurementStore) DeleteAll(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM measurements`); err != nil {
		return fmt.Errorf("measurements.DeleteAll: %w", err)
	}
	return nil
}

// MeasurementStoreStats reports the current row count. Exposed via the
// diagnostics REST endpoint and the history-stats gauge.
type MeasurementStoreStats struct {
	Rows int64
}

// Stats returns the current history statistics.
func (s *MeasurementStore) Stats(ctx context.Context) (MeasurementStoreStats, error) {
	if s == nil || s.db == nil {
		return MeasurementStoreStats{}, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM measurements`)
	var out MeasurementStoreStats
	if err := row.Scan(&out.Rows); err != nil {
		return MeasurementStoreStats{}, fmt.Errorf("measurements.Stats: %w", err)
	}
	return out, nil
}
