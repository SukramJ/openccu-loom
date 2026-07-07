// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"
)

// TestDirtyTracker_RegisterInitiallyDirty pins the post-boot behaviour: a
// freshly-registered central starts in the "walk everything" state so the
// first flush after WireValuesCacheFlusher runs even when no events have
// fired yet — the boot-time RestoreCachedValue pass populates data points
// without bus events, and we still want those to land on disk on the very
// next tick rather than wait for the first CCU push.
func TestDirtyTracker_RegisterInitiallyDirty(t *testing.T) {
	t.Parallel()
	tr := newDirtyTracker()
	tr.Register("c1")
	claim, ok := tr.SwapClean("c1")
	if !ok {
		t.Fatal("SwapClean immediately after Register returned ok=false; want true")
	}
	if !claim.invalidateAll {
		t.Fatal("initial claim.invalidateAll = false; want true")
	}
}

// TestDirtyTracker_MarkSetsDirtyKey exercises the hot-path Mark call: after
// SwapClean drains the initial claim (zero dirty), Mark(channel, parameter)
// must re-arm the tracker with exactly that one key for the next SwapClean.
func TestDirtyTracker_MarkSetsDirtyKey(t *testing.T) {
	t.Parallel()
	tr := newDirtyTracker()
	tr.Register("c1")
	_, _ = tr.SwapClean("c1") // drain the initial claim

	if _, ok := tr.SwapClean("c1"); ok {
		t.Fatal("SwapClean after drain returned ok=true; want false (nothing changed yet)")
	}
	tr.Mark("c1", "DEV:1", "STATE")
	claim, ok := tr.SwapClean("c1")
	if !ok {
		t.Fatal("SwapClean after Mark returned ok=false; want true")
	}
	if claim.invalidateAll {
		t.Fatal("claim.invalidateAll = true after a single Mark; want false (key-scoped claim)")
	}
	if _, marked := claim.keys[dirtyKey{channelAddress: "DEV:1", parameter: "STATE"}]; !marked {
		t.Fatalf("claim.keys = %v, want to contain DEV:1/STATE", claim.keys)
	}
	if len(claim.keys) != 1 {
		t.Fatalf("claim.keys has %d entries, want exactly 1", len(claim.keys))
	}
}

// TestDirtyTracker_MarkUnknownCentralIsNoop guards the hot path against a
// Mark call for a central that never registered (e.g. a central added
// after the flusher already started). The expected outcome is silent: no
// panic, no spurious dirty claim.
func TestDirtyTracker_MarkUnknownCentralIsNoop(t *testing.T) {
	t.Parallel()
	tr := newDirtyTracker()
	tr.Mark("never-registered", "DEV:1", "STATE")
	if _, ok := tr.SwapClean("never-registered"); ok {
		t.Fatal("SwapClean on unknown central returned ok=true")
	}
}

// TestDirtyTracker_DuplicateRegisterPreservesState verifies the idempotent
// registration contract: calling Register twice for the same central must
// not reset an already-drained (clean) state back to "walk everything".
func TestDirtyTracker_DuplicateRegisterPreservesState(t *testing.T) {
	t.Parallel()
	tr := newDirtyTracker()
	tr.Register("c1")
	_, _ = tr.SwapClean("c1") // drain the initial claim
	tr.Register("c1")         // duplicate register

	if _, ok := tr.SwapClean("c1"); ok {
		t.Fatal("SwapClean after duplicate Register returned ok=true; duplicate Register re-armed the dirty claim")
	}
}

// TestDirtyTracker_PerCentralIsolation makes sure marking one central does
// not bleed into another central's dirty keys. Multi-CCU safety per
// ADR 0002.
func TestDirtyTracker_PerCentralIsolation(t *testing.T) {
	t.Parallel()
	tr := newDirtyTracker()
	tr.Register("c1")
	tr.Register("c2")
	_, _ = tr.SwapClean("c1")
	_, _ = tr.SwapClean("c2")

	tr.Mark("c1", "DEV:1", "STATE")
	if _, ok := tr.SwapClean("c1"); !ok {
		t.Fatal("c1 dirty claim did not flip after Mark")
	}
	if _, ok := tr.SwapClean("c2"); ok {
		t.Fatal("c2 dirty claim was set even though only c1 was marked")
	}
}

// TestDirtyTracker_MarkWhileInvalidateAllStaysInvalidateAll verifies that
// Mark-ing individual keys on a central still awaiting its first
// post-Register walk does not narrow that claim down to just the marked
// keys — the pending full walk must still happen.
func TestDirtyTracker_MarkWhileInvalidateAllStaysInvalidateAll(t *testing.T) {
	t.Parallel()
	tr := newDirtyTracker()
	tr.Register("c1")
	tr.Mark("c1", "DEV:1", "STATE")

	claim, ok := tr.SwapClean("c1")
	if !ok {
		t.Fatal("SwapClean returned ok=false; want true")
	}
	if !claim.invalidateAll {
		t.Fatal("claim.invalidateAll = false; a Mark before the first SwapClean must not narrow the pending full walk")
	}
}
