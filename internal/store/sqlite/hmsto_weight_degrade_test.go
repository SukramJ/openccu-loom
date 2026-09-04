// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"testing"
	"time"
)

// The degrade rule under test: merging a source row whose covered span is
// known (weightMs > 0) with one whose span is not (weightMs == 0) makes the
// merged span unknown too, so the merged bucket falls back to the legacy
// sample mean instead of time-weighting only the half whose span it knows.
// Every fold that merges buckets has to apply it — a fold that keeps the
// known half reports a mean over part of the window under a label that
// promises the whole of it, in the SPA's energy and history charts.
//
// The mixed input is what these cases exist for: an all-weighted or
// all-legacy merge produces the same answer with or without the rule.

// TestFoldTierBucketsBy_MixedSpanDegradesToSampleMean covers the calendar
// fold (day / month buckets).
func TestFoldTierBucketsBy_MixedSpanDegradesToSampleMean(t *testing.T) {
	t.Parallel()

	const hour = int64(time.Hour / time.Millisecond)
	src := []tierBucket{
		// Weighted source: 10 W held for a full hour.
		{bucketTS: 0, sum: 10, minV: 10, maxV: 10, count: 1, weightedSum: 10 * float64(hour), weightMs: hour},
		// Legacy source in the same output bucket: no covered span.
		{bucketTS: hour, sum: 30, minV: 30, maxV: 30, count: 1},
	}
	out := foldTierBucketsBy(src, func(int64) int64 { return 0 })
	if len(out) != 1 {
		t.Fatalf("folded into %d buckets, want 1", len(out))
	}
	if out[0].weightMs != 0 || out[0].weightedSum != 0 {
		t.Errorf("merged bucket kept the weighted pair (weightedSum=%v, weightMs=%d); "+
			"a mix with an unspanned source must degrade to the legacy pair",
			out[0].weightedSum, out[0].weightMs)
	}
	if got, want := effectiveAvg(out[0].sum, out[0].weightedSum, out[0].count, out[0].weightMs), 20.0; got != want {
		t.Errorf("merged avg = %v, want %v (sample mean of 10 and 30)", got, want)
	}
}

// TestFoldTierBuckets_MixedSpanDegradesToSampleMean covers the evenly spaced
// chart fold, the path QueryBuckets serves.
func TestFoldTierBuckets_MixedSpanDegradesToSampleMean(t *testing.T) {
	t.Parallel()

	const hour = int64(time.Hour / time.Millisecond)
	src := []tierBucket{
		{bucketTS: 0, sum: 10, minV: 10, maxV: 10, count: 1, weightedSum: 10 * float64(hour), weightMs: hour},
		{bucketTS: hour, sum: 30, minV: 30, maxV: 30, count: 1},
	}
	// One output bucket wide enough to hold both sources.
	out := foldTierBuckets(src, 0, 24*hour, 1)
	if len(out) != 1 {
		t.Fatalf("folded into %d buckets, want 1", len(out))
	}
	if got, want := out[0].Avg, 20.0; got != want {
		t.Errorf("merged Avg = %v, want %v (sample mean; a mix with an unspanned "+
			"source must not report the weighted mean of the spanned half)", got, want)
	}
}

// TestFoldEnergyRowsBy_MixedSpanDegradesToSampleMean covers the energy fold,
// the path QueryEnergy serves for day and month groups.
func TestFoldEnergyRowsBy_MixedSpanDegradesToSampleMean(t *testing.T) {
	t.Parallel()

	const hour = int64(time.Hour / time.Millisecond)
	rows := []EnergyRow{
		{
			ChannelAddress: "DEV:1", Parameter: "POWER", BucketTS: time.UnixMilli(0),
			Sum: 10, Min: 10, Max: 10, Count: 1,
			weightedSum: 10 * float64(hour), weightMs: hour,
		},
		{
			ChannelAddress: "DEV:1", Parameter: "POWER", BucketTS: time.UnixMilli(hour),
			Sum: 30, Min: 30, Max: 30, Count: 1,
		},
	}
	out := foldEnergyRowsBy(rows, func(int64) int64 { return 0 })
	if len(out) != 1 {
		t.Fatalf("folded into %d rows, want 1", len(out))
	}
	if out[0].weightMs != 0 || out[0].weightedSum != 0 {
		t.Errorf("merged row kept the weighted pair (weightedSum=%v, weightMs=%d); "+
			"a mix with an unspanned source must degrade to the legacy pair",
			out[0].weightedSum, out[0].weightMs)
	}
}

// TestMergeWeightedSpan_DegradeRule states the rule once, where the three
// folds now read it from.
func TestMergeWeightedSpan_DegradeRule(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		aSum    float64
		aMs     int64
		bSum    float64
		bMs     int64
		wantSum float64
		wantMs  int64
	}{
		{name: "both spanned add up", aSum: 6, aMs: 2, bSum: 9, bMs: 3, wantSum: 15, wantMs: 5},
		{name: "unspanned second degrades", aSum: 6, aMs: 2, bSum: 0, bMs: 0},
		{name: "unspanned first degrades", aSum: 0, aMs: 0, bSum: 9, bMs: 3},
		{name: "both unspanned stay legacy", aSum: 0, aMs: 0, bSum: 0, bMs: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotSum, gotMs := mergeWeightedSpan(tc.aSum, tc.aMs, tc.bSum, tc.bMs)
			if gotSum != tc.wantSum || gotMs != tc.wantMs {
				t.Errorf("mergeWeightedSpan(%v,%d,%v,%d) = (%v,%d), want (%v,%d)",
					tc.aSum, tc.aMs, tc.bSum, tc.bMs, gotSum, gotMs, tc.wantSum, tc.wantMs)
			}
		})
	}
}
