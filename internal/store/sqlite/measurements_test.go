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

	buckets, err := s.QueryBuckets(ctx, central, iface, ch, param, from, to, 4)
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

	_, err := s.QueryBuckets(ctx, "ccu1", "HmIP-RF", "CH:1", "P", now, later, 0)
	if err == nil {
		t.Error("QueryBuckets(buckets=0): expected error, got nil")
	}

	_, err = s.QueryBuckets(ctx, "ccu1", "HmIP-RF", "CH:1", "P", now, later, -1)
	if err == nil {
		t.Error("QueryBuckets(buckets=-1): expected error, got nil")
	}

	// to == from
	_, err = s.QueryBuckets(ctx, "ccu1", "HmIP-RF", "CH:1", "P", now, now, 10)
	if err == nil {
		t.Error("QueryBuckets(to==from): expected error, got nil")
	}

	// to before from
	_, err = s.QueryBuckets(ctx, "ccu1", "HmIP-RF", "CH:1", "P", later, now, 10)
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

	buckets, err := s.QueryBuckets(ctx, "ccu1", "HmIP-RF", "SHARED:1", "TEMP", from, to, 2)
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
func TestMeasurement_DeleteOlderThan_RemovesOnlyOldRows(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	old := time.UnixMilli(1000)
	cutoff := time.UnixMilli(5000)
	fresh := time.UnixMilli(9000)

	samples := []MeasurementSample{
		{CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "D:1", Parameter: "P", TS: old, Value: 1},
		{CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "D:1", Parameter: "P", TS: fresh, Value: 2},
	}
	if err := s.SaveBatch(ctx, samples); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	n, err := s.DeleteOlderThan(ctx, cutoff)
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
	buckets, err := s.QueryBuckets(ctx, central, iface, "ABC1234:1", "P",
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

	buckets, err := s.QueryBuckets(ctx, "central-b", "HmIP-RF", "DEV:1", "TEMP",
		time.UnixMilli(0), time.UnixMilli(2000), 1)
	if err != nil {
		t.Fatalf("QueryBuckets central-b: %v", err)
	}
	if len(buckets) != 1 || buckets[0].Count != 1 {
		t.Errorf("central-b history was incorrectly removed by DeleteForCentral(central-a)")
	}

	buckets, err = s.QueryBuckets(ctx, "central-a", "HmIP-RF", "DEV:1", "TEMP",
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

	buckets, err := s.QueryBuckets(ctx, central, iface, ch, param,
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
		b, qErr := s.QueryBuckets(ctx, central, iface, ch, param,
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

	got, err := s.QueryBuckets(ctx, "c", "i", "d:1", "P", now, now.Add(time.Second), 1)
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

// TestMeasurement_RollupHourly_Idempotent verifies that running
// RollupHourly twice against the same raw rows (nothing deleted in
// between) produces exactly one unchanged hourly row: the fold recomputes
// the whole bucket from source every run rather than accumulating, so a
// re-run is a no-op overwrite, not a double-count.
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
	first := queryHourly(t, s, central, iface, ch, param)
	if len(first) != 1 {
		t.Fatalf("hourly rows after 1st run = %d, want 1", len(first))
	}

	n2, err := s.RollupHourly(ctx, cutoff)
	if err != nil {
		t.Fatalf("RollupHourly (2nd): %v", err)
	}
	second := queryHourly(t, s, central, iface, ch, param)
	if len(second) != 1 {
		t.Fatalf("hourly rows after 2nd run = %d, want 1 (idempotent)", len(second))
	}
	if n1 != n2 {
		t.Errorf("folded count changed across re-runs: %d then %d", n1, n2)
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
