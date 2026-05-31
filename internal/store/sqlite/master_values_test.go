// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func freshMasterValuesStore(t *testing.T) *MasterValuesStore {
	t.Helper()
	return NewMasterValuesStore(openTestDB(t, "mv.db"))
}

// TestMasterValues_LoadChannel_EmptyCache_ReturnsOkFalse verifies that a
// LoadChannel on a never-written channel returns ok=false and no error.
func TestMasterValues_LoadChannel_EmptyCache_ReturnsOkFalse(t *testing.T) {
	t.Parallel()
	s := freshMasterValuesStore(t)
	ctx := context.Background()

	got, ok, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "GHOST:0")
	if err != nil {
		t.Fatalf("LoadChannel: unexpected error: %v", err)
	}
	if ok {
		t.Errorf("ok = true, want false for never-written channel")
	}
	if got != nil {
		t.Errorf("got = %v, want nil", got)
	}
}

// TestMasterValues_SaveAndLoadRoundtrip verifies that a map of mixed-type
// values survives a full SaveChannel → LoadChannel cycle.
//
// JSON decodes numbers as float64 by default; the test normalises both sides
// through json.Marshal+Unmarshal so the comparison is type-stable.
func TestMasterValues_SaveAndLoadRoundtrip(t *testing.T) {
	t.Parallel()
	s := freshMasterValuesStore(t)
	ctx := context.Background()

	input := map[string]any{
		"PARAM_A": 42,
		"PARAM_B": "x",
		"PARAM_C": true,
	}

	if err := s.SaveChannel(ctx, "ccu1", "HmIP-RF", "DEV:1", input); err != nil {
		t.Fatalf("SaveChannel: %v", err)
	}

	got, ok, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEV:1")
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if !ok {
		t.Fatal("LoadChannel: ok = false, want true")
	}

	// Normalise input through a JSON round-trip so integer 42 becomes
	// float64(42), matching what the store returns via json.Unmarshal.
	want := jsonRoundtrip(t, input)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadChannel result mismatch\n got: %v\nwant: %v", got, want)
	}
}

// TestMasterValues_SaveChannel_NilValuesSkipped verifies that nil entries
// in the input map are not persisted while non-nil entries are.
func TestMasterValues_SaveChannel_NilValuesSkipped(t *testing.T) {
	t.Parallel()
	s := freshMasterValuesStore(t)
	ctx := context.Background()

	if err := s.SaveChannel(ctx, "ccu1", "HmIP-RF", "DEV:2", map[string]any{
		"KEEP": "yes",
		"SKIP": nil,
	}); err != nil {
		t.Fatalf("SaveChannel: %v", err)
	}

	got, ok, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEV:2")
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if !ok {
		t.Fatal("LoadChannel: ok = false after partial write")
	}
	if _, has := got["SKIP"]; has {
		t.Error("nil-valued parameter SKIP must not be persisted")
	}
	if v, has := got["KEEP"]; !has || v != "yes" {
		t.Errorf("KEEP = %v (present=%v), want \"yes\"", v, has)
	}
}

// TestMasterValues_SaveChannel_EmptyMap_NoOp verifies that SaveChannel with
// an empty map is a no-op: no error and no rows written.
func TestMasterValues_SaveChannel_EmptyMap_NoOp(t *testing.T) {
	t.Parallel()
	s := freshMasterValuesStore(t)
	ctx := context.Background()

	if err := s.SaveChannel(ctx, "ccu1", "HmIP-RF", "DEV:3", map[string]any{}); err != nil {
		t.Fatalf("SaveChannel empty map: %v", err)
	}

	_, ok, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEV:3")
	if err != nil {
		t.Fatalf("LoadChannel after empty save: %v", err)
	}
	if ok {
		t.Error("empty-map SaveChannel must not produce a cache hit")
	}
}

// TestMasterValues_SaveParameter_UpsertOverwrites verifies that writing the
// same parameter twice keeps only the most recent value.
func TestMasterValues_SaveParameter_UpsertOverwrites(t *testing.T) {
	t.Parallel()
	s := freshMasterValuesStore(t)
	ctx := context.Background()

	if err := s.SaveParameter(ctx, "ccu1", "HmIP-RF", "DEV:4", "MODE", "first"); err != nil {
		t.Fatalf("SaveParameter first: %v", err)
	}
	if err := s.SaveParameter(ctx, "ccu1", "HmIP-RF", "DEV:4", "MODE", "second"); err != nil {
		t.Fatalf("SaveParameter second: %v", err)
	}

	got, ok, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEV:4")
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if !ok {
		t.Fatal("LoadChannel: ok = false after SaveParameter")
	}
	if got["MODE"] != "second" {
		t.Errorf("MODE = %v, want \"second\"", got["MODE"])
	}
}

// TestMasterValues_MultiCentral_Isolated verifies that two centrals writing
// to the same (interface, channel, parameter) key do not share state.
func TestMasterValues_MultiCentral_Isolated(t *testing.T) {
	t.Parallel()
	s := freshMasterValuesStore(t)
	ctx := context.Background()

	if err := s.SaveChannel(ctx, "ccu1", "HmIP-RF", "SHARED:1", map[string]any{"P": "for-ccu1"}); err != nil {
		t.Fatalf("SaveChannel ccu1: %v", err)
	}
	if err := s.SaveChannel(ctx, "ccu2", "HmIP-RF", "SHARED:1", map[string]any{"P": "for-ccu2"}); err != nil {
		t.Fatalf("SaveChannel ccu2: %v", err)
	}

	got1, ok1, err1 := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "SHARED:1")
	if err1 != nil {
		t.Fatalf("LoadChannel ccu1: %v", err1)
	}
	got2, ok2, err2 := s.LoadChannel(ctx, "ccu2", "HmIP-RF", "SHARED:1")
	if err2 != nil {
		t.Fatalf("LoadChannel ccu2: %v", err2)
	}

	if !ok1 || !ok2 {
		t.Fatalf("ok1=%v ok2=%v, both want true", ok1, ok2)
	}
	if got1["P"] != "for-ccu1" {
		t.Errorf("ccu1 P = %v, want \"for-ccu1\"", got1["P"])
	}
	if got2["P"] != "for-ccu2" {
		t.Errorf("ccu2 P = %v, want \"for-ccu2\"", got2["P"])
	}
}

// TestMasterValues_DeleteChannel verifies that DeleteChannel removes the
// cached values for a channel so that a subsequent LoadChannel returns ok=false.
func TestMasterValues_DeleteChannel(t *testing.T) {
	t.Parallel()
	s := freshMasterValuesStore(t)
	ctx := context.Background()

	if err := s.SaveChannel(ctx, "ccu1", "HmIP-RF", "DEL:0", map[string]any{"X": 1}); err != nil {
		t.Fatalf("SaveChannel: %v", err)
	}
	if err := s.DeleteChannel(ctx, "ccu1", "HmIP-RF", "DEL:0"); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}

	_, ok, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEL:0")
	if err != nil {
		t.Fatalf("LoadChannel after delete: %v", err)
	}
	if ok {
		t.Error("LoadChannel after DeleteChannel: ok = true, want false")
	}
}

// TestMasterValues_DeleteDevice_RemovesAllChannels verifies that DeleteDevice
// removes all channels for the given device while leaving other devices intact.
func TestMasterValues_DeleteDevice_RemovesAllChannels(t *testing.T) {
	t.Parallel()
	s := freshMasterValuesStore(t)
	ctx := context.Background()

	for _, ch := range []string{"dev:0", "dev:1", "dev:2"} {
		if err := s.SaveChannel(ctx, "ccu1", "HmIP-RF", ch, map[string]any{"V": ch}); err != nil {
			t.Fatalf("SaveChannel %s: %v", ch, err)
		}
	}
	if err := s.SaveChannel(ctx, "ccu1", "HmIP-RF", "other_dev:0", map[string]any{"V": "other"}); err != nil {
		t.Fatalf("SaveChannel other_dev:0: %v", err)
	}

	if err := s.DeleteDevice(ctx, "ccu1", "HmIP-RF", "dev"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}

	for _, ch := range []string{"dev:0", "dev:1", "dev:2"} {
		_, ok, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", ch)
		if err != nil {
			t.Fatalf("LoadChannel %s: %v", ch, err)
		}
		if ok {
			t.Errorf("channel %s still present after DeleteDevice", ch)
		}
	}

	_, ok, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "other_dev:0")
	if err != nil {
		t.Fatalf("LoadChannel other_dev:0: %v", err)
	}
	if !ok {
		t.Error("other_dev:0 was incorrectly removed by DeleteDevice(\"dev\")")
	}
}

// TestMasterValues_DeleteDevice_PrefixSafety verifies that DeleteDevice("DEVICE")
// does not touch a device whose address starts with "DEVICE2". The implementation
// appends a ":" suffix to the prefix, preventing accidental prefix collisions.
func TestMasterValues_DeleteDevice_PrefixSafety(t *testing.T) {
	t.Parallel()
	s := freshMasterValuesStore(t)
	ctx := context.Background()

	if err := s.SaveChannel(ctx, "ccu1", "HmIP-RF", "DEVICE:0", map[string]any{"A": 1}); err != nil {
		t.Fatalf("SaveChannel DEVICE:0: %v", err)
	}
	if err := s.SaveChannel(ctx, "ccu1", "HmIP-RF", "DEVICE2:0", map[string]any{"A": 2}); err != nil {
		t.Fatalf("SaveChannel DEVICE2:0: %v", err)
	}

	if err := s.DeleteDevice(ctx, "ccu1", "HmIP-RF", "DEVICE"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}

	// DEVICE:0 must be gone.
	_, ok, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEVICE:0")
	if err != nil {
		t.Fatalf("LoadChannel DEVICE:0: %v", err)
	}
	if ok {
		t.Error("DEVICE:0 should have been deleted")
	}

	// DEVICE2:0 must be unaffected.
	_, ok, err = s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEVICE2:0")
	if err != nil {
		t.Fatalf("LoadChannel DEVICE2:0: %v", err)
	}
	if !ok {
		t.Error("DEVICE2:0 was incorrectly removed by DeleteDevice(\"DEVICE\")")
	}
}

// TestMasterValues_NilStore_NoOps verifies that all mutating and reading
// methods on a nil *MasterValuesStore are safe no-ops (no panic, no error).
func TestMasterValues_NilStore_NoOps(t *testing.T) {
	t.Parallel()
	var s *MasterValuesStore
	ctx := context.Background()

	if err := s.SaveChannel(ctx, "c", "i", "ch:0", map[string]any{"K": 1}); err != nil {
		t.Errorf("nil SaveChannel: %v", err)
	}
	if err := s.SaveParameter(ctx, "c", "i", "ch:0", "K", 1); err != nil {
		t.Errorf("nil SaveParameter: %v", err)
	}
	if err := s.DeleteChannel(ctx, "c", "i", "ch:0"); err != nil {
		t.Errorf("nil DeleteChannel: %v", err)
	}
	if err := s.DeleteDevice(ctx, "c", "i", "dev"); err != nil {
		t.Errorf("nil DeleteDevice: %v", err)
	}
	_, ok, err := s.LoadChannel(ctx, "c", "i", "ch:0")
	if err != nil {
		t.Errorf("nil LoadChannel: %v", err)
	}
	if ok {
		t.Error("nil LoadChannel: ok = true, want false")
	}
}

// jsonRoundtrip serialises v to JSON and back into a map[string]any so
// that numeric values are normalised to float64, matching what
// json.Unmarshal produces when reading stored rows.
func jsonRoundtrip(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("jsonRoundtrip marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("jsonRoundtrip unmarshal: %v", err)
	}
	return out
}
