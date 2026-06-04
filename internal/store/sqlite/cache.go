// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// =============================================================================
// DataOperationResult
// =============================================================================

// DataOperationResult signals the outcome of a cache load or save operation.
type DataOperationResult string

// DataOperationResult values.
const (
	// DataOperationResultLoadSuccess indicates data was loaded successfully.
	DataOperationResultLoadSuccess DataOperationResult = "LOAD_SUCCESS"
	// DataOperationResultNoLoad indicates no data was found (first run).
	DataOperationResultNoLoad DataOperationResult = "NO_LOAD"
	// DataOperationResultLoadFail indicates a storage error during load.
	DataOperationResultLoadFail DataOperationResult = "LOAD_FAIL"
	// DataOperationResultVersionMismatch indicates the on-disk schema
	// is older than the current schema version; the cache should be
	// rebuilt from the CCU.
	DataOperationResultVersionMismatch DataOperationResult = "VERSION_MISMATCH"
	// DataOperationResultSaveSuccess indicates the data was persisted.
	DataOperationResultSaveSuccess DataOperationResult = "SAVE_SUCCESS"
	// DataOperationResultNoSave indicates no write was necessary
	// (content unchanged or caching disabled).
	DataOperationResultNoSave DataOperationResult = "NO_SAVE"
	// DataOperationResultSaveFail indicates a storage error during save.
	DataOperationResultSaveFail DataOperationResult = "SAVE_FAIL"
)

// =============================================================================
// PersistentCache
// =============================================================================

// Persister is the narrow I/O contract a PersistentCache delegates to
// for actual storage (SQLite row, JSON file, …). Callers supply
// concrete implementations; the cache itself is storage-agnostic.
//
// Mirrors the StorageProtocol role.
type Persister interface {
	// Load reads the previously persisted data. Returns (nil, nil)
	// when no persisted data exists yet.
	Load() (map[string]any, error)
	// Save writes data to storage.
	Save(data map[string]any) error
	// Flush ensures any in-progress delayed write completes immediately.
	Flush() error
}

// PersistentCache provides hash-based dirty tracking, optional
// debounced writes, and a shape-aligned load/save surface for caches
// that need to survive process restarts.
//
// It is the Go equivalent of
// (store/persistent/base.py:43) — adapted for Go's synchronous I/O
// model (no asyncio).
//
// Callers must implement [Persister] and pass it to [NewPersistentCache].
// The content map is managed by the caller; PersistentCache only tracks
// whether the content has changed since the last successful save.
type PersistentCache struct {
	mu sync.Mutex

	persister Persister

	// lastHashSaved is the SHA-256 hex of the content at the last
	// successful save. Empty string means "never saved".
	lastHashSaved string

	// delayTimer is the pending debounced write, or nil.
	delayTimer *time.Timer
}

// NewPersistentCache returns a cache wired to persister.
//
// loom:reachable:reason="constructed in central wiring for debounced persistence of master-values and device data"
func NewPersistentCache(persister Persister) *PersistentCache {
	return &PersistentCache{persister: persister}
}

// HasUnsavedChanges reports whether content has changed since the last
// successful save.
func (c *PersistentCache) HasUnsavedChanges(content map[string]any) bool {
	return contentHash(content) != c.lastHashSaved
}

// Load reads data from the underlying Persister and returns the content
// map plus a [DataOperationResult]. Returns nil content on NO_LOAD or
// any failure.
func (c *PersistentCache) Load() (map[string]any, DataOperationResult) {
	data, err := c.persister.Load()
	if err != nil {
		return nil, DataOperationResultLoadFail
	}
	if data == nil {
		return nil, DataOperationResultNoLoad
	}
	hash := contentHash(data)
	c.mu.Lock()
	c.lastHashSaved = hash
	c.mu.Unlock()
	return data, DataOperationResultLoadSuccess
}

// Save writes content to storage when it has changed since the last
// successful save. Returns [DataOperationResultNoSave] when content is
// unchanged (hash match).
func (c *PersistentCache) Save(content map[string]any) DataOperationResult {
	hash := contentHash(content)
	c.mu.Lock()
	if hash == c.lastHashSaved {
		c.mu.Unlock()
		return DataOperationResultNoSave
	}
	c.mu.Unlock()

	if err := c.persister.Save(content); err != nil {
		return DataOperationResultSaveFail
	}
	c.mu.Lock()
	c.lastHashSaved = hash
	c.mu.Unlock()
	return DataOperationResultSaveSuccess
}

// SaveDelayed schedules a debounced save. If called multiple times
// within delay, each call resets the timer so only one write occurs.
// Pass a content accessor func that returns the current content map at
// write time (not a snapshot at schedule time).
func (c *PersistentCache) SaveDelayed(contentFn func() map[string]any, delay time.Duration) {
	if delay <= 0 {
		delay = time.Second
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.delayTimer != nil {
		c.delayTimer.Stop()
	}
	c.delayTimer = time.AfterFunc(delay, func() {
		c.mu.Lock()
		c.delayTimer = nil
		c.mu.Unlock()
		content := contentFn()
		_ = c.Save(content)
	})
}

// Flush cancels any pending debounced write and immediately persists
// content. This is safe to call during shutdown.
func (c *PersistentCache) Flush(content map[string]any) error {
	c.mu.Lock()
	if c.delayTimer != nil {
		c.delayTimer.Stop()
		c.delayTimer = nil
	}
	c.mu.Unlock()

	if err := c.persister.Flush(); err != nil {
		return fmt.Errorf("cache: flush: %w", err)
	}
	// Persist content if it has changed.
	if res := c.Save(content); res == DataOperationResultSaveFail {
		return errors.New("cache: flush save: storage error")
	}
	return nil
}

// =============================================================================
// Internal helpers
// =============================================================================

// contentHash returns the SHA-256 hex of the JSON encoding of v.
func contentHash(v map[string]any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		// Non-serialisable content: return a constant so the cache
		// always thinks it has unsaved changes, which is safe.
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
