// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package health

import (
	"testing"
)

// TestOnStatusChangeFiresOnAChangeOnly pins the status listener: it is
// told when a component's status moves and not when a sample repeats the
// status the component already has.
func TestOnStatusChangeFiresOnAChangeOnly(t *testing.T) {
	t.Parallel()
	tr := NewTracker()
	calls := 0
	tr.OnStatusChange(func() { calls++ })
	step := func(what string, want int, do func()) {
		t.Helper()
		do()
		if calls != want {
			t.Fatalf("after %s: listener calls = %d, want %d", what, calls, want)
		}
	}
	step("a first healthy sample", 1, func() { tr.Record("mqtt", Sample{Healthy: true}) })
	step("a repeated healthy sample", 1, func() { tr.Record("mqtt", Sample{Healthy: true}) })
	step("the first unhealthy sample (degraded)", 2, func() { tr.Record("mqtt", Sample{}) })
	step("the second unhealthy sample (unhealthy)", 3, func() { tr.Record("mqtt", Sample{}) })
	step("a repeated unhealthy sample", 3, func() { tr.Record("mqtt", Sample{}) })
	step("a quality note on an unhealthy component (degraded)", 4, func() { tr.RecordQuality("mqtt", "noise") })
	step("a repeated quality note", 4, func() { tr.RecordQuality("mqtt", "noise") })
	step("a stated unhealthy condition", 5, func() { tr.RecordUnhealthy("mqtt", Sample{}) })
	step("unregistering the component", 6, func() { tr.Unregister("mqtt") })
	step("unregistering it again", 6, func() { tr.Unregister("mqtt") })
}

// TestOnStatusChangeTakesSeveralListenersAndRemovesOne pins that the
// listeners are independent: each is told, and a removed one no longer.
func TestOnStatusChangeTakesSeveralListenersAndRemovesOne(t *testing.T) {
	t.Parallel()
	tr := NewTracker()
	var a, b int
	removeA := tr.OnStatusChange(func() { a++ })
	tr.OnStatusChange(func() { b++ })
	tr.Record("x", Sample{Healthy: true})
	removeA()
	tr.Record("y", Sample{Healthy: true})
	if a != 1 || b != 2 {
		t.Errorf("listener calls = %d and %d, want 1 and 2", a, b)
	}
}

// TestOnStatusChangeListenerMayReadTheTracker pins that a listener runs
// outside the tracker's lock: reading the snapshot from it does not
// deadlock.
func TestOnStatusChangeListenerMayReadTheTracker(t *testing.T) {
	t.Parallel()
	tr := NewTracker()
	seen := 0
	tr.OnStatusChange(func() { seen = len(tr.Snapshot()) })
	tr.Record("x", Sample{Healthy: true})
	if seen != 1 {
		t.Errorf("snapshot read from the listener held %d components, want 1", seen)
	}
}
