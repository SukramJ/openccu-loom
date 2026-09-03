// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// This file pins the post-trigger-disarm transition as one rule, not
// two: the live path (finishTriggered) and the restore path
// (finishTriggeredOnRestore) must leave the zone in the same shape —
// same cleared fields, same journal, same auto-rearm timer.

// hmAlmSeedPostTriggerDisarmZone seeds a zone that disarms after a
// trigger episode and re-arms 30s later, plus one window sensor.
func hmAlmSeedPostTriggerDisarmZone(h *harness) {
	h.t.Helper()
	cfg := defaultZoneConfig()
	cfg.PostTrigger = hmenum.AlarmPostTriggerDisarm
	cfg.AutoRearmSeconds = 30
	h.seedZone("eg", "Erdgeschoss", cfg)
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
}

// TestPostTriggerDisarm_RestoreSchedulesTheAutoRearm drives the gap the
// live-path tests never cross: the daemon dies while the zone is
// triggered and comes back after the trigger window has elapsed, so the
// disarm is executed by the restore path. That path must schedule the
// same auto-rearm the live path schedules — otherwise a zone configured
// with auto_rearm_s is left permanently unprotected by a crash.
func TestPostTriggerDisarm_RestoreSchedulesTheAutoRearm(t *testing.T) {
	h := newHarness(t)
	hmAlmSeedPostTriggerDisarmZone(h)
	h.start()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	// Down past the trigger window: the restore completes the
	// post-trigger disarm without firing outputs.
	h.restart(5 * time.Minute)
	h.wantState("eg", hmenum.AlarmZoneStateDisarmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("elapsed trigger window still fired: %d", n)
	}
	if !h.journal.has("auto_rearm_scheduled") {
		t.Fatalf("restore-side post-trigger disarm scheduled no auto-rearm; journal = %v", h.journal.events())
	}
	row, ok, err := h.states.Get(h.ctx, "eg")
	if err != nil || !ok {
		t.Fatalf("state row: ok=%v err=%v", ok, err)
	}
	if got := decodeAutoRearmMode(t, row.ContextJSON); !hmenum.AlarmMode(got).Armed() {
		t.Fatalf("persisted auto-rearm mode = %q, want the pre-incident armed mode", got)
	}

	// The quiet period elapses, then full mode's own exit delay runs.
	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateArming)
	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)
}

// TestPostTriggerDisarm_RestoreJournalsTheDisarm pins the second
// observable difference between the two copies: an operator reading the
// journal after a restore-completed post-trigger disarm must see the
// same disarm entry the live path emits.
func TestPostTriggerDisarm_RestoreJournalsTheDisarm(t *testing.T) {
	h := newHarness(t)
	hmAlmSeedPostTriggerDisarmZone(h)
	h.start()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "window", true)

	h.restart(5 * time.Minute)
	h.wantState("eg", hmenum.AlarmZoneStateDisarmed)
	if !h.journal.has("disarmed_post_trigger") {
		t.Fatalf("restore-side post-trigger disarm emitted no disarm entry; journal = %v", h.journal.events())
	}
}

// TestPostTriggerDisarm_RestoreClearsTheAlwaysOnResidue pins the field
// set a return to disarmed must leave behind. A zone can be persisted
// Triggered while its context still carries the always-on pre-trigger
// tuple and its incident row is unrecoverable (a failed incidents.Create
// leaves IncidentID 0, journalled as incident_persist_failed). The
// restore then disarms it. If preTriggerState survives that disarm, the
// next ordinary intrusion trigger routes to finishAlwaysOn and the zone
// comes back armed instead of disarming.
func TestPostTriggerDisarm_RestoreClearsTheAlwaysOnResidue(t *testing.T) {
	h := newHarness(t)
	cfg := defaultZoneConfig()
	cfg.PostTrigger = hmenum.AlarmPostTriggerDisarm
	h.seedZone("eg", "Erdgeschoss", cfg)
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	h.seedSensor("hazard", "eg", hmenum.AlarmSensorTypeHazard, engine.SensorConfig{
		AlwaysOn: true,
	})
	h.start()
	h.armFull()

	// An always-on hazard records the pre-trigger tuple in the context.
	h.eng.HandleSensorEvent(h.ctx, "hazard", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	// Make the incident unrecoverable across the restart, the way a
	// failed incidents.Create does: the state row stays Triggered with
	// the pre-trigger tuple, but no open incident can be attached.
	if inc, ok := h.openIncident("eg"); ok {
		if err := h.incidents.Close(h.ctx, inc.ID, testStart.Add(time.Second).UnixMilli(), "lost"); err != nil {
			t.Fatalf("close incident: %v", err)
		}
	}

	h.restart(5 * time.Minute)
	h.wantState("eg", hmenum.AlarmZoneStateDisarmed)
	// Confirms which restore arm ran: the unrecoverable-incident one.
	if !h.journal.has("incident_lost_on_restore") {
		t.Fatalf("the restore did not take the lost-incident path; journal = %v", h.journal.events())
	}

	row, ok, err := h.states.Get(h.ctx, "eg")
	if err != nil || !ok {
		t.Fatalf("state row: ok=%v err=%v", ok, err)
	}
	if got := hmAlmDecodePreTriggerState(t, row.ContextJSON); got != "" {
		t.Fatalf("persisted pre_trigger_state = %q after a disarm, want cleared", got)
	}
}

// hmAlmDecodePreTriggerState reads the persisted always-on pre-trigger
// state out of a zone's context document.
func hmAlmDecodePreTriggerState(t *testing.T, contextJSON string) string {
	t.Helper()
	var doc struct {
		PreTriggerState string `json:"pre_trigger_state"`
	}
	if err := jsonUnmarshal(contextJSON, &doc); err != nil {
		t.Fatalf("context json: %v", err)
	}
	return doc.PreTriggerState
}
