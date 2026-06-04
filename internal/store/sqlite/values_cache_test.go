// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"
	"time"
)

func freshValuesCacheStore(t *testing.T) *ValuesCacheStore {
	t.Helper()
	return NewValuesCacheStore(openTestDB(t, "vc.db"))
}

// nowMS returns the current time truncated to millisecond precision so
// comparisons against UnixMilli-restored values are exact.
func nowMS() time.Time {
	return time.UnixMilli(time.Now().UnixMilli())
}

// TestValuesCache_SaveAndLoadChannel_Roundtrip verifies that SaveValue
// persists non-nil values with the correct ValueType and that nil is
// silently skipped.
func TestValuesCache_SaveAndLoadChannel_Roundtrip(t *testing.T) {
	t.Parallel()
	s := freshValuesCacheStore(t)
	ctx := context.Background()

	now := nowMS()
	const (
		centralName = "ccu1"
		iface       = "HmIP-RF"
		ch          = "DEV:1"
	)

	save := func(param string, val any) {
		t.Helper()
		if err := s.SaveValue(ctx, centralName, iface, ch, param, val, now, now); err != nil {
			t.Fatalf("SaveValue(%s): %v", param, err)
		}
	}

	save("FLOAT_PARAM", float64(22.4))
	save("BOOL_PARAM", true)
	save("INT_PARAM", int(7))
	save("STR_PARAM", "ok")
	save("NIL_PARAM", nil) // must NOT be stored

	got, err := s.LoadChannel(ctx, centralName, iface, ch)
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}

	byParam := make(map[string]CachedValue, len(got))
	for _, cv := range got {
		byParam[cv.Parameter] = cv
	}

	if _, has := byParam["NIL_PARAM"]; has {
		t.Error("NIL_PARAM must not be persisted")
	}

	cases := []struct {
		param   string
		wantTyp ValueType
	}{
		{"FLOAT_PARAM", ValueTypeFloat},
		{"BOOL_PARAM", ValueTypeBool},
		{"INT_PARAM", ValueTypeInt},
		{"STR_PARAM", ValueTypeString},
	}
	for _, tc := range cases {
		cv, ok := byParam[tc.param]
		if !ok {
			t.Errorf("%s: missing from LoadChannel result", tc.param)
			continue
		}
		if cv.Type != tc.wantTyp {
			t.Errorf("%s: Type = %q, want %q", tc.param, cv.Type, tc.wantTyp)
		}
		if cv.LastSeenAt.UnixMilli() != now.UnixMilli() {
			t.Errorf("%s: LastSeenAt mismatch: got %v, want %v", tc.param, cv.LastSeenAt.UnixMilli(), now.UnixMilli())
		}
	}
}

// TestValuesCache_LoadAll_GroupedCorrectly verifies that LoadAll returns the
// nested map keyed by (central, interface, channel) and groups rows correctly
// across two centrals and two interfaces.
func TestValuesCache_LoadAll_GroupedCorrectly(t *testing.T) {
	t.Parallel()
	s := freshValuesCacheStore(t)
	ctx := context.Background()
	now := nowMS()

	entries := []struct {
		centralName, iface, ch, param string
		val                           any
	}{
		{"ccu1", "HmIP-RF", "A:1", "P1", float64(1)},
		{"ccu1", "HmIP-RF", "A:2", "P1", float64(2)},
		{"ccu1", "BidCos-RF", "B:1", "P2", true},
		{"ccu2", "HmIP-RF", "C:1", "P3", "hello"},
	}
	for _, e := range entries {
		if err := s.SaveValue(ctx, e.centralName, e.iface, e.ch, e.param, e.val, now, now); err != nil {
			t.Fatalf("SaveValue %s/%s/%s/%s: %v", e.centralName, e.iface, e.ch, e.param, err)
		}
	}

	all, err := s.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	check := func(centralName, iface, ch string, wantCount int) {
		t.Helper()
		vals, ok := all[centralName][iface][ch]
		if !ok {
			t.Errorf("key [%s][%s][%s] missing", centralName, iface, ch)
			return
		}
		if len(vals) != wantCount {
			t.Errorf("[%s][%s][%s]: got %d entries, want %d", centralName, iface, ch, len(vals), wantCount)
		}
	}

	check("ccu1", "HmIP-RF", "A:1", 1)
	check("ccu1", "HmIP-RF", "A:2", 1)
	check("ccu1", "BidCos-RF", "B:1", 1)
	check("ccu2", "HmIP-RF", "C:1", 1)

	if _, has := all["ccu2"]["HmIP-RF"]["A:1"]; has {
		t.Error("ccu2 must not contain ccu1 entries")
	}
}

// TestValuesCache_SaveBatch_Atomicity verifies that all valid entries in a
// batch are written and that unmarshalable values (channels etc.) are skipped
// without aborting the whole batch.
func TestValuesCache_SaveBatch_Atomicity(t *testing.T) {
	t.Parallel()
	s := freshValuesCacheStore(t)
	ctx := context.Background()
	now := nowMS()

	good := func(param string, val any) SaveEntry {
		return SaveEntry{
			CentralName:    "ccu1",
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "BATCH:1",
			ParameterName:  param,
			Value:          val,
			LastSeenAt:     now,
			LastChangedAt:  now,
		}
	}

	batch := []SaveEntry{
		good("A", float64(1)),
		good("BAD", make(chan int)), // json.Marshal will fail → skipped
		good("B", "two"),
		good("C", true),
	}

	if err := s.SaveBatch(ctx, batch); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	got, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "BATCH:1")
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	byParam := make(map[string]CachedValue, len(got))
	for _, cv := range got {
		byParam[cv.Parameter] = cv
	}

	for _, want := range []string{"A", "B", "C"} {
		if _, ok := byParam[want]; !ok {
			t.Errorf("param %q missing from batch result", want)
		}
	}
	if _, has := byParam["BAD"]; has {
		t.Error("BAD (channel value) must have been skipped by SaveBatch")
	}
}

// TestValuesCache_TypeOfValue_AllScalarKinds verifies that TypeOfValue returns
// the correct discriminator for every supported Go type and nil.
func TestValuesCache_TypeOfValue_AllScalarKinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		val  any
		want ValueType
	}{
		{nil, ValueTypeNull},
		{true, ValueTypeBool},
		{false, ValueTypeBool},
		{int(1), ValueTypeInt},
		{int64(1), ValueTypeInt},
		{float32(1.5), ValueTypeFloat},
		{float64(1.5), ValueTypeFloat},
		{"x", ValueTypeString},
	}
	for _, tc := range cases {
		got := TypeOfValue(tc.val)
		if got != tc.want {
			t.Errorf("TypeOfValue(%T(%v)) = %q, want %q", tc.val, tc.val, got, tc.want)
		}
	}
}

// TestValuesCache_DeleteDevice_PrefixSafety verifies that DeleteDevice("DEVICE")
// does not affect a device whose address begins with "DEVICE2". The
// implementation appends ":" to the prefix to prevent accidental collisions.
func TestValuesCache_DeleteDevice_PrefixSafety(t *testing.T) {
	t.Parallel()
	s := freshValuesCacheStore(t)
	ctx := context.Background()
	now := nowMS()

	save := func(ch string) {
		t.Helper()
		if err := s.SaveValue(ctx, "ccu1", "HmIP-RF", ch, "P", 1, now, now); err != nil {
			t.Fatalf("SaveValue(%s): %v", ch, err)
		}
	}
	save("DEVICE:0")
	save("DEVICE2:0")

	if err := s.DeleteDevice(ctx, "ccu1", "HmIP-RF", "DEVICE"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}

	got, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEVICE:0")
	if err != nil {
		t.Fatalf("LoadChannel DEVICE:0: %v", err)
	}
	if len(got) != 0 {
		t.Error("DEVICE:0 should have been deleted")
	}

	got2, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEVICE2:0")
	if err != nil {
		t.Fatalf("LoadChannel DEVICE2:0: %v", err)
	}
	if len(got2) == 0 {
		t.Error("DEVICE2:0 was incorrectly removed by DeleteDevice(\"DEVICE\")")
	}
}

// TestValuesCache_DeleteAll_ClearsEverything verifies that DeleteAll removes
// every row and that a subsequent LoadAll returns an empty map.
func TestValuesCache_DeleteAll_ClearsEverything(t *testing.T) {
	t.Parallel()
	s := freshValuesCacheStore(t)
	ctx := context.Background()
	now := nowMS()

	for i, ch := range []string{"A:1", "B:2", "C:3"} {
		if err := s.SaveValue(ctx, "ccu1", "HmIP-RF", ch, "P", i, now, now); err != nil {
			t.Fatalf("SaveValue(%s): %v", ch, err)
		}
	}

	if err := s.DeleteAll(ctx); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}

	all, err := s.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll after DeleteAll: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("LoadAll after DeleteAll: got %d top-level entries, want 0", len(all))
	}
}

// TestValuesCache_GCDeadRows_KeepsAliveOnly verifies that GCDeadRows deletes
// rows that are absent from the alive set and preserves rows that are present.
// GCResult.Scanned and Deleted counts must be correct.
func TestValuesCache_GCDeadRows_KeepsAliveOnly(t *testing.T) {
	t.Parallel()
	s := freshValuesCacheStore(t)
	ctx := context.Background()
	now := nowMS()

	const (
		centralName = "ccu1"
		iface       = "HmIP-RF"
	)
	for _, e := range []struct{ ch, param string }{
		{"A:1", "P"},
		{"B:1", "P"},
		{"C:1", "P"},
	} {
		if err := s.SaveValue(ctx, centralName, iface, e.ch, e.param, true, now, now); err != nil {
			t.Fatalf("SaveValue %s.%s: %v", e.ch, e.param, err)
		}
	}

	alive := map[string]struct{}{
		AliveKey(centralName, iface, "A:1", "P"): {},
		AliveKey(centralName, iface, "B:1", "P"): {},
		// C:1.P is intentionally absent → must be deleted
	}

	res, err := s.GCDeadRows(ctx, alive)
	if err != nil {
		t.Fatalf("GCDeadRows: %v", err)
	}
	if res.Scanned != 3 {
		t.Errorf("Scanned = %d, want 3", res.Scanned)
	}
	if res.Deleted != 1 {
		t.Errorf("Deleted = %d, want 1", res.Deleted)
	}

	// A:1 and B:1 must still be readable.
	for _, ch := range []string{"A:1", "B:1"} {
		got, err := s.LoadChannel(ctx, centralName, iface, ch)
		if err != nil {
			t.Fatalf("LoadChannel %s: %v", ch, err)
		}
		if len(got) == 0 {
			t.Errorf("%s: alive entry was unexpectedly deleted", ch)
		}
	}

	// C:1 must be gone.
	gone, err := s.LoadChannel(ctx, centralName, iface, "C:1")
	if err != nil {
		t.Fatalf("LoadChannel C:1: %v", err)
	}
	if len(gone) != 0 {
		t.Error("C:1 should have been GC'd but was not")
	}
}

// TestValuesCache_GCDeadRows_NilAliveSet_NoOp verifies the defensive behaviour:
// a nil alive map must not wipe the cache — it returns 0 deletes.
func TestValuesCache_GCDeadRows_NilAliveSet_NoOp(t *testing.T) {
	t.Parallel()
	s := freshValuesCacheStore(t)
	ctx := context.Background()
	now := nowMS()

	if err := s.SaveValue(ctx, "ccu1", "HmIP-RF", "X:1", "P", 42, now, now); err != nil {
		t.Fatalf("SaveValue: %v", err)
	}

	res, err := s.GCDeadRows(ctx, nil)
	if err != nil {
		t.Fatalf("GCDeadRows(nil): %v", err)
	}
	if res.Deleted != 0 {
		t.Errorf("GCDeadRows(nil): Deleted = %d, want 0", res.Deleted)
	}

	got, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "X:1")
	if err != nil {
		t.Fatalf("LoadChannel after GC(nil): %v", err)
	}
	if len(got) == 0 {
		t.Error("nil alive set must not wipe the cache")
	}
}

// TestValuesCache_LoadChannel_SkipsRowsWithDifferentSchemaVersion verifies that
// LoadChannel silently ignores rows whose cache_schema_version does not match
// the current constant.
func TestValuesCache_LoadChannel_SkipsRowsWithDifferentSchemaVersion(t *testing.T) {
	t.Parallel()
	s := freshValuesCacheStore(t)
	ctx := context.Background()
	now := nowMS()

	// Write a valid row via the store.
	if err := s.SaveValue(ctx, "ccu1", "HmIP-RF", "SV:1", "GOOD", true, now, now); err != nil {
		t.Fatalf("SaveValue GOOD: %v", err)
	}

	// Insert a row with a future schema version directly via SQL.
	db := openTestDB(t, "sv_raw.db")
	sRaw := NewValuesCacheStore(db)
	if err := sRaw.SaveValue(ctx, "ccu1", "HmIP-RF", "SV:1", "OLD", true, now, now); err != nil {
		t.Fatalf("SaveValue OLD: %v", err)
	}
	_, err := db.ExecContext(ctx,
		`UPDATE values_cache SET cache_schema_version = 99 WHERE parameter_name = 'OLD'`)
	if err != nil {
		t.Fatalf("UPDATE schema_version: %v", err)
	}

	got, err := sRaw.LoadChannel(ctx, "ccu1", "HmIP-RF", "SV:1")
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	for _, cv := range got {
		if cv.Parameter == "OLD" {
			t.Error("row with cache_schema_version=99 must be filtered by LoadChannel")
		}
	}
}

// TestValuesCache_Stats_RowsAndBytes verifies that Stats returns the correct row
// count and a positive byte size after writing entries.
func TestValuesCache_Stats_RowsAndBytes(t *testing.T) {
	t.Parallel()
	s := freshValuesCacheStore(t)
	ctx := context.Background()
	now := nowMS()

	const n = 5
	for i := range n {
		param := string(rune('A' + i))
		if err := s.SaveValue(ctx, "ccu1", "HmIP-RF", "STAT:1", param, float64(i), now, now); err != nil {
			t.Fatalf("SaveValue param %s: %v", param, err)
		}
	}

	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Rows != n {
		t.Errorf("Stats.Rows = %d, want %d", st.Rows, n)
	}
	if st.ValueJSONSize <= 0 {
		t.Errorf("Stats.ValueJSONSize = %d, want > 0", st.ValueJSONSize)
	}
}

// TestValuesCache_NilStore_NoOps verifies that every public method on a nil
// *ValuesCacheStore is a safe no-op: no panic and no error returned.
func TestValuesCache_NilStore_NoOps(t *testing.T) {
	t.Parallel()
	var s *ValuesCacheStore
	ctx := context.Background()
	now := nowMS()

	if err := s.SaveValue(ctx, "c", "i", "ch:0", "P", 1, now, now); err != nil {
		t.Errorf("nil SaveValue: %v", err)
	}
	if err := s.SaveBatch(ctx, []SaveEntry{{
		CentralName: "c", InterfaceID: "i",
		ChannelAddress: "ch:0", ParameterName: "P", Value: 1,
		LastSeenAt: now, LastChangedAt: now,
	}}); err != nil {
		t.Errorf("nil SaveBatch: %v", err)
	}
	if err := s.DeleteDevice(ctx, "c", "i", "dev"); err != nil {
		t.Errorf("nil DeleteDevice: %v", err)
	}
	if err := s.DeleteChannel(ctx, "c", "i", "ch:0"); err != nil {
		t.Errorf("nil DeleteChannel: %v", err)
	}
	if err := s.DeleteAll(ctx); err != nil {
		t.Errorf("nil DeleteAll: %v", err)
	}
	got, err := s.LoadChannel(ctx, "c", "i", "ch:0")
	if err != nil {
		t.Errorf("nil LoadChannel: %v", err)
	}
	if got != nil {
		t.Error("nil LoadChannel: want nil slice")
	}
	all, err := s.LoadAll(ctx)
	if err != nil {
		t.Errorf("nil LoadAll: %v", err)
	}
	if all != nil {
		t.Error("nil LoadAll: want nil map")
	}
	res, err := s.GCDeadRows(ctx, map[string]struct{}{})
	if err != nil {
		t.Errorf("nil GCDeadRows: %v", err)
	}
	if res.Scanned != 0 || res.Deleted != 0 {
		t.Errorf("nil GCDeadRows: got %+v, want zero", res)
	}
	if _, err := s.Stats(ctx); err != nil {
		t.Errorf("nil Stats: %v", err)
	}
}

// TestValuesCache_MultiCentral_Isolated verifies that two centrals writing to
// the same (interface, channel, parameter) key do not share state: LoadChannel
// for each central returns only its own value.
func TestValuesCache_MultiCentral_Isolated(t *testing.T) {
	t.Parallel()
	s := freshValuesCacheStore(t)
	ctx := context.Background()
	now := nowMS()

	const (
		iface = "HmIP-RF"
		ch    = "SHARED:1"
		param = "TEMP"
	)

	if err := s.SaveValue(ctx, "ccu1", iface, ch, param, float64(21), now, now); err != nil {
		t.Fatalf("SaveValue ccu1: %v", err)
	}
	if err := s.SaveValue(ctx, "ccu2", iface, ch, param, float64(99), now, now); err != nil {
		t.Fatalf("SaveValue ccu2: %v", err)
	}

	load := func(centralName string) CachedValue {
		t.Helper()
		got, err := s.LoadChannel(ctx, centralName, iface, ch)
		if err != nil {
			t.Fatalf("LoadChannel %s: %v", centralName, err)
		}
		if len(got) != 1 {
			t.Fatalf("LoadChannel %s: got %d entries, want 1", centralName, len(got))
		}
		return got[0]
	}

	cv1 := load("ccu1")
	cv2 := load("ccu2")

	if cv1.Value == cv2.Value {
		t.Errorf("ccu1 and ccu2 share the same value %v — isolation broken", cv1.Value)
	}
}
