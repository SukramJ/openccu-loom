// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package dynamic

import (
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DefaultCommandCacheMaxSize is the upper bound on tracked entries when no
// explicit limit is configured.
const DefaultCommandCacheMaxSize = 1000

// CommandCacheStats is a snapshot of cache-level counters.
type CommandCacheStats struct {
	Hits      uint64
	Misses    uint64
	Evictions uint64
	Size      int
}

// HitRate returns the cache's hit ratio in [0,1]. Returns 0 when no
// lookups have happened yet.
func (s CommandCacheStats) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0
	}
	return float64(s.Hits) / float64(total)
}

// TotalLookups returns the total number of cache lookups (hits + misses).
func (s CommandCacheStats) TotalLookups() uint64 {
	return s.Hits + s.Misses
}

// CommandCache remembers the last value the daemon wrote to each
// DataPointKey. The event coordinator consults it to decide whether an
// incoming CCU event is an echo of the daemon's own command (suppress it) or
// a genuine state change (propagate).
//
// Entries expire after [TTL] — by default 5 seconds, long enough for the CCU
// to bounce the echo back under normal conditions. The cache also enforces a
// maximum size: when a [Record] would exceed [MaxSize], the oldest entry is
// evicted (LRU).
type CommandCache struct {
	TTL     time.Duration
	MaxSize int
	now     func() time.Time
	mu      sync.RWMutex
	items   map[hmtypes.DataPointKey]commandEntry

	hits      uint64
	misses    uint64
	evictions uint64

	// warningLogged is the hysteresis flag that prevents duplicate "cache at
	// capacity" log lines. Guarded by mu.
	warningLogged bool

	// combined-parameter registry. Guarded by mu.
	combinedParams map[string]CombinedParamEntry

	// put-paramset echo registry. Guarded by mu.
	putParamsets map[string]putParamsetEntry
}

type commandEntry struct {
	value any
	at    time.Time
}

// NewCommandCache constructs a cache with a 5-second TTL and the
// default [DefaultCommandCacheMaxSize] limit.
func NewCommandCache() *CommandCache {
	return &CommandCache{
		TTL:     5 * time.Second,
		MaxSize: DefaultCommandCacheMaxSize,
		now:     time.Now,
		items:   make(map[hmtypes.DataPointKey]commandEntry),
	}
}

// Record stores the value we just wrote. When [MaxSize] is exceeded
// the oldest entry is evicted.
func (c *CommandCache) Record(key hmtypes.DataPointKey, value any) {
	c.mu.Lock()
	c.items[key] = commandEntry{value: value, at: c.now()}
	if c.MaxSize > 0 && len(c.items) > c.MaxSize {
		c.evictOldestLocked()
	}
	c.mu.Unlock()
}

// IsEcho reports whether incoming value matches the recorded write
// and arrived within [TTL]. The matching entry is consumed on a hit
// — follow-up genuine updates fall through.
func (c *CommandCache) IsEcho(key hmtypes.DataPointKey, value any) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		c.misses++
		return false
	}
	if c.now().Sub(e.at) > c.TTL {
		delete(c.items, key)
		c.misses++
		return false
	}
	if !looseEqual(e.value, value) {
		c.misses++
		return false
	}
	delete(c.items, key)
	c.hits++
	return true
}

// Len reports the current entry count (for metrics).
func (c *CommandCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Cleanup removes every entry whose TTL has expired. Returns the number of
// entries dropped.
func (c *CommandCache) Cleanup() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	removed := 0
	for k, e := range c.items {
		if now.Sub(e.at) > c.TTL {
			delete(c.items, k)
			removed++
		}
	}
	return removed
}

// Clear drops every entry. Used during shutdown and integration-test
// reset.
func (c *CommandCache) Clear() {
	c.mu.Lock()
	c.items = make(map[hmtypes.DataPointKey]commandEntry)
	c.mu.Unlock()
}

// IsWarningLogged reports whether the cache-capacity warning has already been
// emitted for the current overload period.
func (c *CommandCache) IsWarningLogged() bool {
	c.mu.RLock()
	v := c.warningLogged
	c.mu.RUnlock()
	return v
}

// SetWarningLogged sets the cache-capacity warning hysteresis flag. Set to
// true after the first warning is emitted; reset to false when the cache
// drains below [MaxSize].
func (c *CommandCache) SetWarningLogged(v bool) {
	c.mu.Lock()
	c.warningLogged = v
	c.mu.Unlock()
}

// RecordEviction increments the eviction counter by count (≥1).
func (c *CommandCache) RecordEviction(count uint64) {
	if count == 0 {
		count = 1
	}
	c.mu.Lock()
	c.evictions += count
	c.mu.Unlock()
}

// RecordHit increments the hit counter by 1.
func (c *CommandCache) RecordHit() {
	c.mu.Lock()
	c.hits++
	c.mu.Unlock()
}

// RecordMiss increments the miss counter by 1.
func (c *CommandCache) RecordMiss() {
	c.mu.Lock()
	c.misses++
	c.mu.Unlock()
}

// ResetStats resets all hit/miss/eviction counters to zero.
func (c *CommandCache) ResetStats() {
	c.mu.Lock()
	c.hits = 0
	c.misses = 0
	c.evictions = 0
	c.mu.Unlock()
}

// Stats snapshots the cache counters.
func (c *CommandCache) Stats() CommandCacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return CommandCacheStats{
		Hits:      c.hits,
		Misses:    c.misses,
		Evictions: c.evictions,
		Size:      len(c.items),
	}
}

// evictOldestLocked deletes the entry with the oldest `at` timestamp.
// Caller must hold c.mu.
func (c *CommandCache) evictOldestLocked() {
	var oldestKey hmtypes.DataPointKey
	var oldestAt time.Time
	first := true
	for k, e := range c.items {
		if first || e.at.Before(oldestAt) {
			oldestKey = k
			oldestAt = e.at
			first = false
		}
	}
	if !first {
		delete(c.items, oldestKey)
		c.evictions++
	}
}

// GetLastValue returns the recorded value for key if it exists and its age is
// within maxAge. The second return value reports whether a valid entry was
// found. The entry is NOT consumed — use IsEcho to consume on match.
func (c *CommandCache) GetLastValue(key hmtypes.DataPointKey, maxAge time.Duration) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.items[key]
	if !ok {
		return nil, false
	}
	if c.now().Sub(e.at) > maxAge {
		return nil, false
	}
	return e.value, true
}

// RemoveLastValueSend removes the entry for key if the recorded value matches
// value and the entry is still within maxAge. Returns true when an entry was
// removed.
func (c *CommandCache) RemoveLastValueSend(key hmtypes.DataPointKey, value any, maxAge time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return false
	}
	if c.now().Sub(e.at) > maxAge {
		delete(c.items, key)
		return false
	}
	if !looseEqual(e.value, value) {
		return false
	}
	delete(c.items, key)
	return true
}

// putParamsetEntry groups a paramset write recorded via AddPutParamset.
type putParamsetEntry struct {
	values map[string]any
	at     time.Time
}

// CombinedParamEntry tracks which combined-parameter is derived from a
// (channelAddress, parameter) pair.
type CombinedParamEntry struct {
	channelAddress string
	combinedParam  string
}

// CommandCache extended fields for
// These are initialised lazily in AddCombinedParameter and AddPutParamset so
// that the zero-value NewCommandCache() retains its lean footprint.

// combinedParams maps a source parameter name to the combined-parameter
// descriptor. A nil map means no combined parameters have been registered.
//
// Guarded by CommandCache.mu.

// AddCombinedParameter registers that parameter on channelAddress is part of
// the combined parameter combinedParam. The coordinator uses this to route
// combined-DP writes back through the correct component parameter.
func (c *CommandCache) AddCombinedParameter(parameter, channelAddress, combinedParam string) {
	c.mu.Lock()
	if c.combinedParams == nil {
		c.combinedParams = make(map[string]CombinedParamEntry)
	}
	c.combinedParams[parameter] = CombinedParamEntry{
		channelAddress: channelAddress,
		combinedParam:  combinedParam,
	}
	c.mu.Unlock()
}

// GetCombinedParameter returns the combined-parameter descriptor for
// parameter, if one was registered via AddCombinedParameter.
func (c *CommandCache) GetCombinedParameter(parameter string) (CombinedParamEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.combinedParams == nil {
		return CombinedParamEntry{}, false
	}
	e, ok := c.combinedParams[parameter]
	return e, ok
}

// AddPutParamset records a full paramset write so the coordinator can detect
// echo events for all parameters in that write. Values is a shallow copy;
// the caller must not mutate it after the call.
func (c *CommandCache) AddPutParamset(channelAddress string, psKey hmenum.ParamsetKey, values map[string]any) {
	c.mu.Lock()
	if c.putParamsets == nil {
		c.putParamsets = make(map[string]putParamsetEntry)
	}
	mapKey := channelAddress + "|" + string(psKey)
	c.putParamsets[mapKey] = putParamsetEntry{values: values, at: c.now()}
	c.mu.Unlock()
}

// GetPutParamset returns the most recently recorded paramset write for
// (channelAddress, psKey) if it is still within maxAge. The second return
// value reports whether a valid entry was found.
func (c *CommandCache) GetPutParamset(channelAddress string, psKey hmenum.ParamsetKey, maxAge time.Duration) (map[string]any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.putParamsets == nil {
		return nil, false
	}
	mapKey := channelAddress + "|" + string(psKey)
	e, ok := c.putParamsets[mapKey]
	if !ok {
		return nil, false
	}
	if c.now().Sub(e.at) > maxAge {
		return nil, false
	}
	return e.values, true
}

// looseEqual is strict-equal for the primitive types CCU values take
// after normalisation. It deliberately does not try to bridge int ↔
// float64 — echo suppression must be conservative.
func looseEqual(a, b any) bool {
	switch va := a.(type) {
	case bool:
		vb, ok := b.(bool)
		return ok && va == vb
	case int:
		vb, ok := b.(int)
		return ok && va == vb
	case int32:
		vb, ok := b.(int32)
		return ok && va == vb
	case int64:
		vb, ok := b.(int64)
		return ok && va == vb
	case float64:
		vb, ok := b.(float64)
		return ok && va == vb
	case string:
		vb, ok := b.(string)
		return ok && va == vb
	}
	return false
}
