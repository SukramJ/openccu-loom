// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"
)

// TestAlarmPanelIdentityComesFromTheModel pins the MQTT panel entity to the
// identity internal/alarm stamps on the same panel — and pins the one
// deliberate divergence from it.
//
// The discovery builder used to compose it from a string literal —
// "openccu-loom_alarm_" + zone — beside alarmpanel.PanelUniqueID, with the two
// derived motion entities hanging their suffixes off the same literal. An
// entity id spelled out in two places is one rename away from two entities for
// one zone, and Home Assistant would keep both: the old one permanently
// unavailable, the new one without its history.
//
// # The divergence, and why it is not the literal coming back
//
// On a NON-default topic base the published `unique_id` is the model's id
// with that base's scope in front. The model's id answers "which panel is
// this, inside this daemon"; the published one has to answer "which panel is
// this, in a Home Assistant that two daemons publish into" — and
// `openccu-loom_alarm_master` is the same string in both of them, since the
// master zone exists on every daemon by default. HA rejects the second
// declaration of a unique id outright, so without the scope the second
// daemon's panels simply never appear.
//
// The default base is unchanged, and that is the load-bearing half of this
// test: a single-daemon installation must see nothing move, because re-keying
// an alarm panel costs its history, its entity id and every automation that
// arms it.
func TestAlarmPanelIdentityComesFromTheModel(t *testing.T) {
	t.Parallel()
	for _, zone := range []string{"erdgeschoss", alarmMasterZone} {
		model := alarmpanel.PanelUniqueID(zone)
		if model == "" {
			t.Fatalf("%s: the model produced no id — the guard lost its subject", zone)
		}

		// Default base: byte-for-byte the model's id, as published today.
		def := alarmPanelUniqueID(t, "openccu-loom", zone)
		if def != model {
			t.Errorf("zone %q on the DEFAULT topic base: panel unique_id = %q, want the model's %q — "+
				"every existing installation would lose the panel's history and every automation "+
				"that arms it", zone, def, model)
		}

		// Non-default base: the model's id, scoped, so a sibling daemon's
		// panel is a different entity rather than a rejected duplicate.
		scoped := alarmPanelUniqueID(t, "haus", zone)
		if want := "haus_" + model; scoped != want {
			t.Errorf("zone %q on a non-default topic base: panel unique_id = %q, want %q — two daemons "+
				"both declare the model's id and Home Assistant keeps only whichever arrived first",
				zone, scoped, want)
		}
	}
}

// alarmPanelUniqueID renders one zone's panel on the given topic base and
// reads the published unique_id back out of the payload.
func alarmPanelUniqueID(t *testing.T, base, zone string) string {
	t.Helper()
	item := BuildAlarmPanelDiscovery(base, zone, "Zone", nil, zone == alarmMasterZone, false, false)
	if !item.OK {
		t.Fatalf("zone %q: no discovery item built", zone)
	}
	var body map[string]any
	if err := json.Unmarshal(item.Payload, &body); err != nil {
		t.Fatalf("zone %q: unmarshal payload: %v", zone, err)
	}
	uid, _ := body["unique_id"].(string)
	return uid
}

// TestAlarmMasterZoneTokenIsTheModels keeps the pseudo-zone token equal to the
// model's, since it is part of the aggregate panel's entity id.
func TestAlarmMasterZoneTokenIsTheModels(t *testing.T) {
	t.Parallel()
	if alarmMasterZone != alarmpanel.MasterZoneID {
		t.Errorf("master zone token = %q, the model says %q", alarmMasterZone, alarmpanel.MasterZoneID)
	}
	if !strings.Contains(alarmpanel.PanelUniqueID(alarmMasterZone), alarmMasterZone) {
		t.Error("the master panel id no longer carries the master token")
	}
}
