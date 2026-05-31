// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	store "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// addTestFabric is a convenience wrapper that inserts a minimal fabric and
// fails the test on error.
func addTestFabric(t *testing.T, s *store.Store, fabricIndex uint8, seed byte) {
	t.Helper()
	ctx := context.Background()
	rec := newFabricRecord(fabricIndex, seed)
	if _, err := s.AddFabric(ctx, rec); err != nil {
		t.Fatalf("addTestFabric index=%d seed=%d: %v", fabricIndex, seed, err)
	}
}

// TestIdentity_UpsertInsert verifies a basic insert + round-trip.
func TestIdentity_UpsertInsert(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	addTestFabric(t, s, 1, 1)

	id := store.IdentityRecord{
		FabricIndex: 1,
		NOC:         []byte("noc-bytes"),
		ICAC:        []byte("icac-bytes"),
		PrivateKey:  []byte("private-key-32-bytes-exactly!!!!"),
		IPK:         []byte("ipk-16-bytes!!!!"),
	}
	if err := s.UpsertIdentity(ctx, id); err != nil {
		t.Fatalf("UpsertIdentity: %v", err)
	}

	got, err := s.GetIdentity(ctx, 1)
	if err != nil {
		t.Fatalf("GetIdentity: %v", err)
	}
	if got.FabricIndex != 1 {
		t.Errorf("FabricIndex=%d want 1", got.FabricIndex)
	}
	if !bytes.Equal(got.NOC, id.NOC) {
		t.Errorf("NOC mismatch: got %q want %q", got.NOC, id.NOC)
	}
	if !bytes.Equal(got.ICAC, id.ICAC) {
		t.Errorf("ICAC mismatch: got %q want %q", got.ICAC, id.ICAC)
	}
	if !bytes.Equal(got.PrivateKey, id.PrivateKey) {
		t.Errorf("PrivateKey mismatch")
	}
	if !bytes.Equal(got.IPK, id.IPK) {
		t.Errorf("IPK mismatch")
	}
}

// TestIdentity_UpsertUpdate verifies that a second Upsert overwrites all
// fields.
func TestIdentity_UpsertUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	addTestFabric(t, s, 1, 2)

	first := store.IdentityRecord{
		FabricIndex: 1,
		NOC:         []byte("noc-v1"),
		ICAC:        []byte("icac-v1"),
		PrivateKey:  []byte("privkey-v1-padded-to-32bytes!!!!"),
		IPK:         []byte("ipk-v1-16bytes!!"),
	}
	if err := s.UpsertIdentity(ctx, first); err != nil {
		t.Fatalf("first UpsertIdentity: %v", err)
	}

	second := store.IdentityRecord{
		FabricIndex: 1,
		NOC:         []byte("noc-v2"),
		ICAC:        []byte("icac-v2"),
		PrivateKey:  []byte("privkey-v2-padded-to-32bytes!!!!"),
		IPK:         []byte("ipk-v2-16bytes!!"),
	}
	if err := s.UpsertIdentity(ctx, second); err != nil {
		t.Fatalf("second UpsertIdentity: %v", err)
	}

	got, err := s.GetIdentity(ctx, 1)
	if err != nil {
		t.Fatalf("GetIdentity: %v", err)
	}
	if !bytes.Equal(got.NOC, second.NOC) {
		t.Errorf("NOC not updated: got %q want %q", got.NOC, second.NOC)
	}
	if !bytes.Equal(got.ICAC, second.ICAC) {
		t.Errorf("ICAC not updated: got %q want %q", got.ICAC, second.ICAC)
	}
	if !bytes.Equal(got.PrivateKey, second.PrivateKey) {
		t.Error("PrivateKey not updated")
	}
	if !bytes.Equal(got.IPK, second.IPK) {
		t.Error("IPK not updated")
	}
}

// TestIdentity_NilICAC verifies that a nil ICAC round-trips back as nil
// (stored as SQL NULL, not empty bytes).
func TestIdentity_NilICAC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	addTestFabric(t, s, 1, 3)

	id := store.IdentityRecord{
		FabricIndex: 1,
		NOC:         []byte("noc"),
		ICAC:        nil,
		PrivateKey:  []byte("privkey-padded-to-32bytes!!!!!!!"),
		IPK:         []byte("ipk-padded-16byt"),
	}
	if err := s.UpsertIdentity(ctx, id); err != nil {
		t.Fatalf("UpsertIdentity: %v", err)
	}

	got, err := s.GetIdentity(ctx, 1)
	if err != nil {
		t.Fatalf("GetIdentity: %v", err)
	}
	if got.ICAC != nil {
		t.Errorf("ICAC=%q want nil", got.ICAC)
	}
}

// TestIdentity_NonNilICAC verifies that a non-nil ICAC round-trips back as
// []byte (not nil).
func TestIdentity_NonNilICAC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	addTestFabric(t, s, 1, 4)

	id := store.IdentityRecord{
		FabricIndex: 1,
		NOC:         []byte("noc"),
		ICAC:        []byte("icac-present"),
		PrivateKey:  []byte("privkey-padded-to-32bytes!!!!!!!"),
		IPK:         []byte("ipk-padded-16byt"),
	}
	if err := s.UpsertIdentity(ctx, id); err != nil {
		t.Fatalf("UpsertIdentity: %v", err)
	}

	got, err := s.GetIdentity(ctx, 1)
	if err != nil {
		t.Fatalf("GetIdentity: %v", err)
	}
	if got.ICAC == nil {
		t.Fatal("ICAC is nil, want non-nil []byte")
	}
	if !bytes.Equal(got.ICAC, id.ICAC) {
		t.Errorf("ICAC=%q want %q", got.ICAC, id.ICAC)
	}
}

// TestIdentity_GetMiss verifies ErrIdentityNotFound for an absent fabric.
func TestIdentity_GetMiss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	_, err := s.GetIdentity(ctx, 77)
	if !errors.Is(err, store.ErrIdentityNotFound) {
		t.Errorf("got %v, want ErrIdentityNotFound", err)
	}
}

// TestIdentity_FKConstraint verifies that UpsertIdentity fails when the
// referenced fabric does not exist.
func TestIdentity_FKConstraint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))

	// No fabric inserted — FK must fire.
	id := store.IdentityRecord{
		FabricIndex: 200,
		NOC:         []byte("noc"),
		PrivateKey:  []byte("privkey"),
		IPK:         []byte("ipk"),
	}
	if err := s.UpsertIdentity(ctx, id); err == nil {
		t.Fatal("expected FK error for non-existent fabric, got nil")
	}
}
