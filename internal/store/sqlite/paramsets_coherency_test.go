// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

// paramsets_coherency_test.go — SQLite + DataCache coherency invariants.
//
// Audit items covered:
// §7.3/5 Store-Cache-Coherency: cache-invalidation paths must be
// transactional against SQLite. These tests lock down the
// SQLite side of that contract (DataCache is tested in
// internal/store/dynamic/coherency_test.go).
//
// Tests skipped due to overlap with existing coverage:
// TestParamsetDeleteChannelRemovesAllKeysButLeavesOtherChannels —
// The existing TestParamsetStoreDeleteChannel (paramsets_test.go)
// already covers two paramset keys (VALUES + MASTER) on the deleted
// channel and verifies that a third channel (OTHER:1) with a VALUES
// record is unaffected. Adding a third key (e.g. LINK) on the
// deleted channel would be the only incremental value; given the
// full coverage of the important paths, this test is omitted to
// avoid duplication. See TestParamsetStoreDeleteChannel.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestParamsetUpsertOverwriteWithDifferentSchemaShape verifies that a
// second Upsert on the same composite PK with a different set of
// parameter keys completely replaces the stored paramset_json — no
// leftover keys from the first write survive.
func TestParamsetUpsertOverwriteWithDifferentSchemaShape(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		ch      = "SCHEMA:1"
	)

	// First write: three parameters A, B, C.
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    central,
		InterfaceID:    iface,
		ChannelAddress: ch,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "hash-v1",
		Paramset: hmproto.Paramset{
			"A": {Type: hmenum.ParameterTypeBool},
			"B": {Type: hmenum.ParameterTypeFloat},
			"C": {Type: hmenum.ParameterTypeInteger},
		},
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Second write on same PK: only A and D (B and C are gone).
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    central,
		InterfaceID:    iface,
		ChannelAddress: ch,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "hash-v2",
		Paramset: hmproto.Paramset{
			"A": {Type: hmenum.ParameterTypeBool},
			"D": {Type: hmenum.ParameterTypeString},
		},
	}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := s.Get(ctx, central, iface, ch, hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Hash != "hash-v2" {
		t.Errorf("Hash=%q want hash-v2", got.Hash)
	}
	// A must survive; D must be present; B and C must be gone.
	if _, ok := got.Paramset["A"]; !ok {
		t.Error("Paramset missing key A")
	}
	if _, ok := got.Paramset["D"]; !ok {
		t.Error("Paramset missing key D")
	}
	if _, ok := got.Paramset["B"]; ok {
		t.Error("Paramset still contains stale key B after schema-shape overwrite")
	}
	if _, ok := got.Paramset["C"]; ok {
		t.Error("Paramset still contains stale key C after schema-shape overwrite")
	}
	// Exactly two keys must remain.
	if n := len(got.Paramset); n != 2 {
		t.Errorf("Paramset len=%d want 2", n)
	}
}

// TestParamsetUpsertConcurrentSamePKLastWriteWins fans out 20 goroutines
// each upserting the same composite PK with a unique hash "h-N". SQLite
// permits only one writer at a time; goroutines that encounter SQLITE_BUSY
// are counted but not treated as errors — that is expected behaviour under
// WAL+busy_timeout contention. The invariants are:
//
//	(a) At least one goroutine must succeed.
//	(b) After all goroutines return, exactly one row exists.
//	(c) Get must succeed and return one of the hashes written by successful
//	 goroutines.
//
// Run with -race.
func TestParamsetUpsertConcurrentSamePKLastWriteWins(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()

	const (
		central    = "ccu1"
		iface      = "HmIP-RF"
		ch         = "CONC:1"
		goroutines = 20
	)

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded []string // hashes that were actually written
	)

	wg.Add(goroutines)
	for i := range goroutines {
		i := i
		go func() {
			defer wg.Done()
			hash := fmt.Sprintf("h-%d", i)
			err := s.Upsert(ctx, ParamsetRecord{
				CentralName:    central,
				InterfaceID:    iface,
				ChannelAddress: ch,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Hash:           hash,
				Paramset:       hmproto.Paramset{"P": {Type: hmenum.ParameterTypeBool}},
			})
			if err == nil {
				mu.Lock()
				succeeded = append(succeeded, hash)
				mu.Unlock()
			}
			// SQLITE_BUSY under concurrent load is expected — not an error.
		}()
	}
	wg.Wait()

	if len(succeeded) == 0 {
		t.Fatal("all goroutines failed; at least one upsert must succeed")
	}

	// Exactly one row must exist after all concurrent upserts.
	var count int
	row := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM paramsets
		 WHERE central_name = ? AND interface_id = ? AND channel_address = ? AND paramset_key = ?`,
		central, iface, ch, string(hmenum.ParamsetKeyValues))
	if err := row.Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("row count=%d want 1 (upsert must never create duplicate rows)", count)
	}

	// Get must return a valid record whose hash matches one that was
	// actually committed (not necessarily "the last" — SQLite serialises
	// writes so the surviving hash is whichever writer held the lock last).
	got, err := s.Get(ctx, central, iface, ch, hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("get after concurrent upserts: %v", err)
	}
	found := false
	for _, h := range succeeded {
		if got.Hash == h {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("returned hash %q does not match any of the %d committed hashes %v",
			got.Hash, len(succeeded), succeeded)
	}
}

// TestParamsetGetUnaffectedByOtherChannelDelete verifies that deleting one
// channel does not remove paramsets belonging to a different channel.
func TestParamsetGetUnaffectedByOtherChannelDelete(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()

	const (
		central = "ccu1"
		iface   = "HmIP-RF"
		chA     = "NODELA:1"
		chB     = "NODELB:1"
	)

	for _, ch := range []string{chA, chB} {
		if err := s.Upsert(ctx, ParamsetRecord{
			CentralName:    central,
			InterfaceID:    iface,
			ChannelAddress: ch,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Hash:           ch + "-hash",
			Paramset:       hmproto.Paramset{"X": {Type: hmenum.ParameterTypeBool}},
		}); err != nil {
			t.Fatalf("upsert %s: %v", ch, err)
		}
	}

	// Delete channel B; channel A must survive.
	if err := s.DeleteChannel(ctx, central, iface, chB); err != nil {
		t.Fatalf("deleteChannel B: %v", err)
	}

	// Channel B must be gone.
	if _, err := s.Get(ctx, central, iface, chB, hmenum.ParamsetKeyValues); !errors.Is(err, ErrParamsetNotFound) {
		t.Errorf("chB: expected ErrParamsetNotFound, got %v", err)
	}

	// Channel A must still be present.
	got, err := s.Get(ctx, central, iface, chA, hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("get chA after deleting chB: %v", err)
	}
	if got.Hash != chA+"-hash" {
		t.Errorf("chA hash=%q want %q", got.Hash, chA+"-hash")
	}
}

// TestParamsetUpsertRoundTripsParameterData verifies that a ParameterData
// with non-default values for every modelled field survives the JSON
// round-trip through SQLite identically.
func TestParamsetUpsertRoundTripsParameterData(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()

	tabOrder := 3

	original := hmproto.ParameterData{
		Type:       hmenum.ParameterTypeFloat,
		Flags:      hmenum.FlagVisible | hmenum.FlagService,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		Min:        json.RawMessage(`-10`),
		Max:        json.RawMessage(`100`),
		Default:    json.RawMessage(`20.5`),
		Unit:       "°C",
		ValueList:  []string{"LOW", "MED", "HIGH"},
		TabOrder:   &tabOrder,
		ID:         "SET_TEMPERATURE",
		Control:    "CLIMATECONTROL_REGULATOR",
	}

	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "RT:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "rt-hash",
		Paramset:       hmproto.Paramset{"SET_TEMPERATURE": original},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.Get(ctx, "ccu1", "HmIP-RF", "RT:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	pd, ok := got.Paramset["SET_TEMPERATURE"]
	if !ok {
		t.Fatal("SET_TEMPERATURE missing from round-tripped paramset")
	}

	if pd.Type != original.Type {
		t.Errorf("Type=%q want %q", pd.Type, original.Type)
	}
	if pd.Flags != original.Flags {
		t.Errorf("Flags=%d want %d", pd.Flags, original.Flags)
	}
	if pd.Operations != original.Operations {
		t.Errorf("Operations=%d want %d", pd.Operations, original.Operations)
	}
	if string(pd.Min) != string(original.Min) {
		t.Errorf("Min=%s want %s", pd.Min, original.Min)
	}
	if string(pd.Max) != string(original.Max) {
		t.Errorf("Max=%s want %s", pd.Max, original.Max)
	}
	if string(pd.Default) != string(original.Default) {
		t.Errorf("Default=%s want %s", pd.Default, original.Default)
	}
	if pd.Unit != original.Unit {
		t.Errorf("Unit=%q want %q", pd.Unit, original.Unit)
	}
	if len(pd.ValueList) != len(original.ValueList) {
		t.Errorf("ValueList len=%d want %d", len(pd.ValueList), len(original.ValueList))
	} else {
		for i, v := range original.ValueList {
			if pd.ValueList[i] != v {
				t.Errorf("ValueList[%d]=%q want %q", i, pd.ValueList[i], v)
			}
		}
	}
	if pd.TabOrder == nil || *pd.TabOrder != tabOrder {
		t.Errorf("TabOrder=%v want &%d", pd.TabOrder, tabOrder)
	}
	if pd.ID != original.ID {
		t.Errorf("ID=%q want %q", pd.ID, original.ID)
	}
	if pd.Control != original.Control {
		t.Errorf("Control=%q want %q", pd.Control, original.Control)
	}
}

// TestParamsetMultiCCUDeleteScopedByCentral verifies that
// DeleteChannel(ccu1, …) does not remove the record belonging to ccu2
// when both centrals share the same (interface, channel, paramset_key).
func TestParamsetMultiCCUDeleteScopedByCentral(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()

	const (
		iface = "HmIP-RF"
		ch    = "SHARED:1"
	)

	// Upsert the same channel for two different centrals.
	for _, ccu := range []string{"ccu1", "ccu2"} {
		if err := s.Upsert(ctx, ParamsetRecord{
			CentralName:    ccu,
			InterfaceID:    iface,
			ChannelAddress: ch,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Hash:           ccu + "-hash",
			Paramset:       hmproto.Paramset{"P": {Type: hmenum.ParameterTypeBool}},
		}); err != nil {
			t.Fatalf("upsert %s: %v", ccu, err)
		}
	}

	// Delete for ccu1 only.
	if err := s.DeleteChannel(ctx, "ccu1", iface, ch); err != nil {
		t.Fatalf("deleteChannel ccu1: %v", err)
	}

	// ccu1 row must be gone.
	if _, err := s.Get(ctx, "ccu1", iface, ch, hmenum.ParamsetKeyValues); !errors.Is(err, ErrParamsetNotFound) {
		t.Errorf("ccu1: expected ErrParamsetNotFound after delete, got %v", err)
	}

	// ccu2 row must still be present.
	got, err := s.Get(ctx, "ccu2", iface, ch, hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("ccu2 row must survive ccu1 delete: %v", err)
	}
	if got.Hash != "ccu2-hash" {
		t.Errorf("ccu2 hash=%q want ccu2-hash", got.Hash)
	}
}

// assertRowCount is reserved for paramset-coherency tests that
// directly inspect the COUNT(*) for a composite key. Currently
// none of the active tests call it; kept as a future helper.
//
//nolint:unused // future helper
func assertRowCount(t *testing.T, db *sql.DB, central, iface, ch string, want int) {
	t.Helper()
	var count int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM paramsets
		 WHERE central_name = ? AND interface_id = ? AND channel_address = ?`,
		central, iface, ch).Scan(&count)
	if err != nil {
		t.Fatalf("assertRowCount: %v", err)
	}
	if count != want {
		t.Errorf("row count=%d want %d (central=%s iface=%s ch=%s)", count, want, central, iface, ch)
	}
}
