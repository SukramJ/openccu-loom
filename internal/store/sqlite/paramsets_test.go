// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func freshParamsetStore(t *testing.T) *ParamsetStore {
	t.Helper()
	return NewParamsetStore(openTestDB(t, "ps.db"))
}

func richParamset() hmproto.Paramset {
	return hmproto.Paramset{
		"LEVEL": hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: 7,
		},
		"SET_TEMPERATURE": hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: 7,
		},
		"BOOST_MODE": hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: 7,
		},
	}
}

func upsertTestParamset(t *testing.T, s *ParamsetStore, centralName, iface, chAddr string, psKey hmenum.ParamsetKey) {
	t.Helper()
	rec := ParamsetRecord{
		CentralName:    centralName,
		InterfaceID:    iface,
		ChannelAddress: chAddr,
		ParamsetKey:    psKey,
		Hash:           "hash1",
		Paramset: hmproto.Paramset{
			"LEVEL": {Type: "FLOAT", Min: []byte(`0.0`), Max: []byte(`1.0`)},
			"STATE": {Type: "BOOL"},
		},
	}
	if err := s.Upsert(context.Background(), rec); err != nil {
		t.Fatalf("Upsert %s/%s/%s/%s: %v", centralName, iface, chAddr, psKey, err)
	}
}

// TestParamsetStoreInsertAndGet verifies a full round-trip including
// all primary-key fields, the hash, and the paramset_json blob.
func TestParamsetStoreInsertAndGet(t *testing.T) {
	s := freshParamsetStore(t)
	ctx := context.Background()

	ps := richParamset()
	rec := ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "ABC123:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "hash-v1",
		Paramset:       ps,
	}
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.Get(ctx, "ccu1", "HmIP-RF", "ABC123:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.CentralName != "ccu1" {
		t.Errorf("CentralName=%q want ccu1", got.CentralName)
	}
	if got.InterfaceID != "HmIP-RF" {
		t.Errorf("InterfaceID=%q want HmIP-RF", got.InterfaceID)
	}
	if got.ChannelAddress != "ABC123:1" {
		t.Errorf("ChannelAddress=%q want ABC123:1", got.ChannelAddress)
	}
	if got.ParamsetKey != hmenum.ParamsetKeyValues {
		t.Errorf("ParamsetKey=%q want VALUES", got.ParamsetKey)
	}
	if got.Hash != "hash-v1" {
		t.Errorf("Hash=%q want hash-v1", got.Hash)
	}

	// paramset_json round-trip — all three parameters must survive.
	for _, key := range []string{"LEVEL", "SET_TEMPERATURE", "BOOST_MODE"} {
		if _, ok := got.Paramset[key]; !ok {
			t.Errorf("Paramset missing key %q", key)
		}
	}
	if got.Paramset["LEVEL"].Type != hmenum.ParameterTypeFloat {
		t.Errorf("LEVEL.Type=%q want %q", got.Paramset["LEVEL"].Type, hmenum.ParameterTypeFloat)
	}
	if got.Paramset["BOOST_MODE"].Type != hmenum.ParameterTypeBool {
		t.Errorf("BOOST_MODE.Type=%q want %q", got.Paramset["BOOST_MODE"].Type, hmenum.ParameterTypeBool)
	}
}

// TestParamsetStoreGetMissingReturnsErrParamsetNotFound checks the
// nil-safe / sentinel behaviour on a non-existent composite key.
func TestParamsetStoreGetMissingReturnsErrParamsetNotFound(t *testing.T) {
	s := freshParamsetStore(t)
	_, err := s.Get(context.Background(), "ccu1", "HmIP-RF", "GHOST:0", hmenum.ParamsetKeyValues)
	if !errors.Is(err, ErrParamsetNotFound) {
		t.Fatalf("got %v, want ErrParamsetNotFound", err)
	}
}

// TestParamsetStoreUpsertUpdatesExistingRow verifies the ON CONFLICT path
// updates hash and paramset_json without creating a duplicate.
func TestParamsetStoreUpsertUpdatesExistingRow(t *testing.T) {
	s := freshParamsetStore(t)
	ctx := context.Background()

	rec := ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "UPS:1",
		ParamsetKey:    hmenum.ParamsetKeyMaster,
		Hash:           "old-hash",
		Paramset:       hmproto.Paramset{"A": {Type: hmenum.ParameterTypeBool}},
	}
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Change hash and paramset content, re-upsert with same PK.
	rec.Hash = "new-hash"
	rec.Paramset = hmproto.Paramset{
		"A": {Type: hmenum.ParameterTypeBool},
		"B": {Type: hmenum.ParameterTypeFloat},
	}
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := s.Get(ctx, "ccu1", "HmIP-RF", "UPS:1", hmenum.ParamsetKeyMaster)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Hash != "new-hash" {
		t.Errorf("Hash=%q want new-hash", got.Hash)
	}
	if _, ok := got.Paramset["B"]; !ok {
		t.Errorf("updated paramset missing key B")
	}
}

// TestParamsetStoreDeleteChannel removes all paramsets for a channel and
// verifies none remain while paramsets on other channels survive.
func TestParamsetStoreDeleteChannel(t *testing.T) {
	s := freshParamsetStore(t)
	ctx := context.Background()

	// Two paramset keys for the same channel.
	for _, key := range []hmenum.ParamsetKey{hmenum.ParamsetKeyValues, hmenum.ParamsetKeyMaster} {
		if err := s.Upsert(ctx, ParamsetRecord{
			CentralName:    "ccu1",
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "DEL:1",
			ParamsetKey:    key,
			Hash:           "h",
			Paramset:       hmproto.Paramset{"X": {Type: hmenum.ParameterTypeBool}},
		}); err != nil {
			t.Fatalf("upsert key %q: %v", key, err)
		}
	}
	// A paramset on a different channel that must not be deleted.
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "OTHER:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "h",
		Paramset:       hmproto.Paramset{"Y": {Type: hmenum.ParameterTypeFloat}},
	}); err != nil {
		t.Fatalf("upsert other: %v", err)
	}

	if err := s.DeleteChannel(ctx, "ccu1", "HmIP-RF", "DEL:1"); err != nil {
		t.Fatalf("deleteChannel: %v", err)
	}

	// Both paramsets for DEL:1 must now be gone.
	for _, key := range []hmenum.ParamsetKey{hmenum.ParamsetKeyValues, hmenum.ParamsetKeyMaster} {
		_, err := s.Get(ctx, "ccu1", "HmIP-RF", "DEL:1", key)
		if !errors.Is(err, ErrParamsetNotFound) {
			t.Errorf("key %q: got %v, want ErrParamsetNotFound", key, err)
		}
	}

	// The untouched channel must survive.
	if _, err := s.Get(ctx, "ccu1", "HmIP-RF", "OTHER:1", hmenum.ParamsetKeyValues); err != nil {
		t.Errorf("other channel deleted unexpectedly: %v", err)
	}
}

// TestParamsetStoreDifferentParamsetKeysCoexist ensures that VALUES and
// MASTER records for the same channel do not overwrite each other.
func TestParamsetStoreDifferentParamsetKeysCoexist(t *testing.T) {
	s := freshParamsetStore(t)
	ctx := context.Background()

	valuesRec := ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "MIX:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "val-hash",
		Paramset:       hmproto.Paramset{"LEVEL": {Type: hmenum.ParameterTypeFloat}},
	}
	masterRec := ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "MIX:1",
		ParamsetKey:    hmenum.ParamsetKeyMaster,
		Hash:           "mas-hash",
		Paramset:       hmproto.Paramset{"CYCLIC_INFO_MSG": {Type: hmenum.ParameterTypeBool}},
	}

	for _, r := range []ParamsetRecord{valuesRec, masterRec} {
		if err := s.Upsert(ctx, r); err != nil {
			t.Fatalf("upsert %q: %v", r.ParamsetKey, err)
		}
	}

	gotVal, err := s.Get(ctx, "ccu1", "HmIP-RF", "MIX:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("get VALUES: %v", err)
	}
	gotMas, err := s.Get(ctx, "ccu1", "HmIP-RF", "MIX:1", hmenum.ParamsetKeyMaster)
	if err != nil {
		t.Fatalf("get MASTER: %v", err)
	}

	if _, ok := gotVal.Paramset["LEVEL"]; !ok {
		t.Error("VALUES record lost LEVEL")
	}
	if _, ok := gotMas.Paramset["CYCLIC_INFO_MSG"]; !ok {
		t.Error("MASTER record lost CYCLIC_INFO_MSG")
	}
	if gotVal.Hash != "val-hash" {
		t.Errorf("VALUES Hash=%q want val-hash", gotVal.Hash)
	}
	if gotMas.Hash != "mas-hash" {
		t.Errorf("MASTER Hash=%q want mas-hash", gotMas.Hash)
	}
}

// TestParamsetStoreMultiCCUIsolation verifies that central_name scopes
// paramsets correctly — a record for "ccu1" must not be visible under "ccu2".
func TestParamsetStoreMultiCCUIsolation(t *testing.T) {
	s := freshParamsetStore(t)
	ctx := context.Background()

	for _, ccu := range []string{"ccu1", "ccu2"} {
		if err := s.Upsert(ctx, ParamsetRecord{
			CentralName:    ccu,
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "SHARED:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Hash:           ccu + "-hash",
			Paramset:       hmproto.Paramset{"P": {Type: hmenum.ParameterTypeBool}},
		}); err != nil {
			t.Fatalf("upsert %s: %v", ccu, err)
		}
	}

	got1, err := s.Get(ctx, "ccu1", "HmIP-RF", "SHARED:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("get ccu1: %v", err)
	}
	got2, err := s.Get(ctx, "ccu2", "HmIP-RF", "SHARED:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("get ccu2: %v", err)
	}
	if got1.Hash == got2.Hash {
		t.Errorf("ccu isolation broken: both have Hash=%q", got1.Hash)
	}
}

// TestParamsetStoreNoteOnListAbsence documents that ParamsetStore has no
// ListByChannel method. Callers retrieve individual paramset keys via
// Get; per-channel enumeration (e.g. for cache warm-up) would require
// an explicit query or an additional method. This is noted as a gap for
// future consideration but is not a bug in the current implementation.
func TestParamsetStoreNoteOnListAbsence(t *testing.T) {
	// This test intentionally has no assertions — it exists to document
	// the design gap and will act as a natural placeholder for a future
	// ListByChannel method.
	_ = freshParamsetStore(t)
}

// ---------------------------------------------------------------------------
// ParamsetStore.Size
// ---------------------------------------------------------------------------

func TestParamsetStoreSize(t *testing.T) {
	s := freshParamsetStore(t)
	ctx := context.Background()

	n, err := s.Size(ctx, "ccu1")
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if n != 0 {
		t.Errorf("Size on empty store=%d want 0", n)
	}

	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "CH:1", hmenum.ParamsetKeyValues)
	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "CH:1", hmenum.ParamsetKeyMaster)
	// Different central.
	upsertTestParamset(t, s, "ccu2", "HmIP-RF", "CH:1", hmenum.ParamsetKeyValues)

	n, err = s.Size(ctx, "ccu1")
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if n != 2 {
		t.Errorf("Size=%d want 2", n)
	}
}

// ---------------------------------------------------------------------------
// ParamsetStore.GetChannelParamsetDescriptions
// ---------------------------------------------------------------------------

func TestParamsetStoreGetChannelParamsetDescriptions(t *testing.T) {
	s := freshParamsetStore(t)
	ctx := context.Background()

	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "CH:2", hmenum.ParamsetKeyValues)
	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "CH:2", hmenum.ParamsetKeyMaster)
	// Other channel — must not appear.
	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "OTHER:1", hmenum.ParamsetKeyValues)

	result, err := s.GetChannelParamsetDescriptions(ctx, "ccu1", "HmIP-RF", "CH:2")
	if err != nil {
		t.Fatalf("GetChannelParamsetDescriptions: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len=%d want 2", len(result))
	}
	if _, ok := result[hmenum.ParamsetKeyValues]; !ok {
		t.Error("VALUES not in result")
	}
	if _, ok := result[hmenum.ParamsetKeyMaster]; !ok {
		t.Error("MASTER not in result")
	}
}

// ---------------------------------------------------------------------------
// ParamsetStore.GetParamsetKeys
// ---------------------------------------------------------------------------

func TestParamsetStoreGetParamsetKeys(t *testing.T) {
	s := freshParamsetStore(t)
	ctx := context.Background()

	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "CH:3", hmenum.ParamsetKeyValues)
	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "CH:3", hmenum.ParamsetKeyMaster)

	keys, err := s.GetParamsetKeys(ctx, "ccu1", "HmIP-RF", "CH:3")
	if err != nil {
		t.Fatalf("GetParamsetKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("len=%d want 2", len(keys))
	}
	// Result must contain both keys (order may vary).
	found := map[hmenum.ParamsetKey]bool{}
	for _, k := range keys {
		found[k] = true
	}
	if !found[hmenum.ParamsetKeyValues] {
		t.Error("VALUES not in keys")
	}
	if !found[hmenum.ParamsetKeyMaster] {
		t.Error("MASTER not in keys")
	}
}

// ---------------------------------------------------------------------------
// ParamsetStore.HasInterfaceID
// ---------------------------------------------------------------------------

func TestParamsetStoreHasInterfaceID(t *testing.T) {
	s := freshParamsetStore(t)
	ctx := context.Background()

	has, err := s.HasInterfaceID(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("HasInterfaceID: %v", err)
	}
	if has {
		t.Error("HasInterfaceID on empty store must be false")
	}

	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "CH:1", hmenum.ParamsetKeyValues)

	has, err = s.HasInterfaceID(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("HasInterfaceID after insert: %v", err)
	}
	if !has {
		t.Error("HasInterfaceID after insert must be true")
	}

	// Different interface must still return false.
	has2, err := s.HasInterfaceID(ctx, "ccu1", "BidCos-RF")
	if err != nil {
		t.Fatalf("HasInterfaceID other iface: %v", err)
	}
	if has2 {
		t.Error("HasInterfaceID for interface with no records must be false")
	}
}

// ---------------------------------------------------------------------------
// ParamsetStore.HasParameter
// ---------------------------------------------------------------------------

func TestParamsetStoreHasParameter(t *testing.T) {
	s := freshParamsetStore(t)
	ctx := context.Background()

	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "CH:5", hmenum.ParamsetKeyValues)

	has, err := s.HasParameter(ctx, "ccu1", "HmIP-RF", "CH:5", hmenum.ParamsetKeyValues, "LEVEL")
	if err != nil {
		t.Fatalf("HasParameter LEVEL: %v", err)
	}
	if !has {
		t.Error("HasParameter LEVEL must be true")
	}

	has2, err := s.HasParameter(ctx, "ccu1", "HmIP-RF", "CH:5", hmenum.ParamsetKeyValues, "GHOST_PARAM")
	if err != nil {
		t.Fatalf("HasParameter GHOST: %v", err)
	}
	if has2 {
		t.Error("HasParameter GHOST_PARAM must be false")
	}

	// Non-existent channel.
	has3, err := s.HasParameter(ctx, "ccu1", "HmIP-RF", "GHOST_CH:1", hmenum.ParamsetKeyValues, "LEVEL")
	if err != nil {
		t.Fatalf("HasParameter ghost channel: %v", err)
	}
	if has3 {
		t.Error("HasParameter for ghost channel must be false")
	}
}

// ---------------------------------------------------------------------------
// ParamsetStore.GetParameterData
// ---------------------------------------------------------------------------

func TestParamsetStoreGetParameterData(t *testing.T) {
	s := freshParamsetStore(t)
	ctx := context.Background()

	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "CH:6", hmenum.ParamsetKeyValues)

	pd, err := s.GetParameterData(ctx, "ccu1", "HmIP-RF", "CH:6", hmenum.ParamsetKeyValues, "STATE")
	if err != nil {
		t.Fatalf("GetParameterData: %v", err)
	}
	if pd == nil {
		t.Fatal("GetParameterData returned nil for existing parameter")
	}
	if pd.Type != "BOOL" {
		t.Errorf("Type=%q want BOOL", pd.Type)
	}

	// Missing parameter.
	pd2, err := s.GetParameterData(ctx, "ccu1", "HmIP-RF", "CH:6", hmenum.ParamsetKeyValues, "NOPE")
	if err != nil {
		t.Fatalf("GetParameterData for missing param: %v", err)
	}
	if pd2 != nil {
		t.Errorf("GetParameterData for missing param must return nil, got %+v", pd2)
	}

	// Missing channel.
	pd3, err := s.GetParameterData(ctx, "ccu1", "HmIP-RF", "GHOST:1", hmenum.ParamsetKeyValues, "LEVEL")
	if err != nil {
		t.Fatalf("GetParameterData for ghost channel: %v", err)
	}
	if pd3 != nil {
		t.Errorf("GetParameterData for ghost channel must return nil, got %+v", pd3)
	}
}

// ---------------------------------------------------------------------------
// ParamsetStore.GetChannelAddressesByParamsetKey
// ---------------------------------------------------------------------------

func TestParamsetStoreGetChannelAddressesByParamsetKey(t *testing.T) {
	s := freshParamsetStore(t)
	ctx := context.Background()

	// Device-level (no colon suffix) and channels.
	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "DEV7", hmenum.ParamsetKeyMaster)
	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "DEV7:1", hmenum.ParamsetKeyValues)
	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "DEV7:2", hmenum.ParamsetKeyValues)
	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "DEV7:2", hmenum.ParamsetKeyMaster)
	// Unrelated device.
	upsertTestParamset(t, s, "ccu1", "HmIP-RF", "OTHER:1", hmenum.ParamsetKeyValues)

	result, err := s.GetChannelAddressesByParamsetKey(ctx, "ccu1", "HmIP-RF", "DEV7")
	if err != nil {
		t.Fatalf("GetChannelAddressesByParamsetKey: %v", err)
	}

	valAddrs := result[hmenum.ParamsetKeyValues]
	if len(valAddrs) != 2 {
		t.Errorf("VALUES channels len=%d want 2, got %v", len(valAddrs), valAddrs)
	}
	masterAddrs := result[hmenum.ParamsetKeyMaster]
	if len(masterAddrs) != 2 {
		t.Errorf("MASTER channels len=%d want 2, got %v", len(masterAddrs), masterAddrs)
	}
}

// ---------------------------------------------------------------------------
// ParamsetStore.Size and HasInterfaceID (second variant — in-memory)
// ---------------------------------------------------------------------------

func TestParamsetStoreSizeAndHasInterfaceID(t *testing.T) {
	_, ps, _ := openMem(t)
	ctx := context.Background()

	// Empty.
	n, err := ps.Size(ctx, "ccu1")
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if n != 0 {
		t.Errorf("Size on empty store=%d, want 0", n)
	}
	has, err := ps.HasInterfaceID(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("HasInterfaceID: %v", err)
	}
	if has {
		t.Error("HasInterfaceID must be false on empty store")
	}

	// Insert one paramset.
	if err := ps.Upsert(ctx, ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "ABC:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "h1",
		Paramset:       hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	n, _ = ps.Size(ctx, "ccu1")
	if n != 1 {
		t.Errorf("Size after insert=%d, want 1", n)
	}
	has, _ = ps.HasInterfaceID(ctx, "ccu1", "HmIP-RF")
	if !has {
		t.Error("HasInterfaceID must be true after insert")
	}
	hasNot, _ := ps.HasInterfaceID(ctx, "ccu1", "BidCos-RF")
	if hasNot {
		t.Error("HasInterfaceID must be false for unknown interface")
	}
}

// ---------------------------------------------------------------------------
// ParamsetStore.ListByCentral
// ---------------------------------------------------------------------------

// TestParamsetStoreListByCentral verifies that ListByCentral scopes rows to
// the requested central, returns them ordered by interface_id, then
// channel_address, then paramset_key, and skips rows whose schema_version
// has fallen behind [ParamsetCacheSchemaVersion] (the boot-time hydration
// path must never resurrect a stale cache entry).
func TestParamsetStoreListByCentral(t *testing.T) {
	db := openTestDB(t, "list_by_central.db")
	s := NewParamsetStore(db)
	ctx := context.Background()

	upsert := func(centralName, iface, chAddr string, psKey hmenum.ParamsetKey) {
		t.Helper()
		if err := s.Upsert(ctx, ParamsetRecord{
			CentralName:    centralName,
			InterfaceID:    iface,
			ChannelAddress: chAddr,
			ParamsetKey:    psKey,
			Hash:           "h-" + centralName + "-" + iface + "-" + chAddr + "-" + string(psKey),
			Paramset:       hmproto.Paramset{"X": {Type: hmenum.ParameterTypeBool}},
		}); err != nil {
			t.Fatalf("upsert %s/%s/%s/%s: %v", centralName, iface, chAddr, psKey, err)
		}
	}

	// Two interfaces for the central under test.
	upsert("ccu1", "HmIP-RF", "AAA:1", hmenum.ParamsetKeyValues)
	upsert("ccu1", "HmIP-RF", "AAA:1", hmenum.ParamsetKeyMaster)
	upsert("ccu1", "BidCos-RF", "BBB:1", hmenum.ParamsetKeyValues)
	// A foreign central — must never leak into ccu1's result.
	upsert("ccu2", "HmIP-RF", "ZZZ:1", hmenum.ParamsetKeyValues)

	recs, err := s.ListByCentral(ctx, "ccu1")
	if err != nil {
		t.Fatalf("ListByCentral: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("len=%d want 3, got %+v", len(recs), recs)
	}
	for _, r := range recs {
		if r.CentralName != "ccu1" {
			t.Fatalf("foreign central row leaked into ListByCentral: %+v", r)
		}
	}
	// Deterministic order: interface_id ("BidCos-RF" < "HmIP-RF"), then
	// channel_address, then paramset_key ("MASTER" < "VALUES").
	if recs[0].InterfaceID != "BidCos-RF" || recs[0].ChannelAddress != "BBB:1" {
		t.Errorf("recs[0]=%+v want BidCos-RF/BBB:1 first", recs[0])
	}
	if recs[1].InterfaceID != "HmIP-RF" || recs[1].ParamsetKey != hmenum.ParamsetKeyMaster {
		t.Errorf("recs[1]=%+v want HmIP-RF/MASTER (MASTER sorts before VALUES)", recs[1])
	}
	if recs[2].InterfaceID != "HmIP-RF" || recs[2].ParamsetKey != hmenum.ParamsetKeyValues {
		t.Errorf("recs[2]=%+v want HmIP-RF/VALUES last", recs[2])
	}

	// Downgrade one row's schema_version directly, bypassing Upsert (which
	// always writes the current version) to simulate a stale on-disk row
	// left behind by an older binary.
	if _, err := db.ExecContext(
		ctx,
		`UPDATE paramsets SET schema_version = 0 WHERE central_name = 'ccu1' AND channel_address = 'AAA:1' AND paramset_key = ?`,
		string(hmenum.ParamsetKeyValues),
	); err != nil {
		t.Fatalf("downgrade schema_version: %v", err)
	}

	recs2, err := s.ListByCentral(ctx, "ccu1")
	if err != nil {
		t.Fatalf("ListByCentral after downgrade: %v", err)
	}
	if len(recs2) != 2 {
		t.Fatalf("len=%d after downgrade, want 2, got %+v", len(recs2), recs2)
	}
	for _, r := range recs2 {
		if r.ChannelAddress == "AAA:1" && r.ParamsetKey == hmenum.ParamsetKeyValues {
			t.Fatalf("downgraded row must be excluded from ListByCentral, got %+v", r)
		}
	}
}

// ---------------------------------------------------------------------------
// ParamsetStore.ClearForInterface
// ---------------------------------------------------------------------------

func TestParamsetStoreClearForInterfaceEmptyIsNoOp(t *testing.T) {
	t.Parallel()
	s := NewParamsetStore(openTestDB(t, "ps_clear_empty.db"))
	ctx := context.Background()

	n, err := s.ClearForInterface(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("ClearForInterface: %v", err)
	}
	if n != 0 {
		t.Errorf("ClearForInterface on empty store returned %d, want 0", n)
	}
}

func TestParamsetStoreClearForInterfaceRemovesOnlyMatchingRows(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "ps_clear.db")
	s := NewParamsetStore(db)
	ctx := context.Background()

	upsertClear := func(centralName, iface, channel string) {
		t.Helper()
		err := s.Upsert(ctx, ParamsetRecord{
			CentralName:    centralName,
			InterfaceID:    iface,
			ChannelAddress: channel,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Hash:           "h",
			Paramset:       hmproto.Paramset{},
		})
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	upsertClear("ccu1", "HmIP-RF", "VCU0000001:0")
	upsertClear("ccu1", "HmIP-RF", "VCU0000001:1")
	upsertClear("ccu1", "BidCos-RF", "DEF0000001:0")
	upsertClear("ccu2", "HmIP-RF", "XYZ0000001:0")

	n, err := s.ClearForInterface(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("ClearForInterface: %v", err)
	}
	if n != 2 {
		t.Errorf("ClearForInterface returned %d, want 2", n)
	}

	// BidCos-RF rows must still exist.
	ok, err := s.HasInterfaceID(ctx, "ccu1", "BidCos-RF")
	if err != nil {
		t.Fatalf("HasInterfaceID BidCos-RF: %v", err)
	}
	if !ok {
		t.Error("HasInterfaceID must return true for BidCos-RF after clearing HmIP-RF")
	}

	// ccu2 rows must still exist.
	ok, err = s.HasInterfaceID(ctx, "ccu2", "HmIP-RF")
	if err != nil {
		t.Fatalf("HasInterfaceID ccu2: %v", err)
	}
	if !ok {
		t.Error("HasInterfaceID must return true for ccu2 HmIP-RF after clearing ccu1 HmIP-RF")
	}

	// ccu1 HmIP-RF must now be gone.
	ok, err = s.HasInterfaceID(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("HasInterfaceID after clear: %v", err)
	}
	if ok {
		t.Error("HasInterfaceID must return false for cleared interface")
	}
}
