// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// --- ListAlarmZoneSensors / PutAlarmZoneSensors ---

func TestListAlarmZoneSensors_UnknownZone_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/zones/missing/sensors", http.NoBody)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	ListAlarmZoneSensors(fx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestPutAlarmZoneSensors_UnknownZone_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	body := jsonRequestBody(t, []hmapi.AlarmSensor{})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/missing/sensors", body)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	PutAlarmZoneSensors(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// TestPutAlarmZoneSensors_ReplaceSemantics verifies PUT performs a full
// replace: the two pre-seeded sensors vanish and only the sensors in the
// request body remain enrolled afterwards.
func TestPutAlarmZoneSensors_ReplaceSemantics(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
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
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/eg/sensors", jsonRequestBody(t, replacement))
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmZoneSensors(fx, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}

	rows, err := fx.stores.Sensors.ListByZone(context.Background(), "eg")
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

// TestPutAlarmZoneSensors_EmptyBody_ClearsAllSensors verifies an empty
// array body removes every previously enrolled sensor.
func TestPutAlarmZoneSensors_EmptyBody_ClearsAllSensors(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/eg/sensors", jsonRequestBody(t, []hmapi.AlarmSensor{}))
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmZoneSensors(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	rows, err := fx.stores.Sensors.ListByZone(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list sensors: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("sensors after clear = %+v, want none", rows)
	}
}

// --- ListAlarmZoneOutputs / PutAlarmZoneOutputs ---

func TestListAlarmZoneOutputs_UnknownZone_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/zones/missing/outputs", http.NoBody)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	ListAlarmZoneOutputs(fx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestPutAlarmZoneOutputs_UnknownZone_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/missing/outputs", jsonRequestBody(t, []hmapi.AlarmOutput{}))
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	PutAlarmZoneOutputs(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// TestPutAlarmZoneOutputs_ReplaceSemantics verifies PUT performs a full
// replace of the output set, mirroring the sensors endpoint's semantics.
func TestPutAlarmZoneOutputs_ReplaceSemantics(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedOutput("sirenA", "eg", hmenum.AlarmOutputClassAcousticSiren, alarmOutputConfigFixture())
	rec := &captureRecorder{}

	replacement := []hmapi.AlarmOutput{
		{ID: "light", Class: string(hmenum.AlarmOutputClassAlarmLight), Central: alarmFixtureCentral, ChannelAddress: "light:1", Name: "Light"},
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/eg/outputs", jsonRequestBody(t, replacement))
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmZoneOutputs(fx, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	rows, err := fx.stores.Outputs.ListByZone(context.Background(), "eg")
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

func TestPutAlarmZoneOutputs_InvalidClass_Returns422(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))

	bad := []hmapi.AlarmOutput{{ID: "x", Class: "not-a-real-class", Central: alarmFixtureCentral, ChannelAddress: "x:1"}}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/eg/outputs", jsonRequestBody(t, bad))
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmZoneOutputs(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
	}
}

// TestPutAlarmZoneOutputs_SysvarMirrorWithoutName_Returns422 verifies a
// sysvar_mirror output whose config lacks sysvar_name is rejected: the
// mirror silently skips a nameless target (internal/alarm/sysvar.go's
// mirrorTargets), so accepting it here would let an operator save a
// no-op mirror.
func TestPutAlarmZoneOutputs_SysvarMirrorWithoutName_Returns422(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))

	bad := []hmapi.AlarmOutput{
		{ID: "mirror", Class: string(hmenum.AlarmOutputClassSysvarMirror), Config: json.RawMessage(`{}`)},
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/eg/outputs", jsonRequestBody(t, bad))
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmZoneOutputs(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
	}
}

// TestPutAlarmZoneOutputs_SysvarMirrorWithName_Saves verifies the same
// output class saves once sysvar_name is present.
func TestPutAlarmZoneOutputs_SysvarMirrorWithName_Saves(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))

	good := []hmapi.AlarmOutput{
		{ID: "mirror", Class: string(hmenum.AlarmOutputClassSysvarMirror), Config: json.RawMessage(`{"sysvar_name":"AlarmState"}`)},
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/eg/outputs", jsonRequestBody(t, good))
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmZoneOutputs(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	rows, err := fx.stores.Outputs.ListByZone(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list outputs: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "mirror" {
		t.Fatalf("outputs after save = %+v, want exactly [mirror]", rows)
	}
}

// stubbedOutputEligibilityFixture substitutes a fixed OutputTargetEligible
// verdict over an otherwise real alarmPanelFixture, so PutAlarmZoneOutputs's
// soft target-validation branch (alarm_config.go, "Soft target validation")
// can be exercised without a live central registry. Mirrors the
// embed-and-override pattern of stubbedPanelsFixture in alarm_panels_test.go.
type stubbedOutputEligibilityFixture struct {
	*alarmPanelFixture
	eligible bool
	known    bool
}

func (s stubbedOutputEligibilityFixture) OutputTargetEligible(string, string, hmenum.AlarmOutputClass) (eligible, known bool) {
	return s.eligible, s.known
}

var _ AlarmPanel = stubbedOutputEligibilityFixture{}

// TestPutAlarmZoneOutputs_KnownIneligibleTarget_Returns422AndLeavesStoreUntouched
// verifies a resolvable channel that cannot carry the requested class (the
// runtime driver would fault on every fire) is rejected with 422, and that
// the pre-existing output set is left exactly as it was.
func TestPutAlarmZoneOutputs_KnownIneligibleTarget_Returns422AndLeavesStoreUntouched(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedOutput("sirenA", "eg", hmenum.AlarmOutputClassAcousticSiren, alarmOutputConfigFixture())
	stub := stubbedOutputEligibilityFixture{alarmPanelFixture: fx, eligible: false, known: true}

	replacement := []hmapi.AlarmOutput{
		{ID: "light", Class: string(hmenum.AlarmOutputClassAlarmLight), Central: alarmFixtureCentral, ChannelAddress: "light:1", Name: "Light"},
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/eg/outputs", jsonRequestBody(t, replacement))
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmZoneOutputs(stub, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
	}
	rows, err := fx.stores.Outputs.ListByZone(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list outputs: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "sirenA" {
		t.Fatalf("outputs after refused save = %+v, want unchanged [sirenA]", rows)
	}
}

// TestPutAlarmZoneOutputs_UnknownTarget_SavesDespiteUnresolvedEligibility
// verifies an unresolvable central/channel (known=false, e.g. the CCU is
// down or still booting) never blocks the config save — soft validation
// treats it as eligible and the replace proceeds.
func TestPutAlarmZoneOutputs_UnknownTarget_SavesDespiteUnresolvedEligibility(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	stub := stubbedOutputEligibilityFixture{alarmPanelFixture: fx, eligible: true, known: false}

	replacement := []hmapi.AlarmOutput{
		{ID: "light", Class: string(hmenum.AlarmOutputClassAlarmLight), Central: alarmFixtureCentral, ChannelAddress: "light:1", Name: "Light"},
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/eg/outputs", jsonRequestBody(t, replacement))
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmZoneOutputs(stub, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	rows, err := fx.stores.Outputs.ListByZone(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list outputs: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "light" {
		t.Fatalf("outputs after save = %+v, want exactly [light]", rows)
	}
}
