// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine

import (
	"reflect"
	"testing"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// causeFromSensor is the only place a sensor row becomes an incident
// cause, and every identity field it drops is a field the incident's
// source reference, the source ledger and the report lose with it. The
// projection is hand-written field by field, so this pins its coverage:
// given a row whose identity columns are all set, no field of
// incidentCause but Kind — which the caller supplies — may come out
// empty. A field added to incidentCause and left unassigned fails here.
func TestCauseFromSensorFillsEveryIdentityField(t *testing.T) {
	row := sqlitestore.AlarmSensorRow{
		ID:             "sensor-1",
		Name:           "Küchenfenster",
		CentralName:    "ccu-test",
		SensorType:     hmenum.AlarmSensorTypeWindow,
		InterfaceID:    "ccu-test-HmIP-RF",
		ChannelAddress: "0001D3C99ABCDE:1",
		Parameter:      "STATE",
	}

	got := causeFromSensor(causeKindPendingElapsed, row)

	v := reflect.ValueOf(got)
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		if name == "Kind" {
			continue
		}
		if v.Field(i).String() == "" {
			t.Errorf("incidentCause.%s is empty: causeFromSensor does not project it from the row", name)
		}
	}

	// Each field must carry its own column, not a neighbour's.
	if got.Kind != causeKindPendingElapsed {
		t.Errorf("Kind = %q, want %q", got.Kind, causeKindPendingElapsed)
	}
	if got.SensorID != row.ID {
		t.Errorf("SensorID = %q, want %q", got.SensorID, row.ID)
	}
	if got.SensorName != row.Name {
		t.Errorf("SensorName = %q, want %q", got.SensorName, row.Name)
	}
	if got.Central != row.CentralName {
		t.Errorf("Central = %q, want %q", got.Central, row.CentralName)
	}
	if got.SensorType != string(row.SensorType) {
		t.Errorf("SensorType = %q, want %q", got.SensorType, row.SensorType)
	}
	if got.InterfaceID != row.InterfaceID {
		t.Errorf("InterfaceID = %q, want %q", got.InterfaceID, row.InterfaceID)
	}
	if got.ChannelAddress != row.ChannelAddress {
		t.Errorf("ChannelAddress = %q, want %q", got.ChannelAddress, row.ChannelAddress)
	}
	if got.Parameter != row.Parameter {
		t.Errorf("Parameter = %q, want %q", got.Parameter, row.Parameter)
	}
}
