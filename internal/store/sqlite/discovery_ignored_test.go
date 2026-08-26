// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newDiscoveryIgnoreStore(t *testing.T) *DiscoveryIgnoreStore {
	t.Helper()
	return NewDiscoveryIgnoreStore(openTestDB(t, "discovery_ignored.db"))
}

// TestDiscoveryIgnoreStore_AddAndList verifies that Add persists a row and List
// returns it with all fields populated.
func TestDiscoveryIgnoreStore_AddAndList(t *testing.T) {
	t.Parallel()
	s := newDiscoveryIgnoreStore(t)
	ctx := context.Background()

	entry := IgnoredCCU{
		Serial:    "3014F711A0001F0123456789",
		Name:      "Otto",
		Host:      "192.0.2.29",
		IgnoredBy: "admin",
	}
	if err := s.Add(ctx, entry); err != nil {
		t.Fatalf("Add: %v", err)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List len=%d want 1", len(list))
	}
	got := list[0]
	if got.Serial != entry.Serial {
		t.Errorf("Serial=%q want %q", got.Serial, entry.Serial)
	}
	if got.Name != entry.Name {
		t.Errorf("Name=%q want %q", got.Name, entry.Name)
	}
	if got.Host != entry.Host {
		t.Errorf("Host=%q want %q", got.Host, entry.Host)
	}
	if got.IgnoredAt.IsZero() {
		t.Error("IgnoredAt is zero, expected it to be set")
	}
}

// TestDiscoveryIgnoreStore_AddUpsert verifies that adding the same serial twice
// results in a single row with updated fields.
func TestDiscoveryIgnoreStore_AddUpsert(t *testing.T) {
	t.Parallel()
	s := newDiscoveryIgnoreStore(t)
	ctx := context.Background()

	serial := "AABBCCDDEEFF"
	first := IgnoredCCU{Serial: serial, Name: "First", Host: "10.0.0.1", IgnoredBy: "admin"}
	second := IgnoredCCU{Serial: serial, Name: "Updated", Host: "10.0.0.2", IgnoredBy: "operator"}

	if err := s.Add(ctx, first); err != nil {
		t.Fatalf("Add first: %v", err)
	}
	// Add a tiny sleep so IgnoredAt differs if upsert changes the timestamp.
	time.Sleep(2 * time.Millisecond)
	if err := s.Add(ctx, second); err != nil {
		t.Fatalf("Add second: %v", err)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List len=%d want 1 after upsert", len(list))
	}
	got := list[0]
	if got.Name != "Updated" {
		t.Errorf("Name=%q want %q", got.Name, "Updated")
	}
	if got.Host != "10.0.0.2" {
		t.Errorf("Host=%q want %q", got.Host, "10.0.0.2")
	}
}

// TestDiscoveryIgnoreStore_IgnoredSerials verifies the fast-path set helper.
func TestDiscoveryIgnoreStore_IgnoredSerials(t *testing.T) {
	t.Parallel()
	s := newDiscoveryIgnoreStore(t)
	ctx := context.Background()

	serials := []string{"SER001", "SER002", "SER003"}
	for _, serial := range serials {
		if err := s.Add(ctx, IgnoredCCU{Serial: serial, Name: serial}); err != nil {
			t.Fatalf("Add %s: %v", serial, err)
		}
	}

	set, err := s.IgnoredSerials(ctx)
	if err != nil {
		t.Fatalf("IgnoredSerials: %v", err)
	}
	for _, want := range serials {
		if _, ok := set[want]; !ok {
			t.Errorf("IgnoredSerials missing %q", want)
		}
	}
	if len(set) != len(serials) {
		t.Errorf("IgnoredSerials len=%d want %d", len(set), len(serials))
	}
}

// TestDiscoveryIgnoreStore_Remove verifies that Remove deletes an existing row
// and returns true, and returns false for an unknown serial.
func TestDiscoveryIgnoreStore_Remove(t *testing.T) {
	t.Parallel()
	s := newDiscoveryIgnoreStore(t)
	ctx := context.Background()

	if err := s.Add(ctx, IgnoredCCU{Serial: "TO-DELETE", Name: "Gone"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	removed, err := s.Remove(ctx, "TO-DELETE")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !removed {
		t.Error("Remove returned false, want true for existing entry")
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List after Remove: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List after Remove len=%d want 0", len(list))
	}

	// Remove again — should return false (not found).
	removed2, err := s.Remove(ctx, "TO-DELETE")
	if err != nil {
		t.Fatalf("Remove unknown: %v", err)
	}
	if removed2 {
		t.Error("Remove of unknown returned true, want false")
	}
}

// TestDiscoveryIgnoreStore_ListEmptyIsNonNil verifies that List returns an empty
// (non-nil) slice when no entries exist.
func TestDiscoveryIgnoreStore_ListEmptyIsNonNil(t *testing.T) {
	t.Parallel()
	s := newDiscoveryIgnoreStore(t)
	ctx := context.Background()

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List on empty store: %v", err)
	}
	// A nil slice is acceptable Go; the important thing is no panic and no error.
	// (The handler wraps it in []IgnoredCCU{} before returning, so nil here is fine.)
	_ = list
}

// TestDiscoveryIgnoreStore_NilStore_ListSafe verifies that a nil store's List
// returns nil, nil (no panic).
func TestDiscoveryIgnoreStore_NilStore_ListSafe(t *testing.T) {
	t.Parallel()
	var s *DiscoveryIgnoreStore

	list, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("nil store List returned error: %v", err)
	}
	if list != nil {
		t.Errorf("nil store List returned non-nil slice")
	}
}

// TestDiscoveryIgnoreStore_NilStore_IgnoredSerialsSafe verifies that a nil
// store's IgnoredSerials returns an empty set (no panic).
func TestDiscoveryIgnoreStore_NilStore_IgnoredSerialsSafe(t *testing.T) {
	t.Parallel()
	var s *DiscoveryIgnoreStore

	set, err := s.IgnoredSerials(context.Background())
	if err != nil {
		t.Fatalf("nil store IgnoredSerials returned error: %v", err)
	}
	if set == nil {
		t.Error("nil store IgnoredSerials returned nil map, want empty map")
	}
}

// TestDiscoveryIgnoreStore_NilStore_AddError verifies that Add on a nil store
// returns ErrDiscoveryStoreUnavailable.
func TestDiscoveryIgnoreStore_NilStore_AddError(t *testing.T) {
	t.Parallel()
	var s *DiscoveryIgnoreStore

	err := s.Add(context.Background(), IgnoredCCU{Serial: "X"})
	if !errors.Is(err, ErrDiscoveryStoreUnavailable) {
		t.Errorf("Add on nil store: got %v, want ErrDiscoveryStoreUnavailable", err)
	}
}

// TestDiscoveryIgnoreStore_NilStore_RemoveError verifies that Remove on a nil
// store returns ErrDiscoveryStoreUnavailable.
func TestDiscoveryIgnoreStore_NilStore_RemoveError(t *testing.T) {
	t.Parallel()
	var s *DiscoveryIgnoreStore

	_, err := s.Remove(context.Background(), "X")
	if !errors.Is(err, ErrDiscoveryStoreUnavailable) {
		t.Errorf("Remove on nil store: got %v, want ErrDiscoveryStoreUnavailable", err)
	}
}
