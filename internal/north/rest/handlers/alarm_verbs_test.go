// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// alarmVerbRequest builds a bare POST request against
// /alarm/zones/{id}/{verb} with the chi "id" route param attached.
func alarmVerbRequest(zoneID, verb string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/zones/"+zoneID+"/"+verb, http.NoBody)
	return withChiParam(req, "id", zoneID)
}

// --- DisarmAlarmZone ---

func TestDisarmAlarmZone_HappyPath_Returns204(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	if _, err := fx.eng.Arm(context.Background(), "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, SkipDelay: true}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	rec := &captureRecorder{}

	w := httptest.NewRecorder()
	DisarmAlarmZone(fx, rec).ServeHTTP(w, alarmVerbRequest("eg", "disarm"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	snap, ok := fx.eng.Zone("eg")
	if !ok || snap.State != hmenum.AlarmZoneStateDisarmed {
		t.Errorf("zone state = %+v, want disarmed", snap)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmDisarm {
		t.Fatalf("audit entries = %+v, want one alarm_disarm", rec.entries)
	}
}

func TestDisarmAlarmZone_UnknownZone_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	w := httptest.NewRecorder()
	DisarmAlarmZone(fx, nil).ServeHTTP(w, alarmVerbRequest("missing", "disarm"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// --- SilenceAlarmZone ---

// TestSilenceAlarmZone_DisarmedZone_Returns204 pins the S3/S6 rule at the
// REST surface: silence never fails on state, even against a disarmed
// zone with no open incident.
func TestSilenceAlarmZone_DisarmedZone_Returns204(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	rec := &captureRecorder{}

	w := httptest.NewRecorder()
	SilenceAlarmZone(fx, rec).ServeHTTP(w, alarmVerbRequest("eg", "silence"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmSilence {
		t.Fatalf("audit entries = %+v, want one alarm_silence", rec.entries)
	}
}

func TestSilenceAlarmZone_UnknownZone_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	w := httptest.NewRecorder()
	SilenceAlarmZone(fx, nil).ServeHTTP(w, alarmVerbRequest("missing", "silence"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// --- AcknowledgeAlarmZone ---

func TestAcknowledgeAlarmZone_UnknownZone_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	w := httptest.NewRecorder()
	AcknowledgeAlarmZone(fx, nil).ServeHTTP(w, alarmVerbRequest("missing", "acknowledge"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestAcknowledgeAlarmZone_NoIncident_Returns409(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))

	w := httptest.NewRecorder()
	AcknowledgeAlarmZone(fx, nil).ServeHTTP(w, alarmVerbRequest("eg", "acknowledge"))

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", w.Code, w.Body.String())
	}
}

func TestAcknowledgeAlarmZone_WithOpenIncident_Returns204(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	if _, err := fx.eng.Arm(context.Background(), "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, SkipDelay: true}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	fx.eng.HandleSensorEvent(context.Background(), "window", true)
	if snap, ok := fx.eng.Zone("eg"); !ok || snap.State != hmenum.AlarmZoneStateTriggered {
		t.Fatalf("precondition: zone not triggered, snap=%+v ok=%v", snap, ok)
	}
	rec := &captureRecorder{}

	w := httptest.NewRecorder()
	AcknowledgeAlarmZone(fx, rec).ServeHTTP(w, alarmVerbRequest("eg", "acknowledge"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmAcknowledge {
		t.Fatalf("audit entries = %+v, want one alarm_acknowledge", rec.entries)
	}
}

// --- SilenceAllAlarmZones ---

func TestSilenceAllAlarmZones_Returns204AndSilencesEveryZone(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedZone("og", "Obergeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	if _, err := fx.eng.Arm(context.Background(), "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, SkipDelay: true}); err != nil {
		t.Fatalf("arm eg: %v", err)
	}
	fx.eng.HandleSensorEvent(context.Background(), "window", true)
	if snap, ok := fx.eng.Zone("eg"); !ok || snap.IncidentID == 0 || snap.IncidentSilenced {
		t.Fatalf("precondition: expected an unsilenced open incident, snap=%+v ok=%v", snap, ok)
	}
	rec := &captureRecorder{}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/silence-all", http.NoBody)
	w := httptest.NewRecorder()
	SilenceAllAlarmZones(fx, rec).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	snap, ok := fx.eng.Zone("eg")
	if !ok || !snap.IncidentSilenced {
		t.Errorf("eg incident silenced = %+v, want true", snap)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmSilence {
		t.Fatalf("audit entries = %+v, want one alarm_silence", rec.entries)
	}
}

// --- role-agnostic handler surface ---

// TestAlarmVerbHandlers_NoAuthContextRequired verifies the alarm verb
// handlers carry no role/auth logic of their own: a request whose
// context holds no auth.Identity at all still succeeds and falls back
// to the "anonymous" audit actor. Role/authentication gating for the
// /alarm surface lives exclusively in the router's middleware chain,
// never inside the handler bodies.
func TestAlarmVerbHandlers_NoAuthContextRequired(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	rec := &captureRecorder{}

	// httptest.NewRequest's context deliberately carries no
	// auth.Identity — the exact shape a handler would see if the
	// router's auth middleware were bypassed entirely.
	w := httptest.NewRecorder()
	DisarmAlarmZone(fx, rec).ServeHTTP(w, alarmVerbRequest("eg", "disarm"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 even with no auth context, body=%s", w.Code, w.Body.String())
	}
	if len(rec.entries) != 1 || rec.entries[0].User != "anonymous" {
		t.Fatalf("audit entries = %+v, want single entry with actor=anonymous", rec.entries)
	}
}
