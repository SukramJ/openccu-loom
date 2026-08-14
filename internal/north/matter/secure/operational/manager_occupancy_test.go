// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package operational

// Tests for the read-only views an operator surface takes of the
// session table: how much of the 16-bit id space is in use, and which
// sessions a given peer currently holds.

import (
	"testing"
)

// TestOccupancy_SeparatesLiveSessionsFromReservedIDs pins the split the
// occupancy view exists for.
//
// A staked id has no key material behind it — it is a handshake that has
// announced a session id in Sigma2 and not yet completed. Counting it as
// a live session would tell an operator a controller is connected when
// none is; not counting it at all would hide the exact leak the id space
// can die from, because an abandoned handshake holds its slot for
// [PlaceholderIDTTL].
func TestOccupancy_SeparatesLiveSessionsFromReservedIDs(t *testing.T) {
	t.Parallel()
	m := NewManager(newMinimalFakeStore())

	empty := m.Occupancy()
	if empty.Live != 0 || empty.Reserved != 0 {
		t.Fatalf("fresh manager occupancy = %+v, want no live and no reserved ids", empty)
	}
	if empty.Capacity != SessionIDSpace {
		t.Fatalf("Capacity = %d, want %d — the allocator hands out [1, 0xFFFE]", empty.Capacity, SessionIDSpace)
	}
	if empty.Free() != SessionIDSpace {
		t.Fatalf("Free() = %d, want the whole space on a fresh manager", empty.Free())
	}

	staked, err := m.AllocateID()
	if err != nil {
		t.Fatalf("AllocateID: %v", err)
	}
	if occ := m.Occupancy(); occ.Live != 0 || occ.Reserved != 1 {
		t.Fatalf("after AllocateID occupancy = %+v, want live=0 reserved=1 — an id staked for an "+
			"in-flight handshake is not a connected controller", occ)
	}

	if _, err := m.OpenFromSigmaWithID(staked, 1, 0xB0B, 0xA11CE, 0x1111, nil, lifecycleKeys(0x10)); err != nil {
		t.Fatalf("OpenFromSigmaWithID: %v", err)
	}
	if occ := m.Occupancy(); occ.Live != 1 || occ.Reserved != 0 {
		t.Fatalf("after the handshake completed occupancy = %+v, want live=1 reserved=0", occ)
	}

	abandoned, err := m.AllocateID()
	if err != nil {
		t.Fatalf("AllocateID (second): %v", err)
	}
	occ := m.Occupancy()
	if occ.Live != 1 || occ.Reserved != 1 {
		t.Fatalf("occupancy = %+v, want live=1 reserved=1", occ)
	}
	if occ.Free() != SessionIDSpace-2 {
		t.Errorf("Free() = %d, want %d — both a live session and a staked id consume the space",
			occ.Free(), SessionIDSpace-2)
	}

	m.ReleaseID(abandoned)
	if err := m.Close(staked); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if occ := m.Occupancy(); occ.Live != 0 || occ.Reserved != 0 {
		t.Errorf("occupancy after teardown = %+v, want the table empty again", occ)
	}
}

// TestSessionIDsForPeer_ListsOnlyThatPeersLiveSessions pins the query
// the CASE resume log reads.
//
// Opening a session for a peer evicts that peer's earlier sessions, so
// the ids this returns immediately before an open are exactly the ones
// the open displaces. A staked id must not appear: nothing is displaced
// when a handshake claims its own placeholder.
func TestSessionIDsForPeer_ListsOnlyThatPeersLiveSessions(t *testing.T) {
	t.Parallel()
	m := NewManager(newMinimalFakeStore())

	const (
		fabric  = uint8(1)
		local   = uint64(0xB0B)
		peerA   = uint64(0xA11CE)
		peerB   = uint64(0xB0B0B)
		otherFI = uint8(2)
	)

	a, err := m.OpenFromSigma(fabric, local, peerA, lifecycleKeys(0x10))
	if err != nil {
		t.Fatalf("OpenFromSigma peer A: %v", err)
	}
	if _, err := m.OpenFromSigma(fabric, local, peerB, lifecycleKeys(0x30)); err != nil {
		t.Fatalf("OpenFromSigma peer B: %v", err)
	}
	if _, err := m.AllocateID(); err != nil {
		t.Fatalf("AllocateID: %v", err)
	}

	got := m.SessionIDsForPeer(fabric, peerA)
	if len(got) != 1 || got[0] != a.SessionID {
		t.Fatalf("SessionIDsForPeer(%d, %#x) = %v, want [%d]", fabric, peerA, got, a.SessionID)
	}
	if ids := m.SessionIDsForPeer(otherFI, peerA); len(ids) != 0 {
		t.Errorf("SessionIDsForPeer on the wrong fabric returned %v — a session is scoped to its fabric", ids)
	}
	if ids := m.SessionIDsForPeer(fabric, 0xDEAD); len(ids) != 0 {
		t.Errorf("SessionIDsForPeer for an unknown peer returned %v, want none", ids)
	}
}
