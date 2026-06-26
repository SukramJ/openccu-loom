// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// paramsets_store_cache_coherency_test.go — store/cache coherency for three
// genuine gaps:
//
//  1. Patch-apply pipeline → SQLite write → cache reflects patched data.
//  2. Multi-paramset-key write coherency (VALUES patch must not corrupt MASTER
//     and must not corrupt the shared address-parameter cache).
//  3. Device-model-change handling: the production code has no automatic
//     cache invalidation on model change; the documented mechanism is
//     WipeOutdated (schema_version bump) + re-upsert + WarmCache.

package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/store/patches"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ---------------------------------------------------------------------------
// 1. Patch-apply + SQLite write + cache invalidation
// ---------------------------------------------------------------------------

// TestPatchApplyThenUpsertCacheReflectsNewShape verifies the ingestion
// pipeline contract: a patch registry mutates the in-memory Paramset, the
// caller then persists the patched shape via Upsert, and the resulting
// address-parameter cache entry is consistent with what was actually stored.
//
// The test uses the built-in HM-CC-VG-1 / SET_TEMPERATURE patch (widens
// Min/Max from 0/0 to 4.5/30.5) so it exercises the real production patch
// path without inventing a fake one.
func TestPatchApplyThenUpsertCacheReflectsNewShape(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()
	r := patches.NewRegistry()

	const (
		centralName = "ccu1"
		iface       = "HmIP-RF"
		chAddr      = "VCU0001:1"
		deviceAddr  = "VCU0001"
		psKey       = hmenum.ParamsetKeyValues
	)

	// Simulate a raw paramset arriving from the CCU: SET_TEMPERATURE with
	// bogus 0/0 bounds (the defect the built-in patch corrects).
	rawPS := hmproto.Paramset{
		"SET_TEMPERATURE": hmproto.ParameterData{
			Type: hmenum.ParameterTypeFloat,
			Min:  json.RawMessage(`0`),
			Max:  json.RawMessage(`0`),
		},
		"VALVE_STATE": hmproto.ParameterData{
			Type: hmenum.ParameterTypeFloat,
		},
	}

	// Step 1: apply patches (mutates rawPS in-place).
	changes := r.ApplyParamset("HM-CC-VG-1", chAddr, psKey, rawPS)
	if changes == 0 {
		t.Fatal("expected HM-CC-VG-1 patch to fire; got 0 changes")
	}

	// Verify the patch actually fixed the bounds in the in-memory map.
	pd := rawPS["SET_TEMPERATURE"]
	if string(pd.Min) != "4.5" {
		t.Fatalf("patch: Min=%s want 4.5", pd.Min)
	}
	if string(pd.Max) != "30.5" {
		t.Fatalf("patch: Max=%s want 30.5", pd.Max)
	}

	// Step 2: persist the patched paramset to SQLite (this also updates the
	// address-parameter cache for all parameters in rawPS).
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    centralName,
		InterfaceID:    iface,
		ChannelAddress: chAddr,
		ParamsetKey:    psKey,
		Hash:           "patched-hash",
		Paramset:       rawPS,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Step 3: the cache must know about both parameters (single-channel device
	// → IsInMultipleChannels = false, but the cache must be warm).
	result, hit := s.cacheIsInMultipleChannels(deviceAddr, "SET_TEMPERATURE")
	if !hit {
		t.Error("cache miss after Upsert — address-parameter cache not populated")
	}
	if result {
		t.Error("single-channel device: IsInMultipleChannels must be false")
	}

	result, hit = s.cacheIsInMultipleChannels(deviceAddr, "VALVE_STATE")
	if !hit {
		t.Error("VALVE_STATE: cache miss after Upsert")
	}
	if result {
		t.Error("VALVE_STATE: single-channel, must be false")
	}

	// Step 4: re-read from SQLite and confirm the stored shape is the
	// patched one (Min=4.5, Max=30.5), not the raw CCU values (0/0).
	got, err := s.Get(ctx, centralName, iface, chAddr, psKey)
	if err != nil {
		t.Fatalf("Get after patched Upsert: %v", err)
	}
	stored := got.Paramset["SET_TEMPERATURE"]
	if string(stored.Min) != "4.5" {
		t.Errorf("SQLite Min=%s want 4.5 (patch must persist)", stored.Min)
	}
	if string(stored.Max) != "30.5" {
		t.Errorf("SQLite Max=%s want 30.5 (patch must persist)", stored.Max)
	}
	if got.Hash != "patched-hash" {
		t.Errorf("Hash=%q want patched-hash", got.Hash)
	}
}

// TestPatchApplyThenReUpsertCacheIsUpdated verifies that re-upserting an
// already-cached channel with a newly-patched paramset updates the
// address-parameter cache for the new parameters (and removes no longer
// present ones, since Upsert is a full replace of the paramset_json blob).
func TestPatchApplyThenReUpsertCacheIsUpdated(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()

	const (
		centralName = "ccu1"
		iface       = "HmIP-RF"
		chAddr      = "VCU9999:1"
		deviceAddr  = "VCU9999"
		psKey       = hmenum.ParamsetKeyValues
	)

	// First upsert: two parameters OLD_PARAM and SHARED.
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    centralName,
		InterfaceID:    iface,
		ChannelAddress: chAddr,
		ParamsetKey:    psKey,
		Hash:           "v1",
		Paramset: hmproto.Paramset{
			"OLD_PARAM": {Type: hmenum.ParameterTypeFloat},
			"SHARED":    {Type: hmenum.ParameterTypeBool},
		},
	}); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	// Cache must be warm for OLD_PARAM and SHARED.
	if _, hit := s.cacheIsInMultipleChannels(deviceAddr, "OLD_PARAM"); !hit {
		t.Error("pre-patch: OLD_PARAM must be in cache")
	}

	// Second upsert (simulates post-patch write): OLD_PARAM is gone,
	// NEW_PARAM is added.  SHARED survives.
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    centralName,
		InterfaceID:    iface,
		ChannelAddress: chAddr,
		ParamsetKey:    psKey,
		Hash:           "v2",
		Paramset: hmproto.Paramset{
			"NEW_PARAM": {Type: hmenum.ParameterTypeFloat},
			"SHARED":    {Type: hmenum.ParameterTypeBool},
		},
	}); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	// The cache is additive on Upsert (it adds new parameters; it does not
	// remove stale ones from the cache map). This is the documented behaviour:
	// the address-parameter cache only grows via Upsert/WarmCache and shrinks
	// only via DeleteChannel/ClearForInterface.  A re-upsert with a narrower
	// paramset shape does NOT prune the cache for parameters that were dropped.
	//
	// NEW_PARAM and SHARED must be present in the cache after the second write.
	if _, hit := s.cacheIsInMultipleChannels(deviceAddr, "NEW_PARAM"); !hit {
		t.Error("post re-upsert: NEW_PARAM must be in cache")
	}
	if _, hit := s.cacheIsInMultipleChannels(deviceAddr, "SHARED"); !hit {
		t.Error("post re-upsert: SHARED must be in cache")
	}

	// SQLite must have the second (narrower) shape.
	got, err := s.Get(ctx, centralName, iface, chAddr, psKey)
	if err != nil {
		t.Fatalf("Get after re-upsert: %v", err)
	}
	if _, ok := got.Paramset["NEW_PARAM"]; !ok {
		t.Error("SQLite must contain NEW_PARAM after re-upsert")
	}
	if _, ok := got.Paramset["OLD_PARAM"]; ok {
		t.Error("SQLite must NOT contain OLD_PARAM after re-upsert (full replace)")
	}
}

// ---------------------------------------------------------------------------
// 2. Multi-paramset-key coherency
// ---------------------------------------------------------------------------

// TestMultiParamsetKeyWriteDoesNotCorruptOtherKey exercises a channel with
// both VALUES and MASTER paramset keys. A patch-and-re-upsert on VALUES must
// leave the MASTER row in SQLite untouched, and the address-parameter cache
// must correctly reflect parameters from both keys independently.
func TestMultiParamsetKeyWriteDoesNotCorruptOtherKey(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()

	const (
		centralName = "ccu1"
		iface       = "HmIP-RF"
		chAddr      = "MKDEV:1"
	)

	// Set up: insert VALUES (LEVEL, STATE) and MASTER (CYCLIC_INFO_MSG).
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    centralName,
		InterfaceID:    iface,
		ChannelAddress: chAddr,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "val-orig",
		Paramset: hmproto.Paramset{
			"LEVEL": {Type: hmenum.ParameterTypeFloat},
			"STATE": {Type: hmenum.ParameterTypeBool},
		},
	}); err != nil {
		t.Fatalf("Upsert VALUES: %v", err)
	}
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    centralName,
		InterfaceID:    iface,
		ChannelAddress: chAddr,
		ParamsetKey:    hmenum.ParamsetKeyMaster,
		Hash:           "mas-orig",
		Paramset: hmproto.Paramset{
			"CYCLIC_INFO_MSG":                        {Type: hmenum.ParameterTypeBool},
			"MIN_MAX_VALUE_NOT_RELEVANT_TEMPERATURE": {Type: hmenum.ParameterTypeFloat},
		},
	}); err != nil {
		t.Fatalf("Upsert MASTER: %v", err)
	}

	// Simulate a patch being applied to VALUES only (LEVEL gets a unit fix).
	patchedValues := hmproto.Paramset{
		"LEVEL": {Type: hmenum.ParameterTypeFloat, Unit: "%"},
		"STATE": {Type: hmenum.ParameterTypeBool},
	}
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    centralName,
		InterfaceID:    iface,
		ChannelAddress: chAddr,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "val-patched",
		Paramset:       patchedValues,
	}); err != nil {
		t.Fatalf("Upsert patched VALUES: %v", err)
	}

	// --- SQLite coherency ---

	// VALUES must contain the patched LEVEL unit.
	gotVal, err := s.Get(ctx, centralName, iface, chAddr, hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("Get VALUES: %v", err)
	}
	if gotVal.Hash != "val-patched" {
		t.Errorf("VALUES hash=%q want val-patched", gotVal.Hash)
	}
	if gotVal.Paramset["LEVEL"].Unit != "%" {
		t.Errorf("VALUES LEVEL.Unit=%q want %%", gotVal.Paramset["LEVEL"].Unit)
	}

	// MASTER must be completely untouched by the VALUES write.
	gotMas, err := s.Get(ctx, centralName, iface, chAddr, hmenum.ParamsetKeyMaster)
	if err != nil {
		t.Fatalf("Get MASTER: %v", err)
	}
	if gotMas.Hash != "mas-orig" {
		t.Errorf("MASTER hash=%q want mas-orig (MASTER must not be modified by VALUES write)", gotMas.Hash)
	}
	if _, ok := gotMas.Paramset["CYCLIC_INFO_MSG"]; !ok {
		t.Error("MASTER must still contain CYCLIC_INFO_MSG after VALUES re-upsert")
	}

	// --- GetChannelParamsetDescriptions coherency ---
	// Both keys must be present with their respective parameters.
	allPS, err := s.GetChannelParamsetDescriptions(ctx, centralName, iface, chAddr)
	if err != nil {
		t.Fatalf("GetChannelParamsetDescriptions: %v", err)
	}
	if _, ok := allPS[hmenum.ParamsetKeyValues]; !ok {
		t.Error("GetChannelParamsetDescriptions: VALUES missing")
	}
	if _, ok := allPS[hmenum.ParamsetKeyMaster]; !ok {
		t.Error("GetChannelParamsetDescriptions: MASTER missing")
	}

	// --- Address-parameter cache coherency ---
	// The cache maps (deviceAddress, parameter) to channel sets. Parameters
	// from both VALUES (LEVEL, STATE) and MASTER (CYCLIC_INFO_MSG) are all
	// registered under the single device address. None of them must be
	// cross-contaminated by the VALUES re-upsert.
	//
	// LEVEL and STATE are VALUES parameters — they must be in the cache.
	if _, hit := s.cacheIsInMultipleChannels("MKDEV", "LEVEL"); !hit {
		t.Error("cache: LEVEL (VALUES) must be present")
	}
	if _, hit := s.cacheIsInMultipleChannels("MKDEV", "STATE"); !hit {
		t.Error("cache: STATE (VALUES) must be present")
	}
	// CYCLIC_INFO_MSG is a MASTER parameter — it must not disappear from
	// the cache because of the VALUES upsert (additive cache).
	if _, hit := s.cacheIsInMultipleChannels("MKDEV", "CYCLIC_INFO_MSG"); !hit {
		t.Error("cache: CYCLIC_INFO_MSG (MASTER) must remain after VALUES re-upsert")
	}

	// Verify IsInMultipleChannels is consistent (single channel → false).
	for _, param := range []string{"LEVEL", "STATE", "CYCLIC_INFO_MSG"} {
		ok, err := s.IsInMultipleChannels(ctx, centralName, iface, chAddr, param)
		if err != nil {
			t.Fatalf("IsInMultipleChannels %s: %v", param, err)
		}
		if ok {
			t.Errorf("IsInMultipleChannels(%s): single-channel device must return false", param)
		}
	}
}

// TestMultiParamsetKeyDeleteChannelRemovesBothKeys verifies that DeleteChannel
// removes all paramset keys (VALUES + MASTER) for the channel in a single
// operation and evicts the channel's contribution from the cache for each key's
// parameters.
func TestMultiParamsetKeyDeleteChannelRemovesBothKeys(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()

	const (
		centralName = "ccu1"
		iface       = "HmIP-RF"
		chAddrA     = "DKDEV:1" // channel to be deleted
		chAddrB     = "DKDEV:2" // sibling channel that must survive
	)

	// Insert VALUES + MASTER for chAddrA (the channel we will delete).
	for _, psKey := range []hmenum.ParamsetKey{hmenum.ParamsetKeyValues, hmenum.ParamsetKeyMaster} {
		if err := s.Upsert(ctx, ParamsetRecord{
			CentralName:    centralName,
			InterfaceID:    iface,
			ChannelAddress: chAddrA,
			ParamsetKey:    psKey,
			Hash:           string(psKey) + "-ch1",
			Paramset: hmproto.Paramset{
				"SHARED_PARAM": {Type: hmenum.ParameterTypeFloat},
			},
		}); err != nil {
			t.Fatalf("Upsert chAddrA %s: %v", psKey, err)
		}
	}
	// Insert VALUES only for chAddrB — SHARED_PARAM appears in both channels
	// so IsInMultipleChannels should return true before the delete.
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    centralName,
		InterfaceID:    iface,
		ChannelAddress: chAddrB,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "val-ch2",
		Paramset: hmproto.Paramset{
			"SHARED_PARAM": {Type: hmenum.ParameterTypeFloat},
		},
	}); err != nil {
		t.Fatalf("Upsert chAddrB VALUES: %v", err)
	}

	// Pre-condition: SHARED_PARAM in two channels → IsInMultipleChannels true.
	ok, err := s.IsInMultipleChannels(ctx, centralName, iface, chAddrA, "SHARED_PARAM")
	if err != nil {
		t.Fatalf("IsInMultipleChannels (pre-delete): %v", err)
	}
	if !ok {
		t.Fatal("pre-delete: SHARED_PARAM must be in multiple channels")
	}

	// Delete chAddrA — must remove both VALUES and MASTER rows.
	if err := s.DeleteChannel(ctx, centralName, iface, chAddrA); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}

	// Both paramset keys for chAddrA must be gone.
	for _, psKey := range []hmenum.ParamsetKey{hmenum.ParamsetKeyValues, hmenum.ParamsetKeyMaster} {
		if _, err := s.Get(ctx, centralName, iface, chAddrA, psKey); !errors.Is(err, ErrParamsetNotFound) {
			t.Errorf("Get chAddrA %s after DeleteChannel: want ErrParamsetNotFound, got %v", psKey, err)
		}
	}

	// chAddrB must still be accessible.
	gotB, err := s.Get(ctx, centralName, iface, chAddrB, hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("Get chAddrB after DeleteChannel of chAddrA: %v", err)
	}
	if _, ok := gotB.Paramset["SHARED_PARAM"]; !ok {
		t.Error("chAddrB must still contain SHARED_PARAM after chAddrA is deleted")
	}

	// Cache must reflect that only chAddrB now holds SHARED_PARAM →
	// IsInMultipleChannels must now be false.
	okAfter, err := s.IsInMultipleChannels(ctx, centralName, iface, chAddrB, "SHARED_PARAM")
	if err != nil {
		t.Fatalf("IsInMultipleChannels (post-delete): %v", err)
	}
	if okAfter {
		t.Error("after DeleteChannel of chAddrA: SHARED_PARAM must be in only one channel (chAddrB)")
	}
}

// ---------------------------------------------------------------------------
// 3. Device-model-change coherency
// ---------------------------------------------------------------------------

// TestDeviceModelChangeCoherencyViaWipeAndReUpsert documents and tests the
// production mechanism for handling a device model change:
//
//   - There is NO automatic cache invalidation when the device model changes.
//   - The paramset cache (address-parameter cache) does not store or key by
//     model name; it keys by (deviceAddress, parameter).
//   - The schema_version / WipeOutdated mechanism is the only supported
//     invalidation path for cached paramset *shapes* (it clears rows from
//     all devices whose cached data predates a binary upgrade).
//   - For a live device-model change within the same daemon run, the caller
//     is expected to delete the old channel rows (DeleteChannel or
//     ClearForInterface) and re-upsert the new ones. The address-parameter
//     cache stays consistent because DeleteChannel evicts the channel's
//     contribution.
//
// This test verifies the documented "delete + re-upsert" path and confirms
// the cache is coherent throughout.
func TestDeviceModelChangeCoherencyViaWipeAndReUpsert(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()

	const (
		centralName = "ccu1"
		iface       = "HmIP-RF"
		chAddr      = "MDDEV:1"
		deviceAddr  = "MDDEV"
	)

	// First, store the "old model" paramset: parameter OLD_MODEL_PARAM.
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    centralName,
		InterfaceID:    iface,
		ChannelAddress: chAddr,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "old-model-hash",
		Paramset: hmproto.Paramset{
			"OLD_MODEL_PARAM": {Type: hmenum.ParameterTypeFloat},
		},
	}); err != nil {
		t.Fatalf("Phase1 Upsert: %v", err)
	}

	// Old model parameter must be in cache.
	if _, hit := s.cacheIsInMultipleChannels(deviceAddr, "OLD_MODEL_PARAM"); !hit {
		t.Error("Phase1: OLD_MODEL_PARAM must be in cache")
	}

	// Then the device model changes; the caller deletes the stale channel.
	if err := s.DeleteChannel(ctx, centralName, iface, chAddr); err != nil {
		t.Fatalf("DeleteChannel (model change): %v", err)
	}

	// After delete: the stale paramset row must be gone.
	if _, err := s.Get(ctx, centralName, iface, chAddr, hmenum.ParamsetKeyValues); !errors.Is(err, ErrParamsetNotFound) {
		t.Errorf("after DeleteChannel: want ErrParamsetNotFound, got %v", err)
	}
	// After delete: the old model's parameter must be gone from the cache
	// (DeleteChannel evicts the channel's contribution).
	if _, hit := s.cacheIsInMultipleChannels(deviceAddr, "OLD_MODEL_PARAM"); hit {
		t.Error("after DeleteChannel: OLD_MODEL_PARAM must not remain in cache")
	}

	// Finally, re-upsert with the new model's paramset.
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    centralName,
		InterfaceID:    iface,
		ChannelAddress: chAddr,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "new-model-hash",
		Paramset: hmproto.Paramset{
			"NEW_MODEL_PARAM": {Type: hmenum.ParameterTypeString},
		},
	}); err != nil {
		t.Fatalf("Phase3 Upsert: %v", err)
	}

	// New model parameter must be in cache.
	if _, hit := s.cacheIsInMultipleChannels(deviceAddr, "NEW_MODEL_PARAM"); !hit {
		t.Error("Phase3: NEW_MODEL_PARAM must be in cache after re-upsert")
	}

	// Old model parameter must still be absent (not re-introduced by new upsert).
	if _, hit := s.cacheIsInMultipleChannels(deviceAddr, "OLD_MODEL_PARAM"); hit {
		t.Error("Phase3: OLD_MODEL_PARAM must remain absent (new model does not carry it)")
	}

	// SQLite must reflect the new model shape.
	got, err := s.Get(ctx, centralName, iface, chAddr, hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("Get Phase3: %v", err)
	}
	if got.Hash != "new-model-hash" {
		t.Errorf("Phase3 hash=%q want new-model-hash", got.Hash)
	}
	if _, ok := got.Paramset["NEW_MODEL_PARAM"]; !ok {
		t.Error("Phase3: SQLite must contain NEW_MODEL_PARAM")
	}
	if _, ok := got.Paramset["OLD_MODEL_PARAM"]; ok {
		t.Error("Phase3: SQLite must NOT contain OLD_MODEL_PARAM after model-change re-upsert")
	}
}

// TestDeviceModelChangeNoAutomaticCacheInvalidation documents explicitly that
// a re-upsert with the SAME channel address (without first calling
// DeleteChannel) does NOT remove stale parameters from the address-parameter
// cache — only from SQLite.  The cache is additive on Upsert.
//
// This is expected behaviour: callers that change the device model must call
// DeleteChannel before re-upsert to keep the cache coherent.  Callers that
// only do an in-place patch (same parameters, modified values) never need
// DeleteChannel because the parameter set does not change.
func TestDeviceModelChangeNoAutomaticCacheInvalidation(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()

	const (
		centralName = "ccu1"
		iface       = "HmIP-RF"
		chAddr      = "NOCACHE:1"
		deviceAddr  = "NOCACHE"
	)

	// First upsert: STALE_PARAM.
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    centralName,
		InterfaceID:    iface,
		ChannelAddress: chAddr,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "v1",
		Paramset: hmproto.Paramset{
			"STALE_PARAM": {Type: hmenum.ParameterTypeFloat},
		},
	}); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	// Second upsert: same channel, different parameter set (STALE_PARAM gone,
	// FRESH_PARAM added). No DeleteChannel in between.
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    centralName,
		InterfaceID:    iface,
		ChannelAddress: chAddr,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "v2",
		Paramset: hmproto.Paramset{
			"FRESH_PARAM": {Type: hmenum.ParameterTypeFloat},
		},
	}); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	// SQLite has the new shape (full replace).
	got, err := s.Get(ctx, centralName, iface, chAddr, hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, ok := got.Paramset["STALE_PARAM"]; ok {
		t.Error("SQLite must NOT contain STALE_PARAM after full-replace upsert")
	}
	if _, ok := got.Paramset["FRESH_PARAM"]; !ok {
		t.Error("SQLite must contain FRESH_PARAM")
	}

	// The cache is additive: STALE_PARAM was added by the first Upsert and
	// is NOT removed by the second Upsert.  This is the documented limitation.
	_, staleHit := s.cacheIsInMultipleChannels(deviceAddr, "STALE_PARAM")
	if !staleHit {
		// This would mean the cache evicted STALE_PARAM — that would be a
		// behaviour change in the production code and should be re-evaluated.
		t.Log("note: cache evicted STALE_PARAM on re-upsert (behaviour may have changed)")
	}
	// FRESH_PARAM must always be in the cache after the second Upsert.
	if _, hit := s.cacheIsInMultipleChannels(deviceAddr, "FRESH_PARAM"); !hit {
		t.Error("cache must contain FRESH_PARAM after second Upsert")
	}
}
