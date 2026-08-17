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
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// marshalZoneConfig serializes an engine.ZoneConfig into the raw JSON
// document the AlarmZone.Config wire field carries verbatim.
func marshalZoneConfig(t *testing.T, cfg engine.ZoneConfig) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal zone config: %v", err)
	}
	return b
}

func alarmZoneRequestBody(t *testing.T, zone hmapi.AlarmZone) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(zone)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return bytes.NewReader(b)
}

// --- ListAlarmZones / GetAlarmZone ---

func TestListAlarmZones_Empty(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/zones", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmZones(fx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var body []hmapi.AlarmZone
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("zones = %+v, want empty", body)
	}
}

func TestListAlarmZones_ReturnsSeededAreas(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(30, 15, 60))
	fx.seedZone("og", "Obergeschoss", fullModeZoneConfig(0, 0, 60))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/zones", http.NoBody)
	w := httptest.NewRecorder()
	ListAlarmZones(fx).ServeHTTP(w, req)

	var body []hmapi.AlarmZone
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("zones = %+v, want 2", body)
	}
}

func TestGetAlarmZone_UnknownID_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/zones/missing", http.NoBody)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	GetAlarmZone(fx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestGetAlarmZone_ReturnsSeeded(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(30, 15, 60))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/zones/eg", http.NoBody)
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	GetAlarmZone(fx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var body hmapi.AlarmZone
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.ID != "eg" || body.Name != "Erdgeschoss" {
		t.Errorf("zone = %+v, want id=eg name=Erdgeschoss", body)
	}
}

// --- CreateAlarmZone ---

func TestCreateAlarmZone_HappyPath_Returns201AndRecordsAudit(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	rec := &captureRecorder{}

	body := alarmZoneRequestBody(t, hmapi.AlarmZone{
		Name:   "Erdgeschoss",
		Config: marshalZoneConfig(t, fullModeZoneConfig(30, 15, 60)),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/zones", body)
	w := httptest.NewRecorder()
	CreateAlarmZone(fx, rec).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created hmapi.AlarmZone
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.ID == "" {
		t.Error("created zone has no server-generated id")
	}
	if created.Name != "Erdgeschoss" {
		t.Errorf("name = %q, want Erdgeschoss", created.Name)
	}
	// The new zone must be immediately visible through the engine —
	// CreateAlarmZone reloads before responding.
	if _, ok := fx.eng.Zone(created.ID); !ok {
		t.Error("created zone not visible through the engine after Reload")
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmConfigChange {
		t.Fatalf("audit entries = %+v, want one alarm_config_change", rec.entries)
	}
}

func TestCreateAlarmZone_InvalidConfig_Returns422(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	body := alarmZoneRequestBody(t, hmapi.AlarmZone{
		Name:   "Bad",
		Config: json.RawMessage(`{"modes":123}`),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/zones", body)
	w := httptest.NewRecorder()
	CreateAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
	}
}

func TestCreateAlarmZone_EmptyName_Returns422(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	body := alarmZoneRequestBody(t, hmapi.AlarmZone{
		Name:   "   ",
		Config: marshalZoneConfig(t, fullModeZoneConfig(30, 15, 60)),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/zones", body)
	w := httptest.NewRecorder()
	CreateAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
	}
	if got, err := fx.stores.Zones.GetAll(context.Background()); err != nil || len(got) != 0 {
		t.Errorf("no zone must be persisted for an empty name: zones=%+v err=%v", got, err)
	}
}

func TestCreateAlarmZone_MalformedJSON_Returns400(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/zones", strings.NewReader("{not-json"))
	w := httptest.NewRecorder()
	CreateAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// --- PutAlarmZone ---

func TestPutAlarmZone_UnknownID_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	body := alarmZoneRequestBody(t, hmapi.AlarmZone{Name: "x", Config: marshalZoneConfig(t, fullModeZoneConfig(0, 0, 60))})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/missing", body)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	PutAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestPutAlarmZone_HappyPath_Returns204AndPersists(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(30, 15, 60))
	rec := &captureRecorder{}

	newBody := alarmZoneRequestBody(t, hmapi.AlarmZone{
		Name:   "Erdgeschoss Renamed",
		Config: marshalZoneConfig(t, fullModeZoneConfig(45, 20, 90)),
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/eg", newBody)
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmZone(fx, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	row, ok, err := fx.stores.Zones.Get(context.Background(), "eg")
	if err != nil || !ok {
		t.Fatalf("get zone after put: ok=%v err=%v", ok, err)
	}
	if row.Name != "Erdgeschoss Renamed" {
		t.Errorf("name = %q, want Erdgeschoss Renamed", row.Name)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmConfigChange {
		t.Fatalf("audit entries = %+v, want one alarm_config_change", rec.entries)
	}
}

func TestPutAlarmZone_EmptyName_Returns422AndKeepsStoredName(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(30, 15, 60))

	body := alarmZoneRequestBody(t, hmapi.AlarmZone{
		Name:   "",
		Config: marshalZoneConfig(t, fullModeZoneConfig(45, 20, 90)),
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/eg", body)
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	PutAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", w.Code, w.Body.String())
	}
	row, ok, err := fx.stores.Zones.Get(context.Background(), "eg")
	if err != nil || !ok {
		t.Fatalf("get zone after rejected put: ok=%v err=%v", ok, err)
	}
	if row.Name != "Erdgeschoss" {
		t.Errorf("name = %q, want the stored name to survive a rejected update", row.Name)
	}
}

// --- DeleteAlarmZone ---

func TestDeleteAlarmZone_UnknownID_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/alarm/zones/missing", http.NoBody)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	DeleteAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteAlarmZone_WhileArmed_Returns409(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	if _, err := fx.eng.Arm(context.Background(), "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, SkipDelay: true}); err != nil {
		t.Fatalf("arm: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/alarm/zones/eg", http.NoBody)
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	DeleteAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteAlarmZone_WhileDisarmed_Returns204AndCascadesSensorsOutputs(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	fx.seedOutput("siren", "eg", hmenum.AlarmOutputClassAcousticSiren, alarmOutputConfigFixture())
	rec := &captureRecorder{}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/alarm/zones/eg", http.NoBody)
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	DeleteAlarmZone(fx, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	if _, ok, _ := fx.stores.Zones.Get(context.Background(), "eg"); ok {
		t.Error("zone row still present after delete")
	}
	sensors, err := fx.stores.Sensors.ListByZone(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list sensors: %v", err)
	}
	if len(sensors) != 0 {
		t.Errorf("sensors = %+v, want cascaded delete", sensors)
	}
	outs, err := fx.stores.Outputs.ListByZone(context.Background(), "eg")
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

// TestPutAlarmZoneOutputs_RowIDFromAnotherArea_IsReminted pins the
// cross-zone row-identity contract: a client-supplied id round-trips
// (own rows and fresh ids alike) UNLESS it collides with another
// zone's row — that one is re-minted server-side instead of failing
// the whole replace on the PRIMARY KEY. Clients have derived ids from
// the channel key, so enrolling the same siren in a second zone hit
// exactly that as an opaque 500.
func TestPutAlarmZoneOutputs_RowIDFromAnotherArea_IsReminted(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedZone("og", "Obergeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedOutput("shared-key", "eg", hmenum.AlarmOutputClassAcousticSiren, alarmOutputConfigFixture())

	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/og/outputs",
		strings.NewReader(putOutputsBody("shared-key", "shared-key:1")))
	req = withChiParam(req, "id", "og")
	w := httptest.NewRecorder()
	PutAlarmZoneOutputs(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	og, err := fx.stores.Outputs.ListByZone(context.Background(), "og")
	if err != nil {
		t.Fatalf("list og outputs: %v", err)
	}
	if len(og) != 1 {
		t.Fatalf("og outputs = %d rows, want 1", len(og))
	}
	if og[0].ID == "" || og[0].ID == "shared-key" {
		t.Errorf("og row id = %q, want a fresh non-empty id distinct from the eg row", og[0].ID)
	}
	eg, err := fx.stores.Outputs.ListByZone(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list eg outputs: %v", err)
	}
	if len(eg) != 1 || eg[0].ID != "shared-key" {
		t.Errorf("eg outputs = %+v, want the original shared-key row untouched", eg)
	}
}

// TestPutAlarmZoneOutputs_OwnRowID_RoundTrips pins the stability leg of
// the same contract: a row carrying one of THIS zone's existing ids
// keeps it (the outputs tab round-trips rows verbatim on save), and a
// fresh non-colliding client id is honoured too (covered by the
// replace-semantics suite in alarm_sensors_outputs_test.go).
func TestPutAlarmZoneOutputs_OwnRowID_RoundTrips(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedOutput("mine", "eg", hmenum.AlarmOutputClassAcousticSiren, alarmOutputConfigFixture())

	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/eg/outputs",
		strings.NewReader(putOutputsBody("mine", "mine:1")))
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
	if len(rows) != 1 || rows[0].ID != "mine" {
		t.Errorf("rows = %+v, want the id to round-trip unchanged", rows)
	}
}

// TestPutAlarmZoneSensors_RowIDFromAnotherArea_IsReminted mirrors the
// output contract for sensors: the same PRIMARY KEY across zones must
// re-mint, not 500 — the sensor table shares the id-derivation history.
func TestPutAlarmZoneSensors_RowIDFromAnotherArea_IsReminted(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedZone("og", "Obergeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedSensor("shared-door", "eg", hmenum.AlarmSensorTypeDoor,
		engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/og/sensors",
		strings.NewReader(putSensorsBody("shared-door", "shared-door:1")))
	req = withChiParam(req, "id", "og")
	w := httptest.NewRecorder()
	PutAlarmZoneSensors(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	og, err := fx.stores.Sensors.ListByZone(context.Background(), "og")
	if err != nil {
		t.Fatalf("list og sensors: %v", err)
	}
	if len(og) != 1 {
		t.Fatalf("og sensors = %d rows, want 1", len(og))
	}
	if og[0].ID == "" || og[0].ID == "shared-door" {
		t.Errorf("og row id = %q, want a fresh non-empty id distinct from the eg row", og[0].ID)
	}
	eg, err := fx.stores.Sensors.ListByZone(context.Background(), "eg")
	if err != nil {
		t.Fatalf("list eg sensors: %v", err)
	}
	if len(eg) != 1 || eg[0].ID != "shared-door" {
		t.Errorf("eg sensors = %+v, want the original shared-door row untouched", eg)
	}
}

// TestPutAlarmZoneOutputs_DuplicateIDsInPayload_AreReminted pins the
// in-payload leg: two rows carrying the same id must not collide with
// each other either — the second occurrence gets a fresh id.
func TestPutAlarmZoneOutputs_DuplicateIDsInPayload_AreReminted(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedOutput("dup", "eg", hmenum.AlarmOutputClassAcousticSiren, alarmOutputConfigFixture())

	body := `[{"id":"dup","class":"acoustic_siren","central":"` + alarmFixtureCentral +
		`","channel_address":"a:1","config":{"modes":["full"]}},` +
		`{"id":"dup","class":"acoustic_siren","central":"` + alarmFixtureCentral +
		`","channel_address":"b:1","config":{"modes":["full"]}}]`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alarm/zones/eg/outputs", strings.NewReader(body))
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
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].ID == rows[1].ID {
		t.Errorf("both rows share id %q, want distinct ids", rows[0].ID)
	}
}

// --- uniqueZoneSlug ---

// TestUniqueZoneSlug_BlankStoredSlugStillReservesTheDerivedOne pins a
// post-migration state: a zone whose stored slug was blanked (e.g. by the
// alarm_zone_slug_charset migration) resolves to routingkey.HubSlug(name)
// at read time (internal/security/index.go refreshZoneSlugs), so a new
// zone whose name transliterates to the same slug must not be handed the
// identity the existing zone already answers to.
func TestUniqueZoneSlug_BlankStoredSlugStillReservesTheDerivedOne(t *testing.T) {
	t.Parallel()
	existing := []sqlitestore.AlarmZoneRow{
		{ID: "eg", Name: "Küche", Slug: ""},
	}
	got := uniqueZoneSlug("Kuche", existing)
	if got == "kuche" {
		t.Errorf("slug = %q, want a slug distinct from the existing zone's derived %q", got, "kuche")
	}
	if got != "kuche-2" {
		t.Errorf("slug = %q, want kuche-2", got)
	}
}

// TestUniqueZoneSlug_NonBlankStoredSlugStillWins pins the ordinary case:
// a normally-persisted slug is reserved as-is, independent of the name.
func TestUniqueZoneSlug_NonBlankStoredSlugStillWins(t *testing.T) {
	t.Parallel()
	existing := []sqlitestore.AlarmZoneRow{
		{ID: "eg", Name: "Erdgeschoss", Slug: "custom-slug"},
	}
	got := uniqueZoneSlug("Erdgeschoss", existing)
	if got != "erdgeschoss" {
		t.Errorf("slug = %q, want erdgeschoss (the stored slug must not block the name-derived one)", got)
	}
}

// TestCreateAlarmZone_BlankSiblingSlug_DoesNotCollide is the handler-level
// twin of TestUniqueZoneSlug_BlankStoredSlugStillReservesTheDerivedOne:
// seedZone leaves Slug empty exactly like a post-migration row, and the
// new zone must come out with a slug the security domain will not fold
// into the existing zone's identity.
func TestCreateAlarmZone_BlankSiblingSlug_DoesNotCollide(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Küche", fullModeZoneConfig(30, 15, 60))

	body := alarmZoneRequestBody(t, hmapi.AlarmZone{
		Name:   "Kuche",
		Config: marshalZoneConfig(t, fullModeZoneConfig(30, 15, 60)),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/zones", body)
	w := httptest.NewRecorder()
	CreateAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var created hmapi.AlarmZone
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	row, ok, err := fx.stores.Zones.Get(context.Background(), created.ID)
	if err != nil || !ok {
		t.Fatalf("get created zone: ok=%v err=%v", ok, err)
	}
	if row.Slug == "kuche" {
		t.Errorf("slug = %q, must not collide with the existing zone's derived slug", row.Slug)
	}
}
