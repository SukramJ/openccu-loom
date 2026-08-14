// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Parity tests for bridged-endpoint-number allocation against matter.js HEAD.
//
// matter.js reference:
//   packages/node/src/storage/server/ServerEndpointStores.ts
//
// openccu-loom mapping:
//   ServerEndpointStores#nextNumber (persisted under NEXT_NUMBER_KEY) maps to
//   the matter_metadata row next_endpoint_id; assignNumber maps to
//   [Store.AssignEndpointID] / [Store.UpsertEndpointAssigning]; the
//   #allocatedNumbers / #preAllocatedNumbers skip sets map to the occupancy
//   scan over matter_endpoints.

package store_test

import (
	"context"
	"testing"

	store "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// TestParityMatterJS_EndpointNumberNotReissuedAfterRemoval verifies that the
// number freed by RemoveEndpoint is not handed to a different source key.
//
// Mirrors matter.js ServerEndpointStores.ts assignNumber: numbers are taken
// from the monotonically advancing #nextNumber, and eraseStoreForEndpoint
// only drops the number from the allocated sets — it never rewinds the
// counter, so a released number is not preferentially reissued.
//
// Reissuing it is invisible to a controller: the Aggregator's
// Descriptor.PartsList is unchanged (same set of numbers before and after),
// so Apple Home / Google Home keep the cached DeviceTypeList and cluster set
// for that number and render the new device under the removed device's
// identity.
func TestParityMatterJS_EndpointNumberNotReissuedAfterRemoval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	keyA := testKey("ccu1", "AAA0001", 1, store.DPKindCustom, "SWITCH")
	keyB := testKey("ccu1", "BBB0002", 1, store.DPKindCustom, "SWITCH")
	keyC := testKey("ccu1", "CCC0003", 1, store.DPKindCustom, "SWITCH")

	idA, err := s.UpsertEndpointAssigning(ctx, store.EndpointRecord{Key: keyA})
	if err != nil {
		t.Fatalf("assign A: %v", err)
	}
	idB, err := s.UpsertEndpointAssigning(ctx, store.EndpointRecord{Key: keyB})
	if err != nil {
		t.Fatalf("assign B: %v", err)
	}

	// The device behind A is unpaired: the assembler's vanished-source GC
	// removes its row.
	if err := s.RemoveEndpoint(ctx, keyA); err != nil {
		t.Fatalf("RemoveEndpoint(A): %v", err)
	}

	idC, err := s.UpsertEndpointAssigning(ctx, store.EndpointRecord{Key: keyC})
	if err != nil {
		t.Fatalf("assign C: %v", err)
	}
	if idC == idA {
		t.Errorf("endpoint number %d released by A was reissued to C — controllers keep A's cached accessory for it", idC)
	}
	if idC <= idB {
		t.Errorf("assigned number %d for C is not past B's %d — allocation is not monotonic", idC, idB)
	}
}

// TestParityMatterJS_EndpointNumberFreshAfterReAdd pins the honest consequence
// of monotonic allocation: a source key that is removed and later re-added
// gets a fresh number rather than its previous one. Its old number stays
// retired, which is what keeps it from landing on an unrelated device.
//
// Mirrors matter.js ServerEndpointStores.ts assignNumber: the stored number is
// only reused while the endpoint's store entry survives
// (eraseStoreForEndpoint drops it); afterwards the endpoint allocates from
// #nextNumber like any newcomer.
func TestParityMatterJS_EndpointNumberFreshAfterReAdd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	key := testKey("ccu1", "DDD0004", 1, store.DPKindCustom, "SWITCH")

	first, err := s.UpsertEndpointAssigning(ctx, store.EndpointRecord{Key: key})
	if err != nil {
		t.Fatalf("first assign: %v", err)
	}
	if err := s.RemoveEndpoint(ctx, key); err != nil {
		t.Fatalf("RemoveEndpoint: %v", err)
	}
	second, err := s.UpsertEndpointAssigning(ctx, store.EndpointRecord{Key: key})
	if err != nil {
		t.Fatalf("second assign: %v", err)
	}
	if second == first {
		t.Errorf("re-added key reused retired number %d — the counter was rewound by the removal", second)
	}
}

// TestParityMatterJS_EndpointNumberSeedsAboveExistingRows verifies the upgrade
// path: a database written by the previous hole-filling allocator carries
// endpoint rows but no next_endpoint_id counter. The allocator must seed the
// counter above every number already stored instead of filling the holes those
// rows leave.
//
// Mirrors matter.js ServerEndpointStores.ts load(): every persisted number is
// added to #preAllocatedNumbers so it is skipped even when the counter is
// behind ("Ensure all known numbers are allocated").
func TestParityMatterJS_EndpointNumberSeedsAboveExistingRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	s := store.New(db)

	for i, id := range []uint16{2, 3, 5} {
		key := testKey("ccu1", "EEE0005", i+1, store.DPKindCustom, "SWITCH")
		if err := s.UpsertEndpoint(ctx, store.EndpointRecord{Key: key, EndpointID: id}); err != nil {
			t.Fatalf("seed row %d: %v", id, err)
		}
	}
	// Drop the counter row to reproduce a database migrated from the
	// hole-filling allocator.
	if _, err := db.ExecContext(ctx, `DELETE FROM matter_metadata WHERE key = 'next_endpoint_id'`); err != nil {
		t.Fatalf("clear counter: %v", err)
	}

	id, err := s.AssignEndpointID(ctx)
	if err != nil {
		t.Fatalf("AssignEndpointID: %v", err)
	}
	if id != 6 {
		t.Errorf("assigned %d, want 6 — the counter must seed above the highest stored number, not fill the hole at 4", id)
	}
}
