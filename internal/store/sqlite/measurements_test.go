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

	if err := s.DeleteAll(ctx); err != nil {
		t.Errorf("nil DeleteAll: %v", err)
	}

	if _, err := s.Stats(ctx); err != nil {
		t.Errorf("nil Stats: %v", err)
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

// Ensure errors package is used (satisfies golangci-lint if errors.New is referenced).
var _ = errors.New
