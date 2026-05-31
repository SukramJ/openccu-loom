// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"
)

// TestDirtyTracker_RegisterInitiallyDirty pins the post-boot
// behaviour: a freshly-registered central is dirty so the first
// flush after WireValuesCacheFlusher runs even when no events
// have fired yet — the boot-time RestoreCachedValue pass populates
// data points without bus events, and we still want those to land
// on disk on the very next tick rather than wait for the first
// CCU push.
func TestDirtyTracker_RegisterInitiallyDirty(t *testing.T) {
	t.Parallel()
	tr := newDirtyTracker()
	flag := tr.Register("c1")
	if flag == nil {
		t.Fatal("Register returned nil flag")
	}
	if !tr.SwapClean("c1") {
		t.Fatal("SwapClean immediately after Register returned false; want true")
	}
}

// TestDirtyTracker_MarkSetsDirty exercises the hot-path Mark call:
// after SwapClean (zero dirty), Mark must re-arm the flag for the
// next SwapClean.
func TestDirtyTracker_MarkSetsDirty(t *testing.T) {
	t.Parallel()
	tr := newDirtyTracker()
	tr.Register("c1")
	_ = tr.SwapClean("c1") // reset

	if tr.SwapClean("c1") {
		t.Fatal("SwapClean after reset returned true; want false")
	}
	tr.Mark("c1")
	if !tr.SwapClean("c1") {
		t.Fatal("SwapClean after Mark returned false; want true")
	}
}

// TestDirtyTracker_MarkUnknownCentralIsNoop guards the hot path
// against a Mark call for a central that never registered (e.g. a
// central added after the flusher already started). The expected
// outcome is silent: no panic, no spurious dirty flag.
func TestDirtyTracker_MarkUnknownCentralIsNoop(t *testing.T) {
	t.Parallel()
	tr := newDirtyTracker()
	tr.Mark("never-registered")
	if tr.SwapClean("never-registered") {
		t.Fatal("SwapClean on unknown central returned true")
	}
}

// TestDirtyTracker_DuplicateRegisterReturnsSameFlag verifies the
// idempotent contract: calling Register twice with the same name
// returns the same *atomic.Bool, so a caller that stashed the
// pointer keeps a working reference.
func TestDirtyTracker_DuplicateRegisterReturnsSameFlag(t *testing.T) {
	t.Parallel()
	tr := newDirtyTracker()
	f1 := tr.Register("c1")
	_ = tr.SwapClean("c1")
	f2 := tr.Register("c1")
	if f1 != f2 {
		t.Fatal("second Register returned a different *atomic.Bool")
	}
	// Re-registering must NOT clobber the existing flag's state —
	// the first SwapClean above left it false; the duplicate
	// Register must preserve that.
	if tr.SwapClean("c1") {
		t.Fatal("duplicate Register re-armed the dirty flag; want preserved-clean")
	}
}

// TestDirtyTracker_PerCentralIsolation makes sure marking one
// central does not bleed into another central's flag. Multi-CCU
// safety per ADR 0002.
func TestDirtyTracker_PerCentralIsolation(t *testing.T) {
	t.Parallel()
	tr := newDirtyTracker()
	tr.Register("c1")
	tr.Register("c2")
	_ = tr.SwapClean("c1")
	_ = tr.SwapClean("c2")

	tr.Mark("c1")
	if !tr.SwapClean("c1") {
		t.Fatal("c1 dirty flag did not flip after Mark")
	}
	if tr.SwapClean("c2") {
		t.Fatal("c2 dirty flag was set even though only c1 was marked")
	}
}
