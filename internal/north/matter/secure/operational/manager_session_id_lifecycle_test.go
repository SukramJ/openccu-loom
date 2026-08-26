// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package operational

// White-box tests for the session-id lifecycle: the placeholder slots
// [Manager.AllocateID] stakes for in-flight CASE handshakes, their
// reclamation, and what happens when a pre-allocated id is claimed for
// a peer while a live session still occupies it.

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/sigma"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// lifecycleKeys returns deterministic, non-zero Sigma session keys.
func lifecycleKeys(seed byte) sigma.SessionKeys {
	var keys sigma.SessionKeys
	for i := range keys.I2RKey {
		keys.I2RKey[i] = seed + byte(i)
	}
	for i := range keys.R2IKey {
		keys.R2IKey[i] = seed + byte(i+16)
	}
	return keys
}

// TestOpenFromSigmaWithID_ClosesForeignSessionOccupyingTheID pins that a
// session established under a pre-allocated id never silently displaces
// a live session of a DIFFERENT peer that occupies the same slot: the
// displaced session must go through the full teardown so its keys are
// zeroised and the subscription manager (wired to onSessionClose) drops
// the reports it was still encrypting for the old peer. Mirrors
// matter.js packages/protocol/src/session/SessionManager.ts:490-508,
// where getNextAvailableSessionId closes a session before reusing its id.
func TestOpenFromSigmaWithID_ClosesForeignSessionOccupyingTheID(t *testing.T) {
	t.Parallel()
	m := NewManager(newMinimalFakeStore())
	rec := &closeRecorder{}
	rec.wire(m)

	id, err := m.AllocateID()
	if err != nil {
		t.Fatalf("AllocateID: %v", err)
	}
	first, err := m.OpenFromSigmaWithID(id, 1, 0xB0B, 0xA11CE, 0x1111, nil, lifecycleKeys(0x10))
	if err != nil {
		t.Fatalf("OpenFromSigmaWithID first peer: %v", err)
	}

	// A second, DIFFERENT peer grafts onto the same exchange and lands
	// on the same pre-allocated id.
	second, err := m.OpenFromSigmaWithID(id, 2, 0xB0B, 0xB0B0B, 0x2222, nil, lifecycleKeys(0x50))
	if err != nil {
		t.Fatalf("OpenFromSigmaWithID second peer: %v", err)
	}

	_, closed, _, _ := rec.snapshot()
	if len(closed) != 1 || closed[0] != id {
		t.Fatalf("onSessionClose calls = %v, want [%d] — the displaced session never fired the close cascade", closed, id)
	}
	if _, err := first.Session.Encrypt(&message.Header{}, 0, []byte{0x01}); err == nil {
		t.Error("displaced session still encrypts — its key material was never zeroised")
	}
	got, err := m.Get(id)
	if err != nil {
		t.Fatalf("Get(%d): %v", id, err)
	}
	if got != second {
		t.Errorf("Get(%d) returned the stale entry, want the newly opened one", id)
	}
}

// TestOpenFromPaseWithID_ClosesSessionOccupyingTheID pins the same
// contract on the PASE opener: a commissioning session installed under
// a pre-allocated id must not displace a live session silently either.
func TestOpenFromPaseWithID_ClosesSessionOccupyingTheID(t *testing.T) {
	t.Parallel()
	m := NewManager(newMinimalFakeStore())
	rec := &closeRecorder{}
	rec.wire(m)

	id, err := m.AllocateID()
	if err != nil {
		t.Fatalf("AllocateID: %v", err)
	}
	occupant, err := m.OpenFromSigmaWithID(id, 1, 0xB0B, 0xA11CE, 0x1111, nil, lifecycleKeys(0x10))
	if err != nil {
		t.Fatalf("OpenFromSigmaWithID: %v", err)
	}
	if _, err := m.OpenFromPaseWithID(id, 0xB0B, 0xB0B0B, 0x2222, []byte("shared-secret")); err != nil {
		t.Fatalf("OpenFromPaseWithID: %v", err)
	}

	_, closed, _, _ := rec.snapshot()
	if len(closed) != 1 || closed[0] != id {
		t.Fatalf("onSessionClose calls = %v, want [%d]", closed, id)
	}
	if _, err := occupant.Session.Encrypt(&message.Header{}, 0, []byte{0x01}); err == nil {
		t.Error("displaced session still encrypts — its key material was never zeroised")
	}
}

// TestReapIdle_ReclaimsAbandonedPlaceholderIDs pins that the id a CASE
// handshake staked via AllocateID comes back when the handshake never
// reaches Sigma3. The per-exchange CASE adapter is dropped 60 s after
// its last datagram, so past that point nothing can ever claim the
// placeholder — without reclamation every aborted Sigma1 burns one of
// the 65534 session-id slots for the lifetime of the daemon.
func TestReapIdle_ReclaimsAbandonedPlaceholderIDs(t *testing.T) {
	t.Parallel()
	m := NewManager(newMinimalFakeStore())
	rec := &closeRecorder{}
	rec.wire(m)

	const aborted = 32
	for range aborted {
		if _, err := m.AllocateID(); err != nil {
			t.Fatalf("AllocateID: %v", err)
		}
	}
	if n := m.Active(); n != aborted {
		t.Fatalf("Active() = %d, want %d staked placeholders", n, aborted)
	}

	// A placeholder younger than the TTL must survive — an in-flight
	// handshake still owns its id.
	m.reapIdle(time.Minute)
	if n := m.Active(); n != aborted {
		t.Fatalf("Active() = %d after an early sweep, want %d — an in-flight handshake lost its id", n, aborted)
	}

	// Age every placeholder past the TTL and sweep again.
	m.mu.Lock()
	for _, e := range m.sessions {
		e.allocatedAt = e.allocatedAt.Add(-2 * PlaceholderIDTTL)
	}
	m.mu.Unlock()
	m.reapIdle(time.Minute)

	if n := m.Active(); n != 0 {
		t.Fatalf("Active() = %d after the TTL sweep, want 0 — abandoned placeholders leak their session id", n)
	}
	graceful, closed, _, reannounce := rec.snapshot()
	if len(graceful) != 0 || len(closed) != 0 || reannounce != 0 {
		t.Errorf("placeholder reap fired peer-facing hooks (graceful=%v closed=%v reannounce=%d) — there is no session to notify",
			graceful, closed, reannounce)
	}
}

// TestAllocateID_EvictsOldestPlaceholderWhenExhausted pins the
// last-resort reclaim: with every id staked by a handshake that never
// completed, a fresh allocation takes the oldest placeholder's slot
// instead of failing the CASE exchange outright. Mirrors matter.js
// packages/protocol/src/session/SessionManager.ts:504
// (`getNextAvailableSessionId` → `findOldestInactiveSession`).
func TestAllocateID_EvictsOldestPlaceholderWhenExhausted(t *testing.T) {
	t.Parallel()
	m := NewManager(newMinimalFakeStore())

	base := time.Now()
	const maxID = uint16(0xFFFE)
	m.mu.Lock()
	for id := uint16(1); id <= maxID; id++ {
		// id 7 is the oldest placeholder; every other slot is younger.
		age := base
		if id == 7 {
			age = base.Add(-time.Hour)
		}
		m.sessions[id] = &Entry{SessionID: id, allocatedAt: age}
	}
	m.mu.Unlock()

	id, err := m.AllocateID()
	if err != nil {
		t.Fatalf("AllocateID with a full table: %v, want the oldest placeholder's id", err)
	}
	if id != 7 {
		t.Fatalf("AllocateID = %d, want 7 (the oldest placeholder)", id)
	}
	if n := m.Active(); n != int(maxID) {
		t.Errorf("Active() = %d, want %d — the evicted slot was not reused", n, maxID)
	}
}
