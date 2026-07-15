// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// marshalAreaConfig serializes an engine.AreaConfig into the raw JSON
// document the AlarmArea.Config wire field carries verbatim.
func marshalAreaConfig(t *testing.T, cfg engine.AreaConfig) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal area config: %v", err)
	}
	return b
}

func alarmAreaRequestBody(t *testing.T, area hmapi.AlarmArea) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(area)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return bytes.NewReader(b)
}

// --- ListAlarmAreas / GetAlarmArea ---

func TestListAlarmAreas_Empty(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/areas", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmAreas(fx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var body []hmapi.AlarmArea
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("areas = %+v, want empty", body)
	}
}

func TestListAlarmAreas_ReturnsSeededAreas(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedArea("eg", "Erdgeschoss", fullModeAreaConfig(30, 15, 60))
	fx.seedArea("og", "Obergeschoss", fullModeAreaConfig(0, 0, 60))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/areas", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmAreas(fx).ServeHTTP(w, req)

	var body []hmapi.AlarmArea
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("areas = %+v, want 2", body)
	}
}

func TestGetAlarmArea_UnknownID_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/areas/missing", http.NoBody)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	GetAlarmArea(fx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestGetAlarmArea_ReturnsSeeded(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedArea("eg", "Erdgeschoss", fullModeAreaConfig(30, 15, 60))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/areas/eg", http.NoBody)
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	GetAlarmArea(fx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var body hmapi.AlarmArea
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.ID != "eg" || body.Name != "Erdgeschoss" {
		t.Errorf("area = %+v, want id=eg name=Erdgeschoss", body)
	}
}

// --- CreateAlarmArea ---

func TestCreateAlarmArea_HappyPath_Returns201AndRecordsAudit(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	rec := &captureRecorder{}

	body := alarmAreaRequestBody(t, hmapi.AlarmArea{
		Name:   "Erdgeschoss",
		Config: marshalAreaConfig(t, fullModeAreaConfig(30, 15, 60)),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/areas", body)
	w := httptest.NewRecorder()
	CreateAlarmArea(fx, rec).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created hmapi.AlarmArea
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.ID == "" {
		t.Error("created area has no server-generated id")
	}
	if created.Name != "Erdgeschoss" {
		t.Errorf("name = %q, want Erdgeschoss", created.Name)
	}
	// The new area must be immediately visible through the engine —
	// CreateAlarmArea reloads before responding.
	if _, ok := fx.eng.Area(created.ID); !ok {
		t.Error("created area not visible through the engine after Reload")
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmConfigChange {
		t.Fatalf("audit entries = %+v, want one alarm_config_change", rec.entries)
	}
}

func TestCreateAlarmArea_InvalidConfig_Returns422(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	body := alarmAreaRequestBody(t, hmapi.AlarmArea{
		Name:   "Bad",
		Config: json.RawMessage(`{"modes":123}`),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/areas", body)
	w := httptest.NewRecorder()
	CreateAlarmArea(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
	}
}

func TestCreateAlarmArea_MalformedJSON_Returns400(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/areas", strings.NewReader("{not-json"))
	w := httptest.NewRecorder()
	CreateAlarmArea(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// --- PutAlarmArea ---

func TestPutAlarmArea_UnknownID_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	body := alarmAreaRequestBody(t, hmapi.AlarmArea{Name: "x", Config: marshalAreaConfig(t, fullModeAreaConfig(0, 0, 60))})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/areas/missing", body)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	PutAlarmArea(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestPutAlarmArea_HappyPath_Returns204AndPersists(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedArea("eg", "Erdgeschoss", fullModeAreaConfig(30, 15, 60))
	rec := &captureRecorder{}

	newBody := alarmAreaRequestBody(t, hmapi.AlarmArea{
		Name:   "Erdgeschoss Renamed",
		Config: marshalAreaConfig(t, fullModeAreaConfig(45, 20, 90)),
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/areas/eg", newBody)
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmArea(fx, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	row, ok, err := fx.stores.Areas.Get(context.Background(), "eg")
	if err != nil || !ok {
		t.Fatalf("get area after put: ok=%v err=%v", ok, err)
	}
	if row.Name != "Erdgeschoss Renamed" {
		t.Errorf("name = %q, want Erdgeschoss Renamed", row.Name)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmConfigChange {
		t.Fatalf("audit entries = %+v, want one alarm_config_change", rec.entries)
	}
}

// --- DeleteAlarmArea ---

func TestDeleteAlarmArea_UnknownID_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/alarm/areas/missing", http.NoBody)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	DeleteAlarmArea(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteAlarmArea_WhileArmed_Returns409(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedArea("eg", "Erdgeschoss", fullModeAreaConfig(0, 0, 60))
	if _, err := fx.eng.Arm(context.Background(), "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, SkipDelay: true}); err != nil {
		t.Fatalf("arm: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/alarm/areas/eg", http.NoBody)
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	DeleteAlarmArea(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteAlarmArea_WhileDisarmed_Returns204AndCascadesSensorsOutputs(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedArea("eg", "Erdgeschoss", fullModeAreaConfig(0, 0, 60))
	fx.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	fx.seedOutput("siren", "eg", hmenum.AlarmOutputClassAcousticSiren, alarmOutputConfigFixture())
	rec := &captureRecorder{}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/alarm/areas/eg", http.NoBody)
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	DeleteAlarmArea(fx, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	if _, ok, _ := fx.stores.Areas.Get(context.Background(), "eg"); ok {
		t.Error("area row still present after delete")
	}
	sensors, err := fx.stores.Sensors.ListByArea(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list sensors: %v", err)
	}
	if len(sensors) != 0 {
		t.Errorf("sensors = %+v, want cascaded delete", sensors)
	}
	outs, err := fx.stores.Outputs.ListByArea(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list outputs: %v", err)
	}
	if len(outs) != 0 {
		t.Errorf("outputs = %+v, want cascaded delete", outs)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmConfigChange {
		t.Fatalf("audit entries = %+v, want one alarm_config_change", rec.entries)
	}
}
