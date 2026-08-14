// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package security

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// intrusionSinceMS reads the start time the snapshot reports for a
// running intrusion. It fails rather than returns zero when the class is
// missing or inactive, so a test cannot pass on a snapshot that says
// nothing at all.
func intrusionSinceMS(t *testing.T, svc *Service) int64 {
	t.Helper()
	st, ok := svc.Snapshot().Classes[hmenum.SecurityClassIntrusion]
	if !ok {
		t.Fatal("the snapshot carries no intrusion class; the assertion below would be vacuous")
	}
	if !st.Active {
		t.Fatal("the intrusion class is inactive; the assertion below would be vacuous")
	}
	return st.SinceMS
}

// deactivateSource drives an already-registered source back to inactive
// through the real data-point path.
func deactivateSource(t *testing.T, svc *Service, suffix int) {
	t.Helper()
	svc.mu.Lock()
	_, ref := addAggSource(svc.agg, "home", suffix, hmenum.SecurityClassIntrusion, true)
	svc.mu.Unlock()

	svc.onDataPoint("home", hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(),
		Key: hmtypes.DataPointKey{
			InterfaceID:    ref.InterfaceID,
			ChannelAddress: ref.ChannelAddress,
			Parameter:      ref.Parameter,
		},
		NewValue: hmtypes.BoolValue(false),
	})
}

// TestDisarmingAnUninvolvedZoneKeepsTheRunningIntrusionStartTime pins
// the intrusion start time against an unrelated zone transition.
//
// classSince is keyed by class alone, globally across every zone, while
// the zone state machine runs per zone. Deleting the single global entry
// on any transition out of triggered meant that disarming a quiet zone
// wiped the start time of an incident running in a different one: the
// snapshot kept reporting the intrusion active, but with SinceMS 0, so
// every consumer painted a running break-in as having started at the
// Unix epoch.
func TestDisarmingAnUninvolvedZoneKeepsTheRunningIntrusionStartTime(t *testing.T) {
	t.Parallel()
	svc, _, clk := newTestService(t)

	svc.onAlarmTriggered(hmevent.AlarmTriggeredEvent{
		Base:       hmevent.NewBaseAt(clk.Now()),
		ZoneID:     "zone-a",
		ZoneName:   "Erdgeschoss",
		Mode:       hmenum.AlarmModeFull,
		IncidentID: 7,
	})
	fireSource(t, svc, hmenum.SecurityClassIntrusion, 31)
	want := intrusionSinceMS(t, svc)
	if want == 0 {
		t.Fatal("the triggered incident recorded no intrusion start time at all")
	}

	clk.Advance(time.Minute)
	svc.onAlarmStateChanged(hmevent.AlarmStateChangedEvent{
		Base:     hmevent.NewBaseAt(clk.Now()),
		ZoneID:   "zone-b",
		ZoneName: "Keller",
		From:     hmenum.AlarmZoneStateArmed,
		To:       hmenum.AlarmZoneStateDisarmed,
		Mode:     hmenum.AlarmModeDisarmed,
	})

	if got := intrusionSinceMS(t, svc); got != want {
		t.Fatalf("intrusion since_ms = %d after disarming an uninvolved zone, want %d — the "+
			"running incident in zone-a lost its start time", got, want)
	}
}

// TestTheIntrusionStartTimeSurvivesTheZoneItStartedIn locks the other
// half of the same guard: a zone that leaves triggered while its own
// sources are still active keeps the start time, because the class the
// snapshot reports is still active and still the same detection.
func TestTheIntrusionStartTimeSurvivesTheZoneItStartedIn(t *testing.T) {
	t.Parallel()
	svc, _, clk := newTestService(t)

	svc.onAlarmTriggered(hmevent.AlarmTriggeredEvent{
		Base:       hmevent.NewBaseAt(clk.Now()),
		ZoneID:     "zone-a",
		ZoneName:   "Erdgeschoss",
		Mode:       hmenum.AlarmModeFull,
		IncidentID: 7,
	})
	fireSource(t, svc, hmenum.SecurityClassIntrusion, 32)
	want := intrusionSinceMS(t, svc)

	clk.Advance(time.Minute)
	svc.onAlarmStateChanged(hmevent.AlarmStateChangedEvent{
		Base:     hmevent.NewBaseAt(clk.Now()),
		ZoneID:   "zone-a",
		ZoneName: "Erdgeschoss",
		From:     hmenum.AlarmZoneStateTriggered,
		To:       hmenum.AlarmZoneStateDisarmed,
		Mode:     hmenum.AlarmModeDisarmed,
	})

	if got := intrusionSinceMS(t, svc); got != want {
		t.Fatalf("intrusion since_ms = %d after the zone was disarmed while its door is still "+
			"open, want %d", got, want)
	}
}

// TestTheIntrusionStartTimeResetsOnceNothingIsRunning bounds the guard
// in the other direction: with every zone out of triggered and no
// intrusion source active any more, the start time must be released, so
// the next incident reports its own start rather than inheriting the
// previous one.
func TestTheIntrusionStartTimeResetsOnceNothingIsRunning(t *testing.T) {
	t.Parallel()
	svc, _, clk := newTestService(t)

	svc.onAlarmTriggered(hmevent.AlarmTriggeredEvent{
		Base:       hmevent.NewBaseAt(clk.Now()),
		ZoneID:     "zone-a",
		ZoneName:   "Erdgeschoss",
		Mode:       hmenum.AlarmModeFull,
		IncidentID: 7,
	})
	fireSource(t, svc, hmenum.SecurityClassIntrusion, 33)
	first := intrusionSinceMS(t, svc)

	clk.Advance(time.Minute)
	svc.onAlarmStateChanged(hmevent.AlarmStateChangedEvent{
		Base:     hmevent.NewBaseAt(clk.Now()),
		ZoneID:   "zone-a",
		ZoneName: "Erdgeschoss",
		From:     hmenum.AlarmZoneStateTriggered,
		To:       hmenum.AlarmZoneStateDisarmed,
		Mode:     hmenum.AlarmModeDisarmed,
	})
	deactivateSource(t, svc, 33)

	clk.Advance(time.Hour)
	svc.onAlarmTriggered(hmevent.AlarmTriggeredEvent{
		Base:       hmevent.NewBaseAt(clk.Now()),
		ZoneID:     "zone-a",
		ZoneName:   "Erdgeschoss",
		Mode:       hmenum.AlarmModeFull,
		IncidentID: 8,
	})
	fireSource(t, svc, hmenum.SecurityClassIntrusion, 34)

	second := intrusionSinceMS(t, svc)
	if second == first {
		t.Fatalf("the second incident reports since_ms %d, the start time of the first one — a "+
			"finished incident never released it", second)
	}
	if want := nowMS(clk.Now()); second != want {
		t.Fatalf("intrusion since_ms = %d for the second incident, want %d", second, want)
	}
}
