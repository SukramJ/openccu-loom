// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package operational

import "testing"

// TestRandomInitialSessionID_InRangeAndVaries asserts the allocator
// seed is drawn from [1, 0xFFFE] and is not a fixed constant. Mirrors
// matter.js SessionManager.ts:213 crypto.randomUint16 seeding.
func TestRandomInitialSessionID_InRangeAndVaries(t *testing.T) {
	t.Parallel()
	seen := make(map[uint16]bool)
	for range 256 {
		id := randomInitialSessionID()
		if id == 0 || id > 0xFFFE {
			t.Fatalf("randomInitialSessionID() = %d, want in [1, 0xFFFE]", id)
		}
		seen[id] = true
	}
	if len(seen) < 2 {
		t.Fatalf("randomInitialSessionID() produced %d distinct values over 256 draws; want a randomized seed", len(seen))
	}
}

// TestNewManager_SeedsRandomInitialSessionID asserts a fresh manager
// does not deterministically begin allocating session ids at 1 — it
// seeds a random start like matter.js SessionManager.ts:213.
func TestNewManager_SeedsRandomInitialSessionID(t *testing.T) {
	t.Parallel()
	seen := make(map[uint16]bool)
	for range 64 {
		m := NewManager(newMinimalFakeStore())
		if m.nextID == 0 || m.nextID > 0xFFFE {
			t.Fatalf("NewManager nextID = %d, want in [1, 0xFFFE]", m.nextID)
		}
		seen[m.nextID] = true
	}
	if len(seen) < 2 {
		t.Fatalf("NewManager seeded the same nextID across 64 constructions; want randomized")
	}
}
