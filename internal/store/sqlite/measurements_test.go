// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// openHistoryDB opens (and migrates) a fresh file-backed history SQLite
// database in t's temp directory and registers a cleanup to close it.
func openHistoryDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), name) + "?_pragma=journal_mode(WAL)"
	openMu.Lock()
	db, err := OpenHistory(context.Background(), dsn)
	openMu.Unlock()
	if err != nil {
		t.Fatalf("OpenHistory %s: %v", name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// freshMeasurementStore opens a fresh history DB and returns a ready store.
func freshMeasurementStore(t *testing.T) *MeasurementStore {
	t.Helper()
	return NewMeasurementStore(openHistoryDB(t, "hist.db"))
}

// msTime returns a time.Time truncated to millisecond precision to match
// the UnixMilli storage round-trip.
func msTime(t time.Time) time.Time {
	return time.UnixMilli(t.UnixMilli())
}

// TestMeasurement_OpenHistory_CreatesMigratedTable verifies that OpenHistory
// successfully opens the history DB, runs migrations, and that the
// measurements table is usable via a SaveBatch + Stats roundtrip.
func TestMeasurement_OpenHistory_CreatesMigratedTable(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	samples := []MeasurementSample{
		{CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "DEV:1", Parameter: "TEMP", TS: msTime(time.Now()), Value: 21.5},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Rows != 1 {
		t.Errorf("Stats.Rows = %d, want 1", st.Rows)
	}
}

// TestMeasurement_SaveBatch_CountersReflected verifies that SaveBatch
// increments Stats().Rows and MetricsSnapshot().RowsWritten and .Batches.
func TestMeasurement_SaveBatch_CountersReflected(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()
	base := msTime(time.Now())

	batch1 := []MeasurementSample{
		{CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "CH:1", Parameter: "P", TS: base, Value: 1},
		{CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "CH:1", Parameter: "P", TS: msTime(base.Add(time.Second)), Value: 2},
	}
	if err := s.SaveBatch(ctx, batch1); err != nil {
		t.Fatalf("SaveBatch batch1: %v", err)
	}

	batch2 := []MeasurementSample{
		{CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "CH:1", Parameter: "P", TS: msTime(base.Add(2 * time.Second)), Value: 3},
	}
	if err := s.SaveBatch(ctx, batch2); err != nil {
		t.Fatalf("SaveBatch batch2: %v", err)
	}

	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Rows != 3 {
		t.Errorf("Stats.Rows = %d, want 3", st.Rows)
	}

	m := s.MetricsSnapshot()
	if m.RowsWritten != 3 {
		t.Errorf("MetricsSnapshot.RowsWritten = %d, want 3", m.RowsWritten)
	}
	if m.Batches != 2 {
		t.Errorf("MetricsSnapshot.Batches = %d, want 2", m.Batches)
	}
}

// TestMeasurement_QueryBuckets_AggregatesCorrectly verifies that QueryBuckets
// returns buckets with correct TS, Avg, Min, Max, and Count when multiple
// samples land in the same bucket and when samples are spread across buckets.
func TestMeasurement_QueryBuckets_AggregatesCorrectly(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	// Use a clean epoch-aligned window for determinism.
	from := time.UnixMilli(0)
	to := time.UnixMilli(4000) // 4 000 ms window

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "SENSOR:1"
		param   = "TEMP"
	)

	// Insert samples:
	// ts=0 ms → bucket 0 (width=1000ms): value 10
	// ts=500 ms → bucket 0: value 20   → avg=15, min=10, max=20, count=2
	// ts=1000 ms → bucket 1: value 30  → avg=30, min=30, max=30, count=1
	// ts=3500 ms → bucket 3: value 40  → avg=40, min=40, max=40, count=1
	samples := []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: time.UnixMilli(0), Value: 10},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: time.UnixMilli(500), Value: 20},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: time.UnixMilli(1000), Value: 30},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: time.UnixMilli(3500), Value: 40},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	buckets, _, err := s.QueryBuckets(ctx, central, iface, ch, param, from, to, 4)
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}

	// Expect 3 non-empty buckets (bucket indices 0, 1, 3).
	if len(buckets) != 3 {
		t.Fatalf("QueryBuckets: got %d buckets, want 3", len(buckets))
	}

	type wantBucket struct {
		tsMS  int64
		avg   float64
		min   float64
		max   float64
		count int64
	}
	want := []wantBucket{
		{tsMS: 0, avg: 15, min: 10, max: 20, count: 2},
		{tsMS: 1000, avg: 30, min: 30, max: 30, count: 1},
		{tsMS: 3000, avg: 40, min: 40, max: 40, count: 1},
	}

	for i, w := range want {
		b := buckets[i]
		if b.TS.UnixMilli() != w.tsMS {
			t.Errorf("bucket[%d].TS = %d ms, want %d ms", i, b.TS.UnixMilli(), w.tsMS)
		}
		if b.Avg != w.avg {
			t.Errorf("bucket[%d].Avg = %v, want %v", i, b.Avg, w.avg)
		}
		if b.Min != w.min {
			t.Errorf("bucket[%d].Min = %v, want %v", i, b.Min, w.min)
		}
		if b.Max != w.max {
			t.Errorf("bucket[%d].Max = %v, want %v", i, b.Max, w.max)
		}
		if b.Count != w.count {
			t.Errorf("bucket[%d].Count = %d, want %d", i, b.Count, w.count)
		}
	}
}

// TestMeasurement_QueryBuckets_ErrorPaths verifies that QueryBuckets returns
// errors for invalid arguments: buckets<=0 and to<=from.
func TestMeasurement_QueryBuckets_ErrorPaths(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	now := time.Now()
	later := now.Add(time.Hour)

	_, _, err := s.QueryBuckets(ctx, "ccu1", "HmIP-RF", "CH:1", "P", now, later, 0)
	if err == nil {
		t.Error("QueryBuckets(buckets=0): expected error, got nil")
	}

	_, _, err = s.QueryBuckets(ctx, "ccu1", "HmIP-RF", "CH:1", "P", now, later, -1)
	if err == nil {
		t.Error("QueryBuckets(buckets=-1): expected error, got nil")
	}

	// to == from
	_, _, err = s.QueryBuckets(ctx, "ccu1", "HmIP-RF", "CH:1", "P", now, now, 10)
	if err == nil {
		t.Error("QueryBuckets(to==from): expected error, got nil")
	}

	// to before from
	_, _, err = s.QueryBuckets(ctx, "ccu1", "HmIP-RF", "CH:1", "P", later, now, 10)
	if err == nil {
		t.Error("QueryBuckets(to<from): expected error, got nil")
	}
}

// TestMeasurement_QueryBuckets_Isolation verifies that a second data point
// with the same channel address but a different parameter does not appear in
// results for the first parameter, and that a second central does not bleed
// into the first.
func TestMeasurement_QueryBuckets_Isolation(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	from := time.UnixMilli(0)
	to := time.UnixMilli(2000)
	ts := time.UnixMilli(500)

	// Primary DP under test.
	primary := MeasurementSample{
		CentralName: "ccu1", InterfaceID: "HmIP-RF",
		ChannelAddress: "SHARED:1", Parameter: "TEMP",
		TS: ts, Value: 100,
	}
	// Same channel, different parameter — must not pollute TEMP query.
	otherParam := MeasurementSample{
		CentralName: "ccu1", InterfaceID: "HmIP-RF",
		ChannelAddress: "SHARED:1", Parameter: "HUMIDITY",
		TS: ts, Value: 999,
	}
	// Different central, same everything else — must not pollute ccu1 query.
	otherCentral := MeasurementSample{
		CentralName: "ccu2", InterfaceID: "HmIP-RF",
		ChannelAddress: "SHARED:1", Parameter: "TEMP",
		TS: ts, Value: 888,
	}

	if err := s.SaveBatch(ctx, []MeasurementSample{primary, otherParam, otherCentral}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	buckets, _, err := s.QueryBuckets(ctx, "ccu1", "HmIP-RF", "SHARED:1", "TEMP", from, to, 2)
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if len(buckets) != 1 {
		t.Fatalf("QueryBuckets: got %d buckets, want 1", len(buckets))
	}
	if buckets[0].Avg != 100 {
		t.Errorf("QueryBuckets: Avg = %v, want 100 (other DPs bled in)", buckets[0].Avg)
	}
	if buckets[0].Count != 1 {
		t.Errorf("QueryBuckets: Count = %d, want 1 (other DPs bled in)", buckets[0].Count)
	}
}

// TestMeasurement_DeleteOlderThan_RemovesOnlyOldRows verifies that
// DeleteOlderThan removes rows before the cutoff, keeps rows after it,
// returns the correct removed count, and bumps MetricsSnapshot().RetentionDeleted.
// The old row must first be folded into the hourly tier: the purge floors
// its cutoff by the hourly watermark, so a fold has to run before the row
// is eligible for deletion (the never-purge-before-fold guard).
func TestMeasurement_DeleteOlderThan_RemovesOnlyOldRows(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	old := base                      // hour bucket [10:00, 11:00)
	fresh := base.Add(2 * time.Hour) // hour bucket [12:00, 13:00)

	samples := []MeasurementSample{
		{CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "D:1", Parameter: "P", TS: old, Value: 1},
		{CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "D:1", Parameter: "P", TS: fresh, Value: 2},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	// Fold the old row so it is below the hourly watermark and thus safe to
	// purge; the fresh row stays un-folded and un-purgeable.
	if _, err := s.RollupHourly(ctx, base.Add(90*time.Minute)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}

	n, err := s.DeleteOlderThan(ctx, base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteOlderThan: removed %d rows, want 1", n)
	}

	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Rows != 1 {
		t.Errorf("Stats.Rows = %d, want 1 (fresh row should remain)", st.Rows)
	}

	m := s.MetricsSnapshot()
	if m.RetentionDeleted != 1 {
		t.Errorf("MetricsSnapshot.RetentionDeleted = %d, want 1", m.RetentionDeleted)
	}
}

// TestMeasurement_DeleteDevice_PrefixSafety verifies that DeleteDevice
// removes rows for the exact device ("ABC123" and "ABC123:1") but not rows
// for the longer address "ABC1234:1".
func TestMeasurement_DeleteDevice_PrefixSafety(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	ts := time.UnixMilli(1000)
	const (
		central = "ccu1"
		iface   = "HmIP-RF"
	)

	samples := []MeasurementSample{
		// Should be deleted: bare device address.
		{CentralName: central, InterfaceID: iface, ChannelAddress: "ABC123", Parameter: "P", TS: ts, Value: 1},
		// Should be deleted: channel belonging to "ABC123".
		{CentralName: central, InterfaceID: iface, ChannelAddress: "ABC123:1", Parameter: "P", TS: ts, Value: 2},
		// Must NOT be deleted: different (longer) device address.
		{CentralName: central, InterfaceID: iface, ChannelAddress: "ABC1234:1", Parameter: "P", TS: ts, Value: 3},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	if err := s.DeleteDevice(ctx, central, iface, "ABC123"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}

	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	// Only "ABC1234:1" must remain.
	if st.Rows != 1 {
		t.Errorf("Stats.Rows = %d after DeleteDevice, want 1 (only ABC1234:1 must survive)", st.Rows)
	}

	// Confirm the survivor is ABC1234:1 by querying it.
	buckets, _, err := s.QueryBuckets(ctx, central, iface, "ABC1234:1", "P",
		time.UnixMilli(0), time.UnixMilli(2000), 1)
	if err != nil {
		t.Fatalf("QueryBuckets ABC1234:1: %v", err)
	}
	if len(buckets) != 1 || buckets[0].Count != 1 {
		t.Errorf("ABC1234:1 was incorrectly removed by DeleteDevice(\"ABC123\")")
	}
}

// TestMeasurement_DeleteForCentral_RemovesOnlyThatCentral verifies that
// DeleteForCentral removes every row for the named central across all its
// interfaces/devices while leaving another central's history untouched.
func TestMeasurement_DeleteForCentral_RemovesOnlyThatCentral(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()
	ts := time.UnixMilli(1000)

	samples := []MeasurementSample{
		{CentralName: "central-a", InterfaceID: "HmIP-RF", ChannelAddress: "DEV:1", Parameter: "TEMP", TS: ts, Value: 1},
		{CentralName: "central-a", InterfaceID: "BidCos-RF", ChannelAddress: "DEV:2", Parameter: "HUMIDITY", TS: ts, Value: 2},
		{CentralName: "central-b", InterfaceID: "HmIP-RF", ChannelAddress: "DEV:1", Parameter: "TEMP", TS: ts, Value: 3},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	if err := s.DeleteForCentral(ctx, "central-a"); err != nil {
		t.Fatalf("DeleteForCentral: %v", err)
	}

	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Rows != 1 {
		t.Errorf("Stats.Rows = %d after DeleteForCentral(central-a), want 1 (only central-b must survive)", st.Rows)
	}

	buckets, _, err := s.QueryBuckets(ctx, "central-b", "HmIP-RF", "DEV:1", "TEMP",
		time.UnixMilli(0), time.UnixMilli(2000), 1)
	if err != nil {
		t.Fatalf("QueryBuckets central-b: %v", err)
	}
	if len(buckets) != 1 || buckets[0].Count != 1 {
		t.Errorf("central-b history was incorrectly removed by DeleteForCentral(central-a)")
	}

	buckets, _, err = s.QueryBuckets(ctx, "central-a", "HmIP-RF", "DEV:1", "TEMP",
		time.UnixMilli(0), time.UnixMilli(2000), 1)
	if err != nil {
		t.Fatalf("QueryBuckets central-a: %v", err)
	}
	if len(buckets) != 0 {
		t.Errorf("central-a history survived DeleteForCentral(central-a): %v", buckets)
	}
}

// TestMeasurement_DeleteForCentral_NoRowsForCentral verifies that calling
// DeleteForCentral for a central with no recorded rows is a harmless no-op
// that leaves other centrals' history untouched.
func TestMeasurement_DeleteForCentral_NoRowsForCentral(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()
	ts := time.UnixMilli(1000)

	if err := s.SaveBatch(ctx, []MeasurementSample{
		{CentralName: "central-b", InterfaceID: "HmIP-RF", ChannelAddress: "DEV:1", Parameter: "TEMP", TS: ts, Value: 1},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	if err := s.DeleteForCentral(ctx, "central-never-seen"); err != nil {
		t.Fatalf("DeleteForCentral on absent central: %v", err)
	}

	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Rows != 1 {
		t.Errorf("Stats.Rows = %d after no-op DeleteForCentral, want 1 (central-b must survive)", st.Rows)
	}
}

// TestMeasurement_DeleteAll_EmptiesTable verifies that DeleteAll removes every
// row and Stats().Rows returns zero afterwards.
func TestMeasurement_DeleteAll_EmptiesTable(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()
	base := time.UnixMilli(0)

	samples := make([]MeasurementSample, 5)
	for i := range samples {
		samples[i] = MeasurementSample{
			CentralName: "ccu1", InterfaceID: "HmIP-RF",
			ChannelAddress: "D:1", Parameter: "P",
			TS:    time.UnixMilli(int64(i) * 1000),
			Value: float64(i),
		}
	}
	_ = base
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	if err := s.DeleteAll(ctx); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}

	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats after DeleteAll: %v", err)
	}
	if st.Rows != 0 {
		t.Errorf("Stats.Rows = %d after DeleteAll, want 0", st.Rows)
	}
}

// TestMeasurement_LastWriteWins_SameMillisecond verifies that two SaveBatch
// calls for the same (central, iface, channel, param, ts) result in exactly
// one row whose value comes from the later write.
func TestMeasurement_LastWriteWins_SameMillisecond(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	ts := time.UnixMilli(42000)
	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "COLLISION:1"
		param   = "VAL"
	)

	first := MeasurementSample{
		CentralName: central, InterfaceID: iface,
		ChannelAddress: ch, Parameter: param,
		TS: ts, Value: 10,
	}
	second := MeasurementSample{
		CentralName: central, InterfaceID: iface,
		ChannelAddress: ch, Parameter: param,
		TS: ts, Value: 99, // same ts, different value → must win
	}

	if err := s.SaveBatch(ctx, []MeasurementSample{first}); err != nil {
		t.Fatalf("SaveBatch first: %v", err)
	}
	if err := s.SaveBatch(ctx, []MeasurementSample{second}); err != nil {
		t.Fatalf("SaveBatch second: %v", err)
	}

	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Rows != 1 {
		t.Errorf("Stats.Rows = %d, want 1 (last-write-wins collision)", st.Rows)
	}

	buckets, _, err := s.QueryBuckets(ctx, central, iface, ch, param,
		time.UnixMilli(0), time.UnixMilli(100000), 1)
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if len(buckets) != 1 {
		t.Fatalf("QueryBuckets: got %d buckets, want 1", len(buckets))
	}
	if buckets[0].Avg != 99 {
		t.Errorf("QueryBuckets.Avg = %v, want 99 (second write must win)", buckets[0].Avg)
	}
}

// TestMeasurement_MultiCCU_Isolation verifies that the same
// (interface, channel, parameter) written under two different central names
// produces independent rows and QueryBuckets returns only the matching central's data.
func TestMeasurement_MultiCCU_Isolation(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	ts := time.UnixMilli(5000)
	const (
		iface = "HmIP-RF"
		ch    = "SHARED:1"
		param = "POWER"
	)

	samples := []MeasurementSample{
		{CentralName: "ccu1", InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: ts, Value: 111},
		{CentralName: "ccu2", InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: ts, Value: 222},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Rows != 2 {
		t.Errorf("Stats.Rows = %d, want 2 (one per central)", st.Rows)
	}

	query := func(central string) []MeasurementBucket {
		t.Helper()
		b, _, qErr := s.QueryBuckets(ctx, central, iface, ch, param,
			time.UnixMilli(0), time.UnixMilli(10000), 1)
		if qErr != nil {
			t.Fatalf("QueryBuckets %s: %v", central, qErr)
		}
		return b
	}

	b1 := query("ccu1")
	b2 := query("ccu2")

	if len(b1) != 1 || b1[0].Avg != 111 {
		t.Errorf("ccu1 query: got %v, want [{Avg:111}]", b1)
	}
	if len(b2) != 1 || b2[0].Avg != 222 {
		t.Errorf("ccu2 query: got %v, want [{Avg:222}]", b2)
	}
}

// TestMeasurement_NilStore_NoOps verifies that every public method on a nil
// *MeasurementStore is a safe no-op: no panic and no error returned.
func TestMeasurement_NilStore_NoOps(t *testing.T) {
	t.Parallel()
	var s *MeasurementStore
	ctx := context.Background()
	now := time.Now()

	if err := s.SaveBatch(ctx, []MeasurementSample{{
		CentralName: "c", InterfaceID: "i", ChannelAddress: "d:1", Parameter: "P",
		TS: now, Value: 1,
	}}); err != nil {
		t.Errorf("nil SaveBatch: %v", err)
	}

	got, _, err := s.QueryBuckets(ctx, "c", "i", "d:1", "P", now, now.Add(time.Second), 1)
	if err != nil {
		t.Errorf("nil QueryBuckets: %v", err)
	}
	if got != nil {
		t.Error("nil QueryBuckets: want nil slice")
	}

	n, err := s.DeleteOlderThan(ctx, now)
	if err != nil {
		t.Errorf("nil DeleteOlderThan: %v", err)
	}
	if n != 0 {
		t.Errorf("nil DeleteOlderThan: got %d, want 0", n)
	}

	if err := s.DeleteDevice(ctx, "c", "i", "dev"); err != nil {
		t.Errorf("nil DeleteDevice: %v", err)
	}

	if err := s.DeleteForCentral(ctx, "c"); err != nil {
		t.Errorf("nil DeleteForCentral: %v", err)
	}

	if err := s.DeleteAll(ctx); err != nil {
		t.Errorf("nil DeleteAll: %v", err)
	}

	if _, err := s.Stats(ctx); err != nil {
		t.Errorf("nil Stats: %v", err)
	}

	if n, err := s.RollupHourly(ctx, now); err != nil || n != 0 {
		t.Errorf("nil RollupHourly: n=%d err=%v", n, err)
	}
	if n, err := s.RollupDaily(ctx, now); err != nil || n != 0 {
		t.Errorf("nil RollupDaily: n=%d err=%v", n, err)
	}
	if n, err := s.DeleteHourlyOlderThan(ctx, now); err != nil || n != 0 {
		t.Errorf("nil DeleteHourlyOlderThan: n=%d err=%v", n, err)
	}
	if n, err := s.DeleteDailyOlderThan(ctx, now); err != nil || n != 0 {
		t.Errorf("nil DeleteDailyOlderThan: n=%d err=%v", n, err)
	}

	m := s.MetricsSnapshot()
	if m.RowsWritten != 0 || m.Batches != 0 || m.RetentionDeleted != 0 {
		t.Errorf("nil MetricsSnapshot: got %+v, want zero", m)
	}
}

// TestMeasurement_SaveBatch_Empty_IsNoOp verifies that calling SaveBatch with
// an empty slice neither errors nor increments any counter.
func TestMeasurement_SaveBatch_Empty_IsNoOp(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	if err := s.SaveBatch(ctx, nil); err != nil {
		t.Errorf("SaveBatch(nil): %v", err)
	}
	if err := s.SaveBatch(ctx, []MeasurementSample{}); err != nil {
		t.Errorf("SaveBatch([]): %v", err)
	}

	m := s.MetricsSnapshot()
	if m.Batches != 0 || m.RowsWritten != 0 {
		t.Errorf("empty SaveBatch bumped counters: %+v", m)
	}
}

// rollupRow is a scanned measurements_hourly / measurements_daily row, used
// only by tests to assert on the rollup tiers directly (the store has no
// public reader for them yet — that lands with the energy query endpoint).
type rollupRow struct {
	BucketTS      int64
	Sum, Min, Max float64
	Count         int64
	First, Last   float64
}

// queryHourly returns every measurements_hourly row for the given key,
// ordered by bucket_ts.
func queryHourly(t *testing.T, s *MeasurementStore, central, iface, ch, param string) []rollupRow {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), `
        SELECT bucket_ts, sum, min, max, count, first, last
          FROM measurements_hourly
         WHERE central_name = ? AND interface_id = ? AND channel_address = ? AND parameter = ?
         ORDER BY bucket_ts`, central, iface, ch, param)
	if err != nil {
		t.Fatalf("queryHourly: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []rollupRow
	for rows.Next() {
		var r rollupRow
		if err := rows.Scan(&r.BucketTS, &r.Sum, &r.Min, &r.Max, &r.Count, &r.First, &r.Last); err != nil {
			t.Fatalf("queryHourly scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// queryDaily returns every measurements_daily row for the given key,
// ordered by bucket_ts.
func queryDaily(t *testing.T, s *MeasurementStore, central, iface, ch, param string) []rollupRow {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), `
        SELECT bucket_ts, sum, min, max, count, first, last
          FROM measurements_daily
         WHERE central_name = ? AND interface_id = ? AND channel_address = ? AND parameter = ?
         ORDER BY bucket_ts`, central, iface, ch, param)
	if err != nil {
		t.Fatalf("queryDaily: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []rollupRow
	for rows.Next() {
		var r rollupRow
		if err := rows.Scan(&r.BucketTS, &r.Sum, &r.Min, &r.Max, &r.Count, &r.First, &r.Last); err != nil {
			t.Fatalf("queryDaily scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// insertHourlyRow writes one measurements_hourly row directly, bypassing
// RollupHourly. Used to simulate a series whose folded history exists but
// that never had a raw row recorded through this store — e.g. imported
// history, or a series whose raw retention window has always excluded it.
func insertHourlyRow(
	t *testing.T, s *MeasurementStore,
	central, iface, ch, param string,
	bucketTS int64, sum, minV, maxV float64, count int64, first, last float64,
) {
	t.Helper()
	_, err := s.db.ExecContext(context.Background(), `
        INSERT INTO measurements_hourly
            (central_name, interface_id, channel_address, parameter, bucket_ts,
             sum, min, max, count, first, last)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `, central, iface, ch, param, bucketTS, sum, minV, maxV, count, first, last)
	if err != nil {
		t.Fatalf("insertHourlyRow: %v", err)
	}
}

// advanceHourlyWatermark moves the hourly rollup watermark forward directly,
// without going through RollupHourly, so a manually-inserted hourly row (see
// insertHourlyRow) becomes visible to assembleTierBuckets.
func advanceHourlyWatermark(t *testing.T, s *MeasurementStore, wm int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("advanceHourlyWatermark begin: %v", err)
	}
	if err := advanceWatermark(ctx, tx, rollupTierHourly, wm); err != nil {
		t.Fatalf("advanceHourlyWatermark: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("advanceHourlyWatermark commit: %v", err)
	}
}

// TestMeasurement_RollupHourly_AggregatesCorrectly verifies that
// RollupHourly folds three raw rows in the same hour into one
// measurements_hourly row with exact sum/min/max/count and first/last set
// to the value observed at the earliest/latest ts in the bucket.
func TestMeasurement_RollupHourly_AggregatesCorrectly(t *testing.T) {
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
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base, Value: 10},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(20 * time.Minute), Value: 5},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(50 * time.Minute), Value: 30},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	folded, err := s.RollupHourly(ctx, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	if folded != 3 {
		t.Errorf("RollupHourly folded = %d, want 3", folded)
	}

	rows := queryHourly(t, s, central, iface, ch, param)
	if len(rows) != 1 {
		t.Fatalf("hourly rows = %d, want 1", len(rows))
	}
	r := rows[0]
	wantBucket := base.Truncate(time.Hour).UnixMilli()
	if r.BucketTS != wantBucket {
		t.Errorf("bucket_ts = %d, want %d", r.BucketTS, wantBucket)
	}
	if r.Sum != 45 || r.Min != 5 || r.Max != 30 || r.Count != 3 {
		t.Errorf("sum/min/max/count = %v/%v/%v/%v, want 45/5/30/3", r.Sum, r.Min, r.Max, r.Count)
	}
	if r.First != 10 {
		t.Errorf("first = %v, want 10 (value at earliest ts)", r.First)
	}
	if r.Last != 30 {
		t.Errorf("last = %v, want 30 (value at latest ts)", r.Last)
	}
}

// TestMeasurement_RollupHourly_Idempotent verifies that re-running
// RollupHourly against the same cutoff is a no-op: the first run folds the
// eligible rows and advances the watermark to the cutoff, so a second run
// with the same cutoff folds zero rows (proving the fold is bounded to the
// newly-eligible window and never re-scans a finalized bucket) and leaves
// the one hourly row unchanged.
func TestMeasurement_RollupHourly_Idempotent(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "ENERGY_COUNTER"
	)
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	samples := []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base, Value: 100},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(30 * time.Minute), Value: 150},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	cutoff := base.Add(time.Hour)
	n1, err := s.RollupHourly(ctx, cutoff)
	if err != nil {
		t.Fatalf("RollupHourly (1st): %v", err)
	}
	if n1 != 2 {
		t.Errorf("1st run folded = %d, want 2", n1)
	}
	first := queryHourly(t, s, central, iface, ch, param)
	if len(first) != 1 {
		t.Fatalf("hourly rows after 1st run = %d, want 1", len(first))
	}

	n2, err := s.RollupHourly(ctx, cutoff)
	if err != nil {
		t.Fatalf("RollupHourly (2nd): %v", err)
	}
	if n2 != 0 {
		t.Errorf("2nd run folded = %d, want 0 (watermark past cutoff, no re-scan)", n2)
	}
	second := queryHourly(t, s, central, iface, ch, param)
	if len(second) != 1 {
		t.Fatalf("hourly rows after 2nd run = %d, want 1 (idempotent)", len(second))
	}
	if first[0] != second[0] {
		t.Errorf("hourly row changed across re-runs: %+v then %+v", first[0], second[0])
	}
}

// TestMeasurement_Rollup_MultiCCU_Isolation verifies that two centrals
// writing the same (interface, channel, parameter) within the same hour
// bucket produce two independent hourly rows, never merged.
func TestMeasurement_Rollup_MultiCCU_Isolation(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		iface = "HmIP-RF"
		ch    = "SHARED:1"
		param = "POWER"
	)
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	samples := []MeasurementSample{
		{CentralName: "ccu1", InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base, Value: 10},
		{CentralName: "ccu2", InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base, Value: 999},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}

	r1 := queryHourly(t, s, "ccu1", iface, ch, param)
	r2 := queryHourly(t, s, "ccu2", iface, ch, param)
	if len(r1) != 1 || r1[0].Sum != 10 {
		t.Errorf("ccu1 hourly = %+v, want one row with sum=10", r1)
	}
	if len(r2) != 1 || r2[0].Sum != 999 {
		t.Errorf("ccu2 hourly = %+v, want one row with sum=999", r2)
	}
}

// TestMeasurement_RollupDaily_ReAggregatesHourlyRows verifies that
// RollupDaily folds two hourly buckets from the same UTC day into one
// daily row: sum/count are additive, min/max fold across buckets, first
// comes from the earliest hourly bucket and last from the latest.
func TestMeasurement_RollupDaily_ReAggregatesHourlyRows(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "ENERGY_COUNTER"
	)
	day := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	// Two raw samples an hour apart so they fold into two distinct hourly
	// buckets on the same UTC day.
	samples := []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day.Add(1 * time.Hour), Value: 100},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day.Add(3 * time.Hour), Value: 400},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, day.Add(24*time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	hourly := queryHourly(t, s, central, iface, ch, param)
	if len(hourly) != 2 {
		t.Fatalf("hourly rows = %d, want 2", len(hourly))
	}

	folded, err := s.RollupDaily(ctx, day.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}
	if folded != 2 {
		t.Errorf("RollupDaily folded = %d, want 2", folded)
	}

	daily := queryDaily(t, s, central, iface, ch, param)
	if len(daily) != 1 {
		t.Fatalf("daily rows = %d, want 1", len(daily))
	}
	d := daily[0]
	if d.BucketTS != day.UnixMilli() {
		t.Errorf("daily bucket_ts = %d, want %d", d.BucketTS, day.UnixMilli())
	}
	if d.Sum != 500 || d.Count != 2 || d.Min != 100 || d.Max != 400 {
		t.Errorf("sum/count/min/max = %v/%v/%v/%v, want 500/2/100/400", d.Sum, d.Count, d.Min, d.Max)
	}
	if d.First != 100 {
		t.Errorf("first = %v, want 100 (earliest hourly bucket)", d.First)
	}
	if d.Last != 400 {
		t.Errorf("last = %v, want 400 (latest hourly bucket)", d.Last)
	}
}

// TestMeasurement_DeleteHourlyOlderThan_RespectsCutoff verifies that
// DeleteHourlyOlderThan removes only measurements_hourly rows whose
// bucket_ts is before cutoff, leaves newer hourly rows and the daily tier
// untouched.
func TestMeasurement_DeleteHourlyOlderThan_RespectsCutoff(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "POWER"
	)
	old := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	if err := s.SaveBatch(ctx, []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: old, Value: 1},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: recent, Value: 2},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, recent.Add(time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	if _, err := s.RollupDaily(ctx, recent.Add(24*time.Hour)); err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}

	cutoff := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	n, err := s.DeleteHourlyOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteHourlyOlderThan: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted = %d, want 1 (only the old bucket)", n)
	}

	remaining := queryHourly(t, s, central, iface, ch, param)
	if len(remaining) != 1 || remaining[0].BucketTS != recent.Truncate(time.Hour).UnixMilli() {
		t.Errorf("remaining hourly rows = %+v, want only the recent bucket", remaining)
	}
	daily := queryDaily(t, s, central, iface, ch, param)
	if len(daily) != 2 {
		t.Errorf("daily rows = %d, want 2 (daily tier untouched by hourly delete)", len(daily))
	}
}

// TestMeasurement_DeleteDailyOlderThan_RespectsCutoff verifies that
// DeleteDailyOlderThan removes only measurements_daily rows whose
// bucket_ts is before cutoff.
func TestMeasurement_DeleteDailyOlderThan_RespectsCutoff(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "POWER"
	)
	oldDay := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	recentDay := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	if err := s.SaveBatch(ctx, []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: oldDay, Value: 1},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: recentDay, Value: 2},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, recentDay.Add(time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	if _, err := s.RollupDaily(ctx, recentDay.Add(24*time.Hour)); err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}

	cutoff := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	n, err := s.DeleteDailyOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteDailyOlderThan: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted = %d, want 1 (only the old day)", n)
	}

	remaining := queryDaily(t, s, central, iface, ch, param)
	if len(remaining) != 1 || remaining[0].BucketTS != recentDay.Truncate(24*time.Hour).UnixMilli() {
		t.Errorf("remaining daily rows = %+v, want only the recent day", remaining)
	}
	hourly := queryHourly(t, s, central, iface, ch, param)
	if len(hourly) != 2 {
		t.Errorf("hourly rows = %d, want 2 (hourly tier untouched by daily delete)", len(hourly))
	}
}

// Ensure errors package is used (satisfies golangci-lint if errors.New is referenced).
var _ = errors.New

// energyRowFor returns the single row matching (channel, parameter) from
// rows, or fails the test if there isn't exactly one.
func energyRowFor(t *testing.T, rows []EnergyRow, channel, parameter string) EnergyRow {
	t.Helper()
	var out []EnergyRow
	for _, r := range rows {
		if r.ChannelAddress == channel && r.Parameter == parameter {
			out = append(out, r)
		}
	}
	if len(out) != 1 {
		t.Fatalf("energyRowFor(%s, %s): got %d rows, want 1 (rows=%+v)", channel, parameter, len(out), rows)
	}
	return out[0]
}

// TestMeasurement_QueryEnergy_HourGroupReadsHourlyTier verifies that
// group="hour" reads measurements_hourly directly (one row per bucket,
// no re-aggregation) and that non-energy parameters are filtered out.
func TestMeasurement_QueryEnergy_HourGroupReadsHourlyTier(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV0001:4"
	)
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	samples := []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: "ENERGY_COUNTER", TS: base, Value: 100},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: "ENERGY_COUNTER", TS: base.Add(30 * time.Minute), Value: 150},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: "POWER", TS: base, Value: 20},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: "POWER", TS: base.Add(30 * time.Minute), Value: 40},
		// Non-energy parameter must never surface in the response.
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: "ACTUAL_TEMPERATURE", TS: base, Value: 21.5},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, base.Add(2*time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}

	rows, err := s.QueryEnergy(ctx, central, "", base.Add(-time.Hour), base.Add(2*time.Hour), "hour")
	if err != nil {
		t.Fatalf("QueryEnergy: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (POWER + ENERGY_COUNTER, temperature excluded): %+v", len(rows), rows)
	}
	energy := energyRowFor(t, rows, ch, "ENERGY_COUNTER")
	if energy.First != 100 || energy.Last != 150 {
		t.Errorf("ENERGY_COUNTER first/last = %v/%v, want 100/150", energy.First, energy.Last)
	}
	power := energyRowFor(t, rows, ch, "POWER")
	if power.Sum != 60 || power.Count != 2 || power.Max != 40 {
		t.Errorf("POWER sum/count/max = %v/%v/%v, want 60/2/40", power.Sum, power.Count, power.Max)
	}
}

// TestMeasurement_QueryEnergy_DayGroupReadsDailyTier verifies that
// group="day" reads measurements_daily directly.
func TestMeasurement_QueryEnergy_DayGroupReadsDailyTier(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV0001:4"
		param   = "ENERGY_COUNTER"
	)
	day := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	samples := []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day.Add(1 * time.Hour), Value: 100},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day.Add(3 * time.Hour), Value: 400},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, day.Add(24*time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	if _, err := s.RollupDaily(ctx, day.Add(48*time.Hour)); err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}

	rows, err := s.QueryEnergy(ctx, central, "", day.Add(-24*time.Hour), day.Add(48*time.Hour), "day")
	if err != nil {
		t.Fatalf("QueryEnergy: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 daily bucket: %+v", len(rows), rows)
	}
	if rows[0].BucketTS.UnixMilli() != day.UnixMilli() {
		t.Errorf("bucket_ts = %v, want %v", rows[0].BucketTS, day)
	}
	if rows[0].First != 100 || rows[0].Last != 400 || rows[0].Sum != 500 {
		t.Errorf("first/last/sum = %v/%v/%v, want 100/400/500", rows[0].First, rows[0].Last, rows[0].Sum)
	}
}

// TestMeasurement_QueryEnergy_MonthGroupReAggregatesDaily verifies that
// group="month" folds every measurements_daily bucket within a UTC
// calendar month into one row per (channel, parameter), exactly
// reproducing sum/count and preserving the earliest first / latest last.
func TestMeasurement_QueryEnergy_MonthGroupReAggregatesDaily(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV0001:4"
		param   = "ENERGY_COUNTER"
	)
	day1 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC) // same month as day1
	day3 := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)  // next month
	samples := []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day1, Value: 100},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day2, Value: 300},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day3, Value: 500},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	cutoff := day3.Add(24 * time.Hour)
	if _, err := s.RollupHourly(ctx, cutoff); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	if _, err := s.RollupDaily(ctx, cutoff); err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}

	rows, err := s.QueryEnergy(ctx, central, "", day1.Add(-24*time.Hour), cutoff, "month")
	if err != nil {
		t.Fatalf("QueryEnergy: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (one per calendar month): %+v", len(rows), rows)
	}
	// energyRowFor assumes one row per (channel,parameter); this test has
	// two month buckets for the same pair, so pick March explicitly.
	var march EnergyRow
	var found bool
	for _, r := range rows {
		if r.BucketTS.Month() == time.March {
			march, found = r, true
		}
	}
	if !found {
		t.Fatalf("no March bucket in %+v", rows)
	}
	if march.First != 100 || march.Last != 300 || march.Sum != 400 || march.Count != 2 {
		t.Errorf("March bucket = %+v, want first=100 last=300 sum=400 count=2", march)
	}
	for _, r := range rows {
		if r.BucketTS.Month() == time.April {
			if r.First != 500 || r.Last != 500 || r.Sum != 500 || r.Count != 1 {
				t.Errorf("April bucket = %+v, want first=last=sum=500 count=1", r)
			}
		}
	}
}

// TestMeasurement_QueryEnergy_DeviceAddressPrefixFilter verifies that a
// non-empty deviceAddr restricts rows to that device's channels
// (address or address+":N"), never a device whose address merely shares
// a prefix.
func TestMeasurement_QueryEnergy_DeviceAddressPrefixFilter(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		param   = "POWER"
	)
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	samples := []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: "DEV0001:4", Parameter: param, TS: base, Value: 10},
		{CentralName: central, InterfaceID: iface, ChannelAddress: "DEV0001X:4", Parameter: param, TS: base, Value: 999},
		{CentralName: central, InterfaceID: iface, ChannelAddress: "OTHER:1", Parameter: param, TS: base, Value: 5},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}

	rows, err := s.QueryEnergy(ctx, central, "DEV0001", base.Add(-time.Hour), base.Add(time.Hour), "hour")
	if err != nil {
		t.Fatalf("QueryEnergy: %v", err)
	}
	if len(rows) != 1 || rows[0].ChannelAddress != "DEV0001:4" {
		t.Fatalf("rows = %+v, want exactly DEV0001:4 (not DEV0001X:4 or OTHER:1)", rows)
	}
}

// TestMeasurement_QueryEnergy_CentralScoping verifies that two centrals
// with the same channel/parameter never bleed into each other's result.
func TestMeasurement_QueryEnergy_CentralScoping(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		iface = "HmIP-RF"
		ch    = "SHARED:1"
		param = "POWER"
	)
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	samples := []MeasurementSample{
		{CentralName: "ccu1", InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base, Value: 10},
		{CentralName: "ccu2", InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base, Value: 999},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}

	rows1, err := s.QueryEnergy(ctx, "ccu1", "", base.Add(-time.Hour), base.Add(time.Hour), "hour")
	if err != nil {
		t.Fatalf("QueryEnergy ccu1: %v", err)
	}
	if len(rows1) != 1 || rows1[0].Sum != 10 {
		t.Errorf("ccu1 rows = %+v, want one row sum=10", rows1)
	}
	rows2, err := s.QueryEnergy(ctx, "ccu2", "", base.Add(-time.Hour), base.Add(time.Hour), "hour")
	if err != nil {
		t.Fatalf("QueryEnergy ccu2: %v", err)
	}
	if len(rows2) != 1 || rows2[0].Sum != 999 {
		t.Errorf("ccu2 rows = %+v, want one row sum=999", rows2)
	}
}

// TestMeasurement_QueryEnergy_ErrorPaths verifies the range guard and the
// unsupported-group guard.
func TestMeasurement_QueryEnergy_ErrorPaths(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	if _, err := s.QueryEnergy(ctx, "ccu1", "", base, base, "day"); err == nil {
		t.Error("expected error when to == from")
	}
	if _, err := s.QueryEnergy(ctx, "ccu1", "", base.Add(time.Hour), base, "day"); err == nil {
		t.Error("expected error when to before from")
	}
	if _, err := s.QueryEnergy(ctx, "ccu1", "", base, base.Add(time.Hour), "week"); err == nil {
		t.Error("expected error for unsupported group")
	}
}

// TestMeasurement_QueryEnergy_NilStore_NoOps verifies the nil-safety
// contract shared with the other MeasurementStore accessors.
func TestMeasurement_QueryEnergy_NilStore_NoOps(t *testing.T) {
	t.Parallel()
	var s *MeasurementStore
	rows, err := s.QueryEnergy(context.Background(), "ccu1", "", time.Now(), time.Now().Add(time.Hour), "day")
	if rows != nil || err != nil {
		t.Errorf("nil store QueryEnergy = (%v, %v), want (nil, nil)", rows, err)
	}
}

// energyRowsForParam filters QueryEnergy output to one parameter and maps it
// by bucket-start epoch ms, so a test can assert which buckets are present.
func energyRowsForParam(rows []EnergyRow, param string) map[int64]EnergyRow {
	out := make(map[int64]EnergyRow)
	for _, r := range rows {
		if r.Parameter == param {
			out[r.BucketTS.UnixMilli()] = r
		}
	}
	return out
}

// TestMeasurement_RollupHourly_BoundedFold_NoReFoldBelowWatermark is the
// core guard for the rollup redesign: once a bucket has been folded and the
// watermark advanced past it, a later raw sample landing in that same
// (already finalized) hour bucket is NOT re-folded — proving the fold is
// bounded to the newly-eligible [watermark, cutoff) window rather than
// re-scanning and rewriting every historical bucket on every tick.
func TestMeasurement_RollupHourly_BoundedFold_NoReFoldBelowWatermark(t *testing.T) {
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

	if err := s.SaveBatch(ctx, []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(10 * time.Minute), Value: 42},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	n1, err := s.RollupHourly(ctx, base.Add(90*time.Minute))
	if err != nil {
		t.Fatalf("RollupHourly (1st): %v", err)
	}
	if n1 != 1 {
		t.Fatalf("1st fold = %d, want 1", n1)
	}
	before := queryHourly(t, s, central, iface, ch, param)
	if len(before) != 1 {
		t.Fatalf("hourly rows = %d, want 1", len(before))
	}

	// A late sample lands in the SAME already-finalized hour bucket.
	if err := s.SaveBatch(ctx, []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(20 * time.Minute), Value: 999},
	}); err != nil {
		t.Fatalf("SaveBatch late: %v", err)
	}
	n2, err := s.RollupHourly(ctx, base.Add(90*time.Minute))
	if err != nil {
		t.Fatalf("RollupHourly (2nd): %v", err)
	}
	if n2 != 0 {
		t.Errorf("2nd fold = %d, want 0 (watermark already past the bucket)", n2)
	}
	after := queryHourly(t, s, central, iface, ch, param)
	if len(after) != 1 || after[0] != before[0] {
		t.Errorf("finalized bucket was re-folded: before=%+v after=%+v", before, after)
	}
	if after[0].Sum != 42 {
		t.Errorf("bucket sum = %v, want 42 (the late 999 must NOT be folded in)", after[0].Sum)
	}
}

// TestMeasurement_RollupFoldScanIndexesExist verifies the migration created
// the time-axis indexes the bounded fold and purge range-scan on, so those
// scans do not fall back to a full-table walk.
func TestMeasurement_RollupFoldScanIndexesExist(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='index' AND name IN
		 ('idx_measurements_ts','idx_measurements_hourly_bucket_ts')`)
	if err != nil {
		t.Fatalf("query indexes: %v", err)
	}
	defer func() { _ = rows.Close() }()
	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found[name] = true
	}
	if !found["idx_measurements_ts"] || !found["idx_measurements_hourly_bucket_ts"] {
		t.Errorf("missing fold-scan indexes: %v", found)
	}
}

// TestMeasurement_QueryEnergy_Hour_MergesRawTail verifies that the hourly
// energy query returns the still-un-rolled recent hours (the raw tail)
// alongside the folded hourly tier, so "the current hour" is never missing.
func TestMeasurement_QueryEnergy_Hour_MergesRawTail(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:4"
		param   = "ENERGY_COUNTER"
	)
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := s.SaveBatch(ctx, []MeasurementSample{
		// hour 0 — will be folded.
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base, Value: 1000},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(30 * time.Minute), Value: 1100},
		// hour 2 — the un-rolled tail.
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(2 * time.Hour), Value: 1200},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(2*time.Hour + 30*time.Minute), Value: 1250},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	// Fold only hour 0 (cutoff aligns to base+1h; watermark = base+1h).
	if _, err := s.RollupHourly(ctx, base.Add(90*time.Minute)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}

	rows, err := s.QueryEnergy(ctx, central, "", base.Add(-time.Hour), base.Add(3*time.Hour), "hour")
	if err != nil {
		t.Fatalf("QueryEnergy: %v", err)
	}
	byBucket := energyRowsForParam(rows, param)
	tierBucket, okTier := byBucket[base.UnixMilli()]
	if !okTier {
		t.Errorf("folded hour-0 bucket missing from energy result")
	} else if tierBucket.First != 1000 || tierBucket.Last != 1100 {
		t.Errorf("hour-0 bucket first/last = %v/%v, want 1000/1100", tierBucket.First, tierBucket.Last)
	}
	tailBucket, okTail := byBucket[base.Add(2*time.Hour).UnixMilli()]
	if !okTail {
		t.Fatalf("un-rolled hour-2 tail bucket missing from energy result (raw tail not merged)")
	}
	if tailBucket.First != 1200 || tailBucket.Last != 1250 {
		t.Errorf("hour-2 tail bucket first/last = %v/%v, want 1200/1250", tailBucket.First, tailBucket.Last)
	}
}

// TestMeasurement_QueryEnergy_Day_MergesTiersAndTail verifies the day query
// assembles all three sources — the daily tier, the hourly tail folded to
// day, and the raw tail folded to day — and that the one day bucket the
// hourly and raw tails share is merged with the earlier (hourly) first and
// the later (raw) last.
func TestMeasurement_QueryEnergy_Day_MergesTiersAndTail(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:4"
		param   = "ENERGY_COUNTER"
	)
	day0 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	day1 := day0.Add(24 * time.Hour)
	if err := s.SaveBatch(ctx, []MeasurementSample{
		// day0 — fully rolled to the daily tier.
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day0.Add(1 * time.Hour), Value: 100},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day0.Add(3 * time.Hour), Value: 300},
		// day1 — two hours folded into the hourly tier (single sample each).
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day1.Add(1 * time.Hour), Value: 400},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day1.Add(3 * time.Hour), Value: 600},
		// day1 — a later raw sample that stays un-folded (the raw tail).
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day1.Add(4*time.Hour + 30*time.Minute), Value: 700},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	// Fold day0 + day1's first hours into hourly (cutoff = day1+4h), but roll
	// only day0 into daily (cutoff aligns to day1). The day1 hourly buckets
	// stay in the hourly tier; the day1+4h30m raw sample stays un-folded.
	if _, err := s.RollupHourly(ctx, day1.Add(4*time.Hour+30*time.Minute)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	if _, err := s.RollupDaily(ctx, day1.Add(time.Hour)); err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}

	rows, err := s.QueryEnergy(ctx, central, "", day0.Add(-time.Hour), day1.Add(6*time.Hour), "day")
	if err != nil {
		t.Fatalf("QueryEnergy: %v", err)
	}
	byBucket := energyRowsForParam(rows, param)

	day0Bucket, ok := byBucket[day0.UnixMilli()]
	if !ok {
		t.Errorf("day0 (daily tier) bucket missing")
	} else if day0Bucket.First != 100 || day0Bucket.Last != 300 {
		t.Errorf("day0 first/last = %v/%v, want 100/300", day0Bucket.First, day0Bucket.Last)
	}

	day1Bucket, ok := byBucket[day1.UnixMilli()]
	if !ok {
		t.Fatalf("day1 (hourly tail + raw tail) bucket missing")
	}
	// hourly tail: first=400 (earliest hour), last=600; raw tail: 700 (later).
	if day1Bucket.First != 400 {
		t.Errorf("day1 first = %v, want 400 (earliest hourly-tail reading)", day1Bucket.First)
	}
	if day1Bucket.Last != 700 {
		t.Errorf("day1 last = %v, want 700 (latest raw-tail reading)", day1Bucket.Last)
	}
	if day1Bucket.Sum != 1700 || day1Bucket.Count != 3 {
		t.Errorf("day1 sum/count = %v/%v, want 1700/3", day1Bucket.Sum, day1Bucket.Count)
	}
	if day1Bucket.Min != 400 || day1Bucket.Max != 700 {
		t.Errorf("day1 min/max = %v/%v, want 400/700", day1Bucket.Min, day1Bucket.Max)
	}
}

// TestMeasurement_Retention_FinalizedDailyNotCorruptedByPurge verifies the
// retention corruption fix: after the source raw and hourly rows are purged,
// re-running the daily fold must NOT recompute the finalized daily bucket
// from the surviving (partial) rows — the watermark keeps the fold from ever
// re-reading below the frontier, so the aggregate is stable.
func TestMeasurement_Retention_FinalizedDailyNotCorruptedByPurge(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:4"
		param   = "ENERGY_COUNTER"
	)
	day0 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := s.SaveBatch(ctx, []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day0.Add(1 * time.Hour), Value: 100},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day0.Add(3 * time.Hour), Value: 400},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, day0.Add(24*time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	if _, err := s.RollupDaily(ctx, day0.Add(48*time.Hour)); err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}
	before := queryDaily(t, s, central, iface, ch, param)
	if len(before) != 1 {
		t.Fatalf("daily rows = %d, want 1", len(before))
	}
	if before[0].Sum != 500 || before[0].First != 100 || before[0].Last != 400 {
		t.Fatalf("daily aggregate = %+v, want sum=500 first=100 last=400", before[0])
	}

	// Purge the source rows (both below their fold frontier).
	if _, err := s.DeleteOlderThan(ctx, day0.Add(24*time.Hour)); err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if _, err := s.DeleteHourlyOlderThan(ctx, day0.Add(48*time.Hour)); err != nil {
		t.Fatalf("DeleteHourlyOlderThan: %v", err)
	}

	// Re-run the daily fold as the loop would; it must be a no-op, not a
	// recompute from the now-purged partial rows.
	if n, err := s.RollupDaily(ctx, day0.Add(48*time.Hour)); err != nil {
		t.Fatalf("RollupDaily (re-run): %v", err)
	} else if n != 0 {
		t.Errorf("re-run folded = %d, want 0 (watermark blocks re-fold)", n)
	}
	after := queryDaily(t, s, central, iface, ch, param)
	if len(after) != 1 || after[0] != before[0] {
		t.Errorf("finalized daily aggregate corrupted by purge+refold: before=%+v after=%+v", before, after)
	}
}

// TestMeasurement_QueryBuckets_FinalBucketIndexClamped verifies that a
// sample whose timestamp sits just below `to` folds into the last valid
// bucket (buckets-1) instead of overflowing into a spurious extra bucket at
// index `buckets` (the integer-width truncation bug).
func TestMeasurement_QueryBuckets_FinalBucketIndexClamped(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "SENSOR:1"
		param   = "TEMP"
	)
	from := time.UnixMilli(0)
	to := time.UnixMilli(10) // width = 10/3 = 3ms; ts=9 → index 9/3 = 3 == buckets
	if err := s.SaveBatch(ctx, []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: time.UnixMilli(0), Value: 1},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: time.UnixMilli(6), Value: 2},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: time.UnixMilli(9), Value: 3},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	buckets, _, err := s.QueryBuckets(ctx, central, iface, ch, param, from, to, 3)
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	// Expect exactly two buckets: index 0 (TS=0, ts=0) and index 2 (TS=6,
	// holding both ts=6 and the clamped ts=9). No spurious index-3 bucket.
	if len(buckets) != 2 {
		t.Fatalf("QueryBuckets returned %d buckets, want 2 (no overflow bucket): %+v", len(buckets), buckets)
	}
	last := buckets[len(buckets)-1]
	if last.TS.UnixMilli() != 6 {
		t.Errorf("final bucket TS = %d ms, want 6 (index 2, clamped)", last.TS.UnixMilli())
	}
	if last.Count != 2 {
		t.Errorf("final bucket count = %d, want 2 (ts=6 and clamped ts=9)", last.Count)
	}
	for _, b := range buckets {
		if b.TS.UnixMilli() == 9 {
			t.Errorf("found spurious overflow bucket at TS=9ms (index 3)")
		}
	}
}

// TestPickHistoryTierSelectsByWidth verifies the width-based tier choice in
// pickHistoryTier: day- and hour-wide buckets pick their tier directly from
// the width, and only the sub-hour case falls through to the raw-floor
// check. A single raw row at fromMs keeps that sub-hour case selecting raw
// (rather than being promoted) so this table isolates the width comparison,
// not the promotion behaviour covered separately below.
func TestPickHistoryTierSelectsByWidth(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "TEMP"
	)
	fromMs := int64(1000)
	if err := s.SaveBatch(ctx, []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: time.UnixMilli(fromMs), Value: 1},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	key := seriesKey{central: central, iface: iface, channel: ch, parameter: param}

	cases := []struct {
		name  string
		width int64
		want  HistoryTier
	}{
		{"sub-hour width with fresh raw data", 1000, HistoryTierRaw},
		{"exactly one hour", hourBucketMs, HistoryTierHour},
		{"exactly one day", dayBucketMs, HistoryTierDay},
		{"above one day", dayBucketMs * 2, HistoryTierDay},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.pickHistoryTier(ctx, key, tc.width, fromMs)
			if err != nil {
				t.Fatalf("pickHistoryTier: %v", err)
			}
			if got != tc.want {
				t.Errorf("pickHistoryTier(width=%d) = %q, want %q", tc.width, got, tc.want)
			}
		})
	}
}

// TestQueryBucketsPromotesToHourlyWhenRawPurged is the regression case the
// tiering change exists for: once the raw rows behind a range have been
// rolled up and purged, a narrow-bucket query over that range used to read
// the (now empty) raw table directly and come back empty even though the
// hourly rollup still holds the data. It must instead promote to the hourly
// tier and return the real aggregate.
func TestQueryBucketsPromotesToHourlyWhenRawPurged(t *testing.T) {
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
	if err := s.SaveBatch(ctx, []MeasurementSample{
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base, Value: 10},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(20 * time.Minute), Value: 30},
	}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	if _, err := s.DeleteOlderThan(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	// Confirm the raw rows are actually gone, or the test below would not be
	// exercising the promotion at all.
	if st, err := s.Stats(ctx); err != nil {
		t.Fatalf("Stats: %v", err)
	} else if st.Rows != 0 {
		t.Fatalf("raw rows survived the purge: Stats.Rows = %d, want 0", st.Rows)
	}

	buckets, tier, err := s.QueryBuckets(ctx, central, iface, ch, param,
		base, base.Add(time.Hour), 60) // width = 1 minute, sub-hour
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if tier != HistoryTierHour {
		t.Fatalf("tier = %q, want %q", tier, HistoryTierHour)
	}
	if len(buckets) == 0 {
		t.Fatal("QueryBuckets returned no buckets after promotion; the data did not come back")
	}
	var total int64
	for _, b := range buckets {
		total += b.Count
	}
	if total != 2 {
		t.Errorf("total sample count across buckets = %d, want 2", total)
	}
}

// TestQueryBucketsPromotesToHourlyWhenSeriesHasNoRawRows covers the second
// promotion trigger: a series that never had a raw row recorded through this
// store at all (rawFloor's !ok branch, as opposed to the purge case above
// where a floor exists but is too recent). The hourly row is inserted
// directly and its watermark advanced by hand, since there is no raw data to
// roll up from.
func TestQueryBucketsPromotesToHourlyWhenSeriesHasNoRawRows(t *testing.T) {
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

	insertHourlyRow(t, s, central, iface, ch, param, bucketTS, 40, 10, 30, 2, 10, 30)
	advanceHourlyWatermark(t, s, bucketTS+hourBucketMs)

	buckets, tier, err := s.QueryBuckets(ctx, central, iface, ch, param,
		base.Truncate(time.Hour), base.Truncate(time.Hour).Add(time.Hour), 60) // sub-hour width
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if tier != HistoryTierHour {
		t.Fatalf("tier = %q, want %q", tier, HistoryTierHour)
	}
	if len(buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(buckets))
	}
	if buckets[0].Count != 2 || buckets[0].Avg != 20 {
		t.Errorf("bucket = %+v, want Count=2 Avg=20", buckets[0])
	}
}

// TestQueryBucketsHourlyAssemblyMergesAcrossWatermark verifies that the
// hourly tier assembly merges the persisted rollup (below the watermark)
// with the still-un-rolled raw tail (at/above it) into disjoint buckets: the
// total sample count across every returned bucket must equal the number of
// samples written, proving neither source is double-counted.
func TestQueryBucketsHourlyAssemblyMergesAcrossWatermark(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "POWER"
	)
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	samples := []MeasurementSample{
		// hour 0 — will be folded below the watermark.
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base, Value: 10},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(30 * time.Minute), Value: 20},
		// hour 1 — stays the un-rolled raw tail (RollupHourly's cutoff below
		// stops exactly at the hour-1 boundary).
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(time.Hour), Value: 30},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(time.Hour + 30*time.Minute), Value: 40},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}

	buckets, tier, err := s.QueryBuckets(ctx, central, iface, ch, param,
		base, base.Add(2*time.Hour), 2) // width = 1 hour
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if tier != HistoryTierHour {
		t.Fatalf("tier = %q, want %q", tier, HistoryTierHour)
	}
	if len(buckets) != 2 {
		t.Fatalf("buckets = %d, want 2", len(buckets))
	}
	var total int64
	for _, b := range buckets {
		total += b.Count
	}
	if total != int64(len(samples)) {
		t.Errorf("total sample count across buckets = %d, want %d (a source was double-counted or dropped)", total, len(samples))
	}
	if buckets[0].Count != 2 || buckets[0].Avg != 15 {
		t.Errorf("hour-0 bucket (rollup tier) = %+v, want Count=2 Avg=15", buckets[0])
	}
	if buckets[1].Count != 2 || buckets[1].Avg != 35 {
		t.Errorf("hour-1 bucket (raw tail) = %+v, want Count=2 Avg=35", buckets[1])
	}
}

// TestQueryBucketsDailyAssemblyMergesThreeSources exercises the three-source
// day tier assembly (persisted daily tier + hourly tail folded to day + raw
// tail folded to day) with a fine enough output resolution (one output
// bucket per day) that day0 and day1 stay in separate buckets. The total
// sample count across buckets must equal the number of samples written,
// proving the three sources are disjoint rather than double-counting the
// data at the tier boundaries.
func TestQueryBucketsDailyAssemblyMergesThreeSources(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "POWER"
	)
	day0 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	day1 := day0.Add(24 * time.Hour)
	samples := []MeasurementSample{
		// day0 — fully rolled into the daily tier.
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day0.Add(1 * time.Hour), Value: 10},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day0.Add(3 * time.Hour), Value: 20},
		// day1 — folded into the hourly tier but not yet the daily tier.
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day1.Add(1 * time.Hour), Value: 30},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day1.Add(3 * time.Hour), Value: 40},
		// day1 — a later sample that stays the un-folded raw tail.
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day1.Add(5 * time.Hour), Value: 50},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	// Fold everything up to day1+4h into hourly, but only day0 into daily.
	if _, err := s.RollupHourly(ctx, day1.Add(4*time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	if _, err := s.RollupDaily(ctx, day1); err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}

	buckets, tier, err := s.QueryBuckets(ctx, central, iface, ch, param,
		day0, day1.Add(24*time.Hour), 2) // width = 1 day
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if tier != HistoryTierDay {
		t.Fatalf("tier = %q, want %q", tier, HistoryTierDay)
	}
	if len(buckets) != 2 {
		t.Fatalf("buckets = %d, want 2", len(buckets))
	}
	var total int64
	for _, b := range buckets {
		total += b.Count
	}
	if total != int64(len(samples)) {
		t.Errorf("total sample count across buckets = %d, want %d (a source was double-counted or dropped)", total, len(samples))
	}
	if buckets[0].Count != 2 || buckets[0].Avg != 15 {
		t.Errorf("day0 bucket (daily tier) = %+v, want Count=2 Avg=15", buckets[0])
	}
	if buckets[1].Count != 3 || buckets[1].Avg != 40 {
		t.Errorf("day1 bucket (hourly + raw tail) = %+v, want Count=3 Avg=40", buckets[1])
	}
}

// TestQueryBucketsAverageIsExactNotAverageOfAverages is the core property
// the sum+count rollup design buys: folding two source buckets with very
// different sample counts into one output bucket must produce the true
// sum/count average, not the average of the two sources' own averages. One
// hour contributes a single sample of 100; the other contributes 9999
// samples of 0. The naive average-of-averages would read (100+0)/2 = 50;
// the true average is 100/10000 = 0.01.
func TestQueryBucketsAverageIsExactNotAverageOfAverages(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "POWER"
	)
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	hourB := base.Add(time.Hour)

	const lowSampleCount = 9999
	samples := make([]MeasurementSample, 0, lowSampleCount+1)
	samples = append(samples, MeasurementSample{
		CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param,
		TS: base, Value: 100,
	})
	for i := 0; i < lowSampleCount; i++ {
		// Spread across hour B (300ms apart, well inside the 1h bucket) so
		// every sample gets a distinct primary-key timestamp.
		samples = append(samples, MeasurementSample{
			CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param,
			TS: hourB.Add(time.Duration(i) * 300 * time.Millisecond), Value: 0,
		})
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, base.Add(2*time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}

	// One output bucket spanning both hourly rows forces the merge in
	// foldTierBuckets.
	buckets, tier, err := s.QueryBuckets(ctx, central, iface, ch, param, base, base.Add(2*time.Hour), 1)
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if tier != HistoryTierHour {
		t.Fatalf("tier = %q, want %q", tier, HistoryTierHour)
	}
	if len(buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(buckets))
	}
	if buckets[0].Count != int64(len(samples)) {
		t.Fatalf("Count = %d, want %d", buckets[0].Count, len(samples))
	}
	want := 100.0 / float64(len(samples))
	if buckets[0].Avg != want {
		t.Errorf("Avg = %v, want %v (sum/count)", buckets[0].Avg, want)
	}
	if buckets[0].Avg == 50 {
		t.Error("Avg = 50: this is the average of the two per-hour averages, not the true sum/count")
	}
}

// TestQueryBucketsMinMaxSurviveHourlyRefold verifies that folding two
// already-aggregated hourly rows into a single wider output bucket keeps the
// true global min/max, not just one source's extremes.
func TestQueryBucketsMinMaxSurviveHourlyRefold(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "TEMP"
	)
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	samples := []MeasurementSample{
		// hour 0 — holds the coldest reading of the pair.
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base, Value: -40},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(20 * time.Minute), Value: 5},
		// hour 1 — holds the warmest reading of the pair.
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(time.Hour), Value: 0},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: base.Add(time.Hour + 20*time.Minute), Value: 60},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, base.Add(2*time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}

	buckets, tier, err := s.QueryBuckets(ctx, central, iface, ch, param, base, base.Add(2*time.Hour), 1)
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if tier != HistoryTierHour {
		t.Fatalf("tier = %q, want %q", tier, HistoryTierHour)
	}
	if len(buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(buckets))
	}
	if buckets[0].Min != -40 {
		t.Errorf("Min = %v, want -40 (coldest reading from hour 0)", buckets[0].Min)
	}
	if buckets[0].Max != 60 {
		t.Errorf("Max = %v, want 60 (warmest reading from hour 1)", buckets[0].Max)
	}
}

// TestQueryBucketsMinMaxSurviveDailyThreeSourceMerge exercises the min/max
// fold across all three day-tier sources at once by requesting a single
// output bucket wide enough to swallow day0 and day1 together — including
// the day1 sub-merge of its hourly tail and raw tail (the "day bucket
// straddling the hourly frontier" case the assembleTierBuckets doc comment
// describes). The global min/max must survive intact through every fold.
func TestQueryBucketsMinMaxSurviveDailyThreeSourceMerge(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "DEV:1"
		param   = "TEMP"
	)
	day0 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	day1 := day0.Add(24 * time.Hour)
	samples := []MeasurementSample{
		// day0 (daily tier) — holds the global minimum.
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day0.Add(1 * time.Hour), Value: -100},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day0.Add(3 * time.Hour), Value: 50},
		// day1 hourly tail — holds the global maximum.
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day1.Add(1 * time.Hour), Value: 10},
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day1.Add(3 * time.Hour), Value: 200},
		// day1 raw tail — mid-range, must not overwrite either extreme.
		{CentralName: central, InterfaceID: iface, ChannelAddress: ch, Parameter: param, TS: day1.Add(5 * time.Hour), Value: -5},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if _, err := s.RollupHourly(ctx, day1.Add(4*time.Hour)); err != nil {
		t.Fatalf("RollupHourly: %v", err)
	}
	if _, err := s.RollupDaily(ctx, day1); err != nil {
		t.Fatalf("RollupDaily: %v", err)
	}

	buckets, tier, err := s.QueryBuckets(ctx, central, iface, ch, param,
		day0, day1.Add(24*time.Hour), 1) // width = 2 days, collapses everything into one bucket
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if tier != HistoryTierDay {
		t.Fatalf("tier = %q, want %q", tier, HistoryTierDay)
	}
	if len(buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(buckets))
	}
	if buckets[0].Count != int64(len(samples)) {
		t.Errorf("Count = %d, want %d", buckets[0].Count, len(samples))
	}
	if buckets[0].Min != -100 {
		t.Errorf("Min = %v, want -100", buckets[0].Min)
	}
	if buckets[0].Max != 200 {
		t.Errorf("Max = %v, want 200", buckets[0].Max)
	}
}

// TestFoldTierBuckets exercises the pure re-folding function directly,
// without touching the database, for the edge cases the SQL-side callers
// rely on: index clamping in both directions, accumulation when several
// source buckets land on the same output index (the day-bucket-straddling
// case), skipping empty sources, and a sorted-by-TS result.
func TestFoldTierBuckets(t *testing.T) {
	t.Parallel()

	t.Run("empty input yields nil", func(t *testing.T) {
		if got := foldTierBuckets(nil, 0, 1000, 4); got != nil {
			t.Errorf("foldTierBuckets(nil) = %v, want nil", got)
		}
	})

	t.Run("out-of-range index clamps to the last bucket", func(t *testing.T) {
		// Integer truncation in the width computation can leave a source
		// bucket start beyond fromMs+width*buckets; it must fold into the
		// last valid bucket instead of being dropped or overflowing.
		src := []tierBucket{
			{bucketTS: 100_000, sum: 10, minV: 10, maxV: 10, count: 1},
		}
		got := foldTierBuckets(src, 0, 1000, 4) // valid indices 0..3
		if len(got) != 1 {
			t.Fatalf("got %d buckets, want 1", len(got))
		}
		if want := int64(3 * 1000); got[0].TS.UnixMilli() != want {
			t.Errorf("TS = %d, want %d (clamped to the last bucket)", got[0].TS.UnixMilli(), want)
		}
	})

	t.Run("negative index clamps to zero", func(t *testing.T) {
		// A source bucket that starts before fromMs (possible at a tier
		// boundary) must fold into bucket 0, never underflow the slice.
		src := []tierBucket{
			{bucketTS: -5000, sum: 7, minV: 7, maxV: 7, count: 1},
		}
		got := foldTierBuckets(src, 0, 1000, 4)
		if len(got) != 1 || got[0].TS.UnixMilli() != 0 {
			t.Fatalf("got %+v, want one bucket at TS=0", got)
		}
	})

	t.Run("repeated indices accumulate", func(t *testing.T) {
		// Two source buckets mapping to the same output index — the
		// straddling-day-bucket case — must merge (sum/count add, min/max
		// fold), not silently overwrite one another.
		src := []tierBucket{
			{bucketTS: 0, sum: 10, minV: 1, maxV: 10, count: 2},
			{bucketTS: 500, sum: 5, minV: -3, maxV: 8, count: 3},
		}
		got := foldTierBuckets(src, 0, 1000, 1)
		if len(got) != 1 {
			t.Fatalf("got %d buckets, want 1", len(got))
		}
		b := got[0]
		if b.Count != 5 {
			t.Errorf("Count = %d, want 5 (2+3)", b.Count)
		}
		if b.Avg != 3 { // (10+5) / 5
			t.Errorf("Avg = %v, want 3", b.Avg)
		}
		if b.Min != -3 || b.Max != 10 {
			t.Errorf("Min/Max = %v/%v, want -3/10", b.Min, b.Max)
		}
	})

	t.Run("zero-count sources are skipped", func(t *testing.T) {
		src := []tierBucket{
			{bucketTS: 0, sum: 0, minV: 0, maxV: 0, count: 0},
			{bucketTS: 1000, sum: 20, minV: 20, maxV: 20, count: 1},
		}
		got := foldTierBuckets(src, 0, 1000, 4)
		if len(got) != 1 {
			t.Fatalf("got %d buckets, want 1 (the zero-count source must not produce an empty output bucket)", len(got))
		}
		if got[0].TS.UnixMilli() != 1000 {
			t.Errorf("TS = %d, want 1000", got[0].TS.UnixMilli())
		}
	})

	t.Run("output is sorted ascending by TS", func(t *testing.T) {
		src := []tierBucket{
			{bucketTS: 3000, sum: 3, minV: 3, maxV: 3, count: 1},
			{bucketTS: 0, sum: 0, minV: 0, maxV: 0, count: 1},
			{bucketTS: 1000, sum: 1, minV: 1, maxV: 1, count: 1},
		}
		got := foldTierBuckets(src, 0, 1000, 4)
		if len(got) != 3 {
			t.Fatalf("got %d buckets, want 3", len(got))
		}
		for i := 1; i < len(got); i++ {
			if !got[i].TS.After(got[i-1].TS) {
				t.Errorf("buckets not strictly ascending at index %d: %v then %v", i, got[i-1].TS, got[i].TS)
			}
		}
	})
}

// TestQueryBucketsNoDataReturnsNilWithValidTier verifies that querying a
// series with no recorded rows at all returns a nil bucket slice and no
// error, and that the reported tier is still one of the defined HistoryTier
// values rather than the zero value. The series has no raw floor at all, so
// the sub-hour width requested here is promoted to the hourly tier — the
// same promotion path as when raw data has been purged.
func TestQueryBucketsNoDataReturnsNilWithValidTier(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	buckets, tier, err := s.QueryBuckets(ctx, "ccu1", "HmIP-RF", "DEV:1", "TEMP",
		time.UnixMilli(0), time.UnixMilli(60_000), 10) // width = 6s, sub-hour
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if buckets != nil {
		t.Errorf("buckets = %v, want nil for a series with no recorded rows", buckets)
	}
	if tier != HistoryTierHour {
		t.Errorf("tier = %q, want %q (promoted: the series has no raw floor at all)", tier, HistoryTierHour)
	}
}
