// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
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

func walkTestRequest(zoneID, action string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alarm/zones/"+zoneID+"/walktest/"+action, http.NoBody)
	return withChiParam(req, "id", zoneID)
}

func walkTestStatusRequest(zoneID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/zones/"+zoneID+"/walktest", http.NoBody)
	return withChiParam(req, "id", zoneID)
}

// --- StartAlarmWalkTest ---

func TestStartAlarmWalkTest_HappyPath_Returns204(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	rec := &captureRecorder{}

	w := httptest.NewRecorder()
	StartAlarmWalkTest(fx, rec).ServeHTTP(w, walkTestRequest("eg", "start"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmWalkTest {
		t.Fatalf("audit entries = %+v, want one alarm_walk_test", rec.entries)
	}
}

func TestStartAlarmWalkTest_UnknownZone_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	w := httptest.NewRecorder()
	StartAlarmWalkTest(fx, nil).ServeHTTP(w, walkTestRequest("missing", "start"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// TestStartAlarmWalkTest_WhileArmed_Returns409 verifies a walk test can
// only start on a disarmed zone — starting it while armed is refused.
func TestStartAlarmWalkTest_WhileArmed_Returns409(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	if _, err := fx.eng.Arm(context.Background(), "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, SkipDelay: true}); err != nil {
		t.Fatalf("arm: %v", err)
	}

	w := httptest.NewRecorder()
	StartAlarmWalkTest(fx, nil).ServeHTTP(w, walkTestRequest("eg", "start"))

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", w.Code, w.Body.String())
	}
}

func TestStartAlarmWalkTest_AlreadyActive_Returns409(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	if err := fx.eng.WalkTestStart(context.Background(), "eg", "tester", "test"); err != nil {
		t.Fatalf("walk test start: %v", err)
	}

	w := httptest.NewRecorder()
	StartAlarmWalkTest(fx, nil).ServeHTTP(w, walkTestRequest("eg", "start"))

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", w.Code, w.Body.String())
	}
}

// --- GetAlarmWalkTestStatus ---

func TestGetAlarmWalkTestStatus_UnknownZone_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	w := httptest.NewRecorder()
	GetAlarmWalkTestStatus(fx).ServeHTTP(w, walkTestStatusRequest("missing"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// TestGetAlarmWalkTestStatus_ReflectsSeenSensors verifies a sensor
// activation during a running session ticks that sensor's "tested" flag
// without evaluating it against the (disarmed) state machine.
func TestGetAlarmWalkTestStatus_ReflectsSeenSensors(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	fx.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	fx.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}})
	if err := fx.eng.WalkTestStart(context.Background(), "eg", "tester", "test"); err != nil {
		t.Fatalf("walk test start: %v", err)
	}
	fx.eng.HandleSensorEvent(context.Background(), "door", true)

	w := httptest.NewRecorder()
	GetAlarmWalkTestStatus(fx).ServeHTTP(w, walkTestStatusRequest("eg"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var status hmapi.AlarmWalkTestStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !status.Active {
		t.Fatal("active = false, want true")
	}
	if len(status.Sensors) != 2 {
		t.Fatalf("sensors = %+v, want 2", status.Sensors)
	}
	seen := map[string]bool{}
	for _, s := range status.Sensors {
		seen[s.ID] = s.Tested
	}
	if !seen["door"] {
		t.Error("door.tested = false, want true")
	}
	if seen["window"] {
		t.Error("window.tested = true, want false (never activated)")
	}
	// The state machine must not have evaluated the activation: the
	// zone stays disarmed throughout a walk test.
	if snap, ok := fx.eng.Zone("eg"); !ok || snap.State != hmenum.AlarmZoneStateDisarmed {
		t.Errorf("zone state = %+v, want to remain disarmed during walk test", snap)
	}
}

// --- StopAlarmWalkTest ---

func TestStopAlarmWalkTest_HappyPath_Returns204(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))
	if err := fx.eng.WalkTestStart(context.Background(), "eg", "tester", "test"); err != nil {
		t.Fatalf("walk test start: %v", err)
	}
	rec := &captureRecorder{}

	w := httptest.NewRecorder()
	StopAlarmWalkTest(fx, rec).ServeHTTP(w, walkTestRequest("eg", "stop"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}
	st, err := fx.eng.WalkTestStatus("eg")
	if err != nil {
		t.Fatalf("walk test status: %v", err)
	}
	if st.Active {
		t.Error("active = true after stop, want false")
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionAlarmWalkTest {
		t.Fatalf("audit entries = %+v, want one alarm_walk_test", rec.entries)
	}
}

func TestStopAlarmWalkTest_NotActive_Returns409(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))

	w := httptest.NewRecorder()
	StopAlarmWalkTest(fx, nil).ServeHTTP(w, walkTestRequest("eg", "stop"))

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", w.Code, w.Body.String())
	}
}

func TestStopAlarmWalkTest_UnknownZone_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	w := httptest.NewRecorder()
	StopAlarmWalkTest(fx, nil).ServeHTTP(w, walkTestRequest("missing", "stop"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}
