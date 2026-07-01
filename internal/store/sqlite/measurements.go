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

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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

// energyParameters is the fixed set of parameters the energy endpoint
// aggregates: instantaneous load plus the two cumulative meter counters
// (consumption, feed-in). POWER is instantaneous (W, averaged); the two
// ENERGY_COUNTER parameters are cumulative meters (Wh) whose per-bucket
// consumption is last-first.
var energyParameters = []string{
	string(hmenum.ParameterPower),
	string(hmenum.ParameterEnergyCounter),
	string(hmenum.ParameterEnergyCounterFeedIn),
}

// EnergyRow is one raw (channel, parameter, bucket) rollup row returned by
// [MeasurementStore.QueryEnergy]. The handler folds these into per-device
// consumed/feed-in Wh and avg/peak W — see the ENERGY_COUNTER delta and
// counter-reset rule in internal/north/rest/handlers/energy.go.
type EnergyRow struct {
	ChannelAddress string
	Parameter      string
	BucketTS       time.Time
	Sum            float64
	Min            float64
	Max            float64
	First          float64
	Last           float64
	Count          int64
}

// queryEnergyTierSQL reads one rollup tier (measurements_hourly or
// measurements_daily) directly: each row is already one (channel,
// parameter, bucket) aggregate, so group=hour and group=day need no
// further folding — only the parameter/central/device/range filter.
const queryEnergyTierSQL = `
    SELECT channel_address, parameter, bucket_ts, sum, min, max, first, last, count
      FROM %s
     WHERE central_name = ?
       AND parameter IN (?, ?, ?)
       AND bucket_ts >= ?
       AND bucket_ts < ?
       AND (? = '' OR channel_address = ? OR channel_address LIKE ?)
     ORDER BY channel_address, parameter, bucket_ts
`

// queryEnergyMonthSQL re-aggregates measurements_daily rows into UTC
// calendar-month buckets in SQL rather than in the handler: SQLite has no
// single "truncate to month" function, but `strftime('%s', ts, 'unixepoch',
// 'start of month')` composes cleanly with the same window-function
// fold-per-partition shape [rollupDailySelectSQL] already uses for the
// daily rollup, so the exact-first/last invariant carries over unchanged
// instead of being re-derived ad hoc in Go. sum/count stay additive
// (Σsum, Σcount) so avg recomputed from them is exact; min/max fold with
// MIN/MAX-of-MIN/MAX; first/last are the first daily bucket's `first` and
// the last daily bucket's `last` within the month, ordered by bucket_ts.
const queryEnergyMonthSQL = `
    SELECT DISTINCT
        channel_address, parameter, month_bucket,
        SUM(sum) OVER w,
        MIN(min) OVER w,
        MAX(max) OVER w,
        FIRST_VALUE(first) OVER (
            PARTITION BY channel_address, parameter, month_bucket
            ORDER BY bucket_ts
        ),
        LAST_VALUE(last) OVER (
            PARTITION BY channel_address, parameter, month_bucket
            ORDER BY bucket_ts
            ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING
        ),
        SUM(count) OVER w
      FROM (
        SELECT channel_address, parameter, bucket_ts, sum, min, max, count, first, last,
               CAST(strftime('%s', bucket_ts / 1000, 'unixepoch', 'start of month') AS INTEGER) * 1000
                   AS month_bucket
          FROM measurements_daily
         WHERE central_name = ?
           AND parameter IN (?, ?, ?)
           AND bucket_ts >= ?
           AND bucket_ts < ?
           AND (? = '' OR channel_address = ? OR channel_address LIKE ?)
      )
    WINDOW w AS (PARTITION BY channel_address, parameter, month_bucket)
    ORDER BY channel_address, parameter, month_bucket
`

// QueryEnergy reads the rollup tier matching group ("hour"|"day"|"month")
// for the energy parameters (POWER, ENERGY_COUNTER,
// ENERGY_COUNTER_FEED_IN) in [from, to), scoped to centralName and —
// when deviceAddr is non-empty — to that device's channels
// (deviceAddr or deviceAddr+":*"). "hour" and "day" read the matching
// rollup table directly (each row is already one bucket); "month" has no
// persisted tier, so it re-aggregates measurements_daily to calendar-month
// buckets in SQL (see [queryEnergyMonthSQL]) rather than fetching daily
// rows and folding them in Go — the fold is exact-once in SQL and mirrors
// the existing RollupDaily window-function shape instead of duplicating
// the first/last logic in the handler. Rows are ordered by
// (channel_address, parameter, bucket_ts) so the handler can fold them by
// device without re-sorting. The caller (the energy handler) folds
// per-channel rows into per-device totals and applies the
// counter-reset rule for cumulative parameters.
func (s *MeasurementStore) QueryEnergy(
	ctx context.Context,
	centralName, deviceAddr string,
	from, to time.Time,
	group string,
) ([]EnergyRow, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	fromMs, toMs := from.UnixMilli(), to.UnixMilli()
	if !to.After(from) {
		return nil, errors.New("measurements.QueryEnergy: to must be after from")
	}
	prefix := deviceAddr + ":%"

	var (
		rows *sql.Rows
		err  error
	)
	switch group {
	case "hour":
		rows, err = s.db.QueryContext(ctx,
			fmt.Sprintf(queryEnergyTierSQL, "measurements_hourly"),
			centralName, energyParameters[0], energyParameters[1], energyParameters[2],
			fromMs, toMs, deviceAddr, deviceAddr, prefix)
	case "day":
		rows, err = s.db.QueryContext(ctx,
			fmt.Sprintf(queryEnergyTierSQL, "measurements_daily"),
			centralName, energyParameters[0], energyParameters[1], energyParameters[2],
			fromMs, toMs, deviceAddr, deviceAddr, prefix)
	case "month":
		rows, err = s.db.QueryContext(ctx, queryEnergyMonthSQL,
			centralName, energyParameters[0], energyParameters[1], energyParameters[2],
			fromMs, toMs, deviceAddr, deviceAddr, prefix)
	default:
		return nil, fmt.Errorf("measurements.QueryEnergy: unsupported group %q", group)
	}
	if err != nil {
		return nil, fmt.Errorf("measurements.QueryEnergy: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []EnergyRow
	for rows.Next() {
		var (
			r        EnergyRow
			bucketMs int64
		)
		if err := rows.Scan(
			&r.ChannelAddress, &r.Parameter, &bucketMs,
			&r.Sum, &r.Min, &r.Max, &r.First, &r.Last, &r.Count,
		); err != nil {
			return nil, fmt.Errorf("measurements.QueryEnergy scan: %w", err)
		}
		r.BucketTS = time.UnixMilli(bucketMs)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("measurements.QueryEnergy rows: %w", err)
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

// rollupHourlySelectSQL aggregates raw rows into one row per (data point,
// hour bucket). It is written as a single window-function pass rather than
// a GROUP BY + correlated subquery for two reasons: (1) SQLite evaluates
// window functions after GROUP BY, so a plain aggregate query cannot also
// expose the ungrouped `value`/`ts` columns that FIRST_VALUE/LAST_VALUE
// need; (2) computing every aggregate as a window function over the same
// partition in one pass, then collapsing the redundant per-row copies with
// DISTINCT, matches an aggregate GROUP BY plan performance-wise for SQLite's
// optimizer while keeping first/last exact.
//
// `w` (no ORDER BY) gives whole-partition SUM/MIN/MAX/COUNT. FIRST_VALUE
// uses the default frame (RANGE UNBOUNDED PRECEDING .. CURRENT ROW), which —
// because the frame's lower bound never moves — always resolves to the
// partition's earliest row when ordered by ts ascending. LAST_VALUE needs an
// explicit ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING frame;
// its default frame would otherwise return the *current* row, not the last.
//
// The WHERE clause re-selects every raw row below the cutoff on every call,
// so re-running the fold against the same source rows recomputes the exact
// same aggregate — the ON CONFLICT DO UPDATE overwrite is idempotent, never
// additive.
const rollupHourlySelectSQL = `
    SELECT DISTINCT
        central_name, interface_id, channel_address, parameter, bucket_ts,
        SUM(value) OVER w,
        MIN(value) OVER w,
        MAX(value) OVER w,
        COUNT(*) OVER w,
        FIRST_VALUE(value) OVER (
            PARTITION BY central_name, interface_id, channel_address, parameter, bucket_ts
            ORDER BY ts
        ),
        LAST_VALUE(value) OVER (
            PARTITION BY central_name, interface_id, channel_address, parameter, bucket_ts
            ORDER BY ts
            ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING
        )
      FROM (
        SELECT central_name, interface_id, channel_address, parameter, ts, value,
               ts - (ts % 3600000) AS bucket_ts
          FROM measurements
         WHERE ts < ?
      )
    WINDOW w AS (PARTITION BY central_name, interface_id, channel_address, parameter, bucket_ts)
`

// RollupHourly folds raw measurement rows with ts < olderThan into the
// hourly rollup tier (measurements_hourly), one row per (data point, hour
// bucket): bucket_ts = ts - (ts % 3600000). See [rollupHourlySelectSQL] for
// why the aggregate is a single window-function pass and why re-running it
// is idempotent. Returns the number of raw rows folded (source rows read,
// not buckets written) so callers can log fold volume.
func (s *MeasurementStore) RollupHourly(ctx context.Context, olderThan time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	cutoff := olderThan.UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("measurements.RollupHourly begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var folded int64
	row := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM measurements WHERE ts < ?`, cutoff)
	if err := row.Scan(&folded); err != nil {
		return 0, fmt.Errorf("measurements.RollupHourly count: %w", err)
	}
	if folded == 0 {
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("measurements.RollupHourly commit: %w", err)
		}
		return 0, nil
	}

	if _, err := tx.ExecContext(ctx, `
        INSERT INTO measurements_hourly
            (central_name, interface_id, channel_address, parameter, bucket_ts,
             sum, min, max, count, first, last)
        `+rollupHourlySelectSQL+`
        ON CONFLICT (central_name, interface_id, channel_address, parameter, bucket_ts) DO UPDATE SET
            sum   = excluded.sum,
            min   = excluded.min,
            max   = excluded.max,
            count = excluded.count,
            first = excluded.first,
            last  = excluded.last
    `, cutoff); err != nil {
		return 0, fmt.Errorf("measurements.RollupHourly insert: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("measurements.RollupHourly commit: %w", err)
	}
	return folded, nil
}

// rollupDailySelectSQL re-aggregates hourly rows into one row per (data
// point, UTC day bucket): sum/count are additive (Σsum, Σcount) and remain
// exact because the hourly tier already carries sum+count rather than an
// average. min/max fold with MIN/MAX-of-MIN/MAX. first/last are the first
// hourly bucket's `first` and the last hourly bucket's `last` in the day,
// ordered by the hourly bucket_ts — the same window-function shape as
// [rollupHourlySelectSQL] and idempotent for the same reason (the WHERE
// clause re-reads every hourly row below the cutoff on every call).
const rollupDailySelectSQL = `
    SELECT DISTINCT
        central_name, interface_id, channel_address, parameter, day_bucket,
        SUM(sum) OVER w,
        MIN(min) OVER w,
        MAX(max) OVER w,
        SUM(count) OVER w,
        FIRST_VALUE(first) OVER (
            PARTITION BY central_name, interface_id, channel_address, parameter, day_bucket
            ORDER BY bucket_ts
        ),
        LAST_VALUE(last) OVER (
            PARTITION BY central_name, interface_id, channel_address, parameter, day_bucket
            ORDER BY bucket_ts
            ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING
        )
      FROM (
        SELECT central_name, interface_id, channel_address, parameter,
               bucket_ts, sum, min, max, count, first, last,
               bucket_ts - (bucket_ts % 86400000) AS day_bucket
          FROM measurements_hourly
         WHERE bucket_ts < ?
      )
    WINDOW w AS (PARTITION BY central_name, interface_id, channel_address, parameter, day_bucket)
`

// RollupDaily folds hourly rollup rows with bucket_ts < olderThan into the
// daily rollup tier (measurements_daily): day_bucket = bucket_ts -
// (bucket_ts % 86400000), UTC day boundaries. Returns the number of hourly
// rows folded (source rows read, not buckets written).
func (s *MeasurementStore) RollupDaily(ctx context.Context, olderThan time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	cutoff := olderThan.UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("measurements.RollupDaily begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var folded int64
	row := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM measurements_hourly WHERE bucket_ts < ?`, cutoff)
	if err := row.Scan(&folded); err != nil {
		return 0, fmt.Errorf("measurements.RollupDaily count: %w", err)
	}
	if folded == 0 {
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("measurements.RollupDaily commit: %w", err)
		}
		return 0, nil
	}

	if _, err := tx.ExecContext(ctx, `
        INSERT INTO measurements_daily
            (central_name, interface_id, channel_address, parameter, bucket_ts,
             sum, min, max, count, first, last)
        `+rollupDailySelectSQL+`
        ON CONFLICT (central_name, interface_id, channel_address, parameter, bucket_ts) DO UPDATE SET
            sum   = excluded.sum,
            min   = excluded.min,
            max   = excluded.max,
            count = excluded.count,
            first = excluded.first,
            last  = excluded.last
    `, cutoff); err != nil {
		return 0, fmt.Errorf("measurements.RollupDaily insert: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("measurements.RollupDaily commit: %w", err)
	}
	return folded, nil
}

// DeleteHourlyOlderThan drops every measurements_hourly row whose bucket
// start is before cutoff. Mirrors [MeasurementStore.DeleteOlderThan];
// called only after the daily rollup has folded the affected hourly rows,
// so their sum/count survive in the daily tier. Returns the number of rows
// removed.
func (s *MeasurementStore) DeleteHourlyOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM measurements_hourly WHERE bucket_ts < ?`, cutoff.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("measurements.DeleteHourlyOlderThan: %w", err)
	}
	n, _ := res.RowsAffected()
	s.metricRetentionDeleted.Add(n)
	return n, nil
}

// DeleteDailyOlderThan drops every measurements_daily row whose bucket
// start is before cutoff. Mirrors [MeasurementStore.DeleteOlderThan].
// Callers should skip calling this when the daily-retention config is 0
// (keep daily rows forever — they are tiny). Returns the number of rows
// removed.
func (s *MeasurementStore) DeleteDailyOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM measurements_daily WHERE bucket_ts < ?`, cutoff.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("measurements.DeleteDailyOlderThan: %w", err)
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
