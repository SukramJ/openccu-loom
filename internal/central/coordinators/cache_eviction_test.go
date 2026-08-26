// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests covering CacheCoordinator eviction semantics, ClearAll counter
// reset, ClearAll event emission, and LoadAll persister handling.

package coordinators

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// makeCacheKey builds a DataPointKey scoped to a single VALUES paramset.
func makeCacheKey(iface, channel, param string) hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		InterfaceID:    iface,
		ChannelAddress: channel,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      param,
	}
}

// ── ported tests ──────────────────────────────────────────────────────────────

// TestCacheCoordinatorEvictDevicePartialMatch mirrors the semantics of
// test_remove_device_from_caches: a device eviction must remove the exact-address
// entry and all "DEVICE:N" channel entries while leaving unrelated keys intact.
//
// Python test: TestCacheCoordinatorDeviceRemoval.test_remove_device_from_caches
// (test_central_cache_coordinator.py:369).
func TestCacheCoordinatorEvictDevicePartialMatch(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()

	device := "VCU0000001"
	ch1 := makeCacheKey("iface", device+":0", "STATE")
	ch2 := makeCacheKey("iface", device+":1", "LEVEL")
	unrelated := makeCacheKey("iface", "VCU9999999:0", "TEMP")

	c.Set(ch1, hmtypes.BoolValue(true), "src")
	c.Set(ch2, hmtypes.FloatValue(0.5), "src")
	c.Set(unrelated, hmtypes.FloatValue(21.0), "src")

	if c.Len() != 3 {
		t.Fatalf("pre-evict: want 3 entries, got %d", c.Len())
	}

	// Trigger the same eviction logic exercised by DeviceRemovedEvent.
	c.evictDevice(device)

	if c.Len() != 1 {
		t.Fatalf("post-evict: want 1 entry (unrelated), got %d", c.Len())
	}
	if _, ok := c.Get(unrelated); !ok {
		t.Fatal("unrelated key must survive device eviction")
	}
	if _, ok := c.Get(ch1); ok {
		t.Fatalf("channel key %v must be evicted", ch1)
	}
	if _, ok := c.Get(ch2); ok {
		t.Fatalf("channel key %v must be evicted", ch2)
	}
}

// TestCacheCoordinatorEvictDeviceEmptyAddressIsNoop verifies the guard clause:
// evictDevice("") must not touch any entries.
//
// Python analog: implicit — remove_device_from_caches always passes a non-empty
// address; this covers the guard in Go's implementation.
func TestCacheCoordinatorEvictDeviceEmptyAddressIsNoop(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()
	key := makeCacheKey("iface", "VCU0000001:0", "STATE")
	c.Set(key, hmtypes.BoolValue(true), "src")

	c.evictDevice("") // must be a no-op

	if c.Len() != 1 {
		t.Fatalf("evictDevice(\"\") must not remove entries; got Len=%d", c.Len())
	}
}

// TestCacheCoordinatorClearAllResetsCounters mirrors test_clear_all:
// after ClearAll() the entry map is empty and all metric counters are zero.
//
// Python test: TestCacheCoordinatorClearOperations.test_clear_all
// (test_central_cache_coordinator.py:179).
func TestCacheCoordinatorClearAllResetsCounters(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()
	k1 := makeCacheKey("iface", "A:0", "LEVEL")
	k2 := makeCacheKey("iface", "B:1", "STATE")

	c.Set(k1, hmtypes.FloatValue(0.5), "src")
	c.Set(k2, hmtypes.BoolValue(true), "src")
	_, _ = c.Get(k1)                                       // hit
	_, _ = c.Get(makeCacheKey("iface", "NOTEXIST:0", "X")) // miss

	if c.MetricsDataCacheSize() != 2 {
		t.Fatalf("pre-clear: want size=2, got %d", c.MetricsDataCacheSize())
	}
	if c.MetricsDataCacheHits() != 1 {
		t.Fatalf("pre-clear: want hits=1, got %d", c.MetricsDataCacheHits())
	}
	if c.MetricsDataCacheMisses() != 1 {
		t.Fatalf("pre-clear: want misses=1, got %d", c.MetricsDataCacheMisses())
	}

	c.ClearAll()

	if c.MetricsDataCacheSize() != 0 {
		t.Fatalf("post-clear: want size=0, got %d", c.MetricsDataCacheSize())
	}
	if c.MetricsDataCacheHits() != 0 {
		t.Fatalf("post-clear: want hits=0, got %d", c.MetricsDataCacheHits())
	}
	if c.MetricsDataCacheMisses() != 0 {
		t.Fatalf("post-clear: want misses=0, got %d", c.MetricsDataCacheMisses())
	}
	if c.MetricsDataCacheEvictions() != 0 {
		t.Fatalf("post-clear: want evictions=0, got %d", c.MetricsDataCacheEvictions())
	}
	if _, ok := c.Get(k1); ok {
		t.Fatal("ClearAll must remove all entries — Get returned ok=true after clear")
	}
}

// TestCacheCoordinatorClearAllEmitsInvalidatedEvent verifies that ClearAll
// publishes a CacheInvalidatedEvent on the wired bus carrying the central
// name, the data-cache type, the manual reason, and the count of evicted
// entries.
func TestCacheCoordinatorClearAllEmitsInvalidatedEvent(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()
	bus := events.NewBus()
	c.SubscribeToBus(bus)
	c.SetCentralName("ccu1")

	var (
		mu      sync.Mutex
		caught  []hmevent.CacheInvalidatedEvent
		release = events.Subscribe(bus, func(e hmevent.CacheInvalidatedEvent) {
			mu.Lock()
			caught = append(caught, e)
			mu.Unlock()
		})
	)
	defer release()

	c.Set(makeCacheKey("iface", "A:0", "LEVEL"), hmtypes.FloatValue(0.5), "src")
	c.Set(makeCacheKey("iface", "B:1", "STATE"), hmtypes.BoolValue(true), "src")

	c.ClearAll()

	mu.Lock()
	defer mu.Unlock()
	if len(caught) != 1 {
		t.Fatalf("ClearAll must emit exactly one CacheInvalidatedEvent; got %d", len(caught))
	}
	got := caught[0]
	if got.CentralName != "ccu1" {
		t.Errorf("CentralName: got %q, want %q", got.CentralName, "ccu1")
	}
	if got.CacheType != hmenum.CacheTypeData {
		t.Errorf("CacheType: got %q, want %q", got.CacheType, hmenum.CacheTypeData)
	}
	if got.Reason != hmenum.CacheInvalidationReasonManual {
		t.Errorf("Reason: got %q, want %q", got.Reason, hmenum.CacheInvalidationReasonManual)
	}
	if got.EntriesAffected != 2 {
		t.Errorf("EntriesAffected: got %d, want 2", got.EntriesAffected)
	}
}

// TestCacheCoordinatorClearAllWithReasonShutdown verifies that
// ClearAllWithReason carries the explicit reason through the event,
// distinguishing operator-initiated clears from shutdown-driven ones.
func TestCacheCoordinatorClearAllWithReasonShutdown(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()
	bus := events.NewBus()
	c.SubscribeToBus(bus)
	c.SetCentralName("ccu1")

	var (
		mu     sync.Mutex
		caught []hmevent.CacheInvalidatedEvent
	)
	release := events.Subscribe(bus, func(e hmevent.CacheInvalidatedEvent) {
		mu.Lock()
		caught = append(caught, e)
		mu.Unlock()
	})
	defer release()

	c.ClearAllWithReason(hmenum.CacheInvalidationReasonShutdown)

	mu.Lock()
	defer mu.Unlock()
	if len(caught) != 1 || caught[0].Reason != hmenum.CacheInvalidationReasonShutdown {
		t.Fatalf("want exactly one Shutdown event; got %d events with reasons %+v", len(caught), caught)
	}
}

// TestCacheCoordinatorClearAllNilBusIsSafe verifies the no-bus fast-path:
// ClearAll without a wired bus must not panic and must still reset state.
func TestCacheCoordinatorClearAllNilBusIsSafe(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()
	c.Set(makeCacheKey("iface", "A:0", "LEVEL"), hmtypes.FloatValue(0.5), "src")

	c.ClearAll() // must not panic, no bus wired

	if c.Len() != 0 {
		t.Fatalf("ClearAll without bus must still clear entries; Len=%d", c.Len())
	}
}

// TestCacheCoordinatorLoadAllNoPersisterIsNoop mirrors test_load_all:
// LoadAll with no persister is a safe no-op and leaves the cache empty.
//
// Python test: TestCacheCoordinatorLoadOperations.test_load_all
// (test_central_cache_coordinator.py:243 — Python mocks all cache load methods;
// Go equivalent is the persister-nil fast-path).
func TestCacheCoordinatorLoadAllNoPersisterIsNoop(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()
	// No persister wired — LoadAll must be a no-op.
	if err := c.LoadAll(context.Background()); err != nil {
		t.Fatalf("LoadAll with nil persister must return nil, got: %v", err)
	}
	if c.Len() != 0 {
		t.Fatalf("LoadAll with nil persister must leave cache empty; Len=%d", c.Len())
	}
}

// TestCacheCoordinatorLoadAllPersisterError mirrors test_load_all_handles_exceptions:
// LoadAll propagates errors from the persister (no silent swallowing).
//
// Python test: TestCacheCoordinatorLoadOperations.test_load_all_handles_exceptions
// (test_central_cache_coordinator.py:272).
func TestCacheCoordinatorLoadAllPersisterError(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()

	sentinel := errors.New("load failure")
	fake := &fakeCachePersister{loadErr: sentinel}
	c.SetPersister(fake)

	err := c.LoadAll(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("LoadAll must propagate persister error; got %v", err)
	}
}

// fakeCachePersister is a minimal CachePersister for tests.
type fakeCachePersister struct {
	loadErr error
	saveErr error
	data    map[hmtypes.DataPointKey]DataCacheEntry
}

func (f *fakeCachePersister) LoadDataCache(_ context.Context) (map[hmtypes.DataPointKey]DataCacheEntry, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	return f.data, nil
}

func (f *fakeCachePersister) SaveDataCache(_ context.Context, _ map[hmtypes.DataPointKey]DataCacheEntry) error {
	return f.saveErr
}
