// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	alarmpkg "github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/model/alarmpanel"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// alarmPanelDBOpenMu serialises sqlitestore.Open calls across parallel
// tests in this file — the goose migration bootstrap touches
// library-global state, the same reason internal/alarm/engine's own
// harness_test.go guards Open with a package-level mutex.
var alarmPanelDBOpenMu sync.Mutex

// alarmPanelHarness is a real *engine.Engine over a temp-file SQLite
// store bundle, wired directly as the AlarmPanelQuery facade the
// alarm_panel.* command family consumes (it implements Engine() and
// Stores() itself). Deliberately not a mock of the engine: the wire
// mapping under test (alarmAreaStatus, alarmReadinessDTO,
// alarmEngineError) only proves itself against real state-machine
// transitions, not a hand-rolled stand-in that could silently drift
// from the engine's actual behavior.
type alarmPanelHarness struct {
	t      *testing.T
	ctx    context.Context
	db     *sql.DB
	stores *alarmpkg.Stores
	eng    *engine.Engine
}

func newAlarmPanelHarness(t *testing.T) *alarmPanelHarness {
	t.Helper()
	dsn := sqlitestore.FileDSN(filepath.Join(t.TempDir(), "alarm-panel-ws.db"))
	alarmPanelDBOpenMu.Lock()
	db, err := sqlitestore.Open(context.Background(), dsn)
	alarmPanelDBOpenMu.Unlock()
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stores := alarmpkg.NewStores(db)
	eng, err := engine.New(engine.Deps{
		Areas:     stores.Areas,
		Sensors:   stores.Sensors,
		State:     stores.State,
		Incidents: stores.Incidents,
		Runtime:   stores.Runtime,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return &alarmPanelHarness{t: t, ctx: context.Background(), db: db, stores: stores, eng: eng}
}

// Engine and Stores satisfy AlarmPanelQuery, so the harness itself can
// be passed directly as AlarmPanelCommandsConfig.Panel.
func (h *alarmPanelHarness) Engine() *engine.Engine   { return h.eng }
func (h *alarmPanelHarness) Stores() *alarmpkg.Stores { return h.stores }

// Panels serves an empty projection — the entity view is exercised by
// the service-level tests; the command handler only needs the method.
func (h *alarmPanelHarness) Panels() []alarmpanel.Panel { return nil }

// stubbedPanelHarness substitutes a fixed Panels() result over an
// otherwise real alarmPanelHarness. alarm_panel.panels only calls
// Panels(), so the rest of the AlarmPanelQuery facade can stay the
// harness's real engine/stores.
type stubbedPanelHarness struct {
	*alarmPanelHarness
	panels []alarmpanel.Panel
}

func (s stubbedPanelHarness) Panels() []alarmpanel.Panel { return s.panels }

// seedArea persists one area config row.
func (h *alarmPanelHarness) seedArea(id, name string, cfg engine.AreaConfig) {
	h.t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		h.t.Fatalf("marshal area config: %v", err)
	}
	now := time.Now().UnixMilli()
	if err := h.stores.Areas.Upsert(h.ctx, sqlitestore.AlarmAreaRow{
		ID: id, Name: name, ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		h.t.Fatalf("seed area: %v", err)
	}
}

// seedSensor persists one enrolled sensor row bound to a synthetic
// channel address derived from its id (no real device is needed —
// HandleSensorEvent is driven directly in these tests).
func (h *alarmPanelHarness) seedSensor(id, areaID string, typ hmenum.AlarmSensorType, cfg engine.SensorConfig) {
	h.t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		h.t.Fatalf("marshal sensor config: %v", err)
	}
	now := time.Now().UnixMilli()
	if err := h.stores.Sensors.Upsert(h.ctx, sqlitestore.AlarmSensorRow{
		ID: id, AreaID: areaID, CentralName: "ccu-test", InterfaceID: "HmIP-RF",
		ChannelAddress: id + ":1", Parameter: "STATE", SensorType: typ,
		Name: id, ConfigJSON: string(b), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		h.t.Fatalf("seed sensor: %v", err)
	}
}

// start loads the seeded config into the engine. Cleanup stops it.
func (h *alarmPanelHarness) start() {
	h.t.Helper()
	if err := h.eng.Start(h.ctx); err != nil {
		h.t.Fatalf("engine.Start: %v", err)
	}
	h.t.Cleanup(func() { h.eng.Stop(h.ctx) })
}

// router wires just the alarm_panel.* family onto a fresh Router.
func (h *alarmPanelHarness) router() *Router {
	r := NewRouter()
	RegisterAlarmPanelCommands(r, AlarmPanelCommandsConfig{Panel: h})
	return r
}

// dispatchJSON marshals body (nil for no-args commands) and dispatches
// command through r under ctx.
func dispatchJSON(ctx context.Context, t *testing.T, r *Router, command string, body any) Result {
	t.Helper()
	var raw json.RawMessage
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal args for %s: %v", command, err)
		}
		raw = b
	}
	return r.Dispatch(ctx, command, raw)
}

// oneModeAreaConfig is the minimal single-mode area configuration
// shared by the dispatch tests below.
func oneModeAreaConfig() engine.AreaConfig {
	return engine.AreaConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{
			hmenum.AlarmModeFull: {TriggerSeconds: 60},
		},
	}
}

// TestAlarmPanelArmAndDisarmDispatch drives alarm_panel.arm (with
// skip_delay so the transition is synchronous) then alarm_panel.disarm
// through the real engine and asserts both the wire response and the
// resulting engine state.
func TestAlarmPanelArmAndDisarmDispatch(t *testing.T) {
	h := newAlarmPanelHarness(t)
	h.seedArea("eg", "Erdgeschoss", oneModeAreaConfig())
	h.start()
	r := h.router()

	res := dispatchJSON(opCtx(), t, r, "alarm_panel.arm", map[string]any{
		"area_id": "eg", "mode": "full", "skip_delay": true,
	})
	if res.Error != nil {
		t.Fatalf("arm: %+v", res.Error)
	}
	accepted, ok := res.Data.(hmapi.AlarmArmAccepted)
	if !ok {
		t.Fatalf("arm data type %T, want hmapi.AlarmArmAccepted", res.Data)
	}
	if accepted.State != string(hmenum.AlarmAreaStateArmed) {
		t.Fatalf("arm response state = %q, want armed", accepted.State)
	}
	if snap, ok := h.eng.Area("eg"); !ok || snap.State != hmenum.AlarmAreaStateArmed {
		t.Fatalf("engine snapshot after arm = %+v", snap)
	}

	res = dispatchJSON(opCtx(), t, r, "alarm_panel.disarm", map[string]any{"area_id": "eg"})
	if res.Error != nil {
		t.Fatalf("disarm: %+v", res.Error)
	}
	data, ok := res.Data.(map[string]any)
	if !ok || data["disarmed"] != true || data["area_id"] != "eg" {
		t.Fatalf("disarm data = %+v", res.Data)
	}
	if snap, ok := h.eng.Area("eg"); !ok || snap.State != hmenum.AlarmAreaStateDisarmed {
		t.Fatalf("engine snapshot after disarm = %+v", snap)
	}
}

// TestAlarmPanelArmUnknownAreaIsNotFound asserts the engine's
// ErrUnknownArea maps to the not_found command error.
func TestAlarmPanelArmUnknownAreaIsNotFound(t *testing.T) {
	h := newAlarmPanelHarness(t)
	h.start()
	r := h.router()

	res := dispatchJSON(opCtx(), t, r, "alarm_panel.arm", map[string]any{
		"area_id": "missing", "mode": "full",
	})
	if res.Error == nil || res.Error.Code != "not_found" {
		t.Fatalf("arm unknown area = %+v, want not_found", res.Error)
	}
}

// TestAlarmPanelSilenceDispatch trips an instant (non-entry-delay)
// sensor into a live incident, silences it over the command family,
// and asserts both the wire response and the engine-side silenced
// flag — the same real-transition contract as the arm/disarm test.
func TestAlarmPanelSilenceDispatch(t *testing.T) {
	h := newAlarmPanelHarness(t)
	h.seedArea("eg", "Erdgeschoss", oneModeAreaConfig())
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	h.start()
	r := h.router()

	if res := dispatchJSON(opCtx(), t, r, "alarm_panel.arm", map[string]any{
		"area_id": "eg", "mode": "full", "skip_delay": true,
	}); res.Error != nil {
		t.Fatalf("arm: %+v", res.Error)
	}

	h.eng.HandleSensorEvent(h.ctx, "window", true)
	if snap, ok := h.eng.Area("eg"); !ok || snap.State != hmenum.AlarmAreaStateTriggered {
		t.Fatalf("expected triggered after sensor activation, got %+v", snap)
	}

	res := dispatchJSON(opCtx(), t, r, "alarm_panel.silence", map[string]any{"area_id": "eg"})
	if res.Error != nil {
		t.Fatalf("silence: %+v", res.Error)
	}
	data, ok := res.Data.(map[string]any)
	if !ok || data["silenced"] != true {
		t.Fatalf("silence data = %+v", res.Data)
	}
	if snap, ok := h.eng.Area("eg"); !ok || !snap.IncidentSilenced {
		t.Fatalf("expected incident silenced on engine snapshot, got %+v", snap)
	}
}

// TestAlarmPanelSilenceWithoutIncidentSucceeds pins the engine's S3/S6
// "silence is ungated" invariant (engine.Engine.silenceLocked): unlike
// acknowledge, silencing an area with no open incident is not an
// error — it still issues a defensive StopAll and succeeds, since
// stopping more than strictly necessary is always the safe direction.
func TestAlarmPanelSilenceWithoutIncidentSucceeds(t *testing.T) {
	h := newAlarmPanelHarness(t)
	h.seedArea("eg", "Erdgeschoss", oneModeAreaConfig())
	h.start()
	r := h.router()

	res := dispatchJSON(opCtx(), t, r, "alarm_panel.silence", map[string]any{"area_id": "eg"})
	if res.Error != nil {
		t.Fatalf("silence without incident = %+v, want success", res.Error)
	}
	data, ok := res.Data.(map[string]any)
	if !ok || data["silenced"] != true || data["area_id"] != "eg" {
		t.Fatalf("silence data = %+v", res.Data)
	}
}

// TestAlarmPanelAcknowledgeWithoutIncidentIsConflict asserts the
// engine's ErrNoIncident maps to the conflict command error —
// acknowledge, unlike silence, requires a live incident.
func TestAlarmPanelAcknowledgeWithoutIncidentIsConflict(t *testing.T) {
	h := newAlarmPanelHarness(t)
	h.seedArea("eg", "Erdgeschoss", oneModeAreaConfig())
	h.start()
	r := h.router()

	res := dispatchJSON(opCtx(), t, r, "alarm_panel.acknowledge", map[string]any{"area_id": "eg"})
	if res.Error == nil || res.Error.Code != "conflict" {
		t.Fatalf("acknowledge without incident = %+v, want conflict", res.Error)
	}
}

// TestAlarmPanelSilenceAllAndAcknowledgeDispatch covers the two
// remaining write verbs against a live incident: acknowledge marks it
// seen, silence_all silences every area's incident (here, the one
// area) in a single call.
func TestAlarmPanelSilenceAllAndAcknowledgeDispatch(t *testing.T) {
	h := newAlarmPanelHarness(t)
	h.seedArea("eg", "Erdgeschoss", oneModeAreaConfig())
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	h.start()
	r := h.router()

	if res := dispatchJSON(opCtx(), t, r, "alarm_panel.arm", map[string]any{
		"area_id": "eg", "mode": "full", "skip_delay": true,
	}); res.Error != nil {
		t.Fatalf("arm: %+v", res.Error)
	}
	h.eng.HandleSensorEvent(h.ctx, "window", true)

	res := dispatchJSON(opCtx(), t, r, "alarm_panel.acknowledge", map[string]any{"area_id": "eg"})
	if res.Error != nil {
		t.Fatalf("acknowledge: %+v", res.Error)
	}

	res = dispatchJSON(opCtx(), t, r, "alarm_panel.silence_all", nil)
	if res.Error != nil {
		t.Fatalf("silence_all: %+v", res.Error)
	}
	if snap, ok := h.eng.Area("eg"); !ok || !snap.IncidentSilenced {
		t.Fatalf("expected incident silenced via silence_all, got %+v", snap)
	}
}

// alarmPanelWriteCommands is every alarm_panel.* state-changing
// command, paired with a minimal valid argument body. Used by the
// role-gate test below.
var alarmPanelWriteCommands = []struct {
	name string
	args string
}{
	{"alarm_panel.arm", `{"area_id":"eg","mode":"full"}`},
	{"alarm_panel.disarm", `{"area_id":"eg"}`},
	{"alarm_panel.silence", `{"area_id":"eg"}`},
	{"alarm_panel.silence_all", `{}`},
	{"alarm_panel.acknowledge", `{"area_id":"eg"}`},
}

// TestAlarmPanelWriteCommandsRequireOperatorRole mirrors the
// unauthenticated/viewer/operator progression role_gate_test.go
// applies to the other write-command families: an unattributed
// context is unauthorized, a viewer identity is forbidden, and an
// operator identity clears the gate (the handler may still fail for
// an unrelated reason against the nil-accessor stub — only the auth
// outcome is asserted here, matching
// TestOperatorCanInvokeOperatorCommands's contract). Uses
// stubAlarmPanel (role_gate_test.go) since the role gate runs before
// Dispatch ever calls into the facade.
func TestAlarmPanelWriteCommandsRequireOperatorRole(t *testing.T) {
	r := NewRouter()
	RegisterAlarmPanelCommands(r, AlarmPanelCommandsConfig{Panel: stubAlarmPanel{}})

	for _, tc := range alarmPanelWriteCommands {
		t.Run(tc.name, func(t *testing.T) {
			res := r.Dispatch(context.Background(), tc.name, json.RawMessage(tc.args))
			if res.Error == nil || res.Error.Code != CommandErrorUnauthorized {
				t.Fatalf("unauthenticated dispatch = %+v, want unauthorized", res.Error)
			}
			res = r.Dispatch(viewerCtx(), tc.name, json.RawMessage(tc.args))
			if res.Error == nil || res.Error.Code != CommandErrorForbidden {
				t.Fatalf("viewer dispatch = %+v, want forbidden", res.Error)
			}
			res = r.Dispatch(opCtx(), tc.name, json.RawMessage(tc.args))
			if res.Error != nil && (res.Error.Code == CommandErrorUnauthorized || res.Error.Code == CommandErrorForbidden) {
				t.Fatalf("operator dispatch blocked by role gate: %+v", res.Error)
			}
		})
	}
}

// TestAlarmPanelStateAndReadinessDispatch exercises the two read
// commands the task calls out explicitly: alarm_panel.state renders
// every configured area (no role gate — reads stay open to any
// authenticated caller, mirrored here with a bare context), and
// alarm_panel.readiness renders one area's per-mode verdict and
// requires area_id.
func TestAlarmPanelStateAndReadinessDispatch(t *testing.T) {
	h := newAlarmPanelHarness(t)
	h.seedArea("eg", "Erdgeschoss", oneModeAreaConfig())
	h.start()
	r := h.router()

	res := r.Dispatch(context.Background(), "alarm_panel.state", nil)
	if res.Error != nil {
		t.Fatalf("state: %+v", res.Error)
	}
	data, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("state data type %T, want map[string]any", res.Data)
	}
	areas, ok := data["areas"].([]hmapi.AlarmAreaStatus)
	if !ok || len(areas) != 1 {
		t.Fatalf("areas = %+v", data["areas"])
	}
	if areas[0].ID != "eg" || areas[0].State != string(hmenum.AlarmAreaStateDisarmed) {
		t.Fatalf("area status = %+v, want id=eg state=disarmed", areas[0])
	}

	res = dispatchJSON(context.Background(), t, r, "alarm_panel.readiness", map[string]any{"area_id": "eg"})
	if res.Error != nil {
		t.Fatalf("readiness: %+v", res.Error)
	}
	rdata, ok := res.Data.(map[string]any)
	if !ok || rdata["area_id"] != "eg" {
		t.Fatalf("readiness data = %+v", res.Data)
	}

	res = dispatchJSON(context.Background(), t, r, "alarm_panel.readiness", map[string]any{})
	if res.Error == nil || res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("readiness without area_id = %+v, want bad_request", res.Error)
	}

	res = dispatchJSON(context.Background(), t, r, "alarm_panel.readiness", map[string]any{"area_id": "missing"})
	if res.Error == nil || res.Error.Code != "not_found" {
		t.Fatalf("readiness on unknown area = %+v, want not_found", res.Error)
	}
}

// TestAlarmPanelPanelsDispatchMapsCodePolicyFlags verifies the
// alarm_panel.panels command handler carries a panel's effective
// code-policy flags onto the wire hmapi.AlarmPanelEntity, the same
// projection GET /alarm/panels and MQTT discovery serve.
func TestAlarmPanelPanelsDispatchMapsCodePolicyFlags(t *testing.T) {
	h := newAlarmPanelHarness(t)
	h.start()
	stub := stubbedPanelHarness{
		alarmPanelHarness: h,
		panels: []alarmpanel.Panel{
			{
				UniqueID:           alarmpanel.PanelUniqueID("eg"),
				AreaID:             "eg",
				Name:               "Erdgeschoss",
				State:              "disarmed",
				Available:          true,
				CodeArmRequired:    true,
				CodeDisarmRequired: false,
			},
		},
	}
	r := NewRouter()
	RegisterAlarmPanelCommands(r, AlarmPanelCommandsConfig{Panel: stub})

	res := r.Dispatch(context.Background(), "alarm_panel.panels", nil)
	if res.Error != nil {
		t.Fatalf("panels: %+v", res.Error)
	}
	data, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type %T, want map[string]any", res.Data)
	}
	panels, ok := data["panels"].([]hmapi.AlarmPanelEntity)
	if !ok || len(panels) != 1 {
		t.Fatalf("panels = %+v", data["panels"])
	}
	p := panels[0]
	if p.AreaID != "eg" {
		t.Fatalf("area_id = %q, want eg", p.AreaID)
	}
	if !p.CodeArmRequired {
		t.Errorf("code_arm_required = false, want true")
	}
	if p.CodeDisarmRequired {
		t.Errorf("code_disarm_required = true, want false")
	}
}
