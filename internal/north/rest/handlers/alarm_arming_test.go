// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// armRequest builds a POST /alarm/zones/{id}/arm request with a chi "id"
// route param already attached.
func armRequest(t *testing.T, zoneID string, body hmapi.AlarmArmRequest) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/zones/"+zoneID+"/arm", jsonRequestBody(t, body))
	return withChiParam(req, "id", zoneID)
}

// TestArmAlarmZone_HappyPath_Returns200WithBypassedList verifies a
// successful arm answers 200 with the resulting state, the exit-delay
// length, and the (explicitly requested) bypass list.
func TestArmAlarmZone_HappyPath_Returns200WithBypassedList(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(30, 15, 60))
	fx.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	rec := &captureRecorder{}

	req := armRequest(t, "eg", hmapi.AlarmArmRequest{Mode: string(hmenum.AlarmModeFull), Bypass: []string{"door"}})
	w := httptest.NewRecorder()
	ArmAlarmZone(fx, rec).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var accepted hmapi.AlarmArmAccepted
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if accepted.State != string(hmenum.AlarmZoneStateArming) {
		t.Errorf("state = %q, want arming", accepted.State)
	}
	if accepted.ExitDelayS != 30 {
		t.Errorf("exit_delay_s = %d, want 30", accepted.ExitDelayS)
	}
	if len(accepted.Bypassed) != 1 || accepted.Bypassed[0] != "door" {
		t.Errorf("bypassed = %+v, want [door]", accepted.Bypassed)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmArm {
		t.Fatalf("audit entries = %+v, want one alarm_arm", rec.entries)
	}
}

// TestArmAlarmZone_NotReady_Returns409WithBlockers verifies an arm
// attempt against an open, non-bypassed sensor is refused with 409 and
// the problem body's field errors name the blocking sensor.
func TestArmAlarmZone_NotReady_Returns409WithBlockers(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	fx.eng.HandleSensorEvent(context.Background(), "door", true)

	req := armRequest(t, "eg", hmapi.AlarmArmRequest{Mode: string(hmenum.AlarmModeFull)})
	w := httptest.NewRecorder()
	ArmAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", w.Code, w.Body.String())
	}
	var problemBody problem.Details
	if err := json.Unmarshal(w.Body.Bytes(), &problemBody); err != nil {
		t.Fatalf("unmarshal problem: %v", err)
	}
	if len(problemBody.Errors) != 1 || problemBody.Errors[0].Field != "door" {
		t.Errorf("problem errors = %+v, want one field error naming door", problemBody.Errors)
	}
	// The zone must not have moved off disarmed.
	snap, ok := fx.eng.Zone("eg")
	if !ok || snap.State != hmenum.AlarmZoneStateDisarmed {
		t.Errorf("zone state = %+v, want to remain disarmed after refusal", snap)
	}
}

// TestArmAlarmZone_ForceArm_Returns200AndBypassesBlockers verifies
// force=true accepts the arm despite the blocker and reports it bypassed.
func TestArmAlarmZone_ForceArm_Returns200AndBypassesBlockers(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	fx.eng.HandleSensorEvent(context.Background(), "door", true)

	req := armRequest(t, "eg", hmapi.AlarmArmRequest{Mode: string(hmenum.AlarmModeFull), Force: true, SkipDelay: true})
	w := httptest.NewRecorder()
	ArmAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var accepted hmapi.AlarmArmAccepted
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if accepted.State != string(hmenum.AlarmZoneStateArmed) {
		t.Errorf("state = %q, want armed (skip_delay)", accepted.State)
	}
	sort.Strings(accepted.Bypassed)
	if len(accepted.Bypassed) != 1 || accepted.Bypassed[0] != "door" {
		t.Errorf("bypassed = %+v, want [door]", accepted.Bypassed)
	}
}

func TestArmAlarmZone_UnknownZone_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := armRequest(t, "missing", hmapi.AlarmArmRequest{Mode: string(hmenum.AlarmModeFull)})
	w := httptest.NewRecorder()
	ArmAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestArmAlarmZone_InvalidMode_Returns400(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))

	req := armRequest(t, "eg", hmapi.AlarmArmRequest{Mode: "not-a-mode"})
	w := httptest.NewRecorder()
	ArmAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

// TestArmAlarmZone_UnconfiguredMode_Returns400 verifies arming into a
// syntactically valid mode the zone does not configure is rejected, not
// silently accepted.
func TestArmAlarmZone_UnconfiguredMode_Returns400(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))

	req := armRequest(t, "eg", hmapi.AlarmArmRequest{Mode: string(hmenum.AlarmModeNight)})
	w := httptest.NewRecorder()
	ArmAlarmZone(fx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}
