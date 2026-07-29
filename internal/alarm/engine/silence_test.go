// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine_test

import (
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// This file covers the S3 silence semantics, disarm-from-any-state,
// acknowledge, and the fail-visible (not fail-stop) handling of
// output-port errors.

// lastStop returns the most recent StopAll call, failing the test if
// none was recorded.
func lastStop(t *testing.T, o *fakeOutputs) stopCall {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.stops) == 0 {
		t.Fatal("expected at least one StopAll call")
	}
	return o.stops[len(o.stops)-1]
}

func TestSilence_DuringTriggeredStopsOutputsButStaysTriggered(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	inc, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("expected an open incident")
	}

	if err := h.eng.Silence(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("silence: %v", err)
	}
	if n := h.outputs.stopCount(); n < 1 {
		t.Fatalf("StopAll calls = %d, want >= 1", n)
	}
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	got, ok, err := h.incidents.Get(h.ctx, inc.ID)
	if err != nil || !ok {
		t.Fatalf("get incident: ok=%v err=%v", ok, err)
	}
	if !got.Silenced || got.SilencedBy != "tester" {
		t.Fatalf("incident = %+v, want Silenced by tester", got)
	}
	if !h.journal.has("silenced") {
		t.Fatalf("missing silenced journal entry; got %v", h.journal.events())
	}
}

func TestSilence_SuppressesFurtherRetriggerCycles(t *testing.T) {
	h := newHarness(t)
	cfg := defaultZoneConfig()
	full := cfg.Modes[hmenum.AlarmModeFull]
	full.MaxRetriggerCycles = 3
	cfg.Modes[hmenum.AlarmModeFull] = full
	h.seedZone("eg", "Erdgeschoss", cfg)
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count = %d, want 1", n)
	}

	if err := h.eng.Silence(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("silence: %v", err)
	}

	h.advance(60 * time.Second)
	if n := h.outputs.fireCount(); n != 1 {
		t.Fatalf("FireCycle count after silenced window elapsed = %d, want 1 (no retrigger)", n)
	}
	h.wantState("eg", hmenum.AlarmZoneStateArmed)
	if _, ok := h.openIncident("eg"); ok {
		t.Fatal("incident should be closed after post-trigger")
	}
}

func TestSilence_WithoutIncidentStillStopsOutputs(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	if err := h.eng.Silence(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("silence: %v", err)
	}
	if !h.journal.has("silence_requested") {
		t.Fatalf("missing silence_requested journal entry; got %v", h.journal.events())
	}
	stop := lastStop(t, h.outputs)
	if stop.ZoneID != "eg" || stop.IncidentID != 0 {
		t.Fatalf("stop call = %+v, want zone eg incident 0", stop)
	}
}

func TestSilence_AllSilencesEveryTriggeredZone(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.seedZone("og", "Obergeschoss", defaultZoneConfig())
	h.seedSensor("og-window", "og", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	h.start()

	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm eg: %v", err)
	}
	h.advance(30 * time.Second)
	if _, err := h.eng.Arm(h.ctx, "og", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm og: %v", err)
	}
	h.advance(30 * time.Second)

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.eng.HandleSensorEvent(h.ctx, "og-window", true)
	incEg, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("expected open incident for eg")
	}
	incOg, ok := h.openIncident("og")
	if !ok {
		t.Fatal("expected open incident for og")
	}

	h.eng.SilenceAll(h.ctx, "tester", "test")

	gotEg, ok, err := h.incidents.Get(h.ctx, incEg.ID)
	if err != nil || !ok || !gotEg.Silenced {
		t.Fatalf("eg incident silenced = %+v (ok=%v err=%v)", gotEg, ok, err)
	}
	gotOg, ok, err := h.incidents.Get(h.ctx, incOg.ID)
	if err != nil || !ok || !gotOg.Silenced {
		t.Fatalf("og incident silenced = %+v (ok=%v err=%v)", gotOg, ok, err)
	}
}

func TestDisarm_FromTriggeredClosesIncidentAndClearsBypass(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "door", true)
	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, Force: true, By: "tester"}); err != nil {
		t.Fatalf("force arm: %v", err)
	}
	h.advance(30 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateArmed)
	if got := sortedStrings(h.mustSnapshot("eg").Bypassed); len(got) != 1 || got[0] != "door" {
		t.Fatalf("bypassed before trigger = %v, want [door]", got)
	}

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	inc, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("expected an open incident")
	}

	if err := h.eng.Disarm(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("disarm: %v", err)
	}
	h.wantState("eg", hmenum.AlarmZoneStateDisarmed)
	if n := h.outputs.stopCount(); n < 1 {
		t.Fatalf("StopAll calls = %d, want >= 1", n)
	}
	if got := h.mustSnapshot("eg").Bypassed; len(got) != 0 {
		t.Fatalf("bypassed after disarm = %v, want empty", got)
	}

	got, ok, err := h.incidents.Get(h.ctx, inc.ID)
	if err != nil || !ok {
		t.Fatalf("get incident: ok=%v err=%v", ok, err)
	}
	if got.CloseReason != "disarm" || !got.Silenced || got.ClosedAtMS == 0 {
		t.Fatalf("incident = %+v, want closed via disarm", got)
	}
}

func TestDisarm_OnDisarmedIsIdempotent(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()
	h.wantState("eg", hmenum.AlarmZoneStateDisarmed)

	before := len(h.journal.events())
	if err := h.eng.Disarm(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("disarm: %v", err)
	}
	after := len(h.journal.events())
	if after != before {
		t.Fatalf("journal grew from %d to %d entries on a no-op disarm", before, after)
	}
	h.wantState("eg", hmenum.AlarmZoneStateDisarmed)
}

func TestAcknowledge_WithIncidentJournalsWithoutStateChange(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	if err := h.eng.Acknowledge(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	if !h.journal.has("acknowledged") {
		t.Fatalf("missing acknowledged journal entry; got %v", h.journal.events())
	}
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
}

func TestAcknowledge_WithoutIncidentReturnsError(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()

	err := h.eng.Acknowledge(h.ctx, "eg", "tester", "test")
	if !errors.Is(err, engine.ErrNoIncident) {
		t.Fatalf("err = %v, want ErrNoIncident", err)
	}
}

func TestOutputs_FireFailureIsJournaledNotFailStop(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()
	h.armFull()

	h.outputs.fireErr = errors.New("driver unavailable")
	h.eng.HandleSensorEvent(h.ctx, "window", true)

	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if !h.journal.has("output_fire_failed") {
		t.Fatalf("missing output_fire_failed journal entry; got %v", h.journal.events())
	}
}

func TestOutputs_StopFailureIsJournaledNotFailStop(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	inc, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("expected an open incident")
	}

	h.outputs.stopErr = errors.New("driver unavailable")
	if err := h.eng.Silence(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("silence: %v", err)
	}
	if !h.journal.has("output_stop_failed") {
		t.Fatalf("missing output_stop_failed journal entry; got %v", h.journal.events())
	}

	got, ok, err := h.incidents.Get(h.ctx, inc.ID)
	if err != nil || !ok || !got.Silenced {
		t.Fatalf("incident = %+v (ok=%v err=%v), want Silenced despite the StopAll failure", got, ok, err)
	}
}
