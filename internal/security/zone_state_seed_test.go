// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package security

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestZoneAdoptsTheEngineStateFromThePanelProjection pins the fix for a
// zone that reported no state at all.
//
// The seeding paths — the store refresh and the panel handler — set
// identity only, and the state writers fire on transitions. A daemon
// restarted next to a quiet alarm system therefore held every zone with
// an empty state, and the Config UI rendered the bare translation key
// `alarm.state.` where the arm state belongs.
func TestZoneAdoptsTheEngineStateFromThePanelProjection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		token string
		want  hmenum.AlarmZoneState
	}{
		{name: "disarmed", token: "disarmed", want: hmenum.AlarmZoneStateDisarmed},
		{name: "arming", token: "arming", want: hmenum.AlarmZoneStateArming},
		{name: "pending", token: "pending", want: hmenum.AlarmZoneStatePending},
		{name: "triggered", token: "triggered", want: hmenum.AlarmZoneStateTriggered},
		// Every armed variant collapses onto armed: the protection mode
		// the token also encodes travels in its own field.
		{name: "armed_away", token: "armed_away", want: hmenum.AlarmZoneStateArmed},
		{name: "armed_home", token: "armed_home", want: hmenum.AlarmZoneStateArmed},
		{name: "armed_night", token: "armed_night", want: hmenum.AlarmZoneStateArmed},
		{name: "armed_vacation", token: "armed_vacation", want: hmenum.AlarmZoneStateArmed},
		{name: "armed_custom_bypass", token: "armed_custom_bypass", want: hmenum.AlarmZoneStateArmed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, _, _ := newTestService(t)

			svc.onAlarmPanelChanged(hmevent.AlarmPanelChangedEvent{
				Base:   hmevent.NewBase(),
				ZoneID: "z1",
				Name:   "Erdgeschoss",
				State:  tc.token,
			})

			if got := zoneStateByID(t, svc, "z1"); got != tc.want {
				t.Fatalf("zone state = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestZoneKeepsItsStateOnAnUnknownToken pins the refusal to guess. A
// token this build does not know leaves the state as it was rather than
// overwriting it with a default — on a security surface an invented
// "disarmed" is worse than an admitted gap.
func TestZoneKeepsItsStateOnAnUnknownToken(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)

	svc.onAlarmPanelChanged(hmevent.AlarmPanelChangedEvent{
		Base: hmevent.NewBase(), ZoneID: "z1", Name: "Erdgeschoss", State: "armed_away",
	})
	svc.onAlarmPanelChanged(hmevent.AlarmPanelChangedEvent{
		Base: hmevent.NewBase(), ZoneID: "z1", Name: "Erdgeschoss", State: "teleported",
	})

	if got := zoneStateByID(t, svc, "z1"); got != hmenum.AlarmZoneStateArmed {
		t.Fatalf("zone state = %q, want the previous armed state retained", got)
	}
}

// zoneStateByID reads one zone's arm state out of a snapshot.
// Snapshot.Zones is keyed by slug, so the lookup walks the values.
func zoneStateByID(t *testing.T, svc *Service, id string) hmenum.AlarmZoneState {
	t.Helper()
	snap := svc.Snapshot()
	for slug := range snap.Zones {
		if snap.Zones[slug].ID == id {
			return snap.Zones[slug].State
		}
	}
	t.Fatalf("zone %s missing from the snapshot", id)
	return ""
}
