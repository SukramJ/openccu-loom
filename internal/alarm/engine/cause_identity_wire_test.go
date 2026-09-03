// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// persistedCause is the identity half of the stored incident-cause
// document, as the report and the source ledger read it.
type persistedCause struct {
	Kind           string `json:"kind"`
	SensorID       string `json:"sensor_id"`
	Central        string `json:"central"`
	InterfaceID    string `json:"interface_id"`
	ChannelAddress string `json:"channel_address"`
	Parameter      string `json:"parameter"`
}

// The identity the cause projection carries has to survive all the way
// into the persisted incident: a cause without a channel address
// projects onto an empty source reference, which the incident's source
// list drops, and the report can then no longer say which data point
// fired.
func TestIncidentCauseCarriesTheFiringDataPointIdentity(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.seedSensor("hazard-1", "eg", hmenum.AlarmSensorTypeHazard, engine.SensorConfig{AlwaysOn: true})
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "hazard-1", true)
	h.wantState("eg", hmenum.AlarmZoneStateTriggered)

	inc, ok := h.openIncident("eg")
	if !ok {
		t.Fatal("expected an open incident")
	}
	var cause persistedCause
	if err := jsonUnmarshal(inc.CauseJSON, &cause); err != nil {
		t.Fatalf("unmarshal cause: %v", err)
	}
	if cause.SensorID != "hazard-1" {
		t.Errorf("sensor_id = %q, want hazard-1", cause.SensorID)
	}
	if cause.Central != "ccu-test" {
		t.Errorf("central = %q, want ccu-test", cause.Central)
	}
	if cause.InterfaceID != "HmIP-RF" {
		t.Errorf("interface_id = %q, want HmIP-RF", cause.InterfaceID)
	}
	if cause.ChannelAddress != "hazard-1:1" {
		t.Errorf("channel_address = %q, want hazard-1:1", cause.ChannelAddress)
	}
	if cause.Parameter != "STATE" {
		t.Errorf("parameter = %q, want STATE", cause.Parameter)
	}
}
