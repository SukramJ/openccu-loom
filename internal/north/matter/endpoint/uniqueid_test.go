// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package endpoint

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// TestUniqueIDFor_DistinctAcrossEndpointKeys is the regression guard
// for the Apple Home pair-abort root cause — `uniqueIDFor`
// previously fell back to a literal "openccu-loom-bridged" string for
// every [store.EndpointKey] (the type switch only matched
// `Stringer` / `Central()/Address()` interfaces, neither of which
// EndpointKey satisfies). The result was every bridged endpoint sharing
// `BridgedDeviceBasicInformation.UniqueID` and Apple's HAP service
// mapper collapsing all five into a single HMAccessory. We assert here
// that five concrete EndpointKey instances each hash to a distinct
// 32-character hex value.
func TestUniqueIDFor_DistinctAcrossEndpointKeys(t *testing.T) {
	t.Parallel()
	keys := []store.EndpointKey{
		{CentralName: "GoOtto", DeviceAddress: "000C9709AC8CAF", ChannelNo: 1, DPKind: store.DPKindGeneric, DPKey: ""},
		{CentralName: "GoOtto", DeviceAddress: "000C9709AC8CB1", ChannelNo: 1, DPKind: store.DPKindGeneric, DPKey: ""},
		{CentralName: "GoOtto", DeviceAddress: "000C9709AC8CD4", ChannelNo: 2, DPKind: store.DPKindGeneric, DPKey: ""},
		{CentralName: "GoOtto", DeviceAddress: "00091569A38F49", ChannelNo: 1, DPKind: store.DPKindMeasurement, DPKey: "ILLUMINATION"},
		{CentralName: "GoOtto", DeviceAddress: "000A1709AF4FC9", ChannelNo: 1, DPKind: store.DPKindGeneric, DPKey: ""},
	}
	seen := make(map[string]store.EndpointKey, len(keys))
	for _, k := range keys {
		uid := uniqueIDFor(k)
		if len(uid) != 32 {
			t.Errorf("uniqueIDFor(%+v) returned %d hex chars, want 32: %q", k, len(uid), uid)
		}
		if prev, dup := seen[uid]; dup {
			t.Errorf("collision: uniqueIDFor(%+v) and uniqueIDFor(%+v) both = %q", k, prev, uid)
		}
		seen[uid] = k
	}
	if len(seen) != len(keys) {
		t.Fatalf("only %d unique IDs from %d distinct EndpointKeys", len(seen), len(keys))
	}
}

// TestUniqueIDFor_StableAcrossInvocations asserts the hash is purely
// a function of the key — Matter §9.13.5.20 mandates a persistent
// per-device identifier, and Apple Home pins HMAccessory state by it.
func TestUniqueIDFor_StableAcrossInvocations(t *testing.T) {
	t.Parallel()
	key := store.EndpointKey{
		CentralName:   "GoOtto",
		DeviceAddress: "000C9709AC8CAF",
		ChannelNo:     7,
		DPKind:        store.DPKindGeneric,
		DPKey:         "",
	}
	first := uniqueIDFor(key)
	for i := range 16 {
		got := uniqueIDFor(key)
		if got != first {
			t.Fatalf("uniqueIDFor returned different values for the same key: first=%q got=%q (iteration %d)", first, got, i)
		}
	}
}

// TestUniqueIDFor_PointerAndValueAgree guards the *EndpointKey vs
// EndpointKey path through renderSourceKey — both shapes must produce
// the same fingerprint or the bridge would expose two different
// UniqueIDs depending on whether the assembler hands over a value or
// a pointer.
func TestUniqueIDFor_PointerAndValueAgree(t *testing.T) {
	t.Parallel()
	k := store.EndpointKey{
		CentralName:   "GoOtto",
		DeviceAddress: "000C9709AC8CAF",
		ChannelNo:     1,
		DPKind:        store.DPKindGeneric,
		DPKey:         "",
	}
	if uniqueIDFor(k) != uniqueIDFor(&k) {
		t.Fatalf("uniqueIDFor(value) != uniqueIDFor(pointer): %q vs %q", uniqueIDFor(k), uniqueIDFor(&k))
	}
}

// TestUniqueIDFor_FieldsActuallyMatter ensures every component of the
// EndpointKey 5-tuple feeds into the hash — flipping any one field has
// to produce a different fingerprint. Without this guarantee a key
// renderer that only used a subset of the fields could regress us back
// to the duplicate-fingerprint state we just fixed.
func TestUniqueIDFor_FieldsActuallyMatter(t *testing.T) {
	t.Parallel()
	base := store.EndpointKey{
		CentralName:   "GoOtto",
		DeviceAddress: "000C9709AC8CAF",
		ChannelNo:     1,
		DPKind:        store.DPKindGeneric,
		DPKey:         "STATE",
	}
	baseHash := uniqueIDFor(base)
	mutators := []struct {
		label string
		mod   func(*store.EndpointKey)
	}{
		{"CentralName", func(k *store.EndpointKey) { k.CentralName = "OtherCcu" }},
		{"DeviceAddress", func(k *store.EndpointKey) { k.DeviceAddress = "000C9709AC8CB1" }},
		{"ChannelNo", func(k *store.EndpointKey) { k.ChannelNo = 2 }},
		{"DPKind", func(k *store.EndpointKey) { k.DPKind = store.DPKindMeasurement }},
		{"DPKey", func(k *store.EndpointKey) { k.DPKey = "DIFFERENT" }},
	}
	for _, m := range mutators {
		mutated := base
		m.mod(&mutated)
		if uniqueIDFor(mutated) == baseHash {
			t.Errorf("flipping %s did not change the UniqueID — that field is not feeding the hash", m.label)
		}
	}
}

// TestUniqueIDFor_NilKey returns a deterministic non-empty string so
// the BridgedDeviceBasicInformation cluster can still publish UniqueID;
// the value is not required to be unique across nil-key endpoints
// (those should not exist in production), only to be a valid 32-char
// hex string.
func TestUniqueIDFor_NilKey(t *testing.T) {
	t.Parallel()
	if got := uniqueIDFor(nil); len(got) != 32 {
		t.Fatalf("uniqueIDFor(nil) = %q; want 32-char hex string", got)
	}
}
