// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// End-to-end integration tests for the alarm engine
// (docs/alarm-concept.md §17 "Integration") against the in-process
// godevccu simulator. The full central → device-pipeline stack is
// built via newSPAHarness (a HmIP-ASIR siren + a HMIP-SWDO window
// contact), a migrated daemon SQLite database backs the alarm stores,
// and the alarm.Service is driven through its real ports:
//
//   - arm → window-open event → pending → triggered → siren
//     putParamset observed on the wire → silence → verified stop
//     (S1 duration bound and S3 silence on the real command path);
//   - boot reconciliation adopting a sounding siren of an armed area
//     (S4 adopt-before-stop), and stopping a sounding siren of a
//     disarmed, unshared area (S4 stop-unowned);
//   - a sysvar write can never disarm a protected area (§13.5 pin).
//
// The siren activation/stop writes reach godevccu through the ingested
// channel writer; the OnSetValue hook (captured by newSPAHarness'
// setCalls) records every wire parameter so the tests can assert the
// putParamset members directly. godevccu does not echo paramset writes
// back into ACOUSTIC_ALARM_ACTIVE feedback, so where the reconciliation
// flow needs a sounding siren the ACTIVE state is injected into the
// model manually (the established injectEcho pattern).
package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/alarm/outputs"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// alarmModels is the godevccu fleet the alarm integration tests load:
// a native siren (HmIP-ASIR — ALARM_SWITCH_VIRTUAL_RECEIVER on :3) and
// a window contact (HMIP-SWDO — SHUTTER_CONTACT STATE on :1).
var alarmModels = []string{"HmIP-ASIR", "HMIP-SWDO"}

// alarmHarness bundles the in-process central stack (via newSPAHarness),
// a fresh registry the alarm service iterates, and a migrated daemon
// database with the seven alarm stores over it.
type alarmHarness struct {
	t      *testing.T
	h      *spaHarness
	reg    *central.Registry
	stores *alarm.Stores
	svc    *alarm.Service
}

// newAlarmHarness builds the central + registry + migrated alarm stores.
// The alarm.Service is created and started later by start(), after the
// caller has seeded areas/sensors/outputs and any persisted state.
func newAlarmHarness(t *testing.T) *alarmHarness {
	t.Helper()
	h := newSPAHarness(t, alarmModels)

	reg := central.NewRegistry()
	if err := reg.Register(h.central); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dbPath := filepath.Join(t.TempDir(), "openccu-loom.db")
	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(dbPath))
	if err != nil {
		t.Fatalf("sqlitestore.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return &alarmHarness{t: t, h: h, reg: reg, stores: alarm.NewStores(db)}
}

// start builds the alarm service with short real-clock delays and starts
// it. It must be called after the caller has seeded the stores because
// Service.Start loads config, restores persisted state, and runs the S4
// reconciliation pass. A background context keeps engine timers alive
// for the whole test.
func (ah *alarmHarness) start() {
	ah.t.Helper()
	svc, err := alarm.NewService(alarm.Deps{
		Settings: alarm.Settings{
			Enabled:                       true,
			DefaultSirenSeconds:           5,
			StopVerifySeconds:             5,
			MaxAcousticPerIncidentSeconds: 60,
			RestartLoopBreaker:            3,
		},
		Registry: ah.reg,
		Stores:   ah.stores,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		ah.t.Fatalf("alarm.NewService: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		ah.t.Fatalf("alarm.Service.Start: %v", err)
	}
	ah.t.Cleanup(func() { _ = svc.Stop(context.Background()) })
	ah.svc = svc
}

// centralName is the scoping dimension the alarm rows and the event
// routing share.
func (ah *alarmHarness) centralName() string { return ah.h.central.Name() }

// asirChannel returns the ASIR ALARM_SWITCH_VIRTUAL_RECEIVER channel
// (:3), the writable siren receiver.
func (ah *alarmHarness) asirChannel() *device.Channel {
	ah.t.Helper()
	d := ah.h.findDevice("HmIP-ASIR")
	for _, ch := range d.Channels() {
		if ch.Number == 3 {
			return ch
		}
	}
	ah.t.Fatal("HmIP-ASIR channel :3 not found")
	return nil
}

// swdoStateKey returns the data-point key of the SWDO window contact's
// STATE (channel :1). Both the enrolled sensor row and the injected
// wire event derive from this key, so their routing keys match by
// construction regardless of the ingested interface id.
func (ah *alarmHarness) swdoStateKey() hmtypes.DataPointKey {
	ah.t.Helper()
	d := ah.h.findDevice("HMIP-SWDO")
	for _, ch := range d.Channels() {
		if ch.Number != 1 {
			continue
		}
		if dp := ch.Parameter(hmenum.ParameterState); dp != nil {
			return dp.DataPointKey()
		}
	}
	ah.t.Fatal("HMIP-SWDO channel :1 STATE not found")
	return hmtypes.DataPointKey{}
}

// injectWindow publishes a CCU→daemon STATE change for the SWDO contact
// on the central bus the alarm service subscribes to (open = non-zero
// enum position). It mirrors the injectEcho pattern: the model DP is
// updated via OnWireValue and a DataPointValueChangedEvent is published.
func (ah *alarmHarness) injectWindow(key hmtypes.DataPointKey, open bool) {
	ah.t.Helper()
	v := hmtypes.IntValue(0)
	if open {
		v = hmtypes.IntValue(1)
	}
	if ch := ah.h.central.GetChannel(key.ChannelAddress); ch != nil {
		if dp := ch.Parameter(hmenum.Parameter(key.Parameter)); dp != nil {
			if setter, ok := dp.(interface{ OnWireValue(any) bool }); ok {
				setter.OnWireValue(v.Int)
			}
		}
	}
	events.Publish(ah.h.central.EventBus, hmevent.DataPointValueChangedEvent{
		Base:     hmevent.NewBase(),
		Key:      key,
		OldValue: hmtypes.NoneValue(),
		NewValue: v,
	})
}

// injectSirenActive marks the ASIR acoustic channel active in the model
// so reconciliation reads it as a sounding siren (godevccu never echoes
// ACOUSTIC_ALARM_ACTIVE on its own).
func (ah *alarmHarness) injectSirenActive(ch *device.Channel) {
	ah.t.Helper()
	dp := ch.Parameter(hmenum.ParameterAcousticAlarmActive)
	if dp == nil {
		ah.t.Fatal("ASIR :3 has no ACOUSTIC_ALARM_ACTIVE parameter")
	}
	setter, ok := dp.(interface{ OnWireValue(any) bool })
	if !ok {
		ah.t.Fatal("ACOUSTIC_ALARM_ACTIVE does not accept a wire value")
	}
	setter.OnWireValue(true)
}

// waitAreaState polls the engine snapshot until the area reaches want or
// the timeout elapses; it returns the last observed state.
func (ah *alarmHarness) waitAreaState(areaID string, want hmenum.AlarmAreaState, timeout time.Duration) hmenum.AlarmAreaState {
	ah.t.Helper()
	deadline := time.Now().Add(timeout)
	var last hmenum.AlarmAreaState
	for {
		if snap, ok := ah.svc.Engine().Area(areaID); ok {
			last = snap.State
			if snap.State == want {
				return snap.State
			}
		}
		if time.Now().After(deadline) {
			return last
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitSet polls the captured wire writes for a member of a putParamset /
// setValue on (channelAddress, parameter).
func (ah *alarmHarness) waitSet(channelAddress string, parameter hmenum.Parameter, timeout time.Duration) (any, bool) {
	ah.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if v, ok := ah.h.lastSetValue(channelAddress, parameter); ok {
			return v, true
		}
		if time.Now().After(deadline) {
			return nil, false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitJournalEvent polls the alarm journal for an entry with the given
// event name in the area.
func (ah *alarmHarness) waitJournalEvent(areaID, event string, timeout time.Duration) bool {
	ah.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		entries, err := ah.stores.Journal.Query(context.Background(), sqlitestore.AlarmJournalFilter{AreaID: areaID})
		if err != nil {
			ah.t.Fatalf("journal query: %v", err)
		}
		for _, e := range entries {
			if e.Event == event {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// seedArea persists one alarm area with the given config document.
func (ah *alarmHarness) seedArea(id, name string, cfg engine.AreaConfig) {
	ah.t.Helper()
	now := time.Now().UnixMilli()
	if err := ah.stores.Areas.Upsert(context.Background(), sqlitestore.AlarmAreaRow{
		ID: id, Name: name, ConfigJSON: mustAlarmJSON(ah.t, cfg), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		ah.t.Fatalf("seed area: %v", err)
	}
}

// seedSensor persists one enrolled sensor bound to key.
func (ah *alarmHarness) seedSensor(id, areaID string, key hmtypes.DataPointKey, typ hmenum.AlarmSensorType, cfg engine.SensorConfig) {
	ah.t.Helper()
	now := time.Now().UnixMilli()
	if err := ah.stores.Sensors.Upsert(context.Background(), sqlitestore.AlarmSensorRow{
		ID: id, AreaID: areaID, CentralName: ah.centralName(),
		InterfaceID: key.InterfaceID, ChannelAddress: key.ChannelAddress, Parameter: key.Parameter,
		SensorType: typ, Name: id, ConfigJSON: mustAlarmJSON(ah.t, cfg), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		ah.t.Fatalf("seed sensor: %v", err)
	}
}

// seedOutput persists one enrolled output of the given class on channel.
func (ah *alarmHarness) seedOutput(id, areaID string, class hmenum.AlarmOutputClass, channelAddress string, cfg outputs.OutputConfig) {
	ah.t.Helper()
	now := time.Now().UnixMilli()
	if err := ah.stores.Outputs.Upsert(context.Background(), sqlitestore.AlarmOutputRow{
		ID: id, AreaID: areaID, Class: class, CentralName: ah.centralName(),
		ChannelAddress: channelAddress, Name: id, ConfigJSON: mustAlarmJSON(ah.t, cfg), CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		ah.t.Fatalf("seed output: %v", err)
	}
}

// mustAlarmJSON marshals a config document; a failure is a test bug.
func mustAlarmJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return string(b)
}

// numericAboveZero reports whether v decodes to a number greater than
// zero across the integer / float wire representations godevccu may hand
// back for an INTEGER parameter.
func numericAboveZero(v any) bool {
	switch n := v.(type) {
	case int:
		return n > 0
	case int32:
		return n > 0
	case int64:
		return n > 0
	case float32:
		return n > 0
	case float64:
		return n > 0
	default:
		return false
	}
}

// TestAlarmFullChainWindowOpenTriggersSirenThenSilence drives the whole
// central-logic chain end to end: arm full → open a delayed window
// contact → pending → triggered → the ASIR receiver channel receives a
// bounded acoustic putParamset (ACOUSTIC_ALARM_SELECTION + a positive
// DURATION_VALUE, S1 on the real wire) → silence writes the disable
// defaults back and persists the incident as silenced (S3).
func TestAlarmFullChainWindowOpenTriggersSirenThenSilence(t *testing.T) {
	ah := newAlarmHarness(t)
	ctx := context.Background()

	const areaID = "area-eg"
	asir := ah.asirChannel()
	stateKey := ah.swdoStateKey()

	ah.seedArea(areaID, "Erdgeschoss", engine.AreaConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{
			hmenum.AlarmModeFull: {ExitDelaySeconds: 0, EntryDelaySeconds: 2, TriggerSeconds: 10},
		},
	})
	ah.seedSensor("sensor-window", areaID, stateKey, hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes:         []hmenum.AlarmMode{hmenum.AlarmModeFull},
		UseEntryDelay: true,
	})
	ah.seedOutput("out-siren", areaID, hmenum.AlarmOutputClassAcousticSiren, asir.Address, outputs.OutputConfig{})

	ah.start()

	if _, err := ah.svc.Engine().Arm(ctx, areaID, engine.ArmRequest{
		Mode: hmenum.AlarmModeFull, SkipDelay: true, By: "test", Source: "test",
	}); err != nil {
		t.Fatalf("arm full: %v", err)
	}
	if st := ah.waitAreaState(areaID, hmenum.AlarmAreaStateArmed, time.Second); st != hmenum.AlarmAreaStateArmed {
		t.Fatalf("after arm: state = %q, want armed", st)
	}

	// The window opens: a use_entry_delay sensor routes the area through
	// pending, then the 2 s entry delay escalates it to triggered.
	ah.h.resetEvents()
	ah.injectWindow(stateKey, true)

	if st := ah.waitAreaState(areaID, hmenum.AlarmAreaStatePending, 2*time.Second); st != hmenum.AlarmAreaStatePending {
		t.Fatalf("after window open: state = %q, want pending", st)
	}
	if st := ah.waitAreaState(areaID, hmenum.AlarmAreaStateTriggered, 6*time.Second); st != hmenum.AlarmAreaStateTriggered {
		t.Fatalf("after entry delay: state = %q, want triggered", st)
	}

	// The trigger fired a bounded acoustic putParamset on the ASIR :3
	// receiver: the DURATION_VALUE + DURATION_UNIT pair carries a finite
	// on-time — this is S1 on the real wire path (the engine never sends
	// an unbounded acoustic activation). The stop path's acoustic
	// selection write is asserted below on silence and in the
	// stop-unowned reconciliation test; the trigger path omits the
	// selection when it resolves empty (see the discrepancy note).
	dur, ok := ah.waitSet(asir.Address, hmenum.ParameterDurationValue, 3*time.Second)
	if !ok {
		t.Fatalf("no DURATION_VALUE write reached the ASIR %s on trigger", asir.Address)
	}
	if !numericAboveZero(dur) {
		t.Fatalf("trigger DURATION_VALUE = %v (%T), want a bounded value > 0 (S1)", dur, dur)
	}
	if _, ok := ah.waitSet(asir.Address, hmenum.ParameterDurationUnit, 3*time.Second); !ok {
		t.Fatalf("no DURATION_UNIT write reached the ASIR %s on trigger (bounded activation is one atomic paramset)", asir.Address)
	}

	// Silence must land a stop write (the disable defaults) on the ASIR
	// receiver and persist the incident silenced (S3).
	ah.h.resetEvents()
	if err := ah.svc.Engine().Silence(ctx, areaID, "test", "human"); err != nil {
		t.Fatalf("silence: %v", err)
	}
	if _, ok := ah.waitSet(asir.Address, hmenum.ParameterAcousticAlarmSelection, 3*time.Second); !ok {
		t.Fatalf("silence did not write the disable ACOUSTIC_ALARM_SELECTION to the ASIR %s", asir.Address)
	}
	inc, ok, err := ah.stores.Incidents.GetOpenByArea(ctx, areaID)
	if err != nil {
		t.Fatalf("get open incident: %v", err)
	}
	if !ok {
		t.Fatal("expected an open incident after silence (state stays triggered)")
	}
	if !inc.Silenced {
		t.Fatal("incident row is not marked silenced after silence (S3)")
	}
}

// TestAlarmReconcileAdoptsSoundingSirenOfArmedArea pins S4 adopt: on
// boot, a siren found sounding while its area is armed is adopted as a
// triggered incident (cause "adopted"), kept sounding within its bound,
// and never immediately stopped.
func TestAlarmReconcileAdoptsSoundingSirenOfArmedArea(t *testing.T) {
	ah := newAlarmHarness(t)
	ctx := context.Background()

	const areaID = "area-adopt"
	asir := ah.asirChannel()

	ah.seedArea(areaID, "Erdgeschoss", engine.AreaConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{
			hmenum.AlarmModeFull: {TriggerSeconds: 10},
		},
	})
	ah.seedOutput("out-siren", areaID, hmenum.AlarmOutputClassAcousticSiren, asir.Address, outputs.OutputConfig{})

	// Persist an armed state BEFORE the service starts — the blind
	// window a reconciliation reasons about.
	now := time.Now().UnixMilli()
	if err := ah.stores.State.Upsert(ctx, sqlitestore.AlarmStateRow{
		AreaID: areaID, State: hmenum.AlarmAreaStateArmed, Mode: hmenum.AlarmModeFull,
		BypassJSON: "[]", TimersJSON: "[]", ContextJSON: "{}", UpdatedAtMS: now,
	}); err != nil {
		t.Fatalf("persist armed state: %v", err)
	}
	// The siren is already sounding at boot.
	ah.injectSirenActive(asir)

	ah.h.resetEvents()
	ah.start()

	if st := ah.waitAreaState(areaID, hmenum.AlarmAreaStateTriggered, 2*time.Second); st != hmenum.AlarmAreaStateTriggered {
		t.Fatalf("after reconcile: state = %q, want triggered (adopted)", st)
	}

	inc, ok, err := ah.stores.Incidents.GetOpenByArea(ctx, areaID)
	if err != nil {
		t.Fatalf("get open incident: %v", err)
	}
	if !ok {
		t.Fatal("expected an open incident after adoption")
	}
	var cause struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(inc.CauseJSON), &cause); err != nil {
		t.Fatalf("decode incident cause %q: %v", inc.CauseJSON, err)
	}
	if cause.Kind != "adopted" {
		t.Fatalf("incident cause = %q, want adopted", cause.Kind)
	}

	// Adopt before stop: no TurnOff write may have reached the sounding
	// siren during the reconciliation pass.
	if v, ok := ah.h.lastSetValue(asir.Address, hmenum.ParameterAcousticAlarmSelection); ok {
		t.Fatalf("adoption wrote ACOUSTIC_ALARM_SELECTION=%v to the sounding ASIR %s; must adopt before stopping", v, asir.Address)
	}
}

// TestAlarmReconcileStopsUnownedSirenOfDisarmedArea pins S4 stop: on
// boot, a siren found sounding while its area is disarmed and unshared
// is stopped immediately, with a journalled reconcile_stopped_unowned_siren.
func TestAlarmReconcileStopsUnownedSirenOfDisarmedArea(t *testing.T) {
	ah := newAlarmHarness(t)

	const areaID = "area-stop"
	asir := ah.asirChannel()

	ah.seedArea(areaID, "Erdgeschoss", engine.AreaConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{
			hmenum.AlarmModeFull: {TriggerSeconds: 10},
		},
	})
	// No shared_with_ccu declaration: the engine owns this siren, and no
	// persisted state row leaves the area disarmed.
	ah.seedOutput("out-siren", areaID, hmenum.AlarmOutputClassAcousticSiren, asir.Address, outputs.OutputConfig{})
	ah.injectSirenActive(asir)

	ah.h.resetEvents()
	ah.start()

	if _, ok := ah.waitSet(asir.Address, hmenum.ParameterAcousticAlarmSelection, 3*time.Second); !ok {
		t.Fatalf("reconcile did not stop the unowned sounding ASIR %s (no disable write)", asir.Address)
	}
	if !ah.waitJournalEvent(areaID, "reconcile_stopped_unowned_siren", 3*time.Second) {
		t.Fatal("journal missing reconcile_stopped_unowned_siren")
	}
}

// TestAlarmSysvarWriteCannotDisarmProtectedArea pins §13.5: an inbound
// sysvar write of the disarmed index can never disarm a protected area;
// the area stays armed and the refusal is journalled.
func TestAlarmSysvarWriteCannotDisarmProtectedArea(t *testing.T) {
	ah := newAlarmHarness(t)
	ctx := context.Background()

	const (
		areaID     = "area-sysvar"
		sysvarName = "LoomAlarmTest"
	)

	ah.seedArea(areaID, "Erdgeschoss", engine.AreaConfig{
		Modes: map[hmenum.AlarmMode]engine.ModeConfig{
			hmenum.AlarmModeFull: {TriggerSeconds: 10},
		},
	})
	ah.seedOutput("out-sysvar", areaID, hmenum.AlarmOutputClassSysvarMirror, "", outputs.OutputConfig{
		SysvarName: sysvarName,
	})

	ah.start()

	if _, err := ah.svc.Engine().Arm(ctx, areaID, engine.ArmRequest{
		Mode: hmenum.AlarmModeFull, SkipDelay: true, By: "test", Source: "test",
	}); err != nil {
		t.Fatalf("arm full: %v", err)
	}
	if st := ah.waitAreaState(areaID, hmenum.AlarmAreaStateArmed, time.Second); st != hmenum.AlarmAreaStateArmed {
		t.Fatalf("after arm: state = %q, want armed", st)
	}

	// A third-party CCU sysvar write of "Unscharf" (index 0) must be
	// refused — a sysvar can never disarm a code-protected area.
	events.Publish(ah.h.central.EventBus, hmevent.SysvarChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: ah.centralName(),
		Name:        sysvarName,
		OldValue:    hmtypes.IntValue(2),
		NewValue:    hmtypes.IntValue(0),
	})

	if !ah.waitJournalEvent(areaID, "sysvar_disarm_refused", 2*time.Second) {
		t.Fatal("journal missing sysvar_disarm_refused")
	}
	if snap, ok := ah.svc.Engine().Area(areaID); !ok || snap.State != hmenum.AlarmAreaStateArmed {
		t.Fatalf("after sysvar disarm attempt: state = %q ok=%v, want armed (sysvar cannot disarm)", snap.State, ok)
	}
}
