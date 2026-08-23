// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"
	"time"
)

// insertHourlyRowWeighted writes one measurements_hourly row directly,
// bypassing RollupHourly, with explicit weighted_sum/weight_ms values —
// unlike [insertHourlyRow], which leaves them at their column default (0)
// and so always simulates a pre-migration row. Used to construct a row
// whose covered span IS known, without going through a real fold.
func insertHourlyRowWeighted(
	t *testing.T, s *MeasurementStore,
	central, iface, ch, param string,
	bucketTS int64, sum, minV, maxV float64, count int64, first, last, weightedSum float64, weightMs int64,
) {
	t.Helper()
	_, err := s.db.ExecContext(context.Background(), `
        INSERT INTO measurements_hourly
            (central_name, interface_id, channel_address, parameter, bucket_ts,
             sum, min, max, count, first, last, weighted_sum, weight_ms)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `, central, iface, ch, param, bucketTS, sum, minV, maxV, count, first, last, weightedSum, weightMs)
	if err != nil {
		t.Fatalf("insertHourlyRowWeighted: %v", err)
	}
}

// TestMeasurement_QueryBuckets_WeightedRollupColumns_QueryPathIsTimeWeighted
// is reproducer 1 of the E4-numeric-units-6 rework: a single already-folded
// hour bucket holding 1 W for 59 minutes and 100 W for the final minute must
// report ~2.65 W ((59*1 + 1*100) / 60), read back through the public
// QueryBuckets path — the store's weighted_sum/weight_ms columns, not the
// legacy sum/count pair, must be what QueryBuckets actually divides.
func TestMeasurement_QueryBuckets_WeightedRollupColumns_QueryPathIsTimeWeighted(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "POWER"
	)
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	samples := []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base, Value: 1},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(59 * time.Minute), Value: 100},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}

	buckets, tier, err := s.QueryBuckets(ctx, central, iface, ch, param, base, base.Add(time.Hour), 1)
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if tier != HistoryTierHour {
		t.Fatalf("tier = %q, want %q", tier, HistoryTierHour)
	}
	if len(buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(buckets))
	}
	want := (59*1.0 + 1*100.0) / 60
	if got := buckets[0].Avg; got < want-0.01 || got > want+0.01 {
		t.Errorf("Avg = %v, want ~%v (1 W held 59 min, 100 W held 1 min)", got, want)
	}
}

// TestMeasurement_RollupHourly_CarriesInThePriorHourAcrossTicks is
// reproducer 2 of the E4-numeric-units-6 rework — the one that matters,
// because it drives RollupHourly the way internal/history/recorder.go
// really drives it: one call per tick, each folding exactly the newly
// eligible hour, never one wide fold spanning both hours in a single call.
//
// 0 W is held from 10:50, then 600 W arrives at 11:55. The 11:00 hour
// bucket's own first sample is the 600 W spike, so without a carry-in from
// the sample immediately before the fold window, that bucket has nothing to
// carry backward except its own value and reports 600 W for the whole
// hour — the exact defect this rework fixes. With the carry-in, the 11:00
// bucket holds 0 W for its first 55 minutes and 600 W for the last 5,
// averaging (55*0 + 5*600) / 60 = 50 W.
func TestMeasurement_RollupHourly_CarriesInThePriorHourAcrossTicks(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "POWER"
	)
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC) // hour-aligned
	samples := []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(50 * time.Minute), Value: 0},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(1*time.Hour + 55*time.Minute), Value: 600},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	// Tick 1: folds the 10:00 hour (the 0 W sample). watermark -> 11:00.
	if _, err := s.RollupHourly(ctx, base.Add(1*time.Hour)); err != nil {
		t.Fatalf("RollupHourly (tick 1): %v", err)
	}
	// Tick 2: folds only the 11:00 hour (the 600 W sample) — a SEPARATE
	// call from tick 1, exactly as the recorder's hourly ticker issues it.
	// The 0 W sample from tick 1's window must still be visible to this
	// fold's LAG carry-in even though it is no longer in [watermark, cutoff).
	if _, err := s.RollupHourly(ctx, base.Add(2*time.Hour)); err != nil {
		t.Fatalf("RollupHourly (tick 2): %v", err)
	}

	// Assert from the persisted row, not from a fresh wide fold: read the
	// 11:00 bucket back through the public QueryBuckets path.
	buckets, tier, err := s.QueryBuckets(ctx, central, iface, ch, param,
		base.Add(time.Hour), base.Add(2*time.Hour), 1)
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if tier != HistoryTierHour {
		t.Fatalf("tier = %q, want %q", tier, HistoryTierHour)
	}
	if len(buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(buckets))
	}
	const want = 50.0
	if got := buckets[0].Avg; got < want-0.01 || got > want+0.01 {
		t.Errorf("11:00 bucket Avg = %v, want ~%v (the 600 W spike must not read as the whole hour)", got, want)
	}
}

// TestMeasurement_QueryBuckets_LegacyRollupRowFallsBackToSampleMean is
// reproducer 3 of the E4-numeric-units-6 rework: a rollup row written
// before migrations_history/007_time_weighted_rollups.sql existed has
// weighted_sum = 0 and weight_ms = 0 (the column defaults) alongside a real
// sum/count. Reading it back must report the legacy sample mean it was
// written with, not silently read as 0 or divide by zero.
func TestMeasurement_QueryBuckets_LegacyRollupRowFallsBackToSampleMean(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "POWER"
	)
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	bucketTS := base.Truncate(time.Hour).UnixMilli()

	// A plausible legacy row: two samples summing to 40 (avg 20), with no
	// known covered span — weighted_sum/weight_ms stay at 0.
	insertHourlyRowWeighted(t, s, central, iface, ch, param, bucketTS,
		40 /* sum */, 10 /* min */, 30, /* max */
		2 /* count */, 10 /* first */, 30, /* last */
		0 /* weighted_sum */, 0 /* weight_ms */)
	advanceHourlyWatermark(t, s, bucketTS+hourBucketMs)

	buckets, tier, err := s.QueryBuckets(ctx, central, iface, ch, param,
		base.Truncate(time.Hour), base.Truncate(time.Hour).Add(time.Hour), 1)
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if tier != HistoryTierHour {
		t.Fatalf("tier = %q, want %q", tier, HistoryTierHour)
	}
	if len(buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(buckets))
	}
	if buckets[0].Avg != 20 || buckets[0].Count != 2 {
		t.Errorf("bucket = %+v, want Avg=20 Count=2 (the legacy sample mean, not 0 or a divide-by-zero)", buckets[0])
	}
}
