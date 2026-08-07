// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"math"
	"slices"
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
	return withMigrationLock(ctx, db, func() error {
		goose.SetBaseFS(historyMigrationsFS)
		if err := goose.SetDialect(string(goose.DialectSQLite3)); err != nil {
			return fmt.Errorf("sqlite: history set dialect: %w", err)
		}
		if err := goose.UpContext(ctx, db, "migrations_history"); err != nil {
			return fmt.Errorf("sqlite: history migrate: %w", err)
		}
		return nil
	})
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
	db  *sql.DB
	loc *time.Location

	metricRowsWritten      atomic.Int64
	metricBatches          atomic.Int64
	metricRetentionDeleted atomic.Int64
}

// NewMeasurementStore returns a store backed by the history database that
// folds day and month buckets on the daemon process's own calendar
// ([time.Local]).
//
// The timezone is deliberately the process local zone rather than a config
// knob: the only consumer that reads a meaning into a day boundary is the
// operator looking at a chart, and the SPA labels every bucket with the
// browser's local calendar. Daemon and browser share a household, so the
// process zone is the one that makes "yesterday" mean yesterday. Buckets
// were previously folded on UTC while being labelled locally, which shifted
// every day and month total by the zone's offset at both ends — in CEST a
// "day" ran 02:00 to 02:00 local under the label of the day before.
//
// Operators who run the daemon in a container must therefore give it the
// household's zone (TZ / /etc/localtime) — the same requirement every
// scheduler and log timestamp in the daemon already carries.
func NewMeasurementStore(db *sql.DB) *MeasurementStore {
	return NewMeasurementStoreIn(db, time.Local)
}

// NewMeasurementStoreIn returns a store that folds day and month buckets on
// loc's calendar. Production uses [NewMeasurementStore]; this variant exists
// so a test can pin a zone with a known offset and known DST transitions
// instead of inheriting whatever zone the test machine happens to run in.
// A nil loc falls back to UTC.
func NewMeasurementStoreIn(db *sql.DB, loc *time.Location) *MeasurementStore {
	if loc == nil {
		loc = time.UTC
	}
	return &MeasurementStore{db: db, loc: loc}
}

// dayStartMs returns the start of the local calendar day containing ms, as
// epoch ms. On a spring-forward day whose local midnight does not exist,
// [time.Date] normalizes to the first instant that does, which is the day's
// true start.
func (s *MeasurementStore) dayStartMs(ms int64) int64 {
	y, m, d := time.UnixMilli(ms).In(s.loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, s.loc).UnixMilli()
}

// nextDayStartMs returns the start of the local calendar day *after* the one
// containing ms. Adding one to the day-of-month and letting [time.Date]
// normalize is what makes a 23-hour and a 25-hour day each advance by
// exactly one day: the arithmetic is calendar arithmetic, never a fixed
// 86400000 ms step.
func (s *MeasurementStore) nextDayStartMs(ms int64) int64 {
	y, m, d := time.UnixMilli(ms).In(s.loc).Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, s.loc).UnixMilli()
}

// monthStartMs returns the start of the local calendar month containing ms.
func (s *MeasurementStore) monthStartMs(ms int64) int64 {
	y, m, _ := time.UnixMilli(ms).In(s.loc).Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, s.loc).UnixMilli()
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

// HistoryTier names the source resolution [MeasurementStore.QueryBuckets]
// assembled an answer from. It is reported alongside the buckets so a
// caller can tell the operator that a long range is drawn at hourly or
// daily resolution instead of from raw samples.
type HistoryTier string

const (
	// HistoryTierRaw reads the raw measurements table directly.
	HistoryTierRaw HistoryTier = "raw"
	// HistoryTierHour reads the hourly rollup plus the un-rolled raw tail.
	HistoryTierHour HistoryTier = "hour"
	// HistoryTierDay reads the daily rollup plus the hourly and raw tails.
	HistoryTierDay HistoryTier = "day"
)

// QueryBuckets aggregates the recorded history of one data point in
// [from, to) into at most buckets evenly spaced buckets, returning
// avg/min/max/count per non-empty bucket in chronological order plus the
// source tier the answer was assembled from. The server-side aggregate
// bounds the response regardless of how many rows back the range, so the
// SPA never pulls the raw series.
//
// The source is chosen by the requested bucket width, mirroring the tier
// assembly the energy path already performs ([MeasurementStore.QueryEnergy]):
// a bucket at least a day wide is served from the daily rollup, at least an
// hour wide from the hourly rollup, anything finer from raw rows. Reading
// only the raw table — which the recorder purges after its (short) raw
// retention — is why a range beyond that retention used to come back empty
// even though the rollups still held the data.
//
// Each tier is completed by the still-un-rolled tail above its watermark,
// so the running hour and the running day are never missing. Rollup rows
// carry sum+count, so the average stays exact across a re-fold (never an
// average of averages) and min/max keep the peak contract.
//
// Source buckets start on an hour or on a local calendar day while output
// buckets are aligned to `from`; a source bucket is attributed to the output
// bucket that contains its start. At tier boundaries that shifts a value by
// at most one source-bucket width, which is inherent to downsampling.
//
// buckets must be > 0 and to must be after from.
func (s *MeasurementStore) QueryBuckets(
	ctx context.Context,
	centralName, interfaceID, channelAddress, parameter string,
	from, to time.Time,
	buckets int,
) ([]MeasurementBucket, HistoryTier, error) {
	if s == nil || s.db == nil {
		return nil, HistoryTierRaw, nil
	}
	if buckets <= 0 {
		return nil, "", errors.New("measurements.QueryBuckets: buckets must be > 0")
	}
	fromMs, toMs := from.UnixMilli(), to.UnixMilli()
	if toMs <= fromMs {
		return nil, "", errors.New("measurements.QueryBuckets: to must be after from")
	}
	// Bucket width in ms, at least 1 so very short ranges still group.
	width := (toMs - fromMs) / int64(buckets)
	if width < 1 {
		width = 1
	}
	key := seriesKey{centralName, interfaceID, channelAddress, parameter}

	tier, err := s.pickHistoryTier(ctx, key, width, fromMs, toMs)
	if err != nil {
		return nil, "", err
	}
	if tier == HistoryTierRaw {
		out, err := s.queryRawBuckets(ctx, key, fromMs, toMs, width, buckets)
		return out, HistoryTierRaw, err
	}
	src, err := s.assembleTierBuckets(ctx, key, tier, fromMs, toMs)
	if err != nil {
		return nil, "", err
	}
	return foldTierBuckets(src, fromMs, width, buckets), tier, nil
}

// seriesKey identifies one recorded data point across all three tiers,
// which share the same (central, interface, channel, parameter) key.
type seriesKey struct {
	central   string
	iface     string
	channel   string
	parameter string
}

// pickHistoryTier selects the source resolution for a bucket width. The
// width decides the base choice; a raw choice is then promoted when the
// hourly rollup can fill more of the requested chart than the raw table
// can, because the recorder has already purged the older raw rows and
// only the rollups still hold them. Without the promotion a narrow-bucket
// query over an old range silently renders empty.
//
// The promotion weighs both tiers at the *requested* bucket width, not at
// the window length alone. The hourly tier yields at most one point per
// hour however narrow the buckets are, so promoting a sub-hour request
// costs resolution and only pays off when raw genuinely cannot serve the
// window. A series learned minutes ago has a raw floor inside the window
// for the innocent reason that it did not exist earlier — folding its raw
// tail onto hour buckets collapsed a 30-minute chart to a single point.
func (s *MeasurementStore) pickHistoryTier(
	ctx context.Context, key seriesKey, width, fromMs, toMs int64,
) (HistoryTier, error) {
	switch {
	case width >= dayBucketMs:
		return HistoryTierDay, nil
	case width >= hourBucketMs:
		return HistoryTierHour, nil
	}
	floor, ok, err := s.rawFloor(ctx, key)
	if err != nil {
		return "", err
	}
	if !ok {
		return HistoryTierHour, nil
	}
	if floor <= fromMs {
		return HistoryTierRaw, nil
	}
	// The raw table starts inside the window: whatever lies before `floor`
	// can only come from the hourly rollup. Compare what each tier can
	// actually fill — the raw tail at the requested width against the part
	// of the window the hourly tier reaches, one point per hour. An hourly
	// tier that starts no earlier than the raw floor adds nothing but
	// coarseness, and scores zero here on its own.
	hourFloor, hourOK, err := s.hourlyFloor(ctx, key)
	if err != nil {
		return "", err
	}
	var hourPoints int64
	if hourOK {
		hourPoints = max(toMs-max(fromMs, hourFloor), 0) / hourBucketMs
	}
	if max(toMs-floor, 0)/width >= hourPoints {
		return HistoryTierRaw, nil
	}
	return HistoryTierHour, nil
}

// rawFloor returns the oldest raw sample timestamp for one series and
// false when the series has no raw rows at all.
func (s *MeasurementStore) rawFloor(ctx context.Context, key seriesKey) (oldestMs int64, ok bool, err error) { //nolint:gocritic // named results document which bool means "has rows"
	var floor sql.NullInt64
	err = s.db.QueryRowContext(ctx, `
        SELECT MIN(ts)
          FROM measurements
         WHERE central_name = ? AND interface_id = ?
           AND channel_address = ? AND parameter = ?
    `, key.central, key.iface, key.channel, key.parameter).Scan(&floor)
	if err != nil {
		return 0, false, fmt.Errorf("measurements.rawFloor: %w", err)
	}
	return floor.Int64, floor.Valid, nil
}

// hourlyFloor returns the oldest folded hourly bucket start for one series
// and false when the series has no hourly rows at all. It bounds how far
// back a promotion to the hourly tier could reach.
func (s *MeasurementStore) hourlyFloor(ctx context.Context, key seriesKey) (oldestMs int64, ok bool, err error) { //nolint:gocritic // named results document which bool means "has rows"
	var floor sql.NullInt64
	err = s.db.QueryRowContext(ctx, `
        SELECT MIN(bucket_ts)
          FROM measurements_hourly
         WHERE central_name = ? AND interface_id = ?
           AND channel_address = ? AND parameter = ?
    `, key.central, key.iface, key.channel, key.parameter).Scan(&floor)
	if err != nil {
		return 0, false, fmt.Errorf("measurements.hourlyFloor: %w", err)
	}
	return floor.Int64, floor.Valid, nil
}

// queryRawBuckets is the fast path: one grouped scan of the raw table that
// buckets server-side, unchanged from before the tiering.
func (s *MeasurementStore) queryRawBuckets(
	ctx context.Context, key seriesKey, fromMs, toMs, width int64, buckets int,
) ([]MeasurementBucket, error) {
	// Integer bucket width truncates, so width*buckets can be strictly less
	// than the range: a ts just below `to` then maps to index `buckets`,
	// one past the last valid slot, yielding a spurious tail bucket. Clamp
	// the index to buckets-1 so that straggler folds into the final bucket
	// instead of overflowing into an extra one.
	maxBucket := int64(buckets - 1)
	rows, err := s.db.QueryContext(ctx, `
        SELECT MIN(CAST((ts - ?) / ? AS INTEGER), ?) AS bucket,
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
    `, fromMs, width, maxBucket, key.central, key.iface, key.channel, key.parameter, fromMs, toMs)
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

// tierBucket is one source-tier aggregate, keyed by its own (hour- or
// day-aligned) bucket start. Unlike the energy rows it carries no
// first/last: a history chart needs avg/min/max only, so the folds below
// are plain GROUP BY aggregates instead of window functions.
type tierBucket struct {
	bucketTS int64
	sum      float64
	minV     float64
	maxV     float64
	count    int64
}

// The three history source reads. Each projects the same five-column shape
// (bucket start, sum, min, max, count) so one scanner drains them all.
const (
	historyTierHourlySelectSQL = `
        SELECT bucket_ts, sum, min, max, count
          FROM measurements_hourly
         WHERE central_name = ? AND interface_id = ?
           AND channel_address = ? AND parameter = ?
           AND bucket_ts >= ? AND bucket_ts < ?
    `
	historyTierDailySelectSQL = `
        SELECT bucket_ts, sum, min, max, count
          FROM measurements_daily
         WHERE central_name = ? AND interface_id = ?
           AND channel_address = ? AND parameter = ?
           AND bucket_ts >= ? AND bucket_ts < ?
    `
	// historyRawFoldSQL folds the un-rolled raw tail into fixed-width
	// buckets; the width is a bind parameter, but only the hour width is
	// used — day buckets are calendar buckets and are never expressible as
	// a modulo (see [MeasurementStore.assembleTierBuckets]).
	historyRawFoldSQL = `
        SELECT ts - (ts % ?) AS bucket_ts,
               SUM(value), MIN(value), MAX(value), COUNT(*)
          FROM measurements
         WHERE central_name = ? AND interface_id = ?
           AND channel_address = ? AND parameter = ?
           AND ts >= ? AND ts < ?
         GROUP BY bucket_ts
    `
)

// assembleTierBuckets collects the source buckets for a tier over
// [fromMs, toMs), completing the persisted rollup with the tails that are
// not folded yet.
//
// The day tier's tails are gathered at hour granularity and folded to local
// calendar days in Go, because a calendar day is not a modulo of the epoch:
// it is 23, 24 or 25 hours long depending on the DST transition it spans.
// Both tails go through one fold, so the single day that straddles the
// (hour-aligned) hourly frontier is accumulated rather than emitted twice.
func (s *MeasurementStore) assembleTierBuckets(
	ctx context.Context, key seriesKey, tier HistoryTier, fromMs, toMs int64,
) ([]tierBucket, error) {
	hourlyWM, err := readWatermark(ctx, s.db, rollupTierHourly)
	if err != nil {
		return nil, err
	}
	if tier == HistoryTierHour {
		tierRows, err := s.readHistoryRange(ctx, historyTierHourlySelectSQL, key, fromMs, min(toMs, hourlyWM))
		if err != nil {
			return nil, err
		}
		tail, err := s.foldRawHistory(ctx, key, hourBucketMs, max(fromMs, hourlyWM), toMs)
		if err != nil {
			return nil, err
		}
		return append(tierRows, tail...), nil
	}
	dailyWM, err := readWatermark(ctx, s.db, rollupTierDaily)
	if err != nil {
		return nil, err
	}
	tierRows, err := s.readHistoryRange(ctx, historyTierDailySelectSQL, key, fromMs, min(toMs, dailyWM))
	if err != nil {
		return nil, err
	}
	hourlyTail, err := s.readHistoryRange(ctx, historyTierHourlySelectSQL, key, max(fromMs, dailyWM), min(toMs, hourlyWM))
	if err != nil {
		return nil, err
	}
	rawTail, err := s.foldRawHistory(ctx, key, hourBucketMs, max(fromMs, hourlyWM), toMs)
	if err != nil {
		return nil, err
	}
	dayTail := foldTierBucketsBy(append(hourlyTail, rawTail...), s.dayStartMs)
	return append(tierRows, dayTail...), nil
}

// readHistoryRange runs one of the fixed tier statements over [fromMs, toMs).
// An empty or inverted range yields no rows without touching the database.
func (s *MeasurementStore) readHistoryRange(
	ctx context.Context, query string, key seriesKey, fromMs, toMs int64,
) ([]tierBucket, error) {
	if toMs <= fromMs {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, query,
		key.central, key.iface, key.channel, key.parameter, fromMs, toMs)
	if err != nil {
		return nil, fmt.Errorf("measurements.readHistoryRange: %w", err)
	}
	return scanTierBuckets(rows)
}

// foldRawHistory folds raw rows into widthMs buckets over [fromMs, toMs).
func (s *MeasurementStore) foldRawHistory(
	ctx context.Context, key seriesKey, widthMs, fromMs, toMs int64,
) ([]tierBucket, error) {
	if toMs <= fromMs {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, historyRawFoldSQL,
		widthMs, key.central, key.iface, key.channel, key.parameter, fromMs, toMs)
	if err != nil {
		return nil, fmt.Errorf("measurements.foldRawHistory: %w", err)
	}
	return scanTierBuckets(rows)
}

// scanTierBuckets drains a five-column source-bucket result set.
func scanTierBuckets(rows *sql.Rows) ([]tierBucket, error) {
	defer func() { _ = rows.Close() }()
	var out []tierBucket
	for rows.Next() {
		var b tierBucket
		if err := rows.Scan(&b.bucketTS, &b.sum, &b.minV, &b.maxV, &b.count); err != nil {
			return nil, fmt.Errorf("measurements.scanTierBuckets: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("measurements.scanTierBuckets: %w", err)
	}
	return out, nil
}

// foldTierBucketsBy re-folds source buckets into the coarser buckets named
// by bucketStart, which maps a source bucket's start to the start of the
// bucket that contains it. sum/count are additive and min/max fold, so the
// result carries the same fidelity as a fold performed in SQL — but the
// bucket boundaries can be calendar boundaries, which SQL arithmetic on the
// epoch axis cannot express.
func foldTierBucketsBy(src []tierBucket, bucketStart func(ms int64) int64) []tierBucket {
	if len(src) == 0 {
		return nil
	}
	byStart := make(map[int64]int, len(src))
	out := make([]tierBucket, 0, len(src))
	for i := range src {
		b := &src[i]
		if b.count == 0 {
			continue
		}
		start := bucketStart(b.bucketTS)
		idx, ok := byStart[start]
		if !ok {
			byStart[start] = len(out)
			out = append(out, tierBucket{
				bucketTS: start,
				sum:      b.sum,
				minV:     b.minV,
				maxV:     b.maxV,
				count:    b.count,
			})
			continue
		}
		a := &out[idx]
		a.sum += b.sum
		a.count += b.count
		a.minV = math.Min(a.minV, b.minV)
		a.maxV = math.Max(a.maxV, b.maxV)
	}
	return out
}

// foldTierBuckets re-folds source buckets into the caller's evenly spaced
// output buckets. Accumulating sum and count (rather than averaging the
// sources' averages) keeps the reported average exact; min/max take the
// extremes. Source buckets are unordered and may repeat an output index —
// that is how the day bucket straddling the hourly frontier merges.
func foldTierBuckets(src []tierBucket, fromMs, width int64, buckets int) []MeasurementBucket {
	if len(src) == 0 {
		return nil
	}
	type acc struct {
		sum        float64
		minV, maxV float64
		count      int64
	}
	maxIdx := int64(buckets - 1)
	byIdx := make(map[int64]*acc, len(src))
	order := make([]int64, 0, len(src))
	for i := range src {
		b := &src[i]
		if b.count == 0 {
			continue
		}
		idx := (b.bucketTS - fromMs) / width
		if idx < 0 {
			idx = 0
		}
		if idx > maxIdx {
			idx = maxIdx
		}
		a, ok := byIdx[idx]
		if !ok {
			byIdx[idx] = &acc{sum: b.sum, minV: b.minV, maxV: b.maxV, count: b.count}
			order = append(order, idx)
			continue
		}
		a.sum += b.sum
		a.count += b.count
		a.minV = math.Min(a.minV, b.minV)
		a.maxV = math.Max(a.maxV, b.maxV)
	}
	slices.Sort(order)
	out := make([]MeasurementBucket, 0, len(order))
	for _, idx := range order {
		a := byIdx[idx]
		out = append(out, MeasurementBucket{
			TS:    time.UnixMilli(fromMs + idx*width),
			Avg:   a.sum / float64(a.count),
			Min:   a.minV,
			Max:   a.maxV,
			Count: a.count,
		})
	}
	return out
}

// Bucket widths on the shared epoch-ms axis. hourBucketMs is a real
// alignment: the hourly tier truncates raw sample timestamps to the hour,
// which is timezone-independent for every whole-hour zone offset.
//
// dayBucketMs is only a *width*, used to pick a source tier for a requested
// resolution. It is never an alignment: the daily tier folds on local
// calendar days, whose length is 23, 24 or 25 hours across a DST transition.
const (
	hourBucketMs int64 = 3600000
	dayBucketMs  int64 = 86400000
)

// Rollup tier names — the keys of the measurement_rollup_state watermark
// table (see migrations_history/003_rollup_watermarks.sql).
const (
	rollupTierHourly = "hourly"
	rollupTierDaily  = "daily"
)

// alignDownMs truncates an epoch-ms timestamp down to the start of its
// width-sized bucket. Timestamps are always non-negative (epoch ms since
// 1970), so a plain modulo is correct without a sign guard.
func alignDownMs(ms, width int64) int64 {
	return ms - ms%width
}

// rowQueryer is the read subset of *sql.DB / *sql.Tx that readWatermark
// needs, so the watermark can be read either inside a rollup transaction or
// against the bare handle during a read-only energy query.
type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// readWatermark returns the folded-frontier watermark for a tier: the
// exclusive upper bound (epoch ms) of the source rows already folded into
// that tier. A missing state row means nothing has been folded yet and
// reads as 0.
func readWatermark(ctx context.Context, q rowQueryer, tier string) (int64, error) {
	var wm int64
	err := q.QueryRowContext(ctx,
		`SELECT watermark FROM measurement_rollup_state WHERE tier = ?`, tier).Scan(&wm)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("measurements.readWatermark %s: %w", tier, err)
	}
	return wm, nil
}

// advanceWatermark moves a tier's watermark forward to wm. The MAX() guard
// means a watermark can never move backwards — a regression would let a
// finalized bucket be re-folded from rows a later purge may have already
// removed, corrupting the aggregate.
func advanceWatermark(ctx context.Context, tx *sql.Tx, tier string, wm int64) error {
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO measurement_rollup_state (tier, watermark) VALUES (?, ?)
        ON CONFLICT (tier) DO UPDATE SET watermark = MAX(watermark, excluded.watermark)
    `, tier, wm); err != nil {
		return fmt.Errorf("measurements.advanceWatermark %s: %w", tier, err)
	}
	return nil
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

// EnergyRow is one (channel, parameter, bucket) aggregate returned by
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

// energyTierHourlySelectSQL and energyTierDailySelectSQL each read one
// persisted rollup tier directly: every row is already one (channel,
// parameter, bucket) aggregate over the requested [from, to) window. Kept
// as two fixed statements (rather than one templated with the table name)
// so the query text is never assembled from a caller-supplied string.
const energyTierHourlySelectSQL = `
    SELECT channel_address, parameter, bucket_ts, sum, min, max, first, last, count
      FROM measurements_hourly
     WHERE central_name = ?
       AND parameter IN (?, ?, ?)
       AND bucket_ts >= ?
       AND bucket_ts < ?
       AND (? = '' OR channel_address = ? OR channel_address LIKE ?)
`

const energyTierDailySelectSQL = `
    SELECT channel_address, parameter, bucket_ts, sum, min, max, first, last, count
      FROM measurements_daily
     WHERE central_name = ?
       AND parameter IN (?, ?, ?)
       AND bucket_ts >= ?
       AND bucket_ts < ?
       AND (? = '' OR channel_address = ? OR channel_address LIKE ?)
`

// energyRawFoldSQL folds raw measurement rows into fixed-width buckets over
// [from, to) — the un-rolled recent tail that the persisted tiers do not
// yet cover, so "energy today" / the current hour is present. The bucket
// width is a bind parameter, but only the hour width is ever passed: a day
// bucket is a calendar bucket and is folded from hour buckets in Go (see
// [MeasurementStore.queryEnergyDay]). Same single-pass window-function shape
// as the hourly rollup: first/last stay the value observed at the
// earliest/latest ts inside the bucket.
const energyRawFoldSQL = `
    SELECT DISTINCT
        channel_address, parameter, bucket_ts,
        SUM(value) OVER w,
        MIN(value) OVER w,
        MAX(value) OVER w,
        FIRST_VALUE(value) OVER (
            PARTITION BY channel_address, parameter, bucket_ts ORDER BY ts
        ),
        LAST_VALUE(value) OVER (
            PARTITION BY channel_address, parameter, bucket_ts ORDER BY ts
            ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING
        ),
        COUNT(*) OVER w
      FROM (
        SELECT channel_address, parameter, ts, value, ts - (ts % ?) AS bucket_ts
          FROM measurements
         WHERE central_name = ?
           AND parameter IN (?, ?, ?)
           AND ts >= ?
           AND ts < ?
           AND (? = '' OR channel_address = ? OR channel_address LIKE ?)
      )
    WINDOW w AS (PARTITION BY channel_address, parameter, bucket_ts)
`

// scanEnergyRows drains an EnergyRow result set. All energy queries — the
// tier reads and the two tail folds — project the same nine-column shape
// (channel, parameter, bucket_ts, sum, min, max, first, last, count), so
// they share one scanner.
func scanEnergyRows(rows *sql.Rows) ([]EnergyRow, error) {
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
			return nil, fmt.Errorf("measurements.scanEnergyRows: %w", err)
		}
		r.BucketTS = time.UnixMilli(bucketMs)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("measurements.scanEnergyRows: %w", err)
	}
	return out, nil
}

// readEnergyTier reads persisted tier rows for [fromMs, toMs) using the given
// fixed tier query (one of energyTierHourlySelectSQL / energyTierDailySelectSQL).
// tierName only labels errors. An empty or inverted range yields no rows
// (nil) without touching the database.
func (s *MeasurementStore) readEnergyTier(
	ctx context.Context, query, tierName, central, deviceAddr string, fromMs, toMs int64,
) ([]EnergyRow, error) {
	if toMs <= fromMs {
		return nil, nil
	}
	prefix := deviceAddr + ":%"
	rows, err := s.db.QueryContext(ctx, query,
		central, energyParameters[0], energyParameters[1], energyParameters[2],
		fromMs, toMs, deviceAddr, deviceAddr, prefix)
	if err != nil {
		return nil, fmt.Errorf("measurements.readEnergyTier %s: %w", tierName, err)
	}
	return scanEnergyRows(rows)
}

// foldRawEnergy folds raw rows into widthMs buckets over [fromMs, toMs).
func (s *MeasurementStore) foldRawEnergy(
	ctx context.Context, central, deviceAddr string, widthMs, fromMs, toMs int64,
) ([]EnergyRow, error) {
	if toMs <= fromMs {
		return nil, nil
	}
	prefix := deviceAddr + ":%"
	rows, err := s.db.QueryContext(ctx, energyRawFoldSQL,
		widthMs, central, energyParameters[0], energyParameters[1], energyParameters[2],
		fromMs, toMs, deviceAddr, deviceAddr, prefix)
	if err != nil {
		return nil, fmt.Errorf("measurements.foldRawEnergy: %w", err)
	}
	return scanEnergyRows(rows)
}

// QueryEnergy returns per-(channel, parameter) energy aggregates for the
// energy parameters (POWER, ENERGY_COUNTER, ENERGY_COUNTER_FEED_IN) in
// [from, to), scoped to centralName and — when deviceAddr is non-empty — to
// that device's channels (deviceAddr or deviceAddr+":*").
//
// Every group merges the persisted rollup tiers with the still-un-rolled
// recent tail, so the current hour / day / month is never missing:
//
//   - "hour": the hourly tier below the hourly fold frontier, plus raw rows
//     at or after it folded to hour buckets. The frontier is hour-aligned,
//     so no hour bucket straddles the split — the two sets are disjoint.
//   - "day": the daily tier below the daily frontier, plus the hourly tier
//     between the daily and hourly frontiers, plus raw rows after the hourly
//     frontier folded to hour buckets. Both tails go through one fold to
//     local calendar days, so the single day that straddles the hourly
//     frontier is accumulated rather than emitted twice.
//   - "month": the assembled day rows re-folded to local calendar months,
//     so the running month is present for the same reason the running day
//     is.
//
// Day and month buckets are local calendar buckets (see
// [NewMeasurementStore]); their bucket_ts is the local midnight / local
// first-of-month, which is what the SPA renders with a local date format.
//
// Rows are unordered; the caller ([handlers.FoldEnergyRows]) sorts them and
// applies the counter-reset rule for the cumulative parameters.
func (s *MeasurementStore) QueryEnergy(
	ctx context.Context,
	centralName, deviceAddr string,
	from, to time.Time,
	group string,
) ([]EnergyRow, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if !to.After(from) {
		return nil, errors.New("measurements.QueryEnergy: to must be after from")
	}
	fromMs, toMs := from.UnixMilli(), to.UnixMilli()
	switch group {
	case "hour":
		return s.queryEnergyHour(ctx, centralName, deviceAddr, fromMs, toMs)
	case "day":
		return s.queryEnergyDay(ctx, centralName, deviceAddr, fromMs, toMs)
	case "month":
		days, err := s.queryEnergyDay(ctx, centralName, deviceAddr, fromMs, toMs)
		if err != nil {
			return nil, err
		}
		return foldEnergyRowsBy(days, s.monthStartMs), nil
	default:
		return nil, fmt.Errorf("measurements.QueryEnergy: unsupported group %q", group)
	}
}

// queryEnergyHour assembles hour-granular rows: persisted hourly tier below
// the fold frontier, raw tail at or after it.
func (s *MeasurementStore) queryEnergyHour(
	ctx context.Context, central, deviceAddr string, fromMs, toMs int64,
) ([]EnergyRow, error) {
	hourlyWM, err := readWatermark(ctx, s.db, rollupTierHourly)
	if err != nil {
		return nil, err
	}
	tier, err := s.readEnergyTier(ctx, energyTierHourlySelectSQL, "measurements_hourly", central, deviceAddr, fromMs, min(toMs, hourlyWM))
	if err != nil {
		return nil, err
	}
	tail, err := s.foldRawEnergy(ctx, central, deviceAddr, hourBucketMs, max(fromMs, hourlyWM), toMs)
	if err != nil {
		return nil, err
	}
	return append(tier, tail...), nil
}

// queryEnergyDay assembles day-granular rows: the daily tier below the
// daily frontier, plus the two hour-granular tails above it folded to local
// calendar days.
//
// Folding both tails in one pass is what merges the single local day that
// straddles the (hour-aligned, never day-aligned) hourly frontier — the fold
// keys on the day, not on which tail a row came from.
func (s *MeasurementStore) queryEnergyDay(
	ctx context.Context, central, deviceAddr string, fromMs, toMs int64,
) ([]EnergyRow, error) {
	hourlyWM, err := readWatermark(ctx, s.db, rollupTierHourly)
	if err != nil {
		return nil, err
	}
	dailyWM, err := readWatermark(ctx, s.db, rollupTierDaily)
	if err != nil {
		return nil, err
	}
	tier, err := s.readEnergyTier(ctx, energyTierDailySelectSQL, "measurements_daily", central, deviceAddr, fromMs, min(toMs, dailyWM))
	if err != nil {
		return nil, err
	}
	hourlyTail, err := s.readEnergyTier(ctx, energyTierHourlySelectSQL, "measurements_hourly", central, deviceAddr, max(fromMs, dailyWM), min(toMs, hourlyWM))
	if err != nil {
		return nil, err
	}
	rawTail, err := s.foldRawEnergy(ctx, central, deviceAddr, hourBucketMs, max(fromMs, hourlyWM), toMs)
	if err != nil {
		return nil, err
	}
	return append(tier, foldEnergyRowsBy(append(hourlyTail, rawTail...), s.dayStartMs)...), nil
}

// foldEnergyRowsBy re-folds rows into the coarser buckets named by
// bucketStart, which maps a source bucket's start to the start of the bucket
// that contains it. sum/count are additive; min/max fold; first/last are the
// value of the time-earliest / time-latest contributing source bucket, which
// is what keeps the cumulative-counter delta (last-first) exact across the
// fold.
//
// This is the only place day and month buckets are formed on a query, so a
// calendar boundary is expressed exactly once — SQL arithmetic on the
// epoch-ms axis cannot express one, since a local day spans 23, 24 or 25
// hours and a month 28 to 31 days.
func foldEnergyRowsBy(rows []EnergyRow, bucketStart func(ms int64) int64) []EnergyRow {
	if len(rows) == 0 {
		return nil
	}
	type key struct {
		channel string
		param   string
		bucket  int64
	}
	type acc struct {
		row     EnergyRow
		firstTS int64
		lastTS  int64
	}
	accs := make(map[key]*acc, len(rows))
	order := make([]key, 0, len(rows))
	for _, r := range rows {
		srcMs := r.BucketTS.UnixMilli()
		startMs := bucketStart(srcMs)
		k := key{r.ChannelAddress, r.Parameter, startMs}
		a, ok := accs[k]
		if !ok {
			folded := r
			folded.BucketTS = time.UnixMilli(startMs)
			accs[k] = &acc{row: folded, firstTS: srcMs, lastTS: srcMs}
			order = append(order, k)
			continue
		}
		a.row.Sum += r.Sum
		a.row.Count += r.Count
		if r.Min < a.row.Min {
			a.row.Min = r.Min
		}
		if r.Max > a.row.Max {
			a.row.Max = r.Max
		}
		if srcMs < a.firstTS {
			a.firstTS, a.row.First = srcMs, r.First
		}
		if srcMs > a.lastTS {
			a.lastTS, a.row.Last = srcMs, r.Last
		}
	}
	out := make([]EnergyRow, 0, len(order))
	for _, k := range order {
		out = append(out, accs[k].row)
	}
	return out
}

// DeleteOlderThan drops raw rows older than cutoff. The retention job calls
// it with now-retention. The effective cutoff is floored by the hourly
// watermark so a raw row is never deleted before it has been folded into
// the hourly tier, and aligned down to an hour boundary so a purge never
// splits a bucket a fold might still read — together this keeps a purge
// from corrupting a finalized aggregate. Returns the number of rows removed.
func (s *MeasurementStore) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	watermark, err := readWatermark(ctx, s.db, rollupTierHourly)
	if err != nil {
		return 0, err
	}
	eff := min(alignDownMs(cutoff.UnixMilli(), hourBucketMs), watermark)
	res, err := s.db.ExecContext(ctx, `DELETE FROM measurements WHERE ts < ?`, eff)
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
// The two binds are the fold window [watermark, cutoff): only the raw rows
// in that half-open, ts-indexed range are read. Buckets below the watermark
// are already finalized and never re-scanned; the ON CONFLICT DO UPDATE
// stays only as a safety net against a re-run of the exact same window.
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
         WHERE ts >= ? AND ts < ?
      )
    WINDOW w AS (PARTITION BY central_name, interface_id, channel_address, parameter, bucket_ts)
`

// RollupHourly folds newly-eligible raw rows into the hourly rollup tier
// (measurements_hourly), one row per (data point, hour bucket): bucket_ts =
// ts - (ts % 3600000). Only complete hour buckets in [watermark, cutoff)
// are folded, where cutoff is olderThan aligned down to an hour boundary
// (so no partial boundary bucket is ever written) and watermark is the
// exclusive upper bound of the previous fold. This bounds each run to the
// newly-eligible slice instead of re-scanning the whole raw table, and the
// watermark advances to cutoff so a finalized bucket is never re-folded.
// Returns the number of raw rows folded this run.
func (s *MeasurementStore) RollupHourly(ctx context.Context, olderThan time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	cutoff := alignDownMs(olderThan.UnixMilli(), hourBucketMs)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("measurements.RollupHourly begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	watermark, err := readWatermark(ctx, tx, rollupTierHourly)
	if err != nil {
		return 0, err
	}
	if cutoff <= watermark {
		// No newly-eligible complete hour bucket since the last fold.
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("measurements.RollupHourly commit: %w", err)
		}
		return 0, nil
	}

	var folded int64
	row := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM measurements WHERE ts >= ? AND ts < ?`, watermark, cutoff)
	if err := row.Scan(&folded); err != nil {
		return 0, fmt.Errorf("measurements.RollupHourly count: %w", err)
	}
	if folded > 0 {
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
    `, watermark, cutoff); err != nil {
			return 0, fmt.Errorf("measurements.RollupHourly insert: %w", err)
		}
	}
	if err := advanceWatermark(ctx, tx, rollupTierHourly, cutoff); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("measurements.RollupHourly commit: %w", err)
	}
	return folded, nil
}

// rollupDailySelectSQL re-aggregates the hourly rows of ONE local calendar
// day into one row per data point: sum/count are additive (Σsum, Σcount) and
// remain exact because the hourly tier already carries sum+count rather than
// an average. min/max fold with MIN/MAX-of-MIN/MAX. first/last are the first
// hourly bucket's `first` and the last hourly bucket's `last` in the day,
// ordered by the hourly bucket_ts — the same window-function shape as
// [rollupHourlySelectSQL].
//
// The day is scoped by the bound range rather than computed from bucket_ts,
// because a local calendar day is not a modulo of the epoch: it is 23, 24 or
// 25 hours long depending on the DST transition it spans, so only Go's
// calendar arithmetic can name its boundaries. The three binds are the day's
// own bucket_ts (written verbatim into the daily tier) and the half-open
// [dayStart, dayEnd) range over the hourly bucket_ts axis.
const rollupDailySelectSQL = `
    SELECT DISTINCT
        central_name, interface_id, channel_address, parameter, ?,
        SUM(sum) OVER w,
        MIN(min) OVER w,
        MAX(max) OVER w,
        SUM(count) OVER w,
        FIRST_VALUE(first) OVER (
            PARTITION BY central_name, interface_id, channel_address, parameter
            ORDER BY bucket_ts
        ),
        LAST_VALUE(last) OVER (
            PARTITION BY central_name, interface_id, channel_address, parameter
            ORDER BY bucket_ts
            ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING
        )
      FROM measurements_hourly
     WHERE bucket_ts >= ? AND bucket_ts < ?
    WINDOW w AS (PARTITION BY central_name, interface_id, channel_address, parameter)
`

// RollupDaily folds newly-eligible hourly rows into the daily rollup tier
// (measurements_daily), one row per (data point, local calendar day). Like
// [MeasurementStore.RollupHourly] it folds only complete day buckets in
// [watermark, cutoff) — cutoff is olderThan aligned down to a local day
// start, watermark is the daily tier's frontier over the hourly bucket_ts
// axis — and advances the daily watermark to cutoff. Returns the number of
// hourly rows folded this run.
//
// The fold runs one statement per local day instead of one grouped statement
// over the window: the day boundaries come from the calendar, so they cannot
// be an expression over bucket_ts. The loop is bounded by the hourly rows
// actually present in the window, so an empty or sparse history costs one
// statement per day that holds data, not one per day since the epoch.
//
// The tier's resolution floor is the hour bucket it folds from. In a zone
// whose offset is a whole number of hours the day boundary is exact; in one
// that is not (+05:30, +05:45) it lands on the hour bucket containing local
// midnight, so at most one hour sits on the wrong side of a day edge. Every
// reader derives the boundary the same way, so the tiers stay consistent
// with each other and with the un-rolled tails.
func (s *MeasurementStore) RollupDaily(ctx context.Context, olderThan time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	cutoff := s.dayStartMs(olderThan.UnixMilli())

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("measurements.RollupDaily begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	watermark, err := readWatermark(ctx, tx, rollupTierDaily)
	if err != nil {
		return 0, err
	}
	// The daily fold reads the hourly tier, so it may never advance past
	// what that tier actually holds. Its window is [watermark, cutoff) and
	// the watermark advances across the whole window whether or not rows
	// were found — so a cutoff beyond the hourly frontier skips those days
	// permanently, and the hourly purge (floored by this same watermark)
	// is then free to delete the buckets that would have filled them.
	hourlyWM, err := readWatermark(ctx, tx, rollupTierHourly)
	if err != nil {
		return 0, err
	}
	if hourlyWM < cutoff {
		cutoff = s.dayStartMs(hourlyWM)
	}
	if cutoff <= watermark {
		// No newly-eligible complete day bucket since the last fold.
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("measurements.RollupDaily commit: %w", err)
		}
		return 0, nil
	}

	var (
		folded   int64
		oldest   sql.NullInt64
		newest   sql.NullInt64
		countSQL = `SELECT COUNT(*), MIN(bucket_ts), MAX(bucket_ts)
                      FROM measurements_hourly WHERE bucket_ts >= ? AND bucket_ts < ?`
	)
	if err := tx.QueryRowContext(ctx, countSQL, watermark, cutoff).
		Scan(&folded, &oldest, &newest); err != nil {
		return 0, fmt.Errorf("measurements.RollupDaily count: %w", err)
	}
	if folded > 0 {
		if err := s.foldDaysIntoDailyTier(ctx, tx, watermark, cutoff, oldest.Int64, newest.Int64); err != nil {
			return 0, err
		}
	}
	if err := advanceWatermark(ctx, tx, rollupTierDaily, cutoff); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("measurements.RollupDaily commit: %w", err)
	}
	return folded, nil
}

// foldDaysIntoDailyTier writes one daily row per data point for every local
// calendar day between the oldest and newest hourly bucket in the fold
// window. Each day's range is clamped to [watermark, cutoff) so the fold
// never reads an hourly bucket below the frontier — one a purge may already
// have removed — which would otherwise overwrite a finalized daily row with
// a partial one.
func (s *MeasurementStore) foldDaysIntoDailyTier(
	ctx context.Context, tx *sql.Tx, watermark, cutoff, oldestMs, newestMs int64,
) error {
	const insertSQL = `
        INSERT INTO measurements_daily
            (central_name, interface_id, channel_address, parameter, bucket_ts,
             sum, min, max, count, first, last)
        ` + rollupDailySelectSQL + `
        ON CONFLICT (central_name, interface_id, channel_address, parameter, bucket_ts) DO UPDATE SET
            sum   = excluded.sum,
            min   = excluded.min,
            max   = excluded.max,
            count = excluded.count,
            first = excluded.first,
            last  = excluded.last
    `
	for dayStart := s.dayStartMs(oldestMs); dayStart <= newestMs && dayStart < cutoff; dayStart = s.nextDayStartMs(dayStart) {
		lo := max(dayStart, watermark)
		hi := min(s.nextDayStartMs(dayStart), cutoff)
		if hi <= lo {
			continue
		}
		if _, err := tx.ExecContext(ctx, insertSQL, dayStart, lo, hi); err != nil {
			return fmt.Errorf("measurements.RollupDaily insert: %w", err)
		}
	}
	return nil
}

// DeleteHourlyOlderThan drops measurements_hourly rows older than cutoff.
// The effective cutoff is aligned down to an hour boundary and floored by
// the daily watermark, so an hourly bucket is never deleted before the
// daily fold has consumed it — the same never-purge-before-fold guard the
// raw purge applies, one tier up. Returns the number of rows removed.
func (s *MeasurementStore) DeleteHourlyOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	watermark, err := readWatermark(ctx, s.db, rollupTierDaily)
	if err != nil {
		return 0, err
	}
	eff := min(alignDownMs(cutoff.UnixMilli(), hourBucketMs), watermark)
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM measurements_hourly WHERE bucket_ts < ?`, eff)
	if err != nil {
		return 0, fmt.Errorf("measurements.DeleteHourlyOlderThan: %w", err)
	}
	n, _ := res.RowsAffected()
	s.metricRetentionDeleted.Add(n)
	return n, nil
}

// DeleteDailyOlderThan drops measurements_daily rows older than cutoff. The
// daily tier is terminal (nothing folds out of it), so the cutoff only
// needs local day-boundary alignment — no watermark floor. Callers should
// skip this when the daily-retention config is 0 (keep daily rows forever —
// they are tiny). Returns the number of rows removed.
func (s *MeasurementStore) DeleteDailyOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	eff := s.dayStartMs(cutoff.UnixMilli())
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM measurements_daily WHERE bucket_ts < ?`, eff)
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
	// One statement per tier: the rollups outlive the raw rows, so an
	// unpaired device would keep its aggregates for the tiers' whole
	// retention if only `measurements` were cleared.
	for _, stmt := range []string{
		deleteDeviceRawSQL,
		deleteDeviceHourlySQL,
		deleteDeviceDailySQL,
	} {
		if _, err := s.db.ExecContext(ctx, stmt,
			centralName, interfaceID, deviceAddress, prefix); err != nil {
			return fmt.Errorf("measurements.DeleteDevice: %w", err)
		}
	}
	return nil
}

// deleteDevice*SQL delete one device's rows from each measurement tier.
// Written out per tier rather than assembled from a table name so each
// statement stays a literal constant.
const (
	deleteDeviceRawSQL = `
        DELETE FROM measurements
         WHERE central_name = ? AND interface_id = ?
           AND (channel_address = ? OR channel_address LIKE ? || '%' ESCAPE '\')`
	deleteDeviceHourlySQL = `
        DELETE FROM measurements_hourly
         WHERE central_name = ? AND interface_id = ?
           AND (channel_address = ? OR channel_address LIKE ? || '%' ESCAPE '\')`
	deleteDeviceDailySQL = `
        DELETE FROM measurements_daily
         WHERE central_name = ? AND interface_id = ?
           AND (channel_address = ? OR channel_address LIKE ? || '%' ESCAPE '\')`
)

// DeleteForCentral removes every measurement recorded for the given central,
// across every interface and device. Used on live central removal so a
// removed CCU's history does not linger under a name that could later be
// reused by an unrelated CCU.
func (s *MeasurementStore) DeleteForCentral(ctx context.Context, centralName string) error {
	if s == nil || s.db == nil {
		return nil
	}
	// All three tiers, not just the raw table. The rollups outlive the raw
	// rows by design (hourly 13 months, daily forever by default), so
	// deleting only `measurements` left a removed CCU's aggregates behind —
	// and re-adopting the same central name resurfaced them in the energy
	// views as if the CCU had never been away.
	for _, stmt := range []string{
		`DELETE FROM measurements WHERE central_name = ?`,
		`DELETE FROM measurements_hourly WHERE central_name = ?`,
		`DELETE FROM measurements_daily WHERE central_name = ?`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt, centralName); err != nil {
			return fmt.Errorf("measurements.DeleteForCentral: %w", err)
		}
	}
	return nil
}

// DeleteAll empties the history. Used by the global reset endpoint and by
// tests.
func (s *MeasurementStore) DeleteAll(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	for _, stmt := range []string{
		`DELETE FROM measurements`,
		`DELETE FROM measurements_hourly`,
		`DELETE FROM measurements_daily`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("measurements.DeleteAll: %w", err)
		}
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
