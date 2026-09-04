// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
	"time"
)

// The fourth statement of the degrade rule is the SQL one: RollupDaily folds
// inside the database, so [mergeWeightedSpan] never runs for it and the rule
// is restated as `CASE WHEN MIN(weight_ms) OVER w = 0 THEN 0 ELSE SUM(...) END`
// in [rollupDailySelectSQL]. The Go folds are covered by
// hmsto_weight_degrade_test.go; this case covers the SQL one, over the only
// input that separates the two possible behaviours — a day that mixes an
// hourly row whose covered span is known with one whose span is not.
//
// Without the CASE the day would report the time-weighted mean of the hour it
// knows the span of (10 W) while labelling it as the whole day's average, in
// the SPA's energy and history charts.
func TestW2StoRollupDaily_MixedSpanDegradesToSampleMean(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "POWER"
	)
	day := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	// Hour 1: a row written by the weighted rollup — 10 W held for the whole
	// hour, so its covered span is known.
	insertHourlyRowWeighted(t, s, central, iface, ch, param,
		day.Add(1*time.Hour).UnixMilli(),
		10 /* sum */, 10 /* min */, 10 /* max */, 1 /* count */, 10 /* first */, 10, /* last */
		10*float64(hourBucketMs) /* weighted_sum */, hourBucketMs /* weight_ms */)
	// Hour 3: a row predating migrations_history/007_time_weighted_rollups.sql
	// — a real sum/count, no covered span (the column defaults).
	insertHourlyRowWeighted(t, s, central, iface, ch, param,
		day.Add(3*time.Hour).UnixMilli(),
		30 /* sum */, 30 /* min */, 30 /* max */, 1 /* count */, 30 /* first */, 30, /* last */
		0 /* weighted_sum */, 0 /* weight_ms */)
	// The daily fold never outruns the hourly tier, so its frontier has to
	// clear the day before the fold can consume these two rows.
	advanceHourlyWatermark(t, s, day.Add(24*time.Hour).UnixMilli())

	folded, err := s.RollupDaily(ctx, day.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}
	if folded != 2 {
		t.Fatalf("RollupDaily folded = %d, want 2", folded)
	}

	daily := queryDaily(t, s, central, iface, ch, param)
	if len(daily) != 1 {
		t.Fatalf("daily rows = %d, want 1", len(daily))
	}
	d := daily[0]
	if d.WeightedSum != 0 || d.WeightMs != 0 {
		t.Errorf("daily row kept the weighted pair (weighted_sum=%v, weight_ms=%d); "+
			"a day mixing a spanned with an unspanned hourly row must degrade to the "+
			"legacy pair, the same rule [mergeWeightedSpan] applies in Go",
			d.WeightedSum, d.WeightMs)
	}
	// sum/count stay additive, so the degraded row still reads back as the
	// legacy sample mean rather than as 0 or a divide-by-zero.
	if got, want := effectiveAvg(d.Sum, d.WeightedSum, d.Count, d.WeightMs), 20.0; got != want {
		t.Errorf("daily avg = %v, want %v (sample mean of 10 and 30; the weighted mean "+
			"of the spanned half alone would be 10)", got, want)
	}
}
