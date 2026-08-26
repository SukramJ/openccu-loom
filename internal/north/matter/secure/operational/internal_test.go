// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package operational

// Whitebox tests for internal manager state that cannot be reached from the
// _test package without invasive production code changes.  The tests here
// exercise edge cases that require direct manipulation of unexported fields.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/sigma"
	mstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// minimalFakeStore implements ResumptionStore without any data; the whitebox
// tests below do not exercise the store — we just need a non-nil value to
// satisfy NewManager.
type minimalFakeStore struct {
	mu      sync.RWMutex
	records map[string]mstore.ResumptionRecord
}

func newMinimalFakeStore() *minimalFakeStore {
	return &minimalFakeStore{records: make(map[string]mstore.ResumptionRecord)}
}

func (f *minimalFakeStore) UpsertResumption(_ context.Context, rec mstore.ResumptionRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[string(rec.ResumptionID)] = rec
	return nil
}

func (f *minimalFakeStore) GetResumptionByID(_ context.Context, id []byte) (mstore.ResumptionRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	r, ok := f.records[string(id)]
	if !ok {
		return mstore.ResumptionRecord{}, mstore.ErrResumptionNotFound
	}
	return r, nil
}

func (f *minimalFakeStore) GetResumptionByPeer(_ context.Context, _ uint8, _ uint64) (mstore.ResumptionRecord, error) {
	return mstore.ResumptionRecord{}, mstore.ErrResumptionNotFound
}

func (f *minimalFakeStore) RemoveResumption(_ context.Context, _ uint8, _ uint64) error {
	return nil
}

// TestAllocateIDLocked_WrapAround verifies that allocateIDLocked wraps
// nextID from 0xFFFF back to 1 (i.e. the `m.nextID == 0 || m.nextID > maxID`
// branch) by setting nextID to 0xFFFE just before the allocation call.
func TestAllocateIDLocked_WrapAround(t *testing.T) {
	t.Parallel()

	m := &Manager{
		store:    newMinimalFakeStore(),
		sessions: make(map[uint16]*Entry),
		nextID:   0xFFFE, // next allocation will use 0xFFFE, then increment to 0xFFFF → wrap to 1
	}

	// First allocation: nextID = 0xFFFE → returned, then incremented to
	// 0xFFFF which is > maxID (0xFFFE), so nextID wraps to 1.
	m.mu.Lock()
	id1, err := m.allocateIDLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatalf("first allocateIDLocked: %v", err)
	}
	if id1 != 0xFFFE {
		t.Fatalf("id1 = %d, want 0xFFFE", id1)
	}
	// nextID must have wrapped to 1.
	if m.nextID != 1 {
		t.Fatalf("nextID = %d after wrap, want 1", m.nextID)
	}

	// Second allocation (after wrap): nextID = 1, slot 0xFFFE is now in
	// sessions as placeholder, so slot 1 is free and returned.
	m.sessions[id1] = &Entry{SessionID: id1} // stake a placeholder so the slot is occupied
	m.mu.Lock()
	id2, err := m.allocateIDLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatalf("second allocateIDLocked: %v", err)
	}
	if id2 == 0 || id2 == id1 {
		t.Fatalf("id2 = %d, must be non-zero and != id1 (%d)", id2, id1)
	}
}

// TestAllocateIDLocked_Exhausted verifies that allocateIDLocked returns
// ErrSessionExhausted when every slot in [1, 0xFFFE] is occupied.
func TestAllocateIDLocked_Exhausted(t *testing.T) {
	t.Parallel()

	m := &Manager{
		store:    newMinimalFakeStore(),
		sessions: make(map[uint16]*Entry),
		nextID:   1,
	}

	// Fill every slot.
	const maxID = uint16(0xFFFE)
	for id := uint16(1); id <= maxID; id++ {
		m.sessions[id] = &Entry{SessionID: id}
	}

	m.mu.Lock()
	_, err := m.allocateIDLocked()
	m.mu.Unlock()

	if !errors.Is(err, ErrSessionExhausted) {
		t.Fatalf("err = %v, want ErrSessionExhausted", err)
	}
}

// TestAllocateID_Exhausted verifies that the public AllocateID method
// surfaces ErrSessionExhausted through the mu/Lock path when the id
// space is full.
func TestAllocateID_Exhausted(t *testing.T) {
	t.Parallel()

	m := &Manager{
		store:    newMinimalFakeStore(),
		sessions: make(map[uint16]*Entry),
		nextID:   1,
	}

	// Fill every slot.
	const maxID = uint16(0xFFFE)
	for id := uint16(1); id <= maxID; id++ {
		m.sessions[id] = &Entry{SessionID: id}
	}

	_, err := m.AllocateID()
	if !errors.Is(err, ErrSessionExhausted) {
		t.Fatalf("AllocateID: err = %v, want ErrSessionExhausted", err)
	}
}

// TestOpenFromSigma_ExhaustedIDSpaceReturnsError verifies that
// OpenFromSigma propagates ErrSessionExhausted when the id space is full.
// This exercises the `allocateIDLocked` error branch at line 113-117.
func TestOpenFromSigma_ExhaustedIDSpaceReturnsError(t *testing.T) {
	t.Parallel()

	m := &Manager{
		store:    newMinimalFakeStore(),
		sessions: make(map[uint16]*Entry),
		nextID:   1,
	}

	// Fill every slot.
	const maxID = uint16(0xFFFE)
	for id := uint16(1); id <= maxID; id++ {
		m.sessions[id] = &Entry{SessionID: id}
	}

	var keys sigma.SessionKeys
	for i := range keys.I2RKey {
		keys.I2RKey[i] = byte(i + 1)
	}
	for i := range keys.R2IKey {
		keys.R2IKey[i] = byte(i + 17)
	}

	_, err := m.OpenFromSigma(1, 0x100, 0x200, keys)
	if !errors.Is(err, ErrSessionExhausted) {
		t.Fatalf("OpenFromSigma exhausted: err = %v, want ErrSessionExhausted", err)
	}
}

// TestOpenFromPase_ExhaustedIDSpaceReturnsError verifies that
// OpenFromPase propagates ErrSessionExhausted when the id space is full.
func TestOpenFromPase_ExhaustedIDSpaceReturnsError(t *testing.T) {
	t.Parallel()

	m := &Manager{
		store:    newMinimalFakeStore(),
		sessions: make(map[uint16]*Entry),
		nextID:   1,
	}

	// Fill every slot.
	const maxID = uint16(0xFFFE)
	for id := uint16(1); id <= maxID; id++ {
		m.sessions[id] = &Entry{SessionID: id}
	}

	secret := make([]byte, 16)
	for i := range secret {
		secret[i] = byte(i + 1)
	}

	_, err := m.OpenFromPase(0x100, 0x200, 0x300, secret)
	if !errors.Is(err, ErrSessionExhausted) {
		t.Fatalf("OpenFromPase exhausted: err = %v, want ErrSessionExhausted", err)
	}
}
