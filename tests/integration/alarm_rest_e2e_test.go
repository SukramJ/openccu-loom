// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// End-to-end test for the /api/v1/alarm REST surface
// (internal/north/rest/handlers/alarm.go + alarm_config.go, routed in
// internal/north/rest/router.go) against the same in-process
// central/godevccu stack alarm_engine_e2e_test.go drives at the
// engine level. Where that file calls engine.Engine verbs directly,
// this one walks the same shape (create area, enroll a sensor, arm,
// trip it, silence) entirely over HTTP: POST /alarm/areas, PUT
// .../sensors, POST .../arm, GET /alarm/state polled to triggered,
// POST .../silence, then the persisted incident is read back from the
// store to confirm the silence landed.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// alarmRestHarness wraps an alarmHarness (alarm_engine_e2e_test.go)
// with a REST httptest.Server bound to the same started alarm.Service,
// so the router's /alarm/* routes can be driven end to end over real
// HTTP against the same central/godevccu stack the engine-level tests
// use.
//
// The router is built with only the Alarm dependency (plus a
// fixed-identity auth chain) wired — not the full production Deps
// literal from cmd/openccu-loom/daemon_rest_mount.go. That literal
// carries ~50 unrelated collaborators (devices, hub, links,
// schedules, …); the behaviour under test is scoped to /alarm/*, so
// pulling in the rest of the daemon composition root would be
// disproportionate for what this test verifies.
type alarmRestHarness struct {
	*alarmHarness
	api    *httptest.Server
	client *http.Client
}

// newAlarmRestHarness builds the central + registry + stores + a
// started alarm.Service via newAlarmHarness/start, then wraps it in a
// REST router reachable over HTTP. Every request is resolved to a
// fixed operator identity — auth chain behaviour (login, sessions,
// roles) is covered elsewhere (tests/integration/admin_e2e_test.go,
// the ws role-gate tests); this harness only needs enough identity to
// clear the router's operator-gated /alarm/* mutations.
func newAlarmRestHarness(t *testing.T) *alarmRestHarness {
	t.Helper()
	ah := newAlarmHarness(t)
	ah.start()

	mw := auth.NewMiddleware(nil, nil)
	operatorResolve := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.ContextWithIdentity(r.Context(), auth.Identity{Subject: "test-operator", Role: auth.RoleOperator})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	router := rest.NewRouter(rest.Deps{
		StartedAt:       time.Now(),
		Alarm:           ah.svc,
		AuditRecorder:   audit.NewBuffer(100),
		AuthResolve:     operatorResolve,
		AuthRequire:     mw.Require,
		RequireOperator: func(next http.Handler) http.Handler { return mw.RequireRole(auth.RoleOperator, next) },
	})
	api := httptest.NewServer(router)
	t.Cleanup(api.Close)

	return &alarmRestHarness{alarmHarness: ah, api: api, client: &http.Client{Timeout: 10 * time.Second}}
}

// do issues one JSON request against the harness's REST listener and
// decodes a JSON response body into out (when out is non-nil and the
// body is non-empty). Fails the test on transport errors only —
// status-code assertions stay with the caller so failure messages can
// name the specific step.
func (h *alarmRestHarness) do(method, path string, body, out any) *http.Response {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal %s %s body: %v", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.api.URL+"/api/v1"+path, rdr)
	if err != nil {
		h.t.Fatalf("build %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		h.t.Fatalf("read %s %s body: %v", method, path, err)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			h.t.Fatalf("decode %s %s body %q: %v", method, path, raw, err)
		}
	}
	res.Body = io.NopCloser(bytes.NewReader(raw))
	return res
}

// waitAlarmState polls GET /alarm/state until areaID reports want or
// the timeout elapses, returning the last observed state.
func (h *alarmRestHarness) waitAlarmState(areaID string, want hmenum.AlarmAreaState, timeout time.Duration) hmenum.AlarmAreaState {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	var last hmenum.AlarmAreaState
	for {
		var body struct {
			Areas []hmapi.AlarmAreaStatus `json:"areas"`
		}
		res := h.do(http.MethodGet, "/alarm/state", nil, &body)
		if res.StatusCode != http.StatusOK {
			h.t.Fatalf("GET /alarm/state: status %d", res.StatusCode)
		}
		for _, a := range body.Areas {
			if a.ID != areaID {
				continue
			}
			last = hmenum.AlarmAreaState(a.State)
			if last == want {
				return last
			}
		}
		if time.Now().After(deadline) {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAlarmRestFullChainCreateArmTriggerSilence walks the whole
// management + arm-cycle surface over HTTP: create an area, enroll the
// SWDO window contact as a sensor, arm it, trip the window open on the
// wire, poll the live state to triggered, silence over REST, and
// confirm the incident is persisted silenced.
func TestAlarmRestFullChainCreateArmTriggerSilence(t *testing.T) {
	h := newAlarmRestHarness(t)
	ctx := context.Background()
	stateKey := h.swdoStateKey()

	// 1. Create the area via POST /alarm/areas.
	areaCfg, err := json.Marshal(engine.AreaConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{
			hmenum.AlarmModeFull: {TriggerSeconds: 30},
		},
	})
	if err != nil {
		t.Fatalf("marshal area config: %v", err)
	}
	var area hmapi.AlarmArea
	res := h.do(http.MethodPost, "/alarm/areas", hmapi.AlarmArea{
		Name: "Erdgeschoss", Config: areaCfg,
	}, &area)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST /alarm/areas: status %d", res.StatusCode)
	}
	if area.ID == "" {
		t.Fatal("POST /alarm/areas: response carried no server-generated id")
	}

	// 2. Enroll the SWDO window contact via PUT .../sensors (bulk
	// replace, a raw array body).
	sensorCfg, err := json.Marshal(engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	if err != nil {
		t.Fatalf("marshal sensor config: %v", err)
	}
	res = h.do(http.MethodPut, "/alarm/areas/"+area.ID+"/sensors", []hmapi.AlarmSensor{{
		Central:        h.centralName(),
		InterfaceID:    stateKey.InterfaceID,
		ChannelAddress: stateKey.ChannelAddress,
		Parameter:      stateKey.Parameter,
		Type:           string(hmenum.AlarmSensorTypeWindow),
		Name:           "Window",
		Config:         sensorCfg,
	}}, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT /alarm/areas/%s/sensors: status %d", area.ID, res.StatusCode)
	}

	// 3. Arm via POST .../arm (skip_delay so the transition is
	// synchronous — no exit-delay wait needed).
	var accepted hmapi.AlarmArmAccepted
	res = h.do(http.MethodPost, "/alarm/areas/"+area.ID+"/arm", hmapi.AlarmArmRequest{
		Mode: string(hmenum.AlarmModeFull), SkipDelay: true,
	}, &accepted)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST /alarm/areas/%s/arm: status %d", area.ID, res.StatusCode)
	}
	if accepted.State != string(hmenum.AlarmAreaStateArmed) {
		t.Fatalf("arm response state = %q, want armed", accepted.State)
	}

	// 4. Open the window on the wire and poll GET /alarm/state until
	// the area reports triggered.
	h.h.resetEvents()
	h.injectWindow(stateKey, true)
	if st := h.waitAlarmState(area.ID, hmenum.AlarmAreaStateTriggered, 3*time.Second); st != hmenum.AlarmAreaStateTriggered {
		t.Fatalf("area state after window open = %q, want triggered", st)
	}

	// 5. Silence via REST.
	res = h.do(http.MethodPost, "/alarm/areas/"+area.ID+"/silence", nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /alarm/areas/%s/silence: status %d", area.ID, res.StatusCode)
	}

	// 6. The incident is persisted silenced (the store-level assertion
	// alarm_engine_e2e_test.go also makes after its engine-level
	// silence call).
	inc, ok, err := h.stores.Incidents.GetOpenByArea(ctx, area.ID)
	if err != nil {
		t.Fatalf("get open incident: %v", err)
	}
	if !ok {
		t.Fatal("expected an open incident after silence (state stays triggered)")
	}
	if !inc.Silenced {
		t.Fatal("incident row is not marked silenced after REST silence")
	}
}
