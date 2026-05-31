// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package dynamic

import (
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DataCacheEntry is one cached parameter value.
type DataCacheEntry struct {
	Value      any
	ModifiedAt time.Time
}

// DataCache keeps the latest VALUES-paramset value per DataPointKey.
// It is read by the event coordinator during replay and by the
// optimistic-set path to know what the CCU last acknowledged.
//
// Extended fields mirror the corresponding fields in
// Py).
type DataCache struct {
	mu    sync.RWMutex
	items map[hmtypes.DataPointKey]DataCacheEntry

	// _isInitializing — set true when a bulk-load (Load/AddData) is
	// in progress. Guarded by mu.
	isInitializing bool

	// _refreshedAt per interface — time of last successful full
	// refresh per interface ID. Guarded by mu.
	refreshedAt map[string]time.Time
}

// NewDataCache returns an empty cache.
func NewDataCache() *DataCache {
	return &DataCache{
		items:       make(map[hmtypes.DataPointKey]DataCacheEntry),
		refreshedAt: make(map[string]time.Time),
	}
}

// Put stores a value. Zero-timestamp inputs default to time.Now().
func (c *DataCache) Put(key hmtypes.DataPointKey, value any, at time.Time) {
	if at.IsZero() {
		at = time.Now()
	}
	c.mu.Lock()
	c.items[key] = DataCacheEntry{Value: value, ModifiedAt: at}
	c.mu.Unlock()
}

// Get returns the cached entry and reports presence.
func (c *DataCache) Get(key hmtypes.DataPointKey) (DataCacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.items[key]
	return e, ok
}

// Len reports the current entry count.
func (c *DataCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Forget drops the entry for key.
func (c *DataCache) Forget(key hmtypes.DataPointKey) {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
}

// Keys returns every tracked key. Allocation-heavy — used by
// introspection endpoints and tests, not the hot path.
func (c *DataCache) Keys() []hmtypes.DataPointKey {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]hmtypes.DataPointKey, 0, len(c.items))
	for k := range c.items {
		out = append(out, k)
	}
	return out
}

// Cleanup removes every entry older than ttl. Returns the number of entries
// dropped.
func (c *DataCache) Cleanup(ttl time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ttl <= 0 {
		return 0
	}
	cutoff := time.Now().Add(-ttl)
	removed := 0
	for k, e := range c.items {
		if e.ModifiedAt.Before(cutoff) {
			delete(c.items, k)
			removed++
		}
	}
	return removed
}

// Clear drops every cached entry. Used during shutdown and integration-test
// reset.
func (c *DataCache) Clear() {
	c.mu.Lock()
	c.items = make(map[hmtypes.DataPointKey]DataCacheEntry)
	c.mu.Unlock()
}

// DataCacheStats is a snapshot of cache-level counters.
type DataCacheStats struct {
	// Size is the current number of entries in the cache.
	Size int
	// Name is the canonical cache name.
	Name string
}

// CacheName is the canonical name for the data cache.
const CacheName = "CENTRAL_DATA_CACHE"

// Name returns the canonical cache name.
func (c *DataCache) Name() string {
	return CacheName
}

// Stats snapshots the cache counters.
func (c *DataCache) Stats() DataCacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return DataCacheStats{Size: len(c.items), Name: CacheName}
}

// AddData bulk-loads all parameter values for a given interface. Each entry
// in allDeviceData maps a DataPointKey to its current value. The call sets
// isInitializing, performs a selective clear for the interface, inserts all
// entries, and then clears the initializing flag.
func (c *DataCache) AddData(interfaceID string, allDeviceData map[hmtypes.DataPointKey]any) {
	c.mu.Lock()
	c.isInitializing = true
	// Remove existing entries for this interface.
	for k := range c.items {
		if k.InterfaceID == interfaceID {
			delete(c.items, k)
		}
	}
	now := time.Now()
	for k, v := range allDeviceData {
		c.items[k] = DataCacheEntry{Value: v, ModifiedAt: now}
	}
	c.isInitializing = false
	c.refreshedAt[interfaceID] = now
	c.mu.Unlock()
}

// ClearInterface removes every entry belonging to interfaceID.
// When interfaceID is empty the call is a no-op.
func (c *DataCache) ClearInterface(interfaceID string) {
	if interfaceID == "" {
		return
	}
	c.mu.Lock()
	for k := range c.items {
		if k.InterfaceID == interfaceID {
			delete(c.items, k)
		}
	}
	delete(c.refreshedAt, interfaceID)
	c.mu.Unlock()
}

// Load signals that a pull-path data fetch is starting for interfaceID.
// It sets isInitializing to true so callers can gate on IsInitializing().
// The caller must call SetInitializationComplete() when the load is done.
func (c *DataCache) Load(interfaceID string) {
	c.mu.Lock()
	c.isInitializing = true
	// Clear existing entries for the interface so stale data is not served
	// while a fresh fetch is in-flight.
	for k := range c.items {
		if k.InterfaceID == interfaceID {
			delete(c.items, k)
		}
	}
	c.mu.Unlock()
}

// RefreshDataPointData marks every entry as needing a re-read by resetting
// the refreshedAt timestamps. After this call, callers that check RefreshedAt
// will see zero times and trigger a fresh pull.
func (c *DataCache) RefreshDataPointData() {
	c.mu.Lock()
	c.refreshedAt = make(map[string]time.Time)
	c.mu.Unlock()
}

// SetInitializationComplete clears the isInitializing flag and records the
// refresh timestamp for interfaceID.
func (c *DataCache) SetInitializationComplete(interfaceID string) {
	c.mu.Lock()
	c.isInitializing = false
	c.refreshedAt[interfaceID] = time.Now()
	c.mu.Unlock()
}

// IsInitializing reports whether a bulk load is currently in progress.
func (c *DataCache) IsInitializing() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isInitializing
}

// RefreshedAt returns the time of the last successful initialization
// for interfaceID, and reports whether a timestamp was recorded.
func (c *DataCache) RefreshedAt(interfaceID string) (time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.refreshedAt[interfaceID]
	return t, ok
}

// IsEmpty reports whether the cache contains no entries.
// In Go the cache is always synchronous so there is no MAX_CACHE_AGE
// file-backed lazy-expiry to replicate — the method simply checks the
// current entry count.
func (c *DataCache) IsEmpty() bool {
	c.mu.RLock()
	empty := len(c.items) == 0
	c.mu.RUnlock()
	return empty
}
