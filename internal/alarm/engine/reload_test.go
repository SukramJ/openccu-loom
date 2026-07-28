// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine_test

import (
	"testing"
	"time"
)

// This file covers Reload's zone-drop path: a direct store deletion of
// an zone the engine still manages must never leave a scheduler timer
// outliving the zone it belonged to.

// pendingTimerCount reports the number of timers still registered on
// the harness's manual scheduler.
func (h *harness) pendingTimerCount() int {
	h.t.Helper()
	h.sched.mu.Lock()
	defer h.sched.mu.Unlock()
	return len(h.sched.timers)
}

func TestReload_DroppingAZoneCancelsItsPendingAutoRearmTimer(t *testing.T) {
	h := newHarness(t)
	seedAutoRearmZone(h)
	h.start()
	triggerAndDisarm(h)

	if !h.journal.has("auto_rearm_scheduled") {
		t.Fatalf("missing auto_rearm_scheduled journal entry; got %v", h.journal.events())
	}
	if n := h.pendingTimerCount(); n != 1 {
		t.Fatalf("pending timers = %d, want 1 (the auto-rearm timer)", n)
	}

	if err := h.zones.Delete(h.ctx, "eg"); err != nil {
		t.Fatalf("delete zone from the store: %v", err)
	}
	if err := h.eng.Reload(h.ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, ok := h.eng.Zone("eg"); ok {
		t.Fatal("zone eg still known to the engine after Reload dropped it")
	}
	if n := h.pendingTimerCount(); n != 0 {
		t.Fatalf("pending timers = %d, want 0 (Reload must cancel the auto-rearm timer of a dropped zone)", n)
	}

	// Advancing past the original auto-rearm deadline must neither panic
	// nor resurrect the zone: the timer is gone, not merely a stale
	// no-op guarded by the zoneID lookup.
	h.advance(time.Minute)
	if h.journal.has("auto_rearmed") {
		t.Fatal("auto-rearm fired for an zone Reload already dropped")
	}
	if _, ok := h.eng.Zone("eg"); ok {
		t.Fatal("zone eg reappeared after the cancelled auto-rearm deadline")
	}
}
