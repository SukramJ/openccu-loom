// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func outputTestRequest(t *testing.T, outputID string, body *hmapi.AlarmOutputTestRequest) *http.Request {
	t.Helper()
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(http.MethodPost, "/api/v1/alarm/outputs/"+outputID+"/test", http.NoBody)
	} else {
		req = httptest.NewRequest(http.MethodPost, "/api/v1/alarm/outputs/"+outputID+"/test", jsonRequestBody(t, body))
	}
	return withChiParam(req, "id", outputID)
}

// TestTestAlarmOutput_AcousticSiren_Returns204 verifies a live test fire
// against a supported output class succeeds and is journaled/audited.
func TestTestAlarmOutput_AcousticSiren_Returns204(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedOutput("sirenA", "eg", hmenum.AlarmOutputClassAcousticSiren, alarmOutputConfigFixture())
	rec := &captureRecorder{}

	w := httptest.NewRecorder()
	TestAlarmOutput(fx, rec).ServeHTTP(w, outputTestRequest(t, "sirenA", nil))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmOutputTest {
		t.Fatalf("audit entries = %+v, want one alarm_output_test", rec.entries)
	}
}

// TestTestAlarmOutput_OpticalOnlyRequestBody_Accepted verifies the
// optional optical_only body field is accepted and forwarded.
func TestTestAlarmOutput_OpticalOnlyRequestBody_Accepted(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedOutput("sirenA", "eg", hmenum.AlarmOutputClassAcousticSiren, alarmOutputConfigFixture())

	w := httptest.NewRecorder()
	TestAlarmOutput(fx, nil).ServeHTTP(w, outputTestRequest(t, "sirenA", &hmapi.AlarmOutputTestRequest{OpticalOnly: true}))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
}

// TestTestAlarmOutput_SwitchedSirenActuator_Returns204 verifies the
// actuator-backed test-fire path (switched siren / alarm light) also
// succeeds through the REST surface.
func TestTestAlarmOutput_SwitchedSirenActuator_Returns204(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedOutput("plug", "eg", hmenum.AlarmOutputClassSwitchedSiren, alarmOutputConfigFixture())

	w := httptest.NewRecorder()
	TestAlarmOutput(fx, nil).ServeHTTP(w, outputTestRequest(t, "plug", nil))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
}

// TestTestAlarmOutput_SmokeSounder_Returns409 verifies smoke-detector
// sounders are refused a live test fire: each activation costs
// irreplaceable battery life and likely fans out to the whole
// smoke-detector group (notes/concepts/alarm-concept.md §7).
func TestTestAlarmOutput_SmokeSounder_Returns409(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedOutput("smoke", "eg", hmenum.AlarmOutputClassSmokeSounder, alarmOutputConfigFixture())

	w := httptest.NewRecorder()
	TestAlarmOutput(fx, nil).ServeHTTP(w, outputTestRequest(t, "smoke", nil))

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", w.Code, w.Body.String())
	}
}

// Percent-encoded IDs are decoded centrally by the router (the rest
// package's decodedPathRouting middleware and its test), so this file
// only exercises the handler with the decoded values chi delivers.

func TestTestAlarmOutput_UnknownOutput_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	w := httptest.NewRecorder()
	TestAlarmOutput(fx, nil).ServeHTTP(w, outputTestRequest(t, "missing", nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}
