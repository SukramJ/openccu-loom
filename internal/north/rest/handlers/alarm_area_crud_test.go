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

// putOutputsBody builds the wire body for one output enrollment with an
// explicit row id, mirroring what the setup wizard sends.
func putOutputsBody(id, channel string) string {
	return `[{"id":"` + id + `","class":"acoustic_siren","central":"` + alarmFixtureCentral +
		`","channel_address":"` + channel + `","name":"Siren","config":{"modes":["full"]}}]`
}

// putSensorsBody builds the wire body for one sensor enrollment with an
// explicit row id, mirroring what the setup wizard sends.
func putSensorsBody(id, channel string) string {
	return `[{"id":"` + id + `","central":"` + alarmFixtureCentral +
		`","interface_id":"HmIP-RF","channel_address":"` + channel +
		`","parameter":"STATE","type":"door","name":"Door","config":{"modes":["full"]}}]`
}

// TestPutAlarmAreaOutputs_RowIDFromAnotherArea_IsReminted pins the
// cross-area row-identity contract: a client-supplied id round-trips
// (own rows and fresh ids alike) UNLESS it collides with another
// area's row — that one is re-minted server-side instead of failing
// the whole replace on the PRIMARY KEY. Clients have derived ids from
// the channel key, so enrolling the same siren in a second area hit
// exactly that as an opaque 500.
func TestPutAlarmAreaOutputs_RowIDFromAnotherArea_IsReminted(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedArea("eg", "Erdgeschoss", fullModeAreaConfig(0, 0, 60))
	fx.seedArea("og", "Obergeschoss", fullModeAreaConfig(0, 0, 60))
	fx.seedOutput("shared-key", "eg", hmenum.AlarmOutputClassAcousticSiren, alarmOutputConfigFixture())

	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/areas/og/outputs",
		strings.NewReader(putOutputsBody("shared-key", "shared-key:1")))
	req = withChiParam(req, "id", "og")
	w := httptest.NewRecorder()
	PutAlarmAreaOutputs(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	og, err := fx.stores.Outputs.ListByArea(context.Background(), "og")
	if err != nil {
		t.Fatalf("list og outputs: %v", err)
	}
	if len(og) != 1 {
		t.Fatalf("og outputs = %d rows, want 1", len(og))
	}
	if og[0].ID == "" || og[0].ID == "shared-key" {
		t.Errorf("og row id = %q, want a fresh non-empty id distinct from the eg row", og[0].ID)
	}
	eg, err := fx.stores.Outputs.ListByArea(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list eg outputs: %v", err)
	}
	if len(eg) != 1 || eg[0].ID != "shared-key" {
		t.Errorf("eg outputs = %+v, want the original shared-key row untouched", eg)
	}
}

// TestPutAlarmAreaOutputs_OwnRowID_RoundTrips pins the stability leg of
// the same contract: a row carrying one of THIS area's existing ids
// keeps it (the outputs tab round-trips rows verbatim on save), and a
// fresh non-colliding client id is honoured too (covered by the
// replace-semantics suite in alarm_sensors_outputs_test.go).
func TestPutAlarmAreaOutputs_OwnRowID_RoundTrips(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedArea("eg", "Erdgeschoss", fullModeAreaConfig(0, 0, 60))
	fx.seedOutput("mine", "eg", hmenum.AlarmOutputClassAcousticSiren, alarmOutputConfigFixture())

	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/areas/eg/outputs",
		strings.NewReader(putOutputsBody("mine", "mine:1")))
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmAreaOutputs(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	rows, err := fx.stores.Outputs.ListByArea(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list outputs: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "mine" {
		t.Errorf("rows = %+v, want the id to round-trip unchanged", rows)
	}
}

// TestPutAlarmAreaSensors_RowIDFromAnotherArea_IsReminted mirrors the
// output contract for sensors: the same PRIMARY KEY across areas must
// re-mint, not 500 — the sensor table shares the id-derivation history.
func TestPutAlarmAreaSensors_RowIDFromAnotherArea_IsReminted(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedArea("eg", "Erdgeschoss", fullModeAreaConfig(0, 0, 60))
	fx.seedArea("og", "Obergeschoss", fullModeAreaConfig(0, 0, 60))
	fx.seedSensor("shared-door", "eg", hmenum.AlarmSensorTypeDoor,
		engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/areas/og/sensors",
		strings.NewReader(putSensorsBody("shared-door", "shared-door:1")))
	req = withChiParam(req, "id", "og")
	w := httptest.NewRecorder()
	PutAlarmAreaSensors(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	og, err := fx.stores.Sensors.ListByArea(context.Background(), "og")
	if err != nil {
		t.Fatalf("list og sensors: %v", err)
	}
	if len(og) != 1 {
		t.Fatalf("og sensors = %d rows, want 1", len(og))
	}
	if og[0].ID == "" || og[0].ID == "shared-door" {
		t.Errorf("og row id = %q, want a fresh non-empty id distinct from the eg row", og[0].ID)
	}
	eg, err := fx.stores.Sensors.ListByArea(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list eg sensors: %v", err)
	}
	if len(eg) != 1 || eg[0].ID != "shared-door" {
		t.Errorf("eg sensors = %+v, want the original shared-door row untouched", eg)
	}
}

// TestPutAlarmAreaOutputs_DuplicateIDsInPayload_AreReminted pins the
// in-payload leg: two rows carrying the same id must not collide with
// each other either — the second occurrence gets a fresh id.
func TestPutAlarmAreaOutputs_DuplicateIDsInPayload_AreReminted(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedArea("eg", "Erdgeschoss", fullModeAreaConfig(0, 0, 60))
	fx.seedOutput("dup", "eg", hmenum.AlarmOutputClassAcousticSiren, alarmOutputConfigFixture())

	body := `[{"id":"dup","class":"acoustic_siren","central":"` + alarmFixtureCentral +
		`","channel_address":"a:1","config":{"modes":["full"]}},` +
		`{"id":"dup","class":"acoustic_siren","central":"` + alarmFixtureCentral +
		`","channel_address":"b:1","config":{"modes":["full"]}}]`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/areas/eg/outputs", strings.NewReader(body))
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmAreaOutputs(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	rows, err := fx.stores.Outputs.ListByArea(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list outputs: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].ID == rows[1].ID {
		t.Errorf("both rows share id %q, want distinct ids", rows[0].ID)
	}
}
