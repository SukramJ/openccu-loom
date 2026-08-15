// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

// Fixed identity for every series in this file; the timezone, not the key,
// is what these tests vary.
const (
	localDayCentral = "ccu1"
	localDayIface   = "HmIP-RF"
	localDayChannel = "DEV:1"
	localDayParam   = "POWER"
)

// berlin is a zone with a non-zero offset and both DST transitions, which is
// what makes it a usable stand-in for the households this daemon runs in.
// The zone is loaded explicitly rather than taken from the environment so
// the expectations below hold on any test machine.
func berlin(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("Europe/Berlin unavailable: %v", err)
	}
	return loc
}

// measurementStoreIn opens a fresh history DB folding day and month buckets
// on loc.
func measurementStoreIn(t *testing.T, loc *time.Location) *MeasurementStore {
	t.Helper()
	return NewMeasurementStoreIn(openHistoryDB(t, "hist.db"), loc)
}

// recordSamples writes samples and folds them all the way into the daily
// tier, using a fold cutoff far enough past the last sample that every
// bucket is complete.
func recordSamples(t *testing.T, s *MeasurementStore, samples []MeasurementSample, foldUpTo time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, foldUpTo); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	if _, err := s.RollupDaily(ctx, foldUpTo); err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}
}

// sampleAt returns one sample of value v at ts.
func sampleAt(ts time.Time, v float64) MeasurementSample {
	return MeasurementSample{
		CentralName:    localDayCentral,
		InterfaceID:    localDayIface,
		ChannelAddress: localDayChannel,
		Parameter:      localDayParam,
		TS:             ts,
		Value:          v,
	}
}

// TestDailyBucketFollowsTheLocalCalendarDay pins the defect this file
// exists for: a measurement taken just after local midnight belongs to the
// local calendar day that just started, not to the one that is still
// running in UTC.
//
// The daily tier used to bucket on the UTC day while every surface labels a
// bucket with a local date, so at UTC+2 a reading at 00:30 local on the 6th
// (22:30 UTC on the 5th) was reported under the 5th — and the first two
// hours of every day were counted against the previous one.
func TestDailyBucketFollowsTheLocalCalendarDay(t *testing.T) {
	t.Parallel()
	loc := berlin(t)
	s := measurementStoreIn(t, loc)

	justAfterMidnight := time.Date(2026, 8, 6, 0, 30, 0, 0, loc)
	if justAfterMidnight.UTC().Day() != 5 {
		t.Fatalf("fixture broken: %s is not the previous UTC day", justAfterMidnight.UTC())
	}
	recordSamples(t, s, []MeasurementSample{sampleAt(justAfterMidnight, 42)},
		justAfterMidnight.Add(72*time.Hour))

	daily := queryDaily(t, s, localDayCentral, localDayIface, localDayChannel, localDayParam)
	if len(daily) != 1 {
		t.Fatalf("daily rows = %d, want 1: %+v", len(daily), daily)
	}
	wantDay := time.Date(2026, 8, 6, 0, 0, 0, 0, loc)
	if got := time.UnixMilli(daily[0].BucketTS).In(loc); !got.Equal(wantDay) {
		t.Errorf("daily bucket = %s, want the local day start %s", got, wantDay)
	}
}

// TestEnergyDayBucketFollowsTheLocalCalendarDay is the same assertion one
// layer up: the energy query is what the SPA reads, and it labels each
// bucket with a local date. A bucket start that is not a local midnight
// renders as the wrong day whatever the sum behind it is.
func TestEnergyDayBucketFollowsTheLocalCalendarDay(t *testing.T) {
	t.Parallel()
	loc := berlin(t)
	s := measurementStoreIn(t, loc)
	ctx := context.Background()

	justAfterMidnight := time.Date(2026, 8, 6, 0, 30, 0, 0, loc)
	recordSamples(t, s, []MeasurementSample{sampleAt(justAfterMidnight, 42)},
		justAfterMidnight.Add(72*time.Hour))

	rows, err := s.QueryEnergy(ctx, localDayCentral, "",
		justAfterMidnight.Add(-48*time.Hour), justAfterMidnight.Add(48*time.Hour), "day")
	if err != nil {
		t.Fatalf("QueryEnergy: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("energy rows = %d, want 1: %+v", len(rows), rows)
	}
	wantDay := time.Date(2026, 8, 6, 0, 0, 0, 0, loc)
	if got := rows[0].BucketTS.In(loc); !got.Equal(wantDay) {
		t.Errorf("energy day bucket = %s, want %s", got, wantDay)
	}
}

// TestEnergyMonthBucketFollowsTheLocalCalendarMonth pins the same rule for
// months: the first two hours of the first of a month used to be billed
// against the month before.
func TestEnergyMonthBucketFollowsTheLocalCalendarMonth(t *testing.T) {
	t.Parallel()
	loc := berlin(t)
	s := measurementStoreIn(t, loc)
	ctx := context.Background()

	justAfterMonthStart := time.Date(2026, 8, 1, 0, 30, 0, 0, loc)
	if justAfterMonthStart.UTC().Month() != time.July {
		t.Fatalf("fixture broken: %s is not in the previous UTC month", justAfterMonthStart.UTC())
	}
	recordSamples(t, s, []MeasurementSample{sampleAt(justAfterMonthStart, 17)},
		justAfterMonthStart.Add(72*time.Hour))

	rows, err := s.QueryEnergy(ctx, localDayCentral, "",
		justAfterMonthStart.Add(-48*time.Hour), justAfterMonthStart.Add(48*time.Hour), "month")
	if err != nil {
		t.Fatalf("QueryEnergy: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("energy rows = %d, want 1: %+v", len(rows), rows)
	}
	wantMonth := time.Date(2026, 8, 1, 0, 0, 0, 0, loc)
	if got := rows[0].BucketTS.In(loc); !got.Equal(wantMonth) {
		t.Errorf("energy month bucket = %s, want %s", got, wantMonth)
	}
}

// TestDailyFoldHandlesDSTTransitionDays pins that a short and a long day
// each fold into exactly one bucket and that no sample is lost or counted
// twice across the transition.
//
// A fixed 86400000 ms modulo cuts the 23-hour day one hour early and the
// 25-hour day one hour late, which splits one of them across two buckets
// and leaves the neighbouring day holding an hour that is not its own. Only
// calendar arithmetic gets both right.
func TestDailyFoldHandlesDSTTransitionDays(t *testing.T) {
	t.Parallel()
	loc := berlin(t)

	cases := []struct {
		name     string
		day      time.Time
		wantHrs  int64
		wantSpan time.Duration
	}{
		{
			// Spring forward: 02:00 local jumps to 03:00, so the day is 23h.
			name:     "spring forward is 23 hours",
			day:      time.Date(2026, 3, 29, 0, 0, 0, 0, loc),
			wantHrs:  23,
			wantSpan: 23 * time.Hour,
		},
		{
			// Fall back: 03:00 local repeats 02:00, so the day is 25h.
			name:     "fall back is 25 hours",
			day:      time.Date(2026, 10, 25, 0, 0, 0, 0, loc),
			wantHrs:  25,
			wantSpan: 25 * time.Hour,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := measurementStoreIn(t, loc)

			nextDay := time.Date(tc.day.Year(), tc.day.Month(), tc.day.Day()+1, 0, 0, 0, 0, loc)
			if got := nextDay.Sub(tc.day); got != tc.wantSpan {
				t.Fatalf("fixture broken: day spans %s, want %s", got, tc.wantSpan)
			}

			// One sample per wall-clock hour of the day, each worth 1, so the
			// bucket's count and sum both equal the day's length in hours.
			var samples []MeasurementSample
			for ts := tc.day; ts.Before(nextDay); ts = ts.Add(time.Hour) {
				samples = append(samples, sampleAt(ts, 1))
			}
			// One sample on each neighbouring day, so a boundary that slips by
			// an hour shows up as a wrong count here too.
			samples = append(samples,
				sampleAt(tc.day.Add(-12*time.Hour), 1),
				sampleAt(nextDay.Add(12*time.Hour), 1))

			recordSamples(t, s, samples, nextDay.Add(72*time.Hour))

			daily := queryDaily(t, s, localDayCentral, localDayIface, localDayChannel, localDayParam)
			if len(daily) != 3 {
				t.Fatalf("daily rows = %d, want 3 (day before, day, day after): %+v", len(daily), daily)
			}

			transition := daily[1]
			if got := time.UnixMilli(transition.BucketTS).In(loc); !got.Equal(tc.day) {
				t.Errorf("bucket start = %s, want %s", got, tc.day)
			}
			if transition.Count != tc.wantHrs {
				t.Errorf("bucket count = %d, want %d — the DST day was not folded as one day",
					transition.Count, tc.wantHrs)
			}
			if transition.Sum != float64(tc.wantHrs) {
				t.Errorf("bucket sum = %v, want %v", transition.Sum, float64(tc.wantHrs))
			}

			// Nothing lost, nothing double-counted: every sample is in exactly
			// one bucket.
			var total int64
			for _, r := range daily {
				total += r.Count
			}
			if want := int64(len(samples)); total != want {
				t.Errorf("total folded samples = %d, want %d", total, want)
			}
		})
	}
}

// TestUnfoldedTailUsesTheSameDayBucketAsTheDailyTier pins that the fold and
// the query agree on where a day starts.
//
// The energy query assembles a day from up to three sources — the persisted
// daily tier, the hourly tier above the daily frontier, and the raw tail
// above the hourly frontier. If any of them cut days differently the same
// measurement would move to a different bucket the moment a rollup ran,
// which reads to an operator as history rewriting itself.
func TestUnfoldedTailUsesTheSameDayBucketAsTheDailyTier(t *testing.T) {
	t.Parallel()
	loc := berlin(t)
	ctx := context.Background()

	justAfterMidnight := time.Date(2026, 8, 6, 0, 30, 0, 0, loc)
	from, to := justAfterMidnight.Add(-48*time.Hour), justAfterMidnight.Add(48*time.Hour)

	// The same measurement, read at three different rollup stages.
	stages := []struct {
		name string
		fold func(t *testing.T, s *MeasurementStore)
	}{
		{"raw tail only", func(*testing.T, *MeasurementStore) {}},
		{"folded to hourly", func(t *testing.T, s *MeasurementStore) {
			t.Helper()
			if _, err := s.RollupHourly(ctx, justAfterMidnight.Add(72*time.Hour)); err != nil {
				t.Fatalf("RollupHourly: %v", err)
			}
		}},
		{"folded to daily", func(t *testing.T, s *MeasurementStore) {
			t.Helper()
			recordSamples(t, s, nil, justAfterMidnight.Add(72*time.Hour))
		}},
	}

	want := time.Date(2026, 8, 6, 0, 0, 0, 0, loc)
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			t.Parallel()
			s := measurementStoreIn(t, loc)
			if err := s.SaveBatch(ctx, []MeasurementSample{sampleAt(justAfterMidnight, 42)}); err != nil {
				t.Fatalf("SaveBatch: %v", err)
			}
			stage.fold(t, s)

			rows, err := s.QueryEnergy(ctx, localDayCentral, "", from, to, "day")
			if err != nil {
				t.Fatalf("QueryEnergy: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("energy rows = %d, want 1: %+v", len(rows), rows)
			}
			if got := rows[0].BucketTS.In(loc); !got.Equal(want) {
				t.Errorf("day bucket = %s, want %s", got, want)
			}

			buckets, tier, err := s.QueryBuckets(ctx, localDayCentral, localDayIface,
				localDayChannel, localDayParam, from, to, 4)
			if err != nil {
				t.Fatalf("QueryBuckets: %v", err)
			}
			if len(buckets) != 1 {
				t.Fatalf("history buckets = %d (tier %s), want 1: %+v", len(buckets), tier, buckets)
			}
			if buckets[0].Count != 1 {
				t.Errorf("history bucket count = %d, want 1", buckets[0].Count)
			}
		})
	}
}

// TestNewMeasurementStoreFoldsOnProcessLocalTime pins the production
// default. The zone is not configurable, so the daemon's own zone is the
// only thing that makes a day bucket mean what the SPA prints next to it —
// a store that quietly defaulted to UTC would reintroduce the shift on
// every deployment without a single test noticing.
func TestNewMeasurementStoreFoldsOnProcessLocalTime(t *testing.T) {
	t.Parallel()
	s := NewMeasurementStore(nil)
	if s.loc != time.Local {
		t.Errorf("NewMeasurementStore folds on %v, want time.Local", s.loc)
	}
}

// openHistoryDBAtVersion opens a history DB migrated up to version, so a
// test can seed the state a real installation was in before a migration ran.
func openHistoryDBAtVersion(t *testing.T, name string, version int64) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), name) + "?_pragma=journal_mode(WAL)"
	db, err := sql.Open(DriverName, dsn)
	if err != nil {
		t.Fatalf("openHistoryDBAtVersion Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := applyPragmas(context.Background(), db); err != nil {
		t.Fatalf("openHistoryDBAtVersion applyPragmas: %v", err)
	}
	migrateHistoryTo(t, db, version)
	return db
}

// migrateHistoryTo runs the history series up to version against an already
// open handle.
func migrateHistoryTo(t *testing.T, db *sql.DB, version int64) {
	t.Helper()
	openMu.Lock()
	defer openMu.Unlock()
	goose.SetBaseFS(historyMigrationsFS)
	if err := goose.SetDialect(string(goose.DialectSQLite3)); err != nil {
		t.Fatalf("migrateHistoryTo SetDialect: %v", err)
	}
	if err := goose.UpToContext(context.Background(), db, "migrations_history", version); err != nil {
		t.Fatalf("migrateHistoryTo UpTo %d: %v", version, err)
	}
}

// TestMigration_005_LocalDayBuckets_ResetsTheDailyTier pins the one-time
// correction an existing installation needs.
//
// Its measurements_daily rows were folded on the UTC day and cannot be
// re-cut — the hours behind them are already summed away — so the migration
// drops them and rewinds the daily watermark, which is what makes the next
// rollup rebuild the tier from the untouched hourly rows. Leaving either
// half out is silent: without the delete the stale rows stay and the
// rebuilt ones land beside them; without the rewind the fold never revisits
// the range and the tier stays empty.
//
// Not marked t.Parallel() — openMu is a package-level mutex shared with the
// other migration tests.
func TestMigration_005_LocalDayBuckets_ResetsTheDailyTier(t *testing.T) {
	ctx := context.Background()
	db := openHistoryDBAtVersion(t, "history_004.db", 4)

	// The state a pre-migration daemon leaves behind: one hourly row, one
	// UTC-bucketed daily row folded from it, and both watermarks well past
	// them — a daemon that has been running for days, which is the only
	// state in which a stale daily row can exist at all.
	hourBucket := time.Date(2026, 8, 5, 23, 0, 0, 0, time.UTC).UnixMilli()
	utcDayBucket := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC).UnixMilli()
	hourlyFrontier := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC).UnixMilli()
	seed := []struct {
		query string
		args  []any
	}{
		{
			`INSERT INTO measurements_hourly
            (central_name, interface_id, channel_address, parameter, bucket_ts,
             sum, min, max, count, first, last)
          VALUES (?, ?, ?, ?, ?, 7, 7, 7, 1, 7, 7)`,
			[]any{localDayCentral, localDayIface, localDayChannel, localDayParam, hourBucket},
		},
		{
			`INSERT INTO measurements_daily
            (central_name, interface_id, channel_address, parameter, bucket_ts,
             sum, min, max, count, first, last)
          VALUES (?, ?, ?, ?, ?, 7, 7, 7, 1, 7, 7)`,
			[]any{localDayCentral, localDayIface, localDayChannel, localDayParam, utcDayBucket},
		},
		{
			`UPDATE measurement_rollup_state SET watermark = ? WHERE tier = 'hourly'`,
			[]any{hourlyFrontier},
		},
		{
			`UPDATE measurement_rollup_state SET watermark = ? WHERE tier = 'daily'`,
			[]any{utcDayBucket + dayBucketMs},
		},
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatalf("seed pre-migration state: %v", err)
		}
	}

	migrateHistoryTo(t, db, 5)

	var dailyRows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM measurements_daily`).Scan(&dailyRows); err != nil {
		t.Fatalf("count daily rows: %v", err)
	}
	if dailyRows != 0 {
		t.Errorf("daily rows after migration = %d, want 0 — the UTC-bucketed tier survived", dailyRows)
	}
	wm, err := readWatermark(ctx, db, rollupTierDaily)
	if err != nil {
		t.Fatalf("readWatermark daily: %v", err)
	}
	if wm != 0 {
		t.Errorf("daily watermark after migration = %d, want 0 — the fold will never revisit the range", wm)
	}
	hourlyWM, err := readWatermark(ctx, db, rollupTierHourly)
	if err != nil {
		t.Fatalf("readWatermark hourly: %v", err)
	}
	if hourlyWM != hourlyFrontier {
		t.Errorf("hourly watermark = %d, want it untouched at %d", hourlyWM, hourlyFrontier)
	}

	// The rebuild: the next rollup folds the surviving hourly row into the
	// local day it belongs to — 23:00 UTC at UTC+2 is already the next day.
	loc := berlin(t)
	s := NewMeasurementStoreIn(db, loc)
	if _, err := s.RollupDaily(ctx, time.UnixMilli(hourBucket).Add(72*time.Hour)); err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}
	daily := queryDaily(t, s, localDayCentral, localDayIface, localDayChannel, localDayParam)
	if len(daily) != 1 {
		t.Fatalf("daily rows after re-fold = %d, want 1: %+v", len(daily), daily)
	}
	want := time.Date(2026, 8, 6, 0, 0, 0, 0, loc)
	if got := time.UnixMilli(daily[0].BucketTS).In(loc); !got.Equal(want) {
		t.Errorf("re-folded bucket = %s, want the local day %s", got, want)
	}
}

// midnightDSTZones are the zones whose DST jump lands on local midnight, so
// that a whole calendar day starts at 01:00 and the wall clock 00:00 never
// happens there. They are what separates a day-by-day fold that walks the
// calendar from one that walks a fixpoint.
var midnightDSTZones = []struct {
	zone string
	y    int
	m    time.Month
	d    int
}{
	{zone: "America/Santiago", y: 2025, m: time.September, d: 7},
	{zone: "America/Havana", y: 2025, m: time.March, d: 9},
	{zone: "Atlantic/Azores", y: 2025, m: time.March, d: 30},
}

// TestNextDayStartAdvancesWhenLocalMidnightDoesNotExist pins the invariant
// every day-by-day fold depends on: the next day's start is strictly later
// than this day's.
//
// time.Date normalizes a wall clock the zone never showed, and for a
// midnight transition it normalizes *backwards* onto the previous local
// day — so the naive day start of the transition day is an instant that
// belongs to the day before, and asking for the day after that instant
// lands on it again. A caller stepping with it never moves.
func TestNextDayStartAdvancesWhenLocalMidnightDoesNotExist(t *testing.T) {
	t.Parallel()
	for _, tc := range midnightDSTZones {
		t.Run(tc.zone, func(t *testing.T) {
			t.Parallel()
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Skipf("%s unavailable: %v", tc.zone, err)
			}
			s := NewMeasurementStoreIn(nil, loc)

			noon := time.Date(tc.y, tc.m, tc.d, 12, 0, 0, 0, loc)
			if midnight := time.Date(tc.y, tc.m, tc.d, 0, 0, 0, 0, loc); midnight.Day() == tc.d {
				t.Skipf("fixture stale: local midnight exists on %04d-%02d-%02d in %s",
					tc.y, tc.m, tc.d, tc.zone)
			}

			start := s.dayStartMs(noon.UnixMilli())
			// Every surface labels a bucket with the local date of its
			// start, so a start belonging to the previous day mislabels
			// the whole day and collides with that day's own bucket.
			if got := time.UnixMilli(start).In(loc); got.Year() != tc.y || got.Month() != tc.m || got.Day() != tc.d {
				t.Errorf("dayStartMs(noon) = %s, want the first instant of %04d-%02d-%02d",
					got, tc.y, tc.m, tc.d)
			}
			next := s.nextDayStartMs(start)
			if next <= start {
				t.Fatalf("nextDayStartMs(%s) did not advance — every fold loop stepping with it spins forever",
					time.UnixMilli(start).In(loc))
			}
			if got, want := time.UnixMilli(next).In(loc), noon.AddDate(0, 0, 1); got.Day() != want.Day() {
				t.Errorf("nextDayStartMs landed on %s, want the day after %04d-%02d-%02d",
					got, tc.y, tc.m, tc.d)
			}
		})
	}
}

// TestDailyFoldTerminatesAcrossAMidnightDSTTransition drives the real
// rollup over a zone whose local midnight is skipped.
//
// foldDaysIntoDailyTier steps with nextDayStartMs and clamps every day's
// range to the fold window, so a step that does not advance leaves the loop
// taking its `hi <= lo` branch forever — the rollup goroutine spins inside
// the write transaction RollupDaily opened, which then never commits and
// never releases the history database.
func TestDailyFoldTerminatesAcrossAMidnightDSTTransition(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("America/Santiago unavailable: %v", err)
	}
	s := measurementStoreIn(t, loc)

	// One sample per wall-clock hour from the day before the transition
	// through the day after it, so the fold has to walk across the
	// skipped midnight rather than stop short of it.
	from := time.Date(2025, 9, 6, 0, 0, 0, 0, loc)
	until := time.Date(2025, 9, 9, 0, 0, 0, 0, loc)
	var samples []MeasurementSample
	for ts := from; ts.Before(until); ts = ts.Add(time.Hour) {
		samples = append(samples, sampleAt(ts, 1))
	}
	if err := s.SaveBatch(context.Background(), samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	foldUpTo := until.Add(72 * time.Hour)
	if _, err := s.RollupHourly(context.Background(), foldUpTo); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}

	// The fold runs off-goroutine because the failure mode is a spin, not
	// a wrong answer: a plain call would hang the whole package run.
	done := make(chan error, 1)
	go func() {
		_, err := s.RollupDaily(context.Background(), foldUpTo)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RollupDaily: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("RollupDaily did not return — the day-by-day fold is not advancing across the skipped midnight")
	}

	daily := queryDaily(t, s, localDayCentral, localDayIface, localDayChannel, localDayParam)
	if len(daily) != 3 {
		t.Fatalf("daily rows = %d, want one per local day: %+v", len(daily), daily)
	}
	wantStarts := []time.Time{
		time.Date(2025, 9, 6, 0, 0, 0, 0, loc),
		time.Date(2025, 9, 7, 1, 0, 0, 0, loc), // the transition day begins at 01:00
		time.Date(2025, 9, 8, 0, 0, 0, 0, loc),
	}
	wantCounts := []int64{24, 23, 24}
	var total int64
	for i, row := range daily {
		if got := time.UnixMilli(row.BucketTS).In(loc); !got.Equal(wantStarts[i]) {
			t.Errorf("bucket[%d] start = %s, want %s", i, got, wantStarts[i])
		}
		if row.Count != wantCounts[i] {
			t.Errorf("bucket[%d] count = %d, want %d", i, row.Count, wantCounts[i])
		}
		total += row.Count
	}
	if want := int64(len(samples)); total != want {
		t.Errorf("total folded samples = %d, want %d", total, want)
	}
}
