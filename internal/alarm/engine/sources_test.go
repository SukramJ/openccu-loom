// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// This file covers the per-incident source ledger (sources.go): the
// in-memory accumulator that lets a zone still triggered by one sensor
// record a second (and later) contributing sensor, the durable ledger
// write that accompanies it, and the readiness Details/Blockers
// asymmetry that motivated AlarmBlockerDetail in the first place.

package engine_test

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// fakeSourceLedger records every Append call so a test can assert what
// reached the durable half without a real database.
type fakeSourceLedger struct {
	mu   sync.Mutex
	rows []sqlitestore.AlarmIncidentSource
}

func (l *fakeSourceLedger) Append(_ context.Context, row sqlitestore.AlarmIncidentSource) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rows = append(l.rows, row)
	return nil
}

func (l *fakeSourceLedger) refs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.rows))
	for i := range l.rows {
		out[i] = l.rows[i].Ref
	}
	return out
}

func (l *fakeSourceLedger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.rows)
}

// startWithLedger builds and starts an engine on the harness's existing
// stores and fakes, additionally wiring ledger as the source ledger.
// harness.build (harness_test.go) never sets Deps.SourceLedger, so every
// other engine test already exercises the nil-ledger path; this helper
// is the one addition needed to exercise the durable-write half.
func (h *harness) startWithLedger(ledger engine.IncidentSourceLedger) {
	h.t.Helper()
	eng, err := engine.New(engine.Deps{
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
		SourceLedger: ledger,
	})
	if err != nil {
		h.t.Fatalf("engine.New: %v", err)
	}
	h.eng = eng
	if err := h.eng.Start(h.ctx); err != nil {
		h.t.Fatalf("engine.Start: %v", err)
	}
}

// refFor mirrors the routing key the harness's seeded sensors carry
// (central "ccu-test", interface "HmIP-RF", channel "<id>:1", parameter
// "STATE" — see harness_test.go's seedSensor).
func refFor(sensorID string) string {
	return hmevent.SecurityRefKey("ccu-test", "HmIP-RF", sensorID+":1", "STATE")
}

// TestSources_SecondSensorWhileTriggeredAccumulatesAndRepublishes is the
// headline regression case: before recordSource/publishSourcesChanged
// existed, a second detector activating while the zone was already
// triggered left no trace anywhere — the state machine's trigger()
// short-circuits on an already-triggered zone (engine.go's trigger),
// and nothing else recorded the activation's identity.
func TestSources_SecondSensorWhileTriggeredAccumulatesAndRepublishes(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	ledger := &fakeSourceLedger{}
	h.startWithLedger(ledger)
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	inc, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("expected an open incident after the first trigger")
	}

	h.eng.HandleSensorEvent(h.ctx, "motion", true)

	// (a) the zone stays triggered and no second incident is created.
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	incAfter, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("expected the incident to still be open")
	}
	if incAfter.ID != inc.ID {
		t.Fatalf("incident ID changed from %d to %d, want unchanged (no second incident)", inc.ID, incAfter.ID)
	}
	all, err := h.incidents.ListByZone(h.ctx, "eg", 0)
	if err != nil {
		t.Fatalf("ListByZone: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ListByZone len=%d want 1 (exactly one incident total)", len(all))
	}

	// (b) IncidentSources returns BOTH sources, oldest first.
	sources := h.eng.IncidentSources("eg")
	if len(sources) != 2 {
		t.Fatalf("IncidentSources len=%d want 2: %+v", len(sources), sources)
	}
	if sources[0].Ref != refFor("window") {
		t.Errorf("sources[0].Ref = %q, want %q", sources[0].Ref, refFor("window"))
	}
	if sources[1].Ref != refFor("motion") {
		t.Errorf("sources[1].Ref = %q, want %q", sources[1].Ref, refFor("motion"))
	}

	// (c) a further AlarmTriggeredEvent was published whose Sources has
	// both, while the headline SensorID stays the one that opened the
	// incident.
	triggered := h.sink.triggered()
	if len(triggered) != 2 {
		t.Fatalf("triggered events count = %d, want 2 (initial trigger + sources-changed republish)", len(triggered))
	}
	first, second := triggered[0], triggered[1]
	if len(first.Sources) != 1 || first.Sources[0].Ref != refFor("window") {
		t.Errorf("first triggered event Sources = %+v, want exactly [window]", first.Sources)
	}
	if second.SensorID != "window" {
		t.Errorf("second triggered event SensorID = %q, want window (headline sensor unchanged)", second.SensorID)
	}
	if len(second.Sources) != 2 || second.Sources[0].Ref != refFor("window") || second.Sources[1].Ref != refFor("motion") {
		t.Errorf("second triggered event Sources = %+v, want [window, motion]", second.Sources)
	}

	// (d) the durable ledger received both.
	if got := ledger.refs(); len(got) != 2 || got[0] != refFor("window") || got[1] != refFor("motion") {
		t.Errorf("ledger refs = %v, want [%q, %q]", got, refFor("window"), refFor("motion"))
	}
}

// TestSources_ReactivatingSameSensorPublishesNothingFurther verifies that
// re-activating a sensor already recorded for the running incident is a
// pure no-op on the source side: no new event, no new ledger row —
// dedup by ref.
func TestSources_ReactivatingSameSensorPublishesNothingFurther(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	ledger := &fakeSourceLedger{}
	h.startWithLedger(ledger)
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	triggeredBefore := len(h.sink.triggered())
	ledgerCountBefore := ledger.count()
	sourcesBefore := h.eng.IncidentSources("eg")

	// Clear then re-activate the same sensor while still triggered.
	h.eng.HandleSensorEvent(h.ctx, "window", false)
	h.eng.HandleSensorEvent(h.ctx, "window", true)

	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if got := len(h.sink.triggered()); got != triggeredBefore {
		t.Errorf("triggered event count = %d, want unchanged %d (re-activation must not republish)", got, triggeredBefore)
	}
	if got := ledger.count(); got != ledgerCountBefore {
		t.Errorf("ledger row count = %d, want unchanged %d (re-activation must not append)", got, ledgerCountBefore)
	}
	sourcesAfter := h.eng.IncidentSources("eg")
	if len(sourcesAfter) != len(sourcesBefore) {
		t.Errorf("IncidentSources len=%d, want unchanged %d", len(sourcesAfter), len(sourcesBefore))
	}
}

// TestSources_CloseIncidentResetsAccumulator verifies that closing an
// incident (via Disarm) empties the in-memory source list, and that the
// next incident starts fresh rather than inheriting the previous one's
// sources.
func TestSources_CloseIncidentResetsAccumulator(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	ledger := &fakeSourceLedger{}
	h.startWithLedger(ledger)
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if got := h.eng.IncidentSources("eg"); len(got) != 1 {
		t.Fatalf("IncidentSources before disarm len=%d want 1: %+v", len(got), got)
	}

	if err := h.eng.Disarm(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("disarm: %v", err)
	}
	h.wantState("eg", hmenum.AlarmZoneStateDisarmed)
	if got := h.eng.IncidentSources("eg"); len(got) != 0 {
		t.Errorf("IncidentSources after disarm = %+v, want empty", got)
	}

	// Close the still-open window before re-arming, otherwise it blocks
	// the arm as an open-contact blocker — irrelevant to what this test
	// checks (the source accumulator, not readiness).
	h.eng.HandleSensorEvent(h.ctx, "window", false)

	// A fresh incident must not inherit the closed incident's sources.
	h.armFull()
	h.eng.HandleSensorEvent(h.ctx, "motion", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	got := h.eng.IncidentSources("eg")
	if len(got) != 1 {
		t.Fatalf("IncidentSources for the new incident len=%d want 1: %+v", len(got), got)
	}
	if got[0].Ref != refFor("motion") {
		t.Errorf("new incident source Ref = %q, want %q (must not carry over window)", got[0].Ref, refFor("motion"))
	}
}

// TestSources_CentralLossCauseRecordsNoSourceAndDoesNotPanic verifies
// that a cause without a channel address — a central-loss trigger — is
// accepted by the state machine (the incident still opens) but records
// no source, since incidentCause.sourceRef returns an empty reference
// for it and recordSource drops an empty reference rather than
// panicking on it.
func TestSources_CentralLossCauseRecordsNoSourceAndDoesNotPanic(t *testing.T) {
	h := newHarness(t)
	cfg := defaultZoneConfig()
	cfg.CentralLoss = hmenum.AlarmCentralLossTrigger
	h.seedZone("eg", "Erdgeschoss", cfg)
	h.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	ledger := &fakeSourceLedger{}
	h.startWithLedger(ledger)
	h.armFull()

	h.eng.HandleCentralConnectivity(h.ctx, "ccu-test", false)

	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	if got := h.eng.IncidentSources("eg"); len(got) != 0 {
		t.Errorf("IncidentSources = %+v, want empty for a central-loss cause", got)
	}
	if got := ledger.count(); got != 0 {
		t.Errorf("ledger row count = %d, want 0", got)
	}
	triggered := h.sink.triggered()
	if len(triggered) != 1 {
		t.Fatalf("triggered events count = %d, want 1", len(triggered))
	}
	if len(triggered[0].Sources) != 0 {
		t.Errorf("triggered[0].Sources = %+v, want empty", triggered[0].Sources)
	}
}

// TestSources_NilSourceLedgerStillAccumulatesAndPublishes verifies that
// the durable half is strictly optional: with Deps.SourceLedger left
// nil (the default every other engine test in this package runs with —
// see harness_test.go's build), the in-memory accumulator still
// collects every source and the engine still publishes the full list.
// A missing ledger must never gate an alarm.
func TestSources_NilSourceLedgerStillAccumulatesAndPublishes(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start() // harness.build wires no SourceLedger: ledger is nil.
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)
	h.eng.HandleSensorEvent(h.ctx, "motion", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	sources := h.eng.IncidentSources("eg")
	if len(sources) != 2 {
		t.Fatalf("IncidentSources len=%d want 2: %+v", len(sources), sources)
	}

	triggered := h.sink.triggered()
	if len(triggered) != 2 {
		t.Fatalf("triggered events count = %d, want 2", len(triggered))
	}
	if len(triggered[1].Sources) != 2 {
		t.Errorf("last triggered event Sources = %+v, want 2 entries despite a nil ledger", triggered[1].Sources)
	}
}

// TestReadiness_SensorBothUnreachableAndLowBatteryDetailsVsBlockers
// verifies the asymmetry that motivated AlarmBlockerDetail: a sensor
// that is both unreachable and low on battery produces TWO Details
// entries with distinct reasons, while the flat Blockers list
// deduplicates the same sensor down to one entry and loses which
// reasons applied.
func TestReadiness_SensorBothUnreachableAndLowBatteryDetailsVsBlockers(t *testing.T) {
	h := newHarness(t)
	cfg := defaultZoneConfig()
	// Override LowBattery to Block (default is Warn) so both health
	// classes classify as blocking and land in Blockers — the scenario
	// where the flat list's deduplication actually loses information.
	cfg.Blockers.LowBattery = hmenum.AlarmBlockerPolicyBlock
	h.seedZone("eg", "Erdgeschoss", cfg)
	h.seedSensor("motion", "eg", hmenum.AlarmSensorTypeMotion, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	h.start()

	h.eng.SetSensorAvailability(h.ctx, "motion", false)
	h.eng.SetSensorHealth(h.ctx, "motion", engine.SensorHealth{LowBattery: true})

	snap := h.mustSnapshot("eg")
	rd, ok := snap.Readiness[hmenum.AlarmModeFull]
	if !ok {
		t.Fatal("no readiness verdict for full mode")
	}

	if len(rd.Blockers) != 1 || rd.Blockers[0] != "motion" {
		t.Errorf("Blockers = %v, want exactly [motion] (deduplicated)", rd.Blockers)
	}

	var reasons []hmevent.AlarmBlockerReason
	for _, d := range rd.Details {
		if d.SensorID != "motion" {
			t.Errorf("Details entry for unexpected sensor %q", d.SensorID)
			continue
		}
		if !d.Blocking {
			t.Errorf("Details entry reason=%q Blocking=false, want true", d.Reason)
		}
		reasons = append(reasons, d.Reason)
	}
	if len(rd.Details) != 2 {
		t.Fatalf("Details len=%d want 2 (one per reason): %+v", len(rd.Details), rd.Details)
	}
	wantUnreachable, wantLowBattery := false, false
	for _, r := range reasons {
		switch r {
		case hmevent.AlarmBlockerReasonUnreachable:
			wantUnreachable = true
		case hmevent.AlarmBlockerReasonLowBattery:
			wantLowBattery = true
		default:
			t.Errorf("unexpected blocker reason %q", r)
		}
	}
	if !wantUnreachable || !wantLowBattery {
		t.Errorf("Details reasons = %v, want both unreachable and low_battery", reasons)
	}
}
