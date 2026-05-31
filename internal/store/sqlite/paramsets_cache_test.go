// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// paramsets_cache_test.go — Item 3: address-parameter cache
// in ParamsetStore.
//
// Tests:
// Add → Lookup (cache hit, O(1) path)
// Add two channels → multi-channel detected from cache
// Remove (DeleteChannel) → parameter gone from cache
// Remove then Add → lookup returns false for removed, true after re-add
// ClearForInterface → full cache wipe
// WarmCache → loads existing DB rows into cache
// RegisterAdditionalParameter → adds to cache
// Multiple channels → IsInMultipleChannels returns true from cache

package sqlite

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func freshCacheStore(t *testing.T) *ParamsetStore {
	t.Helper()
	return NewParamsetStore(openTestDB(t, "paramset_cache.db"))
}

func upsertCacheRec(t *testing.T, s *ParamsetStore, chAddr string, ps hmproto.Paramset) {
	t.Helper()
	err := s.Upsert(context.Background(), ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: chAddr,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "h-" + chAddr,
		Paramset:       ps,
	})
	if err != nil {
		t.Fatalf("Upsert %s: %v", chAddr, err)
	}
}

// TestCacheAddLookup verifies that after Upsert the cache hit path is
// used and returns the correct result without a SQL round-trip.
func TestCacheAddLookup(t *testing.T) {
	t.Parallel()
	s := freshCacheStore(t)
	ctx := context.Background()

	// Single channel — IsInMultipleChannels must be false.
	upsertCacheRec(t, s, "DEV1:1", hmproto.Paramset{
		"LEVEL": {Type: "FLOAT"},
	})

	ok, err := s.IsInMultipleChannels(ctx, "ccu1", "HmIP-RF", "DEV1:1", "LEVEL")
	if err != nil {
		t.Fatalf("IsInMultipleChannels: %v", err)
	}
	if ok {
		t.Error("single channel: IsInMultipleChannels must be false")
	}

	// Verify the cache was actually populated (internal state check).
	result, hit := s.cacheIsInMultipleChannels("DEV1", "LEVEL")
	if !hit {
		t.Error("cache miss after Upsert — cache was not populated")
	}
	if result {
		t.Error("single channel: cache entry must be false")
	}
}

// TestCacheMultipleChannelsDetected verifies that after inserting the
// same parameter in two channels, IsInMultipleChannels returns true
// from the cache.
func TestCacheMultipleChannelsDetected(t *testing.T) {
	t.Parallel()
	s := freshCacheStore(t)
	ctx := context.Background()

	upsertCacheRec(t, s, "DEV2:1", hmproto.Paramset{"LEVEL": {Type: "FLOAT"}})
	upsertCacheRec(t, s, "DEV2:2", hmproto.Paramset{"LEVEL": {Type: "FLOAT"}})

	ok, err := s.IsInMultipleChannels(ctx, "ccu1", "HmIP-RF", "DEV2:1", "LEVEL")
	if err != nil {
		t.Fatalf("IsInMultipleChannels: %v", err)
	}
	if !ok {
		t.Error("two channels: IsInMultipleChannels must be true")
	}

	// Also check from the second channel's perspective.
	ok2, err := s.IsInMultipleChannels(ctx, "ccu1", "HmIP-RF", "DEV2:2", "LEVEL")
	if err != nil {
		t.Fatalf("IsInMultipleChannels ch2: %v", err)
	}
	if !ok2 {
		t.Error("two channels from ch2 perspective: must be true")
	}
}

// TestCacheDeleteChannelInvalidates verifies that after DeleteChannel
// the channel's parameter is removed from the cache.
func TestCacheDeleteChannelInvalidates(t *testing.T) {
	t.Parallel()
	s := freshCacheStore(t)
	ctx := context.Background()

	upsertCacheRec(t, s, "DEV3:1", hmproto.Paramset{"LEVEL": {Type: "FLOAT"}})
	upsertCacheRec(t, s, "DEV3:2", hmproto.Paramset{"LEVEL": {Type: "FLOAT"}})

	// Both channels: multi-channel true.
	ok, _ := s.IsInMultipleChannels(ctx, "ccu1", "HmIP-RF", "DEV3:1", "LEVEL")
	if !ok {
		t.Fatal("pre-delete: should be in multiple channels")
	}

	// Remove channel 2.
	if err := s.DeleteChannel(ctx, "ccu1", "HmIP-RF", "DEV3:2"); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}

	// Now only channel 1 remains — multi-channel must be false.
	ok2, err := s.IsInMultipleChannels(ctx, "ccu1", "HmIP-RF", "DEV3:1", "LEVEL")
	if err != nil {
		t.Fatalf("IsInMultipleChannels after delete: %v", err)
	}
	if ok2 {
		t.Error("after delete of ch2: IsInMultipleChannels must be false")
	}
}

// TestCacheClearForInterfaceWipesCache verifies that ClearForInterface
// drops the full in-memory cache.
func TestCacheClearForInterfaceWipesCache(t *testing.T) {
	t.Parallel()
	s := freshCacheStore(t)
	ctx := context.Background()

	upsertCacheRec(t, s, "DEV4:1", hmproto.Paramset{"LEVEL": {Type: "FLOAT"}})
	upsertCacheRec(t, s, "DEV4:2", hmproto.Paramset{"LEVEL": {Type: "FLOAT"}})

	// Confirm cache is warm.
	if _, hit := s.cacheIsInMultipleChannels("DEV4", "LEVEL"); !hit {
		t.Fatal("cache should be warm before clear")
	}

	if _, err := s.ClearForInterface(ctx, "ccu1", "HmIP-RF"); err != nil {
		t.Fatalf("ClearForInterface: %v", err)
	}

	// Cache must be gone — next lookup is a cold miss → SQL fallback → 0 rows → false.
	if _, hit := s.cacheIsInMultipleChannels("DEV4", "LEVEL"); hit {
		t.Error("cache must be empty after ClearForInterface")
	}
	// SQL fallback also returns false because rows were deleted.
	ok, err := s.IsInMultipleChannels(ctx, "ccu1", "HmIP-RF", "DEV4:1", "LEVEL")
	if err != nil {
		t.Fatalf("IsInMultipleChannels after clear: %v", err)
	}
	if ok {
		t.Error("after ClearForInterface: IsInMultipleChannels must be false")
	}
}

// TestCacheWarmCache verifies that WarmCache loads existing DB rows into
// the in-memory cache so subsequent calls use the fast path.
func TestCacheWarmCache(t *testing.T) {
	t.Parallel()

	// Simulate a fresh daemon start: populate the DB first without
	// touching the cache (use a second store that writes to the same DB).
	db := openTestDB(t, "paramset_warm.db")
	s1 := NewParamsetStore(db)
	ctx := context.Background()

	// Insert paramsets via s1 — this also warms s1's cache.
	upsertCacheRec(t, s1, "DEV5:1", hmproto.Paramset{"BOOST_MODE": {Type: "BOOL"}})
	upsertCacheRec(t, s1, "DEV5:2", hmproto.Paramset{"BOOST_MODE": {Type: "BOOL"}})

	// Simulate a process restart: create s2 pointing at the same DB.
	// s2's cache is empty — it doesn't know about the existing rows.
	s2 := NewParamsetStore(db)

	// Before WarmCache: cache miss, SQL fallback used.
	if _, hit := s2.cacheIsInMultipleChannels("DEV5", "BOOST_MODE"); hit {
		t.Error("s2 should have a cold cache before WarmCache")
	}

	// WarmCache loads the existing rows.
	if err := s2.WarmCache(ctx); err != nil {
		t.Fatalf("WarmCache: %v", err)
	}

	// After WarmCache: cache hit should return true (two channels).
	result, hit := s2.cacheIsInMultipleChannels("DEV5", "BOOST_MODE")
	if !hit {
		t.Error("cache miss after WarmCache — should be a hit")
	}
	if !result {
		t.Error("WarmCache: two channels should be detected")
	}

	// IsInMultipleChannels should also return true without SQL.
	ok, err := s2.IsInMultipleChannels(ctx, "ccu1", "HmIP-RF", "DEV5:1", "BOOST_MODE")
	if err != nil {
		t.Fatalf("IsInMultipleChannels after WarmCache: %v", err)
	}
	if !ok {
		t.Error("IsInMultipleChannels after WarmCache: must be true")
	}
}

// TestCacheRegisterAdditionalParameter verifies that
// RegisterAdditionalParameter adds to the in-memory cache (it is no
// longer a no-op after ).
func TestCacheRegisterAdditionalParameter(t *testing.T) {
	t.Parallel()
	s := freshCacheStore(t)
	ctx := context.Background()

	// Start with one channel in the DB.
	upsertCacheRec(t, s, "DEV6:1", hmproto.Paramset{"STATE": {Type: "BOOL"}})

	// Register a second channel for the same parameter via the
	// out-of-band method (simulates a calculated DP that shares the
	// parameter name).
	s.RegisterAdditionalParameter(ctx, "DEV6:2", "STATE")

	// Now two channels are in the cache → IsInMultipleChannels must be true.
	ok, err := s.IsInMultipleChannels(ctx, "ccu1", "HmIP-RF", "DEV6:1", "STATE")
	if err != nil {
		t.Fatalf("IsInMultipleChannels after RegisterAdditionalParameter: %v", err)
	}
	if !ok {
		t.Error("RegisterAdditionalParameter: two channels should be detected")
	}
}

// ---------------------------------------------------------------------------
// IsInMultipleChannels — SQL-fallback path (no warm cache)
// ---------------------------------------------------------------------------

func TestParamsetStoreIsInMultipleChannelsFalseWhenSingleChannel(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "paramset_multi_single.sqlite")
	s := NewParamsetStore(db)
	ctx := context.Background()

	// Insert paramset for one channel only.
	_ = s.Upsert(ctx, ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    "BidCos-RF",
		ChannelAddress: "VCU001:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "h1",
		Paramset: hmproto.Paramset{
			"SET_TEMPERATURE": hmproto.ParameterData{Type: "FLOAT"},
		},
	})

	ok, err := s.IsInMultipleChannels(ctx, "ccu1", "BidCos-RF", "VCU001:1", "SET_TEMPERATURE")
	if err != nil {
		t.Fatalf("IsInMultipleChannels: %v", err)
	}
	if ok {
		t.Error("IsInMultipleChannels must return false when parameter exists in only one channel")
	}
}

func TestParamsetStoreIsInMultipleChannelsTrueWhenMultipleChannels(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "paramset_multi_multi.sqlite")
	s := NewParamsetStore(db)
	ctx := context.Background()

	// Insert the same parameter in two channels of the same device.
	for _, ch := range []string{"VCU001:1", "VCU001:2"} {
		_ = s.Upsert(ctx, ParamsetRecord{
			CentralName:    "ccu1",
			InterfaceID:    "BidCos-RF",
			ChannelAddress: ch,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Hash:           "h" + ch,
			Paramset: hmproto.Paramset{
				"LEVEL": hmproto.ParameterData{Type: "FLOAT"},
			},
		})
	}

	ok, err := s.IsInMultipleChannels(ctx, "ccu1", "BidCos-RF", "VCU001:1", "LEVEL")
	if err != nil {
		t.Fatalf("IsInMultipleChannels: %v", err)
	}
	if !ok {
		t.Error("IsInMultipleChannels must return true when parameter exists in 2+ channels")
	}
}

func TestParamsetStoreIsInMultipleChannelsFalseNoSeparator(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "paramset_multi_nosep.sqlite")
	s := NewParamsetStore(db)
	ctx := context.Background()

	// Device address without separator: should return false immediately.
	ok, err := s.IsInMultipleChannels(ctx, "ccu1", "BidCos-RF", "VCU001", "LEVEL")
	if err != nil {
		t.Fatalf("IsInMultipleChannels: %v", err)
	}
	if ok {
		t.Error("IsInMultipleChannels must return false for address without separator")
	}
}

func TestParamsetStoreIsInMultipleChannelsDifferentParameters(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "paramset_multi_diffparam.sqlite")
	s := NewParamsetStore(db)
	ctx := context.Background()

	// Channel 1 has LEVEL; channel 2 has only STATE.
	_ = s.Upsert(ctx, ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    "BidCos-RF",
		ChannelAddress: "VCU002:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "h1",
		Paramset:       hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: "FLOAT"}},
	})
	_ = s.Upsert(ctx, ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    "BidCos-RF",
		ChannelAddress: "VCU002:2",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "h2",
		Paramset:       hmproto.Paramset{"STATE": hmproto.ParameterData{Type: "BOOL"}},
	})

	// LEVEL is only in channel 1 → false.
	ok, err := s.IsInMultipleChannels(ctx, "ccu1", "BidCos-RF", "VCU002:1", "LEVEL")
	if err != nil {
		t.Fatalf("IsInMultipleChannels: %v", err)
	}
	if ok {
		t.Error("IsInMultipleChannels must return false when parameter exists in only one channel")
	}
}

func TestParamsetStoreRegisterAdditionalParameterNoOp(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "paramset_register_noop.sqlite")
	s := NewParamsetStore(db)
	ctx := context.Background()

	// RegisterAdditionalParameter is a no-op in the SQLite implementation;
	// calling it must not panic or return an error.
	s.RegisterAdditionalParameter(ctx, "VCU003:1", "LEVEL")
}

// ---------------------------------------------------------------------------
// cacheIsInMultipleChannels — device in cache, param not present branch
// ---------------------------------------------------------------------------

func TestCacheIsInMultipleChannelsParamMiss(t *testing.T) {
	t.Parallel()
	s := freshCacheStore(t)

	// Upsert one param so the device entry exists in the cache.
	if err := s.Upsert(context.Background(), ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "DEVX:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "hx1",
		Paramset:       hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// Query for a different parameter — device IS in cache, param is NOT.
	_, hit := s.cacheIsInMultipleChannels("DEVX", "STATE")
	if hit {
		t.Error("param miss must return hit=false")
	}
}

// ---------------------------------------------------------------------------
// WarmCache — skips device-level address (no colon) silently
// ---------------------------------------------------------------------------

func TestWarmCacheSkipsDeviceLevelAddress(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "warmcache_device_level.db")
	s1 := NewParamsetStore(db)
	ctx := context.Background()

	// Insert a device-level paramset (no colon in ChannelAddress).
	// This exercises the splitChannelAddress !ok → continue branch in WarmCache.
	if err := s1.Upsert(ctx, ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "DEVLEVEL", // no ':' → device level
		ParamsetKey:    hmenum.ParamsetKeyMaster,
		Hash:           "hdev",
		Paramset:       hmproto.Paramset{"PARAM1": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}},
	}); err != nil {
		t.Fatalf("Upsert device-level: %v", err)
	}

	// WarmCache must succeed without panic.
	s2 := NewParamsetStore(db)
	if err := s2.WarmCache(ctx); err != nil {
		t.Fatalf("WarmCache: %v", err)
	}
	// The device-level address was skipped — no cache entry for it.
	_, hit := s2.cacheIsInMultipleChannels("DEVLEVEL", "PARAM1")
	if hit {
		t.Error("device-level address must not be inserted into channel cache")
	}
}

// ---------------------------------------------------------------------------
// RegisterAdditionalParameter — device-level address (no colon) early return
// ---------------------------------------------------------------------------

func TestRegisterAdditionalParameterDeviceLevelIsNoop(t *testing.T) {
	t.Parallel()
	s := freshCacheStore(t)
	ctx := context.Background()
	// A device-level address (no colon) must return immediately without panic.
	s.RegisterAdditionalParameter(ctx, "DEVONLY", "PARAM1")
	// Verify no cache entry was added.
	_, hit := s.cacheIsInMultipleChannels("DEVONLY", "PARAM1")
	if hit {
		t.Error("device-level RegisterAdditionalParameter must not add cache entry")
	}
}

// ---------------------------------------------------------------------------
// splitChannelAddress — non-numeric suffix branch (break inside loop)
// ---------------------------------------------------------------------------

func TestSplitChannelAddressNonNumericSuffix(t *testing.T) {
	t.Parallel()
	// "DEV:1a" — the 'a' triggers the non-numeric break; channel must still
	// be parsed as 1 (digits before the non-numeric char).
	dev, ch, ok := splitChannelAddress("DEV:1a")
	if !ok {
		t.Fatal("splitChannelAddress must return ok=true for addr with colon")
	}
	if dev != "DEV" {
		t.Errorf("dev=%q, want DEV", dev)
	}
	if ch != 1 {
		t.Errorf("ch=%d, want 1", ch)
	}
}
