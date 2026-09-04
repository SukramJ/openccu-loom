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
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestAlarmState_NoZones_ReturnsEmptyList verifies GET /alarm/state on a
// freshly started engine (no configured zones) answers 200 with an empty
// zones array rather than null or an error.
func TestAlarmState_NoZones_ReturnsEmptyList(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/state", http.NoBody)
	w := httptest.NewRecorder()
	AlarmState(fx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var body alarmStateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Zones == nil || len(body.Zones) != 0 {
		t.Fatalf("zones = %+v, want empty non-nil slice", body.Zones)
	}
}

// TestAlarmState_ReflectsModeAndReadinessBeforeArm verifies the disarmed
// snapshot carries the zone's per-mode readiness (including blockers from
// an already-open sensor) but no mode, incident, or countdown.
func TestAlarmState_ReflectsModeAndReadinessBeforeArm(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(30, 15, 60))
	fx.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	fx.eng.HandleSensorEvent(context.Background(), "door", true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/state", http.NoBody)
	w := httptest.NewRecorder()
	AlarmState(fx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var body alarmStateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Zones) != 1 {
		t.Fatalf("zones = %+v, want exactly one", body.Zones)
	}
	zone := body.Zones[0]
	if zone.State != string(hmenum.AlarmZoneStateDisarmed) {
		t.Errorf("state = %q, want disarmed", zone.State)
	}
	if zone.Mode != "" {
		t.Errorf("mode = %q, want empty while disarmed", zone.Mode)
	}
	if zone.Incident != nil {
		t.Errorf("incident = %+v, want nil while disarmed", zone.Incident)
	}
	if zone.Countdown != nil {
		t.Errorf("countdown = %+v, want nil while disarmed", zone.Countdown)
	}
	rd, ok := zone.Readiness[string(hmenum.AlarmModeFull)]
	if !ok {
		t.Fatalf("readiness missing full mode: %+v", zone.Readiness)
	}
	if rd.Ready {
		t.Error("ready = true, want false with door open and no bypass")
	}
	if len(rd.Blockers) != 1 || rd.Blockers[0] != "door" {
		t.Errorf("blockers = %+v, want [door]", rd.Blockers)
	}
}

// TestAlarmState_ExitDelayCountdown verifies an arming zone surfaces a
// running exit-delay countdown with the mode's configured total.
func TestAlarmState_ExitDelayCountdown(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(30, 15, 60))

	if _, err := fx.eng.Arm(context.Background(), "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull}); err != nil {
		t.Fatalf("arm: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/state", http.NoBody)
	w := httptest.NewRecorder()
	AlarmState(fx).ServeHTTP(w, req)

	var body alarmStateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	zone := body.Zones[0]
	if zone.State != string(hmenum.AlarmZoneStateArming) {
		t.Fatalf("state = %q, want arming", zone.State)
	}
	if zone.Countdown == nil {
		t.Fatal("countdown = nil, want an active exit-delay countdown")
	}
	if zone.Countdown.Kind != engine.TimerKindExit {
		t.Errorf("countdown.kind = %q, want %q", zone.Countdown.Kind, engine.TimerKindExit)
	}
	if zone.Countdown.TotalS != 30 {
		t.Errorf("countdown.total_s = %d, want 30", zone.Countdown.TotalS)
	}
	if zone.Countdown.RemainingS <= 0 || zone.Countdown.RemainingS > 30 {
		t.Errorf("countdown.remaining_s = %d, want in (0, 30]", zone.Countdown.RemainingS)
	}
}

// TestAlarmState_WalkTestActiveReflected verifies a running walk-test
// session flips walktest_active on the zone's status.
func TestAlarmState_WalkTestActiveReflected(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(0, 0, 60))

	if err := fx.eng.WalkTestStart(context.Background(), "eg", "tester", "test"); err != nil {
		t.Fatalf("walk test start: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/state", http.NoBody)
	w := httptest.NewRecorder()
	AlarmState(fx).ServeHTTP(w, req)

	var body alarmStateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.Zones[0].WalkTestActive {
		t.Error("walktest_active = false, want true")
	}
}

// --- GetAlarmZoneReadiness ---

// TestGetAlarmZoneReadiness_UnknownZone_Returns404 verifies the readiness
// endpoint answers the shared 404 for an unenrolled zone id.
func TestGetAlarmZoneReadiness_UnknownZone_Returns404(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/zones/missing/readiness", http.NoBody)
	req = withChiParam(req, "id", "missing")
	w := httptest.NewRecorder()
	GetAlarmZoneReadiness(fx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// TestGetAlarmZoneReadiness_ReturnsPerModeVerdict verifies the endpoint
// renders one readiness verdict per configured mode, keyed by mode name.
func TestGetAlarmZoneReadiness_ReturnsPerModeVerdict(t *testing.T) {
	t.Parallel()
	fx := newAlarmPanelFixture(t)
	fx.seedZone("eg", "Erdgeschoss", fullModeZoneConfig(30, 15, 60))
	fx.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/zones/eg/readiness", http.NoBody)
	req = withChiParam(req, "id", "eg")
	w := httptest.NewRecorder()
	GetAlarmZoneReadiness(fx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var body map[string]hmapi.AlarmModeReadiness
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rd, ok := body[string(hmenum.AlarmModeFull)]
	if !ok {
		t.Fatalf("missing full mode in response: %+v", body)
	}
	if !rd.Ready {
		t.Errorf("ready = false, want true (window closed, no blockers)")
	}
}
