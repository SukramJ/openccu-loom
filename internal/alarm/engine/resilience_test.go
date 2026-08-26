// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// flakyIncidentStore fails MarkSilenced while delegating everything
// else to the real store.
type flakyIncidentStore struct {
	*sqlitestore.AlarmIncidentStore
	failMarkSilenced bool
}

func (f *flakyIncidentStore) MarkSilenced(ctx context.Context, id, atMS int64, by string) error {
	if f.failMarkSilenced {
		return errors.New("simulated write failure")
	}
	return f.AlarmIncidentStore.MarkSilenced(ctx, id, atMS, by)
}

// flakyStateStore fails Get for one zone ID.
type flakyStateStore struct {
	*sqlitestore.AlarmStateStore
	failGetID string
}

func (f *flakyStateStore) Get(ctx context.Context, zoneID string) (sqlitestore.AlarmStateRow, bool, error) {
	if zoneID == f.failGetID {
		return sqlitestore.AlarmStateRow{}, false, errors.New("simulated read failure")
	}
	return f.AlarmStateStore.Get(ctx, zoneID)
}

// buildWith constructs and starts an engine on the harness's ports
// with substituted store dependencies.
func (h *harness) buildWith(t *testing.T, deps func(*engine.Deps)) {
	t.Helper()
	d := engine.Deps{
		Clock:        h.clk,
		Scheduler:    h.sched,
		Zones:        h.zones,
		Sensors:      h.sensors,
		State:        h.states,
		Incidents:    h.incidents,
		Runtime:      h.runtime,
		Outputs:      h.outputs,
		Sink:         h.sink,
		Journal:      h.journal,
		SensorReader: h.reader,
	}
	deps(&d)
	eng, err := engine.New(d)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	h.eng = eng
}

func TestSilence_SurvivesRestartWhenIncidentWriteFails(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	flaky := &flakyIncidentStore{AlarmIncidentStore: h.incidents, failMarkSilenced: true}
	h.buildWith(t, func(d *engine.Deps) { d.Incidents = flaky })
	if err := h.eng.Start(h.ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	// The silence lands in memory and on the state row, but the
	// incident write fails — journaled, never fatal.
	if err := h.eng.Silence(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("silence: %v", err)
	}
	if !h.journal.has("silence_persist_failed") {
		t.Fatalf("missing persist-failure journal entry; got %v", h.journal.events())
	}
	inc, ok := h.openIncident("eg")
	if !ok || inc.Silenced {
		t.Fatalf("precondition broken: incident row silenced=%v ok=%v (want unsilenced open row)", inc.Silenced, ok)
	}

	// Restart inside the trigger window with a healthy store: the
	// redundant state-row marker must keep the incident silent (S3)
	// and heal the incident row.
	h.restart(10 * time.Second)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("silenced incident re-fired after restart despite marker: %d", n)
	}
	if !h.journal.has("silenced_incident_restored") {
		t.Fatalf("missing silenced-restore journal entry; got %v", h.journal.events())
	}
	inc, ok = h.openIncident("eg")
	if !ok || !inc.Silenced {
		t.Fatalf("incident row not healed: silenced=%v ok=%v", inc.Silenced, ok)
	}
}

func TestStart_PartialFailureCancelsScheduledTimers(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone() // zone "eg"
	h.seedZone("zz", "Zweitbereich", defaultZoneConfig())
	h.start()

	// Leave "eg" mid exit delay so its restore schedules a countdown.
	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.eng.Stop(h.ctx)

	// Second engine: "eg" (sorted first) restores and schedules its
	// timer, then "zz" fails to load — Start must return the error
	// AND cancel the already-scheduled countdown.
	h.freshPorts(h.clk.Now().Add(5 * time.Second))
	h.buildWith(t, func(d *engine.Deps) {
		d.State = &flakyStateStore{AlarmStateStore: h.states, failGetID: "zz"}
	})
	if err := h.eng.Start(h.ctx); err == nil {
		t.Fatal("Start should fail when a state row cannot be loaded")
	}

	// Nothing may fire on the dead engine: no arm completion, no
	// state events, no outputs.
	h.advance(time.Hour)
	if h.journal.has("armed") {
		t.Fatalf("failed Start still completed an arm; journal %v", h.journal.events())
	}
	if n := len(h.sink.stateChanges()); n != 0 {
		t.Fatalf("failed Start published %d state changes", n)
	}
	if n := h.outputs.fireCount(); n != 0 {
		t.Fatalf("failed Start fired outputs: %d", n)
	}
}
