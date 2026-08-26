// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// cache_error_lifecycle_test.go covers CacheCoordinator error-propagation
// and lifecycle scenarios not covered by cache_deep_test.go or
// cache_metrics_test.go.
//
// Covered here:
//   - LoadAll surfaces persister errors
//   - SaveAll surfaces persister errors
//   - Full cache lifecycle: load → set → save → clear
//   - SetDataCacheInitializationComplete idempotent
//   - SaveIfChanged is a no-op when cache is clean after load

package coordinators

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// errPersister is a fake [CachePersister] whose operations always fail.
type errPersister struct {
	loadErr error
	saveErr error
}

func (e *errPersister) LoadDataCache(_ context.Context) (map[hmtypes.DataPointKey]DataCacheEntry, error) {
	return nil, e.loadErr
}

func (e *errPersister) SaveDataCache(_ context.Context, _ map[hmtypes.DataPointKey]DataCacheEntry) error {
	return e.saveErr
}

// TestCacheLoadAllSurfacesPersisterError verifies that LoadAll propagates
// an error returned by the persister to the caller.
func TestCacheLoadAllSurfacesPersisterError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("load error")
	c := NewCacheCoordinator()
	c.SetPersister(&errPersister{loadErr: sentinel})

	err := c.LoadAll(context.Background())
	if err == nil {
		t.Fatal("LoadAll: expected error from persister, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("LoadAll: error=%v, want errors.Is(err, sentinel)", err)
	}
}

// TestCacheSaveAllSurfacesPersisterError verifies that SaveAll propagates
// an error returned by the persister to the caller.
func TestCacheSaveAllSurfacesPersisterError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("save error")
	c := NewCacheCoordinator()
	c.SetPersister(&errPersister{saveErr: sentinel})
	c.Set(dpKey("iface", "A:0", "P"), hmtypes.IntValue(1), "src")

	err := c.SaveAll(context.Background())
	if err == nil {
		t.Fatal("SaveAll: expected error from persister, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("SaveAll: error=%v, want errors.Is(err, sentinel)", err)
	}
}

// TestCacheFullLifecycle exercises load → set → save → clear in sequence
// and asserts that each step produces the expected cache state.
func TestCacheFullLifecycle(t *testing.T) {
	t.Parallel()

	k1 := dpKey("iface", "VCU001:1", "LEVEL")
	k2 := dpKey("iface", "VCU001:2", "STATE")

	// Pre-populate the persister with one stored entry.
	stored := map[hmtypes.DataPointKey]DataCacheEntry{
		k1: {Value: hmtypes.FloatValue(0.5), Source: "stored"},
	}
	fp := &fakePersister{data: stored}
	c := NewCacheCoordinator()
	c.SetPersister(fp)

	// Step 1: LoadAll — cache now has 1 entry.
	if err := c.LoadAll(context.Background()); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if c.Len() != 1 {
		t.Fatalf("after LoadAll: Len=%d, want 1", c.Len())
	}
	if e, ok := c.Get(k1); !ok || e.Value.Float != 0.5 {
		t.Fatalf("loaded entry mismatch: ok=%v value=%v", ok, e.Value)
	}

	// Step 2: Set a second entry — cache now has 2 entries and is dirty.
	c.Set(k2, hmtypes.BoolValue(true), "live")
	if c.Len() != 2 {
		t.Fatalf("after Set: Len=%d, want 2", c.Len())
	}

	// Step 3: SaveAll — persister receives both entries.
	if err := c.SaveAll(context.Background()); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	if fp.saves != 1 {
		t.Fatalf("SaveAll should call persister once, got %d", fp.saves)
	}
	if _, ok := fp.data[k2]; !ok {
		t.Fatal("persisted data should contain the set key k2")
	}

	// Step 4: ClearAll — cache is empty.
	c.ClearAll()
	if c.Len() != 0 {
		t.Fatalf("after ClearAll: Len=%d, want 0", c.Len())
	}
	// Counters reset as well.
	if c.MetricsDataCacheHits() != 0 {
		t.Fatalf("hits after ClearAll=%d, want 0", c.MetricsDataCacheHits())
	}
	if c.MetricsDataCacheMisses() != 0 {
		t.Fatalf("misses after ClearAll=%d, want 0", c.MetricsDataCacheMisses())
	}
	if c.MetricsDataCacheEvictions() != 0 {
		t.Fatalf("evictions after ClearAll=%d, want 0", c.MetricsDataCacheEvictions())
	}
}

// TestCacheInitializationCompleteIdempotent verifies that calling
// SetDataCacheInitializationComplete multiple times does not panic and
// that IsDataCacheInitializationComplete returns true after the first call.
func TestCacheInitializationCompleteIdempotent(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()
	if c.IsDataCacheInitializationComplete() {
		t.Fatal("should not be complete before first call")
	}

	c.SetDataCacheInitializationComplete()
	if !c.IsDataCacheInitializationComplete() {
		t.Fatal("should be complete after first call")
	}

	// Second call must be a no-op, not a panic.
	c.SetDataCacheInitializationComplete()
	if !c.IsDataCacheInitializationComplete() {
		t.Fatal("should remain complete after second call")
	}
}

// TestCacheSaveIfChangedCleanAfterLoad verifies that SaveIfChanged is a
// no-op immediately after LoadAll (cache is in clean state after load).
func TestCacheSaveIfChangedCleanAfterLoad(t *testing.T) {
	t.Parallel()

	stored := map[hmtypes.DataPointKey]DataCacheEntry{
		dpKey("iface", "A:1", "LEVEL"): {Value: hmtypes.FloatValue(1.0), Source: "s"},
	}
	fp := &fakePersister{data: stored}
	c := NewCacheCoordinator()
	c.SetPersister(fp)

	if err := c.LoadAll(context.Background()); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// After load the dirty flag is cleared; SaveIfChanged must skip the save.
	if err := c.SaveIfChanged(context.Background()); err != nil {
		t.Fatalf("SaveIfChanged: %v", err)
	}
	if fp.saves != 0 {
		t.Fatalf("SaveIfChanged after clean LoadAll must not call persister, got %d saves", fp.saves)
	}
}
