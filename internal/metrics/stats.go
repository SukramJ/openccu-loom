// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import (
	"math"
	"sync"
)

// SizeOnlyStats tracks size and eviction count for registries and trackers
// that have no hit/miss semantics (e.g. device descriptions, ping-pong tracker).
type SizeOnlyStats struct {
	mu        sync.RWMutex
	Size      int
	Evictions int
}

// Snapshot returns an immutable copy.
func (s *SizeOnlyStats) Snapshot() SizeOnlySnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return SizeOnlySnapshot{Size: s.Size, Evictions: s.Evictions}
}

// SetSize sets the current entry count.
func (s *SizeOnlyStats) SetSize(n int) {
	s.mu.Lock()
	s.Size = n
	s.mu.Unlock()
}

// RecordEviction increments the eviction counter.
func (s *SizeOnlyStats) RecordEviction() {
	s.mu.Lock()
	s.Evictions++
	s.mu.Unlock()
}

// Reset zeroes all fields.
func (s *SizeOnlyStats) Reset() {
	s.mu.Lock()
	s.Size = 0
	s.Evictions = 0
	s.mu.Unlock()
}

// SizeOnlySnapshot is an immutable point-in-time copy of SizeOnlyStats.
type SizeOnlySnapshot struct {
	Size      int
	Evictions int
}

// CacheStats tracks hit/miss/size/eviction counts for true caches.
type CacheStats struct {
	mu        sync.RWMutex
	Hits      int
	Misses    int
	Size      int
	Evictions int
}

// HitRate returns the hit rate in percent (100.0 if no samples).
func (c *CacheStats) HitRate() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	total := c.Hits + c.Misses
	if total == 0 {
		return 100.0
	}
	return float64(c.Hits) / float64(total) * 100.0
}

// RecordHit increments the hit counter.
func (c *CacheStats) RecordHit() {
	c.mu.Lock()
	c.Hits++
	c.mu.Unlock()
}

// RecordMiss increments the miss counter.
func (c *CacheStats) RecordMiss() {
	c.mu.Lock()
	c.Misses++
	c.mu.Unlock()
}

// SetSize sets the current entry count.
func (c *CacheStats) SetSize(n int) {
	c.mu.Lock()
	c.Size = n
	c.mu.Unlock()
}

// RecordEviction increments the eviction counter.
func (c *CacheStats) RecordEviction() {
	c.mu.Lock()
	c.Evictions++
	c.mu.Unlock()
}

// Reset zeroes all fields.
func (c *CacheStats) Reset() {
	c.mu.Lock()
	c.Hits = 0
	c.Misses = 0
	c.Size = 0
	c.Evictions = 0
	c.mu.Unlock()
}

// Snapshot returns an immutable copy.
func (c *CacheStats) Snapshot() CacheStatsSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return CacheStatsSnapshot{Hits: c.Hits, Misses: c.Misses, Size: c.Size, Evictions: c.Evictions}
}

// CacheStatsSnapshot is an immutable point-in-time copy of CacheStats.
type CacheStatsSnapshot struct {
	Hits      int
	Misses    int
	Size      int
	Evictions int
}

// HitRate returns hit rate in percent (100.0 if no samples).
func (s CacheStatsSnapshot) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 100.0
	}
	return float64(s.Hits) / float64(total) * 100.0
}

// LatencyStats tracks latency sample count, sum, min, and max.
type LatencyStats struct {
	mu      sync.RWMutex
	Count   int
	TotalMs float64
	MinMs   float64
	MaxMs   float64
}

// NewLatencyStats initialises a LatencyStats with MinMs = +Inf so the first
// sample is always recorded correctly.
func NewLatencyStats() *LatencyStats {
	return &LatencyStats{MinMs: math.Inf(1)}
}

// Record adds a latency sample.
func (l *LatencyStats) Record(durationMs float64) {
	l.mu.Lock()
	l.Count++
	l.TotalMs += durationMs
	if durationMs < l.MinMs {
		l.MinMs = durationMs
	}
	if durationMs > l.MaxMs {
		l.MaxMs = durationMs
	}
	l.mu.Unlock()
}

// AvgMs returns the arithmetic mean (0 if no samples).
func (l *LatencyStats) AvgMs() float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.Count == 0 {
		return 0.0
	}
	return l.TotalMs / float64(l.Count)
}

// Reset zeroes all fields (MinMs reset to +Inf).
func (l *LatencyStats) Reset() {
	l.mu.Lock()
	l.Count = 0
	l.TotalMs = 0
	l.MinMs = math.Inf(1)
	l.MaxMs = 0
	l.mu.Unlock()
}

// Snapshot returns an immutable copy.
func (l *LatencyStats) Snapshot() LatencyStatsSnapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return LatencyStatsSnapshot{Count: l.Count, TotalMs: l.TotalMs, MinMs: l.MinMs, MaxMs: l.MaxMs}
}

// LatencyStatsSnapshot is an immutable point-in-time copy of LatencyStats.
type LatencyStatsSnapshot struct {
	Count   int
	TotalMs float64
	MinMs   float64
	MaxMs   float64
}

// AvgMs returns the arithmetic mean (0 if no samples).
func (s LatencyStatsSnapshot) AvgMs() float64 {
	if s.Count == 0 {
		return 0.0
	}
	return s.TotalMs / float64(s.Count)
}

// ServiceStats tracks per-method service call statistics.
type ServiceStats struct {
	mu              sync.RWMutex
	CallCount       int
	ErrorCount      int
	TotalDurationMs float64
	MaxDurationMs   float64
}

// Record adds a service call sample.
func (s *ServiceStats) Record(durationMs float64, hadError bool) {
	s.mu.Lock()
	s.CallCount++
	s.TotalDurationMs += durationMs
	if durationMs > s.MaxDurationMs {
		s.MaxDurationMs = durationMs
	}
	if hadError {
		s.ErrorCount++
	}
	s.mu.Unlock()
}

// AvgDurationMs returns the arithmetic mean (0 if no calls).
func (s *ServiceStats) AvgDurationMs() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.CallCount == 0 {
		return 0.0
	}
	return s.TotalDurationMs / float64(s.CallCount)
}

// ErrorRate returns the error rate in percent.
func (s *ServiceStats) ErrorRate() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.CallCount == 0 {
		return 0.0
	}
	return float64(s.ErrorCount) / float64(s.CallCount) * 100.0
}

// Reset zeroes all fields.
func (s *ServiceStats) Reset() {
	s.mu.Lock()
	s.CallCount = 0
	s.ErrorCount = 0
	s.TotalDurationMs = 0
	s.MaxDurationMs = 0
	s.mu.Unlock()
}

// Snapshot returns an immutable copy.
func (s *ServiceStats) Snapshot() ServiceStatsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ServiceStatsSnapshot{
		CallCount:       s.CallCount,
		ErrorCount:      s.ErrorCount,
		TotalDurationMs: s.TotalDurationMs,
		MaxDurationMs:   s.MaxDurationMs,
	}
}

// ServiceStatsSnapshot is an immutable point-in-time copy of ServiceStats.
type ServiceStatsSnapshot struct {
	CallCount       int
	ErrorCount      int
	TotalDurationMs float64
	MaxDurationMs   float64
}

// AvgDurationMs returns the arithmetic mean (0 if no calls).
func (s ServiceStatsSnapshot) AvgDurationMs() float64 {
	if s.CallCount == 0 {
		return 0.0
	}
	return s.TotalDurationMs / float64(s.CallCount)
}

// ErrorRate returns the error rate in percent.
func (s ServiceStatsSnapshot) ErrorRate() float64 {
	if s.CallCount == 0 {
		return 0.0
	}
	return float64(s.ErrorCount) / float64(s.CallCount) * 100.0
}
