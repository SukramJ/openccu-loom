// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package security

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// zoneIDKnown reports whether snap carries a zone whose ID matches want.
// Snapshot.Zones is keyed by slug rather than ID (aggregate.snapshot in
// aggregate.go), so a lookup has to walk the values.
func zoneIDKnown(t *testing.T, svc *Service, want string) bool {
	t.Helper()
	snap := svc.Snapshot()
	for slug := range snap.Zones {
		if snap.Zones[slug].ID == want {
			return true
		}
	}
	return false
}

// TestServiceRemovesZoneOnAlarmPanelChangedRemoved pins the M3 fix:
// before it, the security domain never subscribed to
// AlarmPanelChangedEvent at all, so a zone entered the aggregate on its
// first AlarmTriggeredEvent and never left — deleting the zone in the
// alarm domain left it standing forever in the security snapshot, which
// meant retractGone on the MQTT plane never saw it disappear and the
// consumer's entity for a deleted zone ghosted indefinitely.
func TestServiceRemovesZoneOnAlarmPanelChangedRemoved(t *testing.T) {
	t.Parallel()
	var alarmBus *events.Bus
	svc, _, clk := newTestService(t, func(d *Deps) { alarmBus = d.AlarmBus })
	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	events.Publish(alarmBus, hmevent.AlarmTriggeredEvent{
		Base: hmevent.NewBaseAt(clk.Now()), ZoneID: "z1", ZoneName: "Erdgeschoss",
		IncidentID: 1, Mode: hmenum.AlarmModeFull,
	})
	if !zoneIDKnown(t, svc, "z1") {
		t.Fatal("zone z1 never entered the snapshot after AlarmTriggeredEvent; the rest of this test would pass vacuously")
	}

	events.Publish(alarmBus, hmevent.AlarmPanelChangedEvent{
		Base: hmevent.NewBaseAt(clk.Now()), ZoneID: "z1", Name: "Erdgeschoss", Removed: true,
	})

	// Bus dispatch runs handlers synchronously in the uncontended case
	// (internal/central/events/bus.go's Publish), but poll on a short
	// deadline rather than asserting immediately: a contended or
	// deferred dispatch would otherwise turn a real gap into a flake
	// instead of a clean failure.
	deadline := time.Now().Add(2 * time.Second)
	for zoneIDKnown(t, svc, "z1") {
		if time.Now().After(deadline) {
			t.Fatal("zone z1 still present in the snapshot after AlarmPanelChangedEvent{Removed:true} — a deleted zone's retained MQTT state and consumer entity would survive the deletion indefinitely")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestServiceIgnoresMasterZoneRemoval locks the companion invariant: the
// master aggregate panel is not a zone and carries no zone view, so no
// AlarmPanelChangedEvent naming alarmpanel.MasterZoneID may touch the
// zone set.
//
// Both directions are driven, because only one of them can actually go
// wrong. Deleting a key that was never inserted is a no-op, so the
// Removed half locks the intent without being able to fail. The create
// half can: the master panel is republished on every panel change, and
// without the guard each one would insert a zone entry keyed "master"
// with a slug derived from its display name — a phantom "Zone Alarm
// system" entity in every consumer, for a zone that does not exist.
func TestServiceIgnoresMasterZoneRemoval(t *testing.T) {
	t.Parallel()
	var alarmBus *events.Bus
	svc, _, clk := newTestService(t, func(d *Deps) { alarmBus = d.AlarmBus })
	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	events.Publish(alarmBus, hmevent.AlarmTriggeredEvent{
		Base: hmevent.NewBaseAt(clk.Now()), ZoneID: "z1", ZoneName: "Erdgeschoss",
		IncidentID: 1, Mode: hmenum.AlarmModeFull,
	})
	if !zoneIDKnown(t, svc, "z1") {
		t.Fatal("zone z1 never entered the snapshot after AlarmTriggeredEvent; the rest of this test would pass vacuously")
	}

	events.Publish(alarmBus, hmevent.AlarmPanelChangedEvent{
		Base: hmevent.NewBaseAt(clk.Now()), ZoneID: alarmpanel.MasterZoneID, Name: "Alarm system", Removed: true,
	})

	if !zoneIDKnown(t, svc, "z1") {
		t.Fatal("zone z1 disappeared after a Removed event naming the master panel — the master aggregate must never be treated as a zone")
	}

	// The half that can fail: an ordinary master-panel update.
	before := len(svc.Snapshot().Zones)
	events.Publish(alarmBus, hmevent.AlarmPanelChangedEvent{
		Base: hmevent.NewBaseAt(clk.Now()), ZoneID: alarmpanel.MasterZoneID,
		Name: "Alarm system", State: string(hmenum.AlarmModeDisarmed),
	})
	if got := len(svc.Snapshot().Zones); got != before {
		t.Fatalf("the zone set grew from %d to %d after a master-panel update — the master "+
			"aggregate was inserted as a zone, which publishes a phantom zone entity for a "+
			"zone that does not exist", before, got)
	}
}
