// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// The tests in this file pin the complete restart-restore table of
// docs/alarm-concept.md §10.2, including the restart-loop breaker and
// the clock-plausibility rule.

func TestRestore_DisarmedStaysDisarmed(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)

	h.restart(time.Minute)
	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("disarmed restore fired outputs: %d", n)
	}
}

func TestRestore_ArmedReEvaluatesFreshValues_InstantSensorTriggers(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.armFull()

	// The window opens while the daemon is down.
	h.reader.set("window", true)
	h.restart(time.Minute)

	h.wantState("eg", hmenum.AlarmAreaStateTriggered)
	if !h.journal.has("activation_during_downtime") {
		t.Fatalf("missing downtime-activation journal entry; got %v", h.journal.events())
	}
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count = %d, want 1", n)
	}
	inc, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("expected an open incident")
	}
	if inc.Mode != hmenum.AlarmModeFull {
		t.Fatalf("incident mode = %s, want full", inc.Mode)
	}
}

func TestRestore_ArmedReEvaluatesFreshValues_DelayedSensorGoesPending(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.armFull()

	// The door (entry-delay flagged) opens while the daemon is down.
	h.reader.set("door", true)
	h.restart(time.Minute)

	h.wantState("eg", hmenum.AlarmAreaStatePending)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("pending restore fired outputs: %d", n)
	}
	// The real entry delay: a disarm inside the window produces no alarm.
	if err := h.eng.Disarm(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("disarm: %v", err)
	}
	h.advance(time.Hour)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("disarmed pending still fired outputs: %d", n)
	}
}

func TestRestore_ArmedSensorOpenAtArmDoesNotTrigger(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()

	// The window is open, gets force-armed (bypassing it would change
	// semantics — instead use an allow-open sensor: seed a dedicated
	// area variant). Here: open window blocks, so force-arm with
	// bypass, then verify the bypassed sensor never triggers.
	h.eng.HandleSensorEvent(h.ctx, "window", true)
	res, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, Force: true, By: "tester"})
	if err != nil {
		t.Fatalf("force arm: %v", err)
	}
	if got := sortedStrings(res.Bypassed); len(got) != 1 || got[0] != "window" {
		t.Fatalf("bypassed = %v, want [window]", got)
	}
	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)

	// Still open after restart: the bypass survives, no trigger.
	h.reader.set("window", true)
	h.restart(time.Minute)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("bypassed sensor fired outputs after restore: %d", n)
	}
}

func TestRestore_ArmingDeadlinePassedCompletesArm(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.wantState("eg", hmenum.AlarmAreaStateArming)

	// Down for longer than the remaining exit delay.
	h.restart(2 * time.Minute)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
	if got := h.mustSnapshot("eg").Mode; got != hmenum.AlarmModeFull {
		t.Fatalf("mode = %s, want full", got)
	}
}

func TestRestore_ArmingDeadlinePassedBlockedFailsArm(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}

	// The window opens during downtime; the completion readiness
	// re-check fails and the arm falls back to disarmed — loudly.
	h.reader.set("window", true)
	h.restart(2 * time.Minute)

	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)
	if !h.journal.has("arm_failed_on_restore") {
		t.Fatalf("missing arm_failed journal entry; got %v", h.journal.events())
	}
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("failed arm fired outputs: %d", n)
	}
}

func TestRestore_ArmingResumesRemainingDelay(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.advance(10 * time.Second) // 20 s remain

	h.restart(5 * time.Second) // 15 s remain after downtime
	h.wantState("eg", hmenum.AlarmAreaStateArming)

	h.advance(14 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateArming)
	h.advance(time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
}

func TestRestore_PendingDeadlinePassedEscalatesToTriggered(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "door", true)
	h.wantState("eg", hmenum.AlarmAreaStatePending)

	// Down past the 15 s entry delay: better a late alarm than a
	// silently swallowed one.
	h.restart(time.Minute)
	h.wantState("eg", hmenum.AlarmAreaStateTriggered)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count = %d, want 1", n)
	}
	fire := h.outputs.lastFire(t)
	if !fire.Opts.Restored {
		t.Fatal("escalated fire not marked Restored")
	}
	if !h.journal.has("pending_elapsed_while_down") {
		t.Fatalf("missing pending-elapsed journal entry; got %v", h.journal.events())
	}
}

func TestRestore_PendingResumesRemainingCountdown(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "door", true)
	h.advance(5 * time.Second) // 10 s remain

	h.restart(4 * time.Second) // 6 s remain
	h.wantState("eg", hmenum.AlarmAreaStatePending)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("resumed pending fired outputs: %d", n)
	}

	// Disarm inside the window: no alarm, ever.
	if err := h.eng.Disarm(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("disarm: %v", err)
	}
	h.advance(time.Hour)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("disarm during pending still alarmed: %d fires", n)
	}
}

func TestRestore_TriggeredInsideWindowRefires(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmAreaStateTriggered)

	h.restart(10 * time.Second) // trigger window is 60 s
	h.wantState("eg", hmenum.AlarmAreaStateTriggered)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count = %d, want 1 (the restore re-fire)", n)
	}
	fire := h.outputs.lastFire(t)
	if !fire.Opts.Restored || fire.Opts.Degraded {
		t.Fatalf("re-fire opts = %+v, want Restored && !Degraded", fire.Opts)
	}
	inc, ok := h.openIncident("eg")
	if !ok || inc.RestoreRefires != 1 {
		t.Fatalf("restore_refires = %d (ok=%v), want 1", inc.RestoreRefires, ok)
	}
}

func TestRestore_TriggeredWindowElapsedExecutesPostTriggerPolicy(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "window", true)

	// Down past the 60 s trigger window: no re-fire, back to armed.
	h.restart(5 * time.Minute)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("elapsed trigger window still fired: %d", n)
	}
	if !h.journal.has("trigger_window_elapsed_while_down") {
		t.Fatalf("missing elapsed-window journal entry; got %v", h.journal.events())
	}
	if _, ok := h.openIncident("eg"); ok {
		t.Fatal("incident should be closed after the elapsed window")
	}
}

func TestRestore_TriggeredWindowElapsedDisarmPolicy(t *testing.T) {
	h := newHarness(t)
	cfg := defaultAreaConfig()
	cfg.PostTrigger = hmenum.AlarmPostTriggerDisarm
	h.seedArea("eg", "Erdgeschoss", cfg)
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	h.start()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "window", true)

	h.restart(5 * time.Minute)
	h.wantState("eg", hmenum.AlarmAreaStateDisarmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("elapsed trigger window still fired: %d", n)
	}
}

func TestRestore_SilencedIncidentStaysSilent(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "window", true)
	if err := h.eng.Silence(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("silence: %v", err)
	}

	// Restart inside the trigger window: S3 persistence — the
	// silenced incident never sounds again, but the state stays
	// triggered.
	h.restart(10 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateTriggered)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("silenced incident re-fired after restart: %d", n)
	}
	if !h.journal.has("silenced_incident_restored") {
		t.Fatalf("missing silenced-restore journal entry; got %v", h.journal.events())
	}

	// The remaining trigger window elapses silently into post-trigger.
	h.advance(time.Minute)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("silenced incident fired on window end: %d", n)
	}
}

func TestRestore_RestartLoopBreakerDegradesAfterK(t *testing.T) {
	h := newHarness(t)
	cfg := defaultAreaConfig()
	// Long window so repeated restarts stay inside it.
	full := cfg.Modes[hmenum.AlarmModeFull]
	full.TriggerSeconds = 600
	cfg.Modes[hmenum.AlarmModeFull] = full
	h.seedArea("eg", "Erdgeschoss", cfg)
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	h.start()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "window", true)

	// K = 3 (default): re-fires 1..3 sound normally, the 4th degrades.
	for i := 1; i <= 3; i++ {
		h.restart(time.Second)
		fire := h.outputs.lastFire(t)
		if fire.Opts.Degraded {
			t.Fatalf("re-fire %d already degraded", i)
		}
	}
	h.restart(time.Second)
	fire := h.outputs.lastFire(t)
	if !fire.Opts.Degraded {
		t.Fatal("4th restore re-fire not degraded — restart-loop breaker missing")
	}
	if !h.journal.has("restart_loop_breaker_degraded") {
		t.Fatalf("missing loop-breaker journal entry; got %v", h.journal.events())
	}
}

func TestRestore_ImplausibleClock_PendingDemotesToArmed(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "door", true)
	h.wantState("eg", hmenum.AlarmAreaStatePending)

	// Boot with a pre-epoch clock (RTC-less host before NTP): never
	// auto-escalate off an untrusted clock.
	h.restartAt(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("implausible clock escalated pending: %d fires", n)
	}
	if !h.journal.has("pending_demoted_implausible_clock") {
		t.Fatalf("missing demotion journal entry; got %v", h.journal.events())
	}
}

func TestRestore_ImplausibleClock_ArmingNeverAutoCompletes(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.advance(10 * time.Second) // 20 s remain

	h.restartAt(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	// Not completed off wall math; the remaining (relative) delay
	// resumes instead.
	h.wantState("eg", hmenum.AlarmAreaStateArming)
	h.advance(20 * time.Second)
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
}

func TestRestore_ImplausibleClock_TriggeredNeverRefires(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "window", true)

	h.restartAt(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	h.wantState("eg", hmenum.AlarmAreaStateTriggered)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("implausible clock re-fired outputs: %d", n)
	}
	inc, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("incident must survive an implausible-clock restore")
	}
	if inc.RestoreRefires != 0 {
		t.Fatalf("restore_refires = %d, want 0 (no re-fire happened)", inc.RestoreRefires)
	}
}

func TestRestore_TriggeredWithLostIncidentNeverRefires(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "window", true)
	inc, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("expected an open incident")
	}

	// Simulate incident-row loss: close it behind the engine's back
	// while the daemon is down.
	h.eng.Stop(h.ctx)
	if err := h.incidents.Close(h.ctx, inc.ID, h.clk.Now().UnixMilli(), "corruption-simulation"); err != nil {
		t.Fatalf("close incident: %v", err)
	}
	h.freshPorts(h.clk.Now().Add(10 * time.Second))
	h.start()

	// Without a ledger there is nothing to bound re-fires: never
	// fire, leave triggered via the post-trigger policy, say so.
	h.wantState("eg", hmenum.AlarmAreaStateArmed)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("lost incident still fired: %d", n)
	}
	if !h.journal.has("incident_lost_on_restore") {
		t.Fatalf("missing incident-lost journal entry; got %v", h.journal.events())
	}
}

func TestRestore_StopPersistsFreshRemainingDurations(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	h.start()
	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.advance(25 * time.Second) // 5 s remain
	h.eng.Stop(h.ctx)

	row, ok, err := h.states.Get(h.ctx, "eg")
	if err != nil || !ok {
		t.Fatalf("state row: ok=%v err=%v", ok, err)
	}
	if row.State != hmenum.AlarmAreaStateArming {
		t.Fatalf("persisted state = %s, want arming", row.State)
	}
	// The tuple must carry the fresh remaining duration, not the
	// schedule-time one — an implausible-clock restore depends on it.
	type tuple struct {
		Kind        string `json:"kind"`
		RemainingMS int64  `json:"remaining_ms"`
	}
	var tuples []tuple
	if err := jsonUnmarshal(row.TimersJSON, &tuples); err != nil {
		t.Fatalf("timers json: %v", err)
	}
	if len(tuples) != 1 || tuples[0].Kind != "exit_delay" {
		t.Fatalf("timers = %+v, want one exit_delay tuple", tuples)
	}
	if got := tuples[0].RemainingMS; got != (5 * time.Second).Milliseconds() {
		t.Fatalf("remaining_ms = %d, want 5000", got)
	}
}
