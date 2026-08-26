// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stubMotionReset stands in for the channel lookup: `supported` names
// the sensors whose channel exposes a writable RESET_MOTION.
type stubMotionReset struct {
	mu        sync.Mutex
	supported map[string]bool
	failFor   map[string]bool
	writes    []string
}

func newStubMotionReset(supported ...string) *stubMotionReset {
	m := &stubMotionReset{supported: map[string]bool{}, failFor: map[string]bool{}}
	for _, id := range supported {
		m.supported[id] = true
	}
	return m
}

func (m *stubMotionReset) Supports(row sqlitestore.AlarmSensorRow) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.supported[row.ID]
}

func (m *stubMotionReset) Reset(_ context.Context, row sqlitestore.AlarmSensorRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes = append(m.writes, row.ID)
	if m.failFor[row.ID] {
		return errStubResetFailed
	}
	return nil
}

var errStubResetFailed = &stubResetError{}

type stubResetError struct{}

func (*stubResetError) Error() string { return "device unreachable" }

// motionFixture seeds one zone with a latched, resettable motion sensor
// and a latched door contact that has no RESET_MOTION.
func motionFixture(t *testing.T, reset *stubMotionReset) *alarmPanelFixture {
	t.Helper()
	f := newAlarmPanelFixtureWithMotionReset(t, reset)
	f.seedZone("eg", "Erdgeschoss", engine.ZoneConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{hmenum.AlarmModeFull: {}},
	})
	f.seedSensor("motion", "eg", hmenum.AlarmSensorTypeMotion, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	f.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	f.eng.HandleSensorEvent(context.Background(), "motion", true)
	f.eng.HandleSensorEvent(context.Background(), "door", true)
	return f
}

// postMotionReset drives one reset route and decodes the result.
func postMotionReset(t *testing.T, h http.HandlerFunc, path, zoneID string) hmapi.AlarmMotionResetResult {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, http.NoBody)
	if zoneID != "" {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", zoneID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out hmapi.AlarmMotionResetResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// TestListAlarmTriggeredMotionReportsOnlyResettableSensors pins that the
// listing names exactly what a reset would act on. A door contact reads
// as open for a real reason and has nothing to reset.
func TestListAlarmTriggeredMotionReportsOnlyResettableSensors(t *testing.T) {
	f := motionFixture(t, newStubMotionReset("motion"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alarm/triggered-motion", http.NoBody)
	rec := httptest.NewRecorder()
	ListAlarmTriggeredMotion(f)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out []hmapi.AlarmTriggeredMotionSensor
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0].SensorID != "motion" {
		t.Fatalf("sensors = %+v, want only the motion sensor", out)
	}
	if out[0].ZoneID != "eg" || out[0].ChannelAddress == "" || out[0].Parameter == "" {
		t.Errorf("sensor is missing its scope fields: %+v", out[0])
	}
}

// TestListAlarmTriggeredMotionScopesToOneZone pins the query parameter.
func TestListAlarmTriggeredMotionScopesToOneZone(t *testing.T) {
	f := motionFixture(t, newStubMotionReset("motion"))

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/alarm/triggered-motion?zone_id=other", http.NoBody)
	rec := httptest.NewRecorder()
	ListAlarmTriggeredMotion(f)(rec, req)

	var out []hmapi.AlarmTriggeredMotionSensor
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("sensors = %+v, want none for an unrelated zone", out)
	}
}

// TestResetAlarmZoneMotionClearsTheZone pins the per-zone verb end to
// end: the response counts what it did and the write reached the port.
func TestResetAlarmZoneMotionClearsTheZone(t *testing.T) {
	reset := newStubMotionReset("motion")
	f := motionFixture(t, reset)

	out := postMotionReset(t, ResetAlarmZoneMotion(f, nil),
		"/api/v1/alarm/zones/eg/reset-motion", "eg")
	if out.Reset != 1 || out.Failed != 0 {
		t.Errorf("result = %+v, want 1 reset / 0 failed", out)
	}
	if len(out.Sensors) != 1 || out.Sensors[0].SensorID != "motion" {
		t.Errorf("sensors = %+v, want the motion sensor named", out.Sensors)
	}
	reset.mu.Lock()
	defer reset.mu.Unlock()
	if len(reset.writes) != 1 || reset.writes[0] != "motion" {
		t.Errorf("writes = %v, want only [motion]", reset.writes)
	}
}

// TestResetAllAlarmMotionCoversEveryZone pins the fleet-wide verb.
func TestResetAllAlarmMotionCoversEveryZone(t *testing.T) {
	reset := newStubMotionReset("motion")
	f := motionFixture(t, reset)

	out := postMotionReset(t, ResetAllAlarmMotion(f, nil), "/api/v1/alarm/reset-motion", "")
	if out.Reset != 1 {
		t.Errorf("result = %+v, want 1 reset", out)
	}
}

// TestResetAlarmMotionReportsPartialFailure pins that a device that did
// not answer is reported in the body rather than as an HTTP error. The
// verb ran; "three of four cleared" is something an operator has to be
// able to see.
func TestResetAlarmMotionReportsPartialFailure(t *testing.T) {
	reset := newStubMotionReset("motion")
	reset.failFor["motion"] = true
	f := motionFixture(t, reset)

	out := postMotionReset(t, ResetAlarmZoneMotion(f, nil),
		"/api/v1/alarm/zones/eg/reset-motion", "eg")
	if out.Reset != 0 || out.Failed != 1 {
		t.Errorf("result = %+v, want 0 reset / 1 failed", out)
	}
}

// TestResetAlarmZoneMotionOnAQuietZone pins the no-op: an empty result,
// still 200, and no write.
func TestResetAlarmZoneMotionOnAQuietZone(t *testing.T) {
	reset := newStubMotionReset("motion")
	f := newAlarmPanelFixtureWithMotionReset(t, reset)
	f.seedZone("eg", "Erdgeschoss", engine.ZoneConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{hmenum.AlarmModeFull: {}},
	})

	out := postMotionReset(t, ResetAlarmZoneMotion(f, nil),
		"/api/v1/alarm/zones/eg/reset-motion", "eg")
	if out.Reset != 0 || out.Failed != 0 || len(out.Sensors) != 0 {
		t.Errorf("result = %+v, want an empty pass", out)
	}
	reset.mu.Lock()
	defer reset.mu.Unlock()
	if len(reset.writes) != 0 {
		t.Errorf("writes = %v, want none on a quiet zone", reset.writes)
	}
}

// TestResetAlarmZoneMotionOnAnUnknownZone pins that an unknown zone is
// a no-op rather than a fleet-wide reset — the empty-scope form means
// "every zone" inside the engine, so a mistyped id must not fall
// through to it.
func TestResetAlarmZoneMotionOnAnUnknownZone(t *testing.T) {
	reset := newStubMotionReset("motion")
	f := motionFixture(t, reset)

	out := postMotionReset(t, ResetAlarmZoneMotion(f, nil),
		"/api/v1/alarm/zones/nope/reset-motion", "nope")
	if out.Reset != 0 || len(out.Sensors) != 0 {
		t.Errorf("result = %+v, want an empty pass for an unknown zone", out)
	}
	reset.mu.Lock()
	defer reset.mu.Unlock()
	if len(reset.writes) != 0 {
		t.Errorf("writes = %v, want none for an unknown zone", reset.writes)
	}
}
