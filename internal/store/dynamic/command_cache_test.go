// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package dynamic

import (
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// CommandCacheStats
// ---------------------------------------------------------------------------

func TestCommandCacheStatsHitRate(t *testing.T) {
	t.Parallel()

	// Zero lookups → HitRate == 0.
	s := CommandCacheStats{}
	if s.HitRate() != 0 {
		t.Fatalf("HitRate()=%v want 0 for zero lookups", s.HitRate())
	}

	// 3 hits, 1 miss → 0.75.
	s = CommandCacheStats{Hits: 3, Misses: 1}
	if got := s.HitRate(); got != 0.75 {
		t.Fatalf("HitRate()=%v want 0.75", got)
	}

	// All misses → 0.0.
	s = CommandCacheStats{Hits: 0, Misses: 5}
	if got := s.HitRate(); got != 0 {
		t.Fatalf("HitRate()=%v want 0 (all misses)", got)
	}
}

func TestCommandCacheStatsTotalLookups(t *testing.T) {
	t.Parallel()
	s := CommandCacheStats{Hits: 3, Misses: 2}
	if got := s.TotalLookups(); got != 5 {
		t.Errorf("TotalLookups()=%d want 5", got)
	}
	s = CommandCacheStats{}
	if got := s.TotalLookups(); got != 0 {
		t.Errorf("TotalLookups() on zero stats=%d want 0", got)
	}
}

// ---------------------------------------------------------------------------
// CommandCache construction and basic operations
// ---------------------------------------------------------------------------

func TestCommandCacheLen(t *testing.T) {
	t.Parallel()

	c := NewCommandCache()
	if c.Len() != 0 {
		t.Fatalf("Len=%d want 0 for empty cache", c.Len())
	}

	c.Record(makeKey("P1"), 1)
	c.Record(makeKey("P2"), 2)
	if c.Len() != 2 {
		t.Fatalf("Len=%d want 2", c.Len())
	}
}

func TestCommandCacheCleanup(t *testing.T) {
	t.Parallel()

	now := time.Now()
	c := NewCommandCache()
	c.TTL = 5 * time.Second
	c.now = func() time.Time { return now }

	// Record two entries at time=now.
	c.Record(makeKey("X"), "old")
	c.Record(makeKey("Y"), "also-old")

	// Advance clock past TTL.
	c.now = func() time.Time { return now.Add(10 * time.Second) }

	// Record a fresh entry.
	c.Record(makeKey("Z"), "fresh")

	removed := c.Cleanup()
	if removed != 2 {
		t.Fatalf("Cleanup removed %d entries, want 2", removed)
	}
	if c.Len() != 1 {
		t.Fatalf("Len=%d after Cleanup, want 1 (fresh entry survives)", c.Len())
	}
}

func TestCommandCacheCleanupNothingExpired(t *testing.T) {
	t.Parallel()

	c := NewCommandCache()
	c.Record(makeKey("A"), 1)
	c.Record(makeKey("B"), 2)

	// All entries are fresh; TTL has not passed yet.
	if n := c.Cleanup(); n != 0 {
		t.Fatalf("Cleanup returned %d, want 0 (nothing expired)", n)
	}
}

func TestCommandCacheClear(t *testing.T) {
	t.Parallel()

	c := NewCommandCache()
	c.Record(makeKey("A"), true)
	c.Record(makeKey("B"), false)
	if c.Len() != 2 {
		t.Fatalf("pre-clear Len=%d want 2", c.Len())
	}

	c.Clear()
	if c.Len() != 0 {
		t.Fatalf("post-clear Len=%d want 0", c.Len())
	}
}

func TestCommandCacheStats(t *testing.T) {
	t.Parallel()

	now := time.Now()
	c := NewCommandCache()
	c.TTL = 5 * time.Second
	c.now = func() time.Time { return now }

	// One hit.
	c.Record(makeKey("HIT"), 42)
	c.IsEcho(makeKey("HIT"), 42) // hit — consumes entry

	// Two misses: key not present, and wrong value.
	c.IsEcho(makeKey("ABSENT"), 1) // key not present → miss
	c.Record(makeKey("WRONG"), "x")
	c.IsEcho(makeKey("WRONG"), "y") // wrong value → miss

	// One expiry miss.
	c.Record(makeKey("EXPIRED"), true)
	c.now = func() time.Time { return now.Add(10 * time.Second) }
	c.IsEcho(makeKey("EXPIRED"), true) // expired → miss

	s := c.Stats()
	if s.Hits != 1 {
		t.Errorf("Stats.Hits=%d want 1", s.Hits)
	}
	if s.Misses != 3 {
		t.Errorf("Stats.Misses=%d want 3", s.Misses)
	}
}

func TestCommandCacheEviction(t *testing.T) {
	t.Parallel()

	c := NewCommandCache()
	c.MaxSize = 2

	t0 := time.Now()
	c.now = func() time.Time { return t0 }
	c.Record(makeKey("FIRST"), 1) // oldest

	c.now = func() time.Time { return t0.Add(time.Millisecond) }
	c.Record(makeKey("SECOND"), 2)

	// Third record exceeds MaxSize — should evict FIRST.
	c.now = func() time.Time { return t0.Add(2 * time.Millisecond) }
	c.Record(makeKey("THIRD"), 3)

	if c.Len() != 2 {
		t.Fatalf("Len=%d after eviction, want 2", c.Len())
	}

	// FIRST must be gone (it was the oldest).
	if c.IsEcho(makeKey("FIRST"), 1) {
		t.Error("FIRST was not evicted — expected it to be the oldest entry removed")
	}

	s := c.Stats()
	if s.Evictions != 1 {
		t.Errorf("Evictions=%d want 1", s.Evictions)
	}
}

func TestCommandCacheIsEchoValueMismatch(t *testing.T) {
	t.Parallel()

	c := NewCommandCache()
	c.Record(makeKey("M"), "hello")

	// Different value → miss, entry must remain (not consumed).
	if c.IsEcho(makeKey("M"), "world") {
		t.Fatal("value mismatch must not be echo")
	}
	// Entry still present — correct value is still an echo.
	if !c.IsEcho(makeKey("M"), "hello") {
		t.Fatal("after value-mismatch miss, matching value must still be echo")
	}
}

func TestCommandCacheConcurrentRecordIsEcho(t *testing.T) {
	t.Parallel()

	c := NewCommandCache()
	const workers = 40

	var wg sync.WaitGroup
	wg.Add(workers)

	for i := range workers {
		i := i
		go func() {
			defer wg.Done()
			k := makeKey("P" + string(rune('A'+i%10)))
			c.Record(k, i)
			_ = c.IsEcho(k, i)
			_ = c.Len()
			_ = c.Stats()
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// CommandCache explicit stat recording
// ---------------------------------------------------------------------------

func TestCommandCacheRecordEviction(t *testing.T) {
	t.Parallel()
	c := NewCommandCache()
	c.RecordEviction(3)
	s := c.Stats()
	if s.Evictions != 3 {
		t.Errorf("Evictions=%d want 3", s.Evictions)
	}
	c.RecordEviction(0) // 0 defaults to 1
	s = c.Stats()
	if s.Evictions != 4 {
		t.Errorf("Evictions after RecordEviction(0)=%d want 4", s.Evictions)
	}
}

func TestCommandCacheRecordHit(t *testing.T) {
	t.Parallel()
	c := NewCommandCache()
	c.RecordHit()
	c.RecordHit()
	s := c.Stats()
	if s.Hits != 2 {
		t.Errorf("Hits=%d want 2 after 2 RecordHit calls", s.Hits)
	}
}

func TestCommandCacheRecordMiss(t *testing.T) {
	t.Parallel()
	c := NewCommandCache()
	c.RecordMiss()
	s := c.Stats()
	if s.Misses != 1 {
		t.Errorf("Misses=%d want 1 after RecordMiss", s.Misses)
	}
}

func TestCommandCacheResetStats(t *testing.T) {
	t.Parallel()
	c := NewCommandCache()
	c.RecordHit()
	c.RecordMiss()
	c.RecordEviction(2)
	c.ResetStats()
	s := c.Stats()
	if s.Hits != 0 || s.Misses != 0 || s.Evictions != 0 {
		t.Errorf("after ResetStats: %+v, want all zero counters", s)
	}
}

// ---------------------------------------------------------------------------
// CommandCache warning-logged flag
// ---------------------------------------------------------------------------

func TestCommandCacheWarningLoggedDefaultFalse(t *testing.T) {
	t.Parallel()
	c := NewCommandCache()
	if c.IsWarningLogged() {
		t.Error("IsWarningLogged must return false for freshly created CommandCache")
	}
}

func TestCommandCacheSetWarningLoggedTrue(t *testing.T) {
	t.Parallel()
	c := NewCommandCache()
	c.SetWarningLogged(true)
	if !c.IsWarningLogged() {
		t.Error("IsWarningLogged must return true after SetWarningLogged(true)")
	}
}

func TestCommandCacheSetWarningLoggedFalse(t *testing.T) {
	t.Parallel()
	c := NewCommandCache()
	c.SetWarningLogged(true)
	c.SetWarningLogged(false)
	if c.IsWarningLogged() {
		t.Error("IsWarningLogged must return false after SetWarningLogged(false)")
	}
}

func TestCommandCacheWarningLoggedReset(t *testing.T) {
	t.Parallel()
	c := NewCommandCache()
	c.SetWarningLogged(true)
	// Clear does not reset the warning flag — that is the caller's concern.
	c.Clear()
	// Flag should remain as set by the caller.
	if !c.IsWarningLogged() {
		t.Error("Clear must not reset the warningLogged flag")
	}
}

func TestCommandCacheWarningLoggedHysteresisConcurrent(t *testing.T) {
	t.Parallel()
	c := NewCommandCache()
	// Concurrent reads and writes must not race.
	done := make(chan struct{})
	go func() {
		for range 100 {
			c.SetWarningLogged(true)
			c.SetWarningLogged(false)
		}
		close(done)
	}()
	for range 100 {
		_ = c.IsWarningLogged()
	}
	<-done
}

// ---------------------------------------------------------------------------
// CommandCache extended API: GetLastValue, RemoveLastValueSend
// ---------------------------------------------------------------------------

func TestCommandCacheGetLastValueHit(t *testing.T) {
	t.Parallel()
	now := time.Now()
	c := NewCommandCache()
	c.now = func() time.Time { return now }

	k := makeDP("HmIP-RF", "ABC:1", "LEVEL")
	c.Record(k, 0.5)

	v, ok := c.GetLastValue(k, 5*time.Second)
	if !ok {
		t.Fatal("GetLastValue: expected hit")
	}
	if v != 0.5 {
		t.Fatalf("GetLastValue value=%v want 0.5", v)
	}
	// entry must NOT be consumed by GetLastValue
	v2, ok2 := c.GetLastValue(k, 5*time.Second)
	if !ok2 || v2 != 0.5 {
		t.Fatal("GetLastValue must not consume the entry")
	}
}

func TestCommandCacheGetLastValueExpired(t *testing.T) {
	t.Parallel()
	now := time.Now()
	c := NewCommandCache()
	c.now = func() time.Time { return now }

	k := makeDP("HmIP-RF", "ABC:1", "LEVEL")
	c.Record(k, 1)

	c.now = func() time.Time { return now.Add(10 * time.Second) }
	_, ok := c.GetLastValue(k, 5*time.Second)
	if ok {
		t.Fatal("GetLastValue: expired entry must return false")
	}
}

func TestCommandCacheGetLastValueMissing(t *testing.T) {
	t.Parallel()
	c := NewCommandCache()
	_, ok := c.GetLastValue(makeDP("HmIP-RF", "X:1", "STATE"), 5*time.Second)
	if ok {
		t.Fatal("GetLastValue: absent key must return false")
	}
}

func TestCommandCacheRemoveLastValueSendMatch(t *testing.T) {
	t.Parallel()
	now := time.Now()
	c := NewCommandCache()
	c.now = func() time.Time { return now }

	k := makeDP("HmIP-RF", "DEF:1", "STATE")
	c.Record(k, true)

	removed := c.RemoveLastValueSend(k, true, 5*time.Second)
	if !removed {
		t.Fatal("RemoveLastValueSend: match within TTL must return true")
	}
	if c.Len() != 0 {
		t.Fatal("entry must be deleted after RemoveLastValueSend match")
	}
}

func TestCommandCacheRemoveLastValueSendMismatch(t *testing.T) {
	t.Parallel()
	c := NewCommandCache()
	k := makeDP("HmIP-RF", "DEF:1", "STATE")
	c.Record(k, true)

	removed := c.RemoveLastValueSend(k, false, 5*time.Second)
	if removed {
		t.Fatal("RemoveLastValueSend: value mismatch must return false")
	}
	// entry must still be present
	if c.Len() != 1 {
		t.Fatal("entry must survive RemoveLastValueSend value mismatch")
	}
}

func TestCommandCacheRemoveLastValueSendExpired(t *testing.T) {
	t.Parallel()
	now := time.Now()
	c := NewCommandCache()
	c.now = func() time.Time { return now }

	k := makeDP("HmIP-RF", "DEF:1", "STATE")
	c.Record(k, 42)

	c.now = func() time.Time { return now.Add(20 * time.Second) }
	removed := c.RemoveLastValueSend(k, 42, 5*time.Second)
	if removed {
		t.Fatal("RemoveLastValueSend: expired entry must return false")
	}
	// expired entry should be cleaned up
	if c.Len() != 0 {
		t.Fatal("expired entry must be deleted on RemoveLastValueSend")
	}
}

// ---------------------------------------------------------------------------
// CommandCache combined-parameter registry
// ---------------------------------------------------------------------------

func TestCommandCacheAddCombinedParameter(t *testing.T) {
	t.Parallel()
	c := NewCommandCache()
	c.AddCombinedParameter("LEVEL", "COVER:1", "COMBINED_LEVEL")

	e, ok := c.GetCombinedParameter("LEVEL")
	if !ok {
		t.Fatal("GetCombinedParameter: registered parameter not found")
	}
	if e.channelAddress != "COVER:1" || e.combinedParam != "COMBINED_LEVEL" {
		t.Fatalf("GetCombinedParameter: entry=%+v", e)
	}
}

func TestCommandCacheGetCombinedParameterMissing(t *testing.T) {
	t.Parallel()
	c := NewCommandCache()
	_, ok := c.GetCombinedParameter("MISSING")
	if ok {
		t.Fatal("GetCombinedParameter: absent key must return false")
	}
}

// ---------------------------------------------------------------------------
// CommandCache put-paramset cache
// ---------------------------------------------------------------------------

func TestCommandCacheAddPutParamset(t *testing.T) {
	t.Parallel()
	now := time.Now()
	c := NewCommandCache()
	c.now = func() time.Time { return now }

	vals := map[string]any{"LEVEL": 0.8, "RAMP_TIME": 1.0}
	c.AddPutParamset("LIGHT:1", hmenum.ParamsetKeyValues, vals)

	got, ok := c.GetPutParamset("LIGHT:1", hmenum.ParamsetKeyValues, 5*time.Second)
	if !ok {
		t.Fatal("GetPutParamset: should find entry within TTL")
	}
	if got["LEVEL"] != 0.8 {
		t.Fatalf("GetPutParamset LEVEL=%v want 0.8", got["LEVEL"])
	}
}

func TestCommandCacheGetPutParamsetExpired(t *testing.T) {
	t.Parallel()
	now := time.Now()
	c := NewCommandCache()
	c.now = func() time.Time { return now }

	c.AddPutParamset("LIGHT:1", hmenum.ParamsetKeyValues, map[string]any{"LEVEL": 1.0})
	c.now = func() time.Time { return now.Add(30 * time.Second) }

	_, ok := c.GetPutParamset("LIGHT:1", hmenum.ParamsetKeyValues, 5*time.Second)
	if ok {
		t.Fatal("GetPutParamset: expired entry must return false")
	}
}

func TestCommandCacheGetPutParamsetMissing(t *testing.T) {
	t.Parallel()
	c := NewCommandCache()
	_, ok := c.GetPutParamset("NONE:1", hmenum.ParamsetKeyValues, 5*time.Second)
	if ok {
		t.Fatal("GetPutParamset: absent channel must return false")
	}
}

// ---------------------------------------------------------------------------
// looseEqual — all type arms
// ---------------------------------------------------------------------------

func TestLooseEqualTypeArms(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"int equal", int(5), int(5), true},
		{"int not equal", int(5), int(6), false},
		{"int32 equal", int32(7), int32(7), true},
		{"int32 not equal", int32(7), int32(8), false},
		{"int64 equal", int64(9), int64(9), true},
		{"int64 not equal", int64(9), int64(10), false},
		{"float64 equal", 1.5, 1.5, true},
		{"float64 not equal", 1.5, 2.5, false},
		{"string equal", "abc", "abc", true},
		{"string not equal", "abc", "xyz", false},
		{"bool equal", true, true, true},
		{"bool not equal", true, false, false},
		{"type mismatch int/float64", int(1), float64(1), false},
		{"unsupported type", []byte{1}, []byte{1}, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := looseEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("looseEqual(%v, %v)=%v want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
