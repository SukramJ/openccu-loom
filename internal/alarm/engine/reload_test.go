// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine_test

import (
	"testing"
	"time"
)

// This file covers Reload's area-drop path: a direct store deletion of
// an area the engine still manages must never leave a scheduler timer
// outliving the area it belonged to.

// pendingTimerCount reports the number of timers still registered on
// the harness's manual scheduler.
func (h *harness) pendingTimerCount() int {
	h.t.Helper()
	h.sched.mu.Lock()
	defer h.sched.mu.Unlock()
	return len(h.sched.timers)
}

func TestReload_DroppingAnAreaCancelsItsPendingAutoRearmTimer(t *testing.T) {
	h := newHarness(t)
	seedAutoRearmArea(h)
	h.start()
	triggerAndDisarm(h)

	if !h.journal.has("auto_rearm_scheduled") {
		t.Fatalf("missing auto_rearm_scheduled journal entry; got %v", h.journal.events())
	}
	if n := h.pendingTimerCount(); n != 1 {
		t.Fatalf("pending timers = %d, want 1 (the auto-rearm timer)", n)
	}

	if err := h.areas.Delete(h.ctx, "eg"); err != nil {
		t.Fatalf("delete area from the store: %v", err)
	}
	if err := h.eng.Reload(h.ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, ok := h.eng.Area("eg"); ok {
		t.Fatal("area eg still known to the engine after Reload dropped it")
	}
	if n := h.pendingTimerCount(); n != 0 {
		t.Fatalf("pending timers = %d, want 0 (Reload must cancel the auto-rearm timer of a dropped area)", n)
	}

	// Advancing past the original auto-rearm deadline must neither panic
	// nor resurrect the area: the timer is gone, not merely a stale
	// no-op guarded by the areaID lookup.
	h.advance(time.Minute)
	if h.journal.has("auto_rearmed") {
		t.Fatal("auto-rearm fired for an area Reload already dropped")
	}
	if _, ok := h.eng.Area("eg"); ok {
		t.Fatal("area eg reappeared after the cancelled auto-rearm deadline")
	}
}
