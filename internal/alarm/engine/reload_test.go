// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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

// TestReload_SeedsCurrentValuesForNewlyEnrolledSensors pins the
// configuration-write half of the same guarantee Start carries: a
// sensor the engine has never seen an event for reads as "unknown",
// and the blocker policy classifies only a *known* active sensor. A
// contact enrolled while it stands open would therefore leave the zone
// reporting ready to arm — on the SPA, on MQTT, everywhere readiness is
// surfaced — until that contact happens to push.
func TestReload_SeedsCurrentValuesForNewlyEnrolledSensors(t *testing.T) {
	h := newHarness(t)
	h.seedZone("eg", "Erdgeschoss", defaultZoneConfig())
	h.start()

	// The operator enrolls a window contact that stands open right now.
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModePerimeter, hmenum.AlarmModeFull},
	})
	h.reader.set("window", true)
	if err := h.eng.Reload(h.ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	rd, ok := h.mustSnapshot("eg").Readiness[hmenum.AlarmModeFull]
	if !ok {
		t.Fatal("no readiness verdict for full after the reload")
	}
	if rd.Ready {
		t.Errorf("zone reports ready to arm with the freshly enrolled window open: %+v", rd)
	}
	if got := sortedStrings(rd.Blockers); len(got) != 1 || got[0] != "window" {
		t.Errorf("blockers = %v, want [window]", got)
	}
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
