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
// identity internal/alarm stamps on the same panel.
//
// The discovery builder used to compose it from a string literal —
// "openccu-loom_alarm_" + zone — beside alarmpanel.PanelUniqueID, with the two
// derived motion entities hanging their suffixes off the same literal. An
// entity id spelled out in two places is one rename away from two entities for
// one zone, and Home Assistant would keep both: the old one permanently
// unavailable, the new one without its history.
func TestAlarmPanelIdentityComesFromTheModel(t *testing.T) {
	t.Parallel()
	for _, zone := range []string{"erdgeschoss", alarmMasterZone} {
		want := alarmpanel.PanelUniqueID(zone)
		if want == "" {
			t.Fatalf("%s: the model produced no id — the guard lost its subject", zone)
		}
		item := BuildAlarmPanelDiscovery("loom", zone, "Zone", nil, zone == alarmMasterZone, false, false)
		if !item.OK {
			t.Fatalf("zone %q: no discovery item built", zone)
		}
		var body map[string]any
		if err := json.Unmarshal(item.Payload, &body); err != nil {
			t.Fatalf("zone %q: unmarshal payload: %v", zone, err)
		}
		got, _ := body["unique_id"].(string)
		if got != want {
			t.Errorf("zone %q: panel unique_id = %q, want the model's %q", zone, got, want)
		}
	}
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
