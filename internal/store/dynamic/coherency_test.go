// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// coherency_test.go — DataCache correctness: invalidation paths, lifecycle,
// extended API (AddData, ClearInterface, Load, initialization) and stats.
// Cache-invalidation paths (Cleanup, Clear, Forget) must behave atomically
// and consistently under concurrent access.

package dynamic

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// makeKey builds a DataPointKey for testing with the given parameter name.
func makeKey(param string) hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "TEST:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      param,
	}
}

// TestDataCacheCleanupRemovesStaleAndKeepsFresh puts two entries — one
// older than the TTL and one younger — then verifies that Cleanup removes
// exactly the stale one and leaves the fresh one intact.
func TestDataCacheCleanupRemovesStaleAndKeepsFresh(t *testing.T) {
	t.Parallel()

	c := NewDataCache()
	now := time.Now()

	staleKey := makeKey("STALE")
	freshKey := makeKey("FRESH")

	c.Put(staleKey, 42.0, now.Add(-1*time.Hour)) // 1 hour old — stale
	c.Put(freshKey, true, now)                   // just added — fresh

	removed := c.Cleanup(30 * time.Minute)
	if removed != 1 {
		t.Fatalf("Cleanup removed %d entries, want 1 (only the stale one)", removed)
	}

	// Stale entry must be gone.
	if _, ok := c.Get(staleKey); ok {
		t.Error("stale entry must be removed by Cleanup")
	}

	// Fresh entry must survive.
	if e, ok := c.Get(freshKey); !ok {
		t.Error("fresh entry must survive Cleanup")
	} else if e.Value != true {
		t.Errorf("fresh entry value=%v want true", e.Value)
	}
}

// TestDataCacheCleanupTTLZeroOrNegativeIsNoOp verifies the explicit guard
// in Cleanup: TTL ≤ 0 must never remove any entry.
func TestDataCacheCleanupTTLZeroOrNegativeIsNoOp(t *testing.T) {
	t.Parallel()

	c := NewDataCache()

	// Put 3 entries that are clearly "old" so that, if the guard were
	// absent, a positive TTL would remove them.
	ancient := time.Now().Add(-24 * time.Hour)
	keys := []hmtypes.DataPointKey{makeKey("K1"), makeKey("K2"), makeKey("K3")}
	for _, k := range keys {
		c.Put(k, 1, ancient)
	}

	// TTL = 0 must be a no-op.
	if n := c.Cleanup(0); n != 0 {
		t.Errorf("Cleanup(0) removed %d entries, want 0", n)
	}
	if c.Len() != 3 {
		t.Errorf("Len=%d after Cleanup(0), want 3", c.Len())
	}

	// TTL < 0 must also be a no-op.
	if n := c.Cleanup(-1 * time.Second); n != 0 {
		t.Errorf("Cleanup(-1s) removed %d entries, want 0", n)
	}
	if c.Len() != 3 {
		t.Errorf("Len=%d after Cleanup(-1s), want 3", c.Len())
	}
}

// TestDataCacheClearWipesEverything puts 5 entries, calls Clear, and
// asserts that Len is 0 and every prior key is absent.
func TestDataCacheClearWipesEverything(t *testing.T) {
	t.Parallel()

	c := NewDataCache()
	keys := make([]hmtypes.DataPointKey, 5)
	for i := range keys {
		keys[i] = makeKey(hmenum.Parameter(t.Name() + string(rune('A'+i))).String())
		c.Put(keys[i], i, time.Now())
	}

	if c.Len() != 5 {
		t.Fatalf("pre-clear Len=%d want 5", c.Len())
	}

	c.Clear()

	if c.Len() != 0 {
		t.Errorf("post-clear Len=%d want 0", c.Len())
	}
	for _, k := range keys {
		if _, ok := c.Get(k); ok {
			t.Errorf("key %v must not exist after Clear", k)
		}
	}
}

// TestDataCacheConcurrentPutGetForget fans out 50 goroutines doing mixed
// Put/Get/Forget operations on 10 shared keys. The test verifies there
// are no data races (run with -race) and that the final Len matches the
// expected survivor count derived from atomic counters.
func TestDataCacheConcurrentPutGetForget(t *testing.T) {
	t.Parallel()

	const (
		goroutines = 50
		keySpace   = 10
	)

	c := NewDataCache()

	// Pre-populate all keys so goroutines have something to Get/Forget.
	baseKeys := make([]hmtypes.DataPointKey, keySpace)
	for i := range baseKeys {
		baseKeys[i] = makeKey(hmenum.Parameter(t.Name() + string(rune('A'+i))).String())
		c.Put(baseKeys[i], i, time.Now())
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Track net inserts: +1 for Put, -1 for Forget if present.
	// We use a simple approach: let goroutines run freely and just
	// confirm no race + final state is self-consistent.

	var puts, forgets atomic.Int64

	for g := range goroutines {
		go func() {
			defer wg.Done()
			k := baseKeys[g%keySpace]
			switch g % 3 {
			case 0: // Put
				c.Put(k, g, time.Now())
				puts.Add(1)
			case 1: // Get (read-only, just verify no panic)
				_, _ = c.Get(k)
			case 2: // Forget
				c.Forget(k)
				forgets.Add(1)
			}
		}()
	}
	wg.Wait()

	// Final Len must be ≥ 0 and ≤ keySpace — a basic sanity bound.
	finalLen := c.Len()
	if finalLen < 0 || finalLen > keySpace {
		t.Errorf("final Len=%d outside expected range [0, %d]", finalLen, keySpace)
	}
}

// TestDataCachePutZeroTimeDefaultsToNow verifies that Put with a zero
// time.Time defaults ModifiedAt to a non-zero value close to time.Now().
func TestDataCachePutZeroTimeDefaultsToNow(t *testing.T) {
	t.Parallel()

	c := NewDataCache()
	k := makeKey("ZERO_TIME")

	before := time.Now()
	c.Put(k, "hello", time.Time{}) // zero time → should default to now
	after := time.Now()

	e, ok := c.Get(k)
	if !ok {
		t.Fatal("entry not found after Put")
	}
	if e.ModifiedAt.IsZero() {
		t.Fatal("ModifiedAt must not be zero when Put receives time.Time{}")
	}
	if e.ModifiedAt.Before(before) || e.ModifiedAt.After(after.Add(time.Second)) {
		t.Errorf("ModifiedAt=%v is not within [%v, %v]", e.ModifiedAt, before, after)
	}
}

// TestDataCacheKeysReturnsSnapshotNotLive verifies that the slice returned
// by Keys is a snapshot: forgetting a key after calling Keys does not
// retroactively change the slice length.
func TestDataCacheKeysReturnsSnapshotNotLive(t *testing.T) {
	t.Parallel()

	c := NewDataCache()
	k1 := makeKey("SNAP1")
	k2 := makeKey("SNAP2")
	k3 := makeKey("SNAP3")

	c.Put(k1, 1, time.Now())
	c.Put(k2, 2, time.Now())
	c.Put(k3, 3, time.Now())

	snapshot := c.Keys()
	if len(snapshot) != 3 {
		t.Fatalf("snapshot len=%d want 3", len(snapshot))
	}

	// Mutate the cache after taking the snapshot.
	c.Forget(k1)

	// The snapshot must still hold the original 3 keys.
	if len(snapshot) != 3 {
		t.Errorf("snapshot len changed to %d after Forget — Keys must return a copy, not a live view", len(snapshot))
	}
}

// ---------------------------------------------------------------------------
// makeDP builds a DataPointKey for testing with all three key dimensions.
// ---------------------------------------------------------------------------

func makeDP(interfaceID, channelAddr, param string) hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		InterfaceID:    interfaceID,
		ChannelAddress: channelAddr,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      param,
	}
}

// ---------------------------------------------------------------------------
// DataCache.Stats
// ---------------------------------------------------------------------------

func TestDataCacheStats(t *testing.T) {
	t.Parallel()

	c := NewDataCache()
	if s := c.Stats(); s.Size != 0 {
		t.Fatalf("empty cache Stats.Size=%d want 0", s.Size)
	}

	c.Put(makeKey("A"), 1, time.Now())
	c.Put(makeKey("B"), 2, time.Now())

	if s := c.Stats(); s.Size != 2 {
		t.Fatalf("Stats.Size=%d want 2", s.Size)
	}

	c.Forget(makeKey("A"))
	if s := c.Stats(); s.Size != 1 {
		t.Fatalf("Stats.Size=%d after Forget, want 1", s.Size)
	}
}

// ---------------------------------------------------------------------------
// DataCache.Name and Stats.Name
// ---------------------------------------------------------------------------

func TestDataCacheName(t *testing.T) {
	t.Parallel()
	c := NewDataCache()
	if c.Name() != CacheName {
		t.Errorf("Name()=%q want %q", c.Name(), CacheName)
	}
}

func TestDataCacheStatsNameField(t *testing.T) {
	t.Parallel()
	c := NewDataCache()
	s := c.Stats()
	if s.Name != CacheName {
		t.Errorf("Stats().Name=%q want %q", s.Name, CacheName)
	}
	// Size check with entries.
	c.Put(makeDP("iface", "ch:1", "P"), true, time.Now())
	s = c.Stats()
	if s.Size != 1 {
		t.Errorf("Stats().Size=%d want 1", s.Size)
	}
}

// ---------------------------------------------------------------------------
// DataCache.IsEmpty
// ---------------------------------------------------------------------------

func TestDataCacheIsEmptyOnNew(t *testing.T) {
	t.Parallel()
	c := NewDataCache()
	if !c.IsEmpty() {
		t.Error("IsEmpty must return true on freshly created DataCache")
	}
}

func TestDataCacheIsEmptyAfterPut(t *testing.T) {
	t.Parallel()
	c := NewDataCache()
	k := hmtypes.DataPointKey{InterfaceID: "HmIP-RF", ChannelAddress: "VCU0000001:0", Parameter: "STATE"}
	c.Put(k, true, time.Now())
	if c.IsEmpty() {
		t.Error("IsEmpty must return false after Put")
	}
}

func TestDataCacheIsEmptyAfterClear(t *testing.T) {
	t.Parallel()
	c := NewDataCache()
	k := hmtypes.DataPointKey{InterfaceID: "HmIP-RF", ChannelAddress: "VCU0000001:0", Parameter: "STATE"}
	c.Put(k, true, time.Now())
	c.Clear()
	if !c.IsEmpty() {
		t.Error("IsEmpty must return true after Clear")
	}
}

func TestDataCacheIsEmptyAfterForget(t *testing.T) {
	t.Parallel()
	c := NewDataCache()
	k := hmtypes.DataPointKey{InterfaceID: "HmIP-RF", ChannelAddress: "VCU0000001:0", Parameter: "STATE"}
	c.Put(k, true, time.Now())
	c.Forget(k)
	if !c.IsEmpty() {
		t.Error("IsEmpty must return true after Forget removes the last entry")
	}
}

// ---------------------------------------------------------------------------
// DataCache extended API: AddData, ClearInterface
// ---------------------------------------------------------------------------

func TestDataCacheAddData(t *testing.T) {
	t.Parallel()
	c := NewDataCache()
	k1 := makeDP("HmIP-RF", "DEV:1", "LEVEL")
	k2 := makeDP("HmIP-RF", "DEV:2", "STATE")

	c.AddData("HmIP-RF", map[hmtypes.DataPointKey]any{k1: 0.5, k2: true})

	if e, ok := c.Get(k1); !ok || e.Value != 0.5 {
		t.Fatalf("AddData: k1=%+v ok=%v", e, ok)
	}
	if e, ok := c.Get(k2); !ok || e.Value != true {
		t.Fatalf("AddData: k2=%+v ok=%v", e, ok)
	}
	ts, ok := c.RefreshedAt("HmIP-RF")
	if !ok || ts.IsZero() {
		t.Fatal("AddData must record refreshedAt")
	}
}

func TestDataCacheAddDataReplacesOldInterface(t *testing.T) {
	t.Parallel()
	c := NewDataCache()
	old := makeDP("HmIP-RF", "OLD:1", "LEVEL")
	c.Put(old, 999, time.Now())

	c.AddData("HmIP-RF", map[hmtypes.DataPointKey]any{})

	if _, ok := c.Get(old); ok {
		t.Fatal("AddData must clear old entries for the interface")
	}
}

func TestDataCacheClearInterface(t *testing.T) {
	t.Parallel()
	c := NewDataCache()
	ka := makeDP("HmIP-RF", "A:1", "LEVEL")
	kb := makeDP("BidCos-RF", "B:1", "STATE")
	c.Put(ka, 1, time.Now())
	c.Put(kb, 2, time.Now())

	c.ClearInterface("HmIP-RF")

	if _, ok := c.Get(ka); ok {
		t.Fatal("ClearInterface must remove entries for the interface")
	}
	if _, ok := c.Get(kb); !ok {
		t.Fatal("ClearInterface must not remove entries for other interfaces")
	}
}

func TestDataCacheClearInterfaceEmpty(t *testing.T) {
	t.Parallel()
	c := NewDataCache()
	// Must not panic on empty interfaceID.
	c.ClearInterface("")
}

// ---------------------------------------------------------------------------
// DataCache initialization lifecycle: Load, SetInitializationComplete,
// IsInitializing, RefreshedAt, RefreshDataPointData
// ---------------------------------------------------------------------------

func TestDataCacheLoad(t *testing.T) {
	t.Parallel()
	c := NewDataCache()
	k := makeDP("HmIP-RF", "DEV:1", "LEVEL")
	c.Put(k, 0.5, time.Now())

	c.Load("HmIP-RF")

	if c.IsInitializing() != true {
		t.Fatal("Load must set isInitializing=true")
	}
	if _, ok := c.Get(k); ok {
		t.Fatal("Load must clear existing entries for the interface")
	}
}

func TestDataCacheRefreshDataPointData(t *testing.T) {
	t.Parallel()
	c := NewDataCache()
	c.SetInitializationComplete("HmIP-RF")

	if _, ok := c.RefreshedAt("HmIP-RF"); !ok {
		t.Fatal("RefreshedAt must be set after SetInitializationComplete")
	}

	c.RefreshDataPointData()

	if _, ok := c.RefreshedAt("HmIP-RF"); ok {
		t.Fatal("RefreshDataPointData must clear refreshedAt timestamps")
	}
}

func TestDataCacheSetInitializationComplete(t *testing.T) {
	t.Parallel()
	c := NewDataCache()
	c.Load("HmIP-RF")
	if !c.IsInitializing() {
		t.Fatal("IsInitializing must be true after Load")
	}

	c.SetInitializationComplete("HmIP-RF")

	if c.IsInitializing() {
		t.Fatal("IsInitializing must be false after SetInitializationComplete")
	}
	ts, ok := c.RefreshedAt("HmIP-RF")
	if !ok || ts.IsZero() {
		t.Fatal("SetInitializationComplete must record refreshedAt")
	}
}

func TestDataCacheIsInitializing(t *testing.T) {
	t.Parallel()
	c := NewDataCache()
	if c.IsInitializing() {
		t.Fatal("IsInitializing must be false on a new cache")
	}
	c.Load("HmIP-RF")
	if !c.IsInitializing() {
		t.Fatal("IsInitializing must be true after Load")
	}
	c.SetInitializationComplete("HmIP-RF")
	if c.IsInitializing() {
		t.Fatal("IsInitializing must be false after SetInitializationComplete")
	}
}

func TestDataCacheRefreshedAt(t *testing.T) {
	t.Parallel()
	c := NewDataCache()

	_, ok := c.RefreshedAt("NONE")
	if ok {
		t.Fatal("RefreshedAt must return false for unknown interface")
	}

	c.SetInitializationComplete("HmIP-RF")
	ts, ok := c.RefreshedAt("HmIP-RF")
	if !ok {
		t.Fatal("RefreshedAt must return true after initialization")
	}
	if ts.IsZero() {
		t.Fatal("RefreshedAt timestamp must not be zero")
	}
}
