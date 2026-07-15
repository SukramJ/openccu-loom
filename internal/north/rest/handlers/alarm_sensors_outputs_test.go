// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func jsonRequestBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return bytes.NewReader(b)
}

// --- ListAlarmAreaSensors / PutAlarmAreaSensors ---

func TestListAlarmAreaSensors_UnknownArea_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/areas/missing/sensors", http.NoBody)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	ListAlarmAreaSensors(fx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestPutAlarmAreaSensors_UnknownArea_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	body := jsonRequestBody(t, []hmapi.AlarmSensor{})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/areas/missing/sensors", body)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	PutAlarmAreaSensors(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// TestPutAlarmAreaSensors_ReplaceSemantics verifies PUT performs a full
// replace: the two pre-seeded sensors vanish and only the sensors in the
// request body remain enrolled afterwards.
func TestPutAlarmAreaSensors_ReplaceSemantics(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedArea("eg", "Erdgeschoss", fullModeAreaConfig(0, 0, 60))
	fx.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	fx.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	rec := &captureRecorder{}

	replacement := []hmapi.AlarmSensor{
		{
			ID:             "motion",
			Central:        alarmFixtureCentral,
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "motion:1",
			Parameter:      "STATE",
			Type:           string(hmenum.AlarmSensorTypeMotion),
			Name:           "Motion",
		},
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/areas/eg/sensors", jsonRequestBody(t, replacement))
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmAreaSensors(fx, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}

	rows, err := fx.stores.Sensors.ListByArea(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list sensors: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "motion" {
		t.Fatalf("sensors after replace = %+v, want exactly [motion]", rows)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmConfigChange {
		t.Fatalf("audit entries = %+v, want one alarm_config_change", rec.entries)
	}
}

// TestPutAlarmAreaSensors_EmptyBody_ClearsAllSensors verifies an empty
// array body removes every previously enrolled sensor.
func TestPutAlarmAreaSensors_EmptyBody_ClearsAllSensors(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedArea("eg", "Erdgeschoss", fullModeAreaConfig(0, 0, 60))
	fx.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/areas/eg/sensors", jsonRequestBody(t, []hmapi.AlarmSensor{}))
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmAreaSensors(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	rows, err := fx.stores.Sensors.ListByArea(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list sensors: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("sensors after clear = %+v, want none", rows)
	}
}

// --- ListAlarmAreaOutputs / PutAlarmAreaOutputs ---

func TestListAlarmAreaOutputs_UnknownArea_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/areas/missing/outputs", http.NoBody)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	ListAlarmAreaOutputs(fx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestPutAlarmAreaOutputs_UnknownArea_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/areas/missing/outputs", jsonRequestBody(t, []hmapi.AlarmOutput{}))
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	PutAlarmAreaOutputs(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// TestPutAlarmAreaOutputs_ReplaceSemantics verifies PUT performs a full
// replace of the output set, mirroring the sensors endpoint's semantics.
func TestPutAlarmAreaOutputs_ReplaceSemantics(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedArea("eg", "Erdgeschoss", fullModeAreaConfig(0, 0, 60))
	fx.seedOutput("sirenA", "eg", hmenum.AlarmOutputClassAcousticSiren, alarmOutputConfigFixture())
	rec := &captureRecorder{}

	replacement := []hmapi.AlarmOutput{
		{ID: "light", Class: string(hmenum.AlarmOutputClassAlarmLight), Central: alarmFixtureCentral, ChannelAddress: "light:1", Name: "Light"},
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/areas/eg/outputs", jsonRequestBody(t, replacement))
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmAreaOutputs(fx, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	rows, err := fx.stores.Outputs.ListByArea(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list outputs: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "light" {
		t.Fatalf("outputs after replace = %+v, want exactly [light]", rows)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmConfigChange {
		t.Fatalf("audit entries = %+v, want one alarm_config_change", rec.entries)
	}
}

func TestPutAlarmAreaOutputs_InvalidClass_Returns422(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedArea("eg", "Erdgeschoss", fullModeAreaConfig(0, 0, 60))

	bad := []hmapi.AlarmOutput{{ID: "x", Class: "not-a-real-class", Central: alarmFixtureCentral, ChannelAddress: "x:1"}}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/areas/eg/outputs", jsonRequestBody(t, bad))
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmAreaOutputs(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
	}
}
