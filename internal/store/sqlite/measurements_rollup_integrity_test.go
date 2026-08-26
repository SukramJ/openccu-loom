// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
	"time"
)

// TestRollupDailyNeverOutrunsTheHourlyTier pins that the daily fold stops
// at the hourly frontier.
//
// Its window is [daily watermark, cutoff) and the watermark advances
// across the whole window whether or not rows were found. A cutoff beyond
// the hourly frontier therefore wrote nothing for those days and then
// skipped them forever — while the hourly purge, floored by that same
// watermark, became free to delete the buckets that would have filled
// them. One sustained hourly-fold failure (disk full, a long SQLITE_BUSY
// stretch) turned into a permanent hole in the daily tier.
func TestRollupDailyNeverOutrunsTheHourlyTier(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "POWER"
	)
	day := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	if err := s.SaveBatch(ctx, []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day, Value: 7},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	// The hourly tier has NOT been folded — this is the state a failing
	// hourly fold leaves behind. A daily fold reaching two days ahead must
	// not consume that window.
	if _, err := s.RollupDaily(ctx, day.Add(48*time.Hour)); err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}

	// The hourly fold recovers and catches up.
	if _, err := s.RollupHourly(ctx, day.Add(48*time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	if _, err := s.RollupDaily(ctx, day.Add(48*time.Hour)); err != nil {
		t.Fatalf("RollupDaily after catch-up: %v", err)
	}

	daily := queryDaily(t, s, central, iface, ch, param)
	if len(daily) != 1 {
		t.Fatalf("daily rows = %d, want 1 — the day was skipped permanently", len(daily))
	}
	if daily[0].BucketTS != day.Truncate(24*time.Hour).UnixMilli() {
		t.Errorf("daily bucket = %d, want the day of the sample", daily[0].BucketTS)
	}
}

// TestDeleteForCentralClearsEveryTier pins that removing a central drops
// its rollups too. They outlive the raw rows by design (hourly 13 months,
// daily forever by default), so deleting only the raw table left the
// aggregates behind — and re-adopting the same central name resurfaced
// them in the energy views as if the CCU had never been away.
func TestDeleteForCentralClearsEveryTier(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu-gone"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "POWER"
	)
	day := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	if err := s.SaveBatch(ctx, []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day, Value: 3},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, day.Add(48*time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	if _, err := s.RollupDaily(ctx, day.Add(48*time.Hour)); err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}
	if got := queryHourly(t, s, central, iface, ch, param); len(got) == 0 {
		t.Fatal("setup produced no hourly rows")
	}
	if got := queryDaily(t, s, central, iface, ch, param); len(got) == 0 {
		t.Fatal("setup produced no daily rows")
	}

	if err := s.DeleteForCentral(ctx, central); err != nil {
		t.Fatalf("DeleteForCentral: %v", err)
	}

	if got := queryHourly(t, s, central, iface, ch, param); len(got) != 0 {
		t.Errorf("hourly rows survived the central removal: %+v", got)
	}
	if got := queryDaily(t, s, central, iface, ch, param); len(got) != 0 {
		t.Errorf("daily rows survived the central removal: %+v", got)
	}
}
