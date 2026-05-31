// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import (
	"math"
	"sync"
	"testing"
)

// TestSizeOnlyStats covers basic accounting.
func TestSizeOnlyStats(t *testing.T) {
	t.Parallel()

	s := &SizeOnlyStats{}
	s.SetSize(10)
	s.RecordEviction()
	s.RecordEviction()

	snap := s.Snapshot()
	if snap.Size != 10 {
		t.Errorf("size=%d, want 10", snap.Size)
	}
	if snap.Evictions != 2 {
		t.Errorf("evictions=%d, want 2", snap.Evictions)
	}

	s.Reset()
	snap2 := s.Snapshot()
	if snap2.Size != 0 || snap2.Evictions != 0 {
		t.Error("reset failed")
	}
}

// TestCacheStats covers hit/miss tracking.
func TestCacheStats(t *testing.T) {
	t.Parallel()

	c := &CacheStats{}
	c.RecordHit()
	c.RecordHit()
	c.RecordMiss()

	if got := c.HitRate(); math.Abs(got-66.66666666666667) > 0.001 {
		t.Errorf("hit rate=%f", got)
	}

	snap := c.Snapshot()
	if snap.Hits != 2 || snap.Misses != 1 {
		t.Errorf("snapshot mismatch: %+v", snap)
	}
}

// TestCacheStatsEmpty verifies 100% hit rate when no samples recorded.
func TestCacheStatsEmpty(t *testing.T) {
	t.Parallel()
	c := &CacheStats{}
	if c.HitRate() != 100.0 {
		t.Error("empty cache should report 100% hit rate")
	}
}

// TestLatencyStats covers min/max/avg.
func TestLatencyStats(t *testing.T) {
	t.Parallel()

	l := NewLatencyStats()
	l.Record(10)
	l.Record(20)
	l.Record(30)

	snap := l.Snapshot()
	if snap.Count != 3 {
		t.Errorf("count=%d", snap.Count)
	}
	if snap.AvgMs() != 20 {
		t.Errorf("avg=%f", snap.AvgMs())
	}
	if snap.MaxMs != 30 {
		t.Errorf("max=%f", snap.MaxMs)
	}
	if snap.MinMs != 10 {
		t.Errorf("min=%f", snap.MinMs)
	}
}

// TestLatencyStatsEmpty verifies zero AvgMs when no samples.
func TestLatencyStatsEmpty(t *testing.T) {
	t.Parallel()
	l := NewLatencyStats()
	if l.AvgMs() != 0 {
		t.Error("empty latency should return 0 avg")
	}
}

// TestServiceStats covers call/error recording.
func TestServiceStats(t *testing.T) {
	t.Parallel()

	s := &ServiceStats{}
	s.Record(50, false)
	s.Record(100, true)

	if s.AvgDurationMs() != 75 {
		t.Errorf("avg=%f", s.AvgDurationMs())
	}
	if math.Abs(s.ErrorRate()-50) > 0.001 {
		t.Errorf("error rate=%f", s.ErrorRate())
	}

	snap := s.Snapshot()
	if snap.CallCount != 2 || snap.ErrorCount != 1 {
		t.Errorf("snapshot: %+v", snap)
	}
}

// TestCacheStatsRace checks that concurrent access is race-free.
func TestCacheStatsRace(t *testing.T) {
	t.Parallel()

	c := &CacheStats{}
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				c.RecordHit()
			} else {
				c.RecordMiss()
			}
			_ = c.HitRate()
			_ = c.Snapshot()
		}(i)
	}
	wg.Wait()

	snap := c.Snapshot()
	if snap.Hits+snap.Misses != 50 {
		t.Errorf("total samples=%d, want 50", snap.Hits+snap.Misses)
	}
}

// TestLatencyStatsRace checks concurrent Record/AvgMs is race-free.
func TestLatencyStatsRace(t *testing.T) {
	t.Parallel()

	l := NewLatencyStats()
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l.Record(float64(i))
			_ = l.AvgMs()
			_ = l.Snapshot()
		}(i)
	}
	wg.Wait()

	snap := l.Snapshot()
	if snap.Count != 100 {
		t.Errorf("count=%d, want 100", snap.Count)
	}
}

// TestServiceStatsRace checks concurrent Record is race-free.
func TestServiceStatsRace(t *testing.T) {
	t.Parallel()

	s := &ServiceStats{}
	var wg sync.WaitGroup
	for i := range 60 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Record(float64(i), i%3 == 0)
			_ = s.AvgDurationMs()
			_ = s.ErrorRate()
			_ = s.Snapshot()
		}(i)
	}
	wg.Wait()

	snap := s.Snapshot()
	if snap.CallCount != 60 {
		t.Errorf("call_count=%d, want 60", snap.CallCount)
	}
}

// ---------------------------------------------------------------------------
// CacheStats — SetSize, RecordEviction, Reset
// ---------------------------------------------------------------------------

func TestCacheStatsSetSizeRecordEvictionReset(t *testing.T) {
	t.Parallel()
	c := &CacheStats{}
	c.SetSize(10)
	c.RecordEviction()
	c.RecordEviction()
	snap := c.Snapshot()
	if snap.Size != 10 || snap.Evictions != 2 {
		t.Errorf("snap=%+v, want Size=10 Evictions=2", snap)
	}
	c.Reset()
	snap2 := c.Snapshot()
	if snap2.Size != 0 || snap2.Evictions != 0 {
		t.Errorf("after reset snap=%+v", snap2)
	}
}

// TestCacheStatsSnapshotHitRate verifies HitRate on a CacheStatsSnapshot value.
func TestCacheStatsSnapshotHitRate(t *testing.T) {
	t.Parallel()
	snap := CacheStatsSnapshot{Hits: 6, Misses: 4}
	if math.Abs(snap.HitRate()-60) > 0.001 {
		t.Errorf("HitRate=%f, want 60", snap.HitRate())
	}
	// empty snapshot
	empty := CacheStatsSnapshot{}
	if empty.HitRate() != 100.0 {
		t.Error("empty snapshot HitRate must be 100")
	}
}

// ---------------------------------------------------------------------------
// LatencyStats — Reset
// ---------------------------------------------------------------------------

func TestLatencyStatsReset(t *testing.T) {
	t.Parallel()
	l := NewLatencyStats()
	l.Record(15)
	l.Record(25)
	l.Reset()
	if l.AvgMs() != 0 {
		t.Errorf("AvgMs after reset=%f, want 0", l.AvgMs())
	}
	snap := l.Snapshot()
	if snap.Count != 0 || snap.TotalMs != 0 || !math.IsInf(snap.MinMs, 1) {
		t.Errorf("snapshot after reset=%+v", snap)
	}
}

// ---------------------------------------------------------------------------
// ServiceStats — Reset and ServiceStatsSnapshot methods
// ---------------------------------------------------------------------------

func TestServiceStatsReset(t *testing.T) {
	t.Parallel()
	s := &ServiceStats{}
	s.Record(100, true)
	s.Reset()
	snap := s.Snapshot()
	if snap.CallCount != 0 || snap.ErrorCount != 0 {
		t.Errorf("snapshot after reset=%+v", snap)
	}
	if s.AvgDurationMs() != 0 || s.ErrorRate() != 0 {
		t.Error("AvgDurationMs/ErrorRate after reset must be 0")
	}
}

func TestServiceStatsSnapshotMethods(t *testing.T) {
	t.Parallel()
	snap := ServiceStatsSnapshot{CallCount: 4, ErrorCount: 2, TotalDurationMs: 100, MaxDurationMs: 40}
	if snap.AvgDurationMs() != 25 {
		t.Errorf("AvgDurationMs=%f, want 25", snap.AvgDurationMs())
	}
	if snap.ErrorRate() != 50 {
		t.Errorf("ErrorRate=%f, want 50", snap.ErrorRate())
	}

	// zero-call variants
	z := ServiceStatsSnapshot{}
	if z.AvgDurationMs() != 0 || z.ErrorRate() != 0 {
		t.Error("zero-call snapshot methods must return 0")
	}
}
